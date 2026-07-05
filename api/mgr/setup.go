package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"ttyd-go/mgr/mitm"
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
		filepath.Join(home, ".npm-global", "bin"),
		// ~/.local/bin holds our self-downloaded static binaries (ffmpeg,
		// node, mihomo) so we don't depend on brew/apt being available or
		// configured. ensureFfmpegAsync populates ffmpeg on first launch.
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".opencode", "bin"),
	}
	// filepath.SplitList / os.PathListSeparator, NOT ":" — on Windows PATH is
	// ";"-separated; a hardcoded ":" mangles the whole PATH into one bogus
	// entry and every subsequent LookPath fails.
	parts = append(parts, filepath.SplitList(os.Getenv("PATH"))...)
	seen := map[string]bool{}
	var filtered []string
	for _, part := range parts {
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		filtered = append(filtered, part)
	}
	os.Setenv("PATH", strings.Join(filtered, string(os.PathListSeparator)))
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

// npmPkgDir strips the version suffix from an npm package spec to get the
// node_modules directory name: "@anthropic-ai/claude-code@latest" → "@anthropic-ai/claude-code".
func npmPkgDir(pkg string) string {
	if len(pkg) > 0 && pkg[0] == '@' {
		if i := strings.LastIndex(pkg, "@"); i > 0 {
			return pkg[:i]
		}
		return pkg
	}
	if i := strings.Index(pkg, "@"); i > 0 {
		return pkg[:i]
	}
	return pkg
}

func npmGlobalInstallCmd(pkg string) string {
	// Registry: prefer npmmirror, fall back to npmjs.org. 主人: 老逻辑「先试 npmjs.org
	// 3s 能通就用」在 CN 是假阳性 —— npmjs.org 根路径常能响应,但实际拉包 tarball 超时
	// EAI_AGAIN(这就是 cicy-mihomo / cicy-code 装更新失败的根因)。npmmirror 是全球 CDN
	// 镜像,CN 快、海外也通,所以优先它;探不通(罕见)才回退官方源。用 -fsI(HEAD + 失败
	// 即错)比裸 -s 更可靠。
	registry := `$(if curl -fsI --max-time 5 https://registry.npmmirror.com/ >/dev/null 2>&1; then echo https://registry.npmmirror.com; else echo https://registry.npmjs.org; fi)`
	// Pre-remove the old package directory so npm's atomic rename-to-temp
	// doesn't fail with ENOTEMPTY on macOS when the directory is non-empty.
	rmOld := `rm -rf "$HOME/.npm-global/lib/node_modules/` + npmPkgDir(pkg) + `"`
	// Fetch resilience: modern CLIs (e.g. claude-code) ship the real binary as a
	// large (~70MB) platform-native OPTIONAL dependency. On a slow link / flaky
	// mirror CDN the default 5-min fetch timeout is hit and — because it's
	// optional — npm SILENTLY SKIPS it, leaving the wrapper without its native
	// binary ("native binary not installed"). Give big optional tarballs a long
	// timeout + several retries so they actually land.
	fetchOpts := `--fetch-retries=5 --fetch-retry-mintimeout=10000 --fetch-retry-maxtimeout=120000 --fetch-timeout=900000`
	return nodeEnvPreamble() + `mkdir -p "$HOME/.npm-global/bin" "$HOME/.npm-global/lib" "$HOME/.npm-global/lib/node_modules" && ` + rmOld + ` && npm install -g --include=optional ` + fetchOpts + ` --registry=` + registry + ` --prefix "$HOME/.npm-global" ` + pkg
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

// geminiInstallCmd installs Google's gemini-cli. Gemini is no longer PRESET as a
// builtin CLI agent (removed from builtinAgents/selectedAgentConfigs) — we use
// Gemini via the OpenAI-compat provider through the gateway instead. This remains
// only for the tmux launcher's legacy gemini-pane path.
func geminiInstallCmd() string {
	return npmGlobalInstallCmd("@google/gemini-cli@latest")
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
		{"claude", "claude", claudeInstallCmd(), true, false},
		{"codex", "codex", codexInstallCmd(), true, false},
		{"opencode", "opencode", opencodeInstallCmd(), true, false},
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

	// Windows:依赖由捆绑的 MSYS2/node 提供,unix 安装命令(apt/brew)必败。
	// 缺什么就警告降级,绝不 os.Exit —— 死掉的服务器对 sidecar 毫无价值。
	if runtime.GOOS == "windows" {
		for _, tool := range missing {
			fmt.Printf("  ⚠️ %s 缺失(Windows 不自动安装,请检查捆绑包/PATH;相关功能降级)\n", tool.Name)
		}
		return
	}

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
		"claude":   {"claude", "claude", claudeInstallCmd(), true, false},
		"cicy":     {"cicy", "cicy", cicyInstallCmd(), true, false},
		"codex":    {"codex", "codex", codexInstallCmd(), true, false},
		"opencode": {"opencode", "opencode", opencodeInstallCmd(), true, false},
	}
}

