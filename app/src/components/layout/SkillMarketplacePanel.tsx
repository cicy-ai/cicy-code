import { memo, useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useTranslation } from 'react-i18next';
import {
  Search, Loader2, CheckCircle2, AlertTriangle, RefreshCw, X, Send,
  Globe, Activity, Server, Plug, Mail, FileText, Code, Terminal, Key, Shield, Package, Cloud,
  Copy, Check, XCircle, Languages,
} from 'lucide-react';
import { cn } from '../../lib/utils';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';

interface InstallStatus {
  installed: boolean;
  config_present?: boolean;
  requires_met?: Record<string, boolean>;
  last_error?: string;
  detail?: Record<string, string>;
}

interface MarketSkill {
  name: string;
  title: string;
  description: string;
  version: string;
  category: string;
  icon?: string;
  tags?: string[];
  binary_aliases?: string[];
  config_file?: string;
  status: InstallStatus;
}

interface SkillDetailPayload {
  skill: MarketSkill;
  skill_md?: string;
  help_md?: string;
  tools_md?: string;
}

type Filter = 'all' | 'installed' | 'available';

const ICON_MAP: Record<string, React.ComponentType<{ className?: string }>> = {
  globe: Globe, activity: Activity, server: Server, plug: Plug, mail: Mail,
  'file-text': FileText, code: Code, terminal: Terminal, key: Key, shield: Shield,
  package: Package, cloud: Cloud,
};

const CATEGORY_GRADIENT: Record<string, string> = {
  network: 'from-sky-500/20 to-blue-500/20 text-sky-300 ring-sky-500/30',
  ai: 'from-violet-500/20 to-fuchsia-500/20 text-violet-300 ring-violet-500/30',
  infra: 'from-amber-500/20 to-orange-500/20 text-amber-300 ring-amber-500/30',
  dev: 'from-emerald-500/20 to-teal-500/20 text-emerald-300 ring-emerald-500/30',
  comms: 'from-pink-500/20 to-rose-500/20 text-pink-300 ring-pink-500/30',
  ops: 'from-zinc-500/20 to-slate-500/20 text-zinc-300 ring-zinc-500/30',
  other: 'from-zinc-500/20 to-zinc-700/20 text-zinc-300 ring-zinc-500/30',
};

function SkillAvatar({ skill, size = 'md' }: { skill: MarketSkill; size?: 'sm' | 'md' | 'lg' }) {
  const Icon = ICON_MAP[skill.icon || 'package'] || Package;
  const grad = CATEGORY_GRADIENT[skill.category] || CATEGORY_GRADIENT.other;
  const dim = size === 'lg' ? 'w-14 h-14 rounded-xl' : size === 'sm' ? 'w-6 h-6 rounded' : 'w-9 h-9 rounded-lg';
  const ico = size === 'lg' ? 'w-7 h-7' : size === 'sm' ? 'w-3.5 h-3.5' : 'w-4 h-4';
  return (
    <div className={cn('shrink-0 flex items-center justify-center bg-gradient-to-br ring-1', dim, grad)}>
      <Icon className={ico} />
    </div>
  );
}

