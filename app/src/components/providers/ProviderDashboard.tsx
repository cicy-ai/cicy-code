import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import {
  Plus, Save, Trash2, Zap, Eye, EyeOff, Check, X,
  Server, Search, ChevronsUpDown, AlertTriangle, Link2, Cpu, ExternalLink,
} from 'lucide-react';
import apiService from '../../services/api';
import AgentAvatar from '../AgentAvatar';
import { useDialogs } from '../ui/Modal';
import { Spinner } from '../ui/Spinner';
import Select, { type SelectOption } from '../ui/Select';

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
const KNOWN_SLOTS = ['claude', 'cicy', 'codex', 'opencode'];
const PROTECTED_PROVIDER_KEYS = new Set(['defaultAnthropic', 'defaultOpenAi']);
const SLOT_LABELS: Record<string, string> = { claude: 'Claude', cicy: 'CiCy', codex: 'Codex', opencode: 'OpenCode' };
const SLOT_DESC: Record<string, string> = { claude: 'Claude Code CLI', cicy: 'CiCy Lite Agent', codex: 'OpenAI Codex CLI', opencode: 'OpenCode CLI' };
// cicy has no protocol restriction: it always speaks Anthropic Messages but the
// gateway bridges an openai-protocol provider down to Chat Completions, so either
// kind of provider can back the cicy slot (mirrors opencode, which is also open).
const SLOT_PROTOCOL: Record<string, string> = { claude: 'anthropic', cicy: '', codex: 'openai', opencode: 'openai' };
const SLOT_FALLBACK_MODEL: Record<string, string> = { claude: 'claude-opus-4-7', cicy: 'deepseek-v4-pro', codex: 'gpt-5.4', opencode: 'deepseek-v4-pro' };

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
// A model-mapping rule edited as a structured row: "when the agent requests
// `from`, send `to` upstream instead". `from` may end with "*" for a prefix
// match, or be empty for the catch-all. Rows are the editable shape; they
// collapse to the Record<string,string> the backend's applyModelMapping reads.
type MappingRow = { from: string; to: string };

function rowsToMapping(rows: MappingRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) {
    const from = (r.from || '').trim();
    const to = (r.to || '').trim();
    if (!from || !to) continue; // incomplete rule — drop it (matches backend)
    out[from] = to;
  }
  return out;
}

function mappingToRows(map?: Record<string, string>): MappingRow[] {
  if (!map) return [];
  return Object.entries(map).map(([from, to]) => ({ from, to }));
}

function editorSnapshot(d: ProviderRecord, modelsText: string, mappingRows: MappingRow[]): string {
  return JSON.stringify({
    key: d.key.trim(),
    name: (d.name || '').trim(),
    url: (d.url || '').trim().replace(/\/+$/, ''),
    apiKey: d.apiKey || '',
    protocol: proto(d) || 'openai',
    defaultModel: (d.defaultModel || '').trim(),
    defaultModels: compactDM(d.defaultModels),
    models: modelsText.split('\n').map((s) => s.trim()).filter(Boolean),
    modelMapping: rowsToMapping(mappingRows),
  });
}

/* ───────────────────────── design atoms ───────────────────────── */

const INPUT = 'h-9 w-full rounded-lg border border-white/[0.09] bg-white/[0.025] px-3 text-[13px] text-zinc-100 placeholder:text-zinc-600 outline-none transition-colors hover:border-white/[0.14] focus:border-blue-500/55 focus:bg-white/[0.045] focus:ring-1 focus:ring-blue-500/15 disabled:opacity-50';
const CARD = 'rounded-xl border border-white/[0.07] bg-white/[0.015]';

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
    <label data-id="provider-field" className={cn('block', className)}>
      <span data-id="provider-field-label" className="mb-1.5 block text-[12px] font-medium text-zinc-400">{label}</span>
      {children}
      {help && <span className="mt-1.5 block text-[11px] leading-snug text-zinc-600">{help}</span>}
    </label>
  );
}

