package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const cicyAdaptResponsesHeader = "X-Cicy-Adapt-Responses-To-ChatCompletions"
const cicyAdaptMessagesHeader = "X-Cicy-Adapt-Messages-To-ChatCompletions"

// shouldAdaptForCodexResponses returns true when the client is calling the
// OpenAI Responses API (codex) and the upstream is NOT OpenAI proper. Only
// api.openai.com natively serves /v1/responses; everyone else (DeepSeek,
// new-api, SiliconFlow, Together, etc.) only speaks Chat Completions, so we
// translate request + streaming response.
func shouldAdaptForCodexResponses(upstreamHost, suffix string) bool {
	if !strings.Contains(strings.ToLower(suffix), "/responses") {
		return false
	}
	h := strings.ToLower(upstreamHost)
	return !strings.Contains(h, "api.openai.com")
}

// rewriteSuffixForChatCompletions converts a Responses-API suffix into the
// equivalent Chat Completions endpoint suffix.
func rewriteSuffixForChatCompletions(suffix string) string {
	return strings.Replace(suffix, "/responses", "/chat/completions", 1)
}

// transformResponsesRequestToChatCompletions converts an OpenAI Responses API
// request body into a Chat Completions request body. Returns the new body and
// the resolved model name (for use when emitting Responses events back).
func transformResponsesRequestToChatCompletions(body []byte) ([]byte, string, error) {
	var src map[string]interface{}
	if err := json.Unmarshal(body, &src); err != nil {
		return body, "", err
	}

	dst := map[string]interface{}{}
	model, _ := src["model"].(string)
	if model != "" {
		dst["model"] = model
	}
	if s, ok := src["stream"].(bool); ok {
		dst["stream"] = s
	}
	for _, k := range []string{"temperature", "top_p", "max_tokens", "stop"} {
		if v, ok := src[k]; ok {
			dst[k] = v
		}
	}
	// Disable DeepSeek thinking mode — otherwise the API requires every
	// assistant turn echoed back to include `reasoning_content`, which codex
	// doesn't track.
	dst["enable_thinking"] = false

	// input + instructions → messages
	messages := make([]map[string]interface{}, 0)
	if instr, ok := src["instructions"].(string); ok && strings.TrimSpace(instr) != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": instr,
		})
	}

	if inputs, ok := src["input"].([]interface{}); ok {
		// DeepSeek's thinking-mode validator on follow-up turns demands the
		// assistant's prior `reasoning_content` echoed back as a non-empty
		// string. It does NOT validate content against what it originally
		// generated — any non-empty value is accepted. We try hard to ship
		// the real reasoning (captured via response-side `type:"reasoning"`
		// roundtrip below); when the upstream emitted no reasoning for a
		// given assistant turn — or that turn predates the response-side
		// roundtrip being deployed — we fall back to a single-char placeholder
		// to satisfy the validator. Empty string is rejected with
		//   `content[].thinking in the thinking mode must be passed back to the API`.
		pendingReasoning := ""
		const reasoningPlaceholder = "."
		consumePendingReasoning := func() string {
			r := pendingReasoning
			pendingReasoning = ""
			if r == "" {
				return reasoningPlaceholder
			}
			return r
		}
		for _, raw := range inputs {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			if typ == "" {
				typ = "message"
			}
			switch typ {
			case "reasoning":
				pendingReasoning = extractReasoningSummary(m)
			case "message":
				role, _ := m["role"].(string)
				if role == "" {
					continue
				}
				// DeepSeek only accepts system/user/assistant/tool — map
				// Responses-specific roles to their nearest equivalents.
				switch role {
				case "developer":
					role = "system"
				}
				text := extractTextFromResponsesContent(m["content"])
				if text == "" {
					continue
				}
				msg := map[string]interface{}{
					"role":    role,
					"content": text,
				}
				if role == "assistant" {
					msg["reasoning_content"] = consumePendingReasoning()
				}
				messages = append(messages, msg)
			case "function_call":
				callID, _ := m["call_id"].(string)
				name, _ := m["name"].(string)
				args, _ := m["arguments"].(string)
				messages = append(messages, map[string]interface{}{
					"role":              "assistant",
					"content":           nil,
					"reasoning_content": consumePendingReasoning(),
					"tool_calls": []map[string]interface{}{
						{
							"id":   callID,
							"type": "function",
							"function": map[string]interface{}{
								"name":      name,
								"arguments": args,
							},
						},
					},
				})
			case "function_call_output":
				callID, _ := m["call_id"].(string)
				output, _ := m["output"].(string)
				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": callID,
					"content":      output,
				})
			}
		}
	}
	dst["messages"] = messages

	// tools: only function tools are portable; DeepSeek rejects built-in
	// Responses-API tool types (web_search, file_search, computer_use, etc.).
	if rawTools, ok := src["tools"].([]interface{}); ok && len(rawTools) > 0 {
		tools := make([]map[string]interface{}, 0, len(rawTools))
		for _, t := range rawTools {
			tm, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _ := tm["type"].(string)
			if typ != "function" {
				// Skip non-function tool types — DeepSeek can't execute them.
				continue
			}
			fn := map[string]interface{}{}
			if name, ok := tm["name"]; ok {
				fn["name"] = name
			}
			if desc, ok := tm["description"]; ok {
				fn["description"] = desc
			}
			fn["parameters"] = sanitizeFunctionParameters(tm["parameters"])
			tools = append(tools, map[string]interface{}{
				"type":     "function",
				"function": fn,
			})
		}
		if len(tools) > 0 {
			dst["tools"] = tools
		}
	}
	if tc, ok := src["tool_choice"]; ok {
		dst["tool_choice"] = tc
	}

	out, err := json.Marshal(dst)
	return out, model, err
}

