// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo, useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import {
  Search, Loader2, CheckCircle2, AlertTriangle, RefreshCw, X, Send,
  Globe, Activity, Server, Plug, Mail, FileText, Code, Terminal, Key, Shield, Package, Cloud,
  Copy, Check, XCircle,
  ExternalLink, Tag, User, Calendar, HardDrive, Hash, FileCode, BookOpen, ShieldCheck, Lock,
  Compass, Users, RotateCw,
} from 'lucide-react';
import { cn, copyToClipboard } from '../../lib/utils';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';
import { ProxyManagerDialog } from './ProxyManagerDialog';
import { ProxySshManagerDialog } from './ProxySshManagerDialog';
import { FrpServerManagerDialog } from './FrpServerManagerDialog';
import { WebClientsDrawer } from './WebClientsDrawer';

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
  installed_version?: string;
  has_update?: boolean;
  source?: string;
  // Name of the private registry this skill came from (e.g. "team-a"). Empty
  // for the public registry. When set, the row shows a private badge.
  registry_source?: string;
}

let marketSkillsCache: MarketSkill[] | null = null;
let marketSkillsRequest: Promise<MarketSkill[]> | null = null;

async function fetchMarketSkills(force = false): Promise<MarketSkill[]> {
  if (!force && marketSkillsCache) return marketSkillsCache;
  if (marketSkillsRequest) return marketSkillsRequest;
  marketSkillsRequest = (async () => {
    const res = await apiService.listMarketSkills();
    marketSkillsCache = Array.isArray(res?.data?.skills) ? res.data.skills : [];
    return marketSkillsCache;
  })().finally(() => { marketSkillsRequest = null; });
  return marketSkillsRequest;
}

interface SkillDetailPayload {
  skill: MarketSkill;
  skill_md?: string;
  help_md?: string;
  tools_md?: string;
  manifest?: SkillManifest | null;
}

// SkillManifest is the raw v2 manifest as published to the registry. Used by
// the detail sidebar to render publisher / version / size / homepage / etc.
// All fields optional — the UI degrades gracefully when missing.
interface SkillManifest {
  name?: string;
  version?: string;
  title?: string;
  description?: string;
  category?: string;
  tags?: string[];
  author?: string;
  homepage?: string;
  license?: string;
  runtime?: { node?: string; python?: string };
  system_requirements?: string[];
  npm_dependencies?: boolean;
  entry?: string;
  permissions?: string[];
  compatible_agents?: string[];
  // Structured tool list registered at publish time. Each entry represents
  // one user-facing operation the skill provides. Shown as a clickable list
  // in the detail panel so users can test individual tools by sending the
  // example_prompt to the active agent.
  tools?: SkillTool[];
  publish?: {
    published_at?: string;
    sha256?: string;
    size?: number;
    download_url?: string;
    source?: { type?: string; repository?: string; tag?: string };
  };
}

interface SkillTool {
  name: string;           // e.g. "cping" or "cf curl"
  description: string;   // one-line description
  example?: string;      // literal CLI example for the code chip
  prompt?: string;       // prompt sent to agent when user clicks "try"
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
    <div data-id="skill-avatar-icon" className={cn('shrink-0 flex items-center justify-center bg-gradient-to-br ring-1', dim, grad)}>
      <Icon className={ico} />
    </div>
  );
}

