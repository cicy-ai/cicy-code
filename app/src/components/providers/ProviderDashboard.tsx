import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import {
  ArrowLeft, Boxes, Plus, RefreshCw, Save, Trash2, Zap, Eye, EyeOff, Check, X,
  Server, Search, ChevronsUpDown, AlertTriangle, Link2, Cpu,
} from 'lucide-react';
import apiService from '../../services/api';
import AgentAvatar from '../AgentAvatar';
import { useDialogs } from '../ui/Modal';
import { Spinner } from '../ui/Spinner';
import Select from '../ui/Select';

/* ───────────────────────── types ───────────────────────── */

interface ProviderRecord {
  key: string;
  name?: string;
  url?: string;
  apiKey?: string;
  protocol?: string;
  defaultModel?: string;
  defaultModels?: Record<string, string>;
  models?: string[];
  modelMapping?: Record<string, string>;
}

interface ProvidersResponse {
  defaults: Record<string, string>;
  items: ProviderRecord[];
  agent_type_slots: string[];
  source?: string;
  source_path?: string;
}

interface TestResult {
  ok: boolean;
  status?: number;
  duration_ms?: number;
  endpoint?: string;
  detail?: string;
  model?: string;
}

type Tab = 'routing' | 'providers';

/* ───────────────────────── constants ───────────────────────── */

const PROTOCOLS = ['openai', 'anthropic'] as const;
const KNOWN_SLOTS = ['claude', 'codex', 'opencode'];
const PROTECTED_PROVIDER_KEYS = new Set(['defaultAnthropic', 'defaultOpenAi']);
const SLOT_LABELS: Record<string, string> = { claude: 'Claude', codex: 'Codex', opencode: 'OpenCode' };
const SLOT_DESC: Record<string, string> = { claude: 'Claude Code CLI', codex: 'OpenAI Codex CLI', opencode: 'OpenCode CLI' };
const SLOT_PROTOCOL: Record<string, string> = { claude: 'anthropic', codex: 'openai', opencode: 'openai' };
const SLOT_FALLBACK_MODEL: Record<string, string> = { claude: 'claude-opus-4-7', codex: 'gpt-5.4', opencode: 'deepseek-v4-pro' };

/* ───────────────────────── helpers ───────────────────────── */

function cn(...parts: Array<string | false | null | undefined>) {
  return parts.filter(Boolean).join(' ');
}
function toast(message: string) {
  window.dispatchEvent(new CustomEvent('show-toast', { detail: message }));
}
function errText(err: any): string {
  return String(err?.response?.data?.detail || err?.message || err || i18n.t('errorUnknown', { ns: 'provider' }));
}
function emptyDraft(): ProviderRecord {
  return { key: '', name: '', url: '', apiKey: '', protocol: 'openai', defaultModel: '', defaultModels: {}, models: [], modelMapping: {} };
}
function proto(p?: ProviderRecord | string | null): string {
  if (!p) return '';
  return String(typeof p === 'string' ? p : p.protocol || '').toLowerCase();
}
function resolveModelFor(p: ProviderRecord | null | undefined, agentType: string): string {
  if (!p) return '';
  return String((p.defaultModels || {})[agentType] || p.defaultModel || '').trim();
}
function compactDM(dm?: Record<string, string>): Record<string, string> {
  return Object.fromEntries(Object.entries(dm || {}).filter(([, v]) => String(v || '').trim()).map(([k, v]) => [k, String(v).trim()]));
}
function editorSnapshot(d: ProviderRecord, modelsText: string): string {
  return JSON.stringify({
    key: d.key.trim(),
    name: (d.name || '').trim(),
    url: (d.url || '').trim().replace(/\/+$/, ''),
    apiKey: d.apiKey || '',
    protocol: proto(d) || 'openai',
    defaultModel: (d.defaultModel || '').trim(),
    defaultModels: compactDM(d.defaultModels),
    models: modelsText.split('\n').map((s) => s.trim()).filter(Boolean),
    modelMapping: d.modelMapping || {},
  });
}

/* ───────────────────────── design atoms ───────────────────────── */

const INPUT = 'h-9 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors hover:border-white/[0.14] focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 disabled:opacity-50';
const CARD = 'rounded-2xl border border-white/[0.07] bg-white/[0.015]';

function StatusDot({ tone }: { tone: 'ok' | 'warn' | 'off' }) {
  const c = tone === 'ok' ? 'bg-emerald-400' : tone === 'warn' ? 'bg-amber-400' : 'bg-zinc-600';
  return <span className={cn('inline-block h-1.5 w-1.5 rounded-full', c)} />;
}

function ProtocolBadge({ protocol: p, dim }: { protocol?: string; dim?: boolean }) {
  const v = proto(p);
  const cls = v === 'anthropic' ? 'bg-amber-500/12 text-amber-300'
    : v === 'openai' ? 'bg-emerald-500/12 text-emerald-300'
      : 'bg-white/[0.06] text-zinc-400';
  return <span className={cn('inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium leading-none', cls, dim && 'opacity-50')}>{v || i18n.t('protoUnspecified', { ns: 'provider' })}</span>;
}

function SectionHeader({ children }: { children: React.ReactNode }) {
  return <div className="text-[11px] font-semibold uppercase tracking-[0.13em] text-zinc-500">{children}</div>;
}

function Field({ label, help, children, className }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <label data-id="provider-dashboard-auto-1" className={cn('block', className)}>
      <span data-id="provider-dashboard-auto-2" className="mb-1.5 block text-[12px] font-medium text-zinc-400">{label}</span>
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
    <button data-id="provider-dashboard-auto-3" {...rest} className={cn('inline-flex items-center justify-center rounded-lg transition-colors disabled:cursor-not-allowed', sizes[size], styles[variant], className)}>
      {busy ? <Spinner size={size === 'sm' ? 'xs' : 'sm'} /> : icon}
      {children}
    </button>
  );
}

