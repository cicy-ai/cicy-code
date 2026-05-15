package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Policy is the runtime audit configuration, loaded from
// ~/cicy-ai/audit/policy.json (or DefaultPolicy when the file is absent).
// Per docs/v1/audit-system-design.md §6.2.
type Policy struct {
	// Hash is the sha256 of the source JSON file. "sha256:DEFAULT" when no
	// policy.json exists. Each event stamps meta.policy_hash with this value
	// so future replays know which policy was in effect.
	Hash string `json:"-"`

	Version  int    `json:"version"`
	Enabled  bool   `json:"enabled"`
	FailMode string `json:"fail_mode"` // "open" | "closed"

	RulesOverride []RuleOverride `json:"rules_override"`
	CustomRules   []CustomRule   `json:"custom_rules"`
	AllowList     AllowList      `json:"allow_list"`

	// Notify drives noise governance (P2-T5) and the channel-delivery
	// pipeline (Phase 3). Defaults applied at load time.
	Notify NotifyConfig `json:"notify"`

	// Preventive controls inline (pre-LLM) blocking. Default off — operators
	// must explicitly enable. See Phase 3 cut 1.
	Preventive PreventiveConfig `json:"preventive"`

	// ResponsiblePersons maps an event to a deduplicated recipient list
	// (Phase 6). Resolution order: by_rule > by_user > by_agent
	// (wildcard-aware) > by_severity > default.
	ResponsiblePersons ResponsiblePersonsConfig `json:"responsible_persons"`

	// IncidentResponse gates the high/critical email dispatch (Phase 6).
	IncidentResponse IncidentResponseConfig `json:"incident_response"`

	// Phase 5 fields — parsed but ignored.
	Retention map[string]interface{} `json:"retention,omitempty"`
	AIAssist  map[string]interface{} `json:"ai_assist,omitempty"`
}

// ResponsiblePersonsConfig maps event identity dimensions to a list of
// email addresses. All matched lists are unioned and deduplicated.
//
// ByAgent keys support a single trailing "*" wildcard:
//
//	"w-100*" matches w-1000, w-10042, etc.
type ResponsiblePersonsConfig struct {
	Default    []string            `json:"default,omitempty"`
	BySeverity map[string][]string `json:"by_severity,omitempty"`
	ByAgent    map[string][]string `json:"by_agent,omitempty"`
	ByUser     map[string][]string `json:"by_user,omitempty"`
	ByRule     map[string][]string `json:"by_rule,omitempty"`
}

// IncidentResponseConfig controls the email dispatch pipeline.
//
//	Enabled              gate; default false.
//	TriggerMinSeverity   high (default) or critical.
//	CooldownSeconds      per finding-hash; default 1800 (30 min).
//	OutputDir            FileMailer write target; default
//	                     ~/cicy-ai/audit/email-out
//	EmailTemplate        "default" (cut 1) | "corp-template" (future).
//	Languages            ["zh-CN","en"] subjects/sections (cut 1 always
//	                     bilingual; field reserved for future per-recipient
//	                     localization).
type IncidentResponseConfig struct {
	Enabled            bool     `json:"enabled"`
	TriggerMinSeverity Severity `json:"trigger_min_severity,omitempty"`
	CooldownSeconds    int      `json:"cooldown_seconds,omitempty"`
	OutputDir          string   `json:"output_dir,omitempty"`
	EmailTemplate      string   `json:"email_template,omitempty"`
	Languages          []string `json:"languages,omitempty"`

	// EmailFrom is the From: address used by ResendMailer. Sourced from
	// (in order): this policy field > CICY_RESEND_FROM env > the
	// "from_address" field in ~/cicy-ai/db/email.json. When none resolve
	// to a non-empty string, the audit pipeline keeps FileMailer.
	EmailFrom string `json:"email_from,omitempty"`

	// AIRemediation controls the AI-generated summary + action plan
	// embedded in incident emails (Phase 6 cut 2b). Default disabled —
	// must be opted into AND the endpoint must be enterprise-self-hosted
	// to keep audit findings from leaking to external SaaS LLMs.
	AIRemediation AIRemediationConfig `json:"ai_remediation,omitempty"`
}

