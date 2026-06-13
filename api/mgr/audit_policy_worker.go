package main

import "strings"

// audit-v2 refactor: the fixed w-6001 "SecOps Lead" singleton pane was removed.
// Its two responsibilities were split into ordinary, user-onboarded cicy agents
// carrying role_template employee templates:
//   - 审核策略专员 — owns policy.json (rules / severity / override / allowlist)
//   - 审计专员     — receives finding hits, verifies & triages (audit_agent_notify.go)
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
