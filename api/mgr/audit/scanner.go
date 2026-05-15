package audit

import (
	"sync"
	"time"
)

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

// BuiltinScanner runs the effective rule set (builtin merged with policy
// overrides and custom rules) against a payload. The active RuleSet is
// swapped atomically on every policy reload — Scan() readers see either
// the pre-reload or post-reload set, never a half-built one.
type BuiltinScanner struct {
	builtin []BuiltinRule

	mu  sync.RWMutex
	set *RuleSet
}

// NewBuiltinScanner constructs a scanner with the builtin rules loaded and
// the default (no-override, no-custom) policy applied. Use SetPolicy to
// install a parsed policy.json.
func NewBuiltinScanner() *BuiltinScanner {
	builtin := BuiltinRules()
	set, _ := BuildRuleSet(builtin, DefaultPolicy()) // cannot fail without custom rules
	return &BuiltinScanner{builtin: builtin, set: set}
}

// SetPolicy rebuilds the effective rule set under the given policy and swaps
// it in atomically. If the new policy produces an invalid rule set
// (regex compile fail, dict_file missing, ...) the swap is skipped and the
// error is returned; the previously-active rule set keeps serving.
func (s *BuiltinScanner) SetPolicy(p *Policy) error {
	rs, err := BuildRuleSet(s.builtin, p)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.set = rs
	s.mu.Unlock()
	return nil
}

// RuleCount returns the current count of active rules (builtin minus
// disabled, plus custom). Read-only snapshot.
func (s *BuiltinScanner) RuleCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.set.Rules)
}

func (s *BuiltinScanner) Scan(payload []byte, direction string, policy *Policy) []Finding {
	if policy != nil && !policy.Enabled {
		return []Finding{}
	}
	if len(payload) == 0 || direction == "" {
		return []Finding{}
	}
	s.mu.RLock()
	rs := s.set
	s.mu.RUnlock()

	out := make([]Finding, 0, 4)
	for _, rule := range rs.Rules {
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
