import { useEffect, useState } from "react";
import {
  Cloud,
  Bot,
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
import GithubAccountsPanel from "./GithubAccountsPanel";
import GoogleAccountsPanel, { GoogleIcon, openChromeProfile } from "./GoogleAccountsPanel";
import ChatGPTAccountsPanel from "./ChatGPTAccountsPanel";
import { AppModal, useDialogs } from "../ui/Modal";

type Platform = "github" | "cloudflare" | "google" | "chatgpt";
type CFAccount = {
  name: string;
  label: string;
  kind: string;
  username: string;
  email: string;
  password_set: boolean;
  profile: string;
  account_id: string;
  target: string;
  token_set: boolean;
  is_default: boolean;
  details?: Record<string, string>;
};
const workerDetailKeys = ["zone", "zone_id"] as const;
const r2DetailKeys = ["bucket", "public_url"] as const;

export default function AccountMatrixPanel({
  active,
  paneId,
}: {
  active: boolean;
  paneId: string;
}) {
  const { t } = useTranslation("workspace");
  const [platform, setPlatform] = useState<Platform>("github");
  const [accounts, setAccounts] = useState<CFAccount[]>([]);
  const [loading, setLoading] = useState(false);
  const [sending, setSending] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState({
    name: "",
    label: "",
    kind: "",
    username: "",
    email: "",
    password: "",
    profile: "",
    account_id: "",
    api_token: "",
    is_default: false,
    details: {} as Record<string, string>,
  });
  const [testing, setTesting] = useState("");
  const [showToken, setShowToken] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [profiles, setProfiles] = useState<string[]>([]);
  const { confirm, node: dialogsNode } = useDialogs();
  const load = () => {
    setLoading(true);
    return Promise.all([apiService.getCloudflareAccounts(), apiService.getGoogleAccounts()])
      .then(([r, profileResponse]) => { setAccounts(r.data?.accounts || []); setProfiles((profileResponse.data?.accounts || []).map((item: any) => String(item.profile))); })
      .finally(() => setLoading(false));
  };
  useEffect(() => {
    if (active && platform === "cloudflare") void load();
  }, [active, platform]);
  const send = async (name: string) => {
    setSending(name);
    try {
      await apiService.sendCommand(
        paneId,
        t("cloudflareAccountAgentPrompt", { name }),
        true,
      );
      window.dispatchEvent(
        new CustomEvent("show-toast", {
          detail: t("accountMatrixSent", { name }),
        }),
      );
    } finally {
      setSending("");
    }
  };
  const beginNew = () => {
    setEditing("");
    setShowToken(false);
    setShowPassword(false);
    setForm({
      name: "",
      label: "",
      kind: "workers",
      username: "",
      email: "",
      password: "",
      profile: "",
      account_id: "",
      api_token: "",
      is_default: false,
      details: {},
    });
  };
  const beginEdit = async (a: CFAccount) => {
    setEditing(a.name);
    setShowToken(false);
    setShowPassword(false);
    const secrets = await apiService.getCloudflareAccountToken(a.name);
    setForm({
      name: a.name,
      label: a.label || "",
      kind: a.kind || "",
      username: a.username || "",
      email: a.email || "",
      password: secrets.data?.password || "",
      profile: a.profile || "",
      account_id: a.account_id || "",
      api_token: secrets.data?.api_token || "",
      is_default: a.is_default,
      details: a.details || {},
    });
  };
  const save = async () => {
    await apiService.saveCloudflareAccount({
      name: form.name,
      label: form.label,
      username: form.username,
      email: form.email,
      password: form.password,
      profile: form.profile,
      account_id: form.account_id,
      is_default: form.is_default,
      details: form.details,
      old_name: editing || undefined,
      api_token: form.api_token || undefined,
    });
    setEditing(null);
    await load();
  };
  const remove = async (name: string) => {
    const ok = await confirm({
      title: t("deleteAccountTitle"),
      body: t("deleteAccountBody", { name }),
      danger: true,
      confirmLabel: t("delete", { defaultValue: "删除" }),
    });
    if (!ok) return;
    await apiService.deleteCloudflareAccount(name);
    await load();
  };
  const test = async (name: string) => {
    setTesting(name);
    try {
      await apiService.testCloudflareAccount(name);
      window.dispatchEvent(
        new CustomEvent("show-toast", { detail: t("cloudflareAccountTestOk") }),
      );
    } finally {
      setTesting("");
    }
  };
  return (
    <div
      data-id="account-matrix-panel"
      className={`h-full min-h-0 overflow-hidden ${active ? "" : "hidden"}`}
    >
      <div data-id="account-matrix-body" className="flex h-full min-h-0">
        <aside
          data-id="account-matrix-platform-tabs"
          className="flex w-14 shrink-0 flex-col items-center gap-1.5 border-r border-white/[0.06] py-3"
        >
          <button
            data-id="account-matrix-platform-chatgpt"
            title="ChatGPT"
            aria-label="ChatGPT"
            onClick={() => setPlatform("chatgpt")}
            className={`grid h-9 w-9 place-items-center rounded-lg border ${platform === "chatgpt" ? "border-emerald-400/30 bg-emerald-400/10 text-emerald-300" : "border-transparent text-zinc-500 hover:bg-white/[0.04]"}`}
          >
            <Bot className="h-4 w-4" />
          </button>
          <button
            data-id="account-matrix-platform-google"
            title="Google"
            aria-label="Google"
            onClick={() => setPlatform("google")}
            className={`grid h-9 w-9 place-items-center rounded-lg border ${platform === "google" ? "border-blue-400/30 bg-blue-400/10 text-blue-300" : "border-transparent text-zinc-500 hover:bg-white/[0.04]"}`}
          >
            <GoogleIcon className="h-4 w-4" />
          </button>
          <button
            data-id="account-matrix-platform-github"
            title="GitHub"
            aria-label="GitHub"
            onClick={() => setPlatform("github")}
            className={`grid h-9 w-9 place-items-center rounded-lg border ${platform === "github" ? "border-white/15 bg-white/[0.08] text-white" : "border-transparent text-zinc-500 hover:bg-white/[0.04]"}`}
          >
            <Github className="h-4 w-4" />
          </button>
          <button
            data-id="account-matrix-platform-cloudflare"
            title="Cloudflare"
            aria-label="Cloudflare"
            onClick={() => setPlatform("cloudflare")}
            className={`grid h-9 w-9 place-items-center rounded-lg border ${platform === "cloudflare" ? "border-orange-400/30 bg-orange-400/10 text-orange-300" : "border-transparent text-zinc-500 hover:bg-white/[0.04]"}`}
          >
            <Cloud className="h-4 w-4" />
          </button>
        </aside>
        <main
          data-id="account-matrix-content"
          className="min-w-0 flex-1 overflow-auto"
        >
          <div
            data-id="account-matrix-github"
            className="h-full"
            style={{ display: platform === "github" ? "block" : "none" }}
          >
            <GithubAccountsPanel
              active={active && platform === "github"}
              paneId={paneId}
            />
          </div>
          <div data-id="account-matrix-google" className="h-full" style={{ display: platform === "google" ? "block" : "none" }}>
            <GoogleAccountsPanel active={active && platform === "google"} />
          </div>
          <div data-id="account-matrix-chatgpt" className="h-full" style={{ display: platform === "chatgpt" ? "block" : "none" }}>
            <ChatGPTAccountsPanel active={active && platform === "chatgpt"} />
          </div>
          {platform === "cloudflare" && (
            <div data-id="account-matrix-cloudflare" className="p-4">
              <header
                data-id="cloudflare-accounts-header"
                className="mb-4 flex items-center gap-3 border-b border-white/[0.06] pb-3"
              >
                <div className="min-w-0 flex-1">
                  <h2 className="flex items-center gap-2 text-[13px] font-semibold text-zinc-100">
                    <Cloud className="h-4 w-4 text-orange-400" />
                    {t("cloudflareAccountsTitle")}
                  </h2>
                  <p className="mt-1 text-[11px] text-zinc-500">
                    {t("cloudflareAccountsSubtitle")}
                  </p>
                </div>
                <button
                  data-id="cloudflare-account-add"
                  onClick={beginNew}
                  className="flex items-center gap-1.5 rounded-md bg-orange-500 px-2.5 py-1.5 text-[11px] font-medium text-black"
                >
                  <Plus className="h-3.5 w-3.5" />
                  {t("githubAccountAdd")}
                </button>
              </header>
              <AppModal open={editing !== null} title={editing ? t("cloudflareAccountEditTitle", { name: editing }) : t("githubAccountAdd")} onClose={() => setEditing(null)} maxWidth="620px">
                <section data-id="cloudflare-account-form">
                  <div className="mb-2 text-[11px] font-medium text-zinc-400">{t("cloudflareSectionBasic")}</div>
                  <div className="grid grid-cols-2 gap-3">
                    {(["name", "label"] as const).map((key) => <label key={key}><span className="mb-1 block text-[11px] text-zinc-500">{t(`cloudflareField_${key}`)}</span><input data-id={`cloudflare-account-${key}-input`} value={form[key]} readOnly={key === "name" && form.email.includes("@")} onChange={(e) => setForm((v) => ({ ...v, [key]: e.target.value }))} className="w-full rounded-md border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none focus:border-orange-500/40 read-only:cursor-not-allowed read-only:text-zinc-500" /></label>)}
                  </div>
                  <div className="my-4 border-t border-white/[0.06]" />
                  <label className="mb-3 block"><span className="mb-1 block text-[11px] text-zinc-500">Chrome Profile</span><div className="flex gap-2"><select data-id="cloudflare-account-profile-select" value={form.profile} onChange={(e) => setForm((v) => ({ ...v, profile: e.target.value }))} className="w-full rounded-md border border-white/[0.08] bg-[#111113] px-3 py-2 text-[12px] text-zinc-200 outline-none"><option value="">{t("selectChromeProfile")}</option>{profiles.map((profile) => <option key={profile} value={profile}>{profile}</option>)}</select><button data-id="cloudflare-account-open-chrome" type="button" disabled={!form.profile} onClick={() => void openChromeProfile(form.profile)} className="rounded-md border border-white/[0.08] px-3 text-zinc-400 disabled:opacity-30" title={t("openInChrome")}><ExternalLink className="h-4 w-4" /></button></div></label>
                  <div className="mb-2 text-[11px] font-medium text-zinc-400">{t("cloudflareSectionLogin")}</div>
                  <div className="grid grid-cols-2 gap-3">
                    {(["username", "email"] as const).map((key) => <label key={key}><span className="mb-1 block text-[11px] text-zinc-500">{t(`cloudflareField_${key}`)}</span><input data-id={`cloudflare-account-${key}-input`} value={form[key]} onChange={(e) => { const value = e.target.value; setForm((v) => ({ ...v, [key]: value, ...(key === "email" && value.includes("@") ? { name: value.split("@")[0] } : {}) })) }} className="w-full rounded-md border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none focus:border-orange-500/40" /></label>)}
                    <label className="col-span-2"><span className="mb-1 block text-[11px] text-zinc-500">{t("cloudflareField_password")}</span><div className="relative"><input data-id="cloudflare-account-password-input" type={showPassword ? "text" : "password"} value={form.password} onChange={(e) => setForm((v) => ({ ...v, password: e.target.value }))} className="w-full rounded-md border border-white/[0.08] bg-black/30 px-3 py-2 pr-10 text-[12px] text-zinc-200 outline-none focus:border-orange-500/40" /><button data-id="cloudflare-account-password-toggle" type="button" onClick={() => setShowPassword((v) => !v)} className="absolute inset-y-0 right-0 px-3 text-zinc-500 hover:text-zinc-300">{showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}</button></div></label>
                  </div>
                  <div className="my-4 border-t border-white/[0.06]" />
                  <div className="mb-2 flex items-center justify-between"><span className="text-[11px] font-medium text-zinc-400">{t("cloudflareSectionCloudflare")}</span><select data-id="cloudflare-account-template" value={form.kind} onChange={(e) => setForm((v) => ({ ...v, kind: e.target.value }))} className="rounded-md border border-white/[0.08] bg-[#111113] px-2 py-1 text-[11px] text-zinc-300 outline-none"><option value="workers">Workers</option><option value="r2">R2</option></select></div>
                  <label className="mb-3 block"><span className="mb-1 block text-[11px] text-zinc-500">{t("cloudflareField_account_id")}</span><input data-id="cloudflare-account-account_id-input" value={form.account_id} onChange={(e) => setForm((v) => ({ ...v, account_id: e.target.value }))} className="w-full rounded-md border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none focus:border-orange-500/40" /></label>
                  <div data-id="cloudflare-account-details" className="grid grid-cols-2 gap-3">
                    {(form.kind === "r2" ? r2DetailKeys : form.kind === "general" ? [] : workerDetailKeys).map((key) => <label key={key}><span className="mb-1 block text-[11px] text-zinc-500">{t(`cloudflareField_${key}`)}</span><input data-id={`cloudflare-account-${key}-input`} value={form.details[key] || ""} onChange={(e) => setForm((v) => ({ ...v, details: { ...v.details, [key]: e.target.value } }))} className="w-full rounded-md border border-white/[0.08] bg-black/30 px-3 py-2 text-[12px] text-zinc-200 outline-none focus:border-orange-500/40" /></label>)}
                  </div>
                  <label className="mt-3 block"><span className="mb-1 block text-[11px] text-zinc-500">API Token</span><div className="relative">
                    <input
                      data-id="cloudflare-account-token-input"
                      type={showToken ? "text" : "password"}
                      value={form.api_token}
                      onChange={(e) =>
                        setForm((v) => ({ ...v, api_token: e.target.value }))
                      }
                      placeholder={
                        editing ? t("githubAccountTokenKeep") : "API Token"
                      }
                      className="w-full rounded-md border border-white/[0.08] bg-black/30 px-3 py-2 pr-10 text-[12px] text-zinc-200 outline-none"
                    />
                    <button
                      data-id="cloudflare-account-token-toggle"
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
                  </div></label>
                  <label className="mt-2 flex items-center gap-2 text-[11px] text-zinc-400">
                    <input
                      type="checkbox"
                      checked={form.is_default}
                      onChange={(e) =>
                        setForm((v) => ({ ...v, is_default: e.target.checked }))
                      }
                    />
                    DEFAULT
                  </label>
                  <div className="sticky bottom-0 mt-4 flex justify-end gap-2 border-t border-white/[0.07] bg-[#161618] pt-4">
                    <button
                      data-id="cloudflare-account-save"
                      onClick={() => void save()}
                      disabled={!form.name || (!editing && !form.api_token)}
                      className="flex items-center gap-1 rounded-md bg-orange-500 px-2.5 py-1.5 text-[11px] text-black disabled:opacity-40"
                    >
                      <Save className="h-3.5 w-3.5" />
                      {t("settingsSave")}
                    </button>
                    <button
                      data-id="cloudflare-account-cancel"
                      onClick={() => setEditing(null)}
                      className="rounded-md p-2 text-zinc-500"
                    >
                      <X className="h-3.5 w-3.5" />
                    </button>
                  </div>
                </section>
              </AppModal>
              <div className="space-y-2">
                {loading ? (
                  <div className="flex justify-center py-12">
                    <Loader2 className="h-5 w-5 animate-spin" />
                  </div>
                ) : (
                  accounts.map((a) => (
                    <article
                      key={a.name}
                      data-id={`cloudflare-account-${a.name}`}
                      className="flex items-center gap-3 rounded-lg border border-white/[0.07] p-3"
                    >
                      <span className="grid h-8 w-8 place-items-center rounded-md bg-orange-500/10 text-orange-400">
                        <Cloud className="h-4 w-4" />
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="text-[12px] font-medium text-zinc-100">
                          {a.label || a.name}
                          {a.is_default && (
                            <span className="ml-2 text-[10px] text-orange-400">
                              DEFAULT
                            </span>
                          )}
                        </div>
                        <div className="truncate text-[11px] text-zinc-500">
                          {a.name} · {a.kind || "cloudflare"}
                          {a.target ? ` · ${a.target}` : ""} ·{" "}
                          {a.token_set
                            ? t("accountTokenConfigured")
                            : t("githubAccountTokenMissing")}
                        </div>
                      </div>
                      <button
                        data-id="cloudflare-account-test"
                        onClick={() => void test(a.name)}
                        className="rounded-md border border-white/[0.08] px-2.5 py-1.5 text-[11px] text-zinc-300"
                      >
                        {testing === a.name ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                          t("githubAccountTest")
                        )}
                      </button>
                      <button
                        data-id="cloudflare-account-send-to-agent"
                        onClick={() => void send(a.name)}
                        disabled={!paneId || sending === a.name}
                        className="rounded-md border border-sky-500/20 bg-sky-500/[0.08] p-2 text-sky-400 disabled:opacity-40"
                      >
                        {sending === a.name ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                          <Send className="h-3.5 w-3.5" />
                        )}
                      </button>
                      <button
                        data-id="cloudflare-account-open-chrome"
                        onClick={() => void openChromeProfile(a.profile)}
                        disabled={!a.profile}
                        className="p-2 text-zinc-500 disabled:opacity-20"
                        title={t("openInChrome")}
                      >
                        <ExternalLink className="h-3.5 w-3.5" />
                      </button>
                      <button
                        data-id="cloudflare-account-edit"
                        onClick={() => void beginEdit(a)}
                        className="p-2 text-zinc-500"
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </button>
                      <button
                        data-id="cloudflare-account-delete"
                        onClick={() => void remove(a.name)}
                        className="p-2 text-zinc-500 hover:text-rose-300"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </article>
                  ))
                )}
              </div>
            </div>
          )}
        </main>
      </div>
      {dialogsNode}
    </div>
  );
}
