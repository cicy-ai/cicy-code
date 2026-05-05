package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

//go:embed resources/cicy-code-server-bridge-0.0.1.vsix
var embeddedCodeServerBridgeVSIX []byte

var cicySkillsInstallOnce sync.Once

const (
	cicySkillsRepoURL        = "https://github.com/cicy-ai/cicy-skills"
	cicySkillsRepoAPIURL     = "https://api.github.com/repos/cicy-ai/cicy-skills"
	cicySkillsDefaultVersion = "v0.1.4"
	cicySkillsDefaultGHProxy = "https://gh-proxy.com/"
	cicySkillsNPMMirror      = "https://registry.npmmirror.com"
	cicyDefaultPyPIMirror    = "https://pypi.tuna.tsinghua.edu.cn/simple"
	cicySkillsInstallLogFile = "cicy-skills-install.log"
)

// 获取用户 shell 的 rc 文件路径
func shellRC() string {
	shell := os.Getenv("SHELL")
	if strings.Contains(shell, "zsh") {
		return "~/.zshrc"
	}
	return "~/.bashrc"
}

func expandHomePath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func extendPATH() {
	home, _ := os.UserHomeDir()
	parts := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		filepath.Join(home, ".npm-global", "bin"),
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
	return `mkdir -p "$HOME/.npm-global/bin" "$HOME/.npm-global/lib" "$HOME/.npm-global/lib/node_modules" && npm install -g --prefix "$HOME/.npm-global" ` + pkg
}

func preinstalledRuntimeInstallCmd(cmd string) string {
	if isContainerRuntime() {
		return ""
	}
	return cmd
}

func openClawInstallCmd() string {
	return npmGlobalInstallCmd("openclaw@latest")
}

func claudeInstallCmd() string {
	return npmGlobalInstallCmd("@anthropic-ai/claude-code@latest")
}

func codexInstallCmd() string {
	return npmGlobalInstallCmd("@openai/codex@latest")
}

func opencodeInstallCmd() string {
	return npmGlobalInstallCmd("opencode-ai@latest")
}

func cicyInstallCmd() string {
	return npmGlobalInstallCmd("cicy-claude@latest")
}

func kiroCliInstallCmd() string {
	return "__cicy_install_kiro_cli_with_progress"
}

func cicyWechatInstallCmd() string {
	return npmGlobalInstallCmd("cicy-wechat@latest")
}

func cicyFeishuInstallCmd() string {
	return npmGlobalInstallCmd("cicy-feishu@latest")
}

func packageInstallCmd(pkg string) string {
	if runtime.GOOS == "darwin" {
		return "brew install " + pkg
	}
	prefix := sudoPrefix()
	return prefix + "apt-get update && " + prefix + "apt-get install -y " + pkg
}

func opensshInstallCmd() string {
	if runtime.GOOS == "darwin" {
		return "brew install openssh"
	}
	prefix := sudoPrefix()
	return prefix + "apt-get update && " + prefix + "apt-get install -y openssh-client"
}

func nodeInstallCmd() string {
	if runtime.GOOS == "darwin" {
		return "brew install node"
	}
	prefix := sudoPrefix()
	return "curl -fsSL https://deb.nodesource.com/setup_22.x | " + prefix + "bash - && " + prefix + "apt-get install -y nodejs"
}

func codeServerInstallCmd() string {
	if isContainerRuntime() {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return "brew install code-server"
	}
	return "curl -fsSL https://code-server.dev/install.sh | sh"
}

func copilotInstallCmd() string {
	return npmGlobalInstallCmd("@github/copilot@latest")
}

func baseTools() []Tool {
	return []Tool{
		{"curl", "curl", packageInstallCmd("curl"), true, false},
		{"unzip", "unzip", packageInstallCmd("unzip"), true, false},
		{"jq", "jq", packageInstallCmd("jq"), true, false},
		{"ssh-keygen", "ssh-keygen", opensshInstallCmd(), true, false},
		{"tmux", "tmux", packageInstallCmd("tmux"), true, false},
		{"git", "git", packageInstallCmd("git"), true, false},
		{"node", "node", nodeInstallCmd(), true, false},
	}
}