// AIRemediationConfig wires the incident-email AI summary feature.
//
// Hard rule (per design §9.6): the prompt body NEVER contains the
// original LLM payload. Only:
//   - severity / agent_id / agent_type / provider / model
//   - rule_id + masked preview text for each finding
//   - the action the audit pipeline took (block / redact / log)
//
// Endpoint MUST be an OpenAI-compatible /chat/completions URL pointing at
// an enterprise-trusted model. Default off; default timeout 10s; default
// max_tokens 600.
type AIRemediationConfig struct {
	Enabled        bool   `json:"enabled"`
	Endpoint       string `json:"endpoint,omitempty"`
	Model          string `json:"model,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	MaxTokens      int    `json:"max_tokens,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// DefaultIncidentResponseConfig returns the Phase 6 cut 1 defaults.
// Note: Enabled stays false; admins must opt in.
func DefaultIncidentResponseConfig() IncidentResponseConfig {
	return IncidentResponseConfig{
		Enabled:            false,
		TriggerMinSeverity: SeverityHigh,
		CooldownSeconds:    1800,
		EmailTemplate:      "default",
		Languages:          []string{"zh-CN", "en"},
	}
}

// Resolve gathers all recipients matching this event and returns them
// sorted + deduplicated. Returns empty when no rule matches (caller MAY
// fall back to Default — Resolve already includes Default as the last
// tier when nothing else matched).
func (r ResponsiblePersonsConfig) Resolve(severity Severity, agentID, userID string, ruleIDs []string) []string {
	set := map[string]struct{}{}
	addAll := func(list []string) {
		for _, addr := range list {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				set[addr] = struct{}{}
			}
		}
	}
	for _, rid := range ruleIDs {
		addAll(r.ByRule[rid])
	}
	if userID != "" {
		addAll(r.ByUser[userID])
	}
	if agentID != "" {
		for pattern, recipients := range r.ByAgent {
			if matchAgentPattern(pattern, agentID) {
				addAll(recipients)
			}
		}
	}
	if severity != "" {
		addAll(r.BySeverity[string(severity)])
	}
	if len(set) == 0 {
		addAll(r.Default)
	}
	out := make([]string, 0, len(set))
	for addr := range set {
		out = append(out, addr)
	}
	sortStrings(out)
	return out
}

// matchAgentPattern returns true if pattern matches agentID. Pattern
// supports exact match or a single trailing "*" for prefix glob.
func matchAgentPattern(pattern, agentID string) bool {
	if pattern == "" {
		return false
	}
	if pattern == agentID {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(agentID, pattern[:len(pattern)-1])
	}
	return false
}

// sortStrings: avoid pulling sort here just for one call site at top of
// the package. Tiny in-place insertion sort suffices for typical
// recipient counts (< 32).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// PreventiveConfig gates the inline scanner that runs BEFORE the request is
// forwarded to the LLM provider. When Enabled and an inline rule fires with
// a default action of block, the gateway / mitm webhook returns HTTP 451 and
// no data leaves the host. Default Enabled=false: cicy-code is detective-
// only out of the box; admins must opt in to preventive.
//
// FailMode mirrors Policy.FailMode but applies specifically to the inline
// scanner. "open" (default) — scanner errors pass-through; "closed" —
// scanner errors return 503 and block the request (compliance-strict mode).
type PreventiveConfig struct {
	Enabled  bool   `json:"enabled"`
	FailMode string `json:"fail_mode,omitempty"` // open | closed
}

// NotifyConfig controls when notify-level events trigger channel delivery
// (Phase 3) and how the noise-governance layer (P2-T5) suppresses repeats.
//
//   RateLimit  per (agent, rule_id) sliding window — caps how many notify
//              events fire within a window; over-cap events are suppressed
//              with notify_suppressed_by="rate_limit". Defaults: 50 per hour.
//   Cooldown   per finding-identity hash (agent + rule + preview) — once a
//              specific value is reported, the same value will not notify
//              again until the cooldown elapses. Default 24h.
//   Suspended  emergency switch (§17.4 design): all notifications turn into
//              notify_suppressed_by="suspended"; events still record.
type NotifyConfig struct {
	MinSeverity Severity                 `json:"min_severity,omitempty"`
	RateLimit   RateLimitConfig          `json:"rate_limit,omitempty"`
	Cooldown    CooldownConfig           `json:"cooldown,omitempty"`
	Channels    []map[string]interface{} `json:"channels,omitempty"`
	Suspended   bool                     `json:"suspended,omitempty"`
}

type RateLimitConfig struct {
	WindowSeconds      int `json:"window_seconds"`
	MaxPerAgentPerRule int `json:"max_per_agent_per_rule"`
}

type CooldownConfig struct {
	Seconds int `json:"seconds"`
}

