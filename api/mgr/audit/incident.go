package audit

import (
	"context"
	"fmt"
	"log"
	"os"
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

	// Phase 6 cut 2b: best-effort AI remediation. Skipped silently when
	// disabled or on any failure (timeout / HTTP error / parse error) —
	// the email still ships with the placeholder section in that case so
	// recipients are never blocked by AI flakiness.
	var ai *AIRemediation
	if cfg.AIRemediation.Enabled {
		got, err := callAIRemediation(context.Background(), cfg.AIRemediation, e)
		if err != nil {
			log.Printf("[audit] ai_remediation skipped event=%s: %v", e.ID, err)
		} else {
			ai = got
			log.Printf("[audit] ai_remediation generated event=%s immediate_actions=%d",
				e.ID, len(ai.ImmediateActions))
		}
	}

	// Phase 6 cut 2c: sign a per-event ack URL recipients can click to
	// record acknowledgement. Best-effort: if signing fails the email
	// still ships, just without the ack link.
	ackURL := ""
	if token, signErr := SignAckToken(p.store.auditRoot, e.ID, AckTokenDefaultTTL); signErr == nil {
		ackURL = buildPublicURL("/api/audit/ack") + "?token=" + token
	} else {
		log.Printf("[audit] ack-token sign failed event=%s: %v", e.ID, signErr)
	}

	subject, body := renderIncidentEmail(e, ruleIDs, cfg, ai, ackURL)
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
// body. When ai is non-nil, its fields replace the placeholder section;
// otherwise the default placeholder is shown. ackURL, when non-empty,
// is rendered into the "查阅 / Confirm" section as a clickable URL.
func renderIncidentEmail(e Event, ruleIDs []string, cfg IncidentResponseConfig, ai *AIRemediation, ackURL string) (subject, body string) {
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
	if ai != nil && (ai.Summary != "" || ai.SeverityExplain != "") {
		if ai.Summary != "" {
			fmt.Fprintf(&b, "%s\n", ai.Summary)
		}
		if ai.SeverityExplain != "" {
			fmt.Fprintf(&b, "\n为什么是 %s:\n%s\n", strings.ToUpper(string(top)), ai.SeverityExplain)
		}
	} else {
		b.WriteString("(AI 辅助未启用或不可用 — 配置 policy.incident_response.ai_remediation 启用)\n")
	}

	b.WriteString("\n──────── 立即处置 ────────\n")
	if ai != nil && len(ai.ImmediateActions) > 0 {
		for _, a := range ai.ImmediateActions {
			fmt.Fprintf(&b, "  □ %s\n", a)
		}
	} else {
		b.WriteString("  □ 登录 dashboard 复核事件真实性\n")
		b.WriteString("  □ 如确认泄露,立即吊销凭据并断开 agent\n")
		b.WriteString("  □ 完成处置后到 dashboard 上 ack 该告警\n")
	}

	if ai != nil && len(ai.LongerTerm) > 0 {
		b.WriteString("\n──────── 后续加固 ────────\n")
		for _, a := range ai.LongerTerm {
			fmt.Fprintf(&b, "  • %s\n", a)
		}
	}

	// Ack link
	if ackURL != "" {
		b.WriteString("\n──────── 确认 / Acknowledge ────────\n")
		fmt.Fprintf(&b, "  完成处置后请点击以下链接关闭告警(30 天内有效):\n  %s\n", ackURL)
	}

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

// buildPublicURL composes a public-facing URL for outbound links. Tries
// CICY_PUBLIC_URL env first; falls back to http://localhost:8008 (the
// in-container default API port). Path is appended verbatim.
func buildPublicURL(path string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("CICY_PUBLIC_URL")), "/")
	if base == "" {
		base = "http://localhost:8008"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
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
