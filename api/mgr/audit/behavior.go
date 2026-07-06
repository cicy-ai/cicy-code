// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/json"
	"strings"
)

// behavior.go — the BEHAVIOUR-layer scanner.
//
// The secret/PII regex scanner (scanner.go) audits what an agent SAYS to the
// model (intent). This file audits what an agent DOES — the tool calls it
// issues each turn (Bash commands, file writes/reads). Damage lives in
// behaviour (rm -rf, writing a key to disk, reading credentials), so this is
// the more fundamental layer.
//
// Input is a DirectionBehavior payload: a JSON array of ToolCall, one per tool
// the agent invoked this turn. It is fed from BOTH paths — the AI gateway
// (cooperative CLIs) and cicy-mitm (everything else) — because both converge in
// aiGatewayAuditSession.completeFromResponse, which already has the normalised
// ToolCalls. One event == one turn's tool calls, so this never re-scans history.

// ToolCall is one normalised tool invocation. The gateway already collapses
// Anthropic tool_use and OpenAI/Codex function_call/tool_calls into a single
// {ToolName, Arguments} shape; Provider is carried so behaviour normalisation
// can resolve provider-specific tool naming (claude Bash vs codex shell, etc).
type ToolCall struct {
	Provider  string `json:"provider"`
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
}

// toolAction is the provider-agnostic view a behaviour rule sees.
type toolAction struct {
	kind    string // "exec" | "write" | "read" | "other"
	target  string // file path (write/read) — best effort
	content string // command (exec) / file content (write) / raw args (other)
}

// normalizeToolCall maps a (provider, tool_name, arguments) triple onto a
// canonical action. Tool NAMES differ across CLIs (Barry: "注册 codex 和 claude
// 可能不同") even though the gateway unifies the call SHAPE — so the mapping is
// keyed on the lower-cased tool name with provider as a tie-breaker, and falls
// back to "other" (still scanned for credential strings) for unknown tools.
func normalizeToolCall(tc ToolCall) toolAction {
	name := strings.ToLower(strings.TrimSpace(tc.ToolName))
	args := tc.Arguments
	// Arguments is a raw JSON string; parse leniently — a non-JSON / partial
	// blob still gets scanned as raw content under "other".
	var m map[string]interface{}
	_ = json.Unmarshal([]byte(args), &m)
	get := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				switch t := v.(type) {
				case string:
					return t
				case []interface{}:
					// codex shell: {"command":["bash","-lc","rm -rf /"]}
					parts := make([]string, 0, len(t))
					for _, e := range t {
						if s, ok := e.(string); ok {
							parts = append(parts, s)
						}
					}
					return strings.Join(parts, " ")
				}
			}
		}
		return ""
	}

	switch {
	// ── exec ────────────────────────────────────────────────────────────
	case name == "bash" || name == "shell" || name == "local_shell" ||
		name == "exec" || name == "run_terminal_cmd" || name == "execute_command" ||
		strings.Contains(name, "shell") || strings.Contains(name, "bash") ||
		strings.Contains(name, "terminal"):
		cmd := get("command", "cmd", "script", "input")
		if cmd == "" {
			cmd = args
		}
		return toolAction{kind: "exec", content: cmd}

	// ── write / edit ────────────────────────────────────────────────────
	case name == "write" || name == "edit" || name == "multiedit" ||
		name == "notebookedit" || name == "create_file" || name == "write_file" ||
		name == "str_replace_editor" || strings.Contains(name, "edit") ||
		strings.Contains(name, "write"):
		target := get("file_path", "path", "filename", "target_file")
		content := get("content", "new_string", "new_str", "file_text", "text")
		return toolAction{kind: "write", target: target, content: content}

	// codex apply_patch: a single patch blob carrying paths + content
	case name == "apply_patch" || strings.Contains(name, "patch"):
		patch := get("input", "patch", "content")
		if patch == "" {
			patch = args
		}
		return toolAction{kind: "write", content: patch}

	// ── read ────────────────────────────────────────────────────────────
	case name == "read" || name == "read_file" || name == "cat" ||
		strings.Contains(name, "read"):
		target := get("file_path", "path", "filename", "target_file")
		if target == "" {
			target = args
		}
		return toolAction{kind: "read", target: target}
	}

	return toolAction{kind: "other", content: args}
}