// RuleOverride changes a builtin rule's runtime properties without altering
// the binary. Setting Disabled removes the rule from the active set;
// Severity / DefaultAction selectively shift its behavior.
type RuleOverride struct {
	ID            string   `json:"id"`
	Disabled      bool     `json:"disabled,omitempty"`
	Severity      Severity `json:"severity,omitempty"`
	DefaultAction Action   `json:"default_action,omitempty"`
}

// CustomRule is an enterprise-defined rule layered on top of the builtin set.
// IDs must use the "custom." prefix so they cannot collide with shipped rules.
type CustomRule struct {
	ID             string    `json:"id"`
	Label          string    `json:"label,omitempty"`
	Category       string    `json:"category,omitempty"`
	Severity       Severity  `json:"severity"`
	ScanDirections []string  `json:"scan_directions"`
	Inline         bool      `json:"inline,omitempty"`
	DefaultAction  Action    `json:"default_action,omitempty"`
	Match          RuleMatch `json:"match"`
}

// RuleMatch is the matcher spec for a CustomRule. Phase 2 supports:
//   - type=regex   pattern is an RE2 expression; flags is the optional Go
//                  regex flag block (i/m/s/U) prepended as "(?<flags>)".
//   - type=dict_file  path is a UTF-8 file with one term per line; lines
//                     starting with # are comments, empty lines are skipped.
type RuleMatch struct {
	Type    string `json:"type"`
	Pattern string `json:"pattern,omitempty"`
	Flags   string `json:"flags,omitempty"`
	Path    string `json:"path,omitempty"`
}

// AllowList suppresses findings (but never the event itself) when the
// originating context matches one of the listed criteria.
//
//	Agents         exact match against Identity.AgentID
//	Paths          exact match against Subject.PayloadRef
//	ContentHashes  exact match against Subject.PayloadSHA256
//	               (intended for one-off false-positive content snapshots)
type AllowList struct {
	Paths         []string `json:"paths"`
	ContentHashes []string `json:"content_hashes"`
	Agents        []string `json:"agents"`
}

// DefaultPolicy returns the policy used when no policy.json is present.
// Enabled, fail-open, no overrides, no custom rules, empty allow list,
// default notify thresholds (50/hour per (agent, rule), 24h cooldown).
func DefaultPolicy() *Policy {
	return &Policy{
		Hash:     "sha256:DEFAULT",
		Version:  1,
		Enabled:  true,
		FailMode: "open",
		AllowList: AllowList{
			Paths:         []string{},
			ContentHashes: []string{},
			Agents:        []string{},
		},
		Notify:           DefaultNotifyConfig(),
		Preventive:       PreventiveConfig{Enabled: false, FailMode: "open"},
		IncidentResponse: DefaultIncidentResponseConfig(),
	}
}

// DefaultNotifyConfig returns conservative defaults that won't drown a fresh
// install in alerts but aren't so loose they hide real signal.
func DefaultNotifyConfig() NotifyConfig {
	return NotifyConfig{
		MinSeverity: SeverityMedium,
		RateLimit: RateLimitConfig{
			WindowSeconds:      3600,
			MaxPerAgentPerRule: 50,
		},
		Cooldown: CooldownConfig{
			Seconds: 86400,
		},
	}
}