// sanitizeFunctionParameters guarantees the JSON Schema is well-formed for
// strict-validating OpenAI-compatible upstreams. Specifically, DeepSeek (and
// any new-api / cicyAi style gateway in front of it) returns
// `Invalid schema for function 'X': null is not of type "array"` when the
// schema omits `required`. OpenAI proper is lenient; everyone downstream is
// not. We also normalize `type` to `"object"` and ensure `properties` exists.
func sanitizeFunctionParameters(p interface{}) map[string]interface{} {
	out, _ := p.(map[string]interface{})
	if out == nil {
		out = map[string]interface{}{}
	}
	if v, ok := out["type"]; !ok || v == nil {
		out["type"] = "object"
	}
	if v, ok := out["properties"]; !ok || v == nil {
		out["properties"] = map[string]interface{}{}
	}
	if v, ok := out["required"]; !ok || v == nil {
		out["required"] = []interface{}{}
	}
	return out
}

// extractReasoningSummary pulls the concatenated reasoning text out of a
// codex Responses `type:"reasoning"` input item. The item carries `summary`
// (array of `{type:"summary_text", text:"..."}`) and optionally `content`
// (rare; same shape). DeepSeek wants the raw text echoed as
// assistant.reasoning_content on the next turn.
func extractReasoningSummary(item map[string]interface{}) string {
	if item == nil {
		return ""
	}
	collect := func(field string) string {
		parts, ok := item[field].([]interface{})
		if !ok {
			return ""
		}
		var b strings.Builder
		for _, p := range parts {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if t, ok := pm["text"].(string); ok {
				b.WriteString(t)
			}
		}
		return b.String()
	}
	if s := collect("summary"); s != "" {
		return s
	}
	return collect("content")
}

