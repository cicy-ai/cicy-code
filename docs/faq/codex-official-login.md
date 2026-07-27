# 如何使用 Codex 官方登录

适用于使用 ChatGPT Plus、Pro、Business、Edu 或 Enterprise 账户登录 Codex 的用户。

## 1. 创建员工

在团队面板顶部点击 **+**，打开“创建并绑定新员工”。

![点击团队面板中的创建员工按钮](/faq/codex-official-login/01-create-agent.png)

## 2. 选择 Codex，并关闭本地网关

智能体类型选择 **Codex**，关闭 **使用本地网关**。其他名称、记忆和角色选项可以按需要设置，最后点击 **创建并绑定**。

![选择 Codex 并关闭使用本地网关](/faq/codex-official-login/02-disable-local-gateway.png)

## 3. 选择使用 ChatGPT 登录

Codex 显示登录方式后，输入 `1`，选择 **Sign in with ChatGPT**。

![选择 Sign in with ChatGPT](/faq/codex-official-login/03-sign-in-chatgpt.png)

## 4. 在浏览器完成登录

复制 Codex 终端显示的 OpenAI OAuth URL，在浏览器中打开并完成 ChatGPT 账户授权。

![复制 OpenAI OAuth URL](/faq/codex-official-login/04-copy-oauth-url.png)

## 5. macOS 完成登录

在 macOS 上，浏览器授权后通常会自动访问本机回调地址，Codex 随即登录成功。

## 6. Windows / WSL 完成回调

Windows 上的 cicy-code 运行在 WSL 环境中。浏览器授权后，Windows 浏览器可能无法直接访问 WSL 内监听的 `localhost:1455` 回调服务。

这时复制浏览器地址栏中完整的回调 URL，打开该员工下方的 **Shell**，执行：

```bash
curl '完整回调 URL'
```

回调 URL 包含一次性的授权参数，必须完整复制。`curl` 成功请求回调地址后，返回 Codex 终端等待登录完成。

![在 Windows WSL 的 Shell 中 curl 回调地址](/faq/codex-official-login/05-windows-wsl-curl.png)
