// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useState, useEffect, useRef, useCallback, useMemo, memo, lazy, Suspense } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation, Trans } from 'react-i18next';
import { TRANSLATED_LNGS } from '../i18n';

type ToastState = {
  message: string;
  variant?: 'default' | 'success';
};
import { useApp } from '../contexts/AppContext';
import { isCicyLiteAgent } from '../lib/agentType';
import { electronRPC } from '../lib/speedup/rpc';
import type { SystemResourceSnapshot } from '../contexts/AppContext';
import {
  Terminal, Folder, X, Settings, Brain, Search,
  LayoutList, Users, Plus, ExternalLink, Key, Bug, Server, MoreHorizontal, ChevronDown, Github, Copy, Check, Send, RotateCcw, Boxes, Package, MessageCircle, Route, SlidersHorizontal,
  Cpu, MemoryStick, HardDrive, Activity, Wifi, WifiOff, ShieldCheck, ListTodo, LineChart, Bot, BookOpen, Store, Timer, Grid3X3, Globe2, Smartphone, FolderKanban, History,
} from 'lucide-react';
import { cn } from '../lib/utils';
import { ModelTag, isChatModel } from '../lib/modelTag';
import AgentAvatar from './AgentAvatar';
import MobileQRPopover from './MobileQRPopover';
import { useDevRegister, devStore } from '../lib/devStore';
import { useAuth } from '../contexts/AuthContext';
import { SendingProvider } from '../contexts/SendingContext';
// import ChatView from './chat/ChatView';
import TodoPanel from './TodoPanel';
import AccountMatrixPanel from './settings/AccountMatrixPanel';
// Lazy: these pull in the heavy CodeMirror editor stack. Behind tab gates, so
// dynamic-importing them keeps codemirror off the first-paint critical path.
const FilesView = lazy(() => import('./files/FilesView'));
const KnowledgePanel = lazy(() => import('./knowledge/KnowledgePanel'));
// Lazy: the team roster opens on demand (icon next to create-worker).
const TeamRosterPanel = lazy(() => import('./layout/TeamRosterPanel'));
import { VoiceFloatingButton } from './VoiceFloatingButton';
import TeamPanel from './layout/TeamPanel';
import GlobalProxyIndicator from './layout/GlobalProxyIndicator';
import { ProxyManagerDialog } from './layout/ProxyManagerDialog';
import SkillMarketplacePanel from './layout/SkillMarketplacePanel';
import CustomAgentsPanel from './layout/CustomAgentsPanel';
import AgentInspector, { InspectorTab } from './layout/AgentInspector';
import AgentProviderRequestView, { type RequestViewTab } from './layout/AgentProviderRequestView';
import AgentUsageLogView from './layout/AgentUsageLogView';
import PolicyTab from './audit/PolicyTab';
import AgentUsageAnalysisView from './layout/AgentUsageAnalysisView';
import CurrentHistoryView from './chat/CurrentHistoryView';
import TokenDialog from './layout/TokenDialog';
import useDesktopEvents from './layout/useDesktopEvents';
import type { AgentCanvasItem } from './layout/AgentStack';
import AgentStack, { CardMoreMenu } from './layout/AgentStack';
import { ShellPanel } from './terminal/ShellPanel';
import WeChatBindModal from './im/WeChatBindModal';
import SettingsModal, { type SettingsSection } from './settings/SettingsModal';
import { useDialogs } from './ui/Modal';
import TipBelow from './ui/TipBelow';
import config, { defaultWorkerWorkspace, syncHostHomeFromPath, urls } from '../config';
import apiService from '../services/api';
import { loadHandled } from './audit/auditHandled';
import { sendCommandToTmux } from '../services/mockApi';
import { sendToAgent } from '../services/agentSend';
import { chatWs } from '../services/chatWs';
import { ApiSwitchDialog } from './layout/ApiSwitchDialog';
import CreateAgentDialog, { CreateAgentValues } from './CreateAgentDialog';
import { lockPointer, unlockPointer, clearPointerLock } from '../lib/pointerLock';
import { emitWebFrameMaskEvent } from '../lib/webFrameMask';
import PortsPanel from './layout/PortsPanel';
import ProjectsPanel, { type ProjectAgent } from './projects/ProjectsPanel';

const cache = {
  get: (k: string, def: any) => { try { const v = JSON.parse(localStorage.getItem(k)!); return v ?? def; } catch { return def; } },
  set: (k: string, v: any) => localStorage.setItem(k, JSON.stringify(v)),
};

const CLI_DRAWER_WIDTH_KEY = 'ws_cliDrawerWidth';
const CLI_CONTENT_MODE_KEY = 'ws_cliContentMode';
const cliContentTabKey = (paneId: string) => `TeamPanel:${paneId}.paneId:cliContentTab`;
const leftPanelKey = (masterAgentId: string) => `ws_leftPanel:${masterAgentId}`;
const projectsOpenKey = (masterAgentId: string) => `ws_projectsOpen:${masterAgentId}`;
const cliContentOpenKey = (masterAgentId: string) => `ws_cliContentOpen:${masterAgentId}`;
const chatClientIdStorageKey = (masterAgentId: string) => `cicy_chat_client_id:${masterAgentId}`;
const chatClientIdStorage = (): Storage =>
  typeof (window as any).electronRPC === 'function' ? localStorage : sessionStorage;
