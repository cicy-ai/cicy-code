package main

// audit_policy_worker.go reserves w-6001 as the single "SecOps Lead" agent —
// the merged audit-advisor + security-officer role. Created on first startup,
// hidden from the regular agent list, surfaced inside the Audit Dashboard
// "Assistant" tab.
//
// 2.1.8: the previously-separate security-officer agent was merged in,
// and the resulting single audit agent moved from w-10000 → w-6001 (port 6001,
// reclaiming the slot the security officer used to hold). One pane handles
// detection triage, policy edits, turnkey channel setup, AND human coordination
// on escalations.
//
// This is a singleton role:
//   - fixed pane_id     "w-6001:main.0"
//   - fixed ttyd port   6001
//   - fixed workspace   ~/cicy-ai/workers/w-6001
//   - fixed agent_type  "claude"
//   - role              "audit-policy-admin" (used to filter out of /api/panes)
//
// Bootstrap is run from checkEnv() once per startup; idempotent — if the
// pane already exists we only refresh CLAUDE.md and re-install skill files.

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	auditPolicyPaneID     = "w-6001:main.0"
	auditPolicyShortPane  = "w-6001"
	auditPolicyPort       = 6001
	auditPolicyAgentType  = "claude"
	auditPolicyRole       = "audit-policy-admin"
	auditPolicyTitle      = "SecOps Lead"
	auditPolicySkillName  = "cicy-audit-policy"
)

//go:embed embed/audit-policy-skill
var auditPolicySkillFS embed.FS

//go:embed embed/audit-policy-CLAUDE.md
var auditPolicyGuidance []byte

// auditPolicyIntroPrompt is the minimal first-turn trigger that makes w-6001
// open proactively. The actual startup routine (pick session language first,
// then readiness + recommendations) lives in the agent's CLAUDE.md, which is
// injected into the gateway request system prompt — so this stays a clean,
// English, one-line trigger rather than a meta-instruction leaking into chat.
// Fires once per process (in-memory queue); a fresh container re-greets.
const auditPolicyIntroPrompt = "A new operator session has started — open with your startup briefing now."

// removeAuditPolicyPane stops and removes the w-6001 SecOps pane when the audit
// master switch is OFF. Mirrors handleDeletePane's core (stop agent + clean the
// registry tables) so a previously-created w-6001 doesn't linger after audit is
// turned off. No-op when the pane doesn't exist.
func removeAuditPolicyPane() {
	paneID := normPaneID(auditPolicyPaneID)
	shortID := shortPaneID(paneID)
	var n int
	store.QueryRow("SELECT COUNT(1) FROM agent_config WHERE pane_id=?", paneID).Scan(&n)
	if n == 0 {
		return
	}
	log.Printf("[audit] OFF — stopping existing %s", shortID)
	func() {
		defer func() { recover() }()
		stopAgentByPaneID(paneID)
	}()
	store.Exec("DELETE FROM pane_agents WHERE pane_id=?", shortID)
	store.Exec("DELETE FROM pane_agents WHERE agent_name=?", shortID)
	store.Exec("DELETE FROM group_windows WHERE win_id=?", paneID)
	store.Exec("DELETE FROM agent_config WHERE pane_id=?", paneID)
}

// setupAuditPolicyAgent installs the skill into ~/cicy-ai/skills/,
// creates the w-6001 pane if missing, then queues the opening
// self-introduction. Safe to call on every startup.
func setupAuditPolicyAgent() {
	if err := installAuditPolicySkill(); err != nil {
		log.Printf("[audit-policy] skill install failed: %v", err)
	}
	if err := writeAuditPolicyGuidance(); err != nil {
		log.Printf("[audit-policy] guidance write failed: %v", err)
	}
	if err := ensureAuditPolicyPane(); err != nil {
		log.Printf("[audit-policy] pane bootstrap failed: %v", err)
		return
	}
	replyInChineseStartupQueue.enqueueText(auditPolicyPaneID, auditPolicyAgentType, auditPolicyIntroPrompt)
	log.Printf("[audit-policy] queued self-intro for %s", auditPolicyPaneID)
}

// installAuditPolicySkill drops the embedded skill files under
// ~/cicy-ai/skills/cicy-audit-policy/ and symlinks the cicy-policy
// CLI into ~/.local/bin/. Existing files are overwritten so a new
// build's skill content always wins.
func installAuditPolicySkill() error {
	dst := filepath.Join(cicySkillsDir, auditPolicySkillName)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dst, err)
	}

	root := "embed/audit-policy-skill"
	if err := fs.WalkDir(auditPolicySkillFS, root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(p, root)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := auditPolicySkillFS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", p, err)
		}
		mode := os.FileMode(0o644)
		// scripts/* keep +x so cicy-policy can run.
		if strings.Contains(filepath.ToSlash(rel), "/scripts/") || strings.HasPrefix(filepath.ToSlash(rel), "scripts/") {
			mode = 0o755
		}
		if err := os.WriteFile(target, body, mode); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	}); err != nil {
		return err
	}

	// Symlink cicy-policy into ~/.local/bin (best-effort).
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return nil
	}
	link := filepath.Join(binDir, "cicy-policy")
	src := filepath.Join(dst, "scripts", "cicy-policy")
	_ = os.Remove(link)
	_ = os.Symlink(src, link)
	return nil
}