func checkEnvironment() []Tool {
	extendPATH()
	tools := append(baseTools(), []Tool{
		{"openclaw", "openclaw", openClawInstallCmd(), true, false},
		{"claude", "claude", claudeInstallCmd(), true, false},
		{"codex", "codex", codexInstallCmd(), true, false},
		{"opencode", "opencode", opencodeInstallCmd(), true, false},
		{"hermes", "hermes", hermesInstallCmd(), true, false},
		{"code-server", "code-server", codeServerInstallCmd(), true, false},
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
	selected := []string{"claude"}
	fmt.Println("\n🤖 默认预装 AI 工具:")
	fmt.Println("  ✅ Claude")
	fmt.Printf("✅ 已选择: %v\n", selected)
	return selected
}

func hermesInstallCmd() string {
	return strings.Join([]string{
		"export HERMES_HOME=\"$HOME/.hermes\"",
		"export HERMES_INSTALL_DIR=\"$HOME/.hermes/hermes-agent\"",
		"curl -fsSL https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh | bash -s -- --no-venv --skip-setup </dev/null",
	}, " && ")
}

func selectedAgentConfigs() map[string]Tool {
	return map[string]Tool{
		"openclaw": {"openclaw", "openclaw", openClawInstallCmd(), true, false},
		"claude":   {"claude", "claude", claudeInstallCmd(), true, false},
		"cicy":     {"cicy", "cicy", cicyInstallCmd(), true, false},
		"codex":    {"codex", "codex", codexInstallCmd(), true, false},
		"opencode": {"opencode", "opencode", opencodeInstallCmd(), true, false},
		"hermes":   {"hermes", "hermes", hermesInstallCmd(), true, false},
	}
}

func ensureAgentToolInstalled(agentType string) {
	agentType = strings.TrimSpace(strings.ToLower(agentType))
	if agentType == "" {
		return
	}
	switch normalizeAgentType(agentType) {
	case "openclaw", "claude", "cicy", "codex", "opencode", "hermes":
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

// builtinAgents defines the available agent catalog (no fixed port binding).
var builtinAgents = []struct {
	AgentType string
	Title     string
}{
	{"claude", "Claude"},
	{"codex", "Codex"},
	{"opencode", "OpenCode"},
	{"kiro-cli", "Kiro CLI"},
	{"copilot", "GitHub Copilot"},
	{"cicy-wechat", "WeChat"},
	{"cicy-feishu", "Feishu"},
	{"openclaw", "OpenClaw"},
	{"hermes", "Hermes Agent"},
	{"cicy-claude", "CiCy"},
}

var nonLabAllowedBuiltinAgents = []string{"claude", "codex", "opencode", "kiro-cli"}

func effectiveAllowedAgentTypes() []string {
	if labMode {
		selected := make([]string, 0, len(builtinAgents))
		for _, ba := range builtinAgents {
			selected = append(selected, ba.AgentType)
		}
		return selected
	}
	selected := make([]string, 0, len(nonLabAllowedBuiltinAgents))
	selected = append(selected, nonLabAllowedBuiltinAgents...)
	return selected
}

func effectiveAgentOptions() []M {
	allowed := effectiveAllowedAgentTypes()
	options := make([]M, 0, len(allowed))
	for _, agentType := range allowed {
		options = append(options, M{
			"value": agentType,
			"label": builtinAgentTitle(agentType),
		})
	}
	return options
}

const primaryWorkerSession = "w-10001"
const primaryWorkerPaneID = "w-10001:main.0"

type builtinWorker struct {
	Port      int
	AgentType string
	Title     string
}

// selectedBuiltinWorkers assigns ports starting from 10001 in the order of selected.
func selectedBuiltinWorkers(selected []string) []builtinWorker {
	workers := make([]builtinWorker, 0, len(selected))
	for i, agentType := range selected {
		agentType = normalizeAgentType(agentType)
		if agentType == "" {
			continue
		}
		workers = append(workers, builtinWorker{
			Port:      10001 + i,
			AgentType: agentType,
			Title:     builtinAgentTitle(agentType),
		})
	}
	return workers
}

func builtinAgentTitle(agentType string) string {
	agentType = normalizeAgentType(agentType)
	for _, ba := range builtinAgents {
		if ba.AgentType == agentType {
			return ba.Title
		}
	}
	return agentType
}

func isBuiltinAgentType(agentType string) bool {
	agentType = normalizeAgentType(agentType)
	for _, ba := range builtinAgents {
		if ba.AgentType == agentType {
			return true
		}
	}
	return false
}

func isAllowedAgentType(agentType string) bool {
	agentType = normalizeAgentType(agentType)
	for _, allowedAgentType := range effectiveAllowedAgentTypes() {
		if allowedAgentType == agentType {
			return true
		}
	}
	return false
}

func parseSelectedAgents(agentList string) ([]string, error) {
	agentList = strings.TrimSpace(agentList)
	if agentList == "" {
		return nil, fmt.Errorf("empty agent list")
	}

	if strings.EqualFold(agentList, "all") {
		return effectiveAllowedAgentTypes(), nil
	}

	seen := map[string]bool{}
	var selected []string
	for _, raw := range strings.Split(agentList, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		agentType := normalizeAgentType(raw)
		if !isBuiltinAgentType(agentType) {
			return nil, fmt.Errorf("unsupported agent: %s", raw)
		}
		if !isAllowedAgentType(agentType) {
			return nil, fmt.Errorf("agent not allowed in current mode: %s", raw)
		}
		if seen[agentType] {
			continue
		}
		seen[agentType] = true
		selected = append(selected, agentType)
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("no valid agents selected")
	}
	return selected, nil
}

const defaultDevAgent = "claude"

func effectiveAgentsFlag() string {
	if value := strings.TrimSpace(agentsFlag); value != "" {
		return value
	}
	if labMode {
		return "all"
	}
	if devMode {
		return defaultDevAgent
	}
	return ""
}

func mustSelectedAgents() []string {
	effective := effectiveAgentsFlag()
	if effective == "" {
		return []string{defaultDevAgent}
	}

	selected, err := parseSelectedAgents(effective)
	if err != nil {
		log.Fatalf("[startup] invalid --agents value %q: %v", effective, err)
	}
	return selected
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

func setWorkerIndex(n int) {
	if n <= 0 {
		return
	}
	if _, err := store.Exec(
		store.Upsert("global_vars", "key_name", []string{"key_name", "value"}, []string{"value"}),
		"worker_index", n,
	); err != nil {
		log.Printf("[startup] failed to set worker_index to %d: %v", n, err)
	}
}

func syncWorkerIndexToExistingAgents() {
	var current sql.NullInt64
	if err := store.QueryRow("SELECT value FROM global_vars WHERE key_name='worker_index'").Scan(&current); err == nil {
		if current.Valid && current.Int64 > 0 {
			return
		}
	}
	var maxPort int
	if err := store.QueryRow("SELECT COALESCE(MAX(ttyd_port), 0) FROM agent_config WHERE active=1").Scan(&maxPort); err != nil {
		log.Printf("[startup] failed to sync worker_index from agent_config: %v", err)
		return
	}
	if maxPort > 0 {
		setWorkerIndex(maxPort)
	}
}

func syncBuiltinAgentTitles(selected []string) {
	for _, w := range selectedBuiltinWorkers(selected) {
		paneID := builtinWorkerSession(w.Port) + ":main.0"
		query := fmt.Sprintf("UPDATE agent_config SET title=?, updated_at=%s WHERE pane_id=? AND (COALESCE(TRIM(title), '')='' OR title=?)", store.Now())
		legacyTitle := ""
		if paneID == primaryWorkerPaneID {
			legacyTitle = "商业顾问"
		}
		if _, err := store.Exec(query, w.Title, paneID, legacyTitle); err != nil {
			log.Printf("[startup] failed to sync builtin title for %s: %v", paneID, err)
		}
	}
}

func hasSelectedAgentType(selected []string, agentType string) bool {
	for _, selectedAgentType := range selected {
		if selectedAgentType == agentType {
			return true
		}
	}
	return false
}

func builtinWorkerSession(port int) string {
	return fmt.Sprintf("w-%d", port)
}

func ensurePrimaryWorkerForBindings(selected []string) {
}

func ensureWorkerBoundToPrimary(workerSession string) {
}

func createSelectedWorkers(selected []string) {
	fmt.Println("\n🚀 创建选中的 Workers...")
	workers := selectedBuiltinWorkers(selected)
	for _, w := range workers {
		paneID := builtinWorkerSession(w.Port) + ":main.0"
		var count int
		store.QueryRow("SELECT COUNT(*) FROM agent_config WHERE pane_id=?", paneID).Scan(&count)
		if count > 0 {
			// Update agent_type and title in case they changed
			store.Exec(fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, updated_at=%s WHERE pane_id=?", store.Now()),
				w.AgentType, w.Title, paneID)
			fmt.Printf("  ⏭ %s - 已存在，已更新\n", w.Title)
			continue
		}
		createBuiltinWorker(w.Port, w.AgentType, w.Title)
	}
	if len(workers) > 0 {
		setWorkerIndex(workers[len(workers)-1].Port)
	}
}

func createBuiltinWorker(port int, agentType, title string) {
	session := fmt.Sprintf("w-%d", port)
	token := getFirstToken()
	if _, err := createManagedPane(paneCreateOpts{
		session:         session,
		title:           title,
		role:            "master",
		defaultModel:    "",
		agentType:       agentType,
		workspace:       builtinWorkerWorkspace(session),
		initScript:      "",
		port:            port,
		token:           token,
		allowAllActions: true,
		replyInChinese:  true,
	}); err != nil {
		fmt.Printf("  ❌ %s 创建失败: %v\n", title, err)
		return
	}
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
	selected, err := parseSelectedAgents(agentList)
	if err != nil {
		fmt.Printf("❌ 无效的 --agents 参数: %v\n", err)
		os.Exit(1)
	}

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
	ensureSSHKeyPair()
	ensureTmuxConf()
	ensureCicyTmuxConf()
	ensureShellRCSourcesCicyTmuxConf()

	setupAIConfigs()

	var count int
	if err := store.QueryRow("SELECT COUNT(*) FROM agent_config").Scan(&count); err != nil {
		log.Fatalf("[startup] failed to query agent_config: %v", err)
	}
	effectiveAgentList := effectiveAgentsFlag()
	if count == 0 {
		if isContainerRuntime() {
			// Preinstalled container runtime must never block on interactive setup.
			// Respect explicit --agents=... when provided; otherwise keep the default
			// footprint minimal with only w-10001 Claude.
			if effectiveAgentList != "" {
				runSetupWithAgents(effectiveAgentList)
			} else {
				createSelectedWorkers([]string{"claude"})
			}
		} else if effectiveAgentList != "" {
			runSetupWithAgents(effectiveAgentList)
		} else {
			runSetup()
		}
	}

	selectedAgents := mustSelectedAgents()
	ensureBuiltinAgents(selectedAgents)
	syncWorkerIndexToExistingAgents()
	syncBuiltinAgentTitles(selectedAgents)
	ensureCicySkillsAsync()
	ensureCodeServer()
}

func setupAIConfigs() {
	cfg := loadRuntimeAIConfig()
	apiKey := cfg.APIKey
	apiUrl := cfg.APIURL
	anthropicUrl := cfg.AnthropicURL
	defaultOpencodeModel := cfg.DefaultOpencodeModel
	defaultClaudeModel := cfg.DefaultClaudeModel
	codexModel := cfg.CodexModel

	if apiKey == "" {
		return
	}

	if apiUrl == "" {
		apiUrl = "http://2000.run:6543/v1"
	}
	if anthropicUrl == "" {
		anthropicUrl = "http://2000.run:6543"
	}
	if defaultOpencodeModel == "" {
		defaultOpencodeModel = "gpt-5.4"
	}
	if defaultClaudeModel == "" {
		defaultClaudeModel = "opus[1m]"
	}
	if codexModel == "" {
		codexModel = "gpt-5.4"
	}

	home, _ := os.UserHomeDir()
	log.Printf("[setup] configuring AI tools with API URL: %s", apiUrl)

	// Set env vars for OpenClaw worker (available via agentBootLines)
	os.Setenv("OPENCLAW_CONFIG_PATH", filepath.Join(home, ".openclaw", "openclaw.json"))
	os.Setenv("OPENAI_API_KEY", apiKey)
	os.Setenv("OPENAI_MODEL", defaultOpencodeModel)
	if openClawToken := strings.TrimSpace(readOpenClawTokenFromGlobalJSON()); openClawToken != "" {
		os.Setenv("OPENCLAW_GATEWAY_TOKEN", openClawToken)
	} else if openClawToken := strings.TrimSpace(readOpenClawTokenFromConfig()); openClawToken != "" {
		os.Setenv("OPENCLAW_GATEWAY_TOKEN", openClawToken)
	}
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

func ensureShellRCSourcesCicyTmuxConf() {
	rcPath := expandHomePath(shellRC())
	if strings.TrimSpace(rcPath) == "" {
		return
	}
	line := `[ -f "$HOME/.cicy_tmux.conf" ] && source "$HOME/.cicy_tmux.conf"`
	current, err := os.ReadFile(rcPath)
	if os.IsNotExist(err) {
		if writeErr := os.WriteFile(rcPath, []byte(line+"\n"), 0644); writeErr != nil {
			log.Fatalf("[startup] failed to write %s: %v", rcPath, writeErr)
		}
		log.Printf("[startup] installing %s", rcPath)
		return
	}
	if err != nil {
		log.Fatalf("[startup] failed to read %s: %v", rcPath, err)
	}
	if strings.Contains(string(current), line) {
		return
	}
	payload := strings.TrimRight(string(current), "\n")
	if payload != "" {
		payload += "\n\n"
	}
	payload += line + "\n"
	if writeErr := os.WriteFile(rcPath, []byte(payload), 0644); writeErr != nil {
		log.Fatalf("[startup] failed to update %s: %v", rcPath, writeErr)
	}
	log.Printf("[startup] updated %s", rcPath)
}

func ensureSSHKeyPair() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[startup] failed to resolve home dir for ssh key setup: %v", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	privateKey := filepath.Join(sshDir, "id_rsa")
	publicKey := privateKey + ".pub"
	authKeysPath := filepath.Join(sshDir, "authkeys")
	authorizedKeysPath := filepath.Join(sshDir, "authorized_keys")
	configPath := filepath.Join(sshDir, "config")

	if err := os.MkdirAll(sshDir, 0700); err != nil {
		log.Fatalf("[startup] failed to create %s: %v", sshDir, err)
	}
	if err := os.Chmod(sshDir, 0700); err != nil {
		log.Printf("[startup] failed to chmod %s to 0700: %v", sshDir, err)
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		log.Fatalf("[startup] ssh-keygen not found after base tool setup: %v", err)
	}

	if _, err := os.Stat(privateKey); err == nil {
		if chmodErr := os.Chmod(privateKey, 0600); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0600: %v", privateKey, chmodErr)
		}
		if _, pubErr := os.Stat(publicKey); os.IsNotExist(pubErr) {
			cmd := exec.Command("ssh-keygen", "-y", "-f", privateKey)
			out, genErr := cmd.Output()
			if genErr != nil {
				log.Fatalf("[startup] failed to regenerate %s from %s: %v", publicKey, privateKey, genErr)
			}
			if writeErr := os.WriteFile(publicKey, out, 0644); writeErr != nil {
				log.Fatalf("[startup] failed to write %s: %v", publicKey, writeErr)
			}
			log.Printf("[startup] regenerated %s", publicKey)
		}
		if chmodErr := os.Chmod(publicKey, 0644); chmodErr != nil && !os.IsNotExist(chmodErr) {
			log.Printf("[startup] failed to chmod %s to 0644: %v", publicKey, chmodErr)
		}
	} else if !os.IsNotExist(err) {
		log.Fatalf("[startup] failed to inspect %s: %v", privateKey, err)
	} else {
		log.Printf("[startup] generating missing SSH keypair at %s", privateKey)
		cmd := exec.Command("ssh-keygen", "-t", "rsa", "-b", "4096", "-N", "", "-f", privateKey, "-q")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("[startup] failed to generate %s: %v", privateKey, err)
		}
		if chmodErr := os.Chmod(privateKey, 0600); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0600: %v", privateKey, chmodErr)
		}
		if chmodErr := os.Chmod(publicKey, 0644); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0644: %v", publicKey, chmodErr)
		}
	}

	pubKeyBytes, readErr := os.ReadFile(publicKey)
	if readErr != nil {
		log.Fatalf("[startup] failed to read %s: %v", publicKey, readErr)
	}
	pubKey := strings.TrimSpace(string(pubKeyBytes))
	if pubKey == "" {
		log.Fatalf("[startup] %s is empty", publicKey)
	}

	ensureSSHAuthKeysFile(authKeysPath, pubKey)
	ensureSSHAuthKeysFile(authorizedKeysPath, pubKey)
	ensureSSHConfigFile(configPath)
}

func ensureSSHAuthKeysFile(path string, pubKey string) {
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("[startup] failed to read %s: %v", path, err)
	}
	if err == nil {
		for _, line := range strings.Split(string(current), "\n") {
			if strings.TrimSpace(line) == pubKey {
				if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
					log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
				}
				return
			}
		}
	}

	var content string
	if len(current) > 0 {
		content = string(current)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += pubKey + "\n"
	} else {
		content = pubKey + "\n"
	}
	if writeErr := os.WriteFile(path, []byte(content), 0600); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", path, writeErr)
	}
	if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
		log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
	}
	log.Printf("[startup] ensured %s contains the local public key", path)
}

