// Package audit provides cicy-code's enterprise audit pipeline.
//
// Design reference: docs/v1/audit-system-design.md.
//
// This file defines the canonical wire types. Field order MUST be stable —
// it determines canonical JSON output and therefore the hash chain. Do not
// reorder fields without bumping SchemaVersion.
package audit

const SchemaVersion = "1"

const (
	DirectionOutbound = "outbound"
	DirectionInbound  = "inbound"
)

const (
	SourceGateway = "gateway"
	SourceMitm    = "mitm"
)

type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type Action string

const (
	ActionLog    Action = "log"
	ActionNotify Action = "notify"
	ActionRedact Action = "redact"
	ActionBlock  Action = "block"
	ActionNone   Action = "none"
)

type FailMode string

const (
	FailOpen   FailMode = "open"
	FailClosed FailMode = "closed"
)

// Event is the canonical audit record. Every field is required: empty string,
// zero, or empty slice — never absent — so canonical JSON stays deterministic.
type Event struct {
	ID            string `json:"id"`
	SchemaVersion string `json:"schema_version"`
	RulesVersion  string `json:"rules_version"`

	Timestamp   string `json:"ts"`
	TsMonotonic int64  `json:"ts_monotonic"`

	PrevHash string `json:"prev_hash"`
	SelfHash string `json:"self_hash"`

	Identity Identity  `json:"identity"`
	Subject  Subject   `json:"subject"`
	Findings []Finding `json:"findings"`
	Decision Decision  `json:"decision"`
	Meta     Meta      `json:"meta"`
}

type Identity struct {
	MachineID     string `json:"machine_id"`
	AgentID       string `json:"agent_id"`
	AgentType     string `json:"agent_type"`
	UserID        string `json:"user_id"`
	SessionID     string `json:"session_id"`
	SourceChannel string `json:"source_channel"`
}

type Subject struct {
	TurnID         string `json:"turn_id"`
	ConversationID string `json:"conversation_id"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	Direction      string `json:"direction"`
	PayloadSize    int    `json:"payload_size"`
	PayloadRef     string `json:"payload_ref"`
	PayloadSHA256  string `json:"payload_sha256"`
}

type Finding struct {
	RuleID      string   `json:"rule_id"`
	RuleVersion string   `json:"rule_version"`
	Severity    Severity `json:"severity"`
	Category    string   `json:"category"`
	MatchCount  int      `json:"match_count"`
	Spans       []Span   `json:"spans"`
}

type Span struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Preview string `json:"preview"`
}

type Decision struct {
	EvaluatedInline bool     `json:"evaluated_inline"`
	EvaluatedAsync  bool     `json:"evaluated_async"`
	Action          Action   `json:"action"`
	Applied         bool     `json:"applied"`
	FailMode        FailMode `json:"fail_mode"`
}

type Meta struct {
	ScannerDurationMs int    `json:"scanner_duration_ms"`
	PipelineError     string `json:"pipeline_error"`
	PolicyHash        string `json:"policy_hash"`

	// AllowlistedBy is non-empty when the event was suppressed by an entry in
	// policy.allow_list. Findings are still empty in this case (the event is
	// recorded for completeness, but the scanner output is not). Values:
	// "agent" | "path" | "content_hash". omitempty keeps Phase-1 events
	// canonical-hash compatible.
	AllowlistedBy  string `json:"allowlisted_by,omitempty"`
	AllowlistMatch string `json:"allowlist_match,omitempty"`
}

// Envelope is the caller-supplied input to Pipeline.Submit. The pipeline
// fills in everything else (id, hashes, timestamps, machine_id, ...).
type Envelope struct {
	AgentID       string
	AgentType     string
	UserID        string
	SessionID     string
	SourceChannel string

	TurnID         string
	ConversationID string
	Provider       string
	Model          string
	Direction      string

	Payload    []byte
	PayloadRef string

	Inline bool

	// submitWallNs / submitMonoNs are populated by Pipeline.Submit at the
	// moment the caller hands off, so the event timestamp reflects when the
	// event actually happened (caller's view) rather than when the async
	// pipeline goroutine got around to processing it. Forensic accuracy
	// requires fixing time at Submit, not process.
	submitWallNs int64
	submitMonoNs int64
}
