package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Tool struct {
	Name        string
	Command     string
	InstallCmd  string
	Required    bool
	Installed   bool
}

// 获取用户 shell 的 rc 文件路径
func shellRC() string {
	shell := os.Getenv("SHELL")
	if strings.Contains(shell, "zsh") {
		return "~/.zshrc"
	}
	return "~/.bashrc"
}

func checkEnvironment() []Tool {
	tools := []Tool{
		// 基础环境（必须全部成功）
		{"unzip", "unzip", "sudo apt-get update && sudo apt-get install -y unzip", true, false},
		{"tmux", "tmux", "sudo apt-get update && sudo apt-get install -y tmux", true, false},
		{"git", "git", "sudo apt-get update && sudo apt-get install -y git", true, false},
		{"node", "node", "curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs", true, false},
		// AI 工具
		{"kiro-cli", "kiro-cli", "curl -fsSL https://cli.kiro.dev/install -o /tmp/kiro-install.sh && yes | bash /tmp/kiro-install.sh && echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> " + shellRC() + " && export PATH=\"$HOME/.local/bin:$PATH\"", true, false},
		{"claude", "claude", "sudo npm install -g @anthropic-ai/claude-code", true, false},
		{"gemini", "gemini", "sudo npm install -g @google/gemini-cli", true, false},
		{"codex", "codex", "sudo npm install -g @openai/codex", true, false},
		{"opencode", "opencode", "curl -fsSL https://opencode.ai/install | bash && echo 'export PATH=\"$HOME/.opencode/bin:$PATH\"' >> " + shellRC() + " && export PATH=\"$HOME/.opencode/bin:$PATH\"", true, false},
	}

	// 扩展 PATH 包含用户安装目录
	home, _ := os.UserHomeDir()
	extraPaths := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".opencode", "bin"),
	}
	os.Setenv("PATH", strings.Join(extraPaths, ":")+":"+os.Getenv("PATH"))

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
	// Optional agents (user picks from these)
	agents := []struct {
		Name string
		Desc string
	}{
		{"claude", "Claude Code - Anthropic 代码助手"},
		{"copilot", "GitHub Copilot CLI - GitHub AI 助手"},
		{"gemini", "Gemini CLI - Google AI 助手"},
		{"codex", "OpenAI Codex - 代码生成助手"},
	}

	fmt.Println("\n🤖 选择要安装的 AI 工具:")
	fmt.Println("  ✅ Kiro CLI Assistant - 多功能 AI 助手 (必装)")
	fmt.Println("  ✅ OpenCode - 开源代码助手 (必装)")
	for i, agent := range agents {
		fmt.Printf("  %d. %s\n", i+1, agent.Desc)
	}
	fmt.Println("  a. 全选 (安装所有工具)")
	fmt.Print("\n请输入要安装的工具编号 (用空格分隔，如: 1 2 3，或输入 a 全选): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	// kiro-cli and opencode are mandatory
	selected := []string{"kiro-cli", "opencode"}

	if input == "" || input == "a" || input == "A" {
		return []string{"kiro-cli", "claude", "copilot", "gemini", "codex", "opencode"}
	}

	parts := strings.Fields(input)
	for _, part := range parts {
		if len(part) == 1 && part[0] >= '1' && int(part[0]-'0') <= len(agents) {
			idx := int(part[0] - '1')
			selected = append(selected, agents[idx].Name)
		}
	}

	fmt.Printf("✅ 已选择: %v\n", selected)
	return selected
}

func installSelectedAgents(selected []string) {
	agentConfigs := map[string]Tool{
		"kiro-cli": {"kiro-cli", "kiro-cli", fmt.Sprintf("curl -fsSL https://cli.kiro.dev/install -o /tmp/kiro-install.sh && yes | bash /tmp/kiro-install.sh && echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> %s && export PATH=\"$HOME/.local/bin:$PATH\"", shellRC()), true, false},
		"claude": {"claude", "claude", "sudo npm install -g @anthropic-ai/claude-code", true, false},
		"copilot": {"copilot", "copilot", "brew install copilot-cli", true, false},
		"gemini": {"gemini", "gemini", "sudo npm install -g @google/gemini-cli", true, false},
		"codex": {"codex", "codex", "sudo npm install -g @openai/codex", true, false},
		"opencode": {"opencode", "opencode", fmt.Sprintf("curl -fsSL https://opencode.ai/install | bash && echo 'export PATH=\"$HOME/.opencode/bin:$PATH\"' >> %s && export PATH=\"$HOME/.opencode/bin:$PATH\"", shellRC()), true, false},
	}

	fmt.Printf("\n📦 安装选中的 AI 工具 (%d 个)...\n", len(selected))
	
	// 扩展 PATH 以检测用户目录下的工具
	home, _ := os.UserHomeDir()
	os.Setenv("PATH", filepath.Join(home, ".local", "bin")+":"+filepath.Join(home, ".opencode", "bin")+":"+os.Getenv("PATH"))

	for _, name := range selected {
		if config, exists := agentConfigs[name]; exists {
			// 已安装则跳过
			if _, err := exec.LookPath(config.Command); err == nil {
				fmt.Printf("  ✅ %s (已安装)\n", config.Name)
				continue
			}
			fmt.Printf("  安装 %s...", config.Name)
			
			cmd := exec.Command("sh", "-c", config.InstallCmd)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Printf(" ❌ 失败: %v\n", err)
				fmt.Printf("❌ %s 安装失败，请检查网络连接\n", config.Name)
				os.Exit(1)
			} else {
				fmt.Printf(" ✅ 完成\n")
			}
		}
	}
}

