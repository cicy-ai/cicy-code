import { useState, useEffect, useRef, useCallback } from 'react';
import { BarChart3, Activity, Zap, Settings, ArrowLeft, RefreshCw, Download, Copy, Check, DollarSign, Hash, Clock, TrendingUp, AlertTriangle, Cpu } from 'lucide-react';
import apiService from '../../services/api';
import config from '../../config';
import { TokenManager } from '../../services/tokenManager';

type Tab = 'overview' | 'usage' | 'live' | 'setup';

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
  return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
};
const relativeTime = (ts: number) => {
  const diff = Date.now() / 1000 - ts;
  if (diff < 60) return `${Math.floor(diff)}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
};

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  return (
    <button onClick={copy} className="ml-2 p-1 rounded hover:bg-white/10 text-[var(--vsc-text-secondary)] hover:text-white transition-colors">
      {copied ? <Check size={14} className="text-emerald-400" /> : <Copy size={14} />}
    </button>
  );
}

function StatCard({ icon: Icon, label, value, sub, color = 'text-blue-400' }: { icon: any; label: string; value: string; sub?: string; color?: string }) {
  return (
    <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
      <div className="flex items-center gap-2 mb-2">
        <Icon size={16} className={color} />
        <span className="text-xs text-[var(--vsc-text-secondary)]">{label}</span>
      </div>
      <div className="text-xl font-semibold text-white">{value}</div>
      {sub && <div className="text-xs text-[var(--vsc-text-muted)] mt-1">{sub}</div>}
    </div>
  );
}

function MiniBarChart({ data, maxValue }: { data: { label: string; value: number }[]; maxValue: number }) {
  return (
    <div className="flex items-end gap-1 h-24">
      {data.map((d, i) => (
        <div key={i} className="flex-1 flex flex-col items-center gap-1">
          <div className="w-full bg-blue-500/20 rounded-t relative overflow-hidden" style={{ height: maxValue > 0 ? `${Math.max(2, (d.value / maxValue) * 80)}px` : '2px' }}>
            <div className="absolute bottom-0 w-full bg-blue-500 rounded-t" style={{ height: '100%' }} />
          </div>
          <span className="text-[9px] text-[var(--vsc-text-muted)] truncate w-full text-center">{d.label}</span>
        </div>
      ))}
    </div>
  );
}

// ── Overview Tab ──
function OverviewTab({ userId, days, setDays }: { userId: string; days: number; setDays: (d: number) => void }) {
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    apiService.getAuditDashboard(userId, days)
      .then(r => setData(r.data?.data))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [userId, days]);

  if (loading) return <div className="flex items-center justify-center h-64 text-[var(--vsc-text-secondary)]"><RefreshCw size={20} className="animate-spin mr-2" /> Loading...</div>;
  if (!data) return <div className="text-center text-[var(--vsc-text-secondary)] py-12">No data available</div>;

  const dailyReversed = [...data.daily].reverse();
  const maxCalls = Math.max(...dailyReversed.map(d => d.calls), 1);
  const maxCost = Math.max(...dailyReversed.map(d => d.cost_usd), 0.01);

  const modelEntries = Object.entries(data.model_breakdown)
    .sort((a, b) => (b[1].cost || 0) - (a[1].cost || 0));

  return (
    <div className="space-y-6">
      {/* Period selector */}
      <div className="flex items-center gap-2">
        {[7, 14, 30].map(d => (
          <button key={d} onClick={() => setDays(d)}
            className={`px-3 py-1 text-xs rounded-md transition-colors ${days === d ? 'bg-blue-600 text-white' : 'bg-[var(--vsc-bg-hover)] text-[var(--vsc-text-secondary)] hover:text-white'}`}>
            {d}d
          </button>
        ))}
      </div>

      {/* Summary cards */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard icon={DollarSign} label="Total Cost" value={formatCost(data.total_cost_usd)} sub={`${days} day period`} color="text-emerald-400" />
        <StatCard icon={Hash} label="API Calls" value={data.total_calls.toLocaleString()} sub={`${data.monthly_calls.toLocaleString()} this month`} color="text-blue-400" />
        <StatCard icon={TrendingUp} label="Input Tokens" value={formatTokens(data.total_input)} color="text-purple-400" />
        <StatCard icon={Cpu} label="Output Tokens" value={formatTokens(data.total_output)} color="text-amber-400" />
      </div>

      {/* Daily charts */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
          <h3 className="text-xs font-medium text-[var(--vsc-text-secondary)] mb-3">Daily API Calls</h3>
          <MiniBarChart data={dailyReversed.map(d => ({ label: d.date.slice(5), value: d.calls }))} maxValue={maxCalls} />
        </div>
        <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
          <h3 className="text-xs font-medium text-[var(--vsc-text-secondary)] mb-3">Daily Cost (USD)</h3>
          <MiniBarChart data={dailyReversed.map(d => ({ label: d.date.slice(5), value: d.cost_usd }))} maxValue={maxCost} />
        </div>
      </div>

      {/* Model breakdown */}
      <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-4 border border-[var(--vsc-border)]">
        <h3 className="text-xs font-medium text-[var(--vsc-text-secondary)] mb-3">Model Breakdown</h3>
        {modelEntries.length === 0 ? (
          <p className="text-[var(--vsc-text-muted)] text-sm">No AI usage recorded yet</p>
        ) : (
          <div className="space-y-2">
            {modelEntries.map(([model, stat]) => {
              const pct = data.total_cost_usd > 0 ? ((stat.cost || 0) / data.total_cost_usd) * 100 : 0;
              return (
                <div key={model} className="flex items-center gap-3 text-sm">
                  <span className="flex-1 truncate text-[var(--vsc-text)] font-mono text-xs">{model}</span>
                  <span className="text-[var(--vsc-text-secondary)] text-xs w-16 text-right">{stat.calls} calls</span>
                  <span className="text-[var(--vsc-text-secondary)] text-xs w-20 text-right">{formatTokens(stat.input_tokens + stat.output_tokens)}</span>
                  <div className="w-24 h-1.5 bg-[var(--vsc-bg-hover)] rounded-full overflow-hidden">
                    <div className="h-full bg-blue-500 rounded-full" style={{ width: `${Math.max(2, pct)}%` }} />
                  </div>
                  <span className="text-white text-xs w-16 text-right font-medium">{formatCost(stat.cost || 0)}</span>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

// ── Usage Log Tab ──
function UsageTab({ userId }: { userId: string }) {
  const [entries, setEntries] = useState<UsageEntry[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    apiService.getAuditUsage(userId, 200)
      .then(r => setEntries(r.data?.data || []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [userId]);

  if (loading) return <div className="flex items-center justify-center h-64 text-[var(--vsc-text-secondary)]"><RefreshCw size={20} className="animate-spin mr-2" /> Loading...</div>;

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
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <span className="text-xs text-[var(--vsc-text-secondary)]">{entries.length} entries</span>
        <button onClick={exportCSV} className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs bg-[var(--vsc-bg-hover)] text-[var(--vsc-text-secondary)] hover:text-white transition-colors">
          <Download size={12} /> Export CSV
        </button>
      </div>
      <div className="overflow-auto max-h-[calc(100vh-280px)]">
        <table className="w-full text-xs">
          <thead className="sticky top-0 bg-[var(--vsc-bg)]">
            <tr className="text-[var(--vsc-text-secondary)] border-b border-[var(--vsc-border)]">
              <th className="text-left py-2 px-2 font-medium">Time</th>
              <th className="text-left py-2 px-2 font-medium">Provider</th>
              <th className="text-left py-2 px-2 font-medium">Model</th>
              <th className="text-right py-2 px-2 font-medium">Input</th>
              <th className="text-right py-2 px-2 font-medium">Output</th>
              <th className="text-right py-2 px-2 font-medium">Cost</th>
              <th className="text-center py-2 px-2 font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((e, i) => (
              <tr key={i} className="border-b border-[var(--vsc-border-subtle)] hover:bg-[var(--vsc-bg-hover)] transition-colors">
                <td className="py-1.5 px-2 text-[var(--vsc-text-muted)] whitespace-nowrap" title={new Date(e.ts * 1000).toLocaleString()}>
                  {relativeTime(e.ts)}
                </td>
                <td className="py-1.5 px-2">
                  {e.ai_usage ? (
                    <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-blue-500/10 text-blue-400">
                      {e.ai_usage.provider}
                    </span>
                  ) : (
                    <span className="text-[var(--vsc-text-muted)]">{e.host}</span>
                  )}
                </td>
                <td className="py-1.5 px-2 font-mono text-[var(--vsc-text)] truncate max-w-[200px]" title={e.ai_usage?.model || e.url}>
                  {e.ai_usage?.model || '-'}
                </td>
                <td className="py-1.5 px-2 text-right text-[var(--vsc-text-secondary)]">
                  {e.ai_usage ? formatTokens(e.ai_usage.input_tokens) : '-'}
                </td>
                <td className="py-1.5 px-2 text-right text-[var(--vsc-text-secondary)]">
                  {e.ai_usage ? formatTokens(e.ai_usage.output_tokens) : '-'}
                </td>
                <td className="py-1.5 px-2 text-right font-medium text-white">
                  {e.ai_usage ? formatCost(e.ai_usage.cost_usd) : '-'}
                </td>
                <td className="py-1.5 px-2 text-center">
                  <span className={`inline-block w-2 h-2 rounded-full ${e.status >= 200 && e.status < 300 ? 'bg-emerald-400' : e.status >= 400 ? 'bg-red-400' : 'bg-yellow-400'}`} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {entries.length === 0 && (
          <div className="text-center py-12 text-[var(--vsc-text-muted)]">No traffic recorded yet. Configure your proxy to get started.</div>
        )}
      </div>
    </div>
  );
}

// ── Live Stream Tab ──
function LiveTab() {
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
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <div className={`flex items-center gap-1.5 text-xs ${connected ? 'text-emerald-400' : 'text-red-400'}`}>
          <div className={`w-2 h-2 rounded-full ${connected ? 'bg-emerald-400 animate-pulse' : 'bg-red-400'}`} />
          {connected ? 'Connected' : 'Disconnected'}
        </div>
        <span className="text-xs text-[var(--vsc-text-muted)]">{events.length} events</span>
      </div>
      <div ref={containerRef} className="overflow-auto max-h-[calc(100vh-280px)] space-y-1">
        {events.map((e, i) => (
          <div key={i} className="flex items-center gap-3 px-3 py-2 rounded-md bg-[var(--vsc-bg-secondary)] border border-[var(--vsc-border-subtle)] text-xs hover:border-[var(--vsc-border)] transition-colors">
            <span className="text-[var(--vsc-text-muted)] w-16 shrink-0">{formatTime(e.ts)}</span>
            <span className={`shrink-0 w-2 h-2 rounded-full ${e.status >= 200 && e.status < 300 ? 'bg-emerald-400' : 'bg-red-400'}`} />
            {e.ai_usage ? (
              <>
                <span className="px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400 text-[10px] font-medium shrink-0">{e.ai_usage.provider}</span>
                <span className="font-mono text-[var(--vsc-text)] truncate">{e.ai_usage.model}</span>
                <span className="ml-auto shrink-0 text-purple-400">{formatTokens(e.ai_usage.input_tokens)}→{formatTokens(e.ai_usage.output_tokens)}</span>
                <span className="shrink-0 text-white font-medium">{formatCost(e.ai_usage.cost_usd)}</span>
              </>
            ) : (
              <>
                <span className="text-[var(--vsc-text-secondary)] truncate flex-1">{e.method} {e.host}{e.url?.split('?')[0]}</span>
                <span className="ml-auto shrink-0 text-[var(--vsc-text-muted)]">{e.req_kb}KB → {e.res_kb}KB</span>
              </>
            )}
            <span className="shrink-0 text-[var(--vsc-text-muted)]">{e.user_id}</span>
          </div>
        ))}
        {events.length === 0 && (
          <div className="text-center py-16 text-[var(--vsc-text-muted)]">
            <Activity size={32} className="mx-auto mb-3 opacity-30" />
            <p>Waiting for traffic...</p>
            <p className="text-[10px] mt-1">Events appear here in real-time</p>
          </div>
        )}
      </div>
    </div>
  );
}

// ── Setup Tab ──
function SetupTab({ proxyToken, onRegister }: { proxyToken: string; onRegister: () => void }) {
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
    <div className="space-y-6 max-w-2xl">
      {/* Token section */}
      <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 className="text-sm font-semibold text-white mb-3">Your Proxy Token</h3>
        {proxyToken ? (
          <div className="space-y-3">
            <div className="flex items-center gap-2 bg-black/30 rounded-md px-3 py-2 font-mono text-xs text-emerald-400 overflow-x-auto">
              <span className="shrink-0">{proxyToken}</span>
              <CopyButton text={proxyToken} />
            </div>
            <div className="flex items-center gap-2 bg-black/30 rounded-md px-3 py-2 font-mono text-xs text-[var(--vsc-text)] overflow-x-auto">
              <span>export https_proxy="{proxyUrl}"</span>
              <CopyButton text={`export https_proxy="${proxyUrl}"`} />
            </div>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-sm text-[var(--vsc-text-secondary)]">Generate a proxy token to start auditing your AI traffic.</p>
            <button onClick={onRegister} className="px-4 py-2 bg-blue-600 text-white text-sm rounded-md hover:bg-blue-700 transition-colors">
              Generate Token
            </button>
          </div>
        )}
      </div>

      {/* Install CA */}
      <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 className="text-sm font-semibold text-white mb-1">Step 1: Install CA Certificate</h3>
        <p className="text-xs text-[var(--vsc-text-secondary)] mb-3">Required for HTTPS traffic inspection. Run this command on your machine:</p>
        <div className="flex items-center gap-2 bg-black/30 rounded-md px-3 py-2 font-mono text-xs text-[var(--vsc-text)]">
          <span>curl -fsSL https://audit.cicy-ai.com/install-ca | bash</span>
          <CopyButton text="curl -fsSL https://audit.cicy-ai.com/install-ca | bash" />
        </div>
        <div className="mt-2 flex gap-3">
          <a href="/ca.pem" className="text-xs text-[var(--vsc-link)] hover:underline">Download CA cert manually</a>
        </div>
      </div>

      {/* Platform guides */}
      <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 className="text-sm font-semibold text-white mb-3">Step 2: Configure Your Tools</h3>
        <div className="space-y-4">
          {(guide?.platforms || defaultPlatforms).map((p, i) => (
            <div key={i}>
              <h4 className="text-xs font-medium text-[var(--vsc-text)] mb-1.5">{p.name}</h4>
              <div className="space-y-1">
                {p.steps.map((s, j) => (
                  <div key={j} className="flex items-start gap-2 text-xs">
                    <span className="text-[var(--vsc-text-muted)] shrink-0 mt-0.5">{j + 1}.</span>
                    <code className="bg-black/20 rounded px-2 py-1 text-[var(--vsc-text-secondary)] flex-1 break-all">
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
      <div className="bg-[var(--vsc-bg-secondary)] rounded-lg p-5 border border-[var(--vsc-border)]">
        <h3 className="text-sm font-semibold text-white mb-3">How It Works</h3>
        <div className="text-xs text-[var(--vsc-text-secondary)] space-y-2 leading-relaxed">
          <p>CiCy Audit acts as a transparent HTTPS proxy between your AI tools and their API providers.</p>
          <div className="bg-black/20 rounded-md p-3 font-mono text-[10px] text-[var(--vsc-text-muted)] leading-loose">
            Your AI Tool → CiCy Audit Proxy → AI Provider (OpenAI, Anthropic, etc.)<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;↓<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;Parse tokens, model, cost<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;↓<br/>
            &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;Dashboard (you are here)
          </div>
          <p>Supported providers: OpenAI, Anthropic, Google (Gemini), DeepSeek, Qwen, Groq, Mistral, OpenRouter, Azure OpenAI, AWS Bedrock.</p>
        </div>
      </div>
    </div>
  );
}

const defaultPlatforms = [
  {
    name: 'macOS / Linux (CLI tools)',
    steps: [
      'curl -fsSL https://audit.cicy-ai.com/install-ca | bash',
      'export https_proxy=https://YOUR_TOKEN:x@audit.cicy-ai.com:8003',
    ],
  },
  {
    name: 'Cursor / VS Code',
    steps: [
      'Install CA certificate first (see Step 1)',
      'Add to settings.json: "http.proxy": "https://YOUR_TOKEN:x@audit.cicy-ai.com:8003"',
    ],
  },
  {
    name: 'Claude Code / Kiro CLI',
    steps: [
      'Install CA certificate first (see Step 1)',
      'export https_proxy=https://YOUR_TOKEN:x@audit.cicy-ai.com:8003',
      'Run your AI tool normally — all traffic is audited automatically',
    ],
  },
];

// ── Main Dashboard ──
export default function AuditDashboard({ onBack }: { onBack?: () => void }) {
  const [tab, setTab] = useState<Tab>('overview');
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
    { id: 'overview', icon: BarChart3, label: 'Overview' },
    { id: 'usage', icon: Clock, label: 'Usage Log' },
    { id: 'live', icon: Activity, label: 'Live' },
    { id: 'setup', icon: Settings, label: 'Setup' },
  ];

  return (
    <div className="h-screen flex flex-col bg-[var(--vsc-bg)] text-[var(--vsc-text)]">
      {/* Header */}
      <header className="flex items-center gap-3 px-4 py-3 border-b border-[var(--vsc-border)] bg-[var(--vsc-bg-titlebar)] shrink-0">
        {onBack && (
          <button onClick={onBack} className="p-1 rounded hover:bg-[var(--vsc-bg-hover)] text-[var(--vsc-text-secondary)] hover:text-white transition-colors">
            <ArrowLeft size={16} />
          </button>
        )}
        <div className="flex items-center gap-2">
          <Zap size={18} className="text-blue-400" />
          <span className="font-semibold text-white text-sm">CiCy Audit</span>
          <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-blue-500/10 text-blue-400 font-medium">Beta</span>
        </div>
        <div className="flex-1" />
        <span className="text-xs text-[var(--vsc-text-muted)]">
          {userId && `User: ${userId}`}
        </span>
      </header>

      {/* Tab bar */}
      <nav className="flex items-center gap-1 px-4 py-1.5 border-b border-[var(--vsc-border)] bg-[var(--vsc-bg)] shrink-0">
        {tabs.map(t => (
          <button key={t.id} onClick={() => setTab(t.id)}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs transition-colors ${tab === t.id ? 'bg-[var(--vsc-bg-active)] text-white' : 'text-[var(--vsc-text-secondary)] hover:text-[var(--vsc-text)] hover:bg-[var(--vsc-bg-hover)]'}`}>
            <t.icon size={14} />
            {t.label}
          </button>
        ))}
      </nav>

      {/* Content */}
      <main className="flex-1 overflow-auto p-4">
        {tab === 'overview' && <OverviewTab userId={userId} days={days} setDays={setDays} />}
        {tab === 'usage' && <UsageTab userId={userId} />}
        {tab === 'live' && <LiveTab />}
        {tab === 'setup' && <SetupTab proxyToken={proxyToken} onRegister={handleRegister} />}
      </main>
    </div>
  );
}