// LoadPolicy reads and validates ~/cicy-ai/audit/policy.json. Returns
// DefaultPolicy() if the file does not exist. On parse / validation error,
// returns the error and the caller should keep the previously-loaded policy
// (the audit must never run without *some* policy).
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultPolicy(), nil
		}
		return nil, err
	}
	p := DefaultPolicy()
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("audit: parse policy.json: %w", err)
	}
	sum := sha256.Sum256(data)
	p.Hash = "sha256:" + hex.EncodeToString(sum[:])
	if err := validatePolicy(p); err != nil {
		return nil, err
	}
	if p.AllowList.Paths == nil {
		p.AllowList.Paths = []string{}
	}
	if p.AllowList.ContentHashes == nil {
		p.AllowList.ContentHashes = []string{}
	}
	if p.AllowList.Agents == nil {
		p.AllowList.Agents = []string{}
	}
	// Fill in notify defaults when only a partial block was provided.
	def := DefaultNotifyConfig()
	if p.Notify.MinSeverity == "" {
		p.Notify.MinSeverity = def.MinSeverity
	}
	if p.Notify.RateLimit.WindowSeconds == 0 {
		p.Notify.RateLimit.WindowSeconds = def.RateLimit.WindowSeconds
	}
	if p.Notify.RateLimit.MaxPerAgentPerRule == 0 {
		p.Notify.RateLimit.MaxPerAgentPerRule = def.RateLimit.MaxPerAgentPerRule
	}
	if p.Notify.Cooldown.Seconds == 0 {
		p.Notify.Cooldown.Seconds = def.Cooldown.Seconds
	}
	if p.Preventive.FailMode == "" {
		p.Preventive.FailMode = "open"
	}
	if p.Preventive.FailMode != "open" && p.Preventive.FailMode != "closed" {
		return nil, fmt.Errorf("audit: preventive.fail_mode invalid %q (want open|closed)", p.Preventive.FailMode)
	}
	// Incident-response defaults: never auto-enable, but keep numeric
	// defaults sane if the operator did enable it with partial config.
	ir := DefaultIncidentResponseConfig()
	if p.IncidentResponse.TriggerMinSeverity == "" {
		p.IncidentResponse.TriggerMinSeverity = ir.TriggerMinSeverity
	}
	if !validSeverity(p.IncidentResponse.TriggerMinSeverity) {
		return nil, fmt.Errorf("audit: incident_response.trigger_min_severity invalid %q", p.IncidentResponse.TriggerMinSeverity)
	}
	if p.IncidentResponse.CooldownSeconds == 0 {
		p.IncidentResponse.CooldownSeconds = ir.CooldownSeconds
	}
	if p.IncidentResponse.EmailTemplate == "" {
		p.IncidentResponse.EmailTemplate = ir.EmailTemplate
	}
	if len(p.IncidentResponse.Languages) == 0 {
		p.IncidentResponse.Languages = ir.Languages
	}
	return p, nil
}

// validatePolicy enforces v2 invariants. Caller MUST NOT activate an invalid
// policy: pipeline integrity beats a clever-but-broken config.
func validatePolicy(p *Policy) error {
	if p == nil {
		return fmt.Errorf("audit: nil policy")
	}
	switch p.FailMode {
	case "", "open", "closed":
	default:
		return fmt.Errorf("audit: invalid fail_mode %q (want open|closed)", p.FailMode)
	}
	if p.FailMode == "" {
		p.FailMode = "open"
	}

	builtinIDs := map[string]bool{}
	for _, r := range BuiltinRules() {
		builtinIDs[r.ID] = true
	}
	for i, o := range p.RulesOverride {
		if o.ID == "" {
			return fmt.Errorf("audit: rules_override[%d]: empty id", i)
		}
		if !builtinIDs[o.ID] {
			return fmt.Errorf("audit: rules_override[%d]: unknown builtin rule id %q", i, o.ID)
		}
		if o.Severity != "" && !validSeverity(o.Severity) {
			return fmt.Errorf("audit: rules_override[%d]: invalid severity %q", i, o.Severity)
		}
		if o.DefaultAction != "" && !validAction(o.DefaultAction) {
			return fmt.Errorf("audit: rules_override[%d]: invalid action %q", i, o.DefaultAction)
		}
	}
	for i, c := range p.CustomRules {
		if !strings.HasPrefix(c.ID, "custom.") {
			return fmt.Errorf("audit: custom_rules[%d]: id %q must start with \"custom.\"", i, c.ID)
		}
		if !validSeverity(c.Severity) {
			return fmt.Errorf("audit: custom_rules[%d %s]: invalid severity %q", i, c.ID, c.Severity)
		}
		if c.DefaultAction != "" && !validAction(c.DefaultAction) {
			return fmt.Errorf("audit: custom_rules[%d %s]: invalid default_action %q", i, c.ID, c.DefaultAction)
		}
		if len(c.ScanDirections) == 0 {
			return fmt.Errorf("audit: custom_rules[%d %s]: scan_directions must list at least one of outbound/inbound", i, c.ID)
		}
		for _, d := range c.ScanDirections {
			if d != DirectionOutbound && d != DirectionInbound {
				return fmt.Errorf("audit: custom_rules[%d %s]: invalid scan_direction %q", i, c.ID, d)
			}
		}
		switch c.Match.Type {
		case "regex":
			pattern := c.Match.Pattern
			if c.Match.Flags != "" {
				pattern = "(?" + c.Match.Flags + ")" + pattern
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("audit: custom_rules[%d %s]: regex compile: %w", i, c.ID, err)
			}
		case "dict_file":
			if strings.TrimSpace(c.Match.Path) == "" {
				return fmt.Errorf("audit: custom_rules[%d %s]: dict_file path required", i, c.ID)
			}
		default:
			return fmt.Errorf("audit: custom_rules[%d %s]: unknown match.type %q", i, c.ID, c.Match.Type)
		}
	}
	return nil
}

