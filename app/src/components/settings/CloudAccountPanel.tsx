// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Settings → CiCy 账号. The single cicy_cloud IM account is the instance's
// identity on CiCy Hub / Cloud; it used to hide inside the IM notification
// list. Here it gets its own page: logged-in state, tenant directory (every
// cicy-code that signed in with the same email), login and sign-out.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Apple, ArrowUpCircle, Check, ChevronDown, Copy, Cpu, Globe, HardDrive, Laptop, Loader2, LogOut, Mail, MemoryStick,
  Monitor, Network, RefreshCw, Search, Server, ShieldCheck, Terminal, WifiOff, X, Zap,
} from 'lucide-react';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';

export interface CloudAccountInfo {
  id: number;
  name: string;
  state: string;
  state_detail?: string;
  config?: Record<string, any>;
}

export interface CloudInstanceInfo {
  instanceId: string;
  teamId?: string;
  status?: string;
  platform?: string;
  lastSeenAt?: string;
  self?: boolean;
  hub?: boolean;
  proxyHost?: string;
  proxyAvailable?: number | boolean;
  arch?: string;
  runtime?: string;
  cpuModel?: string;
  cpuCores?: number;
  memoryTotalMB?: number;
  gpu?: string;
  publicIp?: string;
  version?: string;
  createdAt?: string;
  ports?: Array<{ port: number; name?: string; visibility?: string }>;
  frp?: { host: string; ports: Record<string, number>; ssh?: string; user?: string; sshLive?: boolean; httpLive?: boolean };
  sshUser?: string;
  resources?: {
    cpu_usage_pct?: number; cpu_cores?: number;
    mem_usage_pct?: number; mem_total_bytes?: number; mem_used_bytes?: number;
    disk_usage_pct?: number; disk_total_bytes?: number; disk_used_bytes?: number;
    load_1?: number; load_5?: number; load_15?: number; updated_at?: string;
  };
}

function fmtBytes(value?: number): string {
  if (value == null || !Number.isFinite(value) || value <= 0) return '--';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let next = value; let unit = units[0];
  for (let i = 0; i < units.length; i += 1) { unit = units[i]; if (next < 1024 || i === units.length - 1) break; next /= 1024; }
  return `${next >= 100 ? next.toFixed(0) : next.toFixed(1)} ${unit}`;
}

function parseTime(raw?: string): number {
  if (!raw) return 0;
  const ms = Date.parse(raw.includes('T') ? raw : raw.replace(' ', 'T') + 'Z');
  return Number.isFinite(ms) ? ms : 0;
}

function fmtTime(raw?: string): string {
  const ms = parseTime(raw);
  return ms ? new Date(ms).toLocaleString() : '—';
}

/** "3 分钟前" style relative time, so an offline node reads as a fact, not a timestamp. */
function fmtAgo(raw: string | undefined, t: (k: string, o?: any) => string): string {
  const ms = parseTime(raw);
  if (!ms) return t('cloudNeverSeen', { defaultValue: '从未在线' });
  const diff = Math.max(0, Date.now() - ms);
  const m = Math.floor(diff / 60000);
  if (m < 1) return t('cloudJustNow', { defaultValue: '刚刚' });
  if (m < 60) return t('cloudMinutesAgo', { defaultValue: '{{n}} 分钟前', n: m });
  const h = Math.floor(m / 60);
  if (h < 48) return t('cloudHoursAgo', { defaultValue: '{{n}} 小时前', n: h });
  return t('cloudDaysAgo', { defaultValue: '{{n}} 天前', n: Math.floor(h / 24) });
}

