# Test Start `cicy-code` In macOS Test User

这份 SOP 记录当前用 `ton-mac` 这台 Mac，先停掉原用户环境里的 `code-server` / `tmux` / `8008` 端口占用，再创建测试用户 `cicy-code`，把在当前机器 build 出来的 `cicy-code` binary 拷给测试用户并启动。

## 目标

1. SSH 到 `ton-mac`
2. 停掉原用户下已有的 `code-server`
3. 停掉原用户下已有的 `tmux` 会话
4. 停掉占用 `8008` 的原 `cicy-code`
5. 创建测试用户 `cicy-code`
6. 在当前机器交叉编译 macOS 版本
7. 把 build 后的 binary 拷到 Mac
8. 用测试用户直接启动 `cicy-code` binary

## 当前环境事实

截至这份 SOP 编写时，本机已有可用 SSH alias：

```bash
ssh ton-mac
```

已确认：

- `ton-mac` 对应用户：`ton`
- 远端系统：`macOS 15.7.3`
- CPU 架构：`x86_64`
- `ton` 有免密 `sudo`
- 远端已有 `/usr/local/bin/code-server`

## 前置条件

### 1. 本地仓库目录

```bash
cd ~/projects/cicy-code
```

### 2. 本地能直接 SSH 到 `ton-mac`

```bash
ssh ton-mac 'whoami && uname -m'
```

预期输出类似：

```text
ton
x86_64
```

### 3. 本地准备好要同步到 Mac 的 binary 来源目录

```bash
cd ~/projects/cicy-code
git status --short
```

如果当前工作区有未提交改动，build 出来的 binary 就基于本地当前内容。

## 标准流程

### 0. 删除旧测试用户（全新测试必做）

彻底清除上一轮的残留数据：

```bash
ssh mac 'sudo sysadminctl -deleteUser cicy-code 2>/dev/null || true; sudo rm -rf /Users/cicy-code; echo "done"'
```

验证：

```bash
ssh mac 'id cicy-code 2>&1; ls /Users/cicy-code 2>&1 || echo "目录已删除"'
```

### 1. 清理旧进程

```bash
ssh mac 'sudo pkill -f cicy-code 2>/dev/null || true; sudo pkill -f code-server 2>/dev/null || true; sudo pkill -f gotty 2>/dev/null || true; tmux kill-server 2>/dev/null || true; sleep 1; echo "done"'
```

### 5. 创建测试用户 `cicy-code`

先看用户是否已存在：

```bash
ssh ton-mac 'id cicy-code >/dev/null 2>&1; echo $?'
```

如果输出 `0`，说明用户已存在，直接继续。  
如果输出非 `0`，执行创建：

```bash
ssh ton-mac 'sudo sysadminctl -addUser cicy-code -fullName "CiCy Code" -password "cicy-code-test" -home /Users/cicy-code'
```

然后补一遍目录权限：

```bash
ssh ton-mac 'sudo mkdir -p /Users/cicy-code && sudo chown -R cicy-code:staff /Users/cicy-code'
```

验证：

```bash
ssh ton-mac 'id cicy-code && dscl . -read /Users/cicy-code NFSHomeDirectory'
```

### 6. Build 并 scp 最新 binary 到 Mac

```bash
cd ~/projects/cicy-code
rm -rf api/mgr/ui && cp -r app/dist api/mgr/ui
cd api && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -a -buildvcs=false -o cicy-code-darwin ./mgr/
scp api/cicy-code-darwin mac:/tmp/cicy-code
ssh mac 'chmod +x /tmp/cicy-code && echo "版本: $(/tmp/cicy-code --version 2>&1 | head -1)"'
```

### 7. 把 binary 拷给测试用户

binary 已在 `/tmp/cicy-code`，直接移过去：

