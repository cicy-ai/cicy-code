import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useTransition,
} from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { ChevronRight, ChevronDown, File as FileIcon, Folder, FolderOpen, RefreshCw, Send, Eye, EyeOff, Info, Star, Trash2, Edit3, FilePlus, FolderPlus, Upload, Download, Link as LinkIcon } from 'lucide-react';
import { fsApi, FsEntry, FsListResponse, FsFavorite, joinFsPath } from './api';
import { fsCachePeek, fsCacheSet, fsKey } from './fsCache';

interface FileExplorerProps {
  agentId: string;
  workspaceFolder: string;
  /** Currently opened path (highlighted). */
  activePath?: string;
  onOpenFile: (path: string, entry: FsEntry) => void;
  /** Called the first time a directory is successfully listed, so the parent
   *  can subscribe a fsnotify watcher to that path. */
  onDirLoaded?: (path: string) => void;
  /** Map of dirPath → nonce. When a dir's nonce bumps, the explorer reloads it. */
  dirRefreshNonce?: Record<string, number>;
  /** Page chat-ws client id; passed through to send-path so the broadcast
   *  comes back to this exact tab. */
  pageClientId?: string;
  /** Right-click → show file info modal handler (provided by FilesView). */
  onShowFileInfo?: (path: string) => void;
  /** Right-click → add to favorites handler. */
  onAddFavorite?: (path: string) => void;
  /** Persistent favorites for this agent's workspace. */
  favorites?: FsFavorite[];
  onRemoveFavorite?: (path: string) => void;
  /** CRUD handlers (rename/delete/mkdir/touch). Each may be undefined to hide
   *  the corresponding context-menu entry. */
  onRename?: (path: string, isDir: boolean) => void;
  /** Single or multi delete. The handler decides confirm/dialog. */
  onDelete?: (paths: string[], isDir: boolean) => void;
  onNewFile?: (parentDir: string) => void;
  onNewFolder?: (parentDir: string) => void;
  /** Upload files into the given directory (handler responsible for iterating
   *  the FileList and calling fsApi.upload). */
  onUpload?: (parentDir: string, files: FileList) => void;
  /** Trigger a direct download of the given workspace-relative file path. */
  onDownload?: (path: string) => void;
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
  onDirLoaded,
  dirRefreshNonce,
  pageClientId,
  onShowFileInfo,
  onAddFavorite,
  favorites,
  onRemoveFavorite,
  onRename,
  onDelete,
  onNewFile,
  onNewFolder,
  onUpload,
  onDownload,
  className,
}: FileExplorerProps) {
  const uploadInputRef = useRef<HTMLInputElement | null>(null);
  const pendingUploadDirRef = useRef<string>('');
  const triggerUpload = useCallback((parentDir: string) => {
    if (!onUpload || !uploadInputRef.current) return;
    pendingUploadDirRef.current = parentDir;
    uploadInputRef.current.value = '';
    uploadInputRef.current.click();
  }, [onUpload]);
  // Map<dirPath, DirState>
  const [dirs, setDirs] = useState<Map<string, DirState>>(() => {
    const m = new Map();
    m.set(ROOT_PATH, { ...emptyDirState(), expanded: true });
    return m;
  });
  const [showHidden, setShowHidden] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [menu, setMenu] = useState<ContextMenuState | null>(null);
  // Multi-select state (independent of "active tab" highlighting).
  // selected.size >= 1 always when something is picked; anchor drives shift+click ranges.
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string | null>(null);
  const [, startTransition] = useTransition();
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // Loader. SWR: peek cache paints instantly, then revalidate in the
  // background and update the cache. Idempotent on path; concurrent calls
  // are coalesced via DirState.loading.
  const loadDir = useCallback(
    async (dirPath: string, opts: { force?: boolean } = {}) => {
      const cur = dirs.get(dirPath);
      if (!opts.force && cur && (cur.loaded || cur.loading)) return;

      const key = fsKey.list(agentId, dirPath, showHidden);
      const cached = fsCachePeek<FsListResponse>('list', key);
      setDirs((prev) => {
        const next = new Map(prev);
        const existing = prev.get(dirPath) ?? emptyDirState();
        if (cached) {
          next.set(dirPath, {
            ...existing,
            loading: false,
            loaded: true,
            entries: cached.entries,
            truncated: !!cached.truncated,
            error: '',
          });
        } else {
          next.set(dirPath, { ...emptyDirState(), ...existing, loading: true, error: '' });
        }
        return next;
      });
      if (cached) onDirLoaded?.(dirPath);

      try {
        const res = await fsApi.list(agentId, dirPath, { hidden: showHidden });
        fsCacheSet('list', key, res);
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
        if (!cached) onDirLoaded?.(dirPath);
      } catch (err) {
        setDirs((prev) => {
          const next = new Map(prev);
          const existing = prev.get(dirPath) ?? emptyDirState();
          if (existing.loaded) {
            // Keep showing cached/stale content; only surface error.
            next.set(dirPath, { ...existing, loading: false, error: (err as Error).message });
          } else {
            next.set(dirPath, {
              ...existing,
              loading: false,
              loaded: false,
              error: (err as Error).message,
            });
          }
          return next;
        });
      }
    },
    [agentId, dirs, showHidden, onDirLoaded],
  );

  // External (watcher-driven) refresh: when dirRefreshNonce bumps for a dir
  // that we have loaded, fetch its listing again. Only refresh dirs we
  // already know about — avoids opening collapsed branches uninvited.
  const lastNonceRef = useRef<Record<string, number>>({});
  useEffect(() => {
    if (!dirRefreshNonce) return;
    for (const [dir, n] of Object.entries(dirRefreshNonce)) {
      const prev = lastNonceRef.current[dir] || 0;
      if (n !== prev) {
        lastNonceRef.current[dir] = n;
        if (dirs.has(dir)) {
          loadDir(dir, { force: true });
        }
      }
    }
  }, [dirRefreshNonce, dirs, loadDir]);

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
    (node: VisibleNode, e: React.MouseEvent) => {
      const isMeta = e.metaKey || e.ctrlKey;
      const isShift = e.shiftKey;

      if (isShift && anchor) {
        // Range select from anchor to this node, across the flattened visible list.
        const ai = visible.findIndex((n) => n.path === anchor);
        const bi = visible.findIndex((n) => n.path === node.path);
        if (ai >= 0 && bi >= 0) {
          const [lo, hi] = ai < bi ? [ai, bi] : [bi, ai];
          const next = new Set<string>();
          for (let i = lo; i <= hi; i++) next.add(visible[i].path);
          setSelected(next);
          return;
        }
      }
      if (isMeta) {
        // Toggle this node in selection. Don't open / expand.
        setSelected((prev) => {
          const next = new Set(prev);
          if (next.has(node.path)) next.delete(node.path);
          else next.add(node.path);
          return next;
        });
        setAnchor(node.path);
        return;
      }
      // Plain click: replace selection, set anchor, then open or expand.
      setSelected(new Set([node.path]));
      setAnchor(node.path);
      if (node.isDir) {
        toggleExpand(node.path, true);
      } else {
        const ds = dirs.get(node.path.includes('/') ? node.path.slice(0, node.path.lastIndexOf('/')) : ROOT_PATH);
        const entry = ds?.entries.find((e) => e.name === node.name);
        if (entry) onOpenFile(node.path, entry);
      }
    },
    [anchor, dirs, onOpenFile, toggleExpand, visible],
  );

  const handleContextMenu = useCallback((e: React.MouseEvent, node: VisibleNode) => {
    e.preventDefault();
    // If we right-click a node that's NOT already in the selection,
    // collapse the selection to just that node so the menu acts predictably.
    if (!selected.has(node.path)) {
      setSelected(new Set([node.path]));
      setAnchor(node.path);
    }
    setMenu({ x: e.clientX, y: e.clientY, path: node.path, name: node.name, isDir: node.isDir });
  }, [selected]);

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
        await fsApi.sendPathToAgent(agentId, path, { pageClientId });
      } catch {}
    },
    [agentId, pageClientId],
  );

  // Copy one or many paths (newline-separated) to the clipboard. Always
  // formats as workspace-absolute since the inline "复制相对路径" option
  // was retired in favor of a single, more useful action.
  const handleCopyPath = useCallback((paths: string[]) => {
    const root = workspaceFolder ? workspaceFolder.replace(/\/$/, '') : '';
    const joined = paths
      .map((p) => (root ? `${root}/${p}` : p))
      .join('\n');
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(joined).catch(() => {});
    }
  }, [workspaceFolder]);

  const rootState = dirs.get(ROOT_PATH);

  return (
    <div data-id="file-explorer" className={`flex flex-col h-full text-sm select-none ${className || ''}`}>
      <Header
        showHidden={showHidden}
        onToggleHidden={() => setShowHidden((v) => !v)}
        onRefresh={() => startTransition(() => setRefreshKey((k) => k + 1))}
        onNewFile={onNewFile ? () => onNewFile('') : undefined}
        onNewFolder={onNewFolder ? () => onNewFolder('') : undefined}
      />
      {onUpload && (
        <input
          ref={uploadInputRef}
          data-id="file-explorer-upload-input"
          type="file"
          multiple
          className="hidden"
          onChange={(e) => {
            const files = e.target.files;
            if (files && files.length > 0) {
              onUpload(pendingUploadDirRef.current, files);
            }
            // Reset so picking the same file again still fires onChange.
            e.target.value = '';
          }}
        />
      )}
      <div ref={scrollRef} data-id="file-explorer-scroll" className="flex-1 overflow-auto">
        {favorites && favorites.length > 0 && (
          <div data-id="file-explorer-favorites" className="border-b border-zinc-900 pb-1 mb-1">
            <div className="px-2 py-1 text-[10px] uppercase tracking-wide text-zinc-500 font-medium">
              收藏
            </div>
            {favorites.map((f) => (
              <div
                key={f.path}
                data-id="file-explorer-favorite"
                data-path={f.path}
                className="group flex items-center gap-1 px-2 py-0.5 hover:bg-zinc-800/60 cursor-pointer"
                onClick={() => {
                  const entry: FsEntry = {
                    name: f.name,
                    is_dir: false,
                    size: 0,
                    mtime: 0,
                    mode: '',
                  };
                  onOpenFile(f.path, entry);
                }}
                title={f.path}
              >
                <Star className="w-3 h-3 text-amber-400 shrink-0" />
                <span className="truncate text-zinc-200">{f.name}</span>
                <span className="flex-1" />
                {onRemoveFavorite && (
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      onRemoveFavorite(f.path);
                    }}
                    className="opacity-0 group-hover:opacity-60 hover:opacity-100 text-zinc-500 hover:text-red-400 text-xs px-1"
                    title="移除收藏"
                  >
                    ×
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
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
            const isSelected = selected.has(node.path);
            const childState = node.isDir ? dirs.get(node.path) : undefined;
            return (
              <div
                key={node.path}
                data-id="file-explorer-node"
                data-path={node.path}
                data-is-dir={node.isDir ? 'true' : 'false'}
                data-active={isActive ? 'true' : 'false'}
                data-selected={isSelected ? 'true' : 'false'}
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  width: '100%',
                  transform: `translateY(${vi.start}px)`,
                  height: vi.size,
                }}
                className={`flex items-center gap-1 px-2 cursor-pointer hover:bg-zinc-800/60 ${
                  isSelected ? 'bg-sky-900/40 ring-1 ring-inset ring-sky-700/40' : isActive ? 'bg-zinc-800' : ''
                } ${isHeavy ? 'opacity-60' : ''}`}
                onClick={(e) => handleNodeClick(node, e)}
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
          selectionCount={selected.size}
          onSendToAgent={() => handleSendToAgent(menu.path)}
          // Copy-path is the only multi-aware action right now; rename/delete/etc
          // keep operating on the right-clicked node only.
          onCopyPath={() => {
            const targets = selected.size > 1 && selected.has(menu.path)
              ? Array.from(selected)
              : [menu.path];
            handleCopyPath(targets);
          }}
          onShowFileInfo={onShowFileInfo && !menu.isDir ? () => onShowFileInfo(menu.path) : undefined}
          onAddFavorite={onAddFavorite ? () => onAddFavorite(menu.path) : undefined}
          onRename={onRename ? () => onRename(menu.path, menu.isDir) : undefined}
          onDelete={onDelete ? () => {
            const targets = selected.size > 1 && selected.has(menu.path)
              ? Array.from(selected)
              : [menu.path];
            onDelete(targets, menu.isDir);
          } : undefined}
          onNewFile={onNewFile ? () => onNewFile(menu.isDir ? menu.path : menu.path.includes('/') ? menu.path.slice(0, menu.path.lastIndexOf('/')) : '') : undefined}
          onNewFolder={onNewFolder ? () => onNewFolder(menu.isDir ? menu.path : menu.path.includes('/') ? menu.path.slice(0, menu.path.lastIndexOf('/')) : '') : undefined}
          onUpload={onUpload ? () => triggerUpload(menu.isDir ? menu.path : menu.path.includes('/') ? menu.path.slice(0, menu.path.lastIndexOf('/')) : '') : undefined}
          onDownload={onDownload && !menu.isDir ? () => onDownload(menu.path) : undefined}
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
  onNewFile?: () => void;
  onNewFolder?: () => void;
}

function Header({ showHidden, onToggleHidden, onRefresh, onNewFile, onNewFolder }: HeaderProps) {
  return (
    <div data-id="file-explorer-header" className="flex items-center gap-1 px-2 h-9 border-b border-zinc-800 bg-zinc-900 text-xs text-zinc-300">
      <span className="font-medium">FILES</span>
      <span className="flex-1" />
      {onNewFile && (
        <button
          data-id="file-explorer-new-file"
          className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
          onClick={onNewFile}
          title="新建文件"
        >
          <FilePlus className="w-3.5 h-3.5" />
        </button>
      )}
      {onNewFolder && (
        <button
          data-id="file-explorer-new-folder"
          className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
          onClick={onNewFolder}
          title="新建文件夹"
        >
          <FolderPlus className="w-3.5 h-3.5" />
        </button>
      )}
      <button
        data-id="file-explorer-toggle-hidden"
        className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
        onClick={onToggleHidden}
        title={showHidden ? '隐藏 .* 文件' : '显示隐藏文件'}
      >
        {showHidden ? <Eye className="w-3.5 h-3.5" /> : <EyeOff className="w-3.5 h-3.5" />}
      </button>
      <button
        data-id="file-explorer-refresh"
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
  selectionCount?: number;
  onSendToAgent: () => void;
  onCopyPath: () => void;
  onShowFileInfo?: () => void;
  onAddFavorite?: () => void;
  onRename?: () => void;
  onDelete?: () => void;
  onNewFile?: () => void;
  onNewFolder?: () => void;
  onUpload?: () => void;
  onDownload?: () => void;
  onClose: () => void;
}

function ContextMenu({
  x, y, selectionCount = 1,
  onSendToAgent, onCopyPath,
  onShowFileInfo, onAddFavorite,
  onRename, onDelete, onNewFile, onNewFolder,
  onUpload, onDownload,
  onClose,
}: ContextMenuProps) {
  const multi = selectionCount > 1;
  const ref = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState<{ left: number; top: number }>({ left: x, top: y });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const left = r.right > vw ? Math.max(4, vw - r.width - 4) : x;
    const top = r.bottom > vh ? Math.max(4, vh - r.height - 4) : y;
    if (left !== x || top !== y) setPos({ left, top });
  }, [x, y]);
  return (
    <div
      ref={ref}
      data-id="file-context-menu"
      className="fixed z-[2147483647] min-w-[180px] py-1 rounded-md border border-zinc-700 bg-zinc-900 shadow-xl text-xs text-zinc-200"
      style={{ left: pos.left, top: pos.top }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <MenuItem icon={<Send className="w-3.5 h-3.5" />} onClick={() => { onSendToAgent(); onClose(); }}>
        发送给当前 agent
      </MenuItem>
      {onAddFavorite && (
        <MenuItem icon={<Star className="w-3.5 h-3.5" />} onClick={() => { onAddFavorite(); onClose(); }}>
          加入收藏
        </MenuItem>
      )}
      {onShowFileInfo && (
        <MenuItem icon={<Info className="w-3.5 h-3.5" />} onClick={() => { onShowFileInfo(); onClose(); }}>
          文件信息
        </MenuItem>
      )}
      <Divider />
      {onNewFile && (
        <MenuItem icon={<FilePlus className="w-3.5 h-3.5" />} onClick={() => { onNewFile(); onClose(); }}>
          新建文件
        </MenuItem>
      )}
      {onNewFolder && (
        <MenuItem icon={<FolderPlus className="w-3.5 h-3.5" />} onClick={() => { onNewFolder(); onClose(); }}>
          新建文件夹
        </MenuItem>
      )}
      {onUpload && (
        <MenuItem icon={<Upload className="w-3.5 h-3.5" />} onClick={() => { onUpload(); onClose(); }}>
          上传文件
        </MenuItem>
      )}
      {onDownload && (
        <MenuItem icon={<Download className="w-3.5 h-3.5" />} onClick={() => { onDownload(); onClose(); }}>
          下载
        </MenuItem>
      )}
      {onRename && (
        <MenuItem icon={<Edit3 className="w-3.5 h-3.5" />} onClick={() => { onRename(); onClose(); }}>
          重命名
        </MenuItem>
      )}
      {onDelete && (
        <MenuItem
          icon={<Trash2 className="w-3.5 h-3.5 text-red-400" />}
          onClick={() => { onDelete(); onClose(); }}
          danger
        >
          删除
        </MenuItem>
      )}
      <Divider />
      <MenuItem
        icon={<LinkIcon className="w-3.5 h-3.5" />}
        onClick={() => { onCopyPath(); onClose(); }}
      >
        {multi ? `复制 ${selectionCount} 个路径` : '复制路径'}
      </MenuItem>
    </div>
  );
}

function Divider() {
  return <div className="my-1 border-t border-zinc-800" />;
}

function MenuItem({
  icon,
  onClick,
  children,
  danger,
}: {
  icon?: React.ReactNode;
  onClick: () => void;
  children: React.ReactNode;
  danger?: boolean;
}) {
  const id = typeof children === 'string'
    ? `file-context-menu-${children.replace(/[^\w]/g, '-')}`
    : 'file-context-menu-item';
  return (
    <button
      data-id={id}
      className={`w-full text-left flex items-center gap-2 px-3 py-1.5 hover:bg-zinc-800 ${danger ? 'text-red-300' : ''}`}
      onClick={onClick}
    >
      {icon}
      <span>{children}</span>
    </button>
  );
}
