package main

import (
	"strings"
	"testing"
	"time"
)

func TestEnsureAgentCommandLineUsesSharedInstallHelper(t *testing.T) {
	line := ensureAgentCommandLine("codex", "Codex", "npm install -g @openai/codex@latest", "/tmp/codex.log")
	if !strings.Contains(line, "__cicy_require_command 'codex' 'Codex'") {
		t.Fatalf("ensureAgentCommandLine = %q", line)
	}
}

func TestEnsureAgentCommandLineLiveUsesLiveHelper(t *testing.T) {
	line := ensureAgentCommandLineLive("kiro-cli", "Kiro CLI", "__cicy_local_install_kiro", "/tmp/kiro.log")
	if !strings.Contains(line, "__cicy_require_command_live 'kiro-cli' 'Kiro CLI'") {
		t.Fatalf("ensureAgentCommandLineLive = %q", line)
	}
}

func TestAgentBootLinesKiroUsesLiveInstallAndStartsChat(t *testing.T) {
	lines := agentBootLines("kiro-cli", false, false, true, "w-10001", "")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, "__cicy_local_install_kiro() {") {
		t.Fatalf("kiro boot lines missing local install helper: %s", script)
	}
	if !strings.Contains(script, "__cicy_require_command_live 'kiro-cli' 'Kiro CLI'") {
		t.Fatalf("kiro boot lines missing live install helper: %s", script)
	}
	if !strings.Contains(script, "https://cli.kiro.dev/install") || !strings.Contains(script, `filename="kirocli-${arch}-linux${suffix}.zip"`) {
		t.Fatalf("kiro boot lines missing direct zip download logic: %s", script)
	}
	if !strings.Contains(script, "kiro-cli chat") {
		t.Fatalf("kiro boot lines missing chat startup: %s", script)
	}
	if strings.Contains(script, "0. 跳过登录") || strings.Contains(script, "请选择 [0/1/2]") {
		t.Fatalf("kiro boot lines should not include skip-login option: %s", script)
	}
	if strings.Contains(script, "ANTHROPIC_BASE_URL") || strings.Contains(script, "ANTHROPIC_API_KEY") {
		t.Fatalf("kiro boot lines should not contain anthropic env: %s", script)
	}
	if !strings.Contains(script, `rm -f "$WORKSPACE/.kiro/steering/reply-in-chinese.md"`) {
		t.Fatalf("kiro boot lines should remove reply-in-chinese steering file when disabled: %s", script)
	}
}

func TestAgentBootLinesKiroWritesChineseReplySteeringWhenEnabled(t *testing.T) {
	lines := agentBootLines("kiro-cli", true, true, true, "w-10001", "")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, `mkdir -p "$WORKSPACE/.kiro/steering"`) {
		t.Fatalf("kiro boot lines missing steering directory creation: %s", script)
	}
	if !strings.Contains(script, `cat > "$WORKSPACE/.kiro/steering/reply-in-chinese.md" <<'EOF'`) {
		t.Fatalf("kiro boot lines missing reply-in-chinese steering file write: %s", script)
	}
	if !strings.Contains(script, "inclusion: always") {
		t.Fatalf("kiro boot lines missing steering frontmatter: %s", script)
	}
	if !strings.Contains(script, "kiro-cli chat --trust-all-tools") {
		t.Fatalf("kiro boot lines missing trust-all-tools startup when allowAllActions=true: %s", script)
	}
}

func TestAgentBootLinesCopilotStartsYoloAndTrustsWorkspace(t *testing.T) {
	for _, allowAllActions := range []bool{false, true} {
		lines := agentBootLines("copilot", allowAllActions, false, true, "w-10005", "")
		script := strings.Join(lines, "\n")
		if !strings.Contains(script, "mkdir -p ~/.copilot") {
			t.Fatalf("copilot boot lines missing config dir creation: %s", script)
		}
		if !strings.Contains(script, "__cicy_require_command 'copilot' 'GitHub Copilot'") {
			t.Fatalf("copilot boot lines missing install helper: %s", script)
		}
		if !strings.Contains(script, "trustedFolders") {
			t.Fatalf("copilot boot lines missing trustedFolders config: %s", script)
		}
		if !strings.Contains(script, "copilot --yolo") {
			t.Fatalf("copilot boot lines missing startup command: %s", script)
		}
	}
}

