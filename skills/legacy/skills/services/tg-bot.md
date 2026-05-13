# tg-bot Service

通过 Telegram 远程控制 Tmux 终端的 Bot 服务。

## 服务信息

- **项目路径:** ~/projects/ai-workers/tg-bot
- **管理方式:** Supervisor
- **管理器:** tg-bot-manager
- **Bot 实例:** tg_bot_* (动态创建)

## 快速操作

### 健康检查
```bash
tg-bot-check                      # 自动检查服务状态
```

### 服务控制
```bash
# 查看状态
sudo supervisorctl status | grep tg

# 重启管理器
sudo supervisorctl restart tg-bot-manager

# 重启所有 Bot
sudo supervisorctl restart tg_bot_*

# 停止服务
sudo supervisorctl stop tg-bot-manager
sudo supervisorctl stop tg_bot_*
```

### 查看日志
```bash
# 管理器日志
sudo supervisorctl tail -f tg-bot-manager

# 特定 Bot 日志
sudo supervisorctl tail -f tg_bot_w-20065_main.0

# 所有 tg 相关日志
sudo supervisorctl tail tg-bot-manager
```

## 功能说明

### Telegram 指令

| 指令 | 说明 | 是否执行 |
|------|------|----------|
| 普通文字 | 输入到终端 | 否 |
| `/run xxx` | 执行命令 | 是 |
| `/xxx` | 执行命令 | 是 |

### 使用示例

```
# 输入文字(不执行)
你好，系统上线了吗？

# 执行命令
/run ls -la
/pwd
/run cd /tmp && ls
```

## 架构说明

- **supervisor_manager.py** - 全局管理器(大脑)
  - 监控 MySQL 数据库 `ttyd_config` 表
  - 自动创建/删除 Bot 实例
  - 生成 Supervisor 配置文件

- **tg_bot_bridge.py** - 消息中继脚本
  - 接收 Telegram 消息
  - 发送命令到 Tmux Pane
  - 返回执行结果

## 配置要求

### 数据库表 (ttyd_config)

必需字段:
- `pane_id` - Tmux Pane ID
- `tg_token` - Telegram Bot Token
- `tg_chat_id` - Telegram Chat ID
- `tg_enable` - 启用标志 (1=启用, 0=禁用)
- `workspace` - 工作目录
- `proxy` - 代理地址 (可选)

### 环境变量 (.env)

```bash
MYSQL_HOST=localhost
MYSQL_PORT=3306
MYSQL_USER=root
MYSQL_PASSWORD=xxx
MYSQL_DATABASE=xxx
SUPERVISOR_CONF_DIR=/etc/supervisor/conf.d
TMUX_SOCKET=/home/w3c_offical/.tmux/default
```

## 故障排查

### 管理器未运行
```bash
tg-bot-check                      # 自动诊断
sudo supervisorctl restart tg-bot-manager
```

### Bot 实例未创建
1. 检查数据库配置: `tg_enable=1`
2. 查看管理器日志: `sudo supervisorctl tail tg-bot-manager`
3. 重新加载配置: `sudo supervisorctl reread && sudo supervisorctl update`

### Bot 无响应
1. 检查 Telegram Token 和 Chat ID
2. 检查代理配置
3. 查看 Bot 日志: `sudo supervisorctl tail tg_bot_*`

### 手动重启
```bash
# 重启管理器
sudo supervisorctl restart tg-bot-manager

# 重启特定 Bot
sudo supervisorctl restart tg_bot_w-20065_main.0
```

## 项目结构

```
~/projects/ai-workers/tg-bot/
├── supervisor_manager.py   # 全局管理器
├── tg_bot_bridge.py       # 消息中继脚本
├── supervisor.conf        # Supervisor 配置模板
├── requirements.txt       # Python 依赖
├── .env                   # 环境变量
└── logs/                  # 日志目录
```

## 相关技能

- `check-all` - 统一服务检查
- `fapi` - FastAPI 服务管理

## 注意事项

- 管理器会自动监控数据库变化,动态创建/删除 Bot 实例
- 修改数据库配置后,管理器会自动生效,无需手动重启
- Bot 实例由管理器控制,不要手动修改 Supervisor 配置
- 确保 Tmux Pane 存在且可访问
