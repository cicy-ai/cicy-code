import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useTransition,
} from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { ChevronRight, ChevronDown, File as FileIcon, Folder, FolderOpen, RefreshCw, Send, Eye, EyeOff } from 'lucide-react';
import { fsApi, FsEntry, joinFsPath } from './api';

interface FileExplorerProps {
  agentId: string;
  workspaceFolder: string;
  /** Currently opened path (highlighted). */
  activePath?: string;
  onOpenFile: (path: string, entry: FsEntry) => void;
  className?: string;
}

interface VisibleNode {
  /** workspace-relative path; "" for root. */
  path: string;
  name: string;
  level: number;
  isDir: boolean;
  isSymlink: boolean;
  expanded: boolean;
}

interface DirState {
  loading: boolean;
  loaded: boolean;
  expanded: boolean;
  entries: FsEntry[];
  error: string;
  truncated: boolean;
}

const ROOT_PATH = '';
const HEAVY_DIR_NAMES = new Set(['node_modules', '.git', 'dist', 'build', 'target', '.cache']);

function emptyDirState(): DirState {
  return { loading: false, loaded: false, expanded: false, entries: [], error: '', truncated: false };
}

interface ContextMenuState {
  x: number;
  y: number;
  path: string;
  name: string;
  isDir: boolean;
}