function cmpVersion(a?: string, b?: string): number {
  const pa = String(a || '').split('.').map((x) => parseInt(x, 10) || 0);
  const pb = String(b || '').split('.').map((x) => parseInt(x, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i += 1) {
    const d = (pa[i] || 0) - (pb[i] || 0);
    if (d) return d;
  }
  return 0;
}

const INPUT = 'h-10 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors hover:border-white/[0.14] focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 disabled:opacity-50';
const maskId = (id: string) => (id.length > 14 ? `${id.slice(0, 9)}…${id.slice(-4)}` : id);
const DEFAULT_HUB = 'https://ws.cicy-ai.com';

function errText(e: any): string { return String(e?.response?.data?.error || e?.response?.data?.detail || e?.message || e); }
function toast(m: string) { window.dispatchEvent(new CustomEvent('show-toast', { detail: m })); }

/** The cicy_cloud account, if any — shared by the panel and the gear menu badge. */
export async function fetchCloudAccount(): Promise<CloudAccountInfo | null> {
  const res = await apiService.getIMAccounts();
  const accounts = (res?.data?.accounts || []) as CloudAccountInfo[] & { platform?: string }[];
  const found = (accounts as any[]).find((a) => a.platform === 'cicy_cloud');
  return found ? (found as CloudAccountInfo) : null;
}

/** Open an instance host (or one of its ports) in a new tab. On Hub the tab
 *  is pre-authenticated through a one-time grant; on Cloud it is a plain link. */
async function openInstanceHost(inst: CloudInstanceInfo, port = 0) {
  const fallback = port ? `https://${(inst.proxyHost || '').replace(/^([^.]+)\./, `$1-${port}.`)}` : `https://${inst.proxyHost}`;
  // Open synchronously so popup blockers treat it as user-initiated, then steer it.
  const tab = window.open('about:blank', '_blank', 'noopener');
  try {
    if (inst.hub) {
      const res = await apiService.openCiCyCloudInstance(inst.instanceId, port);
      const url = String(res?.data?.url || '');
      if (url) { if (tab) tab.location.href = url; else window.open(url, '_blank', 'noopener'); return; }
    }
  } catch (e) { toast(errText(e)); }
  if (tab) tab.location.href = fallback; else window.open(fallback, '_blank', 'noopener');
}

/* ───────────────────────── visual atoms ───────────────────────── */

function gaugeColor(value: number | null): string {
  if (value == null) return 'text-zinc-700';
  if (value >= 90) return 'text-red-400';
  if (value >= 70) return 'text-amber-400';
  return 'text-emerald-400';
}

/** Circular gauge: the percentage is the picture, the detail is the caption. */
function Ring({ value, label, caption, icon }: { value: number | null; label: string; caption: string; icon: React.ReactNode }) {
  const size = 56; const stroke = 5; const r = (size - stroke) / 2; const c = 2 * Math.PI * r;
  const v = value == null ? 0 : Math.max(0, Math.min(100, value));
  return (
    <div className="flex min-w-0 flex-1 flex-col items-center gap-1" title={`${label} ${caption}`}>
      <div className="relative" style={{ width: size, height: size }}>
        <svg width={size} height={size} className="-rotate-90">
          <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="currentColor" strokeWidth={stroke} className="text-white/[0.07]" />
          <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="currentColor" strokeWidth={stroke} strokeLinecap="round"
            strokeDasharray={c} strokeDashoffset={c - (c * v) / 100} className={`${gaugeColor(value)} transition-[stroke-dashoffset] duration-700`} />
        </svg>
        <div className="absolute inset-0 grid place-items-center">
          {value == null ? <span className="text-zinc-600">{icon}</span> : <span className="font-mono text-[12px] font-semibold text-zinc-100">{Math.round(v)}<span className="text-[9px] text-zinc-500">%</span></span>}
        </div>
      </div>
      <div className="text-[10px] font-medium text-zinc-400">{label}</div>
      <div className="max-w-full truncate font-mono text-[10px] text-zinc-600">{caption}</div>
    </div>
  );
}

function PlatformIcon({ platform, size = 18 }: { platform?: string; size?: number }) {
  const p = String(platform || '').toLowerCase();
  if (p.includes('darwin') || p.includes('mac')) return <Apple size={size} />;
  if (p.includes('win')) return <Monitor size={size} />;
  if (p.includes('linux')) return <Server size={size} />;
  return <Laptop size={size} />;
}

function platformLabel(platform?: string): string {
  const p = String(platform || '').toLowerCase();
  if (p.includes('darwin') || p.includes('mac')) return 'macOS';
  if (p.includes('win')) return 'Windows';
  if (p.includes('linux')) return 'Linux';
  return platform || '';
}

function CopyButton({ text, label, live, title }: { text: string; label: string; live: boolean; title: string }) {
  const [done, setDone] = useState(false);
  return (
    <button type="button" title={title}
      onClick={(e) => { e.stopPropagation(); void navigator.clipboard?.writeText(text); setDone(true); window.setTimeout(() => setDone(false), 1500); }}
      className={`inline-flex h-8 items-center gap-1.5 rounded-lg border px-3 text-[12px] font-medium transition-colors ${live ? 'border-white/[0.1] bg-white/[0.04] text-zinc-200 hover:border-emerald-500/40 hover:bg-emerald-500/[0.08] hover:text-emerald-200' : 'border-white/[0.06] bg-transparent text-zinc-500 hover:text-zinc-300'}`}>
      {done ? <Check size={13} className="text-emerald-300" /> : <Terminal size={13} />}{label}
    </button>
  );
}

function StatTile({ icon, value, label, tone }: { icon: React.ReactNode; value: number | string; label: string; tone: string }) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-white/[0.07] bg-white/[0.02] px-4 py-3">
      <span className={`grid h-9 w-9 shrink-0 place-items-center rounded-lg ${tone}`}>{icon}</span>
      <div className="min-w-0">
        <div className="text-[18px] font-semibold leading-tight text-zinc-100">{value}</div>
        <div className="truncate text-[11px] text-zinc-500">{label}</div>
      </div>
    </div>
  );
}

/* ───────────────────────── node card ───────────────────────── */

