import { useState, useEffect, useRef, useCallback, useMemo, memo } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation, Trans } from 'react-i18next';
import { TRANSLATED_LNGS } from '../i18n';

type ToastState = {
  message: string;
  variant?: 'default' | 'success';
};
import { useApp } from '../contexts/AppContext';
import type { SystemResourceSnapshot } from '../contexts/AppContext';
import {
  Terminal, MessageSquare, Folder, FolderOpen, X, Settings, Brain, Search,
  LayoutList, Users, User, Plus, ExternalLink, Key, Bug, Server, MoreHorizontal, ChevronDown, Github, Copy, Check, Send, RotateCcw, Boxes, Package, MessageCircle,
  Cpu, MemoryStick, HardDrive, Activity, Wifi, WifiOff, ShieldCheck, ListTodo, LineChart, Building2
} from 'lucide-react';
import { cn } from '../lib/utils';
import AgentAvatar from './AgentAvatar';
import MobileQRPopover from './MobileQRPopover';
import { useDevRegister, devStore } from '../lib/devStore';
import { useAuth } from '../contexts/AuthContext';
import { SendingProvider } from '../contexts/SendingContext';
// import ChatView from './chat/ChatView';
import ChatHistoryView from './chat/ChatHistoryView';
import TodoPanel from './TodoPanel';
import FilesView from './files/FilesView';
import { WebFrame } from './WebFrame';
import { WindowManager } from './terminal/WindowManager';
import { VoiceFloatingButton } from './VoiceFloatingButton';
import TeamPanel from './layout/TeamPanel';
import MeetingRoom from './office/MeetingRoom';
import GlobalProxyIndicator from './layout/GlobalProxyIndicator';
import SkillMarketplacePanel from './layout/SkillMarketplacePanel';
import AgentInspector, { InspectorTab } from './layout/AgentInspector';
import AgentProviderRequestView, { type RequestViewTab } from './layout/AgentProviderRequestView';
import AgentUsageLogView from './layout/AgentUsageLogView';
import AgentUsageAnalysisView from './layout/AgentUsageAnalysisView';
import TokenDialog from './layout/TokenDialog';
import useDesktopEvents from './layout/useDesktopEvents';
import AgentCanvas, { AgentCanvasItem } from './layout/AgentCanvas';
import AgentStack from './layout/AgentStack';
import ProviderDashboard from './providers/ProviderDashboard';
import IMDashboard from './im/IMDashboard';
import WeChatBindModal from './im/WeChatBindModal';
import { useDialogs } from './ui/Modal';
import config, { defaultWorkerWorkspace, getHostHome, syncHostHomeFromPath, toTildePath, urls } from '../config';
import apiService from '../services/api';
import { sendCommandToTmux } from '../services/mockApi';
import { chatWs } from '../services/chatWs';
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
const leftPanelKey = (masterAgentId: string) => `ws_leftPanel:${masterAgentId}`;
const cliContentOpenKey = (masterAgentId: string) => `ws_cliContentOpen:${masterAgentId}`;
const chatClientIdStorageKey = (masterAgentId: string) => `cicy_chat_client_id:${masterAgentId}`;
const chatClientIdStorage = (): Storage =>
  typeof (window as any).electronRPC === 'function' ? localStorage : sessionStorage;
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

function languageDisplayName(code: string): string {
  try {
    const dn = new Intl.DisplayNames([code, 'en'], { type: 'language' });
    const name = dn.of(code);
    if (name && name !== code) return name;
  } catch {}
  return code;
}

// Map BCP-47 language code → ISO 3166-1 alpha-2 country code for the flag emoji.
// For codes that already carry a region (zh-CN), we use that region directly.
const LANG_TO_COUNTRY: Record<string, string> = {
  en: 'US', 'zh-CN': 'CN', ja: 'JP', ko: 'KR',
  vi: 'VN', th: 'TH', id: 'ID', ms: 'MY', tl: 'PH', my: 'MM', km: 'KH', lo: 'LA',
  hi: 'IN', bn: 'BD', ta: 'IN', te: 'IN', ml: 'IN', kn: 'IN', mr: 'IN', gu: 'IN',
  pa: 'IN', ur: 'PK', ne: 'NP', si: 'LK',
  es: 'ES', 'es-MX': 'MX', pt: 'PT', 'pt-BR': 'BR', fr: 'FR', 'fr-CA': 'CA',
  de: 'DE', it: 'IT', nl: 'NL', sv: 'SE', da: 'DK', no: 'NO', fi: 'FI', is: 'IS',
  ga: 'IE', cy: 'GB', eu: 'ES', ca: 'ES', gl: 'ES', lb: 'LU', fo: 'FO',
  pl: 'PL', cs: 'CZ', sk: 'SK', hu: 'HU', ro: 'RO', bg: 'BG', hr: 'HR',
  sr: 'RS', sl: 'SI', mk: 'MK', sq: 'AL', lt: 'LT', lv: 'LV', et: 'EE',
  mt: 'MT', el: 'GR',
  ru: 'RU', uk: 'UA', be: 'BY',
  ar: 'SA', fa: 'IR', he: 'IL', tr: 'TR', az: 'AZ', ku: 'TR',
  kk: 'KZ', ky: 'KG', uz: 'UZ', tg: 'TJ', mn: 'MN',
  hy: 'AM', ka: 'GE',
  sw: 'KE', am: 'ET', ha: 'NG', yo: 'NG', ig: 'NG', zu: 'ZA', xh: 'ZA',
  af: 'ZA', so: 'SO', rw: 'RW', om: 'ET', sn: 'ZW',
};

