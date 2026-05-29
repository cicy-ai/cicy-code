package main

// audit_team_helper_worker.go reserves w-6002 as the local "Team Helper" —
// the long-running, on-machine counterpart of the 30-minute cloud trial
// helper container. Same task list (install / upgrade / token-rotate /
// remove teams), but no time limit.
//
// Lifecycle:
//   - In --helper=1 mode (cloud trial container) this file is SKIPPED at
//     checkEnv time; the helper container builds w-6002 via
//     createSelectedWorkers and Dockerfile COPYs its own install-protocol
//     AGENTS.md on top. We must not overwrite that.
//   - In normal mode (every other cicy-code install) we own w-6002.
//
// Built-in agents (2.1.8+):
//   - w-6002  = team helper (this file)
//   - w-6001 = SecOps Lead (audit advisor + security officer, merged from
//               the previous w-10000 + w-6001 split)
// User workers continue to start at w-10001. The pane-hide logic is
// isBuiltinAgent in audit_policy_worker.go.

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	teamHelperPaneID    = "w-6002:main.0"
	teamHelperShortPane = "w-6002"
	teamHelperPort      = 6002
	teamHelperAgentType = "opencode"
	teamHelperRole      = "team-helper"
	teamHelperTitle     = "Team Helper"
)

//go:embed embed/team-helper-AGENTS.md
var teamHelperGuidance []byte

// teamHelperIntroPrompt is fired the first time the pane comes up — it
// is the minimal English trigger that nudges opencode to read its own
// AGENTS.md and produce the locked-language greeting defined there.
const teamHelperIntroPrompt = "A new user session has started — open with your greeting from AGENTS.md now."

// setupTeamHelperAgent provisions w-6002 alongside the security officer
// (w-6001 SecOps Lead). Called from checkEnv on every
// startup; idempotent — refreshes AGENTS.md and revives the pane.
//
// In --helper=1 mode this is a no-op: the trial container creates w-6002
// itself (via createSelectedWorkers) and ships its own install-protocol
// AGENTS.md via Dockerfile COPY, which we must leave untouched.
func setupTeamHelperAgent() {
	if helperMode {
		return
	}
	if err := writeTeamHelperGuidance(); err != nil {
		log.Printf("[team-helper] guidance write failed: %v", err)
	}
	if err := ensureTeamHelperPane(); err != nil {
		log.Printf("[team-helper] pane bootstrap failed: %v", err)
		return
	}
	replyInChineseStartupQueue.enqueueText(teamHelperPaneID, teamHelperAgentType, teamHelperIntroPrompt)
	log.Printf("[team-helper] queued self-intro for %s", teamHelperPaneID)
}

// writeTeamHelperGuidance drops the embedded persona doc into w-6002's
// workspace every startup so a binary upgrade always wins (binary > disk).
func writeTeamHelperGuidance() error {
	ws := builtinWorkerWorkspace(teamHelperShortPane)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return err
	}
	filename := guidanceFilenameForAgentType(teamHelperAgentType)
	if filename == "" {
		filename = "AGENTS.md"
	}
	return os.WriteFile(filepath.Join(ws, filename), teamHelperGuidance, 0o644)
}

// ensureTeamHelperPane creates the w-6002 pane on first run and revives
// it (with the latest agent_type / role / permission flags) on
// subsequent runs.
func ensureTeamHelperPane() error {
	var workspace, initScript, configJSON, agentType string
	var allowAllActions, replyInChinese, useCustomGateway bool
	var port int
	err := store.QueryRow(`
		SELECT ttyd_port, COALESCE(workspace,''), COALESCE(init_script,''),
		       COALESCE(config,'{}'), COALESCE(agent_type,''),
		       COALESCE(allow_all_actions,0), COALESCE(reply_in_chinese,0),
		       COALESCE(use_custom_gateway,0)
		FROM agent_config WHERE pane_id=?`, teamHelperPaneID,
	).Scan(&port, &workspace, &initScript, &configJSON, &agentType,
		&allowAllActions, &replyInChinese, &useCustomGateway)

	if err == nil && port > 0 {
		store.Exec(
			fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, role=?, allow_all_actions=1, reply_in_chinese=0, updated_at=%s WHERE pane_id=?", store.Now()),
			teamHelperAgentType, teamHelperTitle, teamHelperRole, teamHelperPaneID,
		)
		_, _ = allowAllActions, replyInChinese
		token := getFirstToken()
		startAgentFromConfig(teamHelperPaneID, port, workspace, initScript, configJSON,
			teamHelperAgentType, true, false, useCustomGateway, token)
		return nil
	}

	token := getFirstToken()
	_, err = createManagedPane(paneCreateOpts{
		session:          teamHelperShortPane,
		title:            teamHelperTitle,
		role:             teamHelperRole,
		agentType:        teamHelperAgentType,
		workspace:        builtinWorkerWorkspace(teamHelperShortPane),
		port:             teamHelperPort,
		token:            token,
		allowAllActions:  true,
		replyInChinese:   false,
		useCustomGateway: false, // long-running local helper uses the user's own provider config
	})
	if err != nil {
		return err
	}
	log.Printf("[team-helper] created %s (port %d, role=%s)", teamHelperPaneID, teamHelperPort, teamHelperRole)
	return nil
}
