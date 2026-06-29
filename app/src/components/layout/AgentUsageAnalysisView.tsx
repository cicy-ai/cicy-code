import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Download, Loader2, RefreshCw } from 'lucide-react';
import apiService from '../../services/api';
import { ModelTag } from '../../lib/modelTag';

// Usage analysis (P1): KPI cards + cache-efficiency gauge + this-request token
// composition, derived from current.json / reply.json via /usage-analysis. SVG/
// CSS only (no chart lib). Loads on open + manual refresh (analysis is heavier
// than the live usage table, so no high-frequency poll).

interface Segment {
  key: string;
  label: string;
  est_tokens: number;
  pct: number;
  scaled_tokens: number;
  count?: number;
  messages?: number;
}
interface Analysis {
  model?: string;
  provider?: string;
  kpis?: {
    input_tokens: number; output_tokens: number; total_tokens: number;
    cache_read_input_tokens: number; cache_creation_input_tokens: number;
    cost_credit: number; cost_pricing_known?: boolean; cost_available?: boolean; cache_hit_rate: number; turn_id?: string; turn_requests?: number; status?: string;
  };
  cache?: { read: number; creation: number; fresh: number; input: number; hit_rate: number };
  raw_usage?: Record<string, any>;
  cost_breakdown?: {
    available?: boolean;
    total?: number;
    pricing_known: boolean;
    components?: { key: string; label: string; tokens: number; rate: number; cost: number; pct: number }[];
  };
  breakdown?: { est_total: number; real_input: number; segments: Segment[]; available: boolean };
  history_detail?: {
    message_count: number; total: number;
    by_kind: { kind: string; label: string; tokens: number; count: number; pct: number }[];
    top: { idx: number; role: string; kind: string; label: string; preview: string; tokens: number; pct: number; source?: string }[];
  };
  optimizable_nodes?: { kind: string; tokens: number; count: number; locations: string[]; pct: number }[];
  optimizable_nodes_absent?: string[];
  history?: { ts?: string; input: number; output: number; cache_read: number; total: number; cost: number; latency_ms: number; cache_hit_rate: number }[];
  suggestions?: { level: string; key: string; title: string; detail: string; savings?: { usd_month: number; basis: string } }[];
  savings?: { headline_usd_month: number; req_per_month: number; window_reqs: number; span_days: number; pricing_known: boolean; low_confidence?: boolean };
  cache_diag?: {
    available: boolean;
    verdict?: string;
    level?: string;
    latest_hit_rate?: number;
    rows: { ts?: string; request_id?: string; hit_rate: number; input: number; cache_read: number; changed: string[]; gap_seconds: number; expired: boolean; reason?: string; row_level?: string }[];
  };
}

function fmtNum(n?: number): string {
  if (!n || n <= 0) return '0';
  if (n < 1000) return String(n);
  if (n < 1_000_000) { const k = n / 1000; return `${k < 10 ? k.toFixed(1) : Math.round(k)}k`; }
  const m = n / 1_000_000;
  return `${m < 10 ? m.toFixed(2) : m.toFixed(1)}m`;
}
function fmtFull(n?: number): string { return !n || n <= 0 ? '0' : n.toLocaleString(); }
// Money: keep small figures legible (sub-$1 shows cents/sub-cents) without ever
// rounding a real cost down to a misleading $0.
function fmtMoney(n?: number): string {
  const v = n || 0;
  if (v <= 0) return '$0';
  if (v < 0.01) return '<$0.01';
  if (v < 1) return `$${v.toFixed(2)}`;
  if (v < 100) return `$${v.toFixed(1)}`;
  return `$${Math.round(v).toLocaleString()}`;
}
function fmtPct(x?: number): string { return `${Math.round((x || 0) * 100)}%`; }
// Hit-rate formatting: never round a sub-100% value up to a misleading "100%",
// and never show a flat "0%" for a tiny-but-nonzero rate.
function fmtPct1(x?: number): string {
  const v = (x || 0) * 100;
  if (v <= 0) return '0%';
  if (v >= 100) return '100%';
  if (v > 99.9) return '99.9%';
  if (v < 0.1) return '<0.1%';
  return `${v.toFixed(1)}%`;
}
function fmtTime(ts?: string): string {
  if (!ts) return '--';
  const p = Date.parse(ts);
  if (!Number.isFinite(p)) return ts;
  return new Date(p).toLocaleTimeString(undefined, { hour12: false });
}
function fmtGap(sec?: number): string {
  if (!sec || sec <= 0) return '--';
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  return `${(sec / 3600).toFixed(1)}h`;
}
// Plain-language status for one cache-diag row. Judged by ACTUAL hit rate, not
// raw hash equality — a prefix can shift every request yet still hit ~99%.
function diagReasonText(reason: string | undefined, t: (k: string, d?: string) => string): string {
  switch (reason) {
    case 'first': return t('anDiagReasonFirst', '首次请求 · 基准');
    case 'ok': return t('anDiagReasonOk', '正常命中');
    case 'ok_dynamic': return t('anDiagReasonOkDyn', '正常命中（头部有动态内容，但未影响缓存）');
    case 'expired': return t('anDiagReasonExpired', '间隔超过 5 分钟，缓存过期重建');
    case 'miss_system': return t('anDiagReasonMissSystem', 'system 提示变化，前缀缓存失效');
    case 'miss_tools': return t('anDiagReasonMissTools', '工具集变化，缓存失效');
    case 'miss_first_msg': return t('anDiagReasonMissFirst', '历史头部被改写，缓存失效');
    case 'miss_other': return t('anDiagReasonMissOther', '前缀未变但命中偏低（provider 缓存可能未生效）');
    default: return '';
  }
}
const ROW_LEVEL_TEXT: Record<string, string> = { ok: 'text-emerald-300', neutral: 'text-zinc-500', warn: 'text-amber-300', bad: 'text-red-300' };
const ROW_LEVEL_DOT: Record<string, string> = { ok: 'bg-emerald-400', neutral: 'bg-zinc-500/70', warn: 'bg-amber-400', bad: 'bg-red-400' };

