package main

import "testing"

// todo #104: agent_type "dispatcher" renamed to "cicy"; "cicy" must no longer
// alias the claude-skinned variant.
func TestNormalizeAgentTypeCicyRename(t *testing.T) {
	cases := map[string]string{
		"cicy":        "cicy",
		"dispatcher":  "cicy", // legacy alias
		"secretary":   "cicy", // legacy alias
		"Dispatcher":  "cicy",
		"cicy-claude": "cicy-claude",
		"claude":      "claude",
	}
	for in, want := range cases {
		if got := normalizeAgentType(in); got != want {
			t.Errorf("normalizeAgentType(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCicyGuidanceFile(t *testing.T) {
	if got := guidanceFilenameForAgentType("cicy"); got != "AGENTS.md" {
		t.Errorf("cicy guidance = %q want AGENTS.md", got)
	}
	if got := guidanceFilenameForAgentType("dispatcher"); got != "AGENTS.md" {
		t.Errorf("dispatcher (alias) guidance = %q want AGENTS.md", got)
	}
}
