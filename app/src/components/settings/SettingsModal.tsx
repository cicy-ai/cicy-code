import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { SlidersHorizontal, Globe, MessageCircle, Route, Boxes, X, Check } from 'lucide-react';
import ProviderDashboard from '../providers/ProviderDashboard';
import IMDashboard from '../im/IMDashboard';

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