func ensureSSHConfigFile(path string) {
	const managedBlock = "# cicy-code managed ssh defaults\nHost *\n  IdentityFile ~/.ssh/id_rsa\n  IdentitiesOnly yes\n"

	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("[startup] failed to read %s: %v", path, err)
	}
	if err == nil && strings.Contains(string(current), managedBlock) {
		if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
			log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
		}
		return
	}

	content := managedBlock
	if len(current) > 0 {
		content = string(current)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + managedBlock
	}
	if writeErr := os.WriteFile(path, []byte(content), 0600); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", path, writeErr)
	}
	if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
		log.Printf("[startup] failed to chmod %s to 0600: %v", path, chmodErr)
	}
	log.Printf("[startup] ensured %s exists", path)
}

func cicySkillsVersion() string {
	if v := strings.TrimSpace(os.Getenv("CICY_SKILLS_VERSION")); v != "" {
		return v
	}
	return cicySkillsDefaultVersion
}

func cicySkillsGitHubProxy() string {
	value := strings.TrimSpace(os.Getenv("GITHUB_PROXY"))
	if value == "" {
		value = cicySkillsDefaultGHProxy
	}
	if value == "" {
		return ""
	}
	if !strings.HasSuffix(value, "/") {
		value += "/"
	}
	return value
}

