package audit

// Policy is the runtime-loaded audit policy.
//
// Walking skeleton: only the hash field is populated. Phase 2 (Detection
// Coverage) will flesh out Rules, CustomRules, AllowList, Notify, Retention,
// etc., per docs/v1/audit-system-design.md §6.2.
type Policy struct {
	Hash string
}

// LoadPolicy is a placeholder that returns a default empty policy.
// Phase 2 will read and validate ~/cicy-ai/audit/policy.json here.
func LoadPolicy(path string) (*Policy, error) {
	_ = path
	return &Policy{Hash: "sha256:DEFAULT"}, nil
}
