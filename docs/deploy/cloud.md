---
title: 云端 (cicy-cloud)
description: cicy-code 与 cicy-cloud(多租户网关 + IAM + 计费 + 运营后台)的关系。
---
# 云端 (cicy-cloud)

`cicy-cloud` 是 cicy-code 的**云端部署分支**:在工作区服务器之上加了一层**多租户 AI 网关 + 身份体系(IAM)+ 计费 + 运营后台**,跑在 `cicy-ai.com` / `gateway.cicy-ai.com`。

- **AI 网关**:把 cicy-code 当成一个 provider,统一代理 openai / anthropic / gemini 协议,按团队 key 鉴权、按 agent 记账;
- **身份**:邮箱魔法链接登录、团队网关 key(`sk-cicy-`)、SSO;
- **计费**:预付钱包 + 流水 + 成本×倍率 → 对外价;
- **运营后台**:总览 / 用量 / 用户 / 设备 / 团队 / 计费 / 审计。

`cicy-ai.com` 的落地页与控制台由 cicy-cloud 托管;本文档站(cicy-code)独立托管在 `docs.cicy-ai.com`。
