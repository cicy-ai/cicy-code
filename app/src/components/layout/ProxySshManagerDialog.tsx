// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from 'react';
import { X, RefreshCw, Play, Square, RotateCw, Activity } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';

type SshProfile = {
  name: string;
  proxy_url: string;
  start_cmd?: string;
  status?: string;
};

type LifecycleAction = 'start' | 'stop' | 'restart';

type TestResult = {
  ok: boolean;
  output?: string;
  error?: string;
  elapsed_ms?: number;
  running?: boolean;
};

export function ProxySshManagerDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation('agentInspector');
  const [profiles, setProfiles] = useState<SshProfile[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [pendingAction, setPendingAction] = useState<{ name: string; action: LifecycleAction } | null>(null);
  const [results, setResults] = useState<Record<string, TestResult>>({});

  const loadList = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await apiService.listProxySsh();
      const data = (resp?.data || {}) as { profiles?: SshProfile[]; detail?: string };
      if (data.detail) {
        setError(data.detail);
        setProfiles(null);
        return;
      }
      setProfiles(data.profiles || []);
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
      setProfiles(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    loadList();
    setResults({});
  }, [open, loadList]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  const runLifecycle = useCallback(async (name: string, action: LifecycleAction) => {
    setPendingAction({ name, action });
    try {
      await apiService.proxySshLifecycle(name, action);
    } catch {
      // failures show up via the refreshed list status
    } finally {
      setPendingAction(null);
      await loadList();
    }
  }, [loadList]);

  const runTest = useCallback(async (name: string) => {
    setResults((prev) => ({ ...prev, [name]: { ok: false, running: true } }));
    try {
      const resp = await apiService.testProxySsh(name);
      const data = (resp?.data || {}) as { success?: boolean; output?: string; error?: string; elapsed_ms?: number };
      setResults((prev) => ({
        ...prev,
        [name]: {
          ok: !!data.success,
          output: data.output,
          error: data.error,
          elapsed_ms: data.elapsed_ms,
          running: false,
        },
      }));
    } catch (e: any) {
      setResults((prev) => ({
        ...prev,
        [name]: { ok: false, error: String(e?.response?.data?.detail || e?.message || e), running: false },
      }));
    }
  }, []);

  if (!open) return null;

  return (
    <div
      data-id="proxy-ssh-drawer-overlay"
      className="fixed inset-0 z-[100000] flex justify-end cursor-pointer"
      onClick={onClose}
    >
      <div data-id="proxy-ssh-drawer-backdrop" className="absolute inset-0 bg-black/55 backdrop-blur-sm" />
      <aside
        data-id="proxy-ssh-drawer"
        className="relative z-10 flex h-full w-[820px] max-w-[96vw] cursor-default flex-col border-l border-white/[0.08] bg-[#0f0f11] shadow-2xl shadow-black/60"
        onClick={(e) => e.stopPropagation()}
      >
        <header data-id="proxy-ssh-drawer-header" className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
          <div data-id="proxy-ssh-drawer-header-titles" className="min-w-0">
            <h2 data-id="proxy-ssh-drawer-title" className="text-[15px] font-semibold text-white">{t('proxySshManagerTitle')}</h2>
            <p data-id="proxy-ssh-drawer-subtitle" className="mt-0.5 text-[11px] text-zinc-600">{t('proxySshManagerSubtitle')}</p>
          </div>
          <div data-id="proxy-ssh-drawer-actions" className="flex items-center gap-1">
            <button
              data-id="proxy-ssh-drawer-refresh"
              type="button"
              onClick={loadList}
              disabled={loading}
              className="rounded-lg border border-white/[0.08] bg-white/[0.04] p-1.5 text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:opacity-40"
              title={t('proxyManagerRefresh')}
            >
              <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            </button>
            <button
              data-id="proxy-ssh-drawer-close"
              type="button"
              onClick={onClose}
              className="rounded-lg p-1.5 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </header>

        <div data-id="proxy-ssh-drawer-body" className="flex-1 overflow-y-auto px-5 py-4">
          {error && (
            <div data-id="proxy-ssh-drawer-error" className="mb-3 rounded-lg border border-red-500/25 bg-red-500/10 px-3 py-2 text-[12px] text-red-200 whitespace-pre-wrap">
              {error}
            </div>
          )}
          {!error && !profiles && loading && (
            <div data-id="proxy-ssh-drawer-loading" className="text-[12px] text-zinc-500">{t('proxyManagerLoading')}</div>
          )}
          {!error && profiles && profiles.length === 0 && (
            <div data-id="proxy-ssh-drawer-empty" className="rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-6 text-center text-[12px] text-zinc-500">
              {t('proxySshManagerEmpty')}
            </div>
          )}
          {!error && profiles && profiles.length > 0 && (
            <table data-id="proxy-ssh-drawer-table" className="w-full table-fixed border-collapse text-[12px]">
              <colgroup>
                <col style={{ width: 140 }} />
                <col style={{ width: 78 }} />
                <col />
                <col style={{ width: 196 }} />
              </colgroup>
              <thead>
                <tr className="text-left text-[10px] uppercase tracking-[0.08em] text-zinc-600">
                  <th className="pb-2">{t('proxyManagerColName')}</th>
                  <th className="pb-2">{t('proxySshManagerColStatus')}</th>
                  <th className="pb-2">{t('proxySshManagerColUrl')}</th>
                  <th className="pb-2 text-right">{t('proxySshManagerColActions')}</th>
                </tr>
              </thead>
              <tbody>
                {profiles.map((p) => {
                  const isRunning = String(p.status || '').toLowerCase() === 'running';
                  const isPendingHere = pendingAction?.name === p.name;
                  const testRes = results[p.name];
                  return (
                    <>
                      <tr key={p.name} className="border-t border-white/[0.04] align-middle">
                        <td className="py-2 pr-2 font-mono text-zinc-200 truncate" title={p.name}>{p.name}</td>
                        <td className="py-2 pr-2">
                          <span className={`inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] ${
                            isRunning ? 'border border-emerald-500/30 bg-emerald-500/10 text-emerald-300'
                              : p.status === 'stopped' ? 'border border-zinc-700 bg-zinc-800/60 text-zinc-400'
                              : 'border border-zinc-800 bg-zinc-900 text-zinc-600'
                          }`}>
                            <span className={`inline-block h-1.5 w-1.5 rounded-full ${isRunning ? 'bg-emerald-400' : 'bg-zinc-500'}`} />
                            {p.status || 'unknown'}
                          </span>
                        </td>
                        <td className="py-2 pr-2 font-mono text-[11px] text-zinc-400 truncate" title={p.proxy_url}>{p.proxy_url || '—'}</td>
                        <td className="py-2 pl-2 text-right">
                          <div className="inline-flex items-center gap-1">
                            <ActionBtn dataId={`proxy-ssh-drawer-start-${p.name}`} icon={<Play size={10} />} label={t('proxyManagerLifecycleStart')}
                              pending={isPendingHere && pendingAction?.action === 'start'} disabled={!!pendingAction || isRunning}
                              onClick={() => runLifecycle(p.name, 'start')} />
                            <ActionBtn dataId={`proxy-ssh-drawer-stop-${p.name}`} icon={<Square size={10} />} label={t('proxyManagerLifecycleStop')}
                              pending={isPendingHere && pendingAction?.action === 'stop'} disabled={!!pendingAction || !isRunning}
                              onClick={() => runLifecycle(p.name, 'stop')} />
                            <ActionBtn dataId={`proxy-ssh-drawer-restart-${p.name}`} icon={<RotateCw size={10} />} label={t('proxyManagerLifecycleRestart')}
                              pending={isPendingHere && pendingAction?.action === 'restart'} disabled={!!pendingAction}
                              onClick={() => runLifecycle(p.name, 'restart')} />
                            <button
                              data-id={`proxy-ssh-drawer-test-${p.name}`}
                              type="button"
                              onClick={() => runTest(p.name)}
                              disabled={!!pendingAction || !!testRes?.running}
                              className="ml-1 inline-flex min-w-[58px] items-center justify-center gap-1 rounded-md border border-indigo-500/30 bg-indigo-500/10 px-2 py-0.5 text-[10px] text-indigo-200 hover:bg-indigo-500/20 disabled:opacity-40"
                            >
                              <Activity size={10} />
                              {testRes?.running ? t('proxyManagerTesting') : t('proxyManagerTest')}
                            </button>
                          </div>
                        </td>
                      </tr>
                      {testRes && !testRes.running && (
                        <tr key={`${p.name}-detail`} className="bg-black/20">
                          <td colSpan={4} className="px-2 py-2">
                            <div data-id={`proxy-ssh-drawer-test-result-${p.name}`} className={`rounded px-2 py-1.5 text-[11px] font-mono whitespace-pre-wrap ${
                              testRes.ok ? 'border border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-200'
                                : 'border border-red-500/25 bg-red-500/[0.07] text-red-200'
                            }`}>
                              <div className="flex items-center justify-between gap-2 text-[10px]">
                                <span>{testRes.ok ? 'ok' : 'failed'}{typeof testRes.elapsed_ms === 'number' ? ` · ${testRes.elapsed_ms} ms` : ''}</span>
                              </div>
                              <div className="mt-1 text-[10px] opacity-90">
                                {testRes.error || testRes.output || ''}
                              </div>
                            </div>
                          </td>
                        </tr>
                      )}
                    </>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      </aside>
    </div>
  );
}

function ActionBtn({ dataId, icon, label, pending, disabled, onClick }: {
  dataId: string;
  icon: React.ReactNode;
  label: string;
  pending: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      data-id={dataId}
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={label}
      className="inline-flex h-6 w-6 items-center justify-center rounded border border-white/[0.08] bg-white/[0.03] text-zinc-300 transition-colors hover:bg-white/[0.07] disabled:opacity-30"
    >
      {pending ? <RefreshCw size={10} className="animate-spin" /> : icon}
    </button>
  );
}
