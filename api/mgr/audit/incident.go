package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dispatchIncident is called by pipeline.process AFTER an event is appended.
// audit-v2 architecture: on a qualifying hit the backend does TWO things,
// in parallel intent, then records the cooldown:
//
//	① SMTP alert — auto-emails the responsible person(s) via the active mailer
//	   (SmtpMailer), no longer waiting for an agent to trigger it.
//	② 审计策略专员 — forwards the finding brief to the live 审计策略专员 (the
//	   user's audit advisor) cicy agent for verification / triage / grading.
//
// Cooldown is marked if EITHER channel fired, so a noisy finding doesn't spam.
// Best-effort: any failure logs but does not propagate to the caller path.
// dispatchIncident attempts the owner SMTP/email alert ONLY (no agent forward —
// forwarding to the 审计策略专员 agent made it run an LLM per hit and burned real
// provider balance) and returns a human-readable status recorded on the event
// (e.Meta.AlertStatus). Runs synchronously in the async audit worker (off the
// agent hot path) so the REAL send result is captured before the event persists.
// defaultIncidentCooldown dedups repeat alerts for the same finding identity
// (agent + rule + value). The separate "事件响应" config was removed — alerting
// is driven purely by a rule's 告警 action + a deliverable channel (SMTP +
// responsible person), so this fixed window is the only knob.
const defaultIncidentCooldown = 30 * time.Minute

func (p *Pipeline) dispatchIncident(e Event) string {
	if responseMailerKind != "smtp" && responseMailerKind != "gmail" {
		return "未发送:未配置邮件通道"
	}
	pol := p.CurrentPolicy()
	if !hasResponsible(pol) {
		return "未发送:未设置责任人"
	}
	hash := EventFindingHash(e)
	if hash == "" {
		return "未发送:无指纹"
	}
	if p.incidentCooldown.alreadyDispatched(hash, defaultIncidentCooldown) {
		return "未发送:冷却中"
	}
	// SMTP/email alert to the responsible person(s) ONLY. NEVER forward to an
	// agent — that costs an LLM call per hit.
	if err := p.SendOwnerIncident(e, ""); err != nil {
		log.Printf("[audit] owner alert failed event=%s: %v", e.ID, err)
		return "发送失败:" + firstLineOf(err.Error())
	}
	p.incidentCooldown.markDispatched(hash)
	return "已发送(" + responseMailerKind + ")"
}

// hasResponsible reports whether the policy names at least one responsible
// person to receive alerts (any of default / by_* lists non-empty).
func hasResponsible(pol *Policy) bool {
	if pol == nil {
		return false
	}
	rp := pol.ResponsiblePersons
	if len(rp.Default) > 0 {
		return true
	}
	for _, m := range []map[string][]string{rp.BySeverity, rp.ByAgent, rp.ByUser, rp.ByRule} {
		for _, v := range m {
			if len(v) > 0 {
				return true
			}
		}
	}
	return false
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// SendOwnerIncident renders and emails the incident to the responsible
// person(s). Invoked automatically by dispatchIncident on a qualifying hit
// (note=""), and also by POST /api/audit/notify when the 审计策略专员 escalates
// with its own assessment. `note`, when set, is prepended to the email body.
func (p *Pipeline) SendOwnerIncident(e Event, note string) error {
	pol := p.CurrentPolicy()
	cfg := pol.IncidentResponse
	ruleIDs := uniqueRuleIDs(e.Findings)
	recipients := pol.ResponsiblePersons.Resolve(
		topSeverity(e.Findings), e.Identity.AgentID, e.Identity.UserID, ruleIDs,
	)
	// Ack link ONLY when a public URL is configured — a localhost ack link is
	// useless in an email (points at the recipient's own machine). No public
	// URL → no 确认告警 section at all.
	ackURL := ""
	if publicBaseURL() != "" {
		if token, signErr := SignAckToken(p.store.auditRoot, e.ID, AckTokenDefaultTTL); signErr == nil {
			ackURL = buildPublicURL("/api/audit/ack") + "?token=" + token
		} else {
			log.Printf("[audit] ack-token sign failed event=%s: %v", e.ID, signErr)
		}
	}

	// Channel 1 (default): email to the responsible person(s).
	emailed := false
	var emailErr error
	if len(recipients) > 0 {
		subject, body := renderIncidentEmail(e, ruleIDs, cfg, ackURL)
		if n := strings.TrimSpace(note); n != "" {
			body = "审计顾问研判:\n" + n + "\n\n" + body
		}
		if err := p.mailer.Send(EmailMessage{
			To:       recipients,
			Subject:  subject,
			Body:     body,
			HTMLBody: renderIncidentEmailHTML(e, ruleIDs, ackURL),
			EventID:  e.ID,
			AgentID:  e.Identity.AgentID,
			Severity: topSeverity(e.Findings),
		}); err != nil {
			emailErr = fmt.Errorf("send incident email: %w", err)
		} else {
			emailed = true
			log.Printf("[audit] owner incident dispatched event=%s recipients=%v", e.ID, recipients)
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

	// 2.1.8: Channel 3 (escalate to w-6001 security officer) removed — the audit
	// advisor (w-6001) now owns human coordination itself, so the cross-agent
	// hop is redundant. SendOwnerIncident remains additive across email + WeChat.

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
			Body:     "这是一封 cicy-code 审计「通知渠道」测试邮件。\n收到即说明邮件投递通道(" + responseMailerKind + ")工作正常。\n\n— cicy-code audit",
			EventID:  fmt.Sprintf("test-%d", time.Now().Unix()),
			AgentID:  "audit",
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
func renderIncidentEmail(e Event, ruleIDs []string, cfg IncidentResponseConfig, ackURL string) (subject, body string) {
	top := topSeverity(e.Findings)
	topRule := ""
	if len(ruleIDs) > 0 {
		topRule = ruleIDs[0]
	}
	subject = fmt.Sprintf("[CICY-AUDIT][%s] %s — %s",
		strings.ToUpper(string(top)), topRule, e.Identity.AgentID)

	var b strings.Builder
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
	if e.Subject.Direction != "" {
		fmt.Fprintf(&b, "方向: %s\n", e.Subject.Direction)
	}
	fmt.Fprintf(&b, "命中动作: %s\n", e.Decision.Action)
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

	if ackURL != "" {
		b.WriteString("\n──────── 确认告警 ────────\n")
		fmt.Fprintf(&b, "  处置后点此关闭告警(30 天内有效):\n  %s\n", ackURL)
	}

	b.WriteString("\n— cicy-code 审计自动告警\n")
	return subject, b.String()
}

// publicBaseURL returns the deployment's reachable public origin as configured
// in the settings UI (data-id="settings-public-url-input" → persisted to
// ~/cicy-ai/global.json "public_url"). NOT from any env var. Empty when unset —
// callers then omit public links entirely (a localhost link is useless in an
// email). Trailing slash trimmed.
func publicBaseURL() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, "cicy-ai", "global.json"))
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	s, _ := m["public_url"].(string)
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// buildPublicURL composes an externally-reachable link from the configured
// public URL. Returns "" when no public URL is set (no localhost fallback).
func buildPublicURL(path string) string {
	base := publicBaseURL()
	if base == "" {
		return ""
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
