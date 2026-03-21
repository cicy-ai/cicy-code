import { ReactNode, useEffect, useRef, useState } from 'react';
import { ChevronDown, Home } from 'lucide-react';
import { WebFrame } from './WebFrame';

const FAVORITE_FOLDERS = ['~/', '~/.ssh', '~/projects', '~/skills', '~/Private'];

interface CodeServerPaneProps {
  src: string;
  folderLabel: string;
  homeTitle: string;
  onHome: () => void;
  onNavigate: (folder: string) => void;
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
  rightControls,
  onHeaderMouseDown,
  bodyHidden = false,
  className = '',
}: CodeServerPaneProps) {
  const [showFavorites, setShowFavorites] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!showFavorites) return;
    const onClick = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setShowFavorites(false);
    };
    document.addEventListener('click', onClick);
    return () => document.removeEventListener('click', onClick);
  }, [showFavorites]);

  return (
    <div ref={rootRef} className={`h-full flex flex-col bg-[#0A0A0A] ${className}`}>
      <div
        data-id="code-server-topbar"
        className="h-12 border-b border-white/[0.08] flex items-center px-2 shrink-0 gap-1 bg-gradient-to-r from-[#151925] via-[#121621] to-[#10131c] shadow-[inset_0_-1px_0_rgba(255,255,255,0.04),0_8px_24px_rgba(0,0,0,0.18)]"
        onMouseDown={onHeaderMouseDown}
        style={{ cursor: onHeaderMouseDown ? 'move' : 'default' }}
      >
        <button onClick={onHome} className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title={homeTitle}>
          <Home className="w-3.5 h-3.5" />
        </button>
        <div className="relative">
          <button
            onClick={(e) => { e.stopPropagation(); setShowFavorites(v => !v); }}
            className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"
            title="Favorite folders"
          >
            <ChevronDown className="w-3 h-3" />
          </button>
          {showFavorites && (
            <div className="absolute left-0 top-full mt-1 bg-[#1e1e1e] border border-[var(--vsc-border)] rounded shadow-lg py-1 z-[9999] min-w-32">
              {FAVORITE_FOLDERS.map(folder => (
                <button
                  key={folder}
                  onClick={() => { onNavigate(folder); setShowFavorites(false); }}
                  className="w-full text-left px-3 py-1.5 text-xs text-zinc-400 hover:bg-zinc-700/50 hover:text-zinc-200 transition-colors cursor-pointer"
                >
                  {folder}
                </button>
              ))}
            </div>
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
