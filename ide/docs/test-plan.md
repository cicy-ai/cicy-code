# AI 工头 测试方案

## 测试策略

### 1. 手动验收测试（当前阶段）
每个功能完成后，按照测试清单逐项验收，通过后才关闭 issue。

### 2. E2E 自动化测试（未来）
- 工具：Playwright
- 覆盖：核心流程、多 Agent 协作、队列系统
- CI/CD：每次 commit 自动运行

---

## 已完成功能测试清单

### #9 Role 系统
**测试步骤：**
1. 创建新 agent，检查是否有 role 选择（master/worker）
2. 打开 Settings 页面，修改 role，保存
3. 刷新页面，检查 role 是否保持
4. 打开 Agents tab，检查 role 图标（📋 master / 🔧 worker）

**验收标准：**
- [ ] 创建时可选 role
- [ ] Settings 可修改 role
- [ ] 刷新后 role 保持
- [ ] AgentsListView 显示正确图标

---

### #16 消息队列系统
**测试步骤：**
1. 创建一个 Worker agent（w-test-queue）
2. 启动 kiro-cli，配置 model 为 qwen3-coder-next
3. 通过 API 发送 3 条消息到队列：
   ```bash
   TOKEN=$(python3 -c "import json;print(json.load(open('/home/w3c_offical/global.json'))['api_token'])")
   for i in {1..3}; do
     curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
       "http://127.0.0.1:14445/api/workers/queue" \
       -d "{\"pane_id\":\"w-test-queue\",\"message\":\"测试消息 $i\",\"type\":\"message\",\"priority\":1}"
   done
   ```
4. 发送一条消息让 Worker 进入 thinking 状态：`tmux send-keys -t w-test-queue "hi" Enter`
5. 等待 Worker 完成，检查是否自动收到合并的 3 条消息（用 `---` 分隔）
6. 检查 Worker 是否正确处理了所有消息

**验收标准：**
- [ ] 消息成功入队
- [ ] Worker idle 时自动接收
- [ ] 多条消息合并发送（用 `---` 分隔）
- [ ] Worker 正确处理批量消息
- [ ] 队列状态正确更新（pending → sent）

---

### #17 授权级别
**测试步骤：**
1. 创建新 agent
2. 打开 Settings，选择 trust_level 为 "trust-all"，保存
3. 重启 agent：`tmux send-keys -t <pane> C-c && sleep 2 && tmux send-keys -t <pane> "kiro-cli chat" Enter`
4. 等待 5 秒，检查是否自动执行了 `/tools trust-all`
5. 检查 kiro-cli 输出是否显示 "Tools are now trusted"

**验收标准：**
- [ ] Settings 有 trust_level 下拉
- [ ] 可选 trust-all/ask/deny
- [ ] 重启后自动执行对应命令
- [ ] kiro-cli 显示正确状态

---

### #20 Model 选择器
**测试步骤：**
1. 打开任意 agent 的 Settings 页面
2. 检查是否有 "Default Model" 下拉选择器
3. 选择不同的 model（opus-4.6, haiku-4.5 等）
4. 点击 Save 按钮
5. 刷新页面，检查选择是否保持
6. 通过 API 验证 DB 是否更新：
   ```bash
   TOKEN=$(python3 -c "import json;print(json.load(open('/home/w3c_offical/global.json'))['api_token'])")
   curl -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:14445/api/tmux/panes/<pane_id>"
   ```

**验收标准：**
- [ ] Settings 有 default_model 下拉
- [ ] 包含所有 model 选项（opus-4.6/opus-4.5/sonnet-4.5/sonnet-4/haiku-4.5/deepseek-3.2）
- [ ] 保存后刷新页面，选择保持
- [ ] DB 正确更新

---

### #24 Token 统一管理
**测试步骤：**
1. 打开浏览器 DevTools → Application → Local Storage
2. 检查是否只有 `api_token` 一个 token key
3. 检查是否没有 `tmux_app_token` 和 `token` 这两个旧 key
4. 退出登录，重新登录
5. 再次检查 localStorage，确认只有 `api_token`

**验收标准：**
- [ ] localStorage 只有 api_token
- [ ] 没有 tmux_app_token 和 token
- [ ] 登录后 token 正确保存
- [ ] 刷新页面后 token 保持

---

### #13 UI 改造 - 删除 Password/Queue tabs
**测试步骤：**
1. 打开右侧面板
2. 检查 tabs 列表，确认只有：Agents, Code, Board, Preview, Settings
3. 确认没有 Password 和 Queue tabs
4. 点击 topbar 的 Prompt 按钮（编辑图标）
5. 检查是否弹出 modal 编辑 common prompts
6. 输入内容，点击 Save，检查是否保存成功

**验收标准：**
- [ ] 只有 5 个 tabs（Agents/Code/Board/Preview/Settings）
- [ ] 没有 Password 和 Queue tabs
- [ ] Topbar 有 Prompt 按钮
- [ ] 点击弹出 modal
- [ ] 可以编辑和保存 common prompts

---

### UI 改造 - RightSidePanel 布局
**测试步骤：**
1. 打开 IDE，检查右侧面板是否固定显示（不是 Drawer）
2. 鼠标移到 middle 和 right-side 交界处，检查是否有拖拽条（hover 时半透明蓝色）
3. 拖拽调整宽度，检查：
   - 最小宽度 240px
   - 最大宽度 800px
   - middle 最小宽度 400px