```bash
ssh mac 'sudo mkdir -p /Users/cicy-code/bin && sudo mv /tmp/cicy-code /Users/cicy-code/bin/cicy-code && sudo chown cicy-code:staff /Users/cicy-code/bin/cicy-code && sudo chmod +x /Users/cicy-code/bin/cicy-code'
```

可选验证：

```bash
ssh mac 'ls -l /Users/cicy-code/bin/cicy-code'
```

### 8. 用测试用户启动 `cicy-code`

这里不要再用 `python3 dev.py`，直接跑 build 出来的 binary：

```bash
ssh mac 'sudo -u cicy-code -H sh -c "mkdir -p /Users/cicy-code/logs; /Users/cicy-code/bin/cicy-code --public --agents=claude,codex,opencode > /Users/cicy-code/logs/cicy-code.log 2>&1 < /dev/null &"'
```

### 9. 验证进程和端口

```bash
ssh ton-mac 'pgrep -af cicy-code || true'
ssh ton-mac 'lsof -nP -iTCP:8008 -sTCP:LISTEN || true'
```

如果是后台启动，也可以看日志：

```bash
ssh ton-mac 'tail -n 80 /Users/cicy-code/.dev-logs/remote.log'
```

## 验收

只有下面几条都满足，才算完成：

1. `ssh ton-mac 'id cicy-code'` 正常
2. 原用户下不再有旧的 `code-server`
3. 原用户下不再有旧的 `tmux` 会话
4. `8008` 不再被旧进程占用
5. `~/projects/cicy-code/api/cicy-code` 已在当前机器 build 成功
6. `/Users/cicy-code/bin/cicy-code` 存在且属主正确
7. `/Users/cicy-code/bin/cicy-code --public` 能在测试用户下启动
8. 测试用户启动后的 `cicy-code` 能监听 `8008`

## 清理

如果只是一次性测试，清理命令：

### 删测试 binary

```bash
ssh ton-mac 'sudo rm -rf /Users/cicy-code/cicy-code'
```

### 删测试用户

```bash
ssh ton-mac 'sudo sysadminctl -deleteUser cicy-code || true'
ssh ton-mac 'sudo rm -rf /Users/cicy-code'
```

## 最短命令模板

```bash
# 0. 删除旧用户
ssh mac 'sudo sysadminctl -deleteUser cicy-code 2>/dev/null || true; sudo rm -rf /Users/cicy-code'
# 1-4. 清理旧进程
ssh mac 'sudo pkill -f cicy-code 2>/dev/null || true; sudo pkill -f code-server 2>/dev/null || true; sudo pkill -f gotty 2>/dev/null || true; tmux kill-server 2>/dev/null || true; sleep 1'
# 5. 创建用户
ssh mac 'sudo sysadminctl -addUser cicy-code -fullName "CiCy Code" -password "cicy-code-test" -home /Users/cicy-code; sudo mkdir -p /Users/cicy-code && sudo chown -R cicy-code:staff /Users/cicy-code'
# 6. 下载最新 binary（ghproxy 加速，自动识别架构）
# 6. build + scp
cd ~/projects/cicy-code && SKIP_NPM=1 ./build.sh all 2>&1 | tail -1
scp dist/cicy-code-darwin-amd64 mac:/tmp/cicy-code && ssh mac 'chmod +x /tmp/cicy-code'
# 7. 安装给测试用户
ssh mac 'sudo mkdir -p /Users/cicy-code/bin && sudo mv /tmp/cicy-code /Users/cicy-code/bin/cicy-code && sudo chown cicy-code:staff /Users/cicy-code/bin/cicy-code'
# 8. 启动
ssh mac 'sudo -u cicy-code -H sh -c "mkdir -p /Users/cicy-code/logs; /Users/cicy-code/bin/cicy-code --public --agents=claude,codex,opencode > /Users/cicy-code/logs/cicy-code.log 2>&1 < /dev/null &"'; sleep 4; ssh mac 'curl -s http://127.0.0.1:8008/ | head -1'
```
