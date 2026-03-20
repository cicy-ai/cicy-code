package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	agents := []struct {
		Name string
		Desc string
	}{
		{"claude", "Claude Code - Anthropic 代码助手"},
		{"copilot", "GitHub Copilot CLI - GitHub AI 助手"},
		{"gemini", "Gemini CLI - Google AI 助手"},
		{"codex", "OpenAI Codex - 代码生成助手"},
		{"opencode", "OpenCode - 开源代码助手"},
	}

	fmt.Println("\n🤖 选择要安装的 AI 工具 (kiro-cli 默认安装):")
	fmt.Println("  ✅ Kiro CLI Assistant - 多功能 AI 助手 (必装)")
	for i, agent := range agents {
		fmt.Printf("  %d. %s\n", i+1, agent.Desc)
	}
	fmt.Println("  a. 全选 (安装所有工具)")
	fmt.Print("\n请输入要安装的工具编号 (用空格分隔，如: 1 2 3，或输入 a 全选): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	selected := []string{"kiro-cli"} // kiro-cli 必装

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

func createSelectedWorkers(selected []string) {
	workerConfigs := map[string]struct {
		Name    string
		Tool    string
		Desc    string
	}{
		"kiro-cli": {"kiro", "kiro-cli", "Kiro CLI Assistant"},
		"claude":   {"claude", "claude", "Claude Code Assistant"},
		"copilot":  {"copilot", "copilot", "GitHub Copilot CLI"},
		"gemini":   {"gemini", "gemini", "Gemini AI Assistant"},
		"codex":    {"codex", "codex", "OpenAI Codex Assistant"},
		"opencode": {"opencode", "opencode", "OpenCode Assistant"},
	}

	fmt.Println("\n🚀 创建选中的 Workers...")
	for _, name := range selected {
		if config, exists := workerConfigs[name]; exists {
			createWorker(0, config.Name, config.Tool, config.Desc, "")
		}
	}
}

func createWorker(_ int, name, tool, desc, _ string) {
	token := getFirstToken()
	_, err := doCreatePane(desc, tool, "", tool, "", nil, token)
	if err != nil {
		fmt.Printf("  ❌ %s 创建失败: %v\n", name, err)
		return
	}
	fmt.Printf("  ✅ %s - %s\n", name, desc)
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