4. 刷新页面，检查宽度是否保持
5. 点击 topbar 的右侧面板开关按钮，检查是否可以隐藏/显示
6. 隐藏后刷新，检查状态是否保持

**验收标准：**
- [ ] 右侧面板固定显示（不是 Drawer）
- [ ] 可以拖拽调整宽度（240-800px）
- [ ] middle 最小宽度 400px
- [ ] 宽度保存到 localStorage
- [ ] 刷新后宽度保持
- [ ] Topbar 有开关按钮
- [ ] 可以隐藏/显示右侧面板

---

### CommandPanel 优化
**测试步骤：**
1. 打开 CommandPanel（底部浮动面板）
2. 检查 topbar 左边是否有：
   - TerminalControls（鼠标模式 + 截图按钮）
   - Select 下拉（⚡ 快捷命令）
   - History 按钮
3. 点击 Select，检查选项：
   - 方向键（← ↓ ↑ →）
   - ^C
   - /compact
   - /tools trust-all
   - kiro-cli chat -a
   - /chat resume
   - t/y/n
4. 选择一个命令，检查是否正确发送到 terminal

**验收标准：**
- [ ] Topbar 左边有 TerminalControls + Select + History
- [ ] Select 包含所有快捷命令
- [ ] 选择命令后正确发送
- [ ] 没有多余的按钮（/m /c /ta /ka /cr 已移除）

---

## 批量队列系统测试（重点）

### 测试场景 1：单条消息
**步骤：**
1. Worker idle 状态
2. 发送 1 条消息
3. 发送 "hi" 让 Worker 进入 thinking
4. Worker 完成后检查是否收到消息

**预期：**
- 消息立即发送
- Worker 正确处理

### 测试场景 2：批量消息
**步骤：**
1. Worker idle 状态
2. 连续发送 3 条消息
3. 发送 "hi" 让 Worker 进入 thinking
4. Worker 完成后检查是否收到合并的 3 条消息

**预期：**
- 3 条消息合并发送（用 `---` 分隔）
- Worker 一次性处理所有消息

### 测试场景 3：命令类型
**步骤：**
1. 发送 command 类型消息：`{"type":"command","message":"/context"}`
2. 检查是否直接执行命令（不进对话）

**预期：**
- 命令直接执行
- 不进入对话模式

---

## 回归测试清单

每次发布前，运行以下回归测试：

### 基础功能
- [ ] 登录/登出
- [ ] 创建/删除 agent
- [ ] 切换 agent
- [ ] 发送命令到 terminal

### 核心功能
- [ ] Role 系统正常
- [ ] 消息队列正常
- [ ] 授权级别正常
- [ ] Model 选择器正常

### UI 功能
- [ ] 左侧面板可折叠
- [ ] 右侧面板可拖拽
- [ ] CommandPanel 正常
- [ ] Prompt modal 正常

### 性能
- [ ] 页面加载 < 3s
- [ ] 切换 agent < 1s
- [ ] 拖拽流畅无卡顿

---

## E2E 自动化测试计划（未来）

### 工具选择
- Playwright（推荐）
- 支持多浏览器
- 录制功能
- 截图/视频

### 测试用例
1. **用户登录流程**
2. **创建 Master + 2 Workers**
3. **Master 发送任务到 Worker 队列**
4. **Worker 自动接收并处理任务**
5. **多 Worker 并行处理**
6. **UI 交互测试（拖拽、折叠等）**

### CI/CD 集成
- GitHub Actions
- 每次 push 自动运行测试
- 测试失败阻止 merge

---

## 测试数据

### 测试 Agent 配置
```json
{
  "master": {
    "pane_id": "w-test-master",
    "role": "master",
    "default_model": "claude-opus-4.6",
    "trust_level": "trust-all"
  },
  "worker1": {
    "pane_id": "w-test-worker1",
    "role": "worker",
    "default_model": "claude-haiku-4.5",
    "trust_level": "trust-all"
  },
  "worker2": {
    "pane_id": "w-test-worker2",
    "role": "worker",
    "default_model": "qwen3-coder-next",
    "trust_level": "ask"
  }
}
```

### 测试消息
```json
[
  {"message": "测试消息 1: 回复 OK-1", "type": "message", "priority": 1},
  {"message": "测试消息 2: 回复 OK-2", "type": "message", "priority": 1},
  {"message": "测试消息 3: 回复 OK-3", "type": "message", "priority": 1},
  {"message": "/context", "type": "command", "priority": 2}
]
```

---

## 测试报告模板

### 功能测试报告
```markdown
## 测试功能：#XX 功能名称
**测试时间：** YYYY-MM-DD HH:MM
**测试人员：** 
**测试环境：** Ubuntu 22.04, Chrome 120

### 测试结果
- [ ] 通过
- [ ] 失败

### 测试详情
| 测试项 | 预期结果 | 实际结果 | 状态 |
|--------|----------|----------|------|
| 项目1  | 预期1    | 实际1    | ✅   |
| 项目2  | 预期2    | 实际2    | ❌   |

### 发现的问题
1. 问题描述
2. 复现步骤
3. 截图/日志

### 建议
- 改进建议1
- 改进建议2
```

---

## 下一步

1. **立即执行：** 手动验收已完成功能
2. **本周完成：** 编写核心流程的 E2E 测试
3. **下周完成：** CI/CD 集成
4. **持续改进：** 增加测试覆盖率

---

**文档版本：** v1.0  
**最后更新：** 2026-03-10  
**维护者：** AI 工头团队