function makePageClientId(masterAgentId: string): string {
  const m = String(masterAgentId || 'w-1001').replace(/[^a-zA-Z0-9_-]/g, '') || 'w';
  return `web-${m}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
const CLI_DRAWER_MIN_WIDTH = 360;
const CLI_DRAWER_DEFAULT_WIDTH = 520;
const CLI_DRAWER_MAX_WIDTH = 960;
const TEAM_TERMINAL_ACTIVE_KEY = 'ws_teamTerminalActive';
const GITHUB_ISSUES_URL = 'https://github.com/cicy-ai/cicy-code/issues';
const DOCS_URL = 'https://docs.cicy-ai.com';
const UPGRADE_URL = 'https://cicy-ai.com/team/upgrade';

type MembershipCardState = {
  userId: string;
  level: 'open_source' | 'trial' | 'shared' | 'pro_vm' | 'private_deploy';
  tag: string;
  expiresAt: string | null;
  renewUrl: string | null;
  upgradeUrl: string | null;
};

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

// Flatten the desktop's ipRegion ({country, region, city}) into one display
// string for the chat-client registry, e.g. "US / California / San Jose".
function formatIpRegion(r: any): string {
  if (!r) return '';
  if (typeof r === 'string') return r;
  if (typeof r === 'object') {
    return [r.country, r.region, r.city].map((x) => String(x || '').trim()).filter(Boolean).join(' / ');
  }
  return '';
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

const DEFAULT_MEMBERSHIP_CARD: MembershipCardState = {
  userId: 'open-source',
  level: 'open_source',
  tag: '',
  expiresAt: null,
  renewUrl: null,
  upgradeUrl: UPGRADE_URL,
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
type LeftPanelView = 'team' | 'skills' | 'customAgents' | 'agents' | 'todo' | null;
type WorkspaceCliContentTab = InspectorTab | 'files' | 'todo' | 'audit' | 'github' | 'history' | RequestViewTab;
type CliContentMode = 'fixed';

function normalizeCliContentTab(value: any): WorkspaceCliContentTab {
  if (value === 'files' || value === 'tools' || value === 'brain' || value === 'meta' || value === 'usage' || value === 'analysis' || value === 'settings' || value === 'memory' || value === 'todo' || value === 'audit' || value === 'github' || value === 'history') {
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
    updateGlobalVar,
    isDev,
    setActiveAgentId,
    setAgentDetail: setSharedAgentDetail,
    patchAgentDetail: patchSharedAgentDetail,
    isShellOpen,
    toggleShellOpen,
  } = useApp();
  const { token } = useAuth();
  const { node: dialogsNode } = useDialogs();
  const paneId = agentId || 'w-1001';
  const fullPaneId = `${paneId}:main.0`;

  const mainTab = 'cli' as 'cli' | 'chat';
  const [leftPanelView, setLeftPanelView] = useState<LeftPanelView>(() => {
    const v = cache.get(leftPanelKey(paneId), null);
    // 刷新后恢复上次打开的左栏面板;'todo' 若技能未装会被下方 effect 关掉。
    // 旧缓存里的 'office' 已不在白名单 → 自动落到 null(办公室视图 2026-06-05 下线)。
    const ok: LeftPanelView[] = ['team', 'skills', 'customAgents', 'agents', 'todo'];
    return ok.includes(v) ? v : null;
  });
  const [createAgentOpen, setCreateAgentOpen] = useState(false);
  const [knowledgeOpen, setKnowledgeOpen] = useState(false);
  const [projectsOpen, setProjectsOpen] = useState(() => /^#\/project\/[^/?#]+/.test(window.location.hash) || cache.get(projectsOpenKey(paneId), false) === true);
  const [portsOpen, setPortsOpen] = useState(false);
  const [fixedDomain, setFixedDomain] = useState('');
  const [proxyAvailable, setProxyAvailable] = useState(false);
  useEffect(() => {
    let stopped = false;
    let timer: number | undefined;
    const refresh = async () => {
      try {
        const [accountsRes, instancesRes] = await Promise.all([
          apiService.getIMAccounts(), apiService.getCiCyCloudInstances(),
        ]);
        if (stopped) return;
        const account = (accountsRes?.data?.accounts || []).find((item: any) => item.platform === 'cicy_cloud');
        const instanceID = String(account?.config?.instance_id || '');
        const instance = (instancesRes?.data?.instances || []).find((item: any) => item.instanceId === instanceID);
        // A configured fixed domain is enough to manage ports. Tunnel
        // availability is transient and must not make the Ports control
        // disappear while the heartbeat is reconnecting.
        setFixedDomain(instance?.proxyHost ? `https://${instance.proxyHost}` : '');
        setProxyAvailable(Boolean(instance?.proxyAvailable));
      } catch {
        if (!stopped) { setFixedDomain(''); setProxyAvailable(false); }
      } finally {
        if (!stopped) timer = window.setTimeout(refresh, 15000);
      }
    };
    void refresh();
    return () => { stopped = true; if (timer) window.clearTimeout(timer); };
  }, []);
  const [createAgentSubmitting, setCreateAgentSubmitting] = useState(false);
  const [createAgentInitialValues, setCreateAgentInitialValues] = useState<Partial<CreateAgentValues> | undefined>();
  useEffect(() => {
    const requestCreateAgent = (event: Event) => {
      const detail = (event as CustomEvent).detail || {};
      setCreateAgentInitialValues({
        title: String(detail.title || '').trim(),
        agent_type: 'cicy',
        role_template: String(detail.roleTemplate || '').trim() || 'assistant',
      });
      setCreateAgentOpen(true);
    };
    window.addEventListener('cicy:request-create-agent', requestCreateAgent as EventListener);
    return () => window.removeEventListener('cicy:request-create-agent', requestCreateAgent as EventListener);
  }, []);
  const [, setInspectorOpen] = useState(false);
  const [, setInspectorRequestedTab] = useState<InspectorTab>('overview');
  const [cliContentOpen, setCliContentOpen] = useState(() => cache.get(cliContentOpenKey(paneId), false) === true);
  const [cliContentTab, setCliContentTab] = useState<WorkspaceCliContentTab>(() => normalizeCliContentTab(cache.get(cliContentTabKey(paneId), 'files')));
  // Team roster (花名册) — a top-level overlay inside the cli-tab content area.
  const [rosterOpen, setRosterOpen] = useState(false);
  const [lastSessionSubTab, setLastSessionSubTab] = useState<RequestViewTab>(() => {
    const v = cache.get(cliContentTabKey(paneId), 'files');
    return v === 'meta' || v === 'usage' || v === 'analysis' || v === 'tools' || v === 'brain' ? v : 'analysis';
  });
  // Lazy-mount latch for heavy cli-content tabs. FilesView restores persisted
  // open files, opens fs watchers, and issues stat/read calls
  // on mount — so while mounted-but-hidden they fire fs/stat + fs/read for tabs
  // the user never opened, on every page load. Mount a tab's content only once it
  // has actually been opened; keep it mounted afterwards (tree expansion + open
  // files persist across tab switches). Closed component ⇒ no requests.
  const [seenCliTabs, setSeenCliTabs] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    if (!cliContentOpen) return;
    setSeenCliTabs((prev) => (prev.has(cliContentTab) ? prev : new Set(prev).add(cliContentTab)));
  }, [cliContentOpen, cliContentTab]);
  useEffect(() => {
    if (cliContentTab === 'meta' || cliContentTab === 'usage' || cliContentTab === 'analysis' || cliContentTab === 'tools' || cliContentTab === 'brain') {
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
  // Pending-review count (_inbox) for the red badge on the Knowledge activity icon.
  const [knowledgePendingCount, setKnowledgePendingCount] = useState<number>(0);
  const [auditAlertCount, setAuditAlertCount] = useState<number>(0);
  const [cliDrawerWidth, setCliDrawerWidth] = useState(() => clampCliDrawerWidth(Number(cache.get(CLI_DRAWER_WIDTH_KEY, CLI_DRAWER_DEFAULT_WIDTH))));
  const [cliDrawerResizing, setCliDrawerResizing] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [apiOpen, setApiOpen] = useState(false);
  const [proxyManagerOpen, setProxyManagerOpen] = useState(false);
  const [toast, setToast] = useState<ToastState | null>(null);
  const toastTimerRef = useRef<number>(0);
  // Unified Settings modal (Language / IM / Agent Routing / LLM Providers).
  // Replaces the old activity-bar left-panels for providers & im.
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsSection, setSettingsSection] = useState<SettingsSection>('language');
  const [mobileQROpen, setMobileQROpen] = useState(false);
  // Red badge in Settings → LLM 供应商: true when any agent-type's routed
  // provider has an empty apiKey (e.g. the seeded deepseek/groq defaults ship
  // keyless). Refetched when the settings modal closes so filling a key clears it.
  const [providersNeedKey, setProvidersNeedKey] = useState(false);
  useEffect(() => {
    let alive = true;
    apiService.getProviders().then((resp: any) => {
      const d = resp?.data || {};
      const defaults = d.defaults || d.default || {};
      const byKey: Record<string, any> = {};
      for (const it of (d.items || [])) byKey[it.key] = it;
      const need = Object.values(defaults).some((rk) => {
        const it = byKey[rk as string];
        return !!it && !String(it.apiKey || '').trim();
      });
      if (alive) setProvidersNeedKey(need);
    }).catch(() => {});
    return () => { alive = false; };
  }, [settingsOpen]);
  // Red badge on the Skills entry (btn-skill): true when any PUBLIC-registry skill
  // has an update available. Public = from the public registry (registry_source
  // empty) and not a locally-authored skill (source !== 'user'). Rechecked on
  // mount, on the `cicy:skills-changed` UI event, on window focus, and when the
  // Skills panel is opened — so a CLI-side `skill install` (which fires no browser
  // event) still clears the badge as soon as you return to / open the panel.
  const [publicSkillUpdate, setPublicSkillUpdate] = useState(false);
  const checkPublicSkillUpdate = useCallback(async () => {
    try {
      const res: any = await apiService.listMarketSkills();
      const skills: any[] = Array.isArray(res?.data?.skills) ? res.data.skills : [];
      const has = skills.some((s) => s?.has_update && !s?.registry_source && s?.source !== 'user');
      setPublicSkillUpdate(has);
    } catch { /* leave the badge as-is on transient failures */ }
  }, []);
  useEffect(() => {
    checkPublicSkillUpdate();
    const onChange = () => { checkPublicSkillUpdate(); };
    window.addEventListener('cicy:skills-changed', onChange);
    window.addEventListener('focus', onChange);
    return () => {
      window.removeEventListener('cicy:skills-changed', onChange);
      window.removeEventListener('focus', onChange);
    };
  }, [checkPublicSkillUpdate]);
  // Red badge (version row + activity-bar trigger) when a newer cicy-code is
  // published on npm. Backend caches the registry lookup; we re-check on focus.
  const [versionUpdate, setVersionUpdate] = useState(false);
  const checkVersionUpdate = useCallback(async () => {
    try {
      const res: any = await apiService.getCicyUpdateStatus();
      setVersionUpdate(!!res?.data?.has_update);
    } catch { /* leave the badge as-is on transient failures */ }
  }, []);
  useEffect(() => {
    checkVersionUpdate();
    const onFocus = () => { checkVersionUpdate(); };
    window.addEventListener('focus', onFocus);
    // Periodic re-check (30min == backend cache TTL) so the badge appears even
    // if the tab is left open and never re-focused, on top of the focus trigger.
    const timer = window.setInterval(() => { checkVersionUpdate(); }, 30 * 60 * 1000);
    return () => { window.removeEventListener('focus', onFocus); window.clearInterval(timer); };
  }, [checkVersionUpdate]);
  // Click-to-update: POST triggers cicy-code-update (server restarts itself), then
  // we poll until it's back on a new version and reload to pick up the new build.
  const [updating, setUpdating] = useState(false);
  const applyUpdate = useCallback(async () => {
    if (updating) return;
    setUpdating(true);
    try {
      const res: any = await apiService.applyCicyUpdate();
      if (!res?.data?.started) { setUpdating(false); return; }
      const startedAt = Date.now();
      const poll = async () => {
        if (Date.now() - startedAt > 180000) { setUpdating(false); return; } // give up after 3min
        try {
          const s: any = await apiService.getCicyUpdateStatus();
          const cur = s?.data?.current;
          if (cur && cur !== config.version) { window.location.reload(); return; }
        } catch { /* server restarting — keep polling */ }
        window.setTimeout(poll, 4000);
      };
      window.setTimeout(poll, 6000); // let the restart begin before first poll
    } catch { setUpdating(false); }
  }, [updating]);
  // Red badge on the Settings entry + 通用 item: true when the token-delivery
  // email isn't fully set up — SMTP not ready OR no delivery address (default_to).
  // Refetched when the settings modal closes so configuring it clears the badge.
  const [emailNeedsSetup, setEmailNeedsSetup] = useState(false);
  useEffect(() => {
    let alive = true;
    apiService.getEmailConfig().then((resp: any) => {
      const d = resp?.data || {};
      const need = !d.smtp_ready || !String(d.default_to || '').trim();
      if (alive) setEmailNeedsSetup(need);
    }).catch(() => {});
    return () => { alive = false; };
  }, [settingsOpen]);
  const openSettings = useCallback((s: SettingsSection) => {
    setSettingsSection(s);
    setSettingsOpen(true);
    setMembershipMenuOpen(false);
    setLangMenuOpen(false);
  }, []);
  // Let deep children (e.g. the audit policy panel's "configure SMTP" button)
  // open the global Settings modal at a given section via a window event.
  useEffect(() => {
    const onOpen = (e: Event) => {
      const sec = (e as CustomEvent)?.detail?.section as SettingsSection | undefined;
      openSettings(sec || 'general');
    };
    window.addEventListener('cicy:open-settings', onOpen as EventListener);
    return () => window.removeEventListener('cicy:open-settings', onOpen as EventListener);
  }, [openSettings]);

  const [status, setStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode] = useState<'on' | 'off'>('off');
  const [isRestarting] = useState(false);
  const [agents, setAgents] = useState<any[]>([]);
  const submitCreateAgent = useCallback(async (values: CreateAgentValues) => {
    setCreateAgentSubmitting(true);
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
        lang: values.lang,
        api_style: values.api_style,
      });
      const id = data?.pane_id || data?.id;
      if (id) {
        const { data: fresh } = await apiService.getPanes();
        setAgents(Array.isArray(fresh) ? fresh : fresh?.panes || []);
        setCreateAgentOpen(false);
        setCreateAgentInitialValues(undefined);
        onSelectAgent(String(id).split(':')[0]);
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastCreateWorkerFailed') }));
    } finally {
      setCreateAgentSubmitting(false);
    }
  }, [onSelectAgent, t]);
  const [boundAgents, setBoundAgents] = useState<any[]>([]);
  const [pollStatuses, setPollStatuses] = useState<Record<string, any>>({});
  const [paneDetails, setPaneDetails] = useState<Record<string, any>>({});
  // Fresh paneDetails for the chat-ws message handler (a stable useCallback that
  // reads refs, not state) — used to route code.send_path by the target agent type.
  const paneDetailsRef = useRef<Record<string, any>>({});
  useEffect(() => { paneDetailsRef.current = paneDetails; }, [paneDetails]);
  const [activeTeamPaneId, setActiveTeamPaneId] = useState<Record<string, string>>(() => cache.get(TEAM_TERMINAL_ACTIVE_KEY, {}));
  const [, setInspectorPaneId] = useState(paneId);
  const [, setCanvasLocateRequest] = useState<{ paneId: string; nonce: number; zoomToActual?: boolean } | null>(null);
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

  // Live system_resources (CPU/mem) arrives ~1/sec and always differs, so
  // applying it every second re-renders every useApp() consumer — including
  // this whole component tree — for a stat widget that doesn't need sub-second
  // refresh. Throttle to a few seconds to cut that constant render load.
  const lastSysResAtRef = useRef(0);
  const applySystemResources = useCallback((next: SystemResourceSnapshot) => {
    if (isDeepEqual(systemResourcesRef.current, next)) return;  // unchanged
    const now = performance.now();
    if (now - lastSysResAtRef.current < 3000) return;           // throttle window
    lastSysResAtRef.current = now;
    systemResourcesRef.current = next;
    setSystemResources(next);
  }, [setSystemResources]);

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
  // chat-ws client id, bound to the current master agent (paneId is the short master id, e.g. "w-1001").
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
  // When running inside cicy-desktop, fetch the host's deviceId + egress IP +
  // IP region + system language once (via the get_device_info RPC) so they can
  // ride the chat-WS register and surface in GET /api/chat/clients. No-op in a
  // plain browser (electronRPC absent).
  const [desktopDeviceInfo, setDesktopDeviceInfo] = useState<Record<string, any> | null>(null);
  useEffect(() => {
    if (typeof (window as any).electronRPC !== 'function') return;
    let cancelled = false;
    (async () => {
      try {
        const raw = await electronRPC('get_device_info');
        const info = typeof raw === 'string' ? JSON.parse(raw) : raw;
        if (!cancelled && info && typeof info === 'object') setDesktopDeviceInfo(info);
      } catch {
        /* not in desktop / unavailable — ignore */
      }
    })();
    return () => { cancelled = true; };
  }, []);
  const [chatWsLiveStatus, setChatWsLiveStatus] = useState('idle');
  const [, setChatWsLiveText] = useState('');
  const [chatWsHistoryVersion, setChatWsHistoryVersion] = useState(0);
  const [chatWsInspectorVersion, setChatWsInspectorVersion] = useState(0);
  const [, setChatSuggestionText] = useState('');
  const [, setChatSuggestionPending] = useState(false);
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

  const [showVoiceControl] = useState(false);
  const [voiceLoading, setVoiceLoading] = useState(false);
  const [voiceBtnPos, setVoiceBtnPos] = useState(() => cache.get('ws_voiceBtnPos', { x: 20, y: Math.max(60, window.innerHeight - 400) }));

  const [panelPos] = useState(() => cache.get('agent_panelPos', { x: 20, y: Math.max(60, window.innerHeight - 280) }));
  const [panelSize] = useState(() => cache.get('agent_panelSize', { width: 360, height: 220 }));
  const [activeWinIdx] = useState('0');
  const activityBarRef = useRef<HTMLDivElement>(null);

  const addApp = (window as any).__desktopAddApp || (() => {});
  useDesktopEvents(addApp);

  const leftActive = leftPanelView;
  // Opening the Skills panel rechecks the update badge (catches CLI-side installs).
  useEffect(() => { if (leftActive === 'skills') checkPublicSkillUpdate(); }, [leftActive, checkPublicSkillUpdate]);
  // External "open profile settings" request (from agent-webpage send open_profile_config,
  // relayed by useDesktopEvents). Open Account Matrix → Devices and hand the request to it.
  const [pendingProfileConfig, setPendingProfileConfig] = useState<{ backend: 'chrome' | 'electron'; accountIdx: number; nonce: number } | null>(null);
  useEffect(() => {
    const h = (e: Event) => {
      const d = (e as CustomEvent).detail || {};
      setPendingProfileConfig({ backend: d.backend === 'chrome' ? 'chrome' : 'electron', accountIdx: Number(d.accountIdx), nonce: Date.now() });
      setCliContentMode('fixed');
      setCliContentTab('github');
      setCliContentOpen(true);
    };
    window.addEventListener('cicy-open-profile-config', h as EventListener);
    return () => window.removeEventListener('cicy-open-profile-config', h as EventListener);
  }, []);
  // Hand a browser window to the active agent: send the descriptive prompt to
  // its tmux pane (same path as voice / file-path send), surfaced in the chat.
  const sendBrowserToAgent = useCallback((text: string) => {
    const tmuxTarget = activeCliPaneIdRef.current || paneId;
    // Insert into the agent's input WITHOUT submitting (no Enter) — the user edits/
    // sends it themselves. Routes by type: cicy agents get it in their composer.
    const at = String(paneDetailsRef.current[tmuxTarget.split(':')[0]]?.agent_type || '');
    sendToAgent(tmuxTarget, text, { submit: false, agentType: at }).catch(() => {});
  }, [paneId]);
  const closeLeftPanel = useCallback(() => {
    setLeftPanelView(null);
  }, []);
  // The left menu panel closes from EXACTLY two places, by design:
  //   - data-id="left-panel-close" (the X in the panel header) → closeLeftPanel
  //   - data-id="btn-team" / the activity-bar icons → toggleLeft (toggles itself off)
  // No click-outside / blur auto-close — those felt random and were removed.

  useEffect(() => {
    cache.set(leftPanelKey(paneId), leftActive);
  }, [leftActive, paneId]);
  useEffect(() => { cache.set(projectsOpenKey(paneId), projectsOpen); }, [paneId, projectsOpen]);
  useEffect(() => {
    if (!projectsOpen) return;
    setLeftPanelView(null);
    setKnowledgeOpen(false);
    setCliContentOpen(false);
  }, [projectsOpen]);
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
        // Local installed-skills scan (fast) — NOT the ~2s remote market catalog.
        const res: any = await apiService.getInstalledSkills();
        const skills = Array.isArray(res?.data?.skills) ? res.data.skills : [];
        const installed = skills.some((s: any) => s?.name === 'cicy-todo');
        if (!cancelled) setTodoSkillInstalled(installed);
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
    const response: any = await apiService.updatePane(targetPaneId, { title: nextTitle });
    applyPanePatch(targetPaneId, { title: nextTitle });
    setBoundAgents(prev => prev.map((item: any) => {
      const id = String(item?.name || item?.pane_id || '').replace(/:.*$/, '');
      return id === targetPaneId ? { ...item, title: nextTitle } : item;
    }));
    try {
      const { data } = await apiService.getPanes();
      setAgents(Array.isArray(data) ? data : data?.panes || []);
    } catch {}
    const failures = response?.data?.feishu_title_sync?.failures;
    if (Array.isArray(failures) && failures.length > 0) {
      window.dispatchEvent(new CustomEvent('show-toast', {
        detail: t('agentTitleFeishuSyncFailed'),
      }));
    }
  }, [applyPanePatch, t]);

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
      window.clearTimeout(toastTimerRef.current);
      toastTimerRef.current = window.setTimeout(() => setToast(null), 5000);
    };
    window.addEventListener('show-toast', handler as EventListener);
    return () => {
      window.removeEventListener('show-toast', handler as EventListener);
      window.clearTimeout(toastTimerRef.current);
    };
  }, []);

  // Status change listener (from WebSocket)
  useEffect(() => {
    const handler = (e: CustomEvent) => { if (e.detail?.status) setStatus(e.detail.status); };
    window.addEventListener('agent-status-change', handler as EventListener);
    return () => window.removeEventListener('agent-status-change', handler as EventListener);
  }, []);

  const toggleLeft = (p: 'team' | 'skills' | 'customAgents' | 'todo') => {
    setPortsOpen(false);
    setProjectsOpen(false);
    setLeftPanelView(prev => prev === p ? null : p);
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
      if (typeof document !== 'undefined' && document.hidden) return; // 后台 webview 不轮询
      try {
        const res: any = await apiService.getTodoCounts(activeCliPaneId);
        const c = res?.data || {};
        if (!cancelled) setTodoCount(Number(c.todo) || 0);
      } catch { /* keep previous count on transient error */ }
    };
    refresh();
    const id = window.setInterval(refresh, 10000);
    const onVis = () => { if (!document.hidden) void refresh(); }; // 回前台立即补一次
    document.addEventListener('visibilitychange', onVis);
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
    return () => { cancelled = true; window.clearInterval(id); document.removeEventListener('visibilitychange', onVis); window.removeEventListener('cicy:todos-changed', onChange); };
  }, [todoSkillInstalled, activeCliPaneId]);
  // 知识库待评审角标(knowledgePendingCount)与审计告警角标(auditAlertCount)不再各自
  // HTTP 轮询 —— 两个计数都随 5s 的 WS poll_data 推送下来(见上面 poll_data 消费处:
  // knowledge_pending 直接取;audit_open_ids 由服务端算好、本地再减去 localStorage 的
  // 已处理集)。这样面板没开也不会有独立请求,后台 webview 更是零请求。
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
      // 转发给历史视图(CurrentHistoryView):cicy agent 用它做真 WS 直推(delta
      // 直接追加进 live 尾巴渲染),其他 agent 用它当"催更"信号提前拉 reply.json。
      window.dispatchEvent(new CustomEvent('cicy:agent-stream-delta', { detail: { ...msg.data, kind: 'text' } }));
    } else if (msg?.type === 'thinking_chunk' && msg.data) {
      // thinking 阶段同样直推 —— 这是最长的阶段,只靠轮询最迟钝。
      window.dispatchEvent(new CustomEvent('cicy:agent-stream-delta', { detail: { ...msg.data, kind: 'thinking' } }));
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
        // cicy-lite agents have no terminal — typing into tmux is a no-op. Fill
        // their chat composer (DispatcherChat) instead so the path lands in the
        // input box; terminal agents keep the type-into-tmux path.
        const targetType = String(paneDetailsRef.current[tmuxTarget.split(':')[0]]?.agent_type || '');
        if (isCicyLiteAgent(targetType)) {
          window.dispatchEvent(new CustomEvent('cicy:fill-composer', { detail: { paneId: tmuxTarget, text: promptText } }));
        } else {
          window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: tmuxTarget, q: promptText } }));
          sendCommandToTmux(promptText, tmuxTarget, false).then(() => {
            focusTmuxPaneFrame(tmuxTarget);
          }).catch(() => {
            window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastSendFilePathFailed') }));
          });
        }
      }
    } else if (msg?.type === 'poll_data' && msg.data) {
      const data = msg.data;
      const nextBoundAgents = Array.isArray(data.agents) ? data.agents : [];
      const nextPollStatuses = data.statuses && typeof data.statuses === 'object' ? data.statuses : {};
      setBoundAgents((prev) => isDeepEqual(prev, nextBoundAgents) ? prev : nextBoundAgents);
      setPollStatuses((prev) => isDeepEqual(prev, nextPollStatuses) ? prev : nextPollStatuses);
      if (data.system_resources && typeof data.system_resources === 'object') {
        applySystemResources(data.system_resources as SystemResourceSnapshot);
      }
      if (data.membership && typeof data.membership === 'object') {
        setGlobalVar((prev: any) => {
          const base = prev && typeof prev === 'object' ? prev : {};
          return isDeepEqual(base.membership, data.membership) ? prev : { ...base, membership: data.membership };
        });
      }
      // Two global badge counts now ride poll_data (no separate HTTP polling):
      //   - knowledge_pending: count straight through.
      //   - audit_open_ids: server already excluded server-acked alerts; subtract
      //     the operator's local-dismiss set (localStorage) for the final count.
      if (typeof data.knowledge_pending === 'number') {
        setKnowledgePendingCount((prev) => prev === data.knowledge_pending ? prev : data.knowledge_pending);
      }
      if (Array.isArray(data.audit_open_ids)) {
        const handled = loadHandled();
        const open = (data.audit_open_ids as string[]).filter((id) => !handled[id]).length;
        setAuditAlertCount((prev) => prev === open ? prev : open);
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
      applySystemResources(msg.data as SystemResourceSnapshot);
    }
    broadcastChatWsMessage(msg);
  }, [applySystemResources, broadcastChatWsMessage, fullPaneId, setChatWsState, setGlobalVar, t]);

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
    const extra: Record<string, any> = { platform: wsClientPlatform, user_agent: wsClientUserAgent };
    if (desktopDeviceInfo) {
      if (desktopDeviceInfo.publicIp) extra.public_ip = desktopDeviceInfo.publicIp;
      const region = formatIpRegion(desktopDeviceInfo.ipRegion);
      if (region) extra.ip_region = region;
      if (desktopDeviceInfo.systemLanguage) extra.system_language = desktopDeviceInfo.systemLanguage;
      if (desktopDeviceInfo.deviceId) extra.device_id = desktopDeviceInfo.deviceId;
    }
    chatWs.setActiveAgent(activeCliPaneId, extra);
  }, [activeCliPaneId, wsClientPlatform, wsClientUserAgent, desktopDeviceInfo]);

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

  // "管理节点" opens the same ProxyManagerDialog drawer the skill-detail page
  // uses (data-id="skill-detail-manage-proxy") — direct UI management, no
  // agent round-trip.
  const handleOpenProxyManager = useCallback(() => setProxyManagerOpen(true), []);

  const topBarPaneId = activeCliPaneId || paneId;
  const topBarDetail = paneDetails[topBarPaneId] || (topBarPaneId === paneId ? agentDetail : null);
  const topBarTitle = topBarDetail?.title
    || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === topBarPaneId)?.title
    || (topBarPaneId === paneId ? title : topBarPaneId);
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
  // Open a workspace-relative file in a specific agent's file editor. Switches
  // the content drawer to that agent's FilesView, then dispatches cicy:open-file
  // on the next tick so the re-rendered FilesView (now scoped to the source
  // agent) handles the event. Used by the fork-confirm modal.
  const openAgentFile = useCallback((targetPaneId: string, relPath: string) => {
    if (!targetPaneId || !relPath) return;
    openPaneFiles(targetPaneId);
    setTimeout(() => {
      window.dispatchEvent(new CustomEvent('cicy:open-file', { detail: { path: relPath } }));
    }, 80);
  }, [openPaneFiles]);
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
  const openAgentGuidanceDetail = useCallback((targetPaneId: string) => {
    openPaneMemory(targetPaneId);
  }, [openPaneMemory]);
  useEffect(() => {
    const revealRole = (event: Event) => {
      const slug = String((event as CustomEvent).detail?.slug || '').trim();
      if (!slug) return;
      openPaneMemory(paneId);
      window.setTimeout(() => {
        window.dispatchEvent(new CustomEvent('cicy:open-role', { detail: { slug } }));
      }, 80);
    };
    window.addEventListener('cicy:reveal-role', revealRole as EventListener);
    return () => window.removeEventListener('cicy:reveal-role', revealRole as EventListener);
  }, [openPaneMemory, paneId]);
  // Generic opener for the agent-card header buttons that mirror cli-content-tabs
  // (audit/account matrix) — open the named content tab for that pane.
  const openPaneContent = useCallback((targetPaneId: string, tab: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    setActiveTeamPaneId(prev => ({ ...prev, [paneId]: clean }));
    setCliContentMode('fixed');
    setCliContentTab(normalizeCliContentTab(tab));
    setCliContentOpen(true);
  }, [paneId]);
  const handleSkillDetailOpen = useCallback(() => {
    setCliContentMode('fixed');
    setCliContentOpen(true);
  }, []);
  const handleSkillDetailClose = useCallback(() => {
    setCliContentOpen(false);
  }, []);
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
    { id: 'history', label: t('history', { ns: 'chat', defaultValue: '历史' }), icon: <History className="h-3.5 w-3.5" /> },
    ...(todoSkillInstalled ? [{ id: 'todo', label: t('tabTodo', 'Todo'), icon: <ListTodo className="h-3.5 w-3.5" /> }] : []),
    { id: 'files', label: t('tabFiles'), icon: <Folder className="h-3.5 w-3.5" /> },
    { id: 'session', label: t('tabSession'), icon: <LineChart className="h-3.5 w-3.5" /> },
    { id: 'memory', label: t('tabMemory'), icon: <Brain className="h-3.5 w-3.5" /> },
    ...(globalVar?.audit_enabled === true ? [{ id: 'audit', label: t('tabAudit', { ns: 'audit', defaultValue: '审计' }), icon: <ShieldCheck className="h-3.5 w-3.5" /> }] : []),
    { id: 'github', label: t('accountMatrixTitle', { defaultValue: '账号矩阵' }), icon: <Grid3X3 className="h-3.5 w-3.5" /> },
    { id: 'settings', label: t('tabSettings'), icon: <Settings className="h-3.5 w-3.5" /> },
  ];
  useEffect(() => {
    if (globalVar?.audit_enabled !== true && cliContentTab === 'audit') {
      setCliContentTab('files');
    }
  }, [globalVar?.audit_enabled, cliContentTab]);
  const sessionSubTabs: { id: RequestViewTab; label: string }[] = [
    { id: 'analysis', label: t('tabAnalysis', '分析') },
    { id: 'usage', label: t('tabUsage', '用量') },
    { id: 'meta', label: t('tabMeta') },
    { id: 'tools', label: t('tabTools') },
    { id: 'brain', label: t('tabBrain') },
  ];
  const isSessionTab = (tab: WorkspaceCliContentTab) => tab === 'meta' || tab === 'usage' || tab === 'analysis' || tab === 'tools' || tab === 'brain';
  const renderCliContentPanel = () => (
    // The drawer keeps the FilesView mounted (and laid out off-screen when
    // closed) so its chat-ws :code-ext bridge stays connected — agents can
    // still call agent-editor open / active / tabs even when the user
    // hasn't opened the Files tab yet.
    <div
      ref={cliContentPanelRef}
      data-id="right-panel"
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
        <div data-id="cli-content-tabs" className="flex min-w-0 flex-1 gap-1 overflow-x-auto whitespace-nowrap scrollbar-hairline">
          {cliContentTabs.map((item) => {
            const active = item.id === 'session' ? isSessionTab(cliContentTab) : cliContentTab === item.id;
            return (
              <TipBelow key={item.id} label={item.label} className="shrink-0">
              <button
                data-id={`cli-content-tab-${item.id}`}
                type="button"
                className={`relative shrink-0 select-none rounded-md px-2.5 py-1.5 transition-colors ${
                  active
                    ? 'bg-white/[0.08] text-zinc-100'
                    : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                }`}
                onClick={() => {
                  if (item.id === 'session') setCliContentTab(lastSessionSubTab);
                  else setCliContentTab(item.id as WorkspaceCliContentTab);
                }}
              >
                <span className="inline-flex h-5 items-center">
                  {item.icon}
                </span>
                {item.id === 'todo' && todoCount > 0 && (
                  <span
                    data-id="cli-content-tab-todo-badge"
                    className="pointer-events-none absolute right-0 top-0 inline-flex h-[13px] min-w-[13px] items-center justify-center rounded-full bg-red-500 px-[3px] text-[9px] font-semibold leading-none text-white tabular-nums"
                  >
                    {todoCount > 99 ? '99+' : todoCount}
                  </span>
                )}
                {item.id === 'audit' && auditAlertCount > 0 && (
                  <span
                    data-id="cli-content-tab-audit-badge"
                    className="pointer-events-none absolute right-0 top-0 inline-flex h-[13px] min-w-[13px] items-center justify-center rounded-full bg-red-500 px-[3px] text-[9px] font-semibold leading-none text-white tabular-nums"
                  >
                    {auditAlertCount > 99 ? '99+' : auditAlertCount}
                  </span>
                )}
              </button>
              </TipBelow>
            );
          })}
        </div>
        <div data-id="cli-content-actions" className="ml-3 flex shrink-0 items-center gap-1">
          <TipBelow label={t('close', { ns: 'common' })}>
          <button
            data-id="cli-content-close"
            type="button"
            onClick={() => {
              setCliContentOpen(false);
            }}
            className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
            aria-label={t('close', { ns: 'common' })}
          >
            <X className="h-4 w-4" />
          </button>
          </TipBelow>
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
          data-id="cli-content-history-host"
          className="absolute inset-0 min-h-0"
          style={{ display: cliContentTab === 'history' ? 'block' : 'none' }}
        >
          {cliContentOpen && cliContentTab === 'history' && (
            <CurrentHistoryView
              paneId={activeCliPaneId}
              open
              inspectorVersion={chatWsInspectorVersion}
              fullWidth
              leftAlignQuestions
              agentType={String(
                paneDetails[activeCliPaneId]?.agent_type
                || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === activeCliPaneId)?.agent_type
                || ''
              )}
            />
          )}
        </div>
        <div
          data-id="cli-content-files-host"
          className="absolute inset-0"
          style={cliContentTab === 'files'
            ? { position: 'relative', width: '100%', height: '100%' }
            : { position: 'absolute', width: '100%', height: '100%', visibility: 'hidden', pointerEvents: 'none', zIndex: -1 }
          }
        >
          {seenCliTabs.has('files') && (
            <Suspense fallback={null}>
              <FilesView
                agentId={nativeFilesAgentId}
                workspaceFolder={nativeFilesWorkspace}
                pageClientId={pageClientId}
                className="h-full w-full"
              />
            </Suspense>
          )}
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
          data-id="cli-content-audit-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'audit' ? 'block' : 'none' }}
        >
          {cliContentOpen && cliContentTab === 'audit' && (
            <div className="h-full overflow-auto p-3"><PolicyTab /></div>
          )}
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
          {/* Mount ONLY while active (#5): AgentInspector's memory tab carries
              MemoryView + AgentDocRoleEditor (2 CodeMirror instances). Keeping it
              mounted-but-hidden left those editors resident forever, stacking
              .cm-editor across every tab ever opened. Unmount on switch-away —
              the editors autosave + flush before unmount, so this is safe. */}
          {cliContentOpen && cliContentTab === 'memory' && (
            <AgentInspector
              paneId={activeCliPaneId}
              paneTitle={
                paneDetails[activeCliPaneId]?.title
                || agents.find((item: any) => (item.pane_id || item.id || '').replace(/:.*$/, '') === activeCliPaneId)?.title
                || activeCliPaneId
              }
              open
              embedded
              requestedTab={'memory'}
              liveStatus={chatWsLiveStatus}
              inspectorVersion={chatWsInspectorVersion}
              onPanePatch={applyPanePatch}
              onClose={() => {}}
            />
          )}
        </div>
        <div
          data-id="cli-content-github-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'github' ? 'block' : 'none' }}
        >
          <AccountMatrixPanel
            active={cliContentOpen && cliContentTab === 'github'}
            paneId={activeCliPaneId}
            openConfigRequest={pendingProfileConfig}
            onSendToAgent={sendBrowserToAgent}
            onOpenInEditor={openCodeFile}
          />
        </div>
        <div
          data-id="cli-content-settings-host"
          className="absolute inset-0"
          style={{ display: cliContentTab === 'settings' ? 'block' : 'none' }}
        >
          {/* Mount ONLY while active (#5): same as memory — the settings editors
              were previously always mounted AND always open (open hardcoded true),
              so they stayed resident in the background across every pane/tab. */}
          {cliContentOpen && cliContentTab === 'settings' && (
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
          )}
        </div>
      </div>
    </div>
  );
  const cliFixedContent = renderCliContentPanel();
  const stackHeaderControls = useCallback((targetPaneId: string, showModelPicker = true) => targetPaneId === activeCliPaneId ? (
    <>
      {showModelPicker ? <ModelPicker
        paneId={activeCliPaneId}
        agentDetail={paneDetails[activeCliPaneId.split(':')[0]] || (activeCliPaneId.split(':')[0] === paneId.split(':')[0] ? agentDetail : null)}
        onUpdated={(patch) => applyPanePatch(activeCliPaneId, patch)}
        onOpen={() => refreshPaneDetail(activeCliPaneId)}
      /> : null}
      {/* Model picker sits at the LEFT of the bottom bar (grouped with the
          non-cicy attach button just before it); the spacer pushes the remaining
          controls to the right. */}
      <div data-id="stack-controls-model-spacer" className="flex-1" />
      {fixedDomain && !globalVar?.helper_mode && (
        <>
          <button
            type="button"
            data-id="workspace-ports-toggle"
            onClick={() => {
              setPortsOpen((open) => {
                if (!open && isShellOpen(activeCliPaneId)) toggleShellOpen(activeCliPaneId);
                return !open;
              });
            }}
            aria-pressed={portsOpen}
            title={t('portsPanelToggle', { defaultValue: 'Ports 端口转发' })}
            className={`p-1 rounded border transition-colors cursor-pointer ${portsOpen ? 'text-blue-300 border-blue-400/50 bg-blue-400/10' : 'text-zinc-600 border-zinc-700/60 hover:text-zinc-300 hover:border-zinc-600'}`}
          >
            <Globe2 className="w-3.5 h-3.5" />
          </button>
          {portsOpen && <PortsPanel fixedDomain={fixedDomain} proxyAvailable={proxyAvailable} paneId={activeCliPaneId} onClose={() => setPortsOpen(false)} />}
        </>
      )}
      {!globalVar?.helper_mode && (
        <button
          type="button"
          data-id="workspace-shell-toggle"
          onClick={() => {
            if (!isShellOpen(activeCliPaneId)) setPortsOpen(false);
            toggleShellOpen(activeCliPaneId);
          }}
          aria-pressed={isShellOpen(activeCliPaneId)}
          title={t('shellPanelToggle', { defaultValue: 'Shell 终端' })}
          className={`p-1 rounded border transition-colors cursor-pointer ${isShellOpen(activeCliPaneId) ? 'text-emerald-400 border-emerald-400/50 bg-emerald-400/10' : 'text-zinc-600 border-zinc-700/60 hover:text-zinc-300 hover:border-zinc-600'}`}
        >
          <Terminal className="w-3.5 h-3.5" />
        </button>
      )}
      <SystemResourceMonitor paneId={paneId} />
      <NetworkSignal latency={netLatency} connected={chatWsConnected} clientId={chatWsClientId} onSendClientId={handleSendPageClientIdToAgent} />
      <GlobalProxyIndicator placement="up" onManageNodes={handleOpenProxyManager} />
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
  ) : null, [activeCliPaneId, paneId, paneDetails, agentDetail, applyPanePatch, refreshPaneDetail, netLatency, chatWsConnected, chatWsClientId, handleSendPageClientIdToAgent, handleOpenProxyManager, contextUsage, isShellOpen, toggleShellOpen, t, fixedDomain, proxyAvailable, portsOpen, globalVar?.helper_mode]);
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
  const projectAgents = useMemo<ProjectAgent[]>(() => agents.map((agent: any) => {
    const fullPaneId = String(agent?.pane_id || agent?.id || '');
    const shortId = fullPaneId.replace(/:.*$/, '');
    const detail = paneDetails[shortId] || paneDetails[fullPaneId] || {};
    // Match TeamPanel's reply-metrics lookup exactly: the full pane-id entry is
    // the authoritative poll_data payload; the short-id entry is compatibility
    // fallback and may contain status-only data without model/context/cost.
    const live = pollStatuses[fullPaneId] || pollStatuses[shortId] || {};
    const isApiOnly = detail?.capabilities?.supports_tmux === false;
    return {
      paneId: fullPaneId,
      title: String(agent?.title || detail?.title || shortId),
      agentType: String(agent?.agent_type || detail?.agent_type || ''),
      status: String(live?.status || agent?.status || 'idle'),
      defaultModel: String(agent?.default_model || detail?.default_model || ''),
      workspace: String(agent?.workspace || detail?.workspace || ''),
      machineLabel: String(agent?.machine_label || detail?.machine_label || ''),
      ttydSrc: token && !isApiOnly ? urls.ttydOpen(shortId, token, currentLang) : '',
      isApiOnly,
    };
  }).filter((agent) => agent.paneId), [agents, paneDetails, pollStatuses, token, currentLang]);
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
            onOpenPaneContent={openPaneContent}
            onActivePaneIdChange={handleStackActivePaneIdChange}
            onRenamePaneTitle={handleRenamePaneTitle}
            todoCount={todoCount}
            auditAlertCount={auditAlertCount}
          />
          {rosterOpen && (
            <Suspense fallback={null}>
              <TeamRosterPanel
                panes={agents}
                bindings={boundAgents}
                masterPaneId={paneId.split(':')[0]}
                onClose={() => setRosterOpen(false)}
                onRefresh={refreshPanes}
                onRenameTitle={handleRenamePaneTitle}
                ModelPicker={ModelPicker}
              />
            </Suspense>
          )}
        </div>
      </div>
    </div>
  );

  return (
    <SendingProvider>
    <div data-id="workspace-root" className="flex h-screen overflow-hidden bg-[#0A0A0A] text-zinc-400 relative">
      {/* Roster mode: mask the left region (activity bar + left panel) so it can't
          be clicked, and clicking it closes the roster. Width = activity bar (56px)
          + left panel (360px when open). The roster + right-panel drawer sit to the
          right of this and stay interactive. */}
      {/* Activity Bar */}
      <div data-id="activity-bar" ref={activityBarRef} className="w-14 border-r border-[var(--vsc-border)] flex flex-col items-center py-4 justify-between bg-[#0A0A0A] shrink-0 z-50">
        <div data-id="activity-bar-top" className="flex flex-col gap-4 w-full items-center">
          {/* Office entry retired (2026-06-05) and its components deleted
              (2026-06-11) — the dispatcher (PM) chat now lives directly in the
              team agent card (DispatcherChat). */}
          <SideBtn
            dataId="btn-projects"
            active={projectsOpen}
            icon={<FolderKanban className="w-5 h-5" />}
            title={t('projectsTitle')}
            onClick={() => {
              setPortsOpen(false);
              setProjectsOpen((open) => {
                const next = !open;
                if (next) {
                  setLeftPanelView(null);
                  setKnowledgeOpen(false);
                  setCliContentOpen(false);
                } else if (/^#\/project\//.test(window.location.hash)) {
                  window.location.hash = `#/agent/${encodeURIComponent(paneId.replace(/:.*$/, ''))}`;
                }
                return next;
              });
            }}
            disabled={!!globalVar?.helper_mode}
          />
          <SideBtn dataId="btn-team" active={leftActive === 'team'} icon={<Users className="w-5 h-5" />} title={t('sidebarTeam')} onClick={() => toggleLeft('team')} disabled={!!globalVar?.helper_mode} />
          {/* Helper-mode trial container hides Skills / Providers (gateway) /
              IM / Audit from the activity bar — the drawer should stay
              laser-focused on the install chat. See helperMode in cicy-code
              setup.go and /api/settings/global → helper_mode. */}
          {!globalVar?.helper_mode && (
            <>
              <SideBtn dataId="btn-skill" active={leftActive === 'skills'} icon={<Package className="w-5 h-5" />} title={t('sidebarSkills')} onClick={() => toggleLeft('skills')} badge={publicSkillUpdate} badgeTitle={t('skillUpdateAvailable', { defaultValue: '有技能可更新' })} />
              <SideBtn dataId="btn-agent-role-market" active={leftActive === 'customAgents'} icon={<Store className="w-5 h-5" />} title="角色市场" onClick={() => toggleLeft('customAgents')} />
              <SideBtn
                dataId="btn-knowledge"
                active={knowledgeOpen}
                icon={<BookOpen className="w-5 h-5" />}
                title={t('tabKnowledge', { defaultValue: '知识库' })}
                onClick={() => { setPortsOpen(false); setProjectsOpen(false); setKnowledgeOpen(true); }}
                badge={knowledgePendingCount > 0}
                badgeTitle={t('knowledgePendingBadge', { defaultValue: '{{count}} 条知识待审核', count: knowledgePendingCount })}
              />
              {/* Audit log/policy live as normal right-panel tabs (日志/策略), gated
                  by the audit master switch — no dedicated left-bar entry. */}
              {/* IM moved into the unified Settings modal (bottom-left gear). */}
            </>
          )}
        </div>
        <div data-id="activity-bar-bottom" className="flex w-full flex-col items-center gap-3">
          <button
            ref={membershipTriggerRef}
            type="button"
            data-id="activity-bar-membership-trigger"
            onClick={handleToggleMembershipMenu}
            className={cn('group relative flex h-10 w-10 items-center justify-center rounded-xl transition-all cursor-pointer', membershipTone(membershipCard.level), membershipMenuOpen ? 'text-zinc-100 shadow-[0_12px_30px_rgba(0,0,0,0.28)]' : 'text-zinc-400 hover:text-zinc-100')}
            title={membershipCard.userId}
          >
            <Settings className="h-4 w-4" />
            {(emailNeedsSetup || versionUpdate) && <span data-id="activity-bar-membership-trigger-badge" className="absolute right-1.5 top-1.5 h-2 w-2 rounded-full bg-red-500 ring-2 ring-[#0b0b0c]" title={emailNeedsSetup ? t('emailNeedsSetup', { defaultValue: '未配置令牌投递邮箱 / SMTP' }) : t('versionUpdateAvailable', { defaultValue: '有新版本可更新' })} />}
          </button>
        </div>
      </div>

      {/* Main */}
      <div data-id="main-area" className="flex-1 flex flex-col min-w-0">
        {/* Content */}
        <main data-id="content-area" className="flex-1 relative overflow-hidden">
          <div data-id="main-layout" className="flex h-full min-w-0">
            {projectsOpen ? (
              <>
              <ProjectsPanel
                agents={projectAgents}
                statuses={pollStatuses}
                topRightControls={!cliContentOpen ? (
                  <CardMoreMenu
                    paneId={activeCliPaneId}
                    items={[
                      ...(todoSkillInstalled ? [{ id: 'agent-stack-card-todo', label: t('tabTodo'), icon: <ListTodo className="h-4 w-4" />, onClick: (event) => { event.stopPropagation(); openPaneTodo(activeCliPaneId); }, badge: todoCount }] : []),
                      { id: 'agent-stack-card-files', label: t('tabFiles'), icon: <Folder className="h-4 w-4" />, onClick: (event) => { event.stopPropagation(); openPaneFiles(activeCliPaneId); } },
                      { id: 'agent-stack-card-session', label: t('tabSession'), icon: <LineChart className="h-4 w-4" />, onClick: (event) => { event.stopPropagation(); handleStackOpenSession(activeCliPaneId); } },
                      { id: 'agent-stack-card-memory', label: t('tabMemory'), icon: <Brain className="h-4 w-4" />, onClick: (event) => { event.stopPropagation(); openPaneMemory(activeCliPaneId); } },
                      { id: 'agent-stack-card-audit', label: t('tabAudit', { ns: 'audit', defaultValue: '审计' }), icon: <ShieldCheck className="h-4 w-4" />, onClick: (event) => { event.stopPropagation(); openPaneContent(activeCliPaneId, 'audit'); }, badge: auditAlertCount },
                      { id: 'agent-stack-card-account-matrix', label: t('accountMatrixTitle', { defaultValue: '账号矩阵' }), icon: <Grid3X3 className="h-4 w-4" />, onClick: (event) => { event.stopPropagation(); openPaneContent(activeCliPaneId, 'github'); } },
                      { id: 'agent-stack-card-settings', label: t('tabSettings'), icon: <Settings className="h-4 w-4" />, onClick: (event) => { event.stopPropagation(); openPaneSettings(activeCliPaneId); }, active: cliContentOpen && cliContentTab === 'settings' },
                    ]}
                  />
                ) : null}
                footerControls={stackHeaderControls(activeCliPaneId, false)}
                dockOpen={portsOpen || isShellOpen(activeCliPaneId)}
                shellPanel={(() => {
                  const item = stackItems.find((candidate) => candidate.paneId === activeCliPaneId);
                  return item && !item.isApiOnly && item.ttydSrc ? <ShellPanel agentId={item.paneId} ttydSrc={item.ttydSrc} active /> : null;
                })()}
                onOpenAgent={(targetPaneId) => {
                  setPortsOpen(false);
                  setProjectsOpen(false);
                  onSelectAgent(targetPaneId.replace(/:.*$/, ''));
                }}
                onCreateAgent={() => {
                  setCreateAgentInitialValues(undefined);
                  setCreateAgentOpen(true);
                }}
                onOpenGuidance={openAgentGuidanceDetail}
                onOpenHistory={(targetPaneId) => openPaneContent(targetPaneId, 'history')}
              />
              {cliFixedContent}
              </>
            ) : (
            <>
            {leftActive && !globalVar?.helper_mode ? (
              <div
                data-testid="left-panel"
                data-id="left-panel"
                className="h-full w-[360px] min-w-[360px] max-w-[360px] shrink-0"
              >
                <div data-id="left-panel-wrap" className="h-full flex flex-col bg-[#0A0A0A] border-r border-[var(--vsc-border)] relative z-[130]">
                  <div data-id="left-panel-header" className="h-12 border-b border-[var(--vsc-border)] flex items-center px-2 bg-[#0e0e0e] shrink-0 gap-1">
                    {leftActive === 'agents' ? <>
                      <LayoutList className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-agents" className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelAgents')}</span>
                    </> : leftActive === 'skills' ? <>
                      <Brain className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-skills" className="text-xs font-medium text-zinc-500 flex-1 ml-1">{t('leftPanelSkills')}</span>
                    </> : leftActive === 'customAgents' ? <>
                      <Store className="w-3.5 h-3.5 text-zinc-600" />
                      <span data-id="left-panel-title-custom-agents" className="text-xs font-medium text-zinc-500 flex-1 ml-1">角色市场</span>
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
                          statuses={pollStatuses}
                          onSelectAgent={onSelectAgent}
                          onAgentsChange={setAgents}
                          onCreateAgent={() => { setCreateAgentInitialValues(undefined); setCreateAgentOpen(true); }}
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
                          onOpenAgentFile={openAgentFile}
                          onOpenRoster={() => setRosterOpen(true)}
                        />
                      </div>
                    ) : leftActive === 'skills' ? (
                      <div data-id="left-panel-skills-view" className="absolute inset-0">
                        <SkillMarketplacePanel
                          paneId={activeCliPaneId || paneId}
                          onOpenDetail={handleSkillDetailOpen}
                          onCloseDetail={handleSkillDetailClose}
                        />
                      </div>
                    ) : leftActive === 'customAgents' ? (
                      <div data-id="left-panel-custom-agents-view" className="absolute inset-0">
                        <CustomAgentsPanel paneId={activeCliPaneId || paneId} onCreated={refreshPanes} onSelectAgent={onSelectAgent} marketOnly />
                      </div>
                    ) : null}
                  </div>
                </div>
              </div>
            ) : null}
            <div data-testid="mid-panel" data-id="mid-panel" className="min-w-0 flex-1 relative">
              {rightContent}
            </div>
            {cliFixedContent}
            </>
            )}
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
          {/* 升级按钮已移除（产品决定） */}
          {false && !globalVar?.helper_mode && (
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
          <button
            type="button"
            data-id="top-bar-docs"
            onClick={() => {
              setMembershipMenuOpen(false);
              window.open(DOCS_URL, '_blank', 'noopener,noreferrer');
            }}
            className="mt-1 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
          >
            <span data-id="top-bar-docs-label">{t('docsLink', { defaultValue: '文档' })}</span>
            <BookOpen className="h-3.5 w-3.5" />
          </button>
          {false && isDev ? (
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
                <span data-id="workspace-language-current-flag" aria-hidden>{flagEmoji(currentLang)}</span>
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
                        <span data-id="workspace-language-option-flag" aria-hidden className="text-[12px] leading-none">{flagEmoji(code)}</span>
                        <span data-id={`membership-language-${code}-name`} className="truncate">{languageDisplayName(code)}</span>
                      </span>
                      {active ? <Check className="h-3 w-3 shrink-0 text-emerald-400" /> : null}
                    </button>
                  );
                })}
              </div>
            ) : null}
          </div>
          {/* Settings entries → open the unified fullscreen Settings modal at the
              matching section. Language stays as its own submenu above. */}
          <div data-id="membership-settings-group" className="mt-1 border-t border-white/[0.06] pt-1">
            <button
              type="button"
              data-id="membership-timer"
              onClick={() => openSettings('timer')}
              className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
            >
              <span data-id="membership-timer-label">{t('timer', { ns: 'common', defaultValue: '定时器' })}</span>
              <Timer className="h-3.5 w-3.5" />
            </button>
            {String(globalVar?.public_url || '').trim() ? (
              <button
                type="button"
                data-id="membership-mobile-qr"
                onClick={() => {
                  setMembershipMenuOpen(false);
                  setLangMenuOpen(false);
                  setMobileQROpen(true);
                }}
                className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
              >
                <span data-id="membership-mobile-qr-label">{t('mobileQrTitle')}</span>
                <Smartphone className="h-3.5 w-3.5" />
              </button>
            ) : null}
            <button
              type="button"
              data-id="membership-settings-general"
              onClick={() => openSettings('general')}
              className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
            >
              <span data-id="membership-settings-general-label" className="inline-flex items-center gap-1.5">
                {t('settingsNavGeneral', { defaultValue: '通用' })}
                {emailNeedsSetup && <span data-id="membership-settings-general-badge" className="h-1.5 w-1.5 shrink-0 rounded-full bg-red-500" title={t('emailNeedsSetup', { defaultValue: '未配置令牌投递邮箱 / SMTP' })} />}
              </span>
              <SlidersHorizontal className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              data-id="membership-settings-im"
              onClick={() => openSettings('im')}
              className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
            >
              <span data-id="membership-settings-im-label">{t('settingsNavIM', { defaultValue: 'IM 通知' })}</span>
              <MessageCircle className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              data-id="membership-settings-routing"
              onClick={() => openSettings('routing')}
              className="mt-0.5 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
            >
              <span data-id="membership-settings-routing-label">{t('settingsNavRouting', { defaultValue: 'Agent 路由' })}</span>
              <Route className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              data-id="membership-settings-providers"
              onClick={() => openSettings('providers')}
              className="mt-0.5 flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-[11px] font-semibold text-zinc-200 transition-colors hover:bg-white/5"
            >
              <span data-id="membership-settings-providers-label" className="inline-flex items-center gap-1.5">
                {t('settingsNavProviders', { defaultValue: 'LLM 供应商' })}
                {providersNeedKey && <span data-id="membership-settings-providers-badge" className="h-1.5 w-1.5 shrink-0 rounded-full bg-red-500" title={t('providerMissingApiKey', { defaultValue: '缺少 API key' })} />}
              </span>
              <Boxes className="h-3.5 w-3.5" />
            </button>
          </div>
          <div data-id="membership-version" className="mt-1 flex items-center justify-between rounded-lg px-3 py-2 text-[11px] text-zinc-500">
            <span data-id="membership-version-label">Version</span>
            <span className="flex items-center gap-2">
              {versionUpdate && (
                <button
                  type="button"
                  data-id="membership-version-update"
                  onClick={applyUpdate}
                  disabled={updating}
                  className="rounded-md bg-red-500/15 px-2 py-0.5 text-[10px] font-medium text-red-400 transition-colors hover:bg-red-500/25 disabled:opacity-60"
                  title={t('versionUpdateAvailable', { defaultValue: '有新版本可更新' })}
                >
                  {updating ? t('updating', { defaultValue: '更新中…' }) : t('updateNow', { defaultValue: '更新' })}
                </button>
              )}
              {versionUpdate && !updating && <span data-id="membership-version-badge" className="inline-block h-1.5 w-1.5 rounded-full bg-red-500" title={t('versionUpdateAvailable', { defaultValue: '有新版本可更新' })} />}
              <span data-id="membership-version-value" id="version" className="font-mono text-zinc-300">{config.version}</span>
            </span>
          </div>
        </div>,
        document.body
      ) : null}
      {tokenOpen && <TokenDialog onClose={() => setTokenOpen(false)} />}
      {apiOpen && <ApiSwitchDialog onClose={() => setApiOpen(false)} />}
      <ProxyManagerDialog open={proxyManagerOpen} onClose={() => setProxyManagerOpen(false)} paneId={activeCliPaneId || paneId} />
      <MobileQRPopover workspaceTitle={topBarTitle} open={mobileQROpen} onClose={() => setMobileQROpen(false)} />
      {toast && (
        <div data-id="workspace-toast" role="status" className={`fixed left-1/2 top-4 z-[9999] flex max-w-[min(560px,calc(100vw-32px))] -translate-x-1/2 items-start gap-3 rounded-xl border px-4 py-3 pr-11 text-sm shadow-xl backdrop-blur ${toast.variant === 'success' ? 'border-emerald-500/25 bg-emerald-950/95 text-emerald-50' : 'border-white/10 bg-zinc-800/95 text-zinc-100'}`}>
          <span data-id="workspace-toast-message" className="min-w-0 break-words leading-5">{toast.message}</span>
          <button data-id="workspace-toast-close" type="button" aria-label="Close" onClick={() => { window.clearTimeout(toastTimerRef.current); setToast(null); }} className="absolute right-2 top-2 grid h-7 w-7 place-items-center rounded-lg text-current opacity-60 transition hover:bg-white/10 hover:opacity-100"><X className="h-4 w-4" /></button>
        </div>
      )}
      {dialogsNode}
      <CreateAgentDialog
        open={createAgentOpen}
        submitting={createAgentSubmitting}
        onClose={() => { if (!createAgentSubmitting) { setCreateAgentOpen(false); setCreateAgentInitialValues(undefined); } }}
        onSubmit={submitCreateAgent}
        title={t('drawerCreateTitle')}
        submitLabel={t('drawerCreateSubmit')}
        initialValues={createAgentInitialValues}
      />
      <WeChatBindModal />
      <SettingsModal
        open={settingsOpen}
        section={settingsSection}
        onSection={setSettingsSection}
        onClose={() => setSettingsOpen(false)}
        currentLang={currentLang}
        langs={TRANSLATED_LNGS}
        onChangeLang={(code) => { void i18nLive.changeLanguage(code); }}
        flagEmoji={flagEmoji}
        langName={languageDisplayName}
        version={config.version}
        providersNeedKey={providersNeedKey}
        publicUrl={String(globalVar?.public_url || '')}
        onSavePublicUrl={async (url) => { await updateGlobalVar({ public_url: url }); }}
      />
      <Suspense fallback={null}>
        <KnowledgePanel
          open={knowledgeOpen}
          onClose={() => setKnowledgeOpen(false)}
          agentId={nativeFilesAgentId}
          workspaceFolder={nativeFilesWorkspace}
          pageClientId={pageClientId}
        />
      </Suspense>
    </div>
    </SendingProvider>
  );
}