export default function SkillMarketplacePanel({ paneId, onOpenDetail, onCloseDetail }: {
  paneId: string;
  onOpenDetail?: () => void;
  onCloseDetail?: () => void;
}) {
  const { t } = useTranslation('workspace');
  const [skills, setSkills] = useState<MarketSkill[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string>('');
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [, setBusy] = useState<Record<string, boolean>>({});
  const [selectedName, setSelectedName] = useState<string | null>(null);
  // Three-pillar hub: Discover (browse+install across all sources) / Publish
  // (my always-on self-hosted registry) / Subscribe (registries others shared).
  const [hubTab, setHubTab] = useState<'discover' | 'publish' | 'subscribe'>('discover');
  const [proxyManagerOpen, setProxyManagerOpen] = useState(false);
  const [proxySshManagerOpen, setProxySshManagerOpen] = useState(false);
  const [frpServerManagerOpen, setFrpServerManagerOpen] = useState(false);
  const [webClientsOpen, setWebClientsOpen] = useState(false);
  const [bulkUpdating, setBulkUpdating] = useState(false);
  const [bulkUpdateResult, setBulkUpdateResult] = useState('');
  // Stable refs for props passed to <SkillDetailModal>. Without these, every
  // parent re-render (e.g. the 5s WS poll cycle) emits new closures, which
  // propagates through sendToAgent → MarkdownPane.components → react-markdown
  // and forces the rendered markdown DOM to be replaced — wiping any text
  // selection the user has in the help/tools tab.
  const handleDetailClose = useCallback(() => {
    setSelectedName(null);
    onCloseDetail?.();
  }, [onCloseDetail]);
  const openDetail = useCallback((name: string) => {
    onOpenDetail?.();
    setSelectedName(name);
  }, [onOpenDetail]);
  const handleOpenProxyManager = useCallback(() => { setSelectedName(null); setProxyManagerOpen(true); }, []);
  const handleOpenProxySshManager = useCallback(() => { setSelectedName(null); setProxySshManagerOpen(true); }, []);
  const handleOpenFrpServerManager = useCallback(() => { setSelectedName(null); setFrpServerManagerOpen(true); }, []);
  const handleOpenWebClients = useCallback(() => { setSelectedName(null); setWebClientsOpen(true); }, []);

  const load = async (force = false) => {
    setLoading(true);
    setLoadError('');
    try {
      setSkills(await fetchMarketSkills(force));
    } catch (e: any) {
      setLoadError(e?.message || t('marketplaceLoadFailed'));
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

  // Group by SOURCE (team), not category: the public registry, then one section
  // per subscribed team (registry_source), then locally-authored skills. This
  // lets each subscribed team show its own skill list, with the same install /
  // update / uninstall actions as public skills.
  const grouped = useMemo(() => {
    const out: Record<string, MarketSkill[]> = {};
    for (const s of filtered) {
      const key = s.registry_source
        ? `team:${s.registry_source}`
        : (s.source === 'user' ? 'local' : 'public');
      (out[key] ||= []).push(s);
    }
    return out;
  }, [filtered]);

  // public first, then teams alphabetically, then local.
  const groupRank = (k: string) => (k === 'public' ? 0 : k === 'local' ? 2 : 1);
  const groupLabel = (k: string) => {
    if (k === 'public') return t('marketGroupPublic');
    if (k === 'local') return t('marketGroupLocal');
    return `${t('marketGroupTeam')} · ${k.slice('team:'.length)}`;
  };

  const counts = useMemo(() => ({
    total: skills.length,
    installed: skills.filter(s => s.status.installed).length,
    updates: skills.filter(s => s.status.installed && s.has_update).length,
  }), [skills]);

  const onInstall = async (skill: MarketSkill) => {
    setBusy(b => ({ ...b, [skill.name]: true }));
    try {
      const res = await apiService.installMarketSkill(skill.name);
      const data = res?.data;
      // server returned a fresh skill snapshot — patch in-place to avoid full re-load flicker
      if (data?.skill) {
        const fresh = data.skill as MarketSkill;
        setSkills(prev => prev.map(s => s.name === skill.name ? { ...s, ...fresh, has_update: fresh.has_update ?? false } : s));
      }
      await load(true);
      return data;
    } catch (e: any) {
      await load(true);
      return { ok: false, error: e?.message || t('marketplaceInstallFailed') };
    }
    finally { setBusy(b => { const { [skill.name]: _, ...rest } = b; return rest; }); }
  };

  const onUpdate = async (skill: MarketSkill) => {
    setBusy(b => ({ ...b, [skill.name]: true }));
    try {
      const res = await apiService.updateMarketSkill(skill.name);
      const data = res?.data;
      if (data?.skill) {
        const fresh = data.skill as MarketSkill;
        setSkills(prev => prev.map(s => s.name === skill.name ? { ...s, ...fresh, has_update: fresh.has_update ?? false } : s));
      }
      await load(true);
      return data;
    } catch (e: any) {
      await load(true);
      return { ok: false, error: e?.message || t('marketplaceUpdateFailed') };
    }
    finally { setBusy(b => { const { [skill.name]: _, ...rest } = b; return rest; }); }
  };

  const updateAll = async () => {
    const pending = skills.filter(skill => skill.status.installed && skill.has_update);
    if (pending.length === 0 || bulkUpdating) return;
    setBulkUpdating(true);
    setBulkUpdateResult('');
    let succeeded = 0;
    let failed = 0;
    for (const skill of pending) {
      const result = await onUpdate(skill);
      if (result?.ok === false) failed += 1;
      else succeeded += 1;
    }
    await load(true);
    setBulkUpdateResult(t('marketplaceUpdateAllResult', { succeeded, failed }));
    setBulkUpdating(false);
  };

  // Eject: keep the installed files in ~/cicy-ai/skills/<name>/ but rewrite
  // installed.json source to "local", so the skill becomes user-managed
  // (no auto-update) and surfaces with `source: "local"` in the catalog.
  const onEject = async (skill: MarketSkill) => {
    setBusy(b => ({ ...b, [skill.name]: true }));
    try {
      const res = await (apiService as any).ejectMarketSkill(skill.name);
      const data = res?.data;
      if (data?.skill) {
        const fresh = data.skill as MarketSkill;
        setSkills(prev => prev.map(s => s.name === skill.name ? { ...s, ...fresh } : s));
      }
      await load(true);
      return data;
    } catch (e: any) {
      await load(true);
      return { ok: false, error: e?.message || t('marketplaceEjectFailed') };
    }
    finally { setBusy(b => { const { [skill.name]: _, ...rest } = b; return rest; }); }
  };

  const onUninstall = async (skill: MarketSkill) => {
    setBusy(b => ({ ...b, [skill.name]: true }));
    try {
      const res = await apiService.uninstallMarketSkill(skill.name);
      const data = res?.data;
      if (data?.skill) {
        const fresh = data.skill as MarketSkill;
        setSkills(prev => prev.map(s => s.name === skill.name ? { ...s, ...fresh } : s));
      }
      await load(true);
      return data;
    } catch (e: any) {
      await load(true);
      return { ok: false, error: e?.message || t('marketplaceUninstallFailed') };
    }
    finally { setBusy(b => { const { [skill.name]: _, ...rest } = b; return rest; }); }
  };

  return (
    <>
      <div className="h-full flex flex-col overflow-hidden bg-[#0A0A0A]" data-id="skill-market-root">
        {/* three-pillar hub tabs */}
        <div className="px-2 pt-1.5 border-b border-[var(--vsc-border)] shrink-0 flex items-center gap-0.5" data-id="skill-hub-tabs">
          {(['discover', 'publish', 'subscribe'] as const).map((key) => {
            const Icon = key === 'discover' ? Compass : key === 'publish' ? Server : Users;
            const label = key === 'discover'
              ? t('marketplaceTabDiscover')
              : key === 'publish' ? t('marketplaceTabPublish') : t('marketplaceTabSubscribe');
            return (
              <button
                key={key}
                data-id={`skill-hub-tab-${key}`}
                onClick={() => setHubTab(key)}
                className={cn(
                  'px-2.5 py-1.5 text-[11px] rounded-t inline-flex items-center gap-1 border-b-2 -mb-px transition-colors',
                  hubTab === key
                    ? 'text-zinc-100 border-violet-400'
                    : 'text-zinc-500 border-transparent hover:text-zinc-300'
                )}
              >
                <Icon className="w-3 h-3" /> {label}
              </button>
            );
          })}
        </div>

        {hubTab === 'discover' && (
          <>
            <div className="px-3 py-2 border-b border-[var(--vsc-border)] shrink-0 space-y-2" data-id="skill-market-header">
              <div data-id="skill-market-search-wrap" className="relative">
                <Search className="w-3.5 h-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-zinc-500 pointer-events-none" />
                <input
                  data-id="skill-market-search"
                  value={query}
                  onChange={e => setQuery(e.target.value)}
                  placeholder={t('marketplaceSearchPlaceholder')}
                  className="w-full pl-7 pr-2 py-1.5 text-xs bg-[#0e0e0e] border border-[var(--vsc-border)] rounded text-zinc-300 placeholder:text-zinc-600 focus:outline-none focus:border-zinc-500"
                />
              </div>
              <div data-id="skill-market-filters" className="flex items-center gap-1 overflow-x-auto whitespace-nowrap text-[11px] scrollbar-hairline">
                {(['all','installed','available'] as Filter[]).map(f => (
                  <button
                    key={f}
                    data-id={`skill-market-filter-${f}`}
                    onClick={() => setFilter(f)}
                    className={cn(
                      'shrink-0 whitespace-nowrap px-2 py-0.5 rounded transition-colors',
                      filter === f ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'
                    )}
                  >
                    {f === 'all' && `${t('marketplaceAll')} ${counts.total}`}
                    {f === 'installed' && `${t('marketplaceInstalled')} ${counts.installed}`}
                    {f === 'available' && `${t('marketplaceAvailable')} ${counts.total - counts.installed}`}
                  </button>
                ))}
                <button
                  data-id="skill-market-update-all"
                  onClick={() => { void updateAll(); }}
                  disabled={bulkUpdating || counts.updates === 0}
                  className="ml-auto inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded px-2 py-0.5 text-amber-300 transition-colors hover:bg-amber-500/10 disabled:cursor-not-allowed disabled:text-zinc-600"
                  title={t('marketplaceUpdateAll')}
                >
                  <RefreshCw className={cn('h-3 w-3', bulkUpdating && 'animate-spin')} />
                  {bulkUpdating
                    ? t('marketplaceUpdatingAll')
                    : t('marketplaceUpdateAllCount', { count: counts.updates })}
                </button>
                <button data-id="skill-market-refresh" onClick={() => { void load(true); }} className="shrink-0 p-1 text-zinc-500 hover:text-zinc-300 rounded" title={t('marketplaceRetry')}>
                  <RefreshCw className={cn('w-3 h-3', loading && 'animate-spin')} />
                </button>
              </div>
              {bulkUpdateResult && (
                <div data-id="skill-market-update-all-result" className="text-[10px] text-zinc-500">
                  {bulkUpdateResult}
                </div>
              )}
            </div>

            <div className="flex-1 overflow-y-auto" data-id="skill-market-list">
              {loading && skills.length === 0 ? (
                <SkillListSkeleton />
              ) : loadError ? (
                <div data-id="skill-market-error" className="p-4 text-xs">
                  <div data-id="skill-market-error-msg" className="text-red-400 mb-2">{t('marketplaceFailedToLoad')}: {loadError}</div>
                  <button data-id="skill-market-error-retry" onClick={() => { void load(true); }} className="text-zinc-400 hover:text-zinc-100 underline">{t('marketplaceRetry')}</button>
                </div>
              ) : filtered.length === 0 ? (
                <div data-id="skill-market-empty" className="p-4 text-xs text-zinc-500">{t('marketplaceEmpty')}</div>
              ) : (
                Object.entries(grouped)
                  .sort(([a],[b]) => groupRank(a) - groupRank(b) || a.localeCompare(b))
                  .map(([group, list]) => (
                  <div key={group} data-id={`skill-market-group-${group}`}>
                    <div data-id="skill-market-group-label" className="px-3 pt-3 pb-1 text-[10px] uppercase tracking-wider text-zinc-600">{groupLabel(group)}</div>
                    <div data-id="skill-market-group-items">
                      {list.map(skill => (
                        <SkillRow
                          key={skill.name}
                          skill={skill}
                          selected={selectedName === skill.name}
                          onClick={() => openDetail(skill.name)}
                        />
                      ))}
                    </div>
                  </div>
                ))
              )}
            </div>
          </>
        )}

        {hubTab === 'publish' && (
          <div className="flex-1 overflow-y-auto p-4" data-id="skill-hub-publish">
            <PublishPanel onChanged={load} onOpenDetail={openDetail} />
          </div>
        )}

        {hubTab === 'subscribe' && (
          <div className="flex-1 overflow-y-auto p-4" data-id="skill-hub-subscribe">
            <SubscribePanel onChanged={load} skills={skills} onOpenDetail={openDetail} selectedName={selectedName} />
          </div>
        )}
      </div>

      {selectedName && (
        <SkillDetailModal
          name={selectedName}
          paneId={paneId}
          onClose={handleDetailClose}
          onInstall={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) return await onInstall(sk);
          }}
          onUninstall={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) return await onUninstall(sk);
          }}
          onUpdate={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) return await onUpdate(sk);
          }}
          onEject={async () => {
            const sk = skills.find(s => s.name === selectedName);
            if (sk) return await onEject(sk);
          }}
          onOpenProxyManager={handleOpenProxyManager}
          onOpenProxySshManager={handleOpenProxySshManager}
          onOpenFrpServerManager={handleOpenFrpServerManager}
          onOpenWebClients={handleOpenWebClients}
        />
      )}
      <ProxyManagerDialog open={proxyManagerOpen} onClose={() => setProxyManagerOpen(false)} paneId={paneId} />
      <ProxySshManagerDialog open={proxySshManagerOpen} onClose={() => setProxySshManagerOpen(false)} />
      <FrpServerManagerDialog open={frpServerManagerOpen} onClose={() => setFrpServerManagerOpen(false)} />
      <WebClientsDrawer open={webClientsOpen} onClose={() => setWebClientsOpen(false)} paneId={paneId} />
    </>
  );
}

