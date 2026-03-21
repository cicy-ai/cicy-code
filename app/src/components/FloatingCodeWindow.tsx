import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ChevronUp, Maximize2, Minimize2, Minus, X } from 'lucide-react';
import { lockPointer, unlockPointer } from '../lib/pointerLock';
import CodeServerPane from './CodeServerPane';

const MIN_WIDTH = 720;
const MIN_HEIGHT = 420;
const TOPBAR_HEIGHT = 48;
const EDGE_MARGIN = 16;
const MIN_VISIBLE_WIDTH = 120;
const MIN_VISIBLE_HEIGHT = TOPBAR_HEIGHT;

function getStorageKey(scopeId: string, key: string) {
  return `floating_code_window:${scopeId}:${key}`;
}

function getDefaultPosition() {
  if (typeof window === 'undefined') return { x: 80, y: 72 };
  return {
    x: Math.max(EDGE_MARGIN, Math.round((window.innerWidth - 980) / 2)),
    y: Math.max(EDGE_MARGIN, 72),
  };
}

function getDefaultSize() {
  if (typeof window === 'undefined') return { width: 980, height: 680 };
  return {
    width: Math.min(980, Math.max(MIN_WIDTH, window.innerWidth - 120)),
    height: Math.min(680, Math.max(MIN_HEIGHT, window.innerHeight - 140)),
  };
}

function getMaximizedRect() {
  if (typeof window === 'undefined') return { pos: { x: EDGE_MARGIN, y: EDGE_MARGIN }, size: { width: 1280, height: 800 } };
  return {
    pos: { x: EDGE_MARGIN, y: EDGE_MARGIN },
    size: { width: window.innerWidth - EDGE_MARGIN * 2, height: window.innerHeight - EDGE_MARGIN * 2 },
  };
}

function clampRect(pos: { x: number; y: number }, size: { width: number; height: number }, collapsed = false) {
  if (typeof window === 'undefined') return { pos, size };

  const minHeight = collapsed ? TOPBAR_HEIGHT : MIN_HEIGHT;
  const maxWidth = Math.max(360, window.innerWidth - EDGE_MARGIN * 2);
  const maxHeight = Math.max(TOPBAR_HEIGHT, window.innerHeight - EDGE_MARGIN * 2);
  const nextSize = {
    width: Math.min(size.width, maxWidth),
    height: Math.min(Math.max(size.height, minHeight), maxHeight),
  };

  const nextPos = {
    x: Math.max(-(nextSize.width - MIN_VISIBLE_WIDTH), Math.min(window.innerWidth - MIN_VISIBLE_WIDTH, pos.x)),
    y: Math.max(0, Math.min(window.innerHeight - MIN_VISIBLE_HEIGHT, pos.y)),
  };

  return { pos: nextPos, size: nextSize };
}

interface FloatingCodeWindowProps {
  open: boolean;
  src: string;
  folderLabel: string;
  homeTitle: string;
  storageScopeId: string;
  onHome: () => void;
  onNavigate: (folder: string) => void;
  onClose: () => void;
}

