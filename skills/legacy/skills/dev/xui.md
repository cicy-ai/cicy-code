# xui

X UI 桌面控制代理 CLI，通过 HTTP API 远程控制 X 桌面（截图、输入、窗口管理、Chrome/Electron 管理）。

## 架构

`xui.py` 绑定到指定 DISPLAY 运行 HTTP 服务，端口 = 13430 + display_num。
`xui` CLI 是对 HTTP API 的封装。启动时自动安装依赖、kill 占用端口。

## CLI 用法

```bash
xui [display_num] <command> [args]
```

display_num 默认取当前 `$DISPLAY`，端口 = 13430 + display_num。

## 命令

```bash
# 基础操作
xui 1 open <app>                           # 打开应用
xui 1 type "hello world"                   # 模拟键盘输入
xui 1 windows                              # 列出窗口
xui 1 screenshot [output.png]              # 截图

# Chrome 管理 (调用 ~/tools/chrome.sh)
xui 1 chrome                               # 启动 Chrome (默认 start)
xui 1 chrome profiles                      # 列出所有 Chrome profile
xui 1 chrome start --profile=Default       # 指定 profile 启动
xui 1 chrome start --proxy=socks5://x      # 带代理启动
xui 1 chrome start --debug-port=9222       # 指定调试端口
xui 1 chrome stop
xui 1 chrome restart

# Electron 管理 (~/projects/electron-mcp/main npm start)
xui 1 electron                             # 启动 Electron (默认 start)
xui 1 electron start --url=https://x --port=8101 --proxy=x
xui 1 electron kill                        # 强制杀掉所有 electron 进程
xui 1 electron restart

# Shell
xui 1 shell "ls -la"                       # 执行任意命令

# 安装桌面快捷方式 (支持 Linux/Mac/Windows)
xui quick                                  # 默认 display :1
xui quick 2                                # 为 display :2 创建快捷方式
```

## HTTP API

服务端口: `13430 + display_num`

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/screenshot?app=x&loop=true` | 截图 / 自动刷新页面 |
| POST | `/api/screenshot` | 截图返回 base64 |
| GET | `/api/windows` | 列出窗口 |
| GET | `/api/ui?app=Chrome&depth=3` | UI 无障碍树 |
| POST | `/api/type` | 模拟输入 `{text, target}` |
| POST | `/api/open` | 打开应用 `{app}` |
| GET | `/api/chrome?action=start\|stop\|restart\|profiles&profile=x&proxy=x&debug_port=x` | Chrome 管理 |
| GET | `/api/electron?action=start\|restart\|kill&url=x&port=8101&proxy=x` | Electron 管理 |
| POST | `/api/run_shell` | 执行命令 `{cmd, timeout}` |

## 启动服务

```bash
# 直接启动（自动安装依赖、kill 旧端口）
python3 ~/skills/xui.py --display :1

# 通过快捷方式
xui quick
# 然后双击桌面 xui-1 图标

# VNC 启动时自动启动（已集成到 xstartup）
```

## 文件

- 服务: `~/skills/xui.py`
- CLI: `~/skills/xui` → `~/.local/bin/xui`
- Skill doc: `~/skills/xui.md`
- Chrome 脚本: `~/tools/chrome.sh`
- Electron 项目: `~/projects/electron-mcp/main/`