// behaviorRule is a danger pattern over a tool action's text. The matcher is a
// detect closure (regex or JS), unified with the secret scanner so behaviour
// rules are authored as policy custom_rules (Match.Type regex|js) on the
// "behavior" scan direction. No builtin behaviour rules ship by default —
// rm/sudo/file-writes ARE the job, so blanket builtins false-positive; danger
// patterns are configured via policy.
type behaviorRule struct {
	id       string
	category string
	label    string
	severity Severity
	detect   func([]byte) []Span
}

// behaviorRulesFromPolicy compiles the policy's behaviour-direction custom rules
// (scan_directions includes "behavior") into runnable behaviorRules. Bad patterns
// are skipped (never panic on hand-edited policy). Empty when none configured →
// ScanToolCalls records the tool calls but finds nothing.
func behaviorRulesFromPolicy(policy *Policy) []behaviorRule {
	if policy == nil {
		return nil
	}
	out := make([]behaviorRule, 0, len(policy.CustomRules))
	for _, c := range policy.CustomRules {
		if c.Disabled || !directionMatches(c.ScanDirections, DirectionBehavior) {
			continue
		}
		// All matching runs through the JS engine (compileMatcher): regex is
		// translated to a JS RegExp matcher, js runs its snippet. One path.
		detect, err := compileMatcher(c.Match.Type, c.Match.Pattern, c.Match.Flags)
		if err != nil || detect == nil {
			continue
		}
		cat := c.Category
		if cat == "" {
			cat = "behavior"
		}
		sev := c.Severity
		if sev == "" {
			sev = SeverityMedium
		}
		out = append(out, behaviorRule{id: c.ID, category: cat, label: c.Label, severity: sev, detect: detect})
	}
	return out
}

// ScanToolCalls scans a DirectionBehavior payload (JSON array of ToolCall) against
// the given behaviour rules. Returns findings in the same shape as the secret
// scanner so they flow through the existing pipeline / events / UI unchanged.
func ScanToolCalls(payload []byte, rules []behaviorRule) []Finding {
	if len(payload) == 0 || len(rules) == 0 {
		return []Finding{}
	}
	var calls []ToolCall
	if err := json.Unmarshal(payload, &calls); err != nil || len(calls) == 0 {
		return []Finding{}
	}

	// Accumulate spans per rule across all tool calls in the turn.
	type acc struct {
		rule  behaviorRule
		spans []Span
	}
	order := []string{}
	byRule := map[string]*acc{}

	for _, tc := range calls {
		act := normalizeToolCall(tc) // kind only drives the human-readable preview label
		// Run rules over the FULL tool call text — tool name + raw arguments — so
		// the regex/JS matcher sees everything (command, file path, file content,
		// nested arg arrays). The rule's own pattern decides specificity; we do NOT
		// pre-filter by kind (that would hide args from the matcher).
		scanText := strings.TrimSpace(tc.ToolName + "\n" + tc.Arguments)
		if scanText == "" {
			continue
		}
		for _, rule := range rules {
			spans := rule.detect([]byte(scanText))
			if len(spans) == 0 {
				continue
			}
			a := byRule[rule.id]
			if a == nil {
				a = &acc{rule: rule}
				byRule[rule.id] = a
				order = append(order, rule.id)
			}
			a.spans = append(a.spans, Span{
				Start: spans[0].Start,
				End:   spans[0].End,
				// Preview carries the FULL action (the actual command / file +
				// content the agent ran), not just a window around the match —
				// the whole point of behaviour audit is seeing what was done.
				Preview: fullActionPreview(act.kind, scanText),
				Path:    tc.ToolName,
				Context: "",
			})
		}
	}

	out := make([]Finding, 0, len(order))
	for _, id := range order {
		a := byRule[id]
		out = append(out, Finding{
			RuleID:      a.rule.id,
			RuleVersion: RulesVersion,
			Severity:    a.rule.severity,
			Category:    a.rule.category,
			MatchCount:  len(a.spans),
			Spans:       a.spans,
		})
	}
	return out
}

// fullActionPreview returns the full action (command / file+content) the agent
// ran, prefixed with its kind, capped so a huge file write can't bloat the
// event. This is the audit artefact a human needs to judge a behaviour hit —
// "what did the agent actually do" — so it keeps the whole command, not a
// window around the regex match.
func fullActionPreview(kind, s string) string {
	const max = 4000
	body := s
	if len(body) > max {
		body = body[:max] + "\n…(truncated)"
	}
	if kind != "" {
		return "[" + kind + "] " + body
	}
	return body
}
