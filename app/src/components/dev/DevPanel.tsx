import { useState, useRef, useEffect, useCallback } from 'react';
import { createPortal } from 'react-dom';
import { useDevStore, devStore } from '../../lib/devStore';
import { lockPointer, unlockPointer } from '../../lib/pointerLock';
import { Bug, X, ChevronRight, ChevronDown, Copy, Check, Pencil } from 'lucide-react';

const POS_KEY = 'devpanel_pos';
const SIZE_KEY = 'devpanel_size';
const OPEN_KEY = 'devpanel_open';

export default function DevPanel() {
  const [open, setOpen] = useState(() => localStorage.getItem(OPEN_KEY) === '1');
  const stores = useDevStore();

  useEffect(() => { localStorage.setItem(OPEN_KEY, open ? '1' : '0'); }, [open]);
  useEffect(() => {
    const onOpen = () => setOpen(true);
    window.addEventListener('open-devtools-panel', onOpen);
    return () => window.removeEventListener('open-devtools-panel', onOpen);
  }, []);

  return (
    <>
      {open && createPortal(<Panel stores={stores} onClose={() => setOpen(false)} />, document.body)}
    </>
  );
}

function Panel({ stores, onClose }: { stores: Record<string, { state: Record<string, any>; setter?: any }>; onClose: () => void }) {
  const [pos, setPos] = useState(() => {
    try { return JSON.parse(localStorage.getItem(POS_KEY)!) || { x: window.innerWidth - 460, y: 60 }; } catch { return { x: window.innerWidth - 460, y: 60 }; }
  });
  const [size, setSize] = useState(() => {
    try { return JSON.parse(localStorage.getItem(SIZE_KEY)!) || { w: 420, h: 500 }; } catch { return { w: 420, h: 500 }; }
  });
  const [filter, setFilter] = useState('');
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const dragRef = useRef<{ startX: number; startY: number; startPosX: number; startPosY: number } | null>(null);
  const resizeRef = useRef<{ startX: number; startY: number; startW: number; startH: number } | null>(null);

  useEffect(() => { localStorage.setItem(POS_KEY, JSON.stringify(pos)); }, [pos]);
  useEffect(() => { localStorage.setItem(SIZE_KEY, JSON.stringify(size)); }, [size]);

  // Drag
  const onDragStart = useCallback((e: React.MouseEvent) => {
    dragRef.current = { startX: e.clientX, startY: e.clientY, startPosX: pos.x, startPosY: pos.y };
    lockPointer();
    const onMove = (ev: MouseEvent) => {
      if (!dragRef.current) return;
      setPos({ x: dragRef.current.startPosX + ev.clientX - dragRef.current.startX, y: dragRef.current.startPosY + ev.clientY - dragRef.current.startY });
    };
    const onUp = () => { dragRef.current = null; unlockPointer(); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, [pos]);

  // Resize
  const onResizeStart = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    resizeRef.current = { startX: e.clientX, startY: e.clientY, startW: size.w, startH: size.h };
    lockPointer();
    const onMove = (ev: MouseEvent) => {
      if (!resizeRef.current) return;
      setSize({ w: Math.max(300, resizeRef.current.startW + ev.clientX - resizeRef.current.startX), h: Math.max(200, resizeRef.current.startH + ev.clientY - resizeRef.current.startY) });
    };
    const onUp = () => { resizeRef.current = null; unlockPointer(); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, [size]);

  const toggle = (key: string) => setExpanded(prev => ({ ...prev, [key]: !prev[key] }));
  const storeNames = Object.keys(stores);

  return (
    <div className="fixed z-[999999] flex flex-col bg-[#111113] border border-white/[0.1] rounded-xl shadow-2xl overflow-hidden select-none"
      style={{ left: pos.x, top: pos.y, width: size.w, height: size.h }}>
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2 bg-[#0d0d0f] border-b border-white/[0.06] cursor-move shrink-0" onMouseDown={onDragStart}>
        <Bug className="w-3.5 h-3.5 text-purple-400" />
        <span className="text-xs font-semibold text-purple-300">DevTools</span>
        <span className="text-[10px] text-zinc-600 ml-1">{storeNames.length} stores</span>
        <div className="flex-1" />
        <input value={filter} onChange={e => setFilter(e.target.value)} placeholder="Filter..."
          className="w-28 text-[11px] bg-white/[0.04] border border-white/[0.08] rounded px-2 py-0.5 text-zinc-300 outline-none placeholder:text-zinc-700"
          onClick={e => e.stopPropagation()} />
        <button onClick={onClose} className="p-1 rounded hover:bg-white/[0.06] text-zinc-500 hover:text-zinc-300 cursor-pointer">
          <X className="w-3.5 h-3.5" />
        </button>
      </div>

      {/* Body */}
      <div className="flex-1 overflow-y-auto text-[12px] font-mono">
        {storeNames.map(name => (
          <StoreSection key={name} name={name} state={stores[name].state} hasSetter={!!stores[name].setter}
            filter={filter} expanded={expanded} toggle={toggle} />
        ))}
        {storeNames.length === 0 && (
          <div className="p-4 text-zinc-600 text-center text-[11px]">No stores registered</div>
        )}
      </div>

      {/* Resize handle */}
      <div className="absolute bottom-0 right-0 w-4 h-4 cursor-se-resize" onMouseDown={onResizeStart}>
        <svg className="w-3 h-3 text-zinc-700 absolute bottom-0.5 right-0.5" viewBox="0 0 10 10">
          <path d="M9 1v8H1" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M9 5v4H5" fill="none" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      </div>
    </div>
  );
}

function StoreSection({ name, state, hasSetter, filter, expanded, toggle }: {
  name: string; state: Record<string, any>; hasSetter: boolean;
  filter: string; expanded: Record<string, boolean>; toggle: (k: string) => void;
}) {
  const isOpen = expanded[name] !== false; // default open
  const entries = Object.entries(state);
  const filtered = filter
    ? entries.filter(([k, v]) => k.toLowerCase().includes(filter.toLowerCase()) || String(v).toLowerCase().includes(filter.toLowerCase()))
    : entries;

  return (
    <div className="border-b border-white/[0.04]">
      <button onClick={() => toggle(name)}
        className="w-full flex items-center gap-1.5 px-3 py-1.5 hover:bg-white/[0.03] text-left cursor-pointer">
        {isOpen ? <ChevronDown className="w-3 h-3 text-zinc-500" /> : <ChevronRight className="w-3 h-3 text-zinc-500" />}
        <span className="text-purple-400 font-semibold">{name}</span>
        <span className="text-zinc-700 text-[10px] ml-auto">{entries.length} keys</span>
      </button>
      {isOpen && (
        <div className="pb-1">
          {filtered.map(([key, val]) => (
            <ValueRow key={key} storeName={name} path={key} value={val} hasSetter={hasSetter} depth={0} />
          ))}
          {filtered.length === 0 && <div className="px-6 py-1 text-zinc-700 text-[10px]">No matches</div>}
        </div>
      )}
    </div>
  );
}

function ValueRow({ storeName, path, value, hasSetter, depth }: {
  storeName: string; path: string; value: any; hasSetter: boolean; depth: number;
}) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editVal, setEditVal] = useState('');
  const [copied, setCopied] = useState(false);

  const type = value === null ? 'null' : Array.isArray(value) ? 'array' : typeof value;
  const isExpandable = type === 'object' || type === 'array';
  const isFunc = type === 'function';

  const displayVal = isFunc ? 'ƒ()' : isExpandable
    ? (type === 'array' ? `[${value.length}]` : `{${Object.keys(value).length}}`)
    : String(value);

  const colorClass = type === 'string' ? 'text-green-400' : type === 'number' ? 'text-blue-400'
    : type === 'boolean' ? 'text-yellow-400' : type === 'null' ? 'text-zinc-600' : isFunc ? 'text-zinc-600 italic' : 'text-zinc-400';

  const copy = () => {
    const text = `${storeName}.${path}=${JSON.stringify(value)}`;
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500); }).catch(() => fallbackCopy(text));
    } else fallbackCopy(text);
  };
  const fallbackCopy = (text: string) => {
    const ta = Object.assign(document.createElement('textarea'), { value: text, style: 'position:fixed;opacity:0' });
    document.body.appendChild(ta); ta.select(); document.execCommand('copy'); document.body.removeChild(ta);
    setCopied(true); setTimeout(() => setCopied(false), 1500);
  };

  const startEdit = () => {
    setEditVal(typeof value === 'string' ? value : JSON.stringify(value));
    setEditing(true);
  };

  const commitEdit = () => {
    setEditing(false);
    let parsed: any = editVal;
    try { parsed = JSON.parse(editVal); } catch {}
    devStore.set(storeName, path, parsed);
  };

  const pl = 12 + depth * 12;

  return (
    <>
      <div className="group flex items-center gap-1 px-3 py-[3px] hover:bg-white/[0.03]" style={{ paddingLeft: pl }}>
        {isExpandable ? (
          <button onClick={() => setOpen(!open)} className="p-0 cursor-pointer">
            {open ? <ChevronDown className="w-3 h-3 text-zinc-600" /> : <ChevronRight className="w-3 h-3 text-zinc-600" />}
          </button>
        ) : <span className="w-3" />}

        <span className="text-zinc-400 shrink-0">{path.split('.').pop()}</span>
        <span className="text-zinc-700 mx-0.5">:</span>

        {editing ? (
          <input autoFocus value={editVal} onChange={e => setEditVal(e.target.value)}
            onBlur={commitEdit} onKeyDown={e => { if (e.key === 'Enter') commitEdit(); if (e.key === 'Escape') setEditing(false); }}
            className="flex-1 bg-white/[0.06] border border-purple-500/40 rounded px-1 py-0 text-[11px] text-zinc-200 outline-none" />
        ) : (
          <span className={`truncate ${colorClass}`} title={String(value)}>{displayVal}</span>
        )}

        <div className="ml-auto flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
          {!isFunc && !isExpandable && (
            <button onClick={copy} className="p-0.5 rounded hover:bg-white/[0.08] cursor-pointer" title="Copy">
              {copied ? <Check className="w-3 h-3 text-green-400" /> : <Copy className="w-3 h-3 text-zinc-600" />}
            </button>
          )}
          {hasSetter && !isFunc && !isExpandable && (
            <button onClick={startEdit} className="p-0.5 rounded hover:bg-white/[0.08] cursor-pointer" title="Edit">
              <Pencil className="w-3 h-3 text-zinc-600" />
            </button>
          )}
        </div>
      </div>

      {open && isExpandable && (
        Object.entries(value).map(([k, v]) => (
          <ValueRow key={k} storeName={storeName} path={`${path}.${k}`} value={v} hasSetter={hasSetter} depth={depth + 1} />
        ))
      )}
    </>
  );
}