func extractTextFromResponsesContent(content interface{}) string {
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := content.([]interface{})
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if t, ok := pm["text"].(string); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

// chatCompletionsToResponsesReader streams a Chat Completions SSE response and
// emits an equivalent OpenAI Responses event stream so codex can consume it.
type chatCompletionsToResponsesReader struct {
	src           *bufio.Reader
	srcClose      io.Closer
	out           bytes.Buffer
	upstreamDone  bool
	headerEmitted bool
	completedSent bool

	responseID string
	model      string

	// Reasoning (thinking) item state — DeepSeek emits delta.reasoning_content
	// chunks BEFORE the actual text. We surface those as a Responses
	// `type:"reasoning"` output item so codex stores the summary; on the next
	// turn codex echoes the same reasoning back, satisfying DeepSeek's
	// thinking-mode validator (which rejects empty / mismatched values).
	reasoningItemID   string
	reasoningStarted  bool
	reasoningDoneSent bool
	accumReasoning    strings.Builder
	reasoningIndex    int

	// Text (message) item state — created lazily on first content delta.
	textItemID   string
	textStarted  bool
	textDoneSent bool
	accumText    strings.Builder
	textIndex    int

	// Tool call state per upstream "index" (slot).
	toolCalls       map[int]*toolCallState
	nextOutputIndex int
}

type toolCallState struct {
	itemID      string
	callID      string
	name        string
	outputIndex int
	accumArgs   strings.Builder
	addedSent   bool
	doneSent    bool
}

func newChatCompletionsToResponsesReader(body io.ReadCloser, model string) io.ReadCloser {
	return &chatCompletionsToResponsesReader{
		src:       bufio.NewReader(body),
		srcClose:  body,
		model:     model,
		toolCalls: map[int]*toolCallState{},
	}
}

func (r *chatCompletionsToResponsesReader) Read(p []byte) (int, error) {
	for r.out.Len() == 0 && !r.completedSent {
		if r.upstreamDone {
			r.emitCompletedAndDone()
			break
		}
		if err := r.processOneLine(); err != nil {
			if err == io.EOF {
				r.upstreamDone = true
				continue
			}
			return 0, err
		}
	}
	if r.out.Len() == 0 {
		return 0, io.EOF
	}
	return r.out.Read(p)
}

func (r *chatCompletionsToResponsesReader) Close() error {
	if r.srcClose != nil {
		return r.srcClose.Close()
	}
	return nil
}

func (r *chatCompletionsToResponsesReader) processOneLine() error {
	line, err := r.src.ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	if !strings.HasPrefix(trimmed, "data:") {
		return nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "[DONE]" {
		r.upstreamDone = true
		return nil
	}

	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return nil
	}

	if !r.headerEmitted {
		r.headerEmitted = true
		if id, ok := chunk["id"].(string); ok && id != "" {
			r.responseID = "resp_" + id
		} else {
			r.responseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
		}
		r.emitCreated()
	}

	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	delta, _ := choice["delta"].(map[string]interface{})
	if delta != nil {
		if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
			r.ensureReasoningStarted()
			r.emitReasoningDelta(rc)
			r.accumReasoning.WriteString(rc)
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			// Real content arrived — close the reasoning item first so codex
			// sees them as separate output_items in the right order.
			r.finishReasoningOpenItem()
			r.ensureTextStarted()
			r.emitTextDelta(content)
			r.accumText.WriteString(content)
		}
		if rawTC, ok := delta["tool_calls"].([]interface{}); ok {
			// Tool calls also need reasoning to be closed out first.
			r.finishReasoningOpenItem()
			for _, raw := range rawTC {
				tcm, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				r.handleToolCallDelta(tcm)
			}
		}
	}
	if finish, _ := choice["finish_reason"].(string); finish != "" {
		r.finishOpenItems(finish)
	}
	return nil
}

func (r *chatCompletionsToResponsesReader) ensureReasoningStarted() {
	if r.reasoningStarted {
		return
	}
	r.reasoningStarted = true
	r.reasoningItemID = fmt.Sprintf("rs_%d", time.Now().UnixNano())
	r.reasoningIndex = r.nextOutputIndex
	r.nextOutputIndex++
	r.emitEvent("response.output_item.added", map[string]interface{}{
		"type":         "response.output_item.added",
		"output_index": r.reasoningIndex,
		"item": map[string]interface{}{
			"id":      r.reasoningItemID,
			"type":    "reasoning",
			"summary": []interface{}{},
			"status":  "in_progress",
		},
	})
	r.emitEvent("response.reasoning_summary_part.added", map[string]interface{}{
		"type":          "response.reasoning_summary_part.added",
		"output_index":  r.reasoningIndex,
		"item_id":       r.reasoningItemID,
		"summary_index": 0,
		"part":          map[string]interface{}{"type": "summary_text", "text": ""},
	})
}

func (r *chatCompletionsToResponsesReader) emitReasoningDelta(delta string) {
	r.emitEvent("response.reasoning_summary_text.delta", map[string]interface{}{
		"type":          "response.reasoning_summary_text.delta",
		"output_index":  r.reasoningIndex,
		"item_id":       r.reasoningItemID,
		"summary_index": 0,
		"delta":         delta,
	})
}

