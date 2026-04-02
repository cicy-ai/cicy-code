package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

type Tool struct {
	Name       string
	Command    string
	InstallCmd string
	Required   bool
	Installed  bool
}

//go:embed .tmux.conf
var embeddedTmuxConf string

//go:embed .cicy_tmux.conf
var embeddedCicyTmuxConf string

// 获取用户 shell 的 rc 文件路径
func shellRC() string {
	shell := os.Getenv("SHELL")
	if strings.Contains(shell, "zsh") {
		return "~/.zshrc"
	}
	return "~/.bashrc"
}

func extendPATH() {
	home, _ := os.UserHomeDir()
	parts := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".opencode", "bin"),
	}
	parts = append(parts, strings.Split(os.Getenv("PATH"), ":")...)
	seen := map[string]bool{}
	var filtered []string
	for _, part := range parts {
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		filtered = append(filtered, part)
	}
	os.Setenv("PATH", strings.Join(filtered, ":"))
}

func sudoPrefix() string {
	if runtime.GOOS == "darwin" {
		return ""
	}
	if os.Geteuid() == 0 {
		return ""
	}
	if _, err := exec.LookPath("sudo"); err == nil {
		return "sudo "
	}
	return ""
}

func npmGlobalInstallCmd(pkg string) string {
	return sudoPrefix() + "npm install -g " + pkg
}

func openClawInstallCmd() string {
	if isCloudRunRuntime() {
		return ""
	}
	return npmGlobalInstallCmd("openclaw@latest")
}

func packageInstallCmd(pkg string) string {
	if runtime.GOOS == "darwin" {
		return "brew install " + pkg
	}
	prefix := sudoPrefix()
	return prefix + "apt-get update && " + prefix + "apt-get install -y " + pkg
}

func nodeInstallCmd() string {
	if runtime.GOOS == "darwin" {
		return "brew install node"
	}
	prefix := sudoPrefix()
	return "curl -fsSL https://deb.nodesource.com/setup_22.x | " + prefix + "bash - && " + prefix + "apt-get install -y nodejs"
}

func codeServerInstallCmd() string {
	if runtime.GOOS == "darwin" {
		return "brew install code-server"
	}
	return "curl -fsSL https://code-server.dev/install.sh | sh"
}

func copilotInstallCmd() string {
	if runtime.GOOS == "darwin" {
		return "brew install copilot-cli"
	}
	return npmGlobalInstallCmd("@githubnext/github-copilot-cli")
}

func baseTools() []Tool {
	return []Tool{
		{"curl", "curl", packageInstallCmd("curl"), true, false},
		// {"unzip", "unzip", packageInstallCmd("unzip"), true, false},
		{"tmux", "tmux", packageInstallCmd("tmux"), true, false},
		{"git", "git", packageInstallCmd("git"), true, false},
		{"node", "node", nodeInstallCmd(), true, false},
	}
}

func checkEnvironment() []Tool {
	extendPATH()
	tools := append(baseTools(), []Tool{
		{"openclaw", "openclaw", openClawInstallCmd(), true, false},
		{"claude", "claude", npmGlobalInstallCmd("@anthropic-ai/claude-code"), true, false},
		{"codex", "codex", npmGlobalInstallCmd("@openai/codex"), true, false},
		{"opencode", "opencode", "curl -fsSL https://opencode.ai/install | bash && echo 'export PATH=\"$HOME/.opencode/bin:$PATH\"' >> " + shellRC() + " && export PATH=\"$HOME/.opencode/bin:$PATH\"", true, false},
	}...)

	fmt.Println("🔍 检查环境依赖...")
	for i := range tools {
		_, err := exec.LookPath(tools[i].Command)
		tools[i].Installed = err == nil
		status := "❌"
		if tools[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, tools[i].Name)
	}

	return tools
}

func installMissing(tools []Tool) {
	extendPATH()
	missing := []Tool{}
	for _, tool := range tools {
		if tool.Required && !tool.Installed {
			missing = append(missing, tool)
		}
	}

	if len(missing) == 0 {
		fmt.Println("✅ 所有依赖已安装")
		return
	}

	fmt.Printf("📦 安装缺失依赖 (%d 个)...\n", len(missing))

	// 必须全部安装成功才能继续
	for _, tool := range missing {
		if tool.InstallCmd == "" {
			fmt.Printf("  %s ❌ 缺失且当前环境禁止自动安装\n", tool.Name)
			fmt.Printf("❌ 环境初始化失败，请检查镜像预装内容\n")
			os.Exit(1)
		}
		fmt.Printf("  安装 %s...", tool.Name)

		cmd := exec.Command("sh", "-c", tool.InstallCmd)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf(" ❌ 失败: %v\n", err)
			fmt.Printf("❌ 环境初始化失败，请检查网络连接和权限\n")
			os.Exit(1) // 有任何失败就退出
		} else {
			fmt.Printf(" ✅ 完成\n")
		}
	}
}