export default function FileExplorer({
  agentId,
  workspaceFolder,
  activePath,
  onOpenFile,
  className,
}: FileExplorerProps) {
  // Map<dirPath, DirState>
  const [dirs, setDirs] = useState<Map<string, DirState>>(() => {
    const m = new Map();
    m.set(ROOT_PATH, { ...emptyDirState(), expanded: true });
    return m;
  });
  const [showHidden, setShowHidden] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [menu, setMenu] = useState<ContextMenuState | null>(null);
  const [, startTransition] = useTransition();
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // Loader. Idempotent on path; concurrent calls are coalesced via DirState.loading.
  const loadDir = useCallback(
    async (dirPath: string, opts: { force?: boolean } = {}) => {
      const cur = dirs.get(dirPath);
      if (!opts.force && cur && (cur.loaded || cur.loading)) return;
      setDirs((prev) => {
        const next = new Map(prev);
        next.set(dirPath, { ...emptyDirState(), ...prev.get(dirPath), loading: true, error: '' });
        return next;
      });
      try {
        const res = await fsApi.list(agentId, dirPath, { hidden: showHidden });
        setDirs((prev) => {
          const next = new Map(prev);
          const existing = prev.get(dirPath) ?? emptyDirState();
          next.set(dirPath, {
            ...existing,
            loading: false,
            loaded: true,
            entries: res.entries,
            truncated: !!res.truncated,
            error: '',
          });
          return next;
        });
      } catch (err) {
        setDirs((prev) => {
          const next = new Map(prev);
          const existing = prev.get(dirPath) ?? emptyDirState();
          next.set(dirPath, {
            ...existing,
            loading: false,
            loaded: false,
            error: (err as Error).message,
          });
          return next;
        });
      }
    },
    [agentId, dirs, showHidden],
  );

  // Initial root load + reload on agent / hidden toggle / external refresh
  useEffect(() => {
    setDirs(() => {
      const m = new Map<string, DirState>();
      m.set(ROOT_PATH, { ...emptyDirState(), expanded: true });
      return m;
    });
    loadDir(ROOT_PATH, { force: true });
    // we intentionally don't depend on loadDir to avoid loops
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentId, showHidden, refreshKey]);

  const toggleExpand = useCallback(
    (path: string, isDir: boolean) => {
      if (!isDir) return;
      setDirs((prev) => {
        const next = new Map(prev);
        const cur = prev.get(path) ?? emptyDirState();
        next.set(path, { ...cur, expanded: !cur.expanded });
        return next;
      });
      // Lazy load on first expand
      const cur = dirs.get(path);
      if (!cur || (!cur.loaded && !cur.loading)) {
        loadDir(path);
      }
    },
    [dirs, loadDir],
  );

  // Flatten visible tree using current expanded state
  const visible: VisibleNode[] = useMemo(() => {
    const out: VisibleNode[] = [];
    const walk = (parentPath: string, level: number) => {
      const ds = dirs.get(parentPath);
      if (!ds || !ds.loaded) return;
      for (const e of ds.entries) {
        const childPath = joinFsPath(parentPath, e.name);
        const childDs = dirs.get(childPath);
        const expanded = !!childDs?.expanded;
        out.push({
          path: childPath,
          name: e.name,
          level,
          isDir: e.is_dir,
          isSymlink: !!e.is_symlink,
          expanded,
        });
        if (e.is_dir && expanded) {
          walk(childPath, level + 1);
        }
      }
    };
    walk(ROOT_PATH, 0);
    return out;
  }, [dirs]);

  const virtualizer = useVirtualizer({
    count: visible.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 24,
    overscan: 12,
  });

  const handleNodeClick = useCallback(
    (node: VisibleNode) => {
      if (node.isDir) {
        toggleExpand(node.path, true);
      } else {
        const ds = dirs.get(node.path.includes('/') ? node.path.slice(0, node.path.lastIndexOf('/')) : ROOT_PATH);
        const entry = ds?.entries.find((e) => e.name === node.name);
        if (entry) onOpenFile(node.path, entry);
      }
    },
    [dirs, onOpenFile, toggleExpand],
  );

  const handleContextMenu = useCallback((e: React.MouseEvent, node: VisibleNode) => {
    e.preventDefault();
    setMenu({ x: e.clientX, y: e.clientY, path: node.path, name: node.name, isDir: node.isDir });
  }, []);

  // Close context menu on outside click / escape
  useEffect(() => {
    if (!menu) return;
    const onPointer = () => setMenu(null);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenu(null);
    };
    document.addEventListener('pointerdown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [menu]);

  const handleSendToAgent = useCallback(
    async (path: string) => {
      try {
        await fsApi.sendPathToAgent(agentId, path);
      } catch {}
    },
    [agentId],
  );

  const handleCopyPath = useCallback((path: string, abs: boolean) => {
    const value = abs && workspaceFolder ? `${workspaceFolder.replace(/\/$/, '')}/${path}` : path;
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(value).catch(() => {});
    }
  }, [workspaceFolder]);

  const rootState = dirs.get(ROOT_PATH);

  return (
    <div className={`flex flex-col h-full text-sm select-none ${className || ''}`}>
      <Header
        showHidden={showHidden}
        onToggleHidden={() => setShowHidden((v) => !v)}
        onRefresh={() => startTransition(() => setRefreshKey((k) => k + 1))}
      />
      <div ref={scrollRef} className="flex-1 overflow-auto">
        {rootState?.loading && (
          <div className="px-3 py-2 text-xs text-zinc-500">加载中…</div>
        )}
        {rootState?.error && (
          <div className="px-3 py-2 text-xs text-red-400">{rootState.error}</div>
        )}
        {!rootState?.loading && visible.length === 0 && rootState?.loaded && (
          <div className="px-3 py-2 text-xs text-zinc-500">空目录</div>
        )}
        <div
          style={{
            height: virtualizer.getTotalSize(),
            width: '100%',
            position: 'relative',
          }}
        >
          {virtualizer.getVirtualItems().map((vi) => {
            const node = visible[vi.index];
            if (!node) return null;
            const isHeavy = HEAVY_DIR_NAMES.has(node.name);
            const isActive = node.path === activePath;
            const childState = node.isDir ? dirs.get(node.path) : undefined;
            return (
              <div
                key={node.path}
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  width: '100%',
                  transform: `translateY(${vi.start}px)`,
                  height: vi.size,
                }}
                className={`flex items-center gap-1 px-2 cursor-pointer hover:bg-zinc-800/60 ${
                  isActive ? 'bg-zinc-800' : ''
                } ${isHeavy ? 'opacity-60' : ''}`}
                onClick={() => handleNodeClick(node)}
                onContextMenu={(e) => handleContextMenu(e, node)}
                title={node.path}
              >
                <span style={{ paddingLeft: node.level * 12 }} />
                {node.isDir ? (
                  node.expanded ? (
                    <ChevronDown className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
                  ) : (
                    <ChevronRight className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
                  )
                ) : (
                  <span className="w-3.5 shrink-0" />
                )}
                {node.isDir ? (
                  node.expanded ? (
                    <FolderOpen className="w-3.5 h-3.5 text-amber-400 shrink-0" />
                  ) : (
                    <Folder className="w-3.5 h-3.5 text-amber-400 shrink-0" />
                  )
                ) : (
                  <FileIcon className="w-3.5 h-3.5 text-zinc-400 shrink-0" />
                )}
                <span className="truncate text-zinc-200">{node.name}</span>
                {node.isSymlink && <span className="text-zinc-500 text-xs">→</span>}
                {childState?.loading && (
                  <RefreshCw className="w-3 h-3 animate-spin text-zinc-500" />
                )}
                {childState?.truncated && (
                  <span className="text-xs text-amber-400">…</span>
                )}
              </div>
            );
          })}
        </div>
      </div>
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          path={menu.path}
          isDir={menu.isDir}
          onSendToAgent={() => handleSendToAgent(menu.path)}
          onCopyRelative={() => handleCopyPath(menu.path, false)}
          onCopyAbsolute={() => handleCopyPath(menu.path, true)}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  );
}

