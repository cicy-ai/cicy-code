// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Shared shell for the credential-only matrix platforms (npm, Docker, Aliyun).
// They differ solely in which fields a credential has and what its usage row
// says, so the list/edit/reveal/bind/2FA behaviour lives here once instead of
// being copy-pasted per platform.

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Check, Eye, EyeOff, ExternalLink, Link2, Loader2, MoreHorizontal, Pencil, Plus, RefreshCw, Save, Send, Trash2, Wand2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import { sendToAgent } from "../../services/agentSend";
import { AppModal, useDialogs } from "../ui/Modal";
import { openChromeProfile } from "./GoogleAccountsPanel";
import AccountTOTPModal, { type TOTPValue } from "./AccountTOTPModal";

export type AccountBase = { name: string; profile?: string; "2fa_set"?: boolean };

export type AccountField = {
  key: string;
  // data-id suffix; defaults to the key, so a field named api_token can still
  // render as "<platform>-account-token-input".
  id?: string;
  label: string;
  kind?: "text" | "secret";
  placeholder?: string;
  // Secret fields may be left blank on edit to keep the stored value.
  keepOnEdit?: boolean;
  half?: boolean;
  // Blocks Save until filled — only enforced when creating.
  requiredOnCreate?: boolean;
};

export type AccountApi<A> = {
  list: () => Promise<{ data?: { accounts?: A[] } }>;
  reveal: (name: string) => Promise<{ data?: Record<string, string> }>;
  save: (body: Record<string, string | undefined>) => Promise<unknown>;
  remove: (name: string) => Promise<unknown>;
  usage?: (name: string) => Promise<{ data?: any }>;
  totp?: (name: string) => Promise<{ data?: { capture?: string; code?: string; countdown?: number } }>;
  bind?: (name: string) => Promise<{ data?: { registry?: string; profile?: string } }>;
};

// Fills the form from one pasted credential: `run` probes the platform and
// returns the fields to merge plus a one-line summary of what came back.
export type AccountInspect = {
  label: string;
  ready: (form: Record<string, string>) => boolean;
  run: (form: Record<string, string>) => Promise<{ fields?: Record<string, string>; summary?: string }>;
};

export type TokenAccountPanelProps<A extends AccountBase> = {
  active: boolean;
  paneId: string;
  dataId: string;
  title: string;
  subtitle: string;
  security: string;
  emptyLabel: string;
  editTitle: (name: string) => string;
  icon: ReactNode;
  // Tailwind fragments so each platform keeps its brand accent.
  accent: { button: string; tile: string; focus: string };
  fields: AccountField[];
  api: AccountApi<A>;
  renderMeta: (account: A) => ReactNode;
  renderBadge?: (account: A) => ReactNode;
  renderUsage?: (usage: any, account: A) => ReactNode;
  bindLabel?: string;
  bindToast?: (name: string, response: any) => string;
  agentPrompt: (name: string) => string;
  inspect?: AccountInspect;
};