func selectAgents() []string {
	selected := []string{"openclaw", "codex", "claude"}
	fmt.Println("\n🤖 默认预装 AI 工具:")
	fmt.Println("  ✅ OpenClaw")
	fmt.Println("  ✅ OpenAI Codex")
	fmt.Println("  ✅ Claude Code")
	fmt.Printf("✅ 已选择: %v\n", selected)
	return selected
}

func selectedAgentConfigs() map[string]Tool {
	return map[string]Tool{
		"openclaw": {"openclaw", "openclaw", openClawInstallCmd(), true, false},
		"claude":   {"claude", "claude", npmGlobalInstallCmd("@anthropic-ai/claude-code"), true, false},
		"codex":    {"codex", "codex", npmGlobalInstallCmd("@openai/codex"), true, false},
		"opencode": {"opencode", "opencode", fmt.Sprintf("curl -fsSL https://opencode.ai/install | bash && echo 'export PATH=\"$HOME/.opencode/bin:$PATH\"' >> %s && export PATH=\"$HOME/.opencode/bin:$PATH\"", shellRC()), true, false},
	}
}

func ensureAgentToolInstalled(agentType string) {
	agentType = strings.TrimSpace(strings.ToLower(agentType))
	if agentType == "" {
		return
	}
	switch normalizeAgentType(agentType) {
	case "openclaw", "claude", "codex", "opencode":
		return
	}
	config, exists := selectedAgentConfigs()[agentType]
	if !exists {
		return
	}
	extendPATH()
	if _, err := exec.LookPath(config.Command); err == nil {
		return
	}
	if config.InstallCmd == "" {
		log.Printf("[startup] missing agent tool in current runtime and auto-install disabled: %s", config.Name)
		return
	}
	log.Printf("[startup] installing missing agent tool: %s", config.Name)
	cmd := exec.Command("sh", "-c", config.InstallCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] failed to install %s: %v", config.Name, err)
		return
	}
	extendPATH()
	log.Printf("[startup] installed missing agent tool: %s", config.Name)
}

func installSelectedAgents(selected []string) {
	if len(selected) == 0 {
		return
	}
	fmt.Printf("\n📦 AI 工具将在 tmux pane 启动时按需安装: %v\n", selected)
}

// builtinAgents defines the built-in agents with fixed ports 10001-10004.
var builtinAgents = []struct {
	Port      int
	AgentType string
	Title     string
}{
	{10001, "openclaw", "OpenClaw"},
	{10002, "codex", "Codex"},
	{10003, "claude", "Claude"},
	{10004, "opencode", "OpenCode"},
}

func ensureWorkerIndexAtLeast(n int) {
	if n <= 0 {
		return
	}
	var current int
	_ = store.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&current)
	if current >= n {
		return
	}
	if _, err := store.Exec(
		store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}),
		"worker_index", n,
	); err != nil {
		log.Printf("[startup] failed to update worker_index to %d: %v", n, err)
	}
}

func syncWorkerIndexToBuiltinAgents() {
	maxPort := 0
	for _, ba := range builtinAgents {
		var count int
		if err := store.QueryRow("SELECT COUNT(*) FROM agent_config WHERE agent_type=?", ba.AgentType).Scan(&count); err != nil {
			log.Printf("[startup] failed to inspect builtin agent %s: %v", ba.AgentType, err)
			continue
		}
		if count > 0 && ba.Port > maxPort {
			maxPort = ba.Port
		}
	}
	ensureWorkerIndexAtLeast(maxPort)
}

