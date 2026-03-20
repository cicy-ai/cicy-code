#!/bin/bash
# 环境检测和安装脚本

# 扩展 PATH
export PATH="$HOME/.local/bin:$HOME/.opencode/bin:$PATH"

echo "🎯 Cicy Code 环境检测"
echo "================================"

# 检测函数
check() {
    if command -v "$1" &>/dev/null; then
        echo "  ✅ $1: $(eval "$2" 2>/dev/null || echo 'installed')"
        return 0
    else
        echo "  ❌ $1: not found"
        return 1
    fi
}

echo "🔍 基础环境:"
check node "node -v"
check npm "npm -v"
check tmux "tmux -V"
check git "git --version | cut -d' ' -f3"
check code-server "code-server --version | head -1"

echo ""
echo "🤖 AI 工具:"
check kiro-cli "kiro-cli --version"
check claude "claude -v"
check gemini "gemini --version"
check codex "codex --version"
check opencode "opencode --version"

echo ""
echo "================================"
echo "📦 安装缺失工具? (y/n)"
read -r answer
if [ "$answer" != "y" ]; then
    exit 0
fi

# 检测系统
if [[ "$OSTYPE" == "darwin"* ]]; then
    PKG="brew"
else
    PKG="apt"
fi

echo ""
echo "📦 安装基础环境..."

# Node.js
if ! command -v node &>/dev/null; then
    echo "  安装 node..."
    if [ "$PKG" = "brew" ]; then
        brew install node
    else
        curl -fsSL https://deb.nodesource.com/setup_18.x | sudo -E bash -
        sudo apt-get install -y nodejs
    fi
fi

# tmux
if ! command -v tmux &>/dev/null; then
    echo "  安装 tmux..."
    if [ "$PKG" = "brew" ]; then
        brew install tmux
    else
        sudo apt-get install -y tmux
    fi
fi

# git
if ! command -v git &>/dev/null; then
    echo "  安装 git..."
    if [ "$PKG" = "brew" ]; then
        brew install git
    else
        sudo apt-get install -y git
    fi
fi

# code-server
if ! command -v code-server &>/dev/null; then
    echo "  安装 code-server..."
    curl -fsSL https://code-server.dev/install.sh | sh
fi

echo ""
echo "🤖 安装 AI 工具..."

# kiro-cli
if ! command -v kiro-cli &>/dev/null; then
    echo "  安装 kiro-cli..."
    curl -fsSL https://cli.kiro.dev/install | bash
fi

# claude
if ! command -v claude &>/dev/null; then
    echo "  安装 claude..."
    sudo npm install -g @anthropic-ai/claude-code
fi

# gemini
if ! command -v gemini &>/dev/null; then
    echo "  安装 gemini..."
    sudo npm install -g @google/gemini-cli
fi

# codex
if ! command -v codex &>/dev/null; then
    echo "  安装 codex..."
    sudo npm install -g @openai/codex
fi

# opencode
if ! command -v opencode &>/dev/null; then
    echo "  安装 opencode..."
    curl -fsSL https://opencode.ai/install | bash
fi

echo ""
echo "🎉 安装完成！重新检测:"
echo ""
check kiro-cli "kiro-cli --version"
check claude "claude -v"
check gemini "gemini --version"
check codex "codex --version"
check opencode "opencode --version"