func TestAgentBootLinesHermesUsesHomeLogExprWithoutNestedQuotes(t *testing.T) {
	lines := agentBootLines("hermes", false, false, true, "w-10009", "")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, `export CICY_HERMES_MODEL='claude-opus-4-7'`) {
		t.Fatalf("hermes boot lines should use current configured Hermes model: %s", script)
	}
	if !strings.Contains(script, `export UV_INDEX_URL='https://pypi.tuna.tsinghua.edu.cn/simple'`) {
		t.Fatalf("hermes boot lines missing uv mirror: %s", script)
	}
	if !strings.Contains(script, `export PIP_INDEX_URL='https://pypi.tuna.tsinghua.edu.cn/simple'`) {
		t.Fatalf("hermes boot lines missing pip mirror: %s", script)
	}
	if !strings.Contains(script, `rm -rf "$HERMES_INSTALL_DIR"`) {
		t.Fatalf("hermes boot lines should remove partial install dir before reinstall: %s", script)
	}
	if !strings.Contains(script, `| tee "$install_log"`) {
		t.Fatalf("hermes boot lines should stream install output to terminal: %s", script)
	}
	if !strings.Contains(script, `cicy_clone_hermes() {`) {
		t.Fatalf("hermes boot lines should inject tarball download helper: %s", script)
	}
	if !strings.Contains(script, `https://gh-proxy.com/https://codeload.github.com/NousResearch/hermes-agent/tar.gz/refs/heads/${branch}`) {
		t.Fatalf("hermes boot lines should download source tarball through gh proxy: %s", script)
	}
	if !strings.Contains(script, `if cicy_clone_hermes "$BRANCH" "$INSTALL_DIR"; then`) {
		t.Fatalf("hermes boot lines should replace https git clone with tarball flow: %s", script)
	}
	if !strings.Contains(script, `install_log="$HOME/.cicy/hermes-install-w-10009.log"`) {
		t.Fatalf("hermes boot lines missing expected install log expression: %s", script)
	}
	if strings.Contains(script, `install_log='"$HOME/.cicy/hermes-install-w-10009.log"'`) {
		t.Fatalf("hermes boot lines should not double-quote install log expression: %s", script)
	}
	if !strings.Contains(script, `https://gh-proxy.com/https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh`) {
		t.Fatalf("hermes boot lines should download install script through gh proxy: %s", script)
	}
}

func TestAgentBootLinesOpenCodeDoesNotUseUnsupportedPermissionFlag(t *testing.T) {
	lines := agentBootLines("opencode", true, true, true, "w-10004", "")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, `"permission": "allow"`) {
		t.Fatalf("opencode boot lines missing permission allow config: %s", script)
	}
	if !strings.Contains(script, `"instructions": ["${WORKSPACE}/.opencode/reply-in-chinese.md"]`) {
		t.Fatalf("opencode boot lines missing reply-in-chinese instructions config: %s", script)
	}
	if !strings.Contains(script, `cat > "$WORKSPACE/.opencode/reply-in-chinese.md" <<'EOF'`) {
		t.Fatalf("opencode boot lines missing reply-in-chinese instructions file write: %s", script)
	}
	if !strings.Contains(script, `XDG_CONFIG_HOME="$OPENCODE_CONFIG_ROOT" OPENCODE_CONFIG="$OPENCODE_CONFIG" opencode`) {
		t.Fatalf("opencode boot lines missing startup command: %s", script)
	}
	if strings.Contains(script, "--dangerously-skip-permissions") {
		t.Fatalf("opencode boot lines still contain unsupported permission flag: %s", script)
	}
}

func TestAgentBootLinesOpenCodeWithoutChineseReplyRemovesInstructionsFile(t *testing.T) {
	lines := agentBootLines("opencode", true, false, true, "w-10004", "")
	script := strings.Join(lines, "\n")
	if strings.Contains(script, `"instructions": ["${WORKSPACE}/.opencode/reply-in-chinese.md"]`) {
		t.Fatalf("opencode boot lines should not include reply-in-chinese instructions config: %s", script)
	}
	if !strings.Contains(script, `rm -f "$WORKSPACE/.opencode/reply-in-chinese.md"`) {
		t.Fatalf("opencode boot lines should remove reply-in-chinese instructions file when disabled: %s", script)
	}
}

