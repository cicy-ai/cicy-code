package main

import (
	"fmt"
	"strings"

	"ttyd-go/mgr/audit"
)

// auditSpecialistRoleTemplate is the employee-template slug of the single
// 审计策略专员 (the user's audit advisor) role. A finding hit is dispatched to
// whichever live cicy agent was provisioned with this role_template. The former
// split (审计策略专员 owns policy + 审计专员 triages) was merged into this one
// advisor: it configures rules, interprets logs, and triages hits.
const auditSpecialistRoleTemplate = "审计策略专员"

// Wire the audit pipeline's "forward finding to advisor" channel to the
// cross-agent send path. Runs before main() so the forwarder is set by the
// time the first finding is dispatched.
func init() {
	audit.SetFindingForwarder(forwardAuditFindingToAuditSpecialist)
}

// auditSpecialistPaneID resolves the pane of the live 审计策略专员 agent (a
// normal cicy agent carrying role_template=审计策略专员). Returns "" when none
// is provisioned — callers treat that as "no audit advisor on duty".
func auditSpecialistPaneID() string {
	var pane string
	_ = store.QueryRow(
		"SELECT pane_id FROM agent_config WHERE role_template=? AND COALESCE(active,1)=1 ORDER BY updated_at DESC LIMIT 1",
		auditSpecialistRoleTemplate,
	).Scan(&pane)
	return strings.TrimSpace(pane)
}

// forwardAuditFindingToAuditSpecialist delivers a finding brief to the 审核策略
// 专员 agent — the same delivery `cicy-agent msg` uses (sendTextToPane → the
// advisor receives it as an incoming message). The advisor (an AI agent) then
// verifies (real hit vs false positive), grades severity, and handles the
// response per its charter. The backend decides nothing here.
func forwardAuditFindingToAuditSpecialist(brief string) error {
	pane := auditSpecialistPaneID()
	if pane == "" {
		return fmt.Errorf("no 审计策略专员 agent provisioned (role_template=%s) — finding not dispatched", auditSpecialistRoleTemplate)
	}
	return sendTextToPane(pane, brief, true)
}
