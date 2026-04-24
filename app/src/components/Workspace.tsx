import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { useApp } from '../contexts/AppContext';
import type { SystemResourceSnapshot } from '../contexts/AppContext';
import { createPortal } from 'react-dom';
import {
  Terminal, MessageSquare, Folder, FolderOpen, X, Settings, Brain, Search,
  LayoutList, Users, RotateCcw, Plus, ExternalLink, Key, Bug, Server, MoreHorizontal, ChevronDown, Github, Copy, Check
} from 'lucide-react';
import { assetUrl } from '../lib/assets';
import { cn } from '../lib/utils';
import { useDevRegister } from '../lib/devStore';
import { useAuth } from '../contexts/AuthContext';
import { SendingProvider } from '../contexts/SendingContext';
// import ChatView from './chat/ChatView';
import ChatHistoryView from './chat/ChatHistoryView';
import { WebFrame } from './WebFrame';
import { WindowManager } from './terminal/WindowManager';
import { VoiceFloatingButton } from './VoiceFloatingButton';
import TeamPanel from './layout/TeamPanel';
import SkillPanel from './layout/SkillPanel';
import AgentInspector, { InspectorTab } from './layout/AgentInspector';
import TokenDialog from './layout/TokenDialog';
import useDesktopEvents from './layout/useDesktopEvents';
import AgentCanvas, { AgentCanvasItem } from './layout/AgentCanvas';
import AgentStack from './layout/AgentStack';
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
const CLI_DRAWER_WIDTH_KEY = 'ws_cliDrawerWidth';
const CLI_DRAWER_MIN_WIDTH = 360;
const CLI_DRAWER_DEFAULT_WIDTH = 520;
const CLI_DRAWER_MAX_WIDTH = 960;
const TEAM_TERMINAL_ACTIVE_KEY = 'ws_teamTerminalActive';
const GITHUB_ISSUES_URL = 'https://github.com/cicy-ai/cicy-code/issues';
const UPGRADE_URL = 'https://cicy-ai.com/team/upgrade';
const RENEW_URL = 'https://cicy-ai.com/team/pay';

type MembershipBannerState = {
  kind: string | null;
  tag: string | null;
  expiresAt: string | null;
  showRenew: boolean;
  showUpgrade: boolean;
  renewUrl: string | null;
  upgradeUrl: string | null;
  syncedAt: string | null;
};

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

function formatClockTime(value: string | null): string {
  if (!value) return '';
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleTimeString('zh-CN', { hour12: false });
}

function parseEnvBool(value: unknown): boolean {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'number') return value !== 0;
  if (typeof value !== 'string') return false;
  return ['1', 'true', 'yes', 'on'].includes(value.trim().toLowerCase());
}

function clampCliDrawerWidth(value: number): number {
  if (!Number.isFinite(value)) return CLI_DRAWER_DEFAULT_WIDTH;
  return Math.min(CLI_DRAWER_MAX_WIDTH, Math.max(CLI_DRAWER_MIN_WIDTH, value));
}

function membershipTone(kind: string | null) {
  switch ((kind || '').trim().toLowerCase()) {
    case 'trial':
      return 'border-amber-400/25 bg-amber-400/10';
    case 'shared':
      return 'border-sky-400/25 bg-sky-400/10';
    case 'pro_vm':
      return 'border-emerald-400/25 bg-emerald-400/10';
    case 'private_deploy':
      return 'border-violet-400/25 bg-violet-400/10';
    default:
      return 'border-white/10 bg-white/[0.04]';
  }
}

function membershipTagTone(kind: string | null) {
  switch ((kind || '').trim().toLowerCase()) {
    case 'trial':
      return 'border-amber-300/40 bg-amber-300/15 text-amber-100';
    case 'shared':
      return 'border-sky-300/40 bg-sky-300/15 text-sky-100';
    case 'pro_vm':
      return 'border-emerald-300/40 bg-emerald-300/15 text-emerald-100';
    case 'private_deploy':
      return 'border-violet-300/40 bg-violet-300/15 text-violet-100';
    default:
      return 'border-white/15 bg-white/[0.06] text-zinc-200';
  }
}

function membershipExpireLabel(kind: string | null, expiresAt: string | null) {
  const expiresMs = resolveTrialExpiresMs(null, expiresAt);
  if (expiresMs === null) return '';
  const prefix = (kind || '').trim().toLowerCase() === 'trial' ? '试用到期' : '到期时间';
  return `${prefix} ${formatDateTime(expiresMs)}`;
}

interface Props { agentId: string; onSelectAgent: (id: string) => void; }
type LeftPanelView = 'team' | 'skills' | 'agents' | null;
type WorkspaceCliContentTab = InspectorTab | 'history' | 'files';