func (r *chatCompletionsToResponsesReader) finishReasoningOpenItem() {
	if !r.reasoningStarted || r.reasoningDoneSent {
		return
	}
	r.reasoningDoneSent = true
	text := r.accumReasoning.String()
	r.emitEvent("response.reasoning_summary_text.done", map[string]interface{}{
		"type":          "response.reasoning_summary_text.done",
		"output_index":  r.reasoningIndex,
		"item_id":       r.reasoningItemID,
		"summary_index": 0,
		"text":          text,
	})
	r.emitEvent("response.reasoning_summary_part.done", map[string]interface{}{
		"type":          "response.reasoning_summary_part.done",
		"output_index":  r.reasoningIndex,
		"item_id":       r.reasoningItemID,
		"summary_index": 0,
		"part":          map[string]interface{}{"type": "summary_text", "text": text},
	})
	r.emitEvent("response.output_item.done", map[string]interface{}{
		"type":         "response.output_item.done",
		"output_index": r.reasoningIndex,
		"item": map[string]interface{}{
			"id":      r.reasoningItemID,
			"type":    "reasoning",
			"summary": []map[string]interface{}{{"type": "summary_text", "text": text}},
			"status":  "completed",
		},
	})
}

func (r *chatCompletionsToResponsesReader) emitCreated() {
	r.emitEvent("response.created", map[string]interface{}{
		"type": "response.created",
		"response": map[string]interface{}{
			"id":     r.responseID,
			"object": "response",
			"status": "in_progress",
			"model":  r.model,
		},
	})
}

func (r *chatCompletionsToResponsesReader) ensureTextStarted() {
	if r.textStarted {
		return
	}
	r.textStarted = true
	r.textItemID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
	r.textIndex = r.nextOutputIndex
	r.nextOutputIndex++
	r.emitEvent("response.output_item.added", map[string]interface{}{
		"type":         "response.output_item.added",
		"output_index": r.textIndex,
		"item": map[string]interface{}{
			"id":      r.textItemID,
			"type":    "message",
			"role":    "assistant",
			"content": []interface{}{},
			"status":  "in_progress",
		},
	})
	r.emitEvent("response.content_part.added", map[string]interface{}{
		"type":          "response.content_part.added",
		"output_index":  r.textIndex,
		"item_id":       r.textItemID,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
			"text": "",
		},
	})
}

func (r *chatCompletionsToResponsesReader) handleToolCallDelta(tcm map[string]interface{}) {
	idx := 0
	if v, ok := tcm["index"].(float64); ok {
		idx = int(v)
	}
	state := r.toolCalls[idx]
	if state == nil {
		state = &toolCallState{}
		r.toolCalls[idx] = state
	}
	if id, ok := tcm["id"].(string); ok && id != "" {
		state.callID = id
	}
	if fn, ok := tcm["function"].(map[string]interface{}); ok {
		if name, ok := fn["name"].(string); ok && name != "" {
			state.name = name
		}
		if args, ok := fn["arguments"].(string); ok && args != "" {
			// Emit output_item.added the first time we know enough to identify the call.
			if !state.addedSent && state.name != "" {
				state.itemID = fmt.Sprintf("fc_%d", time.Now().UnixNano())
				state.outputIndex = r.nextOutputIndex
				r.nextOutputIndex++
				state.addedSent = true
				r.emitEvent("response.output_item.added", map[string]interface{}{
					"type":         "response.output_item.added",
					"output_index": state.outputIndex,
					"item": map[string]interface{}{
						"id":        state.itemID,
						"type":      "function_call",
						"call_id":   state.callID,
						"name":      state.name,
						"arguments": "",
						"status":    "in_progress",
					},
				})
			}
			if state.addedSent {
				state.accumArgs.WriteString(args)
				r.emitEvent("response.function_call_arguments.delta", map[string]interface{}{
					"type":         "response.function_call_arguments.delta",
					"output_index": state.outputIndex,
					"item_id":      state.itemID,
					"delta":        args,
				})
			} else {
				// Buffer args until name arrives.
				state.accumArgs.WriteString(args)
			}
		}
	}
	// If only metadata arrived before any args, still create the item once name is set.
	if !state.addedSent && state.name != "" {
		state.itemID = fmt.Sprintf("fc_%d", time.Now().UnixNano())
		state.outputIndex = r.nextOutputIndex
		r.nextOutputIndex++
		state.addedSent = true
		r.emitEvent("response.output_item.added", map[string]interface{}{
			"type":         "response.output_item.added",
			"output_index": state.outputIndex,
			"item": map[string]interface{}{
				"id":        state.itemID,
				"type":      "function_call",
				"call_id":   state.callID,
				"name":      state.name,
				"arguments": "",
				"status":    "in_progress",
			},
		})
		// Flush buffered args (if any).
		if buf := state.accumArgs.String(); buf != "" {
			r.emitEvent("response.function_call_arguments.delta", map[string]interface{}{
				"type":         "response.function_call_arguments.delta",
				"output_index": state.outputIndex,
				"item_id":      state.itemID,
				"delta":        buf,
			})
		}
	}
}