func ensureAgentToolInstalled(agentType string) {
	agentType = strings.TrimSpace(strings.ToLower(agentType))
	if agentType == "" {
		return
	}
	switch normalizeAgentType(agentType) {
	case "claude", "cicy", "codex", "opencode":
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
	{"cicy", "CiCy"},
}

var nonLabAllowedBuiltinAgents = []string{"claude", "codex", "opencode", "cicy"}

// cliAgentsEnabled reports whether pane-based CLI agent types (everything except
// headless cicy) are available on this host. Windows always ships cicy-only:
// there is no ConPTY pane backend by default, so claude/codex/gemini/… are
// neither seeded nor selectable. Every other OS always allows CLI agents.
func cliAgentsEnabled() bool {
	return runtime.GOOS != "windows"
}

func effectiveAllowedAgentTypes() []string {
	// Windows: only the headless cicy agent is offered (no CLI/pane agents).
	if !cliAgentsEnabled() {
		return []string{"cicy"}
	}
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

const primaryWorkerSession = "w-1001"
const primaryWorkerPaneID = "w-1001:main.0"

// Helper mode (--helper=1): a single cicy "团队助手" anchored at w-1001 (master).
// It runs headless (no tmux pane — works on Windows), uses a real shell to install
// Docker + cicy-code, then hands the user off to the full team that runs in the
// container. Helper mode has no official roster, so w-1001 is free for the helper.
const helperWorkerPort = 1001
const helperWorkerSession = "w-1001"
const helperWorkerPaneID = "w-1001:main.0"

func helperModeBuiltinWorker() builtinWorker {
	return builtinWorker{
		Port:      helperWorkerPort,
		AgentType: "cicy",
		Title:     "团队助手",
		// No role template anymore (the 团队助手 template was removed) — uses the
		// default cicy charter.
		Master: true,
	}
}

type builtinWorker struct {
	Port          int
	AgentType     string
	Title         string
	RoleTemplate  string // role template slug (~/cicy-ai/memory/agents/<slug>.md); "" = none
	Master        bool   // the w-1001 PM master (role="master"); others are "worker"
	BindToPrimary bool   // attach under w-1001 by default (shown on the master's team)
}

// officialRoleRoster is the fixed set of agents an official release preinstalls.
// The PM master anchors at w-1001; every OTHER official agent counts UP from
// w-101 (101,102,…). User-created agents count UP from w-1002 (defaultWorkerIndex
// =1001 → next id 1002), so the 101.. role band never collides with them.
// ALL roster agents are created (they live in the DB) AND attached under w-1001
// (BindToPrimary on every non-master entry), so the master's team shows the whole
// preinstalled roster out of the box.
//
// Two flavors:
//   - cicy lite agents: non-coding roles, each with a role template
//     (~/cicy-ai/memory/agents/<slug>.md) that shapes its AGENTS.md charter.
//   - CLI coding agents: claude/codex/opencode, gateway-routed
//     (use_custom_gateway via createBuiltinWorker); no role template.
func officialRoleRoster() []builtinWorker {
	// Minimal cicy roster: only two cicy specialists are preinstalled —
	//   - 知识专员 doubles as the master (team knowledge base + curation of
	//     agents' native Layer-1 auto-memory writes into the canon _inbox),
	//   - 审计策略专员 = the user's audit advisor.
	// The coding agents (claude/codex/opencode) are kept and bind under the
	// master. All other cicy roles (项目经理/HR/产品经理/…) are NOT preinstalled.
	roster := []builtinWorker{
		{Port: 1001, AgentType: "cicy", Title: "Knowledge Specialist", RoleTemplate: "knowledge-specialist", Master: true},
		{Port: 101, AgentType: "claude", Title: "Architect", BindToPrimary: true},
		{Port: 102, AgentType: "codex", Title: "Full-stack Engineer", BindToPrimary: true},
		{Port: 103, AgentType: "opencode", Title: "Software Engineer"},
		{Port: 104, AgentType: "cicy", Title: "Audit Policy Specialist", RoleTemplate: "audit-policy-specialist"},
	}
	return roster
}

// usesOfficialRoster reports whether this runtime should preinstall the fixed
// role roster instead of the legacy per-type builtin layout. Only the flagless
// default release does: an explicit --agents value (incl. "all"), lab/dev and
// helper modes all opt into the per-type path for development/override.
func usesOfficialRoster() bool {
	if helperMode {
		return false
	}
	return strings.TrimSpace(agentsFlag) == "" && !labMode && !devMode
}

// selectedBuiltinWorkers returns the builtin worker layout. Official release
// (usesOfficialRoster) → the fixed role roster (master w-1001 + roles counting
// down). Helper mode → a single Team Helper on w-6002. Otherwise (explicit
// --agents / lab / dev) → per-type, ports counting UP from 1001, master first.
func selectedBuiltinWorkers(selected []string) []builtinWorker {
	if helperMode {
		return []builtinWorker{helperModeBuiltinWorker()}
	}
	// Windows can't orchestrate a tmux team. On win32 (default, no --helper) seed
	// ONLY the 团队专员 (master) — it installs cicy-code into WSL/Docker where the
	// real team runs. Anchor it at the CANONICAL primary session w-1001
	// (primaryWorkerSession), NOT w-100: the desktop's default active pane and
	// every server-side w-1001 master assumption (im.go fallback, todo master
	// workspace, openclaw state) point at w-1001 — seeding at w-100 left w-1001
	// empty/non-functional. Only when launched by cicy-desktop (--desktop): a
	// plain win32 cicy-code without desktop has no team to seed.
	// Plain win32 cicy-code (server / inside a container, no cicy-desktop) has no
	// team to seed. Windows DESKTOP falls through to the official roster below; the
	// CLI coding agents (claude/codex/opencode) are then dropped by cicyOnlyWorkers
	// (cliAgentsEnabled()==false on Windows — no tmux panes), leaving the headless
	// cicy roles (项目经理 master + specialists) which need no panes.
	if runtime.GOOS == "windows" && !desktopMode {
		return nil
	}
	var workers []builtinWorker
	if usesOfficialRoster() {
		workers = officialRoleRoster()
	} else {
		workers = make([]builtinWorker, 0, len(selected))
		for i, agentType := range selected {
			agentType = normalizeAgentType(agentType)
			if agentType == "" {
				continue
			}
			workers = append(workers, builtinWorker{
				Port:          1001 + i,
				AgentType:     agentType,
				Title:         builtinAgentTitle(agentType),
				Master:        i == 0,
				BindToPrimary: i > 0, // per-type (dev): keep attaching all non-master under w-1001
			})
		}
	}
	// Windows: drop every CLI agent from the layout so setup never
	// initializes/installs them — only headless cicy roles are seeded.
	if !cliAgentsEnabled() {
		workers = cicyOnlyWorkers(workers)
	}
	return workers
}

// cicyOnlyWorkers keeps just the headless cicy workers, preserving the Master
// flag: if the master happened to be a (now-dropped) CLI agent, the first
// surviving cicy worker is promoted so the team still has an anchor at w-1001.
func cicyOnlyWorkers(in []builtinWorker) []builtinWorker {
	out := make([]builtinWorker, 0, len(in))
	hadMaster := false
	for _, w := range in {
		if normalizeAgentType(w.AgentType) != "cicy" {
			continue
		}
		if w.Master {
			hadMaster = true
		}
		out = append(out, w)
	}
	if !hadMaster && len(out) > 0 {
		out[0].Master = true
		out[0].BindToPrimary = false
	}
	return out
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
	// The worker number lives only in the agent id itself: pane_id
	// "w-<n>:main.0" (agent id "w-<n>", shortPaneID semantics). Recover the
	// high-water mark by parsing every row's agent id and taking the max <n>.
	rows, err := store.Query("SELECT pane_id FROM agent_config")
	if err != nil {
		log.Printf("[startup] failed to sync worker_index from agent_config: %v", err)
		return
	}
	defer rows.Close()
	maxAgentNum := 0
	for rows.Next() {
		var paneID string
		if rows.Scan(&paneID) != nil {
			continue
		}
		agentID := shortPaneID(strings.TrimSpace(paneID)) // "w-<n>:main.0" → "w-<n>"
		if i := strings.Index(agentID, ":"); i >= 0 {     // tolerate other window suffixes
			agentID = agentID[:i]
		}
		if !strings.HasPrefix(agentID, "w-") {
			continue
		}
		n, convErr := strconv.Atoi(agentID[len("w-"):])
		if convErr != nil || n <= 0 {
			continue
		}
		if n > maxAgentNum {
			maxAgentNum = n
		}
	}
	if maxAgentNum > 0 {
		// Only ever RAISE worker_index, never lower it. setWorkerIndex(max)
		// clobbered it down to the max surviving pane on every restart (the >20000
		// guard above never fires for 10xxx installs), so deleting high-numbered
		// agents + restart pulled worker_index backward → fork re-minted freed ids
		// (deleted ids leave a gap in agent_config that the create-time check can't
		// see). Keeping it at the high-water mark makes fork ids strictly monotonic.
		ensureWorkerIndexAtLeast(maxAgentNum)
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
// under primaryWorkerSession (w-1001). Idempotent thanks to the
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
			// Update agent_type and title in case they changed — except when the
			// pane was deliberately converted to a dispatcher (PM); that
			// conversion must survive restarts (it is not expressible via the
			// --agents startup flag).
			var existingType string
			store.QueryRow("SELECT COALESCE(agent_type,'') FROM agent_config WHERE pane_id=?", paneID).Scan(&existingType)
			if normalizeAgentType(existingType) != "cicy" {
				store.Exec(fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, updated_at=%s WHERE pane_id=?", store.Now()),
					w.AgentType, w.Title, paneID)
			}
			fmt.Printf("  ⏭ %s - 已存在，已更新\n", w.Title)
		} else {
			createBuiltinWorker(w)
		}
		// Only agents flagged BindToPrimary attach under w-1001 by default (HR +
		// Token优化 + 架构师/全栈/UI设计师 for the official roster). The rest are
		// created standalone; the user brings them onto the team on demand (HR
		// helps). Master never binds.
		if w.BindToPrimary && !w.Master {
			ensureWorkerBoundToPrimary(builtinWorkerSession(w.Port))
		}
	}
	if len(workers) > 0 {
		setWorkerIndex(workers[len(workers)-1].Port)
	}
}

// ensureMissingRosterMembers tops up preinstalled roster agents that don't exist
// yet on an ALREADY-set-up install. The first-boot path (count==0 → createSelected
// Workers) only runs on an empty DB, so roles ADDED in a later version (e.g. the
// governance specialists / 团队专员) never reach existing installs on update — Mac
// and older installs would never get them. This runs every startup, creates only
// the missing ones (idempotent COUNT check per pane), and leaves existing agents
// (and the user's own) untouched. Gate to the official roster only.
func ensureMissingRosterMembers(selected []string) {
	for _, w := range selectedBuiltinWorkers(selected) {
		paneID := builtinWorkerSession(w.Port) + ":main.0"
		var count int
		store.QueryRow("SELECT COUNT(*) FROM agent_config WHERE pane_id=?", paneID).Scan(&count)
		if count > 0 {
			continue // already installed
		}
		log.Printf("[startup] top-up missing preinstalled agent: %s (%s)", w.Title, paneID)
		createBuiltinWorker(w)
		if w.BindToPrimary && !w.Master {
			ensureWorkerBoundToPrimary(builtinWorkerSession(w.Port))
		}
	}
}

// reseedBuiltinGuidance regenerates each existing built-in roster agent's
// composed guidance file (CLAUDE.md / AGENTS.md) from the CURRENT templates on
// every startup. A roster agent's persona lives in its role template
// (~/cicy-ai/memory/agents/<slug>.md) and global/project are managed templates,
// so the workspace file is just a derived composition — the create-time seed is
// write-once and would otherwise stay frozen at whatever the binary produced on
// first boot (stale order / missing global+project after a version bump). Only
// touches roster members that already exist; user-created agents are never
// regenerated (their workspace file may carry hand edits).
func reseedBuiltinGuidance(selected []string) {
	for _, w := range selectedBuiltinWorkers(selected) {
		rel := guidanceFilenameForAgentType(w.AgentType)
		if rel == "" {
			continue
		}
		session := builtinWorkerSession(w.Port)
		paneID := session + ":main.0"
		var cnt int
		store.QueryRow("SELECT COUNT(1) FROM agent_config WHERE pane_id=?", paneID).Scan(&cnt)
		if cnt == 0 {
			continue
		}
		ws := builtinWorkerWorkspace(session)
		if strings.TrimSpace(ws) == "" {
			continue
		}
		content := composeGuidanceContent(ws, w.AgentType, paneID, "", w.RoleTemplate, "")
		if strings.TrimSpace(content) == "" {
			continue
		}
		if err := os.MkdirAll(ws, 0755); err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(ws, rel), []byte(content), 0644)
	}
}

