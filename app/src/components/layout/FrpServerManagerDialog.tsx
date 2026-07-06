// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState, Fragment } from 'react';
import { X, RefreshCw, Play, Square, RotateCw, Loader2, ChevronDown, ChevronRight, Server, Copy, Eye, EyeOff, Download } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { cn, copyToClipboard } from '../../lib/utils';

type LifecycleAction = 'start' | 'stop' | 'restart' | 'reload';

type ServerInfo = Record<string, string>;

type Client = {
  key: string;
  user: string;
  clientID: string;
  hostname: string;
  clientIP: string;
  online: boolean;
};

export function FrpServerManagerDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation('agentInspector');
  const [running, setRunning] = useState<boolean | null>(null);
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [clients, setClients] = useState<Client[] | null>(null);
  const [connections, setConnections] = useState<string | null>(null);
  const [logs, setLogs] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [actionPending, setActionPending] = useState<LifecycleAction | null>(null);
  const [actionOutput, setActionOutput] = useState('');
  const [actionFailed, setActionFailed] = useState(false);
  const [showConnections, setShowConnections] = useState(false);
  const [showLogs, setShowLogs] = useState(false);
  const [showInstall, setShowInstall] = useState(true);
  const [install, setInstall] = useState<{
    public_ip?: string;
    server_port?: number;
    token?: string;
    install_command?: string;
    install_command_bash?: string;
    install_command_powershell?: string;
  } | null>(null);
  const [installLoading, setInstallLoading] = useState(false);
  const [installVariant, setInstallVariant] = useState<'bash' | 'powershell'>('bash');
  const [revealToken, setRevealToken] = useState(false);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  const loadStatus = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await apiService.getFrpServerStatus();
      const data = resp?.data as { running?: boolean; info?: ServerInfo };
      setRunning(!!data?.running);
      setInfo(data?.info || null);
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
      setRunning(null);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadClients = useCallback(async () => {
    try {
      const resp = await apiService.getFrpServerClients();
      const data = resp?.data as { clients?: Client[] };
      setClients(data?.clients || []);
    } catch {
      setClients([]);
    }
  }, []);

  const loadConnections = useCallback(async () => {
    try {
      const resp = await apiService.getFrpServerConnections();
      const data = resp?.data as { output?: string };
      setConnections(data?.output || '');
    } catch (e: any) {
      setConnections(String(e?.response?.data?.detail || e?.message || 'failed'));
    }
  }, []);

  const loadLogs = useCallback(async () => {
    try {
      const resp = await apiService.getFrpServerLogs(80);
      const data = resp?.data as { logs?: string };
      setLogs(data?.logs || '');
    } catch (e: any) {
      setLogs(String(e?.response?.data?.detail || e?.message || 'failed'));
    }
  }, []);

  const refresh = useCallback(async () => {
    await loadStatus();
    await loadClients();
  }, [loadStatus, loadClients]);

  const loadInstallInfo = useCallback(async () => {
    setInstallLoading(true);
    try {
      const resp = await apiService.getFrpServerInstallInfo();
      setInstall(resp?.data || null);
    } catch (e: any) {
      setInstall({ install_command: '# failed to load: ' + (e?.message || 'unknown') });
    } finally {
      setInstallLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    refresh();
    loadInstallInfo();
    setActionOutput('');
    setActionFailed(false);
    setShowConnections(false);
    setShowLogs(false);
    setConnections(null);
    setLogs(null);
  }, [open, refresh, loadInstallInfo]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.preventDefault(); onClose(); } };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  const runAction = useCallback(async (action: LifecycleAction) => {
    setActionPending(action);
    setActionOutput('');
    setActionFailed(false);
    try {
      const resp = await apiService.frpServerLifecycle(action);
      const data = resp?.data as { success?: boolean; output?: string; error?: string };
      const failed = data?.success === false;
      setActionFailed(failed);
      setActionOutput(data?.output || data?.error || (failed ? 'action failed' : 'done'));
      if (failed) { setShowLogs(true); await loadLogs(); }
    } catch (e: any) {
      setActionFailed(true);
      setActionOutput(String(e?.response?.data?.detail || e?.message || 'failed'));
      setShowLogs(true);
      await loadLogs();
    } finally {
      setActionPending(null);
      await refresh();
    }
  }, [refresh, loadLogs]);

  const toggleConnections = useCallback(async () => {
    const next = !showConnections;
    setShowConnections(next);
    if (next && connections === null) await loadConnections();
  }, [showConnections, connections, loadConnections]);

  const toggleLogs = useCallback(async () => {
    const next = !showLogs;
    setShowLogs(next);
    if (next && logs === null) await loadLogs();
  }, [showLogs, logs, loadLogs]);

  if (!open) return null;

  const isRunning = running === true;

  return (
    <div
      data-id="frp-server-drawer-overlay"
      className="fixed inset-0 z-[100000] flex justify-end cursor-pointer"
      onClick={onClose}
    >
      <div className="absolute inset-0 bg-black/55 backdrop-blur-sm" />
      <aside
        data-id="frp-server-drawer"
        className="relative z-10 flex h-full w-[760px] max-w-[96vw] cursor-default flex-col border-l border-white/[0.08] bg-[#0f0f11] shadow-2xl shadow-black/60"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <header className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
          <div className="flex items-center gap-2.5 min-w-0">
            <Server className="w-4 h-4 text-zinc-400 shrink-0" />
            <div className="min-w-0">
              <h2 className="text-[15px] font-semibold text-white">{t('frpServerManagerTitle')}</h2>
              <p className="mt-0.5 text-[11px] text-zinc-600">{t('frpServerManagerSubtitle')}</p>
            </div>
          </div>
          <div className="flex items-center gap-1">
            <button
              data-id="frp-server-drawer-refresh"
              type="button"
              onClick={refresh}
              disabled={loading}
              className="rounded-lg border border-white/[0.08] bg-white/[0.04] p-1.5 text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:opacity-40"
            >
              <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
            </button>
            <button
              data-id="frp-server-drawer-close"
              type="button"
              onClick={onClose}
              className="rounded-lg p-1.5 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </header>

        <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">
          {/* Error */}
          {error && (
            <div className="rounded-lg border border-red-500/25 bg-red-500/10 px-3 py-2 text-[12px] text-red-200 whitespace-pre-wrap">
              {error}
            </div>
          )}

          {/* Status card */}
          <div data-id="frp-server-status-card" className="rounded-lg border border-white/[0.07] bg-white/[0.02] p-4">
            <div className="flex items-center justify-between mb-3">
              <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-zinc-500">
                {t('frpServerManagerStatus')}
              </span>
              {running !== null && (
                <span className={cn(
                  'inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-[11px] border',
                  isRunning
                    ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300'
                    : 'border-zinc-700 bg-zinc-800/60 text-zinc-400'
                )}>
                  <span className={cn('h-1.5 w-1.5 rounded-full', isRunning ? 'bg-emerald-400' : 'bg-zinc-500')} />
                  {isRunning ? 'running' : 'stopped'}
                </span>
              )}
            </div>
            {info && (
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[11px]">
                {(['pid', 'config', 'binary', 'log', 'bind', 'dashboard', 'started_at'] as const).map(k => {
                  const v = info[k];
                  if (!v) return null;
                  return (
                    <Fragment key={k}>
                      <dt className="text-zinc-500 whitespace-nowrap">{k}</dt>
                      <dd className="font-mono text-zinc-300 truncate" title={v}>{v}</dd>
                    </Fragment>
                  );
                })}
              </dl>
            )}
            {loading && !info && (
              <div className="text-[12px] text-zinc-500 flex items-center gap-2">
                <Loader2 className="w-3 h-3 animate-spin" /> Loading…
              </div>
            )}
          </div>

          {/* Action buttons */}
          <div data-id="frp-server-actions" className="flex items-center gap-2 flex-wrap">
            {(['start', 'stop', 'restart', 'reload'] as LifecycleAction[]).map(action => {
              const Icon = action === 'start' ? Play : action === 'stop' ? Square : RotateCw;
              const isPending = actionPending === action;
              const disabled = !!actionPending || (action === 'start' && isRunning) || (action === 'stop' && !isRunning);
              return (
                <button
                  key={action}
                  data-id={`frp-server-action-${action}`}
                  type="button"
                  onClick={() => runAction(action)}
                  disabled={disabled}
                  className={cn(
                    'inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-[12px] transition-colors disabled:opacity-40',
                    action === 'start'
                      ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-200 hover:bg-emerald-500/20'
                      : action === 'stop'
                      ? 'border-red-500/30 bg-red-500/10 text-red-200 hover:bg-red-500/20'
                      : 'border-white/[0.08] bg-white/[0.03] text-zinc-300 hover:bg-white/[0.07]'
                  )}
                >
                  {isPending
                    ? <Loader2 className="w-3 h-3 animate-spin" />
                    : <Icon className="w-3 h-3" />}
                  {t(`frpServerManagerAction${action.charAt(0).toUpperCase() + action.slice(1)}` as any)}
                </button>
              );
            })}
          </div>

          {/* Action output */}
          {actionOutput && (
            <div
              data-id="frp-server-action-output"
              className={cn(
                'rounded-lg border px-3 py-2 text-[11px] font-mono whitespace-pre-wrap',
                actionFailed
                  ? 'border-red-500/30 bg-red-500/10 text-red-200'
                  : 'border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-100'
              )}
            >
              {actionOutput}
            </div>
          )}

          {/* Connected clients */}
          <div data-id="frp-server-clients-section">
            <div className="text-[11px] font-medium uppercase tracking-[0.08em] text-zinc-500 mb-2">
              {t('frpServerManagerClients')}
            </div>
            {clients === null ? (
              <div className="text-[12px] text-zinc-500 flex items-center gap-2">
                <Loader2 className="w-3 h-3 animate-spin" /> Loading…
              </div>
            ) : clients.length === 0 ? (
              <div className="rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-4 text-center text-[12px] text-zinc-600">
                {t('frpServerManagerNoClients')}
              </div>
            ) : (
              <table className="w-full table-fixed border-collapse text-[11px]">
                <colgroup>
                  <col style={{ width: 120 }} />
                  <col style={{ width: 80 }} />
                  <col />
                  <col style={{ width: 120 }} />
                  <col style={{ width: 56 }} />
                </colgroup>
                <thead>
                  <tr className="text-left text-[10px] uppercase tracking-[0.08em] text-zinc-600 border-b border-white/[0.05]">
                    <th className="pb-1.5">{t('frpServerManagerColKey')}</th>
                    <th className="pb-1.5">{t('frpServerManagerColUser')}</th>
                    <th className="pb-1.5">{t('frpServerManagerColHost')}</th>
                    <th className="pb-1.5">{t('frpServerManagerColIP')}</th>
                    <th className="pb-1.5">{t('frpServerManagerColOnline')}</th>
                  </tr>
                </thead>
                <tbody>
                  {clients.map((c, i) => (
                    <tr key={i} className="border-t border-white/[0.04] align-middle">
                      <td className="py-1.5 pr-2 font-mono text-zinc-300 truncate" title={c.key}>{c.key || '—'}</td>
                      <td className="py-1.5 pr-2 text-zinc-400 truncate">{c.user || '—'}</td>
                      <td className="py-1.5 pr-2 font-mono text-zinc-400 truncate" title={c.hostname}>{c.hostname || '—'}</td>
                      <td className="py-1.5 pr-2 font-mono text-zinc-400">{c.clientIP || '—'}</td>
                      <td className="py-1.5">
                        <span className={cn('inline-block h-1.5 w-1.5 rounded-full', c.online ? 'bg-emerald-400' : 'bg-zinc-600')} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {/* Connections (collapsible) */}
          <div data-id="frp-server-connections-section">
            <button
              type="button"
              onClick={toggleConnections}
              className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-[0.08em] text-zinc-500 hover:text-zinc-300 transition-colors"
            >
              {showConnections ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
              {t('frpServerManagerConnections')}
            </button>
            {showConnections && (
              <div className="mt-2 rounded-lg border border-white/[0.06] bg-black/30 px-3 py-2 text-[11px] font-mono text-zinc-300 whitespace-pre-wrap max-h-48 overflow-y-auto">
                {connections === null
                  ? <span className="text-zinc-500">Loading…</span>
                  : connections || <span className="text-zinc-600">No connections</span>}
              </div>
            )}
          </div>

          {/* Install on a client (collapsible) */}
          <div data-id="frp-server-install-section">
            <button
              type="button"
              onClick={() => {
                const next = !showInstall;
                setShowInstall(next);
                if (next && !install) loadInstallInfo();
              }}
              className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-[0.08em] text-zinc-500 hover:text-zinc-300 transition-colors"
            >
              {showInstall ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
              <Download className="w-3 h-3" />
              Install on a client machine
            </button>
            {showInstall && (
              <div className="mt-2 rounded-lg border border-white/[0.06] bg-black/30 p-3 space-y-3">
                {installLoading && !install && (
                  <div className="text-[11px] text-zinc-500 flex items-center gap-2">
                    <Loader2 className="w-3 h-3 animate-spin" /> Fetching public IP + token…
                  </div>
                )}
                {install && (
                  <>
                    <dl className="grid grid-cols-[80px_1fr_auto] gap-x-3 gap-y-1 text-[11px] items-center">
                      <dt className="text-zinc-500">Server</dt>
                      <dd className="font-mono text-zinc-200 truncate">
                        {install.public_ip || <span className="text-zinc-600">(unknown)</span>}:{install.server_port || '?'}
                      </dd>
                      <dd>
                        <button
                          type="button"
                          onClick={async () => {
                            const v = `${install.public_ip || ''}:${install.server_port || ''}`;
                            await copyToClipboard(v);
                            setCopiedKey('addr'); setTimeout(() => setCopiedKey(null), 1500);
                          }}
                          className="text-zinc-400 hover:text-zinc-200 p-1"
                          title="Copy"
                        >
                          {copiedKey === 'addr' ? '✓' : <Copy className="w-3 h-3" />}
                        </button>
                      </dd>

                      <dt className="text-zinc-500">Token</dt>
                      <dd className="font-mono text-zinc-200 truncate">
                        {install.token
                          ? (revealToken ? install.token : install.token.slice(0, 8) + '…' + install.token.slice(-4))
                          : <span className="text-zinc-600">(missing)</span>}
                      </dd>
                      <dd className="flex items-center gap-1">
                        <button
                          type="button"
                          onClick={() => setRevealToken(v => !v)}
                          className="text-zinc-400 hover:text-zinc-200 p-1"
                          title={revealToken ? 'Hide' : 'Reveal'}
                        >
                          {revealToken ? <EyeOff className="w-3 h-3" /> : <Eye className="w-3 h-3" />}
                        </button>
                        <button
                          type="button"
                          onClick={async () => {
                            if (!install.token) return;
                            await copyToClipboard(install.token);
                            setCopiedKey('token'); setTimeout(() => setCopiedKey(null), 1500);
                          }}
                          className="text-zinc-400 hover:text-zinc-200 p-1"
                          title="Copy token"
                        >
                          {copiedKey === 'token' ? '✓' : <Copy className="w-3 h-3" />}
                        </button>
                      </dd>
                    </dl>

                    <div className="border-t border-white/[0.05] pt-3">
                      {(() => {
                        const currentCmd = installVariant === 'powershell'
                          ? (install.install_command_powershell || install.install_command || '')
                          : (install.install_command_bash || install.install_command || '');
                        const tabBase = 'px-2 py-0.5 text-[10px] uppercase tracking-wider rounded transition-colors';
                        const tabActive = 'bg-white/[0.08] text-zinc-100';
                        const tabIdle = 'text-zinc-500 hover:text-zinc-300';
                        return (
                          <>
                            <div className="flex items-center justify-between mb-1.5">
                              <div className="flex items-center gap-1">
                                <span className="text-[10px] uppercase tracking-wider text-zinc-500 mr-1">Run on the client</span>
                                <button
                                  type="button"
                                  onClick={() => setInstallVariant('bash')}
                                  className={cn(tabBase, installVariant === 'bash' ? tabActive : tabIdle)}
                                  title="macOS / Linux / WSL"
                                >
                                  bash
                                </button>
                                <button
                                  type="button"
                                  onClick={() => setInstallVariant('powershell')}
                                  className={cn(tabBase, installVariant === 'powershell' ? tabActive : tabIdle)}
                                  title="Native Windows PowerShell"
                                >
                                  PowerShell
                                </button>
                              </div>
                              <button
                                type="button"
                                onClick={async () => {
                                  if (!currentCmd) return;
                                  await copyToClipboard(currentCmd);
                                  setCopiedKey('cmd'); setTimeout(() => setCopiedKey(null), 1500);
                                }}
                                className="inline-flex items-center gap-1 text-[11px] text-zinc-400 hover:text-zinc-100 px-2 py-1 rounded border border-white/[0.06] hover:border-white/[0.12]"
                              >
                                {copiedKey === 'cmd'
                                  ? <>✓ Copied</>
                                  : <><Copy className="w-3 h-3" /> Copy</>}
                              </button>
                            </div>
                            <pre className="font-mono text-[11px] text-zinc-300 whitespace-pre-wrap break-words bg-black/40 rounded p-2 max-h-64 overflow-y-auto">
{currentCmd}
                            </pre>
                            <p className="mt-1.5 text-[10px] text-zinc-600 leading-relaxed">
                              {installVariant === 'powershell'
                                ? <>Paste into <strong className="text-zinc-400">PowerShell</strong> on Windows. The script self-elevates (UAC prompt) and installs frpc as a Windows service that auto-starts on boot.</>
                                : <>Paste into a terminal on macOS / Linux / WSL. frpc registers as a launchd / systemd service and connects back here automatically. Re-running with no args reuses the existing config and hot-reloads.</>
                              }
                            </p>
                          </>
                        );
                      })()}
                    </div>
                  </>
                )}
              </div>
            )}
          </div>

          {/* Logs (collapsible) */}
          <div data-id="frp-server-logs-section">
            <button
              type="button"
              onClick={toggleLogs}
              className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-[0.08em] text-zinc-500 hover:text-zinc-300 transition-colors"
            >
              {showLogs ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
              {t('frpServerManagerLogs')}
            </button>
            {showLogs && (
              <div className="mt-2 rounded-lg border border-white/[0.06] bg-black/30 px-3 py-2 text-[11px] font-mono text-zinc-300 whitespace-pre-wrap max-h-64 overflow-y-auto">
                {logs === null
                  ? <span className="text-zinc-500">Loading…</span>
                  : logs || <span className="text-zinc-600">No logs</span>}
              </div>
            )}
          </div>
        </div>
      </aside>
    </div>
  );
}
