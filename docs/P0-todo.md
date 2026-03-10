# P0 实施 TODO

## Phase 1 — 后端 API + 角色

- [ ] `GET /api/cicy/files?pane={pane_id}` — 列出 .cicy/ 文件
- [ ] `GET /api/cicy/file?pane={pane_id}&name={filename}` — 读取文件内容
- [ ] config JSON 支持 `role` 字段（worker/master）
- [ ] 查询配对 API — 同 workspace 下的 worker/master 互查

## Phase 2 — Watcher Hook + 通知机制

- [ ] Worker idle 时查找同 workspace 的 Master
- [ ] 自动 `tm msg` 通知 Master 验收
- [ ] `POST /api/notify` — Agent 发通知给前端
- [ ] `GET /api/notify/stream` — SSE 推送通道
- [ ] Redis PUBLISH kiro_notify 中转

## Phase 3 — 前端布局

- [ ] 双终端并排（选中一个角色时自动显示配对终端）
- [ ] Dashboard tab（新增）— .cicy/ 文件列表 + todo 进度条 + markdown 渲染
- [ ] 5s 轮询文件内容
- [ ] SSE 监听 /api/notify/stream，执行 action（open_drawer/toast/refresh）
- [ ] Agent 角色标签（📋/🔧）
- [ ] 绑定 agent 时选角色 UI
- [ ] Prompt 移到 topbar
- [ ] 删除 Password tab

## Phase 4 — Master Prompt

- [ ] System prompt 模板（plan → 写 .cicy/ → 等确认 → 分配执行）
- [ ] 验收 prompt（读 todo → 检查 → 派下一项）

## Phase 5 — 端到端测试

- [ ] 用户说需求 → Master 写 .cicy/todo.md
- [ ] 用户在 code-server 审阅确认
- [ ] Master 分配 Worker 执行
- [ ] Worker 完成 → 自动通知 → Master 验收 → 下一项
