# VNC Server

TigerVNC 服务器,提供远程桌面访问 (DISPLAY :1)。

## 服务信息

- **DISPLAY:** :1
- **端口:** 5901
- **用户:** w3c_offical
- **启动脚本:** ~/tools/vnc-start.sh
- **密码:** o2QzMT39E8yweNN

## 快速操作

### 检查状态
```bash
ps aux | grep Xtigervnc | grep :1
```

### 启动服务
```bash
sudo -u w3c_offical bash /home/w3c_offical/tools/vnc-start.sh
```

### 停止服务
```bash
sudo -u w3c_offical vncserver -kill :1
```

### 重启服务
```bash
sudo -u w3c_offical vncserver -kill :1
sudo -u w3c_offical bash /home/w3c_offical/tools/vnc-start.sh
```

## 连接方式

### VNC 客户端
```
地址: <server-ip>:5901
密码: o2QzMT39E8yweNN
```

### SSH 隧道 (推荐)
```bash
ssh -L 5901:localhost:5901 user@server
# 然后连接 localhost:5901
```

## 配置

### 密码管理
```bash
# 更新密码
sudo -u w3c_offical bash -c "echo 'new_password' | vncpasswd -f > ~/.vnc/passwd && chmod 600 ~/.vnc/passwd"

# 查看密码文件
ls -la /home/w3c_offical/.vnc/passwd
```

### 分辨率设置
编辑启动脚本中的 `-geometry` 参数:
```bash
vncserver :1 -geometry 1920x1080 -depth 24
```

## 故障排查

### 服务未运行
```bash
# 检查进程
ps aux | grep Xtigervnc

# 查看日志
cat /home/w3c_offical/.vnc/*.log

# 重新启动
sudo -u w3c_offical vncserver -kill :1
sudo -u w3c_offical bash /home/w3c_offical/tools/vnc-start.sh
```

### 端口占用
```bash
# 检查端口
ss -tlnp | grep 5901

# 强制清理
sudo -u w3c_offical vncserver -kill :1
```

### 无法连接
1. 检查防火墙规则
2. 确认 VNC 服务运行中
3. 验证密码正确
4. 尝试使用 SSH 隧道

## 依赖服务

- **Electron MCP** - 依赖 VNC (DISPLAY :1)
- **vnc-proxy** - 提供 Web 访问接口

## 相关技能

- `check-all` - 统一服务检查 (会自动启动 VNC)
- `vnc-proxy-check` - VNC Web 代理检查

## 注意事项

- VNC 服务必须在 Electron MCP 之前启动
- 服务运行在 w3c_offical 用户下
- 密码存储在 `~/.vnc/passwd` (加密)
- 默认只监听 localhost,需要通过 SSH 隧道或 Cloudflare 访问