func cicySkillsBundleName(goos, goarch, tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	goos = strings.TrimSpace(goos)
	goarch = strings.TrimSpace(goarch)
	if tag == "" || goos == "" || goarch == "" {
		return "", fmt.Errorf("invalid cicy-skills bundle params: goos=%q goarch=%q tag=%q", goos, goarch, tag)
	}
	switch goos {
	case "linux", "darwin":
		return fmt.Sprintf("cicy-skills_%s_%s_%s.tar.gz", tag, goos, goarch), nil
	case "windows":
		return fmt.Sprintf("cicy-skills_%s_%s_%s.zip", tag, goos, goarch), nil
	default:
		return "", fmt.Errorf("unsupported cicy-skills runtime: %s/%s", goos, goarch)
	}
}

func cicySkillsBundleURL(tag string) (string, error) {
	bundle, err := cicySkillsBundleName(runtime.GOOS, runtime.GOARCH, tag)
	if err != nil {
		return "", err
	}
	return cicySkillsGitHubProxy() + cicySkillsRepoURL + "/releases/download/" + tag + "/" + bundle, nil
}

func cicySkillsSourceArchiveName(tag string) string {
	name := strings.TrimSpace(tag)
	name = strings.TrimPrefix(name, "refs/tags/")
	if name == "" {
		name = cicySkillsVersion()
	}
	return name + ".tar.gz"
}

