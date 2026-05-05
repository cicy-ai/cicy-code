# CiCy Code Server Bridge

这是 `cicy-code` 的 code-server / VS Code 扩展。

## 功能

- 资源管理器右键：`发送给当前 agent`
- 编辑器右键：`发送当前文档给 agent`
- 内部命令：`cicy.hostOpenFile`

当前实现位于 `src/extension.ts`，主要行为是：

1. 从当前页面地址推导 API base
2. 把文件路径、workspace folder、可选选区范围打包成 JSON
3. `POST /api/code-server/send-path`
4. 让当前 `cicy-code` 页面把路径转发给当前 agent

`cicy.hostOpenFile` 用于接收宿主页面回推的文件引用，并在 code-server 里打开对应文件；支持普通文件、图片，以及 `path:line[:column]` 形式。

## 构建

```bash
npm install
npm run build
npm run package
```

## 仓库说明

当前仓库只使用这一份扩展目录：

- `api/code-server-extension/`
