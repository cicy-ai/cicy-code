# Skills

`cicy-skills` 仓库统一管理本机 CLI 工具。技能源文件在 `~/projects/cicy-skills/legacy/skills/`，CLI 入口在 `~/projects/cicy-skills/bin/`，全局软链接在 `~/.local/bin/`。

## 目录结构

```
~/projects/cicy-skills/
├── bin/              ← 所有本机 CLI 入口
├── legacy/skills/    ← skill 源文件，`cicy-skills list` 直接扫描这里
├── providers/        ← 外部 provider（如 google-node）
├── docker/           ← Docker 测试环境
├── dist/             ← 本地构建产物
├── Makefile
└── README.md
```

## 使用

### 安装本机 CLI
```bash
make install-local-cli
```

这会：

- 构建 `dist/` 二进制
- 更新 `~/projects/cicy-skills/bin/`
- 同步全局软链接到 `~/.local/bin/`

### 查看当前技能
```bash
cicy-skills list
```

### 测试 8008 AI Gateway Provider
```bash
test-gateway-provider --provider wucur --model gpt-5.5
test-gateway-provider --provider sub2api --model claude-opus-4-7
test-gateway-provider --all --model gpt-5.5
test-gateway-provider --all --model claude-opus-4-7
```

这个脚本会：

- 读取 `~/cicy-ai/global.json` 里的 provider 配置
- 临时切换 `ai.currentProvider`
- 通过真实 `http://127.0.0.1:8008/api/ai-gateway/{openai|anthropic}/test-agent/...` 发请求
- 默认发送 `hi`
- 测试结束后自动恢复原来的 `currentProvider`

### 安装 Codex allow list skill
```bash
cicy-skills agent list codex
cicy-skills agent install codex all
```

当前 Codex allow list 只包括 `google` 和 `cf-tunnel`。
