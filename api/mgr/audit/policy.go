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

	// Phase 3/5 fields — parsed but ignored.
	Retention          map[string]interface{} `json:"retention,omitempty"`
	ResponsiblePersons map[string]interface{} `json:"responsible_persons,omitempty"`
	IncidentResponse   map[string]interface{} `json:"incident_response,omitempty"`
	AIAssist           map[string]interface{} `json:"ai_assist,omitempty"`
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
		Notify:     DefaultNotifyConfig(),
		Preventive: PreventiveConfig{Enabled: false, FailMode: "open"},
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
