# CiCy Code Runtime Bump SOP

这份文档保留为兼容入口；当前详细流程以：

- `docs/sop/v1/bump-docker-version-and-push-dockerhub.md`

为准。

## 当前仓库前提

- 仓库目录建议：`~/projects/cicy-code`
- 全局状态目录：`~/cicy-ai`
- 版本同步入口：`python3 dev.py --bumpVersion <version>`
- runtime push 入口：`python3 dev.py --dockerBuild --dockerBuildVersion <version>`

## 这条链路当前会改哪些地方

`python3 dev.py --bumpVersion <version>` 当前会同步：

- `npm/package.json`
- `app/package.json`
- `app/package-lock.json`
- `app/src/config.ts`
- `api/mgr/main.go`
- `.cicy_tmux.conf`

`python3 dev.py --dockerBuild --dockerBuildVersion <version>` 当前还会：

- 构建 CDN 资产
- 上传 `app` 与 `ttyd` 资产到 COS
- 构建 runtime Docker 镜像
- push Docker Hub tag
- 更新 `~/cicy-ai/global.json -> images.runtime*`

## 最短命令

```bash
cd ~/projects/cicy-code
export DOCKER_IMAGE_REPOSITORY=<your-dockerhub-repo>
python3 dev.py --dockerVersion
python3 dev.py --bumpVersion <version>
python3 dev.py --dockerBuild --dockerBuildVersion <version>
python3 dev.py --dockerVersion
```

## 注意

- `./build.sh docker <tag>` 只做本地镜像构建，不会 push，也不会更新 `global.json`
- `python3 dev.py --dockerBuild` 依赖 `~/cicy-ai/global.json` 里的腾讯 COS 配置
- 不要再使用旧路径名 `cicy-code-v1` 或旧状态目录 `/cicy/global.json`
