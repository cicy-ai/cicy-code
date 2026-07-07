---
title: 快速开始
description: 安装 cicy-code(npx / npm),打开工作区,建 agent、派活、fork。
---
# 快速开始

npm 单二进制分发,一行命令起一支团队。

## 安装

```bash
# 临时跑一次
npx cicy-code

# 或全局安装(国内自动落 npmmirror)
npm i -g cicy-code
npm i -g cicy-code --registry=https://registry.npmmirror.com
```

npm 按 `os` / `cpu` 只装匹配当前平台的二进制(~30 MB),不走 GitHub。

## 打开工作区

启动后浏览器打开 `http://127.0.0.1:8008`。默认端口:

| 服务 | 端口 |
| --- | --- |
| API / 工作区 | `8008` |
| Vite(dev) | `8022` |

## 建 agent、派活、fork

- 为每个项目建项目模板,用「前端 / 后端 / 测试」角色各起一个 agent;
- 跨 agent 协作走 `cicy-agent`,任务清单走 `cicy-todo`;
- `cicy-agent fork` 带完整上下文再开一个,继承角色 + 项目、独立继续。
