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

function ResourceBar({ label, used, total, pct, extra }: { label: string; used?: number; total?: number; pct?: number; extra?: string }) {
  const value = pct != null && Number.isFinite(pct) ? Math.max(0, Math.min(100, pct)) : (used != null && total ? Math.max(0, Math.min(100, (used / total) * 100)) : null);
  const tone = value == null ? 'bg-zinc-600' : value >= 90 ? 'bg-red-400' : value >= 70 ? 'bg-amber-400' : 'bg-emerald-400';
  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="text-zinc-500">{label}</span>
        <span className="truncate font-mono text-zinc-400">{extra ?? (used != null && total ? `${fmtBytes(used)} / ${fmtBytes(total)}` : '')}{value != null ? <span className={`ml-1.5 ${value >= 90 ? 'text-red-300' : value >= 70 ? 'text-amber-300' : 'text-zinc-300'}`}>{value.toFixed(0)}%</span> : <span className="ml-1.5 text-zinc-600">--</span>}</span>
      </div>
      <div className="mt-1 h-1 w-full overflow-hidden rounded-full bg-white/[0.06]"><div className={`h-full rounded-full ${tone}`} style={{ width: `${value ?? 0}%` }} /></div>
    </div>
  );
}

function fmtTime(raw?: string): string {
  if (!raw) return '—';
  const ms = Date.parse(raw.includes('T') ? raw : raw.replace(' ', 'T') + 'Z');
  return Number.isFinite(ms) ? new Date(ms).toLocaleString() : raw;
}

