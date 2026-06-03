# Bump Docker Version And Push DockerHub

这份 SOP 只描述当前代码里真实可执行的 runtime 发布流程。

## 目标

1. bump 仓库版本号
2. 构建 runtime Docker 镜像
3. push 到 Docker Hub
4. 更新 `~/cicy-ai/global.json -> images.runtime*`

## 当前代码事实

### 版本同步入口

```bash
python3 dev.py --bumpVersion <version>
```

它底层调用 `scripts/sync-version.py`，会同步：

- `npm/package.json`
- `app/package.json`
- `app/package-lock.json`
- `app/src/config.ts`
- `api/mgr/main.go`
- `.cicy_tmux.conf`

### Docker build/push 入口

```bash
python3 dev.py --dockerBuild --dockerBuildVersion <version>
```

它当前会自动做：

1. 再次同步版本号
2. `./build.sh assets`
3. `python3 ./scripts/r2-upload.py app`
4. `python3 ./scripts/r2-upload.py ttyd`
5. `./build.sh docker <version>`
6. 给目标仓库打 tag
7. push `<version>` 与 `latest`
8. 更新 `~/cicy-ai/global.json -> images.runtime/runtime_repository/runtime_tag`

## 前置条件

### 1. 固定工作目录

```bash
cd ~/projects/cicy-code
```

### 2. Docker 已登录

```bash
docker login
```

### 3. 指定目标仓库

推荐显式指定：

```bash
export DOCKER_IMAGE_REPOSITORY=<dockerhub-user>/cicy-code-runtime
```

如果不设这个变量，`dev.py` 会尝试从：

1. `~/cicy-ai/global.json -> images.runtime*`
2. 本机 Docker 登录信息

推导目标仓库。

### 4. COS 配置存在

`--dockerBuild` 当前会强制走 CDN 资产链路，所以 `~/cicy-ai/global.json` 里至少要有：

- `tencent.bucket`
- `tencent.region`

## 标准流程

### 1. 看当前状态

```bash
cd ~/projects/cicy-code
export DOCKER_IMAGE_REPOSITORY=<dockerhub-user>/cicy-code-runtime
python3 dev.py --dockerVersion
```

预期至少看到：

- `package_version=...`
- `dockerhub_repository=...`
- `current_runtime_image=...`
- `current_runtime_tag=...`

### 2. bump 版本

```bash
python3 dev.py --bumpVersion 2.0.2
```

预期输出类似：

```text
[dev] bumped version=2.0.2
[dev] npm_version=2.0.2
[dev] mgr_version=2.0.2
[dev] ui_version=2.0.2
[dev] tmux_version=2.0.2
```

### 3. build 并 push

```bash
python3 dev.py --dockerBuild --dockerBuildVersion 2.0.2
```

这一步结束后，目标仓库里应至少有：

- `<repo>:2.0.2`
- `<repo>:latest`

同时 `~/cicy-ai/global.json` 会被更新到新 tag。

### 4. 看更新后的状态

```bash
python3 dev.py --dockerVersion
```

### 5. 验证 `global.json`

```bash
python3 - <<'PY'
import json
from pathlib import Path
p = Path.home() / 'cicy-ai' / 'global.json'
data = json.loads(p.read_text())
print(json.dumps(data.get('images', {}), ensure_ascii=False, indent=2))
PY
```

预期至少包含：

```json
{
  "runtime": "<repo>:2.0.2",
  "runtime_repository": "<repo>",
  "runtime_tag": "2.0.2"
}
```

## 验收

只有下面几条都满足，才算完成：

1. `python3 dev.py --dockerVersion` 显示新 `package_version`
2. Docker Hub 已经能拉到新 tag
3. `~/cicy-ai/global.json -> images.runtime*` 已更新
4. 用这个新 tag 启动的新 runtime 可以正常打开 `/api/health`

## 回滚

如果镜像已经存在，只想把默认 runtime tag 切回旧版本：

```bash
python3 dev.py --dockerSetVersion 2.0.1
```

这一步只更新：

- `~/cicy-ai/global.json -> images.runtime`
- `~/cicy-ai/global.json -> images.runtime_repository`
- `~/cicy-ai/global.json -> images.runtime_tag`

不会重新 build，也不会重新 push。

## 最短命令模板

```bash
cd ~/projects/cicy-code
export DOCKER_IMAGE_REPOSITORY=<dockerhub-user>/cicy-code-runtime
python3 dev.py --dockerVersion
python3 dev.py --bumpVersion 2.0.2
python3 dev.py --dockerBuild --dockerBuildVersion 2.0.2
python3 dev.py --dockerVersion
```
