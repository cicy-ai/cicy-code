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

### 1. SSH 到 Mac

```bash
ssh ton-mac
```

后面也可以继续使用 one-shot SSH 命令，不一定要保持交互 shell。

### 2. 停掉原用户已有 `code-server`

```bash
ssh ton-mac 'sudo pkill -f code-server || true'
```

可选验证：

```bash
ssh ton-mac 'pgrep -af code-server || true'
```

预期：没有输出。

### 3. 停掉原用户已有 `tmux`

```bash
ssh ton-mac 'tmux kill-server || true'
```

可选验证：

```bash
ssh ton-mac 'tmux ls || true'
```

### 4. 停掉占用 `8008` 的原 `cicy-code`

先看谁占着：

```bash
ssh ton-mac 'lsof -nP -iTCP:8008 -sTCP:LISTEN || true'
```

直接杀掉占用：

```bash
ssh ton-mac 'for pid in $(lsof -ti tcp:8008 2>/dev/null); do kill "$pid" || true; done'
```

可选验证：

```bash
ssh ton-mac 'lsof -nP -iTCP:8008 -sTCP:LISTEN || true'
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

### 6. 在当前机器交叉编译 macOS 版本

在当前机器直接编译 macOS 产物：

```bash
cd ~/projects/cicy-code
./build.sh build darwin
```

可选验证：

```bash
ls -l ~/projects/cicy-code/api/cicy-code
file ~/projects/cicy-code/api/cicy-code
```

### 7. 把 build 后的 binary 拷给测试用户

这里只传单个 binary，不同步整份仓库：

```bash
ssh ton-mac 'sudo mkdir -p /Users/cicy-code/bin && sudo chown -R cicy-code:staff /Users/cicy-code/bin'
scp ~/projects/cicy-code/api/cicy-code ton-mac:/tmp/cicy-code
ssh ton-mac 'sudo mv /tmp/cicy-code /Users/cicy-code/bin/cicy-code && sudo chown cicy-code:staff /Users/cicy-code/bin/cicy-code && sudo chmod +x /Users/cicy-code/bin/cicy-code'
```

可选验证：

```bash
ssh ton-mac 'ls -l /Users/cicy-code/bin/cicy-code'
```

### 8. 用测试用户启动 `cicy-code`

这里不要再用 `python3 dev.py`，直接跑 build 出来的 binary：

如果要直接以前台方式启动：

```bash
ssh ton-mac 'sudo -u cicy-code -H zsh -lc "/Users/cicy-code/bin/cicy-code --public"'
```

如果只想后台启动，推荐：

```bash
ssh ton-mac 'sudo -u cicy-code -H sh -lc "mkdir -p /Users/cicy-code/.dev-logs && /Users/cicy-code/bin/cicy-code --public > /Users/cicy-code/.dev-logs/remote.log 2>&1 < /dev/null &"'
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
cd ~/projects/cicy-code
ssh ton-mac 'sudo pkill -f code-server || true'
ssh ton-mac 'tmux kill-server || true'
ssh ton-mac 'pids="$(lsof -ti tcp:8008 2>/dev/null || true)"; if [ -n "$pids" ]; then kill $pids || true; fi'
ssh ton-mac 'id cicy-code >/dev/null 2>&1 || sudo sysadminctl -addUser cicy-code -fullName "CiCy Code" -password "cicy-code-test" -home /Users/cicy-code'
ssh ton-mac 'sudo mkdir -p /Users/cicy-code /Users/ton/projects && sudo chown -R cicy-code:staff /Users/cicy-code'
./build.sh build darwin
rsync -a --delete ~/projects/cicy-code/ ton-mac:/Users/ton/projects/cicy-code/
ssh ton-mac 'sudo rsync -a --delete /Users/ton/projects/cicy-code/ /Users/cicy-code/cicy-code/'
ssh ton-mac 'sudo chown -R cicy-code:staff /Users/cicy-code/cicy-code'
ssh ton-mac 'sudo -u cicy-code -H zsh -lc "cd /Users/cicy-code/cicy-code && ./api/cicy-code --public --dev"'
```
