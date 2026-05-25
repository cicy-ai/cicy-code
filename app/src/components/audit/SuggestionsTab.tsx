// Phase 4 Policy Agent — Suggestions tab.
//
// Lets operators review LLM-generated policy patches and apply / dismiss
// each one. Backed by:
//   GET  /api/audit/policy/suggestions
//   POST /api/audit/policy/suggestions/generate
//   POST /api/audit/policy/suggestions/apply
//   POST /api/audit/policy/suggestions/dismiss
//
// Severity gating: safe applies on a single click, moderate/dangerous
// require an inline "confirm" step.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  RefreshCw, Sparkles, Filter, CheckCircle2, XCircle, AlertTriangle,
  ShieldCheck, FileCode2, Clock,
} from 'lucide-react';
import apiService from '../../services/api';

type SuggestionStatus = 'open' | 'applied' | 'dismissed';
type SuggestionKind = 'allow_list' | 'rule_override' | 'custom_rule' | 'preventive_toggle';
type SuggestionSeverity = 'safe' | 'moderate' | 'dangerous';

interface Suggestion {
  id: string;
  kind: SuggestionKind;
  severity: SuggestionSeverity;
  title: string;
  rationale: string;
  supporting_event_ids?: string[];
  patch: Record<string, unknown>;
  status: SuggestionStatus;
  created_at: string;
  updated_at: string;
}

interface SuggestionsFile {
  version: number;
  generated_at: string;
  based_on_events_from: string;
  based_on_events_to: string;
  suggestions: Suggestion[];
}

const SEVERITY_TONE: Record<SuggestionSeverity, { text: string; bg: string; border: string }> = {
  safe:      { text: 'text-emerald-300', bg: 'bg-emerald-500/10', border: 'border-emerald-500/30' },
  moderate:  { text: 'text-amber-300',   bg: 'bg-amber-500/10',   border: 'border-amber-500/30' },
  dangerous: { text: 'text-red-300',     bg: 'bg-red-500/10',     border: 'border-red-500/30' },
};

const KIND_LABEL_KEY: Record<SuggestionKind, string> = {
  allow_list:        'suggKindAllowList',
  rule_override:     'suggKindRuleOverride',
  custom_rule:       'suggKindCustomRule',
  preventive_toggle: 'suggKindPreventive',
};