func createSelectedWorkers(selected []string) {
	fmt.Println("\n🚀 创建选中的 Workers...")
	maxPort := 0
	for _, ba := range builtinAgents {
		found := false
		for _, s := range selected {
			if s == ba.AgentType {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if ba.Port > maxPort {
			maxPort = ba.Port
		}
		// Skip if already in DB
		var count int
		store.QueryRow("SELECT COUNT(*) FROM agent_config WHERE agent_type=?", ba.AgentType).Scan(&count)
		if count > 0 {
			fmt.Printf("  ⏭ %s - 已存在，跳过\n", ba.Title)
			continue
		}
		createBuiltinWorker(ba.Port, ba.AgentType, ba.Title)
	}
	ensureWorkerIndexAtLeast(maxPort)
	syncWorkerIndexToBuiltinAgents()
}

func createBuiltinWorker(port int, agentType, title string) {
	session := fmt.Sprintf("w-%d", port)
	paneID := session + ":main.0"
	home, _ := os.UserHomeDir()
	workspace := filepath.Join(home, "workers", session)
	os.MkdirAll(workspace, 0755)

	// Create tmux session
	exec.Command("tmux", "new-session", "-d", "-s", session, "-n", "main", "-c", workspace).Run()

	// Insert DB
	store.Exec(fmt.Sprintf(`INSERT INTO agent_config (pane_id, title, ttyd_port, workspace, init_script, config, role, default_model, agent_type, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,%s,%s)`, store.Now(), store.Now()),
		paneID, title, port, workspace, "", "{}", "master", "", agentType)

	// Start ttyd
	token := getFirstToken()
	if err := startInstance(paneID, port, token); err != nil {
		fmt.Printf("  ❌ %s 创建失败: %v\n", title, err)
		return
	}
	waitPort(port, 10*time.Second)
	initPaneEnv(paneEnvOpts{
		paneID:          paneID,
		configJSON:      "{}",
		workspace:       workspace,
		initScript:      "",
		agentType:       agentType,
		allowAllActions: false,
	})
	fmt.Printf("  ✅ %s (w-%d, port %d)\n", title, port, port)
}

func runSetup() {
	fmt.Println("🎯 Cicy Code 环境初始化")
	fmt.Println("=" + strings.Repeat("=", 30))

	// 1. 检查基础环境
	baseTools := baseTools()

	fmt.Println("🔍 检查基础环境...")
	for i := range baseTools {
		_, err := exec.LookPath(baseTools[i].Command)
		baseTools[i].Installed = err == nil
		status := "❌"
		if baseTools[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, baseTools[i].Name)
	}

	// 2. 安装基础环境
	installMissing(baseTools)

	// 4. 让用户选择 AI 工具
	selectedAgents := selectAgents()

	// 5. 安装选中的 AI 工具
	installSelectedAgents(selectedAgents)

	// 6. 创建对应的 workers
	createSelectedWorkers(selectedAgents)

	fmt.Println("=" + strings.Repeat("=", 30))
	fmt.Println("🎉 环境初始化完成！")
}

// runSetupWithAgents runs setup non-interactively with specified agents.
// agentList is comma-separated, e.g. "codex,claude" or "all".
func runSetupWithAgents(agentList string) {
	fmt.Println("🎯 Cicy Code 环境初始化 (non-interactive)")
	fmt.Println("=" + strings.Repeat("=", 30))

	// 1. Check & install base tools
	baseTools := baseTools()
	fmt.Println("🔍 检查基础环境...")
	for i := range baseTools {
		_, err := exec.LookPath(baseTools[i].Command)
		baseTools[i].Installed = err == nil
		status := "❌"
		if baseTools[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, baseTools[i].Name)
	}
	installMissing(baseTools)

	// 2. Parse agent list
	var selected []string
	if agentList == "all" || agentList == "ALL" {
		selected = []string{"openclaw", "codex", "claude", "opencode"}
	} else {
		has := map[string]bool{}
		for _, a := range strings.Split(agentList, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				has[a] = true
			}
		}
		has["openclaw"] = true
		has["codex"] = true
		has["claude"] = true
		for a := range has {
			selected = append(selected, a)
		}
	}
	sort.Strings(selected)

	fmt.Printf("📦 安装 agents: %v\n", selected)
	installSelectedAgents(selected)
	createSelectedWorkers(selected)

	fmt.Println("=" + strings.Repeat("=", 30))
	fmt.Println("🎉 环境初始化完成！")
}

func checkEnv() {
	extendPATH()

	fmt.Println("🔍 检查基础环境...")
	base := baseTools()
	for i := range base {
		_, err := exec.LookPath(base[i].Command)
		base[i].Installed = err == nil
		status := "❌"
		if base[i].Installed {
			status = "✅"
		}
		fmt.Printf("  %s %s\n", status, base[i].Name)
	}
	installMissing(base)
	ensureTmuxConf()
	ensureCicyTmuxConf()

	setupAIConfigs()

	var count int
	if err := store.QueryRow("SELECT COUNT(*) FROM agent_config").Scan(&count); err != nil {
		log.Fatalf("[startup] failed to query agent_config: %v", err)
	}
		if count == 0 {
			if isCloudRunRuntime() {
				// Cloud Run must never block on interactive setup.
				// Keep the default footprint minimal: only bootstrap w-10001 OpenClaw.
				createSelectedWorkers([]string{"openclaw"})
			} else if agentsFlag != "" {
				runSetupWithAgents(agentsFlag)
			} else {
			runSetup()
		}
	}

	ensureBuiltinAgents()
	ensureCodeServer()
}

func setupAIConfigs() {
	apiKey := os.Getenv("CICY_API_KEY")
	apiUrl := os.Getenv("CICY_API_URL")
	anthropicUrl := os.Getenv("CICY_ANTHROPIC_URL")
	defaultModel := os.Getenv("CICY_DEFAULT_MODEL")
	claudeModel := os.Getenv("CICY_CLAUDE_MODEL")
	codexModel := os.Getenv("CICY_CODEX_MODEL")

	if apiKey == "" {
		return
	}

	if apiUrl == "" {
		apiUrl = "http://2000.run:6543/v1"
	}
	if anthropicUrl == "" {
		anthropicUrl = "http://2000.run:6543"
	}
	if defaultModel == "" {
		defaultModel = "shibacc/claude-sonnet-4-6"
	}
	if claudeModel == "" {
		claudeModel = "opus[1m]"
	}
	if codexModel == "" {
		codexModel = "gpt-5.4"
	}

	home, _ := os.UserHomeDir()
	log.Printf("[setup] configuring AI tools with API URL: %s", apiUrl)

	scriptPath := findSetupAgentScript()
	if scriptPath == "" {
		log.Printf("[setup] setup-agent.sh not found")
	} else {
		cmd := exec.Command(scriptPath, apiKey, apiUrl, anthropicUrl, defaultModel, claudeModel, codexModel)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Printf("[setup] setup-agent.sh failed: %v", err)
		}
	}

	// Set env vars for OpenClaw worker (available via agentBootLines)
	os.Setenv("OPENCLAW_CONFIG_PATH", filepath.Join(home, ".openclaw", "openclaw.json"))
	os.Setenv("OPENAI_API_KEY", apiKey)
	os.Setenv("OPENAI_BASE_URL", apiUrl)
	os.Setenv("OPENAI_MODEL", defaultModel)
	if openClawToken := strings.TrimSpace(readOpenClawTokenFromGlobalJSON()); openClawToken != "" {
		os.Setenv("OPENCLAW_GATEWAY_TOKEN", openClawToken)
	} else if openClawToken := strings.TrimSpace(readOpenClawTokenFromConfig()); openClawToken != "" {
		os.Setenv("OPENCLAW_GATEWAY_TOKEN", openClawToken)
	}
}

