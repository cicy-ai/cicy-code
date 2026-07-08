// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { SlidersHorizontal, Globe, MessageCircle, Route, Boxes, X, Check, KeyRound, Mail, RefreshCw, Copy, Eye, EyeOff, AlertTriangle, Paperclip } from 'lucide-react';
import { useApp } from '../../contexts/AppContext';
import ProviderDashboard from '../providers/ProviderDashboard';
import IMDashboard from '../im/IMDashboard';
import apiService from '../../services/api';
import { TokenManager } from '../../services/tokenManager';
import { useDialogs } from '../ui/Modal';

// Unified, productized Settings surface. One fullscreen modal with a left nav
// (Language / IM / Agent Routing / LLM Providers) and a large content area on
// the right — replaces the scattered activity-bar left-panels (providers/im)
// and the membership-popover language submenu. Opened from the bottom-left
// settings popover (entries above the version line).
export type SettingsSection = 'general' | 'language' | 'im' | 'routing' | 'providers';

// Common email providers → their SMTP/IMAP/POP3 servers. Typing an account whose
// domain matches one of these auto-fills the servers, so the user only enters the
// address + auth code. Unknown domains fall back to manual entry.
interface MailProvider { label: string; host: string; port: number; secure: boolean; imap: string; pop3: string; }
const EMAIL_PROVIDERS: Record<string, MailProvider> = {
  'qq.com': { label: 'QQ 邮箱', host: 'smtp.qq.com', port: 465, secure: true, imap: 'imap.qq.com', pop3: 'pop.qq.com' },
  'foxmail.com': { label: 'Foxmail', host: 'smtp.qq.com', port: 465, secure: true, imap: 'imap.qq.com', pop3: 'pop.qq.com' },
  'gmail.com': { label: 'Gmail', host: 'smtp.gmail.com', port: 465, secure: true, imap: 'imap.gmail.com', pop3: 'pop.gmail.com' },
  '163.com': { label: '163 邮箱', host: 'smtp.163.com', port: 465, secure: true, imap: 'imap.163.com', pop3: 'pop.163.com' },
  '126.com': { label: '126 邮箱', host: 'smtp.126.com', port: 465, secure: true, imap: 'imap.126.com', pop3: 'pop.126.com' },
  'outlook.com': { label: 'Outlook', host: 'smtp.office365.com', port: 587, secure: false, imap: 'outlook.office365.com', pop3: 'outlook.office365.com' },
  'hotmail.com': { label: 'Hotmail', host: 'smtp.office365.com', port: 587, secure: false, imap: 'outlook.office365.com', pop3: 'outlook.office365.com' },
  'icloud.com': { label: 'iCloud', host: 'smtp.mail.me.com', port: 587, secure: false, imap: 'imap.mail.me.com', pop3: 'imap.mail.me.com' },
};
const emailDomain = (addr: string): string => {
  const m = /@([^@\s]+)$/.exec((addr || '').trim().toLowerCase());
  return m ? m[1] : '';
};
const providerPreset = (addr: string): MailProvider | null => EMAIL_PROVIDERS[emailDomain(addr)] || null;
const providerLabel = (addr: string): string => (providerPreset(addr)?.label || '');
const isPresetHost = (host: string): boolean => Object.values(EMAIL_PROVIDERS).some((p) => p.host === host);
// Leftover scaffold placeholder like "<paste-smtp-host>" — treat as unset.
const isEmailPlaceholder = (s: any): boolean => /^<paste/.test(String(s || '').trim());

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

  // General → 附件上传上限(MB)。持久化在 global.json max_attachment_mb(通用配置),
  // DispatcherChat 运行时读它约束上传。
  const { globalVar, updateGlobalVar } = useApp();
  const currentMaxAttach = Number(globalVar?.max_attachment_mb) || 100;
  const [attachDraft, setAttachDraft] = useState(String(currentMaxAttach));
  const [attachSaving, setAttachSaving] = useState(false);
  const [attachSaved, setAttachSaved] = useState(false);
  const attachClean = Math.max(1, Math.round(Number(attachDraft) || 0));
  const attachDirty = attachClean !== currentMaxAttach;
  useEffect(() => {
    if (open && section === 'general') { setAttachDraft(String(currentMaxAttach)); setAttachSaved(false); }
  }, [open, section, currentMaxAttach]);
  const saveMaxAttach = async () => {
    setAttachSaving(true);
    setAttachSaved(false);
    try {
      await updateGlobalVar({ max_attachment_mb: attachClean });
      setAttachDraft(String(attachClean));
      setAttachSaved(true);
    } finally {
      setAttachSaving(false);
    }
  };
  // Re-seed the draft whenever the modal (re)opens or the saved value changes.
  useEffect(() => { setUrlDraft(publicUrl); setSavedUrl(false); }, [publicUrl, open]);
  // A trailing slash breaks the QR link building (it appends ?token=… after the
  // path), so normalize it away — on blur (visible) and on save (persisted).
  const normPublicUrl = (s: string) => s.trim().replace(/\/+$/, '');
  const urlDirty = normPublicUrl(urlDraft) !== normPublicUrl(publicUrl);
  const savePublicUrl = async () => {
    setSavingUrl(true);
    setSavedUrl(false);
    try {
      const normalized = normPublicUrl(urlDraft);
      if (normalized !== urlDraft) setUrlDraft(normalized);
      await onSavePublicUrl(normalized);
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
  const [emailForm, setEmailForm] = useState({ host: '', port: 465, secure: true, user: '', pass: '', default_to: '' });
  const [emailSaving, setEmailSaving] = useState(false);
  const [emailSaved, setEmailSaved] = useState(false);
  const [showEmailPass, setShowEmailPass] = useState(false);
  const [apiToken, setApiToken] = useState('');
  const [tokenShown, setTokenShown] = useState(false);
  const [copied, setCopied] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshMsg, setRefreshMsg] = useState<{ kind: 'ok' | 'need-smtp' | 'err'; text: string } | null>(null);
  const sendReady = !!emailCfg?.send_ready;
  // Red badge on the 通用 nav item: SMTP not ready OR no token-delivery address.
  // Derive from emailCfg once loaded (updates live after save); otherwise fetch
  // once on open so the badge is correct even when the modal opens on another tab.
  const [emailNeedsSetup, setEmailNeedsSetup] = useState(false);
  useEffect(() => {
    if (emailCfg) { setEmailNeedsSetup(!emailCfg.smtp_ready || !String(emailCfg.default_to || '').trim()); return; }
    if (!open) return;
    let alive = true;
    apiService.getEmailConfig().then((r: any) => {
      const d = r?.data || {};
      if (alive) setEmailNeedsSetup(!d.smtp_ready || !String(d.default_to || '').trim());
    }).catch(() => {});
    return () => { alive = false; };
  }, [open, emailCfg]);
  // Red badge on the version line when a newer cicy-code is published (cached
  // backend lookup). Fetched once per open, mirroring the email badge above.
  const [versionUpdate, setVersionUpdate] = useState(false);
  useEffect(() => {
    if (!open) return;
    let alive = true;
    apiService.getCicyUpdateStatus().then((r: any) => {
      if (alive) setVersionUpdate(!!r?.data?.has_update);
    }).catch(() => {});
    return () => { alive = false; };
  }, [open]);
  const { confirm, node: dialogNode } = useDialogs();

  useEffect(() => {
    if (!open || section !== 'general') return;
    let alive = true;
    Promise.all([apiService.getEmailConfig(), apiService.getApiToken()])
      .then(([ec, tk]) => {
        if (!alive) return;
        const d = ec.data || {};
        setEmailCfg(d);
        const s = d.smtp || {};
        // Drop leftover scaffold placeholders ("<paste-…>") so the field reads as
        // empty → onEmailUserChange / save can auto-fill the real host from the
        // provider preset instead of writing the placeholder back.
        const loadedHost = isEmailPlaceholder(s.host) ? '' : (s.host || '');
        setEmailForm({ host: loadedHost, port: s.port || 465, secure: s.secure !== false, user: s.user || '', pass: '', default_to: isEmailPlaceholder(d.default_to) ? '' : (d.default_to || '') });
        setApiToken(tk.data?.token || '');
      })
      .catch(() => {});
    return () => { alive = false; };
  }, [open, section]);

  const saveEmail = async () => {
    setEmailSaving(true);
    setEmailSaved(false);
    try {
      const user = emailForm.user.trim();
      const preset = providerPreset(user);
      // Host: use what's typed, but if it's empty or a leftover placeholder,
      // derive it from the provider preset (qq.com → smtp.qq.com) so we never
      // save a placeholder. from defaults to the account.
      let host = emailForm.host.trim();
      if (!host || isEmailPlaceholder(host)) host = preset?.host || '';
      const port = Number(emailForm.port) || 465;
      const secure = !!emailForm.secure;
      const payload: any = {
        smtp: { host, port, secure, user, from: user },
        default_to: emailForm.default_to.trim() || user,
      };
      if (emailForm.pass.trim()) payload.smtp.pass = emailForm.pass;
      if (preset) {
        payload.imap = { host: preset.imap, port: 993, secure: true, user };
        payload.pop3 = { host: preset.pop3, port: 995, secure: true, user };
        if (emailForm.pass.trim()) { payload.imap.pass = emailForm.pass; payload.pop3.pass = emailForm.pass; }
      }
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
    const recipient = (emailCfg?.default_to || emailForm.default_to || emailForm.user || '').trim();
    const ok = await confirm({
      title: t('settingsTokenConfirmTitle', { defaultValue: '刷新 API 令牌?' }),
      body: (
        <>
          {t('settingsTokenConfirmBody', { defaultValue: '当前令牌会立即作废,所有正在用它访问本机的设备和远程会话都需要换用新令牌。' })}
          {recipient ? <><br /><br />{t('settingsTokenConfirmEmail', { defaultValue: '新令牌将发送到:', })}<span className="font-medium text-zinc-200">{recipient}</span></> : null}
        </>
      ),
      danger: true,
      confirmLabel: t('settingsTokenConfirmOk', { defaultValue: '刷新并发送' }),
      cancelLabel: t('settingsCancel', { defaultValue: '取消' }),
    });
    if (!ok) return;
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

  // Typing an account like cicybot@qq.com auto-fills the SMTP server/port so a
  // known provider needs only the account + auth code. Unknown domains keep
  // whatever the user typed manually.
  const onEmailUserChange = (raw: string) => {
    const preset = providerPreset(raw);
    setEmailForm((f) => {
      const next = { ...f, user: raw };
      if (preset && (!f.host || isPresetHost(f.host))) {
        next.host = preset.host;
        next.port = preset.port;
        next.secure = preset.secure;
      }
      return next;
    });
  };
  const detectedProvider = providerLabel(emailForm.user);

  const inp = 'rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-[13px] text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-white/[0.18]';
  const lbl = 'block text-[11px] font-medium text-zinc-400 mb-1';
  const card = 'rounded-xl border border-white/[0.06] bg-white/[0.02] p-5';
  const iconTile = 'flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-white/[0.05] text-zinc-300';
  // Neutral, low-key buttons — match the icon buttons, no loud color fills.
  const btnBase = 'rounded-lg px-3.5 py-2 text-[12px] font-medium transition-colors';
  const btnActive = 'border border-white/[0.1] bg-white/[0.05] text-zinc-100 hover:bg-white/[0.09] hover:text-white';
  const btnDisabled = 'cursor-not-allowed border border-white/[0.06] bg-white/[0.02] text-zinc-600';

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
                {item.id === 'general' && emailNeedsSetup && (
                  <span data-id="settings-modal-nav-general-badge" className="ml-auto h-1.5 w-1.5 shrink-0 rounded-full bg-red-500" title={t('emailNeedsSetup', { defaultValue: '未配置令牌投递邮箱 / SMTP' })} />
                )}
              </button>
            ))}
            {version ? (
              <div data-id="settings-modal-version" className="mt-auto flex items-center gap-1.5 px-3 py-2 text-[10.5px] text-zinc-600">
                {versionUpdate && <span data-id="settings-modal-version-badge" className="h-1.5 w-1.5 shrink-0 rounded-full bg-red-500" title={t('versionUpdateAvailable', { defaultValue: '有新版本可更新' })} />}
                <span>{t('membershipVersion', { defaultValue: '版本' })} {version}</span>
              </div>
            ) : null}
          </nav>

          {/* right content */}
          <div data-id="settings-modal-content" className="relative min-h-0 flex-1 overflow-hidden bg-[#0b0b0d]">
            {/* General — public URL, API token, email delivery */}
            {section === 'general' && (
              <div data-id="settings-section-general" className="h-full overflow-auto">
                <div className="mx-auto max-w-2xl px-8 py-7">
                  <header data-id="settings-general-header" className="mb-6">
                    <h2 className="text-[15px] font-semibold tracking-tight text-zinc-100">{t('settingsNavGeneral', { defaultValue: '通用' })}</h2>
                    <p className="mt-1 text-[12px] leading-5 text-zinc-500">{t('settingsGeneralSubtitle', { defaultValue: '本机访问地址、API 令牌与令牌投递邮箱' })}</p>
                  </header>

                  <div className="space-y-4">
                  {/* Card: 公网访问地址 */}
                  <section data-id="settings-publicurl-block" className={card}>
                    <div className="flex items-start gap-3">
                      <span className={iconTile}><Globe className="h-4 w-4" /></span>
                      <div className="min-w-0 flex-1">
                        <div className="text-[13px] font-semibold text-zinc-100">{t('settingsPublicUrlTitle', { defaultValue: '公网访问地址' })}</div>
                        <div className="mt-0.5 text-[11px] leading-5 text-zinc-500">{t('settingsPublicUrlHint', { defaultValue: '本机对外可达的地址(隧道域名或局域网 IP)。配置后右下角会出现「扫码上手机」二维码;留空则隐藏。' })}</div>
                        <div className="mt-3">
                          <input
                            data-id="settings-public-url-input"
                            type="text"
                            value={urlDraft}
                            onChange={(e) => { setUrlDraft(e.target.value); setSavedUrl(false); }}
                            onBlur={() => setUrlDraft((v) => normPublicUrl(v))}
                            onKeyDown={(e) => { if (e.key === 'Enter' && urlDirty && !savingUrl) void savePublicUrl(); }}
                            placeholder="https://app-xxxx.example.com"
                            spellCheck={false}
                            autoComplete="off"
                            className={`${inp} w-full`}
                          />
                        </div>
                        <div className="mt-3 flex items-center gap-2">
                          <button
                            type="button"
                            data-id="settings-public-url-save"
                            disabled={!urlDirty || savingUrl}
                            onClick={() => void savePublicUrl()}
                            className={`${btnBase} ${urlDirty && !savingUrl ? btnActive : btnDisabled}`}
                          >
                            {savingUrl ? t('settingsSaving', { defaultValue: '保存中…' }) : t('settingsSave', { defaultValue: '保存' })}
                          </button>
                          {savedUrl && !urlDirty ? (
                            <span data-id="settings-public-url-saved" className="flex items-center gap-1 text-[11px] text-emerald-400">
                              <Check className="h-3.5 w-3.5" />{t('settingsSaved', { defaultValue: '已保存' })}
                            </span>
                          ) : null}
                        </div>
                      </div>
                    </div>
                  </section>

                  {/* Card: 附件上传上限 */}
                  <section data-id="settings-attachment-block" className={card}>
                    <div className="flex items-start gap-3">
                      <span className={iconTile}><Paperclip className="h-4 w-4" /></span>
                      <div className="min-w-0 flex-1">
                        <div className="text-[13px] font-semibold text-zinc-100">{t('settingsAttachLimitTitle', { defaultValue: '附件上传上限' })}</div>
                        <div className="mt-0.5 text-[11px] leading-5 text-zinc-500">{t('settingsAttachLimitHint', { defaultValue: '派发 chat 单个附件的最大体积(MB)。任意文档类型均可上传;超过则拒绝。默认 100。' })}</div>
                        <div className="mt-3 flex items-center gap-2">
                          <input
                            data-id="settings-attachment-limit-input"
                            type="number"
                            min={1}
                            value={attachDraft}
                            onChange={(e) => { setAttachDraft(e.target.value); setAttachSaved(false); }}
                            onKeyDown={(e) => { if (e.key === 'Enter' && attachDirty && !attachSaving) void saveMaxAttach(); }}
                            className={`${inp} w-28`}
                          />
                          <span className="text-[12px] text-zinc-500">MB</span>
                        </div>
                        <div className="mt-3 flex items-center gap-2">
                          <button
                            type="button"
                            data-id="settings-attachment-limit-save"
                            disabled={!attachDirty || attachSaving}
                            onClick={() => void saveMaxAttach()}
                            className={`${btnBase} ${attachDirty && !attachSaving ? btnActive : btnDisabled}`}
                          >
                            {attachSaving ? t('settingsSaving', { defaultValue: '保存中…' }) : t('settingsSave', { defaultValue: '保存' })}
                          </button>
                          {attachSaved && !attachDirty ? (
                            <span data-id="settings-attachment-limit-saved" className="flex items-center gap-1 text-[11px] text-emerald-400">
                              <Check className="h-3.5 w-3.5" />{t('settingsSaved', { defaultValue: '已保存' })}
                            </span>
                          ) : null}
                        </div>
                      </div>
                    </div>
                  </section>

                  {/* Card: API Token — show + rotate (rotation emails the new token) */}
                  <section data-id="settings-token-block" className={card}>
                    <div className="flex items-start gap-3">
                      <span className={iconTile}><KeyRound className="h-4 w-4" /></span>
                      <div className="min-w-0 flex-1">
                        <div className="text-[13px] font-semibold text-zinc-100">{t('settingsTokenTitle', { defaultValue: 'API 令牌' })}</div>
                        <div className="mt-0.5 text-[11px] leading-5 text-zinc-500">{t('settingsTokenHint', { defaultValue: '本机 API 访问令牌。刷新会作废旧令牌,并把新令牌发到下方配置的收件邮箱。' })}</div>
                        <div className="mt-3 flex items-center gap-2">
                          <input
                            data-id="settings-token-value"
                            readOnly
                            value={tokenShown ? apiToken : (apiToken ? '•'.repeat(Math.min(40, apiToken.length)) : '')}
                            className={`${inp} min-w-0 flex-1 font-mono !text-[12px]`}
                          />
                          <button data-id="settings-token-toggle" type="button" title={tokenShown ? t('settingsHide', { defaultValue: '隐藏' }) : t('settingsShow', { defaultValue: '显示' })} onClick={() => setTokenShown((v) => !v)} className="shrink-0 rounded-lg border border-white/[0.08] bg-white/[0.03] p-2 text-zinc-400 transition-colors hover:text-zinc-200">
                            {tokenShown ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                          </button>
                          <button data-id="settings-token-copy" type="button" title={t('settingsCopy', { defaultValue: '复制' })} onClick={() => void copyToken()} className="shrink-0 rounded-lg border border-white/[0.08] bg-white/[0.03] p-2 text-zinc-400 transition-colors hover:text-zinc-200">
                            {copied ? <Check className="h-4 w-4 text-emerald-400" /> : <Copy className="h-4 w-4" />}
                          </button>
                        </div>
                        <div className="mt-3 flex items-center gap-2">
                          <button
                            data-id="settings-token-refresh"
                            type="button"
                            disabled={refreshing}
                            onClick={() => void refreshToken()}
                            className={`flex items-center gap-1.5 ${btnBase} ${refreshing ? btnDisabled : btnActive}`}
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
                    </div>
                  </section>

                  {/* Card: Email (SMTP) — same ~/cicy-ai/db/email.json the email skill uses */}
                  <section data-id="settings-email-block" className={card}>
                    <div className="flex items-start gap-3">
                      <span className={iconTile}><Mail className="h-4 w-4" /></span>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <div className="text-[13px] font-semibold text-zinc-100">{t('settingsEmailTitle', { defaultValue: '令牌投递邮箱' })}</div>
                          {emailCfg ? (
                            <span data-id="settings-email-status" className={`rounded-full px-2 py-0.5 text-[10px] font-medium ${sendReady ? 'bg-emerald-500/15 text-emerald-400' : 'bg-red-500/15 text-red-400'}`}>
                              {sendReady ? t('settingsEmailReady', { defaultValue: '已配置' }) : t('settingsEmailUnset', { defaultValue: '未配置' })}
                            </span>
                          ) : null}
                        </div>
                        <div className="mt-0.5 text-[11px] leading-5 text-zinc-500">{t('settingsEmailHint', { defaultValue: '刷新令牌时,新令牌通过这个邮箱发给你。填邮箱账号 + 授权码即可,常见服务商会自动识别。' })}</div>
                        <div className="mt-3 space-y-3">
                      {/* Account — typing this auto-detects the provider's servers */}
                      <div data-id="settings-email-user-field">
                        <label className={lbl}>{t('settingsEmailUserLabel', { defaultValue: '邮箱账号' })}</label>
                        <input data-id="settings-email-user" type="email" placeholder="you@qq.com" value={emailForm.user} onChange={(e) => onEmailUserChange(e.target.value)} className={`${inp} w-full`} spellCheck={false} autoComplete="off" />
                        {detectedProvider ? (
                          <div data-id="settings-email-detected" className="mt-1 flex items-center gap-1 text-[11px] text-emerald-400"><Check className="h-3 w-3" />{t('settingsEmailDetected', { defaultValue: '已识别 {{name}}，服务器已自动填好', name: detectedProvider })}</div>
                        ) : null}
                      </div>
                      {/* Auth code / password */}
                      <div data-id="settings-email-pass-field">
                        <label className={lbl}>{t('settingsEmailPassLabel', { defaultValue: '授权码 / 密码' })}</label>
                        <div className="relative">
                          <input data-id="settings-email-pass" type={showEmailPass ? 'text' : 'password'} placeholder={emailCfg?.smtp?.pass_set ? t('settingsEmailPassSet', { defaultValue: '已设置 (留空保持不变)' }) : t('settingsEmailPassPh', { defaultValue: 'QQ/163 等填授权码，非登录密码' })} value={emailForm.pass} onChange={(e) => setEmailForm((f) => ({ ...f, pass: e.target.value }))} className={`${inp} w-full pr-9`} autoComplete="new-password" />
                          <button
                            type="button"
                            data-id="settings-email-pass-toggle"
                            onClick={() => setShowEmailPass((v) => !v)}
                            className="absolute inset-y-0 right-0 flex items-center px-2.5 text-zinc-500 hover:text-zinc-300"
                            title={showEmailPass ? t('settingsHidePassword', { defaultValue: '隐藏' }) : t('settingsShowPassword', { defaultValue: '显示' })}
                            tabIndex={-1}
                          >
                            {showEmailPass ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                          </button>
                        </div>
                      </div>
                      {/* SMTP server — auto-filled from the account; editable for custom providers */}
                      <div data-id="settings-email-server-field">
                        <label className={lbl}>{t('settingsEmailServerLabel', { defaultValue: 'SMTP 服务器' })}{detectedProvider ? <span className="ml-1 text-zinc-600">{t('settingsEmailAuto', { defaultValue: '（已自动填好，可修改）' })}</span> : null}</label>
                        <div className="flex items-center gap-2">
                          <input data-id="settings-email-host" placeholder="smtp.example.com" value={emailForm.host} onChange={(e) => setEmailForm((f) => ({ ...f, host: e.target.value }))} className={`${inp} flex-1`} spellCheck={false} autoComplete="off" />
                          <input data-id="settings-email-port" type="number" placeholder="465" value={emailForm.port} onChange={(e) => setEmailForm((f) => ({ ...f, port: Number(e.target.value) }))} className={`${inp} w-20`} title={t('settingsEmailPortTitle', { defaultValue: '端口' })} />
                          <label className="flex items-center gap-1.5 whitespace-nowrap text-[12px] text-zinc-400">
                            <input data-id="settings-email-secure" type="checkbox" checked={emailForm.secure} onChange={(e) => setEmailForm((f) => ({ ...f, secure: e.target.checked }))} />
                            {t('settingsEmailSecureShort', { defaultValue: 'TLS' })}
                          </label>
                        </div>
                      </div>
                      {/* Recipient — defaults to the account itself */}
                      <div data-id="settings-email-to-field">
                        <label className={lbl}>{t('settingsEmailToLabel', { defaultValue: '接收新 token 的邮箱' })}</label>
                        <input data-id="settings-email-to" type="email" placeholder={emailForm.user.trim() ? t('settingsEmailToPh', { defaultValue: '默认与账号相同（{{addr}}），可留空', addr: emailForm.user.trim() }) : t('settingsEmailToPhEmpty', { defaultValue: '默认与账号相同，可留空' })} value={emailForm.default_to} onChange={(e) => setEmailForm((f) => ({ ...f, default_to: e.target.value }))} className={`${inp} w-full`} spellCheck={false} autoComplete="off" />
                      </div>
                    </div>
                        <div className="mt-4 flex items-center gap-2">
                          <button data-id="settings-email-save" type="button" disabled={emailSaving} onClick={() => void saveEmail()} className={`${btnBase} ${emailSaving ? btnDisabled : btnActive}`}>
                            {emailSaving ? t('settingsSaving', { defaultValue: '保存中…' }) : t('settingsSave', { defaultValue: '保存' })}
                          </button>
                          {emailSaved ? (
                            <span data-id="settings-email-saved" className="flex items-center gap-1 text-[11px] text-emerald-400"><Check className="h-3.5 w-3.5" />{t('settingsSaved', { defaultValue: '已保存' })}</span>
                          ) : null}
                        </div>
                      </div>
                    </div>
                  </section>
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
                <div ref={setProvRight} data-id="settings-prov-right-mount" className="absolute inset-0" />
                {section === 'routing' ? (
                  <div data-id="settings-routing-hint" className="pointer-events-none absolute inset-0 flex items-center justify-center p-8 text-center">
                    <div data-id="settings-routing-hint-text" className="max-w-xs text-[12px] leading-5 text-zinc-600">
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
      {dialogNode}
    </div>,
    document.body,
  );
}