func (r *chatCompletionsToResponsesReader) emitTextDelta(delta string) {
	r.emitEvent("response.output_text.delta", map[string]interface{}{
		"type":          "response.output_text.delta",
		"output_index":  r.textIndex,
		"item_id":       r.textItemID,
		"content_index": 0,
		"delta":         delta,
	})
}

func (r *chatCompletionsToResponsesReader) finishOpenItems(finish string) {
	r.finishReasoningOpenItem()
	if r.textStarted && !r.textDoneSent {
		text := r.accumText.String()
		r.textDoneSent = true
		r.emitEvent("response.output_text.done", map[string]interface{}{
			"type":          "response.output_text.done",
			"output_index":  r.textIndex,
			"item_id":       r.textItemID,
			"content_index": 0,
			"text":          text,
		})
		r.emitEvent("response.content_part.done", map[string]interface{}{
			"type":          "response.content_part.done",
			"output_index":  r.textIndex,
			"item_id":       r.textItemID,
			"content_index": 0,
			"part": map[string]interface{}{
				"type": "output_text",
				"text": text,
			},
		})
		r.emitEvent("response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": r.textIndex,
			"item": map[string]interface{}{
				"id":     r.textItemID,
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]interface{}{
					{"type": "output_text", "text": text},
				},
			},
		})
	}
	for _, st := range r.toolCalls {
		if !st.addedSent || st.doneSent {
			continue
		}
		args := st.accumArgs.String()
		st.doneSent = true
		r.emitEvent("response.function_call_arguments.done", map[string]interface{}{
			"type":         "response.function_call_arguments.done",
			"output_index": st.outputIndex,
			"item_id":      st.itemID,
			"arguments":    args,
		})
		r.emitEvent("response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": st.outputIndex,
			"item": map[string]interface{}{
				"id":        st.itemID,
				"type":      "function_call",
				"call_id":   st.callID,
				"name":      st.name,
				"arguments": args,
				"status":    "completed",
			},
		})
	}
	_ = finish
}

func (r *chatCompletionsToResponsesReader) emitCompletedAndDone() {
	if r.headerEmitted {
		r.finishOpenItems("")
	}
	if !r.completedSent {
		r.completedSent = true
		if r.responseID == "" {
			r.responseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
		}
		output := make([]map[string]interface{}, 0)
		if r.reasoningStarted {
			output = append(output, map[string]interface{}{
				"id":     r.reasoningItemID,
				"type":   "reasoning",
				"status": "completed",
				"summary": []map[string]interface{}{
					{"type": "summary_text", "text": r.accumReasoning.String()},
				},
			})
		}
		if r.textStarted {
			output = append(output, map[string]interface{}{
				"id":     r.textItemID,
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]interface{}{
					{"type": "output_text", "text": r.accumText.String()},
				},
			})
		}
		for _, st := range r.toolCalls {
			if !st.addedSent {
				continue
			}
			output = append(output, map[string]interface{}{
				"id":        st.itemID,
				"type":      "function_call",
				"call_id":   st.callID,
				"name":      st.name,
				"arguments": st.accumArgs.String(),
				"status":    "completed",
			})
		}
		r.emitEvent("response.completed", map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     r.responseID,
				"object": "response",
				"status": "completed",
				"model":  r.model,
				"output": output,
			},
		})
		r.out.WriteString("data: [DONE]\n\n")
	}
}

func (r *chatCompletionsToResponsesReader) emitEvent(eventType string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	r.out.WriteString("event: ")
	r.out.WriteString(eventType)
	r.out.WriteString("\n")
	r.out.WriteString("data: ")
	r.out.Write(payload)
	r.out.WriteString("\n\n")
}

// Used to silence the linter when imports get rearranged.
var _ = url.Parse

// =============================================================================
// Anthropic Messages ↔ ChatCompletions adaptation (for claude → DeepSeek)
// =============================================================================

// shouldAdaptDeepSeekForAnthropic returns true when the upstream is DeepSeek and
// the client is calling the Anthropic Messages API (claude). DeepSeek only
// speaks Chat Completions, so we translate both request and streaming response.
func shouldAdaptDeepSeekForAnthropic(upstreamHost, suffix string) bool {
	return strings.Contains(strings.ToLower(upstreamHost), "deepseek") &&
		strings.Contains(strings.ToLower(suffix), "/messages")
}

