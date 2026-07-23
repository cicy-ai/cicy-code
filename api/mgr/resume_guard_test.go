// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

func TestClaudeProjectDirSlug(t *testing.T) {
	cases := map[string]string{
		"/Users/cicy/cicy-ai/workers/w-10144": "-Users-cicy-cicy-ai-workers-w-10144",
		"/home/cicy/cicy-ai/workers/w-1001":   "-home-cicy-cicy-ai-workers-w-1001",
		"/Users/x/my.proj_v2":                 "-Users-x-my-proj-v2",
	}
	for in, want := range cases {
		if got := claudeProjectDirSlug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Issue #30: the resume lookup must be scoped to the pane's own project dir,
// never the projects/*/ glob alone, and a foreign match must refuse out loud.
func TestClaudeResumeBootLinesScopedToOwnProjectDir(t *testing.T) {
	script := strings.Join(claudeResumeBootLines(), "\n")
	if !strings.Contains(script, `_projdir="$HOME/.claude/projects/$(printf '%s' "$WORKSPACE"`) {
		t.Fatalf("resume boot lines no longer scope the lookup to the pane's own project dir:\n%s", script)
	}
	if !strings.Contains(script, `[ -f "$_projdir/$_cid.jsonl" ]`) {
		t.Fatalf("resume boot lines must check the transcript inside _projdir:\n%s", script)
	}
	if !strings.Contains(script, "refusing cross-agent resume") {
		t.Fatalf("foreign transcript match must WARN about the refused cross-agent resume:\n%s", script)
	}
	// The glob may only appear in the WARN branch (detection), never as the
	// condition that ENABLES a resume.
	for _, line := range claudeResumeBootLines() {
		if strings.Contains(line, `projects/*/`) && strings.Contains(line, "CICY_RESUME=") {
			t.Fatalf("projects/*/ glob must not gate CICY_RESUME: %s", line)
		}
	}
}