function flagEmoji(code: string): string {
  const country = LANG_TO_COUNTRY[code];
  if (!country) return '';
  return country.toUpperCase().replace(/./g, (ch) =>
    String.fromCodePoint(0x1F1A5 + ch.charCodeAt(0)),
  );
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
type LeftPanelView = 'team' | 'skills' | 'agents' | 'providers' | 'im' | 'todo' | 'office' | null;
type WorkspaceCliContentTab = InspectorTab | 'files' | 'todo' | RequestViewTab;
type CliContentMode = 'fixed';

function normalizeCliContentTab(value: any): WorkspaceCliContentTab {
  if (value === 'files' || value === 'tools' || value === 'brain' || value === 'meta' || value === 'usage' || value === 'analysis' || value === 'settings' || value === 'memory' || value === 'todo') {
    return value;
  }
  return 'files';
}

export default function Workspace({ agentId, onSelectAgent }: Props) {
  const { t, i18n: i18nLive } = useTranslation('workspace');
  const [currentLang, setCurrentLang] = useState<string>(() => i18nLive.resolvedLanguage ?? i18nLive.language ?? 'en');
  const [langMenuOpen, setLangMenuOpen] = useState(false);
  useEffect(() => {
    const handler = (lng: string) => { setCurrentLang(lng); setLangMenuOpen(false); };
    i18nLive.on('languageChanged', handler);
    return () => { i18nLive.off('languageChanged', handler); };
  }, [i18nLive]);
  const {
    setChatWsState,
    setChatWsSender,
    sendChatWsMessage,
    broadcastChatWsMessage,
    systemResources,
    setSystemResources,
    globalVar,
    setGlobalVar,
    isDev,
    setActiveAgentId,
    setAgentDetail: setSharedAgentDetail,
    patchAgentDetail: patchSharedAgentDetail,
    shellPanelOpen,
    toggleShellPanel,
  } = useApp();
  const { token, hasPermission } = useAuth();
  const { confirm, node: dialogsNode } = useDialogs();
  const paneId = agentId || 'w-10001';
  const fullPaneId = `${paneId}:main.0`;
  const initialPaneIdRef = useRef(paneId);

  const mainTab: 'cli' | 'chat' = 'cli';
  const [leftPanelView, setLeftPanelView] = useState<LeftPanelView>(() => {
    const v = cache.get(leftPanelKey(paneId), null);
    if (v === 'team' || v === 'skills') return v;
    return null;
  });
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [inspectorRequestedTab, setInspectorRequestedTab] = useState<InspectorTab>('overview');
  const [cliContentOpen, setCliContentOpen] = useState(() => cache.get(cliContentOpenKey(paneId), false) === true);
  const [cliContentTab, setCliContentTab] = useState<WorkspaceCliContentTab>(() => normalizeCliContentTab(cache.get(cliContentTabKey(paneId), 'files')));
  const [lastSessionSubTab, setLastSessionSubTab] = useState<RequestViewTab>(() => {
    const v = cache.get(cliContentTabKey(paneId), 'files');
    return v === 'tools' || v === 'brain' || v === 'meta' || v === 'usage' || v === 'analysis' ? v : 'analysis';
  });
  useEffect(() => {
    if (cliContentTab === 'tools' || cliContentTab === 'brain' || cliContentTab === 'meta' || cliContentTab === 'usage' || cliContentTab === 'analysis') {
      setLastSessionSubTab(cliContentTab);
    }
  }, [cliContentTab]);
  const [cliContentMode, setCliContentMode] = useState<CliContentMode>('fixed');
  // Whether the `cicy-todo` skill is installed on the local host. Gates the
  // Todo button in the activity bar and the Todo tab in cliContentTabs. The
  // marketplace panel dispatches `cicy:skills-changed` after install/uninstall;
  // we re-fetch on that signal so the UI updates without a page reload.
  // null = install status not yet checked. Distinguishing "pending" from
  // "confirmed not installed" matters: the reset-todo-tab effect below must NOT
  // clobber a cache-restored 'todo'/'memory' tab while the async check is still
  // in flight, or the restored current tab is lost on every reload.
  const [todoSkillInstalled, setTodoSkillInstalled] = useState<boolean | null>(null);
  // Pending-todo count (status todo on the active pane) for the red
  // badge on the Todo tab.
  const [todoCount, setTodoCount] = useState<number>(0);
  const [cliDrawerWidth, setCliDrawerWidth] = useState(() => clampCliDrawerWidth(Number(cache.get(CLI_DRAWER_WIDTH_KEY, CLI_DRAWER_DEFAULT_WIDTH))));
  const [cliDrawerResizing, setCliDrawerResizing] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [apiOpen, setApiOpen] = useState(false);
  const [toast, setToast] = useState<ToastState | null>(null);
  const [providersLeftMount, setProvidersLeftMount] = useState<HTMLDivElement | null>(null);
  const [providersRightMount, setProvidersRightMount] = useState<HTMLDivElement | null>(null);
  const [imLeftMount, setImLeftMount] = useState<HTMLDivElement | null>(null);
  const [imRightMount, setImRightMount] = useState<HTMLDivElement | null>(null);

  const [status, setStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode, setMouseMode] = useState<'on' | 'off'>('off');
  const [isRestarting, setIsRestarting] = useState(false);
  const [agents, setAgents] = useState<any[]>([]);
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
  // Mirror the primary pane's detail into AppContext so any other component (footer
  // ModelPicker, secondary panels, etc.) can subscribe via useApp().activeAgentDetail
  // and stay in sync without prop-drilling.
  useEffect(() => {
    setSharedAgentDetail(paneId, agentDetail);
  }, [paneId, agentDetail, setSharedAgentDetail]);
  const title = agentDetail?.title || '-';
  const [netLatency, setNetLatency] = useState<number | null>(null);
  const [chatWsConnected, setChatWsConnected] = useState(false);
  const [chatWsClientId, setChatWsClientId] = useState<string | null>(null);
  // chat-ws client id, bound to the current master agent (paneId is the short master id, e.g. "w-10001").
  // Kept in sessionStorage so it survives reloads in the same tab; a BroadcastChannel guard below
  // re-generates it if another tab in this browser is already using the same id — which happens when
  // a tab is *duplicated* (Chrome copies sessionStorage) — so the two tabs don't fight over the slot.
  const [pageClientId, setPageClientId] = useState<string>(() => {
    try {
      const cur = chatClientIdStorage().getItem(chatClientIdStorageKey(paneId));
      if (cur) return cur;
    } catch {}
    const next = makePageClientId(paneId);
    try { chatClientIdStorage().setItem(chatClientIdStorageKey(paneId), next); } catch {}
    return next;
  });
  const pageClientClaimTsRef = useRef<number>(Date.now() + Math.random());
  // re-bind when the master agent changes
  useEffect(() => {
    let next: string;
    try {
      next = chatClientIdStorage().getItem(chatClientIdStorageKey(paneId)) || '';
    } catch { next = ''; }
    if (!next) {
      next = makePageClientId(paneId);
      try { chatClientIdStorage().setItem(chatClientIdStorageKey(paneId), next); } catch {}
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
        try { chatClientIdStorage().setItem(chatClientIdStorageKey(paneId), next); } catch {}
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
  // Refs kept in sync so the chat-ws message handler (a stable useCallback)
  // can read the latest paneId / pageClientId / openCodeFile without taking
  // them as deps (which would invalidate the callback on every change and
  // re-subscribe to the singleton).
  const paneIdRef = useRef(paneId);
  const pageClientIdRef = useRef<string>('');
  useEffect(() => { paneIdRef.current = paneId; }, [paneId]);
  useEffect(() => { pageClientIdRef.current = pageClientId; }, [pageClientId]);
  const openCodeFileRef = useRef<((p: string, r?: string) => void) | null>(null);

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
    cache.set(leftPanelKey(paneId), leftActive === 'team' || leftActive === 'skills' ? leftActive : null);
  }, [leftActive, paneId]);
  useEffect(() => { cache.set(TEAM_TERMINAL_ACTIVE_KEY, activeTeamPaneId); }, [activeTeamPaneId]);
  useEffect(() => { cache.set(CLI_DRAWER_WIDTH_KEY, cliDrawerWidth); }, [cliDrawerWidth]);
  useEffect(() => { cache.set(cliContentOpenKey(paneId), cliContentOpen); }, [cliContentOpen, paneId]);
  useEffect(() => {
    cache.set(CLI_CONTENT_MODE_KEY, 'fixed');
  }, []);
  useEffect(() => {
    setCliContentTab(normalizeCliContentTab(cache.get(cliContentTabKey(paneId), 'files')));
  }, [paneId]);
  useEffect(() => {
    cache.set(cliContentTabKey(paneId), cliContentTab);
  }, [cliContentTab, paneId]);
  // Watch the cicy-todo install status. Fetch once on mount + on every
  // `cicy:skills-changed` window event (dispatched by SkillMarketplacePanel
  // after install/uninstall/update). Auto-collapse the left Todo drawer and
  // reset the cli-content tab if the skill gets uninstalled while open.
  useEffect(() => {
    let cancelled = false;
    const fetchInstalled = async () => {
      try {
        const res: any = await apiService.listMarketSkills();
        const skills = Array.isArray(res?.data?.skills) ? res.data.skills : [];
        const todo = skills.find((s: any) => s?.name === 'cicy-todo');
        if (!cancelled) setTodoSkillInstalled(!!todo?.status?.installed);
      } catch {
        if (!cancelled) setTodoSkillInstalled(false);
      }
    };
    fetchInstalled();
    const onChange = () => { fetchInstalled(); };
    window.addEventListener('cicy:skills-changed', onChange);
    return () => { cancelled = true; window.removeEventListener('cicy:skills-changed', onChange); };
  }, []);
  useEffect(() => {
    // Only reset once the skill is CONFIRMED absent (=== false). While the
    // check is pending (null) leave a cache-restored 'todo' tab alone.
    if (todoSkillInstalled === false) {
      if (leftPanelView === 'todo') setLeftPanelView(null);
      if (cliContentTab === 'todo') setCliContentTab('files');
    }
  }, [todoSkillInstalled, leftPanelView, cliContentTab]);
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
    const shortTarget = targetPaneId.split(':')[0];
    setPaneDetails(prev => ({ ...prev, [shortTarget]: { ...(prev[shortTarget] || {}), ...patch } }));
    patchSharedAgentDetail(shortTarget, patch);
    if (shortTarget === paneId.split(':')[0]) {
      setAgentDetail((prev: any) => {
        const next = { ...(prev || {}), ...patch };
        const workspace = next?.workspace || defaultWorkerWorkspace(shortTarget);
        syncHostHomeFromPath(workspace);
        agentWorkspaceRef.current = workspace;
        return next;
      });
    }
    setAgents(prev => prev.map(a => {
      const id = (a.pane_id || a.id || '').split(':')[0];
      return id === shortTarget ? { ...a, ...patch } : a;
    }));
    // TeamPanel's title field comes from boundAgents (poll_data); without this
    // the panel sticks with the old value until the next poll cycle (~1s).
    setBoundAgents(prev => prev.map((b: any) => {
      const id = String(b?.name || b?.pane_id || '').split(':')[0];
      return id === shortTarget ? { ...b, ...patch } : b;
    }));
    // Nudge the server for a fresh poll_data so the other indicators
    // (status, machine bindings, etc.) also catch up immediately.
    try { chatWs.send({ type: 'poll_request' }); } catch {}
  }, [paneId, patchSharedAgentDetail]);
  const handleRenamePaneTitle = useCallback(async (targetPaneId: string, nextTitle: string) => {
    await apiService.updatePane(targetPaneId, { title: nextTitle });
    applyPanePatch(targetPaneId, { title: nextTitle });
    setBoundAgents(prev => prev.map((item: any) => {
      const id = String(item?.name || item?.pane_id || '').replace(/:.*$/, '');
      return id === targetPaneId ? { ...item, title: nextTitle } : item;
    }));
    try {
      const { data } = await apiService.getPanes();
      setAgents(Array.isArray(data) ? data : data?.panes || []);
    } catch {}
  }, [applyPanePatch]);

  const refreshPanes = useCallback(async () => {
    if (!token) return;
    try {
      const { data } = await apiService.getPanes();
      setAgents(Array.isArray(data) ? data : data?.panes || []);
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
    // 5s WS poll fallback + immediate request on visibility
    const sendPollRequest = () => {
      try { chatWs.send({ type: 'poll_request' }); } catch (e) { console.warn('[poll_request] send failed:', e); }
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

  const handleRestart = async () => {
    if (isApiOnlyRuntime) return;
    if (!(await confirm({ body: `Restart ${paneId}?` }))) return;
    setIsRestarting(true);
    try { await apiService.restartPane(paneId); for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); try { const { data } = await apiService.getTtydStatus(paneId); if (data.status === 'running') break; } catch {} } } catch { alert(t('alertRestartFailed')); } finally { setIsRestarting(false); }
  };
  const handleCapture = async () => { if (isApiOnlyRuntime) return; try { const { data } = await apiService.capturePane(paneId, 100); if (data.output) await navigator.clipboard.writeText(data.output); } catch {} };
  const handleToggleMouse = async () => { if (isApiOnlyRuntime) return; const n = mouseMode === 'on' ? 'off' : 'on'; try { await apiService.toggleMouse(n, fullPaneId); setMouseMode(n); } catch {} };

  const toggleLeft = (p: 'team' | 'skills' | 'providers' | 'im' | 'todo' | 'office') => {
    setLeftPanelView(prev => prev === p ? null : p);
  };

  const toggleAgents = () => {
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
  // Keep a ref so long-lived effects (chat-ws lifecycle) can read the latest
  // value at firing time without taking activeCliPaneId as a dep — otherwise
  // poll-driven auto-switches between agents would tear down + recreate the
  // WS every time the active pane flipped.
  const activeCliPaneIdRef = useRef(activeCliPaneId);
  useEffect(() => { activeCliPaneIdRef.current = activeCliPaneId; }, [activeCliPaneId]);
  // Keep the Todo-tab badge count fresh: fetch on mount / pane switch / install
  // flip, poll every 10s, and refresh immediately on `cicy:todos-changed`
  // (dispatched by TodoPanel after add/status/delete). Count = pending todos.
  useEffect(() => {
    if (!todoSkillInstalled) { setTodoCount(0); return; }
    let cancelled = false;
    const refresh = async () => {
      try {
        const res: any = await apiService.getTodoCounts(activeCliPaneId);
        const c = res?.data || {};
        if (!cancelled) setTodoCount(Number(c.todo) || 0);
      } catch { /* keep previous count on transient error */ }
    };
    refresh();
    const id = window.setInterval(refresh, 10000);
    // TodoPanel carries fresh counts in the event detail; use them directly
    // when they're for the active pane, otherwise refetch.
    const onChange = (e: Event) => {
      const d = (e as CustomEvent).detail;
      if (d && d.paneId === activeCliPaneId) {
        setTodoCount(Number(d.todo) || 0);
      } else {
        refresh();
      }
    };
    window.addEventListener('cicy:todos-changed', onChange);
    return () => { cancelled = true; window.clearInterval(id); window.removeEventListener('cicy:todos-changed', onChange); };
  }, [todoSkillInstalled, activeCliPaneId]);
  useEffect(() => {
    setInspectorPaneId(activeCliPaneId || paneId);
  }, [activeCliPaneId, paneId]);
  // Publish the active CLI pane id into AppContext so cross-component consumers
  // (ModelPicker, other panels) can read activeAgentDetail without prop drilling.
  useEffect(() => {
    setActiveAgentId(activeCliPaneId || paneId);
  }, [activeCliPaneId, paneId, setActiveAgentId]);
  // Fetch pane detail for the active CLI pane so footer ModelPicker (and other
  // active-card UI) can read use_custom_gateway/agent_type/provider models for
  // panes other than the workspace's primary one.
  useEffect(() => {
    if (!activeCliPaneId) return;
    const shortActive = activeCliPaneId.split(':')[0];
    if (!shortActive) return;
    // Fetch full pane detail (incl. runtime_ai_provider_options / runtime_ai_default,
    // which only the GET endpoint returns) for the active CLI pane. Includes the
    // workspace's primary pane — without it, the footer ModelPicker has nothing
    // to render its provider list from when sitting on a primary pane.
    const cached = paneDetails[shortActive];
    if (cached && cached.runtime_ai_provider_options && cached.agent_type) return;
    let cancelled = false;
    (async () => {
      try {
        const { data } = await apiService.getPane(activeCliPaneId);
        if (cancelled || !data) return;
        setPaneDetails(prev => ({ ...prev, [shortActive]: { ...(prev[shortActive] || {}), ...data } }));
        setSharedAgentDetail(shortActive, data);
      } catch {}
    })();
    return () => { cancelled = true; };
  }, [activeCliPaneId, paneId, paneDetails, setSharedAgentDetail]);
  // Force a re-fetch of a pane's detail (incl. runtime_ai_provider_options),
  // bypassing the cache guard above. Wired to the ModelPicker's open handler so
  // edits to providers/models in global.json show up the moment the list is
  // opened, instead of only after a full page reload.
  const refreshPaneDetail = useCallback(async (targetPaneId: string) => {
    const short = targetPaneId.split(':')[0];
    if (!short) return;
    try {
      const { data } = await apiService.getPane(targetPaneId);
      if (!data) return;
      setPaneDetails(prev => ({ ...prev, [short]: { ...(prev[short] || {}), ...data } }));
      setSharedAgentDetail(short, data);
    } catch {}
  }, [setSharedAgentDetail]);
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
    // Hand the context the singleton's send. The singleton owns the WS so this
    // is a stable function pointer; no need to re-register on re-render.
    setChatWsSender((payload: unknown) => chatWs.send(payload));
    return () => { setChatWsSender(() => false); };
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
    const rpcResultHandler = (e: Event) => {
      const detail = (e as CustomEvent).detail;
      sendChatWsMessage({ type: 'rpc_result', data: detail });
    };
    window.addEventListener('gemini-vision-result', visionHandler as EventListener);
    window.addEventListener('gemini-ask-result', askHandler as EventListener);
    window.addEventListener('agent-pong', pongHandler as EventListener);
    window.addEventListener('ipc-pong', ipcPongHandler as EventListener);
    window.addEventListener('rpc-result', rpcResultHandler as EventListener);
    return () => {
      window.removeEventListener('gemini-vision-result', visionHandler as EventListener);
      window.removeEventListener('gemini-ask-result', askHandler as EventListener);
      window.removeEventListener('agent-pong', pongHandler as EventListener);
      window.removeEventListener('ipc-pong', ipcPongHandler as EventListener);
      window.removeEventListener('rpc-result', rpcResultHandler as EventListener);
    };
  }, [sendChatWsMessage]);

  // === chat-ws ownership lives in services/chatWs.ts (module singleton) ===
  // React only:
  //   1. tells the singleton what URL params to use (configure)
  //   2. tells it the current active agent (setActiveAgent)
  //   3. subscribes to messages / connection state
  // The singleton owns the WebSocket lifetime. Re-renders, StrictMode double
  // mounts, dep churn — none of it disturbs the connection.

  const handleChatWsMessage = useCallback((msg: any) => {
    const masterPaneId = paneIdRef.current;
    const pageClientIdNow = chatWs.currentClientId() || pageClientIdRef.current;
    if (msg?.type === 'user_q') {
      setChatWsLiveStatus('pending');
      setChatWsLiveText('');
      setChatSuggestionText('');
      setChatSuggestionPending(false);
      setChatWsState({ activeChatPaneId: masterPaneId, chatWsLiveStatus: 'pending', chatWsLiveText: '' });
    } else if (msg?.type === 'ai_chunk') {
      const delta = String(msg.data?.delta || '');
      if (delta) {
        setChatWsLiveText((prev) => {
          const next = `${prev}${delta}`;
          setChatWsState({ activeChatPaneId: masterPaneId, chatWsLiveStatus: 'streaming', chatWsLiveText: next });
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
      setChatWsState({ activeChatPaneId: masterPaneId, chatWsLiveStatus: nextStatus === 'thinking' ? 'pending' : nextStatus === 'working' || nextStatus === 'tool_call' || nextStatus === 'tool_use' ? 'tool_use' : nextStatus === 'streaming' ? 'streaming' : nextStatus === 'failed' || nextStatus === 'error' ? 'failed' : 'done' });
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
      setChatWsState({ activeChatPaneId: masterPaneId, chatWsLiveStatus: 'done' });
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
    } else if (msg?.type === 'wechat_bind_request') {
      // audit advisor (w-6001) asked the UI to pop the WeChat bind modal
      window.dispatchEvent(new CustomEvent('open-wechat-bind'));
    } else if (msg?.type === 'webpage_ping') {
      const versionText = document.getElementById('version')?.textContent?.trim() || config.version;
      chatWs.send({ type: 'webpage_pong', data: { requestId: msg.data?.requestId, version: versionText } });
    } else if (msg?.type === 'exec_js' && msg.data?.code) {
      // Async-aware: if the expression resolves to a Promise (e.g. an
      // `(async () => {...})()` IIFE), wait for it and send the resolved
      // value back. Objects are JSON-stringified so the caller can parse
      // structured payloads — String({x:1}) would have collapsed to the
      // unhelpful "[object Object]".
      void (async () => {
        try {
          let result: any = window.eval(msg.data.code);
          if (result && typeof result.then === 'function') {
            result = await result;
          }
          const payload =
            result === null || result === undefined ? ''
            : typeof result === 'object' ? JSON.stringify(result)
            : String(result);
          chatWs.send({ type: 'exec_js_result', data: { requestId: msg.data?.requestId, result: payload } });
        } catch (error: any) {
          chatWs.send({ type: 'exec_js_result', data: { requestId: msg.data?.requestId, error: error?.message || String(error) } });
        }
      })();
    } else if (msg?.type === 'code.open_file' && msg.data?.path) {
      // Legacy: kept for backward compat with older callers. New callers should
      // use the sync /api/chat/code-open endpoint instead — see openCodeFile.
      openCodeFileRef.current?.(String(msg.data.path || ''), String(msg.data?.requestId || ''));
    } else if (msg?.type === 'code.show_files') {
      // Pure UX nudge from the agent-editor pipeline: bring the Files panel
      // (which hosts the native editor) to the foreground so the :code-ext
      // bridge is alive. The actual file open is a direct sync POST to that
      // WS — no page-side relay.
      setCliContentTab('files');
      setCliContentOpen(true);
    } else if (msg?.type === 'code.send_path' && msg.data?.path) {
      const targetClientId = String(msg.data?.page_client_id || '').trim();
      const filePath = String(msg.data.path || '').trim();
      if (targetClientId === pageClientIdNow && filePath) {
        const workspaceState = devStore.getSnapshot().Workspace?.state || {};
        const runtimeActivePaneId = String(workspaceState.activeCliPaneId || activeCliPaneIdRef.current || masterPaneId).trim();
        const tmuxTarget = runtimeActivePaneId || masterPaneId;
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
      const st = data.statuses?.[fullPaneId] || data.statuses?.[masterPaneId];
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
  }, [broadcastChatWsMessage, fullPaneId, setChatWsState, setGlobalVar, setSystemResources, t]);

  // (1) Drive the singleton's URL params. configure() reconnects only when
  // URL-affecting params actually change.
  useEffect(() => {
    if (!token || !paneId) {
      chatWs.shutdown();
      setChatWsConnected(false);
      setChatWsClientId(null);
      setNetLatency(null);
      setChatWsLiveStatus('idle');
      setChatWsLiveText('');
      setChatWsState({ activeChatPaneId: null, chatWsConnected: false, chatWsClientId: null, chatWsLiveStatus: 'idle', chatWsLiveText: '' });
      return;
    }
    // Match the old behavior: on (re)connect-causing param change clear any
    // stale live AI text/status. The singleton re-emits chatWsConnected=true
    // once the WS opens.
    setChatWsLiveStatus('idle');
    setChatWsLiveText('');
    setChatWsState({ activeChatPaneId: paneId, chatWsLiveStatus: 'idle', chatWsLiveText: '' });
    chatWs.configure({
      apiBase: config.apiBase,
      paneId,
      token,
      clientId: pageClientId,
      platform: wsClientPlatform,
      userAgent: wsClientUserAgent,
    });
  }, [paneId, pageClientId, token, wsClientPlatform, wsClientUserAgent, setChatWsState]);

  // (2) Subscribe to messages. Run once for the component lifetime —
  // handleChatWsMessage is a stable useCallback.
  useEffect(() => chatWs.subscribe(handleChatWsMessage), [handleChatWsMessage]);

  // (3) Mirror singleton connection state into React.
  useEffect(() => chatWs.onConnectedChange((connected) => {
    setChatWsConnected(connected);
    setChatWsState({ chatWsConnected: connected, activeChatPaneId: paneIdRef.current });
    if (!connected) {
      setNetLatency(null);
    }
  }), [setChatWsState]);

  useEffect(() => chatWs.onClientIdChange((id) => {
    setChatWsClientId(id);
    setChatWsState({ chatWsClientId: id });
  }), [setChatWsState]);

  useEffect(() => chatWs.onLatencyChange((ms) => { setNetLatency(ms); }), []);

  // Tell the singleton which agent is active. It caches the value and pushes
  // `register_active_channel` on every (re)connect — no React-side guard
  // against "connected yet?" timing needed.
  useEffect(() => {
    chatWs.setActiveAgent(activeCliPaneId, { platform: wsClientPlatform, user_agent: wsClientUserAgent });
  }, [activeCliPaneId, wsClientPlatform, wsClientUserAgent]);

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
    apiService.chatPush({
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
    }).catch(() => {});
  }, [pageClientId, paneId, token]);

  // Bind the ref now that openCodeFile is in scope; chat-ws message handler
  // (declared above for hoisting) reads via openCodeFileRef.current to avoid
  // capturing it as a dep.
  useEffect(() => { openCodeFileRef.current = openCodeFile; }, [openCodeFile]);

  const handleSendPageClientIdToAgent = useCallback(async () => {
    const currentClientId = String(chatWsClientId || pageClientId || '').trim();
    const workspaceState = devStore.getSnapshot().Workspace?.state || {};
    const tmuxTarget = String(workspaceState.activeCliPaneId || '').trim();
    if (!currentClientId || !tmuxTarget) return;
    const promptText = `My browser page clientId: ${currentClientId}.`;
    try {
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: tmuxTarget, q: promptText } }));
      await sendCommandToTmux(promptText, tmuxTarget, true);
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastClientIdSent') }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastClientIdFailed') }));
    }
  }, [activeCliPaneId, chatWsClientId, pageClientId, paneId]);

  // Delegate proxy-node management to the current agent: send it a prompt to use
  // the cicy-mihomo skill to add/edit nodes in mihomo.yaml and reload. The agent
  // owns the skill, so node management stays inside the agent loop.
  const handleManageNodesViaSkill = useCallback(async () => {
    const workspaceState = devStore.getSnapshot().Workspace?.state || {};
    const tmuxTarget = String(workspaceState.activeCliPaneId || activeCliPaneId || '').trim();
    if (!tmuxTarget) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '没有可发送的当前 agent' }));
      return;
    }
    const promptText = '用 cicy-mihomo skill 帮我管理代理节点：编辑 ~/cicy-ai/db/mihomo.yaml 的 proxies 添加/修改节点并加入 default_proxy_group，然后 `cicy-mihomo restart` 生效（控制器 reload 受 SAFE_PATHS 限制，要用 restart）。要加的节点信息先跟我确认。';
    try {
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: tmuxTarget, q: promptText } }));
      await sendCommandToTmux(promptText, tmuxTarget, true);
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '已发送给当前 agent' }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '发送失败' }));
    }
  }, [activeCliPaneId]);

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
  const nativeFilesAgentId = (activeCliPaneId || paneId).split(':')[0];
  // Workspace folder follows the active agent, not the master. Each agent is
  // bound to its own workspace in agent_config; the backend /api/fs/* lookup
  // already keys by agent_id, but the frontend "copy absolute path" feature
  // needs the right prefix too.
  const nativeFilesWorkspace =
    paneDetails[nativeFilesAgentId]?.workspace
    || (nativeFilesAgentId === paneId ? agentDetail?.workspace : '')
    || defaultWorkerWorkspace(nativeFilesAgentId);
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
  // markdown history 里点击文件链接 → 揭示文件视图(FilesView 自己监听同一事件打开 tab)。
  useEffect(() => {
    const reveal = () => {
      setCliContentMode('fixed');
      setCliContentTab('files');
      setCliContentOpen(true);
    };
    window.addEventListener('cicy:open-file', reveal as EventListener);
    return () => window.removeEventListener('cicy:open-file', reveal as EventListener);
  }, []);
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
  const openPaneTodo = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentMode('fixed');
    setCliContentTab('todo');
    setCliContentOpen(true);
  }, [paneId]);
  const openPaneMemory = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentMode('fixed');
    setCliContentTab('memory');
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
      setLangMenuOpen(false);
    };
    const handleWindowResize = () => { setMembershipMenuOpen(false); setLangMenuOpen(false); };
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
      setLangMenuOpen(false);
      return;
    }
    const rect = membershipTriggerRef.current?.getBoundingClientRect();
    if (!rect) return;
    setMembershipPopoverPos({ x: rect.right + 8, y: 0 });
    setMembershipMenuOpen(true);
  }, [membershipMenuOpen]);

  useDevRegister('Workspace', {
    paneId, status, mouseMode, isRestarting,
    netLatency,
    pageClientId, chatWsConnected,
    agentsCount: agents.length,
    activeCliPaneId,
    leftPanel: leftActive, activeWinIdx,
    cliContentMode,
    cliContentTab,
  }, {
    cliContentMode: setCliContentMode,
    cliContentTab: setCliContentTab,
  });
  const cliContentTabs: { id: string; label: string; icon: React.ReactNode }[] = [
    ...(todoSkillInstalled ? [{ id: 'todo', label: t('tabTodo', 'Todo'), icon: <ListTodo className="h-3.5 w-3.5" /> }] : []),
    { id: 'files', label: t('tabFiles'), icon: <Folder className="h-3.5 w-3.5" /> },
    { id: 'session', label: t('tabSession'), icon: <LineChart className="h-3.5 w-3.5" /> },
    { id: 'memory', label: t('tabMemory'), icon: <Brain className="h-3.5 w-3.5" /> },
    { id: 'settings', label: t('tabSettings'), icon: <Settings className="h-3.5 w-3.5" /> },
  ];
  const sessionSubTabs: { id: RequestViewTab; label: string }[] = [
    { id: 'analysis', label: t('tabAnalysis', '分析') },
    { id: 'usage', label: t('tabUsage', '用量') },
    { id: 'meta', label: t('tabMeta') },
    { id: 'tools', label: t('tabTools') },
    { id: 'brain', label: t('tabBrain') },
  ];
  const isSessionTab = (tab: WorkspaceCliContentTab) => tab === 'tools' || tab === 'brain' || tab === 'meta' || tab === 'usage' || tab === 'analysis';
  const renderCliContentPanel = () => (
    // The drawer keeps the FilesView mounted (and laid out off-screen when
    // closed) so its chat-ws :code-ext bridge stays connected — agents can
    // still call agent-editor open / active / tabs even when the user
    // hasn't opened the Files tab yet.
    <div
      ref={cliContentPanelRef}
      data-id="cli-content-fixed"
      className={cn(
        'h-full min-w-0 shrink-0 flex flex-col bg-[#0b0b0d]',
        'border-l border-[var(--vsc-border)]'
      )}
      style={
        cliContentOpen
          ? { width: `${cliDrawerWidth}px`, position: 'relative' }
          : {
              width: `${cliDrawerWidth}px`,
              position: 'absolute',
              right: 0,
              top: 0,
              bottom: 0,
              visibility: 'hidden',
              pointerEvents: 'none',
              zIndex: -1,
            }
      }
    >
      {cliDrawerResizing ? (
        <div
          data-id="cli-content-resize-overlay"
          className="fixed inset-0 z-[40] cursor-col-resize"
        />
      ) : null}
      <div
        data-id="cli-content-resize-handle"
        className={cn(
          // z-40: above the panel content but BELOW every dropdown/popover
          // (Select portal z-200/260, menus z-180/220) and modal (z-10000+),
          // so the thin handle never pokes through an open dropdown/modal.
          'absolute top-0 bottom-0 z-[40] w-1 cursor-col-resize bg-transparent transition-colors hover:bg-blue-400/70 active:bg-blue-400/80',
          'left-0'
        )}
        onMouseDown={handleCliDrawerResizeStart}
      />
      <div data-id="cli-content-tabs-wrap" className="flex h-12 shrink-0 items-center justify-between border-b border-[var(--vsc-border)] px-3">
        <div data-id="cli-content-tabs" className="flex gap-1 overflow-x-auto whitespace-nowrap scrollbar-none">
          {cliContentTabs.map((item) => {
            const active = item.id === 'session' ? isSessionTab(cliContentTab) : cliContentTab === item.id;
            return (
              <button
                data-id={`cli-content-tab-${item.id}`}
                key={item.id}
                type="button"
                className={`shrink-0 select-none rounded-md px-2.5 py-1.5 text-[12px] font-medium leading-5 tracking-[0.01em] transition-colors ${
                  active
                    ? 'bg-white/[0.08] text-zinc-100'
                    : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                }`}
                onClick={() => {
                  if (item.id === 'session') setCliContentTab(lastSessionSubTab);
                  else setCliContentTab(item.id as WorkspaceCliContentTab);
                }}
              >
                <span className="inline-flex items-center gap-1.5">
                  <span data-id={`cli-content-tab-icon-${item.id}`} className="shrink-0 opacity-80">{item.icon}</span>
                  {item.label}
                  {item.id === 'todo' && todoCount > 0 && (
                    <span
                      data-id="cli-content-tab-todo-badge"
                      className="inline-flex h-[16px] min-w-[16px] items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-semibold leading-none text-white tabular-nums"
                    >
                      {todoCount > 99 ? '99+' : todoCount}
                    </span>
                  )}
                </span>
              </button>
            );
          })}
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
      {isSessionTab(cliContentTab) ? (
        <div data-id="cli-content-session-subtabs" className="flex shrink-0 items-center gap-1 border-b border-[var(--vsc-border)] px-3 py-1.5">
          {sessionSubTabs.map((item) => (
            <button
              data-id={`cli-content-session-sub-${item.id}`}
              key={item.id}
              type="button"
              className={`shrink-0 rounded-md px-2 py-1 text-[11px] font-medium leading-4 tracking-[0.01em] transition-colors ${
                cliContentTab === item.id
                  ? 'bg-white/[0.06] text-zinc-100'
                  : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
              }`}
              onClick={() => setCliContentTab(item.id)}
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}
      <div data-id="cli-content-body" className="min-h-0 flex-1 relative">
        <div
          data-id="cli-content-files-host"
          className="absolute inset-0"
          style={cliContentTab === 'files'
            ? { position: 'relative', width: '100%', height: '100%' }
            : { position: 'absolute', width: '100%', height: '100%', visibility: 'hidden', pointerEvents: 'none', zIndex: -1 }
          }
        >
          <FilesView
            agentId={nativeFilesAgentId}
            workspaceFolder={nativeFilesWorkspace}
            pageClientId={pageClientId}
            className="h-full w-full"
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
          data-id="cli-content-usage-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'usage' ? 'block' : 'none' }}
        >
          <AgentUsageLogView paneId={activeCliPaneId} active={cliContentOpen && cliContentTab === 'usage'} />
        </div>
        <div
          data-id="cli-content-analysis-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'analysis' ? 'block' : 'none' }}
        >
          <AgentUsageAnalysisView paneId={activeCliPaneId} active={cliContentOpen && cliContentTab === 'analysis'} />
        </div>
        <div
          data-id="cli-content-todo-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'todo' ? 'block' : 'none' }}
        >
          <TodoPanel paneId={activeCliPaneId} active={cliContentOpen && cliContentTab === 'todo'} isMaster={activeCliPaneId === paneId} />
        </div>
        <div
          data-id="cli-content-memory-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'memory' ? 'block' : 'none' }}
        >
          <AgentInspector
            paneId={activeCliPaneId}
            paneTitle={
              paneDetails[activeCliPaneId]?.title
              || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === activeCliPaneId)?.title
              || activeCliPaneId
            }
            open={cliContentOpen && cliContentTab === 'memory'}
            embedded
            requestedTab={'memory'}
            liveStatus={chatWsLiveStatus}
            inspectorVersion={chatWsInspectorVersion}
            onPanePatch={applyPanePatch}
            onClose={() => {}}
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
  const cliFixedContent = renderCliContentPanel();
  const stackHeaderControls = useCallback((targetPaneId: string) => targetPaneId === activeCliPaneId ? (
    <>
      <ModelPicker
        paneId={activeCliPaneId}
        agentDetail={paneDetails[activeCliPaneId.split(':')[0]] || (activeCliPaneId.split(':')[0] === paneId.split(':')[0] ? agentDetail : null)}
        onUpdated={(patch) => applyPanePatch(activeCliPaneId, patch)}
        onOpen={() => refreshPaneDetail(activeCliPaneId)}
      />
      <button
        type="button"
        data-id="workspace-shell-toggle"
        onClick={toggleShellPanel}
        aria-pressed={shellPanelOpen}
        title={t('shellPanelToggle', { defaultValue: 'Shell 终端' })}
        className={`p-1 rounded transition-colors cursor-pointer ${shellPanelOpen ? 'text-emerald-400' : 'text-zinc-600 hover:text-zinc-300'}`}
      >
        <Terminal className="w-3.5 h-3.5" />
      </button>
      <SystemResourceMonitor paneId={paneId} />
      <NetworkSignal latency={netLatency} connected={chatWsConnected} clientId={chatWsClientId} onSendClientId={handleSendPageClientIdToAgent} />
      <GlobalProxyIndicator placement="up" onManageNodes={handleManageNodesViaSkill} />
      <button data-id="workspace-token-open" onClick={() => setTokenOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title={t('apiTokenButton')}><Key className="w-3.5 h-3.5" /></button>
      <button data-id="workspace-api-open" onClick={() => setApiOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title={t('apiServerButton')}><Server className="w-3.5 h-3.5" /></button>
      {contextUsage != null && (
        <div data-id="context-usage" className="flex items-center gap-1.5 rounded-full bg-white/[0.02] px-2 py-0.5">
          <div data-id="context-bar" className="h-1 w-12 overflow-hidden rounded-full bg-white/[0.04]">
            <div data-id="context-bar-fill" className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400/60' : contextUsage > 50 ? 'bg-yellow-400/60' : 'bg-emerald-400/60'}`} style={{ width: `${contextUsage}%` }} />
          </div>
          <span data-id="context-pct" className="font-mono text-xs leading-none text-zinc-600">{contextUsage}%</span>
        </div>
      )}
    </>
  ) : null, [activeCliPaneId, paneId, paneDetails, agentDetail, applyPanePatch, refreshPaneDetail, netLatency, chatWsConnected, chatWsClientId, handleSendPageClientIdToAgent, handleManageNodesViaSkill, contextUsage, shellPanelOpen, toggleShellPanel, t]);
  // Memoized so the stack's `items` keeps a stable identity across the
  // per-token Workspace re-renders a live conversation triggers (those tokens
  // touch chat-live state the stack never reads). Combined with React.memo on
  // AgentStack, the panel + its ttyd iframes skip those renders entirely.
  const stackItems = useMemo(() => buildCanvasItems({
    paneId,
    token,
    canvasPaneIds,
    agents,
    boundAgents,
    paneDetails,
    pollStatuses,
    agentDetail,
    lang: currentLang,
  }), [paneId, token, canvasPaneIds, agents, boundAgents, paneDetails, pollStatuses, agentDetail, currentLang]);
  const handleStackOpenSession = useCallback((targetPaneId: string) => {
    openPaneRequestView(targetPaneId, lastSessionSubTab);
  }, [openPaneRequestView, lastSessionSubTab]);
  const handleStackActivePaneIdChange = useCallback((targetPaneId: string) => {
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: targetPaneId }));
  }, [paneId]);
  const rightContent = (
    <div data-id="right-tabs" className="h-full relative overflow-hidden">
      <div data-id="chat-tab" className="absolute inset-0 flex justify-center" style={{ display: mainTab === 'chat' ? 'flex' : 'none' }}>
        <div data-id="chat-tab-inner" className="w-full max-w-5xl h-full">
          {/* ChatView reference kept as a comment, temporarily disabled to block its internal stats/chat requests
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
            items={stackItems}
            activePaneId={activeCliPaneId}
            settingsShortcutActive={cliContentOpen && cliContentTab === 'settings'}
            renderHeaderControls={stackHeaderControls}
            showHeaderButtons={!cliContentOpen}
            onOpenPaneSettings={openPaneSettings}
            onOpenPaneFiles={openPaneFiles}
            onOpenPaneSession={handleStackOpenSession}
            onOpenPaneTodo={todoSkillInstalled ? openPaneTodo : undefined}
            onOpenPaneMemory={openPaneMemory}
            onActivePaneIdChange={handleStackActivePaneIdChange}
            onRenamePaneTitle={handleRenamePaneTitle}
            todoCount={todoCount}
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
          <SideBtn dataId="btn-office" active={leftActive === 'office'} icon={<Building2 className="w-5 h-5" />} title={t('sidebarOffice', { defaultValue: '办公室' })} onClick={() => toggleLeft('office')} />
          {/* Helper-mode trial container hides Skills / Providers (gateway) /
              IM / Audit from the activity bar — the drawer should stay
              laser-focused on the install chat. See helperMode in cicy-code
              setup.go and /api/settings/global → helper_mode. */}
          {!globalVar?.helper_mode && (
            <>
              <SideBtn dataId="btn-skill" active={leftActive === 'skills'} icon={<Package className="w-5 h-5" />} title={t('sidebarSkills')} onClick={() => toggleLeft('skills')} />
              <SideBtn dataId="btn-providers" active={leftActive === 'providers'} icon={<Boxes className="w-5 h-5" />} title={t('sidebarProviders')} onClick={() => toggleLeft('providers')} />
              <SideBtn dataId="btn-im" active={leftActive === 'im'} icon={<MessageCircle className="w-5 h-5" />} title={t('sidebarIM', 'IM')} onClick={() => toggleLeft('im')} />
            </>
          )}
        </div>
        <div data-id="activity-bar-bottom" className="flex w-full flex-col items-center gap-3">
          <MobileQRPopover workspaceTitle={topBarTitle} />
          <button
            ref={membershipTriggerRef}
            type="button"
            data-id="activity-bar-membership-trigger"
            onClick={handleToggleMembershipMenu}
            className={cn('group flex h-10 w-10 items-center justify-center rounded-xl transition-all cursor-pointer', membershipTone(membershipCard.level), membershipMenuOpen ? 'text-zinc-100 shadow-[0_12px_30px_rgba(0,0,0,0.28)]' : 'text-zinc-400 hover:text-zinc-100')}
            title={membershipCard.userId}
          >
            <Settings className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Main */}
      <div data-id="main-area" className="flex-1 flex flex-col min-w-0">
        {/* Content */}
        <main data-id="content-area" className="flex-1 relative overflow-hidden">
          <div data-id="main-layout" className="flex h-full min-w-0">
            {leftActive && leftActive !== 'office' ? (
              <div
                data-testid="left-panel"
                data-id="left-panel"
                className="h-full w-[320px] min-w-[320px] max-w-[320px] shrink-0"
              >
                <div data-id="left-panel-wrap" className="h-full flex flex-col bg-[#0A0A0A] border-r border-[var(--vsc-border)] relative z-[130]">
                  <div data-id="left-panel-header" className="h-12 border-b border-[var(--vsc-border)] flex items-center px-2 bg-[#0e0e0e] shrink-0 gap-1">
                    {leftActive === 'agents' ? <>
                      <LayoutList className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-agents" className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelAgents')}</span>
                    </> : leftActive === 'skills' ? <>
                      <Brain className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-skills" className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelSkills')}</span>
                    </> : leftActive === 'providers' ? <>
                      <Boxes className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-providers" className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('sidebarProviders')}</span>
                    </> : leftActive === 'im' ? <>
                      <MessageCircle className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-im" className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('sidebarIM', 'IM')}</span>
                    </> : <>
                      <Users className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-team" className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelTeam')}</span>
                    </>}
                    <button data-id="left-panel-close" onClick={closeLeftPanel} className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"><X className="w-3.5 h-3.5" /></button>
                  </div>
                  <div data-id="left-panel-body" className="flex-1 relative overflow-hidden bg-[#0A0A0A] z-[131]">
                    {leftActive === 'agents' ? (
                      <div data-id="left-panel-agents-view" className="absolute inset-0 overflow-auto">
                        <AgentDrawer agents={agents} paneId={paneId}
                          onSelectAgent={onSelectAgent}
                          onAgentsChange={setAgents}
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
                          onRefreshPoll={() => { try { chatWs.send({ type: 'poll_request' }); } catch {} }}
                          onOpenSettingsPane={(targetPaneId) => {
                            openPaneInCurrentTerminal(targetPaneId);
                            openInspectorForPane(targetPaneId, 'settings');
                          }}
                        />
                      </div>
                    ) : leftActive === 'skills' ? (
                      <div data-id="left-panel-skills-view" className="absolute inset-0">
                        <SkillMarketplacePanel paneId={activeCliPaneId || paneId} />
                      </div>
                    ) : leftActive === 'providers' ? (
                      <div data-id="left-panel-providers-view" className="absolute inset-0" ref={setProvidersLeftMount} />
                    ) : leftActive === 'im' ? (
                      <div data-id="left-panel-im-view" className="absolute inset-0" ref={setImLeftMount} />
                    ) : null}
                  </div>
                </div>
              </div>
            ) : null}
            <div data-testid="right-panel" data-id="right-panel" className="min-w-0 flex-1 relative">
              {leftActive === 'office' ? <MeetingRoom /> : rightContent}
              {leftActive === 'providers' && (
                <div data-id="providers-right-mount" ref={setProvidersRightMount} />
              )}
              {leftActive === 'im' && (
                <div data-id="im-right-mount" ref={setImRightMount} />
              )}
            </div>
            {leftActive === 'providers' && (
              <ProviderDashboard leftMount={providersLeftMount} rightMount={providersRightMount} />
            )}
            {leftActive === 'im' && (
              <IMDashboard leftMount={imLeftMount} rightMount={imRightMount} />
            )}
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
            onRecordEnd={(shouldSend) => {
              const rec = (window as any).__voiceRec as MediaRecorder | undefined;
              if (rec && rec.state !== 'inactive') {
                rec.onstop = async () => {
                  (window as any).__voiceStream?.getTracks().forEach((t: any) => t.enabled = false);
                  if (!shouldSend) return;
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
                <span data-id="membership-dropdown-tag" className={cn('inline-flex rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase', membershipTagTone(membershipCard.level))}>
                  {displayTag}
                </span>
              ) : null;
            })()}
            {membershipCard.expiresAt ? (
              <div data-id="membership-dropdown-expires-at" className="mt-1 text-[11px] font-mono text-zinc-100">{t('membershipExpiresAt', { date: formatDateTime(Date.parse(membershipCard.expiresAt)) })}</div>
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
              <span data-id="membership-renew-btn-label">{t('membershipRenew')}</span>
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
              <span data-id="membership-upgrade-btn-label">{t('membershipUpgrade')}</span>
              <ExternalLink className="h-3.5 w-3.5" />
            </button>
          ) : null}
          {!globalVar?.helper_mode && (
            <button
              type="button"
              data-id="top-bar-audit-dashboard"
              onClick={() => {
                setMembershipMenuOpen(false);
                window.location.hash = '#/audit';
              }}
              className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
              title="Audit Dashboard"
            >
              <span data-id="top-bar-audit-dashboard-label">Audit</span>
              <ShieldCheck className="h-3.5 w-3.5" />
            </button>
          )}
          <button
            type="button"
            data-id="top-bar-github-issues"
            onClick={() => {
              setMembershipMenuOpen(false);
              window.open(GITHUB_ISSUES_URL, '_blank', 'noopener,noreferrer');
            }}
            className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
          >
            <span data-id="top-bar-github-issues-label">GitHub Issues</span>
            <Github className="h-3.5 w-3.5" />
          </button>
          {isDev ? (
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
            <span data-id="membership-devtools-label">{t('debugTools')}</span>
            <Bug className="h-3.5 w-3.5" />
          </button>
          ) : null}
          <div data-id="membership-language" className="mt-1">
            <button
              type="button"
              data-id="membership-language-trigger"
              onPointerDown={(e) => e.stopPropagation()}
              onClick={(e) => {
                e.stopPropagation();
                setLangMenuOpen((open) => !open);
              }}
              className="flex w-full cursor-pointer items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
              title={t('language', { ns: 'common' })}
            >
              <span data-id="membership-language-trigger-label">{t('language', { ns: 'common' })}</span>
              <span data-id="membership-language-trigger-value" className="flex items-center gap-1.5 text-[11px] font-normal text-zinc-400">
                <span data-id="workspace-auto-1" aria-hidden>{flagEmoji(currentLang)}</span>
                <span data-id="membership-language-current">{languageDisplayName(currentLang)}</span>
                <ChevronDown className={`h-3 w-3 transition-transform ${langMenuOpen ? 'rotate-180' : ''}`} />
              </span>
            </button>
            {langMenuOpen ? (
              <div
                data-id="membership-language-menu"
                className="mt-1 max-h-60 overflow-y-auto rounded-lg border border-white/[0.06] bg-[#0c0c0e]"
              >
                {TRANSLATED_LNGS.map((code) => {
                  const active = currentLang === code;
                  return (
                    <button
                      key={code}
                      type="button"
                      data-id={`membership-language-${code}`}
                      onPointerDown={(e) => e.stopPropagation()}
                      onClick={(e) => {
                        e.stopPropagation();
                        setLangMenuOpen(false);
                        if (!active) void i18nLive.changeLanguage(code);
                      }}
                      className={`flex w-full cursor-pointer items-center justify-between gap-2 px-3 py-2 text-left text-[11px] transition-colors hover:bg-white/5 ${active ? 'text-zinc-100' : 'text-zinc-300'}`}
                      title={code}
                    >
                      <span data-id={`membership-language-${code}-label`} className="flex min-w-0 items-center gap-1.5">
                        <span data-id="workspace-auto-2" aria-hidden className="text-[12px] leading-none">{flagEmoji(code)}</span>
                        <span data-id={`membership-language-${code}-name`} className="truncate">{languageDisplayName(code)}</span>
                      </span>
                      {active ? <Check className="h-3 w-3 shrink-0 text-emerald-400" /> : null}
                    </button>
                  );
                })}
              </div>
            ) : null}
          </div>
          <div data-id="membership-version" className="mt-1 flex items-center justify-between rounded-lg px-3 py-2 text-[11px] text-zinc-500">
            <span data-id="membership-version-label">Version</span>
            <span data-id="membership-version-value" id="version" className="font-mono text-zinc-300">{config.version}</span>
          </div>
        </div>,
        document.body
      ) : null}
      {tokenOpen && <TokenDialog onClose={() => setTokenOpen(false)} />}
      {apiOpen && <ApiSwitchDialog onClose={() => setApiOpen(false)} />}
      {toast && <div data-id="workspace-toast" className={`fixed top-4 left-1/2 -translate-x-1/2 z-[9999] px-4 py-2 text-sm rounded-lg shadow-lg ${toast.variant === 'success' ? 'bg-green-600 text-white' : 'bg-zinc-800 text-white'}`}>{toast.message}</div>}
      {dialogsNode}
      <WeChatBindModal />
    </div>
    </SendingProvider>
  );
}

