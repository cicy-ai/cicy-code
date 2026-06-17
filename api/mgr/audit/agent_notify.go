package audit

import (
	"fmt"
	"log"
	"strings"
)

// findingForwarder hands a triggering finding to the 审核策略专员 (the user's
// audit advisor) agent, which verifies the hit, grades severity, and handles
// the response per its charter. Injected by the main package, which owns the
// cross-agent send path (sendTextToPane → the live 审核策略专员 pane). nil =
// forwarding disabled.
var findingForwarder func(brief string) error

// SetFindingForwarder wires the "forward finding to the audit advisor" channel.
// Called once at startup from the main package.
func SetFindingForwarder(fn func(brief string) error) { findingForwarder = fn }

// forwardFindingToAdvisor pushes a finding brief to the 审核策略专员 for triage.
// The advisor owns the verification/grading; the SMTP owner alert is sent
// separately by dispatchIncident. Returns false when the channel is unset or
// the send fails (e.g. no 审核策略专员 agent currently provisioned).
func forwardFindingToAdvisor(e Event) bool {
	if findingForwarder == nil {
		return false
	}
	if err := findingForwarder(renderFindingBrief(e)); err != nil {
		log.Printf("[audit] forward-to-advisor failed event=%s: %v", e.ID, err)
		return false
	}
	log.Printf("[audit] finding forwarded to 审核策略专员 event=%s agent=%s findings=%d",
		e.ID, e.Identity.AgentID, len(e.Findings))
	return true
}

// renderFindingBrief is the message the 审核策略专员 receives. Metadata +
// masked preview only — never the raw payload. The advisor reasons over this
// brief and handles the response per its charter.
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
	b.WriteString("请按你的 charter 用审计 tool 研判并处置:核实真命中 vs 误报 → 分级 → 误报就用 audit_allowlist_add 加白名单/调规则 / 真命中归档或升级(高危通知合伙人、建议拦截须用户拍板)。\n")
	return b.String()
}
