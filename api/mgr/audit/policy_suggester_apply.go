package audit

// Apply a suggestion's PolicyPatch into ~/cicy-ai/audit/policy.json.
// Reuses WriteGlobalPolicy to keep the validate-then-atomic-write path
// consistent with manual edits + the existing PolicyForm UI.

import (
	"encoding/json"
	"fmt"
	"os"
)

// ApplySuggestion locates the suggestion by ID, merges its patch into the
// current policy file, persists the result, and marks the suggestion
// status=applied. Returns the new policy_hash.
//
// Idempotent only at the suggestion level (calling twice with the same
// ID is a no-op after the first apply) — NOT at the patch level (a
// patch creating "custom.foo" + a second patch creating "custom.foo"
// will replace, not duplicate).
func ApplySuggestion(id string) (string, error) {
	s, err := LookupSuggestion(id)
	if err != nil {
		return "", err
	}
	if s.Status == "applied" {
		return "", fmt.Errorf("suggestion %s already applied", id)
	}

	path := DefaultPolicyPath()
	if path == "" {
		return "", fmt.Errorf("audit: cannot resolve policy.json path")
	}

	// Load current raw policy (or seed with the default-shape JSON if absent).
	var current map[string]interface{}
	body, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		seed, _ := json.Marshal(DefaultPolicy())
		_ = json.Unmarshal(seed, &current)
	} else {
		if err := json.Unmarshal(body, &current); err != nil {
			return "", fmt.Errorf("policy.json parse: %w", err)
		}
	}
	if current == nil {
		current = map[string]interface{}{}
	}

	if err := mergePolicyPatch(current, s.Patch); err != nil {
		return "", fmt.Errorf("merge patch: %w", err)
	}

	merged, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return "", err
	}
	hash, err := WriteGlobalPolicy(merged)
	if err != nil {
		return "", err
	}

	if err := SetSuggestionStatus(id, "applied"); err != nil {
		// Policy already updated; log but don't roll back.
		return hash, fmt.Errorf("policy applied (%s) but suggestion status update failed: %w", hash, err)
	}
	return hash, nil
}

// mergePolicyPatch updates the raw policy map with patch fields. Lists
// are append-with-dedup by id; single objects (allow_list, preventive)
// are deep-merged.
func mergePolicyPatch(current map[string]interface{}, patch PolicyPatch) error {
	if len(patch.RulesOverride) > 0 {
		existing := readPolicyList(current, "rules_override")
		current["rules_override"] = mergeByID(existing, mapAllRuleOverrides(patch.RulesOverride))
	}
	if len(patch.CustomRules) > 0 {
		existing := readPolicyList(current, "custom_rules")
		current["custom_rules"] = mergeByID(existing, mapAllCustomRules(patch.CustomRules))
	}
	if patch.AllowList != nil {
		existing, _ := current["allow_list"].(map[string]interface{})
		if existing == nil {
			existing = map[string]interface{}{}
		}
		existing["paths"] = mergeStringSet(existing["paths"], patch.AllowList.Paths)
		existing["content_hashes"] = mergeStringSet(existing["content_hashes"], patch.AllowList.ContentHashes)
		existing["agents"] = mergeStringSet(existing["agents"], patch.AllowList.Agents)
		current["allow_list"] = existing
	}
	if patch.Preventive != nil {
		// Marshal-roundtrip the patch into a map so we can merge each field.
		patchBytes, _ := json.Marshal(patch.Preventive)
		var pm map[string]interface{}
		_ = json.Unmarshal(patchBytes, &pm)
		existing, _ := current["preventive"].(map[string]interface{})
		if existing == nil {
			existing = map[string]interface{}{}
		}
		for k, v := range pm {
			existing[k] = v
		}
		current["preventive"] = existing
	}
	return nil
}

func readPolicyList(current map[string]interface{}, key string) []map[string]interface{} {
	raw, ok := current[key]
	if !ok {
		return nil
	}
	items, _ := raw.([]interface{})
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func mapAllRuleOverrides(items []RuleOverride) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		b, _ := json.Marshal(it)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		out = append(out, m)
	}
	return out
}

func mapAllCustomRules(items []CustomRule) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		b, _ := json.Marshal(it)
		var m map[string]interface{}
		_ = json.Unmarshal(b, &m)
		out = append(out, m)
	}
	return out
}

// mergeByID returns the union of existing and patch, with patch entries
// overriding existing entries with the same "id" field.
func mergeByID(existing, patch []map[string]interface{}) []interface{} {
	byID := map[string]map[string]interface{}{}
	var order []string
	push := func(m map[string]interface{}) {
		id, _ := m["id"].(string)
		if id == "" {
			id = fmt.Sprintf("__noid_%d", len(order))
		}
		if _, exists := byID[id]; !exists {
			order = append(order, id)
		}
		byID[id] = m
	}
	for _, m := range existing {
		push(m)
	}
	for _, m := range patch {
		push(m)
	}
	out := make([]interface{}, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func mergeStringSet(existing interface{}, add []string) []interface{} {
	seen := map[string]bool{}
	var out []interface{}
	switch e := existing.(type) {
	case []interface{}:
		for _, v := range e {
			s, _ := v.(string)
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range e {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, s := range add {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
