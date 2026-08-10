// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from "react";
import { ExternalLink, Eye, EyeOff, KeyRound, Loader2, Pencil, Plus, Save, Smartphone, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import apiService from "../../services/api";
import { AppModal } from "../ui/Modal";
import AccountTOTPModal, { type TOTPValue } from "./AccountTOTPModal";

type GoogleAccount = {
  profile: string;
  email: string;
  mobile: string;
  recovery_email: string;
  password_set: boolean;
  "2fa_set": boolean;
};

const emptyForm = { profile: "", email: "", password: "", "2fa": "", mobile: "", recovery_email: "" };

export default function GoogleAccountsPanel({ active }: { active: boolean }) {
  const { t } = useTranslation("workspace");
  const [accounts, setAccounts] = useState<GoogleAccount[]>([]);
  const [loading, setLoading] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState(emptyForm);
  const [showPassword, setShowPassword] = useState(false);
  const [show2FA, setShow2FA] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [totpName, setTotpName] = useState<string | null>(null);
  const [totpValue, setTotpValue] = useState<TOTPValue>();
  const [totpLoading, setTotpLoading] = useState(false);
  const profileOptions = accounts.map((account) => account.profile);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await apiService.getGoogleAccounts();
      setAccounts(response.data?.accounts || []);
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
    setForm(emptyForm);
    setShowPassword(false);
    setShow2FA(false);
  };

  const beginEdit = async (account: GoogleAccount) => {
    setEditing(account.profile);
    setShowPassword(false);
    setShow2FA(false);
    const secrets = await apiService.getGoogleAccountSecrets(account.profile);
    setForm({
      profile: account.profile,
      email: account.email || "",
      password: secrets.data?.password || "",
      "2fa": secrets.data?.["2fa"] || "",
      mobile: account.mobile || "",
      recovery_email: account.recovery_email || "",
    });
  };

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await apiService.saveGoogleAccount(form);
      setEditing(null);
      await load();
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
    } finally {
      setSaving(false);
    }
  };
  const showTOTP = async (profile: string) => {
    setTotpName(profile); setTotpValue(undefined); setTotpLoading(true);
    try { const response = await apiService.getGoogleAccountTOTP(profile); setTotpValue({ capture: String(response.data?.capture || response.data?.code || ""), expiresAt: Date.now() + Number(response.data?.countdown || 0) * 1000 }); }
    catch (e: any) { setError(String(e?.response?.data?.detail || e?.message || e)); }
    finally { setTotpLoading(false); }
  };

  const inputClass = "w-full rounded-lg border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none focus:border-blue-500/40";
  return (
    <div data-id="google-accounts-panel" className={`h-full overflow-auto p-4 ${active ? "" : "hidden"}`}>
      <header data-id="google-accounts-header" className="mb-4 flex items-center gap-3 border-b border-white/[0.06] pb-3">
        <div className="min-w-0 flex-1">
          <h2 className="flex items-center gap-2 text-[13px] font-semibold text-zinc-100"><GoogleIcon className="h-4 w-4" />{t("googleAccountsTitle")}</h2>
          <p className="mt-1 text-[11px] text-zinc-500">{t("googleAccountsSubtitle")}</p>
        </div>
        <button data-id="google-account-add" onClick={beginNew} className="flex items-center gap-1.5 rounded-md bg-blue-500 px-2.5 py-1.5 text-[11px] font-medium text-white"><Plus className="h-3.5 w-3.5" />{t("githubAccountAdd")}</button>
      </header>
      {error && <div data-id="google-accounts-error" className="mb-3 rounded-lg border border-rose-500/20 bg-rose-500/[0.08] px-3 py-2 text-[12px] text-rose-300">{error}</div>}
      <AppModal open={editing !== null} title={editing ? t("googleAccountEditTitle", { profile: editing }) : t("googleAccountAddTitle")} onClose={() => setEditing(null)} maxWidth="580px">
        <section data-id="google-account-form" className="grid grid-cols-2 gap-3">
          <label><span className="mb-1 block text-[11px] text-zinc-500">Chrome Profile</span><div className="flex gap-2"><select data-id="google-account-profile-select" disabled={Boolean(editing)} value={form.profile} onChange={(e) => setForm((v) => ({ ...v, profile: e.target.value }))} className={`${inputClass} disabled:text-zinc-500`}><option value="">{t("selectChromeProfile")}</option>{profileOptions.map((profile) => <option key={profile} value={profile}>{profile}</option>)}</select><button data-id="google-account-open-chrome" type="button" disabled={!form.profile} onClick={() => void openChromeProfile(form.profile).catch((e) => setError(String(e?.message || e)))} className="rounded-lg border border-white/[0.08] px-3 text-zinc-400 disabled:opacity-30" title={t("openInChrome")}><ExternalLink className="h-4 w-4" /></button></div></label>
          <label><span className="mb-1 block text-[11px] text-zinc-500">Email</span><input data-id="google-account-email-input" type="email" value={form.email} onChange={(e) => setForm((v) => ({ ...v, email: e.target.value }))} className={inputClass} /></label>
          <label><span className="mb-1 block text-[11px] text-zinc-500">Mobile</span><input data-id="google-account-mobile-input" value={form.mobile} onChange={(e) => setForm((v) => ({ ...v, mobile: e.target.value }))} className={inputClass} /></label>
          <label><span className="mb-1 block text-[11px] text-zinc-500">{t("googleRecoveryEmail")}</span><input data-id="google-account-recovery-email-input" type="email" value={form.recovery_email} onChange={(e) => setForm((v) => ({ ...v, recovery_email: e.target.value }))} className={inputClass} /></label>
          <SecretField dataId="google-account-password" label="Password" value={form.password} visible={showPassword} onToggle={() => setShowPassword((v) => !v)} onChange={(password) => setForm((v) => ({ ...v, password }))} inputClass={inputClass} />
          <SecretField dataId="google-account-2fa" label="2FA" value={form["2fa"]} visible={show2FA} onToggle={() => setShow2FA((v) => !v)} onChange={(value) => setForm((v) => ({ ...v, "2fa": value }))} inputClass={inputClass} />
          <div className="col-span-2 mt-2 flex justify-end gap-2 border-t border-white/[0.07] pt-4">
            <button data-id="google-account-save" disabled={saving || !form.profile.trim() || !form.email.trim()} onClick={() => void save()} className="flex items-center gap-1.5 rounded-md bg-blue-500 px-3 py-2 text-[11px] text-white disabled:opacity-40">{saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}{t("settingsSave")}</button>
            <button data-id="google-account-cancel" onClick={() => setEditing(null)} className="rounded-md p-2 text-zinc-500"><X className="h-3.5 w-3.5" /></button>
          </div>
        </section>
      </AppModal>
      <div data-id="google-account-list" className="space-y-2">
        {loading ? <div className="flex justify-center py-12"><Loader2 className="h-5 w-5 animate-spin" /></div> : accounts.map((account) => (
          <article key={account.profile} data-id={`google-account-${account.profile}`} className="flex items-center gap-3 rounded-lg border border-white/[0.07] p-3">
            <span className="grid h-8 w-8 place-items-center rounded-md bg-white/[0.06]"><GoogleIcon className="h-4 w-4" /></span>
            <div className="min-w-0 flex-1"><div className="truncate text-[12px] font-medium text-zinc-100">{account.email || t("googleEmailMissing")}</div><div className="mt-0.5 flex flex-wrap gap-x-3 text-[10px] text-zinc-500"><span>{account.profile}</span>{account.mobile && <span className="flex items-center gap-1"><Smartphone className="h-3 w-3" />{account.mobile}</span>}{account["2fa_set"] ? <button data-id="google-account-generate-2fa" onClick={() => void showTOTP(account.profile)} className="flex items-center gap-1 text-amber-400"><KeyRound className="h-3 w-3" />2FA</button> : <span className="flex items-center gap-1"><KeyRound className="h-3 w-3" />2FA —</span>}</div></div>
            <button data-id="google-account-open-chrome" onClick={() => void openChromeProfile(account.profile).catch((e) => setError(String(e?.message || e)))} className="p-2 text-zinc-500 hover:text-zinc-200" title={t("openInChrome")}><ExternalLink className="h-3.5 w-3.5" /></button>
            <button data-id="google-account-edit" onClick={() => void beginEdit(account)} className="p-2 text-zinc-500 hover:text-zinc-200"><Pencil className="h-3.5 w-3.5" /></button>
          </article>
        ))}
      </div>
      <AccountTOTPModal name={totpName} value={totpValue} loading={totpLoading} onClose={() => setTotpName(null)} />
    </div>
  );
}

