package audit

import "time"

// Scanner produces findings for one payload.
//
// Walking skeleton ships NoopScanner; P1-T6 introduces BuiltinScanner with
// 10 stock rules per docs/v1/audit-system-design.md §6.3. Phase 2 layers
// custom rules / dictionaries / overrides via policy.json on top of the
// builtin set.
type Scanner interface {
	Scan(payload []byte, direction string, policy *Policy) []Finding
}

// NoopScanner is the empty-result implementation, kept for tests and for the
// "audit disabled" mode where we want events to land but no rules to run.
type NoopScanner struct{}

func (NoopScanner) Scan(payload []byte, direction string, policy *Policy) []Finding {
	_ = payload
	_ = direction
	_ = policy
	return []Finding{}
}

// BuiltinScanner runs the v1 stock rule set against a payload.
type BuiltinScanner struct {
	rules []BuiltinRule
}

func NewBuiltinScanner() *BuiltinScanner {
	return &BuiltinScanner{rules: BuiltinRules()}
}

func (s *BuiltinScanner) Scan(payload []byte, direction string, policy *Policy) []Finding {
	_ = policy
	if len(payload) == 0 || direction == "" {
		return []Finding{}
	}
	out := make([]Finding, 0, 4)
	for _, rule := range s.rules {
		if !directionMatches(rule.ScanDirections, direction) {
			continue
		}
		spans := rule.Detect(payload)
		if len(spans) == 0 {
			continue
		}
		out = append(out, Finding{
			RuleID:      rule.ID,
			RuleVersion: RulesVersion,
			Severity:    rule.Severity,
			Category:    rule.Category,
			MatchCount:  len(spans),
			Spans:       spans,
		})
	}
	return out
}

func directionMatches(allowed []string, direction string) bool {
	for _, d := range allowed {
		if d == direction {
			return true
		}
	}
	return false
}

// elapsedMs returns the milliseconds since start, clamped to int.
func elapsedMs(start time.Time) int {
	return int(time.Since(start) / time.Millisecond)
}
