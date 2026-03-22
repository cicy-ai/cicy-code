import { useState } from 'react';
import { X, Plus, Trash2, Check } from 'lucide-react';
import { getApiBase, setApiBase } from '../../config';

const LS_PRESETS = 'cicy_api_presets';

function loadPresets() {
  try { return JSON.parse(localStorage.getItem(LS_PRESETS) || '[]'); }
  catch { return []; }
}

function savePresets(p: { label: string; url: string }[]) {
  localStorage.setItem(LS_PRESETS, JSON.stringify(p));
}

export function ApiSwitchDialog({ onClose }: { onClose: () => void }) {
  const [presets, setPresets] = useState(loadPresets);
  const [current, setCurrent] = useState(getApiBase);
  const [newLabel, setNewLabel] = useState('');
  const [newUrl, setNewUrl] = useState('');

  function select(url: string) {
    setApiBase(url);
    setCurrent(url);
  }

  function add() {
    if (!newLabel || !newUrl) return;
    const p = [...presets, { label: newLabel, url: newUrl }];
    setPresets(p); savePresets(p);
    setNewLabel(''); setNewUrl('');
  }

  function remove(i: number) {
    const p = presets.filter((_: any, idx: number) => idx !== i);
    setPresets(p); savePresets(p);
  }

  function apply() {
    window.location.reload();
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div className="bg-[#1a1a1a] border border-white/10 rounded-xl w-[420px] p-5 shadow-2xl" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <span className="text-sm font-medium text-zinc-200">API 服务器</span>
          <button onClick={onClose} className="text-zinc-500 hover:text-zinc-300"><X className="w-4 h-4" /></button>
        </div>

        <div className="space-y-1.5 mb-4">
          {/* 当前（不可删除） */}
          <div className={`flex items-center gap-2 px-3 py-2 rounded-lg cursor-pointer ${current === (import.meta.env.VITE_API_BASE || '') ? 'bg-white/10' : 'hover:bg-white/5'}`}
            onClick={() => select(import.meta.env.VITE_API_BASE || '')}>
            <Check className={`w-3.5 h-3.5 shrink-0 ${current === (import.meta.env.VITE_API_BASE || '') ? 'text-emerald-400' : 'text-transparent'}`} />
            <span className="text-xs text-zinc-300 w-20 shrink-0">默认</span>
            <span className="text-xs text-zinc-500 font-mono truncate flex-1">{import.meta.env.VITE_API_BASE || '(同源)'}</span>
          </div>
          {presets.map((p: { label: string; url: string }, i: number) => (
            <div key={i} className={`flex items-center gap-2 px-3 py-2 rounded-lg cursor-pointer group ${current === p.url ? 'bg-white/10' : 'hover:bg-white/5'}`}
              onClick={() => select(p.url)}>
              <Check className={`w-3.5 h-3.5 shrink-0 ${current === p.url ? 'text-emerald-400' : 'text-transparent'}`} />
              <span className="text-xs text-zinc-300 w-20 shrink-0">{p.label}</span>
              <span className="text-xs text-zinc-500 font-mono truncate flex-1">{p.url}</span>
              <button onClick={e => { e.stopPropagation(); remove(i); }}
                className="opacity-0 group-hover:opacity-100 text-zinc-600 hover:text-red-400 transition-opacity">
                <Trash2 className="w-3.5 h-3.5" />
              </button>
            </div>
          ))}
        </div>

        <div className="flex gap-2 mb-4">
          <input value={newLabel} onChange={e => setNewLabel(e.target.value)} placeholder="名称"
            className="w-20 bg-white/5 border border-white/10 rounded px-2 py-1.5 text-xs text-zinc-300 outline-none focus:border-white/20" />
          <input value={newUrl} onChange={e => setNewUrl(e.target.value)} placeholder="https://..."
            className="flex-1 bg-white/5 border border-white/10 rounded px-2 py-1.5 text-xs text-zinc-300 font-mono outline-none focus:border-white/20" />
          <button onClick={add} className="p-1.5 bg-white/5 hover:bg-white/10 rounded text-zinc-400 hover:text-zinc-200">
            <Plus className="w-4 h-4" />
          </button>
        </div>

        <div className="flex justify-between items-center">
          <span className="text-[10px] text-zinc-600 font-mono truncate max-w-[260px]">当前: {current || '(默认)'}</span>
          <button onClick={apply} className="px-3 py-1.5 bg-emerald-600/80 hover:bg-emerald-600 text-white text-xs rounded-lg">
            应用并刷新
          </button>
        </div>
      </div>
    </div>
  );
}