export default function SkillMarketplacePanel({ paneId }: { paneId: string }) {
  const { t } = useTranslation('workspace');
  const [skills, setSkills] = useState<MarketSkill[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string>('');
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [selectedName, setSelectedName] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    setLoadError('');
    try {
      const res = await apiService.listMarketSkills();
      setSkills(Array.isArray(res?.data?.skills) ? res.data.skills : []);
    } catch (e: any) {
      setLoadError(e?.message || 'load failed');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const filtered = useMemo(() => {
    let list = skills;
    if (filter === 'installed') list = list.filter(s => s.status.installed);
    if (filter === 'available') list = list.filter(s => !s.status.installed);
    if (query.trim()) {
      const q = query.trim().toLowerCase();
      list = list.filter(s =>
        s.name.toLowerCase().includes(q)
        || s.title.toLowerCase().includes(q)
        || s.description.toLowerCase().includes(q)
        || (s.tags || []).some(tag => tag.toLowerCase().includes(q))
      );
    }
    return list;
  }, [skills, filter, query]);

  const grouped = useMemo(() => {
    const out: Record<string, MarketSkill[]> = {};
    for (const s of filtered) {
      const key = s.category || 'other';
      (out[key] ||= []).push(s);
    }
    return out;
  }, [filtered]);

  const counts = useMemo(() => ({
    total: skills.length,
    installed: skills.filter(s => s.status.installed).length,
  }), [skills]);

  const onInstall = async (skill: MarketSkill) => {
    setBusy(b => ({ ...b, [skill.name]: true }));
    try {
      const res = await apiService.installMarketSkill(skill.name);
      const status = res?.data?.status as InstallStatus | undefined;
      if (status) {
        setSkills(prev => prev.map(s => s.name === skill.name ? { ...s, status } : s));
      } else {
        await load();
      }
    } catch { await load(); }
    finally { setBusy(b => { const { [skill.name]: _, ...rest } = b; return rest; }); }
  };

  const onUninstall = async (skill: MarketSkill) => {
    setBusy(b => ({ ...b, [skill.name]: true }));
    try {
      await apiService.uninstallMarketSkill(skill.name);
      await load();
    } catch {}
    finally { setBusy(b => { const { [skill.name]: _, ...rest } = b; return rest; }); }
  };

  return (
    <>
      <div className="h-full flex flex-col overflow-hidden bg-[#0A0A0A]" data-id="skill-market-root">
        <div className="px-3 py-2 border-b border-[var(--vsc-border)] shrink-0 space-y-2" data-id="skill-market-header">
          <div className="relative">
            <Search className="w-3.5 h-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-zinc-500 pointer-events-none" />
            <input
              data-id="skill-market-search"
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder={t('marketplaceSearchPlaceholder')}
              className="w-full pl-7 pr-2 py-1.5 text-xs bg-[#0e0e0e] border border-[var(--vsc-border)] rounded text-zinc-300 placeholder:text-zinc-600 focus:outline-none focus:border-zinc-500"
            />
          </div>
          <div className="flex items-center gap-1 text-[11px]">
            {(['all','installed','available'] as Filter[]).map(f => (
              <button
                key={f}
                data-id={`skill-market-filter-${f}`}
                onClick={() => setFilter(f)}
                className={cn(
                  'px-2 py-0.5 rounded transition-colors',
                  filter === f ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'
                )}
              >
                {f === 'all' && `${t('marketplaceAll')} ${counts.total}`}
                {f === 'installed' && `${t('marketplaceInstalled')} ${counts.installed}`}
                {f === 'available' && `${t('marketplaceAvailable')} ${counts.total - counts.installed}`}
              </button>
            ))}
            <button data-id="skill-market-refresh" onClick={load} className="ml-auto p-1 text-zinc-500 hover:text-zinc-300 rounded" title={t('marketplaceRetry')}>
              <RefreshCw className={cn('w-3 h-3', loading && 'animate-spin')} />
            </button>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto" data-id="skill-market-list">
          {loading && skills.length === 0 ? (
            <div className="p-4 text-xs text-zinc-500 flex items-center gap-2">
              <Loader2 className="w-3 h-3 animate-spin" /> {t('marketplaceInstalling')}
            </div>
          ) : loadError ? (
            <div className="p-4 text-xs">
              <div className="text-red-400 mb-2">{t('marketplaceFailedToLoad')}: {loadError}</div>
              <button onClick={load} className="text-zinc-400 hover:text-zinc-100 underline">{t('marketplaceRetry')}</button>
            </div>
          ) : filtered.length === 0 ? (
            <div className="p-4 text-xs text-zinc-500">{t('marketplaceEmpty')}</div>
          ) : (
            Object.entries(grouped).sort(([a],[b]) => a.localeCompare(b)).map(([category, list]) => (
              <div key={category} data-id={`skill-market-cat-${category}`}>
                <div className="px-3 pt-3 pb-1 text-[10px] uppercase tracking-wider text-zinc-600">{category}</div>
                <div>
                  {list.map(skill => (
                    <SkillRow
                      key={skill.name}
                      skill={skill}
                      selected={selectedName === skill.name}
                      onClick={() => setSelectedName(skill.name)}
                    />
                  ))}
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      {selectedName && (
        <SkillDetailModal
          name={selectedName}
          paneId={paneId}
          onClose={() => setSelectedName(null)}
          onInstall={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) await onInstall(sk);
          }}
          onUninstall={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) await onUninstall(sk);
          }}
        />
      )}
    </>
  );
}

function SkillRow({ skill, selected, onClick }: {
  skill: MarketSkill;
  selected: boolean;
  onClick: () => void;
}) {
  const installed = skill.status.installed;

  return (
    <div
      data-id={`skill-market-skill-${skill.name}`}
      data-selected={selected ? 'true' : undefined}
      aria-selected={selected}
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onClick(); } }}
      className={cn(
        'group h-[68px] px-3 py-2 border-b border-[var(--vsc-border)]/40 transition-colors cursor-pointer focus:outline-none flex items-center relative',
        selected
          ? 'bg-indigo-500/[0.08] before:absolute before:left-0 before:top-1 before:bottom-1 before:w-[2px] before:rounded-r before:bg-indigo-400'
          : 'hover:bg-white/[0.03] focus:bg-white/[0.05]'
      )}
    >
      <div className="flex items-start gap-2.5 w-full">
        <SkillAvatar skill={skill} size="md" />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 mb-0.5">
            <div className="text-xs font-medium text-zinc-200 truncate">{skill.title}</div>
            {installed && (
              <span className="text-[10px] px-1 py-0.5 rounded bg-emerald-500/15 text-emerald-400 inline-flex items-center" data-id={`skill-market-installed-${skill.name}`}>
                <CheckCircle2 className="w-2.5 h-2.5" />
              </span>
            )}
            <span className="ml-auto text-[10px] text-zinc-600 shrink-0">v{skill.version}</span>
          </div>
          <div className="text-[11px] text-zinc-500 line-clamp-2 leading-snug min-h-[2.4em]">{skill.description}</div>
        </div>
      </div>
    </div>
  );
}