const INPUT = 'h-10 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors hover:border-white/[0.14] focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 disabled:opacity-50';
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
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const acc = await fetchCloudAccount();
      setAccount(acc);
      onAccountChange?.(acc);
      if (acc && acc.state === 'connected') {
        const res = await apiService.getCiCyCloudInstances().catch(() => null);
        setInstances(((res?.data?.instances || []) as CloudInstanceInfo[]));
      } else {
        setInstances([]);
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

  const submit = async () => {
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
                      {account.config?.instance_id ? ` · ${String(account.config.instance_id).slice(0, 16)}…` : ''}
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

        {connected && (
          <section data-id="cloud-account-instances" className="space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-[12px] font-semibold text-zinc-400">{t('cloudInstancesTitle', { defaultValue: '同一账号下的 cicy-code' })} <span className="text-zinc-600">{instances.length}</span></h3>
            </div>
            <div className="overflow-hidden rounded-xl border border-white/[0.08]">
              {instances.length === 0 && <div className="px-4 py-4 text-[12px] text-zinc-600">{t('cloudInstancesEmpty', { defaultValue: '还没有其他实例。用同一个 Email 在另一台机器上登录即可出现在这里。' })}</div>}
              {instances.map((inst) => {
                const online = inst.status === 'online';
                return (
                  <div key={inst.instanceId} data-id={`cloud-instance-${inst.instanceId}`} className="flex items-center gap-3 border-b border-white/[0.05] px-4 py-2.5 last:border-b-0">
                    <span className={`h-2 w-2 shrink-0 rounded-full ${online ? 'bg-emerald-400' : 'bg-zinc-600'}`} />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-[13px] text-zinc-200">{inst.teamId || inst.instanceId}{inst.self ? <span className="ml-2 rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-zinc-400">{t('cloudInstanceSelf', { defaultValue: '本机' })}</span> : null}</div>
                      <div className="truncate font-mono text-[11px] text-zinc-600">{inst.instanceId}{inst.platform ? ` · ${inst.platform}` : ''}</div>
                      <div data-id={`cloud-instance-sys-${inst.instanceId}`} className="mt-1.5 grid grid-cols-2 gap-x-4 gap-y-0.5 text-[11px] text-zinc-500 sm:grid-cols-3">
                        <div className="truncate" title={[inst.platform, inst.arch, inst.runtime].filter(Boolean).join(' · ')}><span className="text-zinc-600">{t('cloudSysOS', { defaultValue: '系统' })} </span>{[inst.platform, inst.arch, inst.runtime].filter(Boolean).join(' · ') || '—'}</div>
                        <div className="truncate" title={inst.cpuModel}><span className="text-zinc-600">CPU </span>{inst.cpuModel ? `${inst.cpuModel}${inst.cpuCores ? ` · ${inst.cpuCores}C` : ''}` : '—'}</div>
                        <div className="truncate"><span className="text-zinc-600">{t('cloudSysMem', { defaultValue: '内存' })} </span>{inst.memoryTotalMB ? `${(inst.memoryTotalMB / 1024).toFixed(1)} GB` : '—'}</div>
                        <div className="truncate" title={inst.gpu}><span className="text-zinc-600">GPU </span>{inst.gpu || '—'}</div>
                        <div className="truncate font-mono"><span className="font-sans text-zinc-600">IP </span>{inst.publicIp || '—'}</div>
                        <div className="truncate"><span className="text-zinc-600">{t('cloudSysSeen', { defaultValue: '最近在线' })} </span>{fmtTime(inst.lastSeenAt)}</div>
                        {inst.version ? <div className="truncate font-mono"><span className="font-sans text-zinc-600">cicy-code </span>{inst.version}</div> : null}
                      </div>
                      {inst.resources ? (
                        <div data-id={`cloud-instance-res-${inst.instanceId}`} className="mt-2 grid grid-cols-1 gap-x-5 gap-y-1.5 sm:grid-cols-3">
                          <ResourceBar label="CPU" pct={inst.resources.cpu_usage_pct} extra={inst.resources.cpu_cores ? `${inst.resources.cpu_cores} cores` : ''} />
                          <ResourceBar label={t('cloudSysMem', { defaultValue: '内存' })} used={inst.resources.mem_used_bytes} total={inst.resources.mem_total_bytes} pct={inst.resources.mem_usage_pct} />
                          <ResourceBar label={t('cloudSysDisk', { defaultValue: '磁盘' })} used={inst.resources.disk_used_bytes} total={inst.resources.disk_total_bytes} pct={inst.resources.disk_usage_pct} />
                          {inst.resources.load_1 != null && (
                            <div className="text-[11px] text-zinc-500 sm:col-span-3"><span className="text-zinc-600">{t('cloudSysLoad', { defaultValue: '负载' })} </span><span className="font-mono text-zinc-400">{[inst.resources.load_1, inst.resources.load_5, inst.resources.load_15].map((v) => (v ?? 0).toFixed(2)).join(' · ')}</span><span className="ml-1 text-zinc-600">1m / 5m / 15m</span></div>
                          )}
                        </div>
                      ) : null}
                      {inst.ports && inst.ports.length > 0 && inst.proxyHost ? (
                        <div data-id={`cloud-instance-ports-${inst.instanceId}`} className="mt-1.5 flex flex-wrap gap-1.5">
                          {inst.ports.filter((p) => p.visibility !== 'closed').map((p) => {
                            const base = inst.proxyHost!.replace(/^([^.]+)\./, `$1-${p.port}.`);
                            return (
                              <a key={p.port} href={`https://${base}`} target="_blank" rel="noopener noreferrer" onClick={(e) => e.stopPropagation()}
                                className="inline-flex items-center gap-1 rounded-md border border-white/[0.08] bg-white/[0.03] px-1.5 py-0.5 font-mono text-[10px] text-zinc-400 hover:border-blue-500/40 hover:text-blue-300"
                                title={`https://${base}`}>
                                <span className={`h-1.5 w-1.5 rounded-full ${p.visibility === 'public' ? 'bg-emerald-400' : 'bg-zinc-500'}`} />:{p.port}{p.name ? ` ${p.name}` : ''}
                              </a>
                            );
                          })}
                        </div>
                      ) : null}
                      {inst.proxyHost ? (
                        <a data-id={`cloud-instance-domain-${inst.instanceId}`} href={`https://${inst.proxyHost}`} target="_blank" rel="noopener noreferrer"
                          onClick={(e) => e.stopPropagation()}
                          className={`mt-0.5 inline-flex max-w-full items-center gap-1 truncate font-mono text-[11px] underline-offset-2 hover:underline ${inst.proxyAvailable ? 'text-blue-400 hover:text-blue-300' : 'text-zinc-500 hover:text-zinc-300'}`}
                          title={inst.proxyAvailable ? undefined : t('cloudProxyOffline', { defaultValue: '隧道未上报，域名暂时不可用' })}>
                          <ExternalLink size={10} className="shrink-0" />
                          <span className="truncate">{inst.proxyHost}</span>
                        </a>
                      ) : null}
                    </div>
                    <div className="text-[11px] text-zinc-500">{online ? t('cloudOnline', { defaultValue: '在线' }) : t('cloudOffline', { defaultValue: '离线' })}</div>
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
                  <div className="mt-1 text-[11px] text-zinc-500">{t('cloudLoginMailHint', { defaultValue: '请打开 {{email}} 并点击登录链接，本页面会自动完成绑定。', email })}</div>
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
