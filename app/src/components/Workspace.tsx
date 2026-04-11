import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import {
  Terminal, MessageSquare, Home, Folder, FolderOpen, X, Settings, Brain, Search,
  LayoutList, Users, RotateCcw, Plus, ExternalLink, Key, Bug, Server, MoreHorizontal, ChevronDown, Github
} from 'lucide-react';
import { assetUrl } from '../lib/assets';
import { cn } from '../lib/utils';
import { useDevRegister } from '../lib/devStore';
import { useAuth } from '../contexts/AuthContext';
import { SendingProvider } from '../contexts/SendingContext';
import ChatView from './chat/ChatView';
import { CommandPanel } from './terminal/CommandPanel';
import { WindowManager } from './terminal/WindowManager';
import { VoiceFloatingButton } from './VoiceFloatingButton';
import FloatingCodeWindow from './FloatingCodeWindow';
import TeamPanel from './layout/TeamPanel';
import SkillPanel from './layout/SkillPanel';
import SettingsFloat from './layout/SettingsFloat';
import TokenDialog from './layout/TokenDialog';
import useDesktopEvents from './layout/useDesktopEvents';
import AgentCanvas, { AgentCanvasItem } from './layout/AgentCanvas';
import { useDialog } from '../contexts/DialogContext';
import config, { getHostHome, syncHostHomeFromPath, toTildePath, urls } from '../config';
import apiService from '../services/api';
import { sendCommandToTmux } from '../services/mockApi';
import { ApiSwitchDialog } from './layout/ApiSwitchDialog';
import CreateAgentDialog, { CreateAgentValues } from './CreateAgentDialog';

const cache = {
  get: (k: string, def: any) => { try { const v = JSON.parse(localStorage.getItem(k)!); return v ?? def; } catch { return def; } },
  set: (k: string, v: any) => localStorage.setItem(k, JSON.stringify(v)),
};

const LEFT_PANEL_WIDTH = 320;
const getFloatingOpenKey = (paneId: string) => `ws_floatingCodeOpen:${paneId}`;
const TEAM_TERMINAL_ACTIVE_KEY = 'ws_teamTerminalActive';
const GITHUB_ISSUES_URL = 'https://github.com/cicy-ai/cicy-code/issues';
const UPGRADE_URL = 'https://cicy-ai.com/team/dashbaord#upgrade';

function parseIsPro(value: unknown): boolean | null {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'number') {
    if (value === 1) return true;
    if (value === 0) return false;
    return null;
  }
  if (typeof value !== 'string') return null;
  const raw = value.trim().toLowerCase();
  if (!raw) return null;
  if (['1', 'true', 'yes', 'on', 'pro'].includes(raw)) return true;
  if (['0', 'false', 'no', 'off', 'trial', 'free'].includes(raw)) return false;
  return null;
}

function resolveTrialExpiresMs(epoch: number | null, expiresAt: string | null): number | null {
  if (typeof epoch === 'number' && Number.isFinite(epoch) && epoch > 0) {
    return epoch * 1000;
  }
  if (!expiresAt) return null;
  const parsed = Date.parse(expiresAt);
  return Number.isFinite(parsed) ? parsed : null;
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
}

function formatDateTime(ms: number): string {
  return new Date(ms).toLocaleString('zh-CN', { hour12: false });
}

interface Props { agentId: string; onSelectAgent: (id: string) => void; }
type LeftPanelView = 'team' | 'skills' | 'agents' | null;