func rewriteSuffixForDeepSeekChatCompletionsFromMessages(suffix string) string {
	return strings.Replace(suffix, "/messages", "/chat/completions", 1)
}

// transformMessagesRequestToChatCompletions converts an Anthropic Messages API
// request body into a Chat Completions request body. Returns new body + model.
func transformMessagesRequestToChatCompletions(body []byte) ([]byte, string, error) {
	var src map[string]interface{}
	if err := json.Unmarshal(body, &src); err != nil {
		return body, "", err
	}

	dst := map[string]interface{}{}
	model, _ := src["model"].(string)
	if model != "" {
		dst["model"] = model
	}
	if s, ok := src["stream"].(bool); ok {
		dst["stream"] = s
	}
	for _, k := range []string{"temperature", "top_p", "max_tokens", "stop_sequences"} {
		if v, ok := src[k]; ok {
			// Anthropic uses stop_sequences; OpenAI uses stop.
			if k == "stop_sequences" {
				dst["stop"] = v
			} else {
				dst[k] = v
			}
		}
	}
	dst["enable_thinking"] = false

	messages := make([]map[string]interface{}, 0)

	// Anthropic `system` is either a string or an array of blocks.
	if sys, ok := src["system"]; ok {
		sysText := flattenAnthropicSystem(sys)
		if strings.TrimSpace(sysText) != "" {
			messages = append(messages, map[string]interface{}{
				"role":    "system",
				"content": sysText,
			})
		}
	}

	if rawMsgs, ok := src["messages"].([]interface{}); ok {
		for _, raw := range rawMsgs {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := m["role"].(string)
			content := m["content"]
			out := convertAnthropicMessageToChatCompletions(role, content)
			messages = append(messages, out...)
		}
	}
	dst["messages"] = messages

	// tools: {name, description, input_schema} → {type:"function", function:{name, description, parameters}}
	if rawTools, ok := src["tools"].([]interface{}); ok && len(rawTools) > 0 {
		tools := make([]map[string]interface{}, 0, len(rawTools))
		for _, t := range rawTools {
			tm, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			// Skip built-in anthropic tool types (computer_20241022, etc.)
			if typ, _ := tm["type"].(string); typ != "" && typ != "custom" && typ != "function" {
				continue
			}
			fn := map[string]interface{}{}
			if name, ok := tm["name"]; ok {
				fn["name"] = name
			}
			if desc, ok := tm["description"]; ok {
				fn["description"] = desc
			}
			fn["parameters"] = sanitizeFunctionParameters(tm["input_schema"])
			tools = append(tools, map[string]interface{}{
				"type":     "function",
				"function": fn,
			})
		}
		if len(tools) > 0 {
			dst["tools"] = tools
		}
	}
	if tc, ok := src["tool_choice"]; ok {
		dst["tool_choice"] = tc
	}

	out, err := json.Marshal(dst)
	return out, model, err
}

func flattenAnthropicSystem(sys interface{}) string {
	if s, ok := sys.(string); ok {
		return s
	}
	parts, ok := sys.([]interface{})
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if t, ok := pm["text"].(string); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

// convertAnthropicMessageToChatCompletions converts a single Anthropic message
// item into one or more ChatCompletions messages. Returns a slice because
// assistant turns with mixed text+tool_use translate to a single message with
// tool_calls, but tool_result blocks become separate role=tool entries.
func convertAnthropicMessageToChatCompletions(role string, content interface{}) []map[string]interface{} {
	if s, ok := content.(string); ok {
		return []map[string]interface{}{
			{"role": role, "content": s},
		}
	}
	parts, ok := content.([]interface{})
	if !ok {
		return nil
	}

	var text strings.Builder
	toolCalls := make([]map[string]interface{}, 0)
	toolResultMsgs := make([]map[string]interface{}, 0)
	for _, p := range parts {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := pm["type"].(string)
		switch typ {
		case "text":
			if t, ok := pm["text"].(string); ok {
				text.WriteString(t)
			}
		case "tool_use":
			id, _ := pm["id"].(string)
			name, _ := pm["name"].(string)
			input := pm["input"]
			argsJSON, _ := json.Marshal(input)
			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   id,
				"type": "function",
				"function": map[string]interface{}{
					"name":      name,
					"arguments": string(argsJSON),
				},
			})
		case "tool_result":
			id, _ := pm["tool_use_id"].(string)
			body := flattenToolResultContent(pm["content"])
			toolResultMsgs = append(toolResultMsgs, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": id,
				"content":      body,
			})
		}
	}

	out := make([]map[string]interface{}, 0, 1+len(toolResultMsgs))
	// For assistant role: emit one message with text content + tool_calls (both may be empty).
	// For user role: emit text message if present; tool_result becomes role=tool messages.
	if role == "assistant" {
		msg := map[string]interface{}{"role": "assistant"}
		if text.Len() > 0 {
			msg["content"] = text.String()
		} else {
			msg["content"] = nil
		}
		if len(toolCalls) > 0 {
			msg["tool_calls"] = toolCalls
		}
		msg["reasoning_content"] = ""
		out = append(out, msg)
	} else {
		if text.Len() > 0 {
			out = append(out, map[string]interface{}{
				"role":    role,
				"content": text.String(),
			})
		}
	}
	out = append(out, toolResultMsgs...)
	return out
}

