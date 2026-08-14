// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import apiService from '../../services/api';

type Visibility = 'private' | 'public';
type PortRow = { port: number; name: string; visibility: Visibility; online: boolean; detected?: boolean };

function portURL(fixedDomain: string, port: number): string {
  const host = fixedDomain.replace(/^https?:\/\//, '').replace(/\/$/, '');
  const suffix = '.cicy-ai.com';
  const label = host.endsWith(suffix) ? host.slice(0, -suffix.length) : host;
  return `https://${label}-p${port}${suffix}`;
}

export default function PortsPanel({ fixedDomain, proxyAvailable, paneId }: { fixedDomain: string; proxyAvailable: boolean; paneId: string; onClose: () => void }) {
  const [ports, setPorts] = useState<PortRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const res = await apiService.getPublishedPorts();
      setPorts((res?.data?.ports || []) as PortRow[]);
      setError('');
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || '端口列表加载失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(load, 5000);
    return () => window.clearInterval(timer);
  }, [load]);

  const sorted = useMemo(() => [...ports].sort((a, b) => a.port - b.port), [ports]);

  const save = async (nextPort: number, nextName: string, visibility: Visibility) => {
    setSaving(true); setError('');
    try {
      await apiService.savePublishedPort(nextPort, nextName, visibility);
      await load();
    } catch (e: any) {
      setError(e?.response?.data?.error || e?.message || '端口保存失败');
    } finally { setSaving(false); }
  };

  const agentDock = document.querySelector<HTMLElement>(`[data-id="agent-stack-card-${paneId}"]`);
  const dock = agentDock || document.querySelector<HTMLElement>('[data-id="project-infinite-canvas"]');
  if (!dock) return null;

  return createPortal(
      <section data-id="ports-panel" className={agentDock ? 'shrink-0 overflow-hidden border-t border-white/[0.06] bg-[#101012]' : 'absolute inset-x-0 bottom-12 z-40 max-h-[260px] overflow-hidden border-t border-white/[0.06] bg-[#101012] shadow-2xl'}>
        <div className="max-h-[280px] overflow-auto p-3">
          {error && <div className="mb-3 rounded-lg border border-red-500/20 bg-red-500/[0.06] px-3 py-2 text-[11px] text-red-300">{error}</div>}
          {loading ? <div className="py-8 text-center text-[12px] text-zinc-600">加载中…</div> : sorted.length === 0 ? <div className="py-8 text-center text-[12px] text-zinc-600">还没有转发端口</div> : (
            <div className="space-y-1.5">
              {sorted.map((item) => (
                <div key={item.port} data-id={`ports-row-${item.port}`} className="grid grid-cols-[64px_180px_minmax(0,1fr)_90px] items-center gap-2 overflow-hidden rounded-lg border border-white/[0.06] bg-white/[0.02] px-2.5 py-2">
                  <span className="font-mono text-[12px] text-zinc-200">{item.port}</span>
                  <div className={`min-w-0 text-[10px] ${item.online && proxyAvailable ? 'text-emerald-400' : 'text-zinc-600'}`}>{!item.online ? '本地未监听' : proxyAvailable ? '本地在线 · 公网在线' : '本地在线 · 公网离线'}</div>
                  <button type="button" disabled={!item.online || !proxyAvailable} onClick={() => window.open(portURL(fixedDomain, item.port), '_blank', 'noopener,noreferrer')} title={proxyAvailable ? portURL(fixedDomain, item.port) : '临时 CFT 未连接'} className="min-w-0 truncate text-left font-mono text-[10px] text-blue-400 hover:text-blue-300 disabled:text-zinc-600">{portURL(fixedDomain, item.port)}</button>
                  <select aria-label={`${item.port} visibility`} value={item.visibility} disabled={saving} onChange={(e) => void save(item.port, item.name, e.target.value as Visibility)} className="h-7 rounded border border-white/[0.08] bg-[#111113] px-1.5 text-[11px] text-zinc-300 outline-none">
                    <option value="private">Private</option><option value="public">Public</option>
                  </select>
                </div>
              ))}
            </div>
          )}
        </div>
      </section>, dock,
  );
}
