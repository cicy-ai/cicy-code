package main

import (
	"compress/gzip"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"ttyd-go/skillcmd"
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

var cicySkillsInstallOnce sync.Once

const (
	cicyDefaultPyPIMirror    = "https://pypi.tuna.tsinghua.edu.cn/simple"
	cicySkillsInstallLogFile = "cicy-skills-install.log"
	defaultGitHubProxy       = "https://gh-proxy.com/"
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
		// ~/.cicy-ai/bin holds our self-downloaded static binaries
		// (ffmpeg) so we don't depend on brew/apt being available or
		// configured. ensureFfmpegAsync populates this on first launch.
		filepath.Join(home, ".cicy-ai", "bin"),
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
	// Auto-detect network: try registry.npmjs.org with a 3-second timeout.
	// If reachable, use it directly; otherwise fall back to npmmirror (China).
	registry := `$(if curl -s --max-time 3 https://registry.npmjs.org/ >/dev/null 2>&1; then echo https://registry.npmjs.org; else echo https://registry.npmmirror.com; fi)`
	return `mkdir -p "$HOME/.npm-global/bin" "$HOME/.npm-global/lib" "$HOME/.npm-global/lib/node_modules" && npm install -g --include=optional --registry=` + registry + ` --prefix "$HOME/.npm-global" ` + pkg
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

func cursorInstallCmd() string {
	return preinstalledRuntimeInstallCmd("curl https://cursor.com/install -fsS | bash")
}

func cicyInstallCmd() string {
	return npmGlobalInstallCmd("cicy-claude@latest")
}

func kiroCliInstallCmd() string {
	return "__cicy_install_kiro_cli_with_progress"
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
	// Linux: install Node 24 from prebuilt binary tarball under
	// $HOME/.local — no apt-add-repository, no sudo for the node bits
	// themselves, no nodesource dependency. Mirror order is auto-picked:
	// probe npmmirror (CN) first; if reachable, prefer it; else fall back
	// to nodejs.org. PATH wiring lands in $HOME/.local/bin which baseTools
	// already export.
	return `set -e
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) NARCH=x64 ;;
  aarch64|arm64) NARCH=arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac
INDEX_URL=""
for url in \
    "https://npmmirror.com/mirrors/node/index.json" \
    "https://nodejs.org/dist/index.json"; do
  if curl -fsI --max-time 5 "$url" >/dev/null 2>&1; then INDEX_URL="$url"; break; fi
done
[ -n "$INDEX_URL" ] || { echo "no reachable node index" >&2; exit 1; }
NODE_VER=$(curl -fsSL --max-time 15 "$INDEX_URL" | grep -m1 -oE '"v24\.[0-9]+\.[0-9]+"' | head -1 | tr -d '"')
[ -n "$NODE_VER" ] || NODE_VER="v24.0.0"
BASE=$(dirname "$INDEX_URL")
TARBALL="$BASE/${NODE_VER}/node-${NODE_VER}-linux-${NARCH}.tar.xz"
echo "  → fetching $TARBALL"
mkdir -p "$HOME/.local" "$HOME/.local/bin"
TMP=$(mktemp -t node.XXXXXX.tar.xz)
curl -fsSL --max-time 300 "$TARBALL" -o "$TMP"
tar -xJf "$TMP" -C "$HOME/.local"
rm -f "$TMP"
ln -sfn "$HOME/.local/node-${NODE_VER}-linux-${NARCH}" "$HOME/.local/node"
ln -sf "$HOME/.local/node/bin/node" "$HOME/.local/bin/node"
ln -sf "$HOME/.local/node/bin/npm" "$HOME/.local/bin/npm"
ln -sf "$HOME/.local/node/bin/npx" "$HOME/.local/bin/npx"
echo "  → installed $($HOME/.local/bin/node --version)"`
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
		{"cursor-agent", "cursor-agent", cursorInstallCmd(), true, false},
		{"hermes", "hermes", hermesInstallCmd(), true, false},
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
	rawScriptURL := defaultGitHubProxy + "https://raw.githubusercontent.com/NousResearch/hermes-agent/main/scripts/install.sh"
	archiveURL := defaultGitHubProxy + "https://codeload.github.com/NousResearch/hermes-agent/tar.gz/refs/heads/${branch}"
	return strings.Join([]string{
		"export HERMES_HOME=\"$HOME/.hermes\"",
		"export HERMES_INSTALL_DIR=\"$HOME/.hermes/hermes-agent\"",
		"export UV_INDEX_URL=\"https://pypi.tuna.tsinghua.edu.cn/simple\"",
		"export PIP_INDEX_URL=\"https://pypi.tuna.tsinghua.edu.cn/simple\"",
		"export REPO_URL_HTTPS=\"https://gh-proxy.com/https://github.com/NousResearch/hermes-agent.git\"",
		"export BRANCH=\"main\"",
		"export INSTALL_DIR=\"$HERMES_INSTALL_DIR\"",
		"cicy_clone_hermes() { branch=\"$1\"; install_dir=\"$2\"; tmp_dir=$(mktemp -d); archive_url=\"" + archiveURL + "\"; if ! curl -fsSL \"$archive_url\" -o \"$tmp_dir/hermes-agent.tar.gz\"; then rm -rf \"$tmp_dir\"; return 1; fi; if ! tar -xzf \"$tmp_dir/hermes-agent.tar.gz\" -C \"$tmp_dir\"; then rm -rf \"$tmp_dir\"; return 1; fi; root_dir=$(find \"$tmp_dir\" -maxdepth 1 -mindepth 1 -type d | head -n 1); if [ -z \"$root_dir\" ]; then rm -rf \"$tmp_dir\"; return 1; fi; rm -rf \"$install_dir\"; mkdir -p \"$(dirname \"$install_dir\")\"; mv \"$root_dir\" \"$install_dir\"; rm -rf \"$tmp_dir\"; return 0; }",
		"if cicy_clone_hermes \"$BRANCH\" \"$INSTALL_DIR\"; then curl -fsSL " + rawScriptURL + " | HERMES_HOME=\"$HERMES_HOME\" HERMES_INSTALL_DIR=\"$HERMES_INSTALL_DIR\" bash -s -- --no-venv --skip-setup </dev/null; else curl -fsSL " + rawScriptURL + " | bash -s -- --no-venv --skip-setup </dev/null; fi",
	}, " && ")
}

func selectedAgentConfigs() map[string]Tool {
	return map[string]Tool{
		"openclaw": {"openclaw", "openclaw", openClawInstallCmd(), true, false},
		"claude":   {"claude", "claude", claudeInstallCmd(), true, false},
		"cicy":     {"cicy", "cicy", cicyInstallCmd(), true, false},
		"codex":    {"codex", "codex", codexInstallCmd(), true, false},
		"opencode": {"opencode", "opencode", opencodeInstallCmd(), true, false},
		"kiro-cli": {"kiro-cli", "kiro-cli", kiroCliInstallCmd(), true, false},
		"cursor":   {"cursor-agent", "cursor-agent", cursorInstallCmd(), true, false},
		"hermes":   {"hermes", "hermes", hermesInstallCmd(), true, false},
	}
}

func ensureAgentToolInstalled(agentType string) {
	agentType = strings.TrimSpace(strings.ToLower(agentType))
	if agentType == "" {
		return
	}
	switch normalizeAgentType(agentType) {
	case "openclaw", "claude", "cicy", "codex", "opencode", "cursor", "hermes":
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
	{"cursor", "Cursor"},
	{"kiro-cli", "Kiro CLI"},
	{"copilot", "GitHub Copilot"},
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
		if current.Valid && current.Int64 > int64(defaultWorkerIndex) {
			return
		}
	}
	var maxPort int
	if err := store.QueryRow("SELECT COALESCE(MAX(ttyd_port), 0) FROM agent_config").Scan(&maxPort); err != nil {
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

// ensureWorkerBoundToPrimary inserts a pane_agents row attaching workerSession
// under primaryWorkerSession (w-10001). Idempotent thanks to the
// UNIQUE(pane_id, agent_name) constraint. No-op when workerSession is the
// primary itself.
func ensureWorkerBoundToPrimary(workerSession string) {
	workerSession = shortPaneID(strings.TrimSpace(workerSession))
	if workerSession == "" || workerSession == primaryWorkerSession {
		return
	}
	res, err := bindAgentCore(primaryWorkerSession, workerSession, true, "")
	if err != nil {
		log.Printf("[startup] failed to bind %s under %s: %v", workerSession, primaryWorkerSession, err)
		return
	}
	if !res.AlreadyBound {
		log.Printf("[startup] bound %s under %s", workerSession, primaryWorkerSession)
	}
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
		} else {
			createBuiltinWorker(w.Port, w.AgentType, w.Title)
		}
		// With more than one builtin agent, the non-primary ones get attached
		// under w-10001 so they appear in the same chat session by default.
		if len(workers) > 1 {
			ensureWorkerBoundToPrimary(builtinWorkerSession(w.Port))
		}
	}
	if len(workers) > 0 {
		setWorkerIndex(workers[len(workers)-1].Port)
	}
}

func createBuiltinWorker(port int, agentType, title string) {
	session := fmt.Sprintf("w-%d", port)
	token := getFirstToken()
	if _, err := createManagedPane(paneCreateOpts{
		session:          session,
		title:            title,
		role:             "master",
		defaultModel:     "",
		agentType:        agentType,
		workspace:        builtinWorkerWorkspace(session),
		initScript:       "",
		port:             port,
		token:            token,
		allowAllActions:  true,
		replyInChinese:   false,
		useCustomGateway: true,
		useProxy:         false,
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
	ensureCicyShellInit()

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
	// Bring up the local mihomo proxy in the BACKGROUND. It used to run
	// synchronously here (so proxy-routed workers wouldn't dial a dead :9001
	// on boot), but on a fresh runtime `cicy-mihomo install` downloads a
	// binary over a possibly-throttled link — that blocked checkEnv(), which
	// runs before http.ListenAndServe, so the API didn't bind for minutes and
	// the desktop "open" landed on a login page. A fresh runtime has no
	// proxy-routed workers yet, and existing ones tolerate a brief proxy gap,
	// so backgrounding it is safe and gets :8008 serving immediately.
	go startCicyMihomoIfNeeded()
	ensureBuiltinAgents(selectedAgents)
	syncWorkerIndexToExistingAgents()
	syncBuiltinAgentTitles(selectedAgents)
	go ensureFfmpegAsync()
	go ensurePreinstalledSkills()
}

var preinstalledSkills = []string{
	"agent-chrome", "agent-editor", "agent-desktop", "agent-webpage",
	"cicy-agent", "cicy-todo", "cicy-mihomo", "cicy-ssh", "proxy_ssh", "globalApiToken",
	"agent-summary",
}

func ensurePreinstalledSkills() {
	installed, err := skillcmd.PublicInstalled()
	if err != nil {
		log.Printf("[startup] failed to read installed skills: %v", err)
		return
	}
	installedMap := map[string]bool{}
	for _, s := range installed.Skills {
		installedMap[s.Name] = true
	}
	for _, name := range preinstalledSkills {
		if installedMap[name] {
			continue
		}
		log.Printf("[startup] pre-installing skill: %s", name)
		if _, err := skillcmd.PublicInstall(name, io.Discard); err != nil {
			log.Printf("[startup] skill %s install failed: %v", name, err)
		} else {
			log.Printf("[startup] skill %s installed", name)
		}
	}
	// Backfill ~/.local/bin/<name> symlinks for every recorded skill —
	// repairs installs that completed but lost their PATH entry (e.g. due
	// to a transient mkdir/symlink error). Idempotent; quiet on a clean
	// host. See api/skillcmd/repair.go for rationale.
	repaired, errs := skillcmd.EnsureBinSymlinks()
	if len(repaired) > 0 {
		log.Printf("[startup] repaired skill symlinks in ~/.local/bin: %v", repaired)
	}
	for _, e := range errs {
		log.Printf("[startup] skill symlink repair: %v", e)
	}
}

func ensureFfmpegAsync() {
	extendPATH()
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		return
	}
	if isContainerRuntime() {
		return
	}
	// Try the static-binary download path first. Works without brew/apt
	// or any system package manager — pure curl + extract into
	// ~/.cicy-ai/bin/ffmpeg (already on PATH via extendPATH). Falls back
	// to brew/apt only if download fails (e.g. mirrors all unreachable).
	if err := installStaticFfmpeg(); err == nil {
		log.Printf("[startup] ffmpeg installed (static binary)")
		return
	} else {
		log.Printf("[startup] static ffmpeg install failed (%v), falling back to system package manager...", err)
	}
	var installCmd string
	if runtime.GOOS == "darwin" {
		// Set Homebrew bottle mirror for faster downloads in China.
		installCmd = `HOMEBREW_BOTTLE_DOMAIN=https://mirrors.ustc.edu.cn/homebrew-bottles brew install ffmpeg`
	} else {
		installCmd = sudoPrefix() + "apt-get install -y ffmpeg"
	}
	log.Printf("[startup] installing ffmpeg in background...")
	cmd := exec.Command("sh", "-c", installCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] ffmpeg install failed (voice features degraded): %v", err)
		return
	}
	log.Printf("[startup] ffmpeg installed")
}

// installStaticFfmpeg downloads a prebuilt ffmpeg binary into
// $HOME/.cicy-ai/bin/ffmpeg. The destination is on PATH via
// extendPATH, so `exec.LookPath("ffmpeg")` resolves to it immediately
// after this returns. Cross-platform: works the same on Linux, macOS,
// and inside WSL Ubuntu — no brew, no apt, no sudo.
//
// Sources picked for stability + China reachability:
//   - Linux:   johnvansickle.com (mirrored via ghproxy.net for CN)
//   - macOS:   evermeet.cx (single-binary tarballs, x64 + arm64)
// Both providers ship truly static builds (no dynamic dependencies)
// suitable for headless containers/WSL.
func installStaticFfmpeg() error {
	if runtime.GOOS == "windows" {
		// cicy-code on Windows runs inside WSL Ubuntu, so we never hit
		// this path with GOOS=windows. Reject explicitly to make any
		// future Windows-native build fail loudly instead of silently
		// downloading an x86_64 ELF that wouldn't run.
		return fmt.Errorf("static-ffmpeg install not supported on windows host")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user-home: %w", err)
	}
	binDir := filepath.Join(home, ".cicy-ai", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("mkdir bin: %w", err)
	}
	dst := filepath.Join(binDir, "ffmpeg")

	// Pick mirror URL list (cn first if we detect cn, else direct).
	// detectCNNetwork mirrors the wslInstaller probe — Baidu HEAD with
	// short timeout. Treats unknown as cn so we lean on mirrors when
	// network detection is inconclusive.
	urls := staticFfmpegURLs(runtime.GOOS, runtime.GOARCH, detectCNNetwork())
	if len(urls) == 0 {
		return fmt.Errorf("no static ffmpeg URL for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	tmpDir, err := os.MkdirTemp("", "ffmpeg-stage")
	if err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var lastErr error
	for _, u := range urls {
		log.Printf("[startup] static ffmpeg: trying %s", u)
		if err := downloadAndExtractFfmpeg(u, tmpDir, dst); err != nil {
			lastErr = err
			log.Printf("[startup] static ffmpeg: %s failed: %v", u, err)
			continue
		}
		return nil
	}
	return fmt.Errorf("all mirrors failed: %w", lastErr)
}

// staticFfmpegURLs returns ordered candidate URLs for the given
// platform. CN-friendly mirrors come first when network=="cn".
//
// Linux source: BtbN/FFmpeg-Builds on GitHub. Picked over
// johnvansickle.com because (a) hosted on github → ghproxy.net mirrors
// actually work, and (b) CN bandwidth to GitHub releases is far better
// than to johnvansickle's static hosting.
// macOS source: evermeet.cx is still the canonical Mac static-build
// provider; we keep it as the primary and don't currently have a
// github-hosted mirror.
func staticFfmpegURLs(goos, goarch, network string) []string {
	switch goos {
	case "linux":
		var asset string
		switch goarch {
		case "amd64":
			// lgpl build (~70 MB) is GPL-feature-stripped but keeps
			// libmp3lame/libopus/libpulse — everything cicy-code's
			// voice path actually uses. The gpl variant (~200 MB)
			// bundles vulkan/libplacebo/svt-av1 etc. that we don't use.
			asset = "ffmpeg-master-latest-linux64-lgpl.tar.xz"
		case "arm64":
			asset = "ffmpeg-master-latest-linuxarm64-lgpl.tar.xz"
		default:
			return nil
		}
		direct := "https://github.com/BtbN/FFmpeg-Builds/releases/latest/download/" + asset
		mirrors := []string{
			"https://ghproxy.net/" + direct,
			"https://gh-proxy.com/" + direct,
		}
		if network == "cn" || network == "unknown" {
			return append(mirrors, direct)
		}
		return append([]string{direct}, mirrors...)
	case "darwin":
		// evermeet ships separate per-arch builds.
		var path string
		switch goarch {
		case "amd64":
			path = "ffmpeg.zip"
		case "arm64":
			path = "ffmpeg-arm64.zip"
		default:
			return nil
		}
		return []string{"https://evermeet.cx/ffmpeg/getrelease/" + path}
	}
	return nil
}

// downloadAndExtractFfmpeg fetches `url` (a .tar.xz or .zip), extracts
// the embedded `ffmpeg` binary, and atomically renames it to `dst`.
// Uses system curl + tar/unzip for portability — no Go-side archive
// libs needed, and existing cicy-code installs already have these
// tools (curl is a baseTools required; tar is in coreutils everywhere;
// unzip is a baseTool too).
func downloadAndExtractFfmpeg(url, tmpDir, dst string) error {
	arc := filepath.Join(tmpDir, "archive")
	// Single-stream download + size sanity check. -L to follow github
	// redirects, --fail to surface 4xx/5xx as non-zero exit. 300s ≈ 5m
	// is a generous ceiling for a ~40 MB download even on slow CN
	// links; we'd rather hit it than have an indefinite hang.
	dl := exec.Command("curl", "-fL", "--max-time", "300", "-o", arc, url)
	dl.Stderr = os.Stderr
	if err := dl.Run(); err != nil {
		return fmt.Errorf("curl: %w", err)
	}
	st, err := os.Stat(arc)
	if err != nil {
		return fmt.Errorf("stat archive: %w", err)
	}
	if st.Size() < 5*1024*1024 {
		return fmt.Errorf("archive too small (%d bytes) — likely error page", st.Size())
	}

	// Extract. Archive layouts vary across providers:
	//   evermeet.cx (macOS zip): flat — `ffmpeg` at the top
	//   johnvansickle  (.tar.xz): `ffmpeg-X.Y-amd64-static/ffmpeg`
	//   BtbN/FFmpeg-Builds (.tar.xz): `<versioned-dir>/bin/ffmpeg`
	// We dump everything then `find -name ffmpeg -type f` rather than
	// hard-coding the nested path, which would break next time someone
	// switches mirrors.
	var ex *exec.Cmd
	if strings.HasSuffix(url, ".zip") {
		ex = exec.Command("sh", "-c", fmt.Sprintf("cd %q && unzip -o -q %q", tmpDir, arc))
	} else {
		ex = exec.Command("sh", "-c", fmt.Sprintf("tar -xJf %q -C %q", arc, tmpDir))
	}
	ex.Stderr = os.Stderr
	if err := ex.Run(); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	found, err := findFile(tmpDir, "ffmpeg")
	if err != nil {
		return fmt.Errorf("locate ffmpeg in archive: %w", err)
	}
	tmpBin := found
	if err := os.Chmod(tmpBin, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	// Atomic rename within the same filesystem (~/.cicy-ai/bin is on
	// the user's home FS, same as tmpDir which we put under /tmp...
	// wait, that crosses /tmp → /home which may be different mounts).
	// Use os.Rename and fall back to copy-then-remove if it fails with
	// EXDEV (cross-device link).
	if err := os.Rename(tmpBin, dst); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("rename: %w", err)
		}
		if err := copyFile(tmpBin, dst, 0755); err != nil {
			return fmt.Errorf("copy: %w", err)
		}
	}
	return nil
}

// detectCNNetwork returns "cn" if we can reach baidu.com within 3s,
// "global" if we can reach google.com, else "unknown". Mirrors the
// renderer-side probe so we use consistent mirror priorities across
// install paths.
func detectCNNetwork() string {
	probe := func(url string) bool {
		c := exec.Command("curl", "-fsI", "--max-time", "3", url)
		return c.Run() == nil
	}
	if probe("https://www.baidu.com/") {
		return "cn"
	}
	if probe("https://www.google.com/generate_204") {
		return "global"
	}
	return "unknown"
}

// findFile walks `root` and returns the first regular-file match for
// `name` (basename match). Used to locate the ffmpeg binary inside an
// extracted archive without hard-coding the provider-specific nested
// directory layout. Returns os.ErrNotExist if nothing matches.
func findFile(root, name string) (string, error) {
	var hit string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == name {
			hit = p
			return io.EOF // short-circuit the walk
		}
		return nil
	})
	if err != nil && err != io.EOF {
		return "", err
	}
	if hit == "" {
		return "", os.ErrNotExist
	}
	return hit, nil
}

// copyFile reads src and writes it to dst with the given mode.
// Used as a cross-device fallback when os.Rename fails with EXDEV.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// CJK font installation is triggered lazily — the first time the AI
// gateway sees a request body containing Chinese/Japanese/Korean
// characters. See MaybeEnsureCJKFontsForBytes below. Wrapped in
// sync.Once so concurrent gateway requests don't double-install.
var cjkFontsOnce sync.Once

// MaybeEnsureCJKFontsForBytes scans an arbitrary byte slice for CJK
// Unicode characters (Han / Hiragana / Katakana / Hangul). If found, and
// CJK fonts are not yet installed, it kicks off a one-shot background
// install. Safe to call from any hot path — does at most one fc-list
// probe per process lifetime, then no-op forever after.
func MaybeEnsureCJKFontsForBytes(b []byte) {
	if len(b) == 0 || runtime.GOOS == "darwin" || isContainerRuntime() {
		return
	}
	if !containsCJK(b) {
		return
	}
	cjkFontsOnce.Do(func() { go ensureCJKFonts() })
}

// containsCJK reports whether b contains any rune in the common CJK
// Unicode blocks. Walks the UTF-8 bytes once with utf8.DecodeRune; bails
// at the first hit.
func containsCJK(b []byte) bool {
	for i := 0; i < len(b); {
		r, sz := utf8.DecodeRune(b[i:])
		if sz == 0 {
			break
		}
		i += sz
		// CJK Unified Ideographs U+4E00–U+9FFF (Han)
		// CJK Extension A     U+3400–U+4DBF
		// Hiragana            U+3040–U+309F
		// Katakana            U+30A0–U+30FF
		// Hangul Syllables    U+AC00–U+D7AF
		// Hangul Jamo         U+1100–U+11FF
		if (r >= 0x4E00 && r <= 0x9FFF) ||
			(r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0x3040 && r <= 0x30FF) ||
			(r >= 0xAC00 && r <= 0xD7AF) ||
			(r >= 0x1100 && r <= 0x11FF) {
			return true
		}
	}
	return false
}

// ensureCJKFonts installs a CJK font set so ffmpeg / headless renderers
// can display Chinese, Japanese, and Korean text instead of tofu boxes.
// Only invoked via MaybeEnsureCJKFontsForBytes once per process.
func ensureCJKFonts() {
	if runtime.GOOS == "darwin" || isContainerRuntime() {
		return
	}
	// Quick probe: if any wqy/noto-cjk font already exists, skip.
	if out, err := exec.Command("sh", "-c", "fc-list 2>/dev/null | grep -iE 'wqy|noto.*cjk|source.han' | head -1").Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return
	}
	// Debian/Ubuntu: fonts-wqy-microhei is small (~5MB) and has a
	// permissive license; fonts-noto-cjk is bigger (~200MB) but covers
	// JP/KR. We install wqy by default; agent can apt-get noto-cjk on
	// demand if it needs JP/KR coverage.
	installCmd := sudoPrefix() + "apt-get install -y --no-install-recommends fonts-wqy-microhei fontconfig && fc-cache -f"
	log.Printf("[ai-gateway] CJK detected — installing fonts-wqy-microhei in background...")
	cmd := exec.Command("sh", "-c", installCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[ai-gateway] CJK font install failed (CJK rendering degraded): %v", err)
	} else {
		log.Printf("[ai-gateway] CJK fonts installed")
	}
}

