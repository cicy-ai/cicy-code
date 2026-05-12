import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { createPortal } from 'react-dom';
import {
  ArrowLeft, MessageCircle, Plus, RefreshCw, Save, Trash2, Zap, Eye, EyeOff, Check, X, Loader2,
  Send, Link2, QrCode, ChevronDown, ExternalLink,
} from 'lucide-react';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';

/* ───────────── types ───────────── */

interface IMAccount {
  id: number;
  platform: 'telegram' | 'wechat' | string;
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
interface PaneOpt { pane_id: string; title: string; agent_type: string }

/* ───────────── helpers ───────────── */

function cn(...p: Array<string | false | null | undefined>) { return p.filter(Boolean).join(' '); }
function toast(m: string) { window.dispatchEvent(new CustomEvent('show-toast', { detail: m })); }
function errText(e: any) { return String(e?.response?.data?.detail || e?.message || e || i18n.t('errorUnknown', { ns: 'im' })); }
function shortPane(id: string) { return String(id || '').replace(/:main\.0$/, ''); }

const INPUT = 'h-9 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors hover:border-white/[0.14] focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 disabled:opacity-50';
const CARD = 'rounded-2xl border border-white/[0.07] bg-white/[0.015]';

function PlatformIcon({ platform, size = 14 }: { platform: string; size?: number }) {
  if (platform === 'telegram') return <Send size={size} className="text-sky-400" />;
  if (platform === 'wechat') return <MessageCircle size={size} className="text-emerald-400" />;
  return <MessageCircle size={size} className="text-zinc-400" />;
}
function platformLabel(p: string) { return p === 'telegram' ? 'Telegram' : p === 'wechat' ? i18n.t('platformWechat', { ns: 'im' }) : p; }

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
function Btn({ variant = 'secondary', icon, children, busy, className, ...rest }: {
  variant?: BtnVariant; icon?: React.ReactNode; children?: React.ReactNode; busy?: boolean;
} & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const styles: Record<BtnVariant, string> = {
    primary: 'bg-white text-[#0b0b0c] hover:bg-zinc-200 disabled:bg-white/40 font-medium',
    secondary: 'border border-white/[0.1] bg-white/[0.03] text-zinc-200 hover:bg-white/[0.07] hover:border-white/[0.16]',
    ghost: 'text-zinc-400 hover:text-zinc-100 hover:bg-white/[0.05]',
    danger: 'border border-red-500/20 bg-red-500/[0.07] text-red-300 hover:bg-red-500/[0.14]',
  };
  return (
    <button {...rest} className={cn('inline-flex h-8 items-center justify-center gap-1.5 rounded-lg px-3 text-[13px] transition-colors disabled:cursor-not-allowed', styles[variant], className)}>
      {busy ? <Loader2 size={14} className="animate-spin" /> : icon}{children}
    </button>
  );
}
function Skeleton({ className }: { className?: string }) { return <div className={cn('animate-pulse rounded-md bg-white/[0.045]', className)} />; }

/* ───────────── component ───────────── */

export default function IMDashboard({ onBack }: { onBack?: () => void }) {
  const { t } = useTranslation('im');
  const { confirm, node: dialogsNode } = useDialogs();
  const [loading, setLoading] = useState(true);
  const [accounts, setAccounts] = useState<IMAccount[]>([]);
  const [platforms, setPlatforms] = useState<IMPlatform[]>([]);
  const [panes, setPanes] = useState<PaneOpt[]>([]);
  const [selId, setSelId] = useState<number | null>(null);
  const [query, setQuery] = useState('');
  const [addOpen, setAddOpen] = useState(false);
  const addRef = useRef<HTMLDivElement>(null);
  // wechat add-via-scan modal
  const [wxLogin, setWxLogin] = useState<{ sessionId: string; qrcodeUrl: string; state: string; detail?: string } | null>(null);

  // editor draft (selected account)
  const [draftName, setDraftName] = useState('');
  const [draftToken, setDraftToken] = useState('');
  const [showToken, setShowToken] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testRes, setTestRes] = useState<{ ok: boolean; detail?: string; duration_ms?: number } | null>(null);
  const [busyAct, setBusyAct] = useState(false);
  const [qr, setQr] = useState<{ state: string; detail?: string; qrcode_url?: string; nick_name?: string } | null>(null);