func validSeverity(s Severity) bool {
	switch s {
	case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	}
	return false
}

func validAction(a Action) bool {
	switch a {
	case ActionLog, ActionNotify, ActionRedact, ActionBlock, ActionNone:
		return true
	}
	return false
}

// AllowlistDecision tells the pipeline which (if any) allowlist entry
// suppresses findings for this event. Empty Reason means "no suppression".
type AllowlistDecision struct {
	Suppressed bool
	Reason     string // "agent" | "path" | "content_hash"
	Match      string // the value that matched (echoed back for forensics)
}

// CheckAllowList applies the policy's allow list. Returns the first match.
// Order: agent > path > content_hash (most specific first).
func (p *Policy) CheckAllowList(agentID, payloadRef, payloadSHA256 string) AllowlistDecision {
	if p == nil {
		return AllowlistDecision{}
	}
	for _, a := range p.AllowList.Agents {
		if a == agentID && a != "" {
			return AllowlistDecision{Suppressed: true, Reason: "agent", Match: a}
		}
	}
	for _, path := range p.AllowList.Paths {
		if path == "" {
			continue
		}
		if path == payloadRef || strings.HasPrefix(payloadRef, path) {
			return AllowlistDecision{Suppressed: true, Reason: "path", Match: path}
		}
	}
	for _, h := range p.AllowList.ContentHashes {
		if h == payloadSHA256 && h != "" {
			return AllowlistDecision{Suppressed: true, Reason: "content_hash", Match: h}
		}
	}
	return AllowlistDecision{}
}

// policyWriteMu serializes read-modify-write to policy.json. The audit
// pipeline accepts the file as the single source of truth; we never
// mutate the in-memory *Policy directly — every change goes through the
// file so fsnotify reload picks it up uniformly.
var policyWriteMu sync.Mutex

// DefaultPolicyPath returns the conventional policy.json location used by
// Init. Exposed for tools and HTTP handlers that mutate the policy.
func DefaultPolicyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "cicy-ai", "audit", "policy.json")
}

// AllowListCategory restricts the public AddToAllowList API to the three
// supported allow_list sub-arrays. Spelled out as constants so handlers
// don't pass raw JSON keys.
type AllowListCategory string

const (
	AllowCategoryContentHash AllowListCategory = "content_hash"
	AllowCategoryAgent       AllowListCategory = "agent"
	AllowCategoryPath        AllowListCategory = "path"
)

// AddToAllowList atomically appends value to the named allow_list bucket
// in policy.json. Idempotent: a value that is already present is a no-op
// (no error, file not rewritten). Returns the *policy.json path written
// when a change occurred, or "" when nothing changed.
//
// The implementation uses map[string]interface{} so unknown / future
// policy fields are preserved verbatim — operators can edit by hand AND
// via the API without one clobbering the other.
func AddToAllowList(category AllowListCategory, value, reason string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("audit: allow_list value empty")
	}
	var key string
	switch category {
	case AllowCategoryContentHash:
		key = "content_hashes"
	case AllowCategoryAgent:
		key = "agents"
	case AllowCategoryPath:
		key = "paths"
	default:
		return "", fmt.Errorf("audit: unknown allow_list category %q", category)
	}

	path := DefaultPolicyPath()
	if path == "" {
		return "", fmt.Errorf("audit: cannot resolve policy.json path")
	}

	policyWriteMu.Lock()
	defer policyWriteMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}

	raw := map[string]interface{}{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	allowList, _ := raw["allow_list"].(map[string]interface{})
	if allowList == nil {
		allowList = map[string]interface{}{}
	}

	existing, _ := allowList[key].([]interface{})
	for _, e := range existing {
		if s, ok := e.(string); ok && s == value {
			return "", nil // already present, idempotent no-op
		}
	}
	existing = append(existing, value)
	allowList[key] = existing
	raw["allow_list"] = allowList

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return "", err
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	_ = reason // recorded by the caller (log line + meta-audit event in P5)
	return path, nil
}

// expandUserPath resolves a leading "~/" to the current user's home directory.
// Falls back to the input unchanged if HOME is unresolvable.
func expandUserPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