// anyActiveAgentUsesProxy returns true if at least one active row in
// agent_config has the proxy flag flipped on (proxy_enable=1).
func anyActiveAgentUsesProxy() bool {
	var count int
	if err := store.QueryRow(
		`SELECT COUNT(*) FROM agent_config
		  WHERE active=1
		    AND COALESCE(proxy_enable, 0)=1
		    AND (
		      pane_id = 'w-10001:main.0'
		      OR pane_id IN (
		        SELECT agent_name || ':main.0'
		        FROM pane_agents
		        WHERE pane_id = 'w-10001' AND status='active'
		      )
		    )`,
	).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// startCicyMihomoIfNeeded brings up the local mihomo proxy synchronously when
// any active worker is configured to route via it. This blocks startup until
// mihomo is up so the workers don't race a half-started proxy.
//
// In v2, cicy-mihomo is installed via `cicy-code skill install cicy-mihomo`
// rather than auto-installed at startup. If the wrapper is missing the
// caller logs a warning and skips — the user is expected to install the
// skill once per host.
func startCicyMihomoIfNeeded() {
	// Start mihomo if the skill is installed (wrapper present), regardless
	// of whether any agent currently uses it as a proxy. This ensures
	// chrome-profile listeners and MATCH rules are active from the start.
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	wrapper := filepath.Join(home, ".local", "bin", "cicy-mihomo")
	if _, err := os.Stat(wrapper); err != nil {
		log.Printf("[startup] cicy-mihomo wrapper missing — proxy-using workers may fail. Install with: cicy-code skill install cicy-mihomo")
		return
	}

	// Step 1: download mihomo binary if missing. ensureMihomoBinaryInstalled
	// is idempotent (skip when already on disk).
	logPath := filepath.Join(cicyLogsDir, cicySkillsInstallLogFile)
	_ = os.MkdirAll(cicyLogsDir, 0o755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("[startup] failed to open mihomo install log: %v", err)
		return
	}
	defer logFile.Close()
	ensureMihomoBinaryInstalled(logFile, logPath)

	// Step 3: start mihomo. Skip if already running. `cicy-mihomo status`
	// always exits 0, so we parse stdout for "status: running".
	if out, err := exec.Command(wrapper, "status").Output(); err == nil && strings.Contains(string(out), "status: running") {
		return
	}
	// Generate a default config if one doesn't exist yet (idempotent — fails
	// loudly when one already exists, which is fine since we ignore the error).
	mihomoCfg := filepath.Join(home, "cicy-ai", "db", "mihomo.yaml")
	if _, err := os.Stat(mihomoCfg); os.IsNotExist(err) {
		if err := exec.Command(wrapper, "gen-config").Run(); err != nil {
			log.Printf("[startup] cicy-mihomo gen-config failed: %v", err)
			return
		}
	}
	cmd := exec.Command(wrapper, "start")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] cicy-mihomo start failed: %v (proxy-using workers may not connect)", err)
		return
	}
	log.Printf("[startup] cicy-mihomo started for proxy-using workers")
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
	ensureManagedDotfile(".tmux.conf", embeddedTmuxConf)
}