function SideBtn({ dataId, active, icon, title, onClick, disabled = false, badge = false, badgeTitle = '缺少 API key' }: { dataId: string; active: boolean; icon: React.ReactNode; title: string; onClick: () => void; disabled?: boolean; badge?: boolean; badgeTitle?: string }) {
  return (
    <button data-id={dataId} onClick={disabled ? undefined : onClick} disabled={disabled} aria-disabled={disabled} className={cn("p-2.5 rounded-xl transition-all relative", disabled ? "text-zinc-700 opacity-50 cursor-not-allowed" : cn("cursor-pointer", active ? "text-zinc-300 bg-white/[0.06]" : "text-zinc-600 hover:text-zinc-400 hover:bg-white/[0.03]"))} title={title}>
      {icon}
      {active && !disabled && <div data-id={`${dataId}-active-indicator`} className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-5 bg-blue-500/60 rounded-r" />}
      {badge && <span data-id={`${dataId}-badge`} className="absolute right-1 top-1 h-2 w-2 rounded-full bg-red-500 ring-2 ring-[#0b0b0c]" title={badgeTitle} />}
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
    roleTemplate: detail?.role_template || agent?.role_template || '',
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
      roleTemplate: meta.roleTemplate,
      isPrimary: targetPaneId === paneId,
      isApiOnly: meta.isApiOnly,
    };
  });
}

