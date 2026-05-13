# Skills 修复清单

## ✅ 已完成

- [x] 1. **fast-api** — 重写为 curl wrapper
- [x] 2. **tm** — 新写，基于 fast-api
- [x] 3. **gpt** — Go API /api/ai/chat + CLI
- [x] 4. **gpt-chat** — 多轮对话 + 历史
- [x] 5. **eng** — Go API /api/ai/correct + CLI
- [x] 7. **gmail** — 从 GitHub clone，修复，重新授权
- [x] 8. **google** — 同上
- [x] 9. **tg** — Go API /api/tg/send + CLI
- [x] 10. **gemini-ask** — 端口 → 8008
- [x] 11. **gemini-vision** — 端口 → 8008
- [x] 12. **ipc-ping** — 端口 → 8008
- [x] 13. **check-all** — 端口 → 8008
- [x] 14. **mysql-exec** — 适配 docker-compose

## 🗑️ 已删除

- cdp — 不再需要
- cicy-rpc — 等 Mac 环境就绪后再装

## 🔵 待定

- [ ] **gemini-vision-agent** — 通过 Agent Page 调用，待 Mac 环境就绪后对接
- [ ] **cicy-rpc** — 等 Mac 环境就绪后再装
- [ ] **grant-trial-credit** — 给 OpenClaw 补完整收尾：客服消息自动提取 6 位验证码、调用发放命令、把“已发放/已存在无重复发放/验证码不存在”结果回传给客服
