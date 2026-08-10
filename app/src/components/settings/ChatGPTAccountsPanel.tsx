// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from "react";
import { ExternalLink, Eye, EyeOff, Loader2, Pencil, Plus, Save, Trash2, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import { AppModal, useDialogs } from "../ui/Modal";
import { openChromeProfile } from "./GoogleAccountsPanel";
import AccountTOTPModal, { type TOTPValue } from "./AccountTOTPModal";
import { assetUrl } from "../../lib/assets";

type ChatGPTAccount = { name: string; email: string; mobile: string; profile: string; password_set: boolean; "2fa_set": boolean };
const emptyForm = { name: "", email: "", password: "", mobile: "", "2fa": "", profile: "" };

export default function ChatGPTAccountsPanel({ active }: { active: boolean }) {
  const { t } = useTranslation("workspace");
  const [accounts, setAccounts] = useState<ChatGPTAccount[]>([]);
  const [profiles, setProfiles] = useState<string[]>([]);
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState(emptyForm);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [show2FA, setShow2FA] = useState(false);
  const [error, setError] = useState("");
  const { confirm, node: dialogsNode } = useDialogs();
  const [totpName, setTotpName] = useState<string | null>(null);
  const [totpValue, setTotpValue] = useState<TOTPValue>();
  const [totpLoading, setTotpLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [chatGPTResponse, profilesResponse] = await Promise.all([apiService.getChatGPTAccounts(), apiService.getGoogleAccounts()]);
      setAccounts(chatGPTResponse.data?.accounts || []);
      setProfiles((profilesResponse.data?.accounts || []).map((item: any) => String(item.profile)));
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { if (active) void load(); }, [active, load]);
  const beginNew = () => { setEditing(""); setForm(emptyForm); setShowPassword(false); setShow2FA(false); };
  const beginEdit = async (account: ChatGPTAccount) => {
    setEditing(account.name); setShowPassword(false); setShow2FA(false);
    const secrets = await apiService.getChatGPTAccountSecrets(account.name);
    setForm({ name: account.name, email: account.email || "", mobile: account.mobile || "", profile: account.profile || "", password: secrets.data?.password || "", "2fa": secrets.data?.["2fa"] || "" });
  };
  const save = async () => {
    setSaving(true); setError("");
    try { await apiService.saveChatGPTAccount({ ...form, old_name: editing || undefined }); setEditing(null); await load(); }
    catch (e: any) { setError(String(e?.response?.data?.detail || e?.message || e)); }
    finally { setSaving(false); }
  };
  const remove = async (name: string) => {
    if (!await confirm({ title: t("deleteAccountTitle"), body: t("deleteAccountBody", { name }), danger: true, confirmLabel: t("delete") })) return;
    await apiService.deleteChatGPTAccount(name); await load();
  };
  const showTOTP = async (name: string) => {
    setTotpName(name); setTotpValue(undefined); setTotpLoading(true);
    try { const response = await apiService.getChatGPTAccountTOTP(name); setTotpValue({ capture: String(response.data?.capture || response.data?.code || ""), expiresAt: Date.now() + Number(response.data?.countdown || 0) * 1000 }); }
    catch (e: any) { setError(String(e?.response?.data?.detail || e?.message || e)); }
    finally { setTotpLoading(false); }
  };
  const inputClass = "w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none focus:border-emerald-500/40";
  return <div data-id="chatgpt-accounts-panel" className={`h-full overflow-auto p-4 ${active ? "" : "hidden"}`}>
    <header data-id="chatgpt-accounts-header" className="mb-4 flex items-center gap-3 border-b border-white/[0.06] pb-3"><div className="min-w-0 flex-1"><h2 className="flex items-center gap-2 text-[13px] font-semibold text-zinc-100"><Bot className="h-4 w-4 text-emerald-400" />ChatGPT</h2><p className="mt-1 text-[11px] text-zinc-500">{t("chatGPTAccountsSubtitle")}</p></div><button data-id="chatgpt-account-add" onClick={beginNew} className="flex items-center gap-1.5 rounded-md bg-emerald-500 px-2.5 py-1.5 text-[11px] font-medium text-black"><Plus className="h-3.5 w-3.5" />{t("githubAccountAdd")}</button></header>
    {error && <div data-id="chatgpt-accounts-error" className="mb-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.08] px-3 py-2 text-[12px] text-rose-300">{error}</div>}
    <AppModal open={editing !== null} title={editing ? t("chatGPTAccountEditTitle", { name: editing }) : t("chatGPTAccountAddTitle")} onClose={() => setEditing(null)} maxWidth="580px">
      <section data-id="chatgpt-account-form" className="grid grid-cols-2 gap-3">
        <label><span className="mb-1 block text-[11px] text-zinc-500">Email</span><input data-id="chatgpt-account-email-input" type="email" value={form.email} onChange={(e) => { const email = e.target.value; setForm((v) => ({ ...v, email, ...(email.includes("@") ? { name: email.split("@")[0] } : {}) })); }} className={inputClass} /></label>
        <label><span className="mb-1 block text-[11px] text-zinc-500">Mobile</span><input data-id="chatgpt-account-mobile-input" value={form.mobile} onChange={(e) => setForm((v) => ({ ...v, mobile: e.target.value }))} className={inputClass} /></label>
        <Secret dataId="chatgpt-account-password" label="Password" value={form.password} visible={showPassword} toggle={() => setShowPassword((v) => !v)} change={(password) => setForm((v) => ({ ...v, password }))} inputClass={inputClass} />
        <Secret dataId="chatgpt-account-2fa" label="2FA" value={form["2fa"]} visible={show2FA} toggle={() => setShow2FA((v) => !v)} change={(value) => setForm((v) => ({ ...v, "2fa": value }))} inputClass={inputClass} />
        <label className="col-span-2"><span className="mb-1 block text-[11px] text-zinc-500">Chrome Profile</span><div className="flex gap-2"><select data-id="chatgpt-account-profile-select" value={form.profile} onChange={(e) => setForm((v) => ({ ...v, profile: e.target.value }))} className={inputClass}><option value="">{t("selectChromeProfile")}</option>{profiles.map((profile) => <option key={profile} value={profile}>{profile}</option>)}</select><button data-id="chatgpt-account-open-chrome" type="button" disabled={!form.profile} onClick={() => void openChromeProfile(form.profile).catch((e) => setError(String(e?.message || e)))} className="rounded-lg border border-white/[0.08] px-3 text-zinc-400 disabled:opacity-30" title={t("openInChrome")}><ExternalLink className="h-4 w-4" /></button></div></label>
        <div className="col-span-2 mt-2 flex justify-end gap-2 border-t border-white/[0.07] pt-4"><button data-id="chatgpt-account-save" disabled={saving || !form.email.trim() || !form.profile} onClick={() => void save()} className="flex items-center gap-1.5 rounded-md bg-emerald-500 px-3 py-2 text-[11px] text-black disabled:opacity-40">{saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}{t("settingsSave")}</button><button data-id="chatgpt-account-cancel" onClick={() => setEditing(null)} className="rounded-md p-2 text-zinc-500"><X className="h-3.5 w-3.5" /></button></div>
      </section>
    </AppModal>
    <div data-id="chatgpt-account-list" className="space-y-2">{loading ? <div className="flex justify-center py-12"><Loader2 className="h-5 w-5 animate-spin" /></div> : accounts.map((account) => <article key={account.name} data-id={`chatgpt-account-${account.name}`} className="flex items-center gap-3 rounded-lg border border-white/[0.07] p-3"><span className="grid h-8 w-8 place-items-center rounded-md bg-emerald-500/10 text-emerald-400"><Bot className="h-4 w-4" /></span><div className="min-w-0 flex-1"><div className="truncate text-[12px] font-medium text-zinc-100">{account.email}</div><div className="mt-0.5 flex gap-2 text-[10px] text-zinc-500"><span>{account.profile || "—"}</span>{account["2fa_set"] ? <button data-id="chatgpt-account-generate-2fa" onClick={() => void showTOTP(account.name)} className="text-amber-400">2FA</button> : <span>2FA —</span>}{account.mobile && <span>{account.mobile}</span>}</div></div>{account.profile && <button data-id="chatgpt-account-open-chrome" onClick={() => void openChromeProfile(account.profile).catch((e) => setError(String(e?.message || e)))} className="p-2 text-zinc-500" title={t("openInChrome")}><ExternalLink className="h-3.5 w-3.5" /></button>}<button data-id="chatgpt-account-edit" onClick={() => void beginEdit(account)} className="p-2 text-zinc-500"><Pencil className="h-3.5 w-3.5" /></button><button data-id="chatgpt-account-delete" onClick={() => void remove(account.name)} className="p-2 text-zinc-500 hover:text-rose-300"><Trash2 className="h-3.5 w-3.5" /></button></article>)}</div>
    <AccountTOTPModal name={totpName} value={totpValue} loading={totpLoading} onClose={() => setTotpName(null)} onRefresh={() => { if (totpName) void showTOTP(totpName); }} />
    {dialogsNode}
  </div>;
}

export function ChatGPTIcon({ className = "" }: { className?: string }) {
  return <img data-id="chatgpt-icon" src={assetUrl("/assets/logos/openai.svg")} className={`${className} brightness-0 invert`} alt="" />;
}

const Bot = ChatGPTIcon;

function Secret({ dataId, label, value, visible, toggle, change, inputClass }: { dataId: string; label: string; value: string; visible: boolean; toggle: () => void; change: (value: string) => void; inputClass: string }) {
  return <label data-id={`${dataId}-field`}><span className="mb-1 block text-[11px] text-zinc-500">{label}</span><div className="relative"><input data-id={`${dataId}-input`} type={visible ? "text" : "password"} value={value} onChange={(e) => change(e.target.value)} className={`${inputClass} pr-10`} autoComplete="new-password" /><button data-id={`${dataId}-toggle`} type="button" onClick={toggle} className="absolute inset-y-0 right-0 px-3 text-zinc-500">{visible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}</button></div></label>;
}