func ensureCicyTmuxConf() {
	ensureManagedDotfile(".cicy_tmux.conf", embeddedCicyTmuxConf)
}

// ensureManagedDotfile installs ~/<name>:
//   - --dev: symlink it to <repo>/<name> (the working dir is the repo root in dev
//     mode), so edits to the source file take effect without a rebuild. Any
//     existing path is removed first; a real, non-matching file is saved to .bak.
//   - otherwise: materialize the embedded content, replacing whatever is there —
//     including a stale dev-mode symlink (we must never write through it).
func ensureManagedDotfile(name, embedded string) {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[startup] failed to resolve home dir for %s: %v", name, err)
	}
	dst := filepath.Join(home, name)

	backupIfForeign := func() {
		if info, statErr := os.Lstat(dst); statErr == nil && info.Mode().IsRegular() {
			if data, readErr := os.ReadFile(dst); readErr == nil && len(data) > 0 && string(data) != embedded {
				if writeErr := os.WriteFile(dst+".bak", data, 0644); writeErr == nil {
					log.Printf("[startup] backed up %s -> %s.bak", dst, dst)
				}
			}
		}
	}

	if devMode {
		src := name
		if wd, wdErr := os.Getwd(); wdErr == nil {
			src = filepath.Join(wd, name)
		}
		if abs, absErr := filepath.Abs(src); absErr == nil {
			src = abs
		}
		if info, statErr := os.Lstat(dst); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if cur, _ := os.Readlink(dst); cur == src {
					return
				}
			} else {
				backupIfForeign()
			}
			_ = os.Remove(dst)
		}
		if linkErr := os.Symlink(src, dst); linkErr != nil {
			log.Fatalf("[startup] failed to symlink %s -> %s: %v", dst, src, linkErr)
		}
		log.Printf("[startup] dev: linked %s -> %s", dst, src)
		return
	}

	if info, statErr := os.Lstat(dst); statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			if data, readErr := os.ReadFile(dst); readErr == nil && string(data) == embedded {
				return
			}
			backupIfForeign()
		}
		_ = os.Remove(dst)
	} else {
		log.Printf("[startup] installing %s", dst)
	}
	if writeErr := os.WriteFile(dst, []byte(embedded), 0644); writeErr != nil {
		log.Fatalf("[startup] failed to write %s: %v", dst, writeErr)
	}
}