function SideBtn({ dataId, active, icon, title, onClick }: { dataId: string; active: boolean; icon: React.ReactNode; title: string; onClick: () => void }) {
  return (
    <button data-id={dataId} onClick={onClick} className={cn("p-2.5 rounded-xl transition-all relative cursor-pointer", active ? "text-zinc-300 bg-white/[0.06]" : "text-zinc-600 hover:text-zinc-400 hover:bg-white/[0.03]")} title={title}>
      {icon}
      {active && <div data-id={`${dataId}-active-indicator`} className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-5 bg-blue-500/60 rounded-r" />}
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
  lang,
}: {
  paneId: string;
  token: string | null;
  canvasPaneIds: string[];
  agents: any[];
  boundAgents: any[];
  paneDetails: Record<string, any>;
  pollStatuses: Record<string, any>;
  agentDetail: any;
  lang?: string;
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
      ttydSrc: token && !meta.isApiOnly ? urls.ttydOpen(targetPaneId, token, lang) : '',
      workspace: meta.workspace,
      isPrimary: targetPaneId === paneId,
      isApiOnly: meta.isApiOnly,
    };
  });
}

function AgentDrawer({ agents, paneId, onSelectAgent, onAgentsChange, onOpenSettings }: {
  agents: any[]; paneId: string;
  onSelectAgent: (id: string) => void; onAgentsChange: (a: any[]) => void;
  onOpenSettings: (id: string) => void;
}) {
  const { t } = useTranslation('workspace');
  const [search, setSearch] = useState('');
  const [adding, setAdding] = useState(false);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  const { confirm, node: drawerDialogsNode } = useDialogs();

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
        use_custom_gateway: values.use_custom_gateway,
        use_proxy: values.use_proxy,
        project_template: values.project_template,
        role_template: values.role_template,
      });
      const id = data?.pane_id || data?.id;
      if (id) {
        const { data: fresh } = await apiService.getPanes();
        onAgentsChange(Array.isArray(fresh) ? fresh : fresh?.panes || []);
        setCreateDialogOpen(false);
        onSelectAgent(id.split(':')[0]);
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastCreateWorkerFailed') }));
    } finally { setAdding(false); }
  };

  const handleDelete = async (id: string) => {
    const sid = id.split(':')[0];
    if (sid === 'w-10001') return;
    const ok = await confirm({
      body: <Trans i18nKey="drawerConfirmDelete" ns="workspace" values={{ name: sid }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
      danger: true,
    });
    if (!ok) return;
    try {
      await apiService.deletePane(id);
      const { data: fresh } = await apiService.getPanes();
      const list = Array.isArray(fresh) ? fresh : fresh?.panes || [];
      onAgentsChange(list);
      if (sid === paneId) {
        const idx = agents.findIndex(a => (a.pane_id || a.id) === id);
        const next = agents[idx + 1] || agents[idx - 1];
        onSelectAgent(next ? (next.pane_id || next.id).split(':')[0] : 'w-10001');
      }
    } catch {}
  };

  const handleRestart = async (id: string, title: string) => {
    const sid = id.split(':')[0];
    const ok = await confirm({
      body: <Trans i18nKey="drawerConfirmRestart" ns="workspace" values={{ name: title || sid }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
    });
    if (!ok) return;
    try {
      await apiService.restartPane(sid);
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastWorkerRestarting', { name: title || sid }) }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastWorkerRestartFailed', { name: title || sid }) }));
    }
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
            <div data-id="agent-search-input-wrap" className="relative flex-1">
              <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600" />
              <input
                data-id="agent-search-input"
                type="search"
                placeholder={t('drawerSearchPlaceholder')}
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="w-full bg-white/[0.02] border border-[var(--vsc-border)] rounded-lg pl-9 pr-3 py-2 text-sm focus:outline-none focus:border-white/[0.08] placeholder:text-zinc-700 text-zinc-400"
              />
            </div>
            <button data-id="agent-drawer-add" onClick={() => setCreateDialogOpen(true)} disabled={adding}
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
                          <span data-id="agent-menu-open-label">{t('drawerOpen')}</span>
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
                          <span data-id="agent-menu-restart-label">{t('drawerRestart')}</span>
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
                          <span data-id="agent-menu-settings-label">{t('drawerSettings')}</span>
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
                            <span data-id="agent-menu-delete-label">{t('drawerDelete')}</span>
                          </button>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                  <div data-id={`agent-row-body-${shortId}`} className="flex items-center gap-3 flex-1 min-w-0 text-left">
                    <AgentAvatar
                      agentType={agent.agent_type}
                      title={agent.title || shortId}
                      dataId="agent-avatar"
                      variant="panel"
                    />
	                    <div data-id={`agent-row-info-${shortId}`} className="flex-1 min-w-0 pr-7">
	                      <div data-id={`agent-row-title-row-${shortId}`} className="flex items-center gap-1.5">
	                        <h3 data-id={`agent-row-title-${shortId}`} className={cn("text-sm font-medium truncate", isActive ? "text-blue-300" : "text-zinc-300")}>{agent.title || shortId}</h3>
	                      </div>
	                      <p data-id={`agent-row-id-${shortId}`} className={cn("text-xs font-mono mt-0.5 truncate", isActive ? "text-blue-400/50" : "text-zinc-600")}>{shortId}</p>
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
      {drawerDialogsNode}
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