func TestAgentBootLinesClaudeWritesSettingsFile(t *testing.T) {
	lines := agentBootLines("claude", false, false, false, "w-10001", "")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, `mkdir -p "$WORKSPACE/.cicy"`) {
		t.Fatalf("claude boot lines missing workspace runtime mkdir: %s", script)
	}
	if !strings.Contains(script, `cat > "$WORKSPACE/.cicy/claude-settings.json" <<EOF`) {
		t.Fatalf("claude boot lines missing settings file write: %s", script)
	}
	if !strings.Contains(script, `"ANTHROPIC_BASE_URL": "http://127.0.0.1:8008/api/ai-gateway/anthropic/${X_AGENT_SHORT_ID}"`) {
		t.Fatalf("claude boot lines missing anthropic base url template in settings content: %s", script)
	}
	if !strings.Contains(script, `claude --settings "$WORKSPACE/.cicy/claude-settings.json"`) {
		t.Fatalf("claude boot lines missing claude startup: %s", script)
	}
	if strings.Contains(script, `claude --bare --settings "$WORKSPACE/.cicy/claude-settings.json"`) {
		t.Fatalf("claude boot lines should not use --bare: %s", script)
	}
}

func TestAgentBootLinesClaudeUsesPaneDefaultModel(t *testing.T) {
	lines := agentBootLines("claude", false, false, false, "w-10028", "claude-opus-4-6")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, `"model": "claude-opus-4-6"`) {
		t.Fatalf("claude boot lines should use pane default_model: %s", script)
	}
	if strings.Contains(script, `"model": "claude-opus-4-7"`) {
		t.Fatalf("claude boot lines should not fall back to global 4.7 when pane default_model is set: %s", script)
	}
}

func TestAgentBootLinesClaudeWritesSettingsFileWithoutGatewayAuth(t *testing.T) {
	lines := agentBootLines("claude", false, false, true, "w-10001", "")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, `cat > "$WORKSPACE/.cicy/claude-settings.json" <<EOF`) {
		t.Fatalf("claude login-mode boot lines missing settings file write: %s", script)
	}
	if strings.Contains(script, `"ANTHROPIC_AUTH_TOKEN": "cicy-local-gateway"`) {
		t.Fatalf("claude login-mode boot lines should not inject anthropic auth token: %s", script)
	}
	if strings.Contains(script, `"ANTHROPIC_BASE_URL": "http://127.0.0.1:8008/api/ai-gateway/anthropic/${X_AGENT_SHORT_ID}"`) {
		t.Fatalf("claude login-mode boot lines should not inject anthropic base url: %s", script)
	}
	if !strings.Contains(script, `  "model": "`) {
		t.Fatalf("claude login-mode boot lines missing model field: %s", script)
	}
	if !strings.Contains(script, `claude --settings "$WORKSPACE/.cicy/claude-settings.json"`) {
		t.Fatalf("claude login-mode boot lines missing claude startup: %s", script)
	}
}

func TestAgentBootLinesClaudeAllowAllActionsUsesDangerousSkipPermissions(t *testing.T) {
	claudeScript := strings.Join(agentBootLines("claude", true, false, true, "w-10001", ""), "\n")
	if !strings.Contains(claudeScript, `claude --settings "$WORKSPACE/.cicy/claude-settings.json" --dangerously-skip-permissions`) {
		t.Fatalf("claude allow-all boot lines missing dangerous skip flag: %s", claudeScript)
	}
	if strings.Contains(claudeScript, "--permission-mode bypassPermissions") {
		t.Fatalf("claude allow-all boot lines should not use permission-mode bypassPermissions: %s", claudeScript)
	}

	cicyClaudeScript := strings.Join(agentBootLines("cicy-claude", true, false, true, "w-10001", ""), "\n")
	if !strings.Contains(cicyClaudeScript, `cicy-claude --bare --settings "$WORKSPACE/.cicy/cicy-settings.json" --dangerously-skip-permissions`) {
		t.Fatalf("cicy-claude allow-all boot lines missing dangerous skip flag: %s", cicyClaudeScript)
	}
	if strings.Contains(cicyClaudeScript, "--permission-mode bypassPermissions") {
		t.Fatalf("cicy-claude allow-all boot lines should not use permission-mode bypassPermissions: %s", cicyClaudeScript)
	}
}

