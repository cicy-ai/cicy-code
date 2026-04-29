# OpenClaw 微信 SOP

这份文档只描述当前代码里的 OpenClaw 微信通道。

## 先分清两个概念

- `openclaw`：走 OpenClaw profile、网关、插件、消息通道
- `cicy-wechat`：另一个独立 CLI agent 类型，不是这份 SOP 的对象

这份 SOP 只适用于 **agent_type=openclaw** 的 pane。

## 当前代码行为

`api/mgr/tmux.go` 里，OpenClaw pane 启动时会：

1. 为当前 pane 建独立 profile：`~/.openclaw-<pane-short-id>`
2. 复制基础 identity / devices / extensions
3. 安装 `@tencent-weixin/openclaw-weixin`
4. 如有需要执行 `openclaw --profile <pane> channels login --channel openclaw-weixin`
5. 等待日志出现 `weixin monitor started`
6. 同时维护 `openclaw-gateway.log`、账号文件、欢迎消息逻辑

所以：**不要假设固定是 `w-10001`**。哪一个 pane 是 OpenClaw，就用哪一个 pane 的短 ID 作为 profile。

## 启动方式

推荐先把服务跑起来，再创建一个 OpenClaw pane。

### 本地模式

```bash
cd ~/projects/cicy-code
python3 dev.py
```

### Docker 模式

```bash
cd ~/projects/cicy-code
python3 dev.py --docker --port 8026 --agents all
```

## 创建 OpenClaw pane

可以直接在 UI 里创建一个 `OpenClaw` agent。

也可以走 API：

```bash
curl -X POST 'http://127.0.0.1:8008/api/tmux/create?token=<token>'       -H 'Content-Type: application/json'       --data '{
    "title": "OpenClaw",
    "agent_type": "openclaw",
    "allow_all_actions": true
  }'
```

假设返回的 pane 是 `w-10008:main.0`，那么它的 OpenClaw profile 就是：

- `w-10008`

状态目录通常是：

- `~/.openclaw-w-10008`

## 首次登录微信

打开这个 OpenClaw pane 后，如果还没有微信账号，启动脚本会自动跑：

```bash
openclaw --profile <pane-short-id> channels login --channel openclaw-weixin
```

然后在终端里进入二维码登录流程。

## 手动重新登录

```bash
openclaw --profile <pane-short-id> channels login --channel openclaw-weixin
```

例如：

```bash
openclaw --profile w-10008 channels login --channel openclaw-weixin
```

如果是在 Docker 容器里执行：

```bash
docker exec -it cicy-code-dev sh -lc 'openclaw --profile w-10008 channels login --channel openclaw-weixin'
```

## 判断是否已就绪

先看账号文件：

```bash
cat ~/.openclaw-<pane-short-id>/openclaw-weixin/accounts.json
```

再看网关日志：

```bash
grep -nE 'Login confirmed|weixin monitor started' ~/.openclaw-<pane-short-id>/openclaw-gateway.log
```

关键字：

- `Login confirmed`
- `weixin monitor started`

只有出现 `weixin monitor started`，才表示通道已经进入可交互状态。

## 主动发消息

当前代码提供了 manager API：

```text
POST /api/openclaw/message/send
```

请求体：

```json
{
  "pane_id": "w-10008:main.0",
  "channel": "openclaw-weixin",
  "target": "<wechat-user-id>",
  "message": "测试消息",
  "timeout_seconds": 20
}
```

示例：

```bash
curl -X POST 'http://127.0.0.1:8008/api/openclaw/message/send?token=<token>'       -H 'Content-Type: application/json'       --data '{
    "pane_id": "w-10008:main.0",
    "channel": "openclaw-weixin",
    "target": "<wechat-user-id>",
    "message": "测试消息",
    "timeout_seconds": 20
  }'
```

这个接口会从 `pane_id` 推导 profile，底层调用：

```bash
openclaw --profile <pane-short-id> message send --channel openclaw-weixin --target <target> --message <message>
```

也可以直接手工调用上面的 CLI。

## target 怎么拿

最简单的方式是先让对方给这个微信账号发一条消息，再从日志里取 `from=`：

```bash
grep -n 'inbound:' ~/.openclaw-<pane-short-id>/openclaw-gateway.log | tail -n 20
```

`from=` 后面的值就是后续主动发消息要用的 `target`。

## 成功判断

API 成功时会返回：

- `success: true`
- 可选 `message_id`

同时日志里通常会看到 outbound 记录。当前实现判断成功的关键字是输出里包含：

- `Sent via`

## 结论

当前仓库里，微信登录与发消息的正确主线是：

1. 运行 `cicy-code`
2. 创建 `openclaw` pane
3. 用 pane 的短 ID 作为 OpenClaw profile
4. 完成 `openclaw-weixin` 登录
5. 等 `weixin monitor started`
6. 通过 `/api/openclaw/message/send` 或 `openclaw ... message send` 发消息