function SkillListSkeleton() {
  // Mirrors a real SkillRow's 68px height + 3 fake category groups.
  const groups = [
    { name: 'network', rows: 3 },
    { name: 'ai', rows: 2 },
    { name: 'infra', rows: 2 },
  ];
  return (
    <div data-id="skill-market-skeleton">
      {groups.map((g) => (
        <div key={g.name}>
          <div className="px-3 pt-3 pb-1">
            <div className="h-2.5 w-16 rounded bg-white/[0.05] animate-pulse" />
          </div>
          {Array.from({ length: g.rows }).map((_, i) => (
            <div
              key={i}
              className="h-[68px] px-3 py-2 border-b border-[var(--vsc-border)]/40 flex items-center"
            >
              <div className="flex items-start gap-2.5 w-full">
                <div className="shrink-0 w-9 h-9 rounded-lg bg-white/[0.05] animate-pulse" />
                <div className="flex-1 min-w-0 space-y-1.5">
                  <div className="flex items-center gap-2">
                    <div className="h-2.5 w-24 rounded bg-white/[0.06] animate-pulse" />
                    <div className="ml-auto h-2 w-8 rounded bg-white/[0.04] animate-pulse" />
                  </div>
                  <div className="h-2 w-full rounded bg-white/[0.04] animate-pulse" />
                  <div className="h-2 w-3/4 rounded bg-white/[0.04] animate-pulse" />
                </div>
              </div>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

// SubscribePanel — registries others shared with you. Add by pasting their
// address + token; their skills then appear in Discover (badged by source).
// Multiple peers supported; the public registry is always present (built-in).
interface RegistrySourceView {
  name: string;
  url: string;
  has_token: boolean;
  public: boolean;
}

function SubscribePanel({ onChanged, skills, onOpenDetail, selectedName }: { onChanged: () => void; skills: MarketSkill[]; onOpenDetail: (name: string) => void; selectedName: string | null }) {
  const { t } = useTranslation('workspace');
  const { confirm, node: confirmNode } = useDialogs();
  const [sources, setSources] = useState<RegistrySourceView[]>([]);
  const [loading, setLoading] = useState(true);
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiService.listSkillRegistries();
      setSources((res?.data?.sources || []) as RegistrySourceView[]);
    } catch (e: any) {
      setError(e?.message || 'failed to load sources');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  const add = async () => {
    if (!url.trim()) { setError(t('subscribeErrUrl')); return; }
    if (!name.trim()) { setError(t('subscribeErrName')); return; }
    setBusy(true);
    setError('');
    try {
      // url may carry ?token=… (the share link); backend extracts it.
      await apiService.addSkillRegistry({ name: name.trim(), url: url.trim() });
      setName(''); setUrl('');
      await refresh();
      onChanged();
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || t('marketplaceError'));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (key: string) => {
    const ok = await confirm({
      title: t('subscribeRemoveTitle'),
      body: t('subscribeRemoveConfirm', { name: key }),
      danger: true,
      confirmLabel: t('subscribeRemove'),
    });
    if (!ok) return;
    setBusy(true);
    setError('');
    try {
      await apiService.removeSkillRegistry(key);
      await refresh();
      onChanged();
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || t('marketplaceError'));
    } finally {
      setBusy(false);
    }
  };

  const teams = sources.filter(s => !s.public);
  return (
    <div className="space-y-4" data-id="subscribe-panel">
          {confirmNode}
          {/* Subscribe to a source — primary action, at the top */}
          <div className="space-y-2" data-id="skill-registry-add">
            <div className="text-[11px] font-medium text-zinc-400">{t('subscribeAddTitle')}</div>
            <input
              data-id="skill-registry-add-url"
              value={url}
              onChange={e => setUrl(e.target.value)}
              placeholder={t('subscribeUrlPlaceholder')}
              className="w-full px-2 py-1.5 text-xs bg-[#0a0a0a] border border-[var(--vsc-border)] rounded text-zinc-300 placeholder:text-zinc-600 focus:outline-none focus:border-zinc-500"
            />
            <input
              data-id="skill-registry-add-name"
              value={name}
              onChange={e => setName(e.target.value)}
              placeholder={t('subscribeNamePlaceholder')}
              className="w-full px-2 py-1.5 text-xs bg-[#0a0a0a] border border-[var(--vsc-border)] rounded text-zinc-300 placeholder:text-zinc-600 focus:outline-none focus:border-zinc-500"
            />
            {error && <div data-id="skill-registry-error" className="text-[11px] text-red-400">{error}</div>}
            <button
              data-id="skill-registry-add-btn"
              onClick={add}
              disabled={busy || !url.trim() || !name.trim()}
              className="w-full px-3 py-1.5 text-xs rounded bg-violet-500/20 text-violet-200 hover:bg-violet-500/30 disabled:opacity-40 inline-flex items-center justify-center gap-1"
            >
              {busy ? <Loader2 className="w-3 h-3 animate-spin" /> : <Lock className="w-3 h-3" />}
              {t('subscribeAdd')}
            </button>
          </div>

          {/* Subscribed teams — each with its own skill list (same rows as the market) */}
          <div data-id="skill-registries-list" className="space-y-3 pt-3 border-t border-[var(--vsc-border)]/40">
            {loading ? (
              <div className="text-[11px] text-zinc-600 flex items-center gap-1"><Loader2 className="w-3 h-3 animate-spin" /> {t('marketplaceModalLoading')}</div>
            ) : teams.length === 0 ? (
              <div className="text-[11px] text-zinc-600" data-id="skill-registry-empty">{t('subscribeNoSources')}</div>
            ) : teams.map(s => {
              const teamSkills = skills.filter(k => k.registry_source === s.name);
              return (
              <div key={s.name} data-id={`skill-registry-${s.name}`}>
                {/* team header */}
                <div className="flex items-center gap-2 px-1 pb-1.5">
                  <span className="text-[10px] px-1 py-0.5 rounded inline-flex items-center gap-0.5 bg-violet-500/15 text-violet-300">
                    <Lock className="w-2.5 h-2.5" />
                    {s.name}
                  </span>
                  <span className="text-[11px] text-zinc-500 truncate flex-1" title={s.url}>{s.url}</span>
                  {s.has_token && <Key className="w-3 h-3 text-zinc-600" />}
                  <button
                    data-id={`skill-registry-remove-${s.name}`}
                    onClick={() => remove(s.name)}
                    disabled={busy}
                    className="p-0.5 text-zinc-600 hover:text-red-400 rounded disabled:opacity-40"
                    title={t('subscribeRemove')}
                  >
                    <XCircle className="w-3.5 h-3.5" />
                  </button>
                </div>
                {/* this team's skills — identical rows to the market (icon + name + description + status), click to open detail (install / update / uninstall) */}
                <div data-id={`skill-registry-skills-${s.name}`} className="rounded border border-[var(--vsc-border)]/40 overflow-hidden">
                  {teamSkills.length === 0 ? (
                    <div className="text-[10px] text-zinc-600 px-3 py-2" data-id={`skill-registry-skill-${s.name}-empty`}>{t('subscribeNoSkills')}</div>
                  ) : teamSkills.map(k => (
                    <SkillRow
                      key={k.name}
                      skill={k}
                      selected={selectedName === k.name}
                      onClick={() => onOpenDetail(k.name)}
                      hideSourceBadge
                    />
                  ))}
                </div>
              </div>
              );
            })}
          </div>
    </div>
  );
}

// PublishPanel — this node's always-on self-hosted registry (no start/stop).
// Share its address + read token with others; publish/yank your private skills.
// The address is the node's public URL (CICY_PUBLIC_URL) + "/registry".
function PublishPanel({ onChanged, onOpenDetail }: { onChanged: () => void; onOpenDetail: (name: string) => void }) {
  const { t } = useTranslation('workspace');
  const [status, setStatus] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [copied, setCopied] = useState('');

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiService.getLocalRegistry();
      setStatus(res?.data || null);
    } catch (e: any) {
      setError(e?.message || t('marketplaceError'));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  const copy = (text: string, key: string) => {
    copyToClipboard(text);
    setCopied(key);
    setTimeout(() => setCopied(''), 1200);
  };

  const rotate = async () => {
    if (!window.confirm(t('publishRotateConfirm'))) return;
    setBusy(true); setError('');
    try { await apiService.rotateLocalRegistry(); await refresh(); onChanged(); }
    catch (e: any) { setError(e?.response?.data?.detail || e?.message || t('marketplaceError')); }
    finally { setBusy(false); }
  };
  const setShared = async (name: string, on: boolean) => {
    setBusy(true); setError('');
    try {
      if (on) await apiService.publishLocalRegistry(name);
      else await apiService.unpublishLocalRegistry(name);
      await refresh();
      onChanged();
    } catch (e: any) { setError(e?.response?.data?.detail || e?.message || t('marketplaceError')); }
    finally { setBusy(false); }
  };

  if (loading && !status) {
    return <div className="text-[11px] text-zinc-600 flex items-center gap-1"><Loader2 className="w-3 h-3 animate-spin" /> {t('marketplaceModalLoading')}</div>;
  }
  const shareLink = status?.share_url || '';
  const skills = (status?.my_skills || []) as { name: string; title?: string; description?: string; category?: string; version: string; published: boolean }[];

  return (
    <div className="space-y-4" data-id="publish-panel">
      {error && <div className="text-[11px] text-red-400">{error}</div>}

      {/* share card — one link (token embedded) to copy & send */}
      <div className="space-y-1.5 p-2.5 rounded bg-white/[0.03] border border-[var(--vsc-border)]/40" data-id="publish-share">
        <div className="text-[11px] text-zinc-400">{t('publishShareLabel')}</div>
        <div className="flex items-center gap-2">
          <code className="flex-1 px-2 py-1 text-[11px] bg-[#0a0a0a] border border-[var(--vsc-border)] rounded text-violet-300 truncate font-mono" data-id="publish-share-url">{shareLink || '—'}</code>
          <button onClick={() => copy(shareLink, 'link')} disabled={!shareLink} className="p-1 text-zinc-500 hover:text-zinc-200 rounded disabled:opacity-40" title={t('publishCopyLink')}>
            {copied === 'link' ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
          </button>
          <button onClick={rotate} disabled={busy} className="p-1 text-zinc-500 hover:text-amber-300 rounded disabled:opacity-40" title={t('publishRefreshToken')}>
            <RotateCw className={cn('w-3.5 h-3.5', busy && 'animate-spin')} />
          </button>
        </div>
      </div>

      {/* my skills — toggle each to share into my registry */}
      <div className="space-y-2 pt-1">
        {skills.length > 0 ? (
          <div className="-mx-4 pt-1" data-id="publish-list">
            {skills.map(s => (
              <SkillRow
                key={s.name}
                selected={false}
                onClick={() => onOpenDetail(s.name)}
                skill={{
                  name: s.name,
                  title: s.title || s.name,
                  description: s.description || '',
                  version: s.version,
                  category: s.category || 'other',
                  source: 'private',
                  tags: [],
                  status: { installed: false, config_present: true },
                } as unknown as MarketSkill}
                trailing={
                  <div className="flex items-center gap-2">
                    {s.published && <span className="text-[10px] px-1 py-0.5 rounded bg-emerald-500/15 text-emerald-400">{t('publishedBadge')}</span>}
                    <button
                      data-id={`publish-toggle-${s.name}`}
                      onClick={() => setShared(s.name, !s.published)}
                      disabled={busy}
                      role="switch"
                      aria-checked={s.published}
                      title={s.published ? t('publishToggleOff') : t('publishToggleOn')}
                      className={cn('relative w-9 h-5 rounded-full transition-colors shrink-0 disabled:opacity-40', s.published ? 'bg-emerald-500/70' : 'bg-zinc-600/50')}
                    >
                      <span className={cn('absolute top-0.5 w-4 h-4 rounded-full bg-white transition-all', s.published ? 'left-[18px]' : 'left-0.5')} />
                    </button>
                  </div>
                }
              />
            ))}
          </div>
        ) : (
          <div className="text-[11px] text-zinc-600 pt-1">{t('publishEmpty')}</div>
        )}
      </div>
    </div>
  );
}

function SkillRow({ skill, selected, onClick, trailing, hideSourceBadge }: {
  skill: MarketSkill;
  selected: boolean;
  onClick: () => void;
  trailing?: ReactNode;
  // Hide the registry-source badge when the row is already shown under that
  // team's header (subscribe tab) — avoids repeating "hk" on every row.
  hideSourceBadge?: boolean;
}) {
  const { t } = useTranslation('workspace');
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
        'group min-h-[68px] px-3 py-2 border-b border-[var(--vsc-border)]/40 transition-colors cursor-pointer focus:outline-none flex items-start relative',
        selected
          ? 'bg-indigo-500/[0.08] before:absolute before:left-0 before:top-1 before:bottom-1 before:w-[2px] before:rounded-r before:bg-indigo-400'
          : 'hover:bg-white/[0.03] focus:bg-white/[0.05]'
      )}
    >
      <div data-id="skill-row-inner" className="flex items-start gap-2.5 w-full">
        <SkillAvatar skill={skill} size="md" />
        <div data-id="skill-row-info" className="flex-1 min-w-0">
          <div data-id="skill-row-title-row" className="flex items-center gap-2 mb-0.5">
            <div data-id="skill-row-title" className="text-xs font-medium text-zinc-200 truncate">{skill.title}</div>
            {skill.registry_source && !hideSourceBadge && (
              <span
                className="text-[10px] px-1 py-0.5 rounded bg-violet-500/15 text-violet-300 inline-flex items-center gap-0.5 shrink-0"
                data-id={`skill-market-private-${skill.name}`}
                title={t('marketplacePrivateRegistry', { name: skill.registry_source })}
              >
                <Lock className="w-2.5 h-2.5" />
                <span>{skill.registry_source}</span>
              </span>
            )}
            {installed && !skill.has_update && (
              <span className="text-[10px] px-1 py-0.5 rounded bg-emerald-500/15 text-emerald-400 inline-flex items-center" data-id={`skill-market-installed-${skill.name}`}>
                <CheckCircle2 className="w-2.5 h-2.5" />
              </span>
            )}
            {installed && skill.has_update && (
              <span className="text-[10px] px-1 py-0.5 rounded bg-amber-500/20 text-amber-300 inline-flex items-center gap-0.5" data-id={`skill-market-update-${skill.name}`} title={`${skill.installed_version} → ${skill.version}`}>
                <span>v{skill.version}</span>
              </span>
            )}
            <span data-id="skill-row-version" className="ml-auto text-[10px] text-zinc-600 shrink-0">
              {installed && skill.installed_version && skill.installed_version !== 'user' && !skill.has_update
                ? `v${skill.installed_version}`
                : `v${skill.version}`}
            </span>
          </div>
          <div data-id="skill-row-desc" className="text-[11px] text-zinc-500 leading-snug">{skill.description}</div>
        </div>
        {trailing && <div className="shrink-0 self-center" onClick={(e) => e.stopPropagation()}>{trailing}</div>}
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
    <span data-id="skill-cat-badge" className={cn('inline-flex items-center text-[10px] px-1.5 py-0.5 rounded bg-gradient-to-br ring-1', grad)}>
      {category}
    </span>
  );
}

function InlineStatus({ skill }: { skill: MarketSkill }) {
  const { t } = useTranslation('workspace');
  const pills: { ok: boolean; label: string; title?: string }[] = [];
  if (skill.status.installed) {
    pills.push({ ok: !skill.status.last_error, label: skill.status.last_error ? t('marketplaceError') : t('marketplaceInstalled'), title: skill.status.last_error });
  } else {
    pills.push({ ok: false, label: t('marketplaceAvailable') });
  }
  if (skill.config_file) {
    pills.push({ ok: !!skill.status.config_present, label: t('marketplaceConfig'), title: skill.config_file });
  }
  if (skill.status.requires_met) {
    for (const [dep, met] of Object.entries(skill.status.requires_met)) {
      pills.push({ ok: met, label: dep });
    }
  }
  return (
    <>
      {pills.map((p, i) => (
        <span data-id="skill-inline-status-pill"
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


function SkillDetailModal({ name, paneId, onClose, onInstall, onUninstall, onUpdate, onEject, onOpenProxyManager, onOpenProxySshManager, onOpenFrpServerManager, onOpenWebClients }: {
  name: string;
  paneId: string;
  onClose: () => void;
  onInstall: () => Promise<{ log?: string; ok?: boolean; error?: string } | void>;
  onUninstall: () => Promise<{ log?: string; ok?: boolean; error?: string } | void>;
  onUpdate: () => Promise<{ log?: string; ok?: boolean; error?: string; from?: string; to?: string; updated?: boolean } | void>;
  onEject: () => Promise<{ log?: string; ok?: boolean; error?: string; source?: string; path?: string } | void>;
  onOpenProxyManager: () => void;
  onOpenProxySshManager: () => void;
  onOpenFrpServerManager: () => void;
  onOpenWebClients: () => void;
}) {
  const { t } = useTranslation('workspace');
  const [data, setData] = useState<SkillDetailPayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [busyAction, setBusyAction] = useState<'install' | 'update' | 'uninstall' | 'eject' | null>(null);
  const busy = busyAction !== null;
  const [installLog, setInstallLog] = useState('');
  const [installError, setInstallError] = useState('');
  const [, setSendText] = useState('');
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
      setGoogleError(e?.message || t('marketplaceLoadFailed'));
    }
  }, [name]);

  useEffect(() => { refreshGoogleStatus(); }, [refreshGoogleStatus]);

  const handleGoogleCancel = () => {
    setGoogleBusy(false);
    setGoogleDevice(null);
    setGoogleError('');
  };

  const handleGoogleDisconnect = async () => {
    const ok = await confirm({
      title: t('marketplaceGoogleDisconnectTitle'),
      body: <>{t('marketplaceGoogleDisconnectBody')} <code className="font-mono text-xs">~/cicy-ai/db/google.json</code>.</>,
      confirmLabel: t('marketplaceGoogleDisconnect'),
      danger: true,
    });
    if (!ok) return;
    setGoogleBusy(true);
    setGoogleError('');
    try {
      await apiService.disconnectGoogleSkillConfig();
      await refreshGoogleStatus();
    } catch (e: any) {
      setGoogleError(e?.message || t('marketplaceGoogleDisconnectFailed'));
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
    copyToClipboard(text);
    setCopied(text);
    setTimeout(() => setCopied(prev => prev === text ? '' : prev), 1200);
  };

  const { confirm, node: confirmNode } = useDialogs();

  const handleInstall = async () => {
    setBusyAction('install');
    setInstallLog('');
    setInstallError('');
    try {
      const r = await onInstall();
      if (r && typeof r === 'object') {
        if (r.log) setInstallLog(r.log);
        if (r.ok === false) setInstallError(r.error || t('marketplaceInstallFailed'));
      }
      await fetchDetail();
    } catch (e: any) {
      setInstallError(e?.message || t('marketplaceInstallFailed'));
    } finally {
      setBusyAction(null);
    }
  };
  const handleUpdate = async () => {
    setBusyAction('update');
    setInstallLog('');
    setInstallError('');
    try {
      const r = await onUpdate();
      if (r && typeof r === 'object') {
        if (r.log) setInstallLog(r.log);
        if (r.ok === false) setInstallError(r.error || t('marketplaceUpdateFailed'));
      }
      await fetchDetail();
    } catch (e: any) {
      setInstallError(e?.message || t('marketplaceUpdateFailed'));
    } finally {
      setBusyAction(null);
    }
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
    setBusyAction('uninstall');
    setInstallLog('');
    setInstallError('');
    try {
      const r = await onUninstall();
      if (r && typeof r === 'object') {
        if (r.log) setInstallLog(r.log);
        if (r.ok === false) setInstallError(r.error || t('marketplaceUninstallFailed'));
      }
      await fetchDetail();
    } catch (e: any) {
      setInstallError(e?.message || t('marketplaceUninstallFailed'));
    } finally {
      setBusyAction(null);
    }
  };

  const handleEject = async () => {
    const ok = await confirm({
      title: t('marketplaceEjectConfirmTitle', { defaultValue: 'Eject to local?', title: data?.skill?.title || name }),
      body: t('marketplaceEjectConfirmBody', { defaultValue: 'The skill files stay in place under ~/cicy-ai/skills/' + name + '/, but the installed.json source is rewritten to local — future skill updates will skip it. You can re-install any time to restore the registry version.' }),
      confirmLabel: t('marketplaceEject', { defaultValue: 'Eject' }),
    });
    if (!ok) return;
    setBusyAction('eject');
    setInstallLog('');
    setInstallError('');
    try {
      const r = await onEject();
      if (r && typeof r === 'object') {
        if (r.log) setInstallLog(r.log);
        if (r.ok === false) setInstallError(r.error || t('marketplaceEjectFailed'));
      }
      await fetchDetail();
    } catch (e: any) {
      setInstallError(e?.message || t('marketplaceEjectFailed'));
    } finally {
      setBusyAction(null);
    }
  };

  // Stable sender — referentially identical across renders so memoized
  // children (MarkdownPane) don't re-render on every parent state tick.
  // Optimistic: close instantly, send in the background, toast the result.
  const sendToAgent = useCallback((text: string) => {
    const trimmed = (text || '').trim();
    if (!trimmed || !paneId) return;
    onClose();
    const toast = (msg: string) => window.dispatchEvent(new CustomEvent('show-toast', { detail: msg }));
    apiService.sendCommand(paneId, trimmed, true)
      .then(() => toast(i18n.t('marketplaceSentToast', { ns: 'workspace', pane: paneId })))
      .catch(() => toast(i18n.t('marketplaceSentToastFailed', { ns: 'workspace', pane: paneId })));
  }, [paneId, onClose]);

  const skill = data?.skill;

  const [portalTarget, setPortalTarget] = useState<HTMLElement | null>(null);
  useEffect(() => {
    // Keep marketplace navigation self-contained: opening a skill detail
    // replaces the right drawer content instead of covering the main
    // workspace/editor in the middle panel.
    setPortalTarget(document.querySelector('[data-id="right-panel"]') as HTMLElement | null);
  }, []);
  if (!portalTarget) return null;

  return createPortal(
    <div
      className="absolute inset-0 z-30 animate-[fadeIn_120ms_ease-out]"
      data-id="skill-detail-modal"
    >
      <div data-id="skill-detail-container" className="h-full w-full flex flex-col overflow-hidden bg-[#161618]">
        {/* Header */}
        <div className="px-5 pt-5 pb-3 border-b border-white/[0.06] shrink-0" data-id="skill-detail-header">
          <div data-id="skill-detail-header-inner" className="flex items-start gap-3">
            {skill ? <SkillAvatar skill={skill} size="lg" /> : <div className="w-14 h-14 rounded-xl bg-white/[0.07] animate-pulse shrink-0" />}
            <div data-id="skill-detail-meta" className="flex-1 min-w-0">
              {loading && !skill ? (
                <div className="space-y-2 pt-0.5">
                  <div className="h-4 w-36 rounded-md bg-white/[0.08] animate-pulse" />
                  <div className="h-3 w-24 rounded-md bg-white/[0.05] animate-pulse" />
                  <div className="flex gap-1.5 mt-1.5">
                    <div className="h-4 w-14 rounded bg-white/[0.05] animate-pulse" />
                    <div className="h-4 w-10 rounded bg-white/[0.05] animate-pulse" />
                  </div>
                  <div className="h-3 w-full rounded-md bg-white/[0.05] animate-pulse mt-1" />
                  <div className="h-3 w-4/5 rounded-md bg-white/[0.05] animate-pulse" />
                  <div className="flex gap-2 mt-2">
                    <div className="h-6 w-20 rounded bg-white/[0.05] animate-pulse" />
                  </div>
                </div>
              ) : (
                <>
              <div data-id="skill-detail-title-row" className="flex items-center gap-2">
                <div className="text-base font-semibold text-zinc-100" data-id="skill-detail-title">
                  {skill?.title || name}
                </div>
                {skill && <StatusPill skill={skill} />}
                {skill?.source === 'local' && (
                  <span
                    data-id="skill-detail-source-local"
                    title={t('marketplaceSourceLocalTooltip', { defaultValue: 'Local override — installed.json source=local; no auto-update.' })}
                    className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-200 leading-none"
                  >local</span>
                )}
                {skill?.source === 'user' && (
                  <span
                    data-id="skill-detail-source-user"
                    title={t('marketplaceSourceUserTooltip', { defaultValue: 'User-authored skill under ~/cicy-ai/skills/' })}
                    className="text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded bg-zinc-700/40 text-zinc-300 leading-none"
                  >user</span>
                )}
              </div>
              <div data-id="skill-detail-version-row" className="text-[11px] text-zinc-500 mt-0.5">
                {skill ? (
                  <>
                    {skill.installed_version && skill.installed_version !== 'user' ? (
                      skill.has_update ? (
                        <span className="inline-flex items-center gap-1">
                          <span className="text-amber-300">v{skill.installed_version}</span>
                          <span>→</span>
                          <span className="text-emerald-300">v{skill.version}</span>
                          <span className="ml-1">{t('marketplacePublisher')}</span>
                        </span>
                      ) : (
                        <>{t('marketplaceVersionInstalled')} v{skill.installed_version} · {t('marketplacePublisher')}</>
                      )
                    ) : (
                      <>v{skill.version} · {t('marketplacePublisher')}</>
                    )}
                  </>
                ) : ''}
              </div>
              {skill && (
                <div className="mt-1.5 flex flex-wrap items-center gap-1.5" data-id="skill-detail-status-inline">
                  <CategoryBadge category={skill.category} />
                  <InlineStatus skill={skill} />
                </div>
              )}
              {skill && <div className="mt-2 text-xs text-zinc-300 leading-relaxed">{skill.description}</div>}
              {(installLog || installError) && (
                <div data-id="skill-detail-install-log" className="mt-2">
                  {installError && (
                    <div className="text-[11px] text-red-400 mb-1 inline-flex items-center gap-1">
                      <XCircle className="w-3 h-3" />
                      {installError}
                    </div>
                  )}
                  {installLog && (
                    <pre className="text-[10px] leading-tight text-zinc-400 bg-black/30 border border-white/[0.05] rounded p-2 max-h-32 overflow-auto whitespace-pre-wrap font-mono">
{installLog}
                    </pre>
                  )}
                </div>
              )}
                </>
              )}
              {skill && skill.source === 'private' && (
                <div data-id="skill-detail-private-note" className="mt-3 text-[11px] text-zinc-500 inline-flex items-center gap-1.5">
                  <Lock className="w-3 h-3 text-violet-300" />
                  {t('publishPrivateNote', { defaultValue: '私有库 skill —— 在「我的库」用「公开」开关分享给别人' })}
                </div>
              )}
              {skill && skill.source !== 'private' && (
                <div data-id="skill-detail-actions" className="mt-3 flex items-center gap-2 flex-wrap">
                  {skill.status.installed ? (
                    <>
                      <button data-id="skill-detail-update" onClick={handleUpdate} disabled={busy} className={`text-[12px] px-3 py-1.5 rounded disabled:opacity-50 transition-colors inline-flex items-center gap-1 ${skill.has_update ? 'bg-amber-500/20 text-amber-200 hover:bg-amber-500/30' : 'border border-zinc-700 text-zinc-400 hover:text-zinc-200 hover:border-zinc-500'}`}>
                        {busyAction === 'update' ? <Loader2 className="w-3 h-3 animate-spin" /> : <RefreshCw className="w-3 h-3" />}
                        {busyAction === 'update' ? t('marketplaceUpdating') : skill.has_update ? `${t('marketplaceUpdate')} → v${skill.version}` : t('marketplaceUpdate')}
                      </button>
                      <button data-id="skill-detail-reinstall" onClick={handleInstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded border border-zinc-700 text-zinc-300 hover:text-zinc-100 hover:border-zinc-500 disabled:opacity-50 transition-colors inline-flex items-center gap-1">
                        {busyAction === 'install' && <Loader2 className="w-3 h-3 animate-spin" />}
                        {busyAction === 'install' ? t('marketplaceInstalling') : t('marketplaceReinstall')}
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
                      ) : skill.name === 'frp-server' ? (
                        <button data-id="skill-detail-manage-frp-server" onClick={onOpenFrpServerManager} className="text-[12px] px-3 py-1.5 rounded bg-indigo-500/20 text-indigo-200 hover:bg-indigo-500/30 transition-colors inline-flex items-center gap-1">
                          <Server className="w-3 h-3" />
                          {t('frpServerManagerTitle')}
                        </button>
                      ) : skill.name === 'agent-webpage' ? (
                        <button data-id="skill-detail-manage-web-clients" onClick={onOpenWebClients} className="text-[12px] px-3 py-1.5 rounded bg-indigo-500/20 text-indigo-200 hover:bg-indigo-500/30 transition-colors inline-flex items-center gap-1">
                          <Globe className="w-3 h-3" />
                          Clients
                        </button>
                      ) : (
                        <>
                          {!skill.source && (
                            <button
                              data-id="skill-detail-eject"
                              onClick={handleEject}
                              disabled={busy}
                              title={t('marketplaceEjectTooltip', { defaultValue: 'Convert to a local override; files stay editable, no auto-update.' })}
                              className="text-[12px] px-3 py-1.5 rounded bg-amber-500/15 text-amber-200 hover:bg-amber-500/25 disabled:opacity-50 transition-colors inline-flex items-center gap-1"
                            >
                              {busyAction === 'eject' && <Loader2 className="w-3 h-3 animate-spin" />}
                              {busyAction === 'eject' ? t('marketplaceEjecting', { defaultValue: 'Ejecting…' }) : t('marketplaceEject', { defaultValue: 'Eject' })}
                            </button>
                          )}
                          <button data-id="skill-detail-uninstall" onClick={handleUninstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded text-zinc-400 hover:text-zinc-200 disabled:opacity-50 transition-colors">
                            {t('marketplaceUninstall')}
                          </button>
                        </>
                      )}
                    </>
                  ) : (
                    <button data-id="skill-detail-install" onClick={handleInstall} disabled={busy} className="text-[12px] px-3 py-1.5 rounded bg-indigo-500/20 text-indigo-200 hover:bg-indigo-500/30 disabled:opacity-50 transition-colors inline-flex items-center gap-1">
                      {busyAction === 'install' && <Loader2 className="w-3 h-3 animate-spin" />}
                      {busyAction === 'install' ? t('marketplaceInstalling') : t('marketplaceInstall')}
                    </button>
                  )}
                  {skill.name === 'google' && googleStatus?.connected && (
                    <>
                      <span data-id="skill-detail-google-connected" className="text-[12px] px-2 py-1 rounded bg-emerald-500/15 text-emerald-300 inline-flex items-center gap-1.5">
                        <CheckCircle2 className="w-3 h-3" />
                        {googleStatus.authorized_email || t('marketplaceGoogleConnected')}
                      </span>
                      <button data-id="skill-detail-google-disconnect" onClick={handleGoogleDisconnect} disabled={googleBusy} className="text-[12px] px-2 py-1 rounded text-zinc-400 hover:text-zinc-200 disabled:opacity-50 transition-colors">
                        {t('marketplaceGoogleDisconnect')}
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
                      {t('marketplaceGoogleAuthorize')}
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

        {/* Body — markdown content + metadata sidebar (VS Code marketplace style).
            On wider containers (>768px) the sidebar sits to the right; on
            narrower portals it stacks below the doc. */}
        <div className="flex-1 overflow-y-auto" data-id="skill-detail-body">
          {loading ? (
            <div className="px-5 py-4">
              <div data-id="skill-detail-body-skeleton" className="space-y-2.5 py-1 animate-pulse">
                {[100, 88, 94, 72, 85, 60].map((w, i) => (
                  <div key={i} className="h-3 rounded-md bg-white/[0.06]" style={{ width: `${w}%` }} />
                ))}
                <div className="h-5" />
                <div className="h-3 w-1/3 rounded-md bg-white/[0.08]" />
                {[82, 68, 90, 55].map((w, i) => (
                  <div key={`b${i}`} className="h-3 rounded-md bg-white/[0.06]" style={{ width: `${w}%` }} />
                ))}
              </div>
            </div>
          ) : !skill ? (
            <div className="px-5 py-4 text-xs text-zinc-500" data-id="skill-detail-no-data">{t('marketplaceNoData')}</div>
          ) : (
            <SkillDetailTabs data={data} skill={skill} setSendText={setSendText} sendToAgent={sendToAgent} copy={copy} copied={copied} />
          )}
        </div>

      </div>
      {confirmNode}
    </div>,
    portalTarget
  );
}

// SkillDetailTabs renders Help / Tools / Updates tabs + sidebar.
type DetailTab = 'help' | 'tools' | 'updates';

function SkillDetailTabs({ data, skill, setSendText, sendToAgent, copy, copied }: {
  data: SkillDetailPayload | null;
  skill: MarketSkill;
  setSendText: (s: string) => void;
  sendToAgent: (s: string) => void;
  copy: (s: string) => void;
  copied: string;
}) {
  const { t, i18n } = useTranslation('workspace');
  const tools = data?.manifest?.tools || [];
  const [tab, setTab] = useState<DetailTab>('help');
  const [versions, setVersions] = useState<Array<{ version: string; published_at?: string; size?: number }> | null>(null);
  const [versionsLoading, setVersionsLoading] = useState(false);

  // Fetch version history when Updates tab opens (registry directly).
  useEffect(() => {
    if (tab !== 'updates' || versions !== null) return;
    setVersionsLoading(true);
    const url = `https://skills.cicy-ai.com/v1/skills/${encodeURIComponent(skill.name)}/versions`;
    fetch(url)
      .then(r => r.ok ? r.json() : null)
      .then(d => {
        const list = d?.data?.versions || [];
        // De-duplicate by version (keep newest published_at)
        const map = new Map<string, { version: string; published_at?: string; size?: number }>();
        for (const v of list) {
          const prev = map.get(v.version);
          if (!prev || (v.published_at && v.published_at > (prev.published_at || ''))) map.set(v.version, v);
        }
        setVersions(Array.from(map.values()).sort((a, b) => (b.published_at || '').localeCompare(a.published_at || '')));
      })
      .catch(() => setVersions([]))
      .finally(() => setVersionsLoading(false));
  }, [tab, skill.name, versions]);

  const tabs: { id: DetailTab; label: string; badge?: string }[] = [
    { id: 'help',    label: t('marketplaceTabHelp',    { defaultValue: 'Help' }) },
    { id: 'tools',   label: t('marketplaceTabTools',   { defaultValue: 'Tools' }), badge: tools.length > 0 ? String(tools.length) : undefined },
    { id: 'updates', label: t('marketplaceTabUpdates', { defaultValue: 'Updates' }) },
  ];

  return (
    <div className="flex flex-col h-full">
      {/* Tab bar */}
      <div className="flex items-center border-b border-white/[0.06] px-5 shrink-0">
        {tabs.map(({ id, label, badge }) => (
          <button
            key={id}
            data-id={`skill-detail-tab-${id}`}
            onClick={() => setTab(id)}
            className={`mr-4 py-2.5 text-[12px] border-b-2 transition-colors inline-flex items-center gap-1.5 ${
              tab === id
                ? 'border-blue-400 text-zinc-100 font-medium'
                : 'border-transparent text-zinc-500 hover:text-zinc-300'
            }`}
          >
            <span>{label}</span>
            {badge && <span className="px-1 rounded bg-white/[0.06] text-zinc-500 text-[10px] font-mono">{badge}</span>}
          </button>
        ))}
      </div>

      {/* Content + sidebar */}
      <div className="flex-1 min-h-0 flex">
        {/* Main content (scrollable) */}
        <div className="flex-1 overflow-y-auto px-5 py-4 min-w-0">
          {tab === 'help' && (
            (data?.help_md || data?.skill_md)
              ? <MarkdownPane
                  content={data?.help_md || data?.skill_md || ''}
                  setSendText={setSendText}
                  skillName={skill.name}
                  manifest={data?.manifest || null}
                />
              : <div className="text-xs text-zinc-500">{t('marketplaceNoContent')}</div>
          )}
          {tab === 'tools' && (
            (data?.tools_md || tools.length > 0)
              ? <div data-id="skill-tools-content" className="space-y-4">
                  {data?.tools_md && <MarkdownPane content={data.tools_md} setSendText={setSendText} skillName={skill.name} manifest={data?.manifest || null} />}
                  {tools.length > 0 && <SkillToolsPanel tools={tools} skillName={skill.name} installed={skill.status.installed} onSend={sendToAgent} />}
                </div>
              : <div className="text-xs text-zinc-500">{t('marketplaceNoContent')}</div>
          )}
          {tab === 'updates' && (
            <SkillUpdatesPanel
              skill={skill}
              manifest={data?.manifest || null}
              versions={versions}
              loading={versionsLoading}
              lang={i18n.language || 'en'}
              t={t}
            />
          )}
        </div>
        {/* Sidebar (fixed width, hidden on narrow) */}
        <aside className="hidden md:block w-[240px] shrink-0 border-l border-white/[0.06] overflow-y-auto">
          <SkillDetailSidebar skill={skill} manifest={data?.manifest || null} copy={copy} copied={copied} />
        </aside>
      </div>
    </div>
  );
}

// SkillUpdatesPanel renders the version history fetched from the registry.
// Highlights the currently installed version and the latest available.
// Each row links to the matching GitHub release page.
function SkillUpdatesPanel({ skill, manifest, versions, loading, lang, t }: {
  skill: MarketSkill;
  manifest: SkillManifest | null;
  versions: Array<{ version: string; published_at?: string; size?: number }> | null;
  loading: boolean;
  lang: string;
  t: (k: string, opt?: any) => string;
}) {
  if (loading) {
    return (
      <div className="space-y-2 animate-pulse">
        {[100, 75, 90, 60].map((w, i) => (
          <div key={i} className="h-10 rounded-md bg-white/[0.04]" style={{ width: `${w}%` }} />
        ))}
      </div>
    );
  }
  if (!versions || versions.length === 0) {
    return <div className="text-xs text-zinc-500">{t('marketplaceNoUpdates', { defaultValue: 'No version history available.' })}</div>;
  }
  const repo = manifest?.publish?.source?.repository;
  return (
    <div className="space-y-2" data-id="skill-updates-panel">
      {versions.map((v) => {
        const isInstalled = skill.installed_version === v.version;
        const isLatest = skill.version === v.version;
        const releaseUrl = repo ? `https://github.com/${repo}/releases/tag/${skill.name}-v${v.version}` : null;
        const Inner = (
          <>
            <div className="flex items-center justify-between gap-2 flex-wrap">
              <div className="flex items-center gap-2">
                <span className={`text-[12px] font-mono font-medium ${isLatest ? 'text-blue-300' : 'text-zinc-200'}`}>
                  v{v.version}
                </span>
                {isLatest && (
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/20 text-blue-300">
                    {t('marketplaceVersionLatest', { defaultValue: 'latest' })}
                  </span>
                )}
                {isInstalled && (
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/20 text-emerald-300 inline-flex items-center gap-1">
                    <CheckCircle2 className="w-2.5 h-2.5" />
                    {t('marketplaceVersionInstalled', { defaultValue: 'installed' })}
                  </span>
                )}
              </div>
              <div className="text-[10px] text-zinc-500 flex items-center gap-2">
                {v.size && <span>{formatBytes(v.size)}</span>}
                {v.published_at && <span>{formatDate(v.published_at, lang)}</span>}
                {releaseUrl && <ExternalLink className="w-3 h-3 opacity-50 group-hover:opacity-100" />}
              </div>
            </div>
          </>
        );
        const baseClass = `block rounded-md border px-3 py-2 transition-colors ${
          isLatest ? 'border-blue-500/40 bg-blue-500/5' : 'border-white/[0.06] bg-black/20'
        }`;
        if (releaseUrl) {
          return (
            <a
              key={v.version + (v.published_at || '')}
              href={releaseUrl}
              target="_blank"
              rel="noopener noreferrer"
              className={`group ${baseClass} hover:border-blue-500/50 hover:bg-blue-500/10`}
            >
              {Inner}
            </a>
          );
        }
        return (
          <div key={v.version + (v.published_at || '')} className={baseClass}>
            {Inner}
          </div>
        );
      })}
    </div>
  );
}

// SkillToolsPanel renders the structured tool list from manifest.tools as
// a VS Code-style command palette. Each tool shows: name (code chip),
// description, and a "→ Agent" button that sends tool.prompt (or a default
// prompt) to the currently active agent pane.
function SkillToolsPanel({ tools, skillName, installed, onSend }: {
  tools: SkillTool[];
  skillName: string;
  installed: boolean;
  onSend: (prompt: string) => void;
}) {
  const { t } = useTranslation('workspace');
  return (
    <div data-id="skill-tools-panel" className="mt-1 mb-2">
      <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-zinc-500 mb-2">
        <Terminal className="w-3 h-3" />
        <span>{t('marketplaceToolsTitle', { defaultValue: 'Tools' })}</span>
        <span className="px-1 rounded bg-white/[0.05] text-zinc-500 font-mono">{tools.length}</span>
      </div>
      <div className="space-y-1.5">
        {tools.map((tool, i) => (
          <div key={i} data-id={`skill-tool-${tool.name}`}
            className="flex items-start justify-between gap-3 rounded-md border border-white/[0.06] bg-black/20 px-3 py-2 hover:bg-black/30 transition-colors group"
          >
            <div className="min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <code className="text-[11px] text-amber-200 font-mono shrink-0">{tool.name}</code>
                {tool.example && (
                  <span className="text-[10px] text-zinc-500 font-mono truncate max-w-[200px]">{tool.example}</span>
                )}
              </div>
              <div className="text-[11px] text-zinc-400 mt-0.5 leading-relaxed">{tool.description}</div>
            </div>
            <button
              data-id={`skill-tool-send-${tool.name}`}
              disabled={!installed}
              onClick={() => onSend(t('marketplaceTestToolPrompt', { name: skillName, command: tool.example || tool.name }))}
              title={installed ? t('marketplaceSendToAgent') : t('marketplaceInstallFirst')}
              className="shrink-0 text-[10px] px-2 py-1 rounded border border-white/[0.07] text-zinc-500 hover:text-zinc-200 hover:border-zinc-500 disabled:opacity-30 disabled:cursor-not-allowed transition-colors inline-flex items-center"
            >
              <Send className="w-3 h-3" />
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}

const MarkdownPane = memo(function MarkdownPane({ content, setSendText, skillName, manifest }: { content: string; setSendText: (s: string) => void; skillName: string; manifest?: SkillManifest | null }) {
  const { t } = useTranslation('workspace');
  const shown = content.startsWith('---')
    ? content.replace(/^---[\s\S]*?---\n?/, '')
    : content;

  // Rewrite relative markdown links (./help.md, tools.md, etc.) to GitHub
  // blob URLs so References sections actually go somewhere. Without this,
  // links like `[help.md](./help.md)` 404 because the modal isn't a file
  // browser. Falls back to the original href when manifest.publish.source
  // is missing (user-authored skills, registry fetch failure).
  //
  // Resolution order:
  //   1. Match against manifest.files (help.md → manifest.files.help_md path)
  //   2. Otherwise treat as relative to skills/<name>/
  const rewriteLink = (href: string | undefined): string | undefined => {
    if (!href) return href;
    // Already absolute (http/https/mailto) or fragment — leave alone.
    if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('#')) return href;

    const repo = manifest?.publish?.source?.repository;
    const tag = manifest?.publish?.source?.tag;
    if (!repo || !tag) return href;

    // Drop leading ./ or /
    let rel = href.replace(/^\.?\//, '');

    // Resolve common file-map aliases via manifest.files. Some skills have
    // help.md at the skill root, others under references/ — the manifest
    // is authoritative. Strip the leading "./" or "/" before lookup.
    const files = (manifest as any)?.files || {};
    const aliases: Record<string, string | undefined> = {
      'help.md': files.help_md,
      'tools.md': files.tools_md,
      'readme.md': files.readme,
      'README.md': files.readme,
      'skill.md': files.skill_md,
      'SKILL.md': files.skill_md,
    };
    if (aliases[rel]) rel = aliases[rel] as string;

    return `https://github.com/${repo}/blob/${tag}/skills/${skillName}/${rel}`;
  };

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
    a: ({ href, ...p }: any) => {
      const rewritten = rewriteLink(href);
      const isExternal = rewritten && /^https?:\/\//i.test(rewritten);
      return (
        <a
          className="text-blue-400 hover:underline inline-flex items-center gap-1"
          target={isExternal ? '_blank' : undefined}
          rel={isExternal ? 'noreferrer' : undefined}
          href={rewritten}
          {...p}
        >
          {p.children}
          {isExternal && href !== rewritten && <ExternalLink className="w-3 h-3 opacity-60" />}
        </a>
      );
    },
    code: ({ children, className, ...props }: any) => {
      const inline = !(className && /language-/.test(className));
      const text = String(children).replace(/\n$/, '');
      if (inline) {
        return (
          <code className="px-1 py-0.5 rounded bg-white/[0.06] text-[11px] text-amber-200 font-mono">{text}</code>
        );
      }
      return (
        <div data-id="skill-md-code-block" className="my-2 rounded-md bg-black/40 border border-white/[0.04] overflow-hidden">
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
  }), [t, setSendText, skillName, manifest]);

  // Memoize the full Markdown render keyed on shown — re-parse only when the
  // displayed text actually changes (tab switch, translation toggle, fetch).
  const rendered = useMemo(() => (
    <Markdown remarkPlugins={[remarkGfm]} components={components}>{shown}</Markdown>
  ), [shown, components]);

  if (!content || !content.trim()) {
    return <div className="text-xs text-zinc-500">{t('marketplaceNoContent')}</div>;
  }

  return (
    <div data-id="skill-md-pane" className="relative">
      <div data-id="skill-md-content" className="prose-skill text-[13px] leading-relaxed text-zinc-300">
        {rendered}
      </div>
    </div>
  );
});


// SkillDetailSidebar renders a compact metadata column to the right of the
// markdown body, modeled after the VS Code marketplace detail page.
//
// Sections (each only rendered when data is present):
//   • Identifier        — name + version
//   • Last published    — formatted publish.published_at
//   • Publisher         — manifest.author
//   • Size              — formatted publish.size
//   • License           — manifest.license
//   • Categories / Tags — manifest.category + tags
//   • Permissions       — manifest.permissions
//   • Compatible agents — manifest.compatible_agents
//   • Runtime           — manifest.runtime.{node,python}
//   • Resources         — homepage / repository / release
//
// All sections degrade gracefully: when manifest is null (e.g. user-authored
// skills not in the registry, or registry fetch failed), the sidebar shows
// only the basics derived from the MarketSkill summary (name + version).
function SkillDetailSidebar({
  skill,
  manifest,
  copy,
  copied,
}: {
  skill: MarketSkill;
  manifest: SkillManifest | null;
  copy: (s: string) => void;
  copied: string;
}) {
  const { t, i18n } = useTranslation();
  const m = manifest || {};
  const pub = m.publish || {};
  const lang = i18n.language || 'en';

  const published = pub.published_at ? formatDate(pub.published_at, lang) : '';
  const size = typeof pub.size === 'number' ? formatBytes(pub.size) : '';
  const repoUrl = pub.source?.repository ? `https://github.com/${pub.source.repository}` : '';
  const releaseUrl = pub.source?.repository && pub.source?.tag
    ? `https://github.com/${pub.source.repository}/releases/tag/${pub.source.tag}`
    : '';

  return (
    <aside
      data-id="skill-detail-sidebar"
      className="px-4 py-4 space-y-3.5 text-[11px]"
    >
      {/* Identifier */}
      <SidebarSection title={t('marketplaceSidebarIdentifier', { defaultValue: 'Identifier' })} icon={<Hash className="w-3 h-3" />}>
        <button
          onClick={() => copy(skill.name)}
          className="group inline-flex items-center gap-1.5 max-w-full text-left font-mono text-[11px] text-zinc-300 hover:text-zinc-100 transition-colors"
          title={t('marketplaceSidebarCopyId', { defaultValue: 'Copy identifier' })}
        >
          <span className="truncate">{skill.name}</span>
          {copied === skill.name
            ? <Check className="w-3 h-3 text-emerald-400 shrink-0" />
            : <Copy className="w-3 h-3 opacity-50 group-hover:opacity-100 shrink-0" />}
        </button>
      </SidebarSection>

      {/* Version */}
      <SidebarSection title={t('marketplaceSidebarVersion', { defaultValue: 'Version' })} icon={<Tag className="w-3 h-3" />}>
        <div className="text-zinc-300">v{skill.version}</div>
        {skill.installed_version && skill.installed_version !== 'user' && skill.installed_version !== skill.version && (
          <div className="text-[10px] text-zinc-500 mt-0.5">
            {t('marketplaceSidebarInstalled', { defaultValue: 'Installed' })}: v{skill.installed_version}
          </div>
        )}
      </SidebarSection>

      {/* Published */}
      {published && (
        <SidebarSection title={t('marketplaceSidebarLastPublished', { defaultValue: 'Last published' })} icon={<Calendar className="w-3 h-3" />}>
          <div className="text-zinc-300">{published}</div>
        </SidebarSection>
      )}

      {/* Publisher */}
      {m.author && (
        <SidebarSection title={t('marketplaceSidebarPublisher', { defaultValue: 'Publisher' })} icon={<User className="w-3 h-3" />}>
          <div className="text-zinc-300">{m.author}</div>
        </SidebarSection>
      )}

      {/* Size */}
      {size && (
        <SidebarSection title={t('marketplaceSidebarSize', { defaultValue: 'Size' })} icon={<HardDrive className="w-3 h-3" />}>
          <div className="text-zinc-300">{size}</div>
        </SidebarSection>
      )}

      {/* License */}
      {m.license && (
        <SidebarSection title={t('marketplaceSidebarLicense', { defaultValue: 'License' })} icon={<FileCode className="w-3 h-3" />}>
          <div className="text-zinc-300">{m.license}</div>
        </SidebarSection>
      )}

      {/* Tags */}
      {m.tags && m.tags.length > 0 && (
        <SidebarSection title={t('marketplaceSidebarTags', { defaultValue: 'Tags' })} icon={<Tag className="w-3 h-3" />}>
          <div className="flex flex-wrap gap-1">
            {m.tags.map(tag => (
              <span key={tag} className="px-1.5 py-0.5 rounded bg-white/[0.05] text-zinc-400 text-[10px]">
                {tag}
              </span>
            ))}
          </div>
        </SidebarSection>
      )}

      {/* Permissions */}
      {m.permissions && m.permissions.length > 0 && (
        <SidebarSection title={t('marketplaceSidebarPermissions', { defaultValue: 'Permissions' })} icon={<ShieldCheck className="w-3 h-3" />}>
          <div className="flex flex-wrap gap-1">
            {m.permissions.map(p => (
              <span key={p} className="px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-300/90 text-[10px]">
                {p}
              </span>
            ))}
          </div>
        </SidebarSection>
      )}

      {/* Runtime */}
      {(m.runtime?.node || m.runtime?.python) && (
        <SidebarSection title={t('marketplaceSidebarRuntime', { defaultValue: 'Runtime' })} icon={<Terminal className="w-3 h-3" />}>
          {m.runtime?.node && <div className="text-zinc-400 text-[10px]">node {m.runtime.node}</div>}
          {m.runtime?.python && <div className="text-zinc-400 text-[10px]">python {m.runtime.python}</div>}
        </SidebarSection>
      )}

      {/* Compatible agents */}
      {m.compatible_agents && m.compatible_agents.length > 0 && (
        <SidebarSection title={t('marketplaceSidebarAgents', { defaultValue: 'Compatible agents' })} icon={<Code className="w-3 h-3" />}>
          <div className="flex flex-wrap gap-1">
            {m.compatible_agents.map(a => (
              <span key={a} className="px-1.5 py-0.5 rounded bg-white/[0.05] text-zinc-400 text-[10px] font-mono">
                {a === '*' ? 'any' : a}
              </span>
            ))}
          </div>
        </SidebarSection>
      )}

      {/* Resources */}
      {(m.homepage || repoUrl || releaseUrl) && (
        <SidebarSection title={t('marketplaceSidebarResources', { defaultValue: 'Resources' })} icon={<BookOpen className="w-3 h-3" />}>
          <div className="space-y-1">
            {m.homepage && (
              <ResourceLink href={m.homepage} label={t('marketplaceSidebarHomepage', { defaultValue: 'Homepage' })} />
            )}
            {repoUrl && (
              <ResourceLink href={repoUrl} label={t('marketplaceSidebarRepository', { defaultValue: 'Repository' })} />
            )}
            {releaseUrl && (
              <ResourceLink href={releaseUrl} label={t('marketplaceSidebarRelease', { defaultValue: 'Release' })} />
            )}
          </div>
        </SidebarSection>
      )}

      {/* SHA256 (collapsed below) */}
      {pub.sha256 && (
        <SidebarSection title="SHA-256" icon={<Hash className="w-3 h-3" />}>
          <button
            onClick={() => copy(pub.sha256!)}
            className="group block max-w-full text-left font-mono text-[10px] text-zinc-500 hover:text-zinc-300 transition-colors break-all leading-tight"
            title="Copy SHA-256"
          >
            {pub.sha256.slice(0, 32)}…
            {copied === pub.sha256
              ? <Check className="w-3 h-3 text-emerald-400 inline ml-1" />
              : <Copy className="w-2.5 h-2.5 inline ml-1 opacity-30 group-hover:opacity-80" />}
          </button>
        </SidebarSection>
      )}
    </aside>
  );
}

function SidebarSection({ title, icon, children }: { title: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="space-y-1" data-sidebar-section={title}>
      <div className="flex items-center gap-1 text-[10px] uppercase tracking-wide text-zinc-500">
        {icon}
        <span>{title}</span>
      </div>
      <div className="pl-4">{children}</div>
    </div>
  );
}

function ResourceLink({ href, label }: { href: string; label: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1 text-blue-400 hover:text-blue-300 hover:underline transition-colors"
    >
      <ExternalLink className="w-3 h-3" />
      <span>{label}</span>
    </a>
  );
}

// formatBytes returns "6.4 KB" / "1.2 MB" style strings.
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

// formatDate localizes an ISO date for sidebar display. Falls back to the
// raw string on parse failure.
function formatDate(iso: string, lang: string): string {
  try {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleDateString(lang, { year: 'numeric', month: 'short', day: 'numeric' });
  } catch {
    return iso;
  }
}