// builtinAgents defines the 6 built-in agents with fixed ports 10001-10006.
var builtinAgents = []struct {
	Port      int
	AgentType string
	Title     string
}{
	{10001, "kiro-cli", "Kiro CLI Assistant"},
	{10002, "claude", "Claude Code Assistant"},
	{10003, "copilot", "GitHub Copilot CLI"},
	{10004, "gemini", "Gemini AI Assistant"},
	{10005, "codex", "OpenAI Codex Assistant"},
	{10006, "opencode", "OpenCode Assistant"},
}

func createSelectedWorkers(selected []string) {
	fmt.Println("\n🚀 创建选中的 Workers...")
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
		// Skip if already in DB
		var count int
		store.QueryRow("SELECT COUNT(*) FROM agent_config WHERE agent_type=?", ba.AgentType).Scan(&count)
		if count > 0 {
			fmt.Printf("  ⏭ %s - 已存在，跳过\n", ba.Title)
			continue
		}
		createBuiltinWorker(ba.Port, ba.AgentType, ba.Title)
	}
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
	fmt.Printf("  ✅ %s (w-%d, port %d)\n", title, port, port)
}

func runSetup() {
	fmt.Println("🎯 Cicy Code 环境初始化")
	fmt.Println("=" + strings.Repeat("=", 30))

	// 1. 检查基础环境
	baseTools := []Tool{
		{"unzip", "unzip", "sudo apt-get update && sudo apt-get install -y unzip", true, false},
		{"tmux", "tmux", "sudo apt-get update && sudo apt-get install -y tmux", true, false},
		{"git", "git", "sudo apt-get update && sudo apt-get install -y git", true, false},
		{"node", "node", "curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs", true, false},
	}

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

	// 3. 更新 PATH (include Homebrew on macOS)
	os.Setenv("PATH", "/opt/homebrew/bin:/usr/local/bin:/usr/bin:"+os.Getenv("PATH"))

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
// agentList is comma-separated, e.g. "kiro-cli,claude" or "all".
func runSetupWithAgents(agentList string) {
	fmt.Println("🎯 Cicy Code 环境初始化 (non-interactive)")
	fmt.Println("=" + strings.Repeat("=", 30))

	// 1. Check & install base tools
	baseTools := []Tool{
		{"unzip", "unzip", "sudo apt-get update && sudo apt-get install -y unzip", true, false},
		{"tmux", "tmux", "sudo apt-get update && sudo apt-get install -y tmux", true, false},
		{"git", "git", "sudo apt-get update && sudo apt-get install -y git", true, false},
		{"node", "node", "curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt-get install -y nodejs", true, false},
	}
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

	os.Setenv("PATH", "/opt/homebrew/bin:/usr/local/bin:/usr/bin:"+os.Getenv("PATH"))

	// 2. Parse agent list
	var selected []string
	if agentList == "all" || agentList == "ALL" {
		selected = []string{"kiro-cli", "claude", "copilot", "gemini", "codex", "opencode"}
	} else {
		// Always include mandatory agents
		has := map[string]bool{}
		for _, a := range strings.Split(agentList, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				has[a] = true
			}
		}
		has["kiro-cli"] = true
		has["opencode"] = true
		for a := range has {
			selected = append(selected, a)
		}
	}

	fmt.Printf("📦 安装 agents: %v\n", selected)
	installSelectedAgents(selected)
	createSelectedWorkers(selected)

	fmt.Println("=" + strings.Repeat("=", 30))
	fmt.Println("🎉 环境初始化完成！")
}

// ensureBuiltinAgents restores tmux sessions and ttyd for agents already in DB.
func ensureBuiltinAgents() {
	rows, err := store.Query("SELECT pane_id, ttyd_port, workspace FROM agent_config WHERE active=1")
	if err != nil {
		return
	}
	defer rows.Close()

	token := getFirstToken()
	for rows.Next() {
		var paneID, workspace string
		var port int
		rows.Scan(&paneID, &port, &workspace)
		if paneID == "" || port == 0 {
			continue
		}

		// Ensure tmux session
		sess := strings.Split(paneID, ":")[0]
		if exec.Command("tmux", "has-session", "-t", sess).Run() != nil {
			if workspace == "" {
				home, _ := os.UserHomeDir()
				workspace = filepath.Join(home, "workers", sess)
			}
			os.MkdirAll(workspace, 0755)
			exec.Command("tmux", "new-session", "-d", "-s", sess, "-n", "main", "-c", workspace).Run()
			log.Printf("[startup] created session %s", sess)
		}

		// Ensure ttyd
		if !isPortListening(port) {
			startInstance(paneID, port, token)
			log.Printf("[startup] started %s on :%d", paneID, port)
		}
	}
}
