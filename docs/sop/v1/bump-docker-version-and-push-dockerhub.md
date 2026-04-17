# Bump Docker Version And Push DockerHub

## 目的

规范 `cicy-code-v1` 的 runtime Docker 版本升级流程。

这份 SOP 只描述当前标准链路：

1. bump 代码版本
2. build runtime image
3. push Docker Hub
4. 更新 `~/global.json -> images.runtime`

## 标准目标

标准目标镜像是：

```bash
cicybot/cicy-code-runtime:<new_version>
```

并且同时刷新：

```bash
cicybot/cicy-code-runtime:latest
```

## 完成定义

只有下面 6 条都满足，才算完成：

1. `python3 dev.py --dockerVersion` 显示 `package_version=<new_version>`
2. `python3 dev.py --bumpVersion <new_version>` 已执行
3. `python3 dev.py --dockerBuild --dockerBuildVersion <new_version>` 已执行
4. Docker Hub 上存在 `cicybot/cicy-code-runtime:<new_version>`
5. `~/global.json -> images.runtime_tag` 已切到 `<new_version>`
6. `~/global.json -> images.runtime` 已切到 `cicybot/cicy-code-runtime:<new_version>`

只改版本号，不 build/push，不算完成。

## 工作目录

统一在项目目录执行：

```bash
cd /home/w3c_offical/projects/cicy-code-v1
```

不要混用别的工作树路径。

## 前置条件

### 1. Docker 已登录目标仓库

必须能 push `cicybot/cicy-code-runtime`。

先确认：

```bash
docker login
cat ~/.docker/config.json
```

### 2. 当前机器具备 COS 上传配置

`python3 dev.py --dockerBuild` 会先跑：

- `./build.sh assets`
- `python3 ./scripts/cos-upload.py app`
- `python3 ./scripts/cos-upload.py ttyd`

所以 `~/global.json` 里至少要有：

- `tencent.bucket`
- `tencent.region`

### 3. 固定目标仓库

标准流程里不要依赖本机 Docker 登录用户名自动推断仓库。

统一显式指定：

```bash
export DOCKER_IMAGE_REPOSITORY=cicybot/cicy-code-runtime
```

## 标准流程

### 1. 看当前版本

```bash
cd /home/w3c_offical/projects/cicy-code-v1
export DOCKER_IMAGE_REPOSITORY=cicybot/cicy-code-runtime
python3 dev.py --dockerVersion
```

预期至少看到：

- `package_version=...`
- `dockerhub_repository=cicybot/cicy-code-runtime`
- `current_runtime_image=...`
- `current_runtime_tag=...`

### 2. 决定新版本号

标准做法：patch 加一位。

例如：

- 当前：`1.0.25`
- 新版：`1.0.26`

### 3. bump 代码版本

```bash
cd /home/w3c_offical/projects/cicy-code-v1
python3 dev.py --bumpVersion 1.0.26
```

预期输出类似：

```bash
[dev] bumped version=1.0.26
[dev] npm_version=1.0.26
[dev] mgr_version=1.0.26
[dev] ui_version=1.0.26
[dev] tmux_version=1.0.26
```

这一步只同步版本号，还没有 build/push。

### 4. build 并 push Docker Hub

```bash
cd /home/w3c_offical/projects/cicy-code-v1
export DOCKER_IMAGE_REPOSITORY=cicybot/cicy-code-runtime
python3 dev.py --dockerBuild --dockerBuildVersion 1.0.26
```

这一步当前会自动做：

1. 再次同步版本
2. build 静态资源
3. 上传 `app` 和 `ttyd` 静态资源到 COS
4. 执行 `./build.sh docker 1.0.26`
5. 打本地 tag：
   `cicy-code:1.0.26`
6. 打远端 tag：
   `cicybot/cicy-code-runtime:1.0.26`
7. 打 latest：
   `cicybot/cicy-code-runtime:latest`
8. push 这两个 Docker Hub tag
9. 更新 `~/global.json`

### 5. 验证 `~/global.json`

```bash
python3 - <<'PY'
import json
from pathlib import Path
p = Path('/home/w3c_offical/global.json')
data = json.loads(p.read_text())
print(json.dumps(data.get('images', {}), ensure_ascii=False, indent=2))
PY
```

预期至少包含：

```json
{
  "runtime": "cicybot/cicy-code-runtime:1.0.26",
  "runtime_repository": "cicybot/cicy-code-runtime",
  "runtime_tag": "1.0.26"
}
```

### 6. 再看一次版本状态

```bash
cd /home/w3c_offical/projects/cicy-code-v1
export DOCKER_IMAGE_REPOSITORY=cicybot/cicy-code-runtime
python3 dev.py --dockerVersion
```

预期：

- `package_version=1.0.26`
- `dockerhub_repository=cicybot/cicy-code-runtime`
- `current_runtime_image=cicybot/cicy-code-runtime:1.0.26`
- `current_runtime_tag=1.0.26`

## 验收

### 1. 本地镜像

```bash
docker images | rg 'cicy-code|cicybot/cicy-code-runtime'
```

预期至少有：

- `cicy-code 1.0.26`
- `cicybot/cicy-code-runtime 1.0.26`
- `cicybot/cicy-code-runtime latest`

### 2. Docker Hub

标准要求是 Docker Hub 上已经能拉到新 tag。

最简单验证：

```bash
docker pull cicybot/cicy-code-runtime:1.0.26
```

### 3. global.json

```bash
python3 - <<'PY'
import json
from pathlib import Path
p = Path('/home/w3c_offical/global.json')
data = json.loads(p.read_text())
images = data.get('images', {})
print(images.get('runtime'))
print(images.get('runtime_repository'))
print(images.get('runtime_tag'))
PY
```

预期：

```bash
cicybot/cicy-code-runtime:1.0.26
cicybot/cicy-code-runtime
1.0.26
```

## 最短命令模板

```bash
cd /home/w3c_offical/projects/cicy-code-v1
export DOCKER_IMAGE_REPOSITORY=cicybot/cicy-code-runtime
python3 dev.py --dockerVersion
python3 dev.py --bumpVersion 1.0.26
python3 dev.py --dockerBuild --dockerBuildVersion 1.0.26
python3 dev.py --dockerVersion
```

## 回滚

如果新镜像已经 push，但你要把默认 runtime tag 切回旧版本，不重新 build：

```bash
cd /home/w3c_offical/projects/cicy-code-v1
export DOCKER_IMAGE_REPOSITORY=cicybot/cicy-code-runtime
python3 dev.py --dockerSetVersion 1.0.25
```

这一步只会更新：

- `~/global.json -> images.runtime`
- `~/global.json -> images.runtime_repository`
- `~/global.json -> images.runtime_tag`

不会重新 build，也不会重新 push。

## 禁忌

- 不要手改多个版本文件，统一走 `python3 dev.py --bumpVersion`
- 不要只 build 本地镜像，不 push Docker Hub
- 不要不设 `DOCKER_IMAGE_REPOSITORY` 就直接执行标准发布
- 不要只看 `package_version`，不看 `~/global.json`
- 不要在未确认 COS 配置时直接跑 `--dockerBuild`