function resourceSeverity(pct: number | null | undefined) {
  if (pct == null || Number.isNaN(pct)) {
    return { text: 'text-zinc-500', bar: 'bg-zinc-600', track: 'bg-white/[0.04]', value: pct };
  }
  if (pct >= 85) return { text: 'text-rose-500/70', bar: 'bg-rose-400', track: 'bg-rose-500/[0.10]', value: pct };
  if (pct >= 65) return { text: 'text-amber-500/70', bar: 'bg-amber-400', track: 'bg-amber-500/[0.10]', value: pct };
  return { text: 'text-zinc-500', bar: 'bg-emerald-400', track: 'bg-emerald-500/[0.08]', value: pct };
}

const ResourceChip = memo(function ResourceChip({ label, pct, dataId }: { label: string; pct: number | null | undefined; dataId: string }) {
  const sev = resourceSeverity(pct);
  const fillPct = sev.value == null || Number.isNaN(sev.value) ? 0 : Math.max(0, Math.min(100, sev.value));
  return (
    <div data-id={dataId} className="flex items-center gap-1.5 leading-none">
      <div data-id={`${dataId}-track`} className={`relative h-3 w-[3px] overflow-hidden rounded-full ${sev.track}`}>
        <div
          data-id={`${dataId}-bar`}
          className={`absolute bottom-0 left-0 right-0 ${sev.bar} transition-[height] duration-300`}
          style={{ height: `${fillPct}%` }}
        />
      </div>
      <span data-id={`${dataId}-label`} className="text-[10px] tracking-[0.06em] text-zinc-500">{label}</span>
    </div>
  );
});