func cicySkillsSourceURL(tag string) string {
	return cicySkillsGitHubProxy() + cicySkillsRepoURL + "/archive/refs/tags/" + strings.TrimSpace(tag) + ".tar.gz"
}

func cicySkillsSourceAPIURL(tag string) string {
	return cicySkillsRepoAPIURL + "/tarball/" + strings.TrimSpace(tag)
}

func cicySkillsReleaseAPIURL(tag string) string {
	return cicySkillsRepoAPIURL + "/releases/tags/" + strings.TrimSpace(tag)
}

func cicySkillsDistBinaryPath() string {
	return filepath.Join(cicySkillsProjectDir(), "dist", "cicy-skills")
}

func cicySkillsProjectDir() string {
	return filepath.Join(cicySkillsDir, "cicy-skills")
}

func cicySkillsMountedProjectDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "projects", "cicy-skills")
}

func cicySkillsSkillDocPath(profile, skill string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "."+profile, "skills", skill, "SKILL.md")
}

func cicySkillsCommandPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin", name)
}

func needsCicySkillsInstall() bool {
	required := []string{
		filepath.Join(cicySkillsProjectDir(), "go.mod"),
		filepath.Join(cicySkillsProjectDir(), "providers", "google-node", "package.json"),
		cicySkillsDistBinaryPath(),
		cicySkillsCommandPath("cicy-skills"),
		cicySkillsCommandPath("agent-code-server"),
		cicySkillsCommandPath("agent-webpage"),
		cicySkillsSkillDocPath("codex", "agent-code-server"),
		cicySkillsSkillDocPath("claude", "agent-code-server"),
		cicySkillsSkillDocPath("openclaw", "agent-code-server"),
	}
	for _, path := range required {
		if strings.TrimSpace(path) == "" {
			return true
		}
		if _, err := os.Stat(path); err != nil {
			return true
		}
	}
	for _, name := range []string{"webpage", "webpage-ping", "ipc-ping", "agent-page-ping"} {
		path := cicySkillsCommandPath(name)
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func cicySkillsInstallScript(tag string) (string, error) {
	bundleName, err := cicySkillsBundleName(runtime.GOOS, runtime.GOARCH, tag)
	if err != nil {
		return "", err
	}
	bundleURL, err := cicySkillsBundleURL(tag)
	if err != nil {
		return "", err
	}
	sourceURL := cicySkillsSourceURL(tag)
	sourceArchiveName := cicySkillsSourceArchiveName(tag)
	bundleDir := strings.TrimSuffix(bundleName, ".tar.gz")
	projectRoot := cicySkillsProjectDir()
	localProjectRoot := cicySkillsMountedProjectDir()
	distFiles := []string{"cicy-skills", "cicy-skillsd", "cicy-hosttools", "stt", "tts"}
	var copyLines []string
	var localCopyLines []string
	var localDistChecks []string
	var buildLines []string
	for _, name := range distFiles {
		copyLines = append(copyLines,
			fmt.Sprintf("cp -f \"$bundle_dir/%s\" %q", name, filepath.Join(projectRoot, "dist", name)),
			fmt.Sprintf("chmod +x %q", filepath.Join(projectRoot, "dist", name)),
		)
		localCopyLines = append(localCopyLines,
			fmt.Sprintf("    cp -f %q %q", filepath.Join(localProjectRoot, "dist", name), filepath.Join(projectRoot, "dist", name)),
			fmt.Sprintf("    chmod +x %q", filepath.Join(projectRoot, "dist", name)),
		)
		localDistChecks = append(localDistChecks, fmt.Sprintf("[ -x %q ]", filepath.Join(localProjectRoot, "dist", name)))
		buildLines = append(buildLines, fmt.Sprintf("      CGO_ENABLED=0 GOOS=%q GOARCH=%q go build -o %q ./%s", runtime.GOOS, runtime.GOARCH, filepath.Join(projectRoot, "dist", name), filepath.ToSlash(filepath.Join("cmd", name))))
	}
	npmMirror := ""
	if strings.TrimSpace(os.Getenv("CN_MIRROR")) == "1" {
		npmMirror = fmt.Sprintf("export NPM_CONFIG_REGISTRY=%q\n", cicySkillsNPMMirror)
	}
	script := fmt.Sprintf(`set -e
root=%q
project_root=%q
local_project_root=%q
tag=%q
tmp="$(mktemp -d)"
token="${CICY_SKILLS_GITHUB_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

download_release_bundle() {
  out="$1"
  if [ -n "$token" ]; then
    release_json="$(curl --http1.1 -fsSL -H "Authorization: Bearer $token" -H "Accept: application/vnd.github+json" %q)"
    asset_api_url="$(printf '%%s' "$release_json" | python3 - %q <<'PY'
import json
import sys

name = sys.argv[1]
data = json.load(sys.stdin)
for asset in data.get("assets", []):
    if asset.get("name") == name:
        print(asset.get("url", ""))
        break
else:
    raise SystemExit(f"missing release asset: {name}")
PY
)"
    curl --http1.1 -fsSL -H "Authorization: Bearer $token" -H "Accept: application/octet-stream" "$asset_api_url" -o "$out"
    return
  fi
  curl -fsSL %q -o "$out"
}

download_source_archive() {
  out="$1"
  if [ -n "$token" ]; then
    curl --http1.1 -fsSL -H "Authorization: Bearer $token" -H "Accept: application/octet-stream" %q -o "$out"
    return
  fi
  curl -fsSL %q -o "$out"
}

mkdir -p "$root" "$project_root" %q
rm -rf "$project_root"
mkdir -p "$project_root" %q
if [ -f "$local_project_root/go.mod" ]; then
  cp -a "$local_project_root"/. "$project_root"/
  if %s; then
%s
  else
    (
      cd "$project_root"
%s
    )
  fi
else
  download_source_archive "$tmp/%s"
  mkdir -p "$tmp/source"
  tar -xzf "$tmp/%s" -C "$tmp/source"
  src_dir="$(find "$tmp/source" -mindepth 1 -maxdepth 1 -type d | head -n1)"
  if [ -z "$src_dir" ] || [ ! -d "$src_dir" ]; then
    echo "missing extracted cicy-skills source dir" >&2
    exit 1
  fi

  download_release_bundle "$tmp/%s"
  mkdir -p "$tmp/release"
  tar -xzf "$tmp/%s" -C "$tmp/release"
  bundle_dir="$tmp/release/%s"
  if [ ! -d "$bundle_dir" ]; then
    echo "missing extracted cicy-skills release dir: $bundle_dir" >&2
    exit 1
  fi

  cp -a "$src_dir"/. "$project_root"/
  mkdir -p %q
%s
fi
%s
export CICY_SKILLS_ROOT="$project_root"
%s install all
%s agent sync openclaw
`, cicySkillsDir, projectRoot, localProjectRoot, tag, cicySkillsReleaseAPIURL(tag), bundleName, bundleURL, cicySkillsSourceAPIURL(tag), sourceURL, filepath.Join(projectRoot, "dist"), filepath.Join(projectRoot, "dist"), strings.Join(localDistChecks, " && "), strings.Join(localCopyLines, "\n"), strings.Join(buildLines, "\n"), sourceArchiveName, sourceArchiveName, bundleName, bundleName, bundleDir, filepath.Join(projectRoot, "dist"), strings.Join(copyLines, "\n"), npmMirror, cicySkillsDistBinaryPath(), cicySkillsDistBinaryPath())
	return script, nil
}

func ensureCicySkillsAsync() {
	cicySkillsInstallOnce.Do(func() {
		if !needsCicySkillsInstall() {
			return
		}
		go func() {
			tag := cicySkillsVersion()
			script, err := cicySkillsInstallScript(tag)
			if err != nil {
				log.Printf("[startup] skipped cicy-skills bootstrap: %v", err)
				return
			}
			if err := os.MkdirAll(cicyStateDir, 0755); err != nil {
				log.Printf("[startup] failed to create %s for cicy-skills bootstrap: %v", cicyStateDir, err)
				return
			}
			logPath := filepath.Join(cicyStateDir, cicySkillsInstallLogFile)
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				log.Printf("[startup] failed to open cicy-skills install log: %v", err)
				return
			}
			defer logFile.Close()
			log.Printf("[startup] installing cicy-skills in background: root=%s version=%s", cicySkillsDir, tag)
			cmd := exec.Command("sh", "-lc", script)
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			cmd.Env = os.Environ()
			if err := cmd.Run(); err != nil {
				log.Printf("[startup] cicy-skills bootstrap failed: %v (log: %s)", err, logPath)
				return
			}
			extendPATH()
			if needsCicySkillsInstall() {
				log.Printf("[startup] cicy-skills bootstrap incomplete: required commands or docs still missing (log: %s)", logPath)
				return
			}
			log.Printf("[startup] cicy-skills bootstrap completed")
		}()
	})
}

func ensureCodeServer() {
	go ensureCodeServerAsync()
}

func ensureCodeServerUserSettings(home string) {
	userDir := filepath.Join(home, ".local", "share", "code-server", "User")
	if err := os.MkdirAll(userDir, 0755); err != nil {
		log.Printf("[startup] failed to create code-server User dir: %v", err)
		return
	}

	settingsPath := filepath.Join(userDir, "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(settingsPath); err == nil && strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &settings); err != nil {
			log.Printf("[startup] failed to parse code-server settings.json, recreating: %v", err)
			settings = map[string]any{}
		}
	}

	if _, ok := settings["workbench.colorTheme"]; !ok {
		settings["workbench.colorTheme"] = "Default Dark+"
	}
	if _, ok := settings["workbench.iconTheme"]; !ok {
		settings["workbench.iconTheme"] = "simple-icons"
	}

	payload, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		log.Printf("[startup] failed to marshal code-server settings: %v", err)
		return
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(settingsPath, payload, 0644); err != nil {
		log.Printf("[startup] failed to write code-server settings.json: %v", err)
	}
}

