# AGENTS.md

AI Agent 技能手册。技能源文件在 `~/projects/cicy-skills/legacy/skills/`，CLI 入口在 `~/projects/cicy-skills/bin/`，全局软链接在 `~/.local/bin/`。

`cicy-skills list` 是当前技能清单的准源。

## 常用命令

直接运行即可，无需路径：

| 命令 | 说明 |
|------|------|
| `tm ls` | 列出所有 tmux panes |
| `tm status [pane]` | 查看 pane 状态 |
| `tm capture <pane>` | 捕获 pane 内容 |
| `tm msg <pane> <text>` | 向 pane 发送消息 |
| `tm send-keys <pane> <keys>` | 发送按键 |
| `xui` | 面板管理工具 |
| `todo` | TODO 管理 |
| `cping <host>` | Cloudflare 延迟测试 |
| `cf-tunnel` | Cloudflare Tunnel 管理 |
| `mysql-exec <sql>` | 执行 MySQL 查询 |
| `tg <chat_id> <msg>` | 发送 Telegram 消息 |
| `gemini-ask <prompt>` | Gemini 问答 |
| `gemini-vision <prompt>` | Gemini 视觉问答 |
| `gpt <prompt>` | GPT 问答 |
| `eng <prompt>` | English 问答 |
| `gpt-chat` | 交互式 GPT 对话 |
| `google gmail list 5` | 查看最近邮件 |

## 完整 Skills 目录

```
~/projects/cicy-skills/
├── bin/              CLI 入口 symlink
├── legacy/skills/    技能源文件
├── providers/        外部 provider
└── Makefile
```

### Layer 1: 基础设施 (infra/)

| 命令 | 说明 |
|------|------|
| `cf-tunnel` | Cloudflare Tunnel 管理 |
| `cf-tunnel-py` | Cloudflare Tunnel (Python) |
| `cping <host>` | Cloudflare 延迟测试 |
| `ft` | 文件传输工具 |
| `mysql-exec <sql>` | 执行 MySQL 查询 |
| `vnc-proxy` | VNC 代理管理 |
| `vnc-proxy-check` | VNC 代理检查 |
| `vnc-server` | VNC 服务说明 |

### Layer 2: 开发工具 (dev/)

| 命令 | 说明 |
|------|------|
| `tm` | Tmux 会话管理（核心） |
| `xui` | 面板 UI 管理 |
| `todo` | TODO 管理 |
| `vphone-ctl` | 虚拟手机控制 |
| `ax-click` | AX 点击工具 |
| `ax-tree` | AX 树工具 |

### Layer 3: AI 能力 (ai/)

| 命令 | 说明 |
|------|------|
| `gemini-ask <prompt>` | Gemini 问答 |
| `gemini-vision <prompt>` | Gemini 视觉 |
| `gpt <prompt>` | GPT-4 问答 |
| `gpt-chat` | 交互式 GPT |
| `eng <prompt>` | 英文问答 |
| `stt` | 语音转文字 |
| `tts` | 文字转语音 |

### Layer 4: CiCy 平台 (cicy/)

| 命令 | 说明 |
|------|------|
| `agent-page-ping` | Agent 页面状态 |
| `ipc-ping` | IPC 通信测试 |
| `help` | CiCy 内部命令提示 |
| `webpage` | 网页客户端控制 |
| `webpage-ping` | 网页客户端快速探活 |
| `cf-workers` | Cloudflare Workers |
| `cf-pages` | Cloudflare Pages |

### Layer 5: 外部服务 (services/)

| 命令 | 说明 |
|------|------|
| `tg <chat_id> <msg>` | 发送 Telegram |
| `tg-bot` | Telegram Bot 管理 |
| `tg-bot-check` | Telegram Bot 检查 |
| `tg-sender` | Telegram 发送说明 |
| `google` | Google 服务 |

## 常用操作示例

```bash
# Tmux 操作
tm ls                           # 列出所有 panes
tm status w-10001               # 查看 w-10001 状态
tm capture w-10001              # 捕获内容
tm msg w-10001 "hello"          # 发消息
tm send-keys w-10001 Enter      # 发送按键

# 消息通知
tg 123456 "任务完成"            # 发 Telegram

# Google
google help
google gmail list 5
```

## 当前路径

- 仓库: `/home/w3c_offical/projects/cicy-skills/`
- Workers: `/home/w3c_offical/Private/workers/`
- Codex skills: `/home/w3c_offical/.codex/skills/`
- 主控 pane: `w-10001`
