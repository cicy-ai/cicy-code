import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation, Trans } from 'react-i18next';

type ToastState = {
  message: string;
  variant?: 'default' | 'success';
};
import { useApp } from '../contexts/AppContext';
import type { SystemResourceSnapshot } from '../contexts/AppContext';
import {
  Terminal, MessageSquare, Folder, FolderOpen, X, Settings, Brain, Search,
  LayoutList, Users, User, Plus, ExternalLink, Key, Bug, Server, MoreHorizontal, ChevronDown, Github, Copy, Check, Send, RotateCcw, Boxes, MessageCircle
} from 'lucide-react';
import { cn } from '../lib/utils';
import AgentAvatar from './AgentAvatar';
import { useDevRegister, devStore } from '../lib/devStore';
import { useAuth } from '../contexts/AuthContext';
import { SendingProvider } from '../contexts/SendingContext';
// import ChatView from './chat/ChatView';
import ChatHistoryView from './chat/ChatHistoryView';
import CurrentHistoryView from './chat/CurrentHistoryView';
import { WebFrame } from './WebFrame';
import { WindowManager } from './terminal/WindowManager';
import { VoiceFloatingButton } from './VoiceFloatingButton';
import TeamPanel from './layout/TeamPanel';
import SkillPanel from './layout/SkillPanel';
import AgentInspector, { InspectorTab } from './layout/AgentInspector';
import AgentProviderRequestView, { type RequestViewTab } from './layout/AgentProviderRequestView';
import TokenDialog from './layout/TokenDialog';
import useDesktopEvents from './layout/useDesktopEvents';
import AgentCanvas, { AgentCanvasItem } from './layout/AgentCanvas';
import AgentStack from './layout/AgentStack';
import ProviderDashboard from './providers/ProviderDashboard';
import IMDashboard from './im/IMDashboard';
import { useDialog } from '../contexts/DialogContext';
import config, { defaultWorkerWorkspace, getHostHome, syncHostHomeFromPath, toTildePath, urls } from '../config';
import apiService from '../services/api';
import { sendCommandToTmux } from '../services/mockApi';
import { ApiSwitchDialog } from './layout/ApiSwitchDialog';
import CreateAgentDialog, { CreateAgentValues } from './CreateAgentDialog';
import { lockPointer, unlockPointer, clearPointerLock } from '../lib/pointerLock';
import { emitWebFrameMaskEvent } from '../lib/webFrameMask';

const cache = {
  get: (k: string, def: any) => { try { const v = JSON.parse(localStorage.getItem(k)!); return v ?? def; } catch { return def; } },
  set: (k: string, v: any) => localStorage.setItem(k, JSON.stringify(v)),
};

