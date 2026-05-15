// PolicyForm — structured editor for audit policy. Drops in alongside the
// raw-JSON view as a more product-grade entry point for operators.
//
// Per docs/v1/audit-system-design.md §6.2 + §7.1, this form covers the
// frequently-edited surface area:
//   - master switches (enabled / preventive / incident_response / notify.suspended)
//   - rules_override table (10 builtin rules)
//   - allow_list (agents / paths / content_hashes chip editors)
//   - responsible_persons (default + by_severity/agent/user/rule chip editors)
//   - notify.rate_limit + cooldown numeric tuning
//   - incident_response subform + AI remediation
// custom_rules creation stays in the raw JSON view; this form lists existing
// custom rules read-only with a "edit in raw" hint.

import { useState, useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, ChevronRight, Plus, X, AlertTriangle, Shield, Mail, Filter, Database, Bell } from 'lucide-react';

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

// ── Builtin rule list (mirrors BuiltinRules() in audit/builtin_rules.go) ──

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

// ── Utility components ─────────────────────────────────────────────────────

function Section({
  id, title, icon: Icon, defaultOpen = true, hint, children,
}: {
  id: string; title: string; icon?: any; defaultOpen?: boolean; hint?: string; children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <section data-id={`audit-policy-section-${id}`} className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] overflow-hidden">
      <button type="button" onClick={() => setOpen(o => !o)}
        className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-zinc-200 hover:bg-[var(--vsc-bg-hover)] transition-colors text-left">
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        {Icon ? <Icon size={14} className="text-blue-400" /> : null}
        <span>{title}</span>
        {hint && <span className="ml-auto text-[10px] font-normal text-[var(--vsc-text-muted)]">{hint}</span>}
      </button>
      {open && <div className="px-3 py-3 border-t border-[var(--vsc-border)] space-y-3">{children}</div>}
    </section>
  );
}

function Switch({ checked, onChange, label, hint, danger }: {
  checked: boolean; onChange: (v: boolean) => void; label: string; hint?: string; danger?: boolean;
}) {
  return (
    <label className="flex items-start gap-2 cursor-pointer select-none">
      <input type="checkbox" checked={!!checked} onChange={e => onChange(e.target.checked)}
        className="mt-0.5 accent-blue-500" />
      <div className="flex-1">
        <div className={`text-xs ${danger && checked ? 'text-red-300' : 'text-[var(--vsc-text)]'}`}>{label}</div>
        {hint && <div className="text-[10px] text-[var(--vsc-text-muted)] mt-0.5">{hint}</div>}
      </div>
    </label>
  );
}

function NumberField({ value, onChange, label, hint, min, max, suffix }: {
  value: number | undefined; onChange: (v: number) => void; label: string; hint?: string;
  min?: number; max?: number; suffix?: string;
}) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[10px] text-[var(--vsc-text-secondary)]">{label}</span>
      <div className="flex items-center gap-2">
        <input type="number" value={value ?? ''} min={min} max={max}
          onChange={e => onChange(e.target.value === '' ? 0 : Number(e.target.value))}
          className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)] w-32" />
        {suffix && <span className="text-[10px] text-[var(--vsc-text-muted)]">{suffix}</span>}
      </div>
      {hint && <span className="text-[10px] text-[var(--vsc-text-muted)]">{hint}</span>}
    </div>
  );
}

function TextField({ value, onChange, label, hint, placeholder, type = 'text' }: {
  value: string | undefined; onChange: (v: string) => void; label: string; hint?: string;
  placeholder?: string; type?: string;
}) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-[10px] text-[var(--vsc-text-secondary)]">{label}</span>
      <input type={type} value={value ?? ''} onChange={e => onChange(e.target.value)} placeholder={placeholder}
        className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)] w-full" />
      {hint && <span className="text-[10px] text-[var(--vsc-text-muted)]">{hint}</span>}
    </div>
  );
}

function Select<T extends string>({ value, onChange, options, label, hint }: {
  value: T | undefined; onChange: (v: T) => void; options: readonly T[];
  label?: string; hint?: string;
}) {
  return (
    <div className="flex flex-col gap-1">
      {label && <span className="text-[10px] text-[var(--vsc-text-secondary)]">{label}</span>}
      <select value={value ?? ''} onChange={e => onChange(e.target.value as T)}
        className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)]">
        <option value="">—</option>
        {options.map(o => <option key={o} value={o}>{o}</option>)}
      </select>
      {hint && <span className="text-[10px] text-[var(--vsc-text-muted)]">{hint}</span>}
    </div>
  );
}

