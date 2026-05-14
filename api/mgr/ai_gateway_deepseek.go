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

// shouldAdaptDeepSeekForCodex returns true when the upstream is DeepSeek and the
// client is calling the Responses API (codex). DeepSeek only speaks Chat
// Completions, so we translate request + streaming response.
func shouldAdaptDeepSeekForCodex(upstreamHost, suffix string) bool {
	return strings.Contains(strings.ToLower(upstreamHost), "deepseek") &&
		strings.Contains(strings.ToLower(suffix), "/responses")
}

// rewriteSuffixForDeepSeekChatCompletions converts a Responses-API suffix into
// the equivalent Chat Completions endpoint suffix (so the reverse proxy hits
// the right DeepSeek path).
func rewriteSuffixForDeepSeekChatCompletions(suffix string) string {
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

	// input + instructions → messages
	messages := make([]map[string]interface{}, 0)
	if instr, ok := src["instructions"].(string); ok && strings.TrimSpace(instr) != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": instr,
		})
	}

	if inputs, ok := src["input"].([]interface{}); ok {
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
				messages = append(messages, map[string]interface{}{
					"role":    role,
					"content": text,
				})
			case "function_call":
				callID, _ := m["call_id"].(string)
				name, _ := m["name"].(string)
				args, _ := m["arguments"].(string)
				messages = append(messages, map[string]interface{}{
					"role": "assistant",
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
			if params, ok := tm["parameters"]; ok {
				fn["parameters"] = params
			}
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
	textDoneSent  bool
	completedSent bool

	responseID string
	itemID     string
	model      string
	accumText  strings.Builder
}

func newChatCompletionsToResponsesReader(body io.ReadCloser, model string) io.ReadCloser {
	return &chatCompletionsToResponsesReader{
		src:      bufio.NewReader(body),
		srcClose: body,
		model:    model,
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
		r.itemID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
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
		if content, ok := delta["content"].(string); ok && content != "" {
			r.emitTextDelta(content)
			r.accumText.WriteString(content)
		}
	}
	if finish, _ := choice["finish_reason"].(string); finish != "" && !r.textDoneSent {
		r.emitTextDone()
	}
	return nil
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
	r.emitEvent("response.output_item.added", map[string]interface{}{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]interface{}{
			"id":      r.itemID,
			"type":    "message",
			"role":    "assistant",
			"content": []interface{}{},
			"status":  "in_progress",
		},
	})
	r.emitEvent("response.content_part.added", map[string]interface{}{
		"type":          "response.content_part.added",
		"output_index":  0,
		"item_id":       r.itemID,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
			"text": "",
		},
	})
}

func (r *chatCompletionsToResponsesReader) emitTextDelta(delta string) {
	r.emitEvent("response.output_text.delta", map[string]interface{}{
		"type":          "response.output_text.delta",
		"output_index":  0,
		"item_id":       r.itemID,
		"content_index": 0,
		"delta":         delta,
	})
}

func (r *chatCompletionsToResponsesReader) emitTextDone() {
	r.textDoneSent = true
	text := r.accumText.String()
	r.emitEvent("response.output_text.done", map[string]interface{}{
		"type":          "response.output_text.done",
		"output_index":  0,
		"item_id":       r.itemID,
		"content_index": 0,
		"text":          text,
	})
	r.emitEvent("response.content_part.done", map[string]interface{}{
		"type":          "response.content_part.done",
		"output_index":  0,
		"item_id":       r.itemID,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
			"text": text,
		},
	})
	r.emitEvent("response.output_item.done", map[string]interface{}{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item": map[string]interface{}{
			"id":     r.itemID,
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]interface{}{
				{"type": "output_text", "text": text},
			},
		},
	})
}

func (r *chatCompletionsToResponsesReader) emitCompletedAndDone() {
	if r.headerEmitted && !r.textDoneSent {
		r.emitTextDone()
	}
	if !r.completedSent {
		r.completedSent = true
		if r.responseID == "" {
			r.responseID = fmt.Sprintf("resp_%d", time.Now().UnixNano())
		}
		r.emitEvent("response.completed", map[string]interface{}{
			"type": "response.completed",
			"response": map[string]interface{}{
				"id":     r.responseID,
				"object": "response",
				"status": "completed",
				"model":  r.model,
				"output": []map[string]interface{}{
					{
						"id":     r.itemID,
						"type":   "message",
						"role":   "assistant",
						"status": "completed",
						"content": []map[string]interface{}{
							{"type": "output_text", "text": r.accumText.String()},
						},
					},
				},
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