export default function TokenAccountPanel<A extends AccountBase>({
  active,
  paneId,
  dataId,
  title,
  subtitle,
  security,
  emptyLabel,
  editTitle,
  icon,
  accent,
  fields,
  api,
  renderMeta,
  renderBadge,
  renderUsage,
  bindLabel,
  bindToast,
  agentPrompt,
  inspect,
}: TokenAccountPanelProps<A>) {
  const { t } = useTranslation("workspace");
  const [accounts, setAccounts] = useState<A[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const emptyForm = useCallback(() => Object.fromEntries(fields.map((field) => [field.key, ""])) as Record<string, string>, [fields]);
  const [form, setForm] = useState<Record<string, string>>(emptyForm);
  const [profiles, setProfiles] = useState<string[]>([]);
  const [visible, setVisible] = useState<Record<string, boolean>>({});
  const [saving, setSaving] = useState(false);
  const [generating2FA, setGenerating2FA] = useState("");
  const [totp, setTotp] = useState<Record<string, TOTPValue>>({});
  const [totpModal, setTotpModal] = useState<string | null>(null);
  const { confirm, node: dialogsNode } = useDialogs();
  const [sending, setSending] = useState("");
  const [binding, setBinding] = useState("");
  const [usage, setUsage] = useState<Record<string, any>>({});
  const [usageLoading, setUsageLoading] = useState<Record<string, boolean>>({});
  const [actionMenu, setActionMenu] = useState<{ name: string; top: number; right: number } | null>(null);
  const [inspecting, setInspecting] = useState(false);
  const [inspectSummary, setInspectSummary] = useState("");

  const describe = (e: any) => String(e?.response?.data?.detail || e?.message || e);
  // The `api` prop is an object literal rebuilt on every render of the platform
  // panel; keying the loaders on its identity would re-fetch in a loop.
  const apiRef = useRef(api);
  apiRef.current = api;
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [r, profileResponse] = await Promise.all([apiRef.current.list(), apiService.getGoogleAccounts()]);
      setAccounts(r.data?.accounts || []);
      setProfiles((profileResponse.data?.accounts || []).map((item: any) => String(item.profile)));
    } catch (e: any) {
      setError(describe(e));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    if (active) void load();
  }, [active, load]);

  const fetchUsage = useCallback(
    async (name: string) => {
      const usageApi = apiRef.current.usage;
      if (!usageApi) return;
      setUsageLoading((v) => ({ ...v, [name]: true }));
      try {
        const response = await usageApi(name);
        setUsage((v) => ({ ...v, [name]: response.data }));
      } catch (e: any) {
        setUsage((v) => ({ ...v, [name]: { error: describe(e) } }));
      } finally {
        setUsageLoading((v) => ({ ...v, [name]: false }));
      }
    },
    [],
  );
  useEffect(() => {
    if (!active) return;
    accounts.forEach((account) => void fetchUsage(account.name));
  }, [active, accounts, fetchUsage]);

  const beginNew = () => {
    setEditing("");
    setForm(emptyForm());
    setVisible({});
    setError("");
    setInspectSummary("");
  };
  const beginEdit = async (account: A) => {
    setEditing(account.name);
    setVisible({});
    setError("");
    setInspectSummary("");
    try {
      const revealed = (await api.reveal(account.name)).data || {};
      setForm({
        ...(Object.fromEntries(
          fields.map((field) => {
            const stored = (account as Record<string, any>)[field.key];
            const secret = revealed[field.key];
            return [field.key, String(secret ?? stored ?? "")];
          }),
        ) as Record<string, string>),
        profile: account.profile || "",
      });
    } catch {
      setError(t("tokenRevealFailed", { defaultValue: "Token 读取失败" }));
    }
  };
  const cancel = () => {
    setEditing(null);
    setForm(emptyForm());
  };
  const save = async () => {
    setSaving(true);
    setError("");
    try {
      const body: Record<string, string | undefined> = { old_name: editing || undefined };
      fields.forEach((field) => {
        const value = (form[field.key] || "").trim();
        // A blank keep-on-edit secret means "leave the stored one alone", so it
        // must be omitted rather than sent as an empty string.
        body[field.key] = field.keepOnEdit && !value ? undefined : value;
      });
      body.profile = form.profile || "";
      await api.save(body);
      cancel();
      await load();
    } catch (e: any) {
      setError(describe(e));
    } finally {
      setSaving(false);
    }
  };
  // Auto-fill: probe the platform with what has been typed so far and merge
  // back whatever it could resolve, leaving fields the user already filled.
  const runInspect = async () => {
    if (!inspect) return;
    setInspecting(true);
    setError("");
    try {
      const { fields: filled, summary } = await inspect.run(form);
      if (filled) setForm((v) => ({ ...v, ...filled }));
      setInspectSummary(summary || "");
    } catch (e: any) {
      setInspectSummary("");
      setError(describe(e));
    } finally {
      setInspecting(false);
    }
  };
  const remove = async (name: string) => {
    const ok = await confirm({
      title: t("deleteAccountTitle", { defaultValue: "删除账号？" }),
      body: t("deleteAccountBody", { name, defaultValue: `确定删除账号 ${name}？此操作无法撤销。` }),
      danger: true,
      confirmLabel: t("delete", { defaultValue: "删除" }),
    });
    if (!ok) return;
    setError("");
    try {
      await api.remove(name);
      await load();
    } catch (e: any) {
      setError(describe(e));
    }
  };
  const generate2FA = async (name: string) => {
    if (!api.totp) return;
    setGenerating2FA(name);
    setTotpModal(name);
    try {
      const response = await api.totp(name);
      const countdown = Number(response.data?.countdown || 0);
      setTotp((v) => ({ ...v, [name]: { capture: String(response.data?.capture || response.data?.code || ""), expiresAt: Date.now() + countdown * 1000 } }));
    } catch {
      setError(t("github2FAGenerateFailed"));
    } finally {
      setGenerating2FA("");
    }
  };
  const bind = async (name: string) => {
    if (!api.bind) return;
    setBinding(name);
    setError("");
    try {
      const response = await api.bind(name);
      window.dispatchEvent(new CustomEvent("show-toast", { detail: bindToast ? bindToast(name, response.data) : name }));
    } catch (e: any) {
      setError(describe(e));
    } finally {
      setBinding("");
    }
  };
  const sendToCurrentAgent = async (name: string) => {
    setSending(name);
    try {
      const handled = await sendToAgent(paneId, agentPrompt(name), { submit: true });
      if (handled) {
        window.dispatchEvent(new CustomEvent("show-toast", { detail: t("githubAccountSentToAgent", { name, defaultValue: `已将账号 ${name} 发送给当前 Agent` }) }));
      }
    } catch {
      window.dispatchEvent(new CustomEvent("show-toast", { detail: t("githubAccountSendFailed", { defaultValue: "发送失败" }) }));
    } finally {
      setSending("");
    }
  };

  const inputClass = `w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600 ${accent.focus}`;
  const saveDisabled =
    saving ||
    !(form.name || "").trim() ||
    fields.some((field) => field.requiredOnCreate && !editing && !(form[field.key] || "").trim());
  return (
    <div data-id={`${dataId}-accounts-section`} className={`h-full overflow-auto ${active ? "" : "hidden"}`}>
      <div data-id={`${dataId}-accounts-panel`} className="h-full p-4">
        <header data-id={`${dataId}-accounts-header`} className="mb-4 flex items-center justify-between gap-3 border-b border-white/[0.06] pb-3">
          <div className="min-w-0">
            <h2 className="flex items-center gap-2 text-[13px] font-semibold text-zinc-100">
              {icon}
              {title}
            </h2>
            <p className="mt-1 truncate text-[11px] text-zinc-500">{subtitle}</p>
          </div>
          <button data-id={`${dataId}-account-add`} type="button" onClick={beginNew} className={`inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md px-2.5 py-1.5 text-[11px] font-medium ${accent.button}`}>
            <Plus className="h-3.5 w-3.5" />
            {t("githubAccountAdd", { defaultValue: "添加账号" })}
          </button>
        </header>
        {error && (
          <div data-id={`${dataId}-accounts-error`} className="mb-4 rounded-lg border border-rose-500/20 bg-rose-500/[0.08] px-3 py-2 text-[12px] text-rose-300">
            {error}
          </div>
        )}
        <AppModal open={editing !== null} title={editing ? editTitle(editing) : t("githubAccountAdd", { defaultValue: "添加账号" })} onClose={cancel}>
          <section data-id={`${dataId}-account-form`}>
            <div className="grid gap-3 md:grid-cols-2">
              {fields.map((field) => {
                const fieldId = field.id || field.key;
                const secret = field.kind === "secret";
                return (
                  <div key={field.key} className={field.half ? "" : "md:col-span-2"}>
                    <label className="mb-1 block text-[11px] text-zinc-500">{field.label}</label>
                    <div className="relative">
                      <input
                        data-id={`${dataId}-account-${fieldId}-input`}
                        type={secret && !visible[field.key] ? "password" : "text"}
                        value={form[field.key] || ""}
                        onChange={(e) => {
                          const value = e.target.value;
                          setForm((v) => ({ ...v, [field.key]: value }));
                        }}
                        className={`${inputClass}${secret ? " pr-10" : ""}`}
                        placeholder={secret && editing && field.keepOnEdit ? t("githubAccountTokenKeep", { defaultValue: "留空保持现有 Token" }) : field.placeholder}
                        autoComplete={secret ? "new-password" : "off"}
                      />
                      {secret && (
                        <button
                          data-id={`${dataId}-account-${fieldId}-toggle`}
                          type="button"
                          onClick={() => setVisible((v) => ({ ...v, [field.key]: !v[field.key] }))}
                          className="absolute inset-y-0 right-0 px-3 text-zinc-500 hover:text-zinc-300"
                          aria-label={visible[field.key] ? t("hideToken", { defaultValue: "隐藏" }) : t("showToken", { defaultValue: "显示" })}
                        >
                          {visible[field.key] ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
              <div className="md:col-span-2">
                <label className="mb-1 block text-[11px] text-zinc-500">Chrome Profile</label>
                <div className="flex gap-2">
                  <select data-id={`${dataId}-account-profile-select`} value={form.profile || ""} onChange={(e) => setForm((v) => ({ ...v, profile: e.target.value }))} className={inputClass}>
                    <option value="">{t("selectChromeProfile")}</option>
                    {profiles.map((profile) => (
                      <option key={profile} value={profile}>
                        {profile}
                      </option>
                    ))}
                  </select>
                  <button data-id={`${dataId}-account-open-chrome`} type="button" disabled={!form.profile} onClick={() => void openChromeProfile(form.profile).catch((e) => setError(describe(e)))} className="rounded-lg border border-white/[0.08] px-3 text-zinc-400 disabled:opacity-30" title={t("openInChrome")}>
                    <ExternalLink className="h-4 w-4" />
                  </button>
                </div>
              </div>
            </div>
            {inspect && (
              <div data-id={`${dataId}-account-inspect-row`} className="mt-3 flex items-center gap-2">
                <button
                  data-id={`${dataId}-account-inspect`}
                  type="button"
                  disabled={inspecting || !inspect.ready(form)}
                  onClick={() => void runInspect()}
                  className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.12] px-2.5 py-1.5 text-[11px] text-zinc-300 hover:bg-white/[0.06] disabled:opacity-40"
                >
                  {inspecting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Wand2 className="h-3.5 w-3.5" />}
                  {inspect.label}
                </button>
                {inspectSummary && (
                  <span data-id={`${dataId}-account-inspect-summary`} className="min-w-0 flex-1 truncate text-[11px] text-emerald-400/90" title={inspectSummary}>
                    {inspectSummary}
                  </span>
                )}
              </div>
            )}
            <div className="mt-4 flex gap-2">
              <button data-id={`${dataId}-account-save`} type="button" disabled={saveDisabled} onClick={() => void save()} className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-[12px] font-medium disabled:opacity-40 ${accent.button}`}>
                <Save className="h-3.5 w-3.5" />
                {saving ? t("settingsSaving", { defaultValue: "保存中…" }) : t("settingsSave", { defaultValue: "保存" })}
              </button>
              <button data-id={`${dataId}-account-cancel`} type="button" onClick={cancel} className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] px-3 py-2 text-[12px] text-zinc-400">
                <X className="h-3.5 w-3.5" />
                {t("cancel", { defaultValue: "取消" })}
              </button>
            </div>
          </section>
        </AppModal>
        <div data-id={`${dataId}-account-list`} className="space-y-2">
          {loading ? (
            <div data-id={`${dataId}-accounts-loading`} className="space-y-2">
              {[0, 1, 2].map((item) => (
                <div key={item} data-id={`${dataId}-account-skeleton`} className="flex h-[92px] animate-pulse items-center gap-3 rounded-lg border border-white/[0.07] bg-white/[0.02] p-3">
                  <div className="h-8 w-8 rounded-md bg-white/[0.07]" />
                  <div className="flex-1">
                    <div className="h-3 w-24 rounded bg-white/[0.08]" />
                    <div className="mt-2 h-2.5 w-48 rounded bg-white/[0.05]" />
                    <div className="mt-2 h-2.5 w-36 rounded bg-white/[0.05]" />
                  </div>
                  <div className="h-7 w-16 rounded bg-white/[0.05]" />
                </div>
              ))}
            </div>
          ) : accounts.length === 0 ? (
            <div data-id={`${dataId}-accounts-empty`} className="rounded-lg border border-dashed border-white/[0.08] py-12 text-center text-[12px] text-zinc-500">
              {emptyLabel}
            </div>
          ) : (
            accounts.map((account) => (
              <article key={account.name} data-id={`${dataId}-account-${account.name}`} className="flex h-[92px] items-center gap-3 overflow-hidden rounded-lg border border-white/[0.07] bg-white/[0.02] p-3">
                <span className={`grid h-8 w-8 shrink-0 place-items-center rounded-md ${accent.tile}`}>{icon}</span>
                <div className="min-w-0 flex-1">
                  <div data-id={`${dataId}-account-name`} className="flex items-center gap-2 text-[12px] font-medium text-zinc-100">
                    {account.name}
                    {renderBadge?.(account)}
                  </div>
                  <div data-id={`${dataId}-account-meta`} className="mt-0.5 truncate text-[11px] text-zinc-500">
                    {renderMeta(account)}
                  </div>
                  {usage[account.name] && !usage[account.name].error && renderUsage && (
                    <div data-id={`${dataId}-account-usage`} className="mt-1 flex flex-wrap gap-x-3 text-[10px] text-zinc-500">
                      {renderUsage(usage[account.name], account)}
                    </div>
                  )}
                  {usageLoading[account.name] && !usage[account.name] && (
                    <div data-id={`${dataId}-account-usage-skeleton`} className="mt-2 flex animate-pulse gap-2">
                      <span className="h-2.5 w-20 rounded bg-white/[0.07]" />
                      <span className="h-2.5 w-20 rounded bg-white/[0.07]" />
                      <span className="h-2.5 w-12 rounded bg-white/[0.07]" />
                    </div>
                  )}
                  {usage[account.name]?.error && (
                    <div data-id={`${dataId}-account-usage-error`} className="mt-1 truncate text-[10px] text-amber-500" title={usage[account.name].error}>
                      {usage[account.name].error}
                    </div>
                  )}
                </div>
                <button
                  data-id={`${dataId}-account-more`}
                  type="button"
                  onClick={(event) => {
                    const rect = event.currentTarget.getBoundingClientRect();
                    setActionMenu(actionMenu?.name === account.name ? null : { name: account.name, top: rect.bottom + 4, right: window.innerWidth - rect.right });
                  }}
                  title={t("rosterMore", { defaultValue: "更多" })}
                  className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-white/[0.08] text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-100"
                >
                  <MoreHorizontal className="h-4 w-4" />
                </button>
              </article>
            ))
          )}
        </div>
        {actionMenu &&
          (() => {
            const account = accounts.find((item) => item.name === actionMenu.name);
            if (!account) return null;
            const itemClass = "flex w-full items-center gap-2 px-3 py-2 text-left text-[12px] text-zinc-300 hover:bg-white/[0.06] hover:text-white disabled:opacity-40";
            return createPortal(
              <>
                <button data-id={`${dataId}-account-more-backdrop`} type="button" aria-label={t("cancel", { defaultValue: "关闭" })} className="fixed inset-0 z-[100] cursor-default" onClick={() => setActionMenu(null)} />
                <div data-id={`${dataId}-account-more-menu`} className="fixed z-[101] min-w-[200px] overflow-hidden rounded-lg border border-white/[0.1] bg-[#181818] py-1 shadow-2xl" style={{ top: actionMenu.top, right: actionMenu.right }}>
                  {api.bind && (
                    <button data-id={`${dataId}-account-bind`} type="button" disabled={binding === account.name} className={itemClass} onClick={() => { setActionMenu(null); void bind(account.name); }}>
                      {binding === account.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Link2 className="h-3.5 w-3.5" />}
                      {bindLabel}
                    </button>
                  )}
                  {api.usage && (
                    <button data-id={`${dataId}-account-refresh-usage`} type="button" disabled={usageLoading[account.name]} className={itemClass} onClick={() => { setActionMenu(null); void fetchUsage(account.name); }}>
                      <RefreshCw className={`h-3.5 w-3.5 ${usageLoading[account.name] ? "animate-spin" : ""}`} />
                      {t("githubUsageRefresh")}
                    </button>
                  )}
                  {api.totp && account["2fa_set"] && (
                    <button data-id={`${dataId}-account-generate-2fa`} type="button" disabled={generating2FA === account.name} className={itemClass} onClick={() => { setActionMenu(null); void generate2FA(account.name); }}>
                      {generating2FA === account.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <span className="w-3.5 text-center text-[10px] font-semibold text-amber-300">2F</span>}
                      {t("github2FAModalTitle", { name: account.name })}
                    </button>
                  )}
                  {account.profile && (
                    <button data-id={`${dataId}-account-open-chrome`} type="button" className={itemClass} onClick={() => { setActionMenu(null); void openChromeProfile(account.profile || "").catch((e) => setError(describe(e))); }}>
                      <ExternalLink className="h-3.5 w-3.5" />
                      {t("openInChrome")}
                    </button>
                  )}
                  <button data-id={`${dataId}-account-send-to-agent`} type="button" disabled={sending === account.name} className={itemClass} onClick={() => { setActionMenu(null); void sendToCurrentAgent(account.name); }}>
                    {sending === account.name ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
                    {t("githubAccountSendToAgent", { defaultValue: "发送给当前 Agent" })}
                  </button>
                  <button data-id={`${dataId}-account-edit`} type="button" className={itemClass} onClick={() => { setActionMenu(null); void beginEdit(account); }}>
                    <Pencil className="h-3.5 w-3.5" />
                    {t("settingsEdit", { defaultValue: "编辑" })}
                  </button>
                  <div className="my-1 border-t border-white/[0.07]" />
                  <button data-id={`${dataId}-account-delete`} type="button" className={`${itemClass} text-rose-400 hover:text-rose-300`} onClick={() => { setActionMenu(null); void remove(account.name); }}>
                    <Trash2 className="h-3.5 w-3.5" />
                    {t("settingsDelete", { defaultValue: "删除" })}
                  </button>
                </div>
              </>,
              document.body,
            );
          })()}
        <AccountTOTPModal name={totpModal} value={totpModal ? totp[totpModal] : undefined} loading={Boolean(totpModal && generating2FA === totpModal)} onClose={() => setTotpModal(null)} onRefresh={() => { if (totpModal) void generate2FA(totpModal); }} />
        <div data-id={`${dataId}-accounts-security-note`} className="mt-5 flex items-start gap-2 text-[11px] leading-5 text-zinc-600">
          <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />
          {security}
        </div>
      </div>
      {dialogsNode}
    </div>
  );
}
