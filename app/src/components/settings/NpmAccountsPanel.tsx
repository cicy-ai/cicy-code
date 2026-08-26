// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import TokenAccountPanel, { type AccountField } from "./TokenAccountPanel";

// The npm wordmark: a red tile with the three-stroke "npm" glyph knocked out.
export function NpmIcon({ className = "" }: { className?: string }) {
  return (
    <svg data-id="npm-icon" viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true">
      <path d="M2 6h20v11h-10v2H7v-2H2V6zm2 9h3V9h2v6h2V8H4v7zm8-7v9h2v-2h4V8h-6zm2 2h2v3h-2v-3zm5-2v7h2V8h-2z" />
    </svg>
  );
}

type NpmAccount = {
  name: string;
  email: string;
  token_set: boolean;
  token_tail?: string;
  "2fa_set": boolean;
  profile: string;
  password_set: boolean;
  registry: string;
  scope: string;
};

const formatDownloads = (value: number) =>
  value >= 1000000 ? `${(value / 1000000).toFixed(1)}M` : value >= 1000 ? `${(value / 1000).toFixed(1)}k` : String(value ?? 0);

export default function NpmAccountsPanel({ active, paneId }: { active: boolean; paneId: string }) {
  const { t } = useTranslation("workspace");
  const fields: AccountField[] = [
    { key: "name", label: t("githubAccountName", { defaultValue: "账号名称" }), placeholder: "cicy-ai", half: true },
    { key: "email", label: "Email", placeholder: "name@example.com", half: true },
    { key: "api_token", id: "token", label: t("npmAccountToken", { defaultValue: "Access Token（Automation / Granular）" }), kind: "secret", placeholder: "npm_…", keepOnEdit: true, requiredOnCreate: true },
    { key: "registry", label: t("npmAccountRegistry", { defaultValue: "Registry" }), placeholder: "https://registry.npmjs.org", half: true },
    { key: "scope", label: t("npmAccountScope", { defaultValue: "Scope（可选）" }), placeholder: "@cicy", half: true },
    { key: "2fa", label: "2FA", kind: "secret", keepOnEdit: true, half: true },
    { key: "password", label: "Password", kind: "secret", keepOnEdit: true, half: true },
  ];
  return (
    <TokenAccountPanel<NpmAccount>
      active={active}
      paneId={paneId}
      dataId="npm"
      title={t("npmAccountsTitle", { defaultValue: "npm 账号" })}
      subtitle={t("npmAccountsSubtitle", { defaultValue: "管理本机 npm.json 中供 Agent 使用的多个 npm 账号。Token 不会在页面中回显。" })}
      security={t("npmAccountsSecurity", { defaultValue: "Token 仅保存在本机 ~/cicy-ai/db/npm.json，接口只返回是否已配置和末四位。" })}
      emptyLabel={t("npmAccountsEmpty", { defaultValue: "还没有 npm 账号" })}
      editTitle={(name) => t("npmAccountEditTitle", { name, defaultValue: `编辑 npm 账号 ${name}` })}
      icon={<NpmIcon className="h-4 w-4 text-rose-400" />}
      accent={{ button: "bg-rose-500 text-white hover:bg-rose-400", tile: "bg-rose-500/10 text-rose-400", focus: "focus:border-rose-500/40" }}
      fields={fields}
      api={{
        list: apiService.getNpmAccounts,
        reveal: apiService.getNpmAccountToken,
        save: apiService.saveNpmAccount as any,
        remove: apiService.deleteNpmAccount,
        usage: apiService.getNpmAccountUsage,
        totp: apiService.getNpmAccountTOTP,
        bind: apiService.bindNpmAccount,
      }}
      inspect={{
        label: t("npmAccountInspect", { defaultValue: "从 Token 自动获取" }),
        ready: (form) => Boolean((form.api_token || "").trim()),
        run: async (form) => {
          const response = await apiService.inspectNpmAccount({ api_token: (form.api_token || "").trim(), registry: (form.registry || "").trim() });
          const data = response.data || {};
          // Only fill what the user has not typed; the account name follows the
          // npm username so the matrix key matches the real identity.
          const filled: Record<string, string> = {};
          if (data.username) filled.name = data.username;
          if (data.email) filled.email = data.email;
          if (data.registry) filled.registry = data.registry;
          if (!(form.scope || "").trim() && data.scopes?.length === 1) filled.scope = data.scopes[0];
          const parts = [data.username];
          if (data.tfa_mode) parts.push(`2FA ${data.tfa_mode}`);
          parts.push(t("npmUsagePackages", { packages: data.packages || 0, defaultValue: `${data.packages || 0} 个包` }));
          if (data.private_packages) parts.push(t("npmInspectPrivate", { privates: data.private_packages, defaultValue: `${data.private_packages} 个私有` }));
          if (data.scopes?.length) parts.push(data.scopes.join(" "));
          if (data.token_automation) parts.push("automation");
          if (data.token_readonly) parts.push("read-only");
          if (data.notes?.length) parts.push(t("npmInspectPartial", { defaultValue: "部分信息该 Token 无权读取" }));
          return { fields: filled, summary: parts.filter(Boolean).join(" · ") };
        },
      }}
      bindLabel={t("npmAccountBind", { defaultValue: "设为本机 npm 账号" })}
      bindToast={(name, data) => t("npmAccountBound", { name, registry: data?.registry || "", defaultValue: `已把 ${name} 写入 ~/.npmrc` })}
      agentPrompt={(name) =>
        t("npmAccountAgentPrompt", {
          name,
          defaultValue:
            "本机 npm 账号已切换为「{{name}}」（Token 已写入 ~/.npmrc）。先执行 npm whoami 确认身份，然后等待我的下一步指令。不要读取、输出或泄露 ~/.npmrc 与 ~/cicy-ai/db/npm.json 中的 Token。",
        })
      }
      renderBadge={(account) => (account.scope ? <span data-id="npm-account-scope" className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-zinc-400">{account.scope}</span> : null)}
      renderMeta={(account) => (
        <>
          {account.email || "—"} · {account.token_set ? `Token ••••${account.token_tail || ""}` : t("githubAccountTokenMissing", { defaultValue: "未设置 Token" })}
        </>
      )}
      renderUsage={(usage, account) => (
        <>
          <span>{usage.username || account.name}</span>
          <span>{t("npmUsagePackages", { packages: usage.packages, defaultValue: `${usage.packages} 个包` })}</span>
          <span>
            {t("npmUsageDownloads", { downloads: formatDownloads(usage.downloads), defaultValue: `月下载 ${formatDownloads(usage.downloads)}` })}
            {usage.downloads_partial ? "+" : ""}
          </span>
          {usage.last_publish && <span>{t("npmUsageLastPublish", { date: String(usage.last_publish).slice(0, 10), defaultValue: `最近发布 ${String(usage.last_publish).slice(0, 10)}` })}</span>}
        </>
      )}
    />
  );
}