func createBuiltinWorker(w builtinWorker) {
	session := fmt.Sprintf("w-%d", w.Port)
	token := getFirstToken()
	// Team Helper (--helper=1) opts OUT of our custom /api/ai-gateway: it falls
	// through to the default provider so a fresh, pre-Docker machine can talk to
	// the helper without the user having configured a gateway/token yet. Non-helper
	// builtins use our gateway as normal.
	useCustomGateway := !helperMode
	role := "worker"
	if w.Master {
		role = "master"
	}
	if _, err := createManagedPane(paneCreateOpts{
		session:          session,
		title:            w.Title,
		role:             role,
		defaultModel:     "",
		agentType:        w.AgentType,
		workspace:        builtinWorkerWorkspace(session),
		initScript:       "",
		token:            token,
		allowAllActions:  true,
		replyInChinese:   false,
		useCustomGateway: useCustomGateway,
		useProxy:         false,
		roleTemplate:     w.RoleTemplate,
		// Only BindToPrimary members (HR + Token优化) attach under w-1001; the rest
		// are created standalone (in the DB, off the master's team) until added.
		skipPrimaryBind: !w.BindToPrimary,
		// Roster builtins are created config-only (no pane). cicy members run
		// headless (warmCicySessions) — including the helper-mode 团队助手, which
		// must stay headless so it works on Windows without tmux. Non-cicy members
		// are launched only once bound under w-1001 (ensureBuiltinAgents).
		configOnly: !helperMode || normalizeAgentType(w.AgentType) == "cicy",
	}); err != nil {
		fmt.Printf("  ❌ %s 创建失败: %v\n", w.Title, err)
		return
	}
	fmt.Printf("  ✅ %s (w-%d, port %d)\n", w.Title, w.Port, w.Port)
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
	// Seed ~/cicy-ai/assets/ (with a README) so there's always a known place to
	// drop uploaded resources.
	ensureAssetsDir()
	// On a dev machine with the repo checked out, symlink the packaged seed dir
	// to ~/cicy-ai/memory-seed for easy editing (no-op elsewhere).
	ensureMemorySeedLink()
	// Seed ~/cicy-ai/memory/global.md with the default template on first boot so
	// the memory editor and agent-creation always have a base layer to compose.
	ensureGlobalMemoryTemplate()
	// Seed the official role templates (~/cicy-ai/memory/agents/<slug>.md) too, so
	// the role roster's cicy agents compose their AGENTS.md with the right charter.
	// Must run before worker creation below.
	ensureRoleMemoryTemplates()
	// Seed the out-of-the-box "default" project so every agent shares one memory
	// pool from first boot (A→B works with zero setup). Users add their own
	// projects later; unassigned agents fall back to "default" at launch.
	ensureDefaultProject()

	ensureDefaultProviders()
	applyGatewayEnvToDefaultProviders()
	ensureClientProviders()
	setupAIConfigs()

	var count int
	if err := store.QueryRow("SELECT COUNT(*) FROM agent_config").Scan(&count); err != nil {
		log.Fatalf("[startup] failed to query agent_config: %v", err)
	}
	effectiveAgentList := effectiveAgentsFlag()
	if count == 0 {
		switch {
		case helperMode:
			// Team-Helper mode short-circuits everything else: a single headless
			// cicy "团队助手" on w-1001 (selectedBuiltinWorkers returns it regardless
			// of the arg here), independent of --agents.
			createSelectedWorkers(nil)
		case isContainerRuntime():
			// Preinstalled container runtime must never block on interactive setup.
			// Respect explicit --agents=... when provided; otherwise keep the default
			// footprint minimal with only w-1001 Claude.
			if effectiveAgentList != "" {
				runSetupWithAgents(effectiveAgentList)
			} else {
				createSelectedWorkers([]string{"claude"})
			}
		case effectiveAgentList != "":
			runSetupWithAgents(effectiveAgentList)
		default:
			runSetup()
		}
	}

	selectedAgents := mustSelectedAgents()
	// Top-up preinstalled roster agents missing on an existing install (roles
	// added in a later version don't reach already-set-up machines via the
	// count==0 first-boot path above). No-op on a fresh boot (just created) and
	// for non-official layouts (--agents/lab/dev manage their own).
	if usesOfficialRoster() {
		ensureMissingRosterMembers(selectedAgents)
		// Keep preset roster guidance in sync with the current templates/order
		// (the create-time seed is write-once and goes stale across versions).
		reseedBuiltinGuidance(selectedAgents)
	}
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
	// audit-v2: no w-6001 singleton pane to bootstrap/remove anymore — the audit
	// advisor role is now an ordinary cicy agent (role_template=审计策略专员) the
	// operator onboards on demand. Collection/scanning is wired in main.go via
	// audit.Init(); finding hits dispatch to the 审计策略专员 (audit_agent_notify.go).
	go ensureFfmpegAsync()
	go ensurePreinstalledSkills()
}

