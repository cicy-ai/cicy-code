// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import TokenAccountPanel, { type AccountField } from "./TokenAccountPanel";

// Docker's whale, simplified to a single-colour glyph so it reads at 16px.
export function DockerIcon({ className = "" }: { className?: string }) {
  return (
    <svg data-id="docker-icon" viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true">
      <path d="M4 10h2.4v2.3H4V10zm3.1 0h2.4v2.3H7.1V10zm0-2.9h2.4v2.3H7.1V7.1zm3.1 2.9h2.4v2.3h-2.4V10zm0-2.9h2.4v2.3h-2.4V7.1zm0-2.9h2.4v2.3h-2.4V4.2zM13.3 10h2.4v2.3h-2.4V10zm7.5.4c-.5-.4-1.7-.5-2.6-.3-.1-.9-.6-1.6-1.4-2.3l-.5-.4-.4.5c-.5.7-.7 1.9-.2 2.8-.3.2-.8.4-1.5.4H2.4c-.3 1.7.1 3.9 1.5 5.4 1.3 1.4 3.2 2.1 5.7 2.1 5.4 0 9.4-2.5 11.2-7 .8 0 2.4 0 3.2-1.6l.2-.4-.4-.3c-.4-.3-1.6-.6-3-.9z" />
    </svg>
  );
}

type DockerAccount = {
  name: string;
  username: string;
  email: string;
  token_set: boolean;
  token_tail?: string;
  "2fa_set": boolean;
  profile: string;
  password_set: boolean;
  registry: string;
};

const formatPulls = (value: number) =>
  value >= 1000000 ? `${(value / 1000000).toFixed(1)}M` : value >= 1000 ? `${(value / 1000).toFixed(1)}k` : String(value ?? 0);