// writeAuditPolicyGuidance refreshes the agent-rules file inside
// w-6001's workspace every startup (AGENTS.md for opencode/codex,
// CLAUDE.md for claude), so a newer build's prompt always wins. The
// workspace itself is created lazily by createManagedPane.
func writeAuditPolicyGuidance() error {
	ws := builtinWorkerWorkspace(auditPolicyShortPane)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return err
	}
	filename := guidanceFilenameForAgentType(auditPolicyAgentType)
	if filename == "" {
		filename = "AGENTS.md"
	}
	return os.WriteFile(filepath.Join(ws, filename), auditPolicyGuidance, 0o644)
}

// ensureAuditPolicyPane creates the w-6001 pane on first run. On
// subsequent runs the row already exists in agent_config, but the
// underlying tmux session and ttyd are NOT recreated by
// ensureBuiltinAgents (which only revives panes bound to w-10001).
// So we explicitly revive them here via startAgentFromConfig, and
// refresh title/role to let a binary upgrade rename the slot. Always
// idempotent.
func ensureAuditPolicyPane() error {
	var workspace, initScript, configJSON, agentType string
	var allowAllActions, replyInChinese, useCustomGateway bool
	var port int
	err := store.QueryRow(`
		SELECT ttyd_port, COALESCE(workspace,''), COALESCE(init_script,''),
		       COALESCE(config,'{}'), COALESCE(agent_type,''),
		       COALESCE(allow_all_actions,0), COALESCE(reply_in_chinese,0),
		       COALESCE(use_custom_gateway,0)
		FROM agent_config WHERE pane_id=?`, auditPolicyPaneID,
	).Scan(&port, &workspace, &initScript, &configJSON, &agentType,
		&allowAllActions, &replyInChinese, &useCustomGateway)

	if err == nil && port > 0 {
		// Already provisioned — refresh metadata then revive session/ttyd.
		// w-6001 is a trusted internal admin agent — it must run its own tools
		// (cicy-policy, cicy-agent msg, shell) without permission prompts, or its
		// loop stalls. Force allow_all_actions on, upgrading older rows too.
		// Global platform: w-6001 defaults to English and asks the operator to
		// pick the session language (see CLAUDE.md), so don't force Chinese.
		store.Exec(
			fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, role=?, allow_all_actions=1, reply_in_chinese=0, updated_at=%s WHERE pane_id=?", store.Now()),
			auditPolicyAgentType, auditPolicyTitle, auditPolicyRole, auditPolicyPaneID,
		)
		_, _ = allowAllActions, replyInChinese
		token := getFirstToken()
		startAgentFromConfig(auditPolicyPaneID, port, workspace, initScript, configJSON,
			auditPolicyAgentType, true, false, useCustomGateway, token)
		return nil
	}

	// First run — create from scratch.
	token := getFirstToken()
	_, err = createManagedPane(paneCreateOpts{
		session:          auditPolicyShortPane,
		title:            auditPolicyTitle,
		role:             auditPolicyRole,
		agentType:        auditPolicyAgentType,
		workspace:        builtinWorkerWorkspace(auditPolicyShortPane),
		port:             auditPolicyPort,
		token:            token,
		allowAllActions:  true,  // trusted admin agent — run its tools without prompts
		replyInChinese:   false, // global platform — English default, operator picks language
		useCustomGateway: true,
	})
	if err != nil {
		return err
	}
	log.Printf("[audit-policy] created %s (port %d, role=%s)", auditPolicyPaneID, auditPolicyPort, auditPolicyRole)
	return nil
}

// IsAuditPolicyPane reports whether a pane id (full or short) refers
// to the dedicated audit-policy / SecOps agent.
func IsAuditPolicyPane(paneID string) bool {
	short := strings.Split(paneID, ":")[0]
	return short == auditPolicyShortPane
}

// isBuiltinAgent reports whether a pane id belongs to a built-in / system
// agent. Used by /api/panes to hide built-ins from the regular agent list
// (?include_hidden=1 to bypass). Built-ins are now:
//   - w-6001 — SecOps Lead (audit advisor + security officer, merged 2.1.8)
//   - w-6002  — Team Helper
// 2.1.7's "[6001, 10000] range" check is replaced by an explicit-id check:
// w-10001+ are user workers and must NOT be hidden, and there are only two
// built-ins to enumerate.
func isBuiltinAgent(paneID string) bool {
	short := strings.Split(paneID, ":")[0]
	return short == auditPolicyShortPane || short == teamHelperShortPane
}