// ensureCicyShellInit writes ~/.cicy_shell_init — the rcfile tmux's
// default-command points its bash at. Owning the rcfile (instead of relying on
// the user's ~/.bashrc to auto-source things) is robust against:
//   - .bashrc not existing on slim containers
//   - .bashrc being a login-shell skip path (`[[ $- != *i* ]] && return`)
//   - users whose login shell isn't bash (.bashrc never runs at all)
//
// The wrapper still defers to the user's ~/.bashrc when present so personal
// customizations (aliases, prompt) still apply. We then unconditionally source
// ~/.cicy_tmux.conf so PATH/env/functions are always available.
//
// Always overwritten — this is a cicy-managed file; users should edit .bashrc
// or .cicy_tmux.conf, not this shim.
func ensureCicyShellInit() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	body := `# cicy-managed shell init — sourced by tmux via ` + "`bash --rcfile`" + `.
# Do NOT edit; cicy-code overwrites this file on startup. Put personal
# customizations in ~/.bashrc, and cicy-tmux logic in ~/.cicy_tmux.conf.
[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"
[ -f "$HOME/.cicy_tmux.conf" ] && . "$HOME/.cicy_tmux.conf"
`
	path := filepath.Join(home, ".cicy_shell_init")
	if existing, readErr := os.ReadFile(path); readErr == nil && string(existing) == body {
		return
	}
	_ = os.WriteFile(path, []byte(body), 0644)
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