const SEG_COLORS: Record<string, string> = {
  system: 'bg-violet-400/70',
  tools: 'bg-amber-400/70',
  history: 'bg-sky-400/70',
};
const SEG_TEXT: Record<string, string> = {
  system: 'text-violet-300',
  tools: 'text-amber-300',
  history: 'text-sky-300',
};
// SVG stroke colors for the request-composition donut (match SEG_COLORS palette).
const SEG_HEX: Record<string, string> = {
  system: '#a78bfa', // violet-400
  tools: '#fbbf24',  // amber-400
  history: '#38bdf8', // sky-400
};

const COST_COLOR: Record<string, string> = {
  fresh_input: 'bg-sky-400/70',
  cache_read: 'bg-emerald-400/70',
  cache_write: 'bg-violet-400/70',
  output: 'bg-rose-400/70',
};

const KIND_COLOR: Record<string, string> = {
  user_text: 'bg-zinc-400/70',
  assistant_text: 'bg-violet-400/70',
  tool_use: 'bg-amber-400/70',
  tool_result: 'bg-rose-400/70',
  thinking: 'bg-sky-400/70',
};

// Optimizable resident components: stable color + the i18n keys for label/hint.
// `chip` is the compact tag shown on "最大的几段" rows; the panel uses label+hint.
const NODE_STYLE: Record<string, { dot: string; chip: string; labelKey: string; labelDefault: string; hintKey: string; hintDefault: string }> = {
  compact: { dot: 'bg-orange-400/80', chip: 'bg-orange-400/15 text-orange-300', labelKey: 'anNodeCompact', labelDefault: '/compact 产物', hintKey: 'anNodeCompactHint', hintDefault: '/compact 把上下文冻进历史（摘要 + 冻结快照），每次请求都带着；只有 /clear 能真正清除' },
  compact_summary: { dot: 'bg-orange-400/80', chip: 'bg-orange-400/15 text-orange-300', labelKey: 'anNodeCompactSummary', labelDefault: '/compact 摘要', hintKey: 'anNodeCompactHint', hintDefault: '/compact 把上下文冻进历史（摘要 + 冻结快照），每次请求都带着；只有 /clear 能真正清除' },
  compact_snapshot: { dot: 'bg-orange-400/80', chip: 'bg-orange-400/15 text-orange-300', labelKey: 'anNodeCompactSnapshot', labelDefault: '/compact 快照', hintKey: 'anNodeCompactHint', hintDefault: '/compact 把上下文冻进历史（摘要 + 冻结快照），每次请求都带着；只有 /clear 能真正清除' },
  claude_md: { dot: 'bg-teal-400/80', chip: 'bg-teal-400/15 text-teal-300', labelKey: 'anNodeClaudeMd', labelDefault: 'CLAUDE.md / 项目指令', hintKey: 'anNodeClaudeMdHint', hintDefault: '项目指令每次请求常驻；精简 CLAUDE.md 可降低每次开销' },
  skills: { dot: 'bg-fuchsia-400/80', chip: 'bg-fuchsia-400/15 text-fuchsia-300', labelKey: 'anNodeSkills', labelDefault: 'Skills 目录', hintKey: 'anNodeSkillsHint', hintDefault: '技能目录常驻；停用不用的 skill 或换精简版可减负' },
  mcp: { dot: 'bg-cyan-400/80', chip: 'bg-cyan-400/15 text-cyan-300', labelKey: 'anNodeMcp', labelDefault: 'MCP 工具', hintKey: 'anNodeMcpHint', hintDefault: 'MCP 工具 schema 常驻 tools；停用不用的 MCP server 可减负' },
};