func TestDetectClaudePromptStageOnlyAcceptsBypassWhenExplicitChoiceShown(t *testing.T) {
	choicePrompt := `
WARNING: Claude Code running in Bypass Permissions mode

❯ 1. No, exit
  2. Yes, I accept
`
	if got := detectClaudePromptStage(choicePrompt, true); got != claudeStageBypassChoice {
		t.Fatalf("detectClaudePromptStage(choicePrompt, true) = %q, want %q", got, claudeStageBypassChoice)
	}
	if got := detectClaudePromptStage(choicePrompt, false); got != claudeStageNone {
		t.Fatalf("detectClaudePromptStage(choicePrompt, false) = %q, want %q", got, claudeStageNone)
	}

	plainPrompt := "w-10001 $"
	if got := detectClaudePromptStage(plainPrompt, true); got != claudeStageNone {
		t.Fatalf("detectClaudePromptStage(plainPrompt, true) = %q, want %q", got, claudeStageNone)
	}
}

func TestDetectClaudePromptStageStripsTerminalControlNoise(t *testing.T) {
	noisyPrompt := "\x1b[?25lWARNING: Claude Code running in Bypass Permissions mode\n\n❯ 1. No, exit\n  2. Yes, I accept\n\nEnter to confirm · Esc to cancel\n\x1b[?1;2c"
	if got := detectClaudePromptStage(noisyPrompt, true); got != claudeStageBypassChoice {
		t.Fatalf("detectClaudePromptStage(noisyPrompt, true) = %q, want %q", got, claudeStageBypassChoice)
	}
}

func TestIsClaudeBypassAcceptSelected(t *testing.T) {
	notSelected := `
WARNING: Claude Code running in Bypass Permissions mode

❯ 1. No, exit
  2. Yes, I accept
`
	if isClaudeBypassAcceptSelected(notSelected) {
		t.Fatalf("expected accept option to be unselected")
	}

	selected := `
WARNING: Claude Code running in Bypass Permissions mode

  1. No, exit
❯ 2. Yes, I accept
`
	if !isClaudeBypassAcceptSelected(selected) {
		t.Fatalf("expected accept option to be selected")
	}
}

func TestNextClaudeAutoConfirmActionBypassFlow(t *testing.T) {
	choicePrompt := `
WARNING: Claude Code running in Bypass Permissions mode

❯ 1. No, exit
  2. Yes, I accept
`
	selectedPrompt := `
WARNING: Claude Code running in Bypass Permissions mode

  1. No, exit
❯ 2. Yes, I accept
`
	confirmPrompt := `
WARNING: Claude Code running in Bypass Permissions mode

Enter to confirm · Esc to cancel
`
	readyPrompt := `
Welcome to Opus 4.7 xhigh! · /effort to tune speed vs. intelligence

❯ 
`

	var state claudeAutoConfirmState
	now := time.Unix(100, 0)
	if got := nextClaudeAutoConfirmAction(&state, choicePrompt, "claude", true, now); got != claudeActionDown {
		t.Fatalf("choice action = %q, want %q", got, claudeActionDown)
	}
	now = now.Add(260 * time.Millisecond)
	if got := nextClaudeAutoConfirmAction(&state, selectedPrompt, "claude", true, now); got != claudeActionEnter {
		t.Fatalf("selected action = %q, want %q", got, claudeActionEnter)
	}
	now = now.Add(260 * time.Millisecond)
	if got := nextClaudeAutoConfirmAction(&state, confirmPrompt, "claude", true, now); got != claudeActionEnter {
		t.Fatalf("confirm action = %q, want %q", got, claudeActionEnter)
	}
	now = now.Add(260 * time.Millisecond)
	if got := nextClaudeAutoConfirmAction(&state, readyPrompt, "claude", true, now); got != claudeActionReady {
		t.Fatalf("ready action = %q, want %q", got, claudeActionReady)
	}
}

