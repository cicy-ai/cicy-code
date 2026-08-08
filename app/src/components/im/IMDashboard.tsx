// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import {
  Plus, Save, Trash2, Zap, Eye, EyeOff, Check, X, ArrowLeft,
  Send, MessageCircle, QrCode, RefreshCw, Search, ExternalLink, ChevronDown, Loader2,
  Mail,
} from 'lucide-react';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';
import { Spinner } from '../ui/Spinner';
import AgentAvatar from '../AgentAvatar';

/* ───────────── types ───────────── */

type Platform = 'telegram' | 'wechat' | 'feishu' | 'cicy_cloud';

interface IMAccount {
  id: number;
  platform: Platform | string;
  name: string;
  has_secret: boolean;
  secret_tail: string;
  config: Record<string, any>;
  enabled: boolean;
  state: string;
  state_detail: string;
  bound_pane_id: string;
  bound_pane_title: string;
  inbound_to_agent: boolean;
}

interface IMPlatform {
  kind: string;
  label: string;
  needs_token: boolean;
  needs_qr: boolean;
  can_edit: boolean;
  help?: Record<string, string>;
}

interface PaneOpt { pane_id: string; title: string; agent_type: string; use_custom_gateway: boolean }

interface WxLoginState {
  sessionId: string;
  qrcodeUrl: string;
  state: string;
  detail?: string;
}

interface TestResult {
  ok?: boolean;
  success?: boolean;
  error?: string;
  detail?: string;
}

/* ───────────── helpers ───────────── */

function cn(...p: Array<string | false | null | undefined>) { return p.filter(Boolean).join(' '); }

// Numeric id of a pane (`w-104` → 104) for ascending sort; non-numeric → last.
function paneIdNum(id: string) { const m = String(id).match(/(\d+)/); return m ? parseInt(m[1], 10) : Number.MAX_SAFE_INTEGER; }

// BindPicker — the reusable "choose an agent to bind" modal. Used in THREE
// places (detail edit, add-WeChat, add-TG); always a full-screen overlay layered
// above whatever opened it (the add modals sit at z-10000, so pass a higher z).
// Lists EVERY agent (any type), `panes` arrives already id-ascending; searches
// id + title + type. `busyPanes` (bound by other same-platform accounts) are
// shown disabled. Escape / backdrop / the X close it.
function BindPicker({ panes, busyPanes, value, onPick, onClose, z = 10010 }: {
  panes: PaneOpt[];
  busyPanes?: Set<string>;
  value: string;
  onPick: (paneId: string) => void;
  onClose: () => void;
  z?: number;
}) {
  const { t } = useTranslation('im');
  const [q, setQ] = useState('');
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [onClose]);
  const list = useMemo(() => {
    const ql = q.trim().toLowerCase();
    let l = panes.filter((p) => !busyPanes?.has(p.pane_id) || p.pane_id === value);
    if (ql) l = l.filter((p) => `${p.pane_id} ${p.title} ${p.agent_type}`.toLowerCase().includes(ql));
    return l;
  }, [panes, busyPanes, q, value]);
  return createPortal((
    <div
      data-id="im-bind-picker"
      className="fixed inset-0 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      style={{ zIndex: z }}
      onClick={onClose}
    >
      <div
        className="flex h-[72vh] w-full max-w-md flex-col overflow-hidden rounded-2xl border border-white/10 bg-[#161618] shadow-2xl shadow-black/70"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-white/[0.06] px-4 py-3">
          <h3 className="text-[14px] font-semibold text-white">{t('bindModalTitle', '选择绑定 agent')}</h3>
          <button type="button" data-id="im-bind-picker-close" onClick={onClose} className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200">
            <X size={16} />
          </button>
        </div>
        <div className="border-b border-white/[0.06] p-2.5">
          <div className="flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.03] px-2.5">
            <Search size={14} className="shrink-0 text-zinc-500" />
            <input
              data-id="im-bind-picker-search"
              autoFocus
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t('bindSearchPlaceholder', '按 id 或 title 搜索…')}
              className="h-9 w-full bg-transparent text-[13px] text-zinc-100 placeholder:text-zinc-600 focus:outline-none"
            />
          </div>
        </div>
        <div data-id="im-bind-picker-list" className="flex-1 overflow-auto p-1.5">
          <button
            type="button"
            onClick={() => onPick('')}
            className={cn('flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-white/[0.06]', !value && 'bg-white/[0.07]')}
          >
            <span className="grid w-4 place-items-center">{!value && <Check size={13} className="text-blue-400" />}</span>
            <span className="text-zinc-500">{t('unboundOption', '不绑定')}</span>
          </button>
          {list.length > 0 && <div className="my-1 h-px bg-white/[0.06]" />}
          {list.map((p) => {
            const isSel = p.pane_id === value;
            return (
              <button
                key={p.pane_id}
                type="button"
                data-id={`im-bind-picker-option-${p.pane_id}`}
                onClick={() => onPick(p.pane_id)}
                className={cn('flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-white/[0.06]', isSel && 'bg-white/[0.07]')}
              >
                <span className="grid w-4 shrink-0 place-items-center">{isSel && <Check size={13} className="text-blue-400" />}</span>
                <AgentAvatar agentType={p.agent_type} title={p.title || p.pane_id} variant="option" />
                <span className="min-w-0 flex-1">
                  {/* Title on top (what you recognize), id + type below. */}
                  <span className="flex items-center gap-1.5">
                    <span className="truncate text-zinc-100">{p.title || p.pane_id}</span>
                    {p.agent_type && <span className="shrink-0 rounded bg-white/[0.06] px-1 py-px text-[10px] font-medium text-zinc-400">{p.agent_type}</span>}
                  </span>
                  <span className="block truncate font-mono text-[11px] text-zinc-500">{p.pane_id}</span>
                </span>
              </button>
            );
          })}
          {list.length === 0 && (
            <div className="px-3 py-6 text-center text-[12px] text-zinc-600">{t('bindNoMatch', '没有匹配的 agent')}</div>
          )}
        </div>
      </div>
    </div>
  ), document.body);
}
function toast(m: string) { window.dispatchEvent(new CustomEvent('show-toast', { detail: m })); }
function errText(e: any) { return String(e?.response?.data?.detail || e?.message || e || i18n.t('errorUnknown', { ns: 'im' })); }

const INPUT = 'h-9 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors hover:border-white/[0.14] focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 disabled:opacity-50';

function PlatformIcon({ platform, size = 14 }: { platform: string; size?: number }) {
  if (platform === 'cicy_cloud') return <Mail size={size} className="text-blue-400" />;
  if (platform === 'telegram') return <Send size={size} className="text-sky-400" />;
  if (platform === 'wechat') return <MessageCircle size={size} className="text-emerald-400" />;
  if (platform === 'feishu') return <Zap size={size} className="text-indigo-400" />;
  return <MessageCircle size={size} className="text-zinc-400" />;
}

function stateTone(s: string): { tone: string; label: string } {
  switch (s) {
    case 'connected': return { tone: 'bg-emerald-400', label: i18n.t('stateConnected', { ns: 'im' }) };
    case 'qr_wait': return { tone: 'bg-amber-400', label: i18n.t('stateQrWait', { ns: 'im' }) };
    case 'scaned': return { tone: 'bg-amber-400', label: i18n.t('stateScanned', { ns: 'im' }) };
    case 'error': return { tone: 'bg-red-400', label: i18n.t('stateError', { ns: 'im' }) };
    case 'logged_out': return { tone: 'bg-red-400', label: i18n.t('stateLoggedOut', { ns: 'im' }) };
    case 'disabled': return { tone: 'bg-zinc-600', label: i18n.t('stateDisabled', { ns: 'im' }) };
    default: return { tone: 'bg-zinc-500', label: i18n.t('stateStandby', { ns: 'im' }) };
  }
}

function StatusPill({ state }: { state: string }) {
  const { tone, label } = stateTone(state);
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-white/[0.04] px-2 py-1 text-[11px] text-zinc-400">
      <span className={cn('h-1.5 w-1.5 rounded-full', tone)} />{label}
    </span>
  );
}

function SectionHeader({ children }: { children: React.ReactNode }) {
  return <div className="text-[11px] font-semibold uppercase tracking-[0.13em] text-zinc-500">{children}</div>;
}

function Field({ label, help, children }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-[12px] font-medium text-zinc-400">{label}</span>
      {children}
      {help && <span className="mt-1.5 block text-[11px] leading-snug text-zinc-600">{help}</span>}
    </label>
  );
}

type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
function Btn({ variant = 'secondary', size = 'md', icon, children, busy, className, ...rest }: {
  variant?: BtnVariant; size?: 'sm' | 'md'; icon?: React.ReactNode; children?: React.ReactNode; busy?: boolean;
} & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const styles: Record<BtnVariant, string> = {
    primary: 'bg-white text-[#0b0b0c] hover:bg-zinc-200 disabled:bg-white/40 font-medium',
    secondary: 'border border-white/[0.1] bg-white/[0.03] text-zinc-200 hover:bg-white/[0.07] hover:border-white/[0.16]',
    ghost: 'text-zinc-400 hover:text-zinc-100 hover:bg-white/[0.05]',
    danger: 'border border-red-500/20 bg-red-500/[0.07] text-red-300 hover:bg-red-500/[0.14]',
  };
  const sizes = { sm: 'h-7 px-2.5 text-[12px] gap-1', md: 'h-8 px-3 text-[13px] gap-1.5' };
  return (
    <button {...rest} className={cn('inline-flex items-center justify-center rounded-lg transition-colors disabled:cursor-not-allowed', sizes[size], styles[variant], className)}>
      {busy ? <Spinner size={size === 'sm' ? 'xs' : 'sm'} /> : icon}
      {children}
    </button>
  );
}

function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-md bg-white/[0.045]', className)} />;
}

