# 发版流程

## 自动发版（推荐）

当 `npm/package.json` 中的 `version` 字段变更并推送到 `master` 分支时，GitHub Actions 自动执行：

1. 读取 `npm/package.json` 中的版本号
2. 创建 GitHub Release（tag: `v{version}`），上传 `dist/` 下的 4 个二进制
3. 发布 npm 包

### 前置配置

在仓库 **Settings → Secrets and variables → Actions** 中添加：

| Secret | 说明 |
|--------|------|
| `NPM_TOKEN` | npm access token（`npm token create`） |

> `GITHUB_TOKEN` 由 Actions 自动提供，无需手动配置。

### 发版步骤

```bash
# 1. 确保 dist/ 下的二进制已更新（go build 交叉编译）
GOOS=darwin GOARCH=arm64 go build -o dist/cicy-code-darwin-arm64 ./mgr/
GOOS=darwin GOARCH=amd64 go build -o dist/cicy-code-darwin-amd64 ./mgr/
GOOS=linux  GOARCH=amd64 go build -o dist/cicy-code-linux-amd64  ./mgr/
GOOS=linux  GOARCH=arm64 go build -o dist/cicy-code-linux-arm64  ./mgr/

# 2. 更新版本号
cd npm
# 编辑 package.json 中的 version 字段

# 3. 提交并推送
git add -A
git commit -m "chore: release v0.x.x"
git push

# GitHub Actions 自动完成 release + npm publish
```

## 手动发版

如果 Actions 未触发或需要手动补发：

```bash
# 1. 创建 GitHub Release 并上传二进制
cd /path/to/cicy-code
gh release create v0.2.2 \
  dist/cicy-code-darwin-amd64 \
  dist/cicy-code-darwin-arm64 \
  dist/cicy-code-linux-amd64 \
  dist/cicy-code-linux-arm64 \
  --title "v0.2.2" \
  --generate-notes

# 2. 发布 npm
cd npm
npm publish
```

## 版本号规则

- `0.x.y` — 开发阶段
- `x` — 有破坏性变更或重大功能
- `y` — Bug 修复或小功能

## 用户更新

```bash
# 自动检测更新
npx cicy-code

# 强制指定版本
npx cicy-code@0.2.2

# 国内镜像
CN_MIRROR=1 npx cicy-code
```
