---
title: 自托管隧道 · cicy-hub
description: 一条命令在自己的服务器上跑一个中立的零信任反向隧道(cicy-hub),把任意设备的 cicy-code 暴露到 <slug>.gw.<你的域名>,透明转发、不碰数据。
---
# 自托管隧道 · cicy-hub

把你的每台设备串起来:节点(cicy-code)**主动拨出**到 **cicy-hub**,任何人都能在
`https://<slug>.gw.<你的域名>/` 访问那台设备,**体验和它的 `localhost:8008` 完全一样**。
节点自身**不开任何入站端口**。

::: info 这不是"AI 网关"
**cicy-hub** 是让设备可达的**反向隧道中继**。它和 [本地 AI 网关](/advanced/gateway)
(模型路由、按 agent 记账)是两个完全不同的东西——改名就是为了不再和"网关"撞词。
:::

## 核心:透明转发,不做鉴权

cicy-hub 是一根**中立的管子**:按 slug 把请求原样转发给节点,**不鉴权、不改 header、不碰 CORS、不看内容**。
鉴权是**节点自己的事**——用它自己的 `api_token`,和你在本地访问 `localhost:8008?token=` 一模一样。

- 访问 `<slug>.gw.<域名>` = 访问那台机器的本地;
- 带对 `?token=<该节点的 api_token>` 才进得去(乱填直接 401);
- 隧道唯一验的是**节点拨入时的签名 token**(用来知道哪个 slug 对应哪条活隧道),客户端流量从不被检查。

数据主权:cicy-hub 不落库、不解密业务、看不到你的 token。你甚至可以把它跑在**自己的云**上,和我们彻底无关。

## 你需要准备的(仅这三样)

1. 一个你拥有的**域名**;
2. 一条**泛解析** DNS:`*.gw.<你的域名>` → 服务器公网 IP;
3. 一台**服务器**,`443` 端口空着。

其余全自动。

## 一条命令起(自签证书,即起即用)

```sh
git clone https://github.com/cicy-ai/cicy-hub && cd cicy-hub
DOMAIN=example.com docker compose up -d --build
```

首次启动容器会**自动配好一切**,零 cicy-cloud 依赖:

- 生成**节点准入签名密钥对**(决定谁能拨入);
- 自签一张**泛域名 TLS 证书** `*.gw.example.com`;
- 自签一张**授权 license**(默认放开 50 节点上限);
- 起中继,监听 `:443`。

密钥 / 证书 / license 都持久化在 `gwdata` 卷里,重启不变。

## 接入一台设备

```sh
docker compose exec hub enroll my-mac
# 输出(Option A 推荐):一段可直接落盘的 tunnel.json
#   {"url":"wss://my-mac.gw.example.com/_tunnel/connect","token":"<token>","insecure":true}
```

把那段 JSON 存成节点的 `~/cicy-ai/db/tunnel.json`,然后**直接 `cicy-code`(不用任何参数)**即可自动拨号(见下方「节点侧配置」)。
节点拨通后,浏览器打开:

```
https://my-mac.gw.example.com/?token=<该 Mac 的 api_token>
```

`?token=` 会写进 localStorage,之后自动带上——和 `localhost:8008` 无差别。

## 节点侧配置(cicy-code 怎么接上)

隧道只需要两样数据:**url + token**(外加自签证书时的 `insecure`)。token 放命令行不安全
(`ps` 能看到),所以配置尽量落在**文件或环境变量**里 —— 常规情况下**节点一个参数都不用传**。

### 方式一:`tunnel.json`(推荐,0 参数)

把 `enroll` 输出的那段 JSON 存成 `~/cicy-ai/db/tunnel.json`:

```json
{ "url": "wss://my-mac.gw.example.com/_tunnel/connect", "token": "<token>", "insecure": true }
```

然后直接:

```sh
cicy-code        # 检测到 tunnel.json → 自动拨号,无需任何 --tunnel 参数
```

### 方式二:环境变量(容器 / CI)

