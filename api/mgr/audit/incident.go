package audit

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// dispatchIncident is the main entry called by pipeline.process AFTER an
// event has been appended. It evaluates the incident-response config,
// checks cooldown (separate from notify cooldown), resolves recipients,
// renders an email, and hands it to the mailer.
//
// Best-effort: any failure logs but does not propagate.
func (p *Pipeline) dispatchIncident(e Event) {
	pol := p.CurrentPolicy()
	cfg := pol.IncidentResponse
	if !cfg.Enabled {
		return
	}
	if !severityMeetsTrigger(e, cfg.TriggerMinSeverity) {
		return
	}

	hash := EventFindingHash(e)
	if hash == "" {
		return
	}
	if p.incidentCooldown.alreadyDispatched(hash, time.Duration(cfg.CooldownSeconds)*time.Second) {
		return
	}

	ruleIDs := uniqueRuleIDs(e.Findings)
	recipients := pol.ResponsiblePersons.Resolve(
		topSeverity(e.Findings),
		e.Identity.AgentID,
		e.Identity.UserID,
		ruleIDs,
	)
	if len(recipients) == 0 {
		return
	}

	subject, body := renderIncidentEmail(e, ruleIDs, cfg)
	msg := EmailMessage{
		To:       recipients,
		Subject:  subject,
		Body:     body,
		EventID:  e.ID,
		AgentID:  e.Identity.AgentID,
		Severity: topSeverity(e.Findings),
	}
	if err := p.mailer.Send(msg); err != nil {
		log.Printf("[audit] incident email send failed event=%s: %v", e.ID, err)
		return
	}
	p.incidentCooldown.markDispatched(hash)
	log.Printf("[audit] incident email dispatched event=%s severity=%s recipients=%v",
		e.ID, msg.Severity, recipients)
}

// severityMeetsTrigger returns true when the event's top finding severity
// is at or above min.
func severityMeetsTrigger(e Event, min Severity) bool {
	if len(e.Findings) == 0 {
		return false
	}
	rank := map[Severity]int{
		SeverityLow: 1, SeverityMedium: 2, SeverityHigh: 3, SeverityCritical: 4,
	}
	minRank := rank[min]
	if minRank == 0 {
		minRank = rank[SeverityHigh]
	}
	for _, f := range e.Findings {
		if rank[f.Severity] >= minRank {
			return true
		}
	}
	return false
}

func uniqueRuleIDs(findings []Finding) []string {
	if len(findings) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		if _, ok := seen[f.RuleID]; ok {
			continue
		}
		seen[f.RuleID] = struct{}{}
		out = append(out, f.RuleID)
	}
	return out
}

// renderIncidentEmail builds a bilingual (zh-CN + en) plain-text email
// body. AI summary is a placeholder for cut 1 (cut 2 wires the self-
// hosted LLM endpoint).
func renderIncidentEmail(e Event, ruleIDs []string, cfg IncidentResponseConfig) (subject, body string) {
	top := topSeverity(e.Findings)
	topRule := ""
	if len(ruleIDs) > 0 {
		topRule = ruleIDs[0]
	}
	subject = fmt.Sprintf("[CICY-AUDIT][%s] %s — %s",
		strings.ToUpper(string(top)), topRule, e.Identity.AgentID)

	var b strings.Builder
	// Chinese block
	fmt.Fprintf(&b, "事故级别: %s\n", strings.ToUpper(string(top)))
	fmt.Fprintf(&b, "触发时间: %s\n", e.Timestamp)
	fmt.Fprintf(&b, "触发 agent: %s", e.Identity.AgentID)
	if e.Identity.AgentType != "" {
		fmt.Fprintf(&b, " (%s)", e.Identity.AgentType)
	}
	b.WriteString("\n")
	if e.Identity.UserID != "" {
		fmt.Fprintf(&b, "触发用户: %s\n", e.Identity.UserID)
	}
	if e.Subject.Provider != "" || e.Subject.Model != "" {
		fmt.Fprintf(&b, "出站目标: %s / %s\n", e.Subject.Provider, e.Subject.Model)
	}
	fmt.Fprintf(&b, "当时动作: %s (applied: %v)\n", e.Decision.Action, e.Decision.Applied)
	fmt.Fprintf(&b, "事件 ID: %s\n", e.ID)
	if e.Meta.PreRedactRef != "" {
		fmt.Fprintf(&b, "原文(加密): %s\n", e.Meta.PreRedactRef)
	}
	b.WriteString("\n──────── 命中规则 ────────\n")
	for _, f := range e.Findings {
		fmt.Fprintf(&b, "  • %s  [%s]  ×%d", f.RuleID, f.Severity, f.MatchCount)
		if len(f.Spans) > 0 {
			fmt.Fprintf(&b, "  preview: %s", f.Spans[0].Preview)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n──────── AI 摘要 ────────\n")
	b.WriteString("(AI 辅助未启用 — Phase 6 cut 2 接入企业自托管模型后此处展示自动摘要)\n")

	b.WriteString("\n──────── 立即处置 ────────\n")
	b.WriteString("  □ 登录 dashboard 复核事件真实性\n")
	b.WriteString("  □ 如确认泄露,立即吊销凭据并断开 agent\n")
	b.WriteString("  □ 完成处置后到 dashboard 上 ack 该告警\n")

	// English mirror
	b.WriteString("\n──────── English summary ────────\n")
	fmt.Fprintf(&b, "  Severity:   %s\n", strings.ToUpper(string(top)))
	fmt.Fprintf(&b, "  Agent:      %s\n", e.Identity.AgentID)
	fmt.Fprintf(&b, "  Top rule:   %s\n", topRule)
	fmt.Fprintf(&b, "  Event ID:   %s\n", e.ID)
	fmt.Fprintf(&b, "  Action:     %s (applied=%v)\n", e.Decision.Action, e.Decision.Applied)
	b.WriteString("\n— cicy-code audit automated alert. AI suggestions are advisory; act per your enterprise SOP.\n")
	return subject, b.String()
}

// incidentCooldownTracker keeps a per-finding-hash dispatch timestamp.
// Process-local; restart resets so the next high-severity event after a
// restart always fires (intentional fresh-start behavior).
type incidentCooldownTracker struct {
	mu   sync.Mutex
	last map[string]int64
}

func newIncidentCooldownTracker() *incidentCooldownTracker {
	return &incidentCooldownTracker{last: map[string]int64{}}
}

func (c *incidentCooldownTracker) alreadyDispatched(hash string, window time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.last[hash]
	if !ok {
		return false
	}
	return time.Now().UnixNano()-last < window.Nanoseconds()
}

func (c *incidentCooldownTracker) markDispatched(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last[hash] = time.Now().UnixNano()
}