function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-md bg-white/[0.045]', className)} />;
}

/* ───────────────────────── provider picker ───────────────────────── */

function ProviderPicker({
  value, options, restrictToProtocol, busy, disabled, emptyHint, onChange,
}: {
  value: string;
  options: ProviderRecord[];
  restrictToProtocol?: string;
  busy?: boolean;
  disabled?: boolean;
  emptyHint?: string;
  onChange: (key: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const selected = options.find((p) => p.key === value) || null;
  const selectedMismatch = !!selected && !!restrictToProtocol && proto(selected) !== restrictToProtocol;
  const compatible = useMemo(() => restrictToProtocol ? options.filter((p) => proto(p) === restrictToProtocol) : options, [options, restrictToProtocol]);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); setOpen(false); } };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey, true);
    return () => { document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey, true); };
  }, [open]);

  const pick = (k: string) => { onChange(k); setOpen(false); };

  return (
    <div data-id="provider-dashboard-auto-4" ref={ref} className="relative">
      <button data-id="provider-dashboard-auto-5"
        type="button" disabled={disabled} onClick={() => setOpen((o) => !o)}
        className={cn('group flex h-9 w-full items-center gap-2 rounded-lg border bg-white/[0.025] pl-3 pr-2 text-left text-[13px] transition-colors',
          open ? 'border-blue-500/55 ring-1 ring-blue-500/15' : 'border-white/[0.09] hover:border-white/[0.16]',
          disabled && 'cursor-not-allowed opacity-50')}
      >
        {selected ? (
          <>
            <span data-id="provider-dashboard-auto-6" className={cn('truncate', selectedMismatch ? 'text-amber-300' : 'text-zinc-100')}>{selected.name || selected.key}</span>
            <ProtocolBadge protocol={selected.protocol} />
            {selectedMismatch && <AlertTriangle size={12} className="shrink-0 text-amber-400" />}
          </>
        ) : (
          <span data-id="provider-dashboard-auto-7" className="text-zinc-600">{i18n.t('useFallback', { ns: 'provider' })}</span>
        )}
        <span data-id="provider-dashboard-auto-8" className="ml-auto flex shrink-0 items-center gap-1.5">
          {busy && <Spinner size="xs" />}
          <ChevronsUpDown size={13} className="text-zinc-600 transition-colors group-hover:text-zinc-400" />
        </span>
      </button>

      {open && (
        <div data-id="provider-dashboard-auto-9" className="absolute left-0 right-0 z-50 mt-1.5 max-h-72 overflow-auto rounded-xl border border-white/[0.09] bg-[#141416] p-1 shadow-2xl shadow-black/60">
          {restrictToProtocol && (
            <div data-id="provider-dashboard-auto-10" className="px-2.5 pb-1 pt-1.5 text-[10px] uppercase tracking-[0.1em] text-zinc-600">{i18n.t('restrictByProtocol', { ns: 'provider', protocol: restrictToProtocol })}</div>
          )}
          <button data-id="provider-dashboard-auto-11" type="button" onClick={() => pick('')}
            className={cn('flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] transition-colors hover:bg-white/[0.06]', !value && 'bg-white/[0.07]')}>
            <span data-id="provider-dashboard-auto-12" className="grid w-4 place-items-center">{!value && <Check size={13} className="text-blue-400" />}</span>
            <span data-id="provider-dashboard-auto-13" className="text-zinc-500">{i18n.t('useFallback', { ns: 'provider' })}</span>
          </button>
          {compatible.length > 0 && <div className="my-1 h-px bg-white/[0.06]" />}
          {compatible.map((p) => {
            const isSel = p.key === value;
            return (
              <button data-id="provider-dashboard-auto-14" key={p.key} type="button" onClick={() => pick(p.key)}
                className={cn('flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-[13px] transition-colors hover:bg-white/[0.06]', isSel && 'bg-white/[0.07]')}>
                <span data-id="provider-dashboard-auto-15" className="grid w-4 shrink-0 place-items-center">{isSel && <Check size={13} className="text-blue-400" />}</span>
                <span data-id="provider-dashboard-auto-16" className="min-w-0 flex-1">
                  <span data-id="provider-dashboard-auto-17" className="flex items-center gap-1.5">
                    <span data-id="provider-dashboard-auto-18" className="truncate text-zinc-100">{p.name || p.key}</span>
                    <ProtocolBadge protocol={p.protocol} />
                  </span>
                  {p.name && p.name !== p.key && <span className="block truncate font-mono text-[11px] text-zinc-600">{p.key}</span>}
                </span>
              </button>
            );
          })}
          {compatible.length === 0 && <div className="px-2.5 py-3 text-center text-[12px] text-zinc-600">{emptyHint || i18n.t('noProviders', { ns: 'provider' })}</div>}
          {selectedMismatch && selected && (
            <>
              <div data-id="provider-dashboard-auto-19" className="my-1 h-px bg-white/[0.06]" />
              <button data-id="provider-dashboard-auto-20" type="button" onClick={() => pick(selected.key)}
                className="flex w-full items-center gap-2.5 rounded-lg bg-amber-500/[0.06] px-2.5 py-1.5 text-left text-[13px] transition-colors hover:bg-amber-500/[0.12]">
                <span data-id="provider-dashboard-auto-21" className="grid w-4 shrink-0 place-items-center"><Check size={13} className="text-amber-400" /></span>
                <span data-id="provider-dashboard-auto-22" className="min-w-0 flex-1">
                  <span data-id="provider-dashboard-auto-23" className="flex items-center gap-1.5">
                    <span data-id="provider-dashboard-auto-24" className="truncate text-amber-200">{selected.name || selected.key}</span>
                    <ProtocolBadge protocol={selected.protocol} />
                    <span data-id="provider-dashboard-auto-25" className="rounded bg-amber-500/15 px-1 py-px text-[9px] font-medium text-amber-300">{i18n.t('protoMismatchPill', { ns: 'provider' })}</span>
                  </span>
                  <span data-id="provider-dashboard-auto-26" className="block truncate font-mono text-[11px] text-zinc-600">{selected.key}</span>
                </span>
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}

/* ───────────────────────── component ───────────────────────── */

export default function ProviderDashboard({ onBack }: { onBack?: () => void }) {
  const { t } = useTranslation('provider');
  const { confirm, node: dialogsNode } = useDialogs();
  const [tab, setTab] = useState<Tab>('routing');
  const [loading, setLoading] = useState(true);
  const [data, setData] = useState<ProvidersResponse | null>(null);

  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [draft, setDraft] = useState<ProviderRecord>(emptyDraft());
  const [modelsText, setModelsText] = useState('');
  const [baseline, setBaseline] = useState('');
  const [showApiKey, setShowApiKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [testModalOpen, setTestModalOpen] = useState(false);
  const [testPickedModel, setTestPickedModel] = useState<string>('');
  const [modelTestResults, setModelTestResults] = useState<Record<string, TestResult | 'pending'>>({});
  const [savingSlot, setSavingSlot] = useState<string | null>(null);
  const [query, setQuery] = useState('');

  const selectedProvider = useMemo<ProviderRecord | null>(() => {
    if (isNew || !selectedKey || !data) return null;
    return data.items.find((p) => p.key === selectedKey) || null;
  }, [isNew, selectedKey, data]);

  /* ---- editor plumbing ---- */
  const loadEditor = useCallback((p: ProviderRecord | null) => {
    setTestResult(null);
    setShowApiKey(false);
    const next: ProviderRecord = p ? {
      key: p.key, name: p.name || '', url: p.url || '', apiKey: p.apiKey || '',
      protocol: proto(p) || 'openai', defaultModel: p.defaultModel || '',
      defaultModels: { ...(p.defaultModels || {}) }, models: [...(p.models || [])], modelMapping: { ...(p.modelMapping || {}) },
    } : emptyDraft();
    const mt = (next.models || []).join('\n');
    setDraft(next); setModelsText(mt); setBaseline(editorSnapshot(next, mt));
  }, []);

  const load = useCallback(async (preserve?: string): Promise<ProvidersResponse | null> => {
    setLoading(true);
    try {
      const resp = await apiService.getProviders();
      const rawSlots: string[] = Array.isArray(resp.data?.agent_type_slots) ? resp.data.agent_type_slots : [];
      const slots = rawSlots.map((s) => String(s || '').trim()).filter(Boolean);
      const payload: ProvidersResponse = {
        defaults: resp.data?.defaults || {},
        items: Array.isArray(resp.data?.items) ? resp.data.items : [],
        agent_type_slots: slots.length ? slots : KNOWN_SLOTS,
        source: resp.data?.source, source_path: resp.data?.source_path,
      };
      setData(payload);
      const keep = preserve && payload.items.some((p) => p.key === preserve) ? preserve : null;
      if (keep) { setIsNew(false); setSelectedKey(keep); }
      else if (!isNew && (!selectedKey || !payload.items.some((p) => p.key === selectedKey))) {
        const first = payload.items[0]?.key || null;
        setSelectedKey(first);
        if (!first) loadEditor(null);
      }
      return payload;
    } catch (err) {
      toast(i18n.t('loadProvidersFailed', { ns: 'provider', err: errText(err) }));
      return null;
    } finally {
      setLoading(false);
    }
  }, [isNew, selectedKey, loadEditor]);

  useEffect(() => { void load(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (isNew || !selectedKey) { loadEditor(isNew ? null : (selectedKey ? null : null)); return; }
    loadEditor(data?.items.find((p) => p.key === selectedKey) || null);
  }, [isNew, selectedKey]); // eslint-disable-line react-hooks/exhaustive-deps

  /* ---- dirty ---- */
  const snapshot = useMemo(() => editorSnapshot(draft, modelsText), [draft, modelsText]);
  const dirty = snapshot !== baseline;
  const canSave = (isNew || dirty) && !!draft.key.trim() && !!(draft.url || '').trim() && (proto(draft) === 'openai' || proto(draft) === 'anthropic');

  const patchDraft = (patch: Partial<ProviderRecord>) => setDraft((prev) => ({ ...prev, ...patch }));

  const guardDirty = async (action: () => void) => {
    if (!dirty) { action(); return; }
    if (await confirm({ title: i18n.t('confirmDiscardTitle', { ns: 'provider' }), body: i18n.t('confirmDiscardBody', { ns: 'provider' }), confirmLabel: i18n.t('confirmDiscardConfirm', { ns: 'provider' }), danger: true })) action();
  };
  const selectProvider = (key: string) => { void guardDirty(() => { setIsNew(false); setSelectedKey(key); }); };
  const startNew = () => { void guardDirty(() => { setIsNew(true); setSelectedKey(null); }); };

  /* ---- mutations ---- */
  const buildPayload = useCallback((): ProviderRecord => ({
    key: draft.key.trim(),
    name: (draft.name || '').trim() || draft.key.trim(),
    url: (draft.url || '').trim().replace(/\/+$/, ''),
    apiKey: (draft.apiKey || '').trim(),
    protocol: proto(draft) || 'openai',
    defaultModel: (draft.defaultModel || '').trim(),
    defaultModels: compactDM(draft.defaultModels),
    models: modelsText.split('\n').map((s) => s.trim()).filter(Boolean),
    modelMapping: draft.modelMapping || {},
  }), [draft, modelsText]);

  const save = useCallback(async () => {
    const payload = buildPayload();
    if (!payload.key) { toast(i18n.t('errorKeyRequired', { ns: 'provider' })); return; }
    if (!payload.url) { toast(i18n.t('errorUrlRequired', { ns: 'provider' })); return; }
    if (payload.protocol !== 'openai' && payload.protocol !== 'anthropic') { toast(i18n.t('errorProtocolRequired', { ns: 'provider' })); return; }
    setSaving(true);
    try {
      if (isNew) { await apiService.createProvider(payload); toast(i18n.t('providerCreated', { ns: 'provider', key: payload.key })); setIsNew(false); }
      else { const { key, ...rest } = payload; await apiService.updateProvider(key, rest); toast(i18n.t('providerSaved', { ns: 'provider', key })); }
      const fresh = await load(payload.key);
      loadEditor(fresh?.items.find((p) => p.key === payload.key) || null);
    } catch (err) { toast(i18n.t('saveFailed', { ns: 'provider', err: errText(err) })); }
    finally { setSaving(false); }
  }, [buildPayload, isNew, load, loadEditor]);

  const remove = async (key: string) => {
    if (!key) return;
    const ok = await confirm({ title: i18n.t('confirmDeleteTitle', { ns: 'provider' }), body: <>{i18n.t('confirmDeleteBodyPrefix', { ns: 'provider' })} <span className="font-mono text-zinc-100">{key}</span>{i18n.t('confirmDeleteBodySuffix', { ns: 'provider' })}</>, confirmLabel: i18n.t('deleteConfirm', { ns: 'provider' }), danger: true });
    if (!ok) return;
    try {
      await apiService.deleteProvider(key);
      toast(i18n.t('providerDeleted', { ns: 'provider', key }));
      if (selectedKey === key) { setSelectedKey(null); setIsNew(false); }
      await load();
    } catch (err) {
      const refs = err?.response?.data?.references;
      if (Array.isArray(refs) && refs.length) toast(i18n.t('cannotDeleteRefs', { ns: 'provider', refs: refs.join('、') }));
      else toast(i18n.t('deleteFailed', { ns: 'provider', err: errText(err) }));
    }
  };

  const runTest = useCallback(async (overrideModel?: string) => {
    setTesting(true); setTestResult(null);
    try {
      const p = buildPayload();
      const model = (overrideModel ?? p.defaultModel) || '';
      const resp = await apiService.testProvider({ url: p.url, protocol: p.protocol, apiKey: p.apiKey, defaultModel: p.defaultModel, model });
      setTestResult(resp.data as TestResult);
    } catch (err) { setTestResult({ ok: false, detail: errText(err) }); }
    finally { setTesting(false); }
  }, [buildPayload]);

  const testSingleModel = useCallback(async (model: string) => {
    setModelTestResults((prev) => ({ ...prev, [model]: 'pending' }));
    try {
      const p = buildPayload();
      const resp = await apiService.testProvider({ url: p.url, protocol: p.protocol, apiKey: p.apiKey, defaultModel: p.defaultModel, model });
      setModelTestResults((prev) => ({ ...prev, [model]: resp.data as TestResult }));
    } catch (err) {
      setModelTestResults((prev) => ({ ...prev, [model]: { ok: false, detail: errText(err) } }));
    }
  }, [buildPayload]);

  const updateDefault = async (agentType: string, providerKey: string) => {
    if (!data) return;
    setSavingSlot(agentType);
    const prev = { ...data.defaults };
    const next = { ...data.defaults };
    if (providerKey) next[agentType] = providerKey; else delete next[agentType];
    setData({ ...data, defaults: next });
    try {
      const resp = await apiService.updateProviderDefaults(next);
      setData((d) => (d ? { ...d, defaults: resp.data?.defaults || next } : d));
      toast(i18n.t('defaultProviderUpdated', { ns: 'provider', name: SLOT_LABELS[agentType] || agentType }));
    } catch (err) {
      setData((d) => (d ? { ...d, defaults: prev } : d));
      toast(i18n.t('updateFailed', { ns: 'provider', err: errText(err) }));
    } finally { setSavingSlot(null); }
  };

  /* ---- ⌘S ---- */
  const saveRef = useRef(save); saveRef.current = save;
  const canSaveRef = useRef(canSave); canSaveRef.current = canSave;
  const tabRef = useRef(tab); tabRef.current = tab;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S') && tabRef.current === 'providers' && canSaveRef.current) { e.preventDefault(); void saveRef.current(); }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  /* ---- derived ---- */
  const items = data?.items || [];
  const slots = data?.agent_type_slots || KNOWN_SLOTS;
  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter((p) => `${p.name || ''} ${p.key} ${p.url || ''}`.toLowerCase().includes(q));
  }, [items, query]);

  return (
    <div data-id="provider-dashboard-auto-27" className="flex h-screen flex-col bg-[#0A0A0A] text-zinc-300">
      {/* chrome */}
      <header data-id="provider-dashboard-auto-28" className="flex h-12 shrink-0 items-center gap-3 border-b border-white/[0.06] px-4">
        {onBack && (
          <button data-id="provider-dashboard-auto-29" onClick={onBack} className="-ml-1 grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.05] hover:text-zinc-200" title={t('back')}>
            <ArrowLeft size={16} />
          </button>
        )}
        <Boxes size={16} className="text-zinc-400" />
        <span data-id="provider-dashboard-auto-30" className="text-[13px] font-semibold text-white">{t('aiProviders')}</span>
        <div data-id="provider-dashboard-auto-31" className="flex-1" />
        {data?.source_path && (
          <span data-id="provider-dashboard-auto-32" className="hidden font-mono text-[11px] text-zinc-600 lg:inline" title={data.source_path}>{data.source_path}</span>
        )}
        <button data-id="provider-dashboard-auto-33" onClick={() => void load(selectedKey || undefined)} className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.05] hover:text-zinc-200" title={t('refresh')}>
          <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />
        </button>
      </header>

      {/* tabs */}
      <div data-id="provider-dashboard-auto-34" className="flex h-11 shrink-0 items-center border-b border-white/[0.06] px-4">
        <div data-id="provider-dashboard-auto-35" className="inline-flex rounded-lg bg-white/[0.03] p-0.5">
          {([['routing', t('tabRouting')], ['providers', t('tabProviders')]] as const).map(([id, label]) => (
            <button data-id="provider-dashboard-auto-36" key={id} onClick={() => setTab(id)}
              className={cn('flex h-7 items-center rounded-md px-3 text-[13px] transition-colors',
                tab === id ? 'bg-white/[0.08] text-white shadow-sm' : 'text-zinc-500 hover:text-zinc-300')}>
              {label}
            </button>
          ))}
        </div>
      </div>

      <div data-id="provider-dashboard-auto-37" className="flex-1 overflow-hidden">
        {tab === 'routing' ? (
          /* ═══════════ Agent routing ═══════════ */
          <div data-id="provider-dashboard-auto-38" className="h-full overflow-auto">
            <div data-id="provider-dashboard-auto-39" className="px-6 py-6">
              <div data-id="provider-dashboard-auto-40" className="grid grid-cols-[repeat(auto-fill,minmax(280px,1fr))] gap-3">
                {loading && items.length === 0 && [0, 1, 2].map((i) => (
                  <div data-id="provider-dashboard-auto-41" key={i} className={cn(CARD, 'p-4')}>
                    <div data-id="provider-dashboard-auto-42" className="flex items-center gap-3"><Skeleton className="h-9 w-9 rounded-xl" /><div className="flex-1 space-y-2"><Skeleton className="h-3.5 w-24" /><Skeleton className="h-3 w-32" /></div></div>
                    <Skeleton className="mt-4 h-9 w-full rounded-lg" />
                  </div>
                ))}

                {!loading && items.length === 0 && (
                  <div data-id="provider-dashboard-auto-43" className={cn(CARD, 'col-span-full flex flex-col items-center px-6 py-14 text-center')}>
                    <div data-id="provider-dashboard-auto-44" className="grid h-11 w-11 place-items-center rounded-xl bg-white/[0.04]"><Server size={20} className="text-zinc-500" /></div>
                    <div data-id="provider-dashboard-auto-45" className="mt-3 text-[14px] font-medium text-zinc-200">{t('noProvidersHeadline')}</div>
                    <div data-id="provider-dashboard-auto-46" className="mt-1 text-[12px] text-zinc-500">{t('noProvidersBody')}</div>
                    <Btn variant="primary" size="md" icon={<Plus size={14} />} className="mt-4" onClick={() => setTab('providers')}>{t('addProvider')}</Btn>
                  </div>
                )}

                {(items.length > 0 ? slots : []).map((slot) => {
                  const curKey = data?.defaults?.[slot] || '';
                  const provider = curKey ? (items.find((p) => p.key === curKey) || null) : null;
                  const want = SLOT_PROTOCOL[slot];
                  const model = resolveModelFor(provider, slot);
                  const mismatch = !!provider && !!want && proto(provider) !== want;
                  const tone: 'ok' | 'warn' | 'off' = !provider ? 'off' : mismatch ? 'warn' : 'ok';
                  return (
                    <div data-id="provider-dashboard-auto-47" key={slot} className={cn(CARD, 'flex flex-col p-4')}>
                      <div data-id="provider-dashboard-auto-48" className="flex items-center gap-3">
                        <AgentAvatar agentType={slot} title={SLOT_LABELS[slot] || slot} variant="panel" />
                        <div data-id="provider-dashboard-auto-49" className="min-w-0 flex-1">
                          <div data-id="provider-dashboard-auto-50" className="flex items-center gap-2">
                            <span data-id="provider-dashboard-auto-51" className="truncate text-[14px] font-semibold text-white">{SLOT_LABELS[slot] || slot}</span>
                            <StatusDot tone={tone} />
                          </div>
                          <div data-id="provider-dashboard-auto-52" className="mt-0.5 truncate text-[11px] text-zinc-500">{SLOT_DESC[slot] || slot}</div>
                        </div>
                      </div>

                      <div data-id="provider-dashboard-auto-55" className="mt-4">
                        <ProviderPicker
                          value={curKey} options={items} restrictToProtocol={want}
                          emptyHint={want ? t('emptyHintWithProto', { want }) : t('noProviders')}
                          busy={savingSlot === slot} disabled={savingSlot === slot}
                          onChange={(k) => void updateDefault(slot, k)}
                        />
                      </div>

                      <div data-id="provider-dashboard-auto-54" className="mt-3 flex min-w-0 items-center gap-1.5 text-[11px] text-zinc-500">
                        <Cpu size={11} className="shrink-0 text-zinc-600" />
                        <span data-id="provider-dashboard-auto-58" className="truncate font-mono text-zinc-400">{model || <span className="font-sans text-zinc-600">{SLOT_FALLBACK_MODEL[slot] || '—'}</span>}</span>
                      </div>

                      {mismatch && (
                        <div data-id="provider-dashboard-auto-56" className="mt-2 flex items-center gap-1.5 text-[11px] text-amber-300">
                          <AlertTriangle size={11} className="shrink-0 text-amber-400" />
                          <span data-id="provider-dashboard-auto-57" className="truncate">{t('protoMismatchInline', { has: proto(provider), slot: SLOT_LABELS[slot] || slot, want })}</span>
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          </div>
        ) : (
          /* ═══════════ Providers (master/detail) ═══════════ */
          <div data-id="provider-dashboard-auto-58" className="flex h-full">
            {/* rail */}
            <aside data-id="provider-dashboard-auto-59" className="flex w-72 shrink-0 flex-col border-r border-white/[0.06] bg-white/[0.01]">
              <div data-id="provider-dashboard-auto-60" className="p-2.5">
                <div data-id="provider-dashboard-auto-61" className="relative">
                  <Search size={13} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-zinc-600" />
                  <input data-id="provider-dashboard-auto-62" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('searchPlaceholder')} className={cn(INPUT, 'h-8 pl-7 text-[12px]')} />
                </div>
              </div>
              <div data-id="provider-dashboard-auto-63" className="flex-1 overflow-auto px-2 pb-2">
                {loading && items.length === 0 && [0, 1, 2, 3].map((i) => (
                  <div data-id="provider-dashboard-auto-64" key={i} className="flex items-center gap-2 px-2 py-2"><div className="min-w-0 flex-1 space-y-1.5"><Skeleton className="h-3 w-28" /><Skeleton className="h-2.5 w-20" /></div></div>
                ))}
                {!loading && items.length === 0 && <div className="px-3 py-10 text-center text-[12px] leading-relaxed text-zinc-600">{t('noProviders')}<br />{t('addProviderBtn')}</div>}
                {!loading && items.length > 0 && filteredItems.length === 0 && <div className="px-3 py-10 text-center text-[12px] text-zinc-600">{t('noMatchQuery', { query })}</div>}
                <ul data-id="provider-dashboard-auto-65" className="space-y-0.5">
                  {filteredItems.map((p) => {
                    const active = !isNew && selectedKey === p.key;
                    const isProtected = PROTECTED_PROVIDER_KEYS.has(p.key);
                    return (
                      <li data-id="provider-dashboard-auto-66" key={p.key}>
                        <button data-id="provider-dashboard-auto-67" type="button" onClick={() => selectProvider(p.key)}
                          className={cn('group relative flex w-full items-center gap-2 rounded-lg py-2 pl-3 pr-2 text-left transition-colors', active ? 'bg-white/[0.05]' : 'hover:bg-white/[0.03]')}>
                          {active && <span className="absolute inset-y-2 left-0 w-0.5 rounded-r bg-blue-400" />}
                          <div data-id="provider-dashboard-auto-68" className="min-w-0 flex-1">
                            <div data-id="provider-dashboard-auto-69" className="flex items-center gap-1.5">
                              <span data-id="provider-dashboard-auto-70" className={cn('truncate text-[13px]', active ? 'text-white' : 'text-zinc-200')}>{p.name || p.key}</span>
                              <ProtocolBadge protocol={p.protocol} />
                            </div>
                            <div data-id="provider-dashboard-auto-71" className="mt-0.5 flex items-center gap-1.5">
                              <span data-id="provider-dashboard-auto-72" className="truncate font-mono text-[11px] text-zinc-600">{p.key}</span>
                            </div>
                          </div>
                          {!isProtected && (
                            <span data-id="provider-dashboard-auto-73" role="button" tabIndex={-1} onClick={(e) => { e.stopPropagation(); remove(p.key); }}
                              className="grid h-6 w-6 shrink-0 place-items-center rounded text-zinc-600 opacity-0 transition-all hover:bg-red-500/15 hover:text-red-300 group-hover:opacity-100" title={t('delete')}>
                              <Trash2 size={12} />
                            </span>
                          )}
                        </button>
                      </li>
                    );
                  })}
                </ul>
              </div>
              <div data-id="provider-dashboard-auto-74" className="border-t border-white/[0.06] p-2">
                <Btn variant={isNew ? 'primary' : 'secondary'} size="md" icon={<Plus size={14} />} className="w-full" onClick={startNew}>{t('addProvider')}</Btn>
              </div>
            </aside>

            {/* detail */}
            <main data-id="provider-dashboard-auto-75" className="flex-1 overflow-auto">
              {!isNew && !selectedProvider ? (
                <div data-id="provider-dashboard-auto-76" className="flex h-full flex-col items-center justify-center px-6 text-center">
                  <div data-id="provider-dashboard-auto-77" className="grid h-12 w-12 place-items-center rounded-xl bg-white/[0.04]"><Server size={22} className="text-zinc-500" /></div>
                  <div data-id="provider-dashboard-auto-78" className="mt-3 text-[14px] font-medium text-zinc-200">{loading ? t('loadingProviders') : items.length === 0 ? t('noProviders') : t('selectProvider')}</div>
                  <div data-id="provider-dashboard-auto-79" className="mt-1 text-[12px] text-zinc-500">{items.length === 0 ? t('addFirstProvider') : t('pickOrAddNew')}</div>
                  <Btn variant="secondary" size="md" icon={<Plus size={14} />} className="mt-4" onClick={startNew}>{t('addProvider')}</Btn>
                </div>
              ) : (
                <div data-id="provider-dashboard-auto-80" className="mx-auto max-w-[680px] px-8 py-7">
                  {/* header */}
                  <div data-id="provider-dashboard-auto-81" className="flex items-center gap-2.5">
                    <h1 className="truncate text-[17px] font-semibold tracking-tight text-white">{isNew ? t('newProvider') : (selectedProvider?.name || selectedProvider?.key)}</h1>
                    {!isNew && <ProtocolBadge protocol={draft.protocol} />}
                    {dirty && <span className="inline-flex items-center gap-1 text-[11px] text-amber-300"><span className="h-1.5 w-1.5 rounded-full bg-amber-400" />{t('unsaved')}</span>}
                    <div data-id="provider-dashboard-auto-82" className="flex-1" />
                    {!isNew && !PROTECTED_PROVIDER_KEYS.has(draft.key) && <Btn variant="danger" size="sm" icon={<Trash2 size={12} />} onClick={() => remove(draft.key)}>{t('delete')}</Btn>}
                  </div>
                  <div data-id="provider-dashboard-auto-83" className="my-5 h-px bg-white/[0.06]" />

                  <div data-id="provider-dashboard-auto-84" className="space-y-7">
                    {/* Basic info */}
                    <section data-id="provider-dashboard-auto-85" className="space-y-3.5">
                      <SectionHeader>{t('sectionBasic')}</SectionHeader>
                      <div data-id="provider-dashboard-auto-86" className="grid gap-3.5 sm:grid-cols-2">
                        <Field label={t('fieldName')}>
                          <input data-id="provider-dashboard-auto-87" value={draft.name || ''} onChange={(e) => patchDraft({ name: e.target.value })} className={INPUT} placeholder="2000Run Claude" />
                        </Field>
                        <Field label="Key" help={isNew ? t('keyHelpNew') : t('keyHelpExisting')}>
                          <input data-id="provider-dashboard-auto-88" value={draft.key} onChange={(e) => patchDraft({ key: e.target.value })} disabled={!isNew} className={cn(INPUT, 'font-mono', !isNew && 'cursor-not-allowed')} placeholder="2000RunClaude" />
                        </Field>
                      </div>
                    </section>

                    {/* Models */}
                    <section data-id="provider-dashboard-auto-97" className="space-y-3.5">
                      <SectionHeader>{t('sectionModels')}</SectionHeader>
                      {(() => {
                        const availableModels = modelsText.split('\n').map((l) => l.trim()).filter(Boolean);
                        const currentDefault = draft.defaultModel || '';
                        return (
                          <Field label={t('fieldDefaultModel')} help={t('fieldDefaultModelHelp')}>
                            <Select
                              searchable
                              placeholder={t('selectModel')}
                              value={availableModels.includes(currentDefault) ? currentDefault : ''}
                              options={availableModels.map((m) => ({ value: m, label: m }))}
                              onChange={(v) => patchDraft({ defaultModel: v })}
                            />
                          </Field>
                        );
                      })()}
                      <Field label={t('fieldAvailableModels')} help={t('fieldAvailableModelsHelp')}>
                        <textarea data-id="provider-dashboard-auto-103" value={modelsText} onChange={(e) => setModelsText(e.target.value)} rows={4} className={cn(INPUT, 'h-auto resize-y py-2 font-mono leading-relaxed')} placeholder={'gpt-5.5\ngpt-5.4'} />
                      </Field>
                    </section>

                    {/* Access */}
                    <section data-id="provider-dashboard-auto-89" className="space-y-3.5">
                      <SectionHeader>{t('sectionAccess')}</SectionHeader>
                      <Field label="API Base URL" help={t('fieldApiBaseHelp')}>
                        <div data-id="provider-dashboard-auto-90" className="relative">
                          <Link2 size={13} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600" />
                          <input data-id="provider-dashboard-auto-91" value={draft.url || ''} onChange={(e) => patchDraft({ url: e.target.value })} className={cn(INPUT, 'pl-8 font-mono')} placeholder="https://api.2000.run/v1" />
                        </div>
                      </Field>
                      <div data-id="provider-dashboard-auto-92" className="grid gap-3.5 sm:grid-cols-[150px_1fr]">
                        <Field label={t('fieldProtocol')}>
                          <select data-id="provider-dashboard-auto-93" value={proto(draft) || 'openai'} onChange={(e) => patchDraft({ protocol: e.target.value })} className={cn(INPUT, !isNew && 'cursor-not-allowed opacity-60')} disabled={!isNew}>
                            {PROTOCOLS.map((p) => <option key={p} value={p}>{p}</option>)}
                          </select>
                        </Field>
                        <Field label="API Key">
                          <div data-id="provider-dashboard-auto-94" className="relative">
                            {/* type="text" so Chrome's password manager never prompts to save */}
                            <input data-id="provider-dashboard-auto-95"
                              type="text" name="cicy-provider-api-key" value={draft.apiKey || ''} onChange={(e) => patchDraft({ apiKey: e.target.value })}
                              className={cn(INPUT, 'pr-9 font-mono')} placeholder="sk-…" autoComplete="off" autoCapitalize="off" autoCorrect="off" spellCheck={false}
                              data-1p-ignore data-lpignore="true"
                              style={showApiKey ? undefined : ({ WebkitTextSecurity: 'disc' } as React.CSSProperties)}
                            />
                            <button data-id="provider-dashboard-auto-96" type="button" onClick={() => setShowApiKey((s) => !s)} className="absolute right-1.5 top-1/2 grid h-6 w-6 -translate-y-1/2 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300" title={showApiKey ? t('hide') : t('show')}>
                              {showApiKey ? <EyeOff size={13} /> : <Eye size={13} />}
                            </button>
                          </div>
                        </Field>
                      </div>
                    </section>

                    {/* test result */}
                    {testResult && (
                      <div data-id="provider-dashboard-auto-105" className={cn('relative rounded-xl border px-3.5 py-3 text-[12px]',
                        testResult.ok ? 'border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-200' : 'border-red-500/25 bg-red-500/[0.07] text-red-200')}>
                        <button data-id="provider-dashboard-auto-106" onClick={() => setTestResult(null)} className="absolute right-2.5 top-2.5 text-current opacity-50 transition-opacity hover:opacity-100"><X size={12} /></button>
                        <div data-id="provider-dashboard-auto-107" className="flex items-center gap-1.5 pr-5 font-medium">
                          {testResult.ok ? <Check size={13} /> : <X size={13} />}
                          {testResult.ok ? t('testConnSuccess') : t('testConnFailed')}
                          {typeof testResult.status === 'number' && <span className="font-normal opacity-70">· HTTP {testResult.status}</span>}
                          {typeof testResult.duration_ms === 'number' && <span className="font-normal opacity-70">· {testResult.duration_ms} ms</span>}
                          {testResult.model && <span className="font-mono font-normal opacity-70">· {testResult.model}</span>}
                        </div>
                        {testResult.endpoint && <div className="mt-1 break-all font-mono opacity-70">{testResult.endpoint}</div>}
                        {testResult.detail && testResult.detail !== 'ok' && <div className="mt-1 whitespace-pre-wrap break-all opacity-80">{testResult.detail}</div>}
                      </div>
                    )}
                  </div>

                  {/* footer */}
                  <div data-id="provider-dashboard-auto-108" className="mt-7 flex items-center gap-2 border-t border-white/[0.06] pt-5">
                    <Btn variant="primary" size="md" icon={<Save size={14} />} busy={saving} disabled={saving || !canSave} onClick={() => void save()}>{isNew ? t('create') : t('save')}</Btn>
                    {!isNew && <Btn variant="secondary" size="md" icon={<Zap size={14} />} disabled={modelsText.split('\n').map((l) => l.trim()).filter(Boolean).length === 0} onClick={() => { setModelTestResults({}); setTestPickedModel(draft.defaultModel || ''); setTestModalOpen(true); }}>{t('testConnection')}</Btn>}
                    {isNew && <Btn variant="ghost" size="md" onClick={() => { void guardDirty(() => { setIsNew(false); setSelectedKey(items[0]?.key || null); }); }}>{t('cancel')}</Btn>}
                    {!isNew && dirty && <Btn variant="ghost" size="md" onClick={() => loadEditor(selectedProvider)}>{t('discardChanges')}</Btn>}
                    <div data-id="provider-dashboard-auto-109" className="flex-1" />
                    <span data-id="provider-dashboard-auto-110" className="hidden text-[11px] text-zinc-600 sm:flex sm:items-center sm:gap-1.5">
                      <Cpu size={11} /> {t('footnoteSave')}
                    </span>
                  </div>
                </div>
              )}
            </main>
          </div>
        )}
      </div>
      {dialogsNode}

      {/* Per-model test modal */}
      {testModalOpen && (() => {
        const availableModels = modelsText.split('\n').map((l) => l.trim()).filter(Boolean);
        const r = testPickedModel ? modelTestResults[testPickedModel] : null;
        const pending = r === 'pending';
        const result = pending ? null : (r as TestResult | undefined);
        const tone = !result ? 'idle' : result.ok ? 'ok' : 'fail';
        return (
          <div data-id="provider-test-modal" className="fixed inset-0 z-[10000] cursor-pointer" onClick={() => setTestModalOpen(false)}>
            <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
            <div className="absolute right-0 top-0 h-full w-[480px] max-w-[92vw] cursor-default flex flex-col overflow-hidden border-l border-white/[0.08] bg-[#161618] shadow-[0_0_50px_rgba(0,0,0,0.5)] animate-drawer-in"
              onClick={(e) => e.stopPropagation()}>
              <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
                <div className="flex items-center gap-2">
                  <Zap className="w-4 h-4 text-zinc-400" />
                  <h2 className="text-[15px] font-semibold text-white">{t('testModalTitle')}</h2>
                </div>
                <button onClick={() => setTestModalOpen(false)} className="p-1.5 rounded-lg text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors cursor-pointer">
                  <X className="w-4 h-4" />
                </button>
              </div>
              <div className="px-5 py-3 border-b border-white/[0.06] text-[11px] text-zinc-500 font-mono truncate">
                {draft.url} · {proto(draft) || 'openai'}
              </div>
              <div className="px-5 py-4 space-y-3">
                {availableModels.length === 0 ? (
                  <div className="text-center text-[12px] text-zinc-600 py-6">{t('noModelsForTest')}</div>
                ) : (
                  <>
                    <div>
                      <div className="mb-1.5 text-[11px] font-medium text-zinc-500">{t('testModalPickModel')}</div>
                      <Select
                        searchable
                        placeholder={t('selectModel')}
                        value={testPickedModel}
                        options={availableModels.map((m) => ({ value: m, label: m }))}
                        onChange={(v) => setTestPickedModel(v)}
                      />
                    </div>
                    <button
                      type="button"
                      disabled={!testPickedModel || pending}
                      onClick={() => testPickedModel && void testSingleModel(testPickedModel)}
                      className={cn(
                        'inline-flex w-full items-center justify-center gap-2 rounded-lg border px-3 py-2 text-[13px] font-medium transition-colors',
                        'border-white/[0.1] bg-white/[0.04] text-zinc-200 hover:bg-white/[0.08] hover:border-white/[0.16]',
                        'disabled:cursor-not-allowed disabled:opacity-50',
                      )}
                    >
                      {pending ? <Spinner size="xs" /> : <Zap size={13} />}
                      {pending ? t('testing') : (result ? t('testRetry') : t('test'))}
                    </button>
                    {result && (
                      <div className={cn('rounded-lg border px-3 py-2.5 text-[12px]',
                        tone === 'ok'
                          ? 'border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-200'
                          : 'border-red-500/25 bg-red-500/[0.07] text-red-200')}>
                        <div className="flex items-center gap-1.5 font-medium">
                          {tone === 'ok' ? <Check size={13} /> : <X size={13} />}
                          {tone === 'ok' ? t('testOk') : t('testFail')}
                          {typeof result.status === 'number' && <span className="font-normal opacity-70">· HTTP {result.status}</span>}
                          {typeof result.duration_ms === 'number' && <span className="font-normal opacity-70">· {result.duration_ms} ms</span>}
                        </div>
                        {!result.ok && result.detail && (
                          <div className="mt-1 text-[11px] opacity-80 break-all font-mono">{result.detail}</div>
                        )}
                      </div>
                    )}
                  </>
                )}
              </div>
            </div>
          </div>
        );
      })()}
    </div>
  );
}
