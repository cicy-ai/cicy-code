// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// AgentOverride is the L3 override layer (per docs/v1/audit-system-design.md
// §7.1). Each agent can supply its own subset of policy at:
//
//	~/cicy-ai/workers/<agent>/.cicy/audit-overrides.json
//
// Allowed knobs (per-agent makes sense):
//   - rules_override        — disable / re-grade specific builtin rules
//   - allow_list            — agent-local false-positive set
//   - responsible_persons   — agent-specific escalation chain
//
// Disallowed knobs (system-wide; per-agent is risky or meaningless):
//   - preventive            — security policy must be uniform
//   - notify                — global noise governance
//   - incident_response     — system behavior
//   - custom_rules          — new rules apply globally
//   - allow_list.agents     — meaningless when YOU ARE the agent
type AgentOverride struct {
	RulesOverride      []RuleOverride            `json:"rules_override,omitempty"`
	AllowList          AgentAllowListSubset      `json:"allow_list,omitempty"`
	ResponsiblePersons *ResponsiblePersonsConfig `json:"responsible_persons,omitempty"`
}

// AgentAllowListSubset is allow_list minus the "agents" field (which would
// be self-referential at L3).
type AgentAllowListSubset struct {
	Paths         []string `json:"paths,omitempty"`
	ContentHashes []string `json:"content_hashes,omitempty"`
}

// agentOverridePath returns the file path for a given agent under workersRoot.
func agentOverridePath(workersRoot, agentID string) string {
	return filepath.Join(workersRoot, agentID, ".cicy", "audit-overrides.json")
}