export function GoogleIcon({ className = "" }: { className?: string }) {
  return (
    <svg data-id="google-icon" className={className} viewBox="0 0 24 24" aria-hidden="true">
      <path fill="#4285F4" d="M21.6 12.23c0-.71-.06-1.4-.18-2.07H12v3.92h5.38a4.6 4.6 0 0 1-2 3.02v2.54h3.24c1.9-1.75 2.98-4.33 2.98-7.41Z" />
      <path fill="#34A853" d="M12 22c2.7 0 4.97-.9 6.62-2.36l-3.24-2.54c-.9.6-2.05.96-3.38.96-2.61 0-4.82-1.76-5.61-4.13H3.04v2.62A10 10 0 0 0 12 22Z" />
      <path fill="#FBBC05" d="M6.39 13.93A6.02 6.02 0 0 1 6.08 12c0-.67.11-1.32.31-1.93V7.45H3.04A10 10 0 0 0 2 12c0 1.61.38 3.14 1.04 4.55l3.35-2.62Z" />
      <path fill="#EA4335" d="M12 5.94c1.47 0 2.79.5 3.83 1.5l2.87-2.87A9.65 9.65 0 0 0 12 2a10 10 0 0 0-8.96 5.45l3.35 2.62C7.18 7.7 9.39 5.94 12 5.94Z" />
    </svg>
  );
}

export async function openChromeProfile(profile: string) {
  const match = /^profile_(\d+)$/.exec(profile);
  if (!match) throw new Error("Invalid Chrome profile");
  const clientsResponse = await apiService.getChatClients();
  const clients: any[] = Array.isArray(clientsResponse.data) ? clientsResponse.data : [];
  const client = clients.find((item) => item?.isElectron && item?.client_id);
  if (!client) throw new Error("CiCy Desktop is not connected");
  await apiService.chatPush({ client_id: client.client_id, type: "desktop_event", data: { type: "rpc_call", tool: "chrome_launch_profile", args: { accountIdx: Number(match[1]), url: "about:blank" } }, wait_ack: true, timeout_ms: 25000 });
}

function SecretField({ dataId, label, value, visible, onToggle, onChange, inputClass }: { dataId: string; label: string; value: string; visible: boolean; onToggle: () => void; onChange: (value: string) => void; inputClass: string }) {
  return <label data-id={`${dataId}-field`}><span className="mb-1 block text-[11px] text-zinc-500">{label}</span><div className="relative"><input data-id={`${dataId}-input`} type={visible ? "text" : "password"} value={value} onChange={(e) => onChange(e.target.value)} className={`${inputClass} pr-10`} autoComplete="new-password" /><button data-id={`${dataId}-toggle`} type="button" onClick={onToggle} className="absolute inset-y-0 right-0 px-3 text-zinc-500 hover:text-zinc-300">{visible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}</button></div></label>;
}
