package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Native (Windows ConPTY) agent boot. The pane shell is cmd.exe (native) — it
// can launch the native node CLIs (claude/codex/opencode) that cygwin bash
// deadlocks on under our ConPTY. So we DON'T `source boot.sh`; instead Go writes
// the agent's config files and sends the native launch command. These functions
// are only reached when nativePtyActive() (always false off Windows).

// toWinPath normalizes a possibly-POSIX/MSYS path (/c/Users/x) to a Windows
// path (C:\Users\x) so native programs (claude) and Go file ops agree.
func toWinPath(p string) string {
	if len(p) >= 2 && p[0] == '/' &&
		((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) &&
		(len(p) == 2 || p[2] == '/') {
		p = strings.ToUpper(string(p[1])) + ":" + p[2:]
	}
	return strings.ReplaceAll(p, "/", `\`)
}

// toClaudeKey produces the project key Claude Code uses in ~/.claude.json: the
// cwd with a drive letter and FORWARD slashes (e.g. C:/Users/x/w-1013) — NOT
// backslashes. Matching this exactly is what lets the trust pre-write suppress
// the "trust this folder?" dialog.
func toClaudeKey(p string) string {
	if len(p) >= 2 && p[0] == '/' &&
		((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) &&
		(len(p) == 2 || p[2] == '/') {
		p = strings.ToUpper(string(p[1])) + ":" + p[2:]
	}
	return strings.ReplaceAll(p, `\`, "/")
}

// nativeBoot dispatches the native (cmd.exe, no bash/boot.sh) boot per agent
// type. Returns true if it handled the type; false => caller falls back (which
// won't work under cmd — those types are TODO: codex/opencode/gemini/…).
func nativeBoot(opts paneEnvOpts) bool {
	switch normalizeAgentType(opts.agentType) {
	case "claude", "cicy-claude":
		return nativeClaudeBoot(opts)
	case "cicy":
		return nativeCicyBoot(opts)
	case "opencode":
		return nativeOpencodeBoot(opts)
	case "codex":
		return nativeCodexBoot(opts)
	}
	return false
}

// nativeCodexBoot boots codex natively. The launch has codex `-c key=value` TOML
// overrides whose string values need embedded quotes — nightmarish through
// send-keys, so Go writes a boot.bat with exact quoting and we just `call` it.
// (The optional /model catalog is skipped for v1; codex still launches.)
func nativeCodexBoot(opts paneEnvOpts) bool {
	if normalizeAgentType(opts.agentType) != "codex" {
		return false
	}
	ws := toWinPath(strings.TrimSpace(opts.workspace))
	pid := opts.paneID
	if ws == "" || pid == "" {
		return false
	}
	shortID := strings.Split(pid, ":")[0]
	dir := ws + `\.cicy`
	_ = os.MkdirAll(dir, 0755)

	bat := "@echo off\r\n" +
		"set X_AGENT_ID=" + pid + "\r\n" +
		"set X_AGENT_SHORT_ID=" + shortID + "\r\n" +
		"set CICY_AGENT_TYPE=codex\r\n" +
		"set WORKSPACE=" + ws + "\r\n"
	if opts.useCustomGateway {
		baseURL := openAIRuntimeBaseURL(shortID)
		model := resolveCodexStartupModel(opts.defaultModel, loadRuntimeAIConfig(), shortID)
		bypass := ""
		if opts.allowAllActions {
			bypass = " --dangerously-bypass-approvals-and-sandbox"
		}
		bat += "set OPENAI_API_KEY=cicy-local-gateway\r\n" +
			`codex -m "` + model + `" -c "model_provider=custom" -c "model_providers.custom.name=cicy-local" -c "model_providers.custom.base_url=\"` + baseURL + `\""` + bypass + "\r\n"
	} else {
		bat += "codex\r\n"
	}
	_ = os.WriteFile(dir+`\codex-boot.bat`, []byte(bat), 0644)

	waitForCmdReady(pid)
	runTmux("send-keys", "-t", pid, `call "`+dir+`\codex-boot.bat"`, "Enter")
	return true
}

// nativeOpencodeBoot boots opencode natively: Go writes opencode.json (provider
// + model) and the persisted model.json, then sends the env + launch via cmd.
func nativeOpencodeBoot(opts paneEnvOpts) bool {
	if normalizeAgentType(opts.agentType) != "opencode" {
		return false
	}
	ws := toWinPath(strings.TrimSpace(opts.workspace))
	pid := opts.paneID
	if ws == "" || pid == "" {
		return false
	}
	shortID := strings.Split(pid, ":")[0]
	aiCfg := loadRuntimeAIConfig()

	xdgState := ws + `\.opencode\xdg\state`
	_ = os.MkdirAll(xdgState+`\opencode`, 0755)
	_ = os.MkdirAll(ws+`\.opencode`, 0755)

	env := []string{
		"set X_AGENT_ID=" + pid, "set X_AGENT_SHORT_ID=" + shortID,
		"set CICY_AGENT_TYPE=opencode", "set WORKSPACE=" + ws,
		"set XDG_STATE_HOME=" + xdgState,
	}

	if opts.useCustomGateway {
		model := resolveOpencodeStartupModel(opts.defaultModel, aiCfg, shortID)
		modelsBlock := buildOpencodeModelsBlock(opencodeActiveProviderModels(shortID, model))
		topModelField := ""
		if model != "" {
			b, _ := json.Marshal("cicyai/" + model)
			topModelField = `,"model":` + string(b)
		}
		var providerBlock string
		if opencodeActiveProtocol(shortID) == "anthropic" {
			bj, _ := json.Marshal(anthropicRuntimeBaseURL(shortID) + "/v1")
			providerBlock = `"cicyai":{"npm":"@ai-sdk/anthropic","api":"anthropic","name":"cicyAi Gateway","options":{"baseURL":` + string(bj) + `,"apiKey":"cicy-local-gateway"},"models":` + modelsBlock + `}`
		} else {
			bj, _ := json.Marshal(openAIRuntimeBaseURL(shortID) + "/v1")
			providerBlock = `"cicyai":{"npm":"@ai-sdk/openai-compatible","api":"openai","name":"cicyAi Gateway","options":{"baseURL":` + string(bj) + `},"models":` + modelsBlock + `}`
		}
		instr := ""
		if opts.replyInChinese {
			_ = os.WriteFile(ws+`\.opencode\reply-in-chinese.md`, []byte("Always reply in Chinese unless the user explicitly asks for another language.\nKeep code, commands, file paths, environment variables, API identifiers, and other literal tokens unchanged when accuracy matters.\n"), 0644)
			ij, _ := json.Marshal(strings.ReplaceAll(ws, `\`, "/") + "/.opencode/reply-in-chinese.md")
			instr = `,"instructions":[` + string(ij) + `]`
		} else {
			_ = os.Remove(ws + `\.opencode\reply-in-chinese.md`)
		}
		configJSON := `{"$schema":"https://opencode.ai/config.json","permission":"allow"` + instr + topModelField + `,"provider":{` + providerBlock + `}}`
		_ = os.WriteFile(ws+`\.opencode\opencode.json`, []byte(configJSON), 0644)
		if model != "" {
			mid, _ := json.Marshal(model)
			_ = os.WriteFile(xdgState+`\opencode\model.json`, []byte(`{"recent":[{"providerID":"cicyai","modelID":`+string(mid)+`}],"favorite":[],"variant":{}}`), 0644)
		}
		env = append(env, `set OPENCODE_CONFIG=`+ws+`\.opencode\opencode.json`)
	} else {
		_ = os.Remove(ws + `\.opencode\opencode.json`)
	}

	waitForCmdReady(pid)
	runTmux("send-keys", "-t", pid, strings.Join(env, "&"), "Enter")
	runTmux("send-keys", "-t", pid, "opencode", "Enter")
	return true
}

// nativeCicyBoot boots the dispatcher pane: it just runs the in-binary REPL
// (cicy-repl) — a native exe, so cmd.exe launches it fine.
func nativeCicyBoot(opts paneEnvOpts) bool {
	pid := opts.paneID
	if pid == "" {
		return false
	}
	shortID := strings.Split(pid, ":")[0]
	exePath, err := os.Executable()
	if err != nil || strings.TrimSpace(exePath) == "" {
		exePath = "cicy-code"
	}
	waitForCmdReady(pid)
	if ws := toWinPath(strings.TrimSpace(opts.workspace)); ws != "" {
		runTmux("send-keys", "-t", pid, "set X_AGENT_ID="+pid+"&set X_AGENT_SHORT_ID="+shortID+"&set CICY_AGENT_TYPE=cicy&set WORKSPACE="+ws, "Enter")
	}
	runTmux("send-keys", "-t", pid, `"`+exePath+`" cicy-repl --agent `+shortID, "Enter")
	return true
}

// nativeClaudeBoot boots a claude/cicy-claude agent natively under cmd.exe.
// Returns true if it handled the agent type (so initPaneEnv skips boot.sh).
func nativeClaudeBoot(opts paneEnvOpts) bool {
	norm := normalizeAgentType(opts.agentType)
	if norm != "claude" && norm != "cicy-claude" {
		return false
	}
	ws := toWinPath(strings.TrimSpace(opts.workspace))
	pid := opts.paneID
	if ws == "" || pid == "" {
		return false
	}
	shortID := strings.Split(pid, ":")[0]

	settingsFile, launchPrefix := "claude-settings.json", "claude"
	if norm == "cicy-claude" {
		settingsFile, launchPrefix = "cicy-settings.json", "cicy-claude --bare"
	}

	// 1) auto-trust the workspace (claude's cwd under cmd is the Win path).
	writeClaudeTrustNative(ws)

	// 2) gateway settings file (behind the local gateway).
	if opts.useCustomGateway {
		model := resolveClaudeStartupModel(opts.defaultModel, loadRuntimeAIConfig(), shortID)
		writeClaudeSettingsNative(ws, settingsFile, shortID, model)
	}

	// 3) wait for the cmd prompt, set the pane env the agent's tools rely on,
	//    then send the launch command.
	waitForCmdReady(pid)
	setEnv := "set X_AGENT_ID=" + pid + "&set X_AGENT_SHORT_ID=" + shortID +
		"&set CICY_AGENT_TYPE=" + norm + "&set WORKSPACE=" + ws
	runTmux("send-keys", "-t", pid, setEnv, "Enter")

	launch := launchPrefix
	if opts.useCustomGateway {
		launch += ` --settings "` + ws + `\.cicy\` + settingsFile + `"`
	}
	if opts.allowAllActions {
		launch += " --dangerously-skip-permissions"
	}
	runTmux("send-keys", "-t", pid, launch, "Enter")
	if norm == "claude" || norm == "cicy-claude" {
		autoConfirmClaudeStartup(pid, opts.allowAllActions)
	}
	return true
}

// writeClaudeTrustNative merges hasTrustDialogAccepted into ~/.claude.json for
// the workspace key so claude skips its "trust this folder?" prompt.
func writeClaudeTrustNative(workspace string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".claude.json")
	m := map[string]interface{}{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	projects, _ := m["projects"].(map[string]interface{})
	if projects == nil {
		projects = map[string]interface{}{}
	}
	// Claude keys by cwd with FORWARD slashes (C:/Users/..). Write that form
	// (and the backslash form too) so the trust dialog never appears.
	keys := map[string]bool{toClaudeKey(workspace): true, toWinPath(workspace): true}
	for key := range keys {
		p, _ := projects[key].(map[string]interface{})
		if p == nil {
			p = map[string]interface{}{}
		}
		p["hasTrustDialogAccepted"] = true
		p["hasCompletedProjectOnboarding"] = true
		projects[key] = p
	}
	m["projects"] = projects
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(path, b, 0644)
	}
}

// writeClaudeSettingsNative writes <ws>\.cicy\<file> with the local-gateway env
// + model — the same JSON the bash boot heredoc'd.
func writeClaudeSettingsNative(workspace, file, shortID, model string) {
	dir := filepath.Join(workspace, ".cicy")
	_ = os.MkdirAll(dir, 0755)
	settings := map[string]interface{}{
		"env": map[string]interface{}{
			"ANTHROPIC_AUTH_TOKEN": "cicy-local-gateway",
			"ANTHROPIC_BASE_URL":   "http://127.0.0.1:8008/api/ai-gateway/anthropic/" + shortID,
		},
		"model": model,
	}
	if b, err := json.MarshalIndent(settings, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dir, file), b, 0644)
	}
}

// waitForCmdReady waits until the pane's cmd.exe shows its prompt (ends ">")
// so the launch keystrokes land at a prompt rather than mid-startup.
func waitForCmdReady(pid string) bool {
	deadline := time.Now().Add(shellPromptTimeoutForRuntime())
	for time.Now().Before(deadline) {
		if out, err := runTmux("capture-pane", "-t", pid, "-p", "-S", "-40"); err == nil {
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
			if len(lines) > 0 && strings.HasSuffix(strings.TrimSpace(lines[len(lines)-1]), ">") {
				return true
			}
		}
		time.Sleep(shellPromptPollInterval)
	}
	return false
}
