// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import TokenAccountPanel, { type AccountField } from "./TokenAccountPanel";

// The Aliyun cloud mark, reduced to one path.
export function AliyunIcon({ className = "" }: { className?: string }) {
  return (
    <svg data-id="aliyun-icon" viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true">
      <path d="M6.2 4h4.3v2.2H6.6c-1.5 0-2.4.9-2.4 2.4v6.8c0 1.5.9 2.4 2.4 2.4h3.9V20H6.2C3.4 20 2 18.4 2 15.6V8.4C2 5.6 3.4 4 6.2 4zm11.6 0C20.6 4 22 5.6 22 8.4v7.2c0 2.8-1.4 4.4-4.2 4.4h-4.3v-2.2h3.9c1.5 0 2.4-.9 2.4-2.4V8.6c0-1.5-.9-2.4-2.4-2.4h-3.9V4h4.3zM8.4 10.9h7.2v2.2H8.4v-2.2z" />
    </svg>
  );
}

type AliyunAccount = {
  name: string;
  access_key_id: string;
  secret_set: boolean;
  secret_tail?: string;
  region: string;
  account: string;
  email: string;
  "2fa_set": boolean;
  profile: string;
  password_set: boolean;
};

export default function AliyunAccountsPanel({ active, paneId }: { active: boolean; paneId: string }) {
  const { t } = useTranslation("workspace");
  const fields: AccountField[] = [
    { key: "name", label: t("githubAccountName", { defaultValue: "账号名称" }), placeholder: "cicy-prod", half: true },
    { key: "account", label: t("aliyunAccountLogin", { defaultValue: "主账号 / RAM 用户" }), placeholder: "cicy@aliyun.com", half: true },
    { key: "access_key_id", id: "ak", label: "AccessKey ID", placeholder: "LTAI5t…", requiredOnCreate: true },
    { key: "access_key_secret", id: "secret", label: "AccessKey Secret", kind: "secret", keepOnEdit: true, requiredOnCreate: true },
    { key: "region", label: t("aliyunAccountRegion", { defaultValue: "默认 Region" }), placeholder: "cn-hangzhou", half: true },
    { key: "email", label: "Email", placeholder: "name@example.com", half: true },
    { key: "2fa", label: "2FA", kind: "secret", keepOnEdit: true, half: true },
    { key: "password", label: "Password", kind: "secret", keepOnEdit: true, half: true },
  ];
  return (
    <TokenAccountPanel<AliyunAccount>
      active={active}
      paneId={paneId}
      dataId="aliyun"
      title={t("aliyunAccountsTitle", { defaultValue: "阿里云账号" })}
      subtitle={t("aliyunAccountsSubtitle", { defaultValue: "管理本机 aliyun.json 中供 Agent 使用的 AccessKey。Secret 不会在页面中回显。" })}
      security={t("aliyunAccountsSecurity", { defaultValue: "AccessKey Secret 仅保存在本机 ~/cicy-ai/db/aliyun.json，接口只返回是否已配置和末四位。" })}
      emptyLabel={t("aliyunAccountsEmpty", { defaultValue: "还没有阿里云账号" })}
      editTitle={(name) => t("aliyunAccountEditTitle", { name, defaultValue: `编辑阿里云账号 ${name}` })}
      icon={<AliyunIcon className="h-4 w-4 text-orange-400" />}
      accent={{ button: "bg-[#ff6a00] text-white hover:bg-[#ff8124]", tile: "bg-orange-500/10 text-orange-400", focus: "focus:border-orange-500/40" }}
      fields={fields}
      api={{
        list: apiService.getAliyunAccounts,
        reveal: apiService.getAliyunAccountSecret,
        save: apiService.saveAliyunAccount as any,
        remove: apiService.deleteAliyunAccount,
        usage: apiService.getAliyunAccountUsage,
        totp: apiService.getAliyunAccountTOTP,
        bind: apiService.bindAliyunAccount,
      }}
      inspect={{
        label: t("aliyunAccountInspect", { defaultValue: "从 AccessKey 自动获取" }),
        ready: (form) => Boolean((form.access_key_id || "").trim() && (form.access_key_secret || "").trim()),
        run: async (form) => {
          const response = await apiService.inspectAliyunAccount({
            access_key_id: (form.access_key_id || "").trim(),
            access_key_secret: (form.access_key_secret || "").trim(),
            region: (form.region || "").trim(),
          });
          const data = response.data || {};
          const filled: Record<string, string> = {};
          if (data.user_name && !(form.account || "").trim()) filled.account = data.display_name || data.user_name;
          if (data.email) filled.email = data.email;
          if (data.region) filled.region = data.region;
          // A RAM user's name is the most recognisable key for the matrix.
          if (!(form.name || "").trim() && data.user_name) filled.name = data.user_name;
          const parts = [data.identity_type, data.user_name || data.arn, data.account_id ? t("aliyunUsageAccountId", { id: data.account_id, defaultValue: `账号 ${data.account_id}` }) : ""];
          if (data.balance) parts.push(t("aliyunUsageBalance", { balance: data.balance, currency: data.currency || "CNY", defaultValue: `余额 ${data.balance} ${data.currency || "CNY"}` }));
          if (data.notes?.length) parts.push(t("aliyunInspectPartial", { defaultValue: "部分信息该 AccessKey 无权读取" }));
          return { fields: filled, summary: parts.filter(Boolean).join(" · ") };
        },
      }}
      bindLabel={t("aliyunAccountBind", { defaultValue: "设为本机 aliyun CLI 账号" })}
      bindToast={(name) => t("aliyunAccountBound", { name, defaultValue: `已把 ${name} 写入 ~/.aliyun/config.json` })}
      agentPrompt={(name) =>
        t("aliyunAccountAgentPrompt", {
          name,
          defaultValue:
            "本机阿里云 CLI 账号已切换为「{{name}}」（AccessKey 已写入 ~/.aliyun/config.json）。先执行 aliyun sts GetCallerIdentity 确认身份，然后等待我的下一步指令。不要读取、输出或泄露 AccessKey Secret。",
        })
      }
      renderBadge={(account) => <span data-id="aliyun-account-region" className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-zinc-400">{account.region}</span>}
      renderMeta={(account) => (
        <>
          {account.access_key_id || "—"} · {account.secret_set ? `Secret ••••${account.secret_tail || ""}` : t("aliyunAccountSecretMissing", { defaultValue: "未设置 Secret" })}
        </>
      )}
      renderUsage={(usage) => (
        <>
          {usage.account_id && <span>{t("aliyunUsageAccountId", { id: usage.account_id, defaultValue: `账号 ${usage.account_id}` })}</span>}
          {usage.identity_type && <span>{usage.identity_type}</span>}
          {usage.balance_available && <span>{t("aliyunUsageBalance", { balance: usage.balance, currency: usage.currency || "CNY", defaultValue: `余额 ${usage.balance} ${usage.currency || "CNY"}` })}</span>}
          {!usage.balance_available && usage.balance_error && <span className="text-zinc-600" title={usage.balance_error}>{t("aliyunUsageBalanceUnavailable", { defaultValue: "无账单权限" })}</span>}
        </>
      )}
    />
  );
}