// Explicit "not detected" lines for trackable components that are absent — so a
// blank reads as a stated conclusion, not a possible bug.
const NODE_ABSENT: Record<string, { key: string; default: string }> = {
  skills: { key: 'anNodeAbsentSkills', default: '未检测到 skills 目录（本次请求未注入）' },
  mcp: { key: 'anNodeAbsentMcp', default: '未挂载 MCP server' },
};

// Minimal SVG sparkline (no chart lib). Scales values to the viewbox.
function Sparkline({ values, stroke, fill, height = 34 }: { values: number[]; stroke: string; fill?: string; height?: number }) {
  if (!values.length) return <div style={{ height }} />;
  const w = 200;
  const h = height;
  const max = Math.max(...values);
  const min = Math.min(...values, 0);
  const range = max - min || 1;
  const x = (i: number) => (values.length === 1 ? w : (i / (values.length - 1)) * w);
  const y = (v: number) => h - ((v - min) / range) * (h - 2) - 1;
  const line = values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ');
  const area = `0,${h} ${line} ${w},${h}`;
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className="w-full" style={{ height }}>
      {fill ? <polyline points={area} fill={fill} stroke="none" /> : null}
      <polyline points={line} fill="none" stroke={stroke} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

// Donut chart for a parts-of-a-whole breakdown, with a centered total. Pure SVG:
// each slice is a circle arc drawn via stroke-dasharray; the group is rotated so
// slices start at 12 o'clock and stack clockwise.
function Donut({ slices, centerValue, centerLabel, size = 116, thickness = 14 }: {
  slices: { key: string; pct: number; color: string }[];
  centerValue: string;
  centerLabel: string;
  size?: number;
  thickness?: number;
}) {
  const r = (size - thickness) / 2;
  const c = 2 * Math.PI * r;
  const cx = size / 2;
  let acc = 0;
  return (
    <svg viewBox={`0 0 ${size} ${size}`} width={size} height={size} className="shrink-0" role="img">
      <g transform={`rotate(-90 ${cx} ${cx})`}>
        <circle cx={cx} cy={cx} r={r} fill="none" stroke="rgba(255,255,255,0.06)" strokeWidth={thickness} />
        {slices.filter((s) => s.pct > 0).map((s) => {
          const len = s.pct * c;
          const el = (
            <circle
              key={s.key} cx={cx} cy={cx} r={r} fill="none"
              stroke={s.color} strokeWidth={thickness}
              strokeDasharray={`${len} ${c - len}`} strokeDashoffset={-acc * c}
            >
              <title>{`${Math.round(s.pct * 100)}%`}</title>
            </circle>
          );
          acc += s.pct;
          return el;
        })}
      </g>
      <text x="50%" y="45%" textAnchor="middle" dominantBaseline="middle" className="fill-zinc-100" style={{ fontSize: 19, fontWeight: 600 }}>{centerValue}</text>
      <text x="50%" y="62%" textAnchor="middle" dominantBaseline="middle" className="fill-zinc-500" style={{ fontSize: 9, textTransform: 'uppercase', letterSpacing: '0.05em' }}>{centerLabel}</text>
    </svg>
  );
}

export default function AgentUsageAnalysisView({ paneId, active }: { paneId: string; active: boolean }) {
  const { t } = useTranslation('agentInspector');
  const [data, setData] = useState<Analysis | null>(null);
  const [loading, setLoading] = useState(false);
  const loadedRef = useRef(false);

  const refresh = useCallback(async (silent = false) => {
    if (!paneId) return;
    if (!silent) setLoading(true);
    try {
      const { data: next } = await apiService.getAgentUsageAnalysis(paneId);
      setData(next || null);
    } catch {
      /* keep last good */
    } finally {
      loadedRef.current = true;
      if (!silent) setLoading(false);
    }
  }, [paneId]);

  useEffect(() => { loadedRef.current = false; setData(null); }, [paneId]);
  useEffect(() => { if (active && paneId) refresh(); }, [active, paneId, refresh]);

  // Download the full (untruncated) content of one history block by its message
  // index — the panel only carries a short preview. Pulls messages[idx] from
  // current.json via the backend and saves it as a .txt.
  const [dlIdx, setDlIdx] = useState<number | null>(null);
  const downloadBlock = useCallback(async (idx: number) => {
    if (!paneId) return;
    setDlIdx(idx);
    try {
      const { data } = await apiService.getAgentUsageBlock(paneId, idx);
      const text = typeof data?.text === 'string' ? data.text : '';
      const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${paneId}-block-${idx + 1}.txt`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch {
      /* ignore */
    } finally {
      setDlIdx(null);
    }
  }, [paneId]);

  // Auto-refresh once per second while the view is active. Silent so the data
  // updates live without the spinner flickering or blanking on transient errors.
  useEffect(() => {
    if (!active || !paneId) return;
    const id = window.setInterval(() => { refresh(true); }, 1000);
    return () => window.clearInterval(id);
  }, [active, paneId, refresh]);

  const k = data?.kpis;
  const segments = data?.breakdown?.segments || [];

  const cacheHealth = (rate: number) => (rate >= 0.8 ? 'text-emerald-300' : rate >= 0.5 ? 'text-amber-300' : 'text-red-300');

  // Cost is shown only when the model has a confirmed price template; otherwise
  // we display token consumption only (no fabricated estimate).
  const costAvailable = k?.cost_available !== false && k?.cost_pricing_known !== false;
  const kpiCards = [
    { id: 'total', label: t('anColTotal', '总 tokens'), value: fmtNum(k?.total_tokens), full: fmtFull(k?.total_tokens) },
    ...(costAvailable
      ? [{ id: 'cost', label: t('anColCost', '成本'), value: `$${(k?.cost_credit ?? 0).toFixed(2)}`, full: `$${k?.cost_credit ?? 0}` }]
      : []),
    { id: 'input', label: t('anColInput', '输入'), value: fmtNum(k?.input_tokens), full: fmtFull(k?.input_tokens) },
    { id: 'cacheHit', label: t('anColCacheHit', '缓存命中'), value: fmtPct1(k?.cache_hit_rate), full: fmtPct1(k?.cache_hit_rate), cls: cacheHealth(k?.cache_hit_rate || 0) },
    { id: 'output', label: t('anColOutput', '输出'), value: fmtNum(k?.output_tokens), full: fmtFull(k?.output_tokens) },
  ];

  return (
    <div data-id="agent-usage-analysis" className="flex h-full w-full flex-col overflow-hidden">
      <div data-id="agent-usage-analysis-header" className="flex shrink-0 items-center gap-2 border-b border-[var(--vsc-border)] px-3 py-1.5">
        {data?.model ? (
          <ModelTag model={data.model} />
        ) : null}
        {k?.status ? <span className="text-[11px] text-zinc-500">{k.status}</span> : null}
        {k?.turn_requests && k.turn_requests > 0 ? (
          <span data-id="agent-usage-analysis-turnreqs" className="text-[11px] text-zinc-500" title={t('anTurnScope', '统计口径：最近一轮（一次用户提问触发的全部请求）')}>
            {t('anTurnLabel', '本轮')} · {k.turn_requests} {t('anTurnReqs', '次请求')}
          </span>
        ) : null}
        <button
          data-id="agent-usage-analysis-refresh"
          type="button"
          onClick={() => refresh()}
          className="ml-auto inline-flex h-7 w-7 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
          title={t('anRefresh', '刷新')}
          aria-label={t('anRefresh', '刷新')}
        >
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
        </button>
      </div>

      {loading && !data ? (
        <div data-id="agent-usage-analysis-loading" className="flex flex-1 items-center justify-center gap-2 text-[13px] text-zinc-500">
          <Loader2 className="h-4 w-4 animate-spin" /> {t('memLoading', '加载中…')}
        </div>
      ) : (!k || (k.total_tokens === 0 && k.input_tokens === 0)) && segments.length === 0 ? (
        // 兜底:只有当「最近一轮 0 token」**且** breakdown 也没有任何构成数据时才算真空。
        // 否则(例如最后一次是取消/失败回合)仍渲染面板,避免把整页打成空白。
        <div data-id="agent-usage-analysis-empty" className="flex flex-1 items-center justify-center px-4 text-center text-[13px] text-zinc-500">
          {t('anEmpty', '暂无可分析的请求数据')}
        </div>
      ) : (
        <div data-id="agent-usage-analysis-scroll" className="min-h-0 flex-1 space-y-4 overflow-auto p-3">
          {/* KPI cards */}
          <div data-id="agent-usage-analysis-kpis" className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            {kpiCards.map((c) => (
              <div key={c.id} data-id={`agent-usage-kpi-${c.id}`} className="rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-2" title={c.full}>
                <div className="text-[10px] uppercase tracking-wide text-zinc-500">{c.label}</div>
                <div className={`mt-0.5 text-[16px] font-semibold tabular-nums ${c.cls || 'text-zinc-100'}`}>{c.value}</div>
              </div>
            ))}
          </div>

          {/* Savings headline: the single biggest lever, in money/month */}
          {data?.savings && data.savings.headline_usd_month > 0 ? (
            <div data-id="agent-usage-analysis-savings" className="rounded-lg border border-emerald-400/20 bg-emerald-400/[0.06] px-3 py-2.5">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <div className="text-[10px] uppercase tracking-wide text-emerald-300/80">{t('anSavingsTitle', '预计可节省（按当前频率）')}</div>
                  <div className="mt-0.5 flex items-baseline gap-1">
                    <span data-id="agent-usage-savings-amount" className="text-[22px] font-semibold tabular-nums text-emerald-200">{fmtMoney(data.savings.headline_usd_month)}</span>
                    <span className="text-[12px] text-emerald-300/70">/ {t('anSavingsPerMonth', '月')}</span>
                  </div>
                </div>
                <div className="text-right text-[10px] leading-4 text-emerald-300/50">
                  {data.savings.req_per_month > 0 ? (
                    <div>~{fmtNum(Math.round(data.savings.req_per_month))} {t('anSavingsReqMo', '次请求/月')}</div>
                  ) : null}
                  <div>{t('anSavingsBasis', '基于最近')} {data.savings.window_reqs} {t('anSavingsReqs', '次请求')}</div>
                </div>
              </div>
              <div className="mt-1 text-[10px] text-emerald-300/40">
                {data.savings.low_confidence
                  ? t('anSavingsLowConf', '观测窗口较短，按单日活跃量保守估算；持续使用后会更准')
                  : t('anSavingsHint', '采纳下方最高价值的优化项的估算值，详见各项说明')}
              </div>
            </div>
          ) : null}

          {/* Cost breakdown by billing bucket */}
          {data?.cost_breakdown && data.cost_breakdown.available !== false && (data.cost_breakdown.total || 0) > 0 ? (
            <div data-id="agent-usage-analysis-cost" className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
              <div className="mb-2 flex items-center justify-between">
                <div className="text-[11px] font-medium uppercase tracking-wide text-zinc-400">{t('anCostTitle', '成本构成')}</div>
                <div className="text-[13px] font-semibold tabular-nums text-zinc-100">${data.cost_breakdown.total.toFixed(2)}</div>
              </div>
              <div data-id="agent-usage-cost-bar" className="flex h-3 w-full overflow-hidden rounded-full bg-white/[0.04]">
                {data.cost_breakdown.components.filter((c) => c.cost > 0).map((c) => (
                  <div key={c.key} className={COST_COLOR[c.key] || 'bg-zinc-400/60'} style={{ width: `${c.pct * 100}%` }} title={`${c.label}: $${c.cost.toFixed(4)} (${fmtPct(c.pct)})`} />
                ))}
              </div>
              <div className="mt-2 space-y-1">
                {data.cost_breakdown.components.map((c) => (
                  <div key={c.key} data-id={`agent-usage-cost-row-${c.key}`} className="flex items-center justify-between text-[12px]">
                    <span className="inline-flex items-center gap-1.5">
                      <span className={`h-2 w-2 rounded-sm ${COST_COLOR[c.key] || 'bg-zinc-400/60'}`} />
                      <span className="text-zinc-300">{c.label}</span>
                      <span className="text-zinc-600" title={`${t('anCostRate', '单价')}: $${c.rate}/M`}>{fmtNum(c.tokens)} × ${c.rate}/M</span>
                    </span>
                    <span className="flex items-center gap-2 tabular-nums">
                      <span className="text-zinc-500">{fmtPct(c.pct)}</span>
                      <span className="w-16 text-right text-zinc-100">${c.cost.toFixed(c.cost < 1 ? 4 : 2)}</span>
                    </span>
                  </div>
                ))}
              </div>
              {data.cost_breakdown.pricing_known === false ? (
                <div className="mt-1 text-[10px] text-zinc-600">{t('anPriceUnknown', '该模型无内置定价,用通用估算')}</div>
              ) : null}
              {data?.raw_usage ? (
                <details data-id="agent-usage-raw" className="mt-2 border-t border-white/[0.06] pt-2">
                  <summary className="cursor-pointer text-[10px] uppercase tracking-wide text-zinc-500 hover:text-zinc-300">{t('anRawUsage', 'provider 原始 usage')}</summary>
                  <div data-id="agent-usage-raw-rows" className="mt-1.5 space-y-0.5">
                    {Object.entries(data.raw_usage)
                      .filter(([, v]) => typeof v === 'number' || typeof v === 'string')
                      .map(([key, v]) => (
                        <div key={key} className="flex items-center justify-between text-[11px]">
                          <span className="font-mono text-zinc-500">{key}</span>
                          <span className="tabular-nums text-zinc-300">{typeof v === 'number' ? (v as number).toLocaleString() : String(v)}</span>
                        </div>
                      ))}
                  </div>
                  <div className="mt-1 text-[10px] text-zinc-600">{t('anRawUsageHint', '上面的缓存/输入数字均原样取自此处,未经估算')}</div>
                </details>
              ) : null}
            </div>
          ) : null}

          {/* Cache-hit diagnosis (last few requests) */}
          {data?.cache_diag?.available ? (
            <div data-id="agent-usage-analysis-cachediag" className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
              <div className="mb-2 text-[11px] font-medium uppercase tracking-wide text-zinc-400">{t('anDiagTitle', '缓存诊断')}</div>
              <table className="w-full border-collapse text-[12px] tabular-nums">
                <thead>
                  <tr className="text-zinc-500">
                    <th className="px-2 py-1 text-left font-medium">{t('usageColTime', '时间')}</th>
                    <th className="px-2 py-1 text-right font-medium">{t('anColCacheHit', '缓存命中')}</th>
                    <th className="px-2 py-1 text-left font-medium">{t('anDiagStatus', '状态')}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.cache_diag.rows.map((row, i) => {
                    const lvl = row.row_level || 'neutral';
                    return (
                      <tr key={`${row.request_id || i}`} data-id={`agent-usage-cachediag-row-${i}`} className="border-t border-white/[0.04] text-zinc-300">
                        <td className="px-2 py-1.5 text-left text-zinc-500">{fmtTime(row.ts)}</td>
                        <td className={`px-2 py-1.5 text-right font-medium ${cacheHealth(row.hit_rate)}`}>{fmtPct1(row.hit_rate)}</td>
                        <td className="px-2 py-1.5 text-left">
                          <span className="inline-flex items-center gap-1.5">
                            <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${ROW_LEVEL_DOT[lvl] || ROW_LEVEL_DOT.neutral}`} />
                            <span className={ROW_LEVEL_TEXT[lvl] || ROW_LEVEL_TEXT.neutral}>{diagReasonText(row.reason, (k, d) => (d === undefined ? t(k) : t(k, d)))}</span>
                            {i > 0 && row.gap_seconds > 0 ? (
                              <span className="text-zinc-600">· {t('anDiagGap', '间隔')} {fmtGap(row.gap_seconds)}</span>
                            ) : null}
                          </span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : null}

          {/* This-request composition */}
          <div data-id="agent-usage-analysis-breakdown" className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
            <div className="mb-2 flex items-center justify-between">
              <div className="text-[11px] font-medium uppercase tracking-wide text-zinc-400">{t('anBreakdownTitle', '本次请求构成')}</div>
              <div className="text-[10px] text-zinc-600">{t('anEstimate', '~估算')}</div>
            </div>
            <div className="flex items-center gap-4">
              <Donut
                slices={segments.map((s) => ({ key: s.key, pct: s.pct, color: SEG_HEX[s.key] || '#a1a1aa' }))}
                centerValue={fmtNum((data?.breakdown?.real_input || 0) || segments.reduce((a, s) => a + (s.scaled_tokens || 0), 0))}
                centerLabel={t('anColInput', '输入')}
              />
              <div data-id="agent-usage-breakdown-legend" className="min-w-0 flex-1 space-y-1.5">
                {segments.map((s) => (
                  <div key={s.key} data-id={`agent-usage-breakdown-row-${s.key}`} className="flex items-center justify-between text-[12px]">
                    <span className="inline-flex items-center gap-1.5">
                      <span className={`h-2 w-2 rounded-sm ${SEG_COLORS[s.key] || 'bg-zinc-400/60'}`} />
                      <span className={SEG_TEXT[s.key] || 'text-zinc-300'}>{s.label}</span>
                      {s.key === 'tools' && typeof s.count === 'number' ? <span className="text-zinc-600">×{s.count}</span> : null}
                      {s.key === 'history' && typeof s.messages === 'number' ? <span className="text-zinc-600">×{s.messages}</span> : null}
                    </span>
                    <span className="flex items-center gap-2 tabular-nums text-zinc-400">
                      <span className="text-zinc-500">{fmtPct(s.pct)}</span>
                      <span className="text-zinc-200" title={`~${fmtFull(s.scaled_tokens)}`}>{fmtNum(s.scaled_tokens)}</span>
                    </span>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {/* Deep history breakdown */}
          {data?.history_detail && data.history_detail.total > 0 ? (
            <div data-id="agent-usage-analysis-history" className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
              <div className="mb-2 flex items-center justify-between">
                <div className="text-[11px] font-medium uppercase tracking-wide text-zinc-400">{t('anHistoryTitle', '历史构成')}</div>
                <div className="text-[10px] text-zinc-600">{data.history_detail.message_count} {t('anMessages', '条消息')} · ~{t('anEstimate', '估算')}</div>
              </div>
              <div className="space-y-1">
                {data.history_detail.by_kind.filter((kk) => kk.tokens > 0).map((kk) => (
                  <div key={kk.kind} data-id={`agent-usage-history-kind-${kk.kind}`} className="flex items-center gap-2 text-[12px]">
                    <span className="inline-flex w-20 shrink-0 items-center gap-1.5 text-zinc-300">
                      <span className={`h-2 w-2 rounded-sm ${KIND_COLOR[kk.kind] || 'bg-zinc-400/60'}`} />{kk.label}
                    </span>
                    <span className="h-2 flex-1 overflow-hidden rounded-full bg-white/[0.04]">
                      <span className={`block h-full ${KIND_COLOR[kk.kind] || 'bg-zinc-400/60'}`} style={{ width: `${kk.pct * 100}%` }} />
                    </span>
                    <span className="w-10 shrink-0 text-right text-zinc-500 tabular-nums">{fmtPct(kk.pct)}</span>
                    <span className="w-12 shrink-0 text-right text-zinc-200 tabular-nums" title={fmtFull(kk.tokens)}>{fmtNum(kk.tokens)}</span>
                  </div>
                ))}
              </div>
              {data.history_detail.top.length ? (
                <div data-id="agent-usage-history-top" className="mt-3 border-t border-white/[0.06] pt-2">
                  <div className="mb-1 flex items-baseline gap-2">
                    <span className="text-[10px] uppercase tracking-wide text-zinc-600">{t('anTopBlocks', '最大的几段')}</span>
                    <span className="text-[10px] text-zinc-600/80">{t('anTopBlocksHint', '单块明细 · 非累计')}</span>
                  </div>
                  <div className="space-y-1">
                    {data.history_detail.top.map((b, i) => (
                      <div key={`${b.idx}-${i}`} data-id={`agent-usage-history-top-${i}`} className="flex items-center gap-2 text-[12px]">
                        <span className={`h-2 w-2 shrink-0 rounded-sm ${KIND_COLOR[b.kind] || 'bg-zinc-400/60'}`} />
                        <span className="shrink-0 text-zinc-600 tabular-nums" title={t('anTopBlockPos', '在历史中的位置')}>#{b.idx + 1}</span>
                        {b.source && NODE_STYLE[b.source] ? (
                          <span className={`shrink-0 rounded px-1 text-[10px] ${NODE_STYLE[b.source].chip}`} title={t(NODE_STYLE[b.source].hintKey, NODE_STYLE[b.source].hintDefault)}>
                            {t(NODE_STYLE[b.source].labelKey, NODE_STYLE[b.source].labelDefault)}
                          </span>
                        ) : null}
                        <span className="shrink-0 text-zinc-500">{b.label}</span>
                        <span className="min-w-0 flex-1 truncate text-zinc-400" title={b.preview}>{b.preview || '—'}</span>
                        <span className="shrink-0 text-zinc-500 tabular-nums">{fmtPct(b.pct)}</span>
                        <span className="w-12 shrink-0 text-right text-zinc-200 tabular-nums" title={fmtFull(b.tokens)}>{fmtNum(b.tokens)}</span>
                        <button
                          type="button"
                          data-id={`agent-usage-history-top-dl-${b.idx}`}
                          onClick={() => downloadBlock(b.idx)}
                          disabled={dlIdx === b.idx}
                          title={t('anBlockDownload', '下载这一段完整内容')}
                          className="shrink-0 rounded p-0.5 text-zinc-600 transition-colors hover:bg-white/[0.08] hover:text-zinc-200 disabled:opacity-40"
                        >
                          {dlIdx === b.idx ? <Loader2 className="h-3 w-3 animate-spin" /> : <Download className="h-3 w-3" />}
                        </button>
                      </div>
                    ))}
                  </div>
                </div>
              ) : null}
            </div>
          ) : null}

          {/* Optimizable resident nodes: what rides every request + how to shrink it */}
          {(data?.optimizable_nodes && data.optimizable_nodes.length > 0) || (data?.optimizable_nodes_absent && data.optimizable_nodes_absent.length > 0) ? (
            <div data-id="agent-usage-analysis-nodes" className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
              <div className="mb-2 flex items-baseline justify-between">
                <div className="text-[11px] font-medium uppercase tracking-wide text-zinc-400">{t('anNodesTitle', '可优化节点')}</div>
                <div className="text-[10px] text-zinc-600">{t('anNodesHint', '常驻每次请求 · 可主动精简')}</div>
              </div>
              <div className="space-y-2">
                {(data.optimizable_nodes || []).map((n) => {
                  const st = NODE_STYLE[n.kind];
                  return (
                    <div key={n.kind} data-id={`agent-usage-node-${n.kind}`} className="rounded-md border border-white/[0.05] bg-white/[0.015] p-2">
                      <div className="flex items-center gap-2 text-[12px]">
                        <span className={`h-2 w-2 shrink-0 rounded-sm ${st ? st.dot : 'bg-zinc-400/60'}`} />
                        <span className="shrink-0 font-medium text-zinc-200">{st ? t(st.labelKey, st.labelDefault) : n.kind}</span>
                        {n.count > 1 ? <span className="shrink-0 text-zinc-600">×{n.count}</span> : null}
                        {n.locations && n.locations.length ? (
                          <span className="min-w-0 flex-1 truncate text-[10px] text-zinc-600" title={n.locations.join(' ')}>{t('anNodeAt', '位于')} {n.locations.join(' ')}</span>
                        ) : <span className="flex-1" />}
                        <span className="shrink-0 text-zinc-500 tabular-nums">{fmtPct(n.pct)}</span>
                        <span className="w-12 shrink-0 text-right text-zinc-200 tabular-nums" title={fmtFull(n.tokens)}>{fmtNum(n.tokens)}</span>
                      </div>
                      {st ? <div className="mt-1 pl-4 text-[11px] leading-snug text-zinc-500">{t(st.hintKey, st.hintDefault)}</div> : null}
                    </div>
                  );
                })}
                {(data.optimizable_nodes_absent || []).map((k) => {
                  const ab = NODE_ABSENT[k];
                  if (!ab) return null;
                  return (
                    <div key={`absent-${k}`} data-id={`agent-usage-node-absent-${k}`} className="flex items-center gap-2 px-2 text-[11px] text-zinc-600">
                      <span className="h-1.5 w-1.5 shrink-0 rounded-sm bg-zinc-700" />
                      <span>{t(ab.key, ab.default)}</span>
                    </div>
                  );
                })}
              </div>
            </div>
          ) : null}

          {/* History trend */}
          {data?.history && data.history.length >= 2 ? (
            <div data-id="agent-usage-analysis-trend" className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
              <div className="mb-2 text-[11px] font-medium uppercase tracking-wide text-zinc-400">{t('anTrendTitle', '历史趋势')} <span className="text-zinc-600 normal-case">({data.history.length})</span></div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                <div data-id="agent-usage-trend-input">
                  <div className="mb-1 flex items-center justify-between text-[11px] text-zinc-500">
                    <span>{t('anTrendInput', '输入 token')}</span>
                    <span className="tabular-nums text-zinc-300">{fmtNum(data.history[data.history.length - 1].input)}</span>
                  </div>
                  <Sparkline values={data.history.map((p) => p.input)} stroke="#38bdf8" fill="rgba(56,189,248,0.10)" />
                </div>
                <div data-id="agent-usage-trend-cache">
                  <div className="mb-1 flex items-center justify-between text-[11px] text-zinc-500">
                    <span>{t('anTrendCacheHit', '缓存命中率')}</span>
                    <span className="tabular-nums text-zinc-300">{fmtPct(data.history[data.history.length - 1].cache_hit_rate)}</span>
                  </div>
                  <Sparkline values={data.history.map((p) => p.cache_hit_rate)} stroke="#34d399" fill="rgba(52,211,153,0.10)" />
                </div>
                <div data-id="agent-usage-trend-cost">
                  <div className="mb-1 flex items-center justify-between text-[11px] text-zinc-500">
                    <span>{t('anTrendCost', '成本/请求')}</span>
                    <span className="tabular-nums text-zinc-300">{data.history[data.history.length - 1].cost.toFixed(2)}</span>
                  </div>
                  <Sparkline values={data.history.map((p) => p.cost)} stroke="#f59e0b" fill="rgba(245,158,11,0.10)" />
                </div>
              </div>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}
