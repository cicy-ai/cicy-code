# cping

国内多节点 Ping 测试工具。从服务器 TCP 连接国内公共 DNS 节点测延迟，模拟国内用户到服务器的网络质量。

## 用法

```bash
cping <domain_or_ip>
```

## 示例

```bash
cping tn.cicy-ai.com
cping 35.241.97.128
cping baidu.com
```

## 原理

TCP 握手延迟是双向的（SYN → SYN-ACK → ACK），所以从服务器到国内节点的 TCP 延迟 ≈ 国内用户到服务器的延迟。

测试节点：
- 阿里云 DNS (杭州)
- 腾讯云 DNS (深圳)
- 百度 DNS (北京)
- CNNIC (北京)
- 华为云 DNS
- 360 DNS (北京)

## 输出

```
🏓 cping - tn.cicy-ai.com (35.241.97.128)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  📡 本机 → tn.cicy-ai.com      ✅ 0.4ms

📡 服务器 → 国内节点 (TCP延迟≈国内用户延迟):
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  腾讯云DNS(深圳)       ✅ 1.8ms
  阿里云DNS(杭州)       ✅ 2.7ms
  百度DNS(北京)         ✅ 11.0ms
  ...

  📊 最快: 1.8ms | 最慢: 31.4ms | 平均: 12.5ms
```

## 文件

- 脚本: `~/skills/cping.py`
- CLI: `/usr/local/bin/cping`
