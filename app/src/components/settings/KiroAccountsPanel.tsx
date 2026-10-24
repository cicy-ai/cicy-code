// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useState } from "react";
import { Upload } from "lucide-react";
import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import { AppModal } from "../ui/Modal";
import TokenAccountPanel, { type AccountField } from "./TokenAccountPanel";

export function KiroIcon({ className = "" }: { className?: string }) {
  return <img data-id="kiro-icon" src="/assets/logos/kiro.png" alt="" aria-hidden="true" className={`${className} rounded-[3px] object-cover`} />;
}

type KiroAccount = {
  name: string;
  login_type: string;
  email: string;
  password_set: boolean;
  "2fa_set": boolean;
  profile: string;
  note: string;
};

const LOGIN_TYPES = ["github", "builder_id", "google", "identity_center"] as const;

export default function KiroAccountsPanel({ active, paneId }: { active: boolean; paneId: string }) {
  const { t } = useTranslation("workspace");
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState("");
  const [reloadKey, setReloadKey] = useState(0);

  const typeLabel = (value: string) =>
    ({
      github: "GitHub",
      builder_id: "AWS Builder ID",
      google: "Google",
      identity_center: t("kiroLoginIdentityCenter", { defaultValue: "Identity Center（专业版）" }),
    })[value] || value || "—";

  const fields: AccountField[] = [
    { key: "login_type", label: t("kiroAccountLoginType", { defaultValue: "登录方式" }), kind: "select", half: true, options: LOGIN_TYPES.map((value) => ({ value, label: typeLabel(value) })) },
    { key: "email", label: "Email", placeholder: "name@example.com", half: true, requiredOnCreate: true },
    { key: "name", label: t("kiroAccountName", { defaultValue: "账号名称（留空用邮箱）" }), placeholder: "name@example.com" },
    { key: "password", label: "Password", kind: "secret", keepOnEdit: true, half: true },
    { key: "2fa", label: "2FA", kind: "secret", keepOnEdit: true, half: true },
    { key: "note", label: t("awsAccountNote", { defaultValue: "备注" }), placeholder: t("kiroAccountNotePh", { defaultValue: "例如：主号 / 备用 / 额度已用完" }) },
  ];

  const runImport = async () => {
    setImporting(true);
    setImportResult("");
    try {
      const r = await apiService.importKiroAccounts(importText);
      const d = r.data || {};
      const parts = [
        t("kiroImportAdded", { count: (d.added || []).length, defaultValue: `新增 ${(d.added || []).length}` }),
        t("kiroImportUpdated", { count: (d.updated || []).length, defaultValue: `更新 ${(d.updated || []).length}` }),
      ];
      if ((d.skipped_lines || []).length) parts.push(t("kiroImportSkipped", { lines: d.skipped_lines.join(", "), defaultValue: `跳过第 ${d.skipped_lines.join(", ")} 行（格式不对）` }));
      setImportResult(parts.join(" · "));
      setReloadKey((k) => k + 1);
      if (!(d.skipped_lines || []).length) { setImportText(""); setImportOpen(false); window.dispatchEvent(new CustomEvent("show-toast", { detail: parts.join(" · ") })); }
      else setImportText("");
    } catch (e: any) {
      setImportResult(String(e?.response?.data?.detail || e?.message || e));
    } finally {
      setImporting(false);
    }
  };

  return (
    <>
      <TokenAccountPanel<KiroAccount>
        active={active}
        paneId={paneId}
        dataId="kiro"
        title={t("kiroAccountsTitle", { defaultValue: "Kiro 账号" })}
        subtitle={t("kiroAccountsSubtitle", { defaultValue: "管理本机 kiro.json 中的多个 Kiro 登录账号（GitHub / AWS Builder ID / Google）。密码和 2FA 不会在页面中回显。" })}
        security={t("kiroAccountsSecurity", { defaultValue: "密码和 2FA 种子仅保存在本机 ~/cicy-ai/db/kiro.json，接口只返回是否已配置。" })}
        emptyLabel={t("kiroAccountsEmpty", { defaultValue: "还没有 Kiro 账号" })}
        editTitle={(name) => t("kiroAccountEditTitle", { name, defaultValue: `编辑 Kiro 账号 ${name}` })}
        icon={<KiroIcon className="h-4 w-4" />}
        accent={{ button: "bg-violet-500 text-white hover:bg-violet-400", tile: "bg-violet-500/10 text-violet-300", focus: "focus:border-violet-500/40" }}
        fields={fields}
        reloadKey={reloadKey}
        api={{
          list: apiService.getKiroAccounts,
          reveal: apiService.getKiroAccountSecret,
          save: apiService.saveKiroAccount as any,
          remove: apiService.deleteKiroAccount,
          totp: apiService.getKiroAccountTOTP,
          // Every card asks on load; the server answers from a 3-minute cache.
          usage: (name: string) => apiService.getKiroAccountUsage(name),
        }}
        headerActions={
          <button data-id="kiro-account-import" type="button" onClick={() => { setImportResult(""); setImportOpen(true); }} className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-md border border-white/[0.1] px-2.5 py-1.5 text-[11px] text-zinc-300 hover:bg-white/[0.06]">
            <Upload className="h-3.5 w-3.5" />
            {t("kiroAccountImport", { defaultValue: "批量导入" })}
          </button>
        }
        agentPrompt={(name) =>
          t("kiroAccountAgentPrompt", {
            name,
            defaultValue: "需要登录 Kiro 时使用账号「{{name}}」（登录信息在本机 Kiro 账号矩阵里，需要 2FA 时向我要验证码）。不要读取、输出或泄露密码和 2FA 种子。",
          })
        }
        renderUsage={(usage) =>
          usage.logged_in ? (
            <>
              <span data-id="kiro-account-signed-in" className="text-emerald-400">{t("kiroSignedInHere", { defaultValue: "本机登录中" })}</span>
              {usage.plan && <span>{usage.plan}</span>}
              {usage.credits_limit > 0 && (
                <span data-id="kiro-account-credits">
                  {t("kiroCredits", { used: Number(usage.credits_used).toFixed(2).replace(/\.00$/, ""), limit: usage.credits_limit, pct: Math.round(usage.used_pct), defaultValue: `Credits ${usage.credits_used} / ${usage.credits_limit}（${Math.round(usage.used_pct)}%）` })}
                </span>
              )}
              {usage.resets_on && <span>{t("kiroResetsOn", { date: usage.resets_on, defaultValue: `${usage.resets_on} 重置` })}</span>}
              {usage.credits_limit > 0 && (
                <span data-id="kiro-account-credits-progress" className="mt-1 h-1 w-full overflow-hidden rounded-full bg-white/[0.06]">
                  <span className={`block h-full rounded-full ${usage.used_pct >= 90 ? "bg-rose-500" : usage.used_pct >= 70 ? "bg-amber-500" : "bg-emerald-500"}`} style={{ width: `${Math.min(100, usage.used_pct)}%` }} />
                </span>
              )}
            </>
          ) : (
            <span data-id="kiro-account-not-signed-in" className="text-zinc-600">
              {usage.logged_in_email
                ? t("kiroNotSignedIn", { email: usage.logged_in_email, defaultValue: `未在本机登录，无法查询用量（当前登录：${usage.logged_in_email}）` })
                : t("kiroUsageUnavailable", { defaultValue: "本机 kiro-cli 未登录，无法查询用量" })}
            </span>
          )
        }
        renderBadge={(account) => <span data-id="kiro-account-type" className="rounded bg-violet-500/10 px-1.5 py-0.5 text-[10px] text-violet-300">{typeLabel(account.login_type)}</span>}
        renderMeta={(account) => (
          <>
            {account.email || "—"} · {account.password_set ? t("kiroPasswordSet", { defaultValue: "密码已设置" }) : t("kiroPasswordMissing", { defaultValue: "未设置密码" })}
            {account["2fa_set"] ? " · 2FA" : ""}
            {account.note ? <span data-id="kiro-account-note" className="text-zinc-400"> · {account.note}</span> : null}
          </>
        )}
      />
      <AppModal open={importOpen} title={t("kiroAccountImportTitle", { defaultValue: "批量导入 Kiro 账号" })} onClose={() => setImportOpen(false)}>
        <section data-id="kiro-account-import-form">
          <p className="mb-2 text-[11px] leading-5 text-zinc-500">
            {t("kiroAccountImportHint", { defaultValue: "每行一个账号：登录方式:邮箱:密码:2FA（2FA 可省略）。登录方式支持 github、awsBuildId、google。同一邮箱再次导入会更新密码和 2FA，备注保留。" })}
          </p>
          <textarea
            data-id="kiro-account-import-text"
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
            rows={8}
            spellCheck={false}
            placeholder={"github:name@example.com:password:TOTPSEED\nawsBuildId:other@example.com:password:TOTPSEED"}
            className="w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 font-mono text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-violet-500/40"
          />
          {importResult && <div data-id="kiro-account-import-result" className="mt-2 text-[11px] text-zinc-400">{importResult}</div>}
          <div className="mt-3 flex justify-end gap-2">
            <button data-id="kiro-account-import-cancel" type="button" onClick={() => setImportOpen(false)} className="rounded-lg border border-white/[0.08] px-3 py-2 text-[12px] text-zinc-400">{t("cancel", { defaultValue: "取消" })}</button>
            <button data-id="kiro-account-import-submit" type="button" disabled={importing || !importText.trim()} onClick={() => void runImport()} className="rounded-lg bg-violet-500 px-3 py-2 text-[12px] font-medium text-white disabled:opacity-40">
              {importing ? t("settingsSaving", { defaultValue: "保存中…" }) : t("kiroAccountImportSubmit", { defaultValue: "导入" })}
            </button>
          </div>
        </section>
      </AppModal>
    </>
  );
}
