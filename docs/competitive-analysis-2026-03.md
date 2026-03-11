# cicy-code 竞品分析与战略方向

> 日期: 2026-03-11 | 版本: 1.0

---

## 一、2026 竞品格局

| 竞品 | 核心能力 | 多 Agent | 定价 |
|------|---------|---------|------|
| Cursor | 8 个并行 background agent，VS Code fork，深度 codebase 理解 | ✅ 内置并行 | $20-200/月 |
| Windsurf | 被 Cognition (Devin) 收购，Cascade 自主工作流 | ✅ Devin 集成 | 免费起 |
| Claude Code | Agent Teams + Swarm Mode，peer-to-peer 通信，mailbox 系统 | ✅ 原生多 agent | $20-200/月 |
| Google Antigravity | Agent Manager 面板，Gemini 3 驱动，多 agent 并行 + 内置浏览器 | ✅ 原生并行 | 免费预览 |
| Kiro IDE (AWS) | Spec-driven，多 agent 架构（安全/DevOps agent），Agent Hooks | ✅ 专业化 agent | 免费/$19/月 |
| Devin 2.0 | 全自主，Interactive Planning，并行任务 | ✅ 自主并行 | $20-500/月 |
| VS Code 1.109 | 原生支持 Claude + Codex + Copilot 多 agent 编排 | ✅ 2026.02 新增 | 免费 |

---

## 二、ROADMAP 五大卖点 vs 现实

### 1. "不挑 AI，博众家所长" — 差异化正在消失

- VS Code 1.109 已原生支持 Claude + Codex + Copilot 多 agent 编排
- Cursor 支持 Claude Opus 4.6、GPT-5.3-Codex、Gemini 3.1 Pro
- Antigravity 支持 Gemini 3 Pro + Claude Sonnet 4.5 + GPT-OSS
- 我们通过 mitmproxy 拦截 CLI 流量的方式，本质上是 hack，不是原生集成

### 2. "全透明" — 仍有优势但不够大

- Claude Code Agent Teams 有 mailbox 系统，可看到 agent 间通信
- Antigravity 有 Agent Manager 面板，专门做 agent 监控
- 我们的终端实时可见确实透明，但 UX 粗糙（iframe ttyd）

### 3. "Plan 落文件" — Kiro IDE 已经做了

- Kiro IDE 的 spec-driven 就是 plan 落文件（requirements → design → tasks）
- 我们的 `.cicy/todo.md` 方案还没实现（P0 TODO 全是 `[ ]`）

### 4. "人在回路" — 大家都在做

- Cursor plan mode（Shift+Tab）、Devin 2.0 Interactive Planning
- 这不是差异化，是标配

### 5. "成本透明" — 唯一真正的差异点

- 没有任何竞品提供 per-agent 实时 token 消耗监控
- 但这是 nice-to-have，不是用户选择 IDE 的核心理由

---

## 三、真正的优势

### 1. 跨 AI 厂商的统一管控层

不是"支持多模型"（大家都支持），而是"管理多个独立 CLI agent"。Cursor 的 8 个并行 agent 都是 Cursor 自己的 agent，我们可以同时跑 kiro-cli + claude-code + codex-cli，每个是独立进程。这是"AI 的工头"定位。

### 2. 基于 tmux 的极致灵活性

- 每个 agent 是一个 tmux pane，完全隔离
- 可以 SSH 到任何机器（Mac/Win/Linux）启动 agent
- 分布式多设备 agent 网络是独一无二的
- 竞品都是单机单进程

### 3. mitmproxy 流量层

- 不只是成本监控，是完整的 AI 行为审计
- 企业合规场景：知道 AI 调了什么 API、读了什么文件、花了多少钱
- B2B 卖点

---

## 四、致命不足

### 1. 核心功能未完成

- P0 Worker-Master 协同：全是 `[ ]`，一个都没做
- 任务分发器：没做
- `.cicy/` 共享文档：没做
- watcher hook 自动通知：没做
- 目前只是一个"多终端管理器 + 聊天可视化"

### 2. UX 差距巨大

- iframe 嵌入 ttyd，字体/主题不统一，选择/复制有 bug
- 没有原生代码编辑体验（依赖 code-server iframe）
- 竞品是 VS Code fork，原生编辑体验
- UI 是"拼凑"的，不是"设计"的

### 3. 技术债

- mitmproxy 拦截方式脆弱，依赖 HTTPS 中间人
- 没有 agent 协议标准，全靠 `tm msg` 发文本
- 没有自动化测试，没有 CI/CD

### 4. 用户体验断层

- 用户要自己部署 Docker Compose + MySQL + Redis + mitmproxy + code-server
- 竞品是下载即用（Cursor/Windsurf）或 SaaS（Devin）

---

## 五、被忽略的核心卖点：云端工作站解决网络痛点

### 中国开发者的真实痛苦

- 从中国直接 SSH / code-server 访问 GCP → 延迟 300ms+，中文输入法卡死，打字半天出不来
- VPN 不稳定，SSH 断连，terminal 里丢字符
- Cursor / Windsurf 是本地 IDE，调海外 API 一样卡
- 所有竞品的架构都是：`用户电脑 ←→ 海外 AI API`，中间隔着防火墙

### cicy-code 的架构优势

