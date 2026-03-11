# Kiro CLI HTTP 流量分析报告

> Worker: `w-20147` | 时间: 2026-03-10 19:43:05 ~ 19:51:46 UTC
> 数据源: mitmproxy → Redis → MySQL (`http_log` 表)
> 总计: 25 条请求 | 5 轮 LLM 对话 | 0.2941 credits

---

## 一、流程图

```
┌──────────────────────────────────────────────────────────────────┐
│                    kiro-cli 启动 / 首次对话                       │
└──────────────────────────┬───────────────────────────────────────┘
                           │
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
   ┌─────────────┐  ┌───────────┐  ┌──────────────┐
   │ Telemetry×2 │  │ Cognito   │  │ ListModels   │
   │ 客户端启动   │  │ GetId     │  │ + UsageLimits│
   │ SDK 遥测     │  │ 认证/限流  │  │ 模型+配额     │
   └──────┬──────┘  └─────┬─────┘  └──────┬───────┘
          └────────────────┼───────────────┘
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│                    对话循环 (每轮重复 ×4 请求)                     │
│                                                                  │
│   ┌──────────────────────────────────────────────┐               │
│   │ ① GenerateAssistantResponse ⭐                │               │
│   │    发送: history + steering + 用户消息          │               │
│   │    返回: text 或 tool_use (streaming)          │               │
│   └──────────────────┬───────────────────────────┘               │
│                      ▼                                           │
│   ┌──────────────────────────────────────────────┐               │
│   │ ② SendTelemetryEvent                          │               │
│   │    上报: responseLength, firstChunk, chunks    │               │
│   └──────────────────┬───────────────────────────┘               │
│                      ▼                                           │
│   ┌──────────────────────────────────────────────┐               │
│   │ ③④ client-telemetry/metrics × 2               │               │
│   │    AWS SDK 遥测                                │               │
│   └──────────────────────────────────────────────┘               │
└──────────────────────────────────────────────────────────────────┘
```

---

## 二、QA 记录与 Credit 消耗

| 轮次 | Credit | 类型 | 首token | Q (currentMessage) | A |
|------|--------|------|---------|-------------------|---|
| 1 | 0.0545 | text | 3983ms | `hi` | 你好！我是 Kiro，有什么可以帮你的吗？ |
| 2 | 0.0623 | tool_use | 4047ms | `read ~/skills` | → 调用 `fs_read` (读取目录) |
| 3 | 0.0386 | tool_use | 3132ms | *(tool result 自动续)* | → 调用 `execute_bash` |
| 4 | 0.0417 | tool_use | 4049ms | *(tool result 自动续)* | → 调用 `fs_read` |
| 5 | 0.0971 | text | 7705ms | *(tool result 自动续)* | 列出 ~/skills 48 个文件的详细说明 |
| **合计** | **0.2941** | | | | kiro-cli 显示 0.23 (prompt caching 折扣后) |

**说明：**
- `currentMessage`: 请求体中 `conversationState.currentMessage` 字段，是本轮实际发送的新消息
- `history`: 之前所有轮次的完整记录，每轮累积重发
- `text`: 返回纯文本回复
- `tool_use`: 返回工具调用指令 (fs_read / execute_bash 等)，不含文本
- tool_use 轮次也消耗 credit，因为需要处理完整 history
- `read ~/skills` 触发 4 轮 LLM 调用 (轮次 2-5)，轮次 3-5 是 tool result 自动续传

---

## 三、Credit 消耗分析

### 3.1 费率

按此会话速率：每轮平均 0.059 credits，10000 credits/月 ≈ **170,000 轮对话**。

但实际一个用户操作可能触发多轮 (tool use 链)：
- 简单问答: 1 轮 ≈ 0.05 credits
- 带工具调用: 3-4 轮 ≈ 0.20 credits
- 复杂任务: 可能 10+ 轮

### 3.2 上行流量与 Credit 关系

| 轮次 | 上行 KB | Credit | 响应字符 |
|------|---------|--------|----------|
| 1 | 48.9 | 0.0545 | 53 |
| 2 | 49.4 | 0.0623 | 99 (tool) |
| 3 | 50.0 | 0.0386 | 68 (tool) |
| 4 | 50.7 | 0.0417 | 119 (tool) |
| 5 | 55.4 | 0.0971 | 765 |

- 上行逐轮增长 (48.9→55.4 KB)：history 累积，每轮重发完整对话
- 其中 ~20KB 是固定的 steering context (skill-list.md 等)
- Credit 主要由输出长度决定，不是输入