var preinstalledSkills = []string{
	"agent-chrome", "agent-editor", "agent-desktop", "agent-webpage",
	"cicy-agent", "cicy-todo", "cicy-mihomo", "cicy-ssh", "proxy_ssh", "global-api-token",
	"agent-summary",
	// Team knowledge Layer 2 store CLI (add/list/recall/promote/...) — fresh
	// installs get it so agents can record/recall team knowledge out of the box.
	"cicy-knowledge",
	// Audit policy + log CLI — the 审计策略专员 works through this skill (shell +
	// skill, no built-in audit_* tools), so it must be present on fresh installs.
	"cicy-audit-policy",
	// Skill ecosystem conventions (private dev / team install / public PR) —
	// every agent should know these by default.
	"cicy-skill-spec",
	// Author custom cicy agents (persona + tools + model) from the CLI; backs the
	// "build an agent like a skill" flow (~/cicy-ai/agents/<slug>/AGENT.md).
	"agent-creator",
	// Self-hosted email (SMTP/IMAP/POP3) — lets any agent send mail / notify the
	// user on task completion out of the box; reuses the same email.json the
	// token-delivery UI configures. `email status --check` verifies live login.
	"email",
}

func ensurePreinstalledSkills() {
	installed, err := skillcmd.InstalledSkills()
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
		if _, err := skillcmd.InstallSkill(name, io.Discard); err != nil {
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

	// Re-surface installed skills into each detected agent's skills dir
	// (~/.<agent>/skills/). Backfills dirs wiped by an agent CLI reset, or
	// agents that appeared after the skills were installed (e.g. opencode's
	// ~/.opencode/skills coming up empty). Idempotent. See repair.go.
	surfaced, serrs := skillcmd.EnsureAgentSurfacing()
	if len(surfaced) > 0 {
		log.Printf("[startup] surfaced skills to agents: %v", surfaced)
	}
	for _, e := range serrs {
		log.Printf("[startup] skill surfacing: %v", e)
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
	// ~/.local/bin/ffmpeg (already on PATH via extendPATH). Falls back
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
// $HOME/.local/bin/ffmpeg. The destination is on PATH via
// extendPATH, so `exec.LookPath("ffmpeg")` resolves to it immediately
// after this returns. Cross-platform: works the same on Linux, macOS,
// and inside WSL Ubuntu — no brew, no apt, no sudo.
//
// Sources picked for stability + China reachability:
//   - Linux:   johnvansickle.com (mirrored via ghproxy.net for CN)
//   - macOS:   evermeet.cx (single-binary tarballs, x64 + arm64)
//
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
	binDir := filepath.Join(home, ".local", "bin")
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
	// Atomic rename within the same filesystem (~/.local/bin is on
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
		      pane_id = 'w-1001:main.0'
		      OR pane_id IN (
		        SELECT agent_name || ':main.0'
		        FROM pane_agents
		        WHERE pane_id = 'w-1001' AND status='active'
		      )
		    )`,
	).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// cicyMihomoHealOnce ensures at most one background self-heal goroutine per process.
var cicyMihomoHealOnce sync.Once

// selfHealCicyMihomo retries the cicy-mihomo skill install + ~/.local/bin wrapper
// symlink in the background until the wrapper exists, then (re)starts mihomo. Stops a
// transient network failure during first setup from permanently breaking the proxy
// (用户原本只能整个重建容器才恢复). w-10122 #199.
func selfHealCicyMihomo() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	wrapper := filepath.Join(home, ".local", "bin", "cicy-mihomo")
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := os.Stat(wrapper); err == nil {
			log.Printf("[self-heal] cicy-mihomo wrapper present — (re)starting mihomo")
			startCicyMihomoIfNeeded()
			return
		}
		log.Printf("[self-heal] retrying cicy-mihomo skill install")
		if _, ierr := skillcmd.InstallSkill("cicy-mihomo", io.Discard); ierr != nil {
			log.Printf("[self-heal] cicy-mihomo install retry failed: %v", ierr)
			continue
		}
		_, _ = skillcmd.EnsureBinSymlinks()
	}
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
		// The cicy-mihomo skill is normally installed by ensurePreinstalledSkills(),
		// which runs asynchronously (go ...) and races this synchronous startup
		// path — on a fresh boot the wrapper isn't on disk yet, so mihomo would
		// never come up (and with global egress defaulting on, MITM would fail
		// open to direct forever). Install it synchronously here (idempotent) and
		// re-check before giving up.
		log.Printf("[startup] cicy-mihomo wrapper missing — installing skill synchronously")
		// 网络抖动一次就挂(w-10122 #199):InstallSkill 失败原来直接 return,改成重试
		// 3 次 + 退避,瞬时网络失败可恢复。
		var ierr error
		for attempt := 1; attempt <= 3; attempt++ {
			if _, ierr = skillcmd.InstallSkill("cicy-mihomo", io.Discard); ierr == nil {
				break
			}
			log.Printf("[startup] cicy-mihomo install attempt %d/3 failed: %v", attempt, ierr)
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
		// 无论 InstallSkill 是否认为「已装」,都强制重建 ~/.local/bin 软链并校验 wrapper:
		// skill 目录在但软链没建好正是 'cicy-mihomo not found on PATH' 的根因。
		_, _ = skillcmd.EnsureBinSymlinks()
		if _, err := os.Stat(wrapper); err != nil {
			// 仍缺 → 不再「只能整个重建容器」,挂后台周期自愈(每进程最多一个)。
			log.Printf("[startup] cicy-mihomo wrapper still missing after install (last err: %v) — scheduling background self-heal", ierr)
			cicyMihomoHealOnce.Do(func() { go selfHealCicyMihomo() })
			return
		}
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

	// Step 3: guarantee mihomo is actually up. The wrapper tracks liveness via a
	// PID file that goes STALE across a container/process restart — the old pid
	// may be dead or reused — so `cicy-mihomo status`/`start` can wrongly report
	// "already running" and skip launching, leaving :9001 dead. cicy-code MUST
	// bring mihomo up, so we ignore the wrapper's PID bookkeeping and check the
	// controller for REAL liveness; when it's not actually answering we clear the
	// stale PID file and (re)start, verifying after each attempt.
	if mihomoControllerAlive(2 * time.Second) {
		return // already actually running
	}
	// Generate a default config if one doesn't exist yet (idempotent — fails
	// loudly when one already exists, which is fine since we ignore the error).
	mihomoCfg := filepath.Join(home, "cicy-ai", "db", "mihomo.yaml")
	if _, err := os.Stat(mihomoCfg); os.IsNotExist(err) {
		if err := mihomoWrapperCmd(wrapper, "gen-config").Run(); err != nil {
			log.Printf("[startup] cicy-mihomo gen-config failed: %v", err)
			return
		}
	}
	stalePidFile := filepath.Join(home, ".local", "state", "cicy-skills", "mihomo", "pid")
	for attempt := 1; attempt <= 3; attempt++ {
		// Clear any stale PID so the wrapper doesn't short-circuit to
		// "already running" against a dead/reused pid and skip the launch.
		_ = os.Remove(stalePidFile)
		cmd := mihomoWrapperCmd(wrapper, "start")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run() // exit status is unreliable (stale-pid quirk); verify via controller
		if mihomoControllerAlive(6 * time.Second) {
			log.Printf("[startup] cicy-mihomo started for proxy-using workers")
			return
		}
		log.Printf("[startup] cicy-mihomo not up after start attempt %d/3; retrying", attempt)
	}
	log.Printf("[startup] cicy-mihomo failed to come up after 3 attempts (proxy-using workers may not connect)")
}

// ensureMemorySeedLink symlinks the repo's packaged seed dir
// (~/projects/cicy-code/api/mgr/embed/memory-seed) to ~/cicy-ai/memory-seed on a
// DEV machine that has the repo checked out — a convenience so the seed
// templates can be edited from the cicy-ai tree. It's a pure shortcut: no code
// reads ~/cicy-ai/memory-seed (the binary seeds from the EMBEDDED copy). No-op
// when the repo dir is absent (non-dev hosts) or when the link path already
// exists (never clobber an operator's own file/dir/link).
func ensureMemorySeedLink() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	src := filepath.Join(home, "projects", "cicy-code", "api", "mgr", "embed", "memory-seed")
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return // repo seed dir not present — nothing to link
	}
	link := filepath.Join(cicyRootDir, "memory-seed")
	if _, err := os.Lstat(link); err == nil {
		return // already exists (link/dir/file) — don't clobber
	} else if !os.IsNotExist(err) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		log.Printf("[memory-seed] mkdir failed: %v", err)
		return
	}
	if err := os.Symlink(src, link); err != nil {
		log.Printf("[memory-seed] symlink %s -> %s failed: %v", link, src, err)
		return
	}
	log.Printf("[memory-seed] linked %s -> %s", link, src)
}

// ensureAssetsDir creates ~/cicy-ai/assets/ and seeds a README on first boot,
// giving users (and agents) a known, stable location to drop uploaded
// resources. The README is only written when absent — an operator's edits are
// never clobbered; the directory is left alone if it already exists.
func ensureAssetsDir() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		log.Printf("[assets] resolve home failed (skip seed): %v", err)
		return
	}
	dir := filepath.Join(home, "cicy-ai", "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[assets] mkdir failed (skip seed): %v", err)
		return
	}
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); err == nil {
		return // already present — never clobber
	} else if !os.IsNotExist(err) {
		log.Printf("[assets] stat README failed (skip seed): %v", err)
		return
	}
	body := "# Assets\n\n" +
		"This directory holds uploaded resources — images, documents, data files,\n" +
		"and any other assets you or your agents need to reference.\n\n" +
		"Drop files here and reference them by their path under `~/cicy-ai/assets/`.\n"
	if err := os.WriteFile(readme, []byte(body), 0o644); err != nil {
		log.Printf("[assets] seed README write failed: %v", err)
		return
	}
	log.Printf("[assets] seeded %s", readme)
}

// ensureMITMConfig seeds ~/cicy-ai/mitm/config.json with {"enabled": true} on
// first boot, so non-gateway agents (codex/opencode/claude official login) are
// routed through the local MITM and audited by default. If the file already
// exists it is left untouched — the operator's explicit config always wins,
// including a deliberate {"enabled": false}.
//
// Must run BEFORE startMITM() (main.go) so the very first boot already brings
// the MITM up; that's why it's invoked there rather than from checkEnv().
// Path matches mitm.DefaultConfigPath() (~/cicy-ai/mitm/config.json).
func ensureMITMConfig() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		log.Printf("[mitm] resolve home failed (skip seed): %v", err)
		return
	}
	dir := filepath.Join(home, "cicy-ai", "mitm")
	path := filepath.Join(dir, "config.json")
	if _, err := os.Stat(path); err == nil {
		return // already configured — never clobber
	} else if !os.IsNotExist(err) {
		log.Printf("[mitm] stat config failed (skip seed): %v", err)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[mitm] mkdir config dir failed (skip seed): %v", err)
		return
	}
	if err := os.WriteFile(path, []byte("{\"enabled\": true}\n"), 0o644); err != nil {
		log.Printf("[mitm] seed config write failed: %v", err)
		return
	}
	log.Printf("[mitm] seeded default config (enabled) at %s", path)
}

// ensureMITMCAInSystemTrust installs the MITM CA into the LINUX system trust
// store so NON-node agents trust the intercepted TLS. node agents (opencode /
// claude via undici) already trust it via NODE_EXTRA_CA_CERTS, but codex and
// kiro-cli do their real HTTP from a Rust binary that ignores that env and reads
// /etc/ssl/certs instead, which update-ca-certificates populates.
//
// Linux-only on purpose. macOS root-CA trust calls SecTrustSettingsSetTrustSettings,
// which REQUIRES an interactive GUI authorization even as root — a detached
// daemon can't do it silently, and a half-done attempt only leaves an untrusted
// cert in the keychain. So native-macOS users install manually via the audit
// dashboard's "install CA" card (downloads /ca.pem = this node's MITM CA and
// shows `cicy-code mitm install-ca`). The common docker-on-macOS deployment is
// unaffected — agents there run in the Linux container and hit this path.
//
// Why the system store and not SSL_CERT_FILE in boot: update-ca-certificates
// APPENDS our CA to the bundle, so real roots (passthrough hosts, npm, etc.)
// keep working; pointing SSL_CERT_FILE at the bare MITM CA would REPLACE the
// bundle and break TLS to every non-intercepted host.
//
// Idempotent, best-effort (logs and returns on any failure — never blocks boot).
// Must run AFTER startMITM() (main.go), which generates the CA on first start.
// ensureMITMCAInSystemTrust is the BOOT path. Modifying the OS trust anchors is
// gated behind explicit user consent (compliance §1.3/§1.4): on first boot we do
// nothing — the desktop consent card (POST /api/mitm/consent) or `cicy-code mitm
// install-ca` records the opt-in and installs. Once consented, re-running here on
// later boots re-trusts a regenerated CA silently. All three platforms share the
// same gate (mitm.CATrustConsented); a container may bake CICY_CA_TRUST_CONSENT=1.
func ensureMITMCAInSystemTrust() {
	if !mitm.CATrustConsented() {
		return // no opt-in yet — node agents still trust via NODE_EXTRA_CA_CERTS
	}
	if err := installMITMCAOSTrust(); err != nil {
		log.Printf("[mitm] system-trust refresh skipped: %v", err)
	}
}

// installMITMCAOSTrust performs the platform OS-trust install for the locally
// generated MITM CA. Returns nil if already trusted or installed successfully;
// an error otherwise. Caller is responsible for the consent gate — this does the
// privileged write. Shared by the boot refresh and the /api/mitm/consent handler.
func installMITMCAOSTrust() error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("resolve home: %v", err)
	}
	src := filepath.Join(home, "cicy-ai", "db", "mitm-ca.crt")
	srcBytes, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("CA not generated yet (start with MITM enabled once): %v", err)
	}
	switch runtime.GOOS {
	case "windows":
		// LocalMachine\ROOT via CryptoAPI (silent when the process is elevated;
		// a running non-elevated process cannot elevate on demand → need_elevation).
		if err := mitm.InstallRootCA(srcBytes); err != nil {
			return err
		}
		log.Printf("[mitm] installed MITM CA into LocalMachine\\ROOT (thumbprint %s) — codex/kiro schannel TLS now trusts it", mitm.CertThumbprint(srcBytes))
		return nil
	case "linux":
		const dst = "/usr/local/share/ca-certificates/cicy-mitm.crt"
		if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, srcBytes) {
			return nil // already installed this exact CA
		}
		runRoot := func(name string, args ...string) error {
			if os.Geteuid() != 0 {
				args = append([]string{"-n", name}, args...)
				name = "sudo"
			}
			out, err := exec.Command(name, args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(out)))
			}
			return nil
		}
		if err := runRoot("cp", src, dst); err != nil {
			return fmt.Errorf("need root/sudo: %v", err)
		}
		if err := runRoot("update-ca-certificates"); err != nil {
			return fmt.Errorf("update-ca-certificates: %v", err)
		}
		log.Printf("[mitm] installed MITM CA into system trust store (%s) — codex/kiro Rust TLS now trusts it", dst)
		return nil
	case "darwin":
		// Install into the USER's login keychain + user trust domain — NO admin/sudo
		// required (a standard, non-admin user can trust a root for THEMSELVES).
		// System.keychain would need admin (and SecTrust refuses to set trust without
		// GUI auth even as root), so we deliberately avoid it: most users aren't
		// admins, and node agents trust the CA via NODE_EXTRA_CA_CERTS regardless.
		// macOS still shows a one-time "modify trust settings" dialog (the user's own
		// LOGIN password = the second consent) in the desktop-spawned daemon's GUI
		// session. If no GUI session can show it, surface need_elevation so the consent
		// endpoint retries via the CLI in the user's session.
		home, _ := os.UserHomeDir()
		loginKC := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		// Idempotency (matches the linux branch above). `security add-trusted-cert`
		// re-prompts for the login password on EVERY call — macOS asks to modify
		// trust settings each time — so the per-boot ensureMITMCAInSystemTrust
		// refresh pops a password dialog on EVERY launch even when the CA is already
		// installed and trusted. Skip when our CA (matched by SHA-1) is already in
		// the login keychain: we always add it with -r trustRoot, so present ⇒
		// trusted. find-certificate is read-only and never prompts.
		if fp := mitm.CertThumbprint(srcBytes); fp != "" {
			if found, err := exec.Command("security", "find-certificate", "-a", "-Z", loginKC).CombinedOutput(); err == nil {
				if strings.Contains(strings.ToUpper(string(found)), fp) {
					return nil // already installed + trusted — no re-prompt
				}
			}
		}
		out, err := exec.Command("security", "add-trusted-cert", "-r", "trustRoot",
			"-k", loginKC, src).CombinedOutput()
		if err != nil {
			return fmt.Errorf("need_elevation: security add-trusted-cert (user): %v: %s", err, strings.TrimSpace(string(out)))
		}
		log.Printf("[mitm] installed MITM CA into login keychain (user trust, no admin) — codex/kiro TLS now trusts it")
		return nil
	}
	return fmt.Errorf("OS trust install not supported on %s", runtime.GOOS)
}

