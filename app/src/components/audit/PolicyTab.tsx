import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ShieldCheck, ShieldX, EyeOff, Bell, FileText, Zap, Mail, ListChecks, AlertTriangle, X, SlidersHorizontal, RotateCcw, Plus, Trash2, ChevronLeft } from 'lucide-react';
import { Spinner } from '../ui/Spinner';
import Select from '../ui/Select';
import apiService from '../../services/api';

// Configurable view of the EFFECTIVE policy.json (GET/POST /api/audit/policy).
// Two sub-tabs: 设置 (switches / notify / responsible / allowlist) and 规则
// (builtin + custom rules, builtin rows can override the default severity or be
// disabled). Every change writes the whole policy back via POST (atomic + hot
// reload ~200ms).
interface RuleTest { text: string; expect?: string; }
interface RuleOverride { id?: string; severity?: string; default_action?: string; disabled?: boolean; pattern?: string; match_type?: string; tests?: RuleTest[]; }
interface CustomRule {
  id?: string; label?: string; category?: string; severity?: string;
  default_action?: string; inline?: boolean; scan_directions?: string[];
  disabled?: boolean; match?: { type?: string; pattern?: string }; tests?: RuleTest[];
}
interface Policy {
  preventive?: { enabled?: boolean; fail_mode?: string };
  rules_override?: RuleOverride[];
  custom_rules?: CustomRule[];
  notify?: { min_severity?: string };
  incident_response?: { enabled?: boolean; trigger_min_severity?: string; cooldown_seconds?: number };
  responsible_persons?: { default?: string[]; [k: string]: string[] | undefined };
  ai_remediation?: { enabled?: boolean };
  allow_list?: { agents?: string[]; content_hashes?: string[]; paths?: string[] };
}

const sevColor: Record<string, string> = {
  critical: 'text-red-400 border-red-500/30 bg-red-500/10',
  high: 'text-orange-400 border-orange-500/30 bg-orange-500/10',
  medium: 'text-amber-300 border-amber-500/30 bg-amber-500/10',
  low: 'text-zinc-400 border-zinc-500/20 bg-zinc-500/10',
};
const SEVERITIES = ['low', 'medium', 'high', 'critical'];

function ActionBadge({ action }: { action?: string }) {
  const { t } = useTranslation('audit');
  const map: Record<string, { cls: string; icon: any; label: string }> = {
    block: { cls: 'text-red-400 bg-red-500/10 border-red-500/30', icon: ShieldX, label: t('guardBlocked') },
    redact: { cls: 'text-amber-300 bg-amber-500/10 border-amber-500/30', icon: EyeOff, label: t('guardRedacted') },
    notify: { cls: 'text-blue-400 bg-blue-500/10 border-blue-500/30', icon: Bell, label: t('logActionNotify') },
    log: { cls: 'text-zinc-400 bg-zinc-500/10 border-zinc-500/20', icon: FileText, label: t('logActionLog') },
  };
  const m = map[action || 'log'] || map.log;
  const Icon = m.icon;
  return (
    <span className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[12px] font-medium ${m.cls}`}>
      <Icon size={10} />{m.label}
    </span>
  );
}

function Toggle({ on, onClick }: { on: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`relative h-5 w-9 shrink-0 rounded-full transition-colors ${on ? 'bg-emerald-500/80' : 'bg-zinc-600/50'}`}
    >
      <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all ${on ? 'left-[18px]' : 'left-0.5'}`} />
    </button>
  );
}

// A vertical list of selectable options, each with a one-line help.
// Single-select renders radios; multi-select (scan directions) renders checkboxes.
function ChoiceGroup({ options, value, onChange, multi, disabledValues, disabledHint }: {
  options: { value: string; label: string; help: string }[];
  value: string | string[];
  onChange: (v: string) => void;
  multi?: boolean;
  disabledValues?: string[];
  disabledHint?: string;
}) {
  const isOn = (v: string) => (multi ? (value as string[]).includes(v) : value === v);
  const isDisabled = (v: string) => !!disabledValues?.includes(v);
  return (
    <div className="space-y-1">
      {options.map((o) => {
        const on = isOn(o.value);
        const disabled = isDisabled(o.value);
        return (
          <label
            key={o.value}
            className={`flex items-start gap-2 rounded-md border px-2.5 py-1.5 transition-colors ${disabled ? 'opacity-45 cursor-not-allowed border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg)]' : on ? 'cursor-pointer border-blue-500/40 bg-blue-500/[0.07]' : 'cursor-pointer border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg)] hover:border-[var(--vsc-border)]'}`}
          >
            <input type={multi ? 'checkbox' : 'radio'} checked={on} disabled={disabled} onChange={() => { if (!disabled) onChange(o.value); }} className="mt-[3px] shrink-0" />
            <span className="min-w-0 leading-snug">
              <span className={`text-[13px] font-medium ${on ? 'text-white' : 'text-[var(--vsc-text-secondary)]'}`}>{o.label}</span>
              <span className="ml-2 text-[12px] text-[var(--vsc-text-muted)]">{o.help}</span>
              {disabled && disabledHint && <span className="ml-2 text-[12px] text-amber-300/80">{disabledHint}</span>}
            </span>
          </label>
        );
      })}
    </div>
  );
}