export default function Workspace({ agentId, onSelectAgent }: Props) {
  const { token, hasPermission } = useAuth();
  const { confirm } = useDialog();
  const paneId = agentId || 'w-10001';
  const fullPaneId = `${paneId}:main.0`;
  const initialPaneIdRef = useRef(paneId);
  const floatingOpenKey = getFloatingOpenKey(initialPaneIdRef.current);

  const mainTab = 'cli' as 'chat' | 'cli';
  const [leftPanelView, setLeftPanelView] = useState<LeftPanelView>(() => {
    const v = cache.get('ws_leftPanel', null);
    if (v === 'team' || v === 'skills') return v;
    return 'team';
  });
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [apiOpen, setApiOpen] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  const [status, setStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode, setMouseMode] = useState<'on' | 'off'>('off');
  const [isRestarting, setIsRestarting] = useState(false);
  const [agents, set智能体] = useState<any[]>([]);
  const [boundAgents, setBoundAgents] = useState<any[]>([]);
  const [pollStatuses, setPollStatuses] = useState<Record<string, any>>({});
  const [paneDetails, setPaneDetails] = useState<Record<string, any>>({});
  const [codeServerSrc, setCodeServerSrc] = useState('');
  const [floatingCodeOpen, setFloatingCodeOpen] = useState(() => cache.get(floatingOpenKey, false));
  const [codeFolder, setCodeFolder] = useState('');
  const [teamTerminalActive, set团队TerminalActive] = useState<Record<string, string>>(() => cache.get(TEAM_TERMINAL_ACTIVE_KEY, {}));
  const [canvasLocateRequest, setCanvasLocateRequest] = useState<{ paneId: string; nonce: number; zoomToActual?: boolean } | null>(null);
  const codeWindowInitializedRef = useRef(false);
  const agentWorkspaceRef = useRef(`~/workers/${paneId}`);
  const prevCanvasPaneIdsRef = useRef<string[] | null>(null);
  const initialCanvasRestoreScopeRef = useRef<string | null>(null);

  const handleCodeHome = () => {
    const hostHome = getHostHome();
    const next = urls.codeServer(hostHome, token!);
    window.open(next, '_blank');
    if (next !== codeServerSrc) { setCodeServerSrc(next); setCodeFolder(hostHome); }
  };
  const handleCodeServiceOpen = (folder?: string) => {
    const nextFolder = folder || codeFolder;
    if (!nextFolder || !token) return;
    const next = urls.codeServer(nextFolder, token);
    window.open(next, '_blank');
    if (next !== codeServerSrc || nextFolder !== codeFolder) {
      setCodeServerSrc(next);
      setCodeFolder(nextFolder);
    }
  };
  const handleOpenClawOpen = () => {
    if (!token) return;
    window.open(urls.openClaw(token), '_blank');
  };

  const navigateToFolder = (folder: string) => {
    const next = urls.codeServer(folder, token!);
    if (next !== codeServerSrc) { setCodeServerSrc(next); setCodeFolder(folder); }
  };
  const [agentDetail, setAgentDetail] = useState<any>(null);
  const title = agentDetail?.title || '-';
  const [netLatency, setNetLatency] = useState<number | null>(null);
  const [chatWsConnected, setChatWsConnected] = useState(false);
  const [trialExpiresAt, setTrialExpiresAt] = useState<string | null>(null);
  const [trialExpiresAtEpoch, setTrialExpiresAtEpoch] = useState<number | null>(null);
  const [isPro, setIsPro] = useState<boolean | null>(null);
  const [countdownNowMs, setCountdownNowMs] = useState(() => Date.now());
  const isApiOnlyRuntime = !!(agentDetail && agentDetail.capabilities?.supports_tmux === false);

  const [showVoiceControl, setShowVoiceControl] = useState(false);
  const [voiceLoading, setVoiceLoading] = useState(false);
  const [voiceBtnPos, setVoiceBtnPos] = useState(() => cache.get('ws_voiceBtnPos', { x: 20, y: Math.max(60, window.innerHeight - 400) }));

  const [panelPos, setPanelPos] = useState(() => cache.get('agent_panelPos', { x: 20, y: Math.max(60, window.innerHeight - 280) }));
  const [panelSize, setPanelSize] = useState(() => cache.get('agent_panelSize', { width: 360, height: 220 }));
  const [activeWinIdx, setActiveWinIdx] = useState('0');
  const activityBarRef = useRef<HTMLDivElement>(null);

  const addApp = (window as any).__desktopAddApp || (() => {});
  useDesktopEvents(addApp);

  const leftActive = leftPanelView;
  const closeLeftPanel = useCallback(() => {
    setLeftPanelView(null);
  }, []);

  useEffect(() => {
    cache.set('ws_leftPanel', leftActive === 'team' || leftActive === 'skills' ? leftActive : null);
  }, [leftActive]);
  useEffect(() => { cache.set(floatingOpenKey, floatingCodeOpen); }, [floatingCodeOpen, floatingOpenKey]);
  useEffect(() => { cache.set(TEAM_TERMINAL_ACTIVE_KEY, teamTerminalActive); }, [teamTerminalActive]);
  useEffect(() => { cache.set('ws_voiceBtnPos', voiceBtnPos); }, [voiceBtnPos]);
  useEffect(() => { cache.set('agent_panelPos', panelPos); }, [panelPos]);
  useEffect(() => { cache.set('agent_panelSize', panelSize); }, [panelSize]);
  useEffect(() => {
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prevOverflow;
    };
  }, []);
  const applyPanePatch = useCallback((targetPaneId: string, patch: any) => {
    setPaneDetails(prev => ({ ...prev, [targetPaneId]: { ...(prev[targetPaneId] || {}), ...patch } }));
    if (targetPaneId === paneId) setAgentDetail((prev: any) => ({ ...(prev || {}), ...patch }));
    set智能体(prev => prev.map(a => {
      const id = (a.pane_id || a.id || '').split(':')[0];
      return id === targetPaneId ? { ...a, ...patch } : a;
    }));
  }, [paneId]);
  const handleRenamePaneTitle = useCallback(async (targetPaneId: string, nextTitle: string) => {
    await apiService.updatePane(targetPaneId, { title: nextTitle });
    applyPanePatch(targetPaneId, { title: nextTitle });
    setBoundAgents(prev => prev.map((item: any) => {
      const id = String(item?.name || item?.pane_id || '').replace(/:.*$/, '');
      return id === targetPaneId ? { ...item, title: nextTitle } : item;
    }));
    try {
      const { data } = await apiService.getPanes();
      set智能体(Array.isArray(data) ? data : data?.panes || []);
    } catch {}
  }, [applyPanePatch]);

  const refreshPanes = useCallback(async () => {
    if (!token) return;
    try {
      const { data } = await apiService.getPanes();
      set智能体(Array.isArray(data) ? data : data?.panes || []);
      window.dispatchEvent(new CustomEvent('refresh-panes'));
    } catch {}
  }, [token]);
  const refreshPoll = useCallback(async () => {
    const t0 = performance.now();
    try {
      const { data } = await apiService.poll(paneId, { timeout: 5000 });
      const latency = Math.round(performance.now() - t0);
      setNetLatency(latency);
      window.dispatchEvent(new CustomEvent('network-latency', { detail: { latency } }));
      setBoundAgents(Array.isArray(data?.agents) ? data.agents : []);
      setPollStatuses(data?.statuses && typeof data.statuses === 'object' ? data.statuses : {});
      const st = data?.statuses?.[fullPaneId] || data?.statuses?.[paneId];
      if (st?.status) setStatus(st.status);
      if (st?.title) setAgentDetail((prev: any) => prev ? { ...prev, title: st.title } : { title: st.title });
      if (st?.contextUsage != null) setContextUsage(st.contextUsage);
      const nextTrialExpiresAt = typeof data?.trial_expires_at === 'string' && data.trial_expires_at.trim()
        ? data.trial_expires_at.trim()
        : null;
      const nextTrialEpochRaw = data?.trial_expires_at_epoch;
      const nextTrialEpoch = typeof nextTrialEpochRaw === 'number'
        ? nextTrialEpochRaw
        : Number.parseInt(String(nextTrialEpochRaw ?? ''), 10);
      setTrialExpiresAt(nextTrialExpiresAt);
      setTrialExpiresAtEpoch(Number.isFinite(nextTrialEpoch) && nextTrialEpoch > 0 ? nextTrialEpoch : null);
      setIsPro(parseIsPro(data?.is_pro));
    } catch {
      setNetLatency(null);
      window.dispatchEvent(new CustomEvent('network-latency', { detail: { latency: null } }));
    }
  }, [fullPaneId, paneId]);
  useEffect(() => { void refreshPanes(); }, [refreshPanes, paneId]);
  useEffect(() => { 
    apiService.getPane(fullPaneId).then(({ data }) => { 
      setAgentDetail(data);
      setPaneDetails(prev => ({ ...prev, [paneId]: data }));
      const workspace = data?.workspace || `~/workers/${paneId}`;
      syncHostHomeFromPath(workspace);
      agentWorkspaceRef.current = workspace;
      if (!codeWindowInitializedRef.current && token && !(data?.capabilities?.supports_code_server === false || data?.capabilities?.supports_tmux === false)) {
        setCodeFolder(workspace);
        setCodeServerSrc(urls.codeServer(workspace, token));
        codeWindowInitializedRef.current = true;
      }
    }).catch(() => {}); 
  }, [fullPaneId, paneId, token]);
  const prevPaneId = useRef(paneId);
  useEffect(() => {
    if (prevPaneId.current !== paneId) {
      setAgentDetail(null); setStatus('idle'); setContextUsage(null);
      prevPaneId.current = paneId;
    }
  }, [paneId]);
  useEffect(() => {
    let timer: number | null = null;
    let cancelled = false;
    let inFlight = false;
    let rerunRequested = false;

    const clearTimer = () => {
      if (timer !== null) {
        window.clearTimeout(timer);
        timer = null;
      }
    };

    const schedule = (delay = config.pollInterval) => {
      if (cancelled) return;
      clearTimer();
      timer = window.setTimeout(() => {
        void runPoll();
      }, delay);
    };

    const runPoll = async () => {
      if (cancelled) return;
      if (inFlight) {
        rerunRequested = true;
        return;
      }
      inFlight = true;
      try {
        await refreshPoll();
      } finally {
        inFlight = false;
        if (cancelled) return;
        if (rerunRequested) {
          rerunRequested = false;
          schedule(0);
          return;
        }
        schedule();
      }
    };

    const onRefresh = () => {
      rerunRequested = true;
      if (!inFlight) schedule(0);
    };
    const onVisible = () => {
      if (document.visibilityState === 'visible') onRefresh();
    };

    onRefresh();
    window.addEventListener('refresh-panes', onRefresh);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      cancelled = true;
      clearTimer();
      window.removeEventListener('refresh-panes', onRefresh);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [refreshPoll]);

  // Toast listener
  useEffect(() => {
    const handler = (e: CustomEvent) => { setToast(e.detail); setTimeout(() => setToast(null), 5000); };
    window.addEventListener('show-toast', handler as EventListener);
    return () => window.removeEventListener('show-toast', handler as EventListener);
  }, []);

  // Status change listener (from WebSocket)
  useEffect(() => {
    const handler = (e: CustomEvent) => { if (e.detail?.status) setStatus(e.detail.status); };
    window.addEventListener('agent-status-change', handler as EventListener);
    return () => window.removeEventListener('agent-status-change', handler as EventListener);
  }, []);

  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent).detail || {};
      if (detail.agentId !== paneId) return;
      setChatWsConnected(!!detail.connected);
    };
    window.addEventListener('chat-ws-connection', handler as EventListener);
    return () => window.removeEventListener('chat-ws-connection', handler as EventListener);
  }, [paneId]);

  const handleRestart = () => {
    if (isApiOnlyRuntime) return;
    confirm(`Restart ${paneId}?`, async () => {
      setIsRestarting(true);
      try { await apiService.restartPane(paneId); for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); try { const { data } = await apiService.getTtydStatus(paneId); if (data.status === 'running') break; } catch {} } } catch { alert('重启失败'); } finally { setIsRestarting(false); }
    });
  };
  const handleCapture = async () => { if (isApiOnlyRuntime) return; try { const { data } = await apiService.capturePane(paneId, 100); if (data.output) await navigator.clipboard.writeText(data.output); } catch {} };
  const handleToggleMouse = async () => { if (isApiOnlyRuntime) return; const n = mouseMode === 'on' ? 'off' : 'on'; try { await apiService.toggleMouse(n, fullPaneId); setMouseMode(n); } catch {} };

  const toggleLeft = (p: 'team' | 'skills') => {
    setLeftPanelView(prev => prev === p ? null : p);
  };

  const toggle智能体 = () => {
    setLeftPanelView(prev => prev === 'agents' ? null : 'agents');
  };

  const canvasPaneIds = useMemo(() => {
    const next = [paneId];
    boundAgents.forEach((binding: any) => {
      const boundPaneId = String(binding?.name || binding?.pane_id || '').replace(/:.*$/, '');
      if (boundPaneId && !next.includes(boundPaneId)) {
        next.push(boundPaneId);
      }
    });
    return next;
  }, [boundAgents, paneId]);
  useEffect(() => {
    const prev = prevCanvasPaneIdsRef.current;
    prevCanvasPaneIdsRef.current = canvasPaneIds;
    if (!prev) return;
    const addedPaneIds = canvasPaneIds.filter((id) => id !== paneId && !prev.includes(id));
    if (addedPaneIds.length > 0) {
      const nextPaneId = addedPaneIds[addedPaneIds.length - 1];
      set团队TerminalActive(current => ({ ...current, [paneId]: nextPaneId }));
      setCanvasLocateRequest({ paneId: nextPaneId, nonce: Date.now(), zoomToActual: true });
      return;
    }

    const removedPaneIds = prev.filter((id) => id !== paneId && !canvasPaneIds.includes(id));
    if (removedPaneIds.length === 0) return;

    const storedActivePaneId = teamTerminalActive[paneId];
    const nextPaneId = canvasPaneIds.includes(storedActivePaneId) ? storedActivePaneId : paneId;
    set团队TerminalActive(current => (
      current[paneId] === nextPaneId ? current : { ...current, [paneId]: nextPaneId }
    ));
    setCanvasLocateRequest({ paneId: nextPaneId, nonce: Date.now(), zoomToActual: true });
  }, [canvasPaneIds, paneId, teamTerminalActive]);
  const current团队Active = canvasPaneIds.includes(teamTerminalActive[paneId]) ? teamTerminalActive[paneId] : paneId;
  useEffect(() => {
    initialCanvasRestoreScopeRef.current = null;
  }, [paneId]);
  useEffect(() => {
    if (initialCanvasRestoreScopeRef.current === paneId) return;
    const storedActivePaneId = teamTerminalActive[paneId];
    if (storedActivePaneId && storedActivePaneId !== paneId && !canvasPaneIds.includes(storedActivePaneId)) {
      return;
    }
    initialCanvasRestoreScopeRef.current = paneId;
    setCanvasLocateRequest({ paneId: current团队Active, nonce: Date.now(), zoomToActual: true });
  }, [canvasPaneIds, current团队Active, paneId, teamTerminalActive]);
  const settingsTargetPaneId = current团队Active || paneId;
  const settingsTargetFullPaneId = `${settingsTargetPaneId}:main.0`;
  const settingsTargetDetail = paneDetails[settingsTargetPaneId] || (settingsTargetPaneId === paneId ? agentDetail : null);
  const topBarPaneId = settingsTargetPaneId;
  const topBarDetail = settingsTargetDetail;
  const topBarTitle = topBarDetail?.title
    || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === topBarPaneId)?.title
    || (topBarPaneId === paneId ? title : topBarPaneId);
  const topBarWorkspace = topBarDetail?.workspace || `~/workers/${topBarPaneId}`;
  const topBarIsApiOnlyRuntime = !!(
    topBarDetail
    && topBarDetail.capabilities?.supports_tmux === false
  );
  const openPaneInCurrentTerminal = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    set团队TerminalActive(prev => ({ ...prev, [paneId]: clean }));
  }, [paneId]);
  const locatePaneInCanvas = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    set团队TerminalActive(prev => ({ ...prev, [paneId]: clean }));
    setCanvasLocateRequest({ paneId: clean, nonce: Date.now(), zoomToActual: true });
  }, [paneId]);
  useEffect(() => {
    if (!token || !settingsTargetPaneId || settingsTargetPaneId === paneId || paneDetails[settingsTargetPaneId]) return;
    apiService.getPane(`${settingsTargetPaneId}:main.0`).then(({ data }) => {
      setPaneDetails(prev => ({ ...prev, [settingsTargetPaneId]: data }));
    }).catch(() => {});
  }, [paneId, paneDetails, settingsTargetPaneId, token]);
  useEffect(() => {
    document.title = `${topBarTitle} (${topBarPaneId}) | CiCy Code`;
  }, [topBarPaneId, topBarTitle]);
  const trialExpiresMs = useMemo(() => resolveTrialExpiresMs(trialExpiresAtEpoch, trialExpiresAt), [trialExpiresAt, trialExpiresAtEpoch]);
  const isTrialUser = isPro === false;
  const isProUser = isPro === true;
  const showTrialUpgrade = trialExpiresMs !== null && (isTrialUser || isProUser);
  useEffect(() => {
    if (!isTrialUser || trialExpiresMs === null) return;
    setCountdownNowMs(Date.now());
    const timer = window.setInterval(() => setCountdownNowMs(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [isTrialUser, trialExpiresMs]);
  const trialCountdown = useMemo(() => {
    if (trialExpiresMs === null) return '';
    return formatDuration(trialExpiresMs - countdownNowMs);
  }, [countdownNowMs, trialExpiresMs]);
  const trialExpireAtLabel = useMemo(() => {
    if (trialExpiresMs === null) return '';
    return formatDateTime(trialExpiresMs);
  }, [trialExpiresMs]);
  const rightContent = (
    <div data-id="right-content" className="h-full flex flex-col relative">
      <header data-id="top-bar" className="h-12 border-b border-[var(--vsc-border)] bg-[#0A0A0A] flex items-center justify-between px-4 shrink-0 z-10">
        <div data-id="top-bar-left" className="flex items-center gap-3 w-1/3 min-w-0">
          {!topBarIsApiOnlyRuntime && (
            <button
              type="button"
              onClick={handleCodeHome}
              className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer shrink-0"
              title={topBarWorkspace}
            >
              <Home className="w-3.5 h-3.5" />
            </button>
          )}
          {showTrialUpgrade && (
            <div data-id="top-bar-trial-upgrade" className="min-w-0 flex items-center gap-2 rounded-md border border-amber-400/25 bg-amber-400/10 px-2 py-1">
              {isProUser ? (
                <>
                  <span
                    data-id="top-bar-pro-badge"
                    className="rounded border border-emerald-300/40 bg-emerald-300/15 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-200"
                  >
                    PRO
                  </span>
                  <span className="text-[11px] text-amber-100 font-mono whitespace-nowrap">
                    到期时间 {trialExpireAtLabel}
                  </span>
                </>
              ) : (
                <span className="text-[11px] text-amber-100 font-mono whitespace-nowrap">
                  试用剩余 {trialCountdown}
                </span>
              )}
              <button
                type="button"
                data-id="top-bar-upgrade-btn"
                onClick={() => window.open(UPGRADE_URL, '_blank', 'noopener,noreferrer')}
                className="shrink-0 rounded bg-amber-400/80 px-2 py-0.5 text-[10px] font-semibold text-black hover:bg-amber-300 transition-colors"
              >
                我要升级
              </button>
            </div>
          )}
        </div>
        <div data-id="top-bar-center" className="flex items-center justify-center w-1/3" />
        <div data-id="top-bar-right" className="flex items-center justify-end w-1/3 gap-3">
          <SystemResourceMonitor token={token} />
          <NetworkSignal latency={netLatency} connected={chatWsConnected} />
          <button
            data-id="top-bar-github-issues"
            onClick={() => window.open(GITHUB_ISSUES_URL, '_blank', 'noopener,noreferrer')}
            className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"
            title="GitHub Issues"
          >
            <Github className="w-3.5 h-3.5" />
          </button>
          <button onClick={() => setTokenOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title="API 令牌"><Key className="w-3.5 h-3.5" /></button>
          <button onClick={() => setApiOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title="API 服务器"><Server className="w-3.5 h-3.5" /></button>
          <button onClick={() => window.dispatchEvent(new Event('open-devtools-panel'))} className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title="开发工具"><Bug className="w-3.5 h-3.5" /></button>
          <span id="version" className="text-[10px] font-mono text-zinc-600">1.0.18</span>
          {contextUsage != null && (
            <div data-id="context-usage" className="flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-white/[0.02]">
              <div data-id="context-bar" className="w-12 h-1 rounded-full bg-white/[0.04] overflow-hidden">
                <div className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400/60' : contextUsage > 50 ? 'bg-yellow-400/60' : 'bg-emerald-400/60'}`} style={{ width: `${contextUsage}%` }} />
              </div>
              <span data-id="context-pct" className="text-xs text-zinc-600 font-mono">{contextUsage}%</span>
            </div>
          )}
        </div>
      </header>
      <div data-id="right-tabs" className="flex-1 relative overflow-hidden">
        <div data-id="chat-tab" className="absolute inset-0 flex justify-center" style={{ display: mainTab === 'chat' ? 'flex' : 'none' }}>
          <div className="w-full max-w-5xl h-full">
            <ChatView paneId={paneId} token={token!} apiOnly={isApiOnlyRuntime} commandPanel={
            !isApiOnlyRuntime ? <CommandPanel paneTarget={paneId} title={title} token={token}
              panelPosition={panelPos} panelSize={panelSize} readOnly={false}
              onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}}
              onChange={(pos, size) => { setPanelPos(pos); setPanelSize(size); }}
              canSend={status === 'idle'} agentStatus={status} contextUsage={contextUsage}
              mouseMode={mouseMode} onToggleMouse={handleToggleMouse} onRestart={handleRestart}
              isRestarting={isRestarting} onCapturePane={handleCapture}
              hasEditPermission={hasPermission('edit')} hasRestartPermission={hasPermission('restart')}
              hasCapturePermission={hasPermission('capture')} showVoiceControl={showVoiceControl}
              onToggleVoiceControl={() => setShowVoiceControl(v => !v)} /> : null
          } />
          </div>
        </div>
        <div data-id="cli-tab" className="absolute inset-0 flex" style={{ display: mainTab === 'cli' ? 'flex' : 'none' }}>
          <div data-id="cli-terminal-area" className="w-full h-full relative bg-black">
            <AgentCanvas
              scopeId={paneId}
              items={buildCanvasItems({
                paneId,
                token,
                canvasPaneIds,
                agents,
                boundAgents,
                paneDetails,
                pollStatuses,
                agentDetail,
              })}
              activePaneId={current团队Active}
              locateRequest={canvasLocateRequest}
              onActivePaneIdChange={(targetPaneId) => set团队TerminalActive(prev => ({ ...prev, [paneId]: targetPaneId }))}
              onOpenCodeServicePane={(_targetPaneId, workspace) => handleCodeServiceOpen(workspace)}
              onOpenOpenClaw={handleOpenClawOpen}
              onRenamePaneTitle={handleRenamePaneTitle}
              onOpenSettingsPane={(targetPaneId) => {
                set团队TerminalActive(prev => ({ ...prev, [paneId]: targetPaneId }));
                setSettingsOpen(true);
              }}
            />
          </div>
        </div>
      </div>
    </div>
  );

  useDevRegister('Workspace', {
    paneId: fullPaneId, title, status, contextUsage, mouseMode, isRestarting,
    agentDetail, netLatency,
    trialExpiresAt,
    trialExpiresAtEpoch,
    isPro,
    trialCountdown,
    trialExpireAtLabel,
    isTrialUser,
    isProUser,
    showTrialUpgrade,
    agentsCount: agents.length,
    agents: agents.map((a: any) => ({ pane_id: a.pane_id, title: a.title, status: a.status, active: a.active })),
    leftPanel: leftActive, floatingCodeOpen, activeWinIdx,
  });

  return (
    <SendingProvider>
    <div data-id="workspace-root" className="flex h-screen overflow-hidden bg-[#0A0A0A] text-zinc-400 relative">
      {/* Activity Bar */}
      <div data-id="activity-bar" ref={activityBarRef} className="w-14 border-r border-[var(--vsc-border)] flex flex-col items-center py-4 justify-between bg-[#0A0A0A] shrink-0 z-50">
        <div data-id="activity-bar-top" className="flex flex-col gap-4 w-full items-center">
          <SideBtn dataId="btn-team" active={leftActive === 'team'} icon={<Users className="w-5 h-5" />} title="团队" onClick={() => toggleLeft('team')} />
          <SideBtn
            dataId="btn-code-window"
            active={leftActive === 'agents'}
            icon={<LayoutList className="w-5 h-5" />}
            title="智能体"
            onClick={toggle智能体}
          />
        </div>
      </div>

      {/* Main */}
      <div data-id="main-area" className="flex-1 flex flex-col min-w-0">
        {/* Content */}
        <main data-id="content-area" className="flex-1 relative overflow-hidden">
          <div data-id="main-layout" className="flex h-full min-w-0">
            {leftActive ? (
              <div
                data-testid="left-panel"
                data-id="left-panel"
                className="h-full w-[320px] min-w-[320px] max-w-[320px] shrink-0"
              >
                <div data-id="left-panel-wrap" className="h-full flex flex-col bg-[#0A0A0A] border-r border-[var(--vsc-border)] relative z-[130]">
                  <div data-id="left-panel-header" className="h-12 border-b border-[var(--vsc-border)] flex items-center px-2 bg-[#0e0e0e] shrink-0 gap-1">
                    {leftActive === 'agents' ? <>
                      <LayoutList className="w-3.5 h-3.5 text-zinc-600" />
                      <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">智能体</span>
                    </> : leftActive === 'skills' ? <>
                      <Brain className="w-3.5 h-3.5 text-zinc-600" />
                      <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">技能</span>
                    </> : <>
                      <Users className="w-3.5 h-3.5 text-zinc-600" />
                      <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">团队</span>
                    </>}
                    <button data-id="left-panel-close" onClick={closeLeftPanel} className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"><X className="w-3.5 h-3.5" /></button>
                  </div>
                  <div data-id="left-panel-body" className="flex-1 relative overflow-hidden bg-[#0A0A0A] z-[131]">
                    {leftActive === 'agents' ? (
                      <div className="absolute inset-0 overflow-auto">
                        <AgentDrawer agents={agents} paneId={paneId}
                          onSelectAgent={onSelectAgent}
                          on智能体Change={set智能体}
                          onOpenSettings={(targetPaneId) => {
                            onSelectAgent(targetPaneId);
                            setSettingsOpen(true);
                          }}
                        />
                      </div>
                    ) : leftActive === 'team' ? (
                      <div className="absolute inset-0">
                        <TeamPanel
                          paneId={paneId}
                          panes={agents}
                          bindings={boundAgents}
                          statuses={pollStatuses}
                          onOpenInCurrentPane={openPaneInCurrentTerminal}
                          onLocatePane={locatePaneInCanvas}
                          openedPaneIds={canvasPaneIds.filter(id => id !== paneId)}
                          activePaneId={current团队Active}
                          onRefreshPanes={refreshPanes}
                          onRefreshPoll={refreshPoll}
                          onOpenSettingsPane={(targetPaneId) => {
                            openPaneInCurrentTerminal(targetPaneId);
                            setSettingsOpen(true);
                          }}
                        />
                      </div>
                    ) : (
                      <div className="absolute inset-0 overflow-auto">
                        <SkillPanel paneId={paneId} bindings={boundAgents} />
                      </div>
                    )}
                  </div>
                </div>
              </div>
            ) : null}
            <div data-testid="right-panel" data-id="right-panel" className="min-w-0 flex-1">
              {rightContent}
            </div>
          </div>
        </main>
      </div>

      {/* Voice */}
      {showVoiceControl && (
        <div data-id="voice-float">
          <VoiceFloatingButton initialPosition={voiceBtnPos} onPositionChange={setVoiceBtnPos}
            onRecordStart={() => {
              navigator.mediaDevices.getUserMedia({ audio: true }).then(s => {
                (window as any).__voiceStream = s;
                const rec = new MediaRecorder(s, { mimeType: 'audio/webm;codecs=opus' });
                (window as any).__voiceChunks = [] as Blob[];
                rec.ondataavailable = e => { if (e.data.size > 0) (window as any).__voiceChunks.push(e.data); };
                rec.start(); (window as any).__voiceRec = rec;
              });
            }}
            onRecordEnd={(should发送) => {
              const rec = (window as any).__voiceRec as MediaRecorder | undefined;
              if (rec && rec.state !== 'inactive') {
                rec.onstop = async () => {
                  (window as any).__voiceStream?.getTracks().forEach((t: any) => t.enabled = false);
                  if (!should发送) return;
                  const blob = new Blob((window as any).__voiceChunks || [], { type: 'audio/webm' });
                  if (blob.size < 100) return;
                  const fd = new FormData(); fd.append('file', blob, 'voice.webm'); fd.append('engine', 'google');
                  setVoiceLoading(true);
                  try { const { data } = await apiService.stt(fd); if (data.text) { window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneId, q: data.text } })); sendCommandToTmux(data.text, paneId); } } catch {} finally { setVoiceLoading(false); }
                };
                rec.stop();
              }
            }}
            isRecordingExternal={false} isLoading={voiceLoading}
          />
        </div>
      )}

      {!isApiOnlyRuntime && (
        <FloatingCodeWindow
          open={floatingCodeOpen}
          src={codeServerSrc}
          folderLabel={toTildePath(codeFolder)}
          storageScopeId={initialPaneIdRef.current}
          onNavigate={navigateToFolder}
          onClose={() => setFloatingCodeOpen(false)}
        />
      )}

      {settingsOpen && (
        <div data-id="settings-overlay">
          <SettingsFloat
            paneId={settingsTargetPaneId}
            fullPaneId={settingsTargetFullPaneId}
            agentDetail={settingsTargetDetail}
            onAgentDetailChange={(d) => {
              applyPanePatch(settingsTargetPaneId, d);
            }}
            onClose={() => setSettingsOpen(false)}
          />
        </div>
      )}
      {tokenOpen && <TokenDialog onClose={() => setTokenOpen(false)} />}
      {apiOpen && <ApiSwitchDialog onClose={() => setApiOpen(false)} />}
      {toast && <div className="fixed top-4 left-1/2 -translate-x-1/2 z-[9999] px-4 py-2 bg-zinc-800 text-white text-sm rounded-lg shadow-lg">{toast}</div>}
    </div>
    </SendingProvider>
  );
}