function StatusPill({ installed, needsAttention, hasError }: { installed: boolean; needsAttention: boolean; hasError: boolean }) {
  const { t } = useTranslation('workspace');
  if (hasError) return <span className="text-[10px] px-1.5 py-0.5 rounded bg-red-500/15 text-red-400 inline-flex items-center gap-1"><AlertTriangle className="w-2.5 h-2.5" /> {t('marketplaceError')}</span>;
  if (needsAttention) return <span className="text-[10px] px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 inline-flex items-center gap-1"><AlertTriangle className="w-2.5 h-2.5" /> {t('marketplaceMissingDep')}</span>;
  if (installed) return <span className="text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/15 text-emerald-400 inline-flex items-center gap-1"><CheckCircle2 className="w-2.5 h-2.5" /></span>;
  return null;
}

function CategoryBadge({ category }: { category: string }) {
  const grad = CATEGORY_GRADIENT[category] || CATEGORY_GRADIENT.other;
  return (
    <span className={cn('inline-flex items-center text-[10px] px-1.5 py-0.5 rounded bg-gradient-to-br ring-1', grad)}>
      {category}
    </span>
  );
}

function InlineStatus({ skill }: { skill: MarketSkill }) {
  const pills: { ok: boolean; label: string; title?: string }[] = [];
  if (skill.status.installed) {
    pills.push({ ok: !skill.status.last_error, label: skill.status.last_error ? 'error' : 'installed', title: skill.status.last_error });
  } else {
    pills.push({ ok: false, label: 'available' });
  }
  if (skill.config_file) {
    pills.push({ ok: !!skill.status.config_present, label: 'config', title: skill.config_file });
  }
  if (skill.status.requires_met) {
    for (const [dep, met] of Object.entries(skill.status.requires_met)) {
      pills.push({ ok: met, label: dep });
    }
  }
  return (
    <>
      {pills.map((p, i) => (
        <span
          key={i}
          title={p.title || p.label}
          className={cn(
            'inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded',
            p.ok ? 'bg-emerald-500/10 text-emerald-300/90' : 'bg-zinc-500/10 text-zinc-400'
          )}
        >
          {p.ok ? <CheckCircle2 className="w-2.5 h-2.5" /> : <XCircle className="w-2.5 h-2.5" />}
          {p.label}
        </span>
      ))}
    </>
  );
}