func TestNextClaudeAutoConfirmActionStopsWhenClaudeExits(t *testing.T) {
	var state claudeAutoConfirmState
	now := time.Unix(200, 0)
	choicePrompt := `
WARNING: Claude Code running in Bypass Permissions mode

❯ 1. No, exit
  2. Yes, I accept
`
	if got := nextClaudeAutoConfirmAction(&state, choicePrompt, "claude", true, now); got != claudeActionDown {
		t.Fatalf("initial action = %q, want %q", got, claudeActionDown)
	}
	now = now.Add(time.Second)
	if got := nextClaudeAutoConfirmAction(&state, "w-10001 $", "bash", true, now); got != claudeActionStop {
		t.Fatalf("exit action = %q, want %q", got, claudeActionStop)
	}
}

func TestNextClaudeAutoConfirmActionHonorsBypassCooldown(t *testing.T) {
	var state claudeAutoConfirmState
	now := time.Unix(300, 0)
	choicePrompt := `
WARNING: Claude Code running in Bypass Permissions mode

❯ 1. No, exit
  2. Yes, I accept
`
	if got := nextClaudeAutoConfirmAction(&state, choicePrompt, "claude", true, now); got != claudeActionDown {
		t.Fatalf("initial action = %q, want %q", got, claudeActionDown)
	}
	if got := nextClaudeAutoConfirmAction(&state, choicePrompt, "claude", true, now.Add(10*time.Millisecond)); got != claudeActionNone {
		t.Fatalf("cooldown action = %q, want %q", got, claudeActionNone)
	}
	if got := nextClaudeAutoConfirmAction(&state, choicePrompt, "claude", true, now.Add(100*time.Millisecond)); got != claudeActionNone {
		t.Fatalf("mid-cooldown action = %q, want %q", got, claudeActionNone)
	}
	if got := nextClaudeAutoConfirmAction(&state, choicePrompt, "claude", true, now.Add(260*time.Millisecond)); got != claudeActionDown {
		t.Fatalf("post-cooldown action = %q, want %q", got, claudeActionDown)
	}
}

func TestInitPaneEnvScriptBootstrapsCicyTmuxConf(t *testing.T) {
	aiPort := "8008"
	t.Setenv("PORT", aiPort)
	pid := "w-10001:main.0"
	shortID := "w-10001"
	agentNorm := normalizeAgentType("codex")
	sessionEnv := map[string]string{
		"CICY_AGENT_TYPE": agentNorm,
	}
	lines := []string{
		"touch ~/.cicy_tmux.conf",
		"source ~/.cicy_tmux.conf",
		"cd '/tmp/workspace'",
		"export WORKSPACE='/tmp/workspace'",
	}
	for key, value := range sessionEnv {
		if value != "" {
			lines = append(lines, "export "+key+"='"+value+"'")
		}
	}
	lines = append(lines, "export X_AGENT_ID='"+pid+"'", "export X_AGENT_SHORT_ID='"+shortID+"'")
	lines = append(lines, agentBootLines("codex", false, false, true, shortID, "")...)
	script := "#!/usr/bin/env bash\n\n" +
		"if [ -z \"${BASH_VERSION:-}\" ]; then\n" +
		"  _cicy_boot_script_path=\"$PWD/.cicy/boot.sh\"\n" +
		"  bash \"$_cicy_boot_script_path\"\n" +
		"  _cicy_boot_status=$?\n" +
		"  unset _cicy_boot_script_path\n" +
		"  return \"$_cicy_boot_status\" 2>/dev/null || exit \"$_cicy_boot_status\"\n" +
		"fi\n\n" +
		strings.Join(lines, "\n") + "\n"
	if !strings.Contains(script, "source ~/.cicy_tmux.conf") {
		t.Fatalf("boot script missing .cicy_tmux.conf bootstrap: %s", script)
	}
	if !strings.Contains(script, "export X_AGENT_ID='w-10001:main.0'") {
		t.Fatalf("boot script missing X_AGENT_ID: %s", script)
	}
	if !strings.Contains(script, "__cicy_require_command 'codex' 'Codex'") {
		t.Fatalf("boot script missing codex install helper: %s", script)
	}
}
