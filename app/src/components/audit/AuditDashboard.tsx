import { useState, useEffect, useRef, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { BarChart3, Activity, Zap, Settings, ArrowLeft, Download, Copy, Check, DollarSign, Hash, Clock, TrendingUp, Cpu, Sparkles, MessageSquare } from 'lucide-react';
import { Spinner } from '../ui/Spinner';
import DecisionsTab from './DecisionsTab';
import AssistantTab from './AssistantTab';
import apiService from '../../services/api';
import config from '../../config';
import { TokenManager } from '../../services/tokenManager';

type Tab = 'assistant' | 'overview' | 'usage' | 'live' | 'agent' | 'setup';

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

// ── Main Dashboard ──
export default function AuditDashboard({ onBack }: { onBack?: () => void }) {
  const { t } = useTranslation('audit');
  const [tab, setTab] = useState<Tab>('assistant');
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
    { id: 'assistant', icon: MessageSquare, label: t('tabAssistant', 'Assistant') },
    { id: 'overview', icon: BarChart3, label: t('tabOverview') },
    { id: 'usage', icon: Clock, label: t('tabUsage') },
    { id: 'live', icon: Activity, label: t('tabLive') },
    { id: 'agent', icon: Sparkles, label: t('tabAgent') },
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
      <main data-id="audit-dashboard-content" className={tab === 'assistant' ? 'flex-1 min-h-0 overflow-hidden' : 'flex-1 overflow-auto p-4'}>
        {tab === 'assistant' && <AssistantTab />}
        {tab === 'overview' && <OverviewTab userId={userId} days={days} setDays={setDays} />}
        {tab === 'usage' && <UsageTab userId={userId} />}
        {tab === 'live' && <LiveTab />}
        {tab === 'agent' && <DecisionsTab />}
        {tab === 'setup' && <SetupTab proxyToken={proxyToken} onRegister={handleRegister} />}
      </main>
    </div>
  );
}
