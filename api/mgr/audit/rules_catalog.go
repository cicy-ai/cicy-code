package audit

// RuleMeta is the display metadata for one builtin rule, surfaced to the UI so
// users can see what the on-host pipeline enforces out of the box (secret/PII
// detectors AND behaviour rules), separately from the policy.json overrides.
type RuleMeta struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Category   string   `json:"category"` // "secret" | "pii" | "behavior"
	Severity   Severity `json:"severity"`
	Kind       string   `json:"kind"`       // "builtin" (secret/pii regex) | "behavior" (tool-call)
	Directions []string `json:"directions"` // outbound/inbound, or behaviour action kinds
	// Pattern is the rule's regex source (empty for Go-function rules like
	// aws_secret/high_entropy/bank_card/id_card). Editable for regex builtins
	// via rules_override.pattern. Surfaced so the UI shows HOW the rule matches.
	Pattern    string `json:"pattern"`
	Editable   bool   `json:"editable"` // true = pattern can be overridden in policy
	// Tests are the default test cases shipped with a builtin rule, so the UI's
	// "save needs passing tests" gate has something to run out of the box.
	Tests []RuleTest `json:"tests"`
}

// builtinDefaultTests returns the stock test cases for a builtin rule id — a
// known-good (hit) sample and a known-clean (miss) sample. Empty for rules we
// haven't curated samples for yet.
func builtinDefaultTests(id string) []RuleTest {
	switch id {
	case "secret.jwt":
		return []RuleTest{
			{Text: "token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", Expect: "hit"},
		}
	case "secret.bearer_token":
		return []RuleTest{
			{Text: "Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123456789", Expect: "hit"},
		}
	}
	return []RuleTest{}
}

// RuleCatalog returns every builtin rule the pipeline ships with: the regex
// secret/PII detectors (builtin_rules.go) and the behaviour rules (behavior.go).
// This is read-only metadata for display — the rules themselves are code.
func RuleCatalog() []RuleMeta {
	out := make([]RuleMeta, 0, 16)
	for _, r := range BuiltinRules() {
		out = append(out, RuleMeta{
			ID:         r.ID,
			Label:      r.Label,
			Category:   r.Category,
			Severity:   r.Severity,
			Kind:       "builtin",
			Directions: r.ScanDirections,
			Pattern:    r.Pattern, // empty for Go-function rules (override w/ regex|js)
			Editable:   true,      // any builtin can be overridden by regex or JS
			Tests:      builtinDefaultTests(r.ID),
		})
	}
	// Behaviour rules are no longer a hardcoded builtin floor — they are authored
	// as policy custom_rules on the "behavior" scan direction (see
	// behaviorRulesFromPolicy), so they surface through the custom-rules path, not
	// the builtin catalog here.
	return out
}