func flattenToolResultContent(content interface{}) string {
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := content.([]interface{})
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		if t, ok := pm["text"].(string); ok {
			b.WriteString(t)
		}
	}
	return b.String()
}

// chatCompletionsToMessagesReader wraps a Chat Completions SSE response and
// emits Anthropic Messages SSE events so claude can consume it.
type chatCompletionsToMessagesReader struct {
	src           *bufio.Reader
	srcClose      io.Closer
	out           bytes.Buffer
	upstreamDone  bool
	headerEmitted bool
	completedSent bool

	messageID string
	model     string

	// Text block state
	textBlockStarted bool
	textBlockClosed  bool
	textBlockIndex   int
	accumText        strings.Builder

	// Tool-call blocks (per upstream "index" slot)
	toolBlocks      map[int]*anthropicToolBlock
	nextBlockIndex  int
	finishReason    string
}

type anthropicToolBlock struct {
	blockIndex int
	id         string
	name       string
	accumArgs  strings.Builder
	started    bool
	closed     bool
}

func newChatCompletionsToMessagesReader(body io.ReadCloser, model string) io.ReadCloser {
	return &chatCompletionsToMessagesReader{
		src:        bufio.NewReader(body),
		srcClose:   body,
		model:      model,
		toolBlocks: map[int]*anthropicToolBlock{},
	}
}

func (r *chatCompletionsToMessagesReader) Read(p []byte) (int, error) {
	for r.out.Len() == 0 && !r.completedSent {
		if r.upstreamDone {
			r.emitFinalizers()
			break
		}
		if err := r.processOneLine(); err != nil {
			if err == io.EOF {
				r.upstreamDone = true
				continue
			}
			return 0, err
		}
	}
	if r.out.Len() == 0 {
		return 0, io.EOF
	}
	return r.out.Read(p)
}

func (r *chatCompletionsToMessagesReader) Close() error {
	if r.srcClose != nil {
		return r.srcClose.Close()
	}
	return nil
}

func (r *chatCompletionsToMessagesReader) processOneLine() error {
	line, err := r.src.ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	if !strings.HasPrefix(trimmed, "data:") {
		return nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "[DONE]" {
		r.upstreamDone = true
		return nil
	}

	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return nil
	}

	if !r.headerEmitted {
		r.headerEmitted = true
		if id, ok := chunk["id"].(string); ok && id != "" {
			r.messageID = "msg_" + id
		} else {
			r.messageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
		}
		r.emitMessageStart()
	}

	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	delta, _ := choice["delta"].(map[string]interface{})
	if delta != nil {
		if content, ok := delta["content"].(string); ok && content != "" {
			r.ensureTextBlock()
			r.emitTextDelta(content)
			r.accumText.WriteString(content)
		}
		if rawTC, ok := delta["tool_calls"].([]interface{}); ok {
			for _, raw := range rawTC {
				tcm, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				r.handleToolCallDelta(tcm)
			}
		}
	}
	if finish, _ := choice["finish_reason"].(string); finish != "" {
		r.finishReason = finish
	}
	return nil
}