func installCodeServerExtension(home string, extension string) {
	log.Printf("[startup] installing code-server extension: %s", extension)
	cmd := exec.Command("code-server", "--install-extension", extension, "--force")
	cmd.Dir = home
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PORT=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		log.Printf("[startup] failed to install code-server extension %s: %v output=%s", extension, runErr, strings.TrimSpace(string(output)))
		return
	}
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		log.Printf("[startup] code-server extension installed: %s output=%s", extension, trimmed)
	}
}

func installEmbeddedCodeServerExtension(home string, fileName string, payload []byte) {
	if len(payload) == 0 {
		log.Printf("[startup] skipped empty embedded code-server extension: %s", fileName)
		return
	}
	tmpFile, err := os.CreateTemp("", "cicy-code-server-extension-*.vsix")
	if err != nil {
		log.Printf("[startup] failed to create temp vsix for %s: %v", fileName, err)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		log.Printf("[startup] failed to write temp vsix for %s: %v", fileName, err)
		return
	}
	if err := tmpFile.Close(); err != nil {
		log.Printf("[startup] failed to close temp vsix for %s: %v", fileName, err)
		return
	}
	installCodeServerExtension(home, tmpPath)
}

func installBundledCodeServerExtensions(home string) {
	installEmbeddedCodeServerExtension(home, "cicy-code-server-bridge-0.0.1.vsix", embeddedCodeServerBridgeVSIX)
	installCodeServerExtension(home, "laurenttreguier.vscode-simple-icons")
}

