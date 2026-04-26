package main

import (
	"strings"
	"testing"
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
	lines := agentBootLines("kiro-cli", false, "w-10001")
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
	if strings.Contains(script, "ANTHROPIC_BASE_URL") || strings.Contains(script, "ANTHROPIC_API_KEY") {
		t.Fatalf("kiro boot lines should not contain anthropic env: %s", script)
	}
}

func TestAgentBootLinesClaudeWritesSettingsFile(t *testing.T) {
	lines := agentBootLines("claude", false, "w-10001")
	script := strings.Join(lines, "\n")
	if !strings.Contains(script, "claude-settings.json") {
		t.Fatalf("claude boot lines missing settings file write: %s", script)
	}
	if !strings.Contains(script, `claude --settings "$WORKSPACE/claude-settings.json"`) {
		t.Fatalf("claude boot lines missing claude startup: %s", script)
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
	lines = append(lines, agentBootLines("codex", false, shortID)...)
	script := "#!/usr/bin/env bash\n\n" +
		"if [ -z \"${BASH_VERSION:-}\" ]; then\n" +
		"  _cicy_boot_pwd=\"$PWD\"\n" +
		"  bash -lc 'cd \"$1\" && source ./boot.sh' bash \"$_cicy_boot_pwd\"\n" +
		"  _cicy_boot_status=$?\n" +
		"  unset _cicy_boot_pwd\n" +
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
