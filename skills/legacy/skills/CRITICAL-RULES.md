# CRITICAL RULES - 关键禁忌规则

## 🆕 新 Worker 上岗第一课：通信

所有 worker 必须学会通过 tmux API 与主控 w-10001 通信：

```bash
# 向主控汇报/提问
TOKEN=1116568a729f18c9903038ff71e70aa1685888d9e8f4ca34419b9a5d9cf784ffdf1
curl -s -X POST http://127.0.0.1:14444/api/tmux/send_wait \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"target":"w-10001","text":"你要说的内容","timeout":60}'
```

**规则：**
- 完成任务后必须 call w-10001 汇报
- 遇到问题必须 call w-10001 确认
- 不要自己瞎猜，问就对了
- **不要问"要不要继续"，直接干！完成后汇报就行**
- 发消息格式：[w-你的ID] 内容
- **汇报要精简！** 格式：`[w-ID] ✅ Phase X.X 完成 + 一句话总结`，详细内容写到自己的 REPORT.md
- **Token 管理规范：** 不要硬编码 token，使用 `source ~/.env` 加载，.env 权限 600
- **⚠️ 铁律：发消息必须两步！** 第一步 send text，第二步 send keys Enter。缺 Enter 对方收不到！
- **前端开发必须用 CDP：** 用 Chrome DevTools Protocol 看 console、exec JS、检查网络请求。不要盲改前端代码。
- **⚠️ 改完必须自测！** 不自测就说完成 = 扣 KPI。API 用 curl 测，前端用 CDP 看 console。

## ⚠️ 绝对禁止的操作

### 1. 不能杀 tmux

**禁止命令：**
```bash
tmux kill-session
tmux kill-server
pkill tmux
killall tmux
systemctl restart tmux
```

**原因：**
- 所有 AI workers（包括我）都运行在 tmux pane 里
- 用户通过 ttyd 连接到 tmux pane 与 workers 交流
- 杀掉 tmux = 杀掉所有 workers + 断开所有通信

**例外情况：**
- 用户明确要求重启整个系统
- 出现严重故障必须重启（需用户确认）

---

## 安全操作指南

### 查看 tmux 状态（安全）
```bash
tmux ls                    # 列出所有 session
tmux list-panes -a         # 列出所有 pane
ps aux | grep tmux         # 查看 tmux 进程
```

### 操作单个 pane（相对安全）
```bash
tmux send-keys -t <pane_id> "command" Enter   # 发送命令到指定 pane
tmux capture-pane -t <pane_id> -p             # 捕获 pane 输出
```

### 通过 FastAPI 操作（推荐）
```bash
curl -X POST http://127.0.0.1:14444/api/tmux/send \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"win_id": "w-xxxxx:main.0", "text": "command"}'
```

---

---

## 2. 不要随便杀进程重启

**AI Agent 常见坏习惯：**
- ❌ 改完代码就 `docker-compose down && docker-compose up`
- ❌ 改完代码就 `systemctl restart xxx`
- ❌ 改完代码就 `pkill -9 xxx && xxx &`
- ❌ 改完代码就 `supervisorctl restart xxx`

**正确做法：**
- ✅ 大部分服务都有**热重载（hot reload）**
- ✅ 写好代码后，直接看日志确认生效
- ✅ 只有在必要时才重启服务

### 各项目热重载机制

| 项目 | 热重载方式 | 说明 |
|------|-----------|------|
| **ai-desktop** | Docker volume mount | 改代码自动生效，刷新浏览器即可 |
| **tmux-app** | Docker volume mount | 改代码自动生效，刷新浏览器即可 |
| **ttyd-proxy** | Docker volume mount + nodemon | 改代码自动重启 Node 进程 |
| **fast-api** | supervisorctl (auto-restart) | 改代码后看日志，必要时 `supervisorctl restart fast-api` |
| **ttyd** | C 程序 | 需要重新编译，但不要杀整个 tmux |

### 正确的开发流程

```bash
# 1. 修改代码
vim src/xxx.tsx

# 2. 查看日志确认
docker logs -f ai-desktop-web-1        # Docker 项目
tail -f /path/to/service.log           # 其他服务

# 3. 测试功能
# 刷新浏览器 / 发请求测试

# 4. 只在必要时重启
docker-compose restart web             # 只重启单个服务
supervisorctl restart fast-api         # 只重启单个进程
```

**记住：能不重启就不重启，能重启单个服务就不重启整个系统！**

---

## 记住

**核心原则：**
1. **tmux 是整个 AI workers 系统的生命线，不能轻易动它！**
2. **大部分服务都有热重载，改完代码看日志就行，不要动不动就杀进程！**

创建时间：2026-02-25
