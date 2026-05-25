// Phase 6 (audit-v2) — Agent Decisions tab.
//
// The autonomous policy agent (api/mgr/audit/autonomy.go) runs without
// human approval. This tab is the "report to human" channel: it lists
// every tick the agent ran, what changes it applied, and the rationale
// it cited. Mirrors the "AI writes code, human reads commit log" model.
//
// Backend:
//   GET  /api/audit/decisions?limit=100
//   POST /api/audit/decisions/run     (manual "tick now" trigger)

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  RefreshCw, PlayCircle, CheckCircle2, XCircle, Clock,
  Sparkles, FileCode2, AlertCircle, MessageSquare,
} from 'lucide-react';
import apiService from '../../services/api';

interface Explanation {
  decision_id: string;
  summary: string;
  what_changed: string;
  why_now: string;
  impact: string;
  confidence: string;
  raw_markdown?: string;
}

interface DecisionAction {
  kind: string;
  patch: Record<string, unknown>;
  rationale: string;
  applied: boolean;
  skipped_reason?: string;
}

interface Decision {
  id: string;
  timestamp: string;
  trigger: string; // "interval" | "manual"
  events_window_from: string;
  events_window_to: string;
  events_considered: number;
  llm_response_text?: string;
  actions: DecisionAction[];
  policy_hash_before: string;
  policy_hash_after?: string;
  error?: string;
}

interface DecisionsResponse {
  decisions: Decision[];
  count: number;
}

