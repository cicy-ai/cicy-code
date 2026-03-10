import React, { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import apiService from '../services/api';
import { useApp } from '../contexts/AppContext';

interface MinuteStats { minute: string; req_kb: number; res_kb: number; count: number; }
interface LiveEntry { type: string; pane: string; req_kb: number; res_kb: number; ts: number; method?: string; url?: string; status?: number; }

const intervals = [{ label: '1m', val: 1, min: 60 }, { label: '10m', val: 10, min: 600 }, { label: '1h', val: 60, min: 1440 }];

const AggView: React.FC<{ paneId: string }> = ({ paneId }) => {
  const [data, setData] = useState<MinuteStats[]>([]);
  const [loading, setLoading] = useState(true);
  const [iv, setIv] = useState(0);

  useEffect(() => {
    const fetch_ = async () => {
      try {
        const { data: r } = await apiService.getTrafficStats(paneId, intervals[iv].min, intervals[iv].val);
        setData((r.data || []).sort((a: MinuteStats, b: MinuteStats) => a.minute.localeCompare(b.minute)));
      } catch {}
      finally { setLoading(false); }
    };
    fetch_();
    const t = setInterval(fetch_, 30000);
    return () => clearInterval(t);
  }, [paneId, iv]);

  if (loading) return <div className="flex items-center justify-center h-full"><Loader2 className="w-6 h-6 animate-spin text-gray-500" /></div>;
  if (!data.length) return <div className="flex items-center justify-center h-full text-gray-500 text-sm">No traffic data</div>;

  const totalReq = data.reduce((s, d) => s + d.req_kb, 0);
  const totalRes = data.reduce((s, d) => s + d.res_kb, 0);
  const totalCount = data.reduce((s, d) => s + d.count, 0);
  const maxKB = Math.max(...data.map(d => Math.max(d.req_kb, d.res_kb)), 1);

  return (
    <div className="h-full flex flex-col overflow-auto">
      <div className="flex gap-4 mb-3 text-gray-400 items-center">
        <span>📊 {totalCount} reqs</span>
        <span className="text-blue-400">↑ {totalReq.toFixed(1)} KB</span>
        <span className="text-green-400">↓ {totalRes.toFixed(1)} KB</span>
        <span className="ml-auto flex gap-1">{intervals.map((x, i) => (
          <button key={i} onClick={() => setIv(i)} className={`px-2 py-0.5 rounded ${iv === i ? 'bg-gray-600 text-white' : 'text-gray-500 hover:text-gray-300'}`}>{x.label}</button>
        ))}</span>
      </div>
      <div className="flex items-end gap-px h-36 mb-1">
        {data.map((d, i) => (
          <div key={i} className="flex-1 flex gap-px items-end h-full" title={`${d.minute}\nreq: ${d.req_kb.toFixed(1)}KB\nres: ${d.res_kb.toFixed(1)}KB\n${d.count} reqs`}>
            <div className="flex-1 bg-blue-500 rounded-t transition-all" style={{ height: `${(d.req_kb / maxKB) * 100}%`, minHeight: d.req_kb > 0 ? 2 : 0 }} />
            <div className="flex-1 bg-green-500 rounded-t transition-all" style={{ height: `${(d.res_kb / maxKB) * 100}%`, minHeight: d.res_kb > 0 ? 2 : 0 }} />
          </div>
        ))}
      </div>
      <div className="flex justify-between text-gray-600 mb-3">
        <span>{data[0]?.minute.slice(11)}</span>
        <span>{data[data.length - 1]?.minute.slice(11)}</span>
      </div>
      <div className="flex-1 overflow-auto">
        <table className="w-full">
          <thead className="sticky top-0 bg-[#1e1e1e]">
            <tr className="text-gray-500 border-b border-gray-700">
              <th className="px-2 py-1 text-left">Time</th>
              <th className="px-2 py-1 text-right text-blue-400">Req KB</th>
              <th className="px-2 py-1 text-right text-green-400">Res KB</th>
              <th className="px-2 py-1 text-right">Count</th>
            </tr>
          </thead>
          <tbody>
            {[...data].reverse().map((d, i) => (
              <tr key={i} className="border-b border-gray-800 hover:bg-gray-800/50">
                <td className="px-2 py-1 text-gray-300">{d.minute.slice(11)}</td>
                <td className="px-2 py-1 text-right text-blue-300">{d.req_kb.toFixed(1)}</td>
                <td className="px-2 py-1 text-right text-green-300">{d.res_kb.toFixed(1)}</td>
                <td className="px-2 py-1 text-right text-gray-400">{d.count}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
};

const LiveView: React.FC<{ paneId: string }> = ({ paneId }) => {
  const [entries, setEntries] = useState<LiveEntry[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const fetch_ = async () => {
      try {
        const { data: r } = await apiService.getTrafficRaw(paneId);
        setEntries((r.data || []).slice(-200));
      } catch {}
      finally { setLoading(false); }
    };
    fetch_();
    const iv = setInterval(fetch_, 5000);
    return () => clearInterval(iv);
  }, [paneId]);

  if (loading) return <div className="flex items-center justify-center h-full"><Loader2 className="w-6 h-6 animate-spin text-gray-500" /></div>;

  return (
    <div className="h-full flex flex-col overflow-hidden">
      <div className="flex items-center gap-4 mb-2 text-gray-400">
        <span>📡 {entries.length} entries</span>
        <span className="text-blue-400">↑ {entries.filter(e => e.type === 'req').reduce((s, e) => s + e.req_kb, 0).toFixed(1)} KB</span>
        <span className="text-green-400">↓ {entries.filter(e => e.type === 'res').reduce((s, e) => s + e.res_kb, 0).toFixed(1)} KB</span>
      </div>
      {entries.length > 0 && (() => {
        // Group by 10s buckets
        const buckets: Record<number, { req: number; res: number }> = {};
        entries.forEach(e => {
          const b = Math.floor(e.ts / 10) * 10;
          if (!buckets[b]) buckets[b] = { req: 0, res: 0 };
          if (e.type === 'req') buckets[b].req += e.req_kb;
          else buckets[b].res += e.res_kb;
        });
        const sorted = Object.entries(buckets).sort(([a], [b]) => +a - +b).slice(-30);
        const maxKB = Math.max(...sorted.map(([, d]) => Math.max(d.req, d.res)), 0.1);
        return (
          <div className="flex items-end gap-px h-24 mb-2">
            {sorted.map(([ts, d], i) => (
              <div key={i} className="flex-1 flex gap-px items-end h-full" title={`${new Date(+ts * 1000).toLocaleTimeString()}\nreq: ${d.req.toFixed(1)}KB\nres: ${d.res.toFixed(1)}KB`}>
                <div className="flex-1 bg-blue-500 rounded-t transition-all" style={{ height: `${(d.req / maxKB) * 100}%`, minHeight: d.req > 0 ? 2 : 0 }} />
                <div className="flex-1 bg-green-500 rounded-t transition-all" style={{ height: `${(d.res / maxKB) * 100}%`, minHeight: d.res > 0 ? 2 : 0 }} />
              </div>
            ))}
          </div>
        );
      })()}
      <div className="flex-1 overflow-auto">
        {!entries.length ? (
          <div className="flex items-center justify-center h-full text-gray-500 text-sm">No traffic data</div>
        ) : (
          <table className="w-full">
            <thead className="sticky top-0 bg-[#1e1e1e]">
              <tr className="text-gray-500 border-b border-gray-700">
                <th className="px-2 py-1 text-left">Time</th>
                <th className="px-2 py-1 text-left">Type</th>
                <th className="px-2 py-1 text-right">Status</th>
                <th className="px-2 py-1 text-right">Size</th>
              </tr>
            </thead>
            <tbody>
              {[...entries].reverse().map((e, i) => {
                const isReq = e.type === 'req';
                return (
                  <tr key={i} className={`border-b border-gray-800/50 hover:bg-gray-800/50 ${isReq ? 'bg-blue-950/20' : 'bg-green-950/20'}`} title={e.url || ''}>
                    <td className="px-2 py-1 text-gray-500">{new Date(e.ts * 1000).toLocaleTimeString()}</td>
                    <td className="px-2 py-1">
                      <span className={`inline-flex items-center gap-1 ${isReq ? 'text-blue-400' : 'text-green-400'}`}>
                        {isReq ? '↑' : '↓'} <span className="font-medium">{e.method || '-'}</span>
                      </span>
                    </td>
                    <td className="px-2 py-1 text-right">{e.status ? <span className={e.status < 400 ? 'text-green-400' : 'text-red-400'}>{e.status}</span> : <span className="text-gray-600">-</span>}</td>
                    <td className={`px-2 py-1 text-right ${isReq ? 'text-blue-300' : 'text-green-300'}`}>{(isReq ? e.req_kb : e.res_kb).toFixed(1)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
};

export const TrafficChart: React.FC = () => {
  const [tab, setTab] = useState<'agg' | 'live'>('agg');
  const { currentPaneId } = useApp();

  if (!currentPaneId) return <div className="flex items-center justify-center h-full text-gray-500 text-sm">No pane selected</div>;

  const shortPaneId = currentPaneId.split(':')[0];

  return (
    <div className="h-full flex flex-col p-3 text-xs">
      <div className="flex gap-1 mb-3">
        <button onClick={() => setTab('agg')} className={`px-3 py-1 rounded ${tab === 'agg' ? 'bg-gray-700 text-white' : 'text-gray-500 hover:text-gray-300'}`}>聚合</button>
        <button onClick={() => setTab('live')} className={`px-3 py-1 rounded ${tab === 'live' ? 'bg-gray-700 text-white' : 'text-gray-500 hover:text-gray-300'}`}>实时</button>
      </div>
      {tab === 'agg' ? <AggView paneId={shortPaneId} /> : <LiveView paneId={shortPaneId} />}
    </div>
  );
};
