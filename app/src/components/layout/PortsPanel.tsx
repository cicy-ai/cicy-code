// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { ExternalLink, Globe2, Lock, Plus, Trash2, X } from 'lucide-react';
import apiService from '../../services/api';

type Visibility = 'private' | 'public' | 'closed';
type PortRow = { port: number; name: string; visibility: Visibility; online: boolean; detected?: boolean };

function portURL(fixedDomain: string, port: number): string {
  const host = fixedDomain.replace(/^https?:\/\//, '').replace(/\/$/, '');
  const suffix = '.cicy-ai.com';
  const label = host.endsWith(suffix) ? host.slice(0, -suffix.length) : host;
  return `https://${label}-p${port}${suffix}`;
}

export default function PortsPanel({ fixedDomain, onClose }: { fixedDomain: string; onClose: () => void }) {
  const [ports, setPorts] = useState<PortRow[]>([]);
  const [port, setPort] = useState('3000');
  const [name, setName] = useState('');
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

  const add = async () => {
    const value = Number(port);
    if (!Number.isInteger(value)) return;
    await save(value, name.trim(), 'private');
    setName('');
  };

  const remove = async (value: number) => {
    setSaving(true); setError('');
    try { await apiService.deletePublishedPort(value); await load(); }
    catch (e: any) { setError(e?.response?.data?.error || e?.message || '删除失败'); }
    finally { setSaving(false); }
  };

  return createPortal(
    <div data-id="ports-panel-backdrop" className="fixed inset-0 z-[10020] bg-black/35" onMouseDown={onClose}>
      <section data-id="ports-panel" className="absolute bottom-12 right-5 w-[520px] max-w-[calc(100vw-24px)] overflow-hidden rounded-xl border border-white/[0.1] bg-[#151517] shadow-2xl shadow-black/60" onMouseDown={(e) => e.stopPropagation()}>
        <header className="flex items-center gap-2 border-b border-white/[0.07] px-4 py-3">
          <Globe2 className="h-4 w-4 text-blue-300" />
          <div className="min-w-0 flex-1"><div className="text-[13px] font-semibold text-zinc-100">Ports</div><div className="truncate font-mono text-[10px] text-zinc-600">{fixedDomain}</div></div>
          <button type="button" onClick={onClose} className="rounded p-1 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200"><X className="h-4 w-4" /></button>
        </header>

        <div className="max-h-[55vh] overflow-auto p-3">
          <div className="mb-3 grid grid-cols-[90px_1fr_auto] gap-2">
            <input data-id="ports-add-port" type="number" min={1024} max={65535} value={port} onChange={(e) => setPort(e.target.value)} className="h-9 rounded-lg border border-white/[0.09] bg-black/20 px-2 font-mono text-[12px] text-zinc-200 outline-none focus:border-blue-400/50" placeholder="3000" />
            <input data-id="ports-add-name" value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => { if (!e.nativeEvent.isComposing && e.keyCode !== 229 && e.key === 'Enter') void add(); }} className="h-9 rounded-lg border border-white/[0.09] bg-black/20 px-3 text-[12px] text-zinc-200 outline-none focus:border-blue-400/50" placeholder="端口名称（可选）" />
            <button data-id="ports-add" type="button" disabled={saving} onClick={() => void add()} className="inline-flex h-9 items-center gap-1 rounded-lg border border-blue-400/30 bg-blue-400/10 px-3 text-[12px] text-blue-200 hover:bg-blue-400/15 disabled:opacity-50"><Plus className="h-3.5 w-3.5" /> 添加</button>
          </div>

          {error && <div className="mb-3 rounded-lg border border-red-500/20 bg-red-500/[0.06] px-3 py-2 text-[11px] text-red-300">{error}</div>}
          {loading ? <div className="py-8 text-center text-[12px] text-zinc-600">加载中…</div> : sorted.length === 0 ? <div className="py-8 text-center text-[12px] text-zinc-600">还没有转发端口</div> : (
            <div className="space-y-1.5">
              {sorted.map((item) => (
                <div key={item.port} data-id={`ports-row-${item.port}`} className="grid grid-cols-[64px_minmax(0,1fr)_110px_28px_28px] items-center gap-2 rounded-lg border border-white/[0.06] bg-white/[0.02] px-2.5 py-2">
                  <span className="font-mono text-[12px] text-zinc-200">{item.port}</span>
                  <div className="min-w-0"><div className="flex items-center gap-1.5"><span className="truncate text-[11px] text-zinc-300">{item.name || `Port ${item.port}`}</span>{item.detected && <span className="rounded bg-blue-400/10 px-1 py-px text-[9px] text-blue-300">自动检测</span>}</div><div className={`mt-0.5 text-[10px] ${item.online ? 'text-emerald-400' : 'text-zinc-600'}`}>{item.online ? '在线' : '未监听'}</div></div>
                  <select aria-label={`${item.port} visibility`} value={item.visibility} disabled={saving} onChange={(e) => void save(item.port, item.name, e.target.value as Visibility)} className="h-7 rounded border border-white/[0.08] bg-[#111113] px-1.5 text-[11px] text-zinc-300 outline-none">
                    <option value="private">Private</option><option value="public">Public</option><option value="closed">Closed</option>
                  </select>
                  <button type="button" disabled={item.visibility === 'closed'} onClick={() => window.open(portURL(fixedDomain, item.port), '_blank', 'noopener,noreferrer')} title={item.visibility === 'private' ? '登录后打开' : '打开'} className="grid h-7 w-7 place-items-center rounded text-zinc-500 hover:bg-white/[0.06] hover:text-blue-300 disabled:opacity-25">{item.visibility === 'private' ? <Lock className="h-3.5 w-3.5" /> : <ExternalLink className="h-3.5 w-3.5" />}</button>
                  <button type="button" disabled={saving} onClick={() => void remove(item.port)} title="删除" className="grid h-7 w-7 place-items-center rounded text-zinc-600 hover:bg-red-500/10 hover:text-red-300 disabled:opacity-40"><Trash2 className="h-3.5 w-3.5" /></button>
                </div>
              ))}
            </div>
          )}
        </div>
      </section>
    </div>, document.body,
  );
}