func findSetupAgentScript() string {
	candidates := []string{
		"/usr/local/bin/setup-agent.sh",
	}
	if exePath, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(exePath), "setup-agent.sh"),
			filepath.Join(filepath.Dir(exePath), "..", "setup-agent.sh"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "setup-agent.sh"),
			filepath.Join(cwd, "api", "setup-agent.sh"),
		)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func ensureTmuxConf() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[startup] failed to resolve home dir for .tmux.conf: %v", err)
	}
	dst := filepath.Join(home, ".tmux.conf")

	current, err := os.ReadFile(dst)
	if err == nil && string(current) == embeddedTmuxConf {
		return
	}
	if err == nil && len(current) > 0 {
		backup := dst + ".bak"
		if writeErr := os.WriteFile(backup, current, 0644); writeErr != nil {
			log.Fatalf("[startup] failed to back up %s: %v", dst, writeErr)
		}
		log.Printf("[startup] updated %s (backup: %s)", dst, backup)
	} else {
		log.Printf("[startup] installing %s", dst)
	}
	if writeErr := os.WriteFile(dst, []byte(embeddedTmuxConf), 0644); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", dst, writeErr)
	}
}

func ensureCicyTmuxConf() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[startup] failed to resolve home dir for .cicy_tmux.conf: %v", err)
	}
	dst := filepath.Join(home, ".cicy_tmux.conf")

	current, err := os.ReadFile(dst)
	if err == nil && string(current) == embeddedCicyTmuxConf {
		return
	}
	if err == nil && len(current) > 0 {
		backup := dst + ".bak"
		if writeErr := os.WriteFile(backup, current, 0644); writeErr != nil {
			log.Fatalf("[startup] failed to back up %s: %v", dst, writeErr)
		}
		log.Printf("[startup] updated %s (backup: %s)", dst, backup)
	} else {
		log.Printf("[startup] installing %s", dst)
	}
	if writeErr := os.WriteFile(dst, []byte(embeddedCicyTmuxConf), 0644); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", dst, writeErr)
	}
}
func ensureCodeServer() {
	go ensureCodeServerAsync()
}

