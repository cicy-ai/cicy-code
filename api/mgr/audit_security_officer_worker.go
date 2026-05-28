package main

// audit_security_officer_worker.go reserves w-1000 as a dedicated "Security
// Officer" agent — the human-coordination layer that audit incidents escalate
// to (alongside email/WeChat). Created on first startup, hidden from the regular
// agent list, distinct from w-10000 (audit policy admin / SecOps Lead).
//
// Convention: agents with id < 10000 are built-in / system agents (w-1000 is
// the first); w-10000 sits at the boundary (audit admin); user workers are
// >= 10001. The pane-hide logic uses isBuiltinAgent (id <= 10000) so future
// built-in agents are auto-hidden too.

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// securityOfficerPaneID is the built-in security-officer pane. Defined here
	// (not in audit_im_notify.go) so the worker file owns the agent identity.
	securityOfficerPaneID     = "w-1000:main.0"
	securityOfficerShortPane  = "w-1000"
	securityOfficerPort       = 11000 // non-privileged, distinct from user workers (10001+) and admin (10000)
	securityOfficerAgentType  = "claude"
	securityOfficerRole       = "security-officer"
	securityOfficerTitle      = "Security Officer"
)

//go:embed embed/audit-security-officer-CLAUDE.md
var securityOfficerGuidance []byte

// securityOfficerIntroPrompt — minimal English trigger; the real startup
// routine (pick session language, then orient) lives in the agent's CLAUDE.md,
// injected via the gateway. Kept clean to avoid meta-instruction leakage.
const securityOfficerIntroPrompt = "A new operator session has started — open with your startup briefing now."

// setupSecurityOfficerAgent provisions w-1000 alongside the audit admin
// (w-10000). Called from checkEnv on every startup; idempotent — refreshes
// CLAUDE.md and revives the pane.
func setupSecurityOfficerAgent() {
	if err := writeSecurityOfficerGuidance(); err != nil {
		log.Printf("[security-officer] guidance write failed: %v", err)
	}
	if err := ensureSecurityOfficerPane(); err != nil {
		log.Printf("[security-officer] pane bootstrap failed: %v", err)
		return
	}
	replyInChineseStartupQueue.enqueueText(securityOfficerPaneID, securityOfficerAgentType, securityOfficerIntroPrompt)
	log.Printf("[security-officer] queued self-intro for %s", securityOfficerPaneID)
}

// writeSecurityOfficerGuidance drops the embedded role doc into w-1000's
// workspace every startup so a binary upgrade always wins.
func writeSecurityOfficerGuidance() error {
	ws := builtinWorkerWorkspace(securityOfficerShortPane)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return err
	}
	filename := guidanceFilenameForAgentType(securityOfficerAgentType)
	if filename == "" {
		filename = "CLAUDE.md"
	}
	return os.WriteFile(filepath.Join(ws, filename), securityOfficerGuidance, 0o644)
}

// ensureSecurityOfficerPane creates the w-1000 pane on first run and revives
// it (with the latest agent_type / role / permission flags) on subsequent runs.
// Always idempotent — like ensureAuditPolicyPane.
func ensureSecurityOfficerPane() error {
	var workspace, initScript, configJSON, agentType string
	var allowAllActions, replyInChinese, useCustomGateway bool
	var port int
	err := store.QueryRow(`
		SELECT ttyd_port, COALESCE(workspace,''), COALESCE(init_script,''),
		       COALESCE(config,'{}'), COALESCE(agent_type,''),
		       COALESCE(allow_all_actions,0), COALESCE(reply_in_chinese,0),
		       COALESCE(use_custom_gateway,0)
		FROM agent_config WHERE pane_id=?`, securityOfficerPaneID,
	).Scan(&port, &workspace, &initScript, &configJSON, &agentType,
		&allowAllActions, &replyInChinese, &useCustomGateway)

	if err == nil && port > 0 {
		// Already provisioned — refresh metadata + revive. Force the trusted-admin
		// flags so older rows match the current convention.
		store.Exec(
			fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, role=?, allow_all_actions=1, reply_in_chinese=0, updated_at=%s WHERE pane_id=?", store.Now()),
			securityOfficerAgentType, securityOfficerTitle, securityOfficerRole, securityOfficerPaneID,
		)
		_, _ = allowAllActions, replyInChinese
		token := getFirstToken()
		startAgentFromConfig(securityOfficerPaneID, port, workspace, initScript, configJSON,
			securityOfficerAgentType, true, false, useCustomGateway, token)
		return nil
	}

	// First run — create.
	token := getFirstToken()
	_, err = createManagedPane(paneCreateOpts{
		session:          securityOfficerShortPane,
		title:            securityOfficerTitle,
		role:             securityOfficerRole,
		agentType:        securityOfficerAgentType,
		workspace:        builtinWorkerWorkspace(securityOfficerShortPane),
		port:             securityOfficerPort,
		token:            token,
		allowAllActions:  true,
		replyInChinese:   false,
		useCustomGateway: true,
	})
	if err != nil {
		return err
	}
	log.Printf("[security-officer] created %s (port %d, role=%s)", securityOfficerPaneID, securityOfficerPort, securityOfficerRole)
	return nil
}

// isBuiltinAgent reports whether a pane id belongs to a built-in / system
// agent (numeric id ≤ 10000). w-1000 = security officer, w-10000 = audit admin;
// the user explicitly named "id < 10000" as the built-in range and indicated
// more built-ins are coming. Used by the pane-list to hide built-ins from the
// regular agent list (?include_hidden=1 to bypass).
func isBuiltinAgent(paneID string) bool {
	short := strings.Split(paneID, ":")[0]
	if !strings.HasPrefix(short, "w-") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(short, "w-"))
	if err != nil {
		return false
	}
	return n <= 10000
}
