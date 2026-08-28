// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Settings → CiCy 账号. The single cicy_cloud IM account is the instance's
// identity on CiCy Hub / Cloud; it used to hide inside the IM notification
// list. Here it gets its own page: logged-in state, tenant directory (every
// cicy-code that signed in with the same email), login and sign-out.

import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ExternalLink, Loader2, LogOut, Mail, RefreshCw, X } from 'lucide-react';
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
  frp?: { host: string; ports: Record<string, number>; ssh?: string; sshLive?: boolean; httpLive?: boolean };
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


function fmtTime(raw?: string): string {
  if (!raw) return '—';
  const ms = Date.parse(raw.includes('T') ? raw : raw.replace(' ', 'T') + 'Z');
  return Number.isFinite(ms) ? new Date(ms).toLocaleString() : raw;
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
  const [frp, setFrp] = useState<{ supported?: boolean; enabled?: boolean; running?: boolean; error?: string; ports?: Record<string, number>; host?: string } | null>(null);
  const [frpBusy, setFrpBusy] = useState(false);
  const [codeSubmitting, setCodeSubmitting] = useState(false);
  const [error, setError] = useState('');

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

  return (
    <div data-id="cloud-account-panel" className="h-full overflow-y-auto px-8 py-7 text-zinc-300">
      <div className="mx-auto max-w-[680px] space-y-6">
        <div>
          <h2 className="text-[15px] font-semibold text-white">{t('settingsNavAccount', { defaultValue: 'CiCy 账号' })}</h2>
          <p className="mt-1 text-[12px] text-zinc-500">{t('cloudAccountIntro', { defaultValue: '用 Email 登录后，所有用同一 Email 登录的 cicy-code 互相可见、可互发消息。' })}</p>
        </div>

        <section data-id="cloud-account-status" className="rounded-xl border border-white/[0.08] bg-white/[0.025] p-5">
          {loading ? (
            <div className="flex items-center gap-2 text-[12px] text-zinc-500"><Loader2 className="h-4 w-4 animate-spin" />{t('loadingText', { defaultValue: '加载中…' })}</div>
          ) : !account ? (
            <div className="flex items-center justify-between gap-4">
              <div className="flex items-center gap-3">
                <span className="grid h-9 w-9 place-items-center rounded-full bg-white/[0.05] text-zinc-500"><Mail size={16} /></span>
                <div>
                  <div className="text-[13px] font-medium text-zinc-200">{t('cloudNotLoggedIn', { defaultValue: '未登录' })}</div>
                  <div className="text-[11px] text-zinc-500">{t('cloudNotLoggedInHint', { defaultValue: '登录后这台 cicy-code 才能和你的其他实例互通。' })}</div>
                </div>
              </div>
              <button type="button" data-id="cloud-account-login" onClick={openLogin} className="rounded-lg bg-blue-600 px-3.5 py-2 text-[12px] font-medium text-white transition-colors hover:bg-blue-500">{t('cloudLogin', { defaultValue: 'Email 登录' })}</button>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                  <span className="grid h-9 w-9 place-items-center rounded-full bg-blue-500/15 text-blue-300"><Mail size={16} /></span>
                  <div>
                    <div className="flex items-center gap-2 text-[13px] font-medium text-zinc-100">
                      {account.config?.email || account.name}
                      <span data-id="cloud-account-state" className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] ${connected ? 'bg-emerald-500/10 text-emerald-300' : 'bg-red-500/10 text-red-300'}`}>
                        <span className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-400' : 'bg-red-400'}`} />
                        {connected ? t('cloudLoggedIn', { defaultValue: '已登录' }) : (account.state_detail || t('cloudNotAuthed', { defaultValue: '未认证' }))}
                      </span>
                    </div>
                    <div className="mt-0.5 font-mono text-[11px] text-zinc-500">
                      {isHub ? `Hub · ${String(account.config?.cloud_origin || '')}` : `Cloud · Team ${String(account.config?.team_id || '—')}`}
                      {account.config?.instance_id ? <> · <span title={String(account.config.instance_id)}>{maskId(String(account.config.instance_id))}</span></> : null}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <button type="button" data-id="cloud-account-relogin" onClick={openLogin} className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.1] bg-white/[0.04] px-3 py-2 text-[12px] text-zinc-200 transition-colors hover:bg-white/[0.08]"><RefreshCw size={12} />{connected ? t('cloudRelogin', { defaultValue: '重新登录' }) : t('cloudLogin', { defaultValue: 'Email 登录' })}</button>
                  <button type="button" data-id="cloud-account-signout" onClick={() => void signOut()} className="inline-flex items-center gap-1.5 rounded-lg border border-red-500/20 bg-red-500/[0.06] px-3 py-2 text-[12px] text-red-300 transition-colors hover:bg-red-500/[0.12]"><LogOut size={12} />{t('cloudSignOut', { defaultValue: '退出登录' })}</button>
                </div>
              </div>
            </div>
          )}
        </section>

        {connected && isHub && (
          <section data-id="cloud-account-frp" className="flex items-center justify-between gap-4 rounded-xl border border-white/[0.08] bg-white/[0.025] px-5 py-4">
            <div className="min-w-0">
              <div className="flex items-center gap-2 text-[13px] font-medium text-zinc-200">
                {t('cloudFrpTitle', { defaultValue: 'frp 隧道（SSH · 低延迟访问）' })}
                {frp?.enabled ? <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] ${frp.running ? 'bg-emerald-500/10 text-emerald-300' : 'bg-amber-500/10 text-amber-300'}`}><span className={`h-1.5 w-1.5 rounded-full ${frp.running ? 'bg-emerald-400' : 'bg-amber-400'}`} />{frp.running ? t('cloudFrpRunning', { defaultValue: '运行中' }) : t('cloudFrpStarting', { defaultValue: '启动中…' })}</span> : null}
              </div>
              <div className="mt-0.5 text-[11px] text-zinc-500">
                {frp?.supported === false ? t('cloudFrpUnsupported', { defaultValue: '当前系统暂不支持内置 frp 客户端。' })
                  : frp?.running && frp.ports?.ssh ? <span>SSH <code className="rounded bg-white/[0.05] px-1.5 py-0.5 font-mono text-zinc-300">ssh -p {frp.ports.ssh} &lt;user&gt;@{frp.host}</code>{frp.ports.http ? <span> · HTTP 经 hub 本机 :{frp.ports.http}</span> : null}</span>
                  : t('cloudFrpHint', { defaultValue: '开启后这台 cicy-code 自动接入 hub 的 frps：同账号的其他节点可以直接 SSH 过来，域名访问不再绕 Cloudflare。不用敲任何命令。' })}
                {frp?.error ? <span className="ml-2 text-red-300">{frp.error}</span> : null}
              </div>
            </div>
            <button type="button" data-id="cloud-frp-toggle" role="switch" aria-checked={!!frp?.enabled} disabled={frpBusy || frp?.supported === false}
              onClick={() => void toggleFrp(!frp?.enabled)}
              className={`relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-50 ${frp?.enabled ? 'bg-emerald-500' : 'bg-zinc-700'}`}>
              <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all ${frp?.enabled ? 'left-[22px]' : 'left-0.5'}`} />
            </button>
          </section>
        )}

        {connected && (
          <section data-id="cloud-account-instances" className="space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-[12px] font-semibold text-zinc-400">{t('cloudInstancesTitle', { defaultValue: '同一账号下的 cicy-code' })} <span className="text-zinc-600">{instances.length}</span></h3>
            </div>
            <div className="overflow-hidden rounded-xl border border-white/[0.08]">
              {instances.length === 0 && <div className="px-4 py-4 text-[12px] text-zinc-600">{t('cloudInstancesEmpty', { defaultValue: '还没有其他实例。用同一个 Email 在另一台机器上登录即可出现在这里。' })}</div>}
              {instances.map((inst) => {
                const online = inst.status === 'online';
                const r = inst.resources;
                const frp = inst.frp;
                const sysTitle = [
                  [inst.platform, inst.arch, inst.runtime].filter(Boolean).join(' · '),
                  inst.cpuModel ? `CPU ${inst.cpuModel}${inst.cpuCores ? ` · ${inst.cpuCores}C` : ''}` : '',
                  inst.memoryTotalMB ? `${t('cloudSysMem', { defaultValue: '内存' })} ${(inst.memoryTotalMB / 1024).toFixed(1)} GB` : '',
                  inst.gpu ? `GPU ${inst.gpu}` : '',
                  inst.publicIp ? `IP ${inst.publicIp}` : '',
                  `${t('cloudSysSeen', { defaultValue: '最近在线' })} ${fmtTime(inst.lastSeenAt)}`,
                  inst.instanceId,
                ].filter(Boolean).join('\n');
                const pct = (v?: number) => (v != null && Number.isFinite(v) ? Math.max(0, Math.min(100, v)) : null);
                const Meter = ({ label, value, text }: { label: string; value: number | null; text: string }) => (
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline justify-between gap-2 text-[11px] leading-4">
                      <span className="text-zinc-500">{label}</span>
                      <span className="truncate font-mono text-zinc-400">{text}</span>
                    </div>
                    <div className="mt-0.5 h-1 overflow-hidden rounded-full bg-white/[0.06]"><div className={`h-full rounded-full ${value == null ? 'bg-zinc-700' : value >= 90 ? 'bg-red-400' : value >= 70 ? 'bg-amber-400' : 'bg-emerald-400'}`} style={{ width: `${value ?? 0}%` }} /></div>
                  </div>
                );
                const sshCmd = frp?.ports?.ssh ? `ssh -p ${frp.ports.ssh} ${frp.host}` : '';
                return (
                  <div key={inst.instanceId} data-id={`cloud-instance-${inst.instanceId}`} className="border-b border-white/[0.05] px-4 py-3 last:border-b-0" title={sysTitle}>
                    {/* line 1: name · state · domain · version */}
                    <div className="flex items-center gap-2 text-[13px]">
                      <span className={`h-2 w-2 shrink-0 rounded-full ${online ? 'bg-emerald-400' : 'bg-zinc-600'}`} title={online ? t('cloudOnline', { defaultValue: '在线' }) : `${t('cloudOffline', { defaultValue: '离线' })} · ${fmtTime(inst.lastSeenAt)}`} />
                      <span className="truncate font-medium text-zinc-200">{inst.teamId || inst.instanceId}</span>
                      {inst.self ? <span className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-zinc-400">{t('cloudInstanceSelf', { defaultValue: '本机' })}</span> : null}
                      {inst.proxyHost ? (
                        <a data-id={`cloud-instance-domain-${inst.instanceId}`} href={`https://${inst.proxyHost}`} target="_blank" rel="noopener noreferrer"
                          onClick={(e) => { e.preventDefault(); e.stopPropagation(); void openInstanceHost(inst); }}
                          className={`ml-1 inline-flex min-w-0 items-center gap-1 truncate font-mono text-[11px] underline-offset-2 hover:underline ${inst.proxyAvailable ? 'text-blue-400 hover:text-blue-300' : 'text-zinc-500 hover:text-zinc-300'}`}
                          title={inst.proxyAvailable ? `https://${inst.proxyHost}` : t('cloudProxyOffline', { defaultValue: '隧道未上报，域名暂时不可用' })}>
                          <ExternalLink size={10} className="shrink-0" /><span className="truncate">{inst.proxyHost}</span>
                        </a>
                      ) : null}
                      <span className="ml-auto shrink-0 font-mono text-[11px] text-zinc-600">{[inst.platform, inst.arch].filter(Boolean).join('/')}{inst.version ? ` · v${inst.version}` : ''}</span>
                    </div>
                    {/* line 2: cpu / mem / disk / load */}
                    {r ? (
                      <div data-id={`cloud-instance-res-${inst.instanceId}`} className="mt-2 flex items-end gap-4">
                        <Meter label="CPU" value={pct(r.cpu_usage_pct)} text={`${r.cpu_cores ? `${r.cpu_cores}C · ` : ''}${r.cpu_usage_pct != null ? `${r.cpu_usage_pct.toFixed(0)}%` : '--'}`} />
                        <Meter label={t('cloudSysMem', { defaultValue: '内存' })} value={pct(r.mem_usage_pct) ?? (r.mem_used_bytes && r.mem_total_bytes ? (r.mem_used_bytes / r.mem_total_bytes) * 100 : null)} text={`${fmtBytes(r.mem_used_bytes)} / ${fmtBytes(r.mem_total_bytes)}`} />
                        <Meter label={t('cloudSysDisk', { defaultValue: '磁盘' })} value={pct(r.disk_usage_pct) ?? (r.disk_used_bytes && r.disk_total_bytes ? (r.disk_used_bytes / r.disk_total_bytes) * 100 : null)} text={`${fmtBytes(r.disk_used_bytes)} / ${fmtBytes(r.disk_total_bytes)}`} />
                        {r.load_1 != null ? <div className="shrink-0 text-[11px] leading-4 text-zinc-500" title="load 1m / 5m / 15m"><span className="text-zinc-600">{t('cloudSysLoad', { defaultValue: '负载' })} </span><span className="font-mono text-zinc-400">{[r.load_1, r.load_5, r.load_15].map((v) => (v ?? 0).toFixed(2)).join(' · ')}</span></div> : null}
                      </div>
                    ) : (
                      <div className="mt-1.5 text-[11px] text-zinc-600">{online ? t('cloudResPending', { defaultValue: '等待资源上报…' }) : `${t('cloudSysSeen', { defaultValue: '最近在线' })} ${fmtTime(inst.lastSeenAt)}`}</div>
                    )}
                    {/* line 3: ssh + ports */}
                    {(sshCmd || (inst.ports && inst.ports.length > 0 && inst.proxyHost) || (inst.hub && !inst.self && !frp)) ? (
                      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
                        {sshCmd ? (
                          <button type="button" data-id={`cloud-instance-ssh-${inst.instanceId}`} onClick={(e) => { e.stopPropagation(); void navigator.clipboard?.writeText(sshCmd); toast(t('copied', { ns: 'common', defaultValue: '已复制' })); }}
                            className="inline-flex items-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-0.5 font-mono text-zinc-300 hover:border-blue-500/40 hover:text-blue-300"
                            title={frp?.sshLive ? t('cloudSshCopyHint', { defaultValue: '点击复制 SSH 命令' }) : t('cloudSshOffline', { defaultValue: 'frp 未连接，SSH 暂不可达' })}>
                            <span className={`h-1.5 w-1.5 rounded-full ${frp?.sshLive ? 'bg-emerald-400' : 'bg-zinc-600'}`} />{sshCmd}
                          </button>
                        ) : null}
                        {inst.ports && inst.proxyHost ? inst.ports.filter((p) => p.visibility !== 'closed').map((p) => {
                          const base = inst.proxyHost!.replace(/^([^.]+)\./, `$1-${p.port}.`);
                          return (
                            <a key={p.port} href={`https://${base}`} target="_blank" rel="noopener noreferrer" onClick={(e) => { e.preventDefault(); e.stopPropagation(); void openInstanceHost(inst, p.port); }}
                              className="inline-flex items-center gap-1 rounded-md border border-white/[0.08] bg-white/[0.03] px-1.5 py-0.5 font-mono text-[10px] text-zinc-400 hover:border-blue-500/40 hover:text-blue-300" title={`https://${base}`}>
                              <span className={`h-1.5 w-1.5 rounded-full ${p.visibility === 'public' ? 'bg-emerald-400' : 'bg-zinc-500'}`} />:{p.port}{p.name ? ` ${p.name}` : ''}
                            </a>
                          );
                        }) : null}
                        {inst.hub && !inst.self && !frp ? <span className="text-zinc-600">{t('cloudFrpPending', { defaultValue: '未接入 frp（升级到最新版并重启即可自动接入）' })}</span> : null}
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
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
                    <span className="mb-1 block text-[11px] font-medium text-zinc-400">Hub 地址</span>
                    <input data-id="cloud-login-hub-origin" value={hubOrigin} onChange={(e) => { setHubOrigin(e.target.value); setError(''); }} placeholder={DEFAULT_HUB} className={`${INPUT} font-mono`} disabled={submitting} />
                  </label>
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
                  </label>
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