// ensureMihomoBinaryInstalled runs `cicy-mihomo install` to download the real
// mihomo binary into ~/.local/bin/mihomo after the cicy-mihomo skill wrapper
// has landed. Idempotent: skipped if mihomo is already on PATH or at the
// canonical install location. Best-effort: a failure here doesn't fail the
// startup.
func ensureMihomoBinaryInstalled(logFile *os.File, logPath string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	mihomoTarget := filepath.Join(home, ".local", "bin", "mihomo")
	if runtime.GOOS == "windows" {
		mihomoTarget += ".exe"
	}
	// Skip if already installed somewhere reasonable.
	if info, err := os.Stat(mihomoTarget); err == nil && info.Mode()&0o111 != 0 {
		return
	}
	if _, err := exec.LookPath("mihomo"); err == nil {
		return
	}
	// Preferred path: pull the mihomo binary from our COS mirror. We host it
	// ourselves (instead of embedding in this binary or hitting the GitHub
	// release at runtime) for two reasons: COS is CN-fast and reachable when
	// GitHub is blocked, and — critically — the cicy-mihomo GitHub release's
	// amd64 asset is built with GOAMD64=v3, which SIGILLs / refuses to run on
	// pre-AVX2 CPUs (Xeon E5 v2, many cloud VMs, some WSL2). The COS copy is
	// the GOAMD64=v1 baseline build, which runs on every x86-64. Falls through
	// to the cicy-mihomo wrapper install on any failure.
	if err := downloadMihomoFromCOS(mihomoTarget); err == nil {
		log.Printf("[startup] installed mihomo (v1 baseline) from COS → %s", mihomoTarget)
		return
	} else {
		log.Printf("[startup] COS mihomo download failed (%v), falling back to cicy-mihomo install", err)
	}
	wrapper := filepath.Join(home, ".local", "bin", "cicy-mihomo")
	if _, err := os.Stat(wrapper); err != nil {
		// wrapper symlink missing — install all didn't run successfully; bail
		return
	}
	log.Printf("[startup] running cicy-mihomo install")
	fmt.Fprintf(logFile, "[%s] running cicy-mihomo install\n", time.Now().Format(time.RFC3339))
	cmd := exec.Command(wrapper, "install")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		log.Printf("[startup] cicy-mihomo install failed: %v (log: %s — proxy will be unavailable until installed manually)", err, logPath)
	}
}

