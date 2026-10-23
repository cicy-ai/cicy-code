// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import TokenAccountPanel, { type AccountField } from "./TokenAccountPanel";

// A compact AWS mark: the "smile" arrow under a cloud-free wordmark slot.
export function AwsIcon({ className = "" }: { className?: string }) {
  return (
    <svg data-id="aws-icon" viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true">
      <path d="M7.1 10.3c0 .3 0 .6.1.8l.2.6c0 .1.1.2.1.2 0 .1-.1.2-.2.3l-.5.3h-.2c-.1 0-.2 0-.3-.1l-.3-.4-.3-.5c-.7.8-1.6 1.2-2.7 1.2-.7 0-1.3-.2-1.8-.6-.4-.4-.6-1-.6-1.6 0-.7.2-1.3.8-1.7.5-.4 1.2-.6 2.1-.6h.9l1 .1v-.6c0-.6-.1-1-.4-1.3-.2-.2-.7-.4-1.3-.4l-.9.1-.9.3-.3.1h-.1c-.1 0-.2-.1-.2-.3v-.4l.1-.3.3-.2 1-.3 1.2-.2c.9 0 1.6.2 2 .7.4.4.6 1.1.6 2v2.8zM3.5 11.6l.8-.1c.3-.1.6-.3.8-.5l.3-.5.1-.7v-.3l-.8-.1h-.8c-.6 0-1 .1-1.2.3-.3.2-.4.5-.4.9s.1.6.3.8c.2.1.5.2.9.2zm7.3 1c-.2 0-.3 0-.3-.1l-.2-.4-2.1-7v-.4c0-.1.1-.2.2-.2h.9c.2 0 .3 0 .3.1l.2.4 1.5 6 1.4-6 .1-.4.4-.1h.7c.2 0 .3 0 .4.1l.2.4 1.4 6.1 1.6-6.1.2-.4.3-.1h.8c.2 0 .2.1.2.2v.4l-2.2 7-.2.4-.3.1h-.8c-.2 0-.3 0-.3-.1l-.2-.4-1.4-5.8-1.4 5.8-.2.4-.3.1h-.7zm11.6.2c-.5 0-.9-.1-1.4-.2l-1-.4-.2-.2v-.6c0-.2.1-.2.2-.2h.2l.3.1.9.3 1 .1c.5 0 .9-.1 1.2-.3.3-.2.4-.4.4-.8 0-.2-.1-.4-.2-.6l-.8-.4-1.1-.3c-.6-.2-1-.5-1.3-.8-.3-.4-.4-.8-.4-1.2 0-.4.1-.7.2-.9l.6-.7.8-.4 1-.2.6.1.6.1.5.1.4.2.2.2.1.3v.4c0 .2-.1.3-.2.3l-.4-.1c-.4-.2-.9-.3-1.4-.3-.4 0-.8.1-1.1.2-.2.2-.4.4-.4.7 0 .2.1.4.3.6l.9.5 1.1.3c.5.2.9.4 1.2.8.3.3.4.7.4 1.2 0 .4-.1.7-.2 1l-.6.7-.8.5-1.3.1zM21.6 16.9c-2.6 1.9-6.4 2.9-9.6 2.9-4.5 0-8.6-1.7-11.7-4.4-.2-.2 0-.5.3-.3 3.3 1.9 7.4 3.1 11.7 3.1 2.9 0 6-.6 8.9-1.8.4-.2.8.3.4.5zm1.1-1.2c-.3-.4-2.2-.2-3-.1-.3 0-.3-.2-.1-.4 1.5-1 3.9-.7 4.2-.4.3.3-.1 2.7-1.5 3.9-.2.2-.4.1-.3-.1.3-.8 1-2.5.7-2.9z" />
    </svg>
  );
}

type AwsAccount = {
  name: string;
  access_key_id: string;
  secret_set: boolean;
  secret_tail?: string;
  region: string;
  note: string;
};

