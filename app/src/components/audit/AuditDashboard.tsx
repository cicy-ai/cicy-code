import { useState, useEffect, useRef, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { BarChart3, Activity, Zap, Settings, ArrowLeft, Download, Copy, Check, DollarSign, Hash, Clock, TrendingUp, Cpu, ShieldCheck, RefreshCw, ChevronRight, Filter, SlidersHorizontal, Save, RotateCcw, Trash2, AlertCircle } from 'lucide-react';
import { Spinner } from '../ui/Spinner';
import apiService from '../../services/api';
import config from '../../config';
import { TokenManager } from '../../services/tokenManager';

type Tab = 'findings' | 'policy' | 'overview' | 'usage' | 'live' | 'setup';

interface DashboardData {
  user_id: string;
  period_days: number;
  total_cost_usd: number;
  total_calls: number;
  total_input: number;
  total_output: number;
  monthly_calls: number;
  daily: { date: string; calls: number; input_tokens: number; output_tokens: number; cost_usd: number }[];
  model_breakdown: Record<string, { calls: number; input_tokens: number; output_tokens: number; cost: number }>;
}

interface UsageEntry {
  user_id: string;
  method: string;
  url: string;
  host: string;
  status: number;
  req_kb: number;
  res_kb: number;
  ts: number;
  ai_usage?: {
    provider: string;
    model: string;
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
    cost_usd: number;
  };
}

interface SetupGuide {
  proxy_host: string;
  proxy_port: string;
  ca_cert_url: string;
  install_cmd: string;
  ca_ready: boolean;
  platforms: { name: string; steps: string[] }[];
}

const formatCost = (v: number) => v < 0.01 ? `$${v.toFixed(4)}` : `$${v.toFixed(2)}`;
const formatTokens = (v: number) => v >= 1_000_000 ? `${(v / 1_000_000).toFixed(1)}M` : v >= 1_000 ? `${(v / 1_000).toFixed(1)}K` : `${v}`;
const formatTime = (ts: number) => {
  const d = new Date(ts * 1000);
  return d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
};
const relativeTime = (ts: number) => {
  const diff = Date.now() / 1000 - ts;
  if (diff < 60) return i18n.t('secondsAgo', { ns: 'audit', n: Math.floor(diff) });
  if (diff < 3600) return i18n.t('minutesAgo', { ns: 'audit', n: Math.floor(diff / 60) });
  return i18n.t('hoursAgo', { ns: 'audit', n: Math.floor(diff / 3600) });
};

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  return (
    <button data-id="audit-dashboard-copy-button" onClick={copy} className="ml-2 p-1 rounded hover:bg-white/10 text-[var(--vsc-text-secondary)] hover:text-white transition-colors">
      {copied ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
    </button>
  );
}

function StatCard({ icon: Icon, label, value, sub, color = 'text-blue-400' }: { icon: any; label: string; value: string; sub?: string; color?: string }) {
  return (
    <div data-id="audit-dashboard-stat-card" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
      <div data-id="audit-dashboard-stat-card-header" className="flex items-center gap-2 mb-2">
        <Icon size={16} className={color} />
        <span data-id="audit-dashboard-stat-card-label" className="text-xs text-[var(--vsc-text-secondary)]">{label}</span>
      </div>
      <div data-id="audit-dashboard-stat-card-value" className="text-xl font-semibold text-white">{value}</div>
      {sub && <div data-id="audit-dashboard-stat-card-sub" className="text-xs text-[var(--vsc-text-muted)] mt-1">{sub}</div>}
    </div>
  );
}

function MiniBarChart({ data, maxValue }: { data: { label: string; value: number }[]; maxValue: number }) {
  return (
    <div data-id="audit-dashboard-mini-bar-chart" className="flex items-end gap-1 h-24">
      {data.map((d, i) => (
        <div key={i} data-id={`audit-dashboard-mini-bar-chart-col-${i}`} className="flex-1 flex flex-col items-center gap-1">
          <div data-id={`audit-dashboard-mini-bar-chart-bar-${i}`} className="w-full bg-blue-500/20 rounded-t relative overflow-hidden" style={{ height: maxValue > 0 ? `${Math.max(2, (d.value / maxValue) * 80)}px` : '2px' }}>
            <div data-id={`audit-dashboard-mini-bar-chart-bar-fill-${i}`} className="absolute bottom-0 w-full bg-blue-500 rounded-t" style={{ height: '100%' }} />
          </div>
          <span data-id={`audit-dashboard-mini-bar-chart-label-${i}`} className="text-[9px] text-[var(--vsc-text-muted)] truncate w-full text-center">{d.label}</span>
        </div>
      ))}
    </div>
  );
}

// ── Overview Tab ──
function OverviewTab({ userId, days, setDays }: { userId: string; days: number; setDays: (d: number) => void }) {
  const { t } = useTranslation('audit');
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    apiService.getAuditDashboard(userId, days)
      .then(r => setData(r.data?.data))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [userId, days]);

  if (loading) return <div data-id="audit-dashboard-overview-loading" className="flex items-center justify-center h-64 gap-2 text-[var(--vsc-text-secondary)]"><Spinner size="md" /> {t('loading')}</div>;
  if (!data) return <div data-id="audit-dashboard-overview-empty" className="text-center text-[var(--vsc-text-secondary)] py-12">{t('noData')}</div>;

  const dailyReversed = [...data.daily].reverse();
  const maxCalls = Math.max(...dailyReversed.map(d => d.calls), 1);
  const maxCost = Math.max(...dailyReversed.map(d => d.cost_usd), 0.01);

  const modelEntries = Object.entries(data.model_breakdown)
    .sort((a, b) => (b[1].cost || 0) - (a[1].cost || 0));

  return (
    <div data-id="audit-dashboard-overview" className="space-y-6">
      {/* Period selector */}
      <div data-id="audit-dashboard-overview-period-selector" className="flex items-center gap-2">
        {[7, 14, 30].map(d => (
            <button key={d} data-id={`audit-dashboard-overview-period-${d}`} onClick={() => setDays(d)}
            className={`px-3 py-1 text-xs rounded-md transition-colors ${days === d ? 'bg-blue-600 text-white' : 'bg-[var(--vsc-bg-hover)] text-[var(--vsc-text-secondary)] hover:text-white'}`}>
            {t('rangeDays', { n: d })}
          </button>
        ))}
      </div>

      {/* Summary cards */}
      <div data-id="audit-dashboard-overview-stats" className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard icon={DollarSign} label={t('totalCost')} value={formatCost(data.total_cost_usd)} sub={t('rangeSuffix', { days })} color="text-emerald-400" />
        <StatCard icon={Hash} label={t('apiCalls')} value={data.total_calls.toLocaleString()} sub={t('monthlySuffix', { n: data.monthly_calls.toLocaleString() })} color="text-blue-400" />
        <StatCard icon={TrendingUp} label={t('inputTokens')} value={formatTokens(data.total_input)} color="text-purple-400" />
        <StatCard icon={Cpu} label={t('outputTokens')} value={formatTokens(data.total_output)} color="text-amber-400" />
      </div>

      {/* Daily charts */}
      <div data-id="audit-dashboard-overview-daily" className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div data-id="audit-dashboard-overview-daily-calls" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
          <h3 data-id="audit-dashboard-overview-daily-calls-title" className="text-xs font-medium text-[var(--vsc-text-secondary)] mb-3">{t('dailyCalls')}</h3>
          <MiniBarChart data={dailyReversed.map(d => ({ label: d.date.slice(5), value: d.calls }))} maxValue={maxCalls} />
        </div>
        <div data-id="audit-dashboard-overview-daily-cost" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
          <h3 data-id="audit-dashboard-overview-daily-cost-title" className="text-xs font-medium text-[var(--vsc-text-secondary)] mb-3">{t('dailyCost')}</h3>
          <MiniBarChart data={dailyReversed.map(d => ({ label: d.date.slice(5), value: d.cost_usd }))} maxValue={maxCost} />
        </div>
      </div>

      {/* Model breakdown */}
      <div data-id="audit-dashboard-overview-models" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
        <h3 data-id="audit-dashboard-overview-models-title" className="text-xs font-medium text-[var(--vsc-text-secondary)] mb-3">{t('modelMix')}</h3>
        {modelEntries.length === 0 ? (
          <p data-id="audit-dashboard-overview-models-empty" className="text-[var(--vsc-text-muted)] text-sm">{t('noAIRecords')}</p>
        ) : (
          <div data-id="audit-dashboard-overview-models-list" className="space-y-2">
            {modelEntries.map(([model, stat]) => {
              const pct = data.total_cost_usd > 0 ? ((stat.cost || 0) / data.total_cost_usd) * 100 : 0;
              return (
                <div key={model} data-id={`audit-dashboard-overview-models-row-${model}`} className="flex items-center gap-3 text-sm">
                  <span data-id={`audit-dashboard-overview-models-row-${model}-name`} className="flex-1 truncate text-[var(--vsc-text)] font-mono text-xs">{model}</span>
                  <span data-id={`audit-dashboard-overview-models-row-${model}-calls`} className="text-[var(--vsc-text-secondary)] text-xs w-16 text-right">{t('callsCount', { n: stat.calls })}</span>
                  <span data-id={`audit-dashboard-overview-models-row-${model}-tokens`} className="text-[var(--vsc-text-secondary)] text-xs w-20 text-right">{formatTokens(stat.input_tokens + stat.output_tokens)}</span>
                  <div data-id={`audit-dashboard-overview-models-row-${model}-bar`} className="w-24 h-1.5 bg-[var(--vsc-bg-hover)] rounded-full overflow-hidden">
                    <div data-id={`audit-dashboard-overview-models-row-${model}-bar-fill`} className="h-full bg-blue-500 rounded-full" style={{ width: `${Math.max(2, pct)}%` }} />
                  </div>
                  <span data-id={`audit-dashboard-overview-models-row-${model}-cost`} className="text-white text-xs w-16 text-right font-medium">{formatCost(stat.cost || 0)}</span>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

// ── Usage log tab ──
function UsageTab({ userId }: { userId: string }) {
  const { t } = useTranslation('audit');
  const [entries, setEntries] = useState<UsageEntry[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    apiService.getAuditUsage(userId, 200)
      .then(r => setEntries(r.data?.data || []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [userId]);

  if (loading) return <div data-id="audit-dashboard-usage-loading" className="flex items-center justify-center h-64 gap-2 text-[var(--vsc-text-secondary)]"><Spinner size="md" /> {t('loading')}</div>;

  const exportCSV = () => {
    const header = 'time,method,host,url,status,provider,model,input_tokens,output_tokens,cost_usd\n';
    const rows = entries.map(e => {
      const t = new Date(e.ts * 1000).toISOString();
      const u = e.ai_usage;
      return `${t},${e.method},${e.host},${e.url},${e.status},${u?.provider || ''},${u?.model || ''},${u?.input_tokens || 0},${u?.output_tokens || 0},${u?.cost_usd || 0}`;
    }).join('\n');
    const blob = new Blob([header + rows], { type: 'text/csv' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `audit-${userId}-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
  };

  return (
    <div data-id="audit-dashboard-usage" className="space-y-3">
      <div data-id="audit-dashboard-usage-toolbar" className="flex items-center justify-between">
        <span data-id="audit-dashboard-usage-toolbar-count" className="text-xs text-[var(--vsc-text-secondary)]">{t('recordsCount', { n: entries.length })}</span>
        <button data-id="audit-dashboard-usage-export-csv" onClick={exportCSV} className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs bg-[var(--vsc-bg-hover)] text-[var(--vsc-text-secondary)] hover:text-white transition-colors">
          <Download size={12} /> {t('exportCsv')}
        </button>
      </div>
      <div data-id="audit-dashboard-usage-table-wrap" className="overflow-auto max-h-[calc(100vh-280px)]">
        <table data-id="audit-dashboard-usage-table" className="w-full text-xs">
          <thead data-id="audit-dashboard-usage-thead" className="sticky top-0 bg-[var(--vsc-bg)]">
            <tr data-id="audit-dashboard-usage-thead-row" className="text-[var(--vsc-text-secondary)] border-b border-[var(--vsc-border)]">
              <th data-id="audit-dashboard-usage-th-time" className="text-left py-2 px-2 font-medium">{t('colTime')}</th>
              <th data-id="audit-dashboard-usage-th-provider" className="text-left py-2 px-2 font-medium">{t('colProvider')}</th>
              <th data-id="audit-dashboard-usage-th-model" className="text-left py-2 px-2 font-medium">{t('colModel')}</th>
              <th data-id="audit-dashboard-usage-th-input" className="text-right py-2 px-2 font-medium">{t('colInput')}</th>
              <th data-id="audit-dashboard-usage-th-output" className="text-right py-2 px-2 font-medium">{t('colOutput')}</th>
              <th data-id="audit-dashboard-usage-th-cost" className="text-right py-2 px-2 font-medium">{t('colCost')}</th>
              <th data-id="audit-dashboard-usage-th-status" className="text-center py-2 px-2 font-medium">{t('colStatus')}</th>
            </tr>
          </thead>
          <tbody data-id="audit-dashboard-usage-tbody">
            {entries.map((e, i) => (
              <tr key={i} data-id={`audit-dashboard-usage-row-${i}`} className="border-b border-[var(--vsc-border-subtle)] hover:bg-[var(--vsc-bg-hover)] transition-colors">
                <td data-id={`audit-dashboard-usage-row-${i}-time`} className="py-1.5 px-2 text-[var(--vsc-text-muted)] whitespace-nowrap" title={new Date(e.ts * 1000).toLocaleString()}>
                  {relativeTime(e.ts)}
                </td>
                <td data-id={`audit-dashboard-usage-row-${i}-provider`} className="py-1.5 px-2">
                  {e.ai_usage ? (
                    <span data-id={`audit-dashboard-usage-row-${i}-provider-tag`} className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-blue-500/10 text-blue-400">
                      {e.ai_usage.provider}
                    </span>
                  ) : (
                    <span data-id={`audit-dashboard-usage-row-${i}-host`} className="text-[var(--vsc-text-muted)]">{e.host}</span>
                  )}
                </td>
                <td data-id={`audit-dashboard-usage-row-${i}-model`} className="py-1.5 px-2 font-mono text-[var(--vsc-text)] truncate max-w-[200px]" title={e.ai_usage?.model || e.url}>
                  {e.ai_usage?.model || '-'}
                </td>
                <td data-id={`audit-dashboard-usage-row-${i}-input`} className="py-1.5 px-2 text-right text-[var(--vsc-text-secondary)]">
                  {e.ai_usage ? formatTokens(e.ai_usage.input_tokens) : '-'}
                </td>
                <td data-id={`audit-dashboard-usage-row-${i}-output`} className="py-1.5 px-2 text-right text-[var(--vsc-text-secondary)]">
                  {e.ai_usage ? formatTokens(e.ai_usage.output_tokens) : '-'}
                </td>
                <td data-id={`audit-dashboard-usage-row-${i}-cost`} className="py-1.5 px-2 text-right font-medium text-white">
                  {e.ai_usage ? formatCost(e.ai_usage.cost_usd) : '-'}
                </td>
                <td data-id={`audit-dashboard-usage-row-${i}-status`} className="py-1.5 px-2 text-center">
                  <span data-id={`audit-dashboard-usage-row-${i}-status-dot`} className={`inline-block w-2 h-2 rounded-full ${e.status >= 200 && e.status < 300 ? 'bg-emerald-400' : e.status >= 400 ? 'bg-red-400' : 'bg-yellow-400'}`} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {entries.length === 0 && (
          <div data-id="audit-dashboard-usage-empty" className="text-center py-12 text-[var(--vsc-text-muted)]">{t('noTrafficYet')}</div>
        )}
      </div>
    </div>
  );
}

// ── Live Stream Tab ──
function LiveTab() {
  const { t } = useTranslation('audit');
  const [events, setEvents] = useState<UsageEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const eventSourceRef = useRef<EventSource | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const token = TokenManager.getToken();
    if (!token) return;
    const base = config.apiBase || '';
    const url = `${base}/api/audit/live?token=${token}`;
    const es = new EventSource(url);
    eventSourceRef.current = es;

    es.onopen = () => setConnected(true);
    es.onmessage = (ev) => {
      try {
        const entry = JSON.parse(ev.data) as UsageEntry;
        setEvents(prev => [entry, ...prev].slice(0, 200));
      } catch {}
    };
    es.onerror = () => setConnected(false);

    return () => { es.close(); eventSourceRef.current = null; };
  }, []);

  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = 0;
    }
  }, [events]);

  return (
    <div data-id="audit-dashboard-live" className="space-y-3">
      <div data-id="audit-dashboard-live-header" className="flex items-center gap-3">
        <div data-id="audit-dashboard-live-connection" className={`flex items-center gap-1.5 text-xs ${connected ? 'text-emerald-400' : 'text-red-400'}`}>
          <div data-id="audit-dashboard-live-connection-dot" className={`w-2 h-2 rounded-full ${connected ? 'bg-emerald-400 animate-pulse' : 'bg-red-400'}`} />
          {connected ? t('connected') : t('disconnected')}
        </div>
        <span data-id="audit-dashboard-live-events-count" className="text-xs text-[var(--vsc-text-muted)]">{t('eventsCount', { n: events.length })}</span>
      </div>
      <div data-id="audit-dashboard-live-list" ref={containerRef} className="overflow-auto max-h-[calc(100vh-280px)] space-y-1">
        {events.map((e, i) => (
          <div key={i} data-id={`audit-dashboard-live-event-${i}`} className="flex items-center gap-3 px-3 py-2 rounded-md bg-[var(--vsc-bg-secondary)] border border-[var(--vsc-border-subtle)] text-xs hover:border-[var(--vsc-border)] transition-colors">
            <span data-id={`audit-dashboard-live-event-${i}-time`} className="text-[var(--vsc-text-muted)] w-16 shrink-0">{formatTime(e.ts)}</span>
            <span data-id={`audit-dashboard-live-event-${i}-status-dot`} className={`shrink-0 w-2 h-2 rounded-full ${e.status >= 200 && e.status < 300 ? 'bg-emerald-400' : 'bg-red-400'}`} />
            {e.ai_usage ? (
              <>
                <span data-id={`audit-dashboard-live-event-${i}-provider`} className="px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400 text-[10px] font-medium shrink-0">{e.ai_usage.provider}</span>
                <span data-id={`audit-dashboard-live-event-${i}-model`} className="font-mono text-[var(--vsc-text)] truncate">{e.ai_usage.model}</span>
                <span data-id={`audit-dashboard-live-event-${i}-tokens`} className="ml-auto shrink-0 text-purple-400">{formatTokens(e.ai_usage.input_tokens)}→{formatTokens(e.ai_usage.output_tokens)}</span>
                <span data-id={`audit-dashboard-live-event-${i}-cost`} className="shrink-0 text-white font-medium">{formatCost(e.ai_usage.cost_usd)}</span>
              </>
            ) : (
              <>
                <span data-id={`audit-dashboard-live-event-${i}-url`} className="text-[var(--vsc-text-secondary)] truncate flex-1">{e.method} {e.host}{e.url?.split('?')[0]}</span>
                <span data-id={`audit-dashboard-live-event-${i}-bytes`} className="ml-auto shrink-0 text-[var(--vsc-text-muted)]">{e.req_kb}KB → {e.res_kb}KB</span>
              </>
            )}
            <span data-id={`audit-dashboard-live-event-${i}-user`} className="shrink-0 text-[var(--vsc-text-muted)]">{e.user_id}</span>
          </div>
        ))}
        {events.length === 0 && (
          <div data-id="audit-dashboard-live-empty" className="text-center py-16 text-[var(--vsc-text-muted)]">
            <Activity size={32} className="mx-auto mb-3 opacity-30" />
            <p data-id="audit-dashboard-live-empty-title">{t('waitingTraffic')}</p>
            <p data-id="audit-dashboard-live-empty-hint" className="text-[10px] mt-1">{t('eventsLive')}</p>
          </div>
        )}
      </div>
    </div>
  );
}

// ── Setup Tab ──
function SetupTab({ proxyToken, onRegister }: { proxyToken: string; onRegister: () => void }) {
  const { t } = useTranslation('audit');
  const [guide, setGuide] = useState<SetupGuide | null>(null);

  useEffect(() => {
    apiService.getSetupGuide()
      .then(r => setGuide(r.data?.data))
      .catch(() => {});
  }, []);

  const proxyUrl = proxyToken
    ? `https://${proxyToken}:x@${guide?.proxy_host || 'audit.cicy-ai.com'}:${guide?.proxy_port || '8003'}`
    : 'https://YOUR_TOKEN:x@audit.cicy-ai.com:8003';

  return (
    <div data-id="audit-dashboard-setup" className="space-y-6 max-w-2xl">
      {/* Token section */}
      <div data-id="audit-dashboard-setup-token-card" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 data-id="audit-dashboard-setup-token-title" className="text-sm font-semibold text-white mb-3">{t('proxyTokenTitle')}</h3>
        {proxyToken ? (
          <div data-id="audit-dashboard-setup-token-body" className="space-y-3">
            <div data-id="audit-dashboard-setup-token-value" className="flex items-center gap-2 bg-black/30 rounded-md px-3 py-2 font-mono text-xs text-emerald-400 overflow-x-auto">
              <span data-id="audit-dashboard-setup-token-value-text" className="shrink-0">{proxyToken}</span>
              <CopyButton text={proxyToken} />
            </div>
            <div data-id="audit-dashboard-setup-token-export" className="flex items-center gap-2 bg-black/30 rounded-md px-3 py-2 font-mono text-xs text-[var(--vsc-text)] overflow-x-auto">
              <span data-id="audit-dashboard-setup-token-export-text">export https_proxy="{proxyUrl}"</span>
              <CopyButton text={`export https_proxy="${proxyUrl}"`} />
            </div>
          </div>
        ) : (
          <div data-id="audit-dashboard-setup-token-empty" className="space-y-3">
            <p data-id="audit-dashboard-setup-token-intro" className="text-sm text-[var(--vsc-text-secondary)]">{t('proxyTokenIntro')}</p>
            <button data-id="audit-dashboard-setup-token-generate" onClick={onRegister} className="px-4 py-2 bg-blue-600 text-white text-sm rounded-md hover:bg-blue-700 transition-colors">
              {t('generateToken')}
            </button>
          </div>
        )}
      </div>

      {/* Install CA */}
      <div data-id="audit-dashboard-setup-ca-card" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 data-id="audit-dashboard-setup-ca-title" className="text-sm font-semibold text-white mb-1">{t('step1Title')}</h3>
        <p data-id="audit-dashboard-setup-ca-body" className="text-xs text-[var(--vsc-text-secondary)] mb-3">{t('step1Body')}</p>
        <div data-id="audit-dashboard-setup-ca-command" className="flex items-center gap-2 bg-black/30 rounded-md px-3 py-2 font-mono text-xs text-[var(--vsc-text)]">
          <span data-id="audit-dashboard-setup-ca-command-text">curl -fsSL https://audit.cicy-ai.com/install-ca | bash</span>
          <CopyButton text="curl -fsSL https://audit.cicy-ai.com/install-ca | bash" />
        </div>
        <div data-id="audit-dashboard-setup-ca-manual" className="mt-2 flex gap-3">
          <a data-id="audit-dashboard-setup-ca-manual-link" href="/ca.pem" className="text-xs text-[var(--vsc-link)] hover:underline">{t('manualDownloadCert')}</a>
        </div>
      </div>

      {/* Platform guides */}
      <div data-id="audit-dashboard-setup-platforms-card" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 data-id="audit-dashboard-setup-platforms-title" className="text-sm font-semibold text-white mb-3">{t('step2Title')}</h3>
        <div data-id="audit-dashboard-setup-platforms-list" className="space-y-4">
          {(guide?.platforms || defaultPlatforms).map((p, i) => (
            <div key={i} data-id={`audit-dashboard-setup-platform-${i}`}>
              <h4 data-id={`audit-dashboard-setup-platform-${i}-name`} className="text-xs font-medium text-[var(--vsc-text)] mb-1.5">{p.name}</h4>
              <div data-id={`audit-dashboard-setup-platform-${i}-steps`} className="space-y-1">
                {p.steps.map((s, j) => (
                  <div key={j} data-id={`audit-dashboard-setup-platform-${i}-step-${j}`} className="flex items-start gap-2 text-xs">
                    <span data-id={`audit-dashboard-setup-platform-${i}-step-${j}-num`} className="text-[var(--vsc-text-muted)] shrink-0 mt-0.5">{j + 1}.</span>
                    <code data-id={`audit-dashboard-setup-platform-${i}-step-${j}-code`} className="bg-black/20 rounded px-2 py-1 text-[var(--vsc-text-secondary)] flex-1 break-all">
                      {s.replace(/YOUR_TOKEN/g, proxyToken || 'YOUR_TOKEN')}
                    </code>
                    {s.includes('export') && <CopyButton text={s.replace(/YOUR_TOKEN/g, proxyToken || 'YOUR_TOKEN')} />}
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* How it works */}
      <div data-id="audit-dashboard-setup-howitworks-card" className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 data-id="audit-dashboard-setup-howitworks-title" className="text-sm font-semibold text-white mb-3">{t('howItWorksTitle')}</h3>
        <div data-id="audit-dashboard-setup-howitworks-body" className="text-xs text-[var(--vsc-text-secondary)] space-y-2 leading-relaxed">
          <p data-id="audit-dashboard-setup-howitworks-intro">{t('howItWorksIntro')}</p>
          <div data-id="audit-dashboard-setup-howitworks-diagram" className="bg-black/20 rounded-md p-3 font-mono text-[10px] text-[var(--vsc-text-muted)] leading-loose">
            {t('howItWorksDiagram')}<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;↓<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;{t('howItWorksParse')}<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;↓<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;{t('howItWorksDashboard')}
          </div>
          <p data-id="audit-dashboard-setup-howitworks-providers">{t('howItWorksProviders')}</p>
        </div>
      </div>
    </div>
  );
}

const defaultPlatforms = [
  {
    name: i18n.t('platformMacOS', { ns: 'audit' }),
    steps: [
      'curl -fsSL https://audit.cicy-ai.com/install-ca | bash',
      'export https_proxy=https://YOUR_TOKEN:x@audit.cicy-ai.com:8003',
    ],
  },
  {
    name: 'Cursor / VS Code',
    steps: [
      i18n.t('instInstallCert', { ns: 'audit' }),
      i18n.t('instAddSettings', { ns: 'audit' }),
    ],
  },
  {
    name: 'Claude Code / Kiro CLI',
    steps: [
      i18n.t('instInstallCert', { ns: 'audit' }),
      'export https_proxy=https://YOUR_TOKEN:x@audit.cicy-ai.com:8003',
      i18n.t('instRunAsUsual', { ns: 'audit' }),
    ],
  },
];

// ── Findings Tab (Phase 1 walking skeleton) ──

interface AuditEvent {
  id: string;
  schema_version: string;
  rules_version: string;
  ts: string;
  ts_monotonic: number;
  prev_hash: string;
  self_hash: string;
  identity: {
    machine_id: string;
    agent_id: string;
    agent_type: string;
    user_id: string;
    session_id: string;
    source_channel: string;
  };
  subject: {
    turn_id: string;
    conversation_id: string;
    provider: string;
    model: string;
    direction: string;
    payload_size: number;
    payload_ref: string;
    payload_sha256: string;
  };
  findings: Array<{
    rule_id: string;
    rule_version: string;
    severity: string;
    category: string;
    match_count: number;
    spans: Array<{ start: number; end: number; preview: string }>;
  }>;
  decision: {
    evaluated_inline: boolean;
    evaluated_async: boolean;
    action: string;
    applied: boolean;
    fail_mode: string;
  };
  meta: {
    scanner_duration_ms: number;
    pipeline_error: string;
    policy_hash: string;
    allowlisted_by?: string;
    allowlist_match?: string;
    notify_suppressed_by?: string;
    pre_redact_ref?: string;
  };
}

interface AuditEventsResult {
  events: AuditEvent[];
  total: number;
}

interface AuditStatsResult {
  total: number;
  by_severity: Record<string, number>;
  by_rule: Record<string, number>;
  by_agent: Record<string, number>;
  by_action: Record<string, number>;
  by_direction: Record<string, number>;
}

const SEVERITY_TONE: Record<string, string> = {
  low: 'bg-zinc-500/10 text-zinc-300 border-zinc-500/30',
  medium: 'bg-amber-500/10 text-amber-300 border-amber-500/30',
  high: 'bg-orange-500/10 text-orange-300 border-orange-500/30',
  critical: 'bg-red-500/15 text-red-300 border-red-500/40',
};

function SeverityChip({ severity, count }: { severity: string; count?: number }) {
  const tone = SEVERITY_TONE[severity] || SEVERITY_TONE.low;
  return (
    <span data-id={`audit-findings-severity-${severity}`} className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded border text-[10px] font-medium ${tone}`}>
      {severity.toUpperCase()}{count !== undefined && count > 1 ? ` ×${count}` : ''}
    </span>
  );
}

function eventTopSeverity(e: AuditEvent): string {
  const order = ['critical', 'high', 'medium', 'low'];
  for (const s of order) {
    if (e.findings.some(f => f.severity === s)) return s;
  }
  return '';
}

function formatTs(ts: string): string {
  if (!ts) return '—';
  try {
    const d = new Date(ts);
    return d.toLocaleString(undefined, { hour12: false });
  } catch {
    return ts;
  }
}

function FindingsTab() {
  const { t } = useTranslation('audit');
  const [agents, setAgents] = useState<string[]>([]);
  const [filterAgent, setFilterAgent] = useState<string>('');
  const [filterSeverity, setFilterSeverity] = useState<string>('');
  const [filterDirection, setFilterDirection] = useState<'' | 'outbound' | 'inbound'>('');
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [total, setTotal] = useState<number>(0);
  const [stats, setStats] = useState<AuditStatsResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [selectedId, setSelectedId] = useState<string>('');

  const fetchAll = useCallback(async () => {
    setLoading(true);
    try {
      const [evResp, statsResp, agentsResp] = await Promise.all([
        apiService.auditEvents({
          agent_id: filterAgent || undefined,
          severity: filterSeverity || undefined,
          direction: filterDirection || undefined,
          limit: 200,
        }),
        apiService.auditStats({ agent_id: filterAgent || undefined }),
        apiService.auditAgents(),
      ]);
      const evData: AuditEventsResult = evResp.data;
      setEvents(evData.events || []);
      setTotal(evData.total || 0);
      setStats(statsResp.data || null);
      setAgents(agentsResp.data?.agents || []);
    } catch (err) {
      console.error('[audit-findings] fetch failed:', err);
    } finally {
      setLoading(false);
    }
  }, [filterAgent, filterSeverity, filterDirection]);

  useEffect(() => { fetchAll(); }, [fetchAll]);
  useEffect(() => {
    if (!autoRefresh) return;
    const h = setInterval(fetchAll, 5000);
    return () => clearInterval(h);
  }, [autoRefresh, fetchAll]);

  const selected = events.find(e => e.id === selectedId);

  return (
    <div data-id="audit-findings-root" className="flex flex-col gap-3 h-full">
      {/* Filter bar */}
      <div data-id="audit-findings-filters" className="flex flex-wrap items-center gap-2 p-2 rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)]">
        <div className="flex items-center gap-1.5 text-[var(--vsc-text-secondary)]">
          <Filter size={14} />
          <span className="text-xs">{t('findingsFilters')}</span>
        </div>
        <select data-id="audit-findings-filter-agent" value={filterAgent} onChange={e => setFilterAgent(e.target.value)}
          className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)]">
          <option value="">{t('findingsAllAgents')}</option>
          {agents.map(a => <option key={a} value={a}>{a}</option>)}
        </select>
        <select data-id="audit-findings-filter-severity" value={filterSeverity} onChange={e => setFilterSeverity(e.target.value)}
          className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)]">
          <option value="">{t('findingsAnySeverity')}</option>
          <option value="critical">CRITICAL</option>
          <option value="high">HIGH</option>
          <option value="medium">MEDIUM</option>
          <option value="low">LOW</option>
        </select>
        <select data-id="audit-findings-filter-direction" value={filterDirection} onChange={e => setFilterDirection(e.target.value as any)}
          className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)]">
          <option value="">{t('findingsAnyDirection')}</option>
          <option value="outbound">outbound</option>
          <option value="inbound">inbound</option>
        </select>
        <div className="flex-1" />
        <label data-id="audit-findings-autorefresh" className="flex items-center gap-1.5 text-xs text-[var(--vsc-text-secondary)] cursor-pointer">
          <input type="checkbox" checked={autoRefresh} onChange={e => setAutoRefresh(e.target.checked)} className="cursor-pointer" />
          {t('findingsAutoRefresh')}
        </label>
        <button data-id="audit-findings-refresh-now" onClick={fetchAll}
          className="flex items-center gap-1 px-2 py-1 text-xs rounded bg-[var(--vsc-bg-hover)] hover:bg-[var(--vsc-bg-active)] text-[var(--vsc-text)] transition-colors">
          <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
          {t('findingsRefresh')}
        </button>
      </div>

      {/* Stats strip */}
      <div data-id="audit-findings-stats" className="grid grid-cols-2 md:grid-cols-5 gap-2">
        <StatPill label={t('findingsStatTotal')} value={String(stats?.total ?? 0)} tone="text-zinc-200" />
        <StatPill label="CRITICAL" value={String(stats?.by_severity?.critical ?? 0)} tone="text-red-300" />
        <StatPill label="HIGH" value={String(stats?.by_severity?.high ?? 0)} tone="text-orange-300" />
        <StatPill label="MEDIUM" value={String(stats?.by_severity?.medium ?? 0)} tone="text-amber-300" />
        <StatPill label="LOW" value={String(stats?.by_severity?.low ?? 0)} tone="text-zinc-300" />
      </div>

      {/* Event list + detail panel */}
      <div data-id="audit-findings-body" className="flex-1 grid grid-cols-1 lg:grid-cols-[1fr_420px] gap-3 min-h-0">
        <div data-id="audit-findings-list" className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] overflow-hidden flex flex-col min-h-0">
          <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-center justify-between text-xs text-[var(--vsc-text-secondary)]">
            <span>{t('findingsListHeader', { showing: events.length, total })}</span>
          </div>
          <div className="flex-1 overflow-auto">
            {events.length === 0 ? (
              <div className="p-8 text-center text-xs text-[var(--vsc-text-muted)]">
                {loading ? t('loading') : t('findingsEmpty')}
              </div>
            ) : (
              <table className="w-full text-xs">
                <thead className="sticky top-0 bg-[var(--vsc-bg-titlebar)] text-[var(--vsc-text-secondary)]">
                  <tr>
                    <th className="text-left px-3 py-1.5 font-normal">{t('findingsColTime')}</th>
                    <th className="text-left px-2 py-1.5 font-normal">{t('findingsColAgent')}</th>
                    <th className="text-left px-2 py-1.5 font-normal">{t('findingsColDir')}</th>
                    <th className="text-left px-2 py-1.5 font-normal">{t('findingsColProvider')}</th>
                    <th className="text-left px-2 py-1.5 font-normal">{t('findingsColSev')}</th>
                    <th className="text-left px-2 py-1.5 font-normal">{t('findingsColAction')}</th>
                    <th className="w-4" />
                  </tr>
                </thead>
                <tbody>
                  {events.map(e => {
                    const sev = eventTopSeverity(e);
                    const isSel = e.id === selectedId;
                    return (
                      <tr key={e.id}
                        data-id={`audit-findings-row-${e.id}`}
                        onClick={() => setSelectedId(e.id)}
                        className={`border-t border-[var(--vsc-border)] cursor-pointer transition-colors ${isSel ? 'bg-[var(--vsc-bg-active)]' : 'hover:bg-[var(--vsc-bg-hover)]'}`}>
                        <td className="px-3 py-1.5 whitespace-nowrap text-[var(--vsc-text-muted)] font-mono">{formatTs(e.ts)}</td>
                        <td className="px-2 py-1.5 whitespace-nowrap">
                          <span className="text-zinc-200">{e.identity.agent_id || '—'}</span>
                          {e.identity.agent_type && <span className="text-[10px] text-[var(--vsc-text-muted)] ml-1">{e.identity.agent_type}</span>}
                        </td>
                        <td className="px-2 py-1.5 whitespace-nowrap">
                          <span className={`text-[10px] px-1.5 py-0.5 rounded ${e.subject.direction === 'outbound' ? 'bg-blue-500/10 text-blue-300' : 'bg-emerald-500/10 text-emerald-300'}`}>
                            {e.subject.direction || '—'}
                          </span>
                        </td>
                        <td className="px-2 py-1.5 whitespace-nowrap text-[var(--vsc-text-secondary)]">
                          {e.subject.provider || '—'}
                          {e.subject.model && <span className="text-[10px] text-[var(--vsc-text-muted)] ml-1">{e.subject.model}</span>}
                        </td>
                        <td className="px-2 py-1.5 whitespace-nowrap">
                          {sev ? <SeverityChip severity={sev} count={e.findings.length} /> : <span className="text-[var(--vsc-text-muted)]">—</span>}
                        </td>
                        <td className="px-2 py-1.5 whitespace-nowrap text-[var(--vsc-text-secondary)]">{e.decision.action}</td>
                        <td className="px-1 py-1.5"><ChevronRight size={12} className="text-[var(--vsc-text-muted)]" /></td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>
        </div>

        <FindingsDetailPanel event={selected || null} onMarkedFP={fetchAll} />
      </div>
    </div>
  );
}

function StatPill({ label, value, tone = 'text-zinc-200' }: { label: string; value: string; tone?: string }) {
  return (
    <div data-id={`audit-findings-stat-${label}`} className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] px-3 py-2">
      <div className="text-[10px] uppercase tracking-wide text-[var(--vsc-text-muted)]">{label}</div>
      <div className={`text-lg font-semibold tabular-nums ${tone}`}>{value}</div>
    </div>
  );
}

function FindingsDetailPanel({ event, onMarkedFP }: { event: AuditEvent | null; onMarkedFP?: () => void }) {
  const { t } = useTranslation('audit');
  const [fpBusy, setFpBusy] = useState(false);
  const [fpDone, setFpDone] = useState(false);

  useEffect(() => { setFpDone(false); }, [event?.id]);

  const markFP = useCallback(async () => {
    if (!event) return;
    const reason = window.prompt(t('findingsFpPromptReason'), '') ?? '';
    setFpBusy(true);
    try {
      await apiService.auditMarkFalsePositive(event.subject.payload_sha256, reason);
      setFpDone(true);
      onMarkedFP?.();
    } catch (err) {
      console.error('[audit-findings] mark FP failed:', err);
      window.alert(t('findingsFpError'));
    } finally {
      setFpBusy(false);
    }
  }, [event, onMarkedFP, t]);

  if (!event) {
    return (
      <div data-id="audit-findings-detail-empty" className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] flex items-center justify-center text-xs text-[var(--vsc-text-muted)] p-8">
        {t('findingsDetailHint')}
      </div>
    );
  }
  const canMarkFP = event.findings.length > 0 && !event.meta.allowlisted_by;
  return (
    <div data-id="audit-findings-detail" className="rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] flex flex-col min-h-0">
      <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-center gap-2 text-xs">
        <span className="text-[var(--vsc-text-secondary)]">{t('findingsDetailHeader')}</span>
        <code className="font-mono text-[10px] text-[var(--vsc-text-muted)]">{event.id}</code>
        <CopyButton text={event.id} />
        <div className="flex-1" />
        {canMarkFP && (
          <button
            data-id="audit-findings-mark-fp"
            disabled={fpBusy || fpDone}
            onClick={markFP}
            className={`px-2 py-1 text-[10px] rounded border transition-colors ${
              fpDone
                ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-300 cursor-default'
                : fpBusy
                  ? 'border-zinc-700 bg-zinc-800/50 text-zinc-500 cursor-wait'
                  : 'border-amber-500/40 bg-amber-500/10 text-amber-300 hover:bg-amber-500/20'
            }`}
          >
            {fpDone ? t('findingsFpDone') : fpBusy ? t('findingsFpBusy') : t('findingsFpMark')}
          </button>
        )}
      </div>
      <div className="flex-1 overflow-auto p-3 space-y-3 text-xs">
        <DetailSection title={t('findingsSecIdentity')} rows={[
          ['agent_id', event.identity.agent_id],
          ['agent_type', event.identity.agent_type],
          ['user_id', event.identity.user_id],
          ['session_id', event.identity.session_id],
          ['source_channel', event.identity.source_channel],
          ['machine_id', event.identity.machine_id],
        ]} />
        <DetailSection title={t('findingsSecSubject')} rows={[
          ['provider', event.subject.provider],
          ['model', event.subject.model],
          ['direction', event.subject.direction],
          ['turn_id', event.subject.turn_id],
          ['conversation_id', event.subject.conversation_id],
          ['payload_size', String(event.subject.payload_size)],
          ['payload_ref', event.subject.payload_ref],
          ['payload_sha256', event.subject.payload_sha256],
        ]} />
        <DetailSection title={t('findingsSecDecision')} rows={[
          ['action', event.decision.action],
          ['applied', String(event.decision.applied)],
          ['fail_mode', event.decision.fail_mode],
          ['evaluated_inline', String(event.decision.evaluated_inline)],
          ['evaluated_async', String(event.decision.evaluated_async)],
        ]} />
        <DetailSection title={t('findingsSecChain')} rows={[
          ['ts', event.ts],
          ['ts_monotonic', String(event.ts_monotonic)],
          ['prev_hash', event.prev_hash],
          ['self_hash', event.self_hash],
          ['rules_version', event.rules_version],
          ['policy_hash', event.meta.policy_hash],
        ]} />
        <div data-id="audit-findings-detail-findings">
          <div className="text-[10px] uppercase tracking-wide text-[var(--vsc-text-muted)] mb-1">{t('findingsSecFindings')}</div>
          {event.findings.length === 0 ? (
            <div className="text-[var(--vsc-text-muted)]">{t('findingsNoFindings')}</div>
          ) : (
            <div className="space-y-1.5">
              {event.findings.map((f, i) => (
                <div key={i} className="rounded border border-[var(--vsc-border)] p-2 bg-[var(--vsc-bg)]">
                  <div className="flex items-center gap-2">
                    <SeverityChip severity={f.severity} />
                    <code className="font-mono text-[var(--vsc-text)]">{f.rule_id}</code>
                    <span className="text-[var(--vsc-text-muted)]">×{f.match_count}</span>
                  </div>
                  {f.spans.length > 0 && (
                    <div className="mt-1 space-y-0.5 text-[var(--vsc-text-secondary)]">
                      {f.spans.map((s, j) => (
                        <div key={j} className="font-mono text-[10px]">
                          [{s.start}:{s.end}] {s.preview}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function DetailSection({ title, rows }: { title: string; rows: Array<[string, string]> }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wide text-[var(--vsc-text-muted)] mb-1">{title}</div>
      <div className="space-y-0.5">
        {rows.map(([k, v]) => (
          <div key={k} className="flex gap-2">
            <span className="text-[var(--vsc-text-muted)] w-32 shrink-0 font-mono text-[10px]">{k}</span>
            <span className="text-[var(--vsc-text)] break-all font-mono text-[10px]">{v || '—'}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ── Policy Tab (P2-T7+ Cut A) ──

type PolicySubTab = 'global' | 'agent' | 'effective';

function PolicyTab() {
  const { t } = useTranslation('audit');
  const [sub, setSub] = useState<PolicySubTab>('global');
  const [agents, setAgents] = useState<string[]>([]);
  const [selectedAgent, setSelectedAgent] = useState<string>('');

  useEffect(() => {
    apiService.auditAgents()
      .then(r => {
        const list = r.data?.agents || [];
        setAgents(list);
        if (list.length && !selectedAgent) setSelectedAgent(list[0]);
      })
      .catch(err => console.error('[policy] list agents:', err));
  }, []);

  const subs: { id: PolicySubTab; label: string }[] = [
    { id: 'global', label: t('policySubGlobal') },
    { id: 'agent', label: t('policySubAgent') },
    { id: 'effective', label: t('policySubEffective') },
  ];

  return (
    <div data-id="audit-policy-root" className="flex flex-col gap-3 h-full">
      <div data-id="audit-policy-subtabs" className="flex items-center gap-1 p-1 rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] w-fit">
        {subs.map(s => (
          <button key={s.id}
            data-id={`audit-policy-subtab-${s.id}`}
            onClick={() => setSub(s.id)}
            className={`px-3 py-1.5 text-xs rounded transition-colors ${sub === s.id ? 'bg-[var(--vsc-bg-active)] text-white' : 'text-[var(--vsc-text-secondary)] hover:text-[var(--vsc-text)] hover:bg-[var(--vsc-bg-hover)]'}`}>
            {s.label}
          </button>
        ))}
      </div>

      {sub === 'global' && <PolicyEditor mode="global" />}
      {sub === 'agent' && (
        <PolicyEditor mode="agent" agents={agents} selectedAgent={selectedAgent} onSelectAgent={setSelectedAgent} />
      )}
      {sub === 'effective' && (
        <PolicyEditor mode="effective" agents={agents} selectedAgent={selectedAgent} onSelectAgent={setSelectedAgent} />
      )}
    </div>
  );
}

interface PolicyEditorProps {
  mode: 'global' | 'agent' | 'effective';
  agents?: string[];
  selectedAgent?: string;
  onSelectAgent?: (a: string) => void;
}

function PolicyEditor({ mode, agents = [], selectedAgent = '', onSelectAgent }: PolicyEditorProps) {
  const { t } = useTranslation('audit');
  const [raw, setRaw] = useState('');
  const [original, setOriginal] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [info, setInfo] = useState('');
  const [policyHash, setPolicyHash] = useState('');

  const readOnly = mode === 'effective';

  const fetchIt = useCallback(async () => {
    if ((mode === 'agent' || mode === 'effective') && !selectedAgent) {
      setRaw(''); setOriginal(''); return;
    }
    setLoading(true); setError(''); setInfo('');
    try {
      let r;
      if (mode === 'global') r = await apiService.auditPolicyGetGlobal();
      else if (mode === 'agent') r = await apiService.auditPolicyGetAgent(selectedAgent);
      else r = await apiService.auditPolicyGetEffective(selectedAgent);
      const pretty = typeof r.data === 'string' ? r.data : JSON.stringify(r.data, null, 2);
      setRaw(pretty);
      setOriginal(pretty);
    } catch (err: any) {
      setError(err?.response?.data?.detail || String(err));
    } finally {
      setLoading(false);
    }
  }, [mode, selectedAgent]);

  useEffect(() => { fetchIt(); }, [fetchIt]);

  const dirty = raw !== original;

  const onSave = useCallback(async () => {
    setError(''); setInfo('');
    let body = raw;
    try { JSON.parse(body); } catch (e: any) {
      setError(t('policyErrorJSON') + ': ' + e.message);
      return;
    }
    setLoading(true);
    try {
      let r;
      if (mode === 'global') r = await apiService.auditPolicyPostGlobal(body);
      else r = await apiService.auditPolicyPostAgent(selectedAgent, body);
      const hash = r.data?.policy_hash || '';
      if (hash) setPolicyHash(hash);
      setOriginal(raw);
      setInfo(t('policySaved') + (hash ? ` (${hash.slice(0, 20)}...)` : ''));
    } catch (err: any) {
      setError(err?.response?.data?.detail || String(err));
    } finally {
      setLoading(false);
    }
  }, [raw, mode, selectedAgent, t]);

  const onDelete = useCallback(async () => {
    if (mode !== 'agent' || !selectedAgent) return;
    if (!window.confirm(t('policyDeleteConfirm', { agent: selectedAgent }))) return;
    setLoading(true); setError(''); setInfo('');
    try {
      await apiService.auditPolicyPostAgent(selectedAgent, '{}');
      setRaw('{}'); setOriginal('{}');
      setInfo(t('policyDeleted'));
    } catch (err: any) {
      setError(err?.response?.data?.detail || String(err));
    } finally {
      setLoading(false);
    }
  }, [mode, selectedAgent, t]);

  return (
    <div data-id="audit-policy-editor" className="flex flex-col gap-2 flex-1 min-h-0">
      {(mode === 'agent' || mode === 'effective') && (
        <div data-id="audit-policy-agent-bar" className="flex items-center gap-2 p-2 rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)]">
          <span className="text-xs text-[var(--vsc-text-secondary)]">{t('policyAgentSelect')}</span>
          <select data-id="audit-policy-agent-select" value={selectedAgent} onChange={e => onSelectAgent?.(e.target.value)}
            className="text-xs px-2 py-1 rounded bg-[var(--vsc-bg)] border border-[var(--vsc-border)] text-[var(--vsc-text)]">
            <option value="">{t('policyAgentNone')}</option>
            {agents.map(a => <option key={a} value={a}>{a}</option>)}
          </select>
          <button data-id="audit-policy-reload" onClick={fetchIt} disabled={loading}
            className="flex items-center gap-1 px-2 py-1 text-xs rounded bg-[var(--vsc-bg-hover)] hover:bg-[var(--vsc-bg-active)] text-[var(--vsc-text)] transition-colors">
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
            {t('policyReload')}
          </button>
          {readOnly && <span className="ml-auto text-[10px] text-[var(--vsc-text-muted)]">{t('policyReadOnly')}</span>}
        </div>
      )}

      <textarea
        data-id="audit-policy-textarea"
        readOnly={readOnly}
        value={raw}
        onChange={e => { if (!readOnly) setRaw(e.target.value); }}
        spellCheck={false}
        className="flex-1 min-h-[240px] font-mono text-xs p-3 rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] text-[var(--vsc-text)] resize-none focus:outline-none focus:border-blue-500/40"
      />

      {error && (
        <div data-id="audit-policy-error" className="flex items-start gap-2 p-2 rounded border border-red-500/30 bg-red-500/10 text-red-300 text-xs">
          <AlertCircle size={14} className="shrink-0 mt-0.5" />
          <span className="font-mono">{error}</span>
        </div>
      )}
      {info && !error && (
        <div data-id="audit-policy-info" className="flex items-center gap-2 p-2 rounded border border-emerald-500/30 bg-emerald-500/10 text-emerald-300 text-xs">
          <Check size={14} />
          <span className="font-mono">{info}</span>
        </div>
      )}

      {!readOnly && (
        <div data-id="audit-policy-actions" className="flex items-center gap-2">
          {mode === 'agent' && (
            <button data-id="audit-policy-delete" onClick={onDelete} disabled={loading}
              className="flex items-center gap-1 px-3 py-1.5 text-xs rounded border border-red-500/30 bg-red-500/10 text-red-300 hover:bg-red-500/20 transition-colors">
              <Trash2 size={12} />
              {t('policyDelete')}
            </button>
          )}
          <div className="flex-1" />
          <button data-id="audit-policy-reset" disabled={!dirty || loading} onClick={() => setRaw(original)}
            className={`flex items-center gap-1 px-3 py-1.5 text-xs rounded border transition-colors ${dirty ? 'border-zinc-500/30 bg-zinc-500/10 text-zinc-300 hover:bg-zinc-500/20' : 'border-zinc-700 bg-zinc-800/30 text-zinc-600 cursor-default'}`}>
            <RotateCcw size={12} />
            {t('policyReset')}
          </button>
          <button data-id="audit-policy-save" disabled={!dirty || loading} onClick={onSave}
            className={`flex items-center gap-1 px-3 py-1.5 text-xs rounded border transition-colors ${dirty ? 'border-blue-500/40 bg-blue-500/10 text-blue-300 hover:bg-blue-500/20' : 'border-zinc-700 bg-zinc-800/30 text-zinc-600 cursor-default'}`}>
            <Save size={12} />
            {loading ? t('policySaving') : t('policySave')}
          </button>
        </div>
      )}

      {policyHash && (
        <div className="text-[10px] text-[var(--vsc-text-muted)] font-mono">
          policy_hash: {policyHash}
        </div>
      )}
    </div>
  );
}

// ── Main Dashboard ──
export default function AuditDashboard({ onBack }: { onBack?: () => void }) {
  const { t } = useTranslation('audit');
  const [tab, setTab] = useState<Tab>('findings');
  const [userId, setUserId] = useState('');
  const [proxyToken, setProxyToken] = useState('');
  const [days, setDays] = useState(7);

  useEffect(() => {
    const token = TokenManager.getToken();
    if (token && token.includes('.')) {
      try {
        const payload = JSON.parse(atob(token.split('.')[1]));
        setUserId(payload.sub || 'admin');
      } catch { setUserId('admin'); }
    } else {
      setUserId('admin');
    }
    const saved = localStorage.getItem('audit_proxy_token');
    if (saved) setProxyToken(saved);
  }, []);

  const handleRegister = useCallback(async () => {
    try {
      const r = await apiService.registerAuditToken(userId);
      const token = r.data?.data?.token;
      if (token) {
        setProxyToken(token);
        localStorage.setItem('audit_proxy_token', token);
      }
    } catch (err) {
      console.error('Failed to register audit token:', err);
    }
  }, [userId]);

  const tabs: { id: Tab; icon: typeof BarChart3; label: string }[] = [
    { id: 'findings', icon: ShieldCheck, label: t('tabFindings') },
    { id: 'policy', icon: SlidersHorizontal, label: t('tabPolicy') },
    { id: 'overview', icon: BarChart3, label: t('tabOverview') },
    { id: 'usage', icon: Clock, label: t('tabUsage') },
    { id: 'live', icon: Activity, label: t('tabLive') },
    { id: 'setup', icon: Settings, label: t('tabSetup') },
  ];

  return (
    <div data-id="audit-dashboard-root" className="h-screen flex flex-col bg-[var(--vsc-bg)] text-[var(--vsc-text)]">
      {/* Header */}
      <header data-id="audit-dashboard-header" className="flex items-center gap-3 px-4 py-3 border-b border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] shrink-0">
        {onBack && (
          <button data-id="audit-dashboard-header-back" onClick={onBack} className="p-1 rounded hover:bg-[var(--vsc-bg-hover)] text-[var(--vsc-text-secondary)] hover:text-white transition-colors">
            <ArrowLeft size={16} />
          </button>
        )}
        <div data-id="audit-dashboard-header-brand" className="flex items-center gap-2">
          <Zap size={18} className="text-blue-400" />
          <span data-id="audit-dashboard-header-brand-name" className="font-semibold text-white text-sm">CiCy Audit</span>
          <span data-id="audit-dashboard-header-brand-badge" className="text-[10px] px-1.5 py-0.5 rounded-full bg-blue-500/10 text-blue-400 font-medium">{t('betaBadge')}</span>
        </div>
        <div data-id="audit-dashboard-header-spacer" className="flex-1" />
        <span data-id="audit-dashboard-header-user" className="text-xs text-[var(--vsc-text-muted)]">
          {userId && t('userBadge', { userId })}
        </span>
      </header>

      {/* Tab bar */}
      <nav data-id="audit-dashboard-tabs" className="flex items-center gap-1 px-4 py-1.5 border-b border-[var(--vsc-border)] bg-[var(--vsc-bg)] shrink-0">
        {tabs.map(t => (
          <button key={t.id} data-id={`audit-dashboard-tab-${t.id}`} onClick={() => setTab(t.id)}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs transition-colors ${tab === t.id ? 'bg-[var(--vsc-bg-active)] text-white' : 'text-[var(--vsc-text-secondary)] hover:text-[var(--vsc-text)] hover:bg-[var(--vsc-bg-hover)]'}`}>
            <t.icon size={14} />
            {t.label}
          </button>
        ))}
      </nav>

      {/* Content */}
      <main data-id="audit-dashboard-content" className="flex-1 overflow-auto p-4">
        {tab === 'findings' && <FindingsTab />}
        {tab === 'policy' && <PolicyTab />}
        {tab === 'overview' && <OverviewTab userId={userId} days={days} setDays={setDays} />}
        {tab === 'usage' && <UsageTab userId={userId} />}
        {tab === 'live' && <LiveTab />}
        {tab === 'setup' && <SetupTab proxyToken={proxyToken} onRegister={handleRegister} />}
      </main>
    </div>
  );
}