### 3.3 mitmproxy 记录 vs kiro-cli 显示

| 来源 | Credits |
|------|---------|
| mitmproxy (event-stream 里的 usage 字段) | 0.2941 |
| kiro-cli 屏幕显示 | 0.23 |

差异原因：kiro-cli 显示的是 prompt caching 折扣后的净消耗。

---

## 四、可用模型与费率

从 `ListAvailableModels` 响应提取：

| modelId | rateMultiplier | maxInputTokens | promptCaching | 说明 |
|---------|---------------|----------------|---------------|------|
| `auto` | 1.0x | 200K | ✅ | 默认，按任务自动选模型 |
| `claude-opus-4.6` | **2.2x** | 200K | ✅ | 最强最贵 |
| `claude-sonnet-4.6` | 1.3x | 200K | ✅ | 实验预览 |
| `claude-opus-4.5` | **2.2x** | 200K | ✅ | 稳定版 |
| `claude-sonnet-4.5` | 1.3x | 200K | ✅ | 稳定版 |
| `claude-sonnet-4` | 1.3x | 200K | ✅ | 混合推理+编码 |
| `claude-haiku-4.5` | 0.4x | 200K | ✅ | 轻量快速 |
| `deepseek-3.2` | 0.25x | 164K | ❌ | 实验预览 |
| `minimax-m2.1` | 0.15x | 196K | ❌ | 实验预览 |
| `qwen3-coder-next` | **0.05x** | 256K | ❌ | 最便宜 |

费率差 44 倍：opus (2.2x) vs qwen3 (0.05x)。

---

## 五、订阅与配额

从 `GetUsageLimits` 响应提取：

| 项目 | 值 |
|------|-----|
| 订阅类型 | `KIRO POWER` (Q_DEVELOPER_STANDALONE_POWER) |
| 月度配额 | 10,000 Credits |
| 当前已用 | 1,166.25 Credits (11.7%) |
| 免费试用 | 500 Credits (已用完，2026-04-05 到期) |
| 超额单价 | $0.04/Credit |
| 超额状态 | **DISABLED** (不会产生额外费用) |
| 配额重置 | 2026-03-29 |

---

## 六、性能指标

| 指标 | 最小 | 最大 | 平均 |
|------|------|------|------|
| 首 token 延迟 | 3132ms | 7705ms | 4593ms |
| chunk 间隔 | 4.8ms | 25.8ms | 12.9ms |
| 响应长度 | 53 字符 | 765 字符 | 221 字符 |

- 短回复 (~50字符): 首 token ~4s
- 长回复 (~765字符): 首 token ~8s
- Streaming 非常流畅，chunk 间隔 5-26ms

---

## 七、流量统计

### 按 API 分类

| API | 次数 | 上行 KB | 下行 KB | 说明 |
|-----|------|---------|---------|------|
| GenerateAssistantResponse | 5 | 254.4 | 22.3 | 核心 LLM 调用 |
| client-telemetry/metrics | 12 | 17.9 | 0 | AWS SDK 遥测 |
| SendTelemetryEvent | 5 | 4.3 | 0 | 对话性能遥测 |
| ListAvailableModels | 1 | 0 | 4.0 | 模型列表 |
| GetUsageLimits | 1 | 0 | 1.1 | 配额查询 |
| Cognito GetId | 1 | 0.1 | 0.1 | 认证 (被限流) |
| **总计** | **25** | **276.7** | **27.5** | |

### 流量占比

- LLM 调用占上行 **91.9%**，遥测占 8.1%
- 25 条请求中 17 条 (68%) 是遥测，但流量占比仅 8%
- 每轮固定开销：1 LLM + 3 遥测 = 4 条请求

---

## 八、关键发现

1. **一个用户操作 ≠ 一轮 LLM 调用**：`read ~/skills` 触发 4 轮 (tool_use→result→tool_use→result→回复)
2. **Steering context 每轮重发**：~20KB 固定开销，prompt caching 可缓解
3. **Credit 主要由输出决定**：输入 48-55KB 差异不大，但输出 53 vs 765 字符 credit 差 2 倍
4. **mitmproxy 记录的 credit > kiro-cli 显示**：prompt caching 折扣约 22%
5. **Cognito 限流无影响**：kiro-cli 有凭证缓存机制
6. **`currentMessage` 是真正的用户输入**：`history` 只是之前的对话记录，每轮累积重发
