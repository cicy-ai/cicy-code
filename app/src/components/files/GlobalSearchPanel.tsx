import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Search, CaseSensitive, X, RefreshCw } from 'lucide-react';
import { fsApi, FsGrepMatch } from './api';
import i18n from '../../i18n';

const tr = (k: string, o?: Record<string, unknown>) =>
  i18n.t(`fileExplorer.${k}`, { ns: 'workspace', ...o }) as string;

interface Props {
  agentId: string;
  open: boolean;
  onClose: () => void;
  onPick: (path: string, line: number, col: number) => void;
}

// lucide-react doesn't ship a `RegExp` icon name; alias to something present.
const RegexBadge = (props: { className?: string }) => (
  <span className={`text-[10px] font-bold ${props.className || ''}`}>.*</span>
);

/**
 * Cmd+Shift+F full-text search panel. Calls /api/fs/grep (rg --json) on debounce
 * and renders results grouped by file. Click a hit to jump to line:col.
 */
export default function GlobalSearchPanel({ agentId, open, onClose, onPick }: Props) {
  const [q, setQ] = useState('');
  const [caseSens, setCaseSens] = useState(false);
  const [regex, setRegex] = useState(false);
  const [matches, setMatches] = useState<FsGrepMatch[]>([]);
  const [elapsed, setElapsed] = useState(0);
  const [truncated, setTruncated] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!open) return;
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
        const res = await fsApi.grep(agentId, q.trim(), {
          caseSensitive: caseSens,
          regex,
          signal: ctl.signal,
        });
        setMatches(res.matches);
        setElapsed(res.elapsed_ms);
        setTruncated(!!res.truncated);
      } catch (e) {
        if ((e as Error).name !== 'CanceledError') {
          const msg = (e as Error).message || '';
          if (msg.includes('ripgrep_not_installed')) {
            setError(tr('searchNoRipgrep'));
          } else {
            setError(msg || 'search failed');
          }
        }
      } finally {
        setLoading(false);
      }
    }, 250);
    return () => {
      window.clearTimeout(t);
      ctl.abort();
    };
  }, [q, caseSens, regex, agentId, open]);

  const grouped = useMemo(() => {
    const m = new Map<string, FsGrepMatch[]>();
    for (const it of matches) {
      const arr = m.get(it.path) ?? [];
      arr.push(it);
      m.set(it.path, arr);
    }
    return Array.from(m.entries());
  }, [matches]);

  const onKey = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    },
    [onClose],
  );

  if (!open) return null;
  return (
    <div data-id="global-search-panel" className="absolute inset-y-0 left-0 z-30 flex flex-col w-80 bg-zinc-950 border-r border-zinc-800 text-sm">
      <div className="flex items-center gap-2 px-3 py-2 border-b border-zinc-800">
        <Search className="w-4 h-4 text-zinc-500 shrink-0" />
        <input
          ref={inputRef}
          data-id="global-search-input"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={onKey}
          placeholder={tr('searchPlaceholder')}
          className="flex-1 bg-transparent outline-none text-zinc-100 placeholder-zinc-600"
        />
        <button onClick={onClose} className="p-0.5 rounded hover:bg-zinc-800">
          <X className="w-4 h-4 text-zinc-500" />
        </button>
      </div>
      <div className="flex items-center gap-1 px-3 py-1.5 border-b border-zinc-800 text-[11px] text-zinc-400">
        <button
          onClick={() => setCaseSens((v) => !v)}
          title={tr('searchCaseSensitive')}
          className={`p-1 rounded hover:bg-zinc-800 ${caseSens ? 'bg-zinc-800 text-amber-200' : ''}`}
        >
          <CaseSensitive className="w-3.5 h-3.5" />
        </button>
        <button
          onClick={() => setRegex((v) => !v)}
          title={tr('searchRegex')}
          className={`p-1 rounded hover:bg-zinc-800 ${regex ? 'bg-zinc-800 text-amber-200' : ''}`}
        >
          <RegexBadge />
        </button>
        <span className="flex-1" />
        {loading && <RefreshCw className="w-3 h-3 animate-spin" />}
        {!loading && matches.length > 0 && (
          <span>
            {matches.length}
            {truncated ? '+' : ''} {tr('searchResults')} · {elapsed}ms
          </span>
        )}
      </div>
      <div className="flex-1 overflow-auto">
        {error && <div className="px-3 py-2 text-xs text-red-400">{error}</div>}
        {!error && grouped.length === 0 && q && !loading && (
          <div className="px-3 py-2 text-xs text-zinc-500">{tr('noMatches')}</div>
        )}
        {grouped.map(([path, hits]) => (
          <div key={path} data-id="global-search-group" data-path={path} className="border-b border-zinc-900">
            <div className="px-2 py-1 text-[11px] text-zinc-400 sticky top-0 bg-zinc-950 truncate" title={path}>
              {path}
              <span className="text-zinc-600"> · {hits.length}</span>
            </div>
            {hits.map((h, i) => (
              <button
                key={`${path}:${h.line}:${i}`}
                data-id="global-search-hit"
                data-path={path}
                data-line={h.line}
                onClick={() => onPick(h.path, h.line, h.col || 1)}
                className="block w-full text-left px-3 py-1 hover:bg-zinc-800/60"
              >
                <span className="text-zinc-500 mr-2 text-[10px]">{h.line}</span>
                <code className="text-zinc-100 text-xs font-mono truncate inline-block max-w-[280px] align-middle">
                  {h.text}
                </code>
              </button>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