```sh
export CICY_TUNNEL_URL=wss://my-mac.gw.example.com/_tunnel/connect
export CICY_TUNNEL_TOKEN=<token>
cicy-code
```

### 方式三:内联 flag(临时)

```sh
cicy-code --tunnel wss://my-mac.gw.example.com/_tunnel/connect --tunnel-insecure
# token 仍从 tunnel.json / CICY_TUNNEL_TOKEN 取,不进命令行
```

| 名称 | 作用 |
| --- | --- |
| `--tunnel <url>` | 隧道地址,触发拨号(token 走文件/env) |
| `--tunnel-insecure` | 跳过 TLS 校验(**自签证书**时必带) |
| `CICY_TUNNEL_URL` / `CICY_TUNNEL_TOKEN` | 等价于文件里的 url / token |

::: tip 不需要 `--public`
隧道转发到节点的 **`127.0.0.1:<port>`**(回环),而 cicy-code 默认就绑 `127.0.0.1`,所以**不用加 `--public`**。
保持默认回环反而更安全:局域网里没人能直连 :8008,唯一入口是隧道 + 节点 api_token。
`--public`(绑 `0.0.0.0`)只在你还想让同网段直接访问时才加,和隧道无关。
:::

::: tip 自签证书 = insecure
cicy-hub 默认 `TLS_MODE=selfsigned` 时,`tunnel.json` 里要 `"insecure": true`
(或加 `--tunnel-insecure`),否则拨号会因 TLS 校验失败而不断重连。换成 Let's Encrypt /
自带正式证书后设回 `false` / 去掉它。`enroll` 已按当前 TLS 模式自动填好 `insecure`。
:::

::: info 旧名仍可用(deprecated)
`--gateway` / `--gateway-token[-file]` / `--gateway-insecure` / `CICY_GATEWAY_*` / `gateway.json`
作为兼容别名保留,老部署不受影响;新配置请用上面的 `--tunnel*` / `tunnel.json`。
:::

## 浏览器信任的证书(Let's Encrypt,自动)

泛域名证书要走 DNS-01 校验,把 DNS 服务商的 API token 给 cicy-hub 即可自动签发。以 Cloudflare 为例:

```sh
DOMAIN=example.com \
TLS_MODE=letsencrypt \
ACME_EMAIL=you@example.com \
LEGO_PROVIDER=cloudflare \
CF_DNS_API_TOKEN=xxxxxxxx \
docker compose up -d --build
```

其他服务商见 [lego 支持列表](https://go-acme.github.io/lego/dns/),设置对应的 `LEGO_PROVIDER` 与其环境变量。

## 自带证书

```sh
# 把 fullchain 与 key 放进卷:/data/tls-cert.pem  /data/tls-key.pem
DOMAIN=example.com TLS_MODE=custom docker compose up -d --build
```

## 环境变量

| 变量 | 默认 | 含义 |
| --- | --- | --- |
| `DOMAIN` | —(必填) | 对外服务 `*.gw.$DOMAIN` |
| `ORG` | `self` | 你名下节点的逻辑归属 |
| `LICENSE_MAX_NODES` | `50` | 本隧道的并发节点上限 |
| `TLS_MODE` | `selfsigned` | `selfsigned` / `letsencrypt` / `custom` |
| `NODE_TTL` | `8760h` | 签发的节点 token 有效期 |
| `ADDR` | `:443` | 监听地址 |

## 常用命令

```sh
docker compose exec hub info            # 打印当前配置
docker compose exec hub enroll <slug>   # 接入一台新设备
docker compose logs -f hub              # 看隧道起落
```

::: warning 别丢 gwdata 卷
签名密钥在这个卷里。重新生成密钥会让你已经签发的**所有节点 token 失效**,得逐台重新 `enroll`。
:::

## 和 Cloudflare 隧道的区别

[单机部署](/deploy/single) 里的 `--cft` 走 Cloudflare,快、免运维,但域名与信任链在 Cloudflare 手上。
cicy-hub 则是**你自己的中立中继**:自己的域名、自己的证书、自己签发准入,数据只从你的机器过一遍。
两者可以并存——按设备选。
