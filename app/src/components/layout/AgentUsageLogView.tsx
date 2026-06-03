import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronLeft, ChevronRight, Loader2 } from 'lucide-react';
import apiService from '../../services/api';

// Per-request usage log for the 分析 → 用量 tab. Tails the backend's
// usage.jsonl (one line per completed AI request) and refreshes on a short
// interval while open, so new requests show up in near real-time. Supports a
// per-request view and a grouped-by-conversation view.

interface UsageRecord {
  ts?: string;
  conversation_id?: string;
  turn_id?: string;
  request_id?: string;
  provider?: string;
  model?: string;
  status?: string;
  status_code?: number;
  reply_start_ms?: number;
  latency_ms?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_input_tokens?: number;
  cache_creation_input_tokens?: number;
  total_tokens?: number;
}

type GroupMode = 'request' | 'conversation';

interface ConversationAgg {
  conversationId: string;
  model?: string;
  count: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
  latencyMs: number;
  lastTs?: string;
}

const POLL_MS = 1500;
const PAGE_SIZE = 50;

// Abbreviated token count (775826 → "776k", 1234567 → "1.23m"). The real value
// is shown on hover via the cell's title.
function fmtNum(n?: number): string {
  if (!n || n <= 0) return '0';
  if (n < 1000) return String(n);
  if (n < 1_000_000) {
    const k = n / 1000;
    return `${k < 10 ? k.toFixed(1) : Math.round(k)}k`;
  }
  const m = n / 1_000_000;
  return `${m < 10 ? m.toFixed(2) : m.toFixed(1)}m`;
}

// Full, grouped value for the hover tooltip.
function fmtFull(n?: number): string {
  if (!n || n <= 0) return '0';
  return n.toLocaleString();
}

