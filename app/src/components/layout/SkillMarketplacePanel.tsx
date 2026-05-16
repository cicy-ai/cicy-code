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
import { ProxyManagerDialog } from './ProxyManagerDialog';
import { ProxySshManagerDialog } from './ProxySshManagerDialog';

// Prompt sent to the active agent when the user clicks "Authorize Google" in
// the skill detail. The agent uses the `google` skill's `login` tool to drive
// the OAuth setup conversationally — first guiding the user through creating
// a Google Cloud OAuth client if needed, then through the device-code flow.
const GOOGLE_AUTH_PROMPT = [
  '请用 `google` skill 帮我完成 Google 账号授权。先运行 `google login`,然后严格按它 stdout 里给的步骤一步一步带我走 —— 它会自己判断当前状态(还没配 OAuth client / 已配但没授权 / 已授权),并给出对应指引。',
  '',
  '注意:',
  '- 每一步等我确认完再做下一步,不要一次性把所有步骤都说完。',
  '- 如果它说要我去 Google Cloud Console 建 OAuth client,要明确告诉我 Application type 必须选 "TVs and Limited Input devices"(Web application 用不了 device flow)。',
  '- 如果它打印了 user_code 和验证 URL,把它们清楚地展示给我,提醒我浏览器打开 URL 输 code,然后耐心等它轮询出结果。',
  '- 不要替我跳过任何一步,也不要假设我已经做过 —— 一切以 `google login` 的实时输出为准。',
].join('\n');

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
    <div data-id="skill-marketplace-panel-auto-1" className={cn('shrink-0 flex items-center justify-center bg-gradient-to-br ring-1', dim, grad)}>
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
  const [proxyManagerOpen, setProxyManagerOpen] = useState(false);
  const [proxySshManagerOpen, setProxySshManagerOpen] = useState(false);
  // Stable refs for props passed to <SkillDetailModal>. Without these, every
  // parent re-render (e.g. the 5s WS poll cycle) emits new closures, which
  // propagates through sendToAgent → MarkdownPane.components → react-markdown
  // and forces the rendered markdown DOM to be replaced — wiping any text
  // selection the user has in the help/tools tab.
  const handleDetailClose = useCallback(() => setSelectedName(null), []);
  const handleOpenProxyManager = useCallback(() => { setSelectedName(null); setProxyManagerOpen(true); }, []);
  const handleOpenProxySshManager = useCallback(() => { setSelectedName(null); setProxySshManagerOpen(true); }, []);

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
          <div data-id="skill-marketplace-panel-auto-2" className="relative">
            <Search className="w-3.5 h-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-zinc-500 pointer-events-none" />
            <input
              data-id="skill-market-search"
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder={t('marketplaceSearchPlaceholder')}
              className="w-full pl-7 pr-2 py-1.5 text-xs bg-[#0e0e0e] border border-[var(--vsc-border)] rounded text-zinc-300 placeholder:text-zinc-600 focus:outline-none focus:border-zinc-500"
            />
          </div>
          <div data-id="skill-marketplace-panel-auto-3" className="flex items-center gap-1 text-[11px]">
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
            <div data-id="skill-marketplace-panel-auto-4" className="p-4 text-xs text-zinc-500 flex items-center gap-2">
              <Loader2 className="w-3 h-3 animate-spin" /> {t('marketplaceModalLoading')}
            </div>
          ) : loadError ? (
            <div data-id="skill-marketplace-panel-auto-5" className="p-4 text-xs">
              <div data-id="skill-marketplace-panel-auto-6" className="text-red-400 mb-2">{t('marketplaceFailedToLoad')}: {loadError}</div>
              <button data-id="skill-marketplace-panel-auto-7" onClick={load} className="text-zinc-400 hover:text-zinc-100 underline">{t('marketplaceRetry')}</button>
            </div>
          ) : filtered.length === 0 ? (
            <div data-id="skill-marketplace-panel-auto-8" className="p-4 text-xs text-zinc-500">{t('marketplaceEmpty')}</div>
          ) : (
            Object.entries(grouped).sort(([a],[b]) => a.localeCompare(b)).map(([category, list]) => (
              <div key={category} data-id={`skill-market-cat-${category}`}>
                <div data-id="skill-marketplace-panel-auto-9" className="px-3 pt-3 pb-1 text-[10px] uppercase tracking-wider text-zinc-600">{category}</div>
                <div data-id="skill-marketplace-panel-auto-10">
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
          onClose={handleDetailClose}
          onInstall={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) await onInstall(sk);
          }}
          onUninstall={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) await onUninstall(sk);
          }}
          onOpenProxyManager={handleOpenProxyManager}
          onOpenProxySshManager={handleOpenProxySshManager}
        />
      )}
      <ProxyManagerDialog open={proxyManagerOpen} onClose={() => setProxyManagerOpen(false)} paneId={paneId} />
      <ProxySshManagerDialog open={proxySshManagerOpen} onClose={() => setProxySshManagerOpen(false)} />
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
      <div data-id="skill-marketplace-panel-auto-11" className="flex items-start gap-2.5 w-full">
        <SkillAvatar skill={skill} size="md" />
        <div data-id="skill-marketplace-panel-auto-12" className="flex-1 min-w-0">
          <div data-id="skill-marketplace-panel-auto-13" className="flex items-center gap-2 mb-0.5">
            <div data-id="skill-marketplace-panel-auto-14" className="text-xs font-medium text-zinc-200 truncate">{skill.title}</div>
            {installed && (
              <span className="text-[10px] px-1 py-0.5 rounded bg-emerald-500/15 text-emerald-400 inline-flex items-center" data-id={`skill-market-installed-${skill.name}`}>
                <CheckCircle2 className="w-2.5 h-2.5" />
              </span>
            )}
            <span data-id="skill-marketplace-panel-auto-15" className="ml-auto text-[10px] text-zinc-600 shrink-0">v{skill.version}</span>
          </div>
          <div data-id="skill-marketplace-panel-auto-16" className="text-[11px] text-zinc-500 line-clamp-2 leading-snug min-h-[2.4em]">{skill.description}</div>
        </div>
      </div>
    </div>
  );
}

