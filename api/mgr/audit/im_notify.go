package audit

import "strings"

// imNotifier delivers an audit alert through a bound IM channel (WeChat).
// Injected by the main package, which owns the IM send path. nil = no IM
// channel wired. Returns (delivered, err) — additive to email, never primary.
var imNotifier func(text string) (bool, error)

// imBoundCheck reports whether an IM channel is currently bound/connected.
// Injected by the main package; nil = treat as unbound.
var imBoundCheck func() bool

// SetIMNotifier wires the "push an alert to the bound IM channel" path.
func SetIMNotifier(fn func(text string) (bool, error)) { imNotifier = fn }

// SetIMBoundCheck wires the "is an IM channel connected" probe.
func SetIMBoundCheck(fn func() bool) { imBoundCheck = fn }

func imChannelBound() bool { return imBoundCheck != nil && imBoundCheck() }

// securityOfficerNotifier escalates an incident to the security-officer agent
// (w-1000). Injected by the main package (sendTextToPane → w-1000). nil = off.
// This fires alongside email + WeChat — the security officer is an agent, the
// email/WeChat reach the human responsible persons.
var securityOfficerNotifier func(text string) error

// SetSecurityOfficerNotifier wires the "escalate to the w-1000 security officer" path.
func SetSecurityOfficerNotifier(fn func(text string) error) { securityOfficerNotifier = fn }

func notifySecurityOfficer(text string) (bool, error) {
	if securityOfficerNotifier == nil {
		return false, nil
	}
	return true, securityOfficerNotifier(text)
}

// renderSecurityOfficerEscalation is what the w-1000 security-officer agent
// receives. Marks it as a take-ownership escalation, then the standard brief.
func renderSecurityOfficerEscalation(e Event, note, ackURL string) string {
	return "[w-10000] 安全事件升级 · 你是安全员(w-1000),请接管处置 / Security escalation — own this incident\n" +
		renderIMIncident(e, note, ackURL)
}

func notifyIMChannel(text string) (bool, error) {
	if imNotifier == nil {
		return false, nil
	}
	return imNotifier(text)
}

// renderIMIncident is the concise IM (WeChat) form of an incident — short and
// glanceable, unlike the full bilingual email. Severity-tagged header, masked
// findings, optional advisor note, and the ack link.
func renderIMIncident(e Event, note, ackURL string) string {
	var b strings.Builder
	b.WriteString("【审计告警 · " + strings.ToUpper(string(topSeverity(e.Findings))) + "】\n")
	b.WriteString("agent: " + strings.TrimSpace(e.Identity.AgentID))
	if e.Identity.AgentType != "" {
		b.WriteString(" (" + e.Identity.AgentType + ")")
	}
	b.WriteString("\n")
	for _, f := range e.Findings {
		preview := ""
		if len(f.Spans) > 0 {
			preview = f.Spans[0].Preview
		}
		b.WriteString("• " + f.RuleID + " [" + string(f.Severity) + "] " + preview + "\n")
	}
	if n := strings.TrimSpace(note); n != "" {
		b.WriteString("研判: " + n + "\n")
	}
	if ackURL != "" {
		b.WriteString("处置后确认: " + ackURL)
	}
	return strings.TrimRight(b.String(), "\n")
}