function ChipList({ values, onChange, placeholder, readOnly }: {
  values: string[] | undefined; onChange: (next: string[]) => void; placeholder?: string; readOnly?: boolean;
}) {
  const [draft, setDraft] = useState('');
  const list = values || [];
  const add = () => {
    const t = draft.trim();
    if (!t) return;
    if (list.includes(t)) { setDraft(''); return; }
    onChange([...list, t]);
    setDraft('');
  };
  const remove = (i: number) => onChange(list.filter((_, j) => j !== i));
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex flex-wrap gap-1.5 min-h-[24px]">
        {list.map((v, i) => (
          <span key={`${v}-${i}`} className="inline-flex items-center gap-1 px-2 py-0.5 text-[10px] rounded-full bg-blue-500/10 border border-blue-500/30 text-blue-200 font-mono">
            {v}
            {!readOnly && (
              <button type="button" onClick={() => remove(i)}
                className="hover:text-red-300 transition-colors">
                <X size={10} />
              </button>
            )}
          </span>
        ))}
        {list.length === 0 && <span className="text-[10px] text-[var(--vsc-text-muted)] italic">empty</span>}
      </div>
      {!readOnly && (
        <div className="flex items-center gap-1">
          <input value={draft} onChange={e => setDraft(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add(); } }}
            placeholder={placeholder || 'value…'}
            className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)] flex-1 font-mono" />
          <button type="button" onClick={add}
            className="px-2 py-1 text-xs rounded border border-[var(--vsc-border)] bg-[var(--vsc-bg-hover)] hover:bg-[var(--vsc-bg-active)] text-[var(--vsc-text)] transition-colors">
            <Plus size={12} />
          </button>
        </div>
      )}
    </div>
  );
}

function KeyedChipMapEditor({ value, onChange, keyPlaceholder, valuePlaceholder, readOnly }: {
  value: Record<string, string[]> | undefined; onChange: (v: Record<string, string[]>) => void;
  keyPlaceholder?: string; valuePlaceholder?: string; readOnly?: boolean;
}) {
  const [newKey, setNewKey] = useState('');
  const m = value || {};
  const keys = Object.keys(m).sort();
  return (
    <div className="flex flex-col gap-2">
      {keys.map(k => (
        <div key={k} className="flex items-center gap-2">
          <code className="text-[10px] text-[var(--vsc-text-secondary)] min-w-[140px] truncate font-mono">{k}</code>
          <div className="flex-1">
            <ChipList values={m[k]} placeholder={valuePlaceholder || 'recipient…'} readOnly={readOnly}
              onChange={next => {
                const cp = { ...m, [k]: next };
                if (next.length === 0) delete cp[k];
                onChange(cp);
              }} />
          </div>
          {!readOnly && (
            <button type="button" onClick={() => {
              const cp = { ...m };
              delete cp[k];
              onChange(cp);
            }} className="text-[var(--vsc-text-muted)] hover:text-red-300 transition-colors">
              <X size={12} />
            </button>
          )}
        </div>
      ))}
      {!readOnly && (
        <div className="flex items-center gap-1">
          <input value={newKey} onChange={e => setNewKey(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter' && newKey.trim()) {
                onChange({ ...m, [newKey.trim()]: [] });
                setNewKey('');
              }
            }}
            placeholder={keyPlaceholder || 'add key…'}
            className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-dashed border-[var(--vsc-border)] text-[var(--vsc-text)] flex-1 font-mono" />
        </div>
      )}
    </div>
  );
}

