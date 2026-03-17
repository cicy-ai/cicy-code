# CiCy 部署说明

## 项目结构

```
cicy-code/
├── api/          # Go 后端 (ttyd-manager)
├── app/          # React 前端 (IDE)
├── landing/      # 落地页 (CF Worker + Static Assets)
│   ├── app-proxy.js      # Worker 代码 (geo 路由 + HTMLRewriter)
│   ├── wrangler.toml     # Worker 配置
│   ├── cos-upload.py     # 上传 assets 到腾讯云 COS
│   ├── deploy.sh         # 一键部署
│   └── public/           # 静态文件
│       ├── index.html
│       ├── assets/       # Vite 构建产物
│       ├── favicon.svg
│       ├── logo.svg/png
│       ├── robots.txt
│       └── sitemap.xml
├── docker-compose.yml
├── setup.sh
└── ...
```

## 落地页部署

```bash
cd ~/projects/cicy-code/landing
bash deploy.sh
```

做两件事：
1. 上传 assets 到腾讯云 COS（国内 CDN）
2. 部署 Worker + 静态文件到 CF（全球）

### 架构

```
cicy-ai.com / www / app  →  CF Worker
  ├── HTML/图片/favicon  →  Worker Static Assets (CF 全球 CDN)
  ├── /assets/* (国内)   →  HTMLRewriter 改写 URL → 腾讯云 COS 上海
  ├── /assets/* (海外)   →  Worker Static Assets (CF 全球 CDN)
  └── /api/* /ws/*       →  Go API (Cloud Run / VM，仅 app.cicy-ai.com)
```

### 版本管理

- Vite 构建产物自带 hash（`index-BXuJpFW2.js`）
- COS 路径 `/v{VER}/assets/`
- `VER` 在 `app-proxy.js` 和 `cos-upload.py` 中同步

### 配置依赖

`~/global.json`:
```json
{
  "cf": { "prod": { "account_id": "...", "api_token": "..." } },
  "tencent": { "secret_id": "...", "secret_key": "...", "bucket": "cicy-1372193042", "region": "ap-shanghai" }
}
```

## API 部署

见 `setup.sh` / `setup-prod.sh`

## App 前端部署

```bash
cd ~/projects/cicy-code/ide
npm run build
# 产物在 dist/，通过 Nginx 或 Worker 托管
```
