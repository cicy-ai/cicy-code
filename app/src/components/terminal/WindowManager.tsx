import { useState, useEffect, useRef } from 'react';
import { Plus, Pencil, Trash2, ChevronDown, Check, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';

interface Win { index: string; name: string; active: boolean }

export function WindowManager({ session, onActiveChange }: { session: string; onActiveChange?: (win: Win | null) => void }) {
  const { t } = useTranslation('ui');
  const [wins, setWins] = useState<Win[]>([]);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [editName, setEditName] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const { confirm, node: dialogsNode } = useDialogs();

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
  const del = async (idx: string) => {
    if (!(await confirm({ body: t('windowConfirmDelete', { idx }), danger: true }))) return;
    await apiService.deleteWindow(session, idx);
    load();
  };

  return (
    <div data-id="window-manager-auto-1" ref={ref} className="relative z-50">
      <button data-id="window-manager-auto-2" onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 px-2.5 py-1 text-xs bg-white/[0.04] border border-white/[0.08] rounded-md text-zinc-400 hover:text-zinc-200 hover:bg-white/[0.06] transition-colors cursor-pointer">
        <span data-id="window-manager-auto-3" className="font-mono truncate max-w-[120px]">{active ? `${active.index}:${active.name}` : session}</span>
        <ChevronDown className="w-3 h-3 shrink-0" />
      </button>
      {open && (
        <div data-id="window-manager-auto-4" className="absolute right-0 top-full mt-1 w-64 bg-[#1c1c1e]/95 backdrop-blur-xl border border-white/[0.1] rounded-lg shadow-2xl overflow-hidden">
          <div data-id="window-manager-auto-5" className="max-h-64 overflow-y-auto">
            {wins.map(w => (
              <div data-id="window-manager-auto-6" key={w.index} className={`flex items-center gap-2 px-3 py-2 text-sm hover:bg-white/[0.06] group ${w.active ? 'bg-white/[0.04]' : ''}`}>
                {editing === w.index ? (
                  <form data-id="window-manager-auto-7" className="flex-1 flex items-center gap-1" onSubmit={e => { e.preventDefault(); rename(w.index); }}>
                    <input data-id="window-manager-auto-8" autoFocus value={editName} onChange={e => setEditName(e.target.value)}
                      className="flex-1 bg-white/[0.06] border border-white/[0.1] rounded px-1.5 py-0.5 text-xs text-zinc-200 outline-none" />
                    <button data-id="window-manager-auto-9" type="submit" className="p-0.5 text-emerald-400 hover:text-emerald-300 cursor-pointer"><Check size={12} /></button>
                    <button data-id="window-manager-auto-10" type="button" onClick={() => setEditing(null)} className="p-0.5 text-zinc-500 hover:text-zinc-300 cursor-pointer"><X size={12} /></button>
                  </form>
                ) : (
                  <>
                    <button data-id="window-manager-auto-11" onClick={() => select(w.index)} className="flex-1 text-left truncate text-zinc-300 cursor-pointer">
                      <span data-id="window-manager-auto-12" className="text-zinc-500 font-mono mr-1.5">{w.index}</span>{w.name}
                    </button>
                    <div data-id="window-manager-auto-13" className="hidden group-hover:flex items-center gap-0.5">
                      <button data-id="window-manager-auto-14" onClick={() => { setEditing(w.index); setEditName(w.name); }} className="p-1 text-zinc-500 hover:text-zinc-300 cursor-pointer"><Pencil size={11} /></button>
                      <button data-id="window-manager-auto-15" onClick={() => del(w.index)} className="p-1 text-zinc-500 hover:text-red-400 cursor-pointer"><Trash2 size={11} /></button>
                    </div>
                    {w.active && <span className="w-1.5 h-1.5 rounded-full bg-emerald-500/60 shrink-0" />}
                  </>
                )}
              </div>
            ))}
          </div>
          <div data-id="window-manager-auto-16" className="border-t border-white/[0.08]">
            <button data-id="window-manager-auto-17" onClick={create} className="w-full flex items-center gap-2 px-3 py-2 text-xs text-zinc-500 hover:text-zinc-300 hover:bg-white/[0.06] cursor-pointer">
              <Plus size={12} /> {t('windowCreateButton')}
            </button>
          </div>
        </div>
      )}
      {dialogsNode}
    </div>
  );
}