// ── Main form component ───────────────────────────────────────────────────

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

  // Type narrowing helpers — agent override only has a subset of fields.
  const p = policy as Policy & AgentOverride;

  const set = useCallback((patch: any) => {
    if (ro) return;
    onChange({ ...p, ...patch });
  }, [p, onChange, ro]);

  // rules_override as a map for fast lookups
  const overrideMap = useMemo(() => {
    const m: Record<string, RuleOverride> = {};
    (p.rules_override || []).forEach(r => { m[r.id] = r; });
    return m;
  }, [p.rules_override]);

  const setRuleOverride = (id: string, patch: Partial<RuleOverride>) => {
    const existing = overrideMap[id] || { id };
    const next: RuleOverride = { ...existing, ...patch };
    // Drop the override entry if it became a no-op (all fields unset/false/empty).
    const isNoop = !next.disabled && !next.severity && !next.default_action;
    const others = (p.rules_override || []).filter(r => r.id !== id);
    if (isNoop) set({ rules_override: others.length ? others : undefined });
    else set({ rules_override: [...others, next] });
  };

  return (
    <div data-id="audit-policy-form" className="flex flex-col gap-3">
      {/* ── master switches: GLOBAL only ── */}
      {!isAgent && (
        <Section id="master" title={t('formSectionMaster')} icon={Shield} defaultOpen={true}>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <Switch
              checked={p.enabled ?? true}
              onChange={v => set({ enabled: v })}
              label={t('formEnabled')}
              hint={t('formEnabledHint')}
            />
            <Switch
              checked={p.preventive?.enabled ?? false}
              onChange={v => set({ preventive: { ...p.preventive, enabled: v } })}
              label={t('formPreventiveEnabled')}
              hint={t('formPreventiveHint')}
            />
            <Switch
              checked={p.incident_response?.enabled ?? false}
              onChange={v => set({ incident_response: { ...p.incident_response, enabled: v } })}
              label={t('formIncidentEnabled')}
              hint={t('formIncidentHint')}
            />
            <Switch
              checked={p.notify?.suspended ?? false}
              onChange={v => set({ notify: { ...p.notify, suspended: v } })}
              label={t('formNotifySuspended')}
              hint={t('formNotifySuspendedHint')}
              danger
            />
          </div>
          {p.preventive?.enabled && (
            <div className="pt-2 border-t border-[var(--vsc-border)] flex items-center gap-3">
              <span className="text-[10px] text-[var(--vsc-text-secondary)]">{t('formPreventiveFailMode')}:</span>
              {(['open', 'closed'] as const).map(m => (
                <label key={m} className="flex items-center gap-1 text-xs cursor-pointer">
                  <input type="radio" name="failmode" checked={(p.preventive?.fail_mode || 'open') === m}
                    onChange={() => set({ preventive: { ...p.preventive, fail_mode: m } })}
                    className="accent-blue-500" />
                  <span>{m}</span>
                </label>
              ))}
              <span className="text-[10px] text-[var(--vsc-text-muted)] ml-auto">
                {(p.preventive?.fail_mode || 'open') === 'open'
                  ? t('formFailOpenHint')
                  : t('formFailClosedHint')}
              </span>
            </div>
          )}
        </Section>
      )}
      {isAgent && (
        <div className="flex items-center gap-2 p-2 rounded-md border border-blue-500/20 bg-blue-500/5 text-[11px] text-blue-200">
          <AlertTriangle size={12} />
          <span>{t('formAgentScopeNote')}</span>
        </div>
      )}

      {/* ── rules_override table ── */}
      <Section id="rules" title={t('formSectionRules')} icon={Filter} defaultOpen={true}>
        <table className="w-full text-xs">
          <thead className="text-[10px] text-[var(--vsc-text-secondary)] uppercase tracking-wide">
            <tr>
              <th className="text-left py-1.5 pr-2">{t('formColRule')}</th>
              <th className="text-left py-1.5 pr-2">{t('formColSeverity')}</th>
              <th className="text-left py-1.5 pr-2">{t('formColAction')}</th>
              <th className="text-left py-1.5 pr-2">{t('formColDisabled')}</th>
            </tr>
          </thead>
          <tbody>
            {BUILTIN_RULES.map(r => {
              const ov = overrideMap[r.id];
              return (
                <tr key={r.id} className="border-t border-[var(--vsc-border)]">
                  <td className="py-1.5 pr-2">
                    <code className="font-mono text-[var(--vsc-text)]">{r.id}</code>
                    <div className="text-[10px] text-[var(--vsc-text-muted)]">
                      default: {r.severity} / {r.default_action}{r.inline ? ' / inline' : ''}
                    </div>
                  </td>
                  <td className="py-1.5 pr-2">
                    {ro ? (
                      <span className="text-[var(--vsc-text-muted)]">{ov?.severity || r.severity}</span>
                    ) : (
                      <select value={ov?.severity || ''}
                        onChange={e => setRuleOverride(r.id, { severity: (e.target.value || undefined) as Severity })}
                        className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)]">
                        <option value="">{r.severity} (default)</option>
                        {SEVERITY_OPTIONS.map(s => <option key={s} value={s}>{s}</option>)}
                      </select>
                    )}
                  </td>
                  <td className="py-1.5 pr-2">
                    {ro ? (
                      <span className="text-[var(--vsc-text-muted)]">{ov?.default_action || r.default_action}</span>
                    ) : (
                      <select value={ov?.default_action || ''}
                        onChange={e => setRuleOverride(r.id, { default_action: (e.target.value || undefined) as Action })}
                        className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)]">
                        <option value="">{r.default_action} (default)</option>
                        {ACTION_OPTIONS.map(a => <option key={a} value={a}>{a}</option>)}
                      </select>
                    )}
                  </td>
                  <td className="py-1.5 pr-2">
                    <input type="checkbox" disabled={ro}
                      checked={!!ov?.disabled}
                      onChange={e => setRuleOverride(r.id, { disabled: e.target.checked || undefined })}
                      className="accent-red-500" />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Section>

      {/* ── allow_list ── */}
      <Section id="allowlist" title={t('formSectionAllowList')} icon={Database} defaultOpen={false}>
        <div className="space-y-3">
          {!isAgent && (
            <div>
              <div className="text-[10px] text-[var(--vsc-text-secondary)] mb-1">{t('formAllowAgents')}</div>
              <ChipList values={p.allow_list?.agents} readOnly={ro}
                onChange={next => set({ allow_list: { ...p.allow_list, agents: next } })}
                placeholder="w-trusted-bot" />
            </div>
          )}
          <div>
            <div className="text-[10px] text-[var(--vsc-text-secondary)] mb-1">{t('formAllowPaths')}</div>
            <ChipList values={p.allow_list?.paths} readOnly={ro}
              onChange={next => set({ allow_list: { ...p.allow_list, paths: next } })}
              placeholder="mitm:flow-known-fp-" />
          </div>
          <div>
            <div className="text-[10px] text-[var(--vsc-text-secondary)] mb-1">{t('formAllowHashes')}</div>
            <ChipList values={p.allow_list?.content_hashes} readOnly={ro}
              onChange={next => set({ allow_list: { ...p.allow_list, content_hashes: next } })}
              placeholder="sha256:..." />
          </div>
        </div>
      </Section>

      {/* ── responsible_persons ── */}
      <Section id="responsible" title={t('formSectionResponsible')} icon={Mail} defaultOpen={false}>
        <div className="space-y-4">
          <div>
            <div className="text-[10px] text-[var(--vsc-text-secondary)] mb-1">{t('formRespDefault')}</div>
            <ChipList values={p.responsible_persons?.default} readOnly={ro}
              onChange={next => set({ responsible_persons: { ...p.responsible_persons, default: next } })}
              placeholder="sec@yourcorp.com" />
          </div>
          {(['by_severity', 'by_rule', isAgent ? null : 'by_agent', isAgent ? null : 'by_user'].filter(Boolean) as string[]).map(k => (
            <div key={k}>
              <div className="text-[10px] text-[var(--vsc-text-secondary)] mb-1">{t(`formResp_${k}`)}</div>
              <KeyedChipMapEditor
                value={(p.responsible_persons as any)?.[k]} readOnly={ro}
                onChange={next => set({ responsible_persons: { ...p.responsible_persons, [k]: next } })}
                keyPlaceholder={t(`formResp_${k}_key`)}
                valuePlaceholder="email@corp"
              />
            </div>
          ))}
        </div>
      </Section>

      {/* ── incident_response — GLOBAL only ── */}
      {!isAgent && (
        <Section id="incident" title={t('formSectionIncident')} icon={Mail} defaultOpen={false}>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <Select<Severity>
              value={p.incident_response?.trigger_min_severity}
              onChange={v => set({ incident_response: { ...p.incident_response, trigger_min_severity: v } })}
              options={SEVERITY_OPTIONS}
              label={t('formIncidentTrigger')}
              hint={t('formIncidentTriggerHint')}
            />
            <NumberField
              value={p.incident_response?.cooldown_seconds}
              onChange={v => set({ incident_response: { ...p.incident_response, cooldown_seconds: v } })}
              label={t('formIncidentCooldown')}
              suffix="s"
              hint={t('formIncidentCooldownHint')}
            />
            <TextField
              value={p.incident_response?.email_from}
              onChange={v => set({ incident_response: { ...p.incident_response, email_from: v || undefined } })}
              label={t('formIncidentFrom')}
              placeholder="audit@yourcorp.com"
              hint={t('formIncidentFromHint')}
            />
          </div>

          <div className="pt-3 border-t border-[var(--vsc-border)]">
            <Switch
              checked={p.incident_response?.ai_remediation?.enabled ?? false}
              onChange={v => set({
                incident_response: {
                  ...p.incident_response,
                  ai_remediation: { ...p.incident_response?.ai_remediation, enabled: v },
                },
              })}
              label={t('formAIEnabled')}
              hint={t('formAIEnabledHint')}
            />
            {p.incident_response?.ai_remediation?.enabled && (
              <div className="mt-2 grid grid-cols-1 md:grid-cols-2 gap-3 pl-6">
                <TextField
                  value={p.incident_response.ai_remediation.endpoint}
                  onChange={v => set({
                    incident_response: {
                      ...p.incident_response,
                      ai_remediation: { ...p.incident_response?.ai_remediation, endpoint: v },
                    },
                  })}
                  label={t('formAIEndpoint')}
                  placeholder="https://internal-llm.corp/v1"
                />
                <TextField
                  value={p.incident_response.ai_remediation.model}
                  onChange={v => set({
                    incident_response: {
                      ...p.incident_response,
                      ai_remediation: { ...p.incident_response?.ai_remediation, model: v },
                    },
                  })}
                  label={t('formAIModel')}
                  placeholder="internal-fast"
                />
                <TextField
                  value={p.incident_response.ai_remediation.api_key}
                  onChange={v => set({
                    incident_response: {
                      ...p.incident_response,
                      ai_remediation: { ...p.incident_response?.ai_remediation, api_key: v },
                    },
                  })}
                  label={t('formAIKey')}
                  type="password"
                />
                <NumberField
                  value={p.incident_response.ai_remediation.timeout_seconds}
                  onChange={v => set({
                    incident_response: {
                      ...p.incident_response,
                      ai_remediation: { ...p.incident_response?.ai_remediation, timeout_seconds: v },
                    },
                  })}
                  label={t('formAITimeout')}
                  suffix="s"
                />
              </div>
            )}
          </div>
        </Section>
      )}

      {/* ── notify rate_limit / cooldown — GLOBAL only ── */}
      {!isAgent && (
        <Section id="notify" title={t('formSectionNotify')} icon={Bell} defaultOpen={false}>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <Select<Severity>
              value={p.notify?.min_severity}
              onChange={v => set({ notify: { ...p.notify, min_severity: v } })}
              options={SEVERITY_OPTIONS}
              label={t('formNotifyMinSev')}
              hint={t('formNotifyMinSevHint')}
            />
            <NumberField
              value={p.notify?.cooldown?.seconds}
              onChange={v => set({ notify: { ...p.notify, cooldown: { seconds: v } } })}
              label={t('formNotifyCooldown')}
              suffix="s"
              hint={t('formNotifyCooldownHint')}
            />
            <NumberField
              value={p.notify?.rate_limit?.window_seconds}
              onChange={v => set({ notify: { ...p.notify, rate_limit: { ...p.notify?.rate_limit, window_seconds: v } } })}
              label={t('formNotifyWindow')}
              suffix="s"
            />
            <NumberField
              value={p.notify?.rate_limit?.max_per_agent_per_rule}
              onChange={v => set({ notify: { ...p.notify, rate_limit: { ...p.notify?.rate_limit, max_per_agent_per_rule: v } } })}
              label={t('formNotifyMaxPerAgent')}
              hint={t('formNotifyMaxPerAgentHint')}
            />
          </div>
        </Section>
      )}

      {/* ── custom_rules — GLOBAL only, read-only summary ── */}
      {!isAgent && (
        <Section id="custom" title={t('formSectionCustom')}
          hint={`${(p.custom_rules || []).length} ${t('formCustomCount')}`} defaultOpen={false}>
          <div className="text-[10px] text-[var(--vsc-text-muted)] italic">
            {t('formCustomNote')}
          </div>
          <div className="space-y-2">
            {(p.custom_rules || []).map((c, i) => (
              <div key={i} className="rounded border border-[var(--vsc-border)] p-2 text-xs font-mono">
                <div>
                  <span className="text-blue-300">{c.id}</span>
                  <span className="text-[var(--vsc-text-muted)] ml-2">[{c.severity}]</span>
                  <span className="text-[var(--vsc-text-muted)] ml-2">{c.scan_directions.join('/')}</span>
                </div>
                <div className="text-[10px] text-[var(--vsc-text-secondary)] mt-1">
                  match: {c.match.type} {c.match.pattern || c.match.path}
                </div>
              </div>
            ))}
            {(p.custom_rules || []).length === 0 && (
              <div className="text-[10px] text-[var(--vsc-text-muted)] italic">{t('formCustomEmpty')}</div>
            )}
          </div>
        </Section>
      )}
    </div>
  );
}