export default function Workspace({ agentId, onSelectAgent }: Props) {
  const {
    setChatWsState,
    setChatWsSender,
    sendChatWsMessage,
    broadcastChatWsMessage,
    systemResources,
    setSystemResources,
  } = useApp();
  const { token, hasPermission } = useAuth();
  const { confirm } = useDialog();
  const paneId = agentId || 'w-10001';
  const fullPaneId = `${paneId}:main.0`;
  const initialPaneIdRef = useRef(paneId);

  const mainTab = 'cli' as const;
  const [leftPanelView, setLeftPanelView] = useState<LeftPanelView>(() => {
    const v = cache.get('ws_leftPanel', null);
    if (v === 'team' || v === 'skills') return v;
    return 'team';
  });
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [inspectorRequestedTab, setInspectorRequestedTab] = useState<InspectorTab>('overview');
  const [cliContentOpen, setCliContentOpen] = useState(false);
  const [cliContentTab, setCliContentTab] = useState<WorkspaceCliContentTab>('files');
  const [cliDrawerWidth, setCliDrawerWidth] = useState(() => clampCliDrawerWidth(Number(cache.get(CLI_DRAWER_WIDTH_KEY, CLI_DRAWER_DEFAULT_WIDTH))));
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
  const [activeTeamPaneId, setActiveTeamPaneId] = useState<Record<string, string>>(() => cache.get(TEAM_TERMINAL_ACTIVE_KEY, {}));
  const [inspectorPaneId, setInspectorPaneId] = useState(paneId);
  const [canvasLocateRequest, setCanvasLocateRequest] = useState<{ paneId: string; nonce: number; zoomToActual?: boolean } | null>(null);
  const agentWorkspaceRef = useRef(`~/workers/${paneId}`);
  const prevCanvasPaneIdsRef = useRef<string[] | null>(null);
  const initialCanvasRestoreScopeRef = useRef<string | null>(null);
  const initialStackSelectionScopeRef = useRef<string | null>(null);
  const cliDrawerResizeRef = useRef<{ startX: number; startWidth: number } | null>(null);

  const handleOpenClawOpen = () => {
    if (!token) return;
    window.open(urls.openClaw(token), '_blank');
  };
  const [agentDetail, setAgentDetail] = useState<any>(null);
  const title = agentDetail?.title || '-';
  const [netLatency, setNetLatency] = useState<number | null>(null);
  const [chatWsConnected, setChatWsConnected] = useState(false);
  const [chatWsClientId, setChatWsClientId] = useState<string | null>(null);
  const [chatWsLiveStatus, setChatWsLiveStatus] = useState('idle');
  const [chatWsLiveText, setChatWsLiveText] = useState('');
  const [chatWsHistoryVersion, setChatWsHistoryVersion] = useState(0);
  const [chatWsInspectorVersion, setChatWsInspectorVersion] = useState(0);
  const [chatSuggestionText, setChatSuggestionText] = useState('');
  const [chatSuggestionPending, setChatSuggestionPending] = useState(false);
  const [chatSuggestionSending, setChatSuggestionSending] = useState(false);
  const chatWsRef = useRef<WebSocket | null>(null);
  const chatWsReconnectTimerRef = useRef<number | null>(null);
  const chatWsPingTimerRef = useRef<number | null>(null);
  const chatWsPingSentAtRef = useRef<number | null>(null);
  const chatWsPingRequestIdRef = useRef<string | null>(null);

  const [trialExpiresAt, setTrialExpiresAt] = useState<string | null>(null);
  const [trialExpiresAtEpoch, setTrialExpiresAtEpoch] = useState<number | null>(null);
  const [isPro, setIsPro] = useState<boolean | null>(null);
  const [membership, setMembership] = useState<MembershipBannerState>({
    kind: null,
    tag: null,
    expiresAt: null,
    showRenew: false,
    showUpgrade: false,
    renewUrl: null,
    upgradeUrl: null,
    syncedAt: null,
  });
  const [membershipMenuOpen, setMembershipMenuOpen] = useState(false);
  const [membershipRefreshing, setMembershipRefreshing] = useState(false);
  const membershipMenuRef = useRef<HTMLDivElement>(null);
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
  useEffect(() => { cache.set(TEAM_TERMINAL_ACTIVE_KEY, activeTeamPaneId); }, [activeTeamPaneId]);
  useEffect(() => { cache.set(CLI_DRAWER_WIDTH_KEY, cliDrawerWidth); }, [cliDrawerWidth]);
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
  useEffect(() => {
    const handleMouseMove = (event: MouseEvent) => {
      const resizeState = cliDrawerResizeRef.current;
      if (!resizeState) return;
      const nextWidth = clampCliDrawerWidth(resizeState.startWidth + (resizeState.startX - event.clientX));
      setCliDrawerWidth(nextWidth);
    };
    const stopResize = () => {
      if (!cliDrawerResizeRef.current) return;
      cliDrawerResizeRef.current = null;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', stopResize);
    return () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', stopResize);
      stopResize();
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
  useEffect(() => { void refreshPanes(); }, [refreshPanes, paneId]);
  useEffect(() => { 
    apiService.getPane(fullPaneId).then(({ data }) => { 
      setAgentDetail(data);
      setPaneDetails(prev => ({ ...prev, [paneId]: data }));
      const workspace = data?.workspace || `~/workers/${paneId}`;
      syncHostHomeFromPath(workspace);
      agentWorkspaceRef.current = workspace;
    }).catch(() => {}); 
  }, [fullPaneId, paneId]);
  const prevPaneId = useRef(paneId);
  useEffect(() => {
    if (prevPaneId.current !== paneId) {
      setAgentDetail(null); setStatus('idle'); setContextUsage(null);
      prevPaneId.current = paneId;
    }
  }, [paneId]);
  useEffect(() => {
    // 5 秒 WS 轮询兜底 + 页面可见时立即请求
    const sendPollRequest = () => {
      console.log('[poll_request] sending via WS, readyState:', chatWsRef.current?.readyState);
      try { chatWsRef.current?.send(JSON.stringify({ type: 'poll_request' })); } catch (e) { console.warn('[poll_request] send failed:', e); } 
    };
    const onRefresh = () => sendPollRequest();
    const onVisible = () => {
      if (document.visibilityState === 'visible') sendPollRequest();
    };
    const timer = window.setInterval(sendPollRequest, 5000);
    window.addEventListener('refresh-panes', onRefresh);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('refresh-panes', onRefresh);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, []);

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

    const storedActivePaneId = activeTeamPaneId[paneId];
    const nextStoredPaneId = (
      storedActivePaneId
      && canvasPaneIds.includes(storedActivePaneId)
    ) ? storedActivePaneId : paneId;

    const addedPaneIds = canvasPaneIds.filter((id) => id !== paneId && !prev.includes(id));
    if (addedPaneIds.length > 0) {
      if (initialStackSelectionScopeRef.current !== paneId) {
        initialStackSelectionScopeRef.current = paneId;
        setActiveTeamPaneId(current => (
          current[paneId] === nextStoredPaneId ? current : { ...current, [paneId]: nextStoredPaneId }
        ));
        setCanvasLocateRequest({ paneId: nextStoredPaneId, nonce: Date.now(), zoomToActual: true });
        return;
      }
      const nextPaneId = addedPaneIds[addedPaneIds.length - 1];
      setActiveTeamPaneId(current => ({ ...current, [paneId]: nextPaneId }));
      setCanvasLocateRequest({ paneId: nextPaneId, nonce: Date.now(), zoomToActual: true });
      return;
    }

    const removedPaneIds = prev.filter((id) => id !== paneId && !canvasPaneIds.includes(id));
    if (removedPaneIds.length === 0) return;

    setActiveTeamPaneId(current => (
      current[paneId] === nextStoredPaneId ? current : { ...current, [paneId]: nextStoredPaneId }
    ));
    setCanvasLocateRequest({ paneId: nextStoredPaneId, nonce: Date.now(), zoomToActual: true });
  }, [canvasPaneIds, paneId, activeTeamPaneId]);
  const activeCliPaneId = canvasPaneIds.includes(activeTeamPaneId[paneId]) ? activeTeamPaneId[paneId] : paneId;
  useEffect(() => {
    setInspectorPaneId(activeCliPaneId || paneId);
  }, [activeCliPaneId, paneId]);
  const openInspectorForPane = useCallback((targetPaneId: string, nextTab: InspectorTab = 'overview') => {
    const cleanPaneId = targetPaneId.replace(/:.*$/, '');
    setInspectorPaneId(cleanPaneId);
    setInspectorRequestedTab(nextTab);
    setInspectorOpen(true);
  }, []);
  const toggleInspectorSettings = useCallback((targetPaneId: string) => {
    const cleanPaneId = targetPaneId.replace(/:.*$/, '');
    const shouldClose = inspectorOpen && inspectorPaneId === cleanPaneId && inspectorRequestedTab === 'settings';
    setInspectorPaneId(cleanPaneId);
    setInspectorRequestedTab('settings');
    setInspectorOpen(!shouldClose);
  }, [inspectorOpen, inspectorPaneId, inspectorRequestedTab]);
  useEffect(() => {
    initialCanvasRestoreScopeRef.current = null;
    initialStackSelectionScopeRef.current = null;
  }, [paneId]);
  useEffect(() => {
    if (initialCanvasRestoreScopeRef.current === paneId) return;
    const storedActivePaneId = activeTeamPaneId[paneId];
    if (storedActivePaneId && storedActivePaneId !== paneId && !canvasPaneIds.includes(storedActivePaneId)) {
      return;
    }
    initialCanvasRestoreScopeRef.current = paneId;
    setCanvasLocateRequest({ paneId: activeCliPaneId, nonce: Date.now(), zoomToActual: true });
  }, [canvasPaneIds, activeCliPaneId, paneId, activeTeamPaneId]);

  useEffect(() => {
    const send = (payload: unknown) => {
      const ws = chatWsRef.current;
      if (ws?.readyState !== WebSocket.OPEN) return false;
      ws.send(JSON.stringify(payload));
      return true;
    };
    setChatWsSender(send);
    return () => {
      setChatWsSender(() => false);
    };
  }, [setChatWsSender]);

  useEffect(() => {
    const visionHandler = (e: Event) => {
      const detail = (e as CustomEvent).detail;
      sendChatWsMessage({ type: 'gemini_vision_result', data: detail });
    };
    const askHandler = (e: Event) => {
      const detail = (e as CustomEvent).detail;
      sendChatWsMessage({ type: 'gemini_ask_result', data: detail });
    };
    const pongHandler = (e: Event) => {
      const detail = (e as CustomEvent).detail;
      sendChatWsMessage({ type: 'pong', data: detail });
    };
    const ipcPongHandler = (e: Event) => {
      const detail = (e as CustomEvent).detail;
      sendChatWsMessage({ type: 'ipc_pong', data: detail });
    };
    window.addEventListener('gemini-vision-result', visionHandler as EventListener);
    window.addEventListener('gemini-ask-result', askHandler as EventListener);
    window.addEventListener('agent-pong', pongHandler as EventListener);
    window.addEventListener('ipc-pong', ipcPongHandler as EventListener);
    return () => {
      window.removeEventListener('gemini-vision-result', visionHandler as EventListener);
      window.removeEventListener('gemini-ask-result', askHandler as EventListener);
      window.removeEventListener('agent-pong', pongHandler as EventListener);
      window.removeEventListener('ipc-pong', ipcPongHandler as EventListener);
    };
  }, [sendChatWsMessage]);

  useEffect(() => {
    if (!token || !paneId) {
      if (chatWsReconnectTimerRef.current !== null) {
        window.clearTimeout(chatWsReconnectTimerRef.current);
        chatWsReconnectTimerRef.current = null;
      }
      chatWsRef.current?.close();
      chatWsRef.current = null;
      setChatWsConnected(false);
      setChatWsClientId(null);
      setNetLatency(null);
      setChatWsLiveStatus('idle');
      setChatWsLiveText('');
      setChatWsState({
        activeChatPaneId: null,
        chatWsConnected: false,
        chatWsClientId: null,
        chatWsLiveStatus: 'idle',
        chatWsLiveText: '',
      });
      return;
    }
    const agentId = paneId.replace(/:.*$/, '');
    const clientId = (() => {
      const key = `cicy_chat_client_id:${paneId}`;
      try {
        const current = sessionStorage.getItem(key);
        if (current) return current;
        const next = `web-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
        sessionStorage.setItem(key, next);
        return next;
      } catch {
        return `web-${Date.now().toString(36)}`;
      }
    })();
    const proto = config.apiBase.startsWith('https') ? 'wss' : (window.location.protocol === 'https:' ? 'wss' : 'ws');
    const base = config.apiBase.replace(/^https?/, proto);
    const isElectron = typeof (window as any).electronRPC === 'function' ? '1' : '0';
    let dead = false;

    const connect = () => {
      if (dead) return;
      if (chatWsReconnectTimerRef.current !== null) {
        window.clearTimeout(chatWsReconnectTimerRef.current);
        chatWsReconnectTimerRef.current = null;
      }
      if (chatWsPingTimerRef.current !== null) {
        window.clearInterval(chatWsPingTimerRef.current);
        chatWsPingTimerRef.current = null;
      }
      chatWsPingSentAtRef.current = null;
      chatWsPingRequestIdRef.current = null;
      chatWsRef.current?.close();
      const ws = new WebSocket(`${base}/api/chat/ws?agent_id=${encodeURIComponent(agentId)}&token=${encodeURIComponent(token)}&electron=${isElectron}&client_id=${encodeURIComponent(clientId)}`);
      chatWsRef.current = ws;
      const sendLatencyPing = () => {
        if (dead || chatWsRef.current !== ws || ws.readyState !== WebSocket.OPEN) return;
        const requestId = `ping-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
        chatWsPingRequestIdRef.current = requestId;
        chatWsPingSentAtRef.current = performance.now();
        try {
          ws.send(JSON.stringify({ type: 'ping', data: { requestId } }));
        } catch {}
      };
      ws.onopen = () => {
        if (dead || chatWsRef.current !== ws) return;
        setChatWsConnected(true);
        setChatWsClientId(clientId);
        setChatWsState({
          activeChatPaneId: paneId,
          chatWsConnected: true,
          chatWsClientId: clientId,
        });
        // 连接建立后立即请求 poll 数据
        console.log('[poll_request] WS onopen, sending initial poll_request');
        try { ws.send(JSON.stringify({ type: 'poll_request' })); } catch (e) { console.warn('[poll_request] onopen send failed:', e); }
        sendLatencyPing();
        chatWsPingTimerRef.current = window.setInterval(sendLatencyPing, 5000);
      };
      ws.onmessage = (event) => {
        if (dead || chatWsRef.current !== ws) return;
        try {
          const msg = JSON.parse(String(event.data || ''));
          if (msg?.type === 'user_q') {
            setChatWsLiveStatus('pending');
            setChatWsLiveText('');
            setChatSuggestionText('');
            setChatSuggestionPending(false);
            setChatWsState({
              activeChatPaneId: paneId,
              chatWsLiveStatus: 'pending',
              chatWsLiveText: '',
            });
          } else if (msg?.type === 'ai_chunk') {
            const delta = String(msg.data?.delta || '');
            if (delta) {
              setChatWsLiveText((prev) => {
                const next = `${prev}${delta}`;
                setChatWsState({ activeChatPaneId: paneId, chatWsLiveStatus: 'streaming', chatWsLiveText: next });
                return next;
              });
            }
            setChatWsLiveStatus('streaming');
          } else if (msg?.type === 'status_change' && msg.data) {
            window.dispatchEvent(new CustomEvent('agent-status-change', { detail: msg.data }));
            const nextStatus = String(msg.data?.status || '').toLowerCase();
            if (nextStatus === 'thinking') setChatWsLiveStatus('pending');
            else if (nextStatus === 'working' || nextStatus === 'tool_call' || nextStatus === 'tool_use') setChatWsLiveStatus('tool_use');
            else if (nextStatus === 'streaming') setChatWsLiveStatus('streaming');
            else if (nextStatus === 'idle' || nextStatus === 'done' || nextStatus === 'completed') setChatWsLiveStatus('done');
            else if (nextStatus === 'failed' || nextStatus === 'error') setChatWsLiveStatus('failed');
            setChatWsState({ activeChatPaneId: paneId, chatWsLiveStatus: nextStatus === 'thinking' ? 'pending' : nextStatus === 'working' || nextStatus === 'tool_call' || nextStatus === 'tool_use' ? 'tool_use' : nextStatus === 'streaming' ? 'streaming' : nextStatus === 'failed' || nextStatus === 'error' ? 'failed' : 'done' });
            if (nextStatus === 'idle' || nextStatus === 'done' || nextStatus === 'completed' || nextStatus === 'failed') {
              setChatWsInspectorVersion((value) => {
                const next = value + 1;
                setChatWsState({ chatWsInspectorVersion: next });
                return next;
              });
            }
          } else if (msg?.type === 'current_updated') {
            setChatWsHistoryVersion((value) => {
              const next = value + 1;
              setChatWsState({ chatWsHistoryVersion: next });
              return next;
            });
            setChatWsInspectorVersion((value) => {
              const next = value + 1;
              setChatWsState({ chatWsInspectorVersion: next });
              return next;
            });
          } else if (msg?.type === 'ai_done') {
            setChatWsLiveStatus('done');
            setChatWsState({ activeChatPaneId: paneId, chatWsLiveStatus: 'done' });
            setChatWsHistoryVersion((value) => {
              const next = value + 1;
              setChatWsState({ chatWsHistoryVersion: next });
              return next;
            });
            setChatWsInspectorVersion((value) => {
              const next = value + 1;
              setChatWsState({ chatWsInspectorVersion: next });
              return next;
            });
            setChatSuggestionPending(true);
          } else if (msg?.type === 'desktop_event' && msg.data) {
            window.dispatchEvent(new CustomEvent('agent-desktop-event', { detail: msg.data }));
          } else if (msg?.type === 'worker_idle' && msg.data) {
            window.dispatchEvent(new CustomEvent('agent-worker-idle', { detail: msg.data }));
          } else if (msg?.type === 'webpage_ping') {
            const versionText = document.getElementById('version')?.textContent?.trim() || config.version;
            sendChatWsMessage({ type: 'webpage_pong', data: { requestId: msg.data?.requestId, version: versionText } });
          } else if (msg?.type === 'exec_js' && msg.data?.code) {
            try {
              const result = window.eval(msg.data.code);
              sendChatWsMessage({ type: 'exec_js_result', data: { requestId: msg.data?.requestId, result: String(result) } });
            } catch (error: any) {
              sendChatWsMessage({ type: 'exec_js_result', data: { requestId: msg.data?.requestId, error: error?.message || String(error) } });
            }
          } else if (msg?.type === 'pong' && msg.data?.requestId) {
            if (msg.data.requestId === chatWsPingRequestIdRef.current && chatWsPingSentAtRef.current != null) {
              setNetLatency(Math.max(0, Math.round(performance.now() - chatWsPingSentAtRef.current)));
              chatWsPingSentAtRef.current = null;
            }
          } else if (msg?.type === 'poll_data' && msg.data) {
            const data = msg.data;
            console.log('[poll_data]', data);
            setBoundAgents(Array.isArray(data.agents) ? data.agents : []);
            setPollStatuses(data.statuses && typeof data.statuses === 'object' ? data.statuses : {});
            if (data.system_resources && typeof data.system_resources === 'object') {
              setSystemResources(data.system_resources as SystemResourceSnapshot);
            }
            const st = data.statuses?.[fullPaneId] || data.statuses?.[paneId];
            if (st?.status) setStatus(st.status);
            if (st?.title) setAgentDetail((prev: any) => prev ? { ...prev, title: st.title } : { title: st.title });
            if (st?.contextUsage != null) setContextUsage(st.contextUsage);
            setTrialExpiresAt(typeof data.trial_expires_at === 'string' && data.trial_expires_at.trim() ? data.trial_expires_at.trim() : null);
            const epoch = typeof data.trial_expires_at_epoch === 'number' ? data.trial_expires_at_epoch : Number.parseInt(String(data.trial_expires_at_epoch ?? ''), 10);
            setTrialExpiresAtEpoch(Number.isFinite(epoch) && epoch > 0 ? epoch : null);
            setIsPro(parseIsPro(data.is_pro));
            setMembership({
              kind: typeof data.membership_kind === 'string' && data.membership_kind.trim() ? data.membership_kind.trim() : null,
              tag: typeof data.membership_tag === 'string' && data.membership_tag.trim() ? data.membership_tag.trim() : null,
              expiresAt: typeof data.membership_expires_at === 'string' && data.membership_expires_at.trim() ? data.membership_expires_at.trim() : null,
              showRenew: parseEnvBool(data.show_renew),
              showUpgrade: parseEnvBool(data.show_upgrade),
              renewUrl: typeof data.renew_url === 'string' && data.renew_url.trim() ? data.renew_url.trim() : null,
              upgradeUrl: typeof data.upgrade_url === 'string' && data.upgrade_url.trim() ? data.upgrade_url.trim() : null,
              syncedAt: typeof data.membership_synced_at === 'string' && data.membership_synced_at.trim() ? data.membership_synced_at.trim() : new Date().toISOString(),
            });
          }
          if (msg?.type === 'system_resources' && msg.data && typeof msg.data === 'object') {
            setSystemResources(msg.data as SystemResourceSnapshot);
          }
          broadcastChatWsMessage(msg);
        } catch {}
      };
      ws.onclose = () => {
        if (chatWsRef.current === ws) {
          chatWsRef.current = null;
        }
        if (chatWsPingTimerRef.current !== null) {
          window.clearInterval(chatWsPingTimerRef.current);
          chatWsPingTimerRef.current = null;
        }
        chatWsPingSentAtRef.current = null;
        chatWsPingRequestIdRef.current = null;
        setChatWsConnected(false);
        setNetLatency(null);
        setChatWsState({
          activeChatPaneId: paneId,
          chatWsConnected: false,
          chatWsClientId: clientId,
        });
        if (!dead) {
          chatWsReconnectTimerRef.current = window.setTimeout(connect, 3000);
        }
      };
      ws.onerror = () => ws.close();
    };

    setChatWsConnected(false);
    setNetLatency(null);
    setChatWsLiveStatus('idle');
    setChatWsLiveText('');
    setChatWsState({
      activeChatPaneId: paneId,
      chatWsConnected: false,
      chatWsClientId: clientId,
      chatWsLiveStatus: 'idle',
      chatWsLiveText: '',
    });
    connect();
    return () => {
      dead = true;
      if (chatWsReconnectTimerRef.current !== null) {
        window.clearTimeout(chatWsReconnectTimerRef.current);
        chatWsReconnectTimerRef.current = null;
      }
      if (chatWsPingTimerRef.current !== null) {
        window.clearInterval(chatWsPingTimerRef.current);
        chatWsPingTimerRef.current = null;
      }
      chatWsPingSentAtRef.current = null;
      chatWsPingRequestIdRef.current = null;
      if (chatWsRef.current) {
        const closingWs = chatWsRef.current;
        chatWsRef.current = null;
        closingWs.close();
      }
      setChatWsConnected(false);
      setNetLatency(null);
      setChatWsState({
        activeChatPaneId: activeCliPaneId,
        chatWsConnected: false,
        chatWsClientId: clientId,
      });
    };
  }, [broadcastChatWsMessage, paneId, sendChatWsMessage, setChatWsSender, setChatWsState, token]);

  useEffect(() => {
    if (!token || !activeCliPaneId) return;
    let cancelled = false;
    const agentId = activeCliPaneId.replace(/:.*$/, '');
    const reloadSuggestion = async () => {
      try {
        const { data } = await apiService.getAgentHistoryView(agentId);
        if (cancelled) return;
        const turns = Array.isArray(data?.data) ? data.data : [];
        const suggestionTurn = [...turns].reverse().find((turn: any) => /\[SUGGESTION MODE:/i.test(String(turn?.q || '')));
        if (!suggestionTurn) {
          setChatSuggestionText('');
          setChatSuggestionPending(false);
          return;
        }
        const answerText = String(suggestionTurn?.a || '').trim();
        if (answerText && !/\[SUGGESTION MODE:/i.test(answerText)) {
          setChatSuggestionText(answerText);
          setChatSuggestionPending(false);
          return;
        }
        setChatSuggestionText('');
        setChatSuggestionPending(true);
      } catch {
        if (!cancelled) {
          setChatSuggestionText('');
        }
      }
    };
    void reloadSuggestion();
    return () => { cancelled = true; };
  }, [activeCliPaneId, chatWsHistoryVersion, token]);

  const handleExecuteSuggestion = useCallback(async (text: string) => {
    if (!text.trim()) return;
    setChatSuggestionSending(true);
    try {
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: activeCliPaneId, q: text } }));
      await sendCommandToTmux(text, activeCliPaneId);
      setChatSuggestionText('');
      setChatSuggestionPending(false);
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '已发送到当前 pane' }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '发送 suggestion 失败' }));
    } finally {
      setChatSuggestionSending(false);
    }
  }, [activeCliPaneId]);

  const topBarPaneId = activeCliPaneId || paneId;
  const topBarDetail = paneDetails[topBarPaneId] || (topBarPaneId === paneId ? agentDetail : null);
  const topBarTitle = topBarDetail?.title
    || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === topBarPaneId)?.title
    || (topBarPaneId === paneId ? title : topBarPaneId);
  const topBarWorkspace = topBarDetail?.workspace || `~/workers/${topBarPaneId}`;
  const topBarIsApiOnlyRuntime = !!(
    topBarDetail
    && topBarDetail.capabilities?.supports_tmux === false
  );
  const filePaneDetail = paneDetails[paneId] || agentDetail;
  const filePaneWorkspace = filePaneDetail?.workspace || `~/workers/${paneId}`;
  const filePaneIsApiOnlyRuntime = !!(
    filePaneDetail
    && filePaneDetail.capabilities?.supports_tmux === false
  );
  const fileCodeServerSrc = token && !filePaneIsApiOnlyRuntime
    ? urls.codeServer(filePaneWorkspace, token)
    : '';
  const openPaneInCurrentTerminal = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
  }, [paneId]);
  const openPaneHistory = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentTab('settings');
    setCliContentOpen(true);
  }, [paneId]);
  const handleCliDrawerResizeStart = useCallback((event: React.MouseEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.stopPropagation();
    cliDrawerResizeRef.current = {
      startX: event.clientX,
      startWidth: cliDrawerWidth,
    };
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, [cliDrawerWidth]);

  const locatePaneInCanvas = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCanvasLocateRequest({ paneId: clean, nonce: Date.now(), zoomToActual: true });
  }, [paneId]);
  useEffect(() => {
    if (!token || !topBarPaneId || topBarPaneId === paneId || paneDetails[topBarPaneId]) return;
    apiService.getPane(`${topBarPaneId}:main.0`).then(({ data }) => {
      setPaneDetails(prev => ({ ...prev, [topBarPaneId]: data }));
    }).catch(() => {});
  }, [paneId, paneDetails, topBarPaneId, token]);
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
  const membershipBanner = useMemo(() => {
    if (membership.kind) {
      return {
        kind: membership.kind,
        tag: membership.tag,
        expiresLabel: membershipExpireLabel(membership.kind, membership.expiresAt),
        showRenew: membership.showRenew,
        showUpgrade: membership.showUpgrade,
        renewUrl: membership.renewUrl || RENEW_URL,
        upgradeUrl: membership.upgradeUrl || UPGRADE_URL,
        syncedAt: membership.syncedAt,
      };
    }
    if (!showTrialUpgrade) {
      return null;
    }
    return {
      kind: isProUser ? 'pro_vm' : 'trial',
      tag: isProUser ? 'PRO' : '试用',
      expiresLabel: isProUser ? `到期时间 ${trialExpireAtLabel}` : `试用剩余 ${trialCountdown}`,
      showRenew: false,
      showUpgrade: true,
      renewUrl: RENEW_URL,
      upgradeUrl: UPGRADE_URL,
      syncedAt: membership.syncedAt,
    };
  }, [isProUser, membership, showTrialUpgrade, trialCountdown, trialExpireAtLabel]);

  useDevRegister('Workspace', {
    paneId: fullPaneId, title, status, contextUsage, mouseMode, isRestarting,
    agentDetail, netLatency,
    trialExpiresAt,
    trialExpiresAtEpoch,
    isPro,
    membership,
    trialCountdown,
    trialExpireAtLabel,
    isTrialUser,
    isProUser,
    showTrialUpgrade,
    agentsCount: agents.length,
    agents: agents.map((a: any) => ({ pane_id: a.pane_id, title: a.title, status: a.status, active: a.active })),
    leftPanel: leftActive, activeWinIdx,
    cliContentTab,
  }, {
    cliContentTab: setCliContentTab,
  });

  const handleRefreshMembership = useCallback(async () => {
    setMembershipRefreshing(true);
    try {
      chatWsRef.current?.send(JSON.stringify({ type: 'poll_request' }));
    } catch {}
    // 给 WS 响应一点时间
    setTimeout(() => setMembershipRefreshing(false), 500);
  }, []);
  const cliDrawerPortal = cliContentOpen ? createPortal(
    <div data-id="cli-content-portal" className="pointer-events-none fixed inset-y-0 right-0 z-[60] flex">
      <div
        data-id="cli-content-area"
        className="pointer-events-auto relative flex h-full min-w-0 flex-col border-l border-[var(--vsc-border)] bg-[#0b0b0d] shadow-[-20px_0_40px_rgba(0,0,0,0.45)]"
        style={{ width: `${cliDrawerWidth}px`, maxWidth: 'calc(100vw - 120px)' }}
      >
        <div
          data-id="cli-content-resize-handle"
          className="absolute left-0 top-0 bottom-0 w-2 cursor-col-resize"
          onMouseDown={handleCliDrawerResizeStart}
        />
        <div data-id="cli-content-tabs-wrap" className="flex items-center justify-between border-b border-[var(--vsc-border)] px-3 py-2">
          <div data-id="cli-content-tabs" className="flex gap-1 overflow-x-auto whitespace-nowrap scrollbar-none">
            {[
              { id: 'files', label: '文件' },
              { id: 'settings', label: '设置' },
            ].map((item) => (
              <button
                data-id={`cli-content-tab-${item.id}`}
                key={item.id}
                type="button"
                className={`shrink-0 rounded-md px-2.5 py-1.5 text-[11px] leading-5 ${
                  cliContentTab === item.id
                    ? 'bg-white/[0.08] text-zinc-100'
                    : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                }`}
                onClick={() => setCliContentTab(item.id as WorkspaceCliContentTab)}
              >
                {item.label}
              </button>
            ))}
          </div>
          <button
            data-id="cli-content-close"
            type="button"
            onClick={() => setCliContentOpen(false)}
            className="ml-3 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
            title="关闭"
            aria-label="关闭"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div data-id="cli-content-body" className="min-h-0 flex-1 relative">
          <div
            data-id="cli-content-files-host"
            className="absolute inset-0"
            style={{ display: cliContentTab === 'files' ? 'block' : 'none' }}
          >
            {fileCodeServerSrc ? (
              <div data-id="cli-content-files-pane" className="relative h-full w-full">
                <WebFrame
                  src={fileCodeServerSrc}
                  codeServer
                  className="h-full w-full border-0 bg-[#0A0A0A]"
                  title="文件"
                />
              </div>
            ) : (
              <div data-id="cli-content-files-empty" className="flex h-full items-center justify-center text-sm text-zinc-500">
                当前主 agent 没有可用的文件视图
              </div>
            )}
          </div>
          <div
            data-id="cli-content-settings-host"
            className="absolute inset-0"
            style={{ display: cliContentTab === 'settings' ? 'block' : 'none' }}
          >
            <AgentInspector
              paneId={activeCliPaneId}
              paneTitle={
                paneDetails[activeCliPaneId]?.title
                || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === activeCliPaneId)?.title
                || activeCliPaneId
              }
              open
              embedded
              requestedTab={'settings'}
              liveStatus={chatWsLiveStatus}
              inspectorVersion={chatWsInspectorVersion}
              onPanePatch={applyPanePatch}
              onClose={() => {}}
            />
          </div>
        </div>
      </div>
    </div>,
    document.body
  ) : null;
  const rightContent = (
    <div data-id="right-content" className="h-full flex flex-col relative">
      <header data-id="top-bar" className="h-12 border-b border-[var(--vsc-border)] bg-[#0A0A0A] flex items-center justify-between px-4 shrink-0 z-10">
        <div data-id="top-bar-left" className="flex items-center gap-3 w-1/3 min-w-0">
          {membershipBanner && (
            <div
              data-id="top-bar-membership-banner"
              className={cn('min-w-0 flex items-center gap-2 rounded-md border px-2 py-1', membershipTone(membershipBanner.kind))}
            >
              {membershipBanner.tag ? (
                <span
                  data-id="top-bar-membership-tag"
                  className={cn('shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase', membershipTagTone(membershipBanner.kind))}
                >
                  {membershipBanner.tag}
                </span>
              ) : null}
              {membershipBanner.expiresLabel ? (
                <span data-id="top-bar-membership-expire" className="min-w-0 truncate text-[11px] text-zinc-100 font-mono whitespace-nowrap">
                  {membershipBanner.expiresLabel}
                </span>
              ) : null}
              <div ref={membershipMenuRef} className="relative shrink-0">
                <button
                  type="button"
                  data-id="top-bar-membership-menu-btn"
                  onClick={() => setMembershipMenuOpen(prev => !prev)}
                  className="inline-flex items-center gap-1 rounded bg-white/10 px-2 py-1 text-[10px] font-semibold text-white transition-colors hover:bg-white/15"
                >
                  操作
                  <ChevronDown className={cn('h-3.5 w-3.5 transition-transform', membershipMenuOpen ? 'rotate-180' : '')} />
                </button>
                {membershipMenuOpen ? (
                  <div
                    data-id="top-bar-membership-dropdown"
                    className="absolute right-0 top-[calc(100%+8px)] z-40 min-w-[220px] rounded-xl border border-white/10 bg-[#101014]/95 p-2 shadow-[0_18px_48px_rgba(0,0,0,0.45)] backdrop-blur"
                  >
                    <div data-id="top-bar-membership-dropdown-meta" className="mb-2 rounded-lg border border-white/5 bg-white/[0.03] px-3 py-2">
                      <div className="text-[10px] font-semibold uppercase tracking-[0.18em] text-zinc-500">会员信息</div>
                      {membershipBanner.expiresLabel ? (
                        <div className="mt-1 text-[11px] font-mono text-zinc-100">{membershipBanner.expiresLabel}</div>
                      ) : null}
                      <div data-id="top-bar-membership-sync-time" className="mt-1 text-[10px] text-zinc-500">
                        {membershipBanner.syncedAt ? `更新于 ${formatClockTime(membershipBanner.syncedAt)}` : '自动刷新中'}
                      </div>
                    </div>
                    <button
                      type="button"
                      data-id="top-bar-membership-refresh-btn"
                      onClick={() => { void handleRefreshMembership(); }}
                      className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
                    >
                      <span>{membershipRefreshing ? '刷新中...' : '刷新信息'}</span>
                      <RotateCcw className={cn('h-3.5 w-3.5', membershipRefreshing ? 'animate-spin' : '')} />
                    </button>
                    {membershipBanner.showRenew ? (
                      <button
                        type="button"
                        data-id="top-bar-renew-btn"
                        onClick={() => {
                          setMembershipMenuOpen(false);
                          window.open(membershipBanner.renewUrl, '_blank', 'noopener,noreferrer');
                        }}
                        className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-sky-100 transition-colors hover:bg-sky-300/10"
                      >
                        <span>我要续费</span>
                        <ExternalLink className="h-3.5 w-3.5" />
                      </button>
                    ) : null}
                    {membershipBanner.showUpgrade ? (
                      <button
                        type="button"
                        data-id="top-bar-upgrade-btn"
                        onClick={() => {
                          setMembershipMenuOpen(false);
                          window.open(membershipBanner.upgradeUrl, '_blank', 'noopener,noreferrer');
                        }}
                        className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-amber-100 transition-colors hover:bg-amber-300/10"
                      >
                        <span>我要升级</span>
                        <ExternalLink className="h-3.5 w-3.5" />
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </div>
          )}
        </div>
        <div data-id="top-bar-center" className="flex items-center justify-center w-1/3" />
        <div data-id="top-bar-right" className="flex h-full items-center justify-end gap-3 w-1/3">
          <SystemResourceMonitor paneId={paneId} />
          <NetworkSignal latency={netLatency} connected={chatWsConnected} clientId={chatWsClientId} />
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
          <span id="version" className="text-[10px] font-mono leading-none text-zinc-600">{config.version}</span>
          {contextUsage != null && (
            <div data-id="context-usage" className="flex items-center gap-1.5 rounded-full bg-white/[0.02] px-2 py-0.5">
              <div data-id="context-bar" className="h-1 w-12 overflow-hidden rounded-full bg-white/[0.04]">
                <div className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400/60' : contextUsage > 50 ? 'bg-yellow-400/60' : 'bg-emerald-400/60'}`} style={{ width: `${contextUsage}%` }} />
              </div>
              <span data-id="context-pct" className="font-mono text-xs leading-none text-zinc-600">{contextUsage}%</span>
            </div>
          )}
        </div>
      </header>
      <div data-id="right-tabs" className="flex-1 relative overflow-hidden">
        <div data-id="chat-tab" className="absolute inset-0 flex justify-center" style={{ display: mainTab === 'chat' ? 'flex' : 'none' }}>
          <div className="w-full max-w-5xl h-full">
            {/* ChatView 引用先注释保留，暂时停用以阻断其内部 stats/chat 请求
            <ChatView paneId={paneId} token={token!} apiOnly={isApiOnlyRuntime} />
            */}
            <div data-id="chat-view-disabled" className="flex h-full items-center justify-center text-sm text-zinc-500">
              ChatView 已临时停用
            </div>
          </div>
        </div>
        <div data-id="cli-tab" className="absolute inset-0 flex" style={{ display: mainTab === 'cli' ? 'flex' : 'none' }}>
          <div data-id="cli-agent-stack" className="relative h-full min-w-0 flex-1 overflow-hidden bg-[#09090b]">
            <AgentStack
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
              activePaneId={activeCliPaneId}
              historyShortcutActive={cliContentOpen}
              onOpenPaneHistory={openPaneHistory}
              onActivePaneIdChange={(targetPaneId) => {
                setActiveTeamPaneId(prev => ({ ...prev, [paneId]: targetPaneId }));
              }}
            />
          </div>
        </div>
      </div>
    </div>
  );

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
                      <div data-id="left-panel-agents-view" className="absolute inset-0 overflow-auto">
                        <AgentDrawer agents={agents} paneId={paneId}
                          onSelectAgent={onSelectAgent}
                          on智能体Change={set智能体}
                          onOpenSettings={(targetPaneId) => {
                            onSelectAgent(targetPaneId);
                            openInspectorForPane(targetPaneId, 'settings');
                          }}
                        />
                      </div>
                    ) : leftActive === 'team' ? (
                      <div data-id="left-panel-team-view" className="absolute inset-0">
                        <TeamPanel
                          paneId={paneId}
                          panes={agents}
                          bindings={boundAgents}
                          statuses={pollStatuses}
                          onOpenInCurrentPane={openPaneInCurrentTerminal}
                          onLocatePane={locatePaneInCanvas}
                          openedPaneIds={canvasPaneIds.filter(id => id !== paneId)}
                          activePaneId={activeCliPaneId}
                          onRefreshPanes={refreshPanes}
                          onRefreshPoll={() => { try { chatWsRef.current?.send(JSON.stringify({ type: 'poll_request' })); } catch {} }}
                          onOpenSettingsPane={(targetPaneId) => {
                            openPaneInCurrentTerminal(targetPaneId);
                            openInspectorForPane(targetPaneId, 'settings');
                          }}
                        />
                      </div>
                    ) : (
                      <div data-id="left-panel-skills-view" className="absolute inset-0 overflow-auto">
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
      {cliDrawerPortal}
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

function normalizeAgentType(agentType?: string) {
  switch ((agentType || '').trim().toLowerCase()) {
    case 'openclaw':
    case 'opencraw':
      return 'openclaw';
    case 'codex':
    case 'openai':
      return 'codex';
    case 'kiro-cli':
    case 'kiro':
    case 'kiro-cli chat':
      return 'kiro-cli';
    case 'copilot':
    case 'github-copilot':
    case 'ghcopilot':
      return 'copilot';
    case 'gemini':
      return 'codex';
    case 'claude':
    case 'claude code':
    case 'claude-code':
      return 'claude';
    case 'cicy':
    case 'cicy-claude':
      return 'cicy-claude';
    case 'opencode':
    case 'open code':
    case 'open-code':
      return 'opencode';
    case 'hermes':
    case 'hermes-agent':
    case 'hermes agent':
      return 'hermes';
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

  const iconMap: Record<string, { label: string; src?: string; className?: string; textClassName?: string }> = {
    codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
    claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
    'cicy-claude': { label: 'CiCy', src: 'https://cicy-ai.com/logo.svg' },
    opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg'), className: 'h-7 w-7' },
    'kiro-cli': { label: 'Kiro', src: assetUrl('/assets/logos/kiro.png') },
    copilot: { label: 'Copilot', src: assetUrl('/assets/logos/copilot.svg') },
    hermes: { label: 'Hermes', textClassName: 'text-[15px] font-semibold tracking-[0.08em]' },
  };
  const icon = iconMap[normalizedAgentType];
  if (!icon) return null;

  return (
    <div
      data-id="agent-avatar"
      className={`${baseClassName} border-zinc-500/40 bg-zinc-300 text-zinc-950`}
      title={icon.label}
    >
      {icon.src ? (
        <img src={icon.src} alt={icon.label} className={`${icon.className || 'h-8 w-8'} object-contain`} />
      ) : (
        <span className={icon.textClassName || 'text-xs font-semibold uppercase'}>{icon.label.slice(0, 2).toUpperCase()}</span>
      )}
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

function SystemResourceMonitor({ paneId }: { paneId: string }) {
  const { activeChatPaneId, sendChatWsMessage, systemResources } = useApp();
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
    if (!paneId || activeChatPaneId !== paneId) return;
    const requestPoll = () => {
      sendChatWsMessage({ type: 'poll_request' });
    };
    requestPoll();
    if (!open) return;
    const onVisible = () => {
      if (document.visibilityState === 'visible') requestPoll();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [activeChatPaneId, open, paneId, sendChatWsMessage]);

  const cpu = formatResourcePct(systemResources?.cpu_usage_pct);
  const memory = formatResourcePct(systemResources?.mem_usage_pct);
  const disk = formatResourcePct(systemResources?.disk_usage_pct);
  const updatedAt = systemResources?.updated_at ? new Date(systemResources.updated_at).toLocaleTimeString('zh-CN', { hour12: false }) : '--';

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
            <span data-id="system-resource-value-cpu" className="font-mono text-zinc-300">{cpu} · {systemResources?.cpu_cores ?? '--'} cores</span>

            <span data-id="system-resource-label-memory" className="text-zinc-600">内存</span>
            <span data-id="system-resource-value-memory" className="font-mono text-zinc-300">
              {memory} · {formatResourceBytes(systemResources?.mem_used_bytes)} / {formatResourceBytes(systemResources?.mem_total_bytes)}
            </span>

            <span data-id="system-resource-label-disk" className="text-zinc-600">磁盘</span>
            <span data-id="system-resource-value-disk" className="font-mono text-zinc-300">
              {disk} · {formatResourceBytes(systemResources?.disk_used_bytes)} / {formatResourceBytes(systemResources?.disk_total_bytes)}
            </span>

            <span data-id="system-resource-label-load" className="text-zinc-600">负载</span>
            <span data-id="system-resource-value-load" className="font-mono text-zinc-300">
              {formatLoadValue(systemResources?.load_1)} / {formatLoadValue(systemResources?.load_5)} / {formatLoadValue(systemResources?.load_15)}
            </span>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function NetworkSignal({ latency, connected = true, clientId }: { latency: number | null; connected?: boolean; clientId?: string | null }) {
  const [copied, setCopied] = useState(false);
  const [open, setOpen] = useState(false);
  const bars = !connected ? 0 : latency === null ? 4 : latency < 100 ? 4 : latency < 200 ? 3 : latency < 500 ? 2 : 1;
  const color = bars >= 4 ? 'bg-emerald-400' : bars === 3 ? 'bg-emerald-400' : bars === 2 ? 'bg-yellow-400' : bars === 1 ? 'bg-red-400' : 'bg-zinc-700';
  const label = !connected ? '离线' : latency === null ? '在线' : `${latency}ms`;
  const copyPlainText = (text: string) => {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', 'true');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(textarea);
    return ok;
  };
  const handleCopy = async () => {
    if (!clientId) return;
    let ok = false;
    try {
      if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        await navigator.clipboard.writeText(clientId);
        ok = true;
      }
    } catch {}
    if (!ok) ok = copyPlainText(clientId);
    if (!ok) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  };
  return (
    <div
      data-id="network-signal"
      className="relative flex h-5 items-center gap-1.5 pr-1"
      onPointerEnter={() => setOpen(true)}
      onPointerLeave={() => setOpen(false)}
    >
      <div data-id="network-signal-bars" className="flex items-end gap-[2px] h-4">
        {[6, 8, 10, 12].map((h, i) => (
          <div key={i} data-id={`network-signal-bar-${i + 1}`} className={`w-[3px] rounded-sm transition-colors ${i < bars ? color : 'bg-zinc-800'}`} style={{ height: h }} />
        ))}
      </div>
      <span
        data-id="network-signal-label"
        className="min-w-[28px] text-[10px] leading-none text-zinc-600"
      >
        {label}
      </span>
      {open ? (
        <div className="absolute right-0 top-full z-[180] min-w-[240px] rounded-lg border border-white/[0.08] bg-[#111113]/98 px-3 py-2 text-[11px] shadow-2xl backdrop-blur-xl">
          <div className="text-zinc-500">WebSocket</div>
          <div className="mt-1 flex items-start gap-2">
            <div className="min-w-0 flex-1 font-mono text-zinc-200 break-all">{clientId || '未连接 client_id'}</div>
            <button
              type="button"
              data-id="network-signal-copy-client-id"
              className="shrink-0 rounded p-1 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 transition-colors"
              onClick={() => { void handleCopy(); }}
              title="复制 client_id"
            >
              {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
            </button>
          </div>
          <div className="mt-1 text-zinc-500">{connected ? '已连接' : '未连接'}</div>
          {copied ? <div className="mt-1 text-emerald-400">已复制 client_id</div> : null}
        </div>
      ) : null}
    </div>
  );
}