  const sel = useMemo(() => accounts.find((a) => a.id === selId) || null, [accounts, selId]);

  const load = useCallback(async (keepId?: number) => {
    setLoading(true);
    try {
      const [accRes, plRes] = await Promise.all([apiService.getIMAccounts(), apiService.getIMPlatforms()]);
      const accs: IMAccount[] = Array.isArray(accRes.data?.accounts) ? accRes.data.accounts : [];
      setAccounts(accs);
      setPlatforms(Array.isArray(plRes.data?.platforms) ? plRes.data.platforms : []);
      const keep = keepId && accs.some((a) => a.id === keepId) ? keepId : (selId && accs.some((a) => a.id === selId) ? selId : accs[0]?.id ?? null);
      setSelId(keep);
    } catch (e) {
      toast(i18n.t('loadAccountsFailed', { ns: 'im', err: errText(e) }));
    } finally {
      setLoading(false);
    }
  }, [selId]);

  useEffect(() => { void load(); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    apiService.getPanes().then((r) => {
      const list: PaneOpt[] = (Array.isArray(r.data) ? r.data : []).map((p: any) => ({
        pane_id: shortPane(String(p.pane_id || p.id || '')),
        title: String(p.title || ''),
        agent_type: String(p.agent_type || ''),
      })).filter((p: PaneOpt) => p.pane_id);
      setPanes(list);
    }).catch(() => {});
  }, []);

  // sync editor when selection changes
  useEffect(() => {
    setTestRes(null);
    setShowToken(false);
    setHelpOpen(!!sel && sel.platform === 'telegram' && !sel.has_secret);
    setQr(null);
    if (sel) { setDraftName(sel.name || ''); setDraftToken(''); }
    else { setDraftName(''); setDraftToken(''); }
  }, [selId]); // eslint-disable-line react-hooks/exhaustive-deps