// COS-hosted mihomo (GOAMD64=v1 baseline, gzip). Versioned path so we can roll
// the binary without touching this code. amd64 only — arm64 has no GOAMD64
// microarch split, so the arm64 GitHub asset is fine and the wrapper handles it.
const cosMihomoAmd64URL = "https://cicy-1372193042.cos.ap-shanghai.myqcloud.com/mihomo/v1.10.3/mihomo-linux-amd64.gz"

// downloadMihomoFromCOS fetches the gzipped v1-baseline mihomo from COS and
// decompresses it to target, atomically and executable.
func downloadMihomoFromCOS(target string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("COS mihomo mirror only hosts linux/amd64 (got %s/%s)", runtime.GOOS, runtime.GOARCH)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(cosMihomoAmd64URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("COS mihomo HTTP %d", resp.StatusCode)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, gz); err != nil { //nolint:gosec // trusted COS asset
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
}


func mustAtoi(s string) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n <= 0 {
		log.Fatalf("[startup] invalid port %q", s)
	}
	return n
}

// ensureBuiltinAgents restores tmux sessions and ttyd for all active agents.
// The selected builtin list is only used to sync builtin title/agent_type.
func ensureBuiltinAgents(selected []string) {
	workers := selectedBuiltinWorkers(selected)
	desiredByPaneID := make(map[string]builtinWorker, len(workers))
	for _, w := range workers {
		pid := builtinWorkerSession(w.Port) + ":main.0"
		desiredByPaneID[pid] = w
	}

	rows, err := store.Query(`
		SELECT pane_id, ttyd_port, workspace, COALESCE(init_script,''), COALESCE(config,'{}'),
		       COALESCE(agent_type,''), COALESCE(allow_all_actions,0),
		       COALESCE(reply_in_chinese,0), COALESCE(use_custom_gateway,0)
		FROM agent_config
		WHERE active=1
		  AND (
		    pane_id = 'w-10001:main.0'
		    OR pane_id IN (
		      SELECT agent_name || ':main.0'
		      FROM pane_agents
		      WHERE pane_id = 'w-10001' AND status='active'
		    )
		  )
		ORDER BY ttyd_port ASC, pane_id ASC
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	token := getFirstToken()
	for rows.Next() {
		var paneID, workspace, initScript, configJSON, agentType string
		var allowAllActions bool
		var replyInChinese bool
		var useCustomGateway bool
		var port int
		rows.Scan(&paneID, &port, &workspace, &initScript, &configJSON, &agentType, &allowAllActions, &replyInChinese, &useCustomGateway)
		if paneID == "" || port == 0 {
			continue
		}

		// Sync agent_type and title if changed
		if desired, ok := desiredByPaneID[paneID]; ok && normalizeAgentType(agentType) != desired.AgentType {
			store.Exec(fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, updated_at=%s WHERE pane_id=?", store.Now()),
				desired.AgentType, desired.Title, paneID)
			agentType = desired.AgentType
		}

		startAgentFromConfig(paneID, port, workspace, initScript, configJSON, agentType, allowAllActions, replyInChinese, useCustomGateway, token)
	}
}

// startAgentFromConfig brings up a single agent's tmux session, ttyd port,
// and pane environment. Idempotent — skips work that's already done.
func startAgentFromConfig(paneID string, port int, workspace, initScript, configJSON, agentType string,
	allowAllActions, replyInChinese, useCustomGateway bool, token string) {
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

// ensureAgentRunningByPaneID looks up a single agent in agent_config and brings
// it up if its tmux session is missing or its ttyd port isn't listening.
// Used by bind flows so re-binding a previously killed sub-worker auto-revives it.
func ensureAgentRunningByPaneID(paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("paneID required")
	}
	var workspace, initScript, configJSON, agentType string
	var allowAllActions, replyInChinese, useCustomGateway bool
	var port int
	err := store.QueryRow(`
		SELECT ttyd_port, COALESCE(workspace,''), COALESCE(init_script,''),
		       COALESCE(config,'{}'), COALESCE(agent_type,''),
		       COALESCE(allow_all_actions,0), COALESCE(reply_in_chinese,0),
		       COALESCE(use_custom_gateway,0)
		FROM agent_config
		WHERE pane_id=? AND active=1
	`, paneID).Scan(&port, &workspace, &initScript, &configJSON, &agentType,
		&allowAllActions, &replyInChinese, &useCustomGateway)
	if err != nil {
		return fmt.Errorf("agent_config lookup for %s: %w", paneID, err)
	}
	if port == 0 {
		return fmt.Errorf("agent %s has no ttyd_port", paneID)
	}
	startAgentFromConfig(paneID, port, workspace, initScript, configJSON, agentType,
		allowAllActions, replyInChinese, useCustomGateway, getFirstToken())
	return nil
}

// stopAgentByPaneID kills the tmux session and ttyd instance for the given
// agent. Used by unbind flows so removing a binding promptly frees resources.
// The agent_config row itself is left untouched (active stays as-is).
func stopAgentByPaneID(paneID string) {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return
	}
	stopInstance(paneID)
	session := strings.Split(paneID, ":")[0]
	if session != "" {
		exec.Command("tmux", "kill-session", "-t", session).Run()
	}
	log.Printf("[agent-stop] stopped %s", paneID)
}