export default function DockerAccountsPanel({ active, paneId }: { active: boolean; paneId: string }) {
  const { t } = useTranslation("workspace");
  const fields: AccountField[] = [
    { key: "name", label: t("githubAccountName", { defaultValue: "账号名称" }), placeholder: "cicy-ai", half: true },
    { key: "username", label: t("dockerAccountUsername", { defaultValue: "登录名（留空用账号名称）" }), placeholder: "cicy-ai", half: true },
    { key: "api_token", id: "token", label: t("dockerAccountToken", { defaultValue: "Access Token（PAT）" }), kind: "secret", placeholder: "dckr_pat_…", keepOnEdit: true, requiredOnCreate: true },
    { key: "registry", label: t("dockerAccountRegistry", { defaultValue: "Registry" }), placeholder: "docker.io", half: true },
    { key: "email", label: "Email", placeholder: "name@example.com", half: true },
    { key: "2fa", label: "2FA", kind: "secret", keepOnEdit: true, half: true },
    { key: "password", label: "Password", kind: "secret", keepOnEdit: true, half: true },
  ];
  return (
    <TokenAccountPanel<DockerAccount>
      active={active}
      paneId={paneId}
      dataId="docker"
      title={t("dockerAccountsTitle", { defaultValue: "Docker 账号" })}
      subtitle={t("dockerAccountsSubtitle", { defaultValue: "管理本机 docker.json 中供 Agent 使用的镜像仓库账号。Token 不会在页面中回显。" })}
      security={t("dockerAccountsSecurity", { defaultValue: "Token 仅保存在本机 ~/cicy-ai/db/docker.json，接口只返回是否已配置和末四位。" })}
      emptyLabel={t("dockerAccountsEmpty", { defaultValue: "还没有 Docker 账号" })}
      editTitle={(name) => t("dockerAccountEditTitle", { name, defaultValue: `编辑 Docker 账号 ${name}` })}
      icon={<DockerIcon className="h-4 w-4 text-sky-400" />}
      accent={{ button: "bg-sky-500 text-white hover:bg-sky-400", tile: "bg-sky-500/10 text-sky-400", focus: "focus:border-sky-500/40" }}
      fields={fields}
      api={{
        list: apiService.getDockerAccounts,
        reveal: apiService.getDockerAccountToken,
        save: apiService.saveDockerAccount as any,
        remove: apiService.deleteDockerAccount,
        usage: apiService.getDockerAccountUsage,
        totp: apiService.getDockerAccountTOTP,
        bind: apiService.bindDockerAccount,
      }}
      inspect={{
        label: t("npmAccountInspect", { defaultValue: "从 Token 自动获取" }),
        // Docker Hub exchanges the PAT for a JWT, and that exchange needs the
        // login name — so both halves must be present before probing.
        ready: (form) => Boolean((form.api_token || "").trim() && ((form.username || "").trim() || (form.name || "").trim())),
        run: async (form) => {
          const response = await apiService.inspectDockerAccount({
            username: (form.username || "").trim() || (form.name || "").trim(),
            api_token: (form.api_token || "").trim(),
            registry: (form.registry || "").trim(),
          });
          const data = response.data || {};
          const filled: Record<string, string> = {};
          if (data.username) filled.username = data.username;
          if (!(form.name || "").trim() && data.username) filled.name = data.username;
          if (data.email) filled.email = data.email;
          if (data.registry) filled.registry = data.registry;
          const parts = [data.username, data.full_name];
          if (data.orgs?.length) parts.push(data.orgs.join(" "));
          if (data.repositories) parts.push(t("dockerUsageRepos", { repos: data.repositories, defaultValue: `${data.repositories} 个仓库` }));
          if (data.pull_limit > 0) parts.push(t("dockerUsageRateLimit", { remain: data.pull_remain, limit: data.pull_limit, defaultValue: `限额 ${data.pull_remain}/${data.pull_limit}` }));
          if (data.notes?.length) parts.push(t("npmInspectPartial", { defaultValue: "部分信息该 Token 无权读取" }));
          return { fields: filled, summary: parts.filter(Boolean).join(" · ") };
        },
      }}
      bindLabel={t("dockerAccountBind", { defaultValue: "设为本机 Docker 账号" })}
      bindToast={(name, data) => t("dockerAccountBound", { name, registry: data?.registry || "", defaultValue: `已把 ${name} 写入 ~/.docker/config.json` })}
      agentPrompt={(name) =>
        t("dockerAccountAgentPrompt", {
          name,
          defaultValue:
            "本机 Docker 账号已切换为「{{name}}」（凭据已写入 ~/.docker/config.json）。先执行 docker system info | grep Username 确认身份，然后等待我的下一步指令。不要读取、输出或泄露 ~/.docker/config.json 与 ~/cicy-ai/db/docker.json 中的凭据。",
        })
      }
      renderBadge={(account) => (
        account.registry && account.registry !== "docker.io" ? <span data-id="docker-account-registry" className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-zinc-400">{account.registry}</span> : null
      )}
      renderMeta={(account) => (
        <>
          {account.username || account.name} · {account.token_set ? `Token ••••${account.token_tail || ""}` : t("githubAccountTokenMissing", { defaultValue: "未设置 Token" })}
        </>
      )}
      renderUsage={(usage) => (
        <>
          <span>{usage.username}</span>
          {!usage.registry_only && <span>{t("dockerUsageRepos", { repos: usage.repositories, defaultValue: `${usage.repositories} 个仓库` })}</span>}
          {!usage.registry_only && <span>{t("dockerUsagePulls", { pulls: formatPulls(usage.pulls), defaultValue: `拉取 ${formatPulls(usage.pulls)}` })}</span>}
          {usage.pull_limit > 0 && <span>{t("dockerUsageRateLimit", { remain: usage.pull_remain, limit: usage.pull_limit, defaultValue: `限额 ${usage.pull_remain}/${usage.pull_limit}` })}</span>}
        </>
      )}
    />
  );
}
