import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { SlidersHorizontal, Globe, MessageCircle, Route, Boxes, X, Check, KeyRound, Mail, RefreshCw, Copy, Eye, EyeOff, AlertTriangle } from 'lucide-react';
import ProviderDashboard from '../providers/ProviderDashboard';
import IMDashboard from '../im/IMDashboard';
import apiService from '../../services/api';
import { TokenManager } from '../../services/tokenManager';

// Unified, productized Settings surface. One fullscreen modal with a left nav
// (Language / IM / Agent Routing / LLM Providers) and a large content area on
// the right — replaces the scattered activity-bar left-panels (providers/im)
// and the membership-popover language submenu. Opened from the bottom-left
// settings popover (entries above the version line).
export type SettingsSection = 'general' | 'language' | 'im' | 'routing' | 'providers';

interface NavItem {
  id: SettingsSection;
  label: string;
  icon: React.ReactNode;
}

export default function SettingsModal({
  open,
  section,
  onSection,
  onClose,
  currentLang,
  langs,
  onChangeLang,
  flagEmoji,
  langName,
  version,
  publicUrl,
  onSavePublicUrl,
}: {
  open: boolean;
  section: SettingsSection;
  onSection: (s: SettingsSection) => void;
  onClose: () => void;
  currentLang: string;
  langs: readonly string[];
  onChangeLang: (code: string) => void;
  flagEmoji: (code: string) => string;
  langName: (code: string) => string;
  version?: string;
  publicUrl: string;
  onSavePublicUrl: (url: string) => Promise<void>;
}) {
  // Split-pane mount nodes for the embedded dashboards (they render via portals).
  const [imLeft, setImLeft] = useState<HTMLElement | null>(null);
  const [imRight, setImRight] = useState<HTMLElement | null>(null);
  const [provLeft, setProvLeft] = useState<HTMLElement | null>(null);
  const [provRight, setProvRight] = useState<HTMLElement | null>(null);

  // General → Public URL editor (persisted to global.json public_url).
  const [urlDraft, setUrlDraft] = useState(publicUrl);
  const [savingUrl, setSavingUrl] = useState(false);
  const [savedUrl, setSavedUrl] = useState(false);
  // Re-seed the draft whenever the modal (re)opens or the saved value changes.
  useEffect(() => { setUrlDraft(publicUrl); setSavedUrl(false); }, [publicUrl, open]);
  const urlDirty = urlDraft.trim() !== publicUrl.trim();
  const savePublicUrl = async () => {
    setSavingUrl(true);
    setSavedUrl(false);
    try {
      await onSavePublicUrl(urlDraft.trim());
      setSavedUrl(true);
    } finally {
      setSavingUrl(false);
    }
  };

  // General → API token + Email (SMTP). The UI configures the SAME email.json the
  // `email` skill uses; refresh rotates the token and emails it (server gates on
  // SMTP being configured). Token rotation invalidates the current token, so on
  // success we persist the returned new token into TokenManager.
  const [emailCfg, setEmailCfg] = useState<any>(null);
  const [emailForm, setEmailForm] = useState({ host: '', port: 465, secure: true, user: '', pass: '', from: '', default_to: '' });
  const [emailSaving, setEmailSaving] = useState(false);
  const [emailSaved, setEmailSaved] = useState(false);
  const [apiToken, setApiToken] = useState('');
  const [tokenShown, setTokenShown] = useState(false);
  const [copied, setCopied] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshMsg, setRefreshMsg] = useState<{ kind: 'ok' | 'need-smtp' | 'err'; text: string } | null>(null);
  const sendReady = !!emailCfg?.send_ready;

  useEffect(() => {
    if (!open || section !== 'general') return;
    let alive = true;
    Promise.all([apiService.getEmailConfig(), apiService.getApiToken()])
      .then(([ec, tk]) => {
        if (!alive) return;
        const d = ec.data || {};
        setEmailCfg(d);
        const s = d.smtp || {};
        setEmailForm({ host: s.host || '', port: s.port || 465, secure: s.secure !== false, user: s.user || '', pass: '', from: s.from || '', default_to: d.default_to || '' });
        setApiToken(tk.data?.token || '');
      })
      .catch(() => {});
    return () => { alive = false; };
  }, [open, section]);

  const saveEmail = async () => {
    setEmailSaving(true);
    setEmailSaved(false);
    try {
      const payload: any = {
        smtp: { host: emailForm.host.trim(), port: Number(emailForm.port) || 465, secure: !!emailForm.secure, user: emailForm.user.trim(), from: emailForm.from.trim() },
        default_to: emailForm.default_to.trim(),
      };
      if (emailForm.pass.trim()) payload.smtp.pass = emailForm.pass;
      const r = await apiService.saveEmailConfig(payload);
      setEmailCfg(r.data?.config || null);
      setEmailForm((f) => ({ ...f, pass: '' }));
      setEmailSaved(true);
      setRefreshMsg(null);
    } finally {
      setEmailSaving(false);
    }
  };

  const refreshToken = async () => {
    if (!sendReady) { setRefreshMsg({ kind: 'need-smtp', text: t('settingsTokenNeedSmtp', { defaultValue: '请先配置下方的 SMTP,配置后才能刷新并把新 token 发到邮箱。' }) }); return; }
    setRefreshing(true);
    setRefreshMsg(null);
    try {
      const r = await apiService.refreshApiToken();
      const tok = r.data?.token;
      if (tok) { setApiToken(tok); TokenManager.saveToken(tok); }
      const to = r.data?.emailed_to;
      setRefreshMsg({ kind: 'ok', text: to ? t('settingsTokenEmailed', { defaultValue: '新 token 已发送至 {{to}}', to }) : t('settingsTokenRefreshed', { defaultValue: '已刷新' }) });
    } catch (e: any) {
      const code = e?.response?.data?.code;
      if (code === 'EMAIL_NOT_CONFIGURED' || code === 'EMAIL_NOT_INSTALLED') setRefreshMsg({ kind: 'need-smtp', text: t('settingsTokenNeedSmtp', { defaultValue: '请先配置下方的 SMTP,配置后才能刷新并把新 token 发到邮箱。' }) });
      else if (code === 'NO_RECIPIENT') setRefreshMsg({ kind: 'err', text: t('settingsTokenNoRecipient', { defaultValue: '未设置收件邮箱,请在下方 SMTP 配置里填写「收件人」。' }) });
      else setRefreshMsg({ kind: 'err', text: e?.response?.data?.detail || t('settingsTokenRefreshFailed', { defaultValue: '刷新失败' }) });
    } finally {
      setRefreshing(false);
    }
  };

  const copyToken = async () => {
    try { await navigator.clipboard.writeText(apiToken); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch {}
  };

  const inp = 'rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-[13px] text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-white/[0.18]';

  // ESC closes — capture phase so it wins over the embedded dashboards' own
  // key handlers (they only care about their internal editors).
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); onClose(); } };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [open, onClose]);

  const { t } = useTranslation('workspace');
  if (!open) return null;

  const nav: NavItem[] = [
    { id: 'general', label: t('settingsNavGeneral', { defaultValue: '通用' }), icon: <SlidersHorizontal className="h-4 w-4" /> },
    { id: 'language', label: t('settingsNavLanguage', { defaultValue: '语言' }), icon: <Globe className="h-4 w-4" /> },
    { id: 'im', label: t('settingsNavIM', { defaultValue: 'IM 通知' }), icon: <MessageCircle className="h-4 w-4" /> },
    { id: 'routing', label: t('settingsNavRouting', { defaultValue: 'Agent 路由' }), icon: <Route className="h-4 w-4" /> },
    { id: 'providers', label: t('settingsNavProviders', { defaultValue: 'LLM 供应商' }), icon: <Boxes className="h-4 w-4" /> },
  ];
  const isProviderSection = section === 'routing' || section === 'providers';

  return createPortal(
    <div
      data-id="settings-modal-overlay"
      className="fixed inset-0 z-[1000] flex bg-black/70 backdrop-blur-sm"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div
        data-id="settings-modal-card"
        className="absolute inset-4 flex flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#0b0b0d] shadow-[0_40px_120px_rgba(0,0,0,0.6)] lg:inset-10"
      >
        {/* header */}
        <div data-id="settings-modal-header" className="flex h-14 shrink-0 items-center justify-between border-b border-white/[0.06] px-5">
          <div data-id="settings-modal-title" className="text-sm font-semibold text-zinc-100">{t('settingsTitle', { defaultValue: '设置' })}</div>
          <button
            type="button"
            data-id="settings-modal-close"
            onClick={onClose}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            title={t('settingsClose', { defaultValue: '关闭' })}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* body: left nav + right content */}
        <div data-id="settings-modal-body" className="flex min-h-0 flex-1">
          {/* left nav */}
          <nav data-id="settings-modal-nav" className="flex w-52 shrink-0 flex-col gap-0.5 border-r border-white/[0.06] bg-[#09090b] p-2">
            {nav.map((item) => (
              <button
                key={item.id}
                type="button"
                data-id={`settings-modal-nav-${item.id}`}
                onClick={() => onSection(item.id)}
                className={`flex items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[13px] transition-colors ${
                  section === item.id
                    ? 'bg-white/[0.08] text-zinc-100'
                    : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                }`}
              >
                <span className="shrink-0">{item.icon}</span>
                <span data-id={`settings-modal-nav-${item.id}-label`} className="truncate">{item.label}</span>
              </button>
            ))}
            {version ? (
              <div data-id="settings-modal-version" className="mt-auto px-3 py-2 text-[10.5px] text-zinc-600">
                {t('membershipVersion', { defaultValue: '版本' })} {version}
              </div>
            ) : null}
          </nav>

          {/* right content */}
          <div data-id="settings-modal-content" className="relative min-h-0 flex-1 overflow-hidden bg-[#0b0b0d]">
            {/* General — public URL (drives the mobile QR) */}
            {section === 'general' && (
              <div data-id="settings-section-general" className="h-full overflow-auto p-6">
                <div className="mx-auto max-w-md">
                  <div className="mb-1 text-[13px] font-semibold text-zinc-200">{t('settingsPublicUrlTitle', { defaultValue: '公网访问地址' })}</div>
                  <div className="mb-4 text-[11px] leading-5 text-zinc-500">{t('settingsPublicUrlHint', { defaultValue: '本机对外可达的地址(隧道域名或局域网 IP)。配置后右下角会出现「扫码上手机」二维码;留空则隐藏。' })}</div>
                  <input
                    data-id="settings-public-url-input"
                    type="text"
                    value={urlDraft}
                    onChange={(e) => { setUrlDraft(e.target.value); setSavedUrl(false); }}
                    onKeyDown={(e) => { if (e.key === 'Enter' && urlDirty && !savingUrl) void savePublicUrl(); }}
                    placeholder="https://app-xxxx.example.com"
                    spellCheck={false}
                    autoComplete="off"
                    className="w-full rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-[13px] text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-white/[0.18]"
                  />
                  <div className="mt-3 flex items-center gap-2">
                    <button
                      type="button"
                      data-id="settings-public-url-save"
                      disabled={!urlDirty || savingUrl}
                      onClick={() => void savePublicUrl()}
                      className={`rounded-lg px-3.5 py-2 text-[12px] font-semibold transition-colors ${
                        urlDirty && !savingUrl
                          ? 'bg-sky-500/90 text-white hover:bg-sky-500'
                          : 'cursor-not-allowed bg-white/[0.05] text-zinc-600'
                      }`}
                    >
                      {savingUrl ? t('settingsSaving', { defaultValue: '保存中…' }) : t('settingsSave', { defaultValue: '保存' })}
                    </button>
                    {savedUrl && !urlDirty ? (
                      <span data-id="settings-public-url-saved" className="flex items-center gap-1 text-[11px] text-emerald-400">
                        <Check className="h-3.5 w-3.5" />{t('settingsSaved', { defaultValue: '已保存' })}
                      </span>
                    ) : null}
                  </div>

                  {/* API Token — show + rotate (rotation emails the new token) */}
                  <div data-id="settings-token-block" className="mt-8 border-t border-white/[0.06] pt-6">
                    <div className="mb-1 flex items-center gap-1.5 text-[13px] font-semibold text-zinc-200">
                      <KeyRound className="h-3.5 w-3.5 text-zinc-400" />{t('settingsTokenTitle', { defaultValue: 'API Token' })}
                    </div>
                    <div className="mb-3 text-[11px] leading-5 text-zinc-500">{t('settingsTokenHint', { defaultValue: '本机 API 访问令牌。刷新会作废旧令牌,并把新令牌发到下方配置的收件邮箱。' })}</div>
                    <div className="flex items-center gap-2">
                      <input
                        data-id="settings-token-value"
                        readOnly
                        value={tokenShown ? apiToken : (apiToken ? '•'.repeat(Math.min(40, apiToken.length)) : '')}
                        className={`${inp} min-w-0 flex-1 font-mono !text-[12px]`}
                      />
                      <button data-id="settings-token-toggle" type="button" onClick={() => setTokenShown((v) => !v)} className="rounded-lg border border-white/[0.08] bg-white/[0.03] p-2 text-zinc-400 transition-colors hover:text-zinc-200">
                        {tokenShown ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                      </button>
                      <button data-id="settings-token-copy" type="button" onClick={() => void copyToken()} className="rounded-lg border border-white/[0.08] bg-white/[0.03] p-2 text-zinc-400 transition-colors hover:text-zinc-200">
                        {copied ? <Check className="h-4 w-4 text-emerald-400" /> : <Copy className="h-4 w-4" />}
                      </button>
                    </div>
                    <div className="mt-3 flex items-center gap-2">
                      <button
                        data-id="settings-token-refresh"
                        type="button"
                        disabled={refreshing}
                        onClick={() => void refreshToken()}
                        className={`flex items-center gap-1.5 rounded-lg px-3.5 py-2 text-[12px] font-semibold transition-colors ${refreshing ? 'cursor-not-allowed bg-white/[0.05] text-zinc-600' : 'bg-amber-500/90 text-white hover:bg-amber-500'}`}
                      >
                        <RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />{refreshing ? t('settingsTokenRefreshing', { defaultValue: '刷新中…' }) : t('settingsTokenRefresh', { defaultValue: '刷新令牌' })}
                      </button>
                      {refreshMsg ? (
                        <span data-id="settings-token-msg" className={`flex items-center gap-1 text-[11px] ${refreshMsg.kind === 'ok' ? 'text-emerald-400' : refreshMsg.kind === 'need-smtp' ? 'text-amber-400' : 'text-rose-400'}`}>
                          {refreshMsg.kind === 'ok' ? <Check className="h-3.5 w-3.5" /> : <AlertTriangle className="h-3.5 w-3.5" />}{refreshMsg.text}
                        </span>
                      ) : null}
                    </div>
                  </div>

                  {/* Email (SMTP) — same ~/cicy-ai/db/email.json the email skill uses */}
                  <div data-id="settings-email-block" className="mt-8 border-t border-white/[0.06] pt-6">
                    <div className="mb-1 flex items-center gap-1.5 text-[13px] font-semibold text-zinc-200">
                      <Mail className="h-3.5 w-3.5 text-zinc-400" />{t('settingsEmailTitle', { defaultValue: '邮件 (SMTP)' })}
                      {emailCfg ? (
                        <span data-id="settings-email-status" className={`ml-1 rounded px-1.5 py-0.5 text-[10px] font-medium ${sendReady ? 'bg-emerald-500/15 text-emerald-400' : 'bg-zinc-500/15 text-zinc-400'}`}>
                          {sendReady ? t('settingsEmailReady', { defaultValue: '已配置' }) : t('settingsEmailUnset', { defaultValue: '未配置' })}
                        </span>
                      ) : null}
                    </div>
                    <div className="mb-3 text-[11px] leading-5 text-zinc-500">{t('settingsEmailHint', { defaultValue: '用于刷新令牌时把新令牌发到你的邮箱。填写邮箱服务商的 SMTP 信息(如 QQ 邮箱的授权码)。' })}</div>
                    <div className="space-y-2">
                      <input data-id="settings-email-host" placeholder={t('settingsEmailHost', { defaultValue: 'SMTP 服务器 (如 smtp.qq.com)' })} value={emailForm.host} onChange={(e) => setEmailForm((f) => ({ ...f, host: e.target.value }))} className={`${inp} w-full`} spellCheck={false} autoComplete="off" />
                      <div className="flex items-center gap-3">
                        <input data-id="settings-email-port" type="number" placeholder="465" value={emailForm.port} onChange={(e) => setEmailForm((f) => ({ ...f, port: Number(e.target.value) }))} className={`${inp} w-24`} />
                        <label className="flex items-center gap-1.5 text-[12px] text-zinc-400">
                          <input data-id="settings-email-secure" type="checkbox" checked={emailForm.secure} onChange={(e) => setEmailForm((f) => ({ ...f, secure: e.target.checked }))} />
                          {t('settingsEmailSecure', { defaultValue: '隐式 TLS (465);587 请取消勾选' })}
                        </label>
                      </div>
                      <input data-id="settings-email-user" placeholder={t('settingsEmailUser', { defaultValue: '用户名 (邮箱地址)' })} value={emailForm.user} onChange={(e) => setEmailForm((f) => ({ ...f, user: e.target.value }))} className={`${inp} w-full`} spellCheck={false} autoComplete="off" />
                      <input data-id="settings-email-pass" type="password" placeholder={emailCfg?.smtp?.pass_set ? t('settingsEmailPassSet', { defaultValue: '密码已设置 (留空保持不变)' }) : t('settingsEmailPass', { defaultValue: '密码 / 授权码' })} value={emailForm.pass} onChange={(e) => setEmailForm((f) => ({ ...f, pass: e.target.value }))} className={`${inp} w-full`} autoComplete="new-password" />
                      <input data-id="settings-email-from" placeholder={t('settingsEmailFrom', { defaultValue: '发件人 (如 CiCy <you@qq.com>)' })} value={emailForm.from} onChange={(e) => setEmailForm((f) => ({ ...f, from: e.target.value }))} className={`${inp} w-full`} spellCheck={false} autoComplete="off" />
                      <input data-id="settings-email-to" placeholder={t('settingsEmailTo', { defaultValue: '收件人 (token 发到这个邮箱)' })} value={emailForm.default_to} onChange={(e) => setEmailForm((f) => ({ ...f, default_to: e.target.value }))} className={`${inp} w-full`} spellCheck={false} autoComplete="off" />
                    </div>
                    <div className="mt-3 flex items-center gap-2">
                      <button data-id="settings-email-save" type="button" disabled={emailSaving} onClick={() => void saveEmail()} className={`rounded-lg px-3.5 py-2 text-[12px] font-semibold transition-colors ${emailSaving ? 'cursor-not-allowed bg-white/[0.05] text-zinc-600' : 'bg-sky-500/90 text-white hover:bg-sky-500'}`}>
                        {emailSaving ? t('settingsSaving', { defaultValue: '保存中…' }) : t('settingsSave', { defaultValue: '保存' })}
                      </button>
                      {emailSaved ? (
                        <span data-id="settings-email-saved" className="flex items-center gap-1 text-[11px] text-emerald-400"><Check className="h-3.5 w-3.5" />{t('settingsSaved', { defaultValue: '已保存' })}</span>
                      ) : null}
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* Language */}
            {section === 'language' && (
              <div data-id="settings-section-language" className="h-full overflow-auto p-6">
                <div className="mx-auto max-w-md">
                  <div className="mb-1 text-[13px] font-semibold text-zinc-200">{t('settingsNavLanguage', { defaultValue: '语言' })}</div>
                  <div className="mb-4 text-[11px] text-zinc-500">{t('settingsLanguageHint', { defaultValue: '选择界面语言,立即生效。' })}</div>
                  <div className="space-y-1">
                    {langs.map((code) => {
                      const active = currentLang === code;
                      return (
                        <button
                          key={code}
                          type="button"
                          data-id={`settings-language-${code}`}
                          onClick={() => { if (!active) onChangeLang(code); }}
                          className={`flex w-full items-center justify-between gap-2 rounded-lg border px-3 py-2.5 text-left text-[13px] transition-colors ${
                            active
                              ? 'border-emerald-500/30 bg-emerald-500/[0.06] text-zinc-100'
                              : 'border-white/[0.06] bg-white/[0.02] text-zinc-300 hover:border-white/[0.12] hover:bg-white/[0.04]'
                          }`}
                        >
                          <span className="flex min-w-0 items-center gap-2">
                            <span aria-hidden className="text-[15px] leading-none">{flagEmoji(code)}</span>
                            <span className="truncate">{langName(code)}</span>
                          </span>
                          {active ? <Check className="h-3.5 w-3.5 shrink-0 text-emerald-400" /> : null}
                        </button>
                      );
                    })}
                  </div>
                </div>
              </div>
            )}

            {/* IM — split pane hosts the existing IMDashboard via mount nodes */}
            <div data-id="settings-section-im" className={`flex h-full ${section === 'im' ? '' : 'hidden'}`}>
              <div ref={setImLeft} data-id="settings-im-left" className="h-full w-[340px] shrink-0 border-r border-white/[0.06]" />
              <div ref={setImRight} data-id="settings-im-right" className="h-full min-w-0 flex-1" />
            </div>

            {/* Routing + Providers — both backed by ProviderDashboard (tab driven by nav) */}
            <div data-id="settings-section-providers" className={`flex h-full ${isProviderSection ? '' : 'hidden'}`}>
              <div ref={setProvLeft} data-id="settings-prov-left" className="h-full w-[360px] shrink-0 border-r border-white/[0.06]" />
              <div data-id="settings-prov-right" className="relative h-full min-w-0 flex-1">
                <div ref={setProvRight} className="absolute inset-0" />
                {section === 'routing' ? (
                  <div data-id="settings-routing-hint" className="pointer-events-none absolute inset-0 flex items-center justify-center p-8 text-center">
                    <div className="max-w-xs text-[12px] leading-5 text-zinc-600">
                      {t('settingsRoutingHint', { defaultValue: '在左侧为每个 Agent 类型指定默认供应商与模型。供应商在「LLM 供应商」里维护。' })}
                    </div>
                  </div>
                ) : null}
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Embedded dashboards (mount only when their section is/has been opened). */}
      {section === 'im' && <IMDashboard leftMount={imLeft} rightMount={imRight} />}
      {isProviderSection && (
        <ProviderDashboard leftMount={provLeft} rightMount={provRight} tab={section === 'routing' ? 'routing' : 'providers'} hideTabStrip />
      )}
    </div>,
    document.body,
  );
}
