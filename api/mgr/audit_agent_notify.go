package main

import (
	"ttyd-go/mgr/audit"
)

// Wire the audit pipeline's "forward finding to advisor" channel to the
// cross-agent send path. Runs before main() so the forwarder is set by the
// time the first finding is dispatched.
func init() {
	audit.SetFindingForwarder(forwardAuditFindingToAdvisor)
}

// forwardAuditFindingToAdvisor delivers a finding brief to the w-6001 audit
// advisor pane — the same delivery `cicy-agent msg` uses (sendTextToPane → the
// advisor receives it as an incoming message). The advisor (an AI agent) then
// triages and orchestrates the whole response with its own skills: notify the
// offending agent (cicy-agent msg), escalate to the owner (cicy-policy notify),
// tune policy (cicy-policy patch). The backend decides nothing here.
func forwardAuditFindingToAdvisor(brief string) error {
	return sendTextToPane(auditPolicyPaneID, brief, true)
}