// Friendly short model name for the table (raw id stays on hover):
//   claude-opus-4-8 → opus-4.8, claude-sonnet-4-6 → sonnet-4.6
//   gpt-5.5 → gpt-5.5, deepseek-v4-pro → ds-v4-pro
const MODEL_ALIASES: Record<string, string> = {
  // explicit overrides win over the heuristics below
};
function displayModel(raw?: string): string {
  if (!raw || !raw.trim()) return '—';
  const orig = raw.trim();
  // drop a provider path prefix like "anthropic/" or "openai/"
  const m = orig.toLowerCase().replace(/^[a-z0-9.+-]+\//, '');
  if (MODEL_ALIASES[m]) return MODEL_ALIASES[m];
  // claude-<family>-<maj>-<min> → family-maj.min
  const c = m.match(/^claude-(opus|sonnet|haiku)-(\d+)-(\d+)/);
  if (c) return `${c[1]}-${c[2]}.${c[3]}`;
  // deepseek-* → ds-*
  if (m.startsWith('deepseek')) return m.replace(/^deepseek-?/, 'ds-');
  // gpt-* / o1 / o3 / qwen / gemini / kimi: already short enough, keep as-is
  if (/^(gpt|o\d|qwen|gemini|kimi|grok|glm)\b/.test(m)) return m;
  return orig;
}

function fmtLatency(ms?: number): string {
  if (!ms || ms <= 0) return '--';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}

function fmtTime(ts?: string): string {
  if (!ts) return '--';
  const parsed = Date.parse(ts);
  if (!Number.isFinite(parsed)) return ts;
  return new Date(parsed).toLocaleTimeString(undefined, { hour12: false });
}

export default function AgentUsageLogView({ paneId, active }: { paneId: string; active: boolean }) {
  const { t } = useTranslation('agentInspector');
  const [records, setRecords] = useState<UsageRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [groupMode, setGroupMode] = useState<GroupMode>('request');
  const [page, setPage] = useState(0);
  const loadedOnceRef = useRef(false);

  const refresh = useCallback(async () => {
    if (!paneId) return;
    try {
      const { data } = await apiService.getAgentUsageLog(paneId, 200);
      const list = Array.isArray(data?.records) ? (data.records as UsageRecord[]) : [];
      setRecords(list);
    } catch {
      /* keep last good data on transient failure */
    } finally {
      loadedOnceRef.current = true;
      setLoading(false);
    }
  }, [paneId]);

  // Poll while the tab is open; stop when hidden or pane switched away.
  useEffect(() => {
    if (!active || !paneId) return;
    if (!loadedOnceRef.current) setLoading(true);
    refresh();
    const id = window.setInterval(refresh, POLL_MS);
    return () => window.clearInterval(id);
  }, [active, paneId, refresh]);

  // Reset the "first load" spinner gate when the pane changes.
  useEffect(() => {
    loadedOnceRef.current = false;
    setRecords([]);
  }, [paneId]);

  // Aggregate footer totals across the shown records.
  const totals = records.reduce(
    (acc, r) => {
      acc.input += r.input_tokens || 0;
      acc.output += r.output_tokens || 0;
      acc.cacheRead += r.cache_read_input_tokens || 0;
      acc.cacheWrite += r.cache_creation_input_tokens || 0;
      acc.total += r.total_tokens || 0;
      return acc;
    },
    { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
  );

  // Group-by-conversation aggregation. Records arrive newest-first, so the
  // first time a conversation id is seen is its most recent activity — that
  // preserves a newest-conversation-first order in the Map.
  const conversations = useMemo<ConversationAgg[]>(() => {
    const byId = new Map<string, ConversationAgg>();
    for (const r of records) {
      const id = r.conversation_id || '—';
      let agg = byId.get(id);
      if (!agg) {
        agg = { conversationId: id, model: r.model, count: 0, input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0, latencyMs: 0, lastTs: r.ts };
        byId.set(id, agg);
      }
      if (!agg.model && r.model) agg.model = r.model;
      agg.count += 1;
      agg.input += r.input_tokens || 0;
      agg.output += r.output_tokens || 0;
      agg.cacheRead += r.cache_read_input_tokens || 0;
      agg.cacheWrite += r.cache_creation_input_tokens || 0;
      agg.total += r.total_tokens || 0;
      agg.latencyMs += r.latency_ms || 0;
    }
    return Array.from(byId.values());
  }, [records]);

  // Pagination over whichever list the current mode shows.
  const sourceLen = groupMode === 'conversation' ? conversations.length : records.length;
  const pageCount = Math.max(1, Math.ceil(sourceLen / PAGE_SIZE));
  const safePage = Math.min(page, pageCount - 1);
  const pageStart = safePage * PAGE_SIZE;
  const pagedRecords = records.slice(pageStart, pageStart + PAGE_SIZE);
  const pagedConversations = conversations.slice(pageStart, pageStart + PAGE_SIZE);

  // Reset to the first page whenever the pane or grouping changes.
  useEffect(() => { setPage(0); }, [paneId, groupMode]);
  // Keep page in range if the list shrinks.
  useEffect(() => { if (page > pageCount - 1) setPage(pageCount - 1); }, [page, pageCount]);

  const renderToolbar = () => (
    <div data-id="agent-usage-log-toolbar" className="flex shrink-0 items-center gap-1 border-b border-[var(--vsc-border)] px-2 py-1.5">
      {([
        { id: 'request' as GroupMode, label: t('usageByRequest', '按请求') },
        { id: 'conversation' as GroupMode, label: t('usageByConversation', '按会话') },
      ]).map((opt) => (
        <button
          key={opt.id}
          data-id={`agent-usage-group-${opt.id}`}
          type="button"
          onClick={() => setGroupMode(opt.id)}
          className={`rounded-md px-2 py-0.5 text-[11px] font-medium leading-5 transition-colors ${
            groupMode === opt.id ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
          }`}
        >
          {opt.label}
        </button>
      ))}
      {pageCount > 1 && (
        <div data-id="agent-usage-pager" className="ml-auto flex items-center gap-1 text-[11px] text-zinc-500">
          <button
            data-id="agent-usage-page-prev"
            type="button"
            disabled={safePage <= 0}
            onClick={() => setPage((p) => Math.max(0, p - 1))}
            className="rounded p-0.5 hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-30 disabled:hover:bg-transparent"
            aria-label="prev page"
          >
            <ChevronLeft className="h-3.5 w-3.5" />
          </button>
          <span data-id="agent-usage-page-indicator" className="tabular-nums">{safePage + 1}/{pageCount}</span>
          <button
            data-id="agent-usage-page-next"
            type="button"
            disabled={safePage >= pageCount - 1}
            onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
            className="rounded p-0.5 hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-30 disabled:hover:bg-transparent"
            aria-label="next page"
          >
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
        </div>
      )}
    </div>
  );

  if (loading && !records.length) {
    return (
      <div data-id="agent-usage-log" className="flex h-full w-full flex-col overflow-hidden">
        <div data-id="agent-usage-log-loading" className="flex h-full items-center justify-center gap-2 text-[13px] text-zinc-500">
          <Loader2 className="h-4 w-4 animate-spin" /> {t('memLoading', '加载中…')}
        </div>
      </div>
    );
  }
  if (!records.length) {
    return (
      <div data-id="agent-usage-log" className="flex h-full w-full flex-col overflow-hidden">
        {renderToolbar()}
        <div data-id="agent-usage-log-empty" className="flex flex-1 items-center justify-center px-4 text-center text-[13px] text-zinc-500">
          {t('usageEmpty', '暂无请求记录')}
        </div>
      </div>
    );
  }

  return (
    <div data-id="agent-usage-log" className="flex h-full w-full flex-col overflow-hidden">
      {renderToolbar()}
      {groupMode === 'conversation' ? (
        <div data-id="agent-usage-conv-scroll" className="min-h-0 flex-1 overflow-auto">
          <table data-id="agent-usage-conv-table" className="min-w-full whitespace-nowrap border-collapse text-[12px] tabular-nums">
            <thead className="sticky top-0 z-10 bg-[#0b0b0d]">
              <tr className="border-b border-[var(--vsc-border)] text-zinc-500">
                <th className="px-2 py-1.5 text-left font-medium">{t('usageColConversation', '会话')}</th>
                <th className="px-2 py-1.5 text-left font-medium">{t('usageColModel', '模型')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('usageColCount', '请求数')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('usageColInput', '输入')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('usageColOutput', '输出')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('usageColCacheRead', '输入缓存')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('usageColCacheWrite', '输出缓存')}</th>
                <th className="px-2 py-1.5 text-right font-medium">{t('usageColTotal', '总计')}</th>
              </tr>
            </thead>
            <tbody>
              {pagedConversations.map((c, i) => (
                <tr
                  key={`${c.conversationId}-${i}`}
                  data-id={`agent-usage-conv-row-${c.conversationId}`}
                  className="border-b border-white/[0.04] text-zinc-300 hover:bg-white/[0.03]"
                  title={`${c.conversationId}${c.lastTs ? ` · ${fmtTime(c.lastTs)}` : ''}`}
                >
                  <td className="px-2 py-1 text-left font-mono text-zinc-400">{c.conversationId === '—' ? '—' : `${c.conversationId.slice(0, 4)}***`}</td>
                  <td className="px-2 py-1 text-left text-zinc-400">
                    <div className="max-w-[160px] truncate" title={c.model || ''}>{displayModel(c.model)}</div>
                  </td>
                  <td className="px-2 py-1 text-right text-zinc-400">{c.count}</td>
                  <td className="px-2 py-1 text-right" title={fmtFull(c.input)}>{fmtNum(c.input)}</td>
                  <td className="px-2 py-1 text-right" title={fmtFull(c.output)}>{fmtNum(c.output)}</td>
                  <td className="px-2 py-1 text-right text-emerald-300/70" title={fmtFull(c.cacheRead)}>{fmtNum(c.cacheRead)}</td>
                  <td className="px-2 py-1 text-right text-sky-300/70" title={fmtFull(c.cacheWrite)}>{fmtNum(c.cacheWrite)}</td>
                  <td className="px-2 py-1 text-right font-medium text-zinc-100" title={fmtFull(c.total)}>{fmtNum(c.total)}</td>
                </tr>
              ))}
            </tbody>
            <tfoot className="sticky bottom-0 bg-[#0b0b0d]">
              <tr data-id="agent-usage-conv-totals" className="border-t border-[var(--vsc-border)] font-medium text-zinc-200">
                <td className="px-2 py-1.5 text-left text-zinc-500">{t('usageTotal', '合计')} ({conversations.length})</td>
                <td className="px-2 py-1.5 text-left text-zinc-600"></td>
                <td className="px-2 py-1.5 text-right text-zinc-500">{records.length}</td>
                <td className="px-2 py-1.5 text-right" title={fmtFull(totals.input)}>{fmtNum(totals.input)}</td>
                <td className="px-2 py-1.5 text-right" title={fmtFull(totals.output)}>{fmtNum(totals.output)}</td>
                <td className="px-2 py-1.5 text-right text-emerald-300/70" title={fmtFull(totals.cacheRead)}>{fmtNum(totals.cacheRead)}</td>
                <td className="px-2 py-1.5 text-right text-sky-300/70" title={fmtFull(totals.cacheWrite)}>{fmtNum(totals.cacheWrite)}</td>
                <td className="px-2 py-1.5 text-right text-zinc-100" title={fmtFull(totals.total)}>{fmtNum(totals.total)}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      ) : (
        <div data-id="agent-usage-log-scroll" className="min-h-0 flex-1 overflow-auto">
          <table data-id="agent-usage-log-table" className="min-w-full whitespace-nowrap border-collapse text-[12px] tabular-nums">
            <thead className="sticky top-0 z-10 bg-[#0b0b0d]">
              <tr className="border-b border-[var(--vsc-border)] text-zinc-500">
                <th data-id="agent-usage-col-time" className="px-2 py-1.5 text-left font-medium">{t('usageColTime', '时间')}</th>
                <th data-id="agent-usage-col-conversation" className="px-2 py-1.5 text-left font-medium">{t('usageColConversation', '会话')}</th>
                <th data-id="agent-usage-col-model" className="px-2 py-1.5 text-left font-medium">{t('usageColModel', '模型')}</th>
                <th data-id="agent-usage-col-ttft" className="px-2 py-1.5 text-right font-medium">{t('usageColTTFT', '首响')}</th>
                <th data-id="agent-usage-col-latency" className="px-2 py-1.5 text-right font-medium">{t('usageColLatency', '总耗时')}</th>
                <th data-id="agent-usage-col-input" className="px-2 py-1.5 text-right font-medium">{t('usageColInput', '输入')}</th>
                <th data-id="agent-usage-col-output" className="px-2 py-1.5 text-right font-medium">{t('usageColOutput', '输出')}</th>
                <th data-id="agent-usage-col-cache-read" className="px-2 py-1.5 text-right font-medium">{t('usageColCacheRead', '输入缓存')}</th>
                <th data-id="agent-usage-col-cache-write" className="px-2 py-1.5 text-right font-medium">{t('usageColCacheWrite', '输出缓存')}</th>
                <th data-id="agent-usage-col-total" className="px-2 py-1.5 text-right font-medium">{t('usageColTotal', '总计')}</th>
              </tr>
            </thead>
            <tbody>
              {pagedRecords.map((r, i) => (
                <tr
                  key={`${r.request_id || ''}-${r.ts || ''}-${i}`}
                  data-id={`agent-usage-row-${r.request_id || i}`}
                  className={`border-b border-white/[0.04] ${r.status === 'failed' ? 'text-red-300/80' : 'text-zinc-300'} hover:bg-white/[0.03]`}
                  title={`${r.model || ''}${r.status_code ? ` · ${r.status_code}` : ''}`}
                >
                  <td className="px-2 py-1 text-left text-zinc-500">{fmtTime(r.ts)}</td>
                  <td className="px-2 py-1 text-left font-mono text-zinc-500" title={r.conversation_id || ''}>{r.conversation_id ? `${r.conversation_id.slice(0, 4)}***` : '—'}</td>
                  <td className="px-2 py-1 text-left text-zinc-400">
                    <div className="max-w-[160px] truncate" title={r.model || ''}>{displayModel(r.model)}</div>
                  </td>
                  <td className="px-2 py-1 text-right text-zinc-400" title={`${r.reply_start_ms ?? 0}ms`}>{fmtLatency(r.reply_start_ms)}</td>
                  <td className="px-2 py-1 text-right" title={`${r.latency_ms ?? 0}ms`}>{fmtLatency(r.latency_ms)}</td>
                  <td className="px-2 py-1 text-right" title={fmtFull(r.input_tokens)}>{fmtNum(r.input_tokens)}</td>
                  <td className="px-2 py-1 text-right" title={fmtFull(r.output_tokens)}>{fmtNum(r.output_tokens)}</td>
                  <td className="px-2 py-1 text-right text-emerald-300/70" title={fmtFull(r.cache_read_input_tokens)}>{fmtNum(r.cache_read_input_tokens)}</td>
                  <td className="px-2 py-1 text-right text-sky-300/70" title={fmtFull(r.cache_creation_input_tokens)}>{fmtNum(r.cache_creation_input_tokens)}</td>
                  <td className="px-2 py-1 text-right font-medium text-zinc-100" title={fmtFull(r.total_tokens)}>{fmtNum(r.total_tokens)}</td>
                </tr>
              ))}
            </tbody>
            <tfoot className="sticky bottom-0 bg-[#0b0b0d]">
              <tr data-id="agent-usage-log-totals" className="border-t border-[var(--vsc-border)] font-medium text-zinc-200">
                <td className="px-2 py-1.5 text-left text-zinc-500">{t('usageTotal', '合计')} ({records.length})</td>
                <td className="px-2 py-1.5 text-left text-zinc-600"></td>
                <td className="px-2 py-1.5 text-left text-zinc-600"></td>
                <td className="px-2 py-1.5 text-right text-zinc-600">--</td>
                <td className="px-2 py-1.5 text-right text-zinc-600">--</td>
                <td className="px-2 py-1.5 text-right" title={fmtFull(totals.input)}>{fmtNum(totals.input)}</td>
                <td className="px-2 py-1.5 text-right" title={fmtFull(totals.output)}>{fmtNum(totals.output)}</td>
                <td className="px-2 py-1.5 text-right text-emerald-300/70" title={fmtFull(totals.cacheRead)}>{fmtNum(totals.cacheRead)}</td>
                <td className="px-2 py-1.5 text-right text-sky-300/70" title={fmtFull(totals.cacheWrite)}>{fmtNum(totals.cacheWrite)}</td>
                <td className="px-2 py-1.5 text-right text-zinc-100" title={fmtFull(totals.total)}>{fmtNum(totals.total)}</td>
              </tr>
            </tfoot>
          </table>
        </div>
      )}
    </div>
  );
}