export default function FloatingCodeWindow({
  open,
  src,
  folderLabel,
  homeTitle,
  storageScopeId,
  onHome,
  onNavigate,
  onClose,
}: FloatingCodeWindowProps) {
  const [mounted, setMounted] = useState(false);
  const storageKeys = useMemo(() => ({
    pos: getStorageKey(storageScopeId, 'pos'),
    size: getStorageKey(storageScopeId, 'size'),
    collapsed: getStorageKey(storageScopeId, 'collapsed'),
    maximized: getStorageKey(storageScopeId, 'maximized'),
  }), [storageScopeId]);
  const [position, setPosition] = useState(() => {
    try {
      return JSON.parse(localStorage.getItem(storageKeys.pos) || 'null') || getDefaultPosition();
    } catch {
      return getDefaultPosition();
    }
  });
  const [size, setSize] = useState(() => {
    try {
      return JSON.parse(localStorage.getItem(storageKeys.size) || 'null') || getDefaultSize();
    } catch {
      return getDefaultSize();
    }
  });
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(storageKeys.collapsed) === '1');
  const [maximized, setMaximized] = useState(() => localStorage.getItem(storageKeys.maximized) === '1');

  const dragRef = useRef<{ startX: number; startY: number; startPosX: number; startPosY: number } | null>(null);
  const resizeRef = useRef<{ startX: number; startY: number; startW: number; startH: number } | null>(null);
  const restoreRectRef = useRef<{ pos: { x: number; y: number }; size: { width: number; height: number } } | null>(null);

  useEffect(() => { setMounted(true); }, []);
  useEffect(() => { localStorage.setItem(storageKeys.pos, JSON.stringify(position)); }, [position, storageKeys.pos]);
  useEffect(() => { localStorage.setItem(storageKeys.size, JSON.stringify(size)); }, [size, storageKeys.size]);
  useEffect(() => { localStorage.setItem(storageKeys.collapsed, collapsed ? '1' : '0'); }, [collapsed, storageKeys.collapsed]);
  useEffect(() => { localStorage.setItem(storageKeys.maximized, maximized ? '1' : '0'); }, [maximized, storageKeys.maximized]);

  useEffect(() => {
    const syncToViewport = () => {
      if (maximized) {
        const next = getMaximizedRect();
        setPosition(prev => (prev.x === next.pos.x && prev.y === next.pos.y ? prev : next.pos));
        setSize(prev => (prev.width === next.size.width && prev.height === next.size.height ? prev : next.size));
        return;
      }
      const next = clampRect(position, size, collapsed);
      setPosition(prev => (prev.x === next.pos.x && prev.y === next.pos.y ? prev : next.pos));
      setSize(prev => (prev.width === next.size.width && prev.height === next.size.height ? prev : next.size));
    };
    syncToViewport();
    window.addEventListener('resize', syncToViewport);
    return () => window.removeEventListener('resize', syncToViewport);
  }, [collapsed, maximized, position, size]);

  const finishInteraction = useCallback(() => {
    dragRef.current = null;
    resizeRef.current = null;
    unlockPointer();
  }, []);

  const startDrag = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest('button')) return;
    if (maximized) return;

    dragRef.current = { startX: e.clientX, startY: e.clientY, startPosX: position.x, startPosY: position.y };
    lockPointer();

    const onMove = (ev: MouseEvent) => {
      if (!dragRef.current) return;
      const next = clampRect(
        {
          x: dragRef.current.startPosX + ev.clientX - dragRef.current.startX,
          y: dragRef.current.startPosY + ev.clientY - dragRef.current.startY,
        },
        size,
        collapsed
      );
      setPosition(next.pos);
    };

    const onUp = () => {
      finishInteraction();
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    e.preventDefault();
  }, [collapsed, finishInteraction, maximized, position.x, position.y, size]);

  const startResize = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    e.stopPropagation();
    if (maximized || collapsed) return;

    resizeRef.current = { startX: e.clientX, startY: e.clientY, startW: size.width, startH: size.height };
    lockPointer();

    const onMove = (ev: MouseEvent) => {
      if (!resizeRef.current) return;
      const next = clampRect(position, {
        width: Math.max(MIN_WIDTH, resizeRef.current.startW + ev.clientX - resizeRef.current.startX),
        height: Math.max(MIN_HEIGHT, resizeRef.current.startH + ev.clientY - resizeRef.current.startY),
      });
      setPosition(next.pos);
      setSize(next.size);
    };

    const onUp = () => {
      finishInteraction();
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    e.preventDefault();
  }, [collapsed, finishInteraction, maximized, position, size.height, size.width]);

  const toggleMaximized = useCallback(() => {
    if (maximized) {
      const restore = restoreRectRef.current || { pos: getDefaultPosition(), size: getDefaultSize() };
      const next = clampRect(restore.pos, restore.size, collapsed);
      setPosition(next.pos);
      setSize(next.size);
      setMaximized(false);
      return;
    }

    restoreRectRef.current = { pos: position, size };
    const next = getMaximizedRect();
    setCollapsed(false);
    setPosition(next.pos);
    setSize(next.size);
    setMaximized(true);
  }, [collapsed, maximized, position, size]);

  const rightControls = (
    <>
      <button
        type="button"
        onClick={() => setCollapsed(v => !v)}
        className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"
        title={collapsed ? 'Expand' : 'Collapse'}
      >
        <ChevronUp className={`w-3.5 h-3.5 transition-transform ${collapsed ? 'rotate-180' : ''}`} />
      </button>
      <button
        type="button"
        onClick={toggleMaximized}
        className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"
        title={maximized ? 'Restore' : 'Maximize'}
      >
        {maximized ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
      </button>
      <button
        type="button"
        onClick={onClose}
        className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"
        title="Minimize"
      >
        <Minus className="w-3.5 h-3.5" />
      </button>
      <button
        type="button"
        onClick={onClose}
        className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"
        title="Hide"
      >
        <X className="w-3.5 h-3.5" />
      </button>
    </>
  );

  if (!mounted || !src) return null;

  return createPortal(
    <div
      data-id="floating-code-window"
      className="fixed z-[140] overflow-hidden rounded-xl border border-white/[0.14] bg-[#0a0a0a]/95 backdrop-blur-md shadow-[0_28px_90px_rgba(0,0,0,0.62),0_10px_30px_rgba(15,23,42,0.34),0_0_0_1px_rgba(255,255,255,0.03),inset_0_1px_0_rgba(255,255,255,0.05)]"
      style={{
        left: position.x,
        top: Math.max(0, position.y),
        width: size.width,
        height: collapsed ? TOPBAR_HEIGHT : size.height,
        display: open ? 'block' : 'none',
      }}
    >
      <CodeServerPane
        src={src}
        folderLabel={folderLabel}
        homeTitle={homeTitle}
        onHome={onHome}
        onNavigate={onNavigate}
        onHeaderMouseDown={startDrag}
        rightControls={rightControls}
        bodyHidden={collapsed}
      />
      {!collapsed && !maximized && (
        <div className="absolute bottom-0 right-0 w-4 h-4 cursor-se-resize z-10" onMouseDown={startResize} title="Resize">
          <svg className="w-3 h-3 text-zinc-600 absolute bottom-0.5 right-0.5" viewBox="0 0 10 10">
            <path d="M9 1v8H1" fill="none" stroke="currentColor" strokeWidth="1.5" />
            <path d="M9 5v4H5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
        </div>
      )}
    </div>,
    document.body
  );
}