export default function AwsAccountsPanel({ active, paneId }: { active: boolean; paneId: string }) {
  const { t } = useTranslation("workspace");
  const fields: AccountField[] = [
    { key: "name", label: t("githubAccountName", { defaultValue: "账号名称" }), placeholder: "aws-prod", half: true },
    { key: "region", label: t("awsAccountRegion", { defaultValue: "默认 Region" }), placeholder: "us-east-1", half: true },
    { key: "access_key_id", id: "ak", label: "Access Key ID", placeholder: "AKIA…", requiredOnCreate: true },
    { key: "secret_access_key", id: "secret", label: "Secret Access Key", kind: "secret", keepOnEdit: true, requiredOnCreate: true },
    { key: "note", label: t("awsAccountNote", { defaultValue: "备注" }), placeholder: t("awsAccountNotePh", { defaultValue: "例如：生产环境 / 个人测试 / 只读 S3" }) },
  ];
  return (
    <TokenAccountPanel<AwsAccount>
      active={active}
      paneId={paneId}
      dataId="aws"
      title={t("awsAccountsTitle", { defaultValue: "AWS 账号" })}
      subtitle={t("awsAccountsSubtitle", { defaultValue: "管理本机 aws.json 中供 Agent 使用的多组 AWS Access Key，每组可写备注。Secret 不会在页面中回显。" })}
      security={t("awsAccountsSecurity", { defaultValue: "Secret Access Key 仅保存在本机 ~/cicy-ai/db/aws.json，接口只返回是否已配置和末四位。" })}
      emptyLabel={t("awsAccountsEmpty", { defaultValue: "还没有 AWS 账号" })}
      editTitle={(name) => t("awsAccountEditTitle", { name, defaultValue: `编辑 AWS 账号 ${name}` })}
      icon={<AwsIcon className="h-4 w-4 text-amber-400" />}
      accent={{ button: "bg-[#ff9900] text-black hover:bg-[#ffad33]", tile: "bg-amber-500/10 text-amber-400", focus: "focus:border-amber-500/40" }}
      fields={fields}
      api={{
        list: apiService.getAwsAccounts,
        reveal: apiService.getAwsAccountSecret,
        save: apiService.saveAwsAccount as any,
        remove: apiService.deleteAwsAccount,
        usage: apiService.testAwsAccount,
        bind: apiService.bindAwsAccount,
      }}
      inspect={{
        label: t("awsAccountInspect", { defaultValue: "从 Access Key 自动获取" }),
        ready: (form) => Boolean((form.access_key_id || "").trim() && (form.secret_access_key || "").trim()),
        run: async (form) => {
          const response = await apiService.inspectAwsAccount({
            access_key_id: (form.access_key_id || "").trim(),
            secret_access_key: (form.secret_access_key || "").trim(),
            region: (form.region || "").trim(),
          });
          const data = response.data || {};
          const filled: Record<string, string> = {};
          if (!(form.name || "").trim() && data.user_name) filled.name = data.user_name;
          if (!(form.region || "").trim() && data.region) filled.region = data.region;
          const summary = [data.arn, data.account_id ? t("awsUsageAccountId", { id: data.account_id, defaultValue: `账号 ${data.account_id}` }) : ""].filter(Boolean).join(" · ");
          return { fields: filled, summary };
        },
      }}
      bindLabel={t("awsAccountBind", { defaultValue: "设为本机 aws CLI 账号" })}
      bindToast={(name) => t("awsAccountBound", { name, defaultValue: `已把 ${name} 写入 ~/.aws/credentials（并设为 default）` })}
      agentPrompt={(name) =>
        t("awsAccountAgentPrompt", {
          name,
          defaultValue:
            "请使用 aws-cli skill 操作 AWS，指定账号为「{{name}}」。先执行 aws-cli whoami --account {{name}} 确认身份，然后等待我的下一步指令。不要读取、输出或泄露 Secret Access Key。",
        })
      }
      renderBadge={(account) => <span data-id="aws-account-region" className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-zinc-400">{account.region}</span>}
      renderMeta={(account) => (
        <>
          {account.access_key_id || "—"} · {account.secret_set ? `Secret ••••${account.secret_tail || ""}` : t("awsAccountSecretMissing", { defaultValue: "未设置 Secret" })}
          {account.note ? <span data-id="aws-account-note" className="text-zinc-400"> · {account.note}</span> : null}
        </>
      )}
      renderUsage={(usage) => (
        <>
          {usage.account_id && <span>{t("awsUsageAccountId", { id: usage.account_id, defaultValue: `账号 ${usage.account_id}` })}</span>}
          {usage.arn && <span className="truncate" title={usage.arn}>{usage.arn}</span>}
        </>
      )}
    />
  );
}