// uninstallMITMCAOSTrust removes the MITM CA from the OS trust store (revoke
// path). Best-effort: logs and continues on error so the consent flag is still
// cleared by the caller. Mirrors installMITMCAOSTrust's per-platform mechanics.
func uninstallMITMCAOSTrust() {
	switch runtime.GOOS {
	case "windows":
		home, _ := os.UserHomeDir()
		if b, err := os.ReadFile(filepath.Join(home, "cicy-ai", "db", "mitm-ca.crt")); err == nil {
			if err := mitm.RemoveRootCA(b); err != nil {
				log.Printf("[mitm] Windows CA trust uninstall: %v", err)
			}
		}
	case "linux":
		const dst = "/usr/local/share/ca-certificates/cicy-mitm.crt"
		runRoot := func(name string, args ...string) error {
			if os.Geteuid() != 0 {
				args = append([]string{"-n", name}, args...)
				name = "sudo"
			}
			return exec.Command(name, args...).Run()
		}
		_ = runRoot("rm", "-f", dst)
		_ = runRoot("update-ca-certificates", "--fresh")
	case "darwin":
		// Remove from the login keychain (where we now install) and, best-effort,
		// the System keychain (older installs / admin-scope installs).
		home, _ := os.UserHomeDir()
		loginKC := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		_ = exec.Command("security", "delete-certificate", "-c", "cicy-mitm", loginKC).Run()
		_ = exec.Command("security", "delete-certificate", "-c", "cicy-mitm",
			"/Library/Keychains/System.keychain").Run()
	}
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
# Reap this pane's child processes (agent CLIs, e.g. opencode) when the shell
# exits. tmux teardown sends SIGHUP, but some tools ignore it and reparent to
# PID 1, leaking across sessions until they exhaust RAM. Force-kill our direct
# children on exit so a retired pane never leaves a daemon behind.
trap 'pkill -KILL -P "$$" 2>/dev/null' EXIT
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
	// 有就不装: mihomo already resolvable → no work, no network. "Resolvable" means
	// EITHER the cicy-mihomo runtime store (~/cicy-ai/runtime/mihomo/<ver>/, the
	// skill's primary location since v1.4.x), OR the legacy ~/.local/bin/mihomo
	// (what the cicy-desktop app pre-seeds from its bundle, zero network), OR PATH.
	// Checking only ~/.local/bin would false-negative after a runtime-store install
	// and trigger a redundant re-download. Version-aware upgrades are the skill's
	// job (`cicy-mihomo install` compares installed vs npm-latest); we don't
	// re-check every boot to stay network-free.
	if mihomoBinaryResolvable(home, mihomoTarget) {
		return
	}
	// 没有就装. Primary channel is npm — the cicy-mihomo skill `install` pulls the
	// per-platform cicy-mihomo-<plat> subpackage (与 cicy-code 一样, 主人指令) into
	// the runtime store.
	wrapper := filepath.Join(home, ".local", "bin", "cicy-mihomo")
	if _, err := os.Stat(wrapper); err == nil {
		log.Printf("[startup] running cicy-mihomo install (npm)")
		fmt.Fprintf(logFile, "[%s] running cicy-mihomo install (npm)\n", time.Now().Format(time.RFC3339))
		cmd := mihomoWrapperCmd(wrapper, "install")
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.Env = os.Environ()
		if err := cmd.Run(); err != nil {
			log.Printf("[startup] cicy-mihomo install failed: %v (log: %s)", err, logPath)
		}
		if mihomoBinaryResolvable(home, mihomoTarget) {
			return // install succeeded (runtime store or ~/.local/bin)
		}
	}
	// Last-resort fallback (linux/amd64 only): COS v1-baseline mihomo. Kept for
	// the GOAMD64=v3 SIGILL case (pre-AVX2 CPUs) and when npm is unreachable.
	if err := downloadMihomoFromCOS(mihomoTarget); err == nil {
		log.Printf("[startup] installed mihomo (v1 baseline) from COS → %s", mihomoTarget)
		return
	} else if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		log.Printf("[startup] COS mihomo fallback also failed: %v (proxy unavailable until installed manually)", err)
	}
}