func ensureCodeServerAsync() {
	extendPATH()
	if _, err := exec.LookPath("code-server"); err != nil {
		installCmd := codeServerInstallCmd()
		if installCmd == "" {
			log.Printf("[startup] code-server missing in preinstalled runtime; rebuild the base image")
			return
		}
		fmt.Println("📦 后台安装 code-server...")
		cmd := exec.Command("sh", "-c", installCmd)
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
	ensureCodeServerUserSettings(home)

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

	go installBundledCodeServerExtensions(home)
}

func mustAtoi(s string) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		log.Fatalf("[startup] invalid port %q", s)
	}
	return n
}

// ensureBuiltinAgents restores tmux sessions and ttyd only for selected builtin agents.
func ensureBuiltinAgents(selected []string) {
	workers := selectedBuiltinWorkers(selected)
	allowedByPaneID := make(map[string]struct{}, len(workers))
	desiredByPaneID := make(map[string]builtinWorker, len(workers))
	for _, w := range workers {
		pid := builtinWorkerSession(w.Port) + ":main.0"
		allowedByPaneID[pid] = struct{}{}
		desiredByPaneID[pid] = w
	}

	rows, err := store.Query("SELECT pane_id, ttyd_port, workspace, COALESCE(init_script,''), COALESCE(config,'{}'), COALESCE(agent_type,''), COALESCE(allow_all_actions,0), COALESCE(reply_in_chinese,0) FROM agent_config WHERE active=1 ORDER BY ttyd_port ASC, pane_id ASC")
	if err != nil {
		return
	}
	defer rows.Close()

	token := getFirstToken()
	for rows.Next() {
		var paneID, workspace, initScript, configJSON, agentType string
		var allowAllActions bool
		var replyInChinese bool
		var port int
		rows.Scan(&paneID, &port, &workspace, &initScript, &configJSON, &agentType, &allowAllActions, &replyInChinese)
		if paneID == "" || port == 0 {
			continue
		}
		if _, ok := allowedByPaneID[paneID]; !ok {
			continue
		}

		// Sync agent_type and title if changed
		if desired, ok := desiredByPaneID[paneID]; ok && normalizeAgentType(agentType) != desired.AgentType {
			store.Exec(fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, updated_at=%s WHERE pane_id=?", store.Now()),
				desired.AgentType, desired.Title, paneID)
			agentType = desired.AgentType
		}

		// Ensure tmux session
		sess := strings.Split(paneID, ":")[0]
		sessionCreated := false
		if exec.Command("tmux", "has-session", "-t", sess).Run() != nil {
			if workspace == "" {
				workspace = builtinWorkerWorkspace(sess)
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
		}
		if sessionCreated {
			initPaneEnv(paneEnvOpts{
				paneID:          paneID,
				configJSON:      configJSON,
				workspace:       workspace,
				initScript:      initScript,
				agentType:       agentType,
				allowAllActions: allowAllActions,
				replyInChinese:  replyInChinese,
			})
		}
	}
}
