package audit

import "testing"

func scanWithJSRule(t *testing.T, code, payload string) []Finding {
	t.Helper()
	pol := DefaultPolicy()
	pol.CustomRules = []CustomRule{{
		ID:             "test_js",
		Severity:       SeverityHigh,
		ScanDirections: []string{DirectionOutbound},
		Match:          RuleMatch{Type: "js", Pattern: code},
	}}
	sc := NewBuiltinScanner()
	if err := sc.SetPolicy(pol); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	return sc.Scan([]byte(payload), DirectionOutbound, pol)
}

func hasRuleID(fs []Finding, id string) bool {
	for _, f := range fs {
		if f.RuleID == id {
			return true
		}
	}
	return false
}

func TestJSRule_ArrayMatch(t *testing.T) {
	fs := scanWithJSRule(t, `return text.match(/AKIA[0-9A-Z]{16}/g) || [];`, `key=AKIAIOSFODNN7EXAMPLE here`)
	if !hasRuleID(fs, "test_js") {
		t.Fatalf("js array rule did not match: %+v", fs)
	}
}

func TestJSRule_BooleanMatch(t *testing.T) {
	fs := scanWithJSRule(t, `return text.includes('rm -rf /');`, `then run rm -rf / oops`)
	if !hasRuleID(fs, "test_js") {
		t.Fatalf("js boolean rule did not match: %+v", fs)
	}
}

func TestJSRule_NoMatch(t *testing.T) {
	fs := scanWithJSRule(t, `return text.includes('NOPE');`, `clean content`)
	if hasRuleID(fs, "test_js") {
		t.Fatalf("js rule matched clean content: %+v", fs)
	}
}

func TestJSRule_BadCodeRejected(t *testing.T) {
	pol := DefaultPolicy()
	pol.CustomRules = []CustomRule{{ID: "bad", Severity: SeverityLow, ScanDirections: []string{DirectionOutbound}, Match: RuleMatch{Type: "js", Pattern: "this is ((( not js"}}}
	sc := NewBuiltinScanner()
	if err := sc.SetPolicy(pol); err == nil {
		t.Fatalf("expected bad JS to be rejected at SetPolicy")
	}
}
