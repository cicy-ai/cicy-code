// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from "react";
import {
  Check,
  Eye,
  EyeOff,
  ExternalLink,
  Github,
  Loader2,
  Pencil,
  Plus,
  Save,
  Send,
  Trash2,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import { AppModal, useDialogs } from "../ui/Modal";
import { openChromeProfile } from "./GoogleAccountsPanel";
import AccountTOTPModal, { type TOTPValue } from "./AccountTOTPModal";

type GithubAccount = {
  name: string;
  email: string;
  token_set: boolean;
  token_tail?: string;
  "2fa_set": boolean;
  profile: string;
};

export default function GithubAccountsPanel({
  active,
  paneId,
}: {
  active: boolean;
  paneId: string;
}) {
  const { t } = useTranslation("workspace");
  const [accounts, setAccounts] = useState<GithubAccount[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState({ name: "", email: "", api_token: "", "2fa": "", profile: "" });
  const [profiles, setProfiles] = useState<string[]>([]);
  const [showToken, setShowToken] = useState(false);
  const [show2FA, setShow2FA] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState("");
  const [generating2FA, setGenerating2FA] = useState("");
  const [totp, setTotp] = useState<Record<string, TOTPValue>>({});
  const [totpModal, setTotpModal] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<Record<string, string>>({});
  const { confirm, node: dialogsNode } = useDialogs();
  const [sending, setSending] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [r, profileResponse] = await Promise.all([apiService.getGithubAccounts(), apiService.getGoogleAccounts()]);
      setAccounts(r.data?.accounts || []);
      setProfiles((profileResponse.data?.accounts || []).map((item: any) => String(item.profile)));
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    if (active) void load();
  }, [active, load]);

  const beginNew = () => {
    setEditing("");
    setForm({ name: "", email: "", api_token: "", "2fa": "", profile: "" });
    setShowToken(false);
    setShow2FA(false);
    setError("");
  };
  const beginEdit = async (account: GithubAccount) => {
    setEditing(account.name);
    setShowToken(false);
    setShow2FA(false);
    setError("");
    try {
      const r = await apiService.getGithubAccountToken(account.name);
      setForm({ name: account.name, email: account.email || "", api_token: r.data?.api_token || "", "2fa": r.data?.["2fa"] || "", profile: account.profile || "" });
    } catch { setError(t("tokenRevealFailed", { defaultValue: "Token 读取失败" })); }
  };
  const cancel = () => {
    setEditing(null);
    setForm({ name: "", email: "", api_token: "", "2fa": "", profile: "" });
  };
  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await apiService.saveGithubAccount({
        name: form.name.trim(),
        old_name: editing || undefined,
        email: form.email.trim(),
        api_token: form.api_token.trim() || undefined,
        "2fa": form["2fa"].trim(),
        profile: form.profile,
      });
      cancel();
      await load();
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
    } finally {
      setSaving(false);
    }
  };
  const remove = async (name: string) => {
    const ok = await confirm({
      title: t("deleteAccountTitle", { defaultValue: "删除账号？" }),
      body: t("deleteAccountBody", {
        name,
        defaultValue: `确定删除账号 ${name}？此操作无法撤销。`,
      }),
      danger: true,
      confirmLabel: t("delete", { defaultValue: "删除" }),
    });
    if (!ok) return;
    setError("");
    try {
      await apiService.deleteGithubAccount(name);
      await load();
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
    }
  };
  const test = async (name: string) => {
    setTesting(name);
    setTestResult((v) => ({ ...v, [name]: "" }));
    try {
      const r = await apiService.testGithubAccount(name);
      setTestResult((v) => ({ ...v, [name]: `✓ ${r.data?.login || name}` }));
    } catch {
      setTestResult((v) => ({
        ...v,
        [name]: t("githubAccountTestFailed", { defaultValue: "连接失败" }),
      }));
    } finally {
      setTesting("");
    }
  };
  const generate2FA = async (name: string) => {
    setGenerating2FA(name);
    setTotpModal(name);
    try {
      const response = await apiService.getGithubAccountTOTP(name);
      const countdown = Number(response.data?.countdown || 0);
      setTotp((v) => ({ ...v, [name]: { capture: String(response.data?.capture || response.data?.code || ""), expiresAt: Date.now() + countdown * 1000 } }));
    } catch {
      setError(t("github2FAGenerateFailed"));
    } finally {
      setGenerating2FA("");
    }
  };
  const sendToCurrentAgent = async (name: string) => {
    if (!paneId) return;
    setSending(name);
    const prompt = t("githubAccountAgentPrompt", {
      name,
      defaultValue:
        "请使用 github skill 管理 GitHub，指定账号为「{{name}}」。先执行 github whoami --account {{name}} 确认身份，然后等待我的下一步指令。不要读取、输出或泄露 Token。",
    });
    try {
      await apiService.sendCommand(paneId, prompt, true);
      window.dispatchEvent(
        new CustomEvent("show-toast", {
          detail: t("githubAccountSentToAgent", {
            name,
            defaultValue: `已将账号 ${name} 发送给当前 Agent`,
          }),
        }),
      );
    } catch {
      window.dispatchEvent(
        new CustomEvent("show-toast", {
          detail: t("githubAccountSendFailed", { defaultValue: "发送失败" }),
        }),
      );
    } finally {
      setSending("");
    }
  };

  const inputClass =
    "w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-500/40";
  return (
    <div
      data-id="github-accounts-section"
      className={`h-full overflow-auto ${active ? "" : "hidden"}`}
    >
      <div data-id="github-accounts-panel" className="h-full p-4">
        <header
          data-id="github-accounts-header"
          className="mb-4 flex items-center justify-between gap-3 border-b border-white/[0.06] pb-3"
        >
          <div className="min-w-0">
            <h2 className="flex items-center gap-2 text-[13px] font-semibold text-zinc-100">
              <Github className="h-4 w-4 text-zinc-400" />
              {t("githubAccountsTitle", { defaultValue: "GitHub 账号" })}
            </h2>
            <p className="mt-1 truncate text-[11px] text-zinc-500">
              {t("githubAccountsSubtitle", {
                defaultValue:
                  "管理本机 github.json 中供 Agent 使用的多个 GitHub 账号。Token 不会在页面中回显。",
              })}
            </p>
          </div>
          <button
            data-id="github-account-add"
            type="button"
            onClick={beginNew}
            className="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md bg-emerald-500 px-2.5 py-1.5 text-[11px] font-medium text-black hover:bg-emerald-400"
          >
            <Plus className="h-3.5 w-3.5" />
            {t("githubAccountAdd", { defaultValue: "添加账号" })}
          </button>
        </header>
        {error && (
          <div
            data-id="github-accounts-error"
            className="mb-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.08] px-3 py-2 text-[12px] text-rose-300"
          >
            {error}
          </div>
        )}
        <AppModal open={editing !== null} title={editing ? t("githubAccountEditTitle", { name: editing }) : t("githubAccountAdd")} onClose={cancel}>
          <section data-id="github-account-form">
            <div className="grid gap-3 md:grid-cols-2">
              <div>
                <label className="mb-1 block text-[11px] text-zinc-500">
                  {t("githubAccountName", { defaultValue: "账号名称" })}
                </label>
                <input
                  data-id="github-account-name-input"
                  value={form.name}
                  onChange={(e) =>
                    setForm((v) => ({ ...v, name: e.target.value }))
                  }
                  className={inputClass}
                  placeholder="octocat"
                />
              </div>
              <div>
                <label className="mb-1 block text-[11px] text-zinc-500">
                  Email
                </label>
                <input
                  data-id="github-account-email-input"
                  type="email"
                  value={form.email}
                  onChange={(e) =>
                    setForm((v) => ({ ...v, email: e.target.value }))
                  }
                  className={inputClass}
                  placeholder="name@example.com"
                />
              </div>
            </div>
            <div className="mt-3">
              <label className="mb-1 block text-[11px] text-zinc-500">
                Personal Access Token
              </label>
              <div className="relative">
                <input
                  data-id="github-account-token-input"
                  type={showToken ? "text" : "password"}
                  value={form.api_token}
                  onChange={(e) =>
                    setForm((v) => ({ ...v, api_token: e.target.value }))
                  }
                  className={`${inputClass} pr-10`}
                  placeholder={
                    editing
                      ? t("githubAccountTokenKeep", {
                          defaultValue: "留空保持现有 Token",
                        })
                      : "github_pat_…"
                  }
                  autoComplete="new-password"
                />
                <button
                  data-id="github-account-token-toggle"
                  type="button"
                  onClick={() => setShowToken((v) => !v)}
                  className="absolute inset-y-0 right-0 px-3 text-zinc-500 hover:text-zinc-300"
                >
                  {showToken ? (
                    <EyeOff className="h-4 w-4" />
                  ) : (
                    <Eye className="h-4 w-4" />
                  )}
                </button>
              </div>
            </div>
            <div className="mt-3">
              <label className="mb-1 block text-[11px] text-zinc-500">Chrome Profile</label>
              <div className="flex gap-2"><select data-id="github-account-profile-select" value={form.profile} onChange={(e) => setForm((v) => ({ ...v, profile: e.target.value }))} className={inputClass}><option value="">{t("selectChromeProfile")}</option>{profiles.map((profile) => <option key={profile} value={profile}>{profile}</option>)}</select><button data-id="github-account-open-chrome" type="button" disabled={!form.profile} onClick={() => void openChromeProfile(form.profile).catch((e) => setError(String(e?.message || e)))} className="rounded-lg border border-white/[0.08] px-3 text-zinc-400 disabled:opacity-30" title={t("openInChrome")}><ExternalLink className="h-4 w-4" /></button></div>
            </div>
            <div className="mt-3">
              <label className="mb-1 block text-[11px] text-zinc-500">2FA</label>
              <div className="relative">
                <input
                  data-id="github-account-2fa-input"
                  type={show2FA ? "text" : "password"}
                  value={form["2fa"]}
                  onChange={(e) => setForm((v) => ({ ...v, "2fa": e.target.value }))}
                  className={`${inputClass} pr-10`}
                  autoComplete="new-password"
                />
                <button
                  data-id="github-account-2fa-toggle"
                  type="button"
                  onClick={() => setShow2FA((v) => !v)}
                  className="absolute inset-y-0 right-0 px-3 text-zinc-500 hover:text-zinc-300"
                  aria-label={show2FA ? t("hideToken", { defaultValue: "隐藏" }) : t("showToken", { defaultValue: "显示" })}
                >
                  {show2FA ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </div>
            <div className="mt-4 flex gap-2">
              <button
                data-id="github-account-save"
                type="button"
                disabled={
                  saving ||
                  !form.name.trim() ||
                  (!editing && !form.api_token.trim())
                }
                onClick={() => void save()}
                className="inline-flex items-center gap-1.5 rounded-lg bg-emerald-500 px-3 py-2 text-[12px] font-medium text-black disabled:opacity-40"
              >
                <Save className="h-3.5 w-3.5" />
                {saving
                  ? t("settingsSaving", { defaultValue: "保存中…" })
                  : t("settingsSave", { defaultValue: "保存" })}
              </button>
              <button
                data-id="github-account-cancel"
                type="button"
                onClick={cancel}
                className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] px-3 py-2 text-[12px] text-zinc-400"
              >
                <X className="h-3.5 w-3.5" />
                {t("cancel", { defaultValue: "取消" })}
              </button>
            </div>
          </section>
        </AppModal>
        <div data-id="github-account-list" className="space-y-2">
          {loading ? (
            <div
              data-id="github-accounts-loading"
              className="flex justify-center py-12 text-zinc-500"
            >
              <Loader2 className="h-5 w-5 animate-spin" />
            </div>
          ) : accounts.length === 0 ? (
            <div
              data-id="github-accounts-empty"
              className="rounded-lg border border-dashed border-white/[0.08] py-12 text-center text-[12px] text-zinc-500"
            >
              {t("githubAccountsEmpty", { defaultValue: "还没有 GitHub 账号" })}
            </div>
          ) : (
            accounts.map((account) => (
              <article
                key={account.name}
                data-id={`github-account-${account.name}`}
                className="flex items-center gap-3 rounded-lg border border-white/[0.07] bg-white/[0.02] p-3"
              >
                <span className="grid h-8 w-8 shrink-0 place-items-center rounded-md bg-white/[0.06] text-zinc-300">
                  <Github className="h-4 w-4" />
                </span>
                <div className="min-w-0 flex-1">
                  <div
                    data-id="github-account-name"
                    className="text-[12px] font-medium text-zinc-100"
                  >
                    {account.name}
                  </div>
                  <div
                    data-id="github-account-meta"
                    className="mt-0.5 truncate text-[11px] text-zinc-500"
                  >
                    {account.email || "—"} ·{" "}
                    {account.token_set
                      ? `Token ••••${account.token_tail || ""}`
                      : t("githubAccountTokenMissing", {
                          defaultValue: "未设置 Token",
                        })}
                  </div>
                  {testResult[account.name] && (
                    <div
                      data-id="github-account-test-result"
                      className={`mt-1 text-[11px] ${testResult[account.name].startsWith("✓") ? "text-emerald-400" : "text-rose-400"}`}
                    >
                      {testResult[account.name]}
                    </div>
                  )}
                </div>
                {account["2fa_set"] && (
                  <button data-id="github-account-generate-2fa" type="button" onClick={() => void generate2FA(account.name)} disabled={generating2FA === account.name} className="shrink-0 rounded-md border border-amber-500/20 bg-amber-500/[0.08] px-2.5 py-1.5 text-[11px] text-amber-300 disabled:opacity-40">
                    {generating2FA === account.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : "2FA"}
                  </button>
                )}
                {account.profile && <button data-id="github-account-open-chrome" type="button" onClick={() => void openChromeProfile(account.profile).catch((e) => setError(String(e?.message || e)))} className="shrink-0 rounded-md p-2 text-zinc-500 hover:text-zinc-200" title={t("openInChrome")}><ExternalLink className="h-3.5 w-3.5" /></button>}
                <button
                  data-id="github-account-test"
                  type="button"
                  onClick={() => void test(account.name)}
                  disabled={testing === account.name}
                  className="shrink-0 whitespace-nowrap rounded-md border border-white/[0.08] px-2.5 py-1.5 text-[11px] text-zinc-300"
                >
                  {testing === account.name ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  ) : (
                    t("githubAccountTest", { defaultValue: "测试" })
                  )}
                </button>
                <button
                  data-id="github-account-send-to-agent"
                  type="button"
                  onClick={() => void sendToCurrentAgent(account.name)}
                  disabled={!paneId || sending === account.name}
                  title={t("githubAccountSendToAgent", {
                    defaultValue: "发送给当前 Agent",
                  })}
                  className="shrink-0 rounded-md border border-sky-500/20 bg-sky-500/[0.08] p-2 text-sky-400 hover:bg-sky-500/[0.15] disabled:opacity-40"
                >
                  {sending === account.name ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <Send className="h-3.5 w-3.5" />
                  )}
                </button>
                <button
                  data-id="github-account-edit"
                  type="button"
                  onClick={() => void beginEdit(account)}
                  className="shrink-0 rounded-md p-2 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200"
                >
                  <Pencil className="h-3.5 w-3.5" />
                </button>
                <button
                  data-id="github-account-delete"
                  type="button"
                  onClick={() => void remove(account.name)}
                  className="shrink-0 rounded-md p-2 text-zinc-500 hover:bg-white/[0.06] hover:text-rose-300"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </article>
            ))
          )}
        </div>
        <AccountTOTPModal name={totpModal} value={totpModal ? totp[totpModal] : undefined} loading={Boolean(totpModal && generating2FA === totpModal)} onClose={() => setTotpModal(null)} onRefresh={() => { if (totpModal) void generate2FA(totpModal); }} />
        <div
          data-id="github-accounts-security-note"
          className="mt-5 flex items-start gap-2 text-[11px] leading-5 text-zinc-600"
        >
          <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />
          {t("githubAccountsSecurity", {
            defaultValue:
              "Token 仅保存在本机 ~/cicy-ai/db/github.json，接口只返回是否已配置和末四位。",
          })}
        </div>
      </div>
      {dialogsNode}
    </div>
  );
}