// LoadAgentOverride reads the per-agent override file. Returns (nil, nil)
// when the file is absent (= no override for this agent). Returns an error
// when the file exists but is unparseable / fails validation.
func LoadAgentOverride(workersRoot, agentID string) (*AgentOverride, error) {
	if agentID == "" {
		return nil, nil
	}
	path := agentOverridePath(workersRoot, agentID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	ov := &AgentOverride{}
	if err := json.Unmarshal(data, ov); err != nil {
		return nil, fmt.Errorf("audit: parse %s: %w", path, err)
	}
	if err := validateAgentOverride(ov); err != nil {
		return nil, err
	}
	return ov, nil
}

// validateAgentOverride enforces the same severity/action invariants the
// global policy would, plus L3-specific constraints (no custom_rules).
func validateAgentOverride(ov *AgentOverride) error {
	if ov == nil {
		return nil
	}
	builtinIDs := map[string]bool{}
	for _, r := range BuiltinRules() {
		builtinIDs[r.ID] = true
	}
	for i, o := range ov.RulesOverride {
		if o.ID == "" {
			return fmt.Errorf("audit: agent override rules_override[%d]: empty id", i)
		}
		if !builtinIDs[o.ID] {
			return fmt.Errorf("audit: agent override rules_override[%d]: unknown rule %q", i, o.ID)
		}
		if o.Severity != "" && !validSeverity(o.Severity) {
			return fmt.Errorf("audit: agent override rules_override[%d]: invalid severity %q", i, o.Severity)
		}
		if o.DefaultAction != "" && !validAction(o.DefaultAction) {
			return fmt.Errorf("audit: agent override rules_override[%d]: invalid action %q", i, o.DefaultAction)
		}
	}
	return nil
}

// agentOverrideWriteMu serializes per-agent file writes so concurrent
// "Mark FP from this agent" requests don't trample each other.
var agentOverrideWriteMu sync.Mutex

// SaveAgentOverride atomically writes the per-agent override file. Caller
// supplies the validated AgentOverride; nil or empty struct clears the file
// (deletes it so the global policy is the sole authority).
func SaveAgentOverride(workersRoot, agentID string, ov *AgentOverride) error {
	if agentID == "" {
		return fmt.Errorf("audit: empty agent_id")
	}
	if err := validateAgentOverride(ov); err != nil {
		return err
	}
	agentOverrideWriteMu.Lock()
	defer agentOverrideWriteMu.Unlock()

	path := agentOverridePath(workersRoot, agentID)
	// Empty override = delete file (semantic "no override").
	if ov == nil || isEmptyOverride(ov) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(ov, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func isEmptyOverride(ov *AgentOverride) bool {
	if ov == nil {
		return true
	}
	if len(ov.RulesOverride) > 0 {
		return false
	}
	if len(ov.AllowList.Paths) > 0 || len(ov.AllowList.ContentHashes) > 0 {
		return false
	}
	if ov.ResponsiblePersons != nil {
		if len(ov.ResponsiblePersons.Default) > 0 ||
			len(ov.ResponsiblePersons.BySeverity) > 0 ||
			len(ov.ResponsiblePersons.ByAgent) > 0 ||
			len(ov.ResponsiblePersons.ByUser) > 0 ||
			len(ov.ResponsiblePersons.ByRule) > 0 {
			return false
		}
	}
	return true
}

// MergeIntoEffective produces a single *Policy that reflects how the audit
// pipeline should treat events from the given agent. Merge rules:
//
//	rules_override: union. Same id → agent wins; non-overlapping merged.
//	allow_list.paths:         union (dedup)
//	allow_list.content_hashes: union (dedup)
//	allow_list.agents:        global only (per-agent file can't list agents)
//	responsible_persons:      agent's non-empty tier replaces global's same
//	                          tier;  default falls through when agent didn't
//	                          set it. agent's by_rule / by_user / by_agent /
//	                          by_severity completely replace the global maps
//	                          when present.
//	other fields (preventive/notify/incident_response/custom_rules):
//	                          unchanged from global — these are system-wide.
//
// Returns a NEW *Policy; never mutates inputs.
func MergeIntoEffective(global *Policy, override *AgentOverride) *Policy {
	if global == nil {
		global = DefaultPolicy()
	}
	out := *global
	if override == nil {
		return &out
	}

	// rules_override
	if len(override.RulesOverride) > 0 {
		agentByID := make(map[string]bool, len(override.RulesOverride))
		merged := make([]RuleOverride, 0, len(override.RulesOverride)+len(global.RulesOverride))
		for _, ro := range override.RulesOverride {
			merged = append(merged, ro)
			agentByID[ro.ID] = true
		}
		for _, ro := range global.RulesOverride {
			if !agentByID[ro.ID] {
				merged = append(merged, ro)
			}
		}
		out.RulesOverride = merged
	}

	// allow_list union
	out.AllowList = AllowList{
		Paths:         unionStrings(global.AllowList.Paths, override.AllowList.Paths),
		ContentHashes: unionStrings(global.AllowList.ContentHashes, override.AllowList.ContentHashes),
		Agents:        global.AllowList.Agents, // per-agent file can't list agents
	}

	// responsible_persons tier-wise override
	if override.ResponsiblePersons != nil {
		ap := override.ResponsiblePersons
		gp := global.ResponsiblePersons
		merged := ResponsiblePersonsConfig{
			Default:    gp.Default,
			BySeverity: gp.BySeverity,
			ByAgent:    gp.ByAgent,
			ByUser:     gp.ByUser,
			ByRule:     gp.ByRule,
		}
		if len(ap.Default) > 0 {
			merged.Default = ap.Default
		}
		if len(ap.BySeverity) > 0 {
			merged.BySeverity = ap.BySeverity
		}
		if len(ap.ByAgent) > 0 {
			merged.ByAgent = ap.ByAgent
		}
		if len(ap.ByUser) > 0 {
			merged.ByUser = ap.ByUser
		}
		if len(ap.ByRule) > 0 {
			merged.ByRule = ap.ByRule
		}
		out.ResponsiblePersons = merged
	}

	return &out
}

func unionStrings(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ApplyRulesOverrideToFindings post-filters a scan result against a rule-
// override slice (which is the EFFECTIVE override list, i.e. already merged
// global+agent). Rules with Disabled=true drop the matching finding; Severity
// override re-grades it in place.
func ApplyRulesOverrideToFindings(findings []Finding, overrides []RuleOverride) []Finding {
	if len(findings) == 0 || len(overrides) == 0 {
		return findings
	}
	byID := make(map[string]RuleOverride, len(overrides))
	for _, ro := range overrides {
		byID[ro.ID] = ro
	}
	out := findings[:0]
	for _, f := range findings {
		ro, ok := byID[f.RuleID]
		if ok && ro.Disabled {
			continue
		}
		if ok && ro.Severity != "" {
			f.Severity = ro.Severity
		}
		out = append(out, f)
	}
	// Clone to avoid surprising the caller who passed `findings`.
	cp := make([]Finding, len(out))
	copy(cp, out)
	return cp
}