// One settings row: icon + label/desc on the left, a control on the right.
function SettingRow({ icon: Icon, label, desc, on, children }: { icon: any; label: string; desc?: string; on?: boolean; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-[var(--vsc-border)] bg-[var(--vsc-bg-secondary)] px-3 py-2.5">
      <div className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-md ${on ? 'bg-emerald-500/15 text-emerald-400' : 'bg-zinc-500/10 text-zinc-500'}`}>
        <Icon size={15} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-xs font-medium text-white">{label}</div>
        {desc && <div className="text-[12px] text-[var(--vsc-text-muted)] truncate">{desc}</div>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

export default function PolicyTab() {
  const { t } = useTranslation('audit');
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [builtinRules, setBuiltinRules] = useState<any[]>([]);
  const [emailReady, setEmailReady] = useState<boolean | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState(false);
  const [saving, setSaving] = useState(false);
  const [tab, setTab] = useState<'settings' | 'rules'>('rules');
  const [patEdit, setPatEdit] = useState<Record<string, string>>({});
  const [patType, setPatType] = useState<Record<string, string>>({});
  const [newRule, setNewRule] = useState<any | null>(null);
  const [testResults, setTestResults] = useState<Record<number, any>>({});
  const [saveErr, setSaveErr] = useState('');
  const [newPerson, setNewPerson] = useState('');
  const [newAgent, setNewAgent] = useState('');

  // Decision option catalogs (i18n) — each option carries a one-line help.
  const SEV_OPTS = [
    { value: 'low', label: t('sevLow', '低'), help: t('sevLowHelp', '仅留痕,基本不打扰') },
    { value: 'medium', label: t('sevMedium', '中'), help: t('sevMediumHelp', '一般敏感,默认级别') },
    { value: 'high', label: t('sevHigh', '高'), help: t('sevHighHelp', '需要关注,可触发事件响应') },
    { value: 'critical', label: t('sevCritical', '严重'), help: t('sevCriticalHelp', '最高危,优先处置') },
  ];
  const ACTION_OPTS = [
    { value: 'log', label: t('actLog', '记录'), help: t('actLogHelp', '只留痕,不改内容也不拦截(最轻)') },
    { value: 'notify', label: t('actNotify', '告警'), help: t('actNotifyHelp', '命中即通知责任人(推送 / 邮件)') },
    { value: 'block', label: t('actBlock', '拦截'), help: t('actBlockHelp', '直接阻断,不放行这次请求 / 回复') },
  ];
  useEffect(() => {
    let alive = true;
    apiService.getAuditPolicy()
      .then((res: any) => { if (alive) { setPolicy(res?.data || {}); setErr(false); } })
      .catch(() => { if (alive) setErr(true); })
      .finally(() => { if (alive) setLoading(false); });
    apiService.getAuditRules()
      .then((res: any) => { if (alive) setBuiltinRules(res?.data?.rules || []); })
      .catch(() => {});
    apiService.getEmailConfig()
      .then((res: any) => { if (alive) setEmailReady(!!res?.data?.smtp_ready); })
      .catch(() => { if (alive) setEmailReady(false); });
    return () => { alive = false; };
  }, []);

  // save returns {ok} so callers (the rule editor) can await it and surface a
  // server rejection instead of optimistically closing as if it succeeded —
  // the backend re-runs every rule's tests and 400s on failure, so this is the
  // authoritative gate.
  const save = async (next: Policy): Promise<{ ok: boolean; error?: string }> => {
    setPolicy(next); // optimistic
    setSaving(true);
    try {
      await apiService.saveAuditPolicy(next);
      return { ok: true };
    } catch (e: any) {
      const msg = String(e?.response?.data?.error || e?.response?.data || e?.message || '保存失败');
      // reload authoritative state on failure
      try { const r: any = await apiService.getAuditPolicy(); setPolicy(r?.data || {}); } catch { /* ignore */ }
      return { ok: false, error: msg };
    } finally {
      setSaving(false);
    }
  };

  // overrideListWith computes the next rules_override array after merging one
  // override; shared by the quick list toggles and the editor's awaited save.
  const overrideListWith = (id: string, changes: Partial<RuleOverride>): RuleOverride[] => {
    const list: RuleOverride[] = [...(policy?.rules_override || [])];
    const i = list.findIndex((o) => o.id === id);
    const merged: RuleOverride = { ...(i >= 0 ? list[i] : { id }), ...changes };
    const empty = !merged.severity && !merged.disabled && !merged.default_action && !merged.pattern;
    if (empty) { if (i >= 0) list.splice(i, 1); }
    else if (i >= 0) list[i] = merged; else list.push(merged);
    return list;
  };

  const setRuleOverride = (id: string, changes: Partial<RuleOverride>) => {
    if (!policy) return;
    save({ ...policy, rules_override: overrideListWith(id, changes) });
  };

  if (loading) return <div className="flex justify-center py-16"><Spinner size="md" /></div>;
  if (err || !policy) return <div className="py-16 text-center text-sm text-[var(--vsc-text-muted)]">{t('policyLoadError', '无法读取策略')}</div>;

  const customs = policy.custom_rules || [];
  const overrides = policy.rules_override || [];
  const ovById: Record<string, RuleOverride> = Object.fromEntries(overrides.map((o) => [o.id || '', o]));
  const responsible = policy.responsible_persons?.default || [];
  const allowAgents = policy.allow_list?.agents || [];
  const allowHashes = policy.allow_list?.content_hashes || [];

  const TabBtn = ({ id, label, icon: Icon }: { id: 'settings' | 'rules'; label: string; icon: any }) => (
    <button
      data-id={`audit-policy-tab-${id}`}
      onClick={() => setTab(id)}
      className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${tab === id ? 'bg-white/[0.08] text-white' : 'text-zinc-500 hover:text-zinc-300'}`}
    >
      <Icon size={13} />{label}
    </button>
  );

  return (
    <div data-id="audit-policy" className="space-y-4 w-full">
      {/* sub-tabs — hidden while the rule editor is open so it takes over the panel */}
      {!newRule && (
        <div className="flex items-center gap-1 border-b border-[var(--vsc-border)] pb-1">
          <TabBtn id="rules" label={t('policyTabRules', '规则')} icon={ListChecks} />
          <TabBtn id="settings" label={t('policyTabSettings', '设置')} icon={SlidersHorizontal} />
          {saving && <span className="ml-auto inline-flex items-center gap-1 text-[12px] text-[var(--vsc-text-muted)]"><Spinner size="xs" />{t('policySaving', '保存中…')}</span>}
        </div>
      )}

      {tab === 'settings' ? (
        <div className="space-y-2" data-id="audit-policy-settings">
          {/* SMTP / notify email */}
          <SettingRow icon={Mail} label={t('policyNotifyEmail', '通知邮件 (SMTP)')} desc={emailReady === null ? '…' : emailReady ? t('policyEmailReady', '已配置') : t('policyEmailMissing', '未配置,无法发告警邮件')} on={!!emailReady}>
            {emailReady ? (
              <span className="text-[13px] text-emerald-400">✓</span>
            ) : (
              <button
                data-id="audit-policy-config-smtp"
                onClick={() => window.dispatchEvent(new CustomEvent('cicy:open-settings', { detail: { section: 'general' } }))}
                className="rounded-md border border-blue-500/30 bg-blue-500/10 px-2 py-1 text-[13px] text-blue-300 hover:bg-blue-500/15"
              >
                {t('policyConfigSmtp', '去配置')}
              </button>
            )}
          </SettingRow>

          {/* Responsible persons */}
          <div className="rounded-lg border border-[var(--vsc-border)] bg-[var(--vsc-bg-secondary)] px-3 py-2.5">
            <div className="mb-2 text-xs font-medium text-white">{t('policyResponsible', '责任人')}</div>
            <div className="flex flex-wrap items-center gap-1.5">
              {responsible.map((p) => (
                <span key={p} className="inline-flex items-center gap-1 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg)] px-2 py-1 text-[13px] text-[var(--vsc-text)]">
                  {p}
                  <button onClick={() => save({ ...policy, responsible_persons: { ...policy.responsible_persons, default: responsible.filter((x) => x !== p) } })} className="text-zinc-500 hover:text-red-400"><X size={11} /></button>
                </span>
              ))}
              <span className="inline-flex items-center gap-1">
                <input
                  value={newPerson}
                  onChange={(e) => setNewPerson(e.target.value)}
                  onKeyDown={(e) => { if (e.key === 'Enter' && !e.nativeEvent.isComposing && newPerson.trim()) { e.preventDefault(); save({ ...policy, responsible_persons: { ...policy.responsible_persons, default: [...responsible, newPerson.trim()] } }); setNewPerson(''); } }}
                  placeholder={t('policyAddPerson', '邮箱/名字, 回车添加')}
                  className="w-44 rounded border border-[var(--vsc-border)] bg-[var(--vsc-bg)] px-2 py-1 text-[13px] text-white"
                />
              </span>
            </div>
          </div>

          {/* Allow list */}
          <div className="rounded-lg border border-[var(--vsc-border)] bg-[var(--vsc-bg-secondary)] px-3 py-2.5">
            <div className="mb-2 text-xs font-medium text-white">{t('policyAllowlist', '白名单')} <span className="text-[12px] font-normal text-[var(--vsc-text-muted)]">{t('policyAllowlistDesc', '(这些 agent 的命中直接放行)')}</span></div>
            <div className="flex flex-wrap items-center gap-1.5">
              {allowAgents.map((a) => (
                <span key={a} className="inline-flex items-center gap-1 rounded-md border border-emerald-500/20 bg-emerald-500/10 px-2 py-1 text-[13px] font-mono text-emerald-300">
                  {a}
                  <button onClick={() => save({ ...policy, allow_list: { ...policy.allow_list, agents: allowAgents.filter((x) => x !== a) } })} className="text-emerald-500/60 hover:text-red-400"><X size={11} /></button>
                </span>
              ))}
              <input
                value={newAgent}
                onChange={(e) => setNewAgent(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter' && !e.nativeEvent.isComposing && newAgent.trim()) { e.preventDefault(); save({ ...policy, allow_list: { ...policy.allow_list, agents: [...allowAgents, newAgent.trim()] } }); setNewAgent(''); } }}
                placeholder={t('policyAddAgent', 'w-xxxx, 回车添加')}
                className="w-36 rounded border border-[var(--vsc-border)] bg-[var(--vsc-bg)] px-2 py-1 text-[13px] text-white font-mono"
              />
              {allowHashes.length > 0 && (
                <span className="rounded-md border border-zinc-500/20 bg-zinc-500/10 px-2 py-1 text-[13px] text-zinc-400">{t('policyAllowHashes', '{{n}} 个内容哈希', { n: allowHashes.length })}</span>
              )}
            </div>
          </div>
        </div>
      ) : (
        <div className="space-y-2" data-id="audit-policy-rules">
          {newRule ? (
            /* ============ DETAIL: dedicated rule editor ============ */
            (() => {
              const ed = newRule;
              const isBuiltin = ed.mode === 'builtin';
              // Regex and JS are SEPARATE editors with their own content — switching
              // matcher type never carries one's text into the other. Migrated once
              // from the legacy single `pattern` field.
              const edRegex = ed.regex !== undefined ? ed.regex : (ed.match_type === 'regex' ? (ed.pattern || '') : '');
              const edJs = ed.js !== undefined ? ed.js : (ed.match_type === 'js' ? (ed.pattern || '') : '');
              const activePat = ed.match_type === 'js' ? edJs : edRegex;
              const matcherChanged = isBuiltin && (activePat !== (ed.builtinPattern || '') || ed.match_type !== (ed.builtinPattern ? 'regex' : 'js'));
              // ── test cases: a rule carries a list of {text, expect}; each can be
              // run individually and ALL must pass before the rule can be saved.
              const tests: RuleTest[] = ed.tests || [];
              const setTests = (next: RuleTest[]) => setNewRule({ ...ed, tests: next });
              const addTest = () => setTests([...tests, { text: '', expect: 'hit' }]);
              const delTest = (idx: number) => { setTests(tests.filter((_, i) => i !== idx)); setTestResults((p) => { const n = { ...p }; delete n[idx]; return n; }); };
              const updTest = (idx: number, ch: Partial<RuleTest>) => setTests(tests.map((tc, i) => (i === idx ? { ...tc, ...ch } : tc)));
              const runOne = async (idx: number) => {
                const tc = tests[idx];
                try {
                  const r: any = await apiService.testAuditRule({ match_type: ed.match_type, pattern: activePat, text: tc.text });
                  const res = r?.data || {};
                  const pass = ((res.count || 0) > 0) === (tc.expect !== 'miss');
                  setTestResults((p) => ({ ...p, [idx]: { ...res, pass } }));
                  return pass;
                } catch { setTestResults((p) => ({ ...p, [idx]: { error: t('policyTestFail', '测试失败') } })); return false; }
              };
              const runAll = async () => { let all = true; for (let i = 0; i < tests.length; i++) { const ok = await runOne(i); all = all && ok; } return all; };
              const saveEd = async () => {
                setSaveErr('');
                // Gate: a rule with an editable matcher must have ≥1 test case and
                // ALL of them must pass. Pure severity/action/enable tweaks on a
                // builtin (matcher unchanged), or a disabled rule, skip the gate.
                const needsTest = (!isBuiltin || matcherChanged) && activePat.trim() !== '' && !ed.disabled;
                if (needsTest) {
                  if (tests.length === 0) { setSaveErr(t('policyNeedTest', '请至少添加一个测试用例并全部跑通,才能保存')); return; }
                  const allPass = await runAll();
                  if (!allPass) { setSaveErr(t('policyTestsMustPass', '有测试用例未通过,无法保存:请修正匹配规则或用例')); return; }
                }
                let next: Policy;
                if (isBuiltin) {
                  const overridden = activePat && (activePat !== ed.builtinPattern || ed.match_type !== (ed.builtinPattern ? 'regex' : 'js'));
                  next = { ...policy, rules_override: overrideListWith(ed.id, {
                    severity: ed.severity !== ed.builtinSeverity ? ed.severity : undefined,
                    default_action: ed.default_action || undefined,
                    disabled: ed.disabled ? true : undefined,
                    pattern: overridden ? activePat : undefined,
                    match_type: overridden && ed.match_type === 'js' ? 'js' : undefined,
                    tests: overridden && tests.length ? tests : undefined,
                  }) };
                } else {
                  const id = ed.id.trim();
                  if (!id.startsWith('custom.') || !activePat.trim()) { setSaveErr(t('policyCustomIncomplete', '请填写 custom. 开头的 ID 和匹配内容')); return; }
                  // 方向不再是规则配置项(无意义):规则自动扫两边,事件按命中方向标 inbound/outbound。
                  const rule = { id, label: ed.label.trim(), severity: ed.severity, default_action: ed.default_action, match: { type: ed.match_type, pattern: activePat }, disabled: ed.disabled ? true : undefined, tests: tests.length ? tests : undefined };
                  next = { ...policy, custom_rules: [...(policy.custom_rules || []).filter((c) => c.id !== id), rule] };
                }
                // Await the write: the backend re-runs every test and 400s on
                // failure, so only close the editor when the server accepts it.
                const res = await save(next);
                if (!res.ok) { setSaveErr(t('policySaveRejected', '服务器拒绝保存(可能有测试用例未通过):') + (res.error || '')); return; }
                setNewRule(null); setTestResults({});
              };
              return (
                <div data-id="audit-policy-rule-editor" className="space-y-3">
                  {/* editor header */}
                  <div className="flex items-center gap-2 border-b border-[var(--vsc-border)] pb-2">
                    <button onClick={() => { setNewRule(null); setTestResults({}); setSaveErr(''); }} className="inline-flex items-center gap-1 text-xs text-[var(--vsc-text-secondary)] hover:text-white"><ChevronLeft size={14} />{t('policyBack', '返回')}</button>
                    <span className="ml-1 text-xs font-semibold text-white truncate">{ed.mode === 'new' ? t('policyNewRule', '新建规则') : (ed.label || ed.id)}</span>
                    <span className={`ml-auto shrink-0 rounded px-1.5 py-0.5 text-[12px] border ${isBuiltin ? 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20' : 'bg-purple-500/10 text-purple-300 border-purple-500/20'}`}>{isBuiltin ? t('policyBuiltin', '内置') : t('policyCustom', '自定义')}</span>
                  </div>

                  {/* id + name (custom/new) or readonly id (builtin) */}
                  {isBuiltin ? (
                    <div className="font-mono text-[13px] text-[var(--vsc-text-secondary)]">{ed.id}</div>
                  ) : (
                    <div className="space-y-1.5">
                      <label className="block text-[12px] text-[var(--vsc-text-muted)]">{t('policyFieldId', '规则 ID(custom. 开头)')}</label>
                      <input value={ed.id} disabled={ed.mode === 'custom'} onChange={(e) => setNewRule({ ...ed, id: e.target.value })} placeholder="custom.my_rule" className="w-full rounded border border-[var(--vsc-border)] bg-[var(--vsc-bg)] px-2 py-1.5 font-mono text-[13px] text-white disabled:opacity-60" />
                      <label className="block text-[12px] text-[var(--vsc-text-muted)]">{t('policyRuleName', '规则名称')}</label>
                      <input value={ed.label} onChange={(e) => setNewRule({ ...ed, label: e.target.value })} placeholder={t('policyRuleName', '规则名称')} className="w-full rounded border border-[var(--vsc-border)] bg-[var(--vsc-bg)] px-2 py-1.5 text-[13px] text-white" />
                    </div>
                  )}

                  {/* matcher type + body */}
                  <div className="space-y-1.5">
                    <div className="flex items-center gap-3 text-[13px]">
                      <span className="text-[var(--vsc-text-muted)]">{t('policyMatcher', '匹配方式')}</span>
                      {['regex', 'js'].map((mt) => (
                        <label key={mt} className="inline-flex items-center gap-1 cursor-pointer">
                          <input type="radio" checked={ed.match_type === mt} onChange={() => { setNewRule({ ...ed, match_type: mt, regex: edRegex, js: edJs }); setTestResults({}); setSaveErr(''); }} />
                          <span className={ed.match_type === mt ? 'text-white' : 'text-[var(--vsc-text-muted)]'}>{mt === 'regex' ? t('matcherRegex', '正则') : t('matcherJs', 'JS 代码')}</span>
                        </label>
                      ))}
                      {isBuiltin && matcherChanged && <span className="ml-auto rounded bg-blue-500/10 px-1.5 py-0.5 text-[11px] text-blue-300 border border-blue-500/20">{t('policyOverridden', '已覆盖')}</span>}
                      {isBuiltin && ovById[ed.id]?.pattern && <button onClick={() => setNewRule({ ...ed, regex: ed.builtinPattern || '', js: '', match_type: ed.builtinPattern ? 'regex' : 'js' })} className="text-[11px] text-[var(--vsc-text-muted)] hover:text-white">{t('policyResetMatcher', '恢复内置')}</button>}
                    </div>
                    {isBuiltin && !ed.builtinPattern && <div className="text-[11px] text-[var(--vsc-text-muted)]">{t('policyRuleGoFuncShort', '默认:内置函数(熵/校验)')} —— {t('policyOverrideHint', '填正则或写 JS 覆盖它')}</div>}
                    {ed.match_type === 'js' ? (
                      <textarea value={edJs} onChange={(e) => setNewRule({ ...ed, js: e.target.value, regex: edRegex })} spellCheck={false} rows={8} className="w-full rounded border border-[var(--vsc-border)] bg-black/40 px-2 py-1.5 font-mono text-[13px] leading-relaxed text-emerald-200" placeholder="// function(text){ … } 的函数体,返回 true/false 或命中子串数组&#10;return text.match(/sk-[A-Za-z0-9]{20,}/g) || [];" />
                    ) : (
                      <input value={edRegex} onChange={(e) => setNewRule({ ...ed, regex: e.target.value, js: edJs })} spellCheck={false} className="w-full rounded border border-[var(--vsc-border)] bg-black/40 px-2 py-1.5 font-mono text-[13px] text-amber-200" placeholder={t('policyRegexPlaceholder', '正则,如 sk-[A-Za-z0-9]{20,}')} />
                    )}
                    {ed.match_type === 'js' && <div className="text-[11px] text-[var(--vsc-text-muted)]">{t('policyJsHint', 'JS sandbox 运行、200ms 超时;返回 true/false 或命中子串数组。')}</div>}
                  </div>

                  {/* decision */}
                  <div className="space-y-3 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)] p-2.5">
                    <div className="text-[12px] font-semibold text-[var(--vsc-text-secondary)]">{t('policyDecision', '决策')}</div>

                    <div className="flex items-center gap-2">
                      <div className="text-[12px] text-[var(--vsc-text-muted)]">{t('policySeverity', '严重度')}</div>
                      <Select
                        dataId="policy-severity-select"
                        className="min-w-[200px]"
                        compact
                        value={ed.severity || 'medium'}
                        onChange={(v) => setNewRule({ ...ed, severity: v })}
                        options={SEV_OPTS.map((s) => ({ value: s.value, label: `${s.label}（${s.help}）` }))}
                      />
                    </div>

                    <div className="space-y-1.5">
                      <div className="text-[12px] text-[var(--vsc-text-muted)]">{t('policyActionLabel', '命中后')}</div>
                      <ChoiceGroup
                        options={ACTION_OPTS}
                        value={ed.default_action || 'log'}
                        onChange={(v) => setNewRule({ ...ed, default_action: v })}
                        disabledValues={(emailReady && responsible.length > 0) ? [] : ['notify', 'block']}
                        disabledHint={t('policyNeedAlertSetup', '需 SMTP + 责任人')}
                      />
                      {!(emailReady && responsible.length > 0) && (
                        <div className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-[12px] text-amber-300">
                          <AlertTriangle size={13} className="mt-[2px] shrink-0" />
                          <div className="flex-1 leading-relaxed">
                            <div>{t('policyAlertNeedHint', '告警 / 拦截要能投递,必须同时:配置 SMTP + 设置责任人。否则只能「记录」。')}</div>
                            <div className="mt-1 flex flex-wrap gap-2">
                              {!emailReady && <button onClick={() => window.dispatchEvent(new CustomEvent('cicy:open-settings', { detail: { section: 'general' } }))} className="rounded border border-amber-400/40 bg-amber-400/15 px-2 py-0.5 font-medium text-amber-200 hover:bg-amber-400/25">{t('policyConfigSmtp', '配置 SMTP')}{emailReady ? ' ✓' : ''}</button>}
                              {responsible.length === 0 && <button onClick={() => setTab('settings')} className="rounded border border-amber-400/40 bg-amber-400/15 px-2 py-0.5 font-medium text-amber-200 hover:bg-amber-400/25">{t('policyConfigResponsible', '设置责任人')}</button>}
                            </div>
                          </div>
                        </div>
                      )}
                    </div>

                    {/* 扫描方向不再是规则配置项:规则自动扫出站(q)+入站(回复),命中按方向标在事件上。 */}

                    <label className="flex items-center gap-2 cursor-pointer text-[13px] text-[var(--vsc-text-secondary)]">{t('policyEnabled', '启用')}<Toggle on={!ed.disabled} onClick={() => setNewRule({ ...ed, disabled: !ed.disabled })} /></label>
                  </div>

                  {/* test cases — every case must pass before save */}
                  <div className="rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg)] p-2.5 space-y-2">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-[12px] font-semibold text-[var(--vsc-text-secondary)]">🧪 {t('policyTestCases', '测试用例')}</span>
                      <span className="text-[11px] text-amber-300/80">{t('policyTestCasesReq', '(保存前必须全部跑通)')}</span>
                      <button onClick={runAll} className="ml-auto rounded bg-blue-500/15 px-2 py-0.5 text-[12px] text-blue-300 border border-blue-500/30 hover:bg-blue-500/25">{t('policyRunAll', '全部测试')}</button>
                      <button onClick={addTest} className="inline-flex items-center gap-1 rounded border border-[var(--vsc-border)] px-2 py-0.5 text-[12px] text-[var(--vsc-text-secondary)] hover:text-white"><Plus size={11} />{t('policyAddTest', '新增')}</button>
                    </div>
                    {tests.length === 0 && <div className="text-[12px] text-[var(--vsc-text-muted)]">{t('policyNoTests', '还没有测试用例,点「新增」添加;每条标注应命中 / 不应命中。')}</div>}
                    {tests.map((tc, idx) => {
                      const res = testResults[idx];
                      return (
                        <div key={idx} className="rounded border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)] p-2 space-y-1">
                          <div className="flex items-center gap-2">
                            <Select
                              dataId="policy-test-expect-select"
                              className="min-w-[120px]"
                              compact
                              value={tc.expect || 'hit'}
                              onChange={(v) => updTest(idx, { expect: v })}
                              options={[
                                { value: 'hit', label: t('policyExpectHit', '应命中') },
                                { value: 'miss', label: t('policyExpectMiss', '不应命中') },
                              ]}
                            />
                            <button onClick={() => runOne(idx)} className="rounded bg-blue-500/15 px-2 py-0.5 text-[12px] text-blue-300 border border-blue-500/30 hover:bg-blue-500/25">{t('policyRunTest', '测试')}</button>
                            {res && (res.error ? <span className="text-[12px] text-red-400">{res.error}</span> : <span className={`text-[12px] ${res.pass ? 'text-emerald-400' : 'text-red-400'}`}>{res.pass ? t('policyTestPass', '✓ 通过') : t('policyTestFailMark', '✗ 未通过')} ({t('policyTestHits', '命中 {{n}} 处', { n: res.count || 0 })})</span>)}
                            <button onClick={() => delTest(idx)} className="ml-auto text-zinc-500 hover:text-red-400"><Trash2 size={12} /></button>
                          </div>
                          <textarea value={tc.text} onChange={(e) => updTest(idx, { text: e.target.value })} rows={2} className="w-full rounded border border-[var(--vsc-border)] bg-black/30 px-2 py-1 font-mono text-[12px] text-[var(--vsc-text)]" placeholder={t('policyTestSample', '示例文本…')} />
                        </div>
                      );
                    })}
                  </div>

                  {/* actions */}
                  <div className="flex items-center gap-2 border-t border-[var(--vsc-border)] pt-2">
                    <button data-id="audit-policy-rule-save" onClick={saveEd} className="rounded-md bg-blue-600 px-4 py-1.5 text-[13px] font-medium text-white hover:bg-blue-500">{t('policySaveRule', '保存')}</button>
                    {saveErr && <span className="text-[12px] text-red-400">{saveErr}</span>}
                    <button onClick={() => { setNewRule(null); setTestResults({}); setSaveErr(''); }} className="rounded-md border border-[var(--vsc-border)] px-3 py-1.5 text-[13px] text-[var(--vsc-text-secondary)] hover:text-white">{t('policyCancel', '取消')}</button>
                    {ed.mode === 'custom' && <button onClick={() => { save({ ...policy, custom_rules: (policy.custom_rules || []).filter((c) => c.id !== ed.id) }); setNewRule(null); }} className="ml-auto inline-flex items-center gap-1 rounded-md border border-red-500/30 px-3 py-1.5 text-[13px] text-red-400 hover:bg-red-500/10"><Trash2 size={12} />{t('policyDeleteRule', '删除规则')}</button>}
                    {isBuiltin && ovById[ed.id] && <button onClick={() => { setRuleOverride(ed.id, { severity: undefined, default_action: undefined, disabled: undefined, pattern: undefined, match_type: undefined, tests: undefined }); setNewRule(null); }} className="ml-auto inline-flex items-center gap-1 rounded-md border border-[var(--vsc-border)] px-3 py-1.5 text-[13px] text-[var(--vsc-text-secondary)] hover:text-white"><RotateCcw size={12} />{t('policyResetRule', '恢复默认')}</button>}
                  </div>
                </div>
              );
            })()
          ) : (
            /* ============ MASTER: rule list ============ */
            <div className="space-y-1">
              <div className="mb-1 flex items-center gap-2">
                <span className="text-[12px] text-[var(--vsc-text-muted)] flex-1">{t('policyRulesHint2', '点规则进编辑器;匹配可正则或 JS,可测试。')}</span>
                <button data-id="audit-policy-new-rule" onClick={() => { setNewRule({ mode: 'new', id: 'custom.', label: '', match_type: 'js', pattern: 'return text.match(/sk-[A-Za-z0-9]{20,}/g) || [];', severity: 'medium', default_action: 'log', directions: ['outbound'], tests: [{ text: 'key: sk-ABCDEFGHIJKLMNOPQRSTUVWX', expect: 'hit' }, { text: '这里没有任何密钥。', expect: 'miss' }] }); setTestResults({}); setSaveErr(''); }} className="shrink-0 inline-flex items-center gap-1 rounded-md border border-blue-500/30 bg-blue-500/10 px-2 py-1 text-[13px] text-blue-300 hover:bg-blue-500/15"><Plus size={12} />{t('policyNewRule', '新建规则')}</button>
              </div>
              {customs.map((r: any, i) => (
                <button key={`c${i}`} onClick={() => { setNewRule({ mode: 'custom', id: r.id, label: r.label || '', match_type: r.match?.type || 'regex', pattern: r.match?.pattern || '', severity: r.severity || 'medium', default_action: r.default_action || 'log', directions: r.scan_directions || ['outbound'], disabled: !!r.disabled, tests: r.tests || [] }); setTestResults({}); setSaveErr(''); }} className={`flex w-full items-center gap-2 rounded-md border px-3 py-2.5 text-left text-xs hover:border-[var(--vsc-border)] ${r.disabled ? 'opacity-50 border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)]' : 'border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)]'}`}>
                  <span className="font-mono text-[13px] text-[var(--vsc-text)] truncate max-w-[150px]">{r.id}</span>
                  {r.label && <span className="text-[var(--vsc-text-muted)] truncate hidden sm:inline">{r.label}</span>}
                  <span className="ml-auto shrink-0 rounded bg-purple-500/10 px-1.5 py-0.5 text-[12px] text-purple-300 border border-purple-500/20">{t('policyCustom', '自定义')}</span>
                  <span className="shrink-0 rounded bg-zinc-500/10 px-1.5 py-0.5 text-[12px] text-zinc-400 border border-zinc-500/20">{r.match?.type === 'js' ? 'JS' : t('matcherRegex', '正则')}</span>
                  <span className={`shrink-0 rounded border px-1.5 py-0.5 text-[12px] ${sevColor[r.severity || 'low']}`}>{r.severity}</span>
                  {r.disabled && <span className="shrink-0 text-[11px] text-[var(--vsc-text-muted)]">{t('policyDisabled', '已禁用')}</span>}
                </button>
              ))}
              {builtinRules.map((r: any) => {
                const ov = ovById[r.id];
                const enabled = !ov?.disabled;
                const behavior = r.kind === 'behavior';
                return (
                  <button key={r.id} onClick={() => { setNewRule({ mode: 'builtin', id: r.id, label: r.label || '', behavior, match_type: ov?.match_type || (r.pattern ? 'regex' : 'js'), pattern: ov?.pattern ?? r.pattern ?? '', builtinPattern: r.pattern || '', builtinSeverity: r.severity, severity: ov?.severity || r.severity, default_action: ov?.default_action || '', disabled: !!ov?.disabled, directions: r.directions || [], tests: (ov?.tests && ov.tests.length ? ov.tests : r.tests) || [] }); setTestResults({}); setSaveErr(''); }} className={`flex w-full items-center gap-2 rounded-md border px-3 py-2.5 text-left text-xs hover:border-[var(--vsc-border)] ${!enabled ? 'opacity-50 border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)]' : behavior ? 'border-amber-500/25 bg-amber-500/[0.06]' : 'border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)]'}`}>
                    <span className="font-mono text-[13px] text-[var(--vsc-text)] truncate max-w-[150px]">{r.id}</span>
                    {r.label && <span className="text-[var(--vsc-text-muted)] truncate hidden md:inline">{r.label}</span>}
                    <span className={`ml-auto shrink-0 rounded px-1.5 py-0.5 text-[12px] border ${behavior ? 'bg-amber-500/10 text-amber-300 border-amber-500/25' : 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20'}`}>{behavior ? t('policyKindBehavior', '行为') : r.category}</span>
                    {ov?.pattern && <span className="shrink-0 rounded bg-blue-500/10 px-1 py-0.5 text-[11px] text-blue-300 border border-blue-500/20">{ov.match_type === 'js' ? 'JS' : t('matcherRegex', '正则')}</span>}
                    <span className={`shrink-0 rounded border px-1.5 py-0.5 text-[12px] ${sevColor[(ov?.severity || r.severity) || 'low']}`}>{ov?.severity || r.severity}</span>
                    {!enabled && <span className="shrink-0 text-[11px] text-[var(--vsc-text-muted)]">{t('policyDisabled', '已禁用')}</span>}
                  </button>
                );
              })}
              {customs.length + builtinRules.length === 0 && <div className="py-6 text-center text-xs text-[var(--vsc-text-muted)]">{t('policyNoRules', '仅内置默认规则')}</div>}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
