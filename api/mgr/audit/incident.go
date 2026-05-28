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

// dispatchIncident is called by pipeline.process AFTER an event is appended.
// New architecture: the backend does NOT decide the response itself — it
// forwards the finding to the w-10000 audit advisor, which triages and
// orchestrates everything (notify the offending agent / escalate to the owner
// via cicy-policy notify / tune policy). The owner email is sent only when the
// advisor explicitly calls SendOwnerIncident (POST /api/audit/notify).
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
	if forwardFindingToAdvisor(e) {
		p.incidentCooldown.markDispatched(hash)
	}
}

// SendOwnerIncident renders and emails the incident to the responsible
// person(s). Invoked when the advisor (w-10000) escalates to a human via
// POST /api/audit/notify. `note` is the advisor's own assessment (e.g. "GitHub
// token leaked — revoke + rotate now"), prepended to the email body.
func (p *Pipeline) SendOwnerIncident(e Event, note string) error {
	pol := p.CurrentPolicy()
	cfg := pol.IncidentResponse
	ruleIDs := uniqueRuleIDs(e.Findings)
	recipients := pol.ResponsiblePersons.Resolve(
		topSeverity(e.Findings), e.Identity.AgentID, e.Identity.UserID, ruleIDs,
	)
	var ai *AIRemediation
	if cfg.AIRemediation.Enabled {
		if got, err := callAIRemediation(context.Background(), cfg.AIRemediation, e); err == nil {
			ai = got
		} else {
			log.Printf("[audit] ai_remediation skipped event=%s: %v", e.ID, err)
		}
	}
	ackURL := ""
	if token, signErr := SignAckToken(p.store.auditRoot, e.ID, AckTokenDefaultTTL); signErr == nil {
		ackURL = buildPublicURL("/api/audit/ack") + "?token=" + token
	} else {
		log.Printf("[audit] ack-token sign failed event=%s: %v", e.ID, signErr)
	}

	// Channel 1 (default): email to the responsible person(s).
	emailed := false
	var emailErr error
	if len(recipients) > 0 {
		subject, body := renderIncidentEmail(e, ruleIDs, cfg, ai, ackURL)
		if n := strings.TrimSpace(note); n != "" {
			body = "审计顾问 (w-10000) 研判:\n" + n + "\n\n" + body
		}
		if err := p.mailer.Send(EmailMessage{
			To:       recipients,
			Subject:  subject,
			Body:     body,
			EventID:  e.ID,
			AgentID:  e.Identity.AgentID,
			Severity: topSeverity(e.Findings),
		}); err != nil {
			emailErr = fmt.Errorf("send incident email: %w", err)
		} else {
			emailed = true
			log.Printf("[audit] owner incident dispatched (advisor-triggered) event=%s recipients=%v", e.ID, recipients)
		}
	} else {
		emailErr = fmt.Errorf("no responsible person resolved for event %s (configure policy.incident_response.responsible_persons)", e.ID)
	}

	// Channel 2 (additive, optional): WeChat to the bound account owner. Fires
	// alongside email — best-effort, never blocks the email outcome.
	imSent := false
	if imChannelBound() {
		if sent, err := notifyIMChannel(renderIMIncident(e, note, ackURL)); err != nil {
			log.Printf("[audit] IM(wechat) notify failed event=%s: %v", e.ID, err)
		} else if sent {
			imSent = true
			log.Printf("[audit] IM(wechat) notify sent event=%s", e.ID)
		}
	}

	// Success if any channel delivered. Only when nothing went out do we surface
	// the email-side reason (the default channel) to the caller.
	if !emailed && !imSent {
		return emailErr
	}
	return nil
}

// SendTestNotification sends a synthetic alert through the currently-active
// channels (email mailer + WeChat if bound) so an operator can verify delivery
// without triggering a real finding. Returns a human-readable summary of what
// was attempted; err is non-nil only when a configured channel actually failed.
func (p *Pipeline) SendTestNotification(to string) (string, error) {
	to = strings.TrimSpace(to)
	var results []string
	var lastErr error
	if to != "" {
		msg := EmailMessage{
			To:       []string{to},
			Subject:  "[CICY-AUDIT][TEST] 通知渠道连通性测试",
			Body:     "这是一封 cicy-code 审计「通知渠道」测试邮件。\n收到即说明邮件投递通道(" + responseMailerKind + ")工作正常。\n\n— cicy-code audit · w-10000",
			EventID:  fmt.Sprintf("test-%d", time.Now().Unix()),
			AgentID:  "w-10000",
			Severity: SeverityLow,
		}
		if err := p.mailer.Send(msg); err != nil {
			results = append(results, fmt.Sprintf("邮件(%s)→%s: 失败 %v", responseMailerKind, to, err))
			lastErr = err
		} else {
			results = append(results, fmt.Sprintf("邮件(%s)→%s: 已发", responseMailerKind, to))
		}
	} else {
		results = append(results, "邮件: 跳过(未给收件人 to)")
	}
	if imChannelBound() {
		if sent, err := notifyIMChannel("【审计通知测试】这是一条 cicy-code 审计「通知渠道」微信测试消息,收到即说明微信通道正常。"); err != nil {
			results = append(results, "微信: 失败 "+err.Error())
			lastErr = err
		} else if sent {
			results = append(results, "微信: 已发")
		}
	} else {
		results = append(results, "微信: 未绑定(跳过)")
	}
	return strings.Join(results, "\n"), lastErr
}

// SendTestNotificationGlobal runs SendTestNotification on the global pipeline.
func SendTestNotificationGlobal(to string) (string, error) {
	if globalPipeline == nil {
		return "", fmt.Errorf("audit pipeline not initialized")
	}
	return globalPipeline.SendTestNotification(to)
}

// SendOwnerIncidentByID loads an event and escalates it to its responsible
// person(s). Called by POST /api/audit/notify when the advisor escalates.
func SendOwnerIncidentByID(eventID, note string) error {
	if globalPipeline == nil {
		return fmt.Errorf("audit pipeline not initialized")
	}
	e, err := GetEventByID(eventID)
	if err != nil {
		return err
	}
	if e == nil {
		return fmt.Errorf("event %s not found", eventID)
	}
	return globalPipeline.SendOwnerIncident(*e, note)
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
