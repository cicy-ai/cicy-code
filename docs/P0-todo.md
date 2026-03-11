# P0 TODO — 一键部署 + Worker-Master 协同

> 两条线并行推进，目标：让用户 5 分钟跑起来 + 两个 agent 能自动协同。

---

## 线 1：一键部署

### 1.1 Docker Compose 完善
- [ ] 确认所有服务可 `docker compose up -d` 一次启动
- [ ] MySQL 自动建表（init.sql 挂载到 /docker-entrypoint-initdb.d/）
- [ ] 默认配置自动写入（token、初始 pane）
- [ ] 环境变量统一到 `.env` 文件
- [ ] 健康检查：每个服务加 healthcheck + depends_on

### 1.2 首次引导
- [ ] 首次访问检测（无 token 时显示引导页）
- [ ] 设置密码 → 生成 token → 自动跳转主界面
- [ ] 绑定第一个 agent 的引导流程

### 1.3 CF Tunnel 配置
- [ ] 一键脚本：输入域名 → 自动配置 CF Tunnel
- [ ] 文档：手动配置步骤（备选）

### 1.4 部署文档
- [ ] GCP 部署指南（含免费 tier）
- [ ] AWS Lightsail 部署指南
- [ ] 搬瓦工 / Vultr 部署指南
- [ ] 最低配置要求说明（2C4G 够用）

### 1.5 README 重写
- [ ] 面向中国用户的 README
- [ ] 突出"翻墙 AI 工作站"卖点
- [ ] 30 秒看懂是什么 + 5 分钟跑起来

---

## 线 2：Worker-Master 协同

### 2.1 Agent 角色系统
- [ ] config JSON 增加 `role` 字段（worker / master / null）
- [ ] `PUT /api/agents/:id/role` — 设置角色
- [ ] 查询配对 API — 同 workspace 下的 worker/master 互查
- [ ] 前端：绑定 agent 时选角色
- [ ] 前端：角色标签（📋 / 🔧）

### 2.2 Watcher Hook
- [ ] fsnotify 检测 pane 状态变化（thinking → idle）
- [ ] idle 时查找同 workspace 的 Master pane
- [ ] 自动 `tm msg` 通知 Master："Worker {name} 已完成，请验收"
- [ ] 可配置：开关 auto-notify、通知模板

### 2.3 通知系统
- [ ] `POST /api/notify` — Agent 发通知给前端
- [ ] `GET /api/notify/stream` — SSE 推送通道
- [ ] Redis PUBLISH kiro_notify 中转
- [ ] 前端 SSE 监听 → 执行 action（open_drawer / toast / refresh）
- [ ] 支持的 action：open_drawer、open_file、toast、refresh

### 2.4 共享文档 .cicy/
- [ ] `GET /api/cicy/files?pane={pane_id}` — 列出文件
- [ ] `GET /api/cicy/file?pane={pane_id}&name={filename}` — 读取内容
- [ ] 前端 Dashboard tab：文件列表 + todo 进度条
- [ ] todo.md 解析：`- [ ]` / `- [x]` → 进度百分比
- [ ] Markdown 渲染（复用 ChatView 的 remarkGfm）
- [ ] 5s 轮询刷新

### 2.5 Master Prompt
- [ ] 内置 system prompt 模板：plan → 写 .cicy/ → 等确认 → 分配
- [ ] 验收 prompt：读 todo → 检查质量 → 更新状态 → 派下一项
- [ ] 前端：Prompt 模板选择器（绑定 master 时可选）

### 2.6 端到端验证
- [ ] 用户说需求 → Master 写 .cicy/todo.md
- [ ] 用户在 code-server 审阅确认
- [ ] Master 分配 Worker 执行
- [ ] Worker 完成 → watcher 自动通知 → Master 验收 → 下一项
- [ ] 全程自动流转，用户只需确认方案

---

## 完成标准

### 线 1 完成标准
用户在一台全新的 Ubuntu VPS 上：
1. `git clone` + `cp .env.example .env` + 编辑 .env
2. `docker compose up -d`
3. 浏览器打开 → 首次引导 → 设置密码 → 绑定 agent
4. 开始使用，全程 < 5 分钟

### 线 2 完成标准
1. 绑定一个 Master（kiro-cli）+ 一个 Worker（kiro-cli）
2. 对 Master 说需求 → Master 写 .cicy/todo.md
3. 用户确认 → Master 分配 Worker
4. Worker 完成 → 自动通知 Master → Master 验收 → 分配下一项
5. 全程无需用户手动干预（确认方案除外）