interface HeaderProps {
  showHidden: boolean;
  onToggleHidden: () => void;
  onRefresh: () => void;
}

function Header({ showHidden, onToggleHidden, onRefresh }: HeaderProps) {
  return (
    <div className="flex items-center gap-1 px-2 py-1.5 border-b border-zinc-800 bg-zinc-900/50 text-xs text-zinc-300">
      <span className="font-medium">FILES</span>
      <span className="flex-1" />
      <button
        className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
        onClick={onToggleHidden}
        title={showHidden ? '隐藏 .* 文件' : '显示隐藏文件'}
      >
        {showHidden ? <Eye className="w-3.5 h-3.5" /> : <EyeOff className="w-3.5 h-3.5" />}
      </button>
      <button
        className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
        onClick={onRefresh}
        title="刷新"
      >
        <RefreshCw className="w-3.5 h-3.5" />
      </button>
    </div>
  );
}

interface ContextMenuProps {
  x: number;
  y: number;
  path: string;
  isDir: boolean;
  onSendToAgent: () => void;
  onCopyRelative: () => void;
  onCopyAbsolute: () => void;
  onClose: () => void;
}

function ContextMenu({ x, y, path, isDir, onSendToAgent, onCopyRelative, onCopyAbsolute, onClose }: ContextMenuProps) {
  return (
    <div
      className="fixed z-[2147483647] min-w-[180px] py-1 rounded-md border border-zinc-700 bg-zinc-900 shadow-xl text-xs text-zinc-200"
      style={{ left: x, top: y }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <MenuItem icon={<Send className="w-3.5 h-3.5" />} onClick={() => { onSendToAgent(); onClose(); }}>
        发送给当前 agent
      </MenuItem>
      <MenuItem onClick={() => { onCopyRelative(); onClose(); }}>
        复制相对路径
      </MenuItem>
      <MenuItem onClick={() => { onCopyAbsolute(); onClose(); }}>
        复制绝对路径
      </MenuItem>
      <div className="px-3 py-1 text-zinc-500 truncate" title={path}>
        {isDir ? '📁 ' : '📄 '} {path}
      </div>
    </div>
  );
}

function MenuItem({
  icon,
  onClick,
  children,
}: {
  icon?: React.ReactNode;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      className="w-full text-left flex items-center gap-2 px-3 py-1.5 hover:bg-zinc-800"
      onClick={onClick}
    >
      {icon}
      <span>{children}</span>
    </button>
  );
}