function SideBtn({ dataId, active, icon, title, onClick }: { dataId: string; active: boolean; icon: React.ReactNode; title: string; onClick: () => void }) {
  return (
    <button data-id={dataId} onClick={onClick} className={cn("p-2.5 rounded-xl transition-all relative cursor-pointer", active ? "text-zinc-300 bg-white/[0.06]" : "text-zinc-600 hover:text-zinc-400 hover:bg-white/[0.03]")} title={title}>
      {icon}
      {active && <div className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-5 bg-blue-500/60 rounded-r" />}
    </button>
  );
}

function Placeholder({ icon, text }: { icon: React.ReactNode; text: string }) {
  return <div data-id="placeholder" className="absolute inset-0 flex items-center justify-center text-zinc-600 flex-col gap-4 pointer-events-none">{icon}<p className="text-sm">{text}</p></div>;
}

function normalizeAgentType(agentType?: string) {
  switch ((agentType || '').trim().toLowerCase()) {
    case 'openclaw':
    case 'opencraw':
      return 'openclaw';
    case 'codex':
    case 'openai':
    case 'kiro-cli':
    case 'kiro-cli chat':
    case 'gemini':
    case 'copilot':
      return 'codex';
    case 'claude':
    case 'claude code':
    case 'claude-code':
      return 'claude';
    case 'cicy':
      return 'cicy';
    case 'opencode':
    case 'open code':
    case 'open-code':
      return 'opencode';
    default:
      return '';
  }
}

function getPaneStatus(statuses: Record<string, any>, paneId: string) {
  return statuses[`${paneId}:main.0`] || statuses[paneId] || {};
}

function resolvePaneMeta({
  paneId,
  agents,
  boundAgents,
  paneDetails,
  pollStatuses,
  agentDetail,
}: {
  paneId: string;
  agents: any[];
  boundAgents: any[];
  paneDetails: Record<string, any>;
  pollStatuses: Record<string, any>;
  agentDetail: any;
}) {
  const activePaneShortId = String(agentDetail?.pane_id || '').replace(/:.*$/, '');
  const detail = paneDetails[paneId] || (paneId === activePaneShortId ? agentDetail : null);
  const binding = boundAgents.find((item: any) => String(item?.name || item?.pane_id || '').replace(/:.*$/, '') === paneId);
  const agent = agents.find((item: any) => String(item?.pane_id || item?.id || '').replace(/:.*$/, '') === paneId);
  const status = getPaneStatus(pollStatuses, paneId);
  return {
    detail,
    binding,
    agent,
    status,
    title: detail?.title || binding?.title || status?.title || agent?.title || paneId,
    agentType: detail?.agent_type || agent?.agent_type,
    machineLabel: binding?.instance_label || binding?.machine_label || agent?.machine_label || agent?.instance_label || '',
    contextUsage: status?.contextUsage ?? null,
    workspace: detail?.workspace || agent?.workspace || `~/workers/${paneId}`,
    isApiOnly: !!(detail && detail.capabilities?.supports_tmux === false),
  };
}

function buildCanvasItems({
  paneId,
  token,
  canvasPaneIds,
  agents,
  boundAgents,
  paneDetails,
  pollStatuses,
  agentDetail,
}: {
  paneId: string;
  token: string | null;
  canvasPaneIds: string[];
  agents: any[];
  boundAgents: any[];
  paneDetails: Record<string, any>;
  pollStatuses: Record<string, any>;
  agentDetail: any;
}): AgentCanvasItem[] {
  return canvasPaneIds.map((targetPaneId) => {
    const meta = resolvePaneMeta({ paneId: targetPaneId, agents, boundAgents, paneDetails, pollStatuses, agentDetail });
    return {
      paneId: targetPaneId,
      title: meta.title,
      agentType: meta.agentType,
      status: meta.status?.status,
      contextUsage: meta.contextUsage,
      machineLabel: meta.machineLabel,
      ttydSrc: token && !meta.isApiOnly ? urls.ttydOpen(targetPaneId, token) : '',
      workspace: meta.workspace,
      isPrimary: targetPaneId === paneId,
      isApiOnly: meta.isApiOnly,
    };
  });
}

function buildCommandTargets({
  paneId,
  canvasPaneIds,
  agents,
  boundAgents,
  paneDetails,
  pollStatuses,
  agentDetail,
}: {
  paneId: string;
  canvasPaneIds: string[];
  agents: any[];
  boundAgents: any[];
  paneDetails: Record<string, any>;
  pollStatuses: Record<string, any>;
  agentDetail: any;
}) {
  return canvasPaneIds.map((targetPaneId) => {
    const meta = resolvePaneMeta({ paneId: targetPaneId, agents, boundAgents, paneDetails, pollStatuses, agentDetail });
    return {
      paneId: targetPaneId,
      title: meta.title,
      status: meta.status?.status || (targetPaneId === paneId ? 'idle' : ''),
    };
  });
}

function AgentListAvatar({ agentType, title }: { agentType?: string; title: string }) {
  const normalizedAgentType = normalizeAgentType(agentType);
  const baseClassName = 'flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border shadow-sm';

  if (!normalizedAgentType) {
    return (
      <div
        data-id="agent-avatar"
        className={`${baseClassName} border-white/[0.08] bg-white/[0.03] text-zinc-400`}
        title={title}
      >
        <span className="text-xs font-semibold uppercase">{title.slice(0, 1) || '?'}</span>
      </div>
    );
  }

  if (normalizedAgentType === 'openclaw') {
    return (
      <div
        data-id="agent-avatar"
        className={`${baseClassName} border-zinc-500/40 bg-zinc-300 text-zinc-950`}
        title="OpenClaw"
      >
        <span className="text-[20px] leading-none" aria-label="OpenClaw">🦞</span>
      </div>
    );
  }

  const iconMap: Record<string, { label: string; src: string; className?: string }> = {
    codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
    claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
    cicy: { label: 'CiCy', src: 'https://cicy-ai.com/logo.svg' },
    opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg'), className: 'h-7 w-7' },
  };
  const icon = iconMap[normalizedAgentType];
  if (!icon) return null;

  return (
    <div
      data-id="agent-avatar"
      className={`${baseClassName} border-zinc-500/40 bg-zinc-300`}
      title={icon.label}
    >
      <img src={icon.src} alt={icon.label} className={`${icon.className || 'h-6 w-6'} object-contain`} />
    </div>
  );
}

function AgentDrawer({ agents, paneId, onSelectAgent, on智能体Change, onOpenSettings }: {
  agents: any[]; paneId: string;
  onSelectAgent: (id: string) => void; on智能体Change: (a: any[]) => void;
  onOpenSettings: (id: string) => void;
}) {
  const [search, setSearch] = useState('');
  const [adding, setAdding] = useState(false);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  const { confirm } = useDialog();

  useEffect(() => {
    const closeMenu = () => setOpenMenuId(null);
    document.addEventListener('pointerdown', closeMenu);
    return () => document.removeEventListener('pointerdown', closeMenu);
  }, []);
  useDevRegister('AgentDrawer', {
    paneId,
    search,
    filteredCount: agents.length,
    adding,
    createDialogOpen,
    openMenuId,
  });

  const handleQuickAddMaster = async (values: CreateAgentValues) => {
    setAdding(true);
    try {
      const { data } = await apiService.createPane({
        role: 'worker',
        title: values.title,
        agent_type: values.agent_type,
        allow_all_actions: values.allow_all_actions,
        reply_in_chinese: values.reply_in_chinese,
      });
      const id = data?.pane_id || data?.id;
      if (id) {
        const { data: fresh } = await apiService.getPanes();
        on智能体Change(Array.isArray(fresh) ? fresh : fresh?.panes || []);
        setCreateDialogOpen(false);
        onSelectAgent(id.split(':')[0]);
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '创建员工失败' }));
    } finally { setAdding(false); }
  };

  const handleDelete = (id: string) => {
    const sid = id.split(':')[0];
    if (sid === 'w-10001') return;
    confirm(<>删除 <span className="text-zinc-100 font-medium">{sid}</span>？</>, async () => {
      try {
        await apiService.deletePane(id);
        const { data: fresh } = await apiService.getPanes();
        const list = Array.isArray(fresh) ? fresh : fresh?.panes || [];
        on智能体Change(list);
        if (sid === paneId) {
          const idx = agents.findIndex(a => (a.pane_id || a.id) === id);
          const next = agents[idx + 1] || agents[idx - 1];
          onSelectAgent(next ? (next.pane_id || next.id).split(':')[0] : 'w-10001');
        }
      } catch {}
    });
  };

  const handleRestart = (id: string, title: string) => {
    const sid = id.split(':')[0];
    confirm(<>重启 <span className="text-zinc-100 font-medium">{title || sid}</span>？</>, async () => {
      try {
        await apiService.restartPane(sid);
        window.dispatchEvent(new CustomEvent('show-toast', { detail: `${title || sid} 正在重启...` }));
      } catch {
        window.dispatchEvent(new CustomEvent('show-toast', { detail: `错误：${title || sid} 重启失败` }));
      }
    });
  };

  const q = search.toLowerCase();
  const filtered = agents.filter(a => {
    if (!q) return true;
    const id = (a.pane_id || a.id || '').toLowerCase();
    const title = (a.title || '').toLowerCase();
    return id.includes(q) || title.includes(q);
  });

  return (
    <>
      <div data-id="agent-drawer" className="h-full flex flex-col bg-[#0A0A0A]">
        <div data-id="agent-drawer-toolbar" className="px-3 py-2 border-b border-[var(--vsc-border)] shrink-0 bg-[#0A0A0A]">
          <div data-id="agent-search" className="relative flex gap-2">
            <div className="relative flex-1">
              <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600" />
              <input
                type="search"
                placeholder="搜索 ID 或标题..."
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="w-full bg-white/[0.02] border border-[var(--vsc-border)] rounded-lg pl-9 pr-3 py-2 text-sm focus:outline-none focus:border-white/[0.08] placeholder:text-zinc-700 text-zinc-400"
              />
            </div>
            <button onClick={() => setCreateDialogOpen(true)} disabled={adding}
              className="flex items-center gap-1 px-2 py-1.5 rounded-lg border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50 shrink-0"
              title="添加员工">
              <Plus className="w-4 h-4" />
            </button>
          </div>
        </div>
        <div data-id="agent-drawer-body" className="flex-1 overflow-y-auto bg-[#0A0A0A] px-1.5 py-1.5">
          <div data-id="agent-list" className="space-y-2">
            {filtered.map((agent: any) => {
              const id = agent.pane_id || agent.id;
              const shortId = id?.replace(':main.0', '') || id;
              const isMaster = id?.includes('10001');
              const isActive = id === paneId || id?.startsWith(paneId + ':') || paneId?.startsWith(id + ':');
              return (
                <div key={id} data-id={`agent-${id}`}
                  className={cn("w-full flex items-center gap-3 border p-3 rounded-xl transition-all group relative",
                    isActive ? "border-blue-500/50 bg-blue-500/[0.08] ring-1 ring-blue-500/20" : "bg-white/[0.02] border-[var(--vsc-border)] hover:border-white/[0.08]")}>
                  <div
                    data-id={`agent-menu-${shortId}`}
                    className="absolute right-2 top-2 z-20"
                    onPointerDown={e => e.stopPropagation()}
                    onClick={e => e.stopPropagation()}
                  >
                    <button
                      type="button"
                      data-id="agent-menu-button"
                      onClick={() => setOpenMenuId(prev => prev === id ? null : id)}
                      className={cn(
                        "flex h-7 w-7 items-center justify-center rounded-lg transition-all cursor-pointer",
                        openMenuId === id
                          ? "bg-white/[0.08] text-zinc-200"
                          : "text-zinc-700 opacity-0 group-hover:opacity-100 hover:bg-white/[0.05] hover:text-zinc-300"
                      )}
                      title="菜单">
                      <MoreHorizontal className="w-3.5 h-3.5" />
                    </button>
                    {openMenuId === id ? (
                      <div
                        data-id="agent-menu-dropdown"
                        className="absolute right-0 top-9 min-w-[190px] overflow-hidden rounded-xl border border-white/[0.08] bg-[#111113]/98 p-1.5 shadow-2xl backdrop-blur-xl"
                      >
                        <button
                          type="button"
                          data-id="agent-menu-open"
                          onClick={() => {
                            setOpenMenuId(null);
                            window.open(`#/agent/${id.split(':')[0]}`, '_blank');
                          }}
                          className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm text-zinc-300 transition-colors cursor-pointer hover:bg-white/[0.06]"
                        >
                          <ExternalLink className="w-3.5 h-3.5 shrink-0" />
                          <span>打开</span>
                        </button>
                        <button
                          type="button"
                          data-id="agent-menu-restart"
                          onClick={() => {
                            setOpenMenuId(null);
                            handleRestart(id, agent.title || shortId);
                          }}
                          className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm text-zinc-300 transition-colors cursor-pointer hover:bg-white/[0.06]"
                        >
                          <RotateCcw className="w-3.5 h-3.5 shrink-0" />
                          <span>重启</span>
                        </button>
                        <button
                          type="button"
                          data-id="agent-menu-settings"
                          onClick={() => {
                            setOpenMenuId(null);
                            onOpenSettings(id);
                          }}
                          className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm text-zinc-300 transition-colors cursor-pointer hover:bg-white/[0.06]"
                        >
                          <Settings className="w-3.5 h-3.5 shrink-0" />
                          <span>设置</span>
                        </button>
                        {!isMaster ? (
                          <button
                            type="button"
                            data-id="agent-menu-delete"
                            onClick={() => {
                              setOpenMenuId(null);
                              handleDelete(id);
                            }}
                            className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm text-red-300 transition-colors cursor-pointer hover:bg-red-500/10 hover:text-red-200"
                          >
                            <X className="w-3.5 h-3.5 shrink-0" />
                            <span>删除</span>
                          </button>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-3 flex-1 min-w-0 text-left">
                    <AgentListAvatar agentType={agent.agent_type} title={agent.title || shortId} />
	                    <div className="flex-1 min-w-0 pr-7">
	                      <div className="flex items-center gap-1.5">
	                        <h3 className={cn("text-sm font-medium truncate", isActive ? "text-blue-300" : "text-zinc-300")}>{agent.title || shortId}</h3>
	                      </div>
	                      <p className={cn("text-xs font-mono mt-0.5 truncate", isActive ? "text-blue-400/50" : "text-zinc-600")}>{shortId}</p>
	                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>
      <CreateAgentDialog
        open={createDialogOpen}
        submitting={adding}
        onClose={() => setCreateDialogOpen(false)}
        onSubmit={handleQuickAddMaster}
        title="创建员工"
        submitLabel="创建"
      />
    </>
  );
}

interface SystemResourceSnapshot {
  cpu_usage_pct: number;
  cpu_cores: number;
  mem_usage_pct: number;
  mem_total_bytes: number;
  mem_used_bytes: number;
  disk_usage_pct: number;
  disk_total_bytes: number;
  disk_used_bytes: number;
  load_1: number;
  load_5: number;
  load_15: number;
  updated_at: string;
}

function formatResourcePct(value: number | null | undefined) {
  if (value == null || Number.isNaN(value)) return '--';
  return `${Math.round(value)}%`;
}

function formatResourceBytes(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value) || value <= 0) return '--';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let next = value;
  let unit = units[0];
  for (let i = 0; i < units.length; i += 1) {
    unit = units[i];
    if (next < 1024 || i === units.length - 1) break;
    next /= 1024;
  }
  const digits = next >= 100 ? 0 : next >= 10 ? 1 : 2;
  return `${next.toFixed(digits)} ${unit}`;
}

function formatLoadValue(value: number | null | undefined) {
  if (value == null || Number.isNaN(value)) return '--';
  return value.toFixed(2);
}

function SystemResourceMonitor({ token }: { token: string | null }) {
  const [metrics, setMetrics] = useState<SystemResourceSnapshot | null>(null);
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('pointerdown', handlePointerDown);
    return () => document.removeEventListener('pointerdown', handlePointerDown);
  }, []);

  useEffect(() => {
    if (!token) return;
    let dead = false;
    let reconnectTimer: number | null = null;
    let ws: WebSocket | null = null;

    const loadSnapshot = async () => {
      try {
        const { data } = await apiService.getSystemResources({ timeout: 1500 });
        if (!dead) setMetrics(data);
      } catch {}
    };

    const connect = () => {
      if (dead) return;
      const httpBase = config.apiBase || window.location.origin;
      const proto = httpBase.startsWith('https') ? 'wss' : (window.location.protocol === 'https:' ? 'wss' : 'ws');
      const base = httpBase ? httpBase.replace(/^https?/, proto) : `${proto}://${window.location.host}`;
      ws = new WebSocket(`${base}/api/system/resources/ws?token=${encodeURIComponent(token)}`);
      ws.onmessage = (event) => {
        try {
          const next = JSON.parse(event.data);
          if (!dead) setMetrics(next);
        } catch {}
      };
      ws.onclose = () => {
        if (dead) return;
        reconnectTimer = window.setTimeout(connect, 3000);
      };
      ws.onerror = () => ws?.close();
    };

    void loadSnapshot();
    connect();
    return () => {
      dead = true;
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      ws?.close();
    };
  }, [token]);

  const cpu = formatResourcePct(metrics?.cpu_usage_pct);
  const memory = formatResourcePct(metrics?.mem_usage_pct);
  const disk = formatResourcePct(metrics?.disk_usage_pct);
  const updatedAt = metrics?.updated_at ? new Date(metrics.updated_at).toLocaleTimeString('zh-CN', { hour12: false }) : '--';

  return (
    <div data-id="system-resource-root" ref={rootRef} className="relative">
      <div
        data-id="system-resource-trigger"
        role="button"
        tabIndex={0}
        onClick={() => setOpen(prev => !prev)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            setOpen(prev => !prev);
          }
        }}
        className="flex items-center gap-2 rounded-full border border-white/[0.08] bg-white/[0.03] px-2.5 py-1 text-[10px] font-mono text-zinc-500 transition-colors cursor-pointer hover:border-white/[0.12] hover:text-zinc-300"
        title="系统资源"
      >
        <span data-id="system-resource-summary-cpu">CPU {cpu}</span>
        <span data-id="system-resource-summary-memory">MEM {memory}</span>
        <span data-id="system-resource-summary-disk">DSK {disk}</span>
        <ChevronDown data-id="system-resource-chevron" className={`h-3 w-3 transition-transform ${open ? 'rotate-180' : ''}`} />
      </div>
      {open ? (
        <div
          data-id="system-resource-dropdown"
          className="absolute right-0 top-[calc(100%+8px)] z-[180] min-w-[280px] rounded-2xl border border-white/[0.08] bg-[#111113]/98 p-3 shadow-2xl backdrop-blur-xl"
        >
          <div data-id="system-resource-dropdown-header" className="mb-2 flex items-center justify-between">
            <span className="text-xs font-medium text-zinc-300">系统资源</span>
            <span data-id="system-resource-updated-at" className="text-[10px] font-mono text-zinc-600">{updatedAt}</span>
          </div>
          <div data-id="system-resource-grid" className="grid grid-cols-[auto,1fr] gap-x-3 gap-y-1.5 text-[11px]">
            <span data-id="system-resource-label-cpu" className="text-zinc-600">CPU</span>
            <span data-id="system-resource-value-cpu" className="font-mono text-zinc-300">{cpu} · {metrics?.cpu_cores ?? '--'} cores</span>

            <span data-id="system-resource-label-memory" className="text-zinc-600">内存</span>
            <span data-id="system-resource-value-memory" className="font-mono text-zinc-300">
              {memory} · {formatResourceBytes(metrics?.mem_used_bytes)} / {formatResourceBytes(metrics?.mem_total_bytes)}
            </span>

            <span data-id="system-resource-label-disk" className="text-zinc-600">磁盘</span>
            <span data-id="system-resource-value-disk" className="font-mono text-zinc-300">
              {disk} · {formatResourceBytes(metrics?.disk_used_bytes)} / {formatResourceBytes(metrics?.disk_total_bytes)}
            </span>

            <span data-id="system-resource-label-load" className="text-zinc-600">负载</span>
            <span data-id="system-resource-value-load" className="font-mono text-zinc-300">
              {formatLoadValue(metrics?.load_1)} / {formatLoadValue(metrics?.load_5)} / {formatLoadValue(metrics?.load_15)}
            </span>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function NetworkSignal({ latency }: { latency: number | null }) {
  const bars = latency === null ? 0 : latency < 100 ? 4 : latency < 200 ? 3 : latency < 500 ? 2 : 1;
  const color = bars >= 4 ? 'bg-emerald-400' : bars === 3 ? 'bg-emerald-400' : bars === 2 ? 'bg-yellow-400' : bars === 1 ? 'bg-red-400' : 'bg-zinc-700';
  const label = latency === null ? '离线' : `${latency}ms`;
  return (
    <div data-id="network-signal" className="flex items-center gap-1.5 h-4 cursor-default" title={label}>
      <div data-id="network-signal-bars" className="flex items-end gap-[2px] h-4">
        {[6, 8, 10, 12].map((h, i) => (
          <div key={i} data-id={`network-signal-bar-${i + 1}`} className={`w-[3px] rounded-sm transition-colors ${i < bars ? color : 'bg-zinc-800'}`} style={{ height: h }} />
        ))}
      </div>
      <span data-id="network-signal-label" className="mt-[5px] min-w-[28px] text-[10px] leading-none text-zinc-600">{label}</span>
    </div>
  );
}