  // poll QR for wechat accounts not yet connected
  useEffect(() => {
    if (!sel || sel.platform !== 'wechat') return;
    let stop = false;
    const tick = async () => {
      try {
        const r = await apiService.getIMAccountQR(sel.id);
        if (stop) return;
        setQr(r.data);
        if (r.data?.state && r.data.state !== sel.state) {
          // refresh the account row so the list/state pill updates
          void load(sel.id);
        }
      } catch {}
    };
    void tick();
    if (sel.state === 'connected') return; // no need to poll once connected (still fetch once above)
    const iv = window.setInterval(tick, 2500);
    return () => { stop = true; window.clearInterval(iv); };
  }, [selId, sel?.state]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!addOpen) return;
    const onDoc = (e: MouseEvent) => { if (addRef.current && !addRef.current.contains(e.target as Node)) setAddOpen(false); };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [addOpen]);

  // poll the wechat add-via-scan session
  useEffect(() => {
    if (!wxLogin || wxLogin.state === 'created' || wxLogin.state === 'error' || wxLogin.state === 'expired') return;
    let stop = false;
    const tick = async () => {
      try {
        const r = await apiService.getWeChatLoginStatus(wxLogin.sessionId);
        if (stop) return;
        const d = r.data || {};
        if (d.state === 'created') {
          setWxLogin(null);
          toast(i18n.t('wechatAdded', { ns: 'im' }));
          await load(d.account_id);
          return;
        }
        setWxLogin((prev) => prev ? { ...prev, state: String(d.state || prev.state), qrcodeUrl: d.qrcode_url || prev.qrcodeUrl, detail: d.detail } : prev);
      } catch (e: any) {
        if (e?.response?.status === 404) { if (!stop) setWxLogin(null); }
      }
    };
    void tick();
    const iv = window.setInterval(tick, 2500);
    return () => { stop = true; window.clearInterval(iv); };
  }, [wxLogin?.sessionId, wxLogin?.state]); // eslint-disable-line react-hooks/exhaustive-deps

  /* ── mutations ── */
  const startWeChatScan = async () => {
    setAddOpen(false);
    try {
      const r = await apiService.startWeChatLogin();
      setWxLogin({ sessionId: r.data?.session_id, qrcodeUrl: r.data?.qrcode_url || '', state: r.data?.state || 'qr_wait', detail: r.data?.detail });
    } catch (e) { toast(i18n.t('wechatScanFailed', { ns: 'im', err: errText(e) })); }
  };
  const closeWeChatScan = () => {
    const sid = wxLogin?.sessionId;
    setWxLogin(null);
    if (sid) apiService.cancelWeChatLogin(sid).catch(() => {});
  };
  const regenWeChatScan = async () => {
    const sid = wxLogin?.sessionId;
    if (sid) { try { await apiService.cancelWeChatLogin(sid); } catch {} }
    try {
      const r = await apiService.startWeChatLogin();
      setWxLogin({ sessionId: r.data?.session_id, qrcodeUrl: r.data?.qrcode_url || '', state: r.data?.state || 'qr_wait', detail: r.data?.detail });
    } catch (e) { toast(i18n.t('wechatRescanFailed', { ns: 'im', err: errText(e) })); setWxLogin(null); }
  };
  const createAccount = async (platform: string) => {
    setAddOpen(false);
    if (platform === 'wechat') { void startWeChatScan(); return; }
    try {
      const r = await apiService.createIMAccount({ platform });
      const acc = r.data?.account;
      toast(i18n.t('accountAdded', { ns: 'im', platform: platformLabel(platform) }) + (platform === 'telegram' ? i18n.t('telegramBotTokenHint', { ns: 'im' }) : ''));
      await load(acc?.id);
    } catch (e) { toast(i18n.t('addFailed', { ns: 'im', err: errText(e) })); }
  };
  const patchSelected = async (patch: Record<string, any>, okMsg?: string) => {
    if (!sel) return;
    setBusyAct(true);
    try {
      await apiService.updateIMAccount(sel.id, patch);
      if (okMsg) toast(okMsg);
      await load(sel.id);
    } catch (e) { toast(i18n.t('updateFailed', { ns: 'im', err: errText(e) })); }
    finally { setBusyAct(false); }
  };
  const saveBasics = async () => {
    if (!sel) return;
    const patch: Record<string, any> = {};
    if (draftName.trim() && draftName.trim() !== sel.name) patch.name = draftName.trim();
    if (sel.platform === 'telegram' && draftToken.trim()) patch.secret = draftToken.trim();
    if (Object.keys(patch).length === 0) { toast(i18n.t('noChanges', { ns: 'im' })); return; }
    setSaving(true);
    try {
      await apiService.updateIMAccount(sel.id, patch);
      toast(i18n.t('saved', { ns: 'im' }));
      setDraftToken('');
      await load(sel.id);
    } catch (e) { toast(i18n.t('saveFailed', { ns: 'im', err: errText(e) })); }
    finally { setSaving(false); }
  };
  const removeAccount = async (id: number, name: string) => {
    const ok = await confirm({ title: i18n.t('confirmDeleteTitle', { ns: 'im' }), body: <>{i18n.t('confirmDeleteBodyPrefix', { ns: 'im' })} <span className="font-mono text-zinc-100">{name || '#' + id}</span>{i18n.t('confirmDeleteBodySuffix', { ns: 'im' })}</>, confirmLabel: i18n.t('deleteConfirm', { ns: 'im' }), danger: true });
    if (!ok) return;
    try {
      await apiService.deleteIMAccount(id);
      toast(i18n.t('deleted', { ns: 'im' }));
      if (selId === id) setSelId(null);
      await load();
    } catch (e) { toast(i18n.t('deleteFailed', { ns: 'im', err: errText(e) })); }
  };
  const runTest = async () => {
    if (!sel) return;
    setTesting(true); setTestRes(null);
    try { const r = await apiService.testIMAccount(sel.id); setTestRes(r.data); }
    catch (e) { setTestRes({ ok: false, detail: errText(e) }); }
    finally { setTesting(false); }
  };
  const bind = async (paneId: string) => {
    if (!sel) return;
    setBusyAct(true);
    try {
      if (paneId) await apiService.bindIMAccount(sel.id, paneId);
      else await apiService.unbindIMAccount(sel.id);
      toast(paneId ? i18n.t('boundTo', { ns: 'im', paneId }) : i18n.t('unbound', { ns: 'im' }));
      await load(sel.id);
    } catch (e) { toast(i18n.t('bindFailed', { ns: 'im', err: errText(e) })); }
    finally { setBusyAct(false); }
  };
  const relogin = async () => {
    if (!sel) return;
    setBusyAct(true);
    try { await apiService.reloginIMAccount(sel.id); toast(i18n.t('reloginStarted', { ns: 'im' })); await load(sel.id); }
    catch (e) { toast(i18n.t('operationFailed', { ns: 'im', err: errText(e) })); }
    finally { setBusyAct(false); }
  };

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return accounts;
    return accounts.filter((a) => `${a.name} ${a.platform} ${a.config?.bot_username || ''} ${a.bound_pane_id}`.toLowerCase().includes(q));
  }, [accounts, query]);

  const chatId = sel?.platform === 'telegram' ? String(sel?.config?.chat_id || '').trim() : '';
  const botUser = sel?.config?.bot_username ? String(sel.config.bot_username) : '';
  const tgHelp = platforms.find((p) => p.kind === 'telegram')?.help || {};
  const canTest = sel ? (sel.platform === 'telegram' ? !!chatId : sel.state === 'connected') : false;
  const wechatConnected = sel?.platform === 'wechat' && (qr?.state === 'connected' || sel.state === 'connected');
  const qrUrl = qr?.qrcode_url || (sel?.config?.qrcode_url ? String(sel.config.qrcode_url) : '');
  const wechatAddBlocked = wxLogin !== null || accounts.some((a) => a.platform === 'wechat' && a.state !== 'connected');
  const wechatAddHint = wxLogin ? i18n.t('wechatInProgress', { ns: 'im' }) : i18n.t('wechatPendingLogin', { ns: 'im' });

  return (
    <div className="flex h-screen flex-col bg-[#0A0A0A] text-zinc-300">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-white/[0.06] px-4">
        {onBack && (
          <button onClick={onBack} className="-ml-1 grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.05] hover:text-zinc-200" title={t('back')}><ArrowLeft size={16} /></button>
        )}
        <MessageCircle size={16} className="text-zinc-400" />
        <span className="text-[13px] font-semibold text-white">{t('imPlatforms')}</span>
        <div className="flex-1" />
        <span className="text-xs text-zinc-600 tabular-nums">{t('accountsCount', { n: accounts.length })}</span>
        <button onClick={() => void load(selId ?? undefined)} className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.05] hover:text-zinc-200" title={t('refresh')}><RefreshCw size={14} className={loading ? 'animate-spin' : ''} /></button>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {/* rail */}
        <aside className="flex w-72 shrink-0 flex-col border-r border-white/[0.06] bg-white/[0.01]">
          <div className="p-2.5">
            <div ref={addRef} className="relative">
              <Btn variant="primary" icon={<Plus size={14} />} className="w-full justify-between" onClick={() => setAddOpen((o) => !o)}>
                <span>{t('addIMAccount')}</span><ChevronDown size={13} className="opacity-60" />
              </Btn>
              {addOpen && (
                <div className="absolute left-0 right-0 z-50 mt-1.5 rounded-xl border border-white/[0.09] bg-[#141416] p-1 shadow-2xl shadow-black/60">
                  {(platforms.length ? platforms : [{ kind: 'telegram', label: 'Telegram' }, { kind: 'wechat', label: t('platformWechat') }]).map((p) => {
                    const disabled = p.kind === 'wechat' && wechatAddBlocked;
                    return (
                      <button key={p.kind} disabled={disabled} title={disabled ? wechatAddHint : undefined}
                        onClick={() => { if (disabled) return; void createAccount(p.kind); }}
                        className={cn('flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] transition-colors', disabled ? 'cursor-not-allowed opacity-40' : 'hover:bg-white/[0.06]')}>
                        <PlatformIcon platform={p.kind} />{p.label}{p.kind === 'wechat' && <span className="ml-auto text-[10px] text-zinc-600">{disabled ? t('notAddable') : t('scanLogin')}</span>}
                      </button>
                    );
                  })}
                  <div className="px-2.5 py-1 text-[10px] text-zinc-600">{t('discordSoon')}</div>
                </div>
              )}
            </div>
            <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('searchAccount')} className={cn(INPUT, 'mt-2.5 h-8 text-[12px]')} />
          </div>
          <div className="flex-1 overflow-auto px-2 pb-2">
            {loading && accounts.length === 0 && [0, 1, 2].map((i) => <div key={i} className="px-2 py-2"><Skeleton className="h-3 w-28" /><Skeleton className="mt-1.5 h-2.5 w-20" /></div>)}
            {!loading && accounts.length === 0 && <div className="px-3 py-10 text-center text-[12px] leading-relaxed text-zinc-600">{t('noAccountHeadline')}<br />{t('addIMAccount')}</div>}
            {!loading && accounts.length > 0 && filtered.length === 0 && <div className="px-3 py-10 text-center text-[12px] text-zinc-600">{t('noMatch')}</div>}
            <ul className="space-y-0.5">
              {filtered.map((a) => {
                const active = selId === a.id;
                return (
                  <li key={a.id}>
                    <button onClick={() => setSelId(a.id)} className={cn('group relative flex w-full items-center gap-2.5 rounded-lg py-2 pl-3 pr-2 text-left transition-colors', active ? 'bg-white/[0.05]' : 'hover:bg-white/[0.03]')}>
                      {active && <span className="absolute inset-y-2 left-0 w-0.5 rounded-r bg-blue-400" />}
                      <PlatformIcon platform={a.platform} size={15} />
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5">
                          <span className={cn('truncate text-[13px]', active ? 'text-white' : 'text-zinc-200')}>{a.name || platformLabel(a.platform)}</span>
                          {!a.enabled && <span className="rounded bg-white/[0.06] px-1 py-px text-[9px] text-zinc-500">{t('disabledTag')}</span>}
                        </div>
                        <div className="mt-0.5 flex items-center gap-1.5">
                          <span className={cn('h-1.5 w-1.5 rounded-full', stateTone(a.state).tone)} />
                          <span className="truncate text-[11px] text-zinc-600">{stateTone(a.state).label}</span>
                          {a.bound_pane_id && <span className="shrink-0 rounded bg-blue-500/12 px-1 py-px text-[9px] font-medium text-blue-300">→ {a.bound_pane_id}</span>}
                        </div>
                      </div>
                      <span role="button" tabIndex={-1} onClick={(e) => { e.stopPropagation(); removeAccount(a.id, a.name); }} className="grid h-6 w-6 shrink-0 place-items-center rounded text-zinc-600 opacity-0 transition-all hover:bg-red-500/15 hover:text-red-300 group-hover:opacity-100" title={t('delete')}><Trash2 size={12} /></span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        </aside>

        {/* detail */}
        <main className="flex-1 overflow-auto">
          {!sel ? (
            <div className="flex h-full flex-col items-center justify-center px-6 text-center">
              <div className="grid h-12 w-12 place-items-center rounded-xl bg-white/[0.04]"><MessageCircle size={22} className="text-zinc-500" /></div>
              <div className="mt-3 text-[14px] font-medium text-zinc-200">{loading ? t('loadingText') : accounts.length === 0 ? t('noAccountHeadline') : t('selectAccount')}</div>
              <div className="mt-1 text-[12px] text-zinc-500">{t('emptyHelp')}</div>
              {accounts.length === 0 && (
                <div className="mt-4 flex gap-2">
                  <Btn variant="secondary" icon={<Send size={14} />} onClick={() => void createAccount('telegram')}>{t('addTelegram')}</Btn>
                  <Btn variant="secondary" icon={<MessageCircle size={14} />} disabled={wechatAddBlocked} onClick={() => { if (!wechatAddBlocked) void createAccount('wechat'); }}>{t('addWeChat')}</Btn>
                </div>
              )}
            </div>
          ) : (
            <div className="mx-auto max-w-[680px] px-8 py-7">
              <div className="flex items-center gap-2.5">
                <PlatformIcon platform={sel.platform} size={17} />
                <h1 className="truncate text-[17px] font-semibold tracking-tight text-white">{sel.name || platformLabel(sel.platform)}</h1>
                <StatusPill state={qr?.state || sel.state} />
                <div className="flex-1" />
                <Btn variant="danger" icon={<Trash2 size={12} />} className="!h-7 !px-2.5 !text-[12px]" onClick={() => removeAccount(sel.id, sel.name)}>{t('delete')}</Btn>
              </div>
              {sel.state_detail && <div className="mt-1 text-[11px] text-zinc-600">{sel.state_detail}</div>}
              <div className="my-5 h-px bg-white/[0.06]" />

              <div className="space-y-7">
                {/* ── Telegram ── */}
                {sel.platform === 'telegram' && (
                  <>
                    <section className="space-y-3.5">
                      <SectionHeader>{t('sectionConnection')}</SectionHeader>
                      <Field label={t('fieldName')}><input value={draftName} onChange={(e) => setDraftName(e.target.value)} className={INPUT} placeholder="My Bot" /></Field>
                      <Field label={t('fieldBotToken')} help={sel.has_secret ? t('botTokenSet', { tail: sel.secret_tail }) : t('botTokenHint')}>
                        <div className="relative">
                          <Link2 size={13} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600" />
                          <input type="text" name="cicy-im-tg-token" value={draftToken} onChange={(e) => setDraftToken(e.target.value)} className={cn(INPUT, 'pl-8 pr-9 font-mono')} placeholder={sel.has_secret ? '••••••••' : '123456:ABC-...'} autoComplete="off" spellCheck={false} data-1p-ignore data-lpignore="true" style={showToken ? undefined : ({ WebkitTextSecurity: 'disc' } as React.CSSProperties)} />
                          <button type="button" onClick={() => setShowToken((s) => !s)} className="absolute right-1.5 top-1/2 grid h-6 w-6 -translate-y-1/2 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300">{showToken ? <EyeOff size={13} /> : <Eye size={13} />}</button>
                        </div>
                      </Field>
                      <div className="flex items-center justify-between">
                        <button onClick={() => setHelpOpen((o) => !o)} className="flex items-center gap-1 text-[11px] text-zinc-500 transition-colors hover:text-zinc-300"><ChevronDown size={12} className={cn('transition-transform', helpOpen && 'rotate-180')} />{tgHelp.title || t('getBotToken')}</button>
                        <a href={tgHelp.link || 'https://t.me/BotFather'} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 rounded-lg border border-sky-500/25 bg-sky-500/[0.08] px-2.5 py-1 text-[12px] text-sky-300 transition-colors hover:bg-sky-500/[0.16]"><ExternalLink size={12} /> {t('openBotFather')}</a>
                      </div>
                      {helpOpen && (
                        <div className="rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-2.5 text-[12px] leading-relaxed text-zinc-400">
                          {tgHelp.steps || t('botFatherSteps')}
                        </div>
                      )}
                    </section>

                    <section className="space-y-3">
                      <SectionHeader>{t('sectionChatBinding')}</SectionHeader>
                      {chatId ? (
                        <div className="flex items-center gap-2 rounded-lg border border-emerald-500/20 bg-emerald-500/[0.06] px-3 py-2.5 text-[12px] text-emerald-200">
                          <Check size={14} className="text-emerald-400" /> {t('chatBoundTo')} <span className="font-mono">{chatId}</span>
                        </div>
                      ) : (
                        <div className="flex items-center gap-2 rounded-lg border border-white/[0.07] bg-white/[0.02] px-3 py-2.5 text-[12px] text-zinc-400">
                          <QrCode size={14} className="text-zinc-500" /> {t('sendMessageToBind', { bot: '' })}{botUser ? <span className="font-mono text-zinc-300">@{botUser}</span> : t('thisBot')}
                          <div className="flex-1" />
                          <button onClick={() => void load(sel.id)} className="grid h-6 w-6 place-items-center rounded text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-300" title={t('refreshStatus')}><RefreshCw size={13} /></button>
                        </div>
                      )}
                    </section>
                  </>
                )}

                {/* ── WeChat ── */}
                {sel.platform === 'wechat' && (
                  <section className="space-y-3.5">
                    <SectionHeader>{t('sectionLogin')}</SectionHeader>
                    {wechatConnected ? (
                      <div className="flex items-center gap-3 rounded-lg border border-emerald-500/20 bg-emerald-500/[0.06] px-4 py-3 text-[13px] text-emerald-200">
                        <Check size={16} className="text-emerald-400" />
                        <div className="min-w-0 flex-1">
                          <div className="font-medium">{t('loggedIn')}</div>
                          <div className="text-[11px] text-emerald-300/70">{qr?.nick_name || sel.config?.ilink_user_id || ''}</div>
                        </div>
                        <Btn variant="secondary" busy={busyAct} className="!h-7 !px-2.5 !text-[12px]" onClick={relogin}>{t('logoutRelogin')}</Btn>
                      </div>
                    ) : (
                      <div className="rounded-lg border border-white/[0.07] bg-white/[0.02] p-4">
                        <div className="flex items-center gap-2 text-[12px] text-zinc-400"><QrCode size={14} className="text-zinc-500" />{t('wechatScanHint')}{qr?.state ? ` · ${stateTone(qr.state).label}` : ''}</div>
                        <div className="mt-3 flex items-center gap-4">
                          {qrUrl ? (
                            <img src={`https://api.qrserver.com/v1/create-qr-code/?size=220x220&margin=8&data=${encodeURIComponent(qrUrl)}`} alt="WeChat login QR" className="h-[180px] w-[180px] rounded-lg bg-white p-1" />
                          ) : (
                            <div className="grid h-[180px] w-[180px] place-items-center rounded-lg border border-dashed border-white/[0.1] text-[12px] text-zinc-600"><Loader2 size={18} className="animate-spin" /></div>
                          )}
                          <div className="space-y-2 text-[12px] text-zinc-500">
                            <p>{t('wechatStep1')}</p>
                            <p>{t('wechatStep2')}</p>
                            <p>{t('wechatStep3')}</p>
                            <Btn variant="secondary" busy={busyAct} icon={<RefreshCw size={13} />} className="!h-7 !px-2.5 !text-[12px]" onClick={relogin}>{t('regenerateQr')}</Btn>
                            {qrUrl && <div className="break-all text-[10px] text-zinc-700">{qrUrl}</div>}
                          </div>
                        </div>
                      </div>
                    )}
                  </section>
                )}

                {/* Agent binding */}
                <section className="space-y-3.5">
                  <SectionHeader>{t('sectionAgentBinding')}</SectionHeader>
                  <Field label={t('fieldBindAgent')} help={t('fieldBindAgentHelp')}>
                    <select value={sel.bound_pane_id} onChange={(e) => void bind(e.target.value)} className={INPUT} disabled={busyAct}>
                      <option value="">{t('unboundOption')}</option>
                      {panes.map((p) => <option key={p.pane_id} value={p.pane_id}>{p.pane_id}{p.title ? ` · ${p.title}` : ''}{p.agent_type ? ` (${p.agent_type})` : ''}</option>)}
                      {sel.bound_pane_id && !panes.some((p) => p.pane_id === sel.bound_pane_id) && <option value={sel.bound_pane_id}>{sel.bound_pane_id}</option>}
                    </select>
                  </Field>
                  <label className="flex items-center justify-between rounded-lg border border-white/[0.07] bg-white/[0.02] px-3 py-2.5">
                    <span className="text-[12px] text-zinc-300">{t('forwardIncoming')}<span className="ml-2 text-[11px] text-zinc-600">{t('forwardOff')}</span></span>
                    <input type="checkbox" checked={sel.inbound_to_agent} onChange={(e) => void patchSelected({ inbound_to_agent: e.target.checked })} className="h-4 w-4 accent-blue-500" />
                  </label>
                  <label className="flex items-center justify-between rounded-lg border border-white/[0.07] bg-white/[0.02] px-3 py-2.5">
                    <span className="text-[12px] text-zinc-300">{t('enabledLabel')}<span className="ml-2 text-[11px] text-zinc-600">{t('disabledOff')}</span></span>
                    <input type="checkbox" checked={sel.enabled} onChange={(e) => void patchSelected({ enabled: e.target.checked })} className="h-4 w-4 accent-blue-500" />
                  </label>
                </section>

                {testRes && (
                  <div className={cn('relative rounded-xl border px-3.5 py-3 text-[12px]', testRes.ok ? 'border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-200' : 'border-red-500/25 bg-red-500/[0.07] text-red-200')}>
                    <button onClick={() => setTestRes(null)} className="absolute right-2.5 top-2.5 opacity-50 hover:opacity-100"><X size={12} /></button>
                    <div className="flex items-center gap-1.5 pr-5 font-medium">{testRes.ok ? <Check size={13} /> : <X size={13} />}{testRes.ok ? t('testMessageSent') : t('sendFailed')}{typeof testRes.duration_ms === 'number' && <span className="font-normal opacity-70">· {testRes.duration_ms} ms</span>}</div>
                    {testRes.detail && testRes.detail !== 'ok' && <div className="mt-1 whitespace-pre-wrap break-all opacity-80">{testRes.detail}</div>}
                  </div>
                )}
              </div>

              {/* footer */}
              <div className="mt-7 flex items-center gap-2 border-t border-white/[0.06] pt-5">
                {(sel.platform === 'telegram' || draftName.trim() !== sel.name) && (
                  <Btn variant="primary" icon={<Save size={14} />} busy={saving} disabled={saving} onClick={() => void saveBasics()}>{t('save')}</Btn>
                )}
                <Btn variant="secondary" icon={<Zap size={14} />} busy={testing} disabled={testing || !canTest} onClick={() => void runTest()}>{sel.platform === 'wechat' ? t('testSendWeChat') : t('testSend')}</Btn>
                <div className="flex-1" />
                <span className="text-[11px] text-zinc-600">{t('agentReplyHint')}</span>
              </div>
            </div>
          )}
        </main>
      </div>
      {wxLogin && createPortal(
        <div className="fixed inset-0 z-[9999990] flex items-center justify-center bg-black/55 backdrop-blur-sm" onMouseDown={(e) => { if (e.target === e.currentTarget) closeWeChatScan(); }}>
          <div className="mx-4 w-full max-w-[420px] rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60">
            <div className="flex items-center gap-2.5 px-5 pt-5">
              <MessageCircle size={16} className="text-emerald-400" />
              <div className="text-[14px] font-semibold text-white">{t('addWeChat')}</div>
              <div className="flex-1" />
              <button onClick={closeWeChatScan} className="grid h-6 w-6 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300"><X size={14} /></button>
            </div>
            <div className="px-5 pb-5 pt-3">
              {(wxLogin.state === 'error' || wxLogin.state === 'expired') ? (
                <div className="flex flex-col items-center gap-3 py-4 text-center">
                  <div className="text-[13px] text-red-300">{wxLogin.detail || (wxLogin.state === 'expired' ? t('qrExpired') : t('scanFailed'))}</div>
                  <Btn variant="secondary" icon={<RefreshCw size={14} />} onClick={() => void regenWeChatScan()}>{t('regenerateQr')}</Btn>
                </div>
              ) : (
                <div className="flex flex-col items-center gap-3">
                  {wxLogin.qrcodeUrl ? (
                    <img src={`https://api.qrserver.com/v1/create-qr-code/?size=220x220&margin=8&data=${encodeURIComponent(wxLogin.qrcodeUrl)}`} alt="WeChat login QR" className="h-[200px] w-[200px] rounded-lg bg-white p-1" />
                  ) : (
                    <div className="grid h-[200px] w-[200px] place-items-center rounded-lg border border-dashed border-white/[0.1] text-[12px] text-zinc-600"><Loader2 size={18} className="animate-spin" /></div>
                  )}
                  <div className="text-[13px] text-zinc-300">{wxLogin.detail || (wxLogin.state === 'scaned' ? t('alreadyScanned') : t('scanWithWeChat'))}</div>
                  <div className="text-[11px] text-zinc-600">{t('scanSuccessNote')}</div>
                  <div className="flex gap-2">
                    <Btn variant="ghost" icon={<RefreshCw size={13} />} className="!h-7 !px-2.5 !text-[12px]" onClick={() => void regenWeChatScan()}>{t('regenerateAnotherQr')}</Btn>
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>,
        document.body,
      )}
      {dialogsNode}
    </div>
  );
}
