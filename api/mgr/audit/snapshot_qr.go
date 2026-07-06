// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/json"
	"fmt"
	"strings"
)

// qrSnapshot is the forensic record attached to an audited turn: the full QA of
// the turn — system + every message (text, tool_use, tool_result, thinking). It
// is saved to the per-agent snapshots dir and referenced by Event.Meta.SnapshotRef
// so the audit log can answer "这一轮到底发生了什么". Content is REAL (un-redacted)
// — a forensic record must show exactly what leaked; it is no new exposure since
// current.json / reply.json already hold the same plaintext. The block info
// returned to the agent is unaffected — this lives only in the audit trail.
type qrSnapshot struct {
	EventID        string   `json:"event_id"`
	ConversationID string   `json:"conversation_id,omitempty"`
	HistoryID      string   `json:"history_id,omitempty"`
	AgentID        string   `json:"agent_id"`
	Direction      string   `json:"direction"`
	Action         string   `json:"action"`
	Rules          []string `json:"rules,omitempty"`
	Q              string   `json:"q"`     // quick summary: last user message text (redacted)
	Reply          string   `json:"reply"` // quick summary: model reply / blocked note (redacted)

	// Full QA snapshot — the ENTIRE turn, not just q/reply. System prompt plus
	// every message in order, each rendered with ALL its content blocks (text,
	// tool_use command + args, tool_result data, thinking, tool_calls). REAL
	// content, NOT redacted — a forensic record must show exactly what leaked
	// (the actual secret), including the bash/read the agent ran and the data
	// those tools returned/sent.
	System   string        `json:"system,omitempty"`
	Messages []snapMessage `json:"messages,omitempty"`

	Timestamp string `json:"ts"`
}

// snapMessage is one message in the full-QA snapshot: its role and its fully
// rendered content (text + tool calls + tool results + thinking), redacted.
type snapMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// saveQRSnapshot builds and persists the full-QA snapshot for an event and
// returns its ref (empty on failure). The snapshot stores the REAL (un-redacted)
// content — this is a forensic record: the auditor must see exactly what secret
// leaked, not a masked placeholder. It is no NEW exposure: current.json /
// reply.json in the same per-agent history already hold the same plaintext.
func (p *Pipeline) saveQRSnapshot(e Event, env Envelope, findings []Finding) string {
	payload := env.Payload // raw, NOT redacted — forensic snapshot keeps real data
	ruleIDs := uniqueRuleIDs(findings)

	q := extractRoleText(payload, "user")
	var reply string
	switch {
	case e.Decision.Action == ActionBlock:
		reply = "（已拦截·未发送给模型" + rulesSuffix(ruleIDs) + "）"
	case env.Direction == DirectionInbound:
		// The inbound payload IS the model reply — show it.
		if a := extractRoleText(payload, "assistant"); a != "" {
			reply = a
		} else {
			reply = strings.TrimSpace(firstN(string(payload), 4000))
		}
	default:
		reply = "（本轮未记录响应）"
	}

	// Full QA: every message of the turn, fully rendered (text + tool_use +
	// tool_result + thinking + tool_calls), from the REAL payload (un-redacted).
	system, messages := extractConversation(payload)

	snap := qrSnapshot{
		EventID:        e.ID,
		ConversationID: e.Subject.ConversationID,
		HistoryID:      e.Subject.HistoryID,
		AgentID:        env.AgentID,
		Direction:      env.Direction,
		Action:         string(e.Decision.Action),
		Rules:          ruleIDs,
		Q:              firstN(q, 8000),
		Reply:          firstN(reply, 8000),
		System:         firstN(system, 8000),
		Messages:       messages,
		Timestamp:      e.Timestamp,
	}
	blob, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return ""
	}
	ref, err := SaveSnapshot(p.store.workersRoot, env.AgentID, e.ID, blob)
	if err != nil {
		return ""
	}
	return ref
}

func rulesSuffix(ruleIDs []string) string {
	if len(ruleIDs) == 0 {
		return ""
	}
	return ";命中 " + strings.Join(ruleIDs, ", ")
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("…(+%dB)", len(s)-n)
}

// extractRoleText returns the text of the LAST message with the given role in a
// JSON request/response body. Handles Anthropic/OpenAI shapes: messages[] with
// content as a plain string OR an array of {type:"text",text:"…"} blocks.
// Returns "" if not parseable / not found.
func extractRoleText(payload []byte, role string) string {
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return ""
	}
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role != role {
			continue
		}
		return contentToText(body.Messages[i].Content)
	}
	return ""
}

// extractConversation returns the FULL conversation of a request/response body —
// the system prompt plus every message in order, each rendered with ALL its
// content (text, tool_use command+args, tool_result data, thinking, OpenAI
// tool_calls). This is the "整个 QA 快照": the complete turn, not just the last
// user/assistant text. Input is the REDACTED payload so secrets stay masked.
func extractConversation(payload []byte) (system string, messages []snapMessage) {
	var body struct {
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"` // OpenAI-shape assistant tool calls
		} `json:"messages"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return "", nil
	}
	system = contentToTextFull(body.System)
	messages = make([]snapMessage, 0, len(body.Messages))
	for _, m := range body.Messages {
		parts := []string{}
		if c := contentToTextFull(m.Content); c != "" {
			parts = append(parts, c)
		}
		for _, tc := range m.ToolCalls {
			parts = append(parts, "[tool_use "+tc.Function.Name+"] "+strings.TrimSpace(tc.Function.Arguments))
		}
		messages = append(messages, snapMessage{Role: m.Role, Content: strings.Join(parts, "\n")})
	}
	return system, messages
}

// contentToTextFull renders a content value INCLUDING tool_use commands,
// tool_result data and thinking — not just plain text (cf. contentToText). Used
// by the full-QA snapshot so an auditor sees the agent's actual bash/read calls
// and the data those tools returned/sent.
func contentToTextFull(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`   // tool_use args (Anthropic)
		Content  json.RawMessage `json:"content"` // tool_result content (Anthropic)
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	add := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s)
	}
	for _, blk := range blocks {
		switch blk.Type {
		case "text", "":
			add(blk.Text)
		case "thinking":
			add("[thinking] " + blk.Thinking)
		case "tool_use":
			add("[tool_use " + blk.Name + "] " + strings.TrimSpace(string(blk.Input)))
		case "tool_result":
			add("[tool_result] " + contentToTextFull(blk.Content))
		case "image":
			add("[image]")
		default:
			add(blk.Text)
		}
	}
	return b.String()
}

func contentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// content as a plain string
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// content as an array of blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	return ""
}
