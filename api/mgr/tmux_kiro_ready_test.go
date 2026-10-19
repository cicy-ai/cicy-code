// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// Captured from a live kiro-cli 2.32.0 pane: the status bar shows the agent name
// ("kiro_default"), not "Kiro". The old check required "Kiro ·", so every send
// to a kiro pane hit ready-wait-timeout and the message stayed queued forever.
const kiro232IdlePane = `▸ Credits: turn 0.05 • session 0.10 | Time: 1s

───────────────────────────────────────────────────────────────────────────────────────
 Trust All Tools active, confirmations are off · /quit to exit
───────────────────────────────────────────────────────────────────────────────────────
kiro_default · auto · ◔ 2%                                     ~/cicy-ai/workers/w-1002

›  ask a question or describe a task ↵
                                                                    /copy to clipboard
`

func TestKiroInputReady(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"kiro 2.32 idle", kiro232IdlePane, true},
		{"legacy Kiro status bar", "Kiro · auto\n> ask a question or describe a task", true},
		{"custom agent name", "my-agent · claude-sonnet-4.5 · ◑ 40%\n› ask a question or describe a task", true},
		{"placeholder only (booting)", "›  ask a question or describe a task", false},
		{"status bar only (busy)", "kiro_default · auto · ◔ 2%\n⠋ Thinking...", false},
		{"shell prompt", "w-1002 $ ", false},
	}
	for _, c := range cases {
		if got := isAgentInputReady("kiro-cli", c.out); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
