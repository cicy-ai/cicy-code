// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from 'react';
import { X, RefreshCw, Play, Square, RotateCw, RefreshCcw, Sparkles, Globe, Copy, Check, AlertTriangle, ChevronDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import Select from '../ui/Select';
import { sendToAgent as dispatchToAgent } from '../../services/agentSend';
import { useApp } from '../../contexts/AppContext';

type ProxyEntry = {
  name: string;
  type: string;
  last_delay_ms?: number;
  now?: string;
  members?: string[];
};

type ProxyList = {
  groups: ProxyEntry[];
  nodes: ProxyEntry[];
};

type MihomoStatus = {
  running: boolean;
  pid: string;
  binary: string;
  config: string;
  log: string;
  started_at: string;
  controller: string;
  version: string;
};

type LifecycleAction = 'start' | 'stop' | 'restart' | 'reload';

type DelayRow = {
  url: string;
  ok: boolean;
  delay_ms?: number;
  error?: string;
};

type IPResult = {
  ok: boolean;
  ip?: string;
  country?: string;
  cc?: string;
  raw?: string;
  error?: string;
};

type TestResult = {
  results: DelayRow[];
  ip?: IPResult;        // exit IP via the proxy node
  ipDirect?: IPResult;  // exit IP WITHOUT proxy (direct) — for proxy-vs-direct compare
  running?: boolean;
};

// Fixed probe columns shown in the table. Order matters — must match what
// /api/proxy/test returns when no `urls` body field is provided. Keep the
// labels short; the table is dense.
const PROBE_COLUMNS: Array<{ url: string; short: string }> = [
  { url: 'https://api.anthropic.com', short: 'anthropic' },
  { url: 'https://chatgpt.com', short: 'chatgpt' },
  { url: 'https://www.cloudflare.com', short: 'cloudflare' },
];

export function ProxyManagerDialog({
  open,
  onClose,
  paneId,
}: {
  open: boolean;
  onClose: () => void;
  paneId?: string;
}) {
  const { t } = useTranslation('agentInspector');
  const { activeAgentId } = useApp();
  const [list, setList] = useState<ProxyList | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>('');
  const [results, setResults] = useState<Record<string, TestResult>>({});
  const [testingAll, setTestingAll] = useState(false);
  // Row interactions: a node row expands to show its SOURCE yaml config; a group
  // row's "now" cell is a dropdown that switches the active member.
  const [expandedNode, setExpandedNode] = useState<string>('');
  const [switchingGroup, setSwitchingGroup] = useState<string>('');
  const [nodeConfigs, setNodeConfigs] = useState<Record<string, { loading: boolean; yaml?: string; error?: string }>>({});
  const [status, setStatus] = useState<MihomoStatus | null>(null);
  const [pendingAction, setPendingAction] = useState<LifecycleAction | null>(null);
  const [lifecycleOutput, setLifecycleOutput] = useState<string>('');
  type AskKind = 'proxy' | 'group' | 'user';
  const [askAgentSending, setAskAgentSending] = useState<AskKind | ''>('');
  const [askAgentResult, setAskAgentResult] = useState<'' | 'sent' | 'failed'>('');
  const [askAgentError, setAskAgentError] = useState<string>('');
  const [allowLan, setAllowLan] = useState<boolean | null>(null);
  const [resetConfirm, setResetConfirm] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [resetMsg, setResetMsg] = useState<string>('');
  const [allowLanPending, setAllowLanPending] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [exportMode, setExportMode] = useState<'local' | 'lan' | 'public'>('local');
  const [exportLoading, setExportLoading] = useState(false);
  const [exportScript, setExportScript] = useState<string>('');
  const [exportHost, setExportHost] = useState<string>('');
  const [exportWarning, setExportWarning] = useState<string>('');
  const [exportCopied, setExportCopied] = useState(false);

  const loadStatus = useCallback(async () => {
    try {
      const resp = await apiService.getProxyStatus();
      const data = (resp?.data || {}) as Partial<MihomoStatus> & { detail?: string; success?: boolean };
      if (data?.success) {
        setStatus({
          running: !!data.running,
          pid: String(data.pid || ''),
          binary: String(data.binary || ''),
          config: String(data.config || ''),
          log: String(data.log || ''),
          started_at: String(data.started_at || ''),
          controller: String(data.controller || ''),
          version: String(data.version || ''),
        });
      }
    } catch {
      setStatus(null);
    }
  }, []);

  const loadList = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await apiService.getProxyList();
      const data = (resp?.data || {}) as Partial<ProxyList> & { detail?: string };
      if (data.detail) {
        setError(data.detail);
        setList(null);
      } else {
        setList({ groups: data.groups || [], nodes: data.nodes || [] });
      }
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
      setList(null);
    } finally {
      setLoading(false);
    }
  }, []);

  const loadBindMode = useCallback(async () => {
    try {
      const resp = await apiService.getProxyBindMode();
      const data = (resp?.data || {}) as { allow_lan?: boolean };
      setAllowLan(!!data.allow_lan);
    } catch {
      setAllowLan(null);
    }
  }, []);

  const loadExport = useCallback(async (mode: 'local' | 'lan' | 'public') => {
    setExportLoading(true);
    setExportCopied(false);
    try {
      // Default the export's username to Workspace.activeCliPaneId (read out
      // of AppContext as `activeAgentId` — already normalized to short form
      // like "w-1001"). That worker name matches the `IN-USER-PREFIX,w-`
      // routing rule, so the export actually routes out of the box. Fall
      // back to the dialog's own paneId prop, then to whatever the server
      // defaults to.
      const user = String(activeAgentId || paneId || '').replace(/:.*$/, '') || undefined;
      const resp = await apiService.getProxyExport({ ip: mode, user });
      const data = (resp?.data || {}) as { script?: string; host?: string; warning?: string };
      setExportScript(String(data.script || ''));
      setExportHost(String(data.host || ''));
      setExportWarning(String(data.warning || ''));
    } catch (e: any) {
      setExportScript(`# error: ${String(e?.response?.data?.detail || e?.message || e)}`);
      setExportHost('');
      setExportWarning('');
    } finally {
      setExportLoading(false);
    }
  }, [activeAgentId, paneId]);

  useEffect(() => {
    if (!open) return;
    loadStatus();
    loadList();
    loadBindMode();
    setResults({});
    setLifecycleOutput('');
    setExportOpen(false);
  }, [open, loadList, loadStatus, loadBindMode]);

  useEffect(() => {
    if (!open || !exportOpen) return;
    void loadExport(exportMode);
  }, [open, exportOpen, exportMode, loadExport]);

  const toggleAllowLan = useCallback(async (next: boolean) => {
    setAllowLanPending(true);
    try {
      await apiService.setProxyBindMode(next);
      setAllowLan(next);
    } catch {
      // keep previous state on failure
    } finally {
      setAllowLanPending(false);
      await loadStatus();
    }
  }, [loadStatus]);

  const copyExport = useCallback(async () => {
    if (!exportScript) return;
    try {
      await navigator.clipboard.writeText(exportScript);
      setExportCopied(true);
      window.setTimeout(() => setExportCopied(false), 1500);
    } catch {
      // ignore — older browsers without clipboard permission
    }
  }, [exportScript]);

  const runLifecycle = useCallback(async (action: LifecycleAction) => {
    setPendingAction(action);
    setLifecycleOutput('');
    try {
      const resp = await apiService.proxyLifecycle(action);
      const data = (resp?.data || {}) as { success?: boolean; output?: string; error?: string };
      setLifecycleOutput(String(data.output || data.error || ''));
    } catch (e: any) {
      setLifecycleOutput(String(e?.response?.data?.detail || e?.response?.data?.error || e?.message || e));
    } finally {
      setPendingAction(null);
      // Status almost always changes after start/stop/restart, and reload
      // may swap pid/started_at — refetch unconditionally. Proxies list can
      // also change after reload (yaml edits), so refresh both.
      await loadStatus();
      await loadList();
    }
  }, [loadList, loadStatus]);

  const runTest = useCallback(async (name: string) => {
    setResults((prev) => ({
      ...prev,
      [name]: { ...(prev[name] || { results: [] }), running: true },
    }));
    try {
      const resp = await apiService.testProxy(name);
      const data = (resp?.data || {}) as { results?: DelayRow[]; ip?: IPResult; ip_direct?: IPResult };
      setResults((prev) => ({
        ...prev,
        [name]: {
          results: data.results || [],
          ip: data.ip,
          ipDirect: data.ip_direct,
          running: false,
        },
      }));
    } catch (e: any) {
      setResults((prev) => ({
        ...prev,
        [name]: {
          results: [],
          ip: { ok: false, error: String(e?.response?.data?.detail || e?.message || e) },
          running: false,
        },
      }));
    }
  }, []);

  // Switch a group's active member via mihomo's selector, then refresh the list
  // (so `now` updates) and re-probe the group's new exit.
  const handleSelectNode = useCallback(async (group: string, member: string) => {
    setSwitchingGroup(group);
    try {
      await apiService.selectProxy(member, group);
      await loadList();
      void runTest(group);
    } catch (e: any) {
      setError(String(e?.response?.data?.detail || e?.message || e));
    } finally {
      setSwitchingGroup('');
    }
  }, [loadList, runTest]);

  // Fetch a node's SOURCE yaml config (from mihomo.yaml) for the detail panel.
  const fetchNodeConfig = useCallback(async (name: string) => {
    setNodeConfigs((prev) => ({ ...prev, [name]: { ...(prev[name] || {}), loading: true } }));
    try {
      const resp = await apiService.getProxyNodeConfig(name);
      const data = (resp?.data || {}) as { success?: boolean; yaml?: string; detail?: string };
      setNodeConfigs((prev) => ({
        ...prev,
        [name]: data.success
          ? { loading: false, yaml: String(data.yaml || '') }
          : { loading: false, error: String(data.detail || 'not found') },
      }));
    } catch (e: any) {
      setNodeConfigs((prev) => ({ ...prev, [name]: { loading: false, error: String(e?.response?.data?.detail || e?.message || e) } }));
    }
  }, []);

  // Toggle a node's detail panel; fetch its source yaml on open.
  const handleToggleExpand = useCallback((name: string) => {
    setExpandedNode((prev) => {
      const next = prev === name ? '' : name;
      if (next) void fetchNodeConfig(name);
      return next;
    });
  }, [fetchNodeConfig]);

  // Route through the globally selected Team/Project target. A missing target
  // leaves the drawer open; sendToAgent owns the "未选中 Agent" toast.
  const sendToAgent = useCallback(async (kind: AskKind, prompt: string) => {
    const target = String(paneId || '').trim();
    setAskAgentSending(kind);
    setAskAgentResult('');
    setAskAgentError('');
    let resp: any = null;
    let errorMessage = '';
    try {
      const handled = await dispatchToAgent(target, prompt, { submit: false });
      if (!handled) return;
    } catch (e: any) {
      errorMessage = String(e?.response?.data?.detail || e?.message || e);
    } finally {
      setAskAgentSending('');
    }
    const warning = String(resp?.data?.warning || '').trim();
    if (errorMessage) {
      setAskAgentResult('failed');
      setAskAgentError(errorMessage);
    } else if (warning) {
      setAskAgentResult('sent');
      setAskAgentError(warning);
    } else {
      setAskAgentResult('sent');
    }
    window.setTimeout(() => onClose(), 400);
  }, [paneId, onClose]);

  // Single "let the agent manage my proxy" button. Team targets send now;
  // Project targets receive the prompt in their footer for review.
  const askManageProxy = useCallback(() => {
    const prompt = '请帮我用 cicy-mihomo skill 帮我来管理我的本机的 proxy，配置文件在 ~/cicy-ai/db/mihomo.yaml';
    return sendToAgent('proxy', prompt);
  }, [sendToAgent]);

  // Restore-default: backend backs up the current mihomo.yaml then regenerates
  // the default template (cicy-mihomo gen-config --force) and reloads.
  const doResetConfig = useCallback(async () => {
    setResetting(true);
    setResetMsg('');
    try {
      const resp = await apiService.resetProxyConfig();
      const backup = String((resp as any)?.data?.backup || '');
      setResetMsg(backup ? `${t('proxyManagerResetDone')}（backup: ${backup}）` : t('proxyManagerResetDone'));
      setResetConfirm(false);
      await loadList();
    } catch (e: any) {
      setResetMsg(String(e?.response?.data?.detail || e?.message || e));
    } finally {
      setResetting(false);
    }
  }, [loadList, t]);

  const runAll = useCallback(async () => {
    if (!list) return;
    setTestingAll(true);
    try {
      // Sequential, not parallel — exit-IP probe mutates default_proxy_group's
      // selection and parallel runs would race.
      for (const entry of [...list.groups, ...list.nodes]) {
        await runTest(entry.name);
      }
    } finally {
      setTestingAll(false);
    }
  }, [list, runTest]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [open, onClose]);

  if (!open) return null;

  const entries: Array<{ entry: ProxyEntry; kind: 'group' | 'node' }> = list
    ? [
        ...list.groups.map((g) => ({ entry: g, kind: 'group' as const })),
        ...list.nodes.map((n) => ({ entry: n, kind: 'node' as const })),
      ]
    : [];

  return (
    <div
      data-id="proxy-manager-drawer-overlay"
      className="fixed inset-0 z-[100000] flex justify-end cursor-pointer"
      onClick={onClose}
    >
      <div data-id="proxy-manager-drawer-backdrop" className="absolute inset-0 bg-black/55 backdrop-blur-sm" />
      <aside
        data-id="proxy-manager-drawer"
        className="relative z-10 flex h-full w-[920px] max-w-[96vw] cursor-default flex-col border-l border-white/[0.08] bg-[#0f0f11] shadow-2xl shadow-black/60"
        onClick={(e) => e.stopPropagation()}
      >
        <header data-id="proxy-manager-drawer-header" className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
          <div data-id="proxy-manager-drawer-header-titles" className="min-w-0">
            <h2 data-id="proxy-manager-drawer-title" className="text-[15px] font-semibold text-white">{t('proxyManagerTitle')}</h2>
            <p data-id="proxy-manager-drawer-subtitle" className="mt-0.5 text-[11px] text-zinc-600">{t('proxyManagerSubtitle')}</p>
          </div>
          <div data-id="proxy-manager-drawer-actions" className="flex items-center gap-1">
            <button
              data-id="proxy-manager-drawer-reset-config"
              type="button"
              onClick={() => { setResetMsg(''); setResetConfirm(true); }}
              disabled={loading || resetting}
              className="rounded-lg border border-red-900/40 bg-red-950/30 px-2.5 py-1 text-[11px] text-red-300/90 transition-colors hover:bg-red-900/30 disabled:opacity-40"
            >
              {t('proxyManagerResetDefault')}
            </button>
            <button
              data-id="proxy-manager-drawer-test-all"
              type="button"
              onClick={runAll}
              disabled={!list || testingAll || loading}
              className="rounded-lg border border-white/[0.08] bg-white/[0.04] px-2.5 py-1 text-[11px] text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:opacity-40"
            >
              {testingAll ? t('proxyManagerTestingAll') : t('proxyManagerTestAll')}
            </button>
            <button
              data-id="proxy-manager-drawer-refresh"
              type="button"
              onClick={loadList}
              disabled={loading}
              className="rounded-lg p-1.5 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-300 disabled:opacity-40"
              title={t('proxyManagerRefresh')}
            >
              <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
            </button>
            <button
              data-id="proxy-manager-drawer-close"
              type="button"
              onClick={onClose}
              className="rounded-lg p-1.5 text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </header>

        {resetConfirm && (
          <div data-id="proxy-manager-reset-modal" className="fixed inset-0 z-[200] flex items-center justify-center p-4">
            <div className="absolute inset-0 bg-black/60" onClick={() => { if (!resetting) setResetConfirm(false); }} />
            <div className="relative w-full max-w-sm rounded-xl border border-red-900/50 bg-[#141416] p-5 shadow-2xl">
              <div className="flex items-start gap-2">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-red-400" />
                <div className="min-w-0">
                  <h3 data-id="proxy-manager-reset-modal-title" className="text-[14px] font-semibold text-white">{t('proxyManagerResetTitle')}</h3>
                  <p className="mt-1 text-[12px] leading-relaxed text-zinc-400">{t('proxyManagerResetConfirm')}</p>
                  {resetMsg && <p data-id="proxy-manager-reset-modal-msg" className="mt-2 break-all text-[11px] text-amber-400">{resetMsg}</p>}
                </div>
              </div>
              <div className="mt-4 flex justify-end gap-2">
                <button
                  type="button"
                  onClick={() => setResetConfirm(false)}
                  disabled={resetting}
                  className="rounded-lg border border-white/[0.08] bg-white/[0.04] px-3 py-1.5 text-[12px] text-zinc-300 hover:bg-white/[0.08] disabled:opacity-40"
                >
                  {t('proxyManagerResetCancel')}
                </button>
                <button
                  data-id="proxy-manager-reset-confirm-button"
                  type="button"
                  onClick={doResetConfig}
                  disabled={resetting}
                  className="rounded-lg border border-red-700/50 bg-red-800/40 px-3 py-1.5 text-[12px] font-medium text-red-100 hover:bg-red-700/50 disabled:opacity-40"
                >
                  {resetting ? t('proxyManagerResetting') : t('proxyManagerResetOk')}
                </button>
              </div>
            </div>
          </div>
        )}

        <div data-id="proxy-manager-drawer-lifecycle" className="border-b border-white/[0.06] bg-[#0c0c0e] px-5 py-3">
          <div data-id="proxy-manager-drawer-lifecycle-row" className="flex flex-wrap items-center gap-2 text-[12px]">
            <span
              data-id="proxy-manager-drawer-lifecycle-status"
              className={`inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-[11px] ${
                status?.running
                  ? 'border border-emerald-500/30 bg-emerald-500/10 text-emerald-300'
                  : status
                    ? 'border border-zinc-700 bg-zinc-800/60 text-zinc-400'
                    : 'border border-zinc-800 bg-zinc-900 text-zinc-600'
              }`}
            >
              <span
                data-id="proxy-manager-drawer-lifecycle-status-dot"
                className={`inline-block h-1.5 w-1.5 rounded-full ${status?.running ? 'bg-emerald-400' : 'bg-zinc-500'}`}
              />
              {status?.running ? t('proxyManagerLifecycleRunning') : status ? t('proxyManagerLifecycleStopped') : t('proxyManagerLifecycleUnknown')}
            </span>
            {status?.running && status?.pid && (
              <span data-id="proxy-manager-drawer-lifecycle-pid" className="font-mono text-[11px] text-zinc-500">pid {status.pid}</span>
            )}
            <div data-id="proxy-manager-drawer-lifecycle-actions" className="ml-auto flex items-center gap-1">
              {/* running → stop/restart/reload; stopped → start only */}
              {!status?.running && (
                <LifecycleButton dataId="proxy-manager-drawer-lifecycle-start"  action="start"   icon={<Play size={11} />}      label={t('proxyManagerLifecycleStart')}   pending={pendingAction === 'start'}   disabled={!!pendingAction} onRun={runLifecycle} />
              )}
              {status?.running && (
                <>
                  <LifecycleButton dataId="proxy-manager-drawer-lifecycle-stop"   action="stop"    icon={<Square size={11} />}    label={t('proxyManagerLifecycleStop')}    pending={pendingAction === 'stop'}    disabled={!!pendingAction} onRun={runLifecycle} />
                  <LifecycleButton dataId="proxy-manager-drawer-lifecycle-restart" action="restart" icon={<RotateCw size={11} />}  label={t('proxyManagerLifecycleRestart')} pending={pendingAction === 'restart'} disabled={!!pendingAction} onRun={runLifecycle} />
                  <LifecycleButton dataId="proxy-manager-drawer-lifecycle-reload" action="reload"  icon={<RefreshCcw size={11} />} label={t('proxyManagerLifecycleReload')}  pending={pendingAction === 'reload'}  disabled={!!pendingAction} onRun={runLifecycle} />
                </>
              )}
            </div>
          </div>
          {lifecycleOutput && (
            <pre data-id="proxy-manager-drawer-lifecycle-output" className="mt-2 max-h-24 overflow-auto rounded-md bg-black/30 px-2 py-1.5 font-mono text-[10px] leading-relaxed text-zinc-400 whitespace-pre-wrap">{lifecycleOutput}</pre>
          )}
          <div data-id="proxy-manager-drawer-bind-row" className="mt-2 flex flex-wrap items-center gap-3 text-[11px]">
            <label data-id="proxy-manager-drawer-allow-lan" className="inline-flex items-center gap-2 text-zinc-300 cursor-pointer">
              <input
                type="checkbox"
                data-id="proxy-manager-drawer-allow-lan-input"
                checked={!!allowLan}
                disabled={allowLan === null || allowLanPending}
                onChange={(e) => toggleAllowLan(e.target.checked)}
                className="h-3 w-3 cursor-pointer accent-emerald-500 disabled:opacity-40"
              />
              <span data-id="proxy-manager-drawer-allow-lan-label">{t('proxyManagerAllowLan')}</span>
              <span data-id="proxy-manager-drawer-allow-lan-hint" className="font-mono text-zinc-500">
                {allowLan === null ? '…' : allowLan ? '0.0.0.0' : '127.0.0.1'}
              </span>
              {allowLanPending && <RefreshCw size={10} className="animate-spin text-zinc-500" />}
            </label>
            <button
              type="button"
              data-id="proxy-manager-drawer-show-export"
              onClick={() => setExportOpen((v) => !v)}
              className="ml-auto inline-flex items-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.03] px-2.5 py-1 text-zinc-200 transition-colors hover:bg-white/[0.07]"
            >
              <Globe size={11} />
              {exportOpen ? t('proxyManagerExportHide') : t('proxyManagerExportShow')}
            </button>
          </div>
          {exportOpen && (
            <div data-id="proxy-manager-drawer-export" className="mt-2 rounded-lg border border-white/[0.06] bg-black/30 p-2">
              <div data-id="proxy-manager-drawer-export-modes" className="flex flex-wrap items-center gap-1.5 text-[11px]">
                {([
                  { id: 'local', label: t('proxyManagerExportModeLocal') },
                  { id: 'lan', label: t('proxyManagerExportModeLan') },
                  { id: 'public', label: t('proxyManagerExportModePublic') },
                ] as Array<{ id: 'local' | 'lan' | 'public'; label: string }>).map((m) => (
                  <button
                    key={m.id}
                    type="button"
                    data-id={`proxy-manager-drawer-export-mode-${m.id}`}
                    onClick={() => setExportMode(m.id)}
                    className={`rounded-md px-2 py-0.5 transition-colors ${
                      exportMode === m.id
                        ? 'border border-emerald-500/40 bg-emerald-500/10 text-emerald-200'
                        : 'border border-white/[0.06] bg-white/[0.02] text-zinc-400 hover:bg-white/[0.06]'
                    }`}
                  >
                    {m.label}
                  </button>
                ))}
                {exportHost && (
                  <span data-id="proxy-manager-drawer-export-host" className="font-mono text-[10px] text-zinc-500">{exportHost}</span>
                )}
                <button
                  type="button"
                  data-id="proxy-manager-drawer-export-copy"
                  onClick={copyExport}
                  disabled={!exportScript || exportLoading || !!exportWarning}
                  className="ml-auto inline-flex items-center gap-1 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-0.5 text-zinc-200 transition-colors hover:bg-white/[0.07] disabled:opacity-40"
                >
                  {exportCopied ? <Check size={10} className="text-emerald-300" /> : <Copy size={10} />}
                  {exportCopied ? t('proxyManagerExportCopied') : t('proxyManagerExportCopy')}
                </button>
              </div>
              {exportWarning && (
                <div data-id="proxy-manager-drawer-export-warning" className="mt-2 flex items-start gap-1.5 rounded-md border border-amber-500/20 bg-amber-500/[0.08] px-2 py-1.5 text-[10px] leading-relaxed text-amber-200">
                  <AlertTriangle size={11} className="mt-0.5 shrink-0" />
                  <span>{t('proxyManagerExportAuthMissing')}</span>
                </div>
              )}
              <pre data-id="proxy-manager-drawer-export-script" className="mt-2 max-h-48 overflow-auto rounded-md bg-black/40 px-2 py-1.5 font-mono text-[10px] leading-relaxed text-zinc-300 whitespace-pre">
                {exportLoading ? t('proxyManagerLoading') : exportWarning ? '—' : exportScript || '—'}
              </pre>
            </div>
          )}
          <div data-id="proxy-manager-drawer-ask-agent-row" className="mt-2 flex flex-wrap items-center gap-1.5 text-[11px]">
            <button
              data-id="proxy-manager-drawer-ask-add-proxy"
              type="button"
              onClick={askManageProxy}
              disabled={!!askAgentSending}
              className="inline-flex items-center gap-1.5 rounded-md border border-indigo-500/30 bg-indigo-500/10 px-2.5 py-1 text-zinc-100 transition-colors hover:bg-indigo-500/20 disabled:opacity-40"
            >
              <Sparkles size={11} />
              {askAgentSending ? t('proxyManagerAskAgentSending') : t('proxyManagerManageProxy')}
            </button>
            {askAgentResult === 'sent' && (
              <span data-id="proxy-manager-drawer-ask-agent-ok" className="text-emerald-400">{t('proxyManagerAskAgentSent')}</span>
            )}
            {askAgentResult === 'failed' && (
              <span data-id="proxy-manager-drawer-ask-agent-err" className="text-red-400" title={askAgentError}>{t('proxyManagerAskAgentFailed')}</span>
            )}
            <span data-id="proxy-manager-drawer-ask-agent-hint" className="text-zinc-600">{t('proxyManagerAskAgentHint')}</span>
          </div>
        </div>

        <div data-id="proxy-manager-drawer-body" className="flex-1 overflow-y-auto px-5 py-4">
          {error && (
            <div data-id="proxy-manager-drawer-error" className="mb-3 rounded-lg border border-red-500/25 bg-red-500/10 px-3 py-2 text-[12px] text-red-200">
              {error}
            </div>
          )}
          {!error && !list && loading && (
            <div data-id="proxy-manager-drawer-loading" className="rounded-lg border border-dashed border-white/[0.08] px-4 py-8 text-center text-sm text-zinc-600">
              {t('proxyManagerLoading')}
            </div>
          )}
          {list && entries.length === 0 && (
            <div data-id="proxy-manager-drawer-empty" className="rounded-lg border border-dashed border-white/[0.08] px-4 py-8 text-center text-sm text-zinc-600">
              {t('proxyManagerEmpty')}
            </div>
          )}
          {list && entries.length > 0 && (
            <table data-id="proxy-manager-table" className="w-full table-fixed border-separate border-spacing-0 text-[12px]">
              <colgroup data-id="proxy-manager-table-colgroup">
                <col data-id="proxy-manager-col-name" />
                <col data-id="proxy-manager-col-kind" className="w-[52px]" />
                <col data-id="proxy-manager-col-type" className="w-[72px]" />
                <col data-id="proxy-manager-col-now" className="w-[120px]" />
                {PROBE_COLUMNS.map((col) => (
                  <col key={col.url} data-id={`proxy-manager-col-probe-${col.short}`} className="w-[72px]" />
                ))}
                <col data-id="proxy-manager-col-ip" className="w-[160px]" />
                <col data-id="proxy-manager-col-action" className="w-[80px]" />
              </colgroup>
              <thead data-id="proxy-manager-table-head" className="text-[10px] uppercase tracking-[0.12em] text-zinc-500">
                <tr data-id="proxy-manager-table-head-row">
                  <th data-id="proxy-manager-table-head-name" className="sticky top-0 z-10 border-b border-white/[0.08] bg-[#0f0f11] px-2 py-2 text-left">{t('proxyManagerColName')}</th>
                  <th data-id="proxy-manager-table-head-kind" className="sticky top-0 z-10 border-b border-white/[0.08] bg-[#0f0f11] px-2 py-2 text-left">{t('proxyManagerColKind')}</th>
                  <th data-id="proxy-manager-table-head-type" className="sticky top-0 z-10 border-b border-white/[0.08] bg-[#0f0f11] px-2 py-2 text-left">{t('proxyManagerColType')}</th>
                  <th data-id="proxy-manager-table-head-now" className="sticky top-0 z-10 border-b border-white/[0.08] bg-[#0f0f11] px-2 py-2 text-left">{t('proxyManagerColNow')}</th>
                  {PROBE_COLUMNS.map((col) => (
                    <th
                      key={col.url}
                      data-id={`proxy-manager-table-head-probe-${col.short}`}
                      className="sticky top-0 z-10 border-b border-white/[0.08] bg-[#0f0f11] px-2 py-2 text-right"
                    >
                      {col.short}
                    </th>
                  ))}
                  <th data-id="proxy-manager-table-head-ip" className="sticky top-0 z-10 border-b border-white/[0.08] bg-[#0f0f11] px-2 py-2 text-left">{t('proxyManagerColIP')}</th>
                  <th data-id="proxy-manager-table-head-action" className="sticky top-0 z-10 border-b border-white/[0.08] bg-[#0f0f11] px-2 py-2 text-right" />
                </tr>
              </thead>
              <tbody data-id="proxy-manager-table-body">
                {entries.map(({ entry, kind }) => (
                  <ProxyTableRow
                    key={entry.name}
                    entry={entry}
                    kind={kind}
                    result={results[entry.name]}
                    onTest={() => runTest(entry.name)}
                    expanded={expandedNode === entry.name}
                    onToggleExpand={() => handleToggleExpand(entry.name)}
                    onSelectNode={handleSelectNode}
                    switching={switchingGroup === entry.name}
                    nodeConfig={nodeConfigs[entry.name]}
                  />
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div
          data-id="proxy-manager-drawer-config-alert"
          className="flex items-start gap-2 border-t border-red-900/50 bg-red-950/40 px-5 py-2.5 text-[11px] text-red-300/90"
        >
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-red-400/90" />
          <div className="min-w-0">
            <div>
              {t('proxyManagerConfigLabel')}：
              <code data-id="proxy-manager-drawer-config-path" className="select-text break-all font-mono text-red-200">
                {status?.config || '~/cicy-ai/db/mihomo.yaml'}
              </code>
            </div>
            <div data-id="proxy-manager-drawer-config-warn" className="mt-0.5 text-red-400/90">{t('proxyManagerConfigWarn')}</div>
            {resetMsg && !resetConfirm && (
              <div data-id="proxy-manager-drawer-reset-msg" className="mt-1 break-all text-emerald-400/90">{resetMsg}</div>
            )}
          </div>
        </div>
      </aside>
    </div>
  );
}

function LifecycleButton({
  dataId,
  action,
  icon,
  label,
  pending,
  disabled,
  onRun,
}: {
  dataId: string;
  action: LifecycleAction;
  icon: React.ReactNode;
  label: string;
  pending: boolean;
  disabled: boolean;
  onRun: (action: LifecycleAction) => void;
}) {
  return (
    <button
      data-id={dataId}
      type="button"
      onClick={() => onRun(action)}
      disabled={disabled || pending}
      className="inline-flex items-center gap-1 rounded-md border border-white/[0.08] bg-white/[0.04] px-2 py-0.5 text-[11px] text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:opacity-40"
    >
      {pending ? <RefreshCw className="h-3 w-3 animate-spin" /> : icon}
      {label}
    </button>
  );
}

function ProxyTableRow({
  entry,
  kind,
  result,
  onTest,
  expanded,
  onToggleExpand,
  onSelectNode,
  switching,
  nodeConfig,
}: {
  entry: ProxyEntry;
  kind: 'group' | 'node';
  result?: TestResult;
  onTest: () => void;
  expanded: boolean;
  onToggleExpand: () => void;
  onSelectNode: (group: string, member: string) => void;
  switching: boolean;
  nodeConfig?: { loading: boolean; yaml?: string; error?: string };
}) {
  const { t } = useTranslation('agentInspector');
  // Map result rows by url for column lookup.
  const byURL: Record<string, DelayRow> = {};
  if (result?.results) {
    for (const r of result.results) byURL[r.url] = r;
  }
  const isNode = kind === 'node';
  const totalCols = 6 + PROBE_COLUMNS.length;
  return (
    <>
    <tr data-id={`proxy-manager-row-${entry.name}`} className={`text-zinc-300 ${expanded ? 'bg-white/[0.03]' : ''}`}>
      <td data-id={`proxy-manager-cell-${entry.name}-name`} className="border-b border-white/[0.04] px-2 py-2 align-top">
        {isNode ? (
          <button
            type="button"
            data-id={`proxy-manager-cell-${entry.name}-name-toggle`}
            onClick={onToggleExpand}
            title={t('proxyManagerNodeDetail')}
            className="flex w-full items-center gap-1 text-left"
          >
            <ChevronDown className={`h-3 w-3 shrink-0 text-zinc-600 transition-transform ${expanded ? 'rotate-180 text-zinc-300' : '-rotate-90'}`} />
            <span data-id={`proxy-manager-cell-${entry.name}-name-text`} className="truncate font-medium text-zinc-100">{entry.name}</span>
          </button>
        ) : (
          <>
            <div data-id={`proxy-manager-cell-${entry.name}-name-text`} className="truncate font-medium text-zinc-100 select-text">{entry.name}</div>
            {entry.members && entry.members.length > 0 && (
              <div data-id={`proxy-manager-cell-${entry.name}-members`} className="mt-0.5 text-[10px] text-zinc-600">{entry.members.length} {t('proxyManagerMembers')}</div>
            )}
          </>
        )}
      </td>
      <td data-id={`proxy-manager-cell-${entry.name}-kind`} className="border-b border-white/[0.04] px-2 py-2 align-top text-zinc-500">
        {kind === 'group' ? t('proxyManagerKindGroup') : t('proxyManagerKindNode')}
      </td>
      <td data-id={`proxy-manager-cell-${entry.name}-type`} className="border-b border-white/[0.04] px-2 py-2 align-top text-zinc-500">{entry.type}</td>
      <td data-id={`proxy-manager-cell-${entry.name}-now`} className="border-b border-white/[0.04] px-2 py-2 align-top text-zinc-400">
        {kind === 'group' ? (
          entry.members && entry.members.length > 0 ? (
            <div data-id={`proxy-manager-cell-${entry.name}-now-select-wrap`} className="flex items-center gap-1.5">
              <Select
                dataId={`proxy-manager-cell-${entry.name}-now-select`}
                className="max-w-[160px]"
                compact
                searchable
                disabled={switching}
                value={entry.now || ''}
                onChange={(v) => { if (v && v !== entry.now) onSelectNode(entry.name, v); }}
                options={[
                  ...(!entry.members.includes(entry.now || '') && entry.now ? [{ value: entry.now, label: entry.now }] : []),
                  ...entry.members.map((m) => ({ value: m, label: m })),
                ]}
              />
              {switching ? <RefreshCw data-id={`proxy-manager-cell-${entry.name}-switching`} className="h-3 w-3 animate-spin text-zinc-500" /> : null}
            </div>
          ) : (
            <span data-id={`proxy-manager-cell-${entry.name}-now-text`} className="select-text">{entry.now || '-'}</span>
          )
        ) : <span data-id={`proxy-manager-cell-${entry.name}-now-na`} className="text-zinc-700">-</span>}
      </td>
      {PROBE_COLUMNS.map((col) => {
        const row = byURL[col.url];
        return (
          <td
            key={col.url}
            data-id={`proxy-manager-cell-${entry.name}-probe-${col.short}`}
            className="border-b border-white/[0.04] px-2 py-2 text-right align-top font-mono"
          >
            {result?.running && !row ? (
              <span data-id={`proxy-manager-cell-${entry.name}-probe-${col.short}-pending`} className="text-zinc-700">...</span>
            ) : row ? (
              row.ok ? (
                <span data-id={`proxy-manager-cell-${entry.name}-probe-${col.short}-ok`} className="text-emerald-400">{row.delay_ms} ms</span>
              ) : (
                <span data-id={`proxy-manager-cell-${entry.name}-probe-${col.short}-fail`} className="text-red-400" title={row.error}>fail</span>
              )
            ) : (
              <span data-id={`proxy-manager-cell-${entry.name}-probe-${col.short}-empty`} className="text-zinc-700">-</span>
            )}
          </td>
        );
      })}
      <td data-id={`proxy-manager-cell-${entry.name}-ip`} className="border-b border-white/[0.04] px-2 py-2 align-top font-mono">
        {(() => {
          if (result?.running && !result?.ip) {
            return <span data-id={`proxy-manager-cell-${entry.name}-ip-pending`} className="text-zinc-700">...</span>;
          }
          if (!result?.ip) {
            return <span data-id={`proxy-manager-cell-${entry.name}-ip-empty`} className="text-zinc-700">-</span>;
          }
          const proxy = result.ip;
          const direct = result.ipDirect;
          if (!(proxy.ok && proxy.ip)) {
            return <span data-id={`proxy-manager-cell-${entry.name}-ip-fail`} className="text-red-400" title={proxy.error}>fail</span>;
          }
          // Plain IP text (no external link); title carries the full IP so hover
          // always reveals it even when the cell truncates.
          const ipLink = (res: IPResult, slot: string, label?: string) => (
            <span
              data-id={`proxy-manager-cell-${entry.name}-ip-${slot}`}
              title={res.ip}
              className="inline-flex items-center text-emerald-400"
            >
              {label ? <span className="mr-1 text-[10px] text-zinc-500">{label}</span> : null}
              {res.ip}
              {res.cc ? <span className="ml-1 text-zinc-500">{res.cc}</span> : null}
            </span>
          );
          const directOk = !!(direct && direct.ok && direct.ip);
          // Proxy and direct egress identical (or no direct probe) → show ONE IP.
          // Different → show both, labeled 代理 / 直连.
          if (!directOk || direct!.ip === proxy.ip) {
            return ipLink(proxy, 'ok');
          }
          return (
            <div data-id={`proxy-manager-cell-${entry.name}-ip-pair`} className="flex flex-col gap-0.5">
              {ipLink(proxy, 'proxy', '代理')}
              {ipLink(direct!, 'direct', '直连')}
            </div>
          );
        })()}
      </td>
      <td data-id={`proxy-manager-cell-${entry.name}-action`} className="border-b border-white/[0.04] px-2 py-2 text-right align-top">
        <button
          data-id={`proxy-manager-cell-${entry.name}-test-button`}
          type="button"
          onClick={onTest}
          disabled={!!result?.running}
          className="min-w-[60px] rounded-md border border-white/[0.08] bg-white/[0.04] px-2 py-0.5 text-[11px] text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:opacity-50"
        >
          {result?.running ? t('proxyManagerTesting') : t('proxyManagerTest')}
        </button>
      </td>
    </tr>
    {isNode && expanded ? (
      <tr data-id={`proxy-manager-detail-${entry.name}`}>
        <td colSpan={totalCols} className="border-b border-white/[0.04] bg-black/30 px-4 py-3">
          <div data-id={`proxy-manager-detail-${entry.name}-head`} className="mb-1.5 flex items-center gap-2 text-[10px] uppercase tracking-wider text-zinc-500">
            <span>{t('proxyManagerNodeConfig')}</span>
            <span className="font-mono normal-case tracking-normal text-zinc-600">mihomo.yaml</span>
          </div>
          {nodeConfig?.loading ? (
            <div data-id={`proxy-manager-detail-${entry.name}-loading`} className="flex items-center gap-2 text-[11px] text-zinc-500">
              <RefreshCw className="h-3 w-3 animate-spin" />{t('proxyManagerLoading')}
            </div>
          ) : nodeConfig?.error ? (
            <div data-id={`proxy-manager-detail-${entry.name}-error`} className="text-[11px] text-amber-400/90">{nodeConfig.error}</div>
          ) : nodeConfig?.yaml ? (
            <pre data-id={`proxy-manager-detail-${entry.name}-yaml`} className="max-h-[280px] overflow-auto whitespace-pre rounded-md border border-white/[0.06] bg-black/40 p-2.5 font-mono text-[11px] leading-relaxed text-zinc-300 select-text">{nodeConfig.yaml}</pre>
          ) : (
            <div data-id={`proxy-manager-detail-${entry.name}-empty`} className="text-[11px] text-zinc-600">-</div>
          )}
        </td>
      </tr>
    ) : null}
    </>
  );
}

export default ProxyManagerDialog;
