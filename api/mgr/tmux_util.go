// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
)

// runTmux runs a tmux command and returns its trimmed stdout. (Previously lived
// in instance.go alongside the per-pane ttyd port pool, which has been removed
// in favour of inline webtty serving — see ttyd_inline.go.)
func runTmux(args ...string) (string, error) {
	out, err := tmuxCommand(args...).Output()
	return strings.TrimSpace(string(out)), err
}

// extractPaneID pulls the pane_id (the value after -t) out of tmux args.
func extractPaneID(args []string) string {
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