type Tab = 'help' | 'tools';

function SkillDetailModal({ name, paneId, onClose, onInstall, onUninstall }: {
  name: string;
  paneId: string;
  onClose: () => void;
  onInstall: () => Promise<void>;
  onUninstall: () => Promise<void>;
}) {
  const { t } = useTranslation('workspace');
  const [data, setData] = useState<SkillDetailPayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [tab, setTab] = useState<Tab>('help');
  const [busy, setBusy] = useState(false);
  const [sendText, setSendText] = useState('');
  const [sending, setSending] = useState(false);
  const [sendOk, setSendOk] = useState(false);
  const [sendError, setSendError] = useState('');
  const [copied, setCopied] = useState<string>('');

  const fetchDetail = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiService.getMarketSkill(name);
      const payload = res?.data as SkillDetailPayload;
      setData(payload);
      const sk = payload?.skill;
      if (sk) setSendText(`Use the \`${sk.name}\` skill: ${sk.description}`);
    } catch {
    } finally {
      setLoading(false);
    }
  }, [name]);

  useEffect(() => { fetchDetail(); }, [fetchDetail]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.preventDefault(); onClose(); } };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [onClose]);

  const copy = (text: string) => {
    navigator.clipboard?.writeText(text);
    setCopied(text);
    setTimeout(() => setCopied(prev => prev === text ? '' : prev), 1200);
  };

  const { confirm, node: confirmNode } = useDialogs();

  const handleInstall = async () => {
    setBusy(true);
    try { await onInstall(); await fetchDetail(); }
    finally { setBusy(false); }
  };
  const handleUninstall = async () => {
    const cfgPath = data?.skill?.config_file || '~/cicy-ai/db/skills/' + name + '.yaml';
    const ok = await confirm({
      title: t('marketplaceUninstallConfirmTitle', { title: data?.skill?.title || name }),
      body: <>{t('marketplaceUninstallConfirmBodyPrefix')} <code className="font-mono text-xs">{cfgPath}</code> {t('marketplaceUninstallConfirmBodySuffix')}</>,
      confirmLabel: t('marketplaceUninstall'),
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    try { await onUninstall(); await fetchDetail(); }
    finally { setBusy(false); }
  };

  // Stable sender — referentially identical across renders so memoized
  // children (MarkdownPane) don't re-render on every parent state tick.
  const sendToAgent = useCallback(async (text: string) => {
    const trimmed = (text || '').trim();
    if (!trimmed || !paneId) return;
    setSending(true);
    setSendOk(false);
    setSendError('');
    try {
      await apiService.sendKeys(paneId, trimmed + '\n');
      setSendOk(true);
      setTimeout(() => { onClose(); }, 400);
    } catch (e: any) {
      setSendError(e?.message || 'send failed');
    } finally {
      setSending(false);
    }
  }, [paneId, onClose]);

  const handleSend = async (textOverride?: string) => {
    const text = (textOverride ?? sendText).trim();
    if (!text || !paneId) return;
    setSending(true);
    setSendOk(false);
    setSendError('');
    try {
      await apiService.sendKeys(paneId, text + '\n');
      setSendOk(true);
      // close after a brief flash so user sees the success state
      setTimeout(() => { onClose(); }, 400);
    } catch (e: any) {
      setSendError(e?.message || 'send failed');
    } finally {
      setSending(false);
    }
  };

  const skill = data?.skill;

  return createPortal(
    <div
      className="fixed inset-0 z-[9999991] flex items-center justify-center bg-black/55 backdrop-blur-sm animate-[fadeIn_120ms_ease-out]"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}
      data-id="skill-detail-modal"
    >
      <div className="mx-4 w-full max-w-[760px] h-[80vh] flex flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60">
        {/* Header */}
        <div className="px-5 pt-5 pb-3 border-b border-white/[0.06] shrink-0" data-id="skill-detail-header">
          <div className="flex items-start gap-3">
            {skill ? <SkillAvatar skill={skill} size="lg" /> : <div className="w-14 h-14 rounded-xl bg-white/5" />}
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <div className="text-base font-semibold text-zinc-100" data-id="skill-detail-title">
                  {skill?.title || (loading ? '…' : name)}
                </div>
                {skill && <StatusPill installed={skill.status.installed} needsAttention={skill.status.installed && (!skill.status.config_present || !!skill.status.last_error)} hasError={!!skill.status.last_error} />}
              </div>
              <div className="text-[11px] text-zinc-500 mt-0.5">
                {skill ? <>v{skill.version} · {t('marketplacePublisher')}</> : ''}
              </div>
              {skill && (
                <div className="mt-1.5 flex flex-wrap items-center gap-1.5" data-id="skill-detail-status-inline">
                  <CategoryBadge category={skill.category} />
                  <InlineStatus skill={skill} />
                </div>
              )}
              {skill && <div className="mt-2 text-xs text-zinc-300 leading-relaxed">{skill.description}</div>}
              {skill && (
                <div className="mt-3 flex items-center gap-2">
                  {skill.status.installed ? (
                    <>
                      <button data-id="skill-detail-reinstall" onClick={handleInstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded border border-zinc-700 text-zinc-300 hover:text-zinc-100 hover:border-zinc-500 disabled:opacity-50 transition-colors inline-flex items-center gap-1">
                        {busy && <Loader2 className="w-3 h-3 animate-spin" />}
                        {busy ? t('marketplaceInstalling') : t('marketplaceReinstall')}
                      </button>
                      <button data-id="skill-detail-uninstall" onClick={handleUninstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded text-zinc-400 hover:text-zinc-200 disabled:opacity-50 transition-colors">
                        {t('marketplaceUninstall')}
                      </button>
                    </>
                  ) : (
                    <button data-id="skill-detail-install" onClick={handleInstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded bg-indigo-500/20 text-indigo-200 hover:bg-indigo-500/30 disabled:opacity-50 transition-colors inline-flex items-center gap-1">
                      {busy && <Loader2 className="w-3 h-3 animate-spin" />}
                      {busy ? t('marketplaceInstalling') : t('marketplaceInstall')}
                    </button>
                  )}
                </div>
              )}
            </div>
            <button onClick={onClose} className="grid h-7 w-7 shrink-0 place-items-center rounded text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 transition-colors" data-id="skill-detail-close">
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        {/* Tabs */}
        <div className="px-5 pt-2 border-b border-white/[0.06] shrink-0 flex items-center gap-1" data-id="skill-detail-tabs">
          {(['help','tools'] as Tab[]).map(tk => (
            <button
              key={tk}
              data-id={`skill-detail-tab-${tk}`}
              onClick={() => setTab(tk)}
              className={cn(
                'px-3 py-1.5 text-[12px] rounded-t border-b-2 transition-colors -mb-[1px]',
                tab === tk ? 'text-zinc-100 border-indigo-400' : 'text-zinc-500 border-transparent hover:text-zinc-300'
              )}
            >
              {tk === 'help' ? t('marketplaceTabHelp') : t('marketplaceTabTools')}
            </button>
          ))}
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-5 py-4" data-id="skill-detail-body">
          {loading ? (
            <div className="text-xs text-zinc-500 flex items-center gap-2"><Loader2 className="w-3 h-3 animate-spin" /> {t('marketplaceModalLoading')}</div>
          ) : !skill ? (
            <div className="text-xs text-zinc-500">{t('marketplaceNoData')}</div>
          ) : tab === 'help' ? (
            <MarkdownPane content={data?.help_md || data?.skill_md || ''} onTry={sendToAgent} setSendText={setSendText} />
          ) : (
            <MarkdownPane content={data?.tools_md || ''} onTry={sendToAgent} setSendText={setSendText} />
          )}
        </div>

        {/* Send to agent — only enabled when skill is installed */}
        <div className="px-5 py-3 border-t border-white/[0.08] bg-[#101012] shrink-0" data-id="skill-detail-send">
          {!skill?.status.installed ? (
            <div className="flex items-center gap-2 text-[11px] text-zinc-500">
              <AlertTriangle className="w-3 h-3" />
              {t('marketplaceInstallFirst')}
            </div>
          ) : (
            <>
              <div className="flex items-end gap-2">
                <textarea
                  data-id="skill-detail-send-input"
                  value={sendText}
                  onChange={(e) => setSendText(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); handleSend(); } }}
                  placeholder={t('marketplaceSendPlaceholder', { pane: paneId || '—' })}
                  rows={2}
                  className="flex-1 resize-none rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 py-2 text-[12px] text-zinc-100 placeholder:text-zinc-600 outline-none focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15"
                />
                <button
                  data-id="skill-detail-send-button"
                  onClick={() => handleSend()}
                  disabled={sending || !sendText.trim() || !paneId}
                  className={cn(
                    'inline-flex items-center gap-2 px-4 py-2.5 rounded-lg font-medium transition-all shadow-md',
                    'bg-gradient-to-br from-indigo-500 to-purple-500 text-white',
                    'hover:from-indigo-400 hover:to-purple-400 hover:shadow-indigo-500/40 hover:shadow-lg',
                    'focus:outline-none focus:ring-2 focus:ring-indigo-400/50',
                    'disabled:from-zinc-700 disabled:to-zinc-700 disabled:text-zinc-500 disabled:shadow-none disabled:cursor-not-allowed'
                  )}
                >
                  {sending ? <Loader2 className="w-4 h-4 animate-spin" /> : sendOk ? <Check className="w-4 h-4" /> : <Send className="w-4 h-4" />}
                  <span className="text-[13px]">{sendOk ? t('marketplaceSent') : t('marketplaceSendToAgent')}</span>
                </button>
              </div>
              {sendError && <div className="text-[11px] text-red-400 mt-1.5">{sendError}</div>}
              <div className="text-[10px] text-zinc-600 mt-1.5">
                {t('marketplaceSendHint', { pane: paneId || '(none)' })}
              </div>
            </>
          )}
        </div>
      </div>
      {confirmNode}
    </div>,
    document.body
  );
}

const MarkdownPane = memo(function MarkdownPane({ content, onTry, setSendText }: { content: string; onTry: (cmd: string) => void; setSendText: (s: string) => void }) {
  const { t, i18n } = useTranslation('workspace');
  const lang = (i18n.language || 'en').toLowerCase();
  const showTranslate = !lang.startsWith('en') && !!content && content.trim().length > 0;
  const [translated, setTranslated] = useState<string>('');
  const [translating, setTranslating] = useState(false);
  const [translateError, setTranslateError] = useState('');
  const [showOriginal, setShowOriginal] = useState(true);

  const doTranslate = async () => {
    if (translated) {
      setShowOriginal(prev => !prev);
      return;
    }
    setTranslating(true);
    setTranslateError('');
    try {
      const { data } = await apiService.translateText(content, lang);
      setTranslated(String(data?.text || '').trim());
      setShowOriginal(false);
    } catch (e: any) {
      setTranslateError(e?.message || 'translate failed');
    } finally {
      setTranslating(false);
    }
  };

  const shown = showOriginal || !translated ? content : translated;

  // Memoize the components map so react-markdown doesn't re-parse from scratch
  // on every parent state change (send button presses, etc.). t/setSendText/onTry
  // are the only inputs that affect rendered handlers; if the parent gives
  // stable refs (via useCallback) this map stays referentially identical.
  const components = useMemo(() => ({
    h1: (p: any) => <h1 className="text-base font-semibold text-zinc-100 mt-3 mb-2" {...p} />,
    h2: (p: any) => <h2 className="text-sm font-semibold text-zinc-200 mt-4 mb-2 pb-1 border-b border-white/[0.06]" {...p} />,
    h3: (p: any) => <h3 className="text-[13px] font-medium text-zinc-200 mt-3 mb-1.5" {...p} />,
    p: (p: any) => <p className="text-[12px] text-zinc-300 my-1.5" {...p} />,
    ul: (p: any) => <ul className="text-[12px] text-zinc-300 my-1.5 ml-4 list-disc space-y-1" {...p} />,
    ol: (p: any) => <ol className="text-[12px] text-zinc-300 my-1.5 ml-4 list-decimal space-y-1" {...p} />,
    li: (p: any) => <li className="text-[12px] text-zinc-300" {...p} />,
    a: (p: any) => <a className="text-blue-400 hover:underline" target="_blank" rel="noreferrer" {...p} />,
    code: ({ children, className, ...props }: any) => {
      const inline = !(className && /language-/.test(className));
      const text = String(children).replace(/\n$/, '');
      if (inline) {
        return (
          <button
            onClick={() => setSendText(text)}
            className="px-1 py-0.5 rounded bg-white/[0.06] text-[11px] text-amber-200 font-mono hover:bg-white/[0.12] transition-colors"
            title={t('marketplaceLoadIntoSend')}
          >
            {text}
          </button>
        );
      }
      return (
        <div className="my-2 rounded-md bg-black/40 border border-white/[0.04] overflow-hidden">
          <div className="flex items-center justify-end px-2 py-1 border-b border-white/[0.04]">
            <button onClick={() => onTry(text)} className="text-[10px] text-zinc-400 hover:text-zinc-100 transition-colors inline-flex items-center gap-1">
              <Send className="w-2.5 h-2.5" /> {t('marketplaceSendToAgent')}
            </button>
          </div>
          <pre className="text-[11px] text-zinc-300 font-mono whitespace-pre-wrap p-2"><code {...props}>{children}</code></pre>
        </div>
      );
    },
    blockquote: (p: any) => <blockquote className="border-l-2 border-zinc-600 pl-3 my-2 text-zinc-400" {...p} />,
    hr: () => <hr className="my-3 border-white/[0.06]" />,
  }), [t, setSendText, onTry]);

  // Memoize the full Markdown render keyed on shown — re-parse only when the
  // displayed text actually changes (tab switch, translation toggle, fetch).
  const rendered = useMemo(() => (
    <Markdown remarkPlugins={[remarkGfm]} components={components}>{shown}</Markdown>
  ), [shown, components]);

  if (!content || !content.trim()) {
    return <div className="text-xs text-zinc-500">{t('marketplaceNoContent')}</div>;
  }

  return (
    <div className="relative">
      {showTranslate && (
        <div className="flex items-center justify-end mb-2 gap-2">
          {translateError && <span className="text-[10px] text-red-400">{translateError}</span>}
          <button
            data-id="skill-detail-translate"
            onClick={doTranslate}
            disabled={translating}
            className="inline-flex items-center gap-1 text-[11px] px-2 py-0.5 rounded border border-white/[0.08] text-zinc-400 hover:text-zinc-100 hover:border-white/[0.18] disabled:opacity-50 transition-colors"
          >
            {translating ? <Loader2 className="w-3 h-3 animate-spin" /> : <Languages className="w-3 h-3" />}
            {translating
              ? t('marketplaceTranslating')
              : translated
                ? (showOriginal ? t('marketplaceShowTranslation') : t('marketplaceShowOriginal'))
                : t('marketplaceTranslate')}
          </button>
        </div>
      )}
      <div className="prose-skill text-[13px] leading-relaxed text-zinc-300">{rendered}</div>
    </div>
  );
});