const ResourceRow = memo(function ResourceRow({
  icon,
  label,
  pct,
  sub,
  dataId,
}: {
  icon: React.ReactNode;
  label: string;
  pct: number | null | undefined;
  sub?: string;
  dataId: string;
}) {
  const sev = resourceSeverity(pct);
  const fillPct = sev.value == null || Number.isNaN(sev.value) ? 0 : Math.max(0, Math.min(100, sev.value));
  return (
    <div data-id={dataId} className="px-1">
      <div data-id={`${dataId}-header`} className="flex items-center justify-between gap-2">
        <div data-id={`${dataId}-label-wrap`} className="flex items-center gap-2 min-w-0">
          <span data-id={`${dataId}-icon`} className="text-zinc-500">{icon}</span>
          <span data-id={`${dataId}-label`} className="text-[11px] font-medium text-zinc-300">{label}</span>
        </div>
        <div data-id={`${dataId}-values`} className="flex items-baseline gap-1.5 shrink-0">
          {sub ? <span data-id={`${dataId}-sub`} className="font-mono text-[10px] text-zinc-600 truncate max-w-[160px]">{sub}</span> : null}
          <span data-id={`${dataId}-value`} className={`font-mono text-[12px] tabular-nums ${sev.text}`}>{formatResourcePct(pct)}</span>
        </div>
      </div>
      <div data-id={`${dataId}-track`} className={`mt-1.5 h-[3px] w-full overflow-hidden rounded-full ${sev.track}`}>
        <div
          data-id={`${dataId}-bar`}
          className={`h-full ${sev.bar} transition-[width] duration-500`}
          style={{ width: `${fillPct}%` }}
        />
      </div>
    </div>
  );
});

