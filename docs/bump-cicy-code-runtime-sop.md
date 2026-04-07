# CiCy Code Runtime Bump SOP

## 目的

把 `cicy-code-v1` 的 runtime 版本标准化升级为一个新的 Docker Hub tag，并让后续试用/部署默认使用这个新镜像。

这份 SOP 的标准目标是：

1. bump `cicy-code-v1` 版本
2. 构建并推送新的 `cicybot/cicy-code-runtime:<version>`
3. 自动更新 `~/global.json -> images.runtime`
4. 后续 trial deploy / runtime deploy 默认走新镜像

## 标准工作目录

统一在：

```bash
cd /pr/cicy-code-v1
```

不要一会儿在 `/pr/cicy-code-v1`，一会儿在 `/home/w3c_offical/projects/cicy-code-v1` 混着做。

## 版本来源

`dev.py` 的版本同步源最终走：

- `npm/package.json`

并同步到这些目标：

- `npm/package.json`
- `api/mgr/main.go`
- `app/src/components/Workspace.tsx`
- `.cicy_tmux.conf`

对应命令由：

```bash
python3 dev.py --bumpVersion <version>
```

统一处理，不要手改多个文件。

## 标准流程

### 1. 看当前版本

```bash
cd /pr/cicy-code-v1
python3 dev.py --dockerVersion
```

预期看到：

- `package_version`
- `current_runtime_image`
- `current_runtime_tag`

### 2. 决定新版本号

标准做法：patch 加一位。

例如：

- 当前 `1.0.6`
- 新版本用 `1.0.7`

### 3. bump 版本

```bash
cd /pr/cicy-code-v1
python3 dev.py --bumpVersion 1.0.7
```

预期输出类似：

```text
[dev] bumped version=1.0.7
[dev] npm_version=1.0.7
[dev] mgr_version=1.0.7
[dev] ui_version=1.0.7
[dev] tmux_version=1.0.7
```

### 4. 构建并推送 Docker Hub

```bash
cd /pr/cicy-code-v1
python3 dev.py --dockerBuild --dockerBuildVersion 1.0.7
```

这一步会自动做下面几件事：

1. 再次同步版本
2. 调 `./build.sh docker 1.0.7`
3. 打 tag：
   - `cicybot/cicy-code-runtime:1.0.7`
   - `cicybot/cicy-code-runtime:latest`
4. push 到 Docker Hub
5. 自动更新：
   - `~/global.json -> images.runtime`
   - `~/global.json -> images.runtime_repository`
   - `~/global.json -> images.runtime_tag`

### 5. 验证 global.json 已切到新镜像

```bash
python3 - <<'PY'
import json
from pathlib import Path
p = Path('/home/w3c_offical/global.json')
data = json.loads(p.read_text())
print(json.dumps(data.get('images', {}), ensure_ascii=False, indent=2))
PY
```

预期类似：

```json
{
  "runtime": "cicybot/cicy-code-runtime:1.0.7",
  "runtime_repository": "cicybot/cicy-code-runtime",
  "runtime_tag": "1.0.7"
}
```

## 部署到试用链路

`cicy-cloud` 的 trial deploy 脚本读的是本机 `~/global.json` 的 `images.runtime`。

所以只要上面的 `dockerBuild` 成功，后续新的 trial runtime 就会默认使用新镜像，不需要额外手改 trial deploy 脚本。

标准验证方式：

```bash
cd /home/w3c_offical/projects/cicy-cloud
BASE_URL=https://cicy-ai.com ./scripts/trial-claim-support-grant-from-global.sh <claim_code>
```

然后去 trial VM 上验证：

```bash
gcloud compute ssh <trial-vm> --project <project> --zone <zone> --command \
  "sudo docker ps --filter label=cicy.trial.runtime=true --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'"
```

预期镜像应为新版本，例如：

```text
cicybot/cicy-code-runtime:1.0.7
```

## 验收标准

必须同时满足：

1. `python3 dev.py --dockerVersion` 显示 `package_version=<new_version>`
2. Docker Hub 已存在 `cicybot/cicy-code-runtime:<new_version>`
3. `~/global.json images.runtime` 已变成新 tag
4. 新开出来的 trial runtime 确实跑的是新镜像
5. workspace 可正常打开，`/api/health` 正常

## 回滚 SOP

如果新镜像有问题，不重新 build，直接把默认 runtime tag 切回旧版本：

```bash
cd /pr/cicy-code-v1
python3 dev.py --dockerSetVersion 1.0.6
```

这会只更新：

- `~/global.json -> images.runtime`
- `~/global.json -> images.runtime_tag`

不会重新构建镜像。

然后重新发一个新的 trial runtime 即可验证是否已回滚到旧镜像。

## 禁忌

不要这样做：

1. 手工同时改多个版本文件，不走 `dev.py --bumpVersion`
2. 只 push 了新镜像，但没确认 `~/global.json` 是否更新
3. 只看本地 build 成功，不看 trial runtime 实际跑的镜像
4. 在两个工作树里混着 bump，同一轮操作必须固定在一个目录完成

## 最短命令模板

```bash
cd /pr/cicy-code-v1
python3 dev.py --dockerVersion
python3 dev.py --bumpVersion 1.0.7
python3 dev.py --dockerBuild --dockerBuildVersion 1.0.7
```
