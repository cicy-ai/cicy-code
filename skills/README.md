# skills

这个目录放的是随仓库提供的本地 CLI。

当前主要有两个：

- `skills/cicy-code`：节点 API 客户端
- `skills/cicy-master`：本地节点注册表管理器

## 1. `skills/cicy-code`

`cicy-code` 是一个 shell CLI，用来调节点或本机 `cicy-code` 的 HTTP API。

### 默认配置来源

1. 选中节点时：
   - 默认注册表文件：`~/Private/cicy-node.json`
   - 可改写：`CICY_NODES_FILE`
2. 未选节点时：
   - API 默认：`http://127.0.0.1:8008`
   - token 默认读取：`~/cicy-ai/global.json`

### 当前能力

- 通用 API：`api/get/post/patch/put/delete`
- pane / tmux：`panes`、`pane`、`create-pane`、`restart-pane`、`send`、`capture`
- runtime：`runtime-instances`、`runtime-tasks`、`runtime-artifacts`
- shared workspace：`shared-work-items`、`shared-handoffs`、`shared-artifacts`、`shared-events`
- queue / collab / groups / settings / skills
- auth：`tokens`、`create-token`、`delete-token`
- 基础检查：`ping`、`health`

### 能力判断

当前脚本会读取节点能力字段：

- `supports_tmux`
- `supports_ttyd`
- `supports_code_server`

如果节点显式声明这些能力为 false，tmux 相关命令会直接拒绝执行。

### 例子

```bash
# 调本机
skills/cicy-code ping
skills/cicy-code panes

# 调注册表里的某个节点
skills/cicy-code -n dev ping
skills/cicy-code -n dev runtime-instances
skills/cicy-code -n dev send w-10001:main.0 'hello'
```

## 2. `skills/cicy-master`

`cicy-master` 是一个 Python CLI，用来管理本地节点注册表文件。

### 默认文件

- 默认：`~/Private/cicy-node.json`
- 可改写：`CICY_NODES_FILE`
- 兼容导入旧文件：`~/Private/cicy-nodes.json`

### 当前命令

- `list`
- `register`
- `sync`
- `ping`
- `health`

### 当前特性

- canonical schema 归一化
- 接受 `instance_*` 别名并落盘为 `machine_*`
- `sync` 支持：
  - `--from-registry`
  - `--registry-secret`
  - `--migrate-legacy`
  - `--merge-file`
- 写文件时使用：
  - `flock`
  - 同目录临时文件 + `os.replace()` 原子替换
- `ping/health --write` 会把 `online/status/last_seen_at` 回写到注册表

### 例子

```bash
skills/cicy-master list
skills/cicy-master register dev http://127.0.0.1:8008 --set-default
skills/cicy-master ping dev --write
skills/cicy-master health --all --write
```

## 3. `proxy_ssh`

`proxy_ssh` 是一个 Python CLI，用来管理本地 SSH 动态转发代理配置。

### 默认文件

- 默认：`~/cicy-ai/db/proxy_ssh.json`
- 可改写：`CICY_PROXY_SSH_FILE`

### 当前命令

- `list`
- `show`
- `create`
- `start`
- `stop`
- `test`
- `delete`

### 当前特性

- canonical schema：`default + profiles[]`
- 写文件时使用：
  - `flock`
  - 同目录临时文件 + `os.replace()` 原子替换
- 根据结构化字段运行时生成 SSH `-D` 启动命令
- 运行态按本地 `ssh -D` 进程动态检测

### 例子

```bash
skills/proxy_ssh create demo --ssh-host 1.2.3.4 --ssh-user root --local-port 1084
skills/proxy_ssh list
skills/proxy_ssh show demo
skills/proxy_ssh start demo
skills/proxy_ssh test demo
skills/proxy_ssh stop demo
skills/proxy_ssh delete demo
```

## 4. 当前 canonical schema

```json
{
  "default": "dev",
  "machines": [
    {
      "id": "dev",
      "machine_key": "dev",
      "label": "Dev",
      "host": "127.0.0.1",
      "port": 8008,
      "url": "http://127.0.0.1:8008",
      "token": "<api-token>",
      "status": "online",
      "online": true,
      "last_seen_at": "2026-04-29T00:00:00Z",
      "capabilities": {
        "runtime_kind": "container"
      }
    }
  ]
}
```

归一化规则以 `skills/cicy-master` 与 `api/mgr/machines.go` 当前实现为准：

- `machine_key` 为空时优先回退 `id`
- `id` 为空时回退 `machine_key`
- 仍为空时回退 `url`
- `label` 为空时取 `machine_key / id / url`
- `port=0` 时默认 `8008`
- `capabilities.runtime_kind` 缺失时默认 `container`
- `online=true` 时 `status` 归一为 `online`
- `online=true` 且 `last_seen_at` 为空时自动写当前时间

## 4. 需要特别注意的路径差异

当前后端 runtime 默认机器文件是：

- `~/cicy-ai/cicy-node.json`

但 skills 默认机器文件是：

- `~/Private/cicy-node.json`

如果你希望前后端共用同一份注册表，先设置：

```bash
export CICY_NODES_FILE=~/cicy-ai/cicy-node.json
```

## 5. 与 managed runtime 的关系

后端代码当前还支持通过这些环境变量做节点自注册：

- `CICY_MASTER_URL`
- `CICY_MASTER_TOKEN`
- `CICY_PUBLIC_URL`

那是 runtime 侧能力；它和 `skills/cicy-master` 维护的本地注册表是两条并行路径，不要混为一个概念。