function StatusPill({ skill }: { skill: MarketSkill }) {
  const { t } = useTranslation('workspace');
  const status = skill.status;
  if (!status.installed) return null;
  if (status.last_error) {
    return <span title={status.last_error} className="text-[10px] px-1.5 py-0.5 rounded bg-red-500/15 text-red-400 inline-flex items-center gap-1"><AlertTriangle className="w-2.5 h-2.5" /> {t('marketplaceError')}: {status.last_error}</span>;
  }
  if (skill.config_file && !status.config_present) {
    if (skill.name === 'google') {
      return <span className="text-[10px] px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 inline-flex items-center gap-1"><AlertTriangle className="w-2.5 h-2.5" /> {t('marketplaceNeedsOAuth')}</span>;
    }
    return <span title={skill.config_file} className="text-[10px] px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 inline-flex items-center gap-1"><AlertTriangle className="w-2.5 h-2.5" /> {t('marketplaceMissingConfig')}: <code className="font-mono">{skill.config_file}</code></span>;
  }
  const missingDeps = status.requires_met ? Object.entries(status.requires_met).filter(([, met]) => !met).map(([dep]) => dep) : [];
  if (missingDeps.length > 0) {
    return <span title={missingDeps.join(', ')} className="text-[10px] px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-400 inline-flex items-center gap-1"><AlertTriangle className="w-2.5 h-2.5" /> {t('marketplaceMissingDep')}: {missingDeps.join(', ')}</span>;
  }
  return <span className="text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/15 text-emerald-400 inline-flex items-center gap-1"><CheckCircle2 className="w-2.5 h-2.5" /></span>;
}

