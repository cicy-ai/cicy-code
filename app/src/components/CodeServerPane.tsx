import { ReactNode, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ChevronDown, Home } from 'lucide-react';
import { WebFrame } from './WebFrame';
const BTN_CLS = 'p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer';

interface CodeServerPaneProps {
  src: string;
  folderLabel: string;
  homeTitle: string;
  onHome: () => void;
  onNavigate: (folder: string) => void;
  favoriteDirs?: string[];
  rightControls?: ReactNode;
  onHeaderMouseDown?: (e: React.MouseEvent<HTMLDivElement>) => void;
  bodyHidden?: boolean;
  className?: string;
}

export default function CodeServerPane({
  src,
  folderLabel,
  homeTitle,
  onHome,
  onNavigate,
  favoriteDirs,
  rightControls,
  onHeaderMouseDown,
  bodyHidden = false,
  className = '',
}: CodeServerPaneProps) {
  const FAVORITE_FOLDERS: string[] = favoriteDirs || [];
  const [showFavorites, setShowFavorites] = useState(false);
  const [favPos, setFavPos] = useState<{ x: number; y: number } | null>(null);
  const favBtnRef = useRef<HTMLButtonElement>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!showFavorites) return;
    const close = () => setShowFavorites(false);
    document.addEventListener('click', close);
    window.addEventListener('floating-window-move', close);
    window.addEventListener('floating-window-close', close);
    return () => {
      document.removeEventListener('click', close);
      window.removeEventListener('floating-window-move', close);
      window.removeEventListener('floating-window-close', close);
    };
  }, [showFavorites]);

  useEffect(() => { setShowFavorites(false); }, [bodyHidden]);

  return (
    <div ref={rootRef} className={`h-full flex flex-col bg-[#0A0A0A] ${className}`}>
      <div
        data-id="code-server-topbar"
        className="h-12 border-b border-white/[0.08] flex items-center px-2 shrink-0 gap-1 bg-gradient-to-r from-[#151925] via-[#121621] to-[#10131c] shadow-[inset_0_-1px_0_rgba(255,255,255,0.04),0_8px_24px_rgba(0,0,0,0.18)]"
        onMouseDown={onHeaderMouseDown}
        style={{ cursor: onHeaderMouseDown ? 'move' : 'default' }}
      >
        <button onClick={onHome} className={BTN_CLS} title={homeTitle}>
          <Home className="w-3.5 h-3.5" />
        </button>
        <div className="relative">
          {FAVORITE_FOLDERS.length > 0 && <button
            ref={favBtnRef}
            onClick={(e) => {
              e.stopPropagation();
              const r = favBtnRef.current?.getBoundingClientRect();
              if (r) setFavPos({ x: r.left, y: r.bottom + 4 });
              setShowFavorites(v => !v);
            }}
            className={BTN_CLS}
            title="Favorite folders"
          >
            <ChevronDown className="w-3 h-3" />
          </button>}
          {showFavorites && favPos && createPortal(
            <div className="fixed bg-[#1e1e1e] border border-[var(--vsc-border)] rounded shadow-lg py-1 z-[9999] min-w-32" style={{ left: favPos.x, top: favPos.y }}>
              {FAVORITE_FOLDERS.map(folder => {
                const newWinUrl = (() => {
                  try {
                    const u = new URL(src);
                    u.searchParams.set('folder', folder.replace('~', import.meta.env.VITE_HOST_HOME || '/home/w3c_offical'));
                    return u.toString();
                  } catch { return src; }
                })();
                return (
                  <div key={folder} className="flex items-center group">
                    <button
                      onClick={() => { onNavigate(folder); setShowFavorites(false); }}
                      className="flex-1 text-left px-3 py-1.5 text-xs text-zinc-400 hover:bg-zinc-700/50 hover:text-zinc-200 transition-colors cursor-pointer"
                    >
                      {folder}
                    </button>
                    <button
                      onClick={(e) => { e.stopPropagation(); window.open(newWinUrl, '_blank'); setShowFavorites(false); }}
                      className="px-2 py-1.5 text-zinc-600 hover:text-zinc-200 hover:bg-zinc-700/50 transition-colors cursor-pointer opacity-0 group-hover:opacity-100"
                      title="在新窗口打开"
                    >
                      <svg className="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
                    </button>
                  </div>
                );
              })}
            </div>,
            document.body
          )}
        </div>
        <div className="flex-1 min-w-0">
          <span className="text-[10px] font-mono text-zinc-600 truncate block">{folderLabel}</span>
        </div>
        {rightControls}
      </div>

      <div data-id="code-server-body" className="flex-1 relative overflow-hidden" style={{ display: bodyHidden ? 'none' : 'block' }}>
        <WebFrame src={src} codeServer className="w-full h-full border-0 bg-[#0A0A0A]" title="Code Server" />
      </div>
    </div>
  );
}