function NodeCard({ inst, latest, t }: { inst: CloudInstanceInfo; latest: string; t: (k: string, o?: any) => string }) {
  const [open, setOpen] = useState(false);
  const online = inst.status === 'online';
  const r = inst.resources;
  const frp = inst.frp;
  const pct = (v?: number) => (v != null && Number.isFinite(v) ? Math.max(0, Math.min(100, v)) : null);
  const memPct = pct(r?.mem_usage_pct) ?? (r?.mem_used_bytes && r?.mem_total_bytes ? (r.mem_used_bytes / r.mem_total_bytes) * 100 : null);
  const diskPct = pct(r?.disk_usage_pct) ?? (r?.disk_used_bytes && r?.disk_total_bytes ? (r.disk_used_bytes / r.disk_total_bytes) * 100 : null);
  const sshUser = inst.sshUser || frp?.user || '';
  const sshCmd = frp?.ports?.ssh ? `ssh -p ${frp.ports.ssh} ${sshUser ? `${sshUser}@` : ''}${frp.host}` : '';
  const outdated = !!inst.version && !!latest && cmpVersion(inst.version, latest) < 0;
  const ports = (inst.ports || []).filter((p) => p.visibility !== 'closed');
  const load = r?.load_1 != null ? [r.load_1, r.load_5, r.load_15].map((v) => (v ?? 0).toFixed(2)).join(' / ') : '';

  const details: Array<[string, string]> = [
    [t('cloudSysOS', { defaultValue: '系统' }), [platformLabel(inst.platform), inst.arch, inst.runtime].filter(Boolean).join(' · ')],
    ['CPU', inst.cpuModel ? `${inst.cpuModel}${inst.cpuCores ? ` · ${inst.cpuCores}C` : ''}` : '—'],
    [t('cloudSysMem', { defaultValue: '内存' }), inst.memoryTotalMB ? `${(inst.memoryTotalMB / 1024).toFixed(1)} GB` : '—'],
    ['GPU', inst.gpu || '—'],
    ['IP', inst.publicIp || '—'],
    [t('cloudSysLoad', { defaultValue: '负载' }), load || '—'],
    [t('cloudSysSeen', { defaultValue: '最近在线' }), fmtTime(inst.lastSeenAt)],
    ['ID', inst.instanceId],
  ];

  return (
    <div data-id={`cloud-instance-${inst.instanceId}`}
      className={`flex flex-col rounded-2xl border transition-colors ${online ? 'border-white/[0.08] bg-white/[0.025]' : 'border-white/[0.05] bg-white/[0.01]'} ${inst.self ? 'ring-1 ring-blue-500/30' : ''}`}>
      {/* header */}
      <div className="flex items-center gap-3 px-4 pt-4">
        <span className={`grid h-10 w-10 shrink-0 place-items-center rounded-xl ${online ? 'bg-blue-500/12 text-blue-300' : 'bg-white/[0.04] text-zinc-600'}`}>
          <PlatformIcon platform={inst.platform} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className={`truncate text-[14px] font-semibold ${online ? 'text-zinc-100' : 'text-zinc-400'}`}>{inst.teamId || maskId(inst.instanceId)}</span>
            {inst.self ? <span className="rounded-md bg-blue-500/15 px-1.5 py-0.5 text-[10px] font-medium text-blue-300">{t('cloudInstanceSelf', { defaultValue: '本机' })}</span> : null}
          </div>
          <div className="mt-0.5 flex items-center gap-2 text-[11px] text-zinc-500">
            <span className={`inline-flex items-center gap-1 ${online ? 'text-emerald-300' : 'text-zinc-500'}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${online ? 'bg-emerald-400' : 'bg-zinc-600'}`} />
              {online ? t('cloudOnline', { defaultValue: '在线' }) : `${t('cloudOffline', { defaultValue: '离线' })} · ${fmtAgo(inst.lastSeenAt, t)}`}
            </span>
            <span className="text-zinc-700">·</span>
            <span>{platformLabel(inst.platform)}{inst.arch ? ` ${inst.arch}` : ''}</span>
          </div>
        </div>
        {inst.version ? (
          <span title={outdated ? t('cloudOutdated', { defaultValue: '有新版本 v{{v}}', v: latest }) : `v${inst.version}`}
            className={`inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 font-mono text-[10px] ${outdated ? 'bg-amber-500/12 text-amber-300' : 'bg-white/[0.04] text-zinc-500'}`}>
            {outdated ? <ArrowUpCircle size={11} /> : null}v{inst.version}
          </span>
        ) : null}
      </div>

      {/* gauges */}
      <div className="px-4 pt-4">
        {r ? (
          <div data-id={`cloud-instance-res-${inst.instanceId}`} className="flex items-start gap-2">
            <Ring value={pct(r.cpu_usage_pct)} label="CPU" caption={r.cpu_cores ? `${r.cpu_cores} cores` : '—'} icon={<Cpu size={16} />} />
            <Ring value={memPct} label={t('cloudSysMem', { defaultValue: '内存' })} caption={`${fmtBytes(r.mem_used_bytes)} / ${fmtBytes(r.mem_total_bytes)}`} icon={<MemoryStick size={16} />} />
            <Ring value={diskPct} label={t('cloudSysDisk', { defaultValue: '磁盘' })} caption={`${fmtBytes(r.disk_used_bytes)} / ${fmtBytes(r.disk_total_bytes)}`} icon={<HardDrive size={16} />} />
          </div>
        ) : (
          <div className="flex h-[92px] items-center justify-center gap-2 rounded-xl border border-dashed border-white/[0.06] text-[12px] text-zinc-600">
            {online ? <><Loader2 size={14} className="animate-spin" />{t('cloudResPending', { defaultValue: '等待资源上报…' })}</> : <><WifiOff size={14} />{t('cloudOfflineSince', { defaultValue: '最近在线 {{when}}', when: fmtAgo(inst.lastSeenAt, t) })}</>}
          </div>
        )}
      </div>

      {/* actions */}
      <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-white/[0.05] px-4 py-3">
        {inst.proxyHost ? (
          <button type="button" data-id={`cloud-instance-domain-${inst.instanceId}`} onClick={() => void openInstanceHost(inst)}
            disabled={!inst.proxyAvailable}
            title={inst.proxyAvailable ? `https://${inst.proxyHost}` : t('cloudProxyOffline', { defaultValue: '隧道未上报，域名暂时不可用' })}
            className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-blue-600 px-3 text-[12px] font-medium text-white transition-colors hover:bg-blue-500 disabled:cursor-not-allowed disabled:bg-white/[0.05] disabled:text-zinc-500">
            <Globe size={13} />{t('cloudOpen', { defaultValue: '打开' })}
          </button>
        ) : null}
        {sshCmd ? (
          <CopyButton text={sshCmd} label="SSH" live={!!frp?.sshLive}
            title={frp?.sshLive ? `${t('cloudSshCopyHint', { defaultValue: '点击复制 SSH 命令' })}\n${sshCmd}` : t('cloudSshOffline', { defaultValue: 'frp 未连接，SSH 暂不可达' })} />
        ) : null}
        {ports.slice(0, 4).map((p) => (
          <button key={p.port} type="button" onClick={() => void openInstanceHost(inst, p.port)} title={`https://${inst.proxyHost!.replace(/^([^.]+)\./, `$1-${p.port}.`)}`}
            className="inline-flex h-8 items-center gap-1 rounded-lg border border-white/[0.08] bg-white/[0.03] px-2.5 font-mono text-[11px] text-zinc-300 hover:border-blue-500/40 hover:text-blue-200">
            <span className={`h-1.5 w-1.5 rounded-full ${p.visibility === 'public' ? 'bg-emerald-400' : 'bg-zinc-500'}`} />:{p.port}{p.name ? ` ${p.name}` : ''}
          </button>
        ))}
        {inst.hub && !inst.self && !frp && online ? <span className="inline-flex items-center gap-1 text-[11px] text-amber-300/80" title={t('cloudFrpPending', { defaultValue: '未接入 frp（升级到最新版并重启即可自动接入）' })}><Zap size={12} />{t('cloudFrpPendingShort', { defaultValue: '未接入 frp' })}</span> : null}
        <button type="button" onClick={() => setOpen((v) => !v)} aria-expanded={open}
          className="ml-auto inline-flex h-8 items-center gap-1 rounded-lg px-2 text-[11px] text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300">
          {t('cloudDetails', { defaultValue: '详情' })}<ChevronDown size={13} className={`transition-transform ${open ? 'rotate-180' : ''}`} />
        </button>
      </div>

      {open ? (
        <dl data-id={`cloud-instance-sys-${inst.instanceId}`} className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 border-t border-white/[0.05] px-4 py-3 text-[11px]">
          {details.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-zinc-600">{k}</dt>
              <dd className="min-w-0 truncate font-mono text-zinc-400" title={v}>{v}</dd>
            </div>
          ))}
        </dl>
      ) : null}
    </div>
  );
}