function CategoryBadge({ category }: { category: string }) {
  const grad = CATEGORY_GRADIENT[category] || CATEGORY_GRADIENT.other;
  return (
    <span data-id="skill-marketplace-panel-auto-17" className={cn('inline-flex items-center text-[10px] px-1.5 py-0.5 rounded bg-gradient-to-br ring-1', grad)}>
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
        <span data-id="skill-marketplace-panel-auto-18"
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

function SkillDetailModal({ name, paneId, onClose, onInstall, onUninstall, onOpenProxyManager, onOpenProxySshManager }: {
  name: string;
  paneId: string;
  onClose: () => void;
  onInstall: () => Promise<void>;
  onUninstall: () => Promise<void>;
  onOpenProxyManager: () => void;
  onOpenProxySshManager: () => void;
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
  const [googleStatus, setGoogleStatus] = useState<{ connected: boolean; authorized_email?: string; has_shared_client?: boolean } | null>(null);
  const [googleBusy, setGoogleBusy] = useState(false);
  const [googleError, setGoogleError] = useState('');
  const [googleDevice, setGoogleDevice] = useState<{ state: string; user_code: string; verification_url: string; verification_url_complete: string } | null>(null);

  const fetchDetail = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiService.getMarketSkill(name);
      const payload = res?.data as SkillDetailPayload;
      setData(payload);
      const sk = payload?.skill;
      if (sk) setSendText(t('marketplaceTestPrompt', { name: sk.name }));
    } catch {
    } finally {
      setLoading(false);
    }
  }, [name]);

  useEffect(() => { fetchDetail(); }, [fetchDetail]);

  const refreshGoogleStatus = useCallback(async () => {
    if (name !== 'google') return;
    try {
      const res = await apiService.getGoogleSkillConfig();
      setGoogleStatus(res?.data || null);
    } catch (e: any) {
      setGoogleError(e?.message || 'load failed');
    }
  }, [name]);

  useEffect(() => { refreshGoogleStatus(); }, [refreshGoogleStatus]);

  const handleGoogleConnect = async () => {
    setGoogleBusy(true);
    setGoogleError('');
    setGoogleDevice(null);
    try {
      const res = await apiService.deviceConnectGoogleSkillConfig();
      const d = res?.data;
      if (!d?.state || !d?.user_code) {
        setGoogleError(d?.error || 'no device code returned');
        setGoogleBusy(false);
        return;
      }
      const device = {
        state: d.state as string,
        user_code: d.user_code as string,
        verification_url: (d.verification_url as string) || 'https://www.google.com/device',
        verification_url_complete: (d.verification_url_complete as string) || '',
      };
      setGoogleDevice(device);
      if (device.verification_url_complete) {
        window.open(device.verification_url_complete, '_blank', 'noopener');
      }
      let interval = (d.interval as number) || 5;
      const start = Date.now();
      const poll = async () => {
        if (Date.now() - start > 15 * 60 * 1000) {
          setGoogleError('timed out waiting for Google authorization');
          setGoogleBusy(false);
          setGoogleDevice(null);
          return;
        }
        try {
          const r = await apiService.devicePollGoogleSkillConfig(device.state);
          const p = r?.data;
          if (p?.connected) {
            setGoogleBusy(false);
            setGoogleDevice(null);
            await refreshGoogleStatus();
            return;
          }
          if (p?.error && p?.error !== 'slow_down') {
            setGoogleError(String(p.error));
            setGoogleBusy(false);
            setGoogleDevice(null);
            return;
          }
          if (p?.interval) interval = p.interval as number;
        } catch (e: any) {
          setGoogleError(e?.message || 'poll failed');
          setGoogleBusy(false);
          setGoogleDevice(null);
          return;
        }
        setTimeout(poll, interval * 1000);
      };
      setTimeout(poll, interval * 1000);
    } catch (e: any) {
      setGoogleError(e?.response?.data?.detail || e?.response?.data?.error || e?.message || 'connect failed');
      setGoogleBusy(false);
    }
  };

  const handleGoogleCancel = () => {
    setGoogleBusy(false);
    setGoogleDevice(null);
    setGoogleError('');
  };

  const handleGoogleDisconnect = async () => {
    const ok = await confirm({
      title: 'Disconnect Google?',
      body: <>This will revoke the refresh token and delete <code className="font-mono text-xs">~/cicy-ai/db/google.json</code>.</>,
      confirmLabel: 'Disconnect',
      danger: true,
    });
    if (!ok) return;
    setGoogleBusy(true);
    setGoogleError('');
    try {
      await apiService.disconnectGoogleSkillConfig();
      await refreshGoogleStatus();
    } catch (e: any) {
      setGoogleError(e?.message || 'disconnect failed');
    } finally {
      setGoogleBusy(false);
    }
  };

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
      await apiService.sendCommand(paneId, trimmed, true);
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
      await apiService.sendCommand(paneId, text, true);
      setSendOk(true);
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
      <div data-id="skill-marketplace-panel-auto-19" className="mx-4 w-full max-w-[760px] h-[80vh] flex flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60">
        {/* Header */}
        <div className="px-5 pt-5 pb-3 border-b border-white/[0.06] shrink-0" data-id="skill-detail-header">
          <div data-id="skill-marketplace-panel-auto-20" className="flex items-start gap-3">
            {skill ? <SkillAvatar skill={skill} size="lg" /> : <div className="w-14 h-14 rounded-xl bg-white/5" />}
            <div data-id="skill-marketplace-panel-auto-21" className="flex-1 min-w-0">
              <div data-id="skill-marketplace-panel-auto-22" className="flex items-center gap-2">
                <div className="text-base font-semibold text-zinc-100" data-id="skill-detail-title">
                  {skill?.title || (loading ? '…' : name)}
                </div>
                {skill && <StatusPill skill={skill} />}
              </div>
              <div data-id="skill-marketplace-panel-auto-23" className="text-[11px] text-zinc-500 mt-0.5">
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
                <div data-id="skill-marketplace-panel-auto-24" className="mt-3 flex items-center gap-2 flex-wrap">
                  {skill.status.installed ? (
                    <>
                      <button data-id="skill-detail-reinstall" onClick={handleInstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded border border-zinc-700 text-zinc-300 hover:text-zinc-100 hover:border-zinc-500 disabled:opacity-50 transition-colors inline-flex items-center gap-1">
                        {busy && <Loader2 className="w-3 h-3 animate-spin" />}
                        {busy ? t('marketplaceInstalling') : t('marketplaceReinstall')}
                      </button>
                      {skill.name === 'cicy-mihomo' ? (
                        <button data-id="skill-detail-manage-proxy" onClick={onOpenProxyManager} className="text-[12px] px-3 py-1.5 rounded bg-indigo-500/20 text-indigo-200 hover:bg-indigo-500/30 transition-colors inline-flex items-center gap-1">
                          <Shield className="w-3 h-3" />
                          {t('marketplaceManageProxy')}
                        </button>
                      ) : skill.name === 'proxy_ssh' ? (
                        // proxy_ssh is a managed skill — open the dedicated drawer;
                        // uninstall is intentionally not offered (the wrapper stays
                        // resident; users can stop individual profiles in the drawer).
                        <button data-id="skill-detail-manage-proxy-ssh" onClick={onOpenProxySshManager} className="text-[12px] px-3 py-1.5 rounded bg-indigo-500/20 text-indigo-200 hover:bg-indigo-500/30 transition-colors inline-flex items-center gap-1">
                          <Shield className="w-3 h-3" />
                          {t('marketplaceManageProxySsh')}
                        </button>
                      ) : skill.name === 'skill-author' ? null : (
                        <button data-id="skill-detail-uninstall" onClick={handleUninstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded text-zinc-400 hover:text-zinc-200 disabled:opacity-50 transition-colors">
                          {t('marketplaceUninstall')}
                        </button>
                      )}
                    </>
                  ) : (
                    <button data-id="skill-detail-install" onClick={handleInstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded bg-indigo-500/20 text-indigo-200 hover:bg-indigo-500/30 disabled:opacity-50 transition-colors inline-flex items-center gap-1">
                      {busy && <Loader2 className="w-3 h-3 animate-spin" />}
                      {busy ? t('marketplaceInstalling') : t('marketplaceInstall')}
                    </button>
                  )}
                  {skill.name === 'google' && googleStatus?.connected && (
                    <>
                      <span data-id="skill-detail-google-connected" className="text-[12px] px-2 py-1 rounded bg-emerald-500/15 text-emerald-300 inline-flex items-center gap-1.5">
                        <CheckCircle2 className="w-3 h-3" />
                        {googleStatus.authorized_email || 'connected'}
                      </span>
                      <button data-id="skill-detail-google-disconnect" onClick={handleGoogleDisconnect} disabled={googleBusy} className="text-[12px] px-2 py-1 rounded text-zinc-400 hover:text-zinc-200 disabled:opacity-50 transition-colors">
                        Disconnect
                      </button>
                    </>
                  )}
                  {skill.name === 'google' && !googleStatus?.connected && (
                    <button
                      data-id="skill-detail-google-connect"
                      onClick={() => {
                        apiService.sendCommand(paneId, GOOGLE_AUTH_PROMPT, true).catch(() => {});
                        onClose();
                      }}
                      className="text-[12px] px-3 py-1.5 rounded bg-blue-500/20 text-blue-200 hover:bg-blue-500/30 transition-colors inline-flex items-center gap-1"
                    >
                      <Send className="w-3 h-3" />
                      Authorize Google
                    </button>
                  )}
                  {skill.name === 'google' && googleError && (
                    <span className="text-[11px] text-rose-300">{googleError}</span>
                  )}
                </div>
              )}
              {skill?.name === 'google' && googleDevice && (
                <div data-id="skill-detail-google-device" className="mt-3 rounded-lg border border-blue-500/30 bg-blue-500/5 p-3 text-xs text-zinc-200">
                  <div className="flex items-center gap-2">
                    <Loader2 className="w-3 h-3 animate-spin text-blue-300" />
                    <span>Waiting for Google authorization…</span>
                  </div>
                  <div className="mt-2 leading-relaxed">
                    Open <a href={googleDevice.verification_url_complete || googleDevice.verification_url} target="_blank" rel="noopener" className="text-blue-300 underline">{googleDevice.verification_url}</a> and enter this code:
                  </div>
                  <div className="mt-2 flex items-center gap-2">
                    <code className="font-mono text-base tracking-[0.2em] px-2 py-1 rounded bg-black/30 text-zinc-100">{googleDevice.user_code}</code>
                    <button
                      onClick={() => copy(googleDevice.user_code)}
                      className="text-[11px] px-2 py-1 rounded border border-zinc-700 text-zinc-300 hover:text-zinc-100 hover:border-zinc-500 inline-flex items-center gap-1 transition-colors"
                    >
                      {copied === googleDevice.user_code ? <Check className="w-3 h-3 text-emerald-400" /> : <Copy className="w-3 h-3" />}
                      {copied === googleDevice.user_code ? 'Copied' : 'Copy'}
                    </button>
                    <button
                      onClick={handleGoogleCancel}
                      className="text-[11px] px-2 py-1 rounded text-zinc-400 hover:text-zinc-200 transition-colors"
                    >
                      Cancel
                    </button>
                  </div>
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
            <div data-id="skill-marketplace-panel-auto-25" className="text-xs text-zinc-500 flex items-center gap-2"><Loader2 className="w-3 h-3 animate-spin" /> {t('marketplaceModalLoading')}</div>
          ) : !skill ? (
            <div data-id="skill-marketplace-panel-auto-26" className="text-xs text-zinc-500">{t('marketplaceNoData')}</div>
          ) : tab === 'help' ? (
            <MarkdownPane content={data?.help_md || data?.skill_md || ''} onTry={sendToAgent} setSendText={setSendText} skillName={skill.name} clickable={false} />
          ) : (
            <MarkdownPane content={data?.tools_md || ''} onTry={sendToAgent} setSendText={setSendText} skillName={skill.name} clickable={true} />
          )}
        </div>

        {/* Send to agent — only enabled when skill is installed */}
        <div className="px-5 py-3 border-t border-white/[0.08] bg-[#101012] shrink-0" data-id="skill-detail-send">
          {!skill?.status.installed ? (
            <div data-id="skill-marketplace-panel-auto-27" className="flex items-center gap-2 text-[11px] text-zinc-500">
              <AlertTriangle className="w-3 h-3" />
              {t('marketplaceInstallFirst')}
            </div>
          ) : (
            <>
              <div data-id="skill-marketplace-panel-auto-28" className="flex items-end gap-2">
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
                  <span data-id="skill-marketplace-panel-auto-29" className="text-[13px]">{sendOk ? t('marketplaceSent') : t('marketplaceSendToAgent')}</span>
                </button>
              </div>
              {sendError && <div className="text-[11px] text-red-400 mt-1.5">{sendError}</div>}
              <div data-id="skill-marketplace-panel-auto-30" className="text-[10px] text-zinc-600 mt-1.5">
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

const MarkdownPane = memo(function MarkdownPane({ content, onTry, setSendText, skillName, clickable }: { content: string; onTry: (cmd: string) => void; setSendText: (s: string) => void; skillName: string; clickable: boolean }) {
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
        if (clickable) {
          return (
            <button data-id="skill-marketplace-panel-auto-31"
              onClick={() => setSendText(t('marketplaceTestToolPrompt', { name: skillName, command: text }))}
              className="px-1 py-0.5 rounded bg-white/[0.06] text-[11px] text-amber-200 font-mono hover:bg-white/[0.12] transition-colors"
              title={t('marketplaceLoadIntoSend')}
            >
              {text}
            </button>
          );
        }
        return (
          <code className="px-1 py-0.5 rounded bg-white/[0.06] text-[11px] text-amber-200 font-mono">{text}</code>
        );
      }
      return (
        <div data-id="skill-marketplace-panel-auto-32" className="my-2 rounded-md bg-black/40 border border-white/[0.04] overflow-hidden">
          {clickable && (
            <div data-id="skill-marketplace-panel-auto-33" className="flex items-center justify-end px-2 py-1 border-b border-white/[0.04]">
              <button data-id="skill-marketplace-panel-auto-34" onClick={() => onTry(text)} className="text-[10px] text-zinc-400 hover:text-zinc-100 transition-colors inline-flex items-center gap-1">
                <Send className="w-2.5 h-2.5" /> {t('marketplaceSendToAgent')}
              </button>
            </div>
          )}
          <pre className="text-[11px] text-zinc-300 font-mono whitespace-pre-wrap p-2"><code {...props}>{children}</code></pre>
        </div>
      );
    },
    blockquote: (p: any) => <blockquote className="border-l-2 border-zinc-600 pl-3 my-2 text-zinc-400" {...p} />,
    hr: () => <hr className="my-3 border-white/[0.06]" />,
    // Tables in tools.md are typically `| Command | Description |` shaped.
    // The default <table> would collapse columns in this narrow modal and
    // wrap text awkwardly, so we render each row as a stacked card instead:
    // first cell (command) on top as a chip, remaining cells below as muted
    // description text. Inline backticked commands stay clickable via the
    // `code` renderer above.
    table: ({ children }: any) => (
      <div data-id="skill-md-table" className="my-3 rounded-md border border-white/[0.06] divide-y divide-white/[0.04] overflow-hidden bg-black/20">
        {children}
      </div>
    ),
    thead: () => null,
    tbody: ({ children }: any) => <>{children}</>,
    // Each <tr> becomes a card. The CSS variant below targets the *first*
    // child (the command cell) and the rest (description cells) without
    // needing to know cell indices at the React level — `[&>*:first-child]`
    // styles the leading <td>, `[&>*:not(:first-child)]` styles the rest.
    tr: ({ children }: any) => (
      <div className="px-3 py-2 hover:bg-white/[0.03]
        [&>*:first-child]:font-mono [&>*:first-child]:text-amber-200 [&>*:first-child]:text-[12px] [&>*:first-child]:leading-snug [&>*:first-child]:break-words [&>*:first-child]:mb-1
        [&>*:not(:first-child)]:text-zinc-400 [&>*:not(:first-child)]:text-[11px] [&>*:not(:first-child)]:leading-relaxed">
        {children}
      </div>
    ),
    th: () => null,
    td: ({ children }: any) => <div>{children}</div>,
  }), [t, setSendText, onTry, skillName, clickable]);

  // Memoize the full Markdown render keyed on shown — re-parse only when the
  // displayed text actually changes (tab switch, translation toggle, fetch).
  const rendered = useMemo(() => (
    <Markdown remarkPlugins={[remarkGfm]} components={components}>{shown}</Markdown>
  ), [shown, components]);

  if (!content || !content.trim()) {
    return <div className="text-xs text-zinc-500">{t('marketplaceNoContent')}</div>;
  }

  return (
    <div data-id="skill-marketplace-panel-auto-35" className="relative">
      {showTranslate && (
        <div data-id="skill-marketplace-panel-auto-36" className="flex items-center justify-end mb-2 gap-2">
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
      <div data-id="skill-marketplace-panel-auto-37" className="prose-skill text-[13px] leading-relaxed text-zinc-300">{rendered}</div>
    </div>
  );
});