// The wechat backend returns the URL the QR should encode (a redirect that the
// WeChat client opens when scanned), NOT a pre-rendered image. We hand the URL
// to a third-party QR generator to produce the image. Same approach as the
// original IMDashboard.tsx (pre-d0755ce) so behavior matches expectations.
function qrImageFor(content: string, size = 220) {
  return `https://api.qrserver.com/v1/create-qr-code/?size=${size}x${size}&margin=8&data=${encodeURIComponent(content)}`;
}

/* ───────────── component ───────────── */

export default function IMDashboard({ leftMount, rightMount }: {
  leftMount: HTMLElement | null;
  rightMount: HTMLElement | null;
}) {
  const { t } = useTranslation('im');
  const { confirm, node: dialogsNode } = useDialogs();
  const [accounts, setAccounts] = useState<IMAccount[]>([]);
  const [platforms, setPlatforms] = useState<IMPlatform[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<string>('all');
  const [addMenuOpen, setAddMenuOpen] = useState(false);
  const addMenuRef = useRef<HTMLDivElement>(null);
  const [filterMenuOpen, setFilterMenuOpen] = useState(false);
  const filterMenuRef = useRef<HTMLDivElement>(null);
  const [bindOpen, setBindOpen] = useState(false);
  const bindRef = useRef<HTMLDivElement>(null);
  // Add-flow agent binding (WeChat / TG): defaults to the master w-1001, changed
  // via the same BindPicker, layered above the add modals.
  const [addBind, setAddBind] = useState('w-1001');
  const [addBindOpen, setAddBindOpen] = useState(false);
  const [draft, setDraft] = useState<{ name: string; secret: string; boundPaneId: string; appId: string }>({ name: '', secret: '', boundPaneId: '', appId: '' });
  const [baseline, setBaseline] = useState('');
  const [panes, setPanes] = useState<PaneOpt[]>([]);
  const [showSecret, setShowSecret] = useState(false);
  const [secretLoading, setSecretLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [syncingFeishuName, setSyncingFeishuName] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testRes, setTestRes] = useState<TestResult | null>(null);
  const [wxLogin, setWxLogin] = useState<WxLoginState | null>(null);
  const [wxStarting, setWxStarting] = useState(false);
  const [wxError, setWxError] = useState<string>('');
  // Pane id that the next-created wechat account should auto-bind to. Set by
  // addWeChat before starting the QR flow (either an already-available local
  // gateway pane, or a freshly auto-spawned "Wechat Worker").
  const [pendingBindPane, setPendingBindPane] = useState<string>('');
  const [pendingBindCreated, setPendingBindCreated] = useState(false);

  // Telegram add modal state — 用 modal 让用户输入 bot token，再创建账号 + 写 secret。
  // 流程：token 输入 → 后端校验 → 提示用户发送消息绑定 chat_id → 检测到 → 关 modal。
  const [tgModalOpen, setTgModalOpen] = useState(false);
  const [tgTokenInput, setTgTokenInput] = useState('');
  const [tgSubmitting, setTgSubmitting] = useState(false);
  const [tgError, setTgError] = useState('');
  const [tgStep, setTgStep] = useState<'token' | 'wait_message'>('token');
  const [tgPendingAccount, setTgPendingAccount] = useState<IMAccount | null>(null);

  // 飞书添加 modal — 输入 App ID + App Secret(飞书开放平台企业自建应用),
  // 后端验证 tenant token 后创建账号;随后进入「配置向导」:每 5 秒自动体检,
  // 逐项亮灯 + 直达链接,小白照着红灯修,修好灯自动变绿。
  const [fsModalOpen, setFsModalOpen] = useState(false);
  const [fsStep, setFsStep] = useState<'creds' | 'guide'>('creds');
  const [fsAccountId, setFsAccountId] = useState<number | null>(null);
  const [fsChecks, setFsChecks] = useState<any[]>([]);
  const [fsAllOk, setFsAllOk] = useState(false);
  const [fsAppId, setFsAppId] = useState('');
  const [fsSecret, setFsSecret] = useState('');
  const [fsSubmitting, setFsSubmitting] = useState(false);
  const [fsError, setFsError] = useState('');
  const [cloudModalOpen, setCloudModalOpen] = useState(false);
  const [cloudEmail, setCloudEmail] = useState('');
  const [cloudTeam, setCloudTeam] = useState('');
  const [cloudState, setCloudState] = useState('');
  const [cloudSubmitting, setCloudSubmitting] = useState(false);
  const [cloudError, setCloudError] = useState('');
  const [cloudInstance, setCloudInstance] = useState<{ teamId?: string; proxyHost?: string; proxyAvailable?: number | boolean } | null>(null);
  const [cloudTunnelStarting, setCloudTunnelStarting] = useState(false);
  const selected = useMemo(() => accounts.find((a) => a.id === selectedId) || null, [accounts, selectedId]);

  /* ---- editor sync ---- */
  // Default new accounts to w-1001 (the primary built-in pane). If acc has no
  // bind yet and w-1001 exists in panes, pre-fill it but keep baseline empty
  // so the form reads as "dirty" and Save commits the default to the backend.
  const loadEditor = useCallback((acc: IMAccount | null) => {
    setTestRes(null);
    setShowSecret(false);
    if (!acc) {
      const empty = { name: '', secret: '', boundPaneId: '', appId: '' };
      setDraft(empty);
      setBaseline(JSON.stringify(empty));
      return;
    }
    const serverBind = acc.bound_pane_id || '';
    const defaultBind = serverBind || 'w-1001';
    const appId = String((acc as any).config?.app_id ?? '');
    setDraft({ name: acc.name || '', secret: '', boundPaneId: defaultBind, appId });
    setBaseline(JSON.stringify({ name: acc.name || '', secret: '', boundPaneId: serverBind, appId }));
  }, []);

  useEffect(() => { loadEditor(selected); }, [selected, loadEditor]);

  // Eye toggle: hiding is instant. Revealing, when the field is still empty but a
  // token is stored server-side, fetches the full token on demand and fills it in.
  // Filling the stored token is NOT treated as an edit (baseline is bumped to match),
  // so the form stays clean and Save won't needlessly resend it.
  const toggleSecret = useCallback(async () => {
    if (showSecret) { setShowSecret(false); return; }
    if (!draft.secret && selected?.has_secret) {
      setSecretLoading(true);
      try {
        const res = await apiService.getIMAccountSecret(selected.id);
        const full = String((res as any)?.data?.secret || '');
        if (full) {
          setDraft((d) => ({ ...d, secret: full }));
          setBaseline((b) => { try { const o = JSON.parse(b); o.secret = full; return JSON.stringify(o); } catch { return b; } });
        }
      } catch { /* ignore — still reveal whatever's typed */ }
      finally { setSecretLoading(false); }
    }
    setShowSecret(true);
  }, [showSecret, draft.secret, selected]);

  /* ---- panes for bind picker — EVERY agent, any type, any gateway ---- */
  // Reply push works on BOTH paths now: the local gateway (ai_gateway_audit.go)
  // and the non-gateway MITM audit, so binding no longer depends on
  // use_custom_gateway OR agent_type. List every agent (cicy / claude / codex /
  // opencode / …), sorted by id ascending; the picker searches id + title.
  useEffect(() => {
    apiService.getPanes().then((r: any) => {
      const raw = Array.isArray(r.data?.panes) ? r.data.panes : (Array.isArray(r.data) ? r.data : []);
      const list: PaneOpt[] = raw.map((p: any) => ({
        pane_id: String(p.pane_id || p.paneId || '').replace(/:main\.0$/, ''),
        title: String(p.title || p.name || ''),
        agent_type: String(p.agent_type || p.agentType || ''),
        use_custom_gateway: !!(p.use_custom_gateway ?? p.useCustomGateway),
      })).filter((p: PaneOpt) => !!p.pane_id)
        .sort((a: PaneOpt, b: PaneOpt) => paneIdNum(a.pane_id) - paneIdNum(b.pane_id) || a.pane_id.localeCompare(b.pane_id));
      setPanes(list);
    }).catch(() => {});
  }, []);

  /* ---- panes already bound by OTHER same-platform accounts (can't double-bind) ---- */
  const busyPanes = useMemo(() => {
    const busy = new Set<string>();
    if (!selected) return busy;
    for (const a of accounts) {
      if (a.id === selected.id) continue;
      if (a.platform !== selected.platform) continue;
      const id = (a.bound_pane_id || '').replace(/:main\.0$/, '');
      if (id) busy.add(id);
    }
    return busy;
  }, [accounts, selected]);



  const snapshot = JSON.stringify(draft);
  const dirty = snapshot !== baseline;

  /* ---- data load ---- */
  const load = useCallback(async (preserveId?: number) => {
    setLoading(true);
    try {
      const [accRes, plRes] = await Promise.all([apiService.getIMAccounts(), apiService.getIMPlatforms()]);
      const accs: IMAccount[] = (accRes?.data?.accounts || []) as IMAccount[];
      const pls: IMPlatform[] = (plRes?.data?.platforms || []) as IMPlatform[];
      setAccounts(accs);
      setPlatforms(pls);
      if (preserveId && accs.some((a) => a.id === preserveId)) {
        setSelectedId(preserveId);
      } else if (selectedId && !accs.some((a) => a.id === selectedId)) {
        setSelectedId(null);
      }
    } catch (e) {
      toast(t('loadAccountsFailed', { err: errText(e) }));
    } finally {
      setLoading(false);
    }
  }, [selectedId, t]);

  useEffect(() => { void load(); }, []); // eslint-disable-line

  /* ---- filter chips: 'all' + each platform observed in accounts OR in platforms catalog ---- */
  const filterChips = useMemo(() => {
    const seen = new Map<string, { kind: string; label: string; count: number }>();
    seen.set('all', { kind: 'all', label: t('marketplaceAll', { ns: 'workspace' }) || 'All', count: accounts.length });
    for (const p of platforms) {
      seen.set(p.kind, { kind: p.kind, label: p.label || p.kind, count: 0 });
    }
    for (const a of accounts) {
      const k = a.platform || 'other';
      if (!seen.has(k)) seen.set(k, { kind: k, label: k, count: 0 });
      seen.get(k)!.count += 1;
    }
    return Array.from(seen.values());
  }, [platforms, accounts, t]);

  /* ---- filtered list ---- */
  const filteredAccounts = useMemo(() => {
    const q = query.trim().toLowerCase();
    let list = accounts;
    if (filter !== 'all') list = list.filter((a) => a.platform === filter);
    if (q) list = list.filter((a) => `${a.name} ${a.platform} ${a.bound_pane_title}`.toLowerCase().includes(q));
    // Stable sort: platform (alpha), then name
    return [...list].sort((a, b) => (a.platform || '').localeCompare(b.platform || '') || (a.name || '').localeCompare(b.name || ''));
  }, [accounts, query, filter]);

  /* ---- selection ---- */
  const guardDirty = async (fn: () => void) => {
    if (!dirty) { fn(); return; }
    const ok = await confirm({
      title: i18n.t('confirmDiscardTitle', { ns: 'provider' }),
      body: i18n.t('confirmDiscardBody', { ns: 'provider' }),
      confirmLabel: i18n.t('confirmDiscardConfirm', { ns: 'provider' }),
      danger: true,
    });
    if (ok) fn();
  };
  const selectAccount = (acc: IMAccount) => {
    void guardDirty(() => setSelectedId(acc.id));
  };
  const closeDetail = () => { void guardDirty(() => setSelectedId(null)); };

  /* ---- add menu click-outside ---- */
  useEffect(() => {
    if (!addMenuOpen) return;
    const onDoc = (e: MouseEvent) => {
      if (addMenuRef.current && !addMenuRef.current.contains(e.target as Node)) setAddMenuOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [addMenuOpen]);

  /* ---- filter menu click-outside ---- */
  useEffect(() => {
    if (!filterMenuOpen) return;
    const onDoc = (e: MouseEvent) => {
      if (filterMenuRef.current && !filterMenuRef.current.contains(e.target as Node)) setFilterMenuOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [filterMenuOpen]);

  const addAccount = (platform: string) => {
    setAddMenuOpen(false);
    if (platform === 'telegram') return void addTelegram();
    if (platform === 'wechat') return void addWeChat();
    if (platform === 'feishu') return void addFeishu();
    if (platform === 'cicy_cloud') {
      setCloudEmail(''); setCloudTeam(''); setCloudState(''); setCloudError(''); setCloudModalOpen(true);
    }
  };

  const submitCloudEmail = async () => {
    setCloudSubmitting(true); setCloudError('');
    try {
      const res = await apiService.startCiCyCloudLogin(cloudEmail.trim(), cloudTeam.trim());
      setCloudState(String(res?.data?.state || ''));
    } catch (e) { setCloudError(errText(e)); }
    finally { setCloudSubmitting(false); }
  };

  const reloginCloud = () => {
    if (!selected || selected.platform !== 'cicy_cloud') return;
    setCloudEmail(String(selected.config?.email || selected.name || ''));
    setCloudTeam(String(selected.config?.team_id || cloudInstance?.teamId || ''));
    setCloudState(''); setCloudError(''); setCloudModalOpen(true);
  };

  useEffect(() => {
    if (selected?.platform !== 'cicy_cloud') { setCloudInstance(null); return; }
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const refresh = () => apiService.getCiCyCloudInstances().then((res) => {
      if (stopped) return;
      const instanceID = String(selected.config?.instance_id || '');
      const instances = (res?.data?.instances || []) as Array<{ instanceId?: string; teamId?: string; proxyHost?: string; proxyAvailable?: number | boolean }>;
      const current = instances.find((item) => item.instanceId === instanceID) || null;
      setCloudInstance(current);
      if (current?.proxyHost) setCloudTunnelStarting(false);
    }).catch(() => { if (!stopped) setCloudInstance(null); }).finally(() => {
      if (!stopped) timer = setTimeout(refresh, 5000);
    });
    void refresh();
    return () => { stopped = true; if (timer) clearTimeout(timer); };
  }, [selected]);

  const enableCloudTunnel = async () => {
    setCloudTunnelStarting(true); setCloudError('');
    try {
      await apiService.enableCiCyCloudTunnel();
      toast('固定域名正在启动');
    } catch (e) {
      setCloudTunnelStarting(false); setCloudError(errText(e));
    }
  };

  useEffect(() => {
    if (!cloudModalOpen || !cloudState) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        const res = await apiService.getCiCyCloudLoginStatus(cloudState);
        if (stopped) return;
        if (res?.data?.status === 'ready') {
          setCloudModalOpen(false); setCloudState('');
          const account = res?.data?.account as IMAccount | undefined;
          await load(account?.id);
          if (account?.id) setSelectedId(account.id);
          toast('CiCy Cloud 已连接');
          return;
        }
        if (res?.data?.status === 'expired') { setCloudError('登录链接已过期，请重新发送'); return; }
      } catch (e) { if (!stopped) setCloudError(errText(e)); return; }
      timer = setTimeout(poll, 2000);
    };
    timer = setTimeout(poll, 1500);
    return () => { stopped = true; if (timer) clearTimeout(timer); };
  }, [cloudModalOpen, cloudState, load]);

  const addFeishu = () => {
    setFsError('');
    setFsAppId('');
    setFsSecret('');
    setFsStep('creds');
    setFsAccountId(null);
    setFsChecks([]);
    setFsAllOk(false);
    setAddBind('w-1001');
    setAddBindOpen(false);
    setFsModalOpen(true);
  };

  // 已有飞书账号也能随时重进向导(详情页「配置向导」按钮)
  const openFeishuGuide = (accId: number) => {
    setFsError('');
    setFsStep('guide');
    setFsAccountId(accId);
    setFsChecks([]);
    setFsAllOk(false);
    setFsModalOpen(true);
  };

  const submitFeishu = async () => {
    const appId = fsAppId.trim();
    const secret = fsSecret.trim();
    if (!appId || !secret) {
      setFsError(t('feishuCredsRequired', '请填写 App ID 和 App Secret'));
      return;
    }
    setFsSubmitting(true);
    setFsError('');
    try {
      const res = await apiService.createIMAccount({ platform: 'feishu', app_id: appId, secret });
      const a = res?.data?.account as IMAccount | undefined;
      if (!a?.id) throw new Error('create account: missing id');
      if (addBind) { try { await apiService.bindIMAccount(a.id, addBind); } catch (e) { console.warn('feishu bind failed', e); } }
      toast(t('accountAdded', { platform: '飞书' }));
      await load(a.id);
      setSelectedId(a.id);
      // 不关 modal,直接进配置向导:亮灯清单 + 自动重测
      setFsAccountId(a.id);
      setFsStep('guide');
    } catch (e) {
      setFsError(errText(e));
    } finally {
      setFsSubmitting(false);
    }
  };

  // 配置向导:每 5 秒跑一次后端体检,checks 逐项亮灯;全绿停止轮询。
  useEffect(() => {
    if (!fsModalOpen || fsStep !== 'guide' || !fsAccountId) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const tick = async () => {
      if (stopped) return;
      try {
        const r = await apiService.testIMAccount(fsAccountId);
        const d = r?.data ?? {};
        if (!stopped) {
          setFsChecks(Array.isArray(d.checks) ? d.checks : []);
          setFsAllOk(!!d.ok);
          if (d.ok) return;   // 全绿,停
        }
      } catch {}
      if (!stopped) timer = setTimeout(tick, 5000);
    };
    void tick();
    return () => { stopped = true; if (timer) clearTimeout(timer); };
  }, [fsModalOpen, fsStep, fsAccountId]);

  /* ---- mutations ---- */
  // Telegram 添加流程：弹 modal → 输入 bot token → 提交后台校验 → 让用户发条消息给 bot
  // 后端 imHandleInbound 会把 chat_id 写进 acc.config.chat_id；前端轮询检测到 chat_id 出现，
  // 关闭 modal，账号就绪。
  const addTelegram = () => {
    setTgError('');
    setTgTokenInput('');
    setTgStep('token');
    setTgPendingAccount(null);
    setAddBind('w-1001');
    setAddBindOpen(false);
    setTgModalOpen(true);
  };

  const submitTgToken = async () => {
    const token = tgTokenInput.trim();
    if (!token) {
      setTgError(t('telegramTokenRequired'));
      return;
    }
    setTgSubmitting(true);
    setTgError('');
    try {
      // 创建账号时直接带 token —— 后端会调 telegramValidateToken 验证 + 拉 bot_username。
      const res = await apiService.createIMAccount({ platform: 'telegram', secret: token });
      const a = res?.data?.account as IMAccount | undefined;
      if (!a?.id) throw new Error('create account: missing id');
      // Bind to the chosen agent (default w-1001) right away.
      if (addBind) { try { await apiService.bindIMAccount(a.id, addBind); } catch (e) { console.warn('tg bind failed', e); } }
      setTgPendingAccount(a);
      setTgStep('wait_message');
      // 主列表也刷一下让新账号马上显示
      void load(a.id);
    } catch (e) {
      setTgError(errText(e));
    } finally {
      setTgSubmitting(false);
    }
  };

  // 在 wait_message 步骤轮询账号，一旦 chat_id 出现（用户发了消息）就关 modal。
  useEffect(() => {
    if (!tgModalOpen || tgStep !== 'wait_message' || !tgPendingAccount) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const tick = async () => {
      if (stopped) return;
      try {
        const r = await apiService.getIMAccount(tgPendingAccount.id);
        const acc = (r?.data?.account ?? r?.data) as IMAccount | undefined;
        const chatID = String(acc?.config?.chat_id ?? '').trim();
        if (chatID) {
          stopped = true;
          toast(t('accountAdded', { platform: 'Telegram' }));
          setTgModalOpen(false);
          await load(acc?.id ?? tgPendingAccount.id);
          setSelectedId(acc?.id ?? tgPendingAccount.id);
          return;
        }
      } catch {}
      if (!stopped) timer = setTimeout(tick, 1500);
    };
    void tick();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
    };
  }, [tgModalOpen, tgStep, tgPendingAccount, t, load]);

  const addWeChat = async () => {
    // refuse if there's already a pending wechat scan
    const pending = accounts.find((a) => a.platform === 'wechat' && (a.state === 'qr_wait' || a.state === 'scaned' || a.state === 'pending'));
    if (pending) { toast(t('wechatPendingLogin')); return; }
    setWxError('');
    setWxLogin(null);
    setWxStarting(true);

    // Bind target defaults to the master (w-1001); the user can change it to ANY
    // agent via the picker in the modal — onPick updates pendingBindPane too.
    // (No more dedicated auto-created "Wechat Worker".)
    setAddBind('w-1001');
    setPendingBindPane('w-1001');
    setPendingBindCreated(false);

    // Step 2: ask backend for a QR session
    try {
      const r = await apiService.startWeChatLogin();
      const d = r?.data as any;
      setWxLogin({ sessionId: d.session_id || d.sessionId, qrcodeUrl: d.qrcode_url || d.qrcodeUrl, state: d.state || 'qr_wait', detail: d.detail });
    } catch (e) {
      setWxError(errText(e));
      toast(t('wechatScanFailed', { err: errText(e) }));
    } finally {
      setWxStarting(false);
    }
  };

  // poll wechat login until finished
  useEffect(() => {
    if (!wxLogin) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const tick = async () => {
      if (stopped || !wxLogin) return;
      try {
        const r = await apiService.getWeChatLoginStatus(wxLogin.sessionId);
        const d = r?.data as any;
        const nextState = String(d.state || wxLogin.state);
        const newId = (d.account_id as number | undefined) || 0;
        setWxLogin((cur) => cur ? { ...cur, state: nextState, qrcodeUrl: d.qrcode_url || cur.qrcodeUrl, detail: d.detail } : cur);
        // Backend signals success via state="created" (with account_id) — this
        // is the moment to close the modal and refresh the list. Older code
        // looked only for state="connected" and missed this transition.
        if (nextState === 'created' || nextState === 'connected' || newId > 0) {
          stopped = true;
          // Auto-bind to the worker pane chosen (or freshly created) in addWeChat
          if (pendingBindPane && newId) {
            try { await apiService.bindIMAccount(newId, pendingBindPane); }
            catch (e) { console.warn('auto-bind failed', e); }
          }
          setPendingBindPane('');
          setPendingBindCreated(false);
          toast(t('wechatAdded'));
          setWxLogin(null);
          await load(newId || undefined);
          if (newId) setSelectedId(newId);
          return;
        }
        if (nextState === 'expired' || nextState === 'error' || nextState === 'cancelled') {
          // user can regenerate or close
        }
      } catch {
        // keep polling on transient errors
      }
      timer = setTimeout(tick, 2500);
    };
    timer = setTimeout(tick, 1000);
    return () => { stopped = true; if (timer) clearTimeout(timer); };
  }, [wxLogin?.sessionId, t, load]);

  const closeWxModal = async () => {
    const sid = wxLogin?.sessionId;
    const bindPane = pendingBindPane;
    const created = pendingBindCreated;
    setWxLogin(null);
    setWxError('');
    setWxStarting(false);
    setPendingBindPane('');
    setPendingBindCreated(false);
    // 取消后端 QR 守护进程
    if (sid) { try { await apiService.cancelWeChatLogin(sid); } catch {} }
    // 仅删除本次 addWeChat 自动新建的 worker pane（复用已有 pane 不删）
    if (bindPane && created) {
      try { await apiService.deletePane(bindPane); } catch {}
      try {
        const pr = await apiService.getPanes();
        const raw = Array.isArray(pr.data?.panes) ? pr.data.panes : (Array.isArray(pr.data) ? pr.data : []);
        setPanes(raw.map((p: any) => ({
          pane_id: String(p.pane_id || p.paneId || '').replace(/:main\.0$/, ''),
          title: String(p.title || p.name || ''),
          agent_type: String(p.agent_type || p.agentType || ''),
          use_custom_gateway: !!(p.use_custom_gateway ?? p.useCustomGateway),
        })).filter((p: any) => p.pane_id && ['claude', 'opencode', 'codex'].includes(p.agent_type)));
      } catch {}
    }
  };

  const save = async () => {
    if (!selected) return;
    const patch: any = {};
    if (draft.name.trim() !== (selected.name || '')) patch.name = draft.name.trim();
    if (draft.secret.trim() !== '') patch.secret = draft.secret.trim();
    if (selected.platform === 'feishu' && draft.appId.trim() !== String((selected as any).config?.app_id ?? '')) patch.app_id = draft.appId.trim();
    const wantBind = draft.boundPaneId.trim();
    const curBind = selected.bound_pane_id || '';
    const bindChanged = wantBind !== curBind;
    if (Object.keys(patch).length === 0 && !bindChanged) { toast(t('noChanges')); return; }
    setSaving(true);
    try {
      if (Object.keys(patch).length > 0) {
        await apiService.updateIMAccount(selected.id, patch);
      }
      if (bindChanged) {
        if (wantBind) await apiService.bindIMAccount(selected.id, wantBind);
        else await apiService.unbindIMAccount(selected.id);
      }
      toast(t('saved'));
      await load(selected.id);
    } catch (e) {
      toast(t('saveFailed', { err: errText(e) }));
    } finally {
      setSaving(false);
    }
  };

  const syncFeishuName = async () => {
    if (!selected || selected.platform !== 'feishu') return;
    setSyncingFeishuName(true);
    try {
      const res = await apiService.syncFeishuAccountName(selected.id);
      const name = String(res?.data?.name || '');
      toast(t('feishuNameSynced', { name }));
      await load(selected.id);
    } catch (e) {
      toast(t('feishuNameSyncFailed', { err: errText(e) }));
    } finally {
      setSyncingFeishuName(false);
    }
  };

  const remove = async (acc: IMAccount) => {
    const ok = await confirm({
      title: t('confirmDeleteTitle'),
      body: <>{t('confirmDeleteBodyPrefix')} <span className="font-mono text-zinc-100">{acc.name || acc.platform}</span>{t('confirmDeleteBodySuffix')}</>,
      confirmLabel: t('deleteConfirm'),
      danger: true,
    });
    if (!ok) return;
    try {
      await apiService.deleteIMAccount(acc.id);
      toast(t('deleted'));
      if (selectedId === acc.id) setSelectedId(null);
      await load();
    } catch (e) {
      toast(t('deleteFailed', { err: errText(e) }));
    }
  };

  const testSend = async () => {
    if (!selected) return;
    setTesting(true);
    setTestRes(null);
    try {
      const r = await apiService.testIMAccount(selected.id);
      setTestRes(r.data);
      if ((r.data as any)?.ok || (r.data as any)?.success) toast(t('testMessageSent'));
    } catch (e) {
      setTestRes({ ok: false, error: errText(e) });
    } finally {
      setTesting(false);
    }
  };

  const relogin = async () => {
    if (!selected) return;
    try {
      await apiService.reloginIMAccount(selected.id);
      toast(t('reloginStarted'));
      await load(selected.id);
    } catch (e) {
      toast(t('operationFailed', { err: errText(e) }));
    }
  };

  /* ---- ⌘S ---- */
  const saveRef = useRef(save); saveRef.current = save;
  const canSaveRef = useRef(dirty && !!selected); canSaveRef.current = dirty && !!selected;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S') && canSaveRef.current) {
        e.preventDefault();
        void saveRef.current();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  if (!leftMount && !rightMount) return null;

  const detailOpen = !!selected;

  /* ───────── LEFT PANEL ───────── */
  const leftPanelUI = (
    <div data-id="im-left-root" className="h-full flex flex-col bg-[#0A0A0A] text-zinc-300">
      {/* search + add 同行 */}
      <div className="px-2.5 pt-2.5 shrink-0 flex items-center gap-1.5">
        <div className="relative flex-1">
          <Search size={13} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-zinc-600" />
          <input data-id="im-search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('searchAccount')} className={cn(INPUT, 'h-8 pl-7 text-[12px]')} />
        </div>
        {/* 加账号按钮 — 下拉菜单 */}
        <div ref={addMenuRef} className="relative shrink-0">
          <button
            data-id="im-add-btn"
            type="button"
            disabled={wxStarting}
            onClick={() => setAddMenuOpen((o) => !o)}
            className="flex h-8 w-8 items-center justify-center rounded-lg border border-white/[0.09] bg-white/[0.025] text-zinc-400 transition-colors hover:border-white/[0.16] hover:text-zinc-100 disabled:opacity-50"
            title={t('addIMAccount')}
          >
            {wxStarting ? <Spinner size="xs" /> : <Plus size={14} />}
          </button>
          {addMenuOpen && (
            <div data-id="im-add-dropdown" className="absolute right-0 top-[calc(100%+4px)] min-w-[160px] z-50 rounded-xl border border-white/[0.09] bg-[#141416] p-1 shadow-2xl shadow-black/60">
              {platforms.length === 0 && <div className="px-2.5 py-2 text-[11px] text-zinc-600">{t('loadingText')}</div>}
              {platforms.map((p) => {
                const cloudAlreadyAdded = p.kind === 'cicy_cloud' && accounts.some((account) => account.platform === 'cicy_cloud');
                const addable = !cloudAlreadyAdded && (p.kind === 'telegram' || p.kind === 'wechat' || p.kind === 'feishu' || p.kind === 'cicy_cloud');
                return (
                  <button
                    key={p.kind}
                    data-id={`im-add-${p.kind}`}
                    type="button"
                    disabled={!addable}
                    onClick={() => addAccount(p.kind)}
                    className={cn('flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors',
                      addable ? 'text-zinc-200 hover:bg-white/[0.06]' : 'cursor-not-allowed text-zinc-600')}
                  >
                    <PlatformIcon platform={p.kind} size={14} />
                    <span className="flex-1">{p.label}</span>
                    {cloudAlreadyAdded && <span className="text-[10px] text-zinc-600">已添加</span>}
                    {p.needs_qr && <QrCode size={12} className="text-zinc-500" />}
                  </button>
                );
              })}
              <div className="mt-1 border-t border-white/[0.06] px-2.5 py-1.5 text-[10px] text-zinc-600">{t('discordSoon')}</div>
            </div>
          )}
        </div>
      </div>

      {/* platform filter: 全部按钮 + 平台 select 下拉 */}
      <div data-id="im-filters" className="px-2 py-2 shrink-0 flex items-center gap-1.5">
        {(() => {
          const allChip = filterChips.find((c) => c.kind === 'all');
          const platChips = filterChips.filter((c) => c.kind !== 'all');
          const curPlat = filter !== 'all' ? platChips.find((c) => c.kind === filter) : null;
          return (
            <>
              <button
                data-id="im-filter-all"
                onClick={() => setFilter('all')}
                className={cn(
                  'inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-[11px] transition-colors',
                  filter === 'all' ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:text-zinc-300',
                )}
              >
                <span>{allChip?.label}</span>
                <span className={cn('text-[10px]', filter === 'all' ? 'text-zinc-400' : 'text-zinc-600')}>{allChip?.count ?? 0}</span>
              </button>
              <div ref={filterMenuRef} className="relative">
                <button
                  data-id="im-filter-platform-trigger"
                  onClick={() => setFilterMenuOpen((o) => !o)}
                  className={cn(
                    'inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-[11px] transition-colors',
                    curPlat ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:text-zinc-300',
                  )}
                >
                  {curPlat && <PlatformIcon platform={curPlat.kind} size={10} />}
                  <span>{curPlat?.label || t('marketplaceCategoryAll', { defaultValue: '平台' })}</span>
                  {curPlat && <span className={cn('text-[10px]', 'text-zinc-400')}>{curPlat.count}</span>}
                  <ChevronDown size={10} className="text-zinc-500" />
                </button>
                {filterMenuOpen && (
                  <div data-id="im-filter-platform-menu" className="absolute left-0 top-[calc(100%+4px)] z-50 min-w-[140px] rounded-xl border border-white/[0.09] bg-[#141416] p-1 shadow-2xl shadow-black/60">
                    {platChips.map((c) => (
                      <button
                        key={c.kind}
                        data-id={`im-filter-${c.kind}`}
                        onClick={() => { setFilter(c.kind); setFilterMenuOpen(false); }}
                        className={cn(
                          'flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-[12px] transition-colors',
                          filter === c.kind ? 'bg-white/[0.06] text-zinc-100' : 'text-zinc-300 hover:bg-white/[0.04]',
                        )}
                      >
                        <PlatformIcon platform={c.kind} size={12} />
                        <span className="flex-1">{c.label}</span>
                        <span className="text-[10px] text-zinc-500">{c.count}</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </>
          );
        })()}
      </div>

      {/* list — mixed platforms */}
      <div className="flex-1 overflow-auto px-2">
        {loading && accounts.length === 0 && [0, 1, 2].map((i) => (
          <div key={i} className="px-2 py-2 space-y-1.5"><Skeleton className="h-3 w-28" /><Skeleton className="h-2.5 w-20" /></div>
        ))}
        {!loading && filteredAccounts.length === 0 && (
          <div className="px-3 py-10 text-center text-[12px] leading-relaxed text-zinc-600 whitespace-pre-line">{t('emptyAccountsHelp')}</div>
        )}
        <ul className="space-y-0.5">
          {filteredAccounts.map((acc) => {
            const active = selectedId === acc.id;
            return (
              <li key={acc.id}>
                <button data-id={`im-item-${acc.id}`} type="button" onClick={() => selectAccount(acc)}
                  className={cn('group relative flex w-full items-center gap-2 rounded-lg py-2 pl-3 pr-2 text-left transition-colors', active ? 'bg-white/[0.05]' : 'hover:bg-white/[0.03]')}>
                  {active && <span className="absolute inset-y-2 left-0 w-0.5 rounded-r bg-blue-400" />}
                  <PlatformIcon platform={acc.platform} size={14} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      <span className={cn('truncate text-[13px]', active ? 'text-white' : 'text-zinc-200')}>{acc.name || acc.platform}</span>
                      {acc.platform === 'feishu' && (acc as any).config?.app_id ? <span className="shrink-0 font-mono text-[10px] text-zinc-600">…{String((acc as any).config.app_id).slice(-6)}</span> : null}
                      <StatusPill state={acc.state} />
                    </div>
                    {acc.bound_pane_title && (
                      <div className="mt-0.5 truncate font-mono text-[10.5px] text-zinc-600">{t('boundTo', { paneId: acc.bound_pane_title })}</div>
                    )}
                  </div>
                  <span role="button" tabIndex={-1} onClick={(e) => { e.stopPropagation(); void remove(acc); }}
                    className="grid h-6 w-6 shrink-0 place-items-center rounded text-zinc-600 opacity-0 transition-all hover:bg-red-500/15 hover:text-red-300 group-hover:opacity-100" title={t('delete')}>
                    <Trash2 size={12} />
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );

  /* ───────── RIGHT PANEL ───────── */
  const isTelegram = selected?.platform === 'telegram';
  const isWeChat = selected?.platform === 'wechat';
  const isFeishu = selected?.platform === 'feishu';
  const isCloud = selected?.platform === 'cicy_cloud';
  const testOk = !!(testRes && (testRes.ok || testRes.success));
  const testFail = !!(testRes && !testOk);

  const detailUI = selected && (
    <div data-id="im-detail-root" className="absolute inset-0 z-30 flex flex-col bg-[#0A0A0A] text-zinc-300 overflow-hidden">
      <header data-id="im-detail-header" className="flex h-12 shrink-0 items-center gap-2 border-b border-white/[0.06] px-4">
        <button data-id="im-detail-back" onClick={closeDetail} className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.05] hover:text-zinc-200" title={t('back')}>
          <ArrowLeft size={16} />
        </button>
        <PlatformIcon platform={selected.platform} size={14} />
        <h1 className="truncate text-[14px] font-semibold text-white">{selected.name || selected.platform}</h1>
        <StatusPill state={selected.state} />
        {dirty && <span className="inline-flex items-center gap-1 text-[11px] text-amber-300"><span className="h-1.5 w-1.5 rounded-full bg-amber-400" />{i18n.t('unsaved', { ns: 'provider' })}</span>}
        <div className="flex-1" />
        <Btn data-id="im-detail-delete" variant="danger" size="sm" icon={<Trash2 size={12} />} onClick={() => void remove(selected)}>{t('delete')}</Btn>
      </header>

      <main data-id="im-detail-main" className="flex-1 overflow-auto">
        <div className="mx-auto max-w-[680px] px-8 py-7">
          <div className="space-y-7">
            {/* Basic */}
            <section data-id="im-detail-section-connection" className="space-y-3.5">
              <SectionHeader>{t('sectionConnection')}</SectionHeader>
              {!isCloud && <Field label={t('fieldName')}>
                <input data-id="im-detail-name-input" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} className={INPUT} placeholder={selected.platform} />
              </Field>}

              {isCloud && (
                <div data-id="im-detail-cloud-status" className="space-y-3 rounded-xl border border-white/[0.08] bg-white/[0.025] p-4">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <div className="text-[12px] text-zinc-500">连接状态</div>
                      <div className={cn('mt-1 text-[13px] font-medium', selected.state === 'connected' ? 'text-emerald-300' : 'text-red-300')}>
                        {selected.state === 'connected' ? '已登录' : (selected.state_detail || '未认证')}
                      </div>
                    </div>
                    <Btn data-id="im-detail-cloud-relogin" variant={selected.state === 'connected' ? 'secondary' : 'primary'} size="sm" icon={<RefreshCw size={12} />} onClick={reloginCloud}>
                      {selected.state === 'connected' ? '更改 Team' : '重新 Email 登录'}
                    </Btn>
                  </div>
                  <div className="grid gap-3 border-t border-white/[0.06] pt-3 sm:grid-cols-2">
                    <div><div className="text-[11px] text-zinc-600">Email</div><div className="mt-1 font-mono text-[12px] text-zinc-300">{selected.config?.email || selected.name || '—'}</div></div>
                    <div><div className="text-[11px] text-zinc-600">Team</div><div className="mt-1 font-mono text-[12px] text-zinc-300">{cloudInstance?.teamId || selected.config?.team_id || '—'}</div></div>
                    <div className="sm:col-span-2">
                      <div className="text-[11px] text-zinc-600">固定域名</div>
                      <div className="mt-1 flex items-center gap-3">
                        <div className="break-all font-mono text-[12px] text-zinc-300">{cloudInstance?.proxyHost ? `https://${cloudInstance.proxyHost}` : (cloudTunnelStarting ? '正在启动…' : '尚未开启')}</div>
                        {!cloudInstance?.proxyHost && <Btn data-id="im-detail-cloud-enable-tunnel" variant="secondary" size="sm" busy={cloudTunnelStarting} disabled={cloudTunnelStarting} onClick={() => void enableCloudTunnel()}>开启固定域名</Btn>}
                      </div>
                      {cloudError && <div className="mt-2 text-[11px] text-red-300">{cloudError}</div>}
                    </div>
                  </div>
                </div>
              )}

              {isFeishu && (
                <>
                  <Field label="App ID" help={t('feishuAppIdHelp', '飞书开放平台 → 凭证与基础信息。改了会清掉旧会话,重新捕获。')}>
                    <input
                      data-id="im-detail-appid-input"
                      value={draft.appId} onChange={(e) => setDraft((d) => ({ ...d, appId: e.target.value }))}
                      className={cn(INPUT, 'font-mono')} placeholder="cli_a1b2c3d4e5f6g7h8"
                      autoComplete="off" autoCapitalize="off" autoCorrect="off" spellCheck={false}
                    />
                  </Field>
                  <Field
                    label="App Secret"
                    help={selected.has_secret ? t('botTokenSet', { tail: selected.secret_tail || '' }) : t('feishuSecretHelp', '飞书开放平台 → 凭证与基础信息 → App Secret')}
                  >
                    <div className="relative">
                      <input
                        data-id="im-detail-fs-secret-input"
                        type="text" name="cicy-im-fs-secret" value={draft.secret} onChange={(e) => setDraft((d) => ({ ...d, secret: e.target.value }))}
                        className={cn(INPUT, 'pr-9 font-mono')} placeholder={selected.has_secret ? '••• ' + (selected.secret_tail || '') : ''}
                        autoComplete="off" autoCapitalize="off" autoCorrect="off" spellCheck={false}
                        data-1p-ignore data-lpignore="true"
                        style={showSecret ? undefined : ({ WebkitTextSecurity: 'disc' } as React.CSSProperties)}
                      />
                      <button data-id="im-detail-fs-secret-toggle" type="button" disabled={secretLoading} onClick={toggleSecret} className="absolute right-1.5 top-1/2 grid h-6 w-6 -translate-y-1/2 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300 disabled:opacity-50" title={showSecret ? t('hide') : t('show')}>
                        {secretLoading ? <Loader2 size={13} className="animate-spin" /> : showSecret ? <EyeOff size={13} /> : <Eye size={13} />}
                      </button>
                    </div>
                  </Field>
                </>
              )}

              {isTelegram && (
                <>
                  <Field
                    label={t('fieldBotToken')}
                    help={selected.has_secret ? t('botTokenSet', { tail: selected.secret_tail || '' }) : t('botTokenHint')}
                  >
                    <div className="relative">
                      <input
                        data-id="im-detail-secret-input"
                        type="text" name="cicy-im-secret" value={draft.secret} onChange={(e) => setDraft((d) => ({ ...d, secret: e.target.value }))}
                        className={cn(INPUT, 'pr-9 font-mono')} placeholder={selected.has_secret ? '••• ' + (selected.secret_tail || '') : '123456:ABC-DEF...'}
                        autoComplete="off" autoCapitalize="off" autoCorrect="off" spellCheck={false}
                        data-1p-ignore data-lpignore="true"
                        style={showSecret ? undefined : ({ WebkitTextSecurity: 'disc' } as React.CSSProperties)}
                      />
                      <button data-id="im-detail-secret-toggle" type="button" disabled={secretLoading} onClick={toggleSecret} className="absolute right-1.5 top-1/2 grid h-6 w-6 -translate-y-1/2 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300 disabled:opacity-50" title={showSecret ? t('hide') : t('show')}>
                        {secretLoading ? <Loader2 size={13} className="animate-spin" /> : showSecret ? <EyeOff size={13} /> : <Eye size={13} />}
                      </button>
                    </div>
                  </Field>
                  <div className="rounded-lg border border-white/[0.06] bg-white/[0.015] p-3 text-[11.5px] leading-relaxed text-zinc-400">
                    <div className="mb-1.5 flex items-center gap-1.5 text-zinc-300">
                      <a href="https://t.me/BotFather" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sky-300 hover:underline">
                        <ExternalLink size={11} /> {t('openBotFather')}
                      </a>
                    </div>
                    <div className="text-zinc-500">{t('botFatherSteps')}</div>
                  </div>
                </>
              )}

              {isWeChat && (
                <div data-id="im-detail-wechat-login" className="space-y-2.5">
                  <div className="flex items-center gap-2 text-[12px]">
                    <span className="text-zinc-400">{t('sectionLogin')}:</span>
                    {selected.state === 'connected' ? (
                      <>
                        <span className="inline-flex items-center gap-1 text-emerald-300"><Check size={12} /> {t('loggedIn')}</span>
                        {selected.config?.ilink_user_id && (
                          <span className="font-mono text-[11px] text-zinc-500 truncate">{String(selected.config.ilink_user_id)}</span>
                        )}
                      </>
                    ) : (                      <span className="inline-flex items-center gap-1 text-amber-300"><QrCode size={12} /> {stateTone(selected.state).label}</span>
                    )}
                    {selected.state !== 'connected' && (
                      <Btn data-id="im-detail-relogin" variant="ghost" size="sm" icon={<RefreshCw size={12} />} onClick={() => void relogin()}>{t('regenerateQr')}</Btn>
                    )}
                  </div>
                  {selected.state !== 'connected' && selected.config?.qrcode_url && (
                    <img data-id="im-detail-wechat-qr" src={qrImageFor(String(selected.config.qrcode_url), 176)} alt="WeChat QR" className="h-44 w-44 rounded-lg bg-white p-2" />
                  )}
                  <div className="text-[11px] leading-relaxed text-zinc-600 space-y-0.5">
                    <div>{t('wechatStep1')}</div>
                    <div>{t('wechatStep2')}</div>
                    <div>{t('wechatStep3')}</div>
                  </div>
                </div>
              )}
            </section>

            {/* Agent binding */}
            {!isCloud && <section data-id="im-detail-section-binding" className="space-y-3.5">
              <SectionHeader>{t('sectionAgentBinding')}</SectionHeader>
              <Field label={t('fieldBindAgent')} help={(() => {
                const curPane = panes.find((p) => p.pane_id === draft.boundPaneId);
                if (!draft.boundPaneId || !curPane) return t('fieldBindAgentHelp');
                return curPane.use_custom_gateway ? t('fieldBindAgentHelp') : t('fieldBindAgentHelpNoGateway');
              })()}>
                {(() => {
                  const cur = draft.boundPaneId;
                  const curPane = panes.find((p) => p.pane_id === cur);
                  return (
                    <div ref={bindRef} className="relative">
                      <button
                        data-id="im-detail-bind-trigger"
                        type="button"
                        onClick={() => setBindOpen(true)}
                        className={cn(
                          'group flex h-11 w-full items-center gap-2.5 rounded-lg border bg-white/[0.025] pl-2 pr-2 text-left text-[13px] transition-colors',
                          bindOpen ? 'border-blue-500/55 ring-1 ring-blue-500/15' : 'border-white/[0.09] hover:border-white/[0.16]',
                        )}
                      >
                        {cur ? (
                          <>
                            <AgentAvatar agentType={curPane?.agent_type} title={curPane?.title || cur} variant="select" />
                            <div className="min-w-0 flex-1">
                              <div className="flex items-center gap-1.5">
                                <span className="truncate font-mono text-zinc-100">{cur}</span>
                                {curPane?.agent_type && <span className="rounded bg-white/[0.06] px-1 py-px text-[10px] font-medium text-zinc-400">{curPane.agent_type}</span>}
                              </div>
                              <span className="block truncate text-[11px] text-zinc-500">{curPane?.title || (cur === 'w-1001' ? '我01' : '')}</span>
                            </div>
                          </>
                        ) : (
                          <span className="flex-1 text-zinc-500">{t('unboundOption')}</span>
                        )}
                        <ChevronDown size={14} className="shrink-0 text-zinc-500 group-hover:text-zinc-300" />
                      </button>

                      {bindOpen && (
                        <BindPicker
                          panes={panes}
                          busyPanes={busyPanes}
                          value={cur}
                          z={9999}
                          onPick={(id) => { setDraft((d) => ({ ...d, boundPaneId: id })); setBindOpen(false); }}
                          onClose={() => setBindOpen(false)}
                        />
                      )}
                    </div>
                  );
                })()}
              </Field>
            </section>}

            {/* Test result */}
            {!isCloud && testRes && (
              <div data-id="im-detail-test-result" className={cn('relative rounded-xl border px-3.5 py-3 text-[12px]',
                testOk ? 'border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-200'
                  : testFail ? 'border-red-500/25 bg-red-500/[0.07] text-red-200'
                    : 'border-white/[0.08] bg-white/[0.03] text-zinc-300')}>
                <button onClick={() => setTestRes(null)} className="absolute right-2.5 top-2.5 text-current opacity-50 hover:opacity-100"><X size={12} /></button>
                <div className="flex items-center gap-1.5 pr-5 font-medium">
                  {testOk ? <Check size={13} /> : <X size={13} />}
                  {testOk ? t('testMessageSent') : t('sendFailed')}
                </div>
                {(testRes.error || testRes.detail) && (
                  <div className="mt-1 whitespace-pre-wrap break-all opacity-80 text-[11px]">{testRes.error || testRes.detail}</div>
                )}
              </div>
            )}
          </div>

          {/* footer */}
          {!isCloud && <div data-id="im-detail-footer" className="mt-7 flex items-center gap-2 border-t border-white/[0.06] pt-5">
            <Btn data-id="im-detail-save" variant="primary" size="md" icon={<Save size={14} />} busy={saving} disabled={saving || !dirty} onClick={() => void save()}>{t('save')}</Btn>
            <Btn data-id="im-detail-test" variant="secondary" size="md" icon={<Zap size={14} />} busy={testing} disabled={testing || selected.state !== 'connected'} onClick={() => void testSend()}>
              {isWeChat ? t('testSendWeChat') : t('testSend')}
            </Btn>
            {isFeishu && (
              <>
                {!selected.config?.app_name_synced && (
                  <Btn
                    data-id="im-detail-fs-sync-name"
                    variant="secondary"
                    size="md"
                    icon={<RefreshCw size={13} />}
                    busy={syncingFeishuName}
                    disabled={syncingFeishuName}
                    onClick={() => void syncFeishuName()}
                  >
                    {t('feishuSyncName', '同步名称')}
                  </Btn>
                )}
                <Btn data-id="im-detail-fs-guide" variant="secondary" size="md" onClick={() => openFeishuGuide(selected.id)}>
                  {t('feishuGuide', '配置向导')}
                </Btn>
              </>
            )}
            <div className="flex-1" />
          </div>}
        </div>
      </main>
    </div>
  );

  /* ───────── add-flow "bind agent" field (shared by WeChat + TG modals) ─────────
     Shows the chosen agent (default w-1001); clicking opens BindPicker on top. */
  const addBindPane = panes.find((p) => p.pane_id === addBind);
  const addBindField = (
    <div data-id="im-add-bind-field">
      <div className="mb-1.5 text-[12px] font-medium text-zinc-400">{t('fieldBindAgent')}</div>
      <button
        type="button"
        data-id="im-add-bind-trigger"
        onClick={() => setAddBindOpen(true)}
        className="group flex h-11 w-full items-center gap-2.5 rounded-lg border border-white/[0.09] bg-white/[0.025] px-2 text-left text-[13px] transition-colors hover:border-white/[0.16]"
      >
        {addBind ? (
          <>
            <AgentAvatar agentType={addBindPane?.agent_type} title={addBindPane?.title || addBind} variant="select" />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5">
                <span className="truncate font-mono text-zinc-100">{addBind}</span>
                {addBindPane?.agent_type && <span className="rounded bg-white/[0.06] px-1 py-px text-[10px] font-medium text-zinc-400">{addBindPane.agent_type}</span>}
              </div>
              <span className="block truncate text-[11px] text-zinc-500">{addBindPane?.title || (addBind === 'w-1001' ? '我01' : '')}</span>
            </div>
          </>
        ) : (
          <span className="flex-1 text-zinc-500">{t('unboundOption')}</span>
        )}
        <ChevronDown size={14} className="shrink-0 text-zinc-500 group-hover:text-zinc-300" />
      </button>
    </div>
  );

  let cloudModal: React.ReactNode = null;
  if (cloudModalOpen) {
    const waiting = !!cloudState;
    cloudModal = (
      <div data-id="im-cloud-modal" className="fixed inset-0 z-[10000] cursor-pointer" onClick={() => !cloudSubmitting && setCloudModalOpen(false)}>
        <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
        <div className="absolute left-1/2 top-1/2 w-[420px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 cursor-default rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60" onClick={(e) => e.stopPropagation()}>
          <div className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
            <div className="flex items-center gap-2"><PlatformIcon platform="cicy_cloud" size={16} /><h2 className="text-[15px] font-semibold text-white">{selected?.platform === 'cicy_cloud' ? 'CiCy Cloud 登录' : '添加 CiCy Cloud 账号'}</h2></div>
            <button onClick={() => setCloudModalOpen(false)} className="rounded-lg p-1.5 text-zinc-600 hover:bg-white/[0.06] hover:text-zinc-300"><X className="h-4 w-4" /></button>
          </div>
          <div className="space-y-4 px-5 py-5">
            {!waiting ? (
              <>
                <Field label="Email" help="点击邮件中的登录链接后，账号会自动注册或登录当前 cicy-code 实例。">
                  <input data-id="im-cloud-email" autoFocus type="email" value={cloudEmail}
                    onChange={(e) => { setCloudEmail(e.target.value); setCloudError(''); }}
                    placeholder="you@example.com" className={cn(INPUT, 'h-10')} disabled={cloudSubmitting} />
                </Field>
                <Field label="Team" help="Team 是这个 Instance 的固定标识。">
                  <input data-id="im-cloud-team" value={cloudTeam}
                    onChange={(e) => { setCloudTeam(e.target.value); setCloudError(''); }}
                    onKeyDown={(e) => {
                      const composing = e.nativeEvent.isComposing || e.keyCode === 229;
                      if (!composing && e.key === 'Enter' && cloudEmail.trim() && cloudTeam.trim()) void submitCloudEmail();
                    }}
                    placeholder="mac_local" className={cn(INPUT, 'h-10 font-mono')} disabled={cloudSubmitting} />
                </Field>
              </>
            ) : (
              <div className="rounded-xl border border-blue-500/25 bg-blue-500/[0.06] px-4 py-4 text-center">
                <Loader2 className="mx-auto h-5 w-5 animate-spin text-blue-300" />
                <div className="mt-2 text-[13px] font-medium text-zinc-200">登录邮件已发送</div>
                <div className="mt-1 text-[11px] text-zinc-500">请打开 {cloudEmail} 并点击登录链接，本页面会自动完成绑定。</div>
              </div>
            )}
            {cloudError && <div className="rounded-lg border border-red-500/25 bg-red-500/[0.06] px-3 py-2 text-[12px] text-red-300">{cloudError}</div>}
            <div className="flex justify-end gap-2">
              <Btn variant="ghost" size="md" onClick={() => setCloudModalOpen(false)}>取消</Btn>
              {!waiting && <Btn data-id="im-cloud-submit" variant="primary" size="md" onClick={() => void submitCloudEmail()} disabled={cloudSubmitting || !cloudEmail.trim() || !cloudTeam.trim()}>{cloudSubmitting ? <Spinner size="xs" /> : '发送登录邮件'}</Btn>}
              {waiting && cloudError && <Btn variant="secondary" size="md" onClick={() => { setCloudState(''); setCloudError(''); }}>重新发送</Btn>}
            </div>
          </div>
        </div>
      </div>
    );
  }

  /* ───────── WeChat QR modal ───────── */
  // Modal opens as soon as the user clicks "+ WeChat", even before the backend
  // returns the QR URL — otherwise the click feels unresponsive while we POST
  // to the WeChat server (can take several seconds).
  let wxModal: React.ReactNode = null;
  if (wxStarting || wxLogin || wxError) {
    const expired = wxLogin?.state === 'expired';
    const failed = wxLogin?.state === 'error' || !!wxError;
    const scanned = wxLogin?.state === 'scaned';
    const haveQR = !!wxLogin?.qrcodeUrl;
    const loading = wxStarting || (!haveQR && !failed && !expired);
    wxModal = (
      <div data-id="im-wx-modal" className="fixed inset-0 z-[10000] cursor-pointer" onClick={() => void closeWxModal()}>
        <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
        <div className="absolute left-1/2 top-1/2 w-[400px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 cursor-default select-text rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60"
          onClick={(e) => e.stopPropagation()}>
          <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
            <div className="flex items-center gap-2">
              <QrCode className="w-4 h-4 text-emerald-300" />
              <h2 className="text-[15px] font-semibold text-white">{t('scanLogin')}</h2>
            </div>
            <button onClick={() => void closeWxModal()} className="p-1.5 rounded-lg text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors">
              <X className="w-4 h-4" />
            </button>
          </div>
          <div className="px-5 py-5 flex flex-col items-center text-center">
            {loading && (
              <div className="h-52 w-52 grid place-items-center rounded-lg border border-white/[0.06] bg-white/[0.02]">
                <Spinner size="md" />
              </div>
            )}
            {!loading && haveQR && !expired && !failed && (
              <img src={qrImageFor(wxLogin!.qrcodeUrl, 220)} alt="WeChat QR" className="h-52 w-52 rounded-lg bg-white p-2.5" />
            )}
            {!loading && expired && <div className="h-52 w-52 grid place-items-center rounded-lg border border-amber-500/25 bg-amber-500/[0.06] text-[12px] text-amber-300">{t('qrExpired')}</div>}
            {!loading && failed && <div className="h-52 w-52 grid place-items-center rounded-lg border border-red-500/25 bg-red-500/[0.06] text-[12px] text-red-300 px-3 whitespace-pre-wrap">{wxError || t('scanFailed')}</div>}
            <div className="mt-3 text-[13px] font-medium text-zinc-200">
              {loading ? t('loadingText') : scanned ? t('alreadyScanned') : t('scanWithWeChat')}
            </div>
            {wxLogin?.detail && !loading && <div className="mt-1 text-[11px] text-zinc-500">{wxLogin.detail}</div>}
            <div className="mt-3 text-[11px] leading-relaxed text-zinc-600 max-w-[320px]">{t('scanSuccessNote')}</div>
            <div className="mt-4 w-full text-left">{addBindField}</div>
            {!loading && (expired || failed) && (
              <Btn variant="secondary" size="md" icon={<RefreshCw size={13} />} className="mt-4" onClick={() => void addWeChat()}>
                {t('regenerateAnotherQr')}
              </Btn>
            )}
          </div>
        </div>
      </div>
    );
  }

  // 飞书添加 modal — 输入 App ID + App Secret,后端验证后创建账号并绑定所选 agent。
  let fsModal: React.ReactNode = null;
  if (fsModalOpen) {
    const closeFsModal = () => {
      if (fsSubmitting) return;
      setFsModalOpen(false);
      setFsError('');
    };
    fsModal = (
      <div data-id="im-fs-modal" className="fixed inset-0 z-[10000] cursor-pointer" onClick={closeFsModal}>
        <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
        <div className="absolute left-1/2 top-1/2 w-[440px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 cursor-default select-text rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60"
          onClick={(e) => e.stopPropagation()}>
          <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
            <div className="flex items-center gap-2">
              <PlatformIcon platform="feishu" size={16} />
              <h2 className="text-[15px] font-semibold text-white">{fsStep === 'guide' ? t('feishuGuideTitle', '飞书配置向导') : t('addFeishuTitle', '添加飞书机器人')}</h2>
            </div>
            <button data-id="im-fs-modal-close" onClick={closeFsModal} className="p-1.5 rounded-lg text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors">
              <X className="w-4 h-4" />
            </button>
          </div>
          {fsStep === 'creds' && (
          <div className="px-5 py-5 space-y-4">
            <Field label="App ID">
              <input
                data-id="im-fs-modal-appid"
                autoFocus
                type="text"
                value={fsAppId}
                onChange={(e) => { setFsAppId(e.target.value); if (fsError) setFsError(''); }}
                placeholder="cli_a1b2c3d4e5f6g7h8"
                className={cn(INPUT, 'h-10 text-[13px] font-mono')}
                disabled={fsSubmitting}
              />
            </Field>
            <Field label="App Secret">
              <input
                data-id="im-fs-modal-secret"
                type="password"
                value={fsSecret}
                onChange={(e) => { setFsSecret(e.target.value); if (fsError) setFsError(''); }}
                onKeyDown={(e) => { if (e.key === 'Enter' && !fsSubmitting) void submitFeishu(); }}
                placeholder="••••••••••••••••"
                className={cn(INPUT, 'h-10 text-[13px] font-mono')}
                disabled={fsSubmitting}
              />
            </Field>
            {fsError && (
              <div className="rounded-lg border border-red-500/25 bg-red-500/[0.06] px-3 py-2 text-[12px] text-red-300 whitespace-pre-wrap">{fsError}</div>
            )}
            <div className="rounded-lg border border-white/[0.06] bg-white/[0.015] p-3 text-[12px] leading-relaxed text-zinc-400 space-y-1.5">
              <div className="flex items-center gap-1.5 text-zinc-300">
                <a href="https://open.feishu.cn/app" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-indigo-300 hover:underline">
                  <ExternalLink size={11} /> {t('openFeishuConsole', '打开飞书开放平台')}
                </a>
              </div>
              <div className="text-zinc-500">{t('feishuSteps', '创建企业自建应用 → 添加机器人能力 → 一次开通 im:message、im:chat:create、im:message.group_msg 和 im:resource → 事件订阅选「长连接」并订阅 im.message.receive_v1 → 发布。一个 App 可以为多个 Agent 自动创建独立群聊并收发媒体。')}</div>
            </div>
            {addBindField}
            <div className="flex justify-end gap-2 pt-1">
              <Btn data-id="im-fs-modal-cancel" variant="ghost" size="md" onClick={closeFsModal} disabled={fsSubmitting}>{t('cancel')}</Btn>
              <Btn data-id="im-fs-modal-submit" variant="primary" size="md" onClick={() => void submitFeishu()} disabled={fsSubmitting || !fsAppId.trim() || !fsSecret.trim()}>
                {fsSubmitting ? <Spinner size="xs" /> : t('confirm')}
              </Btn>
            </div>
          </div>
          )}
          {fsStep === 'guide' && (
          <div className="px-5 py-5 space-y-2.5 max-h-[70vh] overflow-y-auto">
            <div className="text-[12.5px] leading-relaxed text-zinc-400">
              {t('feishuGuideIntro', '照着')}<b className="text-red-300">{t('feishuGuideRed', '红灯项')}</b>{t('feishuGuideIntro2', '点「去配置」到飞书后台改好,这里每 5 秒自动重测,配好会自动变绿——不用手动刷新。')}
            </div>
            {fsChecks.length === 0 && (
              <div className="py-8 grid place-items-center"><Spinner size="md" /></div>
            )}
            {fsChecks.map((c: any) => (
              <div key={c.key} className={cn('rounded-xl border px-3.5 py-2.5',
                c.status === 'ok' ? 'border-emerald-500/25 bg-emerald-500/[0.06]'
                  : c.status === 'fail' ? 'border-red-500/30 bg-red-500/[0.06]'
                    : 'border-amber-500/25 bg-amber-500/[0.05]')}>
                <div className="flex items-center gap-2">
                  <span className="text-[13px]">{c.status === 'ok' ? '✅' : c.status === 'fail' ? '❌' : '⚠️'}</span>
                  <span className="text-[13px] font-semibold text-zinc-100">{c.name}</span>
                  {c.link && c.status !== 'ok' && (
                    <span className="ml-auto inline-flex shrink-0 items-center gap-1.5">
                      <a href={c.link} target="_blank" rel="noreferrer"
                        className="inline-flex items-center gap-1 rounded-lg border border-sky-500/30 bg-sky-500/[0.08] px-2.5 py-1 text-[11px] text-sky-300 hover:bg-sky-500/[0.15] transition-colors">
                        <ExternalLink size={10} /> {t('goConfigure', '去配置')}
                      </a>
                      <button type="button" title={t('copyLink', '复制链接')}
                        onClick={() => { try { void navigator.clipboard.writeText(c.link); toast(t('linkCopied', '链接已复制')); } catch {} }}
                        className="inline-flex items-center rounded-lg border border-white/[0.12] bg-white/[0.03] px-2 py-1 text-[11px] text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.08] transition-colors">
                        ⧉
                      </button>
                    </span>
                  )}
                </div>
                {c.detail && c.status !== 'ok' && (
                  <div className="mt-1 pl-6 text-[11.5px] leading-relaxed text-zinc-400 whitespace-pre-wrap">{c.detail}</div>
                )}
              </div>
            ))}
            {fsAllOk && (
              <div className="rounded-xl border border-emerald-500/30 bg-emerald-500/[0.08] px-4 py-3 text-[13px] leading-relaxed text-emerald-200">
                🎉 {t('feishuAllReady', '全部就绪!去飞书搜你的应用名,私聊发 /agents 挑一个 agent,再 /bind 绑定——每个会话可以绑不同的 agent。')}
              </div>
            )}
            <div className="flex justify-end pt-1">
              <Btn data-id="im-fs-guide-close" variant="ghost" size="md" onClick={closeFsModal}>{fsAllOk ? t('done', '完成') : t('configureLater', '稍后再配')}</Btn>
            </div>
          </div>
          )}
        </div>
      </div>
    );
  }

  // Telegram 添加 modal — 两步：1) 输入 bot token  2) 引导用户给 bot 发条消息绑定 chat_id。
  let tgModal: React.ReactNode = null;
  if (tgModalOpen) {
    const closeTgModal = () => {
      if (tgSubmitting) return;
      setTgModalOpen(false);
      setTgError('');
    };
    const botUsername = String(tgPendingAccount?.config?.bot_username ?? '').trim();
    const botLink = botUsername ? `https://t.me/${botUsername}` : '';
    tgModal = (
      <div data-id="im-tg-modal" className="fixed inset-0 z-[10000] cursor-pointer" onClick={closeTgModal}>
        <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
        <div className="absolute left-1/2 top-1/2 w-[440px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 cursor-default select-text rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60"
          onClick={(e) => e.stopPropagation()}>
          <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
            <div className="flex items-center gap-2">
              <PlatformIcon platform="telegram" size={16} />
              <h2 className="text-[15px] font-semibold text-white">{t('addTelegramTitle')}</h2>
            </div>
            <button data-id="im-tg-modal-close" onClick={closeTgModal} className="p-1.5 rounded-lg text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors">
              <X className="w-4 h-4" />
            </button>
          </div>
          {tgStep === 'token' && (
            <div className="px-5 py-5 space-y-4">
              <Field label={t('fieldBotToken')} help={t('botTokenHint')}>
                <input
                  data-id="im-tg-modal-token"
                  autoFocus
                  type="text"
                  value={tgTokenInput}
                  onChange={(e) => { setTgTokenInput(e.target.value); if (tgError) setTgError(''); }}
                  onKeyDown={(e) => { if (e.key === 'Enter' && !tgSubmitting) void submitTgToken(); }}
                  placeholder="123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
                  className={cn(INPUT, 'h-10 text-[13px] font-mono')}
                  disabled={tgSubmitting}
                />
              </Field>
              {tgError && (
                <div className="rounded-lg border border-red-500/25 bg-red-500/[0.06] px-3 py-2 text-[12px] text-red-300 whitespace-pre-wrap">{tgError}</div>
              )}
              <div className="rounded-lg border border-white/[0.06] bg-white/[0.015] p-3 text-[12px] leading-relaxed text-zinc-400 space-y-1.5">
                <div className="flex items-center gap-1.5 text-zinc-300">
                  <a href="https://t.me/BotFather" target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sky-300 hover:underline">
                    <ExternalLink size={11} /> {t('openBotFather')}
                  </a>
                </div>
                <div className="text-zinc-500">{t('botFatherSteps')}</div>
              </div>
              {addBindField}
              <div className="flex justify-end gap-2 pt-1">
                <Btn data-id="im-tg-modal-cancel" variant="ghost" size="md" onClick={closeTgModal} disabled={tgSubmitting}>{t('cancel')}</Btn>
                <Btn data-id="im-tg-modal-submit" variant="primary" size="md" onClick={() => void submitTgToken()} disabled={tgSubmitting || !tgTokenInput.trim()}>
                  {tgSubmitting ? <Spinner size="xs" /> : t('confirm')}
                </Btn>
              </div>
            </div>
          )}
          {tgStep === 'wait_message' && (
            <div className="px-5 py-6 flex flex-col items-center text-center">
              <div className="h-14 w-14 rounded-full border border-white/[0.08] bg-white/[0.025] grid place-items-center mb-3">
                <Spinner size="md" />
              </div>
              <div className="text-[14px] font-medium text-zinc-100">{t('tgWaitMessageTitle')}</div>
              <div className="mt-1.5 text-[12px] leading-relaxed text-zinc-500 max-w-[340px]">
                {t('tgWaitMessageHelp')}
              </div>
              {botLink && (
                <a
                  data-id="im-tg-modal-open-bot"
                  href={botLink}
                  target="_blank"
                  rel="noreferrer"
                  className="mt-3 inline-flex items-center gap-1.5 rounded-lg border border-sky-500/30 bg-sky-500/[0.08] px-3 py-1.5 text-[12.5px] text-sky-200 hover:bg-sky-500/[0.14] transition-colors"
                >
                  <ExternalLink size={12} /> @{botUsername}
                </a>
              )}
              <div className="mt-5 w-full flex justify-end">
                <Btn data-id="im-tg-modal-skip" variant="ghost" size="md" onClick={closeTgModal}>{t('addLater')}</Btn>
              </div>
            </div>
          )}
        </div>
      </div>
    );
  }

  return (
    <>
      {leftMount && createPortal(leftPanelUI, leftMount)}
      {rightMount && detailOpen && createPortal(detailUI, rightMount)}
      {wxModal && createPortal(wxModal, document.body)}
      {cloudModal && createPortal(cloudModal, document.body)}
      {tgModal && createPortal(tgModal, document.body)}
      {fsModal && createPortal(fsModal, document.body)}
      {addBindOpen && (
        <BindPicker
          panes={panes}
          value={addBind}
          z={10010}
          onPick={(id) => { setAddBind(id); setPendingBindPane(id); setAddBindOpen(false); }}
          onClose={() => setAddBindOpen(false)}
        />
      )}
      {createPortal(dialogsNode, document.body)}
    </>
  );
}