/* ───────────────────────── panel ───────────────────────── */

type Filter = 'all' | 'online' | 'offline';

export default function CloudAccountPanel({ active, onAccountChange }: { active: boolean; onAccountChange?: (account: CloudAccountInfo | null) => void }) {
  const { t } = useTranslation('workspace');
  const { confirm } = useDialogs();
  const [account, setAccount] = useState<CloudAccountInfo | null>(null);
  const [instances, setInstances] = useState<CloudInstanceInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [modalOpen, setModalOpen] = useState(false);
  const [hubOrigin, setHubOrigin] = useState(DEFAULT_HUB);
  const [email, setEmail] = useState('');
  const [team, setTeam] = useState('');
  const [state, setState] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [code, setCode] = useState('');
  const [frp, setFrp] = useState<{ supported?: boolean; enabled?: boolean; running?: boolean; error?: string; ports?: Record<string, number>; host?: string; user?: string } | null>(null);
  const [frpBusy, setFrpBusy] = useState(false);
  const [codeSubmitting, setCodeSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<Filter>('all');

  const load = useCallback(async () => {
    try {
      const acc = await fetchCloudAccount();
      setAccount(acc);
      onAccountChange?.(acc);
      if (acc && acc.state === 'connected') {
        const res = await apiService.getCiCyCloudInstances().catch(() => null);
        setInstances(((res?.data?.instances || []) as CloudInstanceInfo[]));
        if (acc.config?.mode === 'hub') {
          const f = await apiService.getCiCyCloudFrp().catch(() => null);
          setFrp(f?.data || null);
        } else setFrp(null);
      } else {
        setInstances([]); setFrp(null);
      }
    } catch (e) {
      toast(errText(e));
    } finally {
      setLoading(false);
    }
  }, [onAccountChange]);

  useEffect(() => {
    if (!active) return;
    void load();
    const timer = window.setInterval(() => { void load(); }, 10000);
    return () => window.clearInterval(timer);
  }, [active, load]);

  const openLogin = () => {
    setEmail(String(account?.config?.email || account?.name || ''));
    setTeam(String(account?.config?.team_id || ''));
    if (account?.config?.mode === 'hub') setHubOrigin(String(account?.config?.cloud_origin || DEFAULT_HUB));
    setState(''); setError(''); setModalOpen(true);
  };

  const submitCode = async () => {
    if (!state || code.trim().length !== 6) return;
    setCodeSubmitting(true); setError('');
    try {
      await apiService.submitCiCyCloudLoginCode(state, code.trim());
      // the running poll picks up the approved login on its next tick
    } catch (e) { setError(errText(e)); }
    finally { setCodeSubmitting(false); }
  };

  const submit = async () => {
    setCode('');
    setSubmitting(true); setError('');
    try {
      const res = await apiService.startCiCyCloudLogin(email.trim(), team.trim(), hubOrigin.trim() || DEFAULT_HUB);
      setState(String(res?.data?.state || ''));
    } catch (e) { setError(errText(e)); }
    finally { setSubmitting(false); }
  };

  useEffect(() => {
    if (!modalOpen || !state) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        const res = await apiService.getCiCyCloudLoginStatus(state);
        if (stopped) return;
        if (res?.data?.status === 'ready') {
          setModalOpen(false); setState('');
          toast(t('cloudAccountConnected', { defaultValue: 'CiCy 账号已登录' }));
          await load();
          return;
        }
        if (res?.data?.status === 'expired') { setError(t('cloudLoginExpired', { defaultValue: '登录链接已过期，请重新发送' })); return; }
      } catch (e) { if (!stopped) setError(errText(e)); return; }
      timer = setTimeout(poll, 2000);
    };
    timer = setTimeout(poll, 1500);
    return () => { stopped = true; if (timer) clearTimeout(timer); };
  }, [modalOpen, state, load, t]);

  const signOut = async () => {
    if (!account) return;
    const ok = await confirm({
      title: t('cloudSignOutTitle', { defaultValue: '退出 CiCy 账号？' }),
      body: t('cloudSignOutBody', { defaultValue: '这台 cicy-code 将从同 Email 的实例目录里消失，其他实例无法再向它发消息。' }),
      confirmLabel: t('cloudSignOut', { defaultValue: '退出登录' }), danger: true,
    });
    if (!ok) return;
    try {
      await apiService.deleteIMAccount(account.id);
      await load();
    } catch (e) { toast(errText(e)); }
  };

  const toggleFrp = async (enabled: boolean) => {
    setFrpBusy(true);
    try {
      const res = await apiService.setCiCyCloudFrp(enabled);
      setFrp(res?.data || null);
      toast(enabled ? t('cloudFrpOn', { defaultValue: 'frp 隧道已开启' }) : t('cloudFrpOff', { defaultValue: 'frp 隧道已关闭' }));
      void load();
    } catch (e) { toast(errText(e)); }
    finally { setFrpBusy(false); }
  };

  const connected = !!account && account.state === 'connected';
  const isHub = account?.config?.mode === 'hub';
  const emailText = String(account?.config?.email || account?.name || '');
  const initial = (emailText.trim()[0] || '?').toUpperCase();
  const mySsh = frp?.running && frp.ports?.ssh ? `ssh -p ${frp.ports.ssh} ${frp.user ? `${frp.user}@` : ''}${frp.host}` : '';

  const latest = useMemo(() => instances.reduce((m, i) => (cmpVersion(i.version, m) > 0 ? String(i.version) : m), ''), [instances]);
  const onlineCount = instances.filter((i) => i.status === 'online').length;
  const outdatedCount = instances.filter((i) => i.version && latest && cmpVersion(i.version, latest) < 0).length;
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return instances
      .filter((i) => filter === 'all' || (filter === 'online') === (i.status === 'online'))
      .filter((i) => !q || `${i.teamId || ''} ${i.instanceId} ${i.proxyHost || ''} ${i.publicIp || ''}`.toLowerCase().includes(q))
      .sort((a, b) => Number(!!b.self) - Number(!!a.self) || Number(b.status === 'online') - Number(a.status === 'online') || String(a.teamId || '').localeCompare(String(b.teamId || '')));
  }, [instances, query, filter]);

  return (
    <div data-id="cloud-account-panel" className="h-full overflow-y-auto px-8 py-7 text-zinc-300">
      <div className="mx-auto max-w-[880px] space-y-6">
        <div>
          <h2 className="text-[15px] font-semibold text-white">{t('settingsNavAccount', { defaultValue: 'CiCy 账号' })}</h2>
          <p className="mt-1 text-[12px] text-zinc-500">{t('cloudAccountIntro', { defaultValue: '用 Email 登录后，所有用同一 Email 登录的 cicy-code 互相可见、可互发消息。' })}</p>
        </div>

        {/* identity */}
        <section data-id="cloud-account-status" className="relative overflow-hidden rounded-2xl border border-white/[0.08] bg-gradient-to-br from-blue-500/[0.10] via-white/[0.025] to-transparent p-5">
          {loading ? (
            <div className="flex items-center gap-2 text-[12px] text-zinc-500"><Loader2 className="h-4 w-4 animate-spin" />{t('loadingText', { defaultValue: '加载中…' })}</div>
          ) : !account ? (
            <div className="flex flex-col items-center gap-3 py-4 text-center">
              <span className="grid h-14 w-14 place-items-center rounded-full bg-white/[0.05] text-zinc-500"><Mail size={22} /></span>
              <div>
                <div className="text-[14px] font-medium text-zinc-200">{t('cloudNotLoggedIn', { defaultValue: '未登录' })}</div>
                <div className="mt-0.5 text-[12px] text-zinc-500">{t('cloudNotLoggedInHint', { defaultValue: '登录后这台 cicy-code 才能和你的其他实例互通。' })}</div>
              </div>
              <button type="button" data-id="cloud-account-login" onClick={openLogin} className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2.5 text-[13px] font-medium text-white transition-colors hover:bg-blue-500"><Mail size={14} />{t('cloudLogin', { defaultValue: 'Email 登录' })}</button>
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-4">
              <span className={`grid h-14 w-14 shrink-0 place-items-center rounded-full text-[20px] font-semibold ${connected ? 'bg-blue-500/20 text-blue-200' : 'bg-white/[0.05] text-zinc-500'}`}>{initial}</span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2 text-[15px] font-semibold text-zinc-100">
                  <span className="truncate">{emailText}</span>
                  <span data-id="cloud-account-state" className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${connected ? 'bg-emerald-500/12 text-emerald-300' : 'bg-red-500/12 text-red-300'}`}>
                    <span className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-400' : 'bg-red-400'}`} />
                    {connected ? t('cloudLoggedIn', { defaultValue: '已登录' }) : (account.state_detail || t('cloudNotAuthed', { defaultValue: '未认证' }))}
                  </span>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-zinc-500">
                  <span className="inline-flex items-center gap-1"><Network size={11} />{isHub ? String(account.config?.cloud_origin || '').replace(/^https?:\/\//, '') : `Cloud · Team ${String(account.config?.team_id || '—')}`}</span>
                  {account.config?.team_id && isHub ? <span className="inline-flex items-center gap-1"><Laptop size={11} />{String(account.config.team_id)}</span> : null}
                  {account.config?.instance_id ? <span className="font-mono" title={String(account.config.instance_id)}>{maskId(String(account.config.instance_id))}</span> : null}
                </div>
              </div>
              <div className="flex items-center gap-2">
                <button type="button" data-id="cloud-account-relogin" onClick={openLogin} className="inline-flex h-9 items-center gap-1.5 rounded-lg border border-white/[0.1] bg-white/[0.04] px-3 text-[12px] text-zinc-200 transition-colors hover:bg-white/[0.08]"><RefreshCw size={12} />{connected ? t('cloudRelogin', { defaultValue: '重新登录' }) : t('cloudLogin', { defaultValue: 'Email 登录' })}</button>
                <button type="button" data-id="cloud-account-signout" onClick={() => void signOut()} title={t('cloudSignOut', { defaultValue: '退出登录' })} className="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-white/[0.08] text-zinc-500 transition-colors hover:border-red-500/30 hover:bg-red-500/[0.08] hover:text-red-300"><LogOut size={14} /></button>
              </div>
            </div>
          )}
        </section>

        {/* tunnel */}
        {connected && isHub && (
          <section data-id="cloud-account-frp" className="flex flex-wrap items-center gap-4 rounded-2xl border border-white/[0.08] bg-white/[0.025] px-5 py-4">
            <span className={`grid h-10 w-10 shrink-0 place-items-center rounded-xl ${frp?.enabled && frp.running ? 'bg-emerald-500/12 text-emerald-300' : frp?.enabled ? 'bg-amber-500/12 text-amber-300' : 'bg-white/[0.04] text-zinc-500'}`}><Zap size={18} /></span>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 text-[13px] font-medium text-zinc-200">
                {t('cloudFrpTitleShort', { defaultValue: '直连隧道' })}
                {frp?.enabled ? <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] ${frp.running ? 'bg-emerald-500/10 text-emerald-300' : 'bg-amber-500/10 text-amber-300'}`}><span className={`h-1.5 w-1.5 rounded-full ${frp.running ? 'bg-emerald-400' : 'bg-amber-400'}`} />{frp.running ? t('cloudFrpRunning', { defaultValue: '运行中' }) : t('cloudFrpStarting', { defaultValue: '启动中…' })}</span> : null}
              </div>
              <div className="mt-0.5 text-[11px] text-zinc-500">
                {frp?.supported === false ? t('cloudFrpUnsupported', { defaultValue: '当前系统暂不支持内置 frp 客户端。' })
                  : frp?.enabled ? <span className="inline-flex items-center gap-1"><ShieldCheck size={11} className="text-emerald-400" />{t('cloudFrpOnHint', { defaultValue: '同账号的其他节点可以直接 SSH 到这台机器；域名访问不再绕 Cloudflare。' })}</span>
                  : t('cloudFrpHintShort', { defaultValue: '开启后同账号的节点可以直接 SSH 过来，域名访问更快。不用敲任何命令。' })}
                {frp?.error ? <span className="ml-2 text-red-300">{frp.error}</span> : null}
              </div>
            </div>
            {mySsh ? <CopyButton text={mySsh} label={t('cloudCopySsh', { defaultValue: '复制 SSH' })} live title={mySsh} /> : null}
            <button type="button" data-id="cloud-frp-toggle" role="switch" aria-checked={!!frp?.enabled} disabled={frpBusy || frp?.supported === false}
              onClick={() => void toggleFrp(!frp?.enabled)}
              className={`relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-50 ${frp?.enabled ? 'bg-emerald-500' : 'bg-zinc-700'}`}>
              <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all ${frp?.enabled ? 'left-[22px]' : 'left-0.5'}`} />
            </button>
          </section>
        )}

        {/* nodes */}
        {connected && (
          <section data-id="cloud-account-instances" className="space-y-3">
            {instances.length > 0 ? (
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                <StatTile icon={<Check size={16} />} value={onlineCount} label={t('cloudOnline', { defaultValue: '在线' })} tone="bg-emerald-500/12 text-emerald-300" />
                <StatTile icon={<WifiOff size={16} />} value={instances.length - onlineCount} label={t('cloudOffline', { defaultValue: '离线' })} tone="bg-white/[0.05] text-zinc-400" />
                <StatTile icon={<ArrowUpCircle size={16} />} value={outdatedCount} label={t('cloudOutdatedCount', { defaultValue: '可升级' })} tone={outdatedCount ? 'bg-amber-500/12 text-amber-300' : 'bg-white/[0.05] text-zinc-400'} />
              </div>
            ) : null}

            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-[12px] font-semibold text-zinc-400">{t('cloudInstancesTitle', { defaultValue: '同一账号下的 cicy-code' })} <span className="text-zinc-600">{instances.length}</span></h3>
              {instances.length > 3 ? (
                <>
                  <div className="ml-auto flex items-center gap-1 rounded-lg border border-white/[0.07] bg-white/[0.02] p-0.5 text-[11px]">
                    {(['all', 'online', 'offline'] as Filter[]).map((f) => (
                      <button key={f} type="button" onClick={() => setFilter(f)} className={`rounded-md px-2 py-1 transition-colors ${filter === f ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}>
                        {f === 'all' ? t('cloudFilterAll', { defaultValue: '全部' }) : f === 'online' ? t('cloudOnline', { defaultValue: '在线' }) : t('cloudOffline', { defaultValue: '离线' })}
                      </button>
                    ))}
                  </div>
                  <label className="relative">
                    <Search size={12} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-zinc-600" />
                    <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('cloudSearchNodes', { defaultValue: '搜索节点' })}
                      className="h-8 w-[160px] rounded-lg border border-white/[0.08] bg-white/[0.025] pl-7 pr-2 text-[12px] text-zinc-200 placeholder:text-zinc-600 outline-none focus:border-blue-500/50" />
                  </label>
                </>
              ) : null}
            </div>

            {instances.length === 0 ? (
              <div className="flex flex-col items-center gap-2 rounded-2xl border border-dashed border-white/[0.08] px-4 py-10 text-center">
                <span className="grid h-12 w-12 place-items-center rounded-full bg-white/[0.04] text-zinc-600"><Laptop size={20} /></span>
                <div className="text-[13px] text-zinc-300">{t('cloudInstancesEmptyTitle', { defaultValue: '还没有其他节点' })}</div>
                <div className="max-w-[320px] text-[12px] text-zinc-500">{t('cloudInstancesEmpty', { defaultValue: '还没有其他实例。用同一个 Email 在另一台机器上登录即可出现在这里。' })}</div>
              </div>
            ) : visible.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-white/[0.08] px-4 py-8 text-center text-[12px] text-zinc-600">{t('cloudNoMatch', { defaultValue: '没有匹配的节点' })}</div>
            ) : (
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                {visible.map((inst) => <NodeCard key={inst.instanceId} inst={inst} latest={latest} t={t} />)}
              </div>
            )}
          </section>
        )}
      </div>

      {modalOpen && (
        <div data-id="cloud-login-modal" className="fixed inset-0 z-[10000]" onClick={() => !submitting && setModalOpen(false)}>
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
          <div className="absolute left-1/2 top-1/2 w-[420px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
              <h2 className="text-[15px] font-semibold text-white">{t('cloudLoginTitle', { defaultValue: 'CiCy 账号登录' })}</h2>
              <button onClick={() => setModalOpen(false)} className="rounded-lg p-1.5 text-zinc-600 hover:bg-white/[0.06] hover:text-zinc-300"><X className="h-4 w-4" /></button>
            </div>
            <div className="space-y-4 px-5 py-5">
              {!state ? (
                <>
                  <label className="block">
                    <span className="mb-1 block text-[11px] font-medium text-zinc-400">Email</span>
                    <input data-id="cloud-login-email" autoFocus type="email" value={email} onChange={(e) => { setEmail(e.target.value); setError(''); }} placeholder="you@example.com" className={INPUT} disabled={submitting} />
                    <span className="mt-1 block text-[11px] text-zinc-600">{t('cloudLoginEmailHelp', { defaultValue: '点击邮件里的链接后，这台 cicy-code 自动完成登录。' })}</span>
                  </label>
                  <label className="block">
                    <span className="mb-1 block text-[11px] font-medium text-zinc-400">{t('cloudInstanceName', { defaultValue: '实例名称' })}</span>
                    <input data-id="cloud-login-team" value={team} onChange={(e) => { setTeam(e.target.value); setError(''); }}
                      onKeyDown={(e) => { const composing = e.nativeEvent.isComposing || e.keyCode === 229; if (!composing && e.key === 'Enter' && email.trim() && team.trim()) void submit(); }}
                      placeholder="my-laptop" className={`${INPUT} font-mono`} disabled={submitting} />
                    <span className="mt-1 block text-[11px] text-zinc-600">{t('cloudInstanceNameHelp', { defaultValue: '也是这台机器的访问域名前缀，例如 my-laptop.hub.cicy-ai.com' })}</span>
                  </label>
                  <details className="group">
                    <summary className="cursor-pointer list-none text-[11px] text-zinc-600 hover:text-zinc-400">{t('cloudAdvanced', { defaultValue: '高级选项' })}</summary>
                    <label className="mt-2 block">
                      <span className="mb-1 block text-[11px] font-medium text-zinc-400">Hub</span>
                      <input data-id="cloud-login-hub-origin" value={hubOrigin} onChange={(e) => { setHubOrigin(e.target.value); setError(''); }} placeholder={DEFAULT_HUB} className={`${INPUT} font-mono`} disabled={submitting} />
                    </label>
                  </details>
                </>
              ) : (
                <div className="rounded-xl border border-blue-500/25 bg-blue-500/[0.06] px-4 py-4 text-center">
                  <Loader2 className="mx-auto h-5 w-5 animate-spin text-blue-300" />
                  <div className="mt-2 text-[13px] font-medium text-zinc-200">{t('cloudLoginMailSent', { defaultValue: '登录邮件已发送' })}</div>
                  <div className="mt-1 text-[11px] text-zinc-500">{t('cloudLoginCodeHint', { defaultValue: '打开 {{email}}，点击链接或把邮件里的 6 位验证码填在下面。', email })}</div>
                  <div className="mx-auto mt-3 flex max-w-[260px] items-center gap-2">
                    <input data-id="cloud-login-code" value={code} inputMode="numeric" maxLength={6} autoFocus placeholder="123456"
                      onChange={(e) => { setCode(e.target.value.replace(/\D/g, '')); setError(''); }}
                      onKeyDown={(e) => { if (e.key === 'Enter' && code.trim().length === 6) void submitCode(); }}
                      className={`${INPUT} text-center font-mono text-[18px] tracking-[0.4em]`} disabled={codeSubmitting} />
                    <button type="button" data-id="cloud-login-code-submit" onClick={() => void submitCode()} disabled={codeSubmitting || code.trim().length !== 6}
                      className="shrink-0 rounded-lg bg-blue-600 px-3 py-2 text-[12px] font-medium text-white transition-colors hover:bg-blue-500 disabled:opacity-50">{codeSubmitting ? '…' : t('cloudLoginCodeSubmit', { defaultValue: '验证' })}</button>
                  </div>
                </div>
              )}
              {error && <div className="rounded-lg border border-red-500/25 bg-red-500/[0.06] px-3 py-2 text-[12px] text-red-300">{error}</div>}
              <div className="flex justify-end gap-2">
                <button type="button" onClick={() => setModalOpen(false)} className="rounded-lg px-3.5 py-2 text-[12px] text-zinc-400 hover:text-zinc-200">{t('cancel', { ns: 'common', defaultValue: '取消' })}</button>
                {!state && <button type="button" data-id="cloud-login-submit" onClick={() => void submit()} disabled={submitting || !email.trim() || !team.trim()} className="rounded-lg bg-blue-600 px-3.5 py-2 text-[12px] font-medium text-white transition-colors hover:bg-blue-500 disabled:opacity-50">{submitting ? '…' : t('cloudLoginSend', { defaultValue: '发送登录邮件' })}</button>}
                {state && error && <button type="button" onClick={() => { setState(''); setError(''); }} className="rounded-lg border border-white/[0.1] px-3.5 py-2 text-[12px] text-zinc-200">{t('cloudLoginResend', { defaultValue: '重新发送' })}</button>}
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
