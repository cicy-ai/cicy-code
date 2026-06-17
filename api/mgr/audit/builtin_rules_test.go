package audit

import (
	"testing"
)

// Builtin rules were trimmed (2026-06-15) to just the two unambiguous secret
// detectors — secret.jwt + secret.bearer_token. The previous private_key / aws_*
// / pii.* / network.* builtins were removed (broad PII matchers false-positive on
// normal agent work), so their tests are gone too. Add a rule back here only when
// it is restored in builtin_rules.go.

// findRule returns the BuiltinRule with the given id, or fails the test.
func findRule(t *testing.T, id string) BuiltinRule {
	t.Helper()
	for _, r := range BuiltinRules() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("builtin rule %q not found", id)
	return BuiltinRule{}
}

func assertMatch(t *testing.T, ruleID string, payload string, wantMatch bool) {
	t.Helper()
	rule := findRule(t, ruleID)
	spans := rule.Detect([]byte(payload))
	got := len(spans) > 0
	if got != wantMatch {
		t.Errorf("[%s] match=%v want=%v\n  payload: %q\n  spans: %+v",
			ruleID, got, wantMatch, payload, spans)
	}
}

func TestBuiltin_JWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	assertMatch(t, "secret.jwt", jwt, true)
	assertMatch(t, "secret.jwt", "auth: "+jwt, true)
	assertMatch(t, "secret.jwt", "eyJ.eyJ.short", false)
	assertMatch(t, "secret.jwt", "not a jwt at all", false)
}

func TestBuiltin_BearerToken(t *testing.T) {
	assertMatch(t, "secret.bearer_token",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123",
		true)
	assertMatch(t, "secret.bearer_token", "Bearer abc", false) // too short
}

// TestBuiltinScanner_NoFindingsOnEmpty: empty payload yields zero findings,
// not nil, not panic.
func TestBuiltinScanner_NoFindingsOnEmpty(t *testing.T) {
	s := NewBuiltinScanner()
	out := s.Scan([]byte{}, DirectionOutbound, nil)
	if len(out) != 0 {
		t.Errorf("empty payload: got %d findings, want 0", len(out))
	}
}
