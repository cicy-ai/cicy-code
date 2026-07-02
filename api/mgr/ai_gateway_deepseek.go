package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

// deepseekReasoningModels remembers models the upstream itself has told us require
// DeepSeek's reasoning_content passback — LEARNED at runtime from the provider's
// own 400 ("reasoning_content … must be passed back"), never from the model name.
// This is how a freely-switched cicy model that fronts DeepSeek behind an opaque
// alias (opencodeZen's "big-pickle", any future alias) gets the passback armed
// without hardcoding a single name. Keyed by lowercased model id.
var deepseekReasoningModels sync.Map

func markDeepseekReasoningModel(model string) {
	if m := strings.TrimSpace(strings.ToLower(model)); m != "" {
		deepseekReasoningModels.Store(m, true)
	}
}

func learnedDeepseekReasoningModel(model string) bool {
	m := strings.TrimSpace(strings.ToLower(model))
	if m == "" {
		return false
	}
	_, ok := deepseekReasoningModels.Load(m)
	return ok
}

const cicyAdaptResponsesHeader = "X-Cicy-Adapt-Responses-To-ChatCompletions"
const cicyAdaptMessagesHeader = "X-Cicy-Adapt-Messages-To-ChatCompletions"

// shouldAdaptForCodexResponses returns true when the client is calling the
// OpenAI Responses API (codex) and the upstream does NOT serve /responses, so we
// must send the REQUEST as Chat Completions instead. Only api.openai.com serves
// /responses natively. Everyone else — DeepSeek, new-api, AND the cicy cloud
// gateway (gateway.cicy-ai.com, which 404s /responses) — only accepts
// /chat/completions, so we translate the request body + path.
//
// NOTE: this governs the REQUEST only. The RESPONSE is handled separately by a
// format-detecting reader (newCodexResponsesReader): some of these upstreams (the
// cicy cloud gateway) answer a /chat/completions request in *Responses* format
// already, so that reader passes such a stream through untouched and only
// converts genuine chat.completion.chunk streams. That split is why a plain
// chat->responses wrap produced an empty reply (output:[]) before.
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
func transformResponsesRequestToChatCompletions(body []byte, upstreamHost string) ([]byte, string, error) {
	var src map[string]interface{}
	if err := json.Unmarshal(body, &src); err != nil {
		return body, "", err
	}

	dst := map[string]interface{}{}
	model, _ := src["model"].(string)
	if model != "" {
		dst["model"] = model
	}
	deepseek := isDeepSeekFlavored(upstreamHost, model)
	if s, ok := src["stream"].(bool); ok {
		dst["stream"] = s
		if s {
			// Ask the upstream to include usage in the terminal stream chunk
			// (OpenAI `stream_options.include_usage`). OpenAI-compatible upstreams
			// (DeepSeek, one-api, …) compute usage server-side for billing but do
			// NOT emit it into the stream unless this is set — so without it the
			// gateway/audit sees zero tokens and no cache. Preserve any caller-set
			// value, otherwise default it on.
			if _, ok := src["stream_options"]; ok {
				dst["stream_options"] = src["stream_options"]
			} else {
				dst["stream_options"] = map[string]interface{}{"include_usage": true}
			}
		}
	}
	for _, k := range []string{"temperature", "top_p", "max_tokens", "stop"} {
		if v, ok := src[k]; ok {
			dst[k] = v
		}
	}
	// DeepSeek-ONLY: carry the resolved V4 thinking switch (set on the Responses
	// body by agentInspectorApplyThinking per config), symmetric with the cicy
	// messages bridge — so codex thinking actually follows the toggle instead of
	// being hard-disabled. Multi-turn is kept valid by the reasoning roundtrip
	// below: the response side emits `type:"reasoning"` items codex stores and
	// echoes back as assistant.reasoning_content, with a single-char placeholder
	// fallback when codex drops them (openai/codex#24500). Standard OpenAI
	// upstreams get NO `thinking` field (DeepSeek private extension).
	if deepseek {
		dst["thinking"] = deepSeekThinkingFromSource(src["thinking"])
	}

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
		// First pass: collect every call_id that already has a function_call_output
		// later in the conversation. DeepSeek (and any strict OpenAI-compatible
		// upstream) rejects a request whose history contains an assistant message
		// with tool_calls but no matching tool message — codex sometimes records
		// a function_call without a function_call_output (e.g. the model called a
		// tool that doesn't exist in codex's local registry). For each orphan we
		// synthesize a tool-result with an error stub so the conversation
		// validates without losing the assistant's intent.
		fulfilledCallIDs := map[string]bool{}
		for _, raw := range inputs {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t == "function_call_output" {
				if id, _ := m["call_id"].(string); id != "" {
					fulfilledCallIDs[id] = true
				}
			}
		}
		// Use indexed iteration so we can peek at consecutive items. DeepSeek
		// requires that an assistant message with tool_calls be followed
		// immediately by tool messages for each call_id — so consecutive
		// `function_call` items (from a single assistant turn in codex) must
		// merge into ONE assistant message with multiple tool_calls, then the
		// matching tool messages follow as a contiguous block.
		i := 0
		for i < len(inputs) {
			m, ok := inputs[i].(map[string]interface{})
			if !ok {
				i++
				continue
			}
			typ, _ := m["type"].(string)
			if typ == "" {
				typ = "message"
			}
			switch typ {
			case "reasoning":
				pendingReasoning = extractReasoningSummary(m)
				i++
			case "message":
				role, _ := m["role"].(string)
				if role == "" {
					i++
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
					i++
					continue
				}
				msg := map[string]interface{}{
					"role":    role,
					"content": text,
				}
				if role == "assistant" && deepseek {
					msg["reasoning_content"] = consumePendingReasoning()
				}
				messages = append(messages, msg)
				i++
			case "function_call":
				// Greedily consume every consecutive function_call into ONE
				// assistant message — codex emits them as separate input items
				// but they were a single assistant turn.
				toolCalls := []map[string]interface{}{}
				j := i
				for j < len(inputs) {
					mj, ok := inputs[j].(map[string]interface{})
					if !ok {
						break
					}
					if t, _ := mj["type"].(string); t != "function_call" {
						break
					}
					callID, _ := mj["call_id"].(string)
					name, _ := mj["name"].(string)
					args, _ := mj["arguments"].(string)
					toolCalls = append(toolCalls, map[string]interface{}{
						"id":   callID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      name,
							"arguments": args,
						},
					})
					j++
				}
				asst := map[string]interface{}{
					"role":       "assistant",
					"content":    nil,
					"tool_calls": toolCalls,
				}
				if deepseek {
					asst["reasoning_content"] = consumePendingReasoning()
				}
				messages = append(messages, asst)
				// For each tool_call in this assistant message, ensure a
				// corresponding tool message follows. We emit them now in
				// the same order as the tool_calls. If a function_call_output
				// for the call_id is found later in the input, use its content;
				// otherwise insert a synthetic stub so the assistant.tool_calls
				// → tool_messages pair is balanced (DeepSeek rejects orphans).
				for _, tc := range toolCalls {
					callID, _ := tc["id"].(string)
					content := ""
					// scan ahead in inputs for matching function_call_output
					for k := j; k < len(inputs); k++ {
						mk, ok := inputs[k].(map[string]interface{})
						if !ok {
							continue
						}
						if t, _ := mk["type"].(string); t != "function_call_output" {
							continue
						}
						if id, _ := mk["call_id"].(string); id == callID {
							content, _ = mk["output"].(string)
							break
						}
					}
					if content == "" && callID != "" && !fulfilledCallIDs[callID] {
						content = "Tool call discarded by client — no result produced (e.g. tool not available in local registry)."
					}
					messages = append(messages, map[string]interface{}{
						"role":         "tool",
						"tool_call_id": callID,
						"content":      content,
					})
				}
				i = j
			case "function_call_output":
				// Already emitted inline with its assistant.tool_calls block
				// above. Skip — emitting again here would duplicate tool
				// messages and DeepSeek would reject "unknown tool_call_id"
				// on the second one.
				i++
			default:
				i++
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
			// Tolerate both Responses-API flat format `{type,name,description,parameters}`
			// and chat-completions nested format `{type,function:{name,description,parameters}}`,
			// since upstream callers emit either shape.
			src := tm
			if nested, ok := tm["function"].(map[string]interface{}); ok {
				src = nested
			}
			fn := map[string]interface{}{}
			if name, ok := src["name"]; ok {
				fn["name"] = name
			}
			if desc, ok := src["description"]; ok {
				fn["description"] = desc
			}
			fn["parameters"] = sanitizeFunctionParameters(src["parameters"])
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

	// usage captured from the upstream's terminal chunk (chat-completions shape:
	// prompt_tokens / completion_tokens / prompt_tokens_details.cached_tokens or
	// deepseek's prompt_cache_hit_tokens). Forwarded into response.completed so
	// codex AND the audit see real tokens + cache.
	usage map[string]interface{}
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

// newCodexResponsesReader adapts an upstream's reply to codex's /responses request
// into a Responses event stream — but ONLY when the upstream actually answered in
// Chat Completions format. Some upstreams behind the /chat/completions request we
// send (notably the cicy cloud gateway) ALREADY answer in Responses format; for
// those we must pass the stream through untouched, because running it through the
// chat->responses converter (which scans for chat `delta.content`) would find
// nothing and emit an empty response (output:[]) — the codex empty-reply bug.
// We detect the format by peeking the first bytes of the stream.
func newCodexResponsesReader(body io.ReadCloser) io.ReadCloser {
	br := bufio.NewReader(body)
	prefix, _ := br.Peek(512) // non-consuming; short read at EOF is fine
	if looksLikeResponsesStream(prefix) {
		return &passthroughReadCloser{r: br, c: body}
	}
	return &chatCompletionsToResponsesReader{
		src:       br,
		srcClose:  body,
		toolCalls: map[int]*toolCallState{},
	}
}

// looksLikeResponsesStream reports whether an SSE prefix is already an OpenAI
// Responses event stream (event: response.* / a data payload typed response.*)
// rather than chat.completion.chunk data.
func looksLikeResponsesStream(prefix []byte) bool {
	s := string(prefix)
	return strings.Contains(s, "event: response.") ||
		strings.Contains(s, `"type":"response.`) ||
		strings.Contains(s, `"type": "response.`)
}

// passthroughReadCloser streams an already-buffered reader through verbatim and
// closes the underlying source on Close.
type passthroughReadCloser struct {
	r io.Reader
	c io.Closer
}

func (p *passthroughReadCloser) Read(b []byte) (int, error) { return p.r.Read(b) }

func (p *passthroughReadCloser) Close() error {
	if p.c != nil {
		return p.c.Close()
	}
	return nil
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

	// Capture usage — DeepSeek/one-api send it on the terminal chunk (often with
	// an empty choices array), so read it before the choices guard below.
	if u, ok := chunk["usage"].(map[string]interface{}); ok && len(u) > 0 {
		r.usage = u
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
		respObj := map[string]interface{}{
			"id":     r.responseID,
			"object": "response",
			"status": "completed",
			"model":  r.model,
			"output": output,
		}
		if u := chatUsageToResponsesUsage(r.usage); u != nil {
			respObj["usage"] = u
		}
		r.emitEvent("response.completed", map[string]interface{}{
			"type":     "response.completed",
			"response": respObj,
		})
		r.out.WriteString("data: [DONE]\n\n")
	}
}

// chatUsageToResponsesUsage maps a chat-completions usage object to the OpenAI
// Responses shape (input_tokens / output_tokens / input_tokens_details.
// cached_tokens) so the gateway's audit usage parser + aiGatewayNormalizeOpenAIUsage
// pick up tokens and cache uniformly. Handles DeepSeek's cache fields:
// prompt_tokens_details.cached_tokens and the flat prompt_cache_hit_tokens.
func chatUsageToResponsesUsage(u map[string]interface{}) map[string]interface{} {
	if len(u) == 0 {
		return nil
	}
	num := func(m map[string]interface{}, k string) (float64, bool) {
		v, ok := m[k].(float64)
		return v, ok
	}
	out := map[string]interface{}{}
	if v, ok := num(u, "prompt_tokens"); ok {
		out["input_tokens"] = v
	}
	if v, ok := num(u, "completion_tokens"); ok {
		out["output_tokens"] = v
	}
	if v, ok := num(u, "total_tokens"); ok {
		out["total_tokens"] = v
	}
	cached := 0.0
	if d, ok := u["prompt_tokens_details"].(map[string]interface{}); ok {
		if c, ok := num(d, "cached_tokens"); ok {
			cached = c
		}
	}
	if cached == 0 {
		if c, ok := num(u, "prompt_cache_hit_tokens"); ok {
			cached = c
		}
	}
	if cached > 0 {
		out["input_tokens_details"] = map[string]interface{}{"cached_tokens": cached}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// chatUsageToAnthropicUsage maps chat-completions usage into the Anthropic usage
// shape (input_tokens is uncached-only, with a separate cache_read_input_tokens),
// so claude clients and the audit's Anthropic usage path record tokens + cache.
// DeepSeek's prompt_tokens INCLUDES cached, so we subtract.
func chatUsageToAnthropicUsage(u map[string]interface{}) map[string]interface{} {
	if len(u) == 0 {
		return nil
	}
	num := func(m map[string]interface{}, k string) float64 {
		v, _ := m[k].(float64)
		return v
	}
	prompt := num(u, "prompt_tokens")
	completion := num(u, "completion_tokens")
	cached := 0.0
	if d, ok := u["prompt_tokens_details"].(map[string]interface{}); ok {
		cached = num(d, "cached_tokens")
	}
	if cached == 0 {
		cached = num(u, "prompt_cache_hit_tokens")
	}
	input := prompt - cached
	if input < 0 {
		input = 0
	}
	out := map[string]interface{}{"output_tokens": completion}
	out["input_tokens"] = input
	if cached > 0 {
		out["cache_read_input_tokens"] = cached
	}
	return out
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

// shouldAdaptDeepSeekForAnthropic returns true when the gateway must translate
// an incoming Anthropic Messages request into Chat Completions for the upstream.
// We do this for hosts that only speak Chat Completions natively (vanilla
// api.deepseek.com). DeepSeek also exposes a dedicated Anthropic-compatible
// endpoint at `https://api.deepseek.com/anthropic/v1/messages`; when the
// configured provider URL already points at that path prefix we MUST pass the
// request through as-is — translating it would rewrite the suffix to
// `/anthropic/v1/chat/completions`, which DeepSeek does NOT serve (404).
func shouldAdaptDeepSeekForAnthropic(upstreamHost, basePath, suffix string) bool {
	if !strings.Contains(strings.ToLower(upstreamHost), "deepseek") {
		return false
	}
	if !strings.Contains(strings.ToLower(suffix), "/messages") {
		return false
	}
	// If upstream base path already includes /anthropic, this is DeepSeek's
	// native Messages endpoint — pass through, no translation.
	if strings.Contains(strings.ToLower(basePath), "/anthropic") {
		return false
	}
	return true
}

func rewriteSuffixForDeepSeekChatCompletionsFromMessages(suffix string) string {
	return strings.Replace(suffix, "/messages", "/chat/completions", 1)
}

// isDeepSeekFlavored reports whether the upstream is DeepSeek — directly
// (api.deepseek.com) OR fronted by a proxy (new-api / cicy cloud). It governs
// whether the request transforms layer on DeepSeek's PRIVATE quirks on top of
// the standard Chat Completions translation:
//   - the V4 `thinking:{type:...}` switch (not a standard OpenAI field)
//   - `reasoning_content` echoed on every assistant history turn, which DeepSeek's
//     thinking-mode validator 400s when missing/empty
//
// Standard OpenAI-style upstreams (opencodeZen, generic openai-compat) must NOT
// receive these — they get a clean, unforced translation.
//
// Detection is host OR model name. A proxied DeepSeek carries a non-deepseek host
// (gateway.cicy-ai.com) but the model id ("deepseek-v4"/"deepseek-chat"/
// "deepseek-reasoner") rides the request body unchanged — and the model is exactly
// what arms DeepSeek's validator upstream. Standard models (gpt-*, opencodeZen ids)
// never match, so they stay on the clean path.
func isDeepSeekFlavored(upstreamHost, model string) bool {
	if strings.Contains(strings.ToLower(upstreamHost), "deepseek") {
		return true
	}
	m := strings.ToLower(model)
	if strings.Contains(m, "deepseek") {
		return true
	}
	// Opaque DeepSeek aliases (opencodeZen's "big-pickle", etc.) carry no "deepseek"
	// in the id, so name-matching can't catch them. Instead we LEARN which models
	// demand the reasoning_content passback from the upstream's own 400 (see the
	// retry loop in ai_gateway.go) — no hardcoded alias list, works for any model
	// the user freely switches to.
	if learnedDeepseekReasoningModel(model) {
		return true
	}
	return false
}

// isGeminiFlavored reports whether the upstream is Google Gemini's OpenAI-compat
// endpoint (generativelanguage.googleapis.com/v1beta/openai), directly or behind
// a proxy. Gemini differs from both DeepSeek and standard OpenAI in how it
// carries thinking: NOT a `reasoning_content` field, but the thought text inline
// in `delta.content` wrapped in <thought>…</thought>, flagged per-chunk by
// `extra_content.google.thought == true`. And it only EMITS thoughts when the
// request asks via `extra_body.google.thinking_config.include_thoughts`. Both the
// request injection and the SSE reader gate on this. Host OR model match (a
// proxied Gemini keeps the "gemini-*" model id in the body).
func isGeminiFlavored(upstreamHost, model string) bool {
	h := strings.ToLower(upstreamHost)
	if strings.Contains(h, "generativelanguage") || strings.Contains(h, "googleapis") {
		return true
	}
	return strings.Contains(strings.ToLower(model), "gemini")
}

// stripThoughtTags removes Gemini's literal <thought>/</thought> delimiters that
// bracket the inline thinking text in the OpenAI-compat stream (the opening tag
// rides the first thought chunk, the closing tag prefixes the first answer
// chunk). Safe as a plain replace — the tags only ever appear at those bounds.
func stripThoughtTags(s string) string {
	s = strings.ReplaceAll(s, "<thought>", "")
	s = strings.ReplaceAll(s, "</thought>", "")
	return s
}

// deepSeekThinkingFromSource extracts the resolved DeepSeek V4 thinking switch
// from an Anthropic-shaped `thinking` value (set upstream by
// agentInspectorApplyThinking per the config-driven policy). Only the `type` is
// carried — DeepSeek's switch is `{type:"enabled"|"disabled"}`; budget_tokens and
// other Anthropic subfields are dropped. Absent/garbage → disabled (safe default
// that never trips the thinking-mode validator).
func deepSeekThinkingFromSource(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		if t, ok := m["type"].(string); ok && (t == "enabled" || t == "disabled") {
			return map[string]interface{}{"type": t}
		}
	}
	return map[string]interface{}{"type": "disabled"}
}

// shouldAdaptAnthropicToChatCompletions decides whether an Anthropic Messages
// request hitting the /anthropic endpoint must be bridged down to Chat Completions
// for the upstream. Two cases:
//   - DeepSeek hosts that only serve Chat Completions (shouldAdaptDeepSeekForAnthropic).
//   - The agent's effective provider declares the openai protocol — e.g. the cicy
//     agent (always Anthropic-format) configured onto an openai-protocol provider.
//     The gateway translates the request and wraps the SSE back to Anthropic Messages
//     so the agent never has to speak two protocols.
//
// Native anthropic upstreams skip translation: a provider whose protocol is
// "anthropic" (incl. DeepSeek's /anthropic passthrough handled above) is forwarded
// as-is. pathProvider is the URL segment ("anthropic"); agentID scopes the lookup.
func shouldAdaptAnthropicToChatCompletions(pathProvider, agentID, upstreamHost, basePath, suffix string) bool {
	if shouldAdaptDeepSeekForAnthropic(upstreamHost, basePath, suffix) {
		return true
	}
	if normalizeAIGatewayProvider(pathProvider) != "anthropic" {
		return false // only the /anthropic endpoint carries Messages-format bodies
	}
	if !strings.Contains(strings.ToLower(suffix), "/messages") {
		return false
	}
	if strings.Contains(strings.ToLower(basePath), "/anthropic") {
		return false // upstream is already a native Messages endpoint — pass through
	}
	return aiGatewayEffectiveProviderProtocol(pathProvider, agentID) == "openai"
}

// aiGatewayEffectiveProviderProtocol returns the normalized protocol ("anthropic"
// /"openai"/"") of the provider that actually serves this agent's request, honoring
// the runtime_ai override and the agent-type default, for the given endpoint path.
func aiGatewayEffectiveProviderProtocol(pathProvider, agentID string) string {
	cfg, _, err := resolveRuntimeAIConfigForAgent(pathProvider, agentID)
	if err != nil {
		return ""
	}
	if pc, ok := loadProviderByKey(cfg.Provider); ok && pc != nil {
		return normalizeAIGatewayProvider(pc.Protocol)
	}
	return ""
}

// transformMessagesRequestToChatCompletions converts an Anthropic Messages API
// request body into a Chat Completions request body. Returns new body + model.
func transformMessagesRequestToChatCompletions(body []byte, upstreamHost string) ([]byte, string, error) {
	var src map[string]interface{}
	if err := json.Unmarshal(body, &src); err != nil {
		return body, "", err
	}

	dst := map[string]interface{}{}
	model, _ := src["model"].(string)
	if model != "" {
		dst["model"] = model
	}
	deepseek := isDeepSeekFlavored(upstreamHost, model)
	if s, ok := src["stream"].(bool); ok {
		dst["stream"] = s
		if s {
			// Ask the upstream to stream usage (see the Responses path) so the
			// claude→DeepSeek conversion can surface tokens + cache instead of 0.
			if _, ok := src["stream_options"]; ok {
				dst["stream_options"] = src["stream_options"]
			} else {
				dst["stream_options"] = map[string]interface{}{"include_usage": true}
			}
		}
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
	// DeepSeek-ONLY: carry over the resolved V4 thinking switch (set on the
	// Anthropic body by agentInspectorApplyThinking per config) so thinking
	// actually toggles. Standard OpenAI upstreams get NO `thinking` field —
	// it's a DeepSeek private extension, and forcing it disabled here was what
	// silently killed thinking for every openai-style provider. Their thinking
	// follows the client/model defaults, unforced.
	if deepseek {
		dst["thinking"] = deepSeekThinkingFromSource(src["thinking"])
	}

	// Gemini-ONLY: ask the OpenAI-compat endpoint to return thought summaries so
	// the SSE reader can surface them as Anthropic thinking blocks. Gemini 2.5
	// thinks by default but withholds the reasoning text unless include_thoughts
	// is set. Honor an explicit disable (thinking.type=="disabled") by NOT asking
	// for thoughts; otherwise opt in. This is a Gemini private extension under
	// extra_body — standard OpenAI/DeepSeek upstreams never see it.
	if isGeminiFlavored(upstreamHost, model) {
		wantThoughts := true
		if th, ok := src["thinking"].(map[string]interface{}); ok {
			if t, _ := th["type"].(string); t == "disabled" {
				wantThoughts = false
			}
		}
		if wantThoughts {
			dst["extra_body"] = map[string]interface{}{
				"google": map[string]interface{}{
					"thinking_config": map[string]interface{}{"include_thoughts": true},
				},
			}
		}
	}

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
			out := convertAnthropicMessageToChatCompletions(role, content, deepseek)
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
func convertAnthropicMessageToChatCompletions(role string, content interface{}, deepseek bool) []map[string]interface{} {
	if s, ok := content.(string); ok {
		msg := map[string]interface{}{"role": role, "content": s}
		// DeepSeek's thinking-mode validator demands a non-empty reasoning_content
		// on every assistant history turn; a plain-string assistant turn carries
		// no thinking block, so fall back to the placeholder. (See the block-array
		// path below for the real-thinking echo.)
		if deepseek && role == "assistant" {
			msg["reasoning_content"] = "."
		}
		return []map[string]interface{}{msg}
	}
	parts, ok := content.([]interface{})
	if !ok {
		return nil
	}

	var text strings.Builder
	var reasoning strings.Builder
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
		case "thinking":
			// Captured to echo back as reasoning_content for DeepSeek (below).
			if t, ok := pm["thinking"].(string); ok {
				reasoning.WriteString(t)
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
		// DeepSeek-ONLY: echo the real thinking back as reasoning_content (the
		// validator rejects empty/missing); placeholder when this turn carried
		// none. Standard OpenAI upstreams get no reasoning_content at all — it's
		// not part of the standard API.
		if deepseek {
			rc := strings.TrimSpace(reasoning.String())
			if rc == "" {
				rc = "."
			}
			msg["reasoning_content"] = rc
		}
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

	// Thinking block state. Reasoning (`delta.reasoning_content`) streams BEFORE
	// the visible text; we surface it as an Anthropic `thinking` content block so
	// the agent/UI shows the model's reasoning. This is PROTOCOL-AGNOSTIC — any
	// upstream that emits reasoning_content (DeepSeek AND standard openai-style
	// providers like opencodeZen) gets its thinking rendered.
	thinkingBlockStarted bool
	thinkingBlockClosed  bool
	thinkingBlockIndex   int
	accumThinking        strings.Builder

	// Text block state
	textBlockStarted bool
	textBlockClosed  bool
	textBlockIndex   int
	accumText        strings.Builder

	// Tool-call blocks (per upstream "index" slot)
	toolBlocks     map[int]*anthropicToolBlock
	nextBlockIndex int
	finishReason   string

	// usage captured from the upstream terminal chunk → surfaced (Anthropic shape)
	// in the final message_delta so tokens + cache reach claude and the audit.
	usage map[string]interface{}
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

	// Usage rides the terminal chunk (often with empty choices) — capture before
	// the choices guard.
	if u, ok := chunk["usage"].(map[string]interface{}); ok && len(u) > 0 {
		r.usage = u
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
		// Gemini (OpenAI-compat) marks thought chunks with
		// extra_content.google.thought==true and puts the reasoning text inline in
		// `content` (wrapped in <thought>…</thought>). Detect the marker so the
		// SAME content field routes to thinking vs visible text. The marker is
		// Gemini-specific, so this branch is inert for DeepSeek/standard OpenAI.
		geminiThought := false
		if ec, ok := delta["extra_content"].(map[string]interface{}); ok {
			if g, ok := ec["google"].(map[string]interface{}); ok {
				if th, _ := g["thought"].(bool); th {
					geminiThought = true
				}
			}
		}
		// Reasoning streams first — open/extend the thinking block.
		if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
			r.ensureThinkingBlock()
			r.emitThinkingDelta(rc)
			r.accumThinking.WriteString(rc)
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			if geminiThought {
				// Gemini thought chunk → thinking block (strip <thought> delimiter).
				if clean := stripThoughtTags(content); clean != "" {
					r.ensureThinkingBlock()
					r.emitThinkingDelta(clean)
					r.accumThinking.WriteString(clean)
				}
			} else {
				// Visible answer. The transition chunk prefixes a stray </thought>;
				// strip it (no-op for non-Gemini upstreams).
				if clean := stripThoughtTags(content); clean != "" {
					r.closeThinkingBlock()
					r.ensureTextBlock()
					r.emitTextDelta(clean)
					r.accumText.WriteString(clean)
				}
			}
		}
		if rawTC, ok := delta["tool_calls"].([]interface{}); ok {
			r.closeThinkingBlock()
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

func (r *chatCompletionsToMessagesReader) ensureThinkingBlock() {
	if r.thinkingBlockStarted {
		return
	}
	r.thinkingBlockStarted = true
	r.thinkingBlockIndex = r.nextBlockIndex
	r.nextBlockIndex++
	r.emitEvent("content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": r.thinkingBlockIndex,
		"content_block": map[string]interface{}{
			"type":     "thinking",
			"thinking": "",
		},
	})
}

func (r *chatCompletionsToMessagesReader) emitThinkingDelta(delta string) {
	r.emitEvent("content_block_delta", map[string]interface{}{
		"type":  "content_block_delta",
		"index": r.thinkingBlockIndex,
		"delta": map[string]interface{}{
			"type":     "thinking_delta",
			"thinking": delta,
		},
	})
}

func (r *chatCompletionsToMessagesReader) closeThinkingBlock() {
	if !r.thinkingBlockStarted || r.thinkingBlockClosed {
		return
	}
	r.thinkingBlockClosed = true
	r.emitEvent("content_block_stop", map[string]interface{}{
		"type":  "content_block_stop",
		"index": r.thinkingBlockIndex,
	})
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
	r.closeThinkingBlock()
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
	usage := chatUsageToAnthropicUsage(r.usage)
	if usage == nil {
		usage = map[string]interface{}{"output_tokens": 0}
	}
	r.emitEvent("message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usage,
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