func (r *chatCompletionsToMessagesReader) emitMessageStart() {
	r.emitEvent("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            r.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         r.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})
	r.emitEvent("ping", map[string]interface{}{"type": "ping"})
}

func (r *chatCompletionsToMessagesReader) ensureTextBlock() {
	if r.textBlockStarted {
		return
	}
	r.textBlockStarted = true
	r.textBlockIndex = r.nextBlockIndex
	r.nextBlockIndex++
	r.emitEvent("content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": r.textBlockIndex,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	})
}

func (r *chatCompletionsToMessagesReader) emitTextDelta(delta string) {
	r.emitEvent("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": r.textBlockIndex,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": delta,
		},
	})
}

func (r *chatCompletionsToMessagesReader) closeTextBlock() {
	if !r.textBlockStarted || r.textBlockClosed {
		return
	}
	r.textBlockClosed = true
	r.emitEvent("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": r.textBlockIndex,
	})
}

func (r *chatCompletionsToMessagesReader) handleToolCallDelta(tcm map[string]interface{}) {
	idx := 0
	if v, ok := tcm["index"].(float64); ok {
		idx = int(v)
	}
	state := r.toolBlocks[idx]
	if state == nil {
		state = &anthropicToolBlock{}
		r.toolBlocks[idx] = state
	}
	if id, ok := tcm["id"].(string); ok && id != "" {
		state.id = id
	}
	if fn, ok := tcm["function"].(map[string]interface{}); ok {
		if name, ok := fn["name"].(string); ok && name != "" {
			state.name = name
		}
		if args, ok := fn["arguments"].(string); ok && args != "" {
			state.accumArgs.WriteString(args)
			if !state.started && state.name != "" {
				// Close text block before starting tool block (anthropic style).
				r.closeTextBlock()
				state.started = true
				state.blockIndex = r.nextBlockIndex
				r.nextBlockIndex++
				r.emitEvent("content_block_start", map[string]interface{}{
					"type":  "content_block_start",
					"index": state.blockIndex,
					"content_block": map[string]interface{}{
						"type":  "tool_use",
						"id":    state.id,
						"name":  state.name,
						"input": map[string]interface{}{},
					},
				})
			}
			if state.started {
				r.emitEvent("content_block_delta", map[string]interface{}{
					"type":  "content_block_delta",
					"index": state.blockIndex,
					"delta": map[string]interface{}{
						"type":         "input_json_delta",
						"partial_json": args,
					},
				})
			}
		}
	}
	// Edge: name arrived without args yet.
	if !state.started && state.name != "" {
		r.closeTextBlock()
		state.started = true
		state.blockIndex = r.nextBlockIndex
		r.nextBlockIndex++
		r.emitEvent("content_block_start", map[string]interface{}{
			"type":  "content_block_start",
			"index": state.blockIndex,
			"content_block": map[string]interface{}{
				"type":  "tool_use",
				"id":    state.id,
				"name":  state.name,
				"input": map[string]interface{}{},
			},
		})
		if buf := state.accumArgs.String(); buf != "" {
			r.emitEvent("content_block_delta", map[string]interface{}{
				"type":  "content_block_delta",
				"index": state.blockIndex,
				"delta": map[string]interface{}{
					"type":         "input_json_delta",
					"partial_json": buf,
				},
			})
		}
	}
}

func (r *chatCompletionsToMessagesReader) emitFinalizers() {
	if !r.headerEmitted {
		// Nothing to emit — upstream returned no chunks.
		r.completedSent = true
		return
	}
	r.closeTextBlock()
	for _, tb := range r.toolBlocks {
		if tb.started && !tb.closed {
			tb.closed = true
			r.emitEvent("content_block_stop", map[string]interface{}{
				"type":  "content_block_stop",
				"index": tb.blockIndex,
			})
		}
	}
	stopReason := "end_turn"
	switch r.finishReason {
	case "stop":
		stopReason = "end_turn"
	case "length":
		stopReason = "max_tokens"
	case "tool_calls":
		stopReason = "tool_use"
	case "content_filter":
		stopReason = "stop_sequence"
	}
	r.emitEvent("message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": 0,
		},
	})
	r.emitEvent("message_stop", map[string]interface{}{
		"type": "message_stop",
	})
	r.completedSent = true
}

func (r *chatCompletionsToMessagesReader) emitEvent(eventType string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	r.out.WriteString("event: ")
	r.out.WriteString(eventType)
	r.out.WriteString("\n")
	r.out.WriteString("data: ")
	r.out.Write(payload)
	r.out.WriteString("\n\n")
}