export default function SuggestionsTab() {
  const { t } = useTranslation('audit');
  const [data, setData] = useState<SuggestionsFile | null>(null);
  const [loading, setLoading] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState<string>('');
  const [filterStatus, setFilterStatus] = useState<'all' | SuggestionStatus>('open');
  const [selectedId, setSelectedId] = useState<string>('');
  const [confirmingApply, setConfirmingApply] = useState<string>('');
  const [busy, setBusy] = useState(false);

  const fetchAll = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await apiService.auditPolicySuggestionsList();
      setData(resp.data as SuggestionsFile);
    } catch (err: any) {
      setError(err?.response?.data || err?.message || 'failed to load');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { fetchAll(); }, [fetchAll]);

  const handleGenerate = useCallback(async () => {
    setGenerating(true);
    setError('');
    try {
      await apiService.auditPolicySuggestionsGenerate();
      await fetchAll();
    } catch (err: any) {
      const msg = err?.response?.data || err?.message || 'generate failed';
      setError(typeof msg === 'string' ? msg : JSON.stringify(msg));
    } finally {
      setGenerating(false);
    }
  }, [fetchAll]);

  const handleApply = useCallback(async (id: string) => {
    setBusy(true);
    setError('');
    try {
      await apiService.auditPolicySuggestionsApply(id);
      setConfirmingApply('');
      await fetchAll();
    } catch (err: any) {
      const msg = err?.response?.data || err?.message || 'apply failed';
      setError(typeof msg === 'string' ? msg : JSON.stringify(msg));
    } finally {
      setBusy(false);
    }
  }, [fetchAll]);

  const handleDismiss = useCallback(async (id: string) => {
    setBusy(true);
    setError('');
    try {
      await apiService.auditPolicySuggestionsDismiss(id);
      await fetchAll();
    } catch (err: any) {
      const msg = err?.response?.data || err?.message || 'dismiss failed';
      setError(typeof msg === 'string' ? msg : JSON.stringify(msg));
    } finally {
      setBusy(false);
    }
  }, [fetchAll]);

  const filtered = useMemo(() => {
    const all = data?.suggestions ?? [];
    if (filterStatus === 'all') return all;
    return all.filter(s => s.status === filterStatus);
  }, [data, filterStatus]);

  const selected = filtered.find(s => s.id === selectedId)
    ?? data?.suggestions.find(s => s.id === selectedId);

  const counts = useMemo(() => {
    const c = { open: 0, applied: 0, dismissed: 0, total: 0 };
    for (const s of data?.suggestions ?? []) {
      c.total++;
      c[s.status]++;
    }
    return c;
  }, [data]);

  return (
    <div data-id="audit-suggestions-root" className="flex flex-col gap-3 h-full">
      {/* Top bar */}
      <div data-id="audit-suggestions-topbar" className="flex flex-wrap items-center gap-2 p-2 rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)]">
        <div className="flex items-center gap-1.5 text-[var(--vsc-text-secondary)]">
          <Sparkles size={14} />
          <span className="text-xs">{t('suggHeader')}</span>
        </div>

        <div className="flex items-center gap-1 text-xs">
          <Filter size={12} className="text-[var(--vsc-text-muted)]" />
          {(['open', 'applied', 'dismissed', 'all'] as const).map(s => (
            <button
              key={s}
              data-id={`audit-suggestions-filter-${s}`}
              onClick={() => setFilterStatus(s)}
              className={`px-2 py-0.5 rounded transition-colors ${
                filterStatus === s
                  ? 'bg-[var(--vsc-bg-active)] text-white'
                  : 'text-[var(--vsc-text-secondary)] hover:bg-[var(--vsc-bg-hover)]'
              }`}
            >
              {t(`suggFilter_${s}`)} ({s === 'all' ? counts.total : counts[s]})
            </button>
          ))}
        </div>

        <div className="flex-1" />

        {data?.generated_at && (
          <span className="text-[10px] text-[var(--vsc-text-muted)] flex items-center gap-1">
            <Clock size={11} />
            {t('suggGeneratedAt', { ts: formatRelative(data.generated_at) })}
          </span>
        )}

        <button
          data-id="audit-suggestions-refresh"
          onClick={fetchAll}
          disabled={loading}
          className="flex items-center gap-1 px-2 py-1 text-xs rounded bg-[var(--vsc-bg-hover)] hover:bg-[var(--vsc-bg-active)] text-[var(--vsc-text)] transition-colors disabled:opacity-50"
        >
          <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
          {t('suggRefresh')}
        </button>

        <button
          data-id="audit-suggestions-generate"
          onClick={handleGenerate}
          disabled={generating}
          className="flex items-center gap-1 px-2 py-1 text-xs rounded bg-blue-500/20 hover:bg-blue-500/30 border border-blue-500/40 text-blue-200 transition-colors disabled:opacity-50"
        >
          <Sparkles size={12} className={generating ? 'animate-pulse' : ''} />
          {generating ? t('suggGenerating') : t('suggGenerate')}
        </button>
      </div>

      {error && (
        <div data-id="audit-suggestions-error" className="p-2 rounded border border-red-500/40 bg-red-500/10 text-xs text-red-200">
          {error}
        </div>
      )}

      {/* List + detail */}
      <div data-id="audit-suggestions-body" className="flex-1 grid grid-cols-1 lg:grid-cols-[1fr_480px] gap-3 min-h-0">
        {/* List */}
        <div data-id="audit-suggestions-list" className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] overflow-hidden flex flex-col min-h-0">
          <div className="px-3 py-2 border-b border-[var(--vsc-border)] text-xs text-[var(--vsc-text-secondary)]">
            {t('suggListHeader', { showing: filtered.length, total: counts.total })}
          </div>

          <div className="flex-1 overflow-auto">
            {filtered.length === 0 ? (
              <EmptyState
                hasAny={(data?.suggestions ?? []).length > 0}
                filterStatus={filterStatus}
                onGenerate={handleGenerate}
                generating={generating}
              />
            ) : (
              <ul className="divide-y divide-[var(--vsc-border)]">
                {filtered.map(s => (
                  <li
                    key={s.id}
                    data-id={`audit-suggestions-item-${s.id}`}
                    onClick={() => setSelectedId(s.id)}
                    className={`px-3 py-2 cursor-pointer hover:bg-[var(--vsc-bg-hover)] transition-colors ${
                      selectedId === s.id ? 'bg-[var(--vsc-bg-active)]' : ''
                    }`}
                  >
                    <div className="flex items-start gap-2">
                      <SeverityBadge severity={s.severity} />
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="text-xs text-[var(--vsc-text)] font-medium truncate">{s.title}</span>
                          <StatusBadge status={s.status} />
                        </div>
                        <div className="flex items-center gap-2 mt-0.5">
                          <KindChip kind={s.kind} />
                          <span className="text-[10px] text-[var(--vsc-text-muted)] truncate">{s.rationale}</span>
                        </div>
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>

        {/* Detail */}
        <div data-id="audit-suggestions-detail" className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] overflow-hidden flex flex-col min-h-0">
          {selected ? (
            <SuggestionDetail
              suggestion={selected}
              busy={busy}
              confirmingApply={confirmingApply}
              onConfirmApply={setConfirmingApply}
              onApply={handleApply}
              onDismiss={handleDismiss}
            />
          ) : (
            <div className="flex-1 flex items-center justify-center text-xs text-[var(--vsc-text-muted)] p-6 text-center">
              {t('suggSelectHint')}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ── Small components ──

function SeverityBadge({ severity }: { severity: SuggestionSeverity }) {
  const { t } = useTranslation('audit');
  const tone = SEVERITY_TONE[severity];
  const Icon = severity === 'safe' ? ShieldCheck : severity === 'moderate' ? AlertTriangle : AlertTriangle;
  return (
    <span
      data-id={`audit-suggestions-severity-${severity}`}
      className={`shrink-0 inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium ${tone.text} ${tone.bg} border ${tone.border}`}
    >
      <Icon size={10} />
      {t(`suggSeverity_${severity}`)}
    </span>
  );
}

function StatusBadge({ status }: { status: SuggestionStatus }) {
  const { t } = useTranslation('audit');
  const styles: Record<SuggestionStatus, string> = {
    open:      'text-zinc-300 bg-zinc-500/10 border-zinc-500/30',
    applied:   'text-emerald-300 bg-emerald-500/10 border-emerald-500/30',
    dismissed: 'text-zinc-500 bg-zinc-700/30 border-zinc-700/40 line-through',
  };
  return (
    <span className={`shrink-0 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border ${styles[status]}`}>
      {t(`suggStatus_${status}`)}
    </span>
  );
}

function KindChip({ kind }: { kind: SuggestionKind }) {
  const { t } = useTranslation('audit');
  return (
    <span className="shrink-0 inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] text-zinc-300 bg-[var(--vsc-bg)] border border-[var(--vsc-border)]">
      <FileCode2 size={9} />
      {t(KIND_LABEL_KEY[kind])}
    </span>
  );
}

function SuggestionDetail({
  suggestion, busy, confirmingApply, onConfirmApply, onApply, onDismiss,
}: {
  suggestion: Suggestion;
  busy: boolean;
  confirmingApply: string;
  onConfirmApply: (id: string) => void;
  onApply: (id: string) => void;
  onDismiss: (id: string) => void;
}) {
  const { t } = useTranslation('audit');
  const isOpen = suggestion.status === 'open';
  const needsConfirm = suggestion.severity !== 'safe';
  const isConfirming = confirmingApply === suggestion.id;
  const tone = SEVERITY_TONE[suggestion.severity];

  return (
    <div className="flex flex-col h-full">
      <div className={`px-3 py-2 border-b border-[var(--vsc-border)] flex items-start gap-2 ${tone.bg}`}>
        <SeverityBadge severity={suggestion.severity} />
        <div className="flex-1 min-w-0">
          <div className="text-xs text-white font-semibold leading-tight">{suggestion.title}</div>
          <div className="flex items-center gap-2 mt-1">
            <KindChip kind={suggestion.kind} />
            <StatusBadge status={suggestion.status} />
            <span className="text-[10px] text-[var(--vsc-text-muted)]">
              {t('suggCreatedAt', { ts: formatRelative(suggestion.created_at) })}
            </span>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-auto p-3 space-y-3 text-xs">
        <section>
          <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-1">
            {t('suggSectionRationale')}
          </div>
          <div className="text-[var(--vsc-text)] leading-relaxed whitespace-pre-wrap">
            {suggestion.rationale}
          </div>
        </section>

        {(suggestion.supporting_event_ids ?? []).length > 0 && (
          <section>
            <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-1">
              {t('suggSectionSupporting', { n: suggestion.supporting_event_ids!.length })}
            </div>
            <div className="flex flex-wrap gap-1">
              {suggestion.supporting_event_ids!.map(eid => (
                <code key={eid} className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text-secondary)]">
                  {eid}
                </code>
              ))}
            </div>
          </section>
        )}

        <section>
          <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-1">
            {t('suggSectionPatch')}
          </div>
          <pre className="text-[11px] p-2 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] overflow-auto whitespace-pre-wrap text-[var(--vsc-text)]">
            {JSON.stringify(suggestion.patch, null, 2)}
          </pre>
        </section>
      </div>

      {/* Action footer */}
      <div className="border-t border-[var(--vsc-border)] p-2 flex items-center gap-2">
        {!isOpen ? (
          <div className="text-[10px] text-[var(--vsc-text-muted)] px-1">
            {suggestion.status === 'applied'
              ? t('suggAppliedAt', { ts: formatRelative(suggestion.updated_at) })
              : t('suggDismissedAt', { ts: formatRelative(suggestion.updated_at) })}
          </div>
        ) : isConfirming ? (
          <>
            <span className="text-[11px] text-amber-200 flex items-center gap-1">
              <AlertTriangle size={11} />
              {t('suggConfirmPrompt')}
            </span>
            <div className="flex-1" />
            <button
              onClick={() => onConfirmApply('')}
              disabled={busy}
              className="px-2 py-1 text-xs rounded bg-[var(--vsc-bg-hover)] hover:bg-[var(--vsc-bg-active)] text-[var(--vsc-text)] transition-colors disabled:opacity-50"
            >
              {t('suggConfirmCancel')}
            </button>
            <button
              onClick={() => onApply(suggestion.id)}
              disabled={busy}
              className="px-2 py-1 text-xs rounded bg-red-500/20 hover:bg-red-500/30 border border-red-500/40 text-red-200 transition-colors disabled:opacity-50"
            >
              {t('suggConfirmYes')}
            </button>
          </>
        ) : (
          <>
            <button
              data-id={`audit-suggestions-dismiss-${suggestion.id}`}
              onClick={() => onDismiss(suggestion.id)}
              disabled={busy}
              className="flex items-center gap-1 px-2 py-1 text-xs rounded bg-[var(--vsc-bg-hover)] hover:bg-[var(--vsc-bg-active)] text-[var(--vsc-text-secondary)] transition-colors disabled:opacity-50"
            >
              <XCircle size={12} />
              {t('suggBtnDismiss')}
            </button>
            <div className="flex-1" />
            <button
              data-id={`audit-suggestions-apply-${suggestion.id}`}
              onClick={() => needsConfirm ? onConfirmApply(suggestion.id) : onApply(suggestion.id)}
              disabled={busy}
              className={`flex items-center gap-1 px-2 py-1 text-xs rounded border transition-colors disabled:opacity-50 ${
                suggestion.severity === 'safe'
                  ? 'bg-emerald-500/20 hover:bg-emerald-500/30 border-emerald-500/40 text-emerald-200'
                  : suggestion.severity === 'moderate'
                  ? 'bg-amber-500/20 hover:bg-amber-500/30 border-amber-500/40 text-amber-200'
                  : 'bg-red-500/20 hover:bg-red-500/30 border-red-500/40 text-red-200'
              }`}
            >
              <CheckCircle2 size={12} />
              {needsConfirm ? t('suggBtnApplyConfirm') : t('suggBtnApply')}
            </button>
          </>
        )}
      </div>
    </div>
  );
}

function EmptyState({
  hasAny, filterStatus, onGenerate, generating,
}: {
  hasAny: boolean;
  filterStatus: 'all' | SuggestionStatus;
  onGenerate: () => void;
  generating: boolean;
}) {
  const { t } = useTranslation('audit');
  if (!hasAny) {
    return (
      <div className="flex flex-col items-center justify-center h-full p-6 text-center gap-3">
        <Sparkles size={32} className="text-[var(--vsc-text-muted)]" />
        <div className="text-xs text-[var(--vsc-text-secondary)] max-w-sm">
          {t('suggEmptyIntro')}
        </div>
        <button
          onClick={onGenerate}
          disabled={generating}
          className="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-blue-500/20 hover:bg-blue-500/30 border border-blue-500/40 text-blue-200 transition-colors disabled:opacity-50"
        >
          <Sparkles size={12} className={generating ? 'animate-pulse' : ''} />
          {generating ? t('suggGenerating') : t('suggEmptyAction')}
        </button>
      </div>
    );
  }
  return (
    <div className="flex flex-col items-center justify-center h-full p-6 text-center gap-2">
      <div className="text-xs text-[var(--vsc-text-secondary)]">
        {t('suggEmptyFiltered', { status: filterStatus })}
      </div>
    </div>
  );
}

// ── helpers ──

function formatRelative(ts: string): string {
  if (!ts) return '';
  const then = new Date(ts).getTime();
  if (isNaN(then)) return ts;
  const diffSec = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.round(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.round(diffSec / 3600)}h ago`;
  return `${Math.round(diffSec / 86400)}d ago`;
}
