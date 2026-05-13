# FRP Tunnel (ft) - SSH/端口隧道工具

## 用途
通过 FRP 建立端口隧道，将本地端口映射到远程服务器。

## 安装位置
- Binary: `~/.local/bin/ft` (v1.1.4)
- FRP binary: `~/.frp-tunnel/bin/frpc`
- Config: `~/data/frp/frpc.yaml`
- Log: `~/data/frp/frpc.log`
- Server: `35.220.220.223:7000`

## 常用命令

```bash
# 查看客户端状态
ft client status

# 查看服务端状态
ft server status


# 启动服务端
ft server


# 生成新 token
ft token

# 直接调用 frpc
ft frpc -c ~/data/frp/frpc.yaml
```

## 当前配置
- SSH: localhost:22 → 远程:6000
- Service: localhost:6080 → 远程:16080

## 配置文件格式 (frpc.yaml)
```yaml
auth:
  token: frp_xxx
serverAddr: 35.220.220.223
serverPort: 7000
proxies:
- name: ssh_6000
  type: tcp
  localIP: 127.0.0.1
  localPort: 22
  remotePort: 6000
```