func ensureCodeServerAsync() {
	extendPATH()
	if _, err := exec.LookPath("code-server"); err != nil {
		fmt.Println("📦 后台安装 code-server...")
		cmd := exec.Command("sh", "-c", codeServerInstallCmd())
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if runErr := cmd.Run(); runErr != nil {
			log.Printf("[startup] failed to install code-server: %v", runErr)
			return
		}
		extendPATH()
	}

	csPort := os.Getenv("CS_PORT")
	if csPort == "" {
		csPort = "8002"
	}
	if isPortListening(mustAtoi(csPort)) {
		log.Printf("[startup] code-server already running on :%s", csPort)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[startup] failed to resolve home dir for code-server: %v", err)
		return
	}
	_ = os.Remove(filepath.Join(home, ".local", "share", "code-server", "coder.json"))
	if mkErr := os.MkdirAll(filepath.Join(home, ".cicy"), 0755); mkErr != nil {
		log.Printf("[startup] failed to create ~/.cicy: %v", mkErr)
		return
	}

	logPath := filepath.Join(home, ".cicy", "code-server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("[startup] failed to open code-server log: %v", err)
		return
	}

	cmd := exec.Command("code-server", "--bind-addr", "127.0.0.1:"+csPort, "--auth", "none", home)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PORT=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	cmd.Dir = home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if startErr := cmd.Start(); startErr != nil {
		_ = logFile.Close()
		log.Printf("[startup] failed to start code-server: %v", startErr)
		return
	}
	go func() {
		defer logFile.Close()
		if waitErr := cmd.Wait(); waitErr != nil {
			log.Printf("[startup] code-server exited: %v", waitErr)
		}
	}()

	if !waitPort(mustAtoi(csPort), 20*time.Second) {
		log.Printf("[startup] code-server did not become ready on :%s", csPort)
		return
	}
	log.Printf("[startup] code-server ready on :%s", csPort)
}

func mustAtoi(s string) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		log.Fatalf("[startup] invalid port %q", s)
	}
	return n
}

// ensureBuiltinAgents restores tmux sessions and ttyd for agents already in DB.
func ensureBuiltinAgents() {
	rows, err := store.Query("SELECT pane_id, ttyd_port, workspace, COALESCE(init_script,''), COALESCE(config,'{}'), COALESCE(agent_type,''), COALESCE(allow_all_actions,0) FROM agent_config WHERE active=1")
	if err != nil {
		return
	}
	defer rows.Close()

	token := getFirstToken()
	for rows.Next() {
		var paneID, workspace, initScript, configJSON, agentType string
		var allowAllActions bool
		var port int
		rows.Scan(&paneID, &port, &workspace, &initScript, &configJSON, &agentType, &allowAllActions)
		if paneID == "" || port == 0 {
			continue
		}

		// Ensure tmux session
		sess := strings.Split(paneID, ":")[0]
		sessionCreated := false
		if exec.Command("tmux", "has-session", "-t", sess).Run() != nil {
			if workspace == "" {
				home, _ := os.UserHomeDir()
				workspace = filepath.Join(home, "workers", sess)
			}
			ensureAgentToolInstalled(agentType)
			os.MkdirAll(workspace, 0755)
			exec.Command("tmux", "new-session", "-d", "-s", sess, "-n", "main", "-c", workspace).Run()
			log.Printf("[startup] created session %s", sess)
			sessionCreated = true
		}

		// Ensure ttyd
		if !isPortListening(port) {
			startInstance(paneID, port, token)
			//log.Printf("[startup] started %s on :%d", paneID, port)
		}
		if sessionCreated {
			initPaneEnv(paneEnvOpts{
				paneID:          paneID,
				configJSON:      configJSON,
				workspace:       workspace,
				initScript:      initScript,
				agentType:       agentType,
				allowAllActions: allowAllActions,
			})
		}
	}
}
