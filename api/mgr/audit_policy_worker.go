package main

import "strings"

// audit-v2 refactor: the fixed w-6001 "SecOps Lead" singleton pane was removed.
// Its responsibilities now live in ONE ordinary, user-onboarded cicy agent
// carrying a role_template employee template:
//   - 审核策略专员 — the user's audit advisor: owns policy.json (rules /
//     severity / override / allowlist) AND receives finding hits to verify &
//     triage (audit_agent_notify.go). The old separate 审计专员 seat was
//     merged into this one role.
//
// With no built-in singleton pane left, there is nothing to bootstrap, hide, or
// tear down. isBuiltinAgent is kept (still referenced by /api/panes filtering)
// but now reports false for every pane — no system pane is hidden.

// isBuiltinAgent reports whether a pane id belongs to a built-in / system agent
// that should be hidden from the regular /api/panes listing. After the w-6001
// removal there are no such panes, so this is always false. Kept as the single
// filtering hook in case a future built-in needs hiding again.
func isBuiltinAgent(paneID string) bool {
	_ = strings.TrimSpace(paneID)
	return false
}
