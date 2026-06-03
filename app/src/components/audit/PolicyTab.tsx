import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ShieldCheck, ShieldX, EyeOff, Bell, FileText, Zap, Mail, Bot, ListChecks, AlertTriangle } from 'lucide-react';
import { Spinner } from '../ui/Spinner';
import apiService from '../../services/api';

// Renders the EFFECTIVE policy.json served by GET /api/audit/policy — the
// rules the on-host pipeline actually enforces, authored by the w-6001 SecOps
// agent. (The 'agent'/DecisionsTab reads the autonomy goroutine's
// decisions.ndjson, which is empty unless the autonomous loop is enabled — so
// it shows nothing on a w-6001-driven install even though a policy exists.)
interface RuleOverride { id?: string; severity?: string; default_action?: string; }
interface CustomRule {
  id?: string; label?: string; category?: string; severity?: string;
  default_action?: string; inline?: boolean; scan_directions?: string[];
}
interface Policy {
  enabled?: boolean;
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
    <span className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium ${m.cls}`}>
      <Icon size={10} />{m.label}
    </span>
  );
}

function StatCard({ icon: Icon, label, value, on }: { icon: any; label: string; value: string; on?: boolean }) {
  return (
    <div className="flex items-center gap-2.5 rounded-lg border border-[var(--vsc-border)] bg-[var(--vsc-bg-secondary)] px-3 py-2.5">
      <div className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-md ${on ? 'bg-emerald-500/15 text-emerald-400' : 'bg-zinc-500/10 text-zinc-500'}`}>
        <Icon size={15} />
      </div>
      <div className="min-w-0">
        <div className="text-[10px] uppercase tracking-wide text-[var(--vsc-text-muted)]">{label}</div>
        <div className={`text-xs font-medium truncate ${on ? 'text-white' : 'text-[var(--vsc-text-secondary)]'}`}>{value}</div>
      </div>
    </div>
  );
}

export default function PolicyTab() {
  const { t } = useTranslation('audit');
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState(false);

  useEffect(() => {
    let alive = true;
    apiService.getAuditPolicy()
      .then((res: any) => { if (alive) { setPolicy(res?.data || {}); setErr(false); } })
      .catch(() => { if (alive) setErr(true); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, []);

  if (loading) return <div className="flex justify-center py-16"><Spinner size="md" /></div>;
  if (err || !policy) return <div className="py-16 text-center text-sm text-[var(--vsc-text-muted)]">{t('policyLoadError', '无法读取策略')}</div>;

  const overrides = policy.rules_override || [];
  const customs = policy.custom_rules || [];
  const ruleCount = overrides.length + customs.length;
  const onText = t('policyOn', '已开启');
  const offText = t('policyOff', '未开启');
  const responsible = policy.responsible_persons?.default || [];
  const allowAgents = policy.allow_list?.agents || [];
  const allowHashes = policy.allow_list?.content_hashes || [];

  return (
    <div data-id="audit-policy" className="space-y-5 max-w-3xl">
      {!policy.enabled && (
        <div className="flex items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
          <AlertTriangle size={14} />{t('policyDisabledWarn', '审计已停用 (enabled = false)')}
        </div>
      )}

      {/* Status grid */}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        <StatCard icon={ShieldCheck} label={t('policyGuard', '守护')} value={policy.enabled ? onText : offText} on={!!policy.enabled} />
        <StatCard icon={Zap} label={t('policyPreventive', '实时拦截')} value={policy.preventive?.enabled ? `${onText} · ${policy.preventive?.fail_mode || 'open'}` : offText} on={!!policy.preventive?.enabled} />
        <StatCard icon={Bell} label={t('policyNotifyMin', '告警阈值')} value={policy.notify?.min_severity || '—'} on={!!policy.notify?.min_severity} />
        <StatCard icon={AlertTriangle} label={t('policyIncident', '事件响应')} value={policy.incident_response?.enabled ? `≥ ${policy.incident_response?.trigger_min_severity || 'high'}` : offText} on={!!policy.incident_response?.enabled} />
        <StatCard icon={Bot} label={t('policyAiRemediation', 'AI 研判')} value={policy.ai_remediation?.enabled ? onText : offText} on={!!policy.ai_remediation?.enabled} />
        <StatCard icon={Mail} label={t('policyResponsible', '责任人')} value={responsible[0] || '—'} on={responsible.length > 0} />
      </div>

      {/* Rules */}
      <div>
        <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-white">
          <ListChecks size={14} className="text-blue-400" />{t('policyRules', '规则')} <span className="text-[var(--vsc-text-muted)] font-normal">({ruleCount})</span>
        </div>
        <div className="space-y-1">
          {customs.map((r, i) => (
            <div key={`c${i}`} className="flex items-center gap-2 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)] px-3 py-2 text-xs">
              <span className="font-mono text-[11px] text-[var(--vsc-text)] truncate max-w-[160px]" title={r.id}>{r.id}</span>
              {r.label && <span className="text-[var(--vsc-text-muted)] truncate hidden sm:inline">{r.label}</span>}
              <span className="ml-auto shrink-0 rounded bg-purple-500/10 px-1.5 py-0.5 text-[10px] text-purple-300 border border-purple-500/20">{t('policyCustom', '自定义')}</span>
              <span className={`shrink-0 rounded border px-1.5 py-0.5 text-[10px] ${sevColor[r.severity || 'low']}`}>{r.severity}</span>
              <ActionBadge action={r.default_action} />
            </div>
          ))}
          {overrides.map((r, i) => (
            <div key={`o${i}`} className="flex items-center gap-2 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)] px-3 py-2 text-xs">
              <span className="font-mono text-[11px] text-[var(--vsc-text)] truncate max-w-[200px]" title={r.id}>{r.id}</span>
              <span className="ml-auto shrink-0 rounded bg-zinc-500/10 px-1.5 py-0.5 text-[10px] text-zinc-400 border border-zinc-500/20">{t('policyBuiltin', '内置')}</span>
              <span className={`shrink-0 rounded border px-1.5 py-0.5 text-[10px] ${sevColor[r.severity || 'low']}`}>{r.severity}</span>
              <ActionBadge action={r.default_action} />
            </div>
          ))}
          {ruleCount === 0 && <div className="py-6 text-center text-xs text-[var(--vsc-text-muted)]">{t('policyNoRules', '仅内置默认规则')}</div>}
        </div>
      </div>

      {/* Allow list */}
      {(allowAgents.length > 0 || allowHashes.length > 0) && (
        <div>
          <div className="mb-2 text-xs font-semibold text-white">{t('policyAllowlist', '白名单')}</div>
          <div className="flex flex-wrap gap-1.5">
            {allowAgents.map((a) => (
              <span key={a} className="rounded-md border border-emerald-500/20 bg-emerald-500/10 px-2 py-1 text-[11px] font-mono text-emerald-300">{a}</span>
            ))}
            {allowHashes.length > 0 && (
              <span className="rounded-md border border-zinc-500/20 bg-zinc-500/10 px-2 py-1 text-[11px] text-zinc-400">{t('policyAllowHashes', '{{n}} 个内容哈希', { n: allowHashes.length })}</span>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
