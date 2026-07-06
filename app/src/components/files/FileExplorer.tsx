// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useTransition,
} from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { ChevronRight, ChevronDown, File as FileIcon, Folder, FolderOpen, RefreshCw, Send, Eye, EyeOff, Info, Star, Trash2, Edit3, FilePlus, FolderPlus, Upload, Download, Link as LinkIcon, PanelLeftClose } from 'lucide-react';
import { fsApi, FsEntry, FsListResponse, FsFavorite, FsRoot, joinFsPath, fsParent } from './api';
import { fsCachePeek, fsCacheSet, fsKey } from './fsCache';
import i18n from '../../i18n';

// Copy text to the clipboard, with an execCommand fallback for INSECURE origins
// (navigator.clipboard is undefined over plain http://<ip>:port — which is how
// the dev container UI is served — so "复制路径" silently no-ops without this).
function copyTextToClipboard(text: string) {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopyText(text));
    return;
  }
  fallbackCopyText(text);
}
function fallbackCopyText(text: string) {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
  } catch { /* ignore */ }
}

interface FileExplorerProps {
  agentId: string;
  workspaceFolder: string;
  /** Currently opened path (highlighted). */
  activePath?: string;
  /** Open a file. `root` defaults to "workspace" when omitted — non-workspace
   *  roots come from the multi-root sections (projects / skills / home) and
   *  are forwarded to the editor so it can scope reads correctly. */
  onOpenFile: (path: string, entry: FsEntry, root?: string) => void;
  /** Called the first time a directory is successfully listed, so the parent
   *  can subscribe a fsnotify watcher to that path. */
  onDirLoaded?: (path: string) => void;
  /** Map of dirPath → nonce. When a dir's nonce bumps, the explorer reloads it. */
  dirRefreshNonce?: Record<string, number>;
  /** Bump to refresh the extra-root sections (projects/skills/home), which have
   *  no fsnotify watcher. Driven on panel open / agent switch / mutation. */
  remoteReloadNonce?: number;
  /** Page chat-ws client id; passed through to send-path so the broadcast
   *  comes back to this exact tab. */
  pageClientId?: string;
  /** Right-click → show file info modal handler (provided by FilesView). */
  onShowFileInfo?: (path: string, root?: string) => void;
  /** Right-click → add to favorites handler. */
  onAddFavorite?: (path: string) => void;
  /** Persistent favorites for this agent's workspace. */
  favorites?: FsFavorite[];
  onRemoveFavorite?: (path: string) => void;
  /** CRUD handlers (rename/delete/mkdir/touch). Each may be undefined to hide
   *  the corresponding context-menu entry. */
  /** The optional trailing `root` is the fs root id the path belongs to; it
   *  defaults to the workspace and is passed through for extra-root (projects /
   *  skills / home) context-menu actions. */
  onRename?: (path: string, isDir: boolean, root?: string) => void;
  /** Single or multi delete. The handler decides confirm/dialog. */
  onDelete?: (paths: string[], isDir: boolean, root?: string) => void;
  onNewFile?: (parentDir: string, root?: string) => void;
  onNewFolder?: (parentDir: string, root?: string) => void;
  /** Move one or more root-relative paths into `destDir` (drag-and-drop).
   *  The handler performs fsApi.rename(from → destDir/basename) per source and
   *  refreshes the affected directories. Undefined ⇒ drag-to-move disabled. */
  onMove?: (sources: string[], destDir: string, root?: string) => void;
  /** Upload files into the given directory (handler responsible for iterating
   *  the FileList and calling fsApi.upload). */
  onUpload?: (parentDir: string, files: FileList, root?: string) => void;
  /** Trigger a direct download of the given file path under `root`. */
  onDownload?: (path: string, root?: string) => void;
  /** Collapse (hide) the explorer panel — wired to the FILES header button. */
  onCollapse?: () => void;
  /** When set, the primary tree is anchored to this fs root (e.g. "memory")
   *  instead of the workspace, and the extra-root sections are hidden. The CRUD
   *  handlers from the parent must operate on the same root. */
  scopeRoot?: string;
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
// File-explorer copy lives under the `workspace` namespace's `fileExplorer.*` keys.
const t = (k: string, o?: Record<string, unknown>) =>
  i18n.t(`fileExplorer.${k}`, { ns: 'workspace', ...o }) as string;
// Favorites are disabled for now — flip to true to bring the section + the
// "add to favorites" context-menu action back.
const FAVORITES_ENABLED = false;

function emptyDirState(): DirState {
  return { loading: false, loaded: false, expanded: false, entries: [], error: '', truncated: false };
}

interface ContextMenuState {
  x: number;
  y: number;
  /** For the workspace tree this is the workspace-relative path. For an extra
   *  root it's the root-relative path; `root` then carries that root so the
   *  copy-path action can resolve the correct absolute path. */
  path: string;
  name: string;
  isDir: boolean;
  /** Set only when the menu was opened on an extra-root (projects / skills /
   *  home) node. Non-workspace roots are read-only, so the menu hides every
   *  mutating action and resolves copy-path against `root.path` instead of
   *  `workspaceFolder`. Undefined ⇒ workspace tree (full menu). */
  root?: FsRoot;
}

export default function FileExplorer({
  agentId,
  workspaceFolder,
  activePath,
  onOpenFile,
  onDirLoaded,
  dirRefreshNonce,
  remoteReloadNonce,
  pageClientId,
  onShowFileInfo,
  onAddFavorite,
  favorites,
  onRemoveFavorite,
  onRename,
  onDelete,
  onNewFile,
  onNewFolder,
  onMove,
  onUpload,
  onDownload,
  onCollapse,
  scopeRoot,
  className,
}: FileExplorerProps) {
  // When scopeRoot is set the primary tree is anchored to that fs root (e.g.
  // "memory") instead of the workspace, and the extra-root sections are hidden.
  const primaryRoot = scopeRoot || 'workspace';
  const uploadInputRef = useRef<HTMLInputElement | null>(null);
  const pendingUploadDirRef = useRef<string>('');
  const pendingUploadRootRef = useRef<string | undefined>(undefined);
  const triggerUpload = useCallback((parentDir: string, root?: string) => {
    if (!onUpload || !uploadInputRef.current) return;
    pendingUploadDirRef.current = parentDir;
    pendingUploadRootRef.current = root;
    uploadInputRef.current.value = '';
    uploadInputRef.current.click();
  }, [onUpload]);
  // Map<dirPath, DirState>
  const [dirs, setDirs] = useState<Map<string, DirState>>(() => {
    const m = new Map();
    m.set(ROOT_PATH, { ...emptyDirState(), expanded: true });
    return m;
  });
  // Default to showing ALL files/folders, including dotfiles (.git, .cicy, …).
  // The Eye/EyeOff toggle in the header still lets the operator hide them.
  const [showHidden, setShowHidden] = useState(true);
  const [refreshKey, setRefreshKey] = useState(0);
  const [menu, setMenu] = useState<ContextMenuState | null>(null);
  // Workspace section is the primary tree but still collapsible — matches
  // VS Code where every section in the explorer panel has a chevron.
  const [workspaceExpanded, setWorkspaceExpanded] = useState(true);
  // Multi-root sections (projects / skills / home). The workspace is always
  // rendered as the main tree above; this list excludes "workspace".
  // Full roots list (workspace + knowledge + memory + cicy-ai + projects + home).
  // We keep ALL of them so a scopeRoot view (Knowledge/Memory) can resolve its own
  // absolute base for copy-path; the rendered RemoteSections filter most out below.
  const [allRoots, setAllRoots] = useState<FsRoot[]>([]);
  useEffect(() => {
    if (!agentId) return;
    let cancelled = false;
    fsApi
      .roots(agentId)
      .then((rs) => {
        if (cancelled) return;
        setAllRoots(rs);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [agentId]);
  // RemoteSections: exclude 'workspace' (the main tree), 'memory' and 'knowledge'
  // (dedicated views) — none belong in the file explorer's extra-root sections.
  const extraRoots = useMemo(
    () => allRoots.filter((r) => r.id !== 'workspace' && r.id !== 'memory' && r.id !== 'knowledge'),
    [allRoots],
  );
  // Absolute base the workspace-tree menu resolves copy-path against: the scoped
  // root's own path when a scopeRoot view (Knowledge/Memory), else the workspace.
  const primaryBase = scopeRoot
    ? (allRoots.find((r) => r.id === scopeRoot)?.path || workspaceFolder)
    : workspaceFolder;
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

      const key = fsKey.list(agentId, dirPath, showHidden, primaryRoot);
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
        const res = await fsApi.list(agentId, dirPath, { hidden: showHidden, root: primaryRoot });
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
    [agentId, dirs, showHidden, onDirLoaded, primaryRoot],
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
        if (entry) onOpenFile(node.path, entry, primaryRoot);
      }
    },
    [anchor, dirs, onOpenFile, toggleExpand, visible, primaryRoot],
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

  // --- drag-and-drop move ------------------------------------------------
  // The paths being dragged (the dragged node, or the whole multi-selection
  // when the dragged node is part of it). Held in a ref because dataTransfer
  // isn't readable during dragover (only on drop).
  const dragSourcesRef = useRef<string[]>([]);
  // The section/root the active drag originated from. Moves are confined to the
  // SAME section: a drop is only honored when the target's root === this. Guards
  // against cross-section moves (e.g. two explorers on screen, or a future drag
  // source in another section) — a stale/foreign source ref won't match.
  const dragSourceRootRef = useRef<string>('');
  // Custom MIME marking the drag as an internal cicy-fs move from `primaryRoot`,
  // readable on drop to reject foreign / cross-section sources cross-instance.
  const DRAG_ROOT_MIME = 'application/x-cicy-fs-root';
  // The directory currently hovered as a drop target (for highlight). null when
  // not dragging or hovering a non-droppable spot. ROOT_PATH ('') = workspace root.
  const [dragOverDir, setDragOverDir] = useState<string | null>(null);

  // True only when the active drag belongs to THIS section (same root). Used to
  // gate every drop/highlight so a move can never cross sections.
  const dragIsSameSection = useCallback(
    () => dragSourcesRef.current.length > 0 && dragSourceRootRef.current === primaryRoot,
    [primaryRoot],
  );

  // A move into `destDir` is invalid when the source is already there, is the
  // dest itself, or is an ancestor of the dest (can't move a dir into its own
  // subtree). Returns true when at least one source can actually move.
  const canDropInto = useCallback((sources: string[], destDir: string) => {
    return sources.some((from) => {
      if (from === destDir) return false;
      if (fsParent(from) === destDir) return false;
      if (destDir === from || destDir.startsWith(from + '/')) return false;
      return true;
    });
  }, []);

  const handleDragStart = useCallback(
    (e: React.DragEvent, node: VisibleNode) => {
      if (!onMove || scopeRoot) return;
      // Drag the whole selection when the grabbed node is part of a multi-select;
      // otherwise just this node (and make it the selection for visual feedback).
      const sources = selected.has(node.path) && selected.size > 1
        ? Array.from(selected)
        : [node.path];
      if (!(selected.has(node.path) && selected.size > 1)) {
        setSelected(new Set([node.path]));
        setAnchor(node.path);
      }
      dragSourcesRef.current = sources;
      dragSourceRootRef.current = primaryRoot;
      e.dataTransfer.effectAllowed = 'move';
      // Some browsers require a payload for the drag to start.
      try { e.dataTransfer.setData('text/plain', sources.join('\n')); } catch {}
      try { e.dataTransfer.setData(DRAG_ROOT_MIME, primaryRoot); } catch {}
    },
    [onMove, scopeRoot, selected, primaryRoot],
  );

  const handleDragOverNode = useCallback(
    (e: React.DragEvent, node: VisibleNode) => {
      if (!onMove || !dragIsSameSection()) return;
      const destDir = node.isDir ? node.path : fsParent(node.path);
      if (!canDropInto(dragSourcesRef.current, destDir)) return;
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = 'move';
      setDragOverDir(destDir);
    },
    [onMove, canDropInto],
  );

  // Reject a foreign / cross-section drop: the drag started in another root (or
  // didn't originate from a cicy-fs move at all). Clears any highlight state.
  const isForeignDrop = useCallback(
    (e: React.DragEvent) => {
      if (dragIsSameSection()) return false;
      const fromMime = (() => { try { return e.dataTransfer.getData(DRAG_ROOT_MIME); } catch { return ''; } })();
      return fromMime !== primaryRoot;
    },
    [dragIsSameSection, primaryRoot],
  );

  const resetDragState = useCallback(() => {
    setDragOverDir(null);
    dragSourcesRef.current = [];
    dragSourceRootRef.current = '';
  }, []);

  const handleDropNode = useCallback(
    (e: React.DragEvent, node: VisibleNode) => {
      if (!onMove) return;
      if (isForeignDrop(e)) { resetDragState(); return; }
      const destDir = node.isDir ? node.path : fsParent(node.path);
      const sources = dragSourcesRef.current;
      e.preventDefault();
      e.stopPropagation();
      resetDragState();
      if (canDropInto(sources, destDir)) onMove(sources, destDir);
    },
    [onMove, canDropInto, isForeignDrop, resetDragState],
  );

  // Dropping onto empty space / the workspace section background = move to root.
  const handleDragOverRoot = useCallback(
    (e: React.DragEvent) => {
      if (!onMove || scopeRoot || !dragIsSameSection()) return;
      if (!canDropInto(dragSourcesRef.current, ROOT_PATH)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      setDragOverDir(ROOT_PATH);
    },
    [onMove, scopeRoot, canDropInto, dragIsSameSection],
  );

  const handleDropRoot = useCallback(
    (e: React.DragEvent) => {
      if (!onMove || scopeRoot) return;
      if (isForeignDrop(e)) { resetDragState(); return; }
      const sources = dragSourcesRef.current;
      e.preventDefault();
      resetDragState();
      if (canDropInto(sources, ROOT_PATH)) onMove(sources, ROOT_PATH);
    },
    [onMove, scopeRoot, canDropInto, isForeignDrop, resetDragState],
  );

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
    async (path: string, root?: string) => {
      try {
        await fsApi.sendPathToAgent(agentId, path, { pageClientId, root });
      } catch {}
    },
    [agentId, pageClientId],
  );

  // Copy one or many paths (newline-separated) to the clipboard, formatted as
  // absolute. `base` is the root's absolute base path: `workspaceFolder` for
  // the workspace tree, or the extra root's `FsRoot.path` for projects / skills
  // / home — the supplied paths are relative to whichever base is passed.
  const handleCopyPath = useCallback((paths: string[], base: string) => {
    const root = base ? base.replace(/\/$/, '') : '';
    const joined = paths
      .map((p) => (root ? `${root}/${p}` : p))
      .join('\n');
    copyTextToClipboard(joined);
  }, []);

  // Open the context menu for an extra-root (projects / skills / home) node.
  // These roots are read-only, so the menu render hides every mutating action;
  // `root` is carried so copy-path / send resolve against the right base.
  const handleRemoteContextMenu = useCallback(
    (e: React.MouseEvent, root: FsRoot, path: string, name: string, isDir: boolean) => {
      e.preventDefault();
      // Extra-root nodes live outside the workspace multi-select model, so
      // clear that selection to keep the menu acting on this single node.
      setSelected(new Set());
      setAnchor(null);
      setMenu({ x: e.clientX, y: e.clientY, path, name, isDir, root });
    },
    [],
  );

  const rootState = dirs.get(ROOT_PATH);

  return (
    <div data-id="file-explorer" className={`flex flex-col h-full text-sm select-none ${className || ''}`}>
      <Header
        showHidden={showHidden}
        onToggleHidden={() => setShowHidden((v) => !v)}
        onRefresh={() => startTransition(() => setRefreshKey((k) => k + 1))}
        onNewFile={onNewFile ? () => onNewFile('') : undefined}
        onNewFolder={onNewFolder ? () => onNewFolder('') : undefined}
        onCollapse={onCollapse}
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
              onUpload(pendingUploadDirRef.current, files, pendingUploadRootRef.current);
            }
            // Reset so picking the same file again still fires onChange.
            e.target.value = '';
          }}
        />
      )}
      <div ref={scrollRef} data-id="file-explorer-scroll" className="flex-1 overflow-auto">
        <CollapsibleHeader
          label={scopeRoot ? scopeRoot.toUpperCase() : 'WORKSPACE'}
          expanded={workspaceExpanded}
          onToggle={() => setWorkspaceExpanded((v) => !v)}
          dataId="file-explorer-section-workspace-header"
        />
        {workspaceExpanded && rootState?.loading && !rootState?.loaded && (
          <TreeSkeleton rows={7} />
        )}
        {workspaceExpanded && rootState?.error && (
          <div className="px-3 py-2 text-xs text-red-400">{rootState.error}</div>
        )}
        {workspaceExpanded && !rootState?.loading && visible.length === 0 && rootState?.loaded && (
          <div className="px-3 py-2 text-xs text-zinc-500">{t('emptyDir')}</div>
        )}
        <div
          data-id="file-explorer-root-dropzone"
          data-drop-target={onMove && dragOverDir === ROOT_PATH ? 'true' : 'false'}
          onDragOver={onMove ? handleDragOverRoot : undefined}
          onDragLeave={onMove ? () => setDragOverDir((d) => (d === ROOT_PATH ? null : d)) : undefined}
          onDrop={onMove ? handleDropRoot : undefined}
          style={{
            height: workspaceExpanded ? virtualizer.getTotalSize() : 0,
            width: '100%',
            position: 'relative',
            overflow: 'hidden',
          }}
          className={onMove && dragOverDir === ROOT_PATH ? 'ring-1 ring-inset ring-sky-500/60' : ''}
        >
          {workspaceExpanded && virtualizer.getVirtualItems().map((vi) => {
            const node = visible[vi.index];
            if (!node) return null;
            const isHeavy = HEAVY_DIR_NAMES.has(node.name);
            const isActive = node.path === activePath;
            const isSelected = selected.has(node.path);
            const childState = node.isDir ? dirs.get(node.path) : undefined;
            // The directory this node represents as a drop target (a file drops
            // into its parent); highlight when it's the active drop target.
            const nodeDropDir = node.isDir ? node.path : fsParent(node.path);
            const isDropTarget = !!onMove && dragOverDir !== null && dragOverDir === nodeDropDir;
            const draggable = !!onMove && !scopeRoot;
            return (
              <div
                key={node.path}
                data-id="file-explorer-node"
                data-path={node.path}
                data-is-dir={node.isDir ? 'true' : 'false'}
                data-active={isActive ? 'true' : 'false'}
                data-selected={isSelected ? 'true' : 'false'}
                data-drop-target={isDropTarget ? 'true' : 'false'}
                draggable={draggable}
                onDragStart={draggable ? (e) => handleDragStart(e, node) : undefined}
                onDragOver={onMove ? (e) => handleDragOverNode(e, node) : undefined}
                onDrop={onMove ? (e) => handleDropNode(e, node) : undefined}
                style={{
                  position: 'absolute',
                  top: 0,
                  left: 0,
                  width: '100%',
                  transform: `translateY(${vi.start}px)`,
                  height: vi.size,
                }}
                className={`flex items-center gap-1 px-2 cursor-pointer hover:bg-zinc-800/60 ${
                  isDropTarget
                    ? 'bg-sky-800/50 ring-1 ring-inset ring-sky-500'
                    : isSelected ? 'bg-sky-900/40 ring-1 ring-inset ring-sky-700/40' : isActive ? 'bg-zinc-800' : ''
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
        {!scopeRoot && FAVORITES_ENABLED && (
          <FavoritesSection
            favorites={favorites || []}
            onOpen={(path, name) => {
              const entry: FsEntry = { name, is_dir: false, size: 0, mtime: 0, mode: '' };
              onOpenFile(path, entry);
            }}
            onRemove={onRemoveFavorite}
          />
        )}
        {!scopeRoot && extraRoots.map((r) => (
          <RemoteSection
            key={r.id}
            agentId={agentId}
            root={r}
            onOpenFile={(path, entry) => onOpenFile(path, entry, r.id)}
            onContextMenu={handleRemoteContextMenu}
            onMove={onMove}
            // Header refresh (refreshKey) + parent-driven reload both bump this, so
            // one refresh click reloads every section, not just the workspace tree.
            reloadNonce={(remoteReloadNonce ?? 0) + refreshKey}
          />
        ))}
      </div>
      {menu && menu.root ? (
        // Extra-root (projects / skills / home) node: the SAME full menu as the
        // workspace tree. The parent CRUD handlers are root-aware — each takes a
        // trailing root id (here menu.root.id), and the backend resolves the
        // path against that root's base. Paths from RemoteSection are relative
        // to the root, matching what the handlers expect. (Favorite/file-info
        // stay workspace-only — they have no root-aware contract.)
        <ContextMenu
          x={menu.x}
          y={menu.y}
          selectionCount={1}
          // Pass the root id so the backend resolves the absolute path against
          // this extra root, not the agent workspace (same as copy/rename/delete).
          onSendToAgent={() => handleSendToAgent(menu.path, menu.root!.id)}
          // Copy-path resolves against the extra root's absolute base so the
          // copied path points at the actual file (not under workspaceFolder).
          onCopyPath={() => handleCopyPath([menu.path], menu.root!.path)}
          onRename={onRename ? () => onRename(menu.path, menu.isDir, menu.root!.id) : undefined}
          onDelete={onDelete ? () => onDelete([menu.path], menu.isDir, menu.root!.id) : undefined}
          onNewFile={onNewFile ? () => onNewFile(menu.isDir ? menu.path : fsParent(menu.path), menu.root!.id) : undefined}
          onNewFolder={onNewFolder ? () => onNewFolder(menu.isDir ? menu.path : fsParent(menu.path), menu.root!.id) : undefined}
          onUpload={onUpload ? () => triggerUpload(menu.isDir ? menu.path : fsParent(menu.path), menu.root!.id) : undefined}
          onDownload={onDownload && !menu.isDir ? () => onDownload(menu.path, menu.root!.id) : undefined}
          onClose={() => setMenu(null)}
        />
      ) : menu ? (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          selectionCount={selected.size}
          // Pass the scoped root (Knowledge/Memory views) so the backend resolves
          // the absolute path against it, not the agent workspace. undefined in the
          // normal Files tab → backend defaults to workspace.
          onSendToAgent={() => handleSendToAgent(menu.path, scopeRoot)}
          // Copy-path is the only multi-aware action right now; rename/delete/etc
          // keep operating on the right-clicked node only. primaryBase is the
          // scoped root's absolute base in a scopeRoot view, else the workspace.
          onCopyPath={() => {
            const targets = selected.size > 1 && selected.has(menu.path)
              ? Array.from(selected)
              : [menu.path];
            handleCopyPath(targets, primaryBase);
          }}
          onShowFileInfo={onShowFileInfo && !menu.isDir ? () => onShowFileInfo(menu.path, scopeRoot) : undefined}
          onAddFavorite={FAVORITES_ENABLED && onAddFavorite ? () => onAddFavorite(menu.path) : undefined}
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
      ) : null}
    </div>
  );
}

// Skeleton placeholder shown while a section's directory listing loads — a few
// pulsing rows mimicking the tree layout (icon chip + name bar of varying
// width/indent). Far less jarring than a "加载中…" text flashing in and out.
function TreeSkeleton({ rows = 6, dense = false }: { rows?: number; dense?: boolean }) {
  // Deterministic per-row shape so the skeleton looks like a real varied tree
  // without re-randomizing on every render (which would itself flicker).
  const SHAPES = [
    { indent: 0, w: 'w-28' },
    { indent: 1, w: 'w-20' },
    { indent: 1, w: 'w-32' },
    { indent: 0, w: 'w-24' },
    { indent: 2, w: 'w-16' },
    { indent: 1, w: 'w-28' },
    { indent: 0, w: 'w-20' },
    { indent: 1, w: 'w-24' },
  ];
  return (
    <div data-id="file-explorer-skeleton" className="animate-pulse select-none" aria-hidden="true">
      {Array.from({ length: rows }).map((_, i) => {
        const s = SHAPES[i % SHAPES.length];
        return (
          <div key={i} className={`flex items-center gap-2 px-2 ${dense ? 'py-1' : 'py-1.5'}`}>
            <span style={{ paddingLeft: s.indent * 12 }} />
            <span className="w-3.5 h-3.5 rounded-sm bg-zinc-800 shrink-0" />
            <span className={`h-3 rounded bg-zinc-800 ${s.w}`} />
          </div>
        );
      })}
    </div>
  );
}

interface HeaderProps {
  showHidden: boolean;
  onToggleHidden: () => void;
  onRefresh: () => void;
  onNewFile?: () => void;
  onNewFolder?: () => void;
  onCollapse?: () => void;
}

function Header({ showHidden, onToggleHidden, onRefresh, onNewFile, onNewFolder, onCollapse }: HeaderProps) {
  return (
    <div data-id="file-explorer-header" className="flex items-center gap-1 px-2 h-9 border-b border-zinc-800 bg-zinc-900 text-xs text-zinc-300">
      <button
        data-id="file-explorer-collapse"
        className="flex items-center px-1.5 py-0.5 rounded hover:bg-zinc-800"
        onClick={onCollapse}
        title={t('collapseTree')}
      >
        <PanelLeftClose className="w-3.5 h-3.5" />
      </button>
      <span className="flex-1" />
      {onNewFile && (
        <button
          data-id="file-explorer-new-file"
          className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
          onClick={onNewFile}
          title={t('newFile')}
        >
          <FilePlus className="w-3.5 h-3.5" />
        </button>
      )}
      {onNewFolder && (
        <button
          data-id="file-explorer-new-folder"
          className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
          onClick={onNewFolder}
          title={t('newFolder')}
        >
          <FolderPlus className="w-3.5 h-3.5" />
        </button>
      )}
      <button
        data-id="file-explorer-toggle-hidden"
        className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
        onClick={onToggleHidden}
        title={showHidden ? t('hideDotfiles') : t('showDotfiles')}
      >
        {showHidden ? <Eye className="w-3.5 h-3.5" /> : <EyeOff className="w-3.5 h-3.5" />}
      </button>
      <button
        data-id="file-explorer-refresh"
        className="flex items-center gap-1 px-1.5 py-0.5 rounded hover:bg-zinc-800"
        onClick={onRefresh}
        title={t('refresh')}
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
        {t('sendToAgent')}
      </MenuItem>
      {onAddFavorite && (
        <MenuItem icon={<Star className="w-3.5 h-3.5" />} onClick={() => { onAddFavorite(); onClose(); }}>
          {t('addFavorite')}
        </MenuItem>
      )}
      {onShowFileInfo && (
        <MenuItem icon={<Info className="w-3.5 h-3.5" />} onClick={() => { onShowFileInfo(); onClose(); }}>
          {t('fileInfo')}
        </MenuItem>
      )}
      <Divider />
      {onNewFile && (
        <MenuItem icon={<FilePlus className="w-3.5 h-3.5" />} onClick={() => { onNewFile(); onClose(); }}>
          {t('newFile')}
        </MenuItem>
      )}
      {onNewFolder && (
        <MenuItem icon={<FolderPlus className="w-3.5 h-3.5" />} onClick={() => { onNewFolder(); onClose(); }}>
          {t('newFolder')}
        </MenuItem>
      )}
      {onUpload && (
        <MenuItem icon={<Upload className="w-3.5 h-3.5" />} onClick={() => { onUpload(); onClose(); }}>
          {t('uploadFile')}
        </MenuItem>
      )}
      {onDownload && (
        <MenuItem icon={<Download className="w-3.5 h-3.5" />} onClick={() => { onDownload(); onClose(); }}>
          {t('download')}
        </MenuItem>
      )}
      {onRename && (
        <MenuItem icon={<Edit3 className="w-3.5 h-3.5" />} onClick={() => { onRename(); onClose(); }}>
          {t('rename')}
        </MenuItem>
      )}
      {onDelete && (
        <MenuItem
          icon={<Trash2 className="w-3.5 h-3.5 text-red-400" />}
          onClick={() => { onDelete(); onClose(); }}
          danger
        >
          {t('delete')}
        </MenuItem>
      )}
      <Divider />
      <MenuItem
        icon={<LinkIcon className="w-3.5 h-3.5" />}
        onClick={() => { onCopyPath(); onClose(); }}
      >
        {multi ? t('copyPathN', { count: selectionCount }) : t('copyPath')}
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

// ── multi-root sections ───────────────────────────────────────────────────

// Collapsible section header with chevron + label + optional right-aligned
// count badge. Pure UI — caller owns the expanded/collapsed state.
function CollapsibleHeader({
  label,
  expanded,
  onToggle,
  count,
  dataId,
}: {
  label: string;
  expanded: boolean;
  onToggle: () => void;
  count?: number;
  dataId?: string;
}) {
  return (
    <button
      data-id={dataId}
      onClick={onToggle}
      className="w-full flex items-center gap-1 px-2 pt-2 pb-0.5 text-[10px] uppercase tracking-wide text-zinc-500 hover:text-zinc-300 font-medium select-none"
    >
      {expanded ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
      <span>{label}</span>
      {typeof count === 'number' && (
        <span className="ml-1 text-zinc-600 normal-case">({count})</span>
      )}
    </button>
  );
}

interface FavoritesSectionProps {
  favorites: FsFavorite[];
  onOpen: (path: string, name: string) => void;
  onRemove?: (path: string) => void;
}

function FavoritesSection({ favorites, onOpen, onRemove }: FavoritesSectionProps) {
  // The favorites list itself is short (capped by the backend), so unlike
  // remote sections this expands by default when there's anything in it.
  const [expanded, setExpanded] = useState(true);
  if (favorites.length === 0) return null;
  return (
    <div data-id="file-explorer-favorites" className="border-t border-zinc-800 mt-1 pt-1">
      <CollapsibleHeader
        label="FAVORITES"
        expanded={expanded}
        onToggle={() => setExpanded((v) => !v)}
        count={favorites.length}
        dataId="file-explorer-favorites-header"
      />
      {expanded && favorites.map((f) => (
        <div
          key={f.path}
          data-id="file-explorer-favorite"
          data-path={f.path}
          className="group flex items-center gap-1 px-2 py-0.5 hover:bg-zinc-800/60 cursor-pointer"
          onClick={() => onOpen(f.path, f.name)}
          title={f.path}
        >
          <Star className="w-3 h-3 text-amber-400 shrink-0" />
          <span className="truncate text-zinc-200">{f.name}</span>
          <span className="flex-1" />
          {onRemove && (
            <button
              onClick={(e) => { e.stopPropagation(); onRemove(f.path); }}
              className="opacity-0 group-hover:opacity-60 hover:opacity-100 text-zinc-500 hover:text-red-400 text-xs px-1"
              title={t('removeFavorite')}
            >
              ×
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

interface RemoteSectionProps {
  agentId: string;
  root: FsRoot;
  onOpenFile: (path: string, entry: FsEntry) => void;
  /** Right-click on a node in this section. The parent opens the context menu,
   *  carrying `root` so the actions resolve against the right base. `path` is
   *  root-relative; same value the open handler receives. */
  onContextMenu?: (e: React.MouseEvent, root: FsRoot, path: string, name: string, isDir: boolean) => void;
  /** Drag-to-move within this root (root-aware). Mirrors the workspace tree:
   *  drop a node onto a directory to relocate it there. Undefined ⇒ disabled. */
  onMove?: (sources: string[], destDir: string, root?: string) => void | Promise<void>;
  /** Bump to force-reload every already-loaded dir in this section (keeping
   *  collapse state). Driven by the parent on panel open / agent switch and
   *  after a mutation, since this section has no fsnotify watcher of its own. */
  reloadNonce?: number;
}

// Lazy-loaded subtree for a non-workspace root (projects / skills / home).
// First expand kicks off the listing; collapse keeps state so re-expand is
// instant. Stand-alone state (not shared with the workspace virtualizer) so
// each section can be developed and reasoned about independently.
//
// Hidden files are always shown here — these roots are reference / config
// locations where dot-prefixed entries (`.bashrc`, `.config`, `.git`, …)
// are usually the whole point. The workspace tree's eye-toggle still gates
// dot-files for the active project.
function RemoteSection({ agentId, root, onOpenFile, onContextMenu, onMove, reloadNonce }: RemoteSectionProps) {
  const [expanded, setExpanded] = useState(false);
  // dirs keyed by root-relative path; '' is the root listing.
  const [dirs, setDirs] = useState<Map<string, DirState>>(new Map());
  // Drag-to-move state, local to this section (moves never cross roots).
  const [dragPath, setDragPath] = useState<string | null>(null);
  const [dragOverDir, setDragOverDir] = useState<string | null>(null);

  const loadDir = useCallback(async (path: string, force = false) => {
    const cur = dirs.get(path);
    if (!force && cur && (cur.loaded || cur.loading)) return;
    setDirs((prev) => {
      const next = new Map(prev);
      next.set(path, { ...(prev.get(path) ?? emptyDirState()), loading: true, error: '' });
      return next;
    });
    try {
      const res = await fsApi.list(agentId, path, { hidden: true, root: root.id });
      setDirs((prev) => {
        const next = new Map(prev);
        next.set(path, {
          ...(prev.get(path) ?? emptyDirState()),
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
        next.set(path, {
          ...(prev.get(path) ?? emptyDirState()),
          loading: false,
          error: (err as Error).message,
        });
        return next;
      });
    }
  }, [agentId, dirs, root.id]);

  const handleToggle = useCallback(() => {
    setExpanded((wasExpanded) => {
      const next = !wasExpanded;
      if (next && !dirs.has('')) {
        // Fire lazy load on first expand; subsequent expands reuse the cache.
        loadDir('');
      }
      return next;
    });
  }, [dirs, loadDir]);

  // Parent-driven refresh: re-fetch every dir we've already loaded (root + any
  // expanded subdir), keeping collapse state. This section has no fsnotify
  // watcher, so without this its file/folder state goes stale after external
  // changes or mutations — the nonce bumps on panel open / agent switch / a
  // rename/delete/move, looping the loaded dirs back to fresh.
  const lastReloadRef = useRef(reloadNonce);
  useEffect(() => {
    if (reloadNonce === undefined || reloadNonce === lastReloadRef.current) return;
    lastReloadRef.current = reloadNonce;
    for (const [path, d] of dirs) {
      if (d.loaded || d.loading) loadDir(path, true);
    }
  }, [reloadNonce, dirs, loadDir]);

  // Drop `dragPath` into `destDir` (root-relative). Guards against no-op and
  // moving a dir into itself / its own descendant, then force-reloads the
  // affected dirs (this section owns its own state, so the parent's
  // workspace-scoped refresh doesn't reach it).
  const doMove = useCallback(async (destDir: string) => {
    const from = dragPath;
    setDragPath(null);
    setDragOverDir(null);
    if (!onMove || !from) return;
    if (fsParent(from) === destDir) return;                         // already here
    if (destDir === from || destDir.startsWith(from + '/')) return; // into self/descendant
    await onMove([from], destDir, root.id);
    await loadDir(fsParent(from), true);
    await loadDir(destDir, true);
  }, [dragPath, onMove, root.id, loadDir]);

  // Showing the tree: walk dirs map starting at '' and render each entry.
  // Children render only when their parent is expanded. Independent of the
  // workspace tree's virtualizer; remote trees are expected to stay small
  // because users rarely expand more than one or two levels here.
  const renderDir = (parentPath: string, level: number): React.ReactNode => {
    const ds = dirs.get(parentPath);
    if (!ds || !ds.loaded) return null;
    return ds.entries.map((e) => {
      const childPath = joinFsPath(parentPath, e.name);
      const childDs = dirs.get(childPath);
      const isExpanded = !!childDs?.expanded;
      return (
        <div key={childPath}>
          <div
            data-id="file-explorer-remote-node"
            data-path={childPath}
            data-root={root.id}
            data-is-dir={e.is_dir ? 'true' : 'false'}
            draggable={!!onMove}
            onDragStart={onMove ? (ev) => {
              ev.stopPropagation();
              ev.dataTransfer.setData('text/plain', childPath);
              ev.dataTransfer.effectAllowed = 'move';
              setDragPath(childPath);
            } : undefined}
            onDragEnd={onMove ? () => { setDragPath(null); setDragOverDir(null); } : undefined}
            // Directories are drop targets (move INTO them); files are not.
            onDragOver={onMove && e.is_dir ? (ev) => {
              if (!dragPath || dragPath === childPath || childPath.startsWith(dragPath + '/')) return;
              ev.preventDefault();
              ev.stopPropagation();
              setDragOverDir(childPath);
            } : undefined}
            onDragLeave={onMove && e.is_dir ? (ev) => {
              if ((ev.relatedTarget as Node | null) && (ev.currentTarget as Node).contains(ev.relatedTarget as Node)) return;
              setDragOverDir((d) => (d === childPath ? null : d));
            } : undefined}
            onDrop={onMove && e.is_dir ? (ev) => { ev.preventDefault(); ev.stopPropagation(); void doMove(childPath); } : undefined}
            className={`flex items-center gap-1 px-2 py-0.5 hover:bg-zinc-800/60 cursor-pointer ${
              dragOverDir === childPath ? 'ring-1 ring-inset ring-sky-500/60' : ''
            }`}
            style={{ paddingLeft: 8 + level * 12 }}
            onContextMenu={
              onContextMenu
                ? (ev) => onContextMenu(ev, root, childPath, e.name, e.is_dir)
                : undefined
            }
            onClick={() => {
              if (e.is_dir) {
                setDirs((prev) => {
                  const next = new Map(prev);
                  const cur = prev.get(childPath) ?? emptyDirState();
                  next.set(childPath, { ...cur, expanded: !cur.expanded });
                  return next;
                });
                if (!childDs?.loaded && !childDs?.loading) loadDir(childPath);
              } else {
                onOpenFile(childPath, e);
              }
            }}
            title={childPath}
          >
            {e.is_dir ? (
              isExpanded
                ? <ChevronDown className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
                : <ChevronRight className="w-3.5 h-3.5 text-zinc-500 shrink-0" />
            ) : (
              <span className="w-3.5 shrink-0" />
            )}
            {e.is_dir ? (
              isExpanded
                ? <FolderOpen className="w-3.5 h-3.5 text-amber-400 shrink-0" />
                : <Folder className="w-3.5 h-3.5 text-amber-400 shrink-0" />
            ) : (
              <FileIcon className="w-3.5 h-3.5 text-zinc-400 shrink-0" />
            )}
            <span className="truncate text-zinc-200">{e.name}</span>
            {childDs?.loading && (
              <RefreshCw className="w-3 h-3 animate-spin text-zinc-500" />
            )}
          </div>
          {e.is_dir && isExpanded && renderDir(childPath, level + 1)}
        </div>
      );
    });
  };

  const rootDs = dirs.get('');
  return (
    <div data-id={`file-explorer-section-${root.id}`} className="border-t border-zinc-800 mt-1 pt-1">
      <CollapsibleHeader
        label={root.label.toUpperCase()}
        expanded={expanded}
        onToggle={handleToggle}
        dataId={`file-explorer-section-${root.id}-header`}
      />
      {expanded && (
        <div
          // Drop onto the section background (not a folder) = move to root.
          onDragOver={onMove ? (ev) => {
            if (!dragPath || fsParent(dragPath) === '') return;
            ev.preventDefault();
            setDragOverDir('');
          } : undefined}
          onDrop={onMove ? (ev) => { ev.preventDefault(); void doMove(''); } : undefined}
          className={dragOverDir === '' ? 'ring-1 ring-inset ring-sky-500/60' : ''}
        >
          {rootDs?.loading && !rootDs?.loaded && (
            <TreeSkeleton rows={4} dense />
          )}
          {rootDs?.error && (
            <div className="px-3 py-1.5 text-xs text-red-400">{rootDs.error}</div>
          )}
          {rootDs?.loaded && rootDs.entries.length === 0 && (
            <div className="px-3 py-1.5 text-xs text-zinc-500">{t('emptyDir')}</div>
          )}
          {rootDs?.loaded && renderDir('', 0)}
        </div>
      )}
    </div>
  );
}
