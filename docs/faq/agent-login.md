# Agent 登录与 API 配置 FAQ

这里整理 Claude Code 与 Codex 的常见登录和第三方 API 配置方法。

<a id="claude-official-login"></a>

## 如何使用 Claude Code 官方登录

适用于已有 Claude Pro、Max、Team 或 Enterprise 订阅，希望直接使用官方 Claude Code 登录的用户。

### 1. 创建员工

在团队面板顶部点击 **+**，打开“创建并绑定新员工”。

![点击团队面板中的创建员工按钮](/faq/claude-official-login/01-create-agent.png)

### 2. 选择 Claude，并关闭本地网关

智能体类型选择 **Claude**，然后关闭 **使用本地网关**。其他名称、记忆和角色选项可以按需要设置，最后点击 **创建并绑定**。

![选择 Claude 并关闭使用本地网关](/faq/claude-official-login/02-disable-local-gateway.png)

### 3. 执行登录命令

员工创建完成后，在 Claude Code 终端输入：

```text
/login
```

![在 Claude Code 中输入 login](/faq/claude-official-login/03-run-login.png)

### 4. 选择订阅账户

输入 `1`，选择 **Claude account with subscription**。

![选择 Claude 订阅账户登录](/faq/claude-official-login/04-select-subscription.png)

### 5. 在浏览器完成认证

复制终端显示的 Claude 授权 URL，在浏览器打开，并使用你的 Claude 订阅账户完成认证。

### 6. 将授权码粘贴回终端

认证成功后，如果浏览器没有自动跳回 Claude Code，请复制页面显示的授权码，粘贴到终端的 `Paste code here if prompted` 输入处并回车。

![复制授权 URL 并粘贴认证代码](/faq/claude-official-login/05-browser-auth-code.png)

完成后即可通过官方 Claude 订阅使用 Claude Code。

<a id="claude-third-party-api"></a>

## 如何使用 Claude Code 第三方中转 API

适用于通过 cicy AI 本地网关使用第三方模型或中转 API 的用户。

### 1. 创建员工

在团队面板顶部点击 **+**，打开“创建并绑定新员工”。

![点击团队面板中的创建员工按钮](/faq/claude-third-party-api/01-create-agent.png)

### 2. 选择 Claude，并开启本地网关

智能体类型选择 **Claude**，开启 **使用本地网关**。其他名称、记忆和角色选项可以按需要设置，最后点击 **创建并绑定**。

![选择 Claude 并开启使用本地网关](/faq/claude-third-party-api/02-enable-local-gateway.png)

### 3. 选择第三方模型

员工创建完成后，点击终端底部的模型选择器。在 **Claude 风格**列表中选择已配置的第三方模型；例如图中的 `deepseek-v4-pro`。

![在 Claude Code 中选择第三方模型](/faq/claude-third-party-api/03-select-model.png)

选择完成后，即可通过本地网关使用该第三方模型。

<a id="codex-official-login"></a>

## 如何使用 Codex 官方登录

本条 FAQ 的图文步骤正在整理。

<a id="codex-third-party-api"></a>

## 如何使用 Codex 第三方中转 API

适用于通过 cicy AI 本地网关使用第三方模型或中转 API 的用户。

### 1. 创建员工

在团队面板顶部点击 **+**，打开“创建并绑定新员工”。

![点击团队面板中的创建员工按钮](/faq/codex-third-party-api/01-create-agent.png)

### 2. 选择 Codex，并开启本地网关

智能体类型选择 **Codex**，开启 **使用本地网关**。其他名称、记忆和角色选项可以按需要设置，最后点击 **创建并绑定**。

![选择 Codex 并开启使用本地网关](/faq/codex-third-party-api/02-enable-local-gateway.png)

### 3. 选择第三方模型

员工创建完成后，打开模型选择器并选择已经配置好的第三方模型。

![在 Codex 中选择第三方模型](/faq/codex-third-party-api/03-select-model.png)

选择完成后，即可通过本地网关使用该第三方模型。
