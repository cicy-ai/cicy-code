# OpenClaw 微信插件 SOP

这份文档只讲两件事：

1. 怎么启动 `w-10001` 的微信插件并完成登录
2. 怎么主动给微信发消息

默认约定：

- Docker 外网入口：`http://<host>:8026/`
- 默认微信 worker：`w-10001`
- 默认 OpenClaw profile：`w-10001`
- 默认微信 channel：`openclaw-weixin`

## 1. 启动 Docker

在仓库根目录执行：

```bash
cd /home/w3c_offical/projects/cicy-code-v1
python3 dev.py --docker --port 8026
```

启动成功后会看到类似输出：

```text
[dev] URL: http://<host>:8026/?token=<token>
```

说明：

- Docker 镜像构建阶段已经预装了 `@tencent-weixin/openclaw-weixin`
- `w-10001` 启动时会自动加载微信插件

## 2. 打开 `w-10001`

访问：

```text
http://<host>:8026/?token=<token>
```

进入 `w-10001`。

如果当前没有微信账号，`w-10001` 初始化时会自动执行：

```bash
openclaw channels login --channel openclaw-weixin
```

然后在终端里打印二维码和扫码链接。

## 3. 手动触发微信登录

如果需要手动重新登录，在容器里执行：

```bash
docker exec -it cicy-code-dev sh
su - cicy
cd ~/workers/w-10001
OPENCLAW_STATE_DIR=/home/cicy/.openclaw-w-10001 \
OPENCLAW_CONFIG_PATH=/home/cicy/.openclaw-w-10001/openclaw.json \
openclaw --profile w-10001 channels login --channel openclaw-weixin
```

成功后会看到：

```text
✅ 与微信连接成功！
```

## 4. 判断微信是否登录成功

先看账号文件：

```bash
docker exec cicy-code-dev sh -lc 'cat /home/cicy/.openclaw-w-10001/openclaw-weixin/accounts.json'
```

如果输出类似：

```json
[
  "2652e8abd2ac-im-bot"
]
```

说明登录态已经写入。

再看日志确认 monitor 已启动：

```bash
docker exec cicy-code-dev sh -lc 'grep -nE "Login confirmed|weixin monitor started" /tmp/openclaw/openclaw-$(date +%F).log | tail -n 20'
```

关键日志：

- `Login confirmed`
- `weixin monitor started`

只有看到 `weixin monitor started`，才说明已经真正进入可收发状态。

## 5. 主动给微信发消息

### 方法 A：走 manager API，推荐

接口：

```text
POST /api/openclaw/message/send?token=<token>
```

示例：

```bash
curl -X POST 'http://127.0.0.1:8026/api/openclaw/message/send?token=<token>' \
  -H 'Content-Type: application/json' \
  --data '{
    "pane_id": "w-10001:main.0",
    "channel": "openclaw-weixin",
    "target": "o9cq806mzUS0wi0LLJtulCyvpLfY@im.wechat",
    "message": "测试消息 from API",
    "timeout_seconds": 20
  }'
```

返回成功示例：

```json
{
  "success": true,
  "profile": "w-10001",
  "channel": "openclaw-weixin",
  "target": "o9cq806mzUS0wi0LLJtulCyvpLfY@im.wechat",
  "message": "测试消息 from API",
  "message_id": "openclaw-weixin:1775866912524-a47d152f"
}
```

说明：

- `pane_id` 用来推导 profile，通常填 `w-10001:main.0`
- `target` 是微信用户 ID，不是昵称
- 这个接口底层会调用 OpenClaw 官方 `message send`

### 方法 B：直接走 OpenClaw CLI

```bash
docker exec cicy-code-dev sh -lc '
openclaw --profile w-10001 message send \
  --channel openclaw-weixin \
  --target o9cq806mzUS0wi0LLJtulCyvpLfY@im.wechat \
  --message "测试消息 from CLI"
'
```

成功示例：

```text
✅ Sent via openclaw-weixin. Message ID: openclaw-weixin:...
```

## 6. 怎么拿到微信 target

最简单的方法是先让对方发一条消息，然后从日志里取 `from=`：

```bash
docker exec cicy-code-dev sh -lc 'grep -n "inbound:" /tmp/openclaw/openclaw-$(date +%F).log | tail -n 20'
```

示例：

```text
inbound: from=o9cq806mzUS0wi0LLJtulCyvpLfY@im.wechat ...
```

这里的 `from=` 就是后续主动发消息要用的 `target`。

## 7. 怎么确认消息已经发出

看 API/CLI 返回不够，还要看网关日志：

```bash
docker exec cicy-code-dev sh -lc 'grep -nE "outbound:|text sent OK" /tmp/openclaw/openclaw-$(date +%F).log | tail -n 20'
```

成功关键字：

- `outbound: to=...`
- `outbound: text sent OK to=...`

## 8. 常见问题

### 8.1 扫码成功后很久才收到第一条微信消息

常见原因不是微信网络慢，而是：

1. 扫码成功后 OpenClaw 把 channel 配置写回 `openclaw.json`
2. gateway 因为 `channels` 配置变化发生一次重启
3. 重启完成后 `weixin monitor started`
4. 这时才真正开始消费消息

排查命令：

```bash
docker exec cicy-code-dev sh -lc 'grep -nE "Login confirmed|config change requires gateway restart|weixin monitor started|inbound:" /tmp/openclaw/openclaw-$(date +%F).log | tail -n 50'
```

### 8.2 重启后二维码又出来了

说明当前容器里的微信账号状态没被复用，或者账号文件丢了。

先查：

```bash
docker exec cicy-code-dev sh -lc 'cat /home/cicy/.openclaw-w-10001/openclaw-weixin/accounts.json'
```

如果不存在或为空，就会重新进入扫码态。

### 8.3 `message send` 卡住很久

CLI 有时会慢，但只要输出里包含：

```text
✅ Sent via openclaw-weixin
```

就可以视为已经成功。

如果用 manager API，接口已经把这类情况统一处理掉了。

## 9. 推荐操作顺序

1. 启动 Docker
2. 打开 `w-10001`
3. 扫码登录
4. 等日志出现 `weixin monitor started`
5. 先让微信用户发一条消息，拿到 `target`
6. 通过 `/api/openclaw/message/send` 主动发一条测试消息
7. 看微信是否收到
8. 看日志里是否有 `text sent OK`