export default function DecisionsTab() {
  const { t } = useTranslation('audit');
  const [data, setData] = useState<DecisionsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState('');
  const [selectedId, setSelectedId] = useState('');

  const fetchAll = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await apiService.auditAutonomyDecisions();
      setData(resp.data as DecisionsResponse);
    } catch (err: any) {
      setError(err?.response?.data || err?.message || 'failed to load');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { fetchAll(); }, [fetchAll]);

  const handleRunNow = useCallback(async () => {
    setRunning(true);
    setError('');
    try {
      await apiService.auditAutonomyRunNow();
      await fetchAll();
    } catch (err: any) {
      const msg = err?.response?.data || err?.message || 'run failed';
      setError(typeof msg === 'string' ? msg : JSON.stringify(msg));
    } finally {
      setRunning(false);
    }
  }, [fetchAll]);

  const decisions = data?.decisions ?? [];
  const selected = decisions.find(d => d.id === selectedId);

  const stats = useMemo(() => {
    const s = { total: decisions.length, applied: 0, skipped: 0, errors: 0 };
    for (const d of decisions) {
      if (d.error) s.errors++;
      for (const a of d.actions) {
        if (a.applied) s.applied++;
        else s.skipped++;
      }
    }
    return s;
  }, [decisions]);

  return (
    <div data-id="audit-decisions-root" className="flex flex-col gap-3 h-full">
      {/* Top bar */}
      <div data-id="audit-decisions-topbar" className="flex flex-wrap items-center gap-2 p-2 rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)]">
        <div className="flex items-center gap-1.5 text-[var(--vsc-text-secondary)]">
          <Sparkles size={14} />
          <span className="text-xs">{t('decisionsHeader')}</span>
        </div>

        <span className="text-[10px] text-[var(--vsc-text-muted)]">
          {t('decisionsStats', { total: stats.total, applied: stats.applied, skipped: stats.skipped, errors: stats.errors })}
        </span>

        <div className="flex-1" />

        <button
          data-id="audit-decisions-refresh"
          onClick={fetchAll}
          disabled={loading}
          className="flex items-center gap-1 px-2 py-1 text-xs rounded bg-[var(--vsc-bg-hover)] hover:bg-[var(--vsc-bg-active)] text-[var(--vsc-text)] transition-colors disabled:opacity-50"
        >
          <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
          {t('decisionsRefresh')}
        </button>

        <button
          data-id="audit-decisions-run-now"
          onClick={handleRunNow}
          disabled={running}
          className="flex items-center gap-1 px-2 py-1 text-xs rounded bg-blue-500/20 hover:bg-blue-500/30 border border-blue-500/40 text-blue-200 transition-colors disabled:opacity-50"
        >
          <PlayCircle size={12} className={running ? 'animate-pulse' : ''} />
          {running ? t('decisionsRunning') : t('decisionsRunNow')}
        </button>
      </div>

      {error && (
        <div data-id="audit-decisions-error" className="p-2 rounded border border-red-500/40 bg-red-500/10 text-xs text-red-200">
          {error}
        </div>
      )}

      {/* List + detail */}
      <div data-id="audit-decisions-body" className="flex-1 grid grid-cols-1 lg:grid-cols-[1fr_520px] gap-3 min-h-0">
        <div data-id="audit-decisions-list" className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] overflow-hidden flex flex-col min-h-0">
          <div className="px-3 py-2 border-b border-[var(--vsc-border)] text-xs text-[var(--vsc-text-secondary)]">
            {t('decisionsListHeader', { count: decisions.length })}
          </div>
          <div className="flex-1 overflow-auto">
            {decisions.length === 0 ? (
              <EmptyState onRunNow={handleRunNow} running={running} />
            ) : (
              <ul className="divide-y divide-[var(--vsc-border)]">
                {decisions.map(d => (
                  <DecisionRow
                    key={d.id}
                    decision={d}
                    selected={selectedId === d.id}
                    onSelect={() => setSelectedId(d.id)}
                  />
                ))}
              </ul>
            )}
          </div>
        </div>

        <div data-id="audit-decisions-detail" className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] overflow-hidden flex flex-col min-h-0">
          {selected ? (
            <DecisionDetail decision={selected} />
          ) : (
            <div className="flex-1 flex items-center justify-center text-xs text-[var(--vsc-text-muted)] p-6 text-center">
              {t('decisionsSelectHint')}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function DecisionRow({ decision, selected, onSelect }: { decision: Decision; selected: boolean; onSelect: () => void }) {
  const { t } = useTranslation('audit');
  const applied = decision.actions.filter(a => a.applied).length;
  const skipped = decision.actions.length - applied;
  const hasError = !!decision.error;

  return (
    <li
      data-id={`audit-decisions-item-${decision.id}`}
      onClick={onSelect}
      className={`px-3 py-2 cursor-pointer hover:bg-[var(--vsc-bg-hover)] transition-colors ${selected ? 'bg-[var(--vsc-bg-active)]' : ''}`}
    >
      <div className="flex items-start gap-2">
        <span className="shrink-0 text-[10px] px-1.5 py-0.5 rounded bg-[var(--vsc-bg)] text-[var(--vsc-text-muted)] border border-[var(--vsc-border)]">
          {decision.trigger}
        </span>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-xs text-[var(--vsc-text)] font-mono truncate">{decision.id}</span>
            <span className="text-[10px] text-[var(--vsc-text-muted)] flex items-center gap-1 shrink-0">
              <Clock size={10} />
              {formatRelative(decision.timestamp)}
            </span>
          </div>
          <div className="flex items-center gap-2 mt-0.5 text-[10px]">
            {hasError ? (
              <span className="inline-flex items-center gap-1 text-red-300">
                <AlertCircle size={10} />
                {t('decisionsRowError')}
              </span>
            ) : (
              <>
                <span className="inline-flex items-center gap-1 text-emerald-300">
                  <CheckCircle2 size={10} />
                  {t('decisionsRowApplied', { n: applied })}
                </span>
                {skipped > 0 && (
                  <span className="inline-flex items-center gap-1 text-amber-300">
                    <XCircle size={10} />
                    {t('decisionsRowSkipped', { n: skipped })}
                  </span>
                )}
              </>
            )}
            <span className="text-[var(--vsc-text-muted)]">·</span>
            <span className="text-[var(--vsc-text-muted)]">
              {t('decisionsRowEvents', { n: decision.events_considered })}
            </span>
          </div>
        </div>
      </div>
    </li>
  );
}

function DecisionDetail({ decision }: { decision: Decision }) {
  const { t } = useTranslation('audit');
  const [explanation, setExplanation] = useState<Explanation | null>(null);
  const [explainLoading, setExplainLoading] = useState(false);
  const [explainError, setExplainError] = useState('');

  // Reset explanation when switching decisions.
  useEffect(() => {
    setExplanation(null);
    setExplainError('');
  }, [decision.id]);

  const handleExplain = useCallback(async () => {
    setExplainLoading(true);
    setExplainError('');
    try {
      const resp = await apiService.auditAutonomyExplain(decision.id);
      setExplanation(resp.data as Explanation);
    } catch (err: any) {
      setExplainError(err?.response?.data || err?.message || 'explain failed');
    } finally {
      setExplainLoading(false);
    }
  }, [decision.id]);

  return (
    <div className="flex flex-col h-full">
      <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-start gap-2">
        <div className="flex-1 min-w-0">
          <div className="text-xs font-mono text-white">{decision.id}</div>
          <div className="flex items-center gap-2 mt-1 text-[10px] text-[var(--vsc-text-muted)]">
            <span>{formatAbsolute(decision.timestamp)}</span>
            <span>·</span>
            <span>{t('decisionsDetailTrigger', { trigger: decision.trigger })}</span>
            <span>·</span>
            <span>{t('decisionsDetailEvents', { n: decision.events_considered })}</span>
          </div>
        </div>
        <button
          data-id={`audit-decisions-explain-${decision.id}`}
          onClick={handleExplain}
          disabled={explainLoading}
          className="shrink-0 flex items-center gap-1 px-2 py-1 text-xs rounded bg-blue-500/20 hover:bg-blue-500/30 border border-blue-500/40 text-blue-200 transition-colors disabled:opacity-50"
        >
          <MessageSquare size={12} className={explainLoading ? 'animate-pulse' : ''} />
          {explainLoading ? t('decisionsExplaining') : t('decisionsExplain')}
        </button>
      </div>

      <div className="flex-1 overflow-auto p-3 space-y-3 text-xs">
        {explainError && (
          <section className="p-2 rounded border border-red-500/40 bg-red-500/10 text-red-200">
            <div className="text-[10px] uppercase tracking-wider mb-1">{t('decisionsExplainError')}</div>
            <div className="font-mono break-all">{explainError}</div>
          </section>
        )}

        {explanation && (
          <section className="p-3 rounded border border-blue-500/40 bg-blue-500/5 space-y-2">
            <div className="flex items-center gap-2">
              <MessageSquare size={12} className="text-blue-300 shrink-0" />
              <span className="text-[10px] uppercase tracking-wider text-blue-300">
                {t('decisionsExplainTitle')}
              </span>
              <span className={`shrink-0 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border ${
                explanation.confidence === 'high' ? 'text-emerald-300 bg-emerald-500/10 border-emerald-500/30' :
                explanation.confidence === 'medium' ? 'text-amber-300 bg-amber-500/10 border-amber-500/30' :
                'text-zinc-300 bg-zinc-500/10 border-zinc-500/30'}`}>
                {t('decisionsExplainConfidence', { level: explanation.confidence })}
              </span>
            </div>
            {explanation.summary && (
              <div className="text-[var(--vsc-text)] font-medium">{explanation.summary}</div>
            )}
            {explanation.what_changed && (
              <div>
                <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-0.5">
                  {t('decisionsExplainWhatChanged')}
                </div>
                <div className="text-[var(--vsc-text)] leading-relaxed">{explanation.what_changed}</div>
              </div>
            )}
            {explanation.why_now && (
              <div>
                <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-0.5">
                  {t('decisionsExplainWhyNow')}
                </div>
                <div className="text-[var(--vsc-text)] leading-relaxed">{explanation.why_now}</div>
              </div>
            )}
            {explanation.impact && (
              <div>
                <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-0.5">
                  {t('decisionsExplainImpact')}
                </div>
                <div className="text-[var(--vsc-text)] leading-relaxed">{explanation.impact}</div>
              </div>
            )}
            {explanation.raw_markdown && (
              <pre className="text-[10px] p-1.5 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] overflow-auto whitespace-pre-wrap text-[var(--vsc-text-secondary)]">
                {explanation.raw_markdown}
              </pre>
            )}
          </section>
        )}

        {decision.error && (
          <section className="p-2 rounded border border-red-500/40 bg-red-500/10 text-red-200">
            <div className="text-[10px] uppercase tracking-wider mb-1">{t('decisionsSectionError')}</div>
            <div className="font-mono break-all">{decision.error}</div>
          </section>
        )}

        <section>
          <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-1">
            {t('decisionsSectionWindow')}
          </div>
          <div className="text-[var(--vsc-text)]">
            {formatAbsolute(decision.events_window_from)} → {formatAbsolute(decision.events_window_to)}
          </div>
        </section>

        <section>
          <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-1">
            {t('decisionsSectionPolicyHash')}
          </div>
          <div className="font-mono text-[10px] text-[var(--vsc-text-secondary)] break-all">
            {decision.policy_hash_before || '(none)'}{decision.policy_hash_after && decision.policy_hash_after !== decision.policy_hash_before && (
              <> → <span className="text-emerald-300">{decision.policy_hash_after}</span></>
            )}
          </div>
        </section>

        {decision.actions.length > 0 && (
          <section>
            <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-1">
              {t('decisionsSectionActions', { n: decision.actions.length })}
            </div>
            <ul className="space-y-2">
              {decision.actions.map((a, i) => (
                <li key={i} className={`p-2 rounded border ${a.applied ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-amber-500/30 bg-amber-500/5'}`}>
                  <div className="flex items-center gap-2">
                    {a.applied ? (
                      <CheckCircle2 size={12} className="text-emerald-300 shrink-0" />
                    ) : (
                      <XCircle size={12} className="text-amber-300 shrink-0" />
                    )}
                    <span className="inline-flex items-center gap-1 text-[10px] px-1.5 py-0.5 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-zinc-300">
                      <FileCode2 size={9} />
                      {a.kind}
                    </span>
                    {!a.applied && a.skipped_reason && (
                      <span className="text-[10px] text-amber-300">({a.skipped_reason})</span>
                    )}
                  </div>
                  {a.rationale && (
                    <div className="mt-1 text-[var(--vsc-text)] leading-relaxed">
                      {a.rationale}
                    </div>
                  )}
                  <pre className="mt-1 text-[10px] p-1.5 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] overflow-auto whitespace-pre-wrap text-[var(--vsc-text-secondary)]">
                    {JSON.stringify(a.patch, null, 2)}
                  </pre>
                </li>
              ))}
            </ul>
          </section>
        )}

        {decision.llm_response_text && (
          <section>
            <div className="text-[10px] uppercase tracking-wider text-[var(--vsc-text-muted)] mb-1">
              {t('decisionsSectionLLMRaw')}
            </div>
            <pre className="text-[10px] p-2 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] overflow-auto whitespace-pre-wrap text-[var(--vsc-text-secondary)] max-h-40">
              {decision.llm_response_text}
            </pre>
          </section>
        )}
      </div>
    </div>
  );
}

function EmptyState({ onRunNow, running }: { onRunNow: () => void; running: boolean }) {
  const { t } = useTranslation('audit');
  return (
    <div className="flex flex-col items-center justify-center h-full p-6 text-center gap-3">
      <Sparkles size={32} className="text-[var(--vsc-text-muted)]" />
      <div className="text-xs text-[var(--vsc-text-secondary)] max-w-sm">
        {t('decisionsEmptyIntro')}
      </div>
      <button
        onClick={onRunNow}
        disabled={running}
        className="flex items-center gap-1 px-3 py-1.5 text-xs rounded bg-blue-500/20 hover:bg-blue-500/30 border border-blue-500/40 text-blue-200 transition-colors disabled:opacity-50"
      >
        <PlayCircle size={12} className={running ? 'animate-pulse' : ''} />
        {running ? t('decisionsRunning') : t('decisionsEmptyAction')}
      </button>
    </div>
  );
}

// ── helpers ──

function formatRelative(ts: string): string {
  if (!ts) return '';
  const then = new Date(ts).getTime();
  if (isNaN(then)) return ts;
  const diff = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.round(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.round(diff / 3600)}h ago`;
  return `${Math.round(diff / 86400)}d ago`;
}

function formatAbsolute(ts: string): string {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  return d.toLocaleString();
}