const LEFT_PANEL_WIDTH = 320;
const CLI_DRAWER_WIDTH_KEY = 'ws_cliDrawerWidth';
const CLI_CONTENT_MODE_KEY = 'ws_cliContentMode';
const cliContentTabKey = (paneId: string) => `TeamPanel:${paneId}.paneId:cliContentTab`;
const chatClientIdStorageKey = (masterAgentId: string) => `cicy_chat_client_id:${masterAgentId}`;
function makePageClientId(masterAgentId: string): string {
  const m = String(masterAgentId || 'w-10001').replace(/[^a-zA-Z0-9_-]/g, '') || 'w';
  return `web-${m}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
const CLI_DRAWER_MIN_WIDTH = 360;
const CLI_DRAWER_DEFAULT_WIDTH = 520;
const CLI_DRAWER_MAX_WIDTH = 960;
const TEAM_TERMINAL_ACTIVE_KEY = 'ws_teamTerminalActive';
const GITHUB_ISSUES_URL = 'https://github.com/cicy-ai/cicy-code/issues';
const UPGRADE_URL = 'https://cicy-ai.com/team/upgrade';
const RENEW_URL = 'https://cicy-ai.com/team/pay';

type MembershipCardState = {
  userId: string;
  level: 'open_source' | 'trial' | 'shared' | 'pro_vm' | 'private_deploy';
  tag: string;
  expiresAt: string | null;
  renewUrl: string | null;
  upgradeUrl: string | null;
};

function formatDateTime(ms: number) {
  return new Date(ms).toLocaleString(undefined, { hour12: false });
}

function formatClockTime(value: string | null) {
  if (!value) return '';
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleTimeString(undefined, { hour12: false });
}

function focusTmuxPaneFrame(paneId: string) {
  const id = String(paneId || '').trim();
  if (!id || typeof document === 'undefined') return;
  const shortId = id.split(':')[0];
  const titles = [`stack-${shortId}`, `canvas-${shortId}`, `terminal-${shortId}`];
  const candidates: HTMLIFrameElement[] = [];
  for (const title of titles) {
    document.querySelectorAll<HTMLIFrameElement>(`iframe[title="${title}"]`).forEach((el) => candidates.push(el));
  }
  if (candidates.length === 0) return;
  const target = candidates.find((el) => el.getClientRects().length > 0) || candidates[0];
  const active = document.activeElement as HTMLElement | null;
  if (active) {
    const tag = active.tagName;
    if (active.isContentEditable || tag === 'TEXTAREA' || tag === 'INPUT') {
      try { active.blur(); } catch {}
    }
  }
  const focusXterm = (): boolean => {
    try {
      target.focus();
      target.contentWindow?.focus?.();
      const doc = target.contentDocument;
      const ta = doc?.querySelector<HTMLTextAreaElement>('.xterm-helper-textarea');
      if (ta) {
        ta.focus();
        return doc?.activeElement === ta;
      }
    } catch {}
    return false;
  };
  window.requestAnimationFrame(() => {
    if (focusXterm()) return;
    let tries = 0;
    const retry = () => {
      tries += 1;
      if (focusXterm() || tries >= 5) return;
      window.setTimeout(retry, 60);
    };
    window.setTimeout(retry, 60);
  });
}

function clampCliDrawerWidth(value: number): number {
  if (!Number.isFinite(value)) return CLI_DRAWER_DEFAULT_WIDTH;
  const viewportMax = typeof window === 'undefined' ? CLI_DRAWER_MAX_WIDTH : Math.max(CLI_DRAWER_MIN_WIDTH, window.innerWidth - 120);
  const maxWidth = Math.min(CLI_DRAWER_MAX_WIDTH, viewportMax);
  return Math.min(maxWidth, Math.max(CLI_DRAWER_MIN_WIDTH, value));
}

function detectClientPlatform(): 'win' | 'darwin' | 'linux' {
  if (typeof navigator === 'undefined') return 'linux';
  const source = `${navigator.platform || ''} ${navigator.userAgent || ''}`.toLowerCase();
  if (source.includes('win')) return 'win';
  if (source.includes('mac') || source.includes('darwin')) return 'darwin';
  return 'linux';
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (!value || typeof value !== 'object') return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function isDeepEqual(a: unknown, b: unknown, seen = new WeakMap<object, object>()): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a == null || b == null) return false;
  if (typeof a !== 'object') return false;

  const aObject = a as object;
  const bObject = b as object;
  if (seen.get(aObject) === bObject) return true;
  seen.set(aObject, bObject);

  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i += 1) {
      if (!isDeepEqual(a[i], b[i], seen)) return false;
    }
    return true;
  }

  if (!isPlainObject(a) || !isPlainObject(b)) return false;
  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  if (aKeys.length !== bKeys.length) return false;

  for (const key of aKeys) {
    if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
    if (!isDeepEqual(a[key], b[key], seen)) return false;
  }
  return true;
}

function membershipTone(level: string | null) {
  switch ((level || '').trim().toLowerCase()) {
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

function membershipTagTone(level: string | null) {
  switch ((level || '').trim().toLowerCase()) {
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

const DEFAULT_MEMBERSHIP_CARD: MembershipCardState = {
  userId: 'open-source',
  level: 'open_source',
  tag: '',
  expiresAt: null,
  renewUrl: null,
  upgradeUrl: UPGRADE_URL,
};

const MEMBERSHIP_TAG_FALLBACK_KEY: Record<MembershipCardState['level'], string> = {
  open_source: 'tagOpenSource',
  trial: 'tagTrial',
  shared: 'tagShared',
  pro_vm: '',
  private_deploy: 'tagPrivateDeploy',
};

function normalizeMembershipLevel(value: unknown): MembershipCardState['level'] {
  const raw = String(value || '').trim().toLowerCase();
  if (raw === 'open_source' || raw === 'trial' || raw === 'shared' || raw === 'pro_vm' || raw === 'private_deploy') return raw;
  return DEFAULT_MEMBERSHIP_CARD.level;
}

function normalizeMembershipCard(value: any): MembershipCardState {
  const base = DEFAULT_MEMBERSHIP_CARD;
  const level = normalizeMembershipLevel(value?.level ?? value?.kind);
  const apiTag = typeof value?.tag === 'string' && value.tag.trim() ? value.tag.trim() : '';
  return {
    userId: typeof value?.userId === 'string' && value.userId.trim() ? value.userId.trim() : base.userId,
    level,
    tag: apiTag,
    expiresAt: level === 'open_source' ? null : (typeof value?.expiresAt === 'string' && value.expiresAt.trim() ? value.expiresAt.trim() : base.expiresAt),
    renewUrl: typeof value?.renewUrl === 'string' && value.renewUrl.trim() ? value.renewUrl.trim() : base.renewUrl,
    upgradeUrl: typeof value?.upgradeUrl === 'string' && value.upgradeUrl.trim() ? value.upgradeUrl.trim() : base.upgradeUrl,
  };
}

interface Props { agentId: string; onSelectAgent: (id: string) => void; }
type LeftPanelView = 'team' | 'skills' | 'agents' | null;
type WorkspaceCliContentTab = InspectorTab | 'history' | 'files' | RequestViewTab;
type CliContentMode = 'fixed';

function normalizeCliContentTab(value: any): WorkspaceCliContentTab {
  if (value === 'files' || value === 'history' || value === 'tools' || value === 'brain' || value === 'meta' || value === 'settings') {
    return value;
  }
  return 'files';
}

export default function Workspace({ agentId, onSelectAgent }: Props) {
  const { t } = useTranslation('workspace');
  const {
    setChatWsState,
    setChatWsSender,
    sendChatWsMessage,
    broadcastChatWsMessage,
    systemResources,
    setSystemResources,
    globalVar,
    setGlobalVar,
  } = useApp();
  const { token, hasPermission } = useAuth();
  const { confirm } = useDialog();
  const paneId = agentId || 'w-10001';
  const fullPaneId = `${paneId}:main.0`;
  const initialPaneIdRef = useRef(paneId);

  const mainTab: 'cli' | 'chat' = 'cli';
  const [leftPanelView, setLeftPanelView] = useState<LeftPanelView>(() => {
    const v = cache.get('ws_leftPanel', null);
    if (v === 'team' || v === 'skills') return v;
    return 'team';
  });
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [inspectorRequestedTab, setInspectorRequestedTab] = useState<InspectorTab>('overview');
  const [cliContentOpen, setCliContentOpen] = useState(true);
  const [cliContentTab, setCliContentTab] = useState<WorkspaceCliContentTab>(() => normalizeCliContentTab(cache.get(cliContentTabKey(paneId), 'files')));
  const [cliContentMode, setCliContentMode] = useState<CliContentMode>('fixed');
  const [cliDrawerWidth, setCliDrawerWidth] = useState(() => clampCliDrawerWidth(Number(cache.get(CLI_DRAWER_WIDTH_KEY, CLI_DRAWER_DEFAULT_WIDTH))));
  const [cliDrawerResizing, setCliDrawerResizing] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [apiOpen, setApiOpen] = useState(false);
  const [providersOpen, setProvidersOpen] = useState(false);
  const [imOpen, setImOpen] = useState(false);
  const [toast, setToast] = useState<ToastState | null>(null);

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
  const agentWorkspaceRef = useRef(defaultWorkerWorkspace(paneId));
  const prevCanvasPaneIdsRef = useRef<string[] | null>(null);
  const initialCanvasRestoreScopeRef = useRef<string | null>(null);
  const initialStackSelectionScopeRef = useRef<string | null>(null);
  const cliDrawerResizeRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const cliContentPanelRef = useRef<HTMLDivElement>(null);
  const systemResourcesRef = useRef<SystemResourceSnapshot | null>(systemResources);

  useEffect(() => {
    systemResourcesRef.current = systemResources;
  }, [systemResources]);

  const handleOpenClawOpen = () => {
    if (!token) return;
    window.open(urls.openClaw(token), '_blank');
  };
  const [agentDetail, setAgentDetail] = useState<any>(null);
  const title = agentDetail?.title || '-';
  const [netLatency, setNetLatency] = useState<number | null>(null);
  const [chatWsConnected, setChatWsConnected] = useState(false);
  const [chatWsClientId, setChatWsClientId] = useState<string | null>(null);
  // code-server (the bridge extension inside the iframe) derives its chat-ws client id from
  // pageClientId; only spin it up once the workspace's own chat-ws has connected, by which point
  // pageClientId is final (the BroadcastChannel dedup below has settled it). One-way latch so a
  // later reconnect doesn't tear the iframe down.
  const [codeServerReady, setCodeServerReady] = useState(false);
  useEffect(() => { if (chatWsConnected) setCodeServerReady(true); }, [chatWsConnected]);
  // chat-ws client id, bound to the current master agent (paneId is the short master id, e.g. "w-10001").
  // Kept in sessionStorage so it survives reloads in the same tab; a BroadcastChannel guard below
  // re-generates it if another tab in this browser is already using the same id — which happens when
  // a tab is *duplicated* (Chrome copies sessionStorage) — so the two tabs don't fight over the slot.
  const [pageClientId, setPageClientId] = useState<string>(() => {
    try {
      const cur = sessionStorage.getItem(chatClientIdStorageKey(paneId));
      if (cur) return cur;
    } catch {}
    const next = makePageClientId(paneId);
    try { sessionStorage.setItem(chatClientIdStorageKey(paneId), next); } catch {}
    return next;
  });
  const pageClientClaimTsRef = useRef<number>(Date.now() + Math.random());
  // re-bind when the master agent changes
  useEffect(() => {
    let next: string;
    try {
      next = sessionStorage.getItem(chatClientIdStorageKey(paneId)) || '';
    } catch { next = ''; }
    if (!next) {
      next = makePageClientId(paneId);
      try { sessionStorage.setItem(chatClientIdStorageKey(paneId), next); } catch {}
    }
    pageClientClaimTsRef.current = Date.now() + Math.random();
    setPageClientId(next);
  }, [paneId]);
  // uniqueness guard across tabs of this browser
  useEffect(() => {
    if (typeof BroadcastChannel === 'undefined') return;
    let ch: BroadcastChannel;
    try { ch = new BroadcastChannel('cicy-chat-clientid'); } catch { return; }
    const myTs = pageClientClaimTsRef.current;
    const announce = () => { try { ch.postMessage({ type: 'claim', agentId: paneId, clientId: pageClientId, ts: myTs }); } catch {} };
    ch.onmessage = (e: MessageEvent) => {
      const m: any = e?.data;
      if (!m || m.agentId !== paneId) return;
      if (m.type === 'hello') { announce(); return; }
      if (m.type === 'claim' && m.clientId === pageClientId && typeof m.ts === 'number' && m.ts < myTs) {
        // the other tab claimed this id first → yield, generate a fresh one (triggers chat-ws reconnect)
        const next = makePageClientId(paneId);
        try { sessionStorage.setItem(chatClientIdStorageKey(paneId), next); } catch {}
        pageClientClaimTsRef.current = Date.now() + Math.random();
        setPageClientId(next);
      }
    };
    try { ch.postMessage({ type: 'hello', agentId: paneId }); } catch {}
    announce();
    return () => { try { ch.close(); } catch {} };
  }, [paneId, pageClientId]);
  const wsClientPlatform = useMemo(() => detectClientPlatform(), []);
  const wsClientUserAgent = useMemo(() => (typeof navigator !== 'undefined' ? String(navigator.userAgent || '') : ''), []);
  const [chatWsLiveStatus, setChatWsLiveStatus] = useState('idle');
  const [chatWsLiveText, setChatWsLiveText] = useState('');
  const [chatWsHistoryVersion, setChatWsHistoryVersion] = useState(0);
  const [chatWsInspectorVersion, setChatWsInspectorVersion] = useState(0);
  const [chatSuggestionText, setChatSuggestionText] = useState('');
  const [chatSuggestionPending, setChatSuggestionPending] = useState(false);
  const [chatSuggestionSending, setChatSuggestionSending] = useState(false);
  const [filesTargetPaneId, setFilesTargetPaneId] = useState<string | null>(null);
  const chatWsRef = useRef<WebSocket | null>(null);
  const chatWsReconnectTimerRef = useRef<number | null>(null);
  const chatWsPingTimerRef = useRef<number | null>(null);
  const chatWsPingSentAtRef = useRef<number | null>(null);
  const chatWsPingRequestIdRef = useRef<string | null>(null);

  const membershipCard = useMemo(() => normalizeMembershipCard(globalVar?.membership), [globalVar]);
  const [membershipMenuOpen, setMembershipMenuOpen] = useState(false);
  const [membershipPopoverPos, setMembershipPopoverPos] = useState<{ x: number; y: number } | null>(null);
  const membershipMenuRef = useRef<HTMLDivElement>(null);
  const membershipTriggerRef = useRef<HTMLButtonElement>(null);
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
  useEffect(() => {
    cache.set(CLI_CONTENT_MODE_KEY, 'fixed');
  }, []);
  useEffect(() => {
    setCliContentTab(normalizeCliContentTab(cache.get(cliContentTabKey(paneId), 'files')));
  }, [paneId]);
  useEffect(() => {
    cache.set(cliContentTabKey(paneId), cliContentTab);
  }, [cliContentTab, paneId]);
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
      setCliDrawerResizing(false);
      emitWebFrameMaskEvent({ action: 'end', key: `workspace:${paneId}:cli-drawer-resize`, reason: 'cli-drawer-resize' });
      unlockPointer();
      clearPointerLock();
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', stopResize);
    window.addEventListener('blur', stopResize);
    return () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', stopResize);
      window.removeEventListener('blur', stopResize);
      stopResize();
    };
  }, [paneId]);
  const applyPanePatch = useCallback((targetPaneId: string, patch: any) => {
    setPaneDetails(prev => ({ ...prev, [targetPaneId]: { ...(prev[targetPaneId] || {}), ...patch } }));
    if (targetPaneId === paneId) {
      setAgentDetail((prev: any) => {
        const next = { ...(prev || {}), ...patch };
        const workspace = next?.workspace || defaultWorkerWorkspace(targetPaneId);
        syncHostHomeFromPath(workspace);
        agentWorkspaceRef.current = workspace;
        return next;
      });
    }
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
    } catch {}
  }, [token]);
  useEffect(() => { void refreshPanes(); }, [refreshPanes]);
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
      //console.log('[poll_request] sending via WS, readyState:', chatWsRef.current?.readyState);
      try { chatWsRef.current?.send(JSON.stringify({ type: 'poll_request' })); } catch (e) { console.warn('[poll_request] send failed:', e); } 
    };
    const onVisible = () => {
      if (document.visibilityState === 'visible') sendPollRequest();
    };
    const timer = window.setInterval(sendPollRequest, 5000);
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, []);

  // Toast listener
  useEffect(() => {
    const handler = (e: CustomEvent<string | ToastState>) => {
      const detail = e.detail;
      setToast(typeof detail === 'string' ? { message: detail } : detail);
      setTimeout(() => setToast(null), 5000);
    };
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
      try { await apiService.restartPane(paneId); for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); try { const { data } = await apiService.getTtydStatus(paneId); if (data.status === 'running') break; } catch {} } } catch { alert(t('alertRestartFailed')); } finally { setIsRestarting(false); }
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
    const masterAgentId = paneId.replace(/:.*$/, '');
    const clientId = pageClientId;
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
      const ws = new WebSocket(`${base}/api/chat/ws?master_agent_id=${encodeURIComponent(masterAgentId)}&token=${encodeURIComponent(token)}&electron=${isElectron}&client_id=${encodeURIComponent(clientId)}&platform=${encodeURIComponent(wsClientPlatform)}`);
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
        //console.log('[poll_request] WS onopen, sending initial poll_request');
        try { ws.send(JSON.stringify({ type: 'poll_request' })); } catch (e) { console.warn('[poll_request] onopen send failed:', e); }
        try { ws.send(JSON.stringify({ type: 'register_active_channel', data: { agent_id: activeCliPaneId || paneId, client_id: clientId, channel_type: 'web', platform: wsClientPlatform, user_agent: wsClientUserAgent } })); } catch (e) { console.warn('[chat-ws] register_active_channel onopen failed:', e); }
        sendLatencyPing();
        chatWsPingTimerRef.current = window.setInterval(sendLatencyPing, 5000);
      };
      ws.onmessage = (event) => {
        if (dead || chatWsRef.current !== ws) return;
        try {
          const msg = JSON.parse(String(event.data || ''));
          if (msg?.type === 'current_updated' || msg?.type === 'status_change' || msg?.type === 'ai_chunk') {
            console.log('[chat-ws-live]', msg.type, msg.data || {});
          }
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
          } else if (msg?.type === 'code.open_file' && msg.data?.path) {
            openCodeFile(String(msg.data.path || ''), String(msg.data?.requestId || ''));
          } else if (msg?.type === 'code.send_path' && msg.data?.path) {
            const targetClientId = String(msg.data?.page_client_id || '').trim();
            const filePath = String(msg.data.path || '').trim();
            if (targetClientId === clientId && filePath) {
              const workspaceState = devStore.getSnapshot().Workspace?.state || {};
              const runtimeActivePaneId = String(workspaceState.activeCliPaneId || activeCliPaneId || paneId).trim();
              const tmuxTarget = runtimeActivePaneId || paneId;
              const normalizedFilePath = `/${filePath.replace(/^\/+/, '')}`;
              const promptText = `file://${normalizedFilePath.replace(/^\/+/, '')}`;
              window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: tmuxTarget, q: promptText } }));
              sendCommandToTmux(promptText, tmuxTarget, false).then(() => {
                focusTmuxPaneFrame(tmuxTarget);
              }).catch(() => {
                window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastSendFilePathFailed') }));
              });
            }
          } else if (msg?.type === 'poll_data' && msg.data) {
            const data = msg.data;
            const nextBoundAgents = Array.isArray(data.agents) ? data.agents : [];
            const nextPollStatuses = data.statuses && typeof data.statuses === 'object' ? data.statuses : {};
            setBoundAgents((prev) => isDeepEqual(prev, nextBoundAgents) ? prev : nextBoundAgents);
            setPollStatuses((prev) => isDeepEqual(prev, nextPollStatuses) ? prev : nextPollStatuses);
            if (data.system_resources && typeof data.system_resources === 'object') {
              if (!isDeepEqual(systemResourcesRef.current, data.system_resources)) {
                const nextSystemResources = data.system_resources as SystemResourceSnapshot;
                systemResourcesRef.current = nextSystemResources;
                setSystemResources(nextSystemResources);
              }
            }
            if (data.membership && typeof data.membership === 'object') {
              setGlobalVar((prev: any) => {
                const base = prev && typeof prev === 'object' ? prev : {};
                return isDeepEqual(base.membership, data.membership) ? prev : { ...base, membership: data.membership };
              });
            }
            const st = data.statuses?.[fullPaneId] || data.statuses?.[paneId];
            if (st?.status) setStatus((prev) => prev === st.status ? prev : st.status);
            if (st?.title) setAgentDetail((prev: any) => {
              if (prev?.title === st.title) return prev;
              return prev ? { ...prev, title: st.title } : { title: st.title };
            });
            if (st?.contextUsage != null) setContextUsage((prev) => prev === st.contextUsage ? prev : st.contextUsage);
          }
          if (msg?.type === 'system_resources' && msg.data && typeof msg.data === 'object') {
            if (!isDeepEqual(systemResourcesRef.current, msg.data)) {
              const nextSystemResources = msg.data as SystemResourceSnapshot;
              systemResourcesRef.current = nextSystemResources;
              setSystemResources(nextSystemResources);
            }
          }
          broadcastChatWsMessage(msg);
        } catch {}
      };
      ws.onclose = (event) => {
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
        // 4409 = server superseded us with another connection that owns the
        // same client_id. Auto-reconnecting would just kick out the winner and
        // start a 1s ping-pong; wait for the BroadcastChannel dedup below to
        // regenerate pageClientId, which re-fires this effect with a fresh id.
        const superseded = !!(event && event.code === 4409);
        if (!dead && !superseded) {
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
  }, [activeCliPaneId, broadcastChatWsMessage, pageClientId, paneId, sendChatWsMessage, setChatWsSender, setChatWsState, token, wsClientPlatform, wsClientUserAgent]);

  useEffect(() => {
    const clientId = String(chatWsClientId || pageClientId || '').trim();
    const agentID = String(activeCliPaneId || '').trim();
    if (!clientId || !agentID || !chatWsConnected) return;
    try {
      sendChatWsMessage({ type: 'register_active_channel', data: { agent_id: agentID, client_id: clientId, channel_type: 'web', platform: wsClientPlatform, user_agent: wsClientUserAgent } });
    } catch {}
  }, [activeCliPaneId, chatWsClientId, chatWsConnected, pageClientId, sendChatWsMessage, wsClientPlatform, wsClientUserAgent]);

  useEffect(() => {
    setChatSuggestionText('');
    setChatSuggestionPending(false);
  }, [activeCliPaneId, chatWsHistoryVersion, token]);

  const openCodeFile = useCallback((rawPath: string, requestId?: string) => {
    const filePath = String(rawPath || '').trim();
    if (!filePath) return;
    setCliContentTab('files');
    setCliContentOpen(true);
    const targetPagePane = String(paneId || '').trim();
    const targetClientId = `${pageClientId}:code-ext`;
    fetch('/api/chat/push', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({
        agent_id: targetPagePane,
        client_id: targetClientId,
        type: 'host.open_file',
        data: {
          path: filePath,
          requestId: String(requestId || '').trim(),
          page_client_id: pageClientId,
          code_client_id: targetClientId,
          page_pane: targetPagePane,
        },
      }),
    }).catch(() => {});
  }, [pageClientId, paneId, token]);

  const handleSendPageClientIdToAgent = useCallback(async () => {
    const currentClientId = String(chatWsClientId || pageClientId || '').trim();
    const workspaceState = devStore.getSnapshot().Workspace?.state || {};
    const tmuxTarget = String(workspaceState.activeCliPaneId || '').trim();
    if (!currentClientId || !tmuxTarget) return;
    const promptText = `我的浏览器页面clientId:${currentClientId},你可以通过agent-webpage 和agent-code-server skill 与我通信了!`;
    try {
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: tmuxTarget, q: promptText } }));
      await sendCommandToTmux(promptText, tmuxTarget, true);
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastClientIdSent') }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastClientIdFailed') }));
    }
  }, [activeCliPaneId, chatWsClientId, pageClientId, paneId]);

  const handleCopyPageConnectPrompt = useCallback(async () => {
    const currentClientId = String(chatWsClientId || pageClientId || '').trim();
    if (!currentClientId) return;
    const promptText = `我的浏览器页面clientId:${currentClientId},你可以通过agent-webpage 和agent-code-server skill 与我通信了!`;
    let ok = false;
    try {
      if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        await navigator.clipboard.writeText(promptText);
        ok = true;
      }
    } catch {}
    if (!ok) {
      const textarea = document.createElement('textarea');
      textarea.value = promptText;
      textarea.setAttribute('readonly', 'true');
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      document.body.appendChild(textarea);
      textarea.select();
      ok = document.execCommand('copy');
      document.body.removeChild(textarea);
    }
    if (ok) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastPromptCopied') }));
    }
  }, [chatWsClientId, pageClientId]);

  const topBarPaneId = activeCliPaneId || paneId;
  const topBarDetail = paneDetails[topBarPaneId] || (topBarPaneId === paneId ? agentDetail : null);
  const topBarTitle = topBarDetail?.title
    || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === topBarPaneId)?.title
    || (topBarPaneId === paneId ? title : topBarPaneId);
  const topBarWorkspace = topBarDetail?.workspace || defaultWorkerWorkspace(topBarPaneId);
  const topBarIsApiOnlyRuntime = !!(
    topBarDetail
    && topBarDetail.capabilities?.supports_tmux === false
  );
  const filePaneId = paneId;
  const filePaneDetail = paneDetails[filePaneId] || (filePaneId === paneId ? agentDetail : null);
  const filePaneWorkspace = defaultWorkerWorkspace(filePaneId);
  const filePaneIsApiOnlyRuntime = !!(
    filePaneDetail
    && filePaneDetail.capabilities?.supports_tmux === false
  );
  const fileCodeServerSrc = token && !filePaneIsApiOnlyRuntime && codeServerReady
    ? urls.codeServer(defaultWorkerWorkspace(paneId), token, pageClientId, paneId)
    : '';
  const fileCodeServerWaiting = !!token && !filePaneIsApiOnlyRuntime && !codeServerReady;
  const openPaneInCurrentTerminal = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
  }, [paneId]);
  const openPaneSettings = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentMode('fixed');
    setCliContentTab('settings');
    setCliContentOpen(true);
  }, [paneId]);
  const openPaneHistory = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentMode('fixed');
    setCliContentTab('history');
    setCliContentOpen(true);
  }, [paneId]);
  const openPaneFiles = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    const halfWindowWidth = Math.floor(window.innerWidth / 2);
    if (halfWindowWidth > 0) {
      setCliDrawerWidth(prev => (prev > halfWindowWidth ? halfWindowWidth : prev));
    }
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentMode('fixed');
    setCliContentTab('files');
    setCliContentOpen(true);
  }, [paneId]);
  const openPaneRequestView = useCallback((targetPaneId: string, nextTab: RequestViewTab) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    const halfWindowWidth = Math.floor(window.innerWidth / 2);
    if (halfWindowWidth > 0) {
      setCliDrawerWidth(prev => (prev > halfWindowWidth ? halfWindowWidth : prev));
    }
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentMode('fixed');
    setCliContentTab(nextTab);
    setCliContentOpen(true);
  }, [paneId]);
  const handleCliDrawerResizeStart = useCallback((event: React.MouseEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.stopPropagation();
    cliDrawerResizeRef.current = {
      startX: event.clientX,
      startWidth: cliContentPanelRef.current?.getBoundingClientRect().width || cliDrawerWidth,
    };
    setCliDrawerResizing(true);
    emitWebFrameMaskEvent({ action: 'start', key: `workspace:${paneId}:cli-drawer-resize`, reason: 'cli-drawer-resize' });
    lockPointer();
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  }, [cliDrawerWidth, paneId]);

  const locatePaneInCanvas = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCanvasLocateRequest({ paneId: clean, nonce: Date.now(), zoomToActual: true });
  }, [paneId]);
  useEffect(() => {
    document.title = `${topBarTitle} (${topBarPaneId}) | CiCy Code`;
  }, [topBarPaneId, topBarTitle]);
  useEffect(() => {
    if (!token) return;
  }, [token]);
  useEffect(() => {
    (window as any).__cicyOpenCodeFile = openCodeFile;
    const onCodeFileMessage = (event: MessageEvent) => {
      const data = event.data as any;
      if (!data || data.type !== 'cicy-open-code-file') return;
      openCodeFile(data.path);
    };
    window.addEventListener('message', onCodeFileMessage);
    return () => {
      window.removeEventListener('message', onCodeFileMessage);
      if ((window as any).__cicyOpenCodeFile === openCodeFile) {
        delete (window as any).__cicyOpenCodeFile;
      }
    };
  }, [openCodeFile]);
  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (membershipMenuRef.current?.contains(target) || membershipTriggerRef.current?.contains(target)) return;
      setMembershipMenuOpen(false);
    };
    const handleWindowResize = () => setMembershipMenuOpen(false);
    document.addEventListener('pointerdown', handlePointerDown);
    window.addEventListener('resize', handleWindowResize);
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown);
      window.removeEventListener('resize', handleWindowResize);
    };
  }, []);

  const handleToggleMembershipMenu = useCallback(() => {
    if (membershipMenuOpen) {
      setMembershipMenuOpen(false);
      return;
    }
    const rect = membershipTriggerRef.current?.getBoundingClientRect();
    if (!rect) return;
    setMembershipPopoverPos({ x: rect.right + 8, y: 0 });
    setMembershipMenuOpen(true);
  }, [membershipMenuOpen]);

  useDevRegister('Workspace', {
    paneId: fullPaneId, masterAgentId: paneId, title, status, contextUsage, mouseMode, isRestarting,
    agentDetail, netLatency,
    pageClientId, chatWsClientId, chatWsConnected, codeServerReady,
    membershipCard,
    membershipMenuOpen,
    agentsCount: agents.length,
    agents: agents.map((a: any) => ({ pane_id: a.pane_id, title: a.title, status: a.status, active: a.active })),
    activeTeamPaneId,
    activeCliPaneId,
    leftPanel: leftActive, activeWinIdx,
    cliContentMode,
    cliContentTab,
  }, {
    cliContentMode: setCliContentMode,
    cliContentTab: setCliContentTab,
  });
  const cliContentTabs = [
    { id: 'files', label: t('tabFiles') },
    { id: 'history', label: t('tabHistory') },
    { id: 'tools', label: t('tabTools') },
    { id: 'brain', label: t('tabBrain') },
    { id: 'meta', label: t('tabMeta') },
    { id: 'settings', label: t('tabSettings') },
  ];
  const renderCliContentPanel = () => (
    <div
      ref={cliContentPanelRef}
      data-id="cli-content-fixed"
      className={cn(
        'relative flex h-full min-w-0 shrink-0 flex-col bg-[#0b0b0d]',
        'border-l border-[var(--vsc-border)]'
      )}
      style={{ width: `${cliDrawerWidth}px` }}
    >
      {cliDrawerResizing ? (
        <div
          data-id="cli-content-resize-overlay"
          className="fixed inset-0 z-[279] cursor-col-resize"
        />
      ) : null}
      <div
        data-id="cli-content-resize-handle"
        className={cn(
          'absolute top-0 bottom-0 z-[280] w-1 cursor-col-resize bg-transparent transition-colors hover:bg-blue-400/70 active:bg-blue-400/80',
          'left-0'
        )}
        onMouseDown={handleCliDrawerResizeStart}
      />
      <div data-id="cli-content-tabs-wrap" className="flex items-center justify-between border-b border-[var(--vsc-border)] px-3 py-2">
        <div data-id="cli-content-tabs" className="flex gap-1 overflow-x-auto whitespace-nowrap scrollbar-none">
          {cliContentTabs.map((item) => (
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
        <div data-id="cli-content-actions" className="ml-3 flex shrink-0 items-center gap-1">
          <button
            data-id="cli-content-close"
            type="button"
            onClick={() => {
              setCliContentOpen(false);
            }}
            className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
            title={t('close', { ns: 'common' })}
            aria-label={t('close', { ns: 'common' })}
          >
            <X className="h-4 w-4" />
          </button>
        </div>
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
                title={t('filesPaneTitle')}
              />
            </div>
          ) : (
            <div data-id="cli-content-files-empty" className="flex h-full items-center justify-center text-sm text-zinc-500">
              {fileCodeServerWaiting ? t('filesConnecting') : t('filesNoViewer')}
            </div>
          )}
        </div>
        <div
          data-id="cli-content-history-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'history' ? 'block' : 'none' }}
        >
          <CurrentHistoryView
            paneId={activeCliPaneId}
            open={cliContentOpen && cliContentTab === 'history'}
            inspectorVersion={chatWsInspectorVersion}
          />
        </div>
        <div
          data-id="cli-content-request-view-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'tools' || cliContentTab === 'brain' || cliContentTab === 'meta' ? 'block' : 'none' }}
        >
          <AgentProviderRequestView
            paneId={activeCliPaneId}
            open={cliContentOpen && (cliContentTab === 'tools' || cliContentTab === 'brain' || cliContentTab === 'meta')}
            tab={cliContentTab === 'tools' || cliContentTab === 'brain' || cliContentTab === 'meta' ? cliContentTab : 'tools'}
            inspectorVersion={chatWsInspectorVersion}
          />
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
  );
  const cliFixedContent = cliContentOpen ? renderCliContentPanel() : null;
  const stackHeaderControls = (targetPaneId: string) => targetPaneId === activeCliPaneId ? (
    <>
      <SystemResourceMonitor paneId={paneId} />
      <NetworkSignal latency={netLatency} connected={chatWsConnected} clientId={chatWsClientId} onSendClientId={handleSendPageClientIdToAgent} onCopyPrompt={handleCopyPageConnectPrompt} />
      <button onClick={() => setTokenOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title={t('apiTokenButton')}><Key className="w-3.5 h-3.5" /></button>
      <button onClick={() => setApiOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title={t('apiServerButton')}><Server className="w-3.5 h-3.5" /></button>
      {contextUsage != null && (
        <div data-id="context-usage" className="flex items-center gap-1.5 rounded-full bg-white/[0.02] px-2 py-0.5">
          <div data-id="context-bar" className="h-1 w-12 overflow-hidden rounded-full bg-white/[0.04]">
            <div className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400/60' : contextUsage > 50 ? 'bg-yellow-400/60' : 'bg-emerald-400/60'}`} style={{ width: `${contextUsage}%` }} />
          </div>
          <span data-id="context-pct" className="font-mono text-xs leading-none text-zinc-600">{contextUsage}%</span>
        </div>
      )}
    </>
  ) : null;
  const rightContent = (
    <div data-id="right-tabs" className="h-full relative overflow-hidden">
      <div data-id="chat-tab" className="absolute inset-0 flex justify-center" style={{ display: mainTab === 'chat' ? 'flex' : 'none' }}>
        <div className="w-full max-w-5xl h-full">
          {/* ChatView 引用先注释保留，暂时停用以阻断其内部 stats/chat 请求
          <ChatView paneId={paneId} token={token!} apiOnly={isApiOnlyRuntime} />
          */}
          <div data-id="chat-view-disabled" className="flex h-full items-center justify-center text-sm text-zinc-500">
            {t('chatViewDisabled')}
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
            settingsShortcutActive={cliContentOpen && cliContentTab === 'settings'}
            renderHeaderControls={stackHeaderControls}
            showHeaderButtons={!cliContentOpen}
            onOpenPaneSettings={openPaneSettings}
            onOpenPaneFiles={openPaneFiles}
            onOpenPaneHistory={openPaneHistory}
            onOpenPaneTools={(targetPaneId) => openPaneRequestView(targetPaneId, 'tools')}
            onOpenPaneBrain={(targetPaneId) => openPaneRequestView(targetPaneId, 'brain')}
            onOpenPaneMeta={(targetPaneId) => openPaneRequestView(targetPaneId, 'meta')}
            onActivePaneIdChange={(targetPaneId) => {
              setActiveTeamPaneId(prev => ({ ...prev, [paneId]: targetPaneId }));
            }}
          />
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
          <SideBtn dataId="btn-team" active={leftActive === 'team'} icon={<Users className="w-5 h-5" />} title={t('sidebarTeam')} onClick={() => toggleLeft('team')} />
          <SideBtn dataId="btn-providers" active={providersOpen} icon={<Boxes className="w-5 h-5" />} title={t('sidebarProviders')} onClick={() => setProvidersOpen(true)} />
          <SideBtn dataId="btn-im" active={imOpen} icon={<MessageCircle className="w-5 h-5" />} title={t('sidebarIM')} onClick={() => setImOpen(true)} />
        </div>
        <div data-id="activity-bar-bottom" className="flex w-full flex-col items-center gap-3">
          <button
            ref={membershipTriggerRef}
            type="button"
            data-id="activity-bar-membership-trigger"
            onClick={handleToggleMembershipMenu}
            className={cn('group flex h-10 w-10 items-center justify-center rounded-xl border transition-all cursor-pointer', membershipTone(membershipCard.level), membershipMenuOpen ? 'text-zinc-100 shadow-[0_12px_30px_rgba(0,0,0,0.28)]' : 'text-zinc-400 hover:text-zinc-100 hover:border-white/[0.14]')}
            title={membershipCard.userId}
          >
            <User className="h-4 w-4" />
          </button>
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
                      <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelAgents')}</span>
                    </> : leftActive === 'skills' ? <>
                      <Brain className="w-3.5 h-3.5 text-zinc-600" />
                      <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelSkills')}</span>
                    </> : <>
                      <Users className="w-3.5 h-3.5 text-zinc-600" />
                      <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelTeam')}</span>
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
            {cliFixedContent}
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
      {membershipMenuOpen && membershipPopoverPos ? createPortal(
        <div
          ref={membershipMenuRef}
          data-id="membership-user-popover"
          className="fixed z-[220] min-w-[220px] rounded-xl border border-white/10 bg-[#101014]/95 p-2 shadow-[0_18px_48px_rgba(0,0,0,0.45)] backdrop-blur"
          style={{ left: membershipPopoverPos.x, bottom: 12 }}
        >
          <div data-id="membership-dropdown-meta" className={cn('mb-2 rounded-lg border px-3 py-2', membershipTone(membershipCard.level))}>
            {(() => {
              const fallbackKey = MEMBERSHIP_TAG_FALLBACK_KEY[membershipCard.level];
              const displayTag = membershipCard.tag
                || (membershipCard.level === 'pro_vm' ? 'PRO' : (fallbackKey ? t(fallbackKey) : ''));
              return displayTag ? (
                <span className={cn('inline-flex rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase', membershipTagTone(membershipCard.level))}>
                  {displayTag}
                </span>
              ) : null;
            })()}
            {membershipCard.expiresAt ? (
              <div className="mt-1 text-[11px] font-mono text-zinc-100">{t('membershipExpiresAt', { date: formatDateTime(Date.parse(membershipCard.expiresAt)) })}</div>
            ) : null}
          </div>
          {membershipCard.renewUrl ? (
            <button
              type="button"
              data-id="membership-renew-btn"
              onClick={() => {
                setMembershipMenuOpen(false);
                window.open(membershipCard.renewUrl, '_blank', 'noopener,noreferrer');
              }}
              className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-sky-100 transition-colors hover:bg-sky-300/10"
            >
              <span>{t('membershipRenew')}</span>
              <ExternalLink className="h-3.5 w-3.5" />
            </button>
          ) : null}
          {membershipCard.upgradeUrl ? (
            <button
              type="button"
              data-id="membership-upgrade-btn"
              onClick={() => {
                setMembershipMenuOpen(false);
                window.open(membershipCard.upgradeUrl, '_blank', 'noopener,noreferrer');
              }}
              className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-amber-100 transition-colors hover:bg-amber-300/10"
            >
              <span>{t('membershipUpgrade')}</span>
              <ExternalLink className="h-3.5 w-3.5" />
            </button>
          ) : null}
          <button
            type="button"
            data-id="top-bar-github-issues"
            onClick={() => {
              setMembershipMenuOpen(false);
              window.open(GITHUB_ISSUES_URL, '_blank', 'noopener,noreferrer');
            }}
            className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
          >
            <span>GitHub Issues</span>
            <Github className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            data-id="membership-devtools"
            onClick={() => {
              setMembershipMenuOpen(false);
              window.dispatchEvent(new Event('open-devtools-panel'));
            }}
            className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
            title={t('debugTools')}
          >
            <span>{t('debugTools')}</span>
            <Bug className="h-3.5 w-3.5" />
          </button>
          <div data-id="membership-version" className="mt-1 flex items-center justify-between rounded-lg px-3 py-2 text-[11px] text-zinc-500">
            <span>Version</span>
            <span id="version" className="font-mono text-zinc-300">{config.version}</span>
          </div>
        </div>,
        document.body
      ) : null}
      {tokenOpen && <TokenDialog onClose={() => setTokenOpen(false)} />}
      {apiOpen && <ApiSwitchDialog onClose={() => setApiOpen(false)} />}
      {toast && <div className={`fixed top-4 left-1/2 -translate-x-1/2 z-[9999] px-4 py-2 text-sm rounded-lg shadow-lg ${toast.variant === 'success' ? 'bg-green-600 text-white' : 'bg-zinc-800 text-white'}`}>{toast.message}</div>}
      {providersOpen && createPortal(
        <div data-id="providers-overlay" className="fixed inset-0 z-[9000] bg-[#0A0A0A]">
          <ProviderDashboard onBack={() => setProvidersOpen(false)} />
        </div>,
        document.body,
      )}
      {imOpen && createPortal(
        <div data-id="im-overlay" className="fixed inset-0 z-[9000] bg-[#0A0A0A]">
          <IMDashboard onBack={() => setImOpen(false)} />
        </div>,
        document.body,
      )}
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
    workspace: detail?.workspace || agent?.workspace || defaultWorkerWorkspace(paneId),
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

function AgentDrawer({ agents, paneId, onSelectAgent, on智能体Change, onOpenSettings }: {
  agents: any[]; paneId: string;
  onSelectAgent: (id: string) => void; on智能体Change: (a: any[]) => void;
  onOpenSettings: (id: string) => void;
}) {
  const { t } = useTranslation('workspace');
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
        use_official_auth: values.use_official_auth,
        use_proxy: values.use_proxy,
      });
      const id = data?.pane_id || data?.id;
      if (id) {
        const { data: fresh } = await apiService.getPanes();
        on智能体Change(Array.isArray(fresh) ? fresh : fresh?.panes || []);
        setCreateDialogOpen(false);
        onSelectAgent(id.split(':')[0]);
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastCreateWorkerFailed') }));
    } finally { setAdding(false); }
  };

  const handleDelete = (id: string) => {
    const sid = id.split(':')[0];
    if (sid === 'w-10001') return;
    confirm(<Trans i18nKey="drawerConfirmDelete" ns="workspace" values={{ name: sid }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />, async () => {
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
    confirm(<Trans i18nKey="drawerConfirmRestart" ns="workspace" values={{ name: title || sid }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />, async () => {
      try {
        await apiService.restartPane(sid);
        window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastWorkerRestarting', { name: title || sid }) }));
      } catch {
        window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastWorkerRestartFailed', { name: title || sid }) }));
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
                placeholder={t('drawerSearchPlaceholder')}
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="w-full bg-white/[0.02] border border-[var(--vsc-border)] rounded-lg pl-9 pr-3 py-2 text-sm focus:outline-none focus:border-white/[0.08] placeholder:text-zinc-700 text-zinc-400"
              />
            </div>
            <button onClick={() => setCreateDialogOpen(true)} disabled={adding}
              className="flex items-center gap-1 px-2 py-1.5 rounded-lg border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50 shrink-0"
              title={t('drawerAddWorker')}>
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
                      title={t('drawerMenu')}>
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
                          <span>{t('drawerOpen')}</span>
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
                          <span>{t('drawerRestart')}</span>
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
                          <span>{t('drawerSettings')}</span>
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
                            <span>{t('drawerDelete')}</span>
                          </button>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-3 flex-1 min-w-0 text-left">
                    <AgentAvatar
                      agentType={agent.agent_type}
                      title={agent.title || shortId}
                      dataId="agent-avatar"
                      variant="panel"
                    />
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
        title={t('drawerCreateTitle')}
        submitLabel={t('drawerCreateSubmit')}
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
  const { t } = useTranslation('workspace');
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
  const updatedAt = systemResources?.updated_at ? new Date(systemResources.updated_at).toLocaleTimeString(undefined, { hour12: false }) : '--';

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
        title={t('systemResourceTitle')}
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
            <span className="text-xs font-medium text-zinc-300">{t('systemResourceTitle')}</span>
            <span data-id="system-resource-updated-at" className="text-[10px] font-mono text-zinc-600">{updatedAt}</span>
          </div>
          <div data-id="system-resource-grid" className="grid grid-cols-[auto,1fr] gap-x-3 gap-y-1.5 text-[11px]">
            <span data-id="system-resource-label-cpu" className="text-zinc-600">CPU</span>
            <span data-id="system-resource-value-cpu" className="font-mono text-zinc-300">{cpu} · {systemResources?.cpu_cores ?? '--'} cores</span>

            <span data-id="system-resource-label-memory" className="text-zinc-600">{t('systemResourceMemory')}</span>
            <span data-id="system-resource-value-memory" className="font-mono text-zinc-300">
              {memory} · {formatResourceBytes(systemResources?.mem_used_bytes)} / {formatResourceBytes(systemResources?.mem_total_bytes)}
            </span>

            <span data-id="system-resource-label-disk" className="text-zinc-600">{t('systemResourceDisk')}</span>
            <span data-id="system-resource-value-disk" className="font-mono text-zinc-300">
              {disk} · {formatResourceBytes(systemResources?.disk_used_bytes)} / {formatResourceBytes(systemResources?.disk_total_bytes)}
            </span>

            <span data-id="system-resource-label-load" className="text-zinc-600">{t('systemResourceLoad')}</span>
            <span data-id="system-resource-value-load" className="font-mono text-zinc-300">
              {formatLoadValue(systemResources?.load_1)} / {formatLoadValue(systemResources?.load_5)} / {formatLoadValue(systemResources?.load_15)}
            </span>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function NetworkSignal({ latency, connected = true, clientId, onSendClientId, onCopyPrompt }: { latency: number | null; connected?: boolean; clientId?: string | null; onSendClientId?: () => Promise<void> | void; onCopyPrompt?: () => Promise<void> | void }) {
  const { t } = useTranslation('workspace');
  const [copiedPrompt, setCopiedPrompt] = useState(false);
  const [open, setOpen] = useState(false);
  const [sending, setSending] = useState(false);
  const bars = !connected ? 0 : latency === null ? 4 : latency < 100 ? 4 : latency < 200 ? 3 : latency < 500 ? 2 : 1;
  const color = bars >= 4 ? 'bg-emerald-400' : bars === 3 ? 'bg-emerald-400' : bars === 2 ? 'bg-yellow-400' : bars === 1 ? 'bg-red-400' : 'bg-zinc-700';
  const label = !connected ? t('networkOffline') : latency === null ? t('networkOnline') : `${latency}ms`;
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
  const handleCopyPrompt = async () => {
    if (!onCopyPrompt) return;
    await onCopyPrompt();
    setCopiedPrompt(true);
    window.setTimeout(() => setCopiedPrompt(false), 1200);
  };
  const handleSend = async () => {
    if (!onSendClientId || sending) return;
    setSending(true);
    try {
      await onSendClientId();
    } finally {
      setSending(false);
    }
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
        className="mt-[5px] flex h-4 min-w-[28px] items-center text-[10px] leading-none text-zinc-600"
      >
        {label}
      </span>
      {open ? (
        <div className="absolute right-0 top-full z-[180] min-w-[240px] rounded-lg border border-white/[0.08] bg-[#111113]/98 px-3 py-2 text-[11px] shadow-2xl backdrop-blur-xl">
          <div className="text-zinc-500">WebSocket</div>
          <div className="mt-1 flex items-start gap-2">
            <div className="min-w-0 flex-1 font-mono text-zinc-200 break-all">{clientId || t('networkClientIdMissing')}</div>
          </div>
          <div className="mt-1 text-zinc-500">{connected ? t('networkConnected') : t('networkDisconnected')}</div>
          <button
            type="button"
            data-id="network-signal-send-client-id"
            className="mt-2 inline-flex w-full items-center justify-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.04] px-2 py-1.5 text-[11px] text-zinc-200 transition-colors hover:bg-white/[0.08] disabled:cursor-not-allowed disabled:opacity-60"
            onClick={() => { void handleSend(); }}
            disabled={sending}
          >
            <Send className="h-3.5 w-3.5" />
            <span>{sending ? t('networkSending') : t('networkSendClientId')}</span>
          </button>
          <button
            type="button"
            data-id="network-signal-copy-connect-prompt"
            className="mt-2 inline-flex w-full items-center justify-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.04] px-2 py-1.5 text-[11px] text-zinc-200 transition-colors hover:bg-white/[0.08]"
            onClick={() => { void handleCopyPrompt(); }}
          >
            <Copy className="h-3.5 w-3.5" />
            <span>{t('networkCopyPrompt')}</span>
          </button>
          {copiedPrompt ? <div className="mt-1 text-emerald-400">{t('networkPromptCopied')}</div> : null}
        </div>
      ) : null}
    </div>
  );
}