function ModelPicker({ paneId, agentDetail, onUpdated, onOpen }: { paneId: string; agentDetail: any; onUpdated: (patch: any) => void; onOpen?: () => void }) {
  const { runPaneSaveSerially } = useApp();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handleOutside = (event: Event) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (rootRef.current && rootRef.current.contains(target)) return;
      setOpen(false);
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    // Capture phase so we fire before any inner element calls stopPropagation
    // (terminals, canvases, panes elsewhere on the page may swallow pointer
    // events at bubble phase). mousedown + touchstart cover trackpad/touch and
    // iframes that don't dispatch pointer events to document.
    document.addEventListener('pointerdown', handleOutside, true);
    document.addEventListener('mousedown', handleOutside, true);
    document.addEventListener('touchstart', handleOutside, true);
    document.addEventListener('keydown', handleKeyDown);
    // If focus shifts to an iframe (ttyd, code-server), pointerdown never
    // reaches document — listen for blur on window to detect that.
    const handleWindowBlur = () => {
      if (document.activeElement && document.activeElement.tagName === 'IFRAME') {
        setOpen(false);
      }
    };
    window.addEventListener('blur', handleWindowBlur);
    return () => {
      document.removeEventListener('pointerdown', handleOutside, true);
      document.removeEventListener('mousedown', handleOutside, true);
      document.removeEventListener('touchstart', handleOutside, true);
      document.removeEventListener('keydown', handleKeyDown);
      window.removeEventListener('blur', handleWindowBlur);
    };
  }, [open]);

  const useCustomGateway = !!agentDetail?.use_custom_gateway;
  const agentType = String(agentDetail?.agent_type || '');
  const eligible = useCustomGateway && ['claude', 'codex', 'opencode'].includes(agentType);
  if (!eligible) return null;

  const defaultProviderKey = String(agentDetail?.runtime_ai_default?.provider_name || '').trim();
  const overrideProviderKey = String(agentDetail?.runtime_ai?.provider_name || '').trim();
  const activeProviderKey = overrideProviderKey || defaultProviderKey;
  const providerOptions: any[] = agentDetail?.runtime_ai_provider_options || [];
  const activeProvider = providerOptions.find((p) => p?.key === activeProviderKey);
  const currentModel = String(agentDetail?.default_model || agentDetail?.runtime_ai_default?.model || '');
  const displayModel = currentModel || '—';
  const displayProvider = String(activeProvider?.label || activeProviderKey || '').trim();

  const handleSelect = async (providerKey: string, model: string) => {
    if (saving) return;
    const sameProvider = providerKey === activeProviderKey;
    const sameModel = model === currentModel;
    if (sameProvider && sameModel) { setOpen(false); return; }
    // Runtime override semantic (mirrors the inspector's gateway-override block):
    //   - Pick a model under the agent-type's DEFAULT provider → clear runtime_ai
    //     (no override; gateway routes to the default).
    //   - Pick a model under ANY other provider → set runtime_ai.provider_name to
    //     that provider; gateway routes there on the next request.
    // default_model always reflects the picked model.
    const runtimeAI = providerKey && providerKey !== defaultProviderKey
      ? { provider_name: providerKey }
      : null;
    const payload: Record<string, any> = { default_model: model, runtime_ai: runtimeAI };
    setOpen(false);
    setSaving(true);
    onUpdated(payload);
    try {
      // (A) Per-pane save serialization — shares the same queue as Inspector,
      // so a rapid sequence of ModelPicker click → Inspector toggle is applied
      // to the server in click order.
      await runPaneSaveSerially(paneId, () => apiService.updatePane(paneId, payload));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: 'Failed to update model' }));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div data-id="model-picker-root" ref={rootRef} className="relative mr-auto">
      <button
        type="button"
        data-id="model-picker-trigger"
        onClick={() => setOpen(prev => { const next = !prev; if (next) onOpen?.(); return next; })}
        aria-haspopup="dialog"
        aria-expanded={open}
        title={activeProvider?.label || activeProviderKey || 'Provider'}
        className={`flex h-7 items-center gap-2 rounded-md border px-2.5 transition-colors duration-150 cursor-pointer
          ${open
            ? 'border-white/[0.14] bg-white/[0.05]'
            : 'border-white/[0.06] bg-white/[0.02] hover:border-white/[0.12] hover:bg-white/[0.04]'}
          focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/25`}
      >
        <span data-id="model-picker-current" className="flex max-w-[280px] items-center gap-1.5 truncate font-mono text-[11px]">
          {displayProvider ? (
            <>
              <span data-id="model-picker-provider" className="truncate text-zinc-500">{displayProvider}</span>
              <span data-id="model-picker-sep" className="text-zinc-700">/</span>
            </>
          ) : null}
          <span data-id="model-picker-model" className="truncate text-zinc-300">{displayModel}</span>
        </span>
        <ChevronDown
          data-id="model-picker-chevron"
          className={`h-3 w-3 text-zinc-600 transition-all duration-200 ${open ? 'rotate-180 text-zinc-300' : ''}`}
        />
      </button>
      {open ? (
        <div
          data-id="model-picker-popover"
          role="dialog"
          className="animate-select-in absolute left-0 bottom-[calc(100%+8px)] z-[180] w-[300px] overflow-hidden rounded-xl border border-white/[0.06] bg-[#141416]/[0.98] shadow-[0_20px_50px_-12px_rgba(0,0,0,0.7),inset_0_1px_0_0_rgba(255,255,255,0.04)] backdrop-blur-md"
        >
          <div data-id="model-picker-list" className="max-h-[360px] overflow-y-auto py-1">
            {providerOptions.length === 0 ? (
              <div className="px-3 py-3 text-[12px] text-zinc-500">No providers</div>
            ) : providerOptions.map((p: any) => {
              const pKey = String(p?.key || '');
              const models: string[] = Array.isArray(p?.models) ? p.models.map((m: any) => String(m)) : [];
              const isActiveProvider = pKey === activeProviderKey;
              const isDefaultProvider = pKey === defaultProviderKey;
              return (
                <div key={pKey} data-id={`model-picker-provider-${pKey}`} className="border-b border-white/[0.04] last:border-b-0">
                  <div data-id={`model-picker-provider-header-${pKey}`} className="flex items-center justify-between gap-2 px-3 pt-2 pb-1 text-[10px] uppercase tracking-wider">
                    <div className="flex items-center gap-1.5">
                      <span className={`font-medium ${isActiveProvider ? 'text-blue-300' : 'text-zinc-400'}`}>{p?.label || pKey}</span>
                      {isDefaultProvider ? <span className="rounded bg-zinc-700/40 px-1 py-px text-[9px] font-normal lowercase tracking-normal text-zinc-400">default</span> : null}
                    </div>
                    <span className="font-mono text-[9px] text-zinc-600">{p?.protocol || ''}</span>
                  </div>
                  {models.length === 0 ? (
                    <div className="px-3 py-1.5 text-[11px] text-zinc-600">no models</div>
                  ) : models.map((m) => {
                    const isCurrent = isActiveProvider && m === currentModel;
                    return (
                      <button
                        key={`${pKey}/${m}`}
                        type="button"
                        data-id={`model-picker-item-${pKey}-${m}`}
                        onClick={() => handleSelect(pKey, m)}
                        disabled={saving}
                        className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12px] transition-colors hover:bg-white/[0.04] disabled:cursor-not-allowed disabled:opacity-50 ${isCurrent ? 'bg-blue-500/10 text-blue-200' : 'text-zinc-300'}`}
                      >
                        <span className="flex-1 truncate font-mono">{m}</span>
                        {isCurrent ? <Check className="h-3 w-3 shrink-0" /> : null}
                      </button>
                    );
                  })}
                </div>
              );
            })}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function SystemResourceMonitor({ paneId }: { paneId: string }) {
  const { t } = useTranslation('workspace');
  const { activeChatPaneId, sendChatWsMessage, systemResources } = useApp();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleBlur = () => setOpen(false);
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', handlePointerDown);
    window.addEventListener('blur', handleBlur);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown);
      window.removeEventListener('blur', handleBlur);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [open]);

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

  const cpuPct = systemResources?.cpu_usage_pct;
  const memPct = systemResources?.mem_usage_pct;
  const dskPct = systemResources?.disk_usage_pct;
  const memSub = systemResources?.mem_total_bytes
    ? `${formatResourceBytes(systemResources?.mem_used_bytes)} / ${formatResourceBytes(systemResources?.mem_total_bytes)}`
    : undefined;
  const diskSub = systemResources?.disk_total_bytes
    ? `${formatResourceBytes(systemResources?.disk_used_bytes)} / ${formatResourceBytes(systemResources?.disk_total_bytes)}`
    : undefined;
  const cpuSub = systemResources?.cpu_cores ? `${systemResources.cpu_cores} cores` : undefined;
  const updatedAt = systemResources?.updated_at ? new Date(systemResources.updated_at).toLocaleTimeString(undefined, { hour12: false }) : '--';
  const isLive = systemResources?.updated_at != null;

  return (
    <div data-id="system-resource-root" ref={rootRef} className="relative">
      <button
        type="button"
        data-id="system-resource-trigger"
        onClick={() => setOpen(prev => !prev)}
        aria-haspopup="dialog"
        aria-expanded={open}
        title={t('systemResourceTitle')}
        className={`group/sysres flex h-7 items-center gap-2.5 rounded-md border px-2.5 transition-colors duration-150 cursor-pointer
          ${open
            ? 'border-white/[0.14] bg-white/[0.05]'
            : 'border-white/[0.06] bg-white/[0.02] hover:border-white/[0.12] hover:bg-white/[0.04]'}
          focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/25`}
      >
        <ResourceChip label="C" pct={cpuPct} dataId="system-resource-summary-cpu" />
        <span data-id="workspace-auto-3" className="h-3 w-px bg-white/[0.06]" aria-hidden />
        <ResourceChip label="M" pct={memPct} dataId="system-resource-summary-memory" />
        <span data-id="workspace-auto-4" className="h-3 w-px bg-white/[0.06]" aria-hidden />
        <ResourceChip label="D" pct={dskPct} dataId="system-resource-summary-disk" />
        <ChevronDown
          data-id="system-resource-chevron"
          className={`h-3 w-3 text-zinc-600 transition-all duration-200 ${open ? 'rotate-180 text-zinc-300' : 'group-hover/sysres:text-zinc-400'}`}
        />
      </button>
      {open ? (
        <div
          data-id="system-resource-dropdown"
          role="dialog"
          className="animate-select-in absolute right-0 bottom-[calc(100%+8px)] z-[180] w-[320px] overflow-hidden rounded-xl border border-white/[0.06] bg-[#141416]/[0.98] shadow-[0_20px_50px_-12px_rgba(0,0,0,0.7),inset_0_1px_0_0_rgba(255,255,255,0.04)] backdrop-blur-md"
        >
          <div data-id="system-resource-dropdown-header" className="flex items-center justify-between border-b border-white/[0.05] px-3 py-2.5">
            <div data-id="system-resource-dropdown-title" className="flex items-center gap-2">
              <Activity className="h-3.5 w-3.5 text-zinc-500" />
              <span data-id="system-resource-dropdown-title-label" className="text-[12px] font-medium text-zinc-200">{t('systemResourceTitle')}</span>
            </div>
            <div data-id="system-resource-dropdown-status" className="flex items-center gap-1.5">
              <span data-id="system-resource-dropdown-live-dot" className={`h-1.5 w-1.5 rounded-full ${isLive ? 'bg-emerald-400 animate-pulse' : 'bg-zinc-600'}`} />
              <span data-id="system-resource-updated-at" className="font-mono text-[10px] text-zinc-500">{updatedAt}</span>
            </div>
          </div>
          <div data-id="system-resource-rows" className="flex flex-col gap-2.5 p-3">
            <ResourceRow
              icon={<Cpu className="h-3.5 w-3.5" />}
              label="CPU"
              pct={cpuPct}
              sub={cpuSub}
              dataId="system-resource-row-cpu"
            />
            <ResourceRow
              icon={<MemoryStick className="h-3.5 w-3.5" />}
              label={t('systemResourceMemory')}
              pct={memPct}
              sub={memSub}
              dataId="system-resource-row-memory"
            />
            <ResourceRow
              icon={<HardDrive className="h-3.5 w-3.5" />}
              label={t('systemResourceDisk')}
              pct={dskPct}
              sub={diskSub}
              dataId="system-resource-row-disk"
            />
          </div>
          <div data-id="system-resource-load" className="flex items-center justify-between border-t border-white/[0.05] bg-white/[0.01] px-3 py-2">
            <span data-id="system-resource-load-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-600">{t('systemResourceLoad')}</span>
            <div data-id="system-resource-load-values" className="flex items-baseline gap-1 font-mono text-[11px] text-zinc-400">
              <span data-id="system-resource-load-1" className="tabular-nums">{formatLoadValue(systemResources?.load_1)}</span>
              <span data-id="workspace-auto-5" className="text-zinc-700">·</span>
              <span data-id="system-resource-load-5" className="tabular-nums">{formatLoadValue(systemResources?.load_5)}</span>
              <span data-id="workspace-auto-6" className="text-zinc-700">·</span>
              <span data-id="system-resource-load-15" className="tabular-nums">{formatLoadValue(systemResources?.load_15)}</span>
              <span data-id="system-resource-load-units" className="ml-1 text-[9px] tracking-wider text-zinc-700">1m / 5m / 15m</span>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function networkQuality(connected: boolean, latency: number | null): { bars: 0 | 1 | 2 | 3 | 4; color: string; tone: string; ring: string } {
  if (!connected) return { bars: 0, color: 'bg-rose-400', tone: 'text-rose-300', ring: 'ring-rose-400/40' };
  if (latency === null) return { bars: 4, color: 'bg-emerald-400', tone: 'text-emerald-300', ring: 'ring-emerald-400/40' };
  if (latency < 100) return { bars: 4, color: 'bg-emerald-400', tone: 'text-emerald-300', ring: 'ring-emerald-400/40' };
  if (latency < 200) return { bars: 3, color: 'bg-emerald-400', tone: 'text-emerald-300', ring: 'ring-emerald-400/40' };
  if (latency < 500) return { bars: 2, color: 'bg-amber-400', tone: 'text-amber-300', ring: 'ring-amber-400/40' };
  return { bars: 1, color: 'bg-rose-400', tone: 'text-rose-300', ring: 'ring-rose-400/40' };
}

function NetworkSignal({ latency, connected = true, clientId, onSendClientId }: { latency: number | null; connected?: boolean; clientId?: string | null; onSendClientId?: () => Promise<void> | void }) {
  const { t } = useTranslation('workspace');
  const [copiedId, setCopiedId] = useState(false);
  const [open, setOpen] = useState(false);
  const [sending, setSending] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleBlur = () => setOpen(false);
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', handlePointerDown);
    window.addEventListener('blur', handleBlur);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown);
      window.removeEventListener('blur', handleBlur);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [open]);

  const q = networkQuality(connected, latency);
  const qualityWord = !connected
    ? t('networkOffline')
    : latency === null
      ? t('networkOnline')
      : latency < 100
        ? 'Excellent'
        : latency < 200
          ? 'Good'
          : latency < 500
            ? 'Fair'
            : 'Poor';

  const handleSend = async () => {
    if (!onSendClientId || sending) return;
    setSending(true);
    try {
      await onSendClientId();
    } finally {
      setSending(false);
    }
  };
  const handleCopyClientId = async () => {
    if (!clientId) return;
    let ok = false;
    try {
      if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        await navigator.clipboard.writeText(clientId);
        ok = true;
      }
    } catch {}
    if (!ok) {
      try {
        const textarea = document.createElement('textarea');
        textarea.value = clientId;
        textarea.setAttribute('readonly', 'true');
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();
        document.execCommand('copy');
        document.body.removeChild(textarea);
        ok = true;
      } catch {}
    }
    if (ok) {
      setCopiedId(true);
      window.setTimeout(() => setCopiedId(false), 1200);
    }
  };

  const barHeights = [4, 7, 10, 13];

  return (
    <div
      data-id="network-signal"
      ref={rootRef}
      className="relative"
    >
      <button
        type="button"
        data-id="network-signal-trigger"
        onClick={() => setOpen(prev => !prev)}
        aria-haspopup="dialog"
        aria-expanded={open}
        title={qualityWord}
        className={`flex h-7 items-center gap-2 rounded-md border px-2 transition-colors duration-150 cursor-pointer
          ${open
            ? 'border-white/[0.14] bg-white/[0.05]'
            : 'border-white/[0.06] bg-white/[0.02] hover:border-white/[0.12] hover:bg-white/[0.04]'}
          focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/25`}
      >
        {!connected ? (
          <WifiOff data-id="network-signal-icon" className="h-3.5 w-3.5 text-rose-300 animate-pulse" />
        ) : (
          <div data-id="network-signal-bars" className="flex h-[13px] items-end gap-[2px]">
            {barHeights.map((h, i) => (
              <div
                key={i}
                data-id={`network-signal-bar-${i + 1}`}
                className={`w-[2.5px] rounded-[1px] transition-colors duration-200 ${i < q.bars ? q.color : 'bg-zinc-700/60'}`}
                style={{ height: h }}
              />
            ))}
          </div>
        )}
      </button>

      {open ? (
        <div
          data-id="network-signal-popover"
          className="animate-select-in absolute right-0 bottom-[calc(100%+8px)] z-[180] w-[280px] overflow-hidden rounded-xl border border-white/[0.06] bg-[#141416]/[0.98] shadow-[0_20px_50px_-12px_rgba(0,0,0,0.7),inset_0_1px_0_0_rgba(255,255,255,0.04)] backdrop-blur-md"
        >
          <div data-id="network-signal-popover-header" className="flex items-center justify-between border-b border-white/[0.05] px-3 py-2.5">
            <div data-id="network-signal-popover-title" className="flex items-center gap-2">
              {connected ? <Wifi className="h-3.5 w-3.5 text-zinc-400" /> : <WifiOff className="h-3.5 w-3.5 text-rose-300" />}
              <span data-id="network-signal-popover-title-label" className="text-[12px] font-medium text-zinc-200">WebSocket</span>
            </div>
            <div data-id="network-signal-popover-status" className={`flex items-center gap-1.5 rounded-full bg-white/[0.04] px-2 py-0.5 ring-1 ${q.ring}`}>
              <span data-id="network-signal-popover-status-dot" className={`h-1.5 w-1.5 rounded-full ${q.color} ${connected ? 'animate-pulse' : ''}`} />
              <span data-id="network-signal-popover-status-text" className={`text-[10px] font-medium ${q.tone}`}>{connected ? t('networkConnected') : t('networkDisconnected')}</span>
            </div>
          </div>

          <div data-id="network-signal-popover-latency" className="px-3 py-2.5">
            <div data-id="network-signal-popover-latency-header" className="flex items-center justify-between">
              <span data-id="network-signal-popover-latency-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-600">Latency</span>
              <span data-id="network-signal-popover-latency-quality" className="text-[10px] tracking-wide text-zinc-500">{qualityWord}</span>
            </div>
            <div data-id="network-signal-popover-latency-value-wrap" className="mt-1 flex items-baseline gap-2">
              <span data-id="network-signal-popover-latency-value" className={`font-mono text-lg tabular-nums leading-none ${q.tone}`}>
                {latency != null ? latency : '—'}
              </span>
              <span data-id="network-signal-popover-latency-unit" className="text-[11px] text-zinc-600">ms</span>
            </div>
          </div>

          <div data-id="network-signal-popover-client" className="border-t border-white/[0.05] px-3 py-2.5">
            <span data-id="network-signal-popover-client-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-600">Client ID</span>
            <button
              data-id="network-signal-popover-client-copy"
              type="button"
              onClick={handleCopyClientId}
              disabled={!clientId}
              className="mt-1 group/cid flex w-full items-center gap-1.5 rounded-md border border-white/[0.05] bg-white/[0.02] px-2 py-1.5 text-left transition-colors hover:border-white/[0.10] hover:bg-white/[0.04] disabled:cursor-not-allowed disabled:opacity-60"
              title={clientId ? 'Copy' : ''}
            >
              <span data-id="network-signal-popover-client-value" className="min-w-0 flex-1 truncate font-mono text-[11px] text-zinc-300">
                {clientId || t('networkClientIdMissing')}
              </span>
              {clientId ? (
                copiedId
                  ? <Check className="h-3 w-3 shrink-0 text-emerald-400" />
                  : <Copy className="h-3 w-3 shrink-0 text-zinc-600 transition-colors group-hover/cid:text-zinc-300" />
              ) : null}
            </button>
          </div>

          <div data-id="network-signal-popover-actions" className="flex flex-col gap-1.5 border-t border-white/[0.05] bg-white/[0.01] p-2">
            <button
              type="button"
              data-id="network-signal-send-client-id"
              onClick={() => { void handleSend(); }}
              disabled={sending || !onSendClientId}
              className="inline-flex items-center justify-center gap-1.5 rounded-md border border-white/[0.06] bg-white/[0.03] px-2 py-1.5 text-[11px] font-medium text-zinc-200 transition-all hover:border-white/[0.12] hover:bg-white/[0.06] disabled:cursor-not-allowed disabled:opacity-60"
            >
              <Send className={`h-3 w-3 ${sending ? 'animate-pulse' : ''}`} />
              <span data-id="network-signal-send-client-id-label">{sending ? t('networkSending') : t('networkSendClientId')}</span>
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
