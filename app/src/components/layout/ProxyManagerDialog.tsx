import { useCallback, useEffect, useState } from 'react';
import { X, RefreshCw, Play, Square, RotateCw, RefreshCcw, Sparkles, Globe, Copy, Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
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
  ip?: IPResult;
  running?: boolean;
};

// Fixed probe columns shown in the table. Order matters — must match what
// /api/proxy/test returns when no `urls` body field is provided. Keep the
// labels short; the table is dense.
const PROBE_COLUMNS: Array<{ url: string; short: string }> = [
  { url: 'https://api.anthropic.com', short: 'anthropic' },
  { url: 'https://chatgpt.com', short: 'chatgpt' },
  { url: 'https://api.myip.com', short: 'myip' },
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
  const [status, setStatus] = useState<MihomoStatus | null>(null);
  const [pendingAction, setPendingAction] = useState<LifecycleAction | null>(null);
  const [lifecycleOutput, setLifecycleOutput] = useState<string>('');
  type AskKind = 'proxy' | 'group' | 'user';
  const [askAgentSending, setAskAgentSending] = useState<AskKind | ''>('');
  const [askAgentResult, setAskAgentResult] = useState<'' | 'sent' | 'failed'>('');
  const [askAgentError, setAskAgentError] = useState<string>('');
  const [allowLan, setAllowLan] = useState<boolean | null>(null);
  const [allowLanPending, setAllowLanPending] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [exportMode, setExportMode] = useState<'local' | 'lan' | 'public'>('local');
  const [exportLoading, setExportLoading] = useState(false);
  const [exportScript, setExportScript] = useState<string>('');
  const [exportHost, setExportHost] = useState<string>('');
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
      const data = (resp?.data || {}) as { script?: string; host?: string };
      setExportScript(String(data.script || ''));
      setExportHost(String(data.host || ''));
    } catch (e: any) {
      setExportScript(`# error: ${String(e?.response?.data?.detail || e?.message || e)}`);
      setExportHost('');
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
      const data = (resp?.data || {}) as { results?: DelayRow[]; ip?: IPResult };
      setResults((prev) => ({
        ...prev,
        [name]: {
          results: data.results || [],
          ip: data.ip,
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

  // sendToAgent ships a chat-prompt to the currently bound agent, with the
  // same "show sent chip → close drawer" UX as the original single-button
  // flow but parameterized by which workflow (proxy / group / user) the
  // user clicked. Three buttons share this; the only difference between
  // them is the prompt body.
  const sendToAgent = useCallback(async (kind: AskKind, prompt: string) => {
    const target = String(paneId || '').trim();
    if (!target) {
      // No bound agent — nothing was attempted, so leave the drawer open and
      // show the reason. This is the only branch where we DON'T close.
      setAskAgentResult('failed');
      setAskAgentError('no agent bound');
      return;
    }
    setAskAgentSending(kind);
    setAskAgentResult('');
    setAskAgentError('');
    let resp: any = null;
    let errorMessage = '';
    try {
      resp = await apiService.sendCommand(target, prompt, true);
    } catch (e: any) {
      errorMessage = String(e?.response?.data?.detail || e?.message || e);
    } finally {
      setAskAgentSending('');
    }
    // Once we've actually issued the send call, we treat the request as
    // "shipped" regardless of outcome — tmux can't un-Enter, and partial
    // failures usually mean the prompt is already in the agent's buffer.
    // Surface the warning/error to the user but still close the drawer so
    // they can switch focus to the chat pane.
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

  const askAddProxy = useCallback(() => {
    const configPath = status?.config || '~/cicy-ai/db/mihomo.yaml';
    const prompt = t('proxyManagerAskAddProxyPrompt', { configPath });
    return sendToAgent('proxy', prompt);
  }, [status?.config, sendToAgent, t]);

  const askAddGroup = useCallback(() => {
    const prompt = t('proxyManagerAskAddGroupPrompt');
    return sendToAgent('group', prompt);
  }, [sendToAgent, t]);

  const askAddUser = useCallback(() => {
    const currentUser = String(activeAgentId || paneId || '').replace(/:.*$/, '') || '<user>';
    const prompt = t('proxyManagerAskAddUserPrompt', { user: currentUser });
    return sendToAgent('user', prompt);
  }, [activeAgentId, paneId, sendToAgent, t]);

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
                  disabled={!exportScript || exportLoading}
                  className="ml-auto inline-flex items-center gap-1 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-0.5 text-zinc-200 transition-colors hover:bg-white/[0.07] disabled:opacity-40"
                >
                  {exportCopied ? <Check size={10} className="text-emerald-300" /> : <Copy size={10} />}
                  {exportCopied ? t('proxyManagerExportCopied') : t('proxyManagerExportCopy')}
                </button>
              </div>
              <pre data-id="proxy-manager-drawer-export-script" className="mt-2 max-h-48 overflow-auto rounded-md bg-black/40 px-2 py-1.5 font-mono text-[10px] leading-relaxed text-zinc-300 whitespace-pre">
                {exportLoading ? t('proxyManagerLoading') : exportScript || '—'}
              </pre>
            </div>
          )}
          <div data-id="proxy-manager-drawer-ask-agent-row" className="mt-2 flex flex-wrap items-center gap-1.5 text-[11px]">
            {([
              { kind: 'proxy' as const, dataId: 'proxy-manager-drawer-ask-add-proxy', onClick: askAddProxy, label: t('proxyManagerAddProxy'), pendingLabel: t('proxyManagerAskAgentSending') },
              { kind: 'group' as const, dataId: 'proxy-manager-drawer-ask-add-group', onClick: askAddGroup, label: t('proxyManagerAddGroup'), pendingLabel: t('proxyManagerAskAgentSending') },
              { kind: 'user'  as const, dataId: 'proxy-manager-drawer-ask-add-user',  onClick: askAddUser,  label: t('proxyManagerAddUser'),  pendingLabel: t('proxyManagerAskAgentSending') },
            ]).map((btn) => (
              <button
                key={btn.kind}
                data-id={btn.dataId}
                type="button"
                onClick={btn.onClick}
                disabled={!!askAgentSending || !paneId}
                className="inline-flex items-center gap-1.5 rounded-md border border-indigo-500/30 bg-indigo-500/10 px-2.5 py-1 text-zinc-100 transition-colors hover:bg-indigo-500/20 disabled:opacity-40"
                title={!paneId ? t('proxyManagerAskAgentNoPane') : undefined}
              >
                <Sparkles size={11} />
                {askAgentSending === btn.kind ? btn.pendingLabel : btn.label}
              </button>
            ))}
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
                  />
                ))}
              </tbody>
            </table>
          )}
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
}: {
  entry: ProxyEntry;
  kind: 'group' | 'node';
  result?: TestResult;
  onTest: () => void;
}) {
  const { t } = useTranslation('agentInspector');
  // Map result rows by url for column lookup.
  const byURL: Record<string, DelayRow> = {};
  if (result?.results) {
    for (const r of result.results) byURL[r.url] = r;
  }
  return (
    <tr data-id={`proxy-manager-row-${entry.name}`} className="text-zinc-300">
      <td data-id={`proxy-manager-cell-${entry.name}-name`} className="border-b border-white/[0.04] px-2 py-2 align-top">
        <div data-id={`proxy-manager-cell-${entry.name}-name-text`} className="truncate font-medium text-zinc-100">{entry.name}</div>
        {kind === 'group' && entry.members && entry.members.length > 0 && (
          <div data-id={`proxy-manager-cell-${entry.name}-members`} className="mt-0.5 text-[10px] text-zinc-600">{entry.members.length} {t('proxyManagerMembers')}</div>
        )}
      </td>
      <td data-id={`proxy-manager-cell-${entry.name}-kind`} className="border-b border-white/[0.04] px-2 py-2 align-top text-zinc-500">
        {kind === 'group' ? t('proxyManagerKindGroup') : t('proxyManagerKindNode')}
      </td>
      <td data-id={`proxy-manager-cell-${entry.name}-type`} className="border-b border-white/[0.04] px-2 py-2 align-top text-zinc-500">{entry.type}</td>
      <td data-id={`proxy-manager-cell-${entry.name}-now`} className="border-b border-white/[0.04] px-2 py-2 align-top text-zinc-400">
        {kind === 'group' ? (entry.now || '-') : <span data-id={`proxy-manager-cell-${entry.name}-now-na`} className="text-zinc-700">-</span>}
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
        {result?.running && !result?.ip ? (
          <span data-id={`proxy-manager-cell-${entry.name}-ip-pending`} className="text-zinc-700">...</span>
        ) : result?.ip ? (
          result.ip.ok && result.ip.ip ? (
            <a
              data-id={`proxy-manager-cell-${entry.name}-ip-ok`}
              href={`https://ping0.cc/ip/${encodeURIComponent(result.ip.ip)}`}
              target="_blank"
              rel="noopener noreferrer"
              title={`ping0.cc / ${result.ip.ip}`}
              className="text-emerald-400 underline decoration-emerald-500/30 underline-offset-2 hover:decoration-emerald-300"
            >
              {result.ip.ip}
              {result.ip.cc && <span data-id={`proxy-manager-cell-${entry.name}-ip-cc`} className="ml-1 text-zinc-500">{result.ip.cc}</span>}
            </a>
          ) : (
            <span data-id={`proxy-manager-cell-${entry.name}-ip-fail`} className="text-red-400" title={result.ip.error}>fail</span>
          )
        ) : (
          <span data-id={`proxy-manager-cell-${entry.name}-ip-empty`} className="text-zinc-700">-</span>
        )}
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
  );
}

export default ProxyManagerDialog;
