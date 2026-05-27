package main

// audit_policy_worker.go reserves w-10000 as a dedicated "Audit Policy
// Admin" agent — created on first startup, hidden from the regular
// agent list, and surfaced inside the Audit Dashboard "Assistant" tab.
//
// This is a singleton role:
//   - fixed pane_id     "w-10000:main.0"
//   - fixed ttyd port   10000
//   - fixed workspace   ~/cicy-ai/workers/w-10000
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
	auditPolicyPaneID     = "w-10000:main.0"
	auditPolicyShortPane  = "w-10000"
	auditPolicyPort       = 10000
	auditPolicyAgentType  = "opencode"
	auditPolicyRole       = "audit-policy-admin"
	auditPolicyTitle      = "Audit Policy Admin"
	auditPolicySkillName  = "cicy-audit-policy"
)

//go:embed embed/audit-policy-skill
var auditPolicySkillFS embed.FS

//go:embed embed/audit-policy-CLAUDE.md
var auditPolicyGuidance []byte

// auditPolicyIntroPrompt is the message we feed the agent the first time
// w-10000's Claude CLI is interactive. Tells it to follow the
// "启动自我介绍" section in CLAUDE.md without waiting for the human to
// type. The queue is in-memory only, so this fires once per cicy-code
// process — restart the container = the user sees the intro again
// (intentional; fresh dashboards should feel guided).
const auditPolicyIntroPrompt = "请按 CLAUDE.md 中「启动自我介绍」段落用中文先做一次自我介绍,主动开口,不要等我说话。"

// setupAuditPolicyAgent installs the skill into ~/cicy-ai/skills/,
// creates the w-10000 pane if missing, then queues the opening
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
// w-10000's workspace every startup (AGENTS.md for opencode/codex,
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

// ensureAuditPolicyPane creates the w-10000 pane on first run. On
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
		store.Exec(
			fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, role=?, updated_at=%s WHERE pane_id=?", store.Now()),
			auditPolicyAgentType, auditPolicyTitle, auditPolicyRole, auditPolicyPaneID,
		)
		token := getFirstToken()
		startAgentFromConfig(auditPolicyPaneID, port, workspace, initScript, configJSON,
			auditPolicyAgentType, allowAllActions, replyInChinese, useCustomGateway, token)
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
		allowAllActions:  false,
		replyInChinese:   true,
		useCustomGateway: true,
	})
	if err != nil {
		return err
	}
	log.Printf("[audit-policy] created %s (port %d, role=%s)", auditPolicyPaneID, auditPolicyPort, auditPolicyRole)
	return nil
}

// IsAuditPolicyPane reports whether a pane id (full or short) refers
// to the dedicated audit-policy agent. Used by /api/panes to hide it
// from the regular agent list.
func IsAuditPolicyPane(paneID string) bool {
	short := strings.Split(paneID, ":")[0]
	return short == auditPolicyShortPane
}
