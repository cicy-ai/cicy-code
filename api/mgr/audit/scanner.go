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
	// ScanInline runs only the inline (block/redact) rules — the cheap subset
	// the synchronous preventive path needs before forwarding a request.
	ScanInline(payload []byte, direction string, policy *Policy) []Finding
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

func (NoopScanner) ScanInline(payload []byte, direction string, policy *Policy) []Finding {
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
	return s.scan(payload, direction, policy, false)
}

// ScanInline scans ONLY the inline (block / redact) rules. It runs on the
// preventive path, which is synchronous and blocks the request before it is
// forwarded to the model — so it must stay cheap. Skipping the detective-only
// rules (high_entropy and the rest) keeps the blocking cost to a few ms of
// light regexes instead of the full ~1s scan a request would otherwise wait on.
func (s *BuiltinScanner) ScanInline(payload []byte, direction string, policy *Policy) []Finding {
	return s.scan(payload, direction, policy, true)
}

func (s *BuiltinScanner) scan(payload []byte, direction string, policy *Policy, inlineOnly bool) []Finding {
	if policy != nil && !policy.Enabled {
		return []Finding{}
	}
	if len(payload) == 0 || direction == "" {
		return []Finding{}
	}
	s.mu.RLock()
	rs := s.set
	s.mu.RUnlock()

	// Content-aware pass: index JSON string values once, then scan only the
	// segments OUTSIDE benign protocol fields (Anthropic thinking signatures,
	// base64 image data). Those blobs are the bulk of the body, so skipping
	// them makes the scan both faster and noise-free. Each rule's matches are
	// translated back to global offsets and tagged with their field path.
	// ranges == nil (non-JSON) → one whole-payload segment → original behaviour.
	ranges := indexJSONStrings(payload)
	segments := scanSegments(ranges, payload)
	// Concatenate the non-benign segments into ONE buffer so each rule scans a
	// single time (not once per segment). Matches are mapped back to global
	// payload offsets via segMap.
	buf, segMap := buildScanBuffer(segments, payload)

	out := make([]Finding, 0, 4)
	for _, rule := range rs.Rules {
		// Preventive path only needs the inline block/redact rules; skip the
		// expensive detective-only rules so blocking latency stays minimal.
		if inlineOnly && !(rule.Inline && (rule.DefaultAction == ActionBlock || rule.DefaultAction == ActionRedact)) {
			continue
		}
		if !directionMatches(rule.ScanDirections, direction) {
			continue
		}
		local := rule.Detect(buf)
		if len(local) == 0 {
			continue
		}
		spans := make([]Span, 0, len(local))
		for _, ls := range local {
			gs, ge, ok := mapToGlobal(ls.Start, ls.End, segMap)
			if !ok {
				continue
			}
			ls.Start, ls.End = gs, ge
			spans = append(spans, ls)
		}
		spans = applyStructure(spans, ranges, payload)
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
