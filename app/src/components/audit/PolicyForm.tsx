// PolicyForm — structured editor for audit policy. Production-grade UI.
//
// Cut C goal: feel commercial. Polished primitives, sticky toolbar, dirty
// state, keyboard shortcut, anchor nav, severity-color-coded badges,
// empty states with CTAs, smooth transitions, confirm modals.

import { useState, useCallback, useMemo, useRef, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ChevronDown, ChevronRight, Plus, X, AlertTriangle, Shield, Mail, Filter,
  Database, Bell, FileLock2, Sparkles, Inbox,
} from 'lucide-react';

// ── Type shapes mirroring api/mgr/audit/policy.go ──────────────────────────

export type Severity = 'low' | 'medium' | 'high' | 'critical';
export type Action = 'log' | 'notify' | 'redact' | 'block' | 'none';

export interface RuleOverride {
  id: string;
  disabled?: boolean;
  severity?: Severity;
  default_action?: Action;
}

export interface AllowList {
  agents?: string[];
  paths?: string[];
  content_hashes?: string[];
}

export interface ResponsiblePersonsConfig {
  default?: string[];
  by_severity?: Record<string, string[]>;
  by_agent?: Record<string, string[]>;
  by_user?: Record<string, string[]>;
  by_rule?: Record<string, string[]>;
}

export interface NotifyConfig {
  min_severity?: Severity;
  rate_limit?: { window_seconds?: number; max_per_agent_per_rule?: number };
  cooldown?: { seconds?: number };
  channels?: any[];
  suspended?: boolean;
}

export interface PreventiveConfig {
  enabled?: boolean;
  fail_mode?: 'open' | 'closed';
}

export interface AIRemediationConfig {
  enabled?: boolean;
  endpoint?: string;
  model?: string;
  api_key?: string;
  max_tokens?: number;
  timeout_seconds?: number;
}

export interface IncidentResponseConfig {
  enabled?: boolean;
  trigger_min_severity?: Severity;
  cooldown_seconds?: number;
  email_template?: string;
  email_from?: string;
  languages?: string[];
  ai_remediation?: AIRemediationConfig;
}

export interface CustomRule {
  id: string;
  label?: string;
  category?: string;
  severity: Severity;
  scan_directions: string[];
  inline?: boolean;
  default_action?: Action;
  match: { type: 'regex' | 'dict_file'; pattern?: string; flags?: string; path?: string };
}

export interface Policy {
  version?: number;
  enabled?: boolean;
  fail_mode?: 'open' | 'closed';
  rules_override?: RuleOverride[];
  custom_rules?: CustomRule[];
  allow_list?: AllowList;
  notify?: NotifyConfig;
  preventive?: PreventiveConfig;
  incident_response?: IncidentResponseConfig;
  responsible_persons?: ResponsiblePersonsConfig;
}

export interface AgentOverride {
  rules_override?: RuleOverride[];
  allow_list?: { paths?: string[]; content_hashes?: string[] };
  responsible_persons?: ResponsiblePersonsConfig;
}

// ── Builtin rule list ─────────────────────────────────────────────────────

interface BuiltinRuleMeta {
  id: string;
  severity: Severity;
  inline: boolean;
  default_action: Action;
}
export const BUILTIN_RULES: BuiltinRuleMeta[] = [
  { id: 'secret.private_key',   severity: 'high',   inline: true,  default_action: 'block'  },
  { id: 'secret.aws_akid',      severity: 'high',   inline: true,  default_action: 'redact' },
  { id: 'secret.aws_secret',    severity: 'high',   inline: true,  default_action: 'redact' },
  { id: 'secret.jwt',           severity: 'medium', inline: false, default_action: 'log'    },
  { id: 'secret.bearer_token',  severity: 'medium', inline: false, default_action: 'log'    },
  { id: 'secret.high_entropy',  severity: 'medium', inline: false, default_action: 'log'    },
  { id: 'pii.id_card_cn',       severity: 'medium', inline: false, default_action: 'log'    },
  { id: 'pii.bank_card',        severity: 'medium', inline: false, default_action: 'log'    },
  { id: 'pii.phone_cn',         severity: 'low',    inline: false, default_action: 'log'    },
  { id: 'network.private_ip',   severity: 'low',    inline: false, default_action: 'log'    },
];

