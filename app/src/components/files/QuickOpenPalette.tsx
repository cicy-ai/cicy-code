import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Search, X } from 'lucide-react';
import { fsApi, FsSearchMatch, fsBasename } from './api';

interface Props {
  agentId: string;
  open: boolean;
  onClose: () => void;
  onPick: (path: string) => void;
}

/**
 * Cmd+P / Ctrl+P fuzzy file picker. Debounced backend search via /api/fs/search.
 * Renders as a centered modal overlay with arrow-key navigation.
 */
export default function QuickOpenPalette({ agentId, open, onClose, onPick }: Props) {
  const [q, setQ] = useState('');
  const [matches, setMatches] = useState<FsSearchMatch[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [activeIdx, setActiveIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!open) {
      setQ('');
      setMatches([]);
      setActiveIdx(0);
      setError('');
      return;
    }
    setTimeout(() => inputRef.current?.focus(), 30);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    if (!q.trim()) {
      setMatches([]);
      return;
    }
    abortRef.current?.abort();
    const ctl = new AbortController();
    abortRef.current = ctl;
    const t = window.setTimeout(async () => {
      setLoading(true);
      setError('');
      try {
        const res = await fsApi.search(agentId, q.trim(), { limit: 50, signal: ctl.signal });
        setMatches(res.matches);
        setActiveIdx(0);
      } catch (e) {
        if ((e as Error).name !== 'CanceledError') setError((e as Error).message || 'search failed');
      } finally {
        setLoading(false);
      }
    }, 120);
    return () => {
      window.clearTimeout(t);
      ctl.abort();
    };
  }, [q, agentId, open]);

  const onKey = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        setActiveIdx((i) => Math.min(matches.length - 1, i + 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setActiveIdx((i) => Math.max(0, i - 1));
      } else if (e.key === 'Enter') {
        e.preventDefault();
        const m = matches[activeIdx];
        if (m) {
          onPick(m.path);
          onClose();
        }
      }
    },
    [matches, activeIdx, onClose, onPick],
  );

  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLDivElement>(`[data-idx="${activeIdx}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [activeIdx]);

  if (!open) return null;
  return (
    <div
      data-id="quick-open-backdrop"
      className="fixed inset-0 z-[2147483600] bg-black/30 backdrop-blur-sm flex items-start justify-center pt-24"
      onPointerDown={onClose}
    >
      <div
        data-id="quick-open-palette"
        className="w-full max-w-xl mx-4 rounded-lg border border-zinc-700 bg-zinc-900 shadow-2xl"
        onPointerDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 px-3 py-2 border-b border-zinc-800">
          <Search className="w-4 h-4 text-zinc-500 shrink-0" />
          <input
            ref={inputRef}
            data-id="quick-open-input"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={onKey}
            placeholder="按文件名搜索…"
            className="flex-1 bg-transparent outline-none text-sm text-zinc-100 placeholder-zinc-600"
          />
          <button onClick={onClose} className="p-0.5 rounded hover:bg-zinc-800">
            <X className="w-4 h-4 text-zinc-500" />
          </button>
        </div>
        <div ref={listRef} className="max-h-80 overflow-auto text-sm">
          {loading && <div className="px-3 py-2 text-xs text-zinc-500">搜索中…</div>}
          {!loading && error && <div className="px-3 py-2 text-xs text-red-400">{error}</div>}
          {!loading && !error && q && matches.length === 0 && (
            <div className="px-3 py-2 text-xs text-zinc-500">无匹配</div>
          )}
          {matches.map((m, i) => (
            <div
              key={m.path}
              data-id="quick-open-result"
              data-idx={i}
              data-path={m.path}
              className={`flex items-center gap-2 px-3 py-1.5 cursor-pointer ${
                i === activeIdx ? 'bg-zinc-800' : 'hover:bg-zinc-800/60'
              }`}
              onPointerDown={(e) => e.preventDefault()}
              onClick={() => {
                onPick(m.path);
                onClose();
              }}
              onMouseEnter={() => setActiveIdx(i)}
            >
              <span className="text-zinc-100 truncate">{fsBasename(m.path)}</span>
              <span className="text-xs text-zinc-500 truncate">{m.path}</span>
            </div>
          ))}
        </div>
        <div className="flex items-center justify-end gap-3 px-3 py-1.5 border-t border-zinc-800 text-[10px] text-zinc-500">
          <span>↑↓ 选择</span>
          <span>↵ 打开</span>
          <span>Esc 关闭</span>
        </div>
      </div>
    </div>
  );
}