// mihomoBinaryResolvable reports whether a runnable mihomo binary already exists,
// matching the cicy-mihomo wrapper's own resolution order: the runtime store
// (~/cicy-ai/runtime/mihomo/<current>/mihomo, from versions.json), then the
// legacy ~/.local/bin/mihomo, then PATH.
func mihomoBinaryResolvable(home, legacyTarget string) bool {
	binName := "mihomo"
	if runtime.GOOS == "windows" {
		binName = "mihomo.exe"
	}
	// Runtime store: ~/cicy-ai/runtime/versions.json → .mihomo.current.
	if data, err := os.ReadFile(filepath.Join(home, "cicy-ai", "runtime", "versions.json")); err == nil {
		var v struct {
			Mihomo struct {
				Current string `json:"current"`
			} `json:"mihomo"`
		}
		if json.Unmarshal(data, &v) == nil && v.Mihomo.Current != "" {
			p := filepath.Join(home, "cicy-ai", "runtime", "mihomo", v.Mihomo.Current, binName)
			if info, err := os.Stat(p); err == nil && info.Mode()&0o111 != 0 {
				return true
			}
		}
	}
	if info, err := os.Stat(legacyTarget); err == nil && info.Mode()&0o111 != 0 {
		return true
	}
	if _, err := exec.LookPath("mihomo"); err == nil {
		return true
	}
	return false
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

	// Launch only NON-CICY agents bound under w-1001. cicy agents (master + any
	// team member) run headless and are warmed server-side (warmCicySessions), so
	// they never need a tmux pane here. The `active` column is no longer a launch
	// gate — membership (bound under w-1001) + type (non-cicy) decides. This is
	// what keeps a fresh runtime from booting/installing CLIs for agents the user
	// hasn't pulled onto the team yet.
	rows, err := store.Query(`
		SELECT pane_id, workspace, COALESCE(init_script,''), COALESCE(config,'{}'),
		       COALESCE(agent_type,''), COALESCE(allow_all_actions,0),
		       COALESCE(reply_in_chinese,0), COALESCE(use_custom_gateway,0),
		       COALESCE(use_mitm,1)
		FROM agent_config
		WHERE COALESCE(agent_type,'') NOT IN ('cicy','dispatcher','secretary')
		  AND pane_id IN (
		    SELECT agent_name || ':main.0'
		    FROM pane_agents
		    WHERE pane_id = 'w-1001' AND status='active'
		  )
		ORDER BY pane_id ASC
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
		var useMitm bool
		rows.Scan(&paneID, &workspace, &initScript, &configJSON, &agentType, &allowAllActions, &replyInChinese, &useCustomGateway, &useMitm)
		if paneID == "" {
			continue
		}

		// Sync agent_type and title if changed — but never clobber a
		// deliberate dispatcher (PM) conversion; it is not expressible via
		// the --agents flag and must survive restarts.
		if desired, ok := desiredByPaneID[paneID]; ok && normalizeAgentType(agentType) != desired.AgentType && normalizeAgentType(agentType) != "cicy" {
			store.Exec(fmt.Sprintf("UPDATE agent_config SET agent_type=?, title=?, updated_at=%s WHERE pane_id=?", store.Now()),
				desired.AgentType, desired.Title, paneID)
			agentType = desired.AgentType
		}

		startAgentFromConfig(paneID, workspace, initScript, configJSON, agentType, allowAllActions, replyInChinese, useCustomGateway, useMitm, token)
	}
}

// startAgentFromConfig brings up a single agent's tmux session and pane
// environment. Idempotent — skips work that's already done.
func startAgentFromConfig(paneID string, workspace, initScript, configJSON, agentType string,
	allowAllActions, replyInChinese, useCustomGateway, useMitm bool, token string) {
	// Ensure tmux session
	sess := strings.Split(paneID, ":")[0]
	sessionCreated := false
	if tmuxCommand("has-session", "-t", sess).Run() != nil {
		if workspace == "" {
			workspace = builtinWorkerWorkspace(sess)
		}
		ensureAgentToolInstalled(agentType)
		os.MkdirAll(workspace, 0755)
		tmuxCommand("new-session", "-d", "-s", sess, "-n", "main", "-c", toPosixPath(workspace)).Run()
		log.Printf("[startup] created session %s", sess)
		sessionCreated = true
	}

	// ttyd is served on demand inline; no per-pane instance to ensure.
	if sessionCreated {
		initPaneEnv(paneEnvOpts{
			paneID:          paneID,
			configJSON:      configJSON,
			workspace:       workspace,
			initScript:      initScript,
			agentType:       agentType,
			allowAllActions: allowAllActions,
			replyInChinese:  replyInChinese,
			// Forward use_custom_gateway from the agent config so the regenerated
			// boot.sh matches it. Without this it defaulted to false on every
			// session-recreate (i.e. container restart), clobbering a gateway
			// agent's boot.sh into the non-gateway (official-login + MITM) path
			// even though use_custom_gateway=true in the DB.
			useCustomGateway: useCustomGateway,
			useMitm:          useMitm,
		})
	}
}

// ensureAgentRunningByPaneID looks up a single agent in agent_config and brings
// it up if its tmux session is missing.
// Used by bind flows so re-binding a previously killed sub-worker auto-revives it.
func ensureAgentRunningByPaneID(paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("paneID required")
	}
	var workspace, initScript, configJSON, agentType string
	var allowAllActions, replyInChinese, useCustomGateway, useMitm bool
	err := store.QueryRow(`
		SELECT COALESCE(workspace,''), COALESCE(init_script,''),
		       COALESCE(config,'{}'), COALESCE(agent_type,''),
		       COALESCE(allow_all_actions,0), COALESCE(reply_in_chinese,0),
		       COALESCE(use_custom_gateway,0), COALESCE(use_mitm,1)
		FROM agent_config
		WHERE pane_id=? AND active=1
	`, paneID).Scan(&workspace, &initScript, &configJSON, &agentType,
		&allowAllActions, &replyInChinese, &useCustomGateway, &useMitm)
	if err != nil {
		return fmt.Errorf("agent_config lookup for %s: %w", paneID, err)
	}
	startAgentFromConfig(paneID, workspace, initScript, configJSON, agentType,
		allowAllActions, replyInChinese, useCustomGateway, useMitm, getFirstToken())
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
	// No ttyd instance to stop; killing the tmux session EOFs any live attach.
	session := strings.Split(paneID, ":")[0]
	if session != "" {
		stopShellSession(session) // tear down the grouped shell session
		tmuxCommand("kill-session", "-t", session).Run()
	}
	log.Printf("[agent-stop] stopped %s", paneID)
}
