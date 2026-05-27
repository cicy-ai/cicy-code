package audit

import (
	"fmt"
	"log"
	"strings"
)

// findingForwarder hands a triggering finding to the audit-policy ADVISOR agent
// (w-10000), which then decides the entire response: notify the offending
// agent, escalate to the responsible person, tune policy. Injected by the main
// package, which owns the cross-agent send path (sendTextToPane → w-10000).
// nil = forwarding disabled.
var findingForwarder func(brief string) error

// SetFindingForwarder wires the "forward finding to the advisor" channel.
// Called once at startup from the main package.
func SetFindingForwarder(fn func(brief string) error) { findingForwarder = fn }

// forwardFindingToAdvisor pushes a finding brief to w-10000 for triage. The
// advisor owns all downstream actions; the backend does NOT auto-notify agents
// or owners. Returns false when the channel is unset or the send fails.
func forwardFindingToAdvisor(e Event) bool {
	if findingForwarder == nil {
		return false
	}
	if err := findingForwarder(renderFindingBrief(e)); err != nil {
		log.Printf("[audit] forward-to-advisor failed event=%s: %v", e.ID, err)
		return false
	}
	log.Printf("[audit] finding forwarded to advisor event=%s agent=%s findings=%d",
		e.ID, e.Identity.AgentID, len(e.Findings))
	return true
}

// renderFindingBrief is the message the advisor (w-10000) receives. Metadata +
// masked preview only — never the raw payload. The advisor reasons over this
// brief and orchestrates the response with its own skills.
func renderFindingBrief(e Event) string {
	var b strings.Builder
	b.WriteString("【审计告警 · 待处置】\n")
	b.WriteString(fmt.Sprintf("event: %s\n", e.ID))
	b.WriteString(fmt.Sprintf("涉事 agent: %s (%s)\n", strings.TrimSpace(e.Identity.AgentID), e.Identity.AgentType))
	if e.Subject.Provider != "" || e.Subject.Model != "" {
		b.WriteString(fmt.Sprintf("provider/model: %s / %s\n", e.Subject.Provider, e.Subject.Model))
	}
	b.WriteString(fmt.Sprintf("方向: %s  当前动作: %s\n", e.Subject.Direction, e.Decision.Action))
	b.WriteString("命中规则:\n")
	for _, f := range e.Findings {
		preview := ""
		if len(f.Spans) > 0 {
			preview = f.Spans[0].Preview
		}
		b.WriteString(fmt.Sprintf("  - %s [%s/%s] x%d  %s\n", f.RuleID, f.Severity, f.Category, f.MatchCount, preview))
	}
	b.WriteString("请按 CLAUDE.md「处理审计告警」流程评估并处置:" +
		"通知涉事 agent 改用法(治本) / 严重则 cicy-policy notify <event> 通知责任人(止损) / 必要时 cicy-policy patch 调策略。\n")
	return b.String()
}