```
竞品架构（全部卡）:
  中国用户电脑 ←防火墙/高延迟→ 海外 AI API
  中国用户电脑 ←防火墙/高延迟→ 海外 code-server

cicy-code 架构（不卡）:
  中国用户浏览器 ←CF Tunnel/FRP→ 海外 VPS 工作站
                                    ├── Web UI（轻量交互）
                                    ├── AI Agent × N（本地调 API，零延迟）
                                    ├── code-server（本地访问，零延迟）
                                    ├── mitmproxy（流量监控）
                                    └── tmux（进程管理）
```

关键洞察：
- 用户只传 **文字指令**（几 KB），AI 干的是 **重活**（几百 KB 的 API 调用）
- 重活全在海外 VPS 本地完成，用户感知不到延迟
- CF Tunnel / FRP 只传 UI 交互，带宽需求极低，体验流畅

### 这就是为什么我们做了这些功能

| 功能 | 解决的痛点 |
|------|-----------|
| 命令面板（前端发 prompt） | 不用在卡顿的终端里打中文 |
| 语音输入 | 连打字都省了 |
| ChatView 可视化 | 不用盯着卡顿的终端看输出 |
| code-server dialog | 在同一个页面里看代码，不用另开卡死的 tab |
| Workers 卡片网格 | 一屏看所有 agent 状态，不用切来切去 |

### 竞品无法复制

- Cursor / Windsurf / Antigravity 是本地 IDE，不可能帮你跑在 GCP 上
- Claude Code / Devin 是 SaaS，但不给你 code-server、不给你终端、不给你多 agent 管理
- VS Code Remote SSH 从中国连 GCP 一样卡死

**cicy-code 是唯一一个"AI 工作站全部跑在海外，用户只需要浏览器"的方案。**

---

## 六、战略方向（修正版）

### 核心定位

> **云端 AI 协同工作站**
>
> 一键部署在任何海外机房，中国用户通过浏览器流畅使用所有 AI Agent。
> 不做 AI，做 AI 的工头。不做 IDE，做 AI 团队的云端指挥部。

### 目标用户

- 中国开发者，想用 Claude/GPT/Gemini/Codex 但被墙挡着
- 有 VPN 但体验极差（延迟、断连、中文输入法）
- 愿意花钱租海外 VPS，但不知道怎么搭环境
- 独立开发者 / 小团队，想用 AI 提高产出

### 猛攻方向

#### 方向 1：一键部署 + 开箱即用（最高优先级，重新提升）

之前降级了 P6/P7，现在重新提升——因为核心卖点是"云端工作站"，那部署体验就是生命线。

- `docker compose up` 一条命令跑起全套（API + 前端 + code-server + mitmproxy + Redis + MySQL）
- 自动初始化：建表、默认配置、生成 token
- 用户只需要：租 VPS → 装 Docker → 跑命令 → 配 CF Tunnel → 浏览器打开
- 5 分钟从零到可用

#### 方向 2：多 Agent 编排引擎

- 完成 P0 Worker-Master 协同
- watcher hook：agent idle → 自动触发下一步
- 1 Master + N Worker 拓扑
- 核心差异：不同 AI 厂商的 agent 协同（kiro + claude-code + codex）

#### 方向 3：成本控制 + 审计

- 实时 per-agent token 消耗面板
- 阈值报警 + 自动拦截
- 审计日志

#### 方向 4：分布式节点网络

- CF Tunnel / FRP 连接多个节点（GCP + Mac + Windows + 其他 VPS）
- 一个控制台管理所有节点上的 agent
- 按设备能力自动分配任务

### 优先级重排

| 优先级 | 方向 | 原因 |
|--------|------|------|
| P0 | 一键部署 + 开箱即用 | 核心卖点是云端工作站，部署体验是生命线 |
| P0 | Worker-Master 协同 | 核心差异化功能 |
| P1 | 任务分发器 UI | 多 agent 管理的入口 |
| P2 | Token 消耗面板 + 审计 | 变现基础 |
| P3 | TG 远程控制 | 手机指挥 AI 团队 |
| P4 | 分布式节点网络 | 长期护城河 |
| 继续 | UI/UX 打磨（ChatView、Workers） | 用户体验是留存关键，不能停 |

### UI 原则：不碰撞，但不能 low

不跟 Cursor/Windsurf 比编辑器体验，但 UI 要有质感，让用户觉得这是一个专业产品。

- 干净、一致、专业 — 不花哨，不运维风
- 全局 CSS 变量统一主题（已完成）
- ChatView / Workers 卡片保持设计一致性（已完成）
- 交互细节持续打磨：动画、间距、字号、颜色层次
- 参考标准：Vercel Dashboard / Linear — 简洁但有质感

### 不要做的事

- ❌ 不要做本地 IDE（我们的优势就是云端）
- ❌ 不要做 xterm.js 直连（iframe ttyd 够用，投入产出比低）
- ❌ 不要做去依赖/单二进制（Docker Compose 就是最好的部署方式）
- ❌ 不要追求花哨 UI（不需要动效炫技，追求干净专业）

---

## 七、结论

cicy-code 的真正竞争力不是"更好的 AI"或"更好的 IDE"，而是：

**让中国开发者（以及所有网络受限的用户）通过浏览器流畅使用全球所有 AI Agent，同时管理多个 AI 并行协作。**

竞品解决的是"AI 怎么写代码"，我们解决的是"怎么让 AI 帮你干活不卡"。

一句话 pitch：

> **"你的 AI 团队在云端，你只需要一个浏览器。"**
