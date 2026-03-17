import { useState, useEffect, useRef } from 'react';
import { Plus, Pencil, Trash2, ChevronDown, Check, X } from 'lucide-react';
import apiService from '../../services/api';
import { useDialog } from '../../contexts/DialogContext';

interface Win { index: string; name: string; active: boolean }

export function WindowManager({ session, onActiveChange }: { session: string; onActiveChange?: (win: Win | null) => void }) {
  const [wins, setWins] = useState<Win[]>([]);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [editName, setEditName] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const { confirm } = useDialog();

  const load = () => { apiService.listWindows(session).then(({ data }) => { const w = data.windows || []; setWins(w); onActiveChange?.(w.find((x: Win) => x.active) || null); }).catch(() => {}); };
  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, [session]);
  useEffect(() => { if (!open) return; const h = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false); }; setTimeout(() => document.addEventListener('click', h)); return () => document.removeEventListener('click', h); }, [open]);

  // Disable iframe pointer-events when dropdown is open
  useEffect(() => {
    const area = ref.current?.closest('[data-id="cli-terminal-area"]');
    const iframe = area?.querySelector('iframe, webview') as HTMLElement | null;
    if (iframe) iframe.style.pointerEvents = open ? 'none' : '';
    return () => { if (iframe) iframe.style.pointerEvents = ''; };
  }, [open]);

  const active = wins.find(w => w.active);
  const select = async (idx: string) => { await apiService.selectWindow(session, idx); setOpen(false); setTimeout(load, 500); };
  const create = async () => { await apiService.createWindow(session); load(); };
  const rename = async (idx: string) => { if (!editName.trim()) return; await apiService.renameWindow(session, idx, editName.trim()); setEditing(null); load(); };
  const del = (idx: string) => { confirm(`Delete window ${idx}?`, async () => { await apiService.deleteWindow(session, idx); load(); }); };

  return (
    <div ref={ref} className="relative z-50">
      <button onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 px-2.5 py-1 text-xs bg-white/[0.04] border border-white/[0.08] rounded-md text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.06] transition-colors cursor-pointer">
        <span className="font-mono truncate max-w-[120px]">{active ? `${active.index}:${active.name}` : session}</span>
        <ChevronDown className="w-3 h-3 shrink-0" />
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 w-64 bg-[#1c1c1e]/95 backdrop-blur-xl border border-white/[0.1] rounded-lg shadow-2xl overflow-hidden">
          <div className="max-h-64 overflow-y-auto">
            {wins.map(w => (
              <div key={w.index} className={`flex items-center gap-2 px-3 py-2 text-sm hover:bg-white/[0.06] group ${w.active ? 'bg-white/[0.04]' : ''}`}>
                {editing === w.index ? (
                  <form className="flex-1 flex items-center gap-1" onSubmit={e => { e.preventDefault(); rename(w.index); }}>
                    <input autoFocus value={editName} onChange={e => setEditName(e.target.value)}
                      className="flex-1 bg-white/[0.06] border border-white/[0.1] rounded px-1.5 py-0.5 text-xs text-zinc-200 outline-none" />
                    <button type="submit" className="p-0.5 text-emerald-400 hover:text-emerald-300 cursor-pointer"><Check size={12} /></button>
                    <button type="button" onClick={() => setEditing(null)} className="p-0.5 text-zinc-500 hover:text-zinc-300 cursor-pointer"><X size={12} /></button>
                  </form>
                ) : (
                  <>
                    <button onClick={() => select(w.index)} className="flex-1 text-left truncate text-zinc-300 cursor-pointer">
                      <span className="text-zinc-500 font-mono mr-1.5">{w.index}</span>{w.name}
                    </button>
                    <div className="hidden group-hover:flex items-center gap-0.5">
                      <button onClick={() => { setEditing(w.index); setEditName(w.name); }} className="p-1 text-zinc-500 hover:text-zinc-300 cursor-pointer"><Pencil size={11} /></button>
                      <button onClick={() => del(w.index)} className="p-1 text-zinc-500 hover:text-red-400 cursor-pointer"><Trash2 size={11} /></button>
                    </div>
                    {w.active && <span className="w-1.5 h-1.5 rounded-full bg-emerald-500/60 shrink-0" />}
                  </>
                )}
              </div>
            ))}
          </div>
          <div className="border-t border-white/[0.08]">
            <button onClick={create} className="w-full flex items-center gap-2 px-3 py-2 text-xs text-zinc-500 hover:text-zinc-300 hover:bg-white/[0.06] cursor-pointer">
              <Plus size={12} /> New Window
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