const SEVERITY_OPTIONS: Severity[] = ['low', 'medium', 'high', 'critical'];
const ACTION_OPTIONS: Action[] = ['log', 'notify', 'redact', 'block', 'none'];

// ── UI Primitives ─────────────────────────────────────────────────────────

/** iOS-style toggle. ~36×20, color reflects state + intent. */
function Toggle({
  checked, onChange, disabled, intent = 'primary',
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  intent?: 'primary' | 'danger';
}) {
  const onColor = intent === 'danger' ? 'bg-red-500' : 'bg-blue-500';
  return (
    <button type="button" role="switch" aria-checked={checked} disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500/40
        ${disabled ? 'opacity-40 cursor-not-allowed' : ''}
        ${checked ? onColor : 'bg-zinc-700'}`}>
      <span aria-hidden="true"
        className={`pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out
          ${checked ? 'translate-x-4' : 'translate-x-0.5'}`} />
    </button>
  );
}

const SEVERITY_TONE: Record<string, string> = {
  critical: 'bg-red-500/15 text-red-300 border-red-500/30',
  high:     'bg-orange-500/15 text-orange-300 border-orange-500/30',
  medium:   'bg-amber-500/15 text-amber-300 border-amber-500/30',
  low:      'bg-zinc-500/15 text-zinc-300 border-zinc-500/30',
};

/** Color-coded severity chip. */
function SeverityChip({ value, size = 'sm' }: { value?: string; size?: 'sm' | 'md' }) {
  const tone = SEVERITY_TONE[(value || '').toLowerCase()] || 'bg-zinc-700/20 text-zinc-400 border-zinc-700';
  const pad = size === 'md' ? 'px-2 py-0.5 text-[11px]' : 'px-1.5 py-0.5 text-[10px]';
  return (
    <span className={`inline-flex items-center font-medium uppercase tracking-wider rounded border ${pad} ${tone}`}>
      {value || '—'}
    </span>
  );
}

const ACTION_TONE: Record<string, string> = {
  block:  'bg-red-500/10 text-red-300 border-red-500/25',
  redact: 'bg-orange-500/10 text-orange-300 border-orange-500/25',
  notify: 'bg-amber-500/10 text-amber-300 border-amber-500/25',
  log:    'bg-zinc-600/15 text-zinc-300 border-zinc-600/30',
  none:   'bg-zinc-700/15 text-zinc-400 border-zinc-700/30',
};
function ActionChip({ value }: { value?: string }) {
  const tone = ACTION_TONE[value || ''] || 'bg-zinc-700/15 text-zinc-400 border-zinc-700/30';
  return (
    <span className={`inline-flex items-center px-1.5 py-0.5 text-[10px] font-medium rounded border ${tone}`}>
      {value || '—'}
    </span>
  );
}

/** Section card with collapsible header + counter badge + icon. */
function SectionCard({
  id, title, icon: Icon, counter, hint, defaultOpen = true, children,
}: {
  id: string;
  title: string;
  icon?: any;
  counter?: number | string;
  hint?: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <section data-id={`audit-policy-section-${id}`}
      id={`audit-policy-section-${id}`}
      className="rounded-lg border border-zinc-800/80 bg-zinc-900/40 shadow-sm overflow-hidden scroll-mt-20">
      <button type="button" onClick={() => setOpen(o => !o)}
        className="w-full flex items-center gap-2.5 px-4 py-3 hover:bg-zinc-800/40 transition-colors text-left">
        <span className={`flex h-7 w-7 items-center justify-center rounded-md transition-transform duration-150 ${open ? 'rotate-0' : '-rotate-90'} text-zinc-500`}>
          <ChevronDown size={14} />
        </span>
        {Icon && (
          <span className="flex h-7 w-7 items-center justify-center rounded-md bg-blue-500/10 text-blue-300">
            <Icon size={14} />
          </span>
        )}
        <span className="text-sm font-medium text-zinc-100">{title}</span>
        {counter !== undefined && counter !== 0 && (
          <span className="inline-flex items-center px-1.5 py-0.5 text-[10px] font-medium rounded-full bg-zinc-700/40 text-zinc-300">
            {counter}
          </span>
        )}
        {hint && <span className="ml-auto text-[11px] text-zinc-500">{hint}</span>}
      </button>
      <div className={`grid transition-[grid-template-rows] duration-200 ease-out ${open ? 'grid-rows-[1fr]' : 'grid-rows-[0fr]'}`}>
        <div className="overflow-hidden">
          <div className="px-4 pb-4 pt-1 space-y-4 border-t border-zinc-800/60">
            {children}
          </div>
        </div>
      </div>
    </section>
  );
}

/** Label + control + hint + error row. */
function FormRow({
  label, hint, error, children, className = '',
}: {
  label: string;
  hint?: string;
  error?: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={`grid grid-cols-[180px_1fr] gap-x-4 gap-y-0.5 items-start ${className}`}>
      <div className="pt-1.5">
        <label className="text-xs font-medium text-zinc-300">{label}</label>
        {hint && <p className="mt-1 text-[10px] text-zinc-500 leading-relaxed">{hint}</p>}
      </div>
      <div>
        {children}
        {error && (
          <p className="mt-1 text-[10px] text-red-300 flex items-center gap-1">
            <AlertTriangle size={10} />
            {error}
          </p>
        )}
      </div>
    </div>
  );
}

/** Switch row: toggle + label + danger styling option. */
function ToggleRow({
  label, hint, checked, onChange, disabled, danger,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  danger?: boolean;
}) {
  return (
    <div className={`flex items-start gap-3 p-3 rounded-md border transition-colors
      ${checked && danger ? 'border-red-500/30 bg-red-500/5' :
        checked ? 'border-blue-500/30 bg-blue-500/5' :
        'border-zinc-800/60 bg-zinc-900/30 hover:bg-zinc-800/30'}`}>
      <Toggle checked={checked} onChange={onChange} disabled={disabled} intent={danger ? 'danger' : 'primary'} />
      <div className="flex-1 min-w-0">
        <div className={`text-xs font-medium ${checked && danger ? 'text-red-200' : 'text-zinc-100'}`}>{label}</div>
        {hint && <p className="mt-0.5 text-[10px] text-zinc-500 leading-relaxed">{hint}</p>}
      </div>
    </div>
  );
}

const inputClass =
  'text-xs px-2.5 py-1.5 rounded-md bg-zinc-900/60 border border-zinc-800 text-zinc-100 ' +
  'placeholder:text-zinc-600 focus:outline-none focus:ring-2 focus:ring-blue-500/30 ' +
  'focus:border-blue-500/40 transition-colors disabled:opacity-50 disabled:cursor-not-allowed';

function TextInput(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`${inputClass} ${props.className || ''}`} />;
}

function SelectInput(props: React.SelectHTMLAttributes<HTMLSelectElement> & { children: React.ReactNode }) {
  const { children, ...rest } = props;
  return (
    <select {...rest} className={`${inputClass} pr-7 cursor-pointer ${rest.className || ''}`}>
      {children}
    </select>
  );
}

/** ChipInput — comma / enter / blur all add; smooth slide-in. */
function ChipInput({
  values, onChange, placeholder, readOnly, tone = 'blue',
}: {
  values: string[] | undefined;
  onChange: (next: string[]) => void;
  placeholder?: string;
  readOnly?: boolean;
  tone?: 'blue' | 'gray';
}) {
  const [draft, setDraft] = useState('');
  const list = values || [];
  const inputRef = useRef<HTMLInputElement>(null);

  const commit = useCallback((s: string) => {
    const t = s.trim();
    if (!t) return false;
    if (list.includes(t)) return true;
    onChange([...list, t]);
    return true;
  }, [list, onChange]);

  const remove = useCallback((idx: number) => {
    onChange(list.filter((_, j) => j !== idx));
  }, [list, onChange]);

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      if (commit(draft)) setDraft('');
    } else if (e.key === 'Backspace' && !draft && list.length > 0) {
      remove(list.length - 1);
    }
  };

  const onBlur = () => {
    if (draft.trim()) {
      if (commit(draft)) setDraft('');
    }
  };

  const chipBase = tone === 'blue'
    ? 'bg-blue-500/10 border-blue-500/30 text-blue-200'
    : 'bg-zinc-700/30 border-zinc-700 text-zinc-300';

  return (
    <div className={`flex flex-wrap gap-1.5 px-2 py-1.5 rounded-md border bg-zinc-900/60 border-zinc-800 ${readOnly ? 'opacity-90' : 'focus-within:border-blue-500/40 focus-within:ring-2 focus-within:ring-blue-500/20'} transition-colors`}>
      {list.map((v, i) => (
        <span key={`${v}-${i}`} className={`inline-flex items-center gap-1 px-2 py-0.5 text-[11px] font-mono rounded border ${chipBase}`}>
          <span className="truncate max-w-[280px]">{v}</span>
          {!readOnly && (
            <button type="button" onClick={() => remove(i)}
              className="hover:text-red-300 transition-colors"
              aria-label={`remove ${v}`}>
              <X size={10} />
            </button>
          )}
        </span>
      ))}
      {!readOnly && (
        <input ref={inputRef} value={draft} onChange={e => setDraft(e.target.value)}
          onKeyDown={onKey} onBlur={onBlur}
          placeholder={list.length === 0 ? (placeholder || 'value…') : ''}
          className="flex-1 min-w-[120px] bg-transparent text-xs text-zinc-100 placeholder:text-zinc-600 focus:outline-none font-mono" />
      )}
    </div>
  );
}

/** Empty state with icon + helper text + optional CTA. */
function EmptyState({ icon: Icon = Inbox, label, hint }: { icon?: any; label: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-6 px-4 rounded-md border border-dashed border-zinc-800 bg-zinc-900/20 text-center">
      <Icon size={20} className="text-zinc-600 mb-2" />
      <div className="text-xs text-zinc-400">{label}</div>
      {hint && <div className="mt-1 text-[10px] text-zinc-600">{hint}</div>}
    </div>
  );
}

/** Keyed chip map editor — key on the left, chip list on the right. */
function KeyedChipMapEditor({
  value, onChange, keyPlaceholder, valuePlaceholder, readOnly,
}: {
  value: Record<string, string[]> | undefined;
  onChange: (v: Record<string, string[]>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  readOnly?: boolean;
}) {
  const [newKey, setNewKey] = useState('');
  const m = value || {};
  const keys = Object.keys(m).sort();
  const addKey = () => {
    const k = newKey.trim();
    if (!k || m[k]) { setNewKey(''); return; }
    onChange({ ...m, [k]: [] });
    setNewKey('');
  };
  return (
    <div className="flex flex-col gap-2">
      {keys.length === 0 && <EmptyState label={keyPlaceholder || 'No entries'} hint={readOnly ? undefined : 'Add a key below.'} />}
      {keys.map(k => (
        <div key={k} className="grid grid-cols-[180px_1fr_auto] gap-2 items-start">
          <code className="px-2 py-1.5 text-[11px] text-zinc-300 font-mono bg-zinc-800/40 rounded-md border border-zinc-800 truncate">{k}</code>
          <ChipInput values={m[k]} placeholder={valuePlaceholder || 'recipient…'} readOnly={readOnly}
            onChange={next => {
              const cp = { ...m };
              if (next.length === 0) delete cp[k];
              else cp[k] = next;
              onChange(cp);
            }} />
          {!readOnly && (
            <button type="button"
              onClick={() => { const cp = { ...m }; delete cp[k]; onChange(cp); }}
              className="self-center p-1 text-zinc-500 hover:text-red-400 transition-colors"
              aria-label="remove">
              <X size={12} />
            </button>
          )}
        </div>
      ))}
      {!readOnly && (
        <div className="flex items-center gap-2">
          <TextInput value={newKey} onChange={e => setNewKey(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addKey(); } }}
            placeholder={keyPlaceholder || 'add key (press Enter)…'}
            className="font-mono w-[180px]" />
          <button type="button" onClick={addKey}
            className="inline-flex items-center gap-1 px-2 py-1.5 text-[11px] rounded-md border border-zinc-800 bg-zinc-900/60 text-zinc-300 hover:bg-zinc-800/60 transition-colors">
            <Plus size={11} /> add row
          </button>
        </div>
      )}
    </div>
  );
}

// ── Main form ────────────────────────────────────────────────────────────

export type FormScope = 'global' | 'agent' | 'effective';

interface PolicyFormProps {
  scope: FormScope;
  policy: Policy | AgentOverride;
  onChange: (next: Policy | AgentOverride) => void;
  readOnly?: boolean;
}

export function PolicyForm({ scope, policy, onChange, readOnly }: PolicyFormProps) {
  const { t } = useTranslation('audit');
  const isAgent = scope === 'agent';
  const isEffective = scope === 'effective';
  const ro = readOnly || isEffective;

  const p = policy as Policy & AgentOverride;

  const set = useCallback((patch: any) => {
    if (ro) return;
    onChange({ ...p, ...patch });
  }, [p, onChange, ro]);

  const overrideMap = useMemo(() => {
    const m: Record<string, RuleOverride> = {};
    (p.rules_override || []).forEach(r => { m[r.id] = r; });
    return m;
  }, [p.rules_override]);

  const setRuleOverride = (id: string, patch: Partial<RuleOverride>) => {
    const existing = overrideMap[id] || { id };
    const next: RuleOverride = { ...existing, ...patch };
    const isNoop = !next.disabled && !next.severity && !next.default_action;
    const others = (p.rules_override || []).filter(r => r.id !== id);
    if (isNoop) set({ rules_override: others.length ? others : undefined });
    else set({ rules_override: [...others, next] });
  };

  // Counters for section badges
  const overrideCount = (p.rules_override || []).length;
  const allowListCount =
    (p.allow_list?.agents?.length || 0) +
    (p.allow_list?.paths?.length || 0) +
    (p.allow_list?.content_hashes?.length || 0);
  const responsibleCount =
    (p.responsible_persons?.default?.length || 0) +
    Object.keys(p.responsible_persons?.by_severity || {}).length +
    Object.keys(p.responsible_persons?.by_agent || {}).length +
    Object.keys(p.responsible_persons?.by_user || {}).length +
    Object.keys(p.responsible_persons?.by_rule || {}).length;
  const customRuleCount = (p.custom_rules || []).length;

  return (
    <div data-id="audit-policy-form" className="space-y-3">
      {isAgent && (
        <div className="flex items-start gap-2.5 p-3 rounded-md border border-blue-500/25 bg-blue-500/5 text-[11px] text-blue-200">
          <AlertTriangle size={14} className="shrink-0 mt-0.5" />
          <span className="leading-relaxed">{t('formAgentScopeNote')}</span>
        </div>
      )}

      {/* ── master switches: GLOBAL only ── */}
      {!isAgent && (
        <SectionCard id="master" title={t('formSectionMaster')} icon={Shield} defaultOpen>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5">
            <ToggleRow checked={p.enabled ?? true} onChange={v => set({ enabled: v })}
              disabled={ro} label={t('formEnabled')} hint={t('formEnabledHint')} />
            <ToggleRow checked={p.preventive?.enabled ?? false}
              onChange={v => set({ preventive: { ...p.preventive, enabled: v } })}
              disabled={ro} label={t('formPreventiveEnabled')} hint={t('formPreventiveHint')} />
            <ToggleRow checked={p.incident_response?.enabled ?? false}
              onChange={v => set({ incident_response: { ...p.incident_response, enabled: v } })}
              disabled={ro} label={t('formIncidentEnabled')} hint={t('formIncidentHint')} />
            <ToggleRow checked={p.notify?.suspended ?? false}
              onChange={v => set({ notify: { ...p.notify, suspended: v } })}
              disabled={ro} label={t('formNotifySuspended')} hint={t('formNotifySuspendedHint')} danger />
          </div>
          {p.preventive?.enabled && (
            <FormRow label={t('formPreventiveFailMode')} hint={
              (p.preventive?.fail_mode || 'open') === 'open'
                ? t('formFailOpenHint')
                : t('formFailClosedHint')
            }>
              <div className="flex items-center gap-3">
                {(['open', 'closed'] as const).map(m => (
                  <label key={m}
                    className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium cursor-pointer transition-colors
                      ${(p.preventive?.fail_mode || 'open') === m
                        ? (m === 'open' ? 'bg-blue-500/15 border border-blue-500/40 text-blue-200' : 'bg-red-500/15 border border-red-500/40 text-red-200')
                        : 'bg-zinc-900/40 border border-zinc-800 text-zinc-400 hover:text-zinc-200'}`}>
                    <input type="radio" name="failmode" className="sr-only"
                      checked={(p.preventive?.fail_mode || 'open') === m}
                      disabled={ro}
                      onChange={() => set({ preventive: { ...p.preventive, fail_mode: m } })} />
                    {m}
                  </label>
                ))}
              </div>
            </FormRow>
          )}
        </SectionCard>
      )}

      {/* ── rules_override ── */}
      <SectionCard id="rules" title={t('formSectionRules')} icon={Filter} counter={overrideCount} defaultOpen>
        <div className="overflow-x-auto rounded-md border border-zinc-800/60">
          <table className="w-full text-xs">
            <thead className="text-[10px] text-zinc-500 uppercase tracking-wider bg-zinc-900/40">
              <tr>
                <th className="text-left px-3 py-2 font-medium">{t('formColRule')}</th>
                <th className="text-left px-3 py-2 font-medium w-44">{t('formColSeverity')}</th>
                <th className="text-left px-3 py-2 font-medium w-44">{t('formColAction')}</th>
                <th className="text-left px-3 py-2 font-medium w-20">{t('formColDisabled')}</th>
              </tr>
            </thead>
            <tbody>
              {BUILTIN_RULES.map((r, i) => {
                const ov = overrideMap[r.id];
                const isOverridden = !!ov && (ov.disabled || ov.severity || ov.default_action);
                return (
                  <tr key={r.id}
                    className={`border-t border-zinc-800/60 transition-colors ${isOverridden ? 'bg-blue-500/[0.03]' : i % 2 ? 'bg-zinc-900/20' : ''}`}>
                    <td className="px-3 py-2">
                      <code className="font-mono text-zinc-100 text-[11px]">{r.id}</code>
                      <div className="mt-0.5 flex items-center gap-1.5 text-[10px] text-zinc-500">
                        <SeverityChip value={r.severity} />
                        <ActionChip value={r.default_action} />
                        {r.inline && <span className="text-[10px] text-blue-400">inline</span>}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      {ro ? (
                        <SeverityChip value={ov?.severity || r.severity} />
                      ) : (
                        <SelectInput value={ov?.severity || ''} className="w-full"
                          onChange={e => setRuleOverride(r.id, { severity: (e.target.value || undefined) as Severity })}>
                          <option value="">— default ({r.severity}) —</option>
                          {SEVERITY_OPTIONS.map(s => <option key={s} value={s}>{s}</option>)}
                        </SelectInput>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      {ro ? (
                        <ActionChip value={ov?.default_action || r.default_action} />
                      ) : (
                        <SelectInput value={ov?.default_action || ''} className="w-full"
                          onChange={e => setRuleOverride(r.id, { default_action: (e.target.value || undefined) as Action })}>
                          <option value="">— default ({r.default_action}) —</option>
                          {ACTION_OPTIONS.map(a => <option key={a} value={a}>{a}</option>)}
                        </SelectInput>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <Toggle checked={!!ov?.disabled} disabled={ro} intent="danger"
                        onChange={v => setRuleOverride(r.id, { disabled: v || undefined })} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </SectionCard>

      {/* ── allow_list ── */}
      <SectionCard id="allowlist" title={t('formSectionAllowList')} icon={Database} counter={allowListCount}>
        <div className="space-y-3">
          {!isAgent && (
            <FormRow label="agents" hint={t('formAllowAgents')}>
              <ChipInput values={p.allow_list?.agents} readOnly={ro}
                onChange={next => set({ allow_list: { ...p.allow_list, agents: next } })}
                placeholder="w-trusted-bot" />
            </FormRow>
          )}
          <FormRow label="paths" hint={t('formAllowPaths')}>
            <ChipInput values={p.allow_list?.paths} readOnly={ro}
              onChange={next => set({ allow_list: { ...p.allow_list, paths: next } })}
              placeholder="mitm:flow-known-fp-" />
          </FormRow>
          <FormRow label="content_hashes" hint={t('formAllowHashes')}>
            <ChipInput values={p.allow_list?.content_hashes} readOnly={ro}
              onChange={next => set({ allow_list: { ...p.allow_list, content_hashes: next } })}
              placeholder="sha256:..." />
          </FormRow>
        </div>
      </SectionCard>

      {/* ── responsible_persons ── */}
      <SectionCard id="responsible" title={t('formSectionResponsible')} icon={Mail} counter={responsibleCount}>
        <div className="space-y-4">
          <FormRow label="default" hint={t('formRespDefault')}>
            <ChipInput values={p.responsible_persons?.default} readOnly={ro}
              onChange={next => set({ responsible_persons: { ...p.responsible_persons, default: next } })}
              placeholder="sec@yourcorp.com" />
          </FormRow>
          {(['by_severity', 'by_rule', isAgent ? null : 'by_agent', isAgent ? null : 'by_user'].filter(Boolean) as string[]).map(k => (
            <FormRow key={k} label={k} hint={t(`formResp_${k}`)}>
              <KeyedChipMapEditor
                value={(p.responsible_persons as any)?.[k]} readOnly={ro}
                onChange={next => set({ responsible_persons: { ...p.responsible_persons, [k]: next } })}
                keyPlaceholder={t(`formResp_${k}_key`)}
                valuePlaceholder="email@corp"
              />
            </FormRow>
          ))}
        </div>
      </SectionCard>

      {/* ── incident_response — GLOBAL only ── */}
      {!isAgent && (
        <SectionCard id="incident" title={t('formSectionIncident')} icon={Mail}>
          <div className="space-y-4">
            <FormRow label={t('formIncidentTrigger')} hint={t('formIncidentTriggerHint')}>
              <SelectInput value={p.incident_response?.trigger_min_severity || ''}
                onChange={e => set({ incident_response: { ...p.incident_response, trigger_min_severity: (e.target.value || undefined) as Severity } })}>
                <option value="">— default (high) —</option>
                {SEVERITY_OPTIONS.map(s => <option key={s} value={s}>{s}</option>)}
              </SelectInput>
            </FormRow>
            <FormRow label={t('formIncidentCooldown')} hint={t('formIncidentCooldownHint')}>
              <div className="flex items-center gap-2">
                <TextInput type="number" min={0} value={p.incident_response?.cooldown_seconds ?? ''}
                  onChange={e => set({ incident_response: { ...p.incident_response, cooldown_seconds: Number(e.target.value || 0) } })}
                  className="w-32" />
                <span className="text-[10px] text-zinc-500">seconds</span>
              </div>
            </FormRow>
            <FormRow label={t('formIncidentFrom')} hint={t('formIncidentFromHint')}>
              <TextInput value={p.incident_response?.email_from || ''}
                onChange={e => set({ incident_response: { ...p.incident_response, email_from: e.target.value || undefined } })}
                placeholder="audit@yourcorp.com" className="w-full max-w-md" />
            </FormRow>
          </div>

          <div className="pt-3 mt-3 border-t border-zinc-800/60">
            <ToggleRow
              checked={p.incident_response?.ai_remediation?.enabled ?? false}
              onChange={v => set({
                incident_response: {
                  ...p.incident_response,
                  ai_remediation: { ...p.incident_response?.ai_remediation, enabled: v },
                },
              })}
              disabled={ro}
              label={t('formAIEnabled')}
              hint={t('formAIEnabledHint')}
            />
            {p.incident_response?.ai_remediation?.enabled && (
              <div className="mt-3 pl-12 space-y-3">
                <FormRow label={t('formAIEndpoint')}>
                  <TextInput value={p.incident_response.ai_remediation.endpoint || ''}
                    onChange={e => set({
                      incident_response: {
                        ...p.incident_response,
                        ai_remediation: { ...p.incident_response?.ai_remediation, endpoint: e.target.value },
                      },
                    })}
                    placeholder="https://internal-llm.corp/v1"
                    className="w-full max-w-md" />
                </FormRow>
                <FormRow label={t('formAIModel')}>
                  <TextInput value={p.incident_response.ai_remediation.model || ''}
                    onChange={e => set({
                      incident_response: {
                        ...p.incident_response,
                        ai_remediation: { ...p.incident_response?.ai_remediation, model: e.target.value },
                      },
                    })}
                    placeholder="internal-fast"
                    className="w-full max-w-md" />
                </FormRow>
                <FormRow label={t('formAIKey')}>
                  <TextInput type="password" value={p.incident_response.ai_remediation.api_key || ''}
                    onChange={e => set({
                      incident_response: {
                        ...p.incident_response,
                        ai_remediation: { ...p.incident_response?.ai_remediation, api_key: e.target.value },
                      },
                    })}
                    placeholder="sk-internal-..."
                    className="w-full max-w-md font-mono" />
                </FormRow>
                <FormRow label={t('formAITimeout')}>
                  <div className="flex items-center gap-2">
                    <TextInput type="number" min={1} max={60}
                      value={p.incident_response.ai_remediation.timeout_seconds ?? ''}
                      onChange={e => set({
                        incident_response: {
                          ...p.incident_response,
                          ai_remediation: { ...p.incident_response?.ai_remediation, timeout_seconds: Number(e.target.value || 0) },
                        },
                      })}
                      className="w-24" />
                    <span className="text-[10px] text-zinc-500">seconds</span>
                  </div>
                </FormRow>
              </div>
            )}
          </div>
        </SectionCard>
      )}

      {/* ── notify — GLOBAL only ── */}
      {!isAgent && (
        <SectionCard id="notify" title={t('formSectionNotify')} icon={Bell}>
          <div className="space-y-4">
            <FormRow label={t('formNotifyMinSev')} hint={t('formNotifyMinSevHint')}>
              <SelectInput value={p.notify?.min_severity || ''}
                onChange={e => set({ notify: { ...p.notify, min_severity: (e.target.value || undefined) as Severity } })}>
                <option value="">— default (medium) —</option>
                {SEVERITY_OPTIONS.map(s => <option key={s} value={s}>{s}</option>)}
              </SelectInput>
            </FormRow>
            <FormRow label={t('formNotifyCooldown')} hint={t('formNotifyCooldownHint')}>
              <div className="flex items-center gap-2">
                <TextInput type="number" min={0} value={p.notify?.cooldown?.seconds ?? ''}
                  onChange={e => set({ notify: { ...p.notify, cooldown: { seconds: Number(e.target.value || 0) } } })}
                  className="w-32" />
                <span className="text-[10px] text-zinc-500">seconds</span>
              </div>
            </FormRow>
            <FormRow label={t('formNotifyWindow')}>
              <div className="flex items-center gap-2">
                <TextInput type="number" min={0} value={p.notify?.rate_limit?.window_seconds ?? ''}
                  onChange={e => set({ notify: { ...p.notify, rate_limit: { ...p.notify?.rate_limit, window_seconds: Number(e.target.value || 0) } } })}
                  className="w-32" />
                <span className="text-[10px] text-zinc-500">seconds</span>
              </div>
            </FormRow>
            <FormRow label={t('formNotifyMaxPerAgent')} hint={t('formNotifyMaxPerAgentHint')}>
              <TextInput type="number" min={0} value={p.notify?.rate_limit?.max_per_agent_per_rule ?? ''}
                onChange={e => set({ notify: { ...p.notify, rate_limit: { ...p.notify?.rate_limit, max_per_agent_per_rule: Number(e.target.value || 0) } } })}
                className="w-32" />
            </FormRow>
          </div>
        </SectionCard>
      )}

      {/* ── custom_rules — GLOBAL only, read-only summary ── */}
      {!isAgent && (
        <SectionCard id="custom" title={t('formSectionCustom')} icon={Sparkles} counter={customRuleCount}
          hint={t('formCustomNote')}>
          <div className="space-y-2">
            {(p.custom_rules || []).length === 0 ? (
              <EmptyState icon={FileLock2} label={t('formCustomEmpty')} hint="Use Raw JSON view to author regex / dict_file rules." />
            ) : (
              (p.custom_rules || []).map((c, i) => (
                <div key={i} className="rounded-md border border-zinc-800/60 bg-zinc-900/30 p-3">
                  <div className="flex items-center gap-2">
                    <code className="font-mono text-[12px] text-blue-300">{c.id}</code>
                    <SeverityChip value={c.severity} />
                    <span className="text-[10px] text-zinc-500">{c.scan_directions.join(' / ')}</span>
                    {c.label && <span className="text-[10px] text-zinc-400 italic">— {c.label}</span>}
                  </div>
                  <div className="mt-2 text-[11px] font-mono text-zinc-400 break-all">
                    <span className="text-zinc-500">{c.match.type}:</span> {c.match.pattern || c.match.path}
                  </div>
                </div>
              ))
            )}
          </div>
        </SectionCard>
      )}
    </div>
  );
}

// ── Optional Anchor Nav (used by parent component on wide screens) ────────
export const POLICY_SECTIONS = [
  { id: 'master',      labelKey: 'formSectionMaster',      icon: Shield,    globalOnly: true },
  { id: 'rules',       labelKey: 'formSectionRules',       icon: Filter,    globalOnly: false },
  { id: 'allowlist',   labelKey: 'formSectionAllowList',   icon: Database,  globalOnly: false },
  { id: 'responsible', labelKey: 'formSectionResponsible', icon: Mail,      globalOnly: false },
  { id: 'incident',    labelKey: 'formSectionIncident',    icon: Mail,      globalOnly: true },
  { id: 'notify',      labelKey: 'formSectionNotify',      icon: Bell,      globalOnly: true },
  { id: 'custom',      labelKey: 'formSectionCustom',      icon: Sparkles,  globalOnly: true },
];
