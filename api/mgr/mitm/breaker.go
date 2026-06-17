package mitm

// BreakerHook is the contract between the MITM pump and the audit
// preventive layer. Package main provides an implementation that wraps
// audit.PreventiveCheck — see api/mgr/mitm_breaker_adapter.go.
//
// The hook runs after StartTurn (so the audit pipeline has already
// recorded the outbound payload via current.json) but BEFORE dialing the
// upstream. Block decisions short-circuit the upstream call. (redact removed
// 2026-06-16: the breaker never rewrites a payload — only pass or block.)
type BreakerHook interface {
	Check(req BreakerRequest) BreakerDecision
}

// BreakerRequest carries everything PreventiveCheck needs from MITM.
type BreakerRequest struct {
	AgentID    string
	TurnID     string
	Provider   string
	Model      string
	Host       string // upstream host (api.anthropic.com)
	Direction  string // always "outbound" for now
	PayloadRef string // e.g. "current.json#<turn_id>"
	Payload    []byte // request body
}

// BreakerAction enumerates the outcomes the pump can act on.
type BreakerAction string

const (
	BreakerActionPass  BreakerAction = "pass"
	BreakerActionBlock BreakerAction = "block"
)

// BreakerDecision is the result of Check.
type BreakerDecision struct {
	Action BreakerAction

	// Reason is forwarded into the synthetic error message and audit log.
	// Example: "secret.private_key matched (default block)".
	Reason string

	// RuleID identifies the rule that fired (for X-Cicy-Mitm-Block-Rule
	// and audit envelope). May be empty when Reason is generic.
	RuleID string

	// EventID / Rules / Message carry the UNIFIED audit-block contract so the
	// MITM block response is byte-identical to the gateway's writeGatewayBlocked
	// (403 + X-Cicy-Audit-Blocked/Rules + body.message). Populated by the breaker
	// adapter from audit.PreventiveDecision; empty on pass/redact.
	EventID string   // → X-Cicy-Audit-Blocked
	Rules   []string // → X-Cicy-Audit-Rules + body.rules
	Message string   // → body.message (built via main.auditBlockMessage)
}

// noopBreakerHook permits the pump to use a single code path when no
// breaker is configured (defensive — production wiring always installs
// one, even if all rules are disabled).
type noopBreakerHook struct{}

func (noopBreakerHook) Check(BreakerRequest) BreakerDecision {
	return BreakerDecision{Action: BreakerActionPass, Reason: "no_breaker"}
}