type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
function Btn({ variant = 'secondary', size = 'md', icon, children, busy, className, dataId, ...rest }: {
  variant?: BtnVariant; size?: 'sm' | 'md'; icon?: React.ReactNode; children?: React.ReactNode; busy?: boolean; dataId?: string;
} & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const styles: Record<BtnVariant, string> = {
    primary: 'bg-white text-[#0b0b0c] hover:bg-zinc-200 disabled:bg-white/40 font-medium',
    secondary: 'border border-white/[0.1] bg-white/[0.03] text-zinc-200 hover:bg-white/[0.07] hover:border-white/[0.16]',
    ghost: 'text-zinc-400 hover:text-zinc-100 hover:bg-white/[0.05]',
    danger: 'border border-red-500/20 bg-red-500/[0.07] text-red-300 hover:bg-red-500/[0.14]',
  };
  const sizes = { sm: 'h-7 px-2.5 text-[12px] gap-1', md: 'h-8 px-3 text-[13px] gap-1.5' };
  return (
    <button data-id={dataId ?? 'provider-btn'} {...rest} className={cn('inline-flex items-center justify-center whitespace-nowrap rounded-lg transition-colors disabled:cursor-not-allowed', sizes[size], styles[variant], className)}>
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
    <div data-id="provider-picker" ref={ref} className="relative">
      <button data-id="provider-picker-trigger"
        type="button" disabled={disabled} onClick={() => setOpen((o) => !o)}
        className={cn('group flex h-9 w-full items-center gap-2 rounded-lg border bg-white/[0.025] pl-3 pr-2 text-left text-[13px] transition-colors',
          open ? 'border-blue-500/55 ring-1 ring-blue-500/15' : 'border-white/[0.09] hover:border-white/[0.16]',
          disabled && 'cursor-not-allowed opacity-50')}
      >
        {selected ? (
          <>
            <span data-id="provider-picker-selected-name" className={cn('truncate', selectedMismatch ? 'text-amber-300' : 'text-zinc-100')}>{selected.name || selected.key}</span>
            <ProtocolBadge protocol={selected.protocol} />
            {selectedMismatch && <AlertTriangle size={12} className="shrink-0 text-amber-400" />}
          </>
        ) : (
          <span data-id="provider-picker-fallback" className="text-zinc-600">{i18n.t('useFallback', { ns: 'provider' })}</span>
        )}
        <span data-id="provider-picker-indicators" className="ml-auto flex shrink-0 items-center gap-1.5">
          {busy && <Spinner size="xs" />}
          <ChevronsUpDown size={13} className="text-zinc-600 transition-colors group-hover:text-zinc-400" />
        </span>
      </button>

      {open && (
        <div data-id="provider-picker-menu" className="absolute left-0 right-0 z-50 mt-1.5 max-h-72 overflow-auto rounded-xl border border-white/[0.09] bg-[#141416] p-1 shadow-2xl shadow-black/60">
          {restrictToProtocol && (
            <div data-id="provider-picker-protocol-note" className="px-2.5 pb-1 pt-1.5 text-[10px] uppercase tracking-[0.1em] text-zinc-600">{i18n.t('restrictByProtocol', { ns: 'provider', protocol: restrictToProtocol })}</div>
          )}
          <button data-id="provider-picker-option-fallback" type="button" onClick={() => pick('')}
            className={cn('flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] transition-colors hover:bg-white/[0.06]', !value && 'bg-white/[0.07]')}>
            <span data-id="provider-picker-option-fallback-check" className="grid w-4 place-items-center">{!value && <Check size={13} className="text-blue-400" />}</span>
            <span data-id="provider-picker-option-fallback-label" className="text-zinc-500">{i18n.t('useFallback', { ns: 'provider' })}</span>
          </button>
          {compatible.length > 0 && <div className="my-1 h-px bg-white/[0.06]" />}
          {compatible.map((p) => {
            const isSel = p.key === value;
            return (
              <button data-id={`provider-picker-option-${p.key}`} key={p.key} type="button" onClick={() => pick(p.key)}
                className={cn('flex w-full items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left text-[13px] transition-colors hover:bg-white/[0.06]', isSel && 'bg-white/[0.07]')}>
                <span data-id="provider-picker-option-check" className="grid w-4 shrink-0 place-items-center">{isSel && <Check size={13} className="text-blue-400" />}</span>
                <span data-id="provider-picker-option-body" className="min-w-0 flex-1">
                  <span data-id="provider-picker-option-name-row" className="flex items-center gap-1.5">
                    <span data-id="provider-picker-option-name" className="truncate text-zinc-100">{p.name || p.key}</span>
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
              <div data-id="provider-picker-mismatch-divider" className="my-1 h-px bg-white/[0.06]" />
              <button data-id="provider-picker-mismatch-option" type="button" onClick={() => pick(selected.key)}
                className="flex w-full items-center gap-2.5 rounded-lg bg-amber-500/[0.06] px-2.5 py-1.5 text-left text-[13px] transition-colors hover:bg-amber-500/[0.12]">
                <span data-id="provider-picker-mismatch-check" className="grid w-4 shrink-0 place-items-center"><Check size={13} className="text-amber-400" /></span>
                <span data-id="provider-picker-mismatch-body" className="min-w-0 flex-1">
                  <span data-id="provider-picker-mismatch-name-row" className="flex items-center gap-1.5">
                    <span data-id="provider-picker-mismatch-name" className="truncate text-amber-200">{selected.name || selected.key}</span>
                    <ProtocolBadge protocol={selected.protocol} />
                    <span data-id="provider-picker-mismatch-pill" className="rounded bg-amber-500/15 px-1 py-px text-[9px] font-medium text-amber-300">{i18n.t('protoMismatchPill', { ns: 'provider' })}</span>
                  </span>
                  <span data-id="provider-picker-mismatch-key" className="block truncate font-mono text-[11px] text-zinc-600">{selected.key}</span>
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

export default function ProviderDashboard({ leftMount, rightMount, tab: controlledTab, hideTabStrip }: {
  leftMount: HTMLElement | null;
  rightMount: HTMLElement | null;
  // When the host (e.g. the unified Settings modal) drives which tab shows via
  // its own nav, pass `tab` (controlled) + `hideTabStrip` to suppress the
  // internal strip. Omitted ⇒ self-managed tabs (the legacy left-panel usage).
  tab?: Tab;
  hideTabStrip?: boolean;
}) {
  const { t } = useTranslation('provider');
  const { confirm, node: dialogsNode } = useDialogs();
  const [tabState, setTabState] = useState<Tab>('routing');
  const tab = controlledTab ?? tabState;
  const setTab = setTabState;
  const [loading, setLoading] = useState(true);
  const [data, setData] = useState<ProvidersResponse | null>(null);

  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [draft, setDraft] = useState<ProviderRecord>(emptyDraft());
  const [modelsText, setModelsText] = useState('');
  const [mappingRows, setMappingRows] = useState<MappingRow[]>([]);
  const [baseline, setBaseline] = useState('');
  const [showApiKey, setShowApiKey] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [testModalOpen, setTestModalOpen] = useState(false);
  const [testingAll, setTestingAll] = useState(false);
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
    const rows = mappingToRows(next.modelMapping);
    setDraft(next); setModelsText(mt); setMappingRows(rows); setBaseline(editorSnapshot(next, mt, rows));
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
      else if (!isNew && selectedKey && !payload.items.some((p) => p.key === selectedKey)) {
        setSelectedKey(null);
        loadEditor(null);
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
    if (isNew) { loadEditor(null); return; }
    if (!selectedKey) { loadEditor(null); return; }
    loadEditor(data?.items.find((p) => p.key === selectedKey) || null);
  }, [isNew, selectedKey]); // eslint-disable-line react-hooks/exhaustive-deps

  /* ---- dirty ---- */
  const snapshot = useMemo(() => editorSnapshot(draft, modelsText, mappingRows), [draft, modelsText, mappingRows]);
  const dirty = snapshot !== baseline;
  const canSave = (isNew || dirty) && !!draft.key.trim() && !!(draft.url || '').trim() && (proto(draft) === 'openai' || proto(draft) === 'anthropic');

  const patchDraft = (patch: Partial<ProviderRecord>) => setDraft((prev) => ({ ...prev, ...patch }));

  const guardDirty = async (action: () => void) => {
    if (!dirty) { action(); return; }
    if (await confirm({ title: i18n.t('confirmDiscardTitle', { ns: 'provider' }), body: i18n.t('confirmDiscardBody', { ns: 'provider' }), confirmLabel: i18n.t('confirmDiscardConfirm', { ns: 'provider' }), danger: true })) action();
  };
  const selectProvider = (key: string) => { void guardDirty(() => { setIsNew(false); setSelectedKey(key); setTab('providers'); }); };
  const startNew = () => { void guardDirty(() => { setIsNew(true); setSelectedKey(null); setTab('providers'); }); };

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
    modelMapping: rowsToMapping(mappingRows),
  }), [draft, modelsText, mappingRows]);

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
    } catch (err: any) {
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

  // Test every declared model — bounded concurrency (3 in-flight) so a long
  // model list doesn't fire dozens of upstream requests at once.
  const testAllModels = useCallback(async () => {
    const models = modelsText.split('\n').map((l) => l.trim()).filter(Boolean);
    if (models.length === 0) return;
    setTestingAll(true);
    try {
      const queue = [...models];
      const worker = async () => { for (let m = queue.shift(); m; m = queue.shift()) await testSingleModel(m); };
      await Promise.all([worker(), worker(), worker()]);
    } finally { setTestingAll(false); }
  }, [modelsText, testSingleModel]);

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
  const detailOpenRef = useRef(false); detailOpenRef.current = isNew || !!selectedKey;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S') && detailOpenRef.current && canSaveRef.current) { e.preventDefault(); void saveRef.current(); }
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

  if (!leftMount && !rightMount) return null;

  const detailOpen = isNew || !!selectedKey;

  /* ───────────── LEFT PANEL UI ───────────── */
  const leftPanelUI = (
    <div data-id="providers-left-root" className="h-full flex flex-col bg-[#0A0A0A] text-zinc-300">
      {/* tab strip — suppressed when an external host (Settings modal) drives the tab */}
      {!hideTabStrip && (
      <div data-id="providers-left-tabs" className="px-2 pt-2 pb-2 border-b border-white/[0.06] shrink-0">
        <div className="inline-flex rounded-lg bg-white/[0.03] p-0.5 w-full">
          {([['routing', t('tabRouting')], ['providers', t('tabProviders')]] as const).map(([id, label]) => (
            <button key={id} data-id={`providers-left-tab-${id}`} onClick={() => setTab(id)}
              className={cn('flex flex-1 h-7 items-center justify-center rounded-md px-2 text-[12px] transition-colors',
                tab === id ? 'bg-white/[0.08] text-white shadow-sm' : 'text-zinc-500 hover:text-zinc-300')}>
              {label}
            </button>
          ))}
        </div>
      </div>
      )}

      {/* body */}
      <div className="flex-1 min-h-0 overflow-hidden">
        {tab === 'routing' ? (
          /* ═══════════ Agent routing (single column) ═══════════ */
          <div data-id="providers-left-routing" className="h-full overflow-auto p-2.5 space-y-2.5">
            {loading && items.length === 0 && [0, 1, 2].map((i) => (
              <div key={i} className={cn(CARD, 'p-3')}>
                <div className="flex items-center gap-2.5"><Skeleton className="h-8 w-8 rounded-lg" /><div className="flex-1 space-y-1.5"><Skeleton className="h-3 w-20" /><Skeleton className="h-2.5 w-28" /></div></div>
                <Skeleton className="mt-3 h-8 w-full rounded-lg" />
              </div>
            ))}

            {!loading && items.length === 0 && (
              <div className={cn(CARD, 'flex flex-col items-center px-4 py-10 text-center')}>
                <div className="grid h-10 w-10 place-items-center rounded-xl bg-white/[0.04]"><Server size={18} className="text-zinc-500" /></div>
                <div className="mt-3 text-[13px] font-medium text-zinc-200">{t('noProvidersHeadline')}</div>
                <div className="mt-1 text-[11px] text-zinc-500">{t('noProvidersBody')}</div>
                <Btn variant="primary" size="sm" icon={<Plus size={13} />} className="mt-3" onClick={startNew}>{t('addProvider')}</Btn>
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
                <div key={slot} className={cn(CARD, 'flex flex-col p-3')}>
                  <div className="flex items-center gap-2.5">
                    <AgentAvatar agentType={slot} title={SLOT_LABELS[slot] || slot} variant="panel" />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate text-[13px] font-semibold text-white">{SLOT_LABELS[slot] || slot}</span>
                        <StatusDot tone={tone} />
                      </div>
                      <div className="mt-0.5 truncate text-[10.5px] text-zinc-500">{SLOT_DESC[slot] || slot}</div>
                    </div>
                  </div>

                  <div className="mt-3">
                    <ProviderPicker
                      value={curKey} options={items} restrictToProtocol={want}
                      emptyHint={want ? t('emptyHintWithProto', { want }) : t('noProviders')}
                      busy={savingSlot === slot} disabled={savingSlot === slot}
                      onChange={(k) => void updateDefault(slot, k)}
                    />
                  </div>

                  <div className="mt-2 flex min-w-0 items-center gap-1.5 text-[10.5px] text-zinc-500">
                    <Cpu size={10} className="shrink-0 text-zinc-600" />
                    <span className="truncate font-mono text-zinc-400">{model || <span className="font-sans text-zinc-600">{SLOT_FALLBACK_MODEL[slot] || '—'}</span>}</span>
                  </div>

                  {mismatch && (
                    <div className="mt-1.5 flex items-center gap-1.5 text-[10.5px] text-amber-300">
                      <AlertTriangle size={10} className="shrink-0 text-amber-400" />
                      <span className="truncate">{t('protoMismatchInline', { has: proto(provider), slot: SLOT_LABELS[slot] || slot, want })}</span>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        ) : (
          /* ═══════════ Providers list ═══════════ */
          <div data-id="providers-left-list" className="h-full flex flex-col">
            <div className="p-2.5 shrink-0">
              <div className="relative">
                <Search size={13} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-zinc-600" />
                <input data-id="providers-left-search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('searchPlaceholder')} className={cn(INPUT, 'h-8 pl-7 text-[12px]')} />
              </div>
            </div>
            <div className="flex-1 overflow-auto px-2 pb-2">
              {loading && items.length === 0 && [0, 1, 2, 3].map((i) => (
                <div key={i} className="flex items-center gap-2 px-2 py-2"><div className="min-w-0 flex-1 space-y-1.5"><Skeleton className="h-3 w-28" /><Skeleton className="h-2.5 w-20" /></div></div>
              ))}
              {!loading && items.length === 0 && <div className="px-3 py-10 text-center text-[12px] leading-relaxed text-zinc-600">{t('noProviders')}<br />{t('addProviderBtn')}</div>}
              {!loading && items.length > 0 && filteredItems.length === 0 && <div className="px-3 py-10 text-center text-[12px] text-zinc-600">{t('noMatchQuery', { query })}</div>}
              <ul className="space-y-0.5">
                {filteredItems.map((p) => {
                  const active = !isNew && selectedKey === p.key;
                  const isProtected = PROTECTED_PROVIDER_KEYS.has(p.key);
                  return (
                    <li key={p.key}>
                      <button data-id={`providers-left-item-${p.key}`} type="button" onClick={() => selectProvider(p.key)}
                        className={cn('group relative flex w-full items-center gap-2 rounded-lg py-2 pl-3 pr-2 text-left transition-colors', active ? 'bg-white/[0.05]' : 'hover:bg-white/[0.03]')}>
                        {active && <span className="absolute inset-y-2 left-0 w-0.5 rounded-r bg-blue-400" />}
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-1.5">
                            <span className={cn('truncate text-[13px]', active ? 'text-white' : 'text-zinc-200')}>{p.name || p.key}</span>
                            <ProtocolBadge protocol={p.protocol} />
                            {!String(p.apiKey || '').trim() && (
                              <span data-id={`providers-left-item-${p.key}-nokey-badge`} className="shrink-0 rounded-md bg-red-500 px-1.5 py-0.5 text-[9px] font-semibold leading-none text-white shadow-sm shadow-red-500/30" title={t('missingApiKey', { defaultValue: '缺少 API key' })}>{t('noKey', { defaultValue: '无 key' })}</span>
                            )}
                          </div>
                          <div className="mt-0.5 flex items-center gap-1.5">
                            <span className="truncate font-mono text-[11px] text-zinc-600">{p.key}</span>
                          </div>
                        </div>
                        {!isProtected && (
                          <span role="button" tabIndex={-1} onClick={(e) => { e.stopPropagation(); void remove(p.key); }}
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
            <div className="border-t border-white/[0.06] p-2 shrink-0">
              <Btn dataId="provider-add" variant={isNew ? 'primary' : 'secondary'} size="md" icon={<Plus size={14} />} className="w-full" onClick={startNew}>{t('addProvider')}</Btn>
            </div>
          </div>
        )}
      </div>
    </div>
  );

  /* ───────────── RIGHT PANEL DETAIL ───────────── */
  const detailUI = (
    <div data-id="providers-detail-root" className="absolute inset-0 z-30 flex flex-col bg-[#0A0A0A] text-zinc-300 overflow-hidden">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-white/[0.06] px-4">
        <h1 className="truncate text-[14px] font-semibold text-white">{isNew ? t('newProvider') : (selectedProvider?.name || selectedProvider?.key)}</h1>
        {!isNew && <ProtocolBadge protocol={draft.protocol} />}
        {dirty && <span className="inline-flex items-center gap-1 text-[11px] text-amber-300"><span className="h-1.5 w-1.5 rounded-full bg-amber-400" />{t('unsaved')}</span>}
        <div className="flex-1" />
      </header>
      <main className="flex-1 overflow-auto">
        <div className="mx-auto max-w-[680px] px-8 py-7">
          <div className="space-y-7">
            {/* Basic info */}
            <section className="space-y-3.5">
              <SectionHeader>{t('sectionBasic')}</SectionHeader>
              <div className="grid gap-3.5 sm:grid-cols-2">
                <Field label={t('fieldName')}>
                  <input data-id="provider-detail-name-input" value={draft.name || ''} onChange={(e) => patchDraft({ name: e.target.value })} className={INPUT} placeholder="2000Run Claude" />
                </Field>
                <Field label="Key" help={isNew ? t('keyHelpNew') : t('keyHelpExisting')}>
                  <input data-id="provider-detail-key-input" value={draft.key} onChange={(e) => patchDraft({ key: e.target.value })} disabled={!isNew} className={cn(INPUT, 'font-mono', !isNew && 'cursor-not-allowed')} placeholder="2000RunClaude" />
                </Field>
              </div>
            </section>

            {/* Models */}
            <section className="space-y-3.5">
              <SectionHeader>{t('sectionModels')}</SectionHeader>
              {(() => {
                const availableModels = modelsText.split('\n').map((l) => l.trim()).filter(Boolean);
                const currentDefault = draft.defaultModel || '';
                return (
                  <Field label={t('fieldDefaultModel')} help={t('fieldDefaultModelHelp')}>
                    <Select
                      searchable
                      placeholder={t('selectModel')}
                      dataId="provider-detail-default-model-select" value={availableModels.includes(currentDefault) ? currentDefault : ''}
                      options={availableModels.map((m) => ({ value: m, label: m }))}
                      onChange={(v) => patchDraft({ defaultModel: v })}
                    />
                  </Field>
                );
              })()}
              <Field label={t('fieldAvailableModels')} help={t('fieldAvailableModelsHelp')}>
                <textarea data-id="provider-detail-models-input" value={modelsText} onChange={(e) => setModelsText(e.target.value)} rows={3} className={cn(INPUT, 'h-auto resize-y py-2 font-mono leading-relaxed')} placeholder={'gpt-5.5\ngpt-5.4'} />
              </Field>
              <Field label={t('fieldModelMapping')} help={t('fieldModelMappingHelp')} className="hidden">
                {(() => {
                  const availableModels = modelsText.split('\n').map((l) => l.trim()).filter(Boolean);
                  // Friendly "from" suggestions: catch-all, family prefixes, common
                  // Anthropic/OpenAI model ids, plus this provider's own models.
                  const fromSuggest: SelectOption[] = [
                    { value: 'claude-opus*', label: t('mappingAllOpus') },
                    { value: 'claude-sonnet*', label: t('mappingAllSonnet') },
                    { value: 'claude-haiku*', label: t('mappingAllHaiku') },
                    ...['claude-opus-4-8', 'claude-opus-4-7', 'claude-opus-4-6', 'claude-sonnet-4-6', 'claude-sonnet-4-5', 'claude-haiku-4-5-20251001', 'gpt-5.5', 'gpt-5.4'].map((m) => ({ value: m, label: m })),
                    ...availableModels.map((m) => ({ value: m, label: m })),
                  ].filter((o, i, arr) => arr.findIndex((x) => x.value === o.value) === i);
                  const toOptions: SelectOption[] = availableModels.map((m) => ({ value: m, label: m }));
                  const setRow = (idx: number, patch: Partial<MappingRow>) =>
                    setMappingRows((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r)));
                  const removeRow = (idx: number) => setMappingRows((prev) => prev.filter((_, i) => i !== idx));
                  return (
                    <div data-id="provider-model-mapping" className="space-y-2">
                      {mappingRows.length === 0 ? (
                        <div className="rounded-lg border border-dashed border-white/[0.08] px-3 py-2.5 text-[12px] text-zinc-600">{t('mappingEmpty')}</div>
                      ) : mappingRows.map((row, idx) => (
                        <div key={idx} data-id={`provider-model-mapping-row-${idx}`} className="flex items-center gap-2">
                          <div className="min-w-0 flex-1">
                            <Select searchable allowCustom placeholder={t('mappingFromPlaceholder')} value={row.from}
                              options={fromSuggest} onChange={(v) => setRow(idx, { from: v })} />
                          </div>
                          <span className="shrink-0 text-zinc-600">→</span>
                          <div className="min-w-0 flex-1">
                            <Select searchable allowCustom placeholder={t('mappingToPlaceholder')} value={row.to}
                              options={toOptions} onChange={(v) => setRow(idx, { to: v })} />
                          </div>
                          <button type="button" data-id={`provider-model-mapping-remove-${idx}`} onClick={() => removeRow(idx)}
                            className="shrink-0 flex h-8 w-8 items-center justify-center rounded-lg text-zinc-500 transition-colors hover:bg-red-500/10 hover:text-red-300" title={t('mappingRemove')}>
                            <Trash2 size={14} />
                          </button>
                        </div>
                      ))}
                      <button type="button" data-id="provider-model-mapping-add" onClick={() => setMappingRows((prev) => [...prev, { from: '', to: '' }])}
                        className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-white/[0.03] px-2.5 py-1.5 text-[12px] text-zinc-300 transition-colors hover:bg-white/[0.06]">
                        <Plus size={13} /> {t('mappingAdd')}
                      </button>
                    </div>
                  );
                })()}
              </Field>
            </section>

            {/* Access */}
            <section className="space-y-3.5">
              <SectionHeader>{t('sectionAccess')}</SectionHeader>
              <Field label="API Base URL" help={t('fieldApiBaseHelp')}>
                <div className="relative">
                  <Link2 size={13} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600" />
                  <input data-id="provider-detail-url-input" value={draft.url || ''} onChange={(e) => patchDraft({ url: e.target.value })} className={cn(INPUT, 'pl-8 font-mono')} placeholder="https://api.2000.run/v1" />
                </div>
              </Field>
              <div className="grid gap-3.5 sm:grid-cols-[150px_1fr]">
                <Field label={t('fieldProtocol')}>
                  <Select
                    dataId="provider-protocol-select"
                    className="w-full"
                    disabled={!isNew}
                    value={proto(draft) || 'openai'}
                    onChange={(v) => patchDraft({ protocol: v })}
                    options={PROTOCOLS.map((p) => ({ value: p, label: p }))}
                  />
                </Field>
                <Field label="API Key" help={(draft.url || '').toLowerCase().includes('deepseek.com') ? (
                  <button data-id="provider-detail-get-apikey" type="button"
                    onClick={() => window.open('https://platform.deepseek.com/api_keys', '_blank', 'noopener')}
                    className="inline-flex items-center gap-1 text-[11px] font-medium text-blue-400 transition-colors hover:text-blue-300">
                    {t('getDeepSeekApiKey', { defaultValue: '前往 DeepSeek 开放平台获取 API Key' })}
                    <ExternalLink size={11} />
                  </button>
                ) : undefined}>
                  <div className="relative">
                    {/* type="text" so Chrome's password manager never prompts to save */}
                    <input
                      data-id="provider-detail-apikey-input" type="text" name="cicy-provider-api-key" value={draft.apiKey || ''} onChange={(e) => patchDraft({ apiKey: e.target.value })}
                      className={cn(INPUT, 'pr-9 font-mono')} placeholder="sk-…" autoComplete="off" autoCapitalize="off" autoCorrect="off" spellCheck={false}
                      data-1p-ignore data-lpignore="true"
                      style={showApiKey ? undefined : ({ WebkitTextSecurity: 'disc' } as React.CSSProperties)}
                    />
                    <button data-id="provider-detail-apikey-toggle" type="button" onClick={() => setShowApiKey((s) => !s)} className="absolute right-1.5 top-1/2 grid h-6 w-6 -translate-y-1/2 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300" title={showApiKey ? t('hide') : t('show')}>
                      {showApiKey ? <EyeOff size={13} /> : <Eye size={13} />}
                    </button>
                  </div>
                </Field>
              </div>
            </section>

            {/* test result */}
            {testResult && (
              <div className={cn('relative rounded-xl border px-3.5 py-3 text-[12px]',
                testResult.ok ? 'border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-200' : 'border-red-500/25 bg-red-500/[0.07] text-red-200')}>
                <button data-id="provider-detail-test-result-close" onClick={() => setTestResult(null)} className="absolute right-2.5 top-2.5 text-current opacity-50 transition-opacity hover:opacity-100"><X size={12} /></button>
                <div className="flex items-center gap-1.5 pr-5 font-medium">
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
          <div className="mt-7 border-t border-white/[0.06] pt-5">
            <div className="flex flex-wrap items-center gap-2">
            <Btn dataId="provider-detail-save" variant="primary" size="md" icon={<Save size={14} />} busy={saving} disabled={saving || !canSave} onClick={() => void save()}>{isNew ? t('create') : t('save')}</Btn>
            {!isNew && <Btn dataId="provider-detail-test-connection" variant="secondary" size="md" icon={<Zap size={14} />} disabled={modelsText.split('\n').map((l) => l.trim()).filter(Boolean).length === 0} onClick={() => { setModelTestResults({}); setTestModalOpen(true); }}>{t('test')}</Btn>}
            {isNew && <Btn dataId="provider-detail-cancel" variant="ghost" size="md" onClick={() => { void guardDirty(() => { setIsNew(false); setSelectedKey(items[0]?.key || null); }); }}>{t('cancel')}</Btn>}
            {!isNew && dirty && <Btn dataId="provider-detail-discard" variant="ghost" size="md" onClick={() => loadEditor(selectedProvider)}>{t('discardChanges')}</Btn>}
            </div>
            <div className="mt-2.5 flex items-center gap-1.5 text-[11px] leading-snug text-zinc-600">
              <Cpu size={11} className="shrink-0" /> {t('footnoteSave')}
            </div>
          </div>
        </div>
      </main>
    </div>
  );

  /* ───────────── TEST MODAL ───────────── */
  let testModal: React.ReactNode = null;
  if (testModalOpen) {
    const availableModels = modelsText.split('\n').map((l) => l.trim()).filter(Boolean);
    const isDone = (r: TestResult | 'pending' | undefined): r is TestResult => !!r && r !== 'pending';
    const tested = availableModels.filter((m) => isDone(modelTestResults[m])).length;
    const passed = availableModels.filter((m) => { const r = modelTestResults[m]; return isDone(r) && r.ok; }).length;
    testModal = (
      <div data-id="provider-test-modal" className="fixed inset-0 z-[10000] cursor-pointer" onClick={() => setTestModalOpen(false)}>
        <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
        <div className="absolute right-0 top-0 h-full w-[680px] max-w-[94vw] cursor-default flex flex-col overflow-hidden border-l border-white/[0.08] bg-[#161618] shadow-[0_0_50px_rgba(0,0,0,0.5)] animate-drawer-in"
          onClick={(e) => e.stopPropagation()}>
          <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
            <div className="flex items-center gap-2">
              <Zap className="w-4 h-4 text-zinc-400" />
              <h2 className="text-[15px] font-semibold text-white">{t('testModalTitle')}</h2>
            </div>
            <button data-id="provider-test-modal-close" onClick={() => setTestModalOpen(false)} className="p-1.5 rounded-lg text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.06] transition-colors cursor-pointer">
              <X className="w-4 h-4" />
            </button>
          </div>
          <div className="px-5 py-3 border-b border-white/[0.06] text-[11px] text-zinc-500 font-mono truncate">
            {draft.url} · {proto(draft) || 'openai'}
          </div>
          {availableModels.length === 0 ? (
            <div className="px-5 py-10 text-center text-[12px] text-zinc-600">{t('noModelsForTest')}</div>
          ) : (
            <>
              {/* toolbar: test-all + summary */}
              <div className="flex items-center gap-3 px-5 py-3 border-b border-white/[0.06]">
                <button data-id="provider-test-all" type="button" disabled={testingAll} onClick={() => void testAllModels()}
                  className={cn('inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-[12px] font-medium transition-colors',
                    'border-white/[0.1] bg-white/[0.04] text-zinc-200 hover:bg-white/[0.08] hover:border-white/[0.16]',
                    'disabled:cursor-not-allowed disabled:opacity-50')}>
                  {testingAll ? <Spinner size="xs" /> : <Zap size={13} />}
                  {testingAll ? t('testing') : t('testAll', { defaultValue: 'Test all' })}
                </button>
                <span data-id="provider-test-summary" className="text-[11px] text-zinc-500">
                  {tested > 0
                    ? `${passed}/${availableModels.length} ${t('testPassed', { defaultValue: 'passed' })}`
                    : `${availableModels.length} ${t('testModels', { defaultValue: 'models' })}`}
                </span>
              </div>
              {/* per-model result table */}
              <div className="flex-1 overflow-auto">
                <table data-id="provider-test-table" className="w-full text-[12px]">
                  <thead className="sticky top-0 bg-[#161618]">
                    <tr className="border-b border-white/[0.06] text-[10px] uppercase tracking-wider text-zinc-600">
                      <th className="px-5 py-2 text-left font-medium">{t('testColModel', { defaultValue: 'Model' })}</th>
                      <th className="px-3 py-2 text-left font-medium">{t('testColStatus', { defaultValue: 'Status' })}</th>
                      <th className="px-5 py-2 text-right font-medium" />
                    </tr>
                  </thead>
                  <tbody>
                    {availableModels.map((m) => {
                      const r = modelTestResults[m];
                      const pending = r === 'pending';
                      const res = isDone(r) ? r : undefined;
                      return (
                        <tr key={m} data-id={`provider-test-row-${m}`} className="border-b border-white/[0.04] hover:bg-white/[0.02]">
                          <td className="px-5 py-2.5 font-mono text-zinc-200 max-w-[220px] truncate" title={m}>{m}</td>
                          <td className="px-3 py-2.5">
                            {pending ? (
                              <span className="inline-flex items-center gap-1.5 text-zinc-500"><Spinner size="xs" />{t('testing')}</span>
                            ) : !res ? (
                              <span className="text-zinc-600">—</span>
                            ) : res.ok ? (
                              <span className="inline-flex items-center gap-1.5 text-emerald-300">
                                <Check size={13} />
                                {typeof res.status === 'number' && <span className="opacity-70">HTTP {res.status}</span>}
                                {typeof res.duration_ms === 'number' && <span className="opacity-60">· {res.duration_ms}ms</span>}
                              </span>
                            ) : (
                              <span className="inline-flex max-w-[240px] items-center gap-1.5 text-red-300" title={res.detail || ''}>
                                <X size={13} className="shrink-0" />
                                <span className="truncate">{res.detail || t('testFail')}</span>
                              </span>
                            )}
                          </td>
                          <td className="px-5 py-2.5 text-right">
                            <button data-id={`provider-test-run-${m}`} type="button" disabled={pending}
                              onClick={() => void testSingleModel(m)} title={t('test')}
                              className="inline-flex h-6 w-6 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.08] hover:text-zinc-200 disabled:opacity-40">
                              {pending ? <Spinner size="xs" /> : <Zap size={12} />}
                            </button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </div>
      </div>
    );
  }

  // Suppress unused warnings for state hooks read only inside helpers above
  void runTest; void testing;

  return (
    <>
      {leftMount && createPortal(leftPanelUI, leftMount)}
      {rightMount && detailOpen && createPortal(detailUI, rightMount)}
      {testModal && createPortal(testModal, document.body)}
      {createPortal(dialogsNode, document.body)}
    </>
  );
}