function AgentDrawer({ agents, paneId, statuses = {}, onSelectAgent, onAgentsChange, onOpenSettings, onCreateAgent }: {
  agents: any[]; paneId: string;
  statuses?: Record<string, any>;
  onSelectAgent: (id: string) => void; onAgentsChange: (a: any[]) => void;
  onOpenSettings: (id: string) => void;
  onCreateAgent: () => void;
}) {
  const { t } = useTranslation('workspace');
  const [search, setSearch] = useState('');
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
    openMenuId,
  });

  const handleDelete = async (id: string) => {
    const sid = id.split(':')[0];
    if (sid === 'w-1001') return;
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
        onSelectAgent(next ? (next.pane_id || next.id).split(':')[0] : 'w-1001');
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
            <button data-id="agent-drawer-add" onClick={onCreateAgent}
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
              // reply.json turn status: completed → green, failed → red,
              // anything else (a turn in flight) → pulsing yellow.
              const replyStatus = getPaneStatus(statuses, shortId)?.status;
              const statusDotCls = !replyStatus ? 'bg-zinc-700'
                : replyStatus === 'completed' ? 'bg-emerald-700'
                : replyStatus === 'failed' ? 'bg-red-700'
                : 'bg-yellow-600 animate-pulse';
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
	                        <span data-id={`agent-row-status-dot-${shortId}`} title={replyStatus || ''} className={cn("h-2 w-2 rounded-full shrink-0", statusDotCls)} />
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

// modelFamily groups a model id under a stable vendor family so the picker can
// section a long, growing model list (DeepSeek / Claude / OpenAI / Gemini …)
// instead of one flat scroll. Falls back to the capitalized leading token.
function modelFamily(model: string): string {
  const m = model.toLowerCase();
  if (m.startsWith('deepseek')) return 'DeepSeek';
  if (m.startsWith('claude')) return 'Claude';
  if (m.startsWith('gpt') || /^o[1-9]/.test(m)) return 'OpenAI';
  if (m.startsWith('gemini')) return 'Gemini';
  if (m.startsWith('qwen')) return 'Qwen';
  if (m.startsWith('llama')) return 'Llama';
  const head = (model.split(/[-_/]/)[0] || model).trim();
  return head ? head.charAt(0).toUpperCase() + head.slice(1) : 'Other';
}

function ModelPicker({ paneId, agentDetail, onUpdated, onOpen }: { paneId: string; agentDetail: any; onUpdated: (patch: any) => void; onOpen?: () => void }) {
  const { runPaneSaveSerially } = useApp();
  const { t } = useTranslation('workspace');
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  // Redesigned picker state: top-level "style" tabs (Claude- vs OpenAI-style,
  // i.e. provider protocol), a search box, and a per-model expanded route list.
  const [query, setQuery] = useState('');
  const [activeStyle, setActiveStyle] = useState('');
  const [expandedModel, setExpandedModel] = useState<string | null>(null);
  // Per-provider balance / availability, keyed by provider key. Real balance for
  // providers that expose one (DeepSeek), per-model availability badges for those
  // that don't (Gemini). Fetched on open; cached server-side.
  const [balances, setBalances] = useState<Record<string, any>>({});
  const rootRef = useRef<HTMLDivElement>(null);
  // The popover is portaled to <body> (so an overflow/clip container — like the
  // roster's scrolling list — never crops it) and flips below/above the trigger
  // depending on available space. pos holds its fixed-position coords.
  const popRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top?: number; bottom?: number; maxHeight: number } | null>(null);
  const computePos = useCallback(() => {
    const r = rootRef.current?.getBoundingClientRect();
    if (!r) return;
    const POP_W = 340, GAP = 8, M = 8;
    const left = Math.min(Math.max(r.left, M), window.innerWidth - POP_W - M);
    const spaceBelow = window.innerHeight - r.bottom - GAP - M;
    const spaceAbove = r.top - GAP - M;
    // Anchor the popover's edge to the trigger (top-to-bottom below, or bottom-to-
    // top above) so it never floats away with a gap, and cap its height to the
    // available space (it scrolls internally) so it never clips off-screen. Open
    // below unless there's clearly more room above.
    if (spaceBelow >= 240 || spaceBelow >= spaceAbove) {
      setPos({ left, top: r.bottom + GAP, maxHeight: Math.max(180, Math.min(440, spaceBelow)) });
    } else {
      setPos({ left, bottom: window.innerHeight - r.top + GAP, maxHeight: Math.max(180, Math.min(440, spaceAbove)) });
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    const handleOutside = (event: Event) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (rootRef.current && rootRef.current.contains(target)) return;
      if (popRef.current && popRef.current.contains(target)) return; // clicks inside the portaled popover
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

  // On open, default the active style-tab to the current provider's protocol and
  // reset the transient search/expand state. Derived from the prop directly so it
  // stays above the early-return (rules of hooks).
  useEffect(() => {
    if (!open) return;
    const opts: any[] = agentDetail?.runtime_ai_provider_options || [];
    const defKey = String(agentDetail?.runtime_ai_default?.provider_name || '').trim();
    const ovKey = String(agentDetail?.runtime_ai?.provider_name || '').trim();
    const act = opts.find((p) => p?.key === (ovKey || defKey));
    const protos: string[] = [];
    for (const p of opts) {
      const pr = String(p?.protocol || '').trim() || 'other';
      if (!protos.includes(pr)) protos.push(pr);
    }
    const proto = String(act?.protocol || '').trim();
    setActiveStyle(proto && protos.includes(proto) ? proto : (protos[0] || ''));
    setQuery('');
    setExpandedModel(null);
  }, [open]);

  // Fetch each provider's balance/availability on open (server-side cached, so a
  // re-open is cheap and won't re-burn Gemini's free quota).
  useEffect(() => {
    if (!open) return;
    const opts: any[] = agentDetail?.runtime_ai_provider_options || [];
    const keys = Array.from(new Set(opts.map((p) => String(p?.key || '')).filter(Boolean)));
    keys.forEach((k) => {
      apiService.getProviderBalance(k)
        .then((res: any) => setBalances((prev) => ({ ...prev, [k]: res?.data ?? res })))
        .catch(() => {});
    });
  }, [open]);

  const useCustomGateway = !!agentDetail?.use_custom_gateway;
  const agentType = String(agentDetail?.agent_type || '');
  // The dispatcher always talks through the local gateway by construction, so
  // it is eligible regardless of the use_custom_gateway flag.
  const eligible = isCicyLiteAgent(agentType)
    || (useCustomGateway && ['claude', 'codex', 'opencode', 'gemini'].includes(agentType));
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

  // ── Derive the picker model: style tabs (by provider protocol), then within the
  // active style dedupe models across that style's providers and group by family.
  const stylesPresent: string[] = (() => {
    const seen: string[] = [];
    for (const p of providerOptions) {
      const pr = String(p?.protocol || '').trim() || 'other';
      if (!seen.includes(pr)) seen.push(pr);
    }
    const order = ['anthropic', 'openai'];
    seen.sort((a, b) => {
      const ia = order.indexOf(a); const ib = order.indexOf(b);
      return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
    });
    return seen;
  })();
  const styleOf = (activeStyle && stylesPresent.includes(activeStyle)) ? activeStyle : (stylesPresent[0] || '');
  const styleLabelOf = (s: string) =>
    s === 'anthropic' ? t('modelPicker.styleClaude')
      : s === 'openai' ? t('modelPicker.styleOpenai')
        : s === 'gemini' ? 'Gemini'
          : s === 'other' ? t('modelPicker.styleOther')
            : s;
  const q = query.trim().toLowerCase();
  const styleProviders = providerOptions.filter((p: any) => (String(p?.protocol || '').trim() || 'other') === styleOf);
  const modelOrder: string[] = [];
  const modelToProviders = new Map<string, any[]>();
  for (const p of styleProviders) {
    const models: string[] = Array.isArray(p?.models) ? p.models.map((m: any) => String(m)).filter(isChatModel) : [];
    for (const m of models) {
      if (!modelToProviders.has(m)) { modelToProviders.set(m, []); modelOrder.push(m); }
      modelToProviders.get(m)!.push(p);
    }
  }
  const matchedModels = modelOrder.filter((m) => {
    if (!q) return true;
    if (m.toLowerCase().includes(q)) return true;
    return (modelToProviders.get(m) || []).some((p: any) => String(p?.label || p?.key || '').toLowerCase().includes(q));
  });
  const familyOrder: string[] = [];
  const familyToModels = new Map<string, string[]>();
  for (const m of matchedModels) {
    const fam = modelFamily(m);
    if (!familyToModels.has(fam)) { familyToModels.set(fam, []); familyOrder.push(fam); }
    familyToModels.get(fam)!.push(m);
  }
  const providerLabelOf = (p: any) => String(p?.label || p?.key || '').trim();

  // Balance number for providers that expose one (e.g. DeepSeek); '' otherwise.
  const balanceLabelOf = (p: any) => {
    const b = balances[String(p?.key || '')];
    if (b?.kind === 'balance' && b?.ok && b?.total != null) {
      const cur = b.currency === 'CNY' ? '¥' : b.currency === 'USD' ? '$' : '';
      return `${cur}${b.total}${cur ? '' : (b.currency ? ' ' + b.currency : '')}`;
    }
    return '';
  };
  // Per-model availability across the model's providers (prefers an "ok").
  const modelAvailOf = (m: string, providers: any[]): { status: string; retryAfter?: string } | null => {
    let fallback: any = null;
    for (const p of providers) {
      const b = balances[String(p?.key || '')];
      const hit = Array.isArray(b?.models) ? b.models.find((x: any) => x.model === m) : null;
      if (hit) {
        if (hit.status === 'ok') return hit;
        fallback = fallback || hit;
      }
    }
    return fallback;
  };
  const availBadge = (m: string, providers: any[]) => {
    const a = modelAvailOf(m, providers);
    if (!a) return null;
    if (a.status === 'ok') return <span data-id={`model-picker-avail-${m}`} className="shrink-0 rounded bg-emerald-500/15 px-1 py-px text-[9px] font-medium text-emerald-300/90">{t('modelPicker.availOk', { defaultValue: '可用' })}</span>;
    if (a.status === 'paid') return <span data-id={`model-picker-avail-${m}`} className="shrink-0 rounded bg-amber-500/15 px-1 py-px text-[9px] font-medium text-amber-300/90">{t('modelPicker.availPaid', { defaultValue: '需付费' })}</span>;
    if (a.status === 'quota') return <span data-id={`model-picker-avail-${m}`} className="shrink-0 rounded bg-amber-500/15 px-1 py-px text-[9px] font-medium text-amber-300/90">{t('modelPicker.availQuota', { defaultValue: '额度耗尽' })}{a.retryAfter ? `·${a.retryAfter}` : ''}</span>;
    return null;
  };

  return (
    <div data-id="model-picker-root" ref={rootRef} className="relative mr-auto">
      <button
        type="button"
        data-id="model-picker-trigger"
        onClick={() => setOpen(prev => { const next = !prev; if (next) { computePos(); onOpen?.(); } return next; })}
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
          {currentModel ? (
            <ModelTag model={currentModel} className="shrink-0" />
          ) : (
            <span data-id="model-picker-model" className="truncate text-zinc-300">{displayModel}</span>
          )}
        </span>
        <ChevronDown
          data-id="model-picker-chevron"
          className={`h-3 w-3 text-zinc-600 transition-all duration-200 ${open ? 'rotate-180 text-zinc-300' : ''}`}
        />
      </button>
      {open ? createPortal(
        <>
        {/* No full-screen backdrop: it would sit above the roster rows and swallow
            the click meant to switch agents (open picker → click a row → the click
            hits the backdrop, closing the picker but never reaching the row → "can't
            switch"). Closing on outside-click is handled by the document pointerdown
            listener above (fires for normal DOM) + the window-blur listener (covers
            iframe focus), so the backdrop is unnecessary here. */}
        <div
          ref={popRef}
          data-id="model-picker-popover"
          role="dialog"
          className="animate-select-in fixed z-[300] flex w-[340px] flex-col overflow-hidden rounded-xl border border-white/[0.06] bg-[#141416]/[0.98] shadow-[0_20px_50px_-12px_rgba(0,0,0,0.7),inset_0_1px_0_0_rgba(255,255,255,0.04)] backdrop-blur-md"
          style={{ left: pos?.left, top: pos?.top, bottom: pos?.bottom, maxHeight: pos?.maxHeight }}
        >
          {/* Style tabs — Claude-style vs OpenAI-style (provider protocol). */}
          {stylesPresent.length > 1 ? (
            <div data-id="model-picker-tabs" className="flex shrink-0 gap-1 border-b border-white/[0.06] p-1.5">
              {stylesPresent.map((s) => {
                const isActive = s === styleOf;
                return (
                  <button
                    key={s}
                    type="button"
                    data-id={`model-picker-tab-${s}`}
                    onClick={() => { setActiveStyle(s); setExpandedModel(null); }}
                    className={`flex-1 rounded-md px-2 py-1 text-[11px] font-medium transition-colors ${isActive ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'}`}
                  >
                    {styleLabelOf(s)}
                  </button>
                );
              })}
            </div>
          ) : null}
          {/* Search */}
          <div data-id="model-picker-search" className="flex shrink-0 items-center gap-2 border-b border-white/[0.06] px-3 py-2">
            <Search className="h-3.5 w-3.5 shrink-0 text-zinc-600" />
            <input
              data-id="model-picker-search-input"
              type="text"
              value={query}
              onChange={(e) => { setQuery(e.target.value); setExpandedModel(null); }}
              placeholder={t('modelPicker.searchPlaceholder')}
              className="w-full bg-transparent text-[12px] text-zinc-200 placeholder:text-zinc-600 focus:outline-none"
            />
          </div>
          {/* Current selection summary */}
          {currentModel ? (
            <div data-id="model-picker-current-summary" className="flex shrink-0 items-center gap-2 border-b border-white/[0.06] px-3 py-2">
              <span className="text-[10px] uppercase tracking-wider text-zinc-600">{t('modelPicker.current')}</span>
              <ModelTag model={currentModel} className="shrink-0" />
              {displayProvider ? <span className="truncate text-[10px] text-zinc-500">· {displayProvider}</span> : null}
            </div>
          ) : null}
          {/* Model list */}
          <div data-id="model-picker-list" className="min-h-0 flex-1 overflow-y-auto py-1">
            {familyOrder.length === 0 ? (
              <div data-id="model-picker-empty" className="px-3 py-4 text-center text-[12px] text-zinc-600">
                {providerOptions.length === 0 ? t('modelPicker.noProviders') : t('modelPicker.noMatches')}
              </div>
            ) : familyOrder.map((fam) => (
              <div key={fam} data-id={`model-picker-family-${fam}`} className="mb-0.5">
                <div data-id={`model-picker-family-header-${fam}`} className="px-3 pt-1.5 pb-1 text-[10px] font-medium uppercase tracking-wider text-zinc-500">{fam}</div>
                {(familyToModels.get(fam) || []).map((m) => {
                  const providers = modelToProviders.get(m) || [];
                  const multi = providers.length > 1;
                  const isExpanded = expandedModel === m;
                  const isFree = m.toLowerCase().includes('free');
                  const isCurrent = m === currentModel && providers.some((p: any) => String(p?.key || '') === activeProviderKey);
                  const soleProvider = providers[0];
                  return (
                    <div key={m} data-id={`model-picker-model-${m}`}>
                      <button
                        type="button"
                        data-id={`model-picker-model-row-${m}`}
                        onClick={() => { if (multi) { setExpandedModel(isExpanded ? null : m); } else if (soleProvider) { handleSelect(String(soleProvider.key || ''), m); } }}
                        disabled={saving}
                        className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12px] transition-colors hover:bg-white/[0.04] disabled:cursor-not-allowed disabled:opacity-50 ${isCurrent ? 'bg-blue-500/10 text-blue-200' : 'text-zinc-300'}`}
                      >
                        <span className="min-w-0 flex-1 truncate font-mono">{m}</span>
                        {isFree ? <span data-id={`model-picker-free-${m}`} className="shrink-0 rounded bg-emerald-500/15 px-1 py-px text-[9px] font-medium text-emerald-300/90">free</span> : null}
                        {availBadge(m, providers)}
                        {multi ? (
                          <span data-id={`model-picker-routes-${m}`} className="flex shrink-0 items-center gap-0.5 text-[10px] text-zinc-500">
                            {t('modelPicker.routes', { n: providers.length })}
                            <ChevronDown className={`h-3 w-3 transition-transform ${isExpanded ? 'rotate-180' : ''}`} />
                          </span>
                        ) : (
                          <span data-id={`model-picker-route-${m}`} className="flex shrink-0 items-center gap-1 truncate text-[10px] text-zinc-500">
                            {balanceLabelOf(soleProvider) ? <span data-id={`model-picker-balance-${m}`} className="rounded bg-sky-500/15 px-1 py-px font-medium text-sky-300/90">{balanceLabelOf(soleProvider)}</span> : null}
                            <span className="truncate">{providerLabelOf(soleProvider)}</span>
                          </span>
                        )}
                        {isCurrent && !multi ? <Check className="h-3 w-3 shrink-0" /> : null}
                      </button>
                      {multi && isExpanded ? (
                        <div data-id={`model-picker-routes-list-${m}`} className="bg-black/20">
                          {providers.map((p: any) => {
                            const pKey = String(p?.key || '');
                            const isCurrentRoute = m === currentModel && pKey === activeProviderKey;
                            const isDefaultProvider = pKey === defaultProviderKey;
                            return (
                              <button
                                key={pKey}
                                type="button"
                                data-id={`model-picker-route-item-${pKey}-${m}`}
                                onClick={() => handleSelect(pKey, m)}
                                disabled={saving}
                                className={`flex w-full items-center gap-2 py-1.5 pl-7 pr-3 text-left text-[11px] transition-colors hover:bg-white/[0.04] disabled:cursor-not-allowed disabled:opacity-50 ${isCurrentRoute ? 'text-blue-200' : 'text-zinc-400'}`}
                              >
                                <Route className="h-3 w-3 shrink-0 text-zinc-600" />
                                <span className="min-w-0 flex-1 truncate">{providerLabelOf(p)}</span>
                                {balanceLabelOf(p) ? <span data-id={`model-picker-route-balance-${pKey}-${m}`} className="shrink-0 rounded bg-sky-500/15 px-1 py-px text-[9px] font-medium text-sky-300/90">{balanceLabelOf(p)}</span> : null}
                                {isDefaultProvider ? <span className="shrink-0 rounded bg-zinc-700/40 px-1 py-px text-[9px] text-zinc-400">{t('modelPicker.default')}</span> : null}
                                {isCurrentRoute ? <Check className="h-3 w-3 shrink-0" /> : null}
                              </button>
                            );
                          })}
                        </div>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            ))}
          </div>
        </div>
        </>, document.body) : null}
    </div>
  );
}

function SystemResourceMonitor({ paneId }: { paneId: string }) {
  const { t } = useTranslation('workspace');
  const { activeChatPaneId, sendChatWsMessage, systemResources, globalVar } = useApp();
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
        {!globalVar?.helper_mode && (
          <>
            <ResourceChip label="C" pct={cpuPct} dataId="system-resource-summary-cpu" />
            <span data-id="workspace-system-resource-summary-divider-cpu" className="h-3 w-px bg-white/[0.06]" aria-hidden />
          </>
        )}
        <ResourceChip label="M" pct={memPct} dataId="system-resource-summary-memory" />
        <span data-id="workspace-system-resource-summary-divider-memory" className="h-3 w-px bg-white/[0.06]" aria-hidden />
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
              <span data-id="workspace-system-resource-load-separator-1" className="text-zinc-700">·</span>
              <span data-id="system-resource-load-5" className="tabular-nums">{formatLoadValue(systemResources?.load_5)}</span>
              <span data-id="workspace-system-resource-load-separator-2" className="text-zinc-700">·</span>
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
