import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import {
  Plus, Save, Trash2, Zap, Eye, EyeOff, Check, X,
  Send, MessageCircle, QrCode, RefreshCw, Search, ExternalLink, ChevronDown, Loader2,
} from 'lucide-react';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';
import { Spinner } from '../ui/Spinner';
import AgentAvatar from '../AgentAvatar';

/* ───────────── types ───────────── */

type Platform = 'telegram' | 'wechat';

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
function toast(m: string) { window.dispatchEvent(new CustomEvent('show-toast', { detail: m })); }
function errText(e: any) { return String(e?.response?.data?.detail || e?.message || e || i18n.t('errorUnknown', { ns: 'im' })); }

const INPUT = 'h-9 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors hover:border-white/[0.14] focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 disabled:opacity-50';

function PlatformIcon({ platform, size = 14 }: { platform: string; size?: number }) {
  if (platform === 'telegram') return <Send size={size} className="text-sky-400" />;
  if (platform === 'wechat') return <MessageCircle size={size} className="text-emerald-400" />;
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
  const [draft, setDraft] = useState<{ name: string; secret: string; boundPaneId: string }>({ name: '', secret: '', boundPaneId: '' });
  const [baseline, setBaseline] = useState('');
  const [panes, setPanes] = useState<PaneOpt[]>([]);
  const [showSecret, setShowSecret] = useState(false);
  const [secretLoading, setSecretLoading] = useState(false);
  const [saving, setSaving] = useState(false);
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

  const selected = useMemo(() => accounts.find((a) => a.id === selectedId) || null, [accounts, selectedId]);

  /* ---- editor sync ---- */
  // Default new accounts to w-1001 (the primary built-in pane). If acc has no
  // bind yet and w-1001 exists in panes, pre-fill it but keep baseline empty
  // so the form reads as "dirty" and Save commits the default to the backend.
  const loadEditor = useCallback((acc: IMAccount | null) => {
    setTestRes(null);
    setShowSecret(false);
    if (!acc) {
      const empty = { name: '', secret: '', boundPaneId: '' };
      setDraft(empty);
      setBaseline(JSON.stringify(empty));
      return;
    }
    const serverBind = acc.bound_pane_id || '';
    const defaultBind = serverBind || 'w-1001';
    setDraft({ name: acc.name || '', secret: '', boundPaneId: defaultBind });
    setBaseline(JSON.stringify({ name: acc.name || '', secret: '', boundPaneId: serverBind }));
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

  /* ---- panes for bind picker — any agent, gateway or not ---- */
  // Reply push works on BOTH paths now: the local gateway (ai_gateway_audit.go)
  // and the non-gateway MITM audit, so binding no longer depends on
  // use_custom_gateway. Show every claude / opencode / codex agent regardless of
  // gateway mode. (Non-gateway help text still flags the push caveat.)
  useEffect(() => {
    apiService.getPanes().then((r: any) => {
      const raw = Array.isArray(r.data?.panes) ? r.data.panes : (Array.isArray(r.data) ? r.data : []);
      const list: PaneOpt[] = raw.map((p: any) => ({
        pane_id: String(p.pane_id || p.paneId || '').replace(/:main\.0$/, ''),
        title: String(p.title || p.name || ''),
        agent_type: String(p.agent_type || p.agentType || ''),
        use_custom_gateway: !!(p.use_custom_gateway ?? p.useCustomGateway),
      })).filter((p: PaneOpt) => p.pane_id && ['claude', 'opencode', 'codex'].includes(p.agent_type));
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

  /* ---- ALL wechat-bound panes (used by add flow to decide whether to spawn a new worker) ---- */
  const wechatBoundPanes = useMemo(() => {
    const s = new Set<string>();
    for (const a of accounts) {
      if (a.platform !== 'wechat') continue;
      const id = (a.bound_pane_id || '').replace(/:main\.0$/, '');
      if (id) s.add(id);
    }
    return s;
  }, [accounts]);

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

  /* ---- bind picker click-outside ---- */
  useEffect(() => {
    if (!bindOpen) return;
    const onDoc = (e: MouseEvent) => {
      if (bindRef.current && !bindRef.current.contains(e.target as Node)) setBindOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setBindOpen(false); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      document.removeEventListener('keydown', onKey, true);
    };
  }, [bindOpen]);

  const addAccount = (platform: string) => {
    setAddMenuOpen(false);
    if (platform === 'telegram') return void addTelegram();
    if (platform === 'wechat') return void addWeChat();
  };

  /* ---- mutations ---- */
  // Telegram 添加流程：弹 modal → 输入 bot token → 提交后台校验 → 让用户发条消息给 bot
  // 后端 imHandleInbound 会把 chat_id 写进 acc.config.chat_id；前端轮询检测到 chat_id 出现，
  // 关闭 modal，账号就绪。
  const addTelegram = () => {
    setTgError('');
    setTgTokenInput('');
    setTgStep('token');
    setTgPendingAccount(null);
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

    // Step 1: each wechat account gets its OWN dedicated "Wechat Worker" pane.
    // We never reuse generic local-gateway agents like w-1001 (which the user
    // has for other purposes). Look for an existing unbound Wechat Worker, and
    // if none, spin up a fresh one (claude agent on deepseek with custom gateway).
    let bindTarget = panes.find((p) => p.title === 'Wechat Worker' && !wechatBoundPanes.has(p.pane_id))?.pane_id || '';
    let bindCreated = false;
    if (!bindTarget) {
      try {
        const r = await apiService.createPane({
          title: 'Wechat Worker',
          agent_type: 'claude',
          default_model: 'deepseek-v4-pro',
          use_custom_gateway: true,
        });
        const d = r?.data as any;
        if (!d?.success) throw new Error(d?.error || 'create pane failed');
        bindTarget = String(d.pane_id || '').replace(/:main\.0$/, '');
        bindCreated = true;
        // refresh panes so the new pane shows up in the picker right away
        try {
          const pr = await apiService.getPanes();
          const raw = Array.isArray(pr.data?.panes) ? pr.data.panes : (Array.isArray(pr.data) ? pr.data : []);
          const list: PaneOpt[] = raw.map((p: any) => ({
            pane_id: String(p.pane_id || p.paneId || '').replace(/:main\.0$/, ''),
            title: String(p.title || p.name || ''),
            agent_type: String(p.agent_type || p.agentType || ''),
            use_custom_gateway: !!(p.use_custom_gateway ?? p.useCustomGateway),
          })).filter((p: PaneOpt) => p.pane_id && ['claude', 'opencode', 'codex'].includes(p.agent_type));
          setPanes(list);
        } catch {}
      } catch (e) {
        setWxError(errText(e));
        toast(t('wechatScanFailed', { err: 'auto-create worker failed: ' + errText(e) }));
        setWxStarting(false);
        return;
      }
    }
    setPendingBindPane(bindTarget);
    setPendingBindCreated(bindCreated);

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
                const addable = p.kind === 'telegram' || p.kind === 'wechat';
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
  const testOk = !!(testRes && (testRes.ok || testRes.success));
  const testFail = !!(testRes && !testOk);

  const detailUI = selected && (
    <div data-id="im-detail-root" className="absolute inset-0 z-30 flex flex-col bg-[#0A0A0A] text-zinc-300 overflow-hidden">
      <header data-id="im-detail-header" className="flex h-12 shrink-0 items-center gap-2 border-b border-white/[0.06] px-4">
        <button data-id="im-detail-back" onClick={closeDetail} className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.05] hover:text-zinc-200" title={t('back')}>
          <X size={16} />
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
              <Field label={t('fieldName')}>
                <input data-id="im-detail-name-input" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} className={INPUT} placeholder={selected.platform} />
              </Field>

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
            <section data-id="im-detail-section-binding" className="space-y-3.5">
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
                        onClick={() => setBindOpen((o) => !o)}
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
                        <div data-id="im-detail-bind-dropdown" className="absolute left-0 right-0 z-50 mt-1.5 max-h-72 overflow-auto rounded-xl border border-white/[0.09] bg-[#141416] p-1 shadow-2xl shadow-black/60">
                          <button
                            type="button"
                            onClick={() => { setDraft((d) => ({ ...d, boundPaneId: '' })); setBindOpen(false); }}
                            className={cn('flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-white/[0.06]', !cur && 'bg-white/[0.07]')}
                          >
                            <span className="grid w-4 place-items-center">{!cur && <Check size={13} className="text-blue-400" />}</span>
                            <span className="text-zinc-500">{t('unboundOption')}</span>
                          </button>
                          {panes.length > 0 && <div className="my-1 h-px bg-white/[0.06]" />}
                          {panes.filter(p => !busyPanes.has(p.pane_id) || p.pane_id === cur).map((p) => {
                            const isSel = p.pane_id === cur;
                            return (
                              <button
                                key={p.pane_id}
                                type="button"
                                onClick={() => { setDraft((d) => ({ ...d, boundPaneId: p.pane_id })); setBindOpen(false); }}
                                className={cn(
                                  'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors hover:bg-white/[0.06]',
                                  isSel && 'bg-white/[0.07]',
                                )}
                              >
                                <span className="grid w-4 shrink-0 place-items-center">{isSel && <Check size={13} className="text-blue-400" />}</span>
                                <AgentAvatar agentType={p.agent_type} title={p.title || p.pane_id} variant="option" />
                                <span className="min-w-0 flex-1">
                                  <span className="flex items-center gap-1.5">
                                    <span className="truncate font-mono text-zinc-100">{p.pane_id}</span>
                                    {p.agent_type && <span className="rounded bg-white/[0.06] px-1 py-px text-[10px] font-medium text-zinc-400">{p.agent_type}</span>}
                                  </span>
                                  {p.title && <span className="block truncate text-[11px] text-zinc-500">{p.title}</span>}
                                </span>
                              </button>
                            );
                          })}
                          {/* Fallback: keep old bound pane visible even if it's no longer in panes list */}
                          {cur && !panes.some((p) => p.pane_id === cur) && (
                            <>
                              <div className="my-1 h-px bg-white/[0.06]" />
                              <button
                                type="button"
                                onClick={() => setBindOpen(false)}
                                className="flex w-full items-center gap-2.5 rounded-lg bg-amber-500/[0.06] px-2.5 py-2 text-left text-[13px]"
                              >
                                <span className="grid w-4 shrink-0 place-items-center"><Check size={13} className="text-amber-400" /></span>
                                <span className="font-mono text-amber-200">{cur}</span>
                                <span className="ml-auto text-[10px] text-amber-400">offline?</span>
                              </button>
                            </>
                          )}
                        </div>
                      )}
                    </div>
                  );
                })()}
              </Field>
            </section>

            {/* Test result */}
            {testRes && (
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
          <div data-id="im-detail-footer" className="mt-7 flex items-center gap-2 border-t border-white/[0.06] pt-5">
            <Btn data-id="im-detail-save" variant="primary" size="md" icon={<Save size={14} />} busy={saving} disabled={saving || !dirty} onClick={() => void save()}>{t('save')}</Btn>
            <Btn data-id="im-detail-test" variant="secondary" size="md" icon={<Zap size={14} />} busy={testing} disabled={testing || selected.state !== 'connected'} onClick={() => void testSend()}>
              {isWeChat ? t('testSendWeChat') : t('testSend')}
            </Btn>
            <div className="flex-1" />
          </div>
        </div>
      </main>
    </div>
  );

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
        <div className="absolute left-1/2 top-1/2 w-[400px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 cursor-default rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60"
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
        <div className="absolute left-1/2 top-1/2 w-[440px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 cursor-default rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60"
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
      {tgModal && createPortal(tgModal, document.body)}
      {createPortal(dialogsNode, document.body)}
    </>
  );
}
