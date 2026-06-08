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
// User workers continue to start at w-1001. The pane-hide logic is
// isBuiltinAgent in audit_policy_worker.go.

import (
	_ "embed"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ttyd-go/skillcmd"
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

// setupTeamHelperAgent provisions w-6002 alongside the audit-policy admin
// (w-6001 SecOps Lead, merged in 2.1.8). Called lazily from
// ensureBuiltinPaneLazy on the first chat-ws connect to w-6002;
// idempotent — refreshes AGENTS.md and revives the pane.
//
// In --helper=1 mode this is a no-op: the trial container creates w-6002
// itself (via createSelectedWorkers) and ships its own install-protocol
// AGENTS.md via Dockerfile COPY, which we must leave untouched.
//
// Side-effect: installs the `agent-teams` skill from the public registry
// so the Team Helper agent has a stable CLI for window.cicy.localTeams.*
// instead of hand-rolling agent-webpage exec-js. Scoped to this setup
// (NOT in preinstalledSkills) because the skill is only useful when a
// cicy-desktop webview is connected to a w-6002 pane.
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
	go ensureAgentTeamsSkill()
	replyInChineseStartupQueue.enqueueText(teamHelperPaneID, teamHelperAgentType, teamHelperIntroPrompt)
	log.Printf("[team-helper] queued self-intro for %s", teamHelperPaneID)
}

// ensureAgentTeamsSkill installs `agent-teams` from the public skill
// registry if it isn't already on disk. Runs in a goroutine because the
// network round-trip can be slow on fresh hosts; the helper's first few
// turns will work without it and just fall back to raw agent-webpage
// exec-js as the AGENTS.md backup path documents.
func ensureAgentTeamsSkill() {
	installed, err := skillcmd.InstalledSkills()
	if err == nil {
		for _, s := range installed.Skills {
			if s.Name == "agent-teams" {
				return
			}
		}
	}
	if _, err := skillcmd.InstallSkill("agent-teams", io.Discard); err != nil {
		log.Printf("[team-helper] agent-teams skill install failed: %v", err)
		return
	}
	log.Printf("[team-helper] agent-teams skill installed")
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
		useCustomGateway: false, // Team Helper — trial AND local — always rides the Big Pickle Zen gateway, never the user's custom gateway. The persona is a built-in "service" provided by cicy-code (manage install/upgrade/token/team — see embed/team-helper-AGENTS.md), so we don't want it burning the user's own provider quota. Distinct from regular w-1001+ user workers and w-6001 SecOps Lead, which both use the user's gateway (useCustomGateway:=true / !helperMode).
	})
	if err != nil {
		return err
	}
	log.Printf("[team-helper] created %s (port %d, role=%s)", teamHelperPaneID, teamHelperPort, teamHelperRole)
	return nil
}

// ── lazy bootstrap (built-in panes) ─────────────────────────────────────
//
// Instead of pre-creating w-6002 at every checkEnv() boot, we let the
// pane materialise on first demand. The desktop drawer's <webview> dials
// /api/chat/ws?agent_id=w-6002 the moment the user clicks "Open Helper";
// chatbus.handleChatWS invokes ensureBuiltinPaneLazy(agent_id) right
// before the upgrade.
//
// sync.Once ensures setupTeamHelperAgent runs exactly once per process
// instance. After a daemon restart the Once resets, so the pane is
// always (re)started — whether or not a DB row already existed. This
// fixes the "ttyd not listening after restart" bug where the old DB
// existence pre-check prevented startAgentFromConfig from running.
// setupTeamHelperAgent / ensureTeamHelperPane are idempotent: they update
// the existing DB row and call startAgentFromConfig (which skips
// already-running tmux sessions and ttyd listeners).

var lazyBuiltinOnce sync.Map // key = short pane id (e.g. "w-6002") → *sync.Once

func ensureBuiltinPaneLazy(agentID string) {
	if agentID == "" {
		return
	}
	// helperMode (cloud trial container) builds its own w-6002 via
	// createSelectedWorkers + Dockerfile-baked AGENTS.md. Don't double-up.
	if helperMode {
		return
	}
	short := strings.Split(agentID, ":")[0]
	switch short {
	case teamHelperShortPane:
		// known built-in
	default:
		return
	}

	onceI, _ := lazyBuiltinOnce.LoadOrStore(short, &sync.Once{})
	once := onceI.(*sync.Once)
	once.Do(func() {
		log.Printf("[team-helper] lazy bootstrap on first request to %s", agentID)
		setupTeamHelperAgent()
	})
}
