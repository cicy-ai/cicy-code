import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { X, GitCompare, FileText, Files, FileSearch, Search, PanelLeftOpen } from 'lucide-react';
import FileExplorer from './FileExplorer';
import CodeEditor from './CodeEditor';
import DiffView from './DiffView';
import QuickOpenPalette from './QuickOpenPalette';
import GlobalSearchPanel from './GlobalSearchPanel';
import { fsApi, FsEntry, FsFavorite, FsWatch, fsBasename, fsParent, joinFsPath, openFsWatch } from './api';
import { fsCacheInvalidatePath } from './fsCache';
import { CodeExtOps, installCodeExtBridge } from './codeExtBridge';
import { languageNameForPath } from './language';
import FileInfoModal from './FileInfoModal';
import { PromptModal } from './Modals';
import { useDialogs } from '../ui/Modal';
import i18n from '../../i18n';

// Shared with FileExplorer — all file-view copy lives under workspace.fileExplorer.*.
const tr = (k: string, o?: Record<string, unknown>) =>
  i18n.t(`fileExplorer.${k}`, { ns: 'workspace', ...o }) as string;

interface FilesViewProps {
  agentId: string;
  /** Absolute workspace folder, e.g. ~/cicy-ai/workers/w-1001 expanded. */
  workspaceFolder: string;
  /** Page-level chat-ws client id. Used to register a ":code-ext" alias so
   *  agent-editor's host.* RPCs reach this view over the shared WS. */
  pageClientId?: string;
  /** When set, the explorer + all CRUD are anchored to this fs root (e.g.
   *  "memory") instead of the workspace, and only that root's tree is shown. */
  scopeRoot?: string;
  className?: string;
}

type TabKind = 'file' | 'diff';

interface Tab {
  id: string;
  kind: TabKind;
  path: string;
  /** Filesystem root the path is anchored against. Default "workspace" keeps
   *  existing behavior; other ids (e.g. "projects", "skills", "home") come
   *  from the multi-root explorer sections and make the editor read-only. */
  root: string;
  base?: 'head' | 'index';
}

interface JumpRequest {
  line: number;
  col: number;
  nonce: number;
}

function makeTabId(kind: TabKind, root: string, path: string): string {
  return `${kind}:${root}:${path}`;
}

const IS_MAC = typeof navigator !== 'undefined'
  && /mac|darwin/i.test(`${navigator.platform || ''} ${navigator.userAgent || ''}`);
const MOD_KEY = IS_MAC ? '⌘' : 'Ctrl';
const KBD_CLS = 'inline-flex h-5 min-w-[20px] items-center justify-center rounded border border-white/[0.07] bg-white/[0.03] px-1.5 text-[10px] font-mono text-zinc-400 select-none';

interface PersistedTabs {
  tabs: Tab[];
  activeId: string;
}

const TAB_PERSIST_KEY_PREFIX = 'cicy.files.tabs.';
// v2: tab ids include the fs root id (multi-root explorer). v1 keys are
// silently discarded — the user just loses their pre-upgrade open-tab state.
const TAB_PERSIST_VERSION = 2;

// Tabs are namespaced by fs root: the Files panel (workspace), Knowledge panel
// (scopeRoot="knowledge") and any other scoped FilesView are the SAME component,
// so without this they'd share one key per agent and clobber each other's open
// tabs (knowledge files leaking into Files and back). "workspace" keeps the bare
// legacy key so existing Files tab state survives; scoped roots get a suffix.
function tabPersistKey(agentId: string, fsRoot: string = 'workspace'): string {
  const scope = fsRoot && fsRoot !== 'workspace' ? `${fsRoot}.` : '';
  return `${TAB_PERSIST_KEY_PREFIX}${scope}${agentId}`;
}

function loadPersistedTabs(agentId: string, fsRoot: string = 'workspace'): PersistedTabs {
  if (!agentId) return { tabs: [], activeId: '' };
  try {
    const raw = localStorage.getItem(tabPersistKey(agentId, fsRoot));
    if (!raw) return { tabs: [], activeId: '' };
    const parsed = JSON.parse(raw);
    if (parsed?.v !== TAB_PERSIST_VERSION || !Array.isArray(parsed?.tabs)) {
      return { tabs: [], activeId: '' };
    }
    const tabs: Tab[] = [];
    const seen = new Set<string>();
    for (const t of parsed.tabs) {
      const kind: TabKind = t?.kind === 'diff' ? 'diff' : 'file';
      const path = typeof t?.path === 'string' ? t.path : '';
      if (!path) continue;
      const root = typeof t?.root === 'string' && t.root ? t.root : 'workspace';
      const id = makeTabId(kind, root, path);
      if (seen.has(id)) continue;
      seen.add(id);
      tabs.push({ id, kind, root, path, base: kind === 'diff' ? (t?.base || 'head') : undefined });
    }
    const activeId = typeof parsed.activeId === 'string' && seen.has(parsed.activeId)
      ? parsed.activeId
      : (tabs[tabs.length - 1]?.id || '');
    return { tabs, activeId };
  } catch {
    return { tabs: [], activeId: '' };
  }
}

function savePersistedTabs(agentId: string, tabs: Tab[], activeId: string, fsRoot: string = 'workspace'): void {
  if (!agentId) return;
  try {
    const payload = JSON.stringify({
      v: TAB_PERSIST_VERSION,
      tabs: tabs.map((t) => ({ kind: t.kind, root: t.root, path: t.path, base: t.base })),
      activeId,
    });
    localStorage.setItem(tabPersistKey(agentId, fsRoot), payload);
  } catch {}
}

/**
 * Multi-tab native files view. Holds:
 *   - tab bar (file + diff tabs)
 *   - left explorer
 *   - active editor / diff
 *   - Cmd+P quick open + Cmd+Shift+F global search
 *   - per-file dirty state lifted from CodeEditor
 *   - fsnotify watcher for explorer refresh + external-change detection
 */
export default function FilesView({ agentId, workspaceFolder, pageClientId, scopeRoot, className }: FilesViewProps) {
  // Default fs root for this view's CRUD + tab open. "workspace" preserves the
  // Files-tab behavior; a scopeRoot (e.g. "memory") anchors everything there.
  const fsRoot = scopeRoot || 'workspace';
  // Hydrate from localStorage so reload preserves the open tab set + active.
  // Keyed by (fsRoot, agentId) so different agents — and the File / Knowledge
  // panels (different scopeRoot) — each keep independent, non-colliding tab sets.
  const initialPersist = useMemo(() => loadPersistedTabs(agentId, fsRoot), [agentId, fsRoot]);
  const [tabs, setTabs] = useState<Tab[]>(initialPersist.tabs);
  const [activeId, setActiveId] = useState<string>(initialPersist.activeId);

  // Re-hydrate when agentId (or fs root) changes.
  useEffect(() => {
    const restored = loadPersistedTabs(agentId, fsRoot);
    setTabs(restored.tabs);
    setActiveId(restored.activeId);
    // Don't depend on tabs/activeId — agent/root switch is the only trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentId, fsRoot]);

  // Persist tabs + activeId whenever they change.
  useEffect(() => {
    savePersistedTabs(agentId, tabs, activeId, fsRoot);
  }, [agentId, tabs, activeId, fsRoot]);
  const [dirty, setDirty] = useState<Record<string, boolean>>({});
  const [jumps, setJumps] = useState<Record<string, JumpRequest>>({});
  const [quickOpen, setQuickOpen] = useState(false);
  const [search, setSearch] = useState(false);
  const [explorerCollapsed, setExplorerCollapsed] = useState(false);
  const [reloadKeys, setReloadKeys] = useState<Record<string, number>>({});
  // Keep-alive cap: every open tab used to mount a live CodeMirror (hidden via
  // visibility:hidden, never unmounted), so N open files = N editors all
  // re-rendering = a pegged CPU core. Only keep the active tab + the few most
  // recently visited mounted; older tabs unmount their editor and re-create on
  // re-activation (cheap, and they measure correctly since they're visible then).
  const KEEP_ALIVE_TABS = 4;
  const [warmIds, setWarmIds] = useState<string[]>([]);
  useEffect(() => {
    if (!activeId) return;
    setWarmIds((prev) =>
      prev[0] === activeId
        ? prev
        : [activeId, ...prev.filter((id) => id !== activeId)].slice(0, KEEP_ALIVE_TABS),
    );
  }, [activeId]);
  // Tabs whose editor stays mounted: active + recently visited + any with
  // unsaved edits (never unmount a dirty buffer — it'd drop the user's changes).
  const mountedTabIds = useMemo(() => {
    const s = new Set<string>(warmIds);
    if (activeId) s.add(activeId);
    for (const id of Object.keys(dirty)) if (dirty[id]) s.add(id);
    return s;
  }, [warmIds, activeId, dirty]);
  const [explorerNonce, setExplorerNonce] = useState<Record<string, number>>({});
  // Refreshes the extra-root sections (projects/skills/home), which have no
  // fsnotify watcher: bumped on panel open / agent switch (below) and after any
  // mutation so their listings re-loop to fresh state.
  const [remoteReloadNonce, setRemoteReloadNonce] = useState(0);
  const bumpRemoteReload = useCallback(() => setRemoteReloadNonce((n) => n + 1), []);
  const watchRef = useRef<FsWatch | null>(null);
  const subscribedRef = useRef<Set<string>>(new Set());
  const cursorRef = useRef<Record<string, { line: number; col: number }>>({});
  const [favorites, setFavorites] = useState<FsFavorite[]>([]);
  // Carry the fs root so file-info resolves against the right base in scoped
  // (Knowledge/Memory) and extra-root views, not always the workspace.
  const [infoPath, setInfoPath] = useState<{ path: string; root: string } | null>(null);
  const [renameTarget, setRenameTarget] = useState<{ path: string; isDir: boolean; root: string } | null>(null);
  const [createTarget, setCreateTarget] = useState<{ parentDir: string; kind: 'file' | 'dir'; root: string } | null>(null);
  const { confirm, node: dialogsNode } = useDialogs();

  // On panel open / agent switch, loop the extra-root sections back to fresh —
  // they're lazy and watcher-less, so otherwise an expanded section keeps stale
  // file/folder state from a previous mount.
  useEffect(() => {
    bumpRemoteReload();
  }, [agentId, bumpRemoteReload]);

  // --- watcher lifecycle --------------------------------------------------

  useEffect(() => {
    if (!agentId) return;
    const w = openFsWatch(agentId);
    watchRef.current = w;
    subscribedRef.current = new Set();
    // Subscribe to workspace root so we at least see top-level changes.
    w.subscribe('');
    subscribedRef.current.add('');

    const off = w.onEvent((ev) => {
      if (ev.type === 'pong' || ev.type === 'error') return;
      const evPath = (ev as { path: string }).path || '';
      const parent = fsParent(evPath);
      // Bust cache entries that the event invalidates so revalidations pick
      // up fresh data on next access.
      fsCacheInvalidatePath(agentId, evPath);
      // Refresh the explorer's view of the affected dir.
      setExplorerNonce((prev) => ({ ...prev, [parent]: (prev[parent] || 0) + 1 }));
      // If the affected file matches any open tab, bump its reloadKey so the
      // editor can pick up the change (or warn if dirty).
      setReloadKeys((prev) => {
        const next = { ...prev };
        let changed = false;
        for (const t of tabs) {
          if (t.kind === 'file' && t.path === evPath) {
            next[t.id] = (next[t.id] || 0) + 1;
            changed = true;
          }
        }
        return changed ? next : prev;
      });
    });
    return () => {
      off();
      w.close();
      watchRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentId]);

  const subscribeDir = useCallback((dir: string) => {
    if (!watchRef.current) return;
    if (subscribedRef.current.has(dir)) return;
    subscribedRef.current.add(dir);
    watchRef.current.subscribe(dir);
  }, []);

  // Watch every open file's parent dir — not just dirs expanded in the
  // explorer. A file opened via quick-open / the cicy:open-file event lives in
  // a folder that may never be expanded, so without this its external edits
  // wouldn't bump reloadKey and the clean-adopt / dirty-banner reconciliation
  // in CodeEditor would never fire. Subscription is idempotent.
  useEffect(() => {
    for (const t of tabs) {
      if (t.kind === 'file') subscribeDir(fsParent(t.path));
    }
  }, [tabs, subscribeDir]);

  // --- tab helpers --------------------------------------------------------

  const openFileTab = useCallback(
    (path: string, root: string = fsRoot, jump?: { line: number; col?: number }) => {
      const id = makeTabId('file', root, path);
      setTabs((prev) => (prev.some((t) => t.id === id) ? prev : [...prev, { id, kind: 'file', root, path }]));
      setActiveId(id);
      if (jump) {
        setJumps((prev) => ({
          ...prev,
          [id]: { line: jump.line, col: jump.col || 1, nonce: Date.now() },
        }));
      }
    },
    [],
  );

  // markdown history 里点击文件链接 → 打开到编辑器 tab(Workspace 负责揭示文件视图)。
  useEffect(() => {
    const handler = (e: Event) => {
      let p = String((e as CustomEvent).detail?.path || '').trim();
      if (!p) return;
      if (p.startsWith('file://')) p = p.slice(7);
      const m = p.match(/^(.*?):(\d+)(?::(\d+))?$/);
      if (m) openFileTab(m[1], fsRoot, { line: Number(m[2]), col: m[3] ? Number(m[3]) : 1 });
      else openFileTab(p);
    };
    window.addEventListener('cicy:open-file', handler as EventListener);
    return () => window.removeEventListener('cicy:open-file', handler as EventListener);
  }, [openFileTab, fsRoot]);

  // Diff tabs are workspace-only — diff goes through git, which is scoped to
  // the workspace. Non-workspace files just don't get a "diff" affordance.
  const openDiffTab = useCallback((path: string) => {
    const id = makeTabId('diff', 'workspace', path);
    setTabs((prev) => (prev.some((t) => t.id === id) ? prev : [...prev, { id, kind: 'diff', root: 'workspace', path, base: 'head' }]));
    setActiveId(id);
  }, []);

  const closeTab = useCallback(
    (id: string) => {
      setTabs((prev) => {
        const idx = prev.findIndex((t) => t.id === id);
        if (idx < 0) return prev;
        const next = prev.slice(0, idx).concat(prev.slice(idx + 1));
        if (activeId === id) {
          const fallback = next[idx] || next[idx - 1];
          setActiveId(fallback ? fallback.id : '');
        }
        return next;
      });
      setDirty((prev) => {
        const { [id]: _gone, ...rest } = prev;
        return rest;
      });
      setJumps((prev) => {
        const { [id]: _gone, ...rest } = prev;
        return rest;
      });
      setReloadKeys((prev) => {
        const { [id]: _gone, ...rest } = prev;
        return rest;
      });
    },
    [activeId],
  );

  // Bulk close helpers — keep state shape consistent with closeTab by routing
  // through the same per-tab id deletes, only computed in one shot up front.
  const closeManyTabs = useCallback((idsToClose: string[], keepActiveId?: string) => {
    if (idsToClose.length === 0) return;
    const closeSet = new Set(idsToClose);
    setTabs((prev) => prev.filter((t) => !closeSet.has(t.id)));
    setActiveId((prev) => {
      if (keepActiveId !== undefined) return keepActiveId;
      // Otherwise pick the first surviving tab (or empty if none).
      return prev && !closeSet.has(prev) ? prev : '';
    });
    const drop = (m: Record<string, unknown>) => {
      const out: Record<string, unknown> = {};
      for (const k of Object.keys(m)) if (!closeSet.has(k)) out[k] = m[k];
      return out;
    };
    setDirty((prev) => drop(prev) as Record<string, boolean>);
    setJumps((prev) => drop(prev) as Record<string, JumpRequest>);
    setReloadKeys((prev) => drop(prev) as Record<string, number>);
  }, []);

  const closeOtherTabs = useCallback(
    (keepId: string) => {
      const ids = tabs.filter((t) => t.id !== keepId).map((t) => t.id);
      closeManyTabs(ids, keepId);
    },
    [tabs, closeManyTabs],
  );

  const closeTabsToRight = useCallback(
    (anchorId: string) => {
      const idx = tabs.findIndex((t) => t.id === anchorId);
      if (idx < 0) return;
      const ids = tabs.slice(idx + 1).map((t) => t.id);
      // Active tab is among them? Then fall back to the anchor.
      const keep = ids.includes(activeId) ? anchorId : undefined;
      closeManyTabs(ids, keep);
    },
    [tabs, activeId, closeManyTabs],
  );

  const closeAllTabs = useCallback(() => {
    closeManyTabs(tabs.map((t) => t.id), '');
  }, [tabs, closeManyTabs]);

  const handleOpenFromExplorer = useCallback(
    (path: string, _entry: FsEntry, root: string = 'workspace') => {
      openFileTab(path, root);
    },
    [openFileTab],
  );

  // --- favorites ----------------------------------------------------------
  useEffect(() => {
    if (!agentId) return;
    let cancelled = false;
    fsApi.favoritesList(agentId).then((r) => {
      if (!cancelled) setFavorites(r.items || []);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [agentId]);

  const handleAddFavorite = useCallback(
    async (path: string) => {
      try {
        const r = await fsApi.favoritesAdd(agentId, path);
        setFavorites(r.items || []);
      } catch {}
    },
    [agentId],
  );

  const handleRemoveFavorite = useCallback(
    async (path: string) => {
      try {
        const r = await fsApi.favoritesRemove(agentId, path);
        setFavorites(r.items || []);
      } catch {}
    },
    [agentId],
  );

  // --- rename / delete / mkdir / touch -----------------------------------

  const rebumpDir = useCallback((dir: string) => {
    setExplorerNonce((prev) => ({ ...prev, [dir]: (prev[dir] || 0) + 1 }));
  }, []);

  const handleRenameSubmit = useCallback(
    async (newName: string) => {
      if (!renameTarget) return;
      const root = renameTarget.root;
      const oldPath = renameTarget.path;
      const parent = fsParent(oldPath);
      const newPath = joinFsPath(parent, newName);
      if (newPath === oldPath) {
        setRenameTarget(null);
        return;
      }
      await fsApi.rename(agentId, oldPath, newPath, { root });
      fsCacheInvalidatePath(agentId, oldPath);
      fsCacheInvalidatePath(agentId, newPath);
      rebumpDir(parent);
      bumpRemoteReload(); // extra-root sections have no watcher → refresh explicitly
      // Sync any open tab in the SAME root whose path was renamed (the file
      // itself or a parent dir).
      setTabs((prev) =>
        prev.map((t) => {
          if (t.root !== root) return t;
          if (t.path === oldPath) return { ...t, path: newPath, id: makeTabId(t.kind, t.root, newPath) };
          if (t.path.startsWith(oldPath + '/')) {
            const replaced = newPath + t.path.slice(oldPath.length);
            return { ...t, path: replaced, id: makeTabId(t.kind, t.root, replaced) };
          }
          return t;
        }),
      );
      setActiveId((prev) => {
        if (!prev) return prev;
        const oldId = prev;
        const oldTab = tabs.find((t) => t.id === oldId);
        if (!oldTab || oldTab.root !== root) return prev;
        if (oldTab.path === oldPath) return makeTabId(oldTab.kind, oldTab.root, newPath);
        if (oldTab.path.startsWith(oldPath + '/')) {
          return makeTabId(oldTab.kind, oldTab.root, newPath + oldTab.path.slice(oldPath.length));
        }
        return prev;
      });
      setRenameTarget(null);
    },
    [agentId, renameTarget, tabs, rebumpDir, bumpRemoteReload],
  );

  // Drag-and-drop move: relocate each source into destDir via fsApi.rename
  // (from → destDir/basename). Workspace-only, same as rename. Refreshes the
  // source parents + destDir and rewrites any open tab whose path moved.
  const handleMove = useCallback(
    async (sources: string[], destDir: string, root: string = fsRoot) => {
      const pairs: Array<{ from: string; to: string }> = [];
      for (const from of sources) {
        const to = joinFsPath(destDir, fsBasename(from));
        if (to === from) continue;                       // same place
        if (fsParent(from) === destDir) continue;        // already here
        if (destDir === from || destDir.startsWith(from + '/')) continue; // into self/descendant
        pairs.push({ from, to });
      }
      if (pairs.length === 0) return;
      const affectedDirs = new Set<string>();
      const moved: Array<{ from: string; to: string }> = [];
      // Sequential so one failure (e.g. destination_exists) doesn't poison the rest.
      for (const { from, to } of pairs) {
        try {
          await fsApi.rename(agentId, from, to, { root });
          fsCacheInvalidatePath(agentId, from);
          fsCacheInvalidatePath(agentId, to);
          affectedDirs.add(fsParent(from));
          affectedDirs.add(destDir);
          moved.push({ from, to });
        } catch (e) {
          // eslint-disable-next-line no-console
          console.warn('move failed', from, '→', to, e);
        }
      }
      affectedDirs.forEach((d) => rebumpDir(d));
      bumpRemoteReload();
      if (moved.length === 0) return;
      // Rewrite open tabs / active id whose path (or ancestor) just moved.
      const remap = (p: string): string | null => {
        for (const { from, to } of moved) {
          if (p === from) return to;
          if (p.startsWith(from + '/')) return to + p.slice(from.length);
        }
        return null;
      };
      setTabs((prev) =>
        prev.map((t) => {
          if (t.root !== root) return t;
          const np = remap(t.path);
          return np ? { ...t, path: np, id: makeTabId(t.kind, t.root, np) } : t;
        }),
      );
      setActiveId((prev) => {
        if (!prev) return prev;
        const oldTab = tabs.find((t) => t.id === prev);
        if (!oldTab || oldTab.root !== root) return prev;
        const np = remap(oldTab.path);
        return np ? makeTabId(oldTab.kind, oldTab.root, np) : prev;
      });
    },
    [agentId, fsRoot, rebumpDir, tabs, bumpRemoteReload],
  );

  // Confirm-then-delete. Renders the standard modal confirm with a path
  // summary; on OK runs the same sequential delete + tab/dirty cleanup that
  // the old <ConfirmModal> wrapper used to drive via state.
  const handleDeleteRequest = useCallback(
    async (paths: string[], isDir: boolean, root: string = fsRoot) => {
      if (paths.length === 0) return;
      const body = (
        <div className="space-y-1">
          {paths.length > 1 ? (
            <>
              <div>{tr('deleteListIntro', { count: paths.length })}</div>
              <ul className="max-h-40 overflow-auto font-mono text-[11px] text-zinc-100 bg-zinc-950 border border-zinc-800 rounded p-2 space-y-0.5">
                {paths.map((p) => (
                  <li key={p} className="truncate">{p}</li>
                ))}
              </ul>
            </>
          ) : (
            <div>{tr('deleteOnePrefix')} <span className="font-mono text-zinc-100">{paths[0]}</span></div>
          )}
          {isDir && (
            <div className="text-amber-400">{tr('deleteDirWarning')}</div>
          )}
        </div>
      );
      const title = paths.length > 1
        ? tr('deleteTitleN', { count: paths.length })
        : (isDir ? tr('deleteTitleDir') : tr('deleteTitleFile'));
      const ok = await confirm({ title, body, confirmLabel: tr('delete'), danger: true });
      if (!ok) return;
      const parentDirs = new Set<string>();
      // Sequential delete so errors on one don't poison the rest.
      for (const p of paths) {
        try {
          await fsApi.delete(agentId, p, isDir, { root });
          fsCacheInvalidatePath(agentId, p);
          parentDirs.add(fsParent(p));
        } catch (e) {
          // eslint-disable-next-line no-console
          console.warn('delete failed', p, e);
        }
      }
      parentDirs.forEach((d) => rebumpDir(d));
      bumpRemoteReload();
      setTabs((prev) =>
        prev.filter((t) => {
          if (t.root !== root) return true; // only close tabs in the affected root
          for (const p of paths) {
            if (t.path === p || t.path.startsWith(p + '/')) return false;
          }
          return true;
        }),
      );
      setDirty((prev) => {
        const next = { ...prev };
        for (const k of Object.keys(next)) {
          for (const p of paths) {
            if (k.endsWith(`:${p}`) || k.includes(`:${p}/`)) {
              delete next[k];
              break;
            }
          }
        }
        return next;
      });
    },
    [agentId, confirm, rebumpDir, fsRoot, bumpRemoteReload],
  );

  const handleUpload = useCallback(
    async (parentDir: string, files: FileList, root: string = fsRoot) => {
      const arr = Array.from(files);
      for (const f of arr) {
        const target = joinFsPath(parentDir, f.name);
        try {
          await fsApi.upload(agentId, target, f, { root });
          fsCacheInvalidatePath(agentId, target);
        } catch (e) {
          // Surface as a toast-style alert; could be wired to a snackbar later.
          // eslint-disable-next-line no-console
          console.warn('upload failed', target, e);
        }
      }
      rebumpDir(parentDir);
      bumpRemoteReload();
    },
    [agentId, rebumpDir, fsRoot, bumpRemoteReload],
  );

  const handleDownload = useCallback((path: string, root: string = fsRoot) => {
    fsApi.download(agentId, path, root);
  }, [agentId, fsRoot]);

  const handleCreateSubmit = useCallback(
    async (name: string) => {
      if (!createTarget) return;
      const root = createTarget.root;
      const newPath = joinFsPath(createTarget.parentDir, name);
      if (createTarget.kind === 'file') {
        await fsApi.touch(agentId, newPath, { root });
      } else {
        await fsApi.mkdir(agentId, newPath, { root });
      }
      fsCacheInvalidatePath(agentId, newPath);
      rebumpDir(createTarget.parentDir);
      bumpRemoteReload();
      if (createTarget.kind === 'file') {
        openFileTab(newPath, root);
      }
      setCreateTarget(null);
    },
    [agentId, createTarget, rebumpDir, openFileTab, bumpRemoteReload],
  );

  // --- global hotkeys -----------------------------------------------------

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      if (!mod) return;
      if (e.key === 'p' || e.key === 'P') {
        if (!e.shiftKey) {
          e.preventDefault();
          setQuickOpen(true);
        }
      } else if ((e.key === 'f' || e.key === 'F') && e.shiftKey) {
        e.preventDefault();
        setSearch(true);
      } else if ((e.key === 'w' || e.key === 'W') && activeId) {
        e.preventDefault();
        closeTab(activeId);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [activeId, closeTab]);

  // --- derived ------------------------------------------------------------

  const activeTab = useMemo(() => tabs.find((t) => t.id === activeId), [tabs, activeId]);
  const activePath = activeTab?.kind === 'file' ? activeTab.path : '';

  // --- :code-ext bridge for agent-editor compatibility ---------------
  // Re-uses the existing chat-ws; the server aliases <pageClientId>:code-ext
  // to this client on register, so agent-editor's host.open_file /
  // host.active_file / host.list_tabs / host.ping land here instead of the
  // old VSIX extension.
  const opsRef = useRef<CodeExtOps | null>(null);
  opsRef.current = {
    openFile: async (path, line, col) => {
      openFileTab(path, fsRoot, line ? { line, col } : undefined);
    },
    getActiveFile: () => {
      const cur = activeId ? cursorRef.current[activeId] : undefined;
      return {
        path: activePath,
        language: activePath ? languageNameForPath(activePath) : '',
        line: cur?.line ?? 0,
        column: cur?.col ?? 0,
      };
    },
    listTabs: () =>
      tabs.map((t) => ({
        path: t.path,
        label: fsBasename(t.path),
        isActive: t.id === activeId,
        isDirty: !!dirty[t.id],
      })),
  };

  useEffect(() => {
    // Only the main Files tab owns the :code-ext editor bridge. Scoped views
    // (Knowledge/Memory) still receive pageClientId so send-to-agent can route,
    // but must NOT register a second bridge under the same id (it would collide).
    if (!pageClientId || scopeRoot) return;
    const stop = installCodeExtBridge(pageClientId, opsRef);
    return stop;
  }, [pageClientId, scopeRoot]);

  // --- render -------------------------------------------------------------

  return (
    <div data-id="files-view" className={`flex h-full ${className || ''} relative`}>
      <div data-id="files-view-explorer" className={`${explorerCollapsed ? 'hidden' : 'w-64'} shrink-0 border-r border-zinc-800 bg-[#0A0A0A]`}>
        <FileExplorer
          onCollapse={() => setExplorerCollapsed(true)}
          agentId={agentId}
          workspaceFolder={workspaceFolder}
          scopeRoot={scopeRoot}
          activePath={activePath}
          onOpenFile={handleOpenFromExplorer}
          onDirLoaded={subscribeDir}
          dirRefreshNonce={explorerNonce}
          remoteReloadNonce={remoteReloadNonce}
          pageClientId={pageClientId}
          onShowFileInfo={(path, root) => setInfoPath({ path, root: root || fsRoot })}
          onAddFavorite={handleAddFavorite}
          favorites={favorites}
          onRemoveFavorite={handleRemoveFavorite}
          onRename={(path, isDir, root = fsRoot) => setRenameTarget({ path, isDir, root })}
          onDelete={(paths, isDir, root = fsRoot) => handleDeleteRequest(paths, isDir, root)}
          onNewFile={(parentDir, root = fsRoot) => setCreateTarget({ parentDir, kind: 'file', root })}
          onNewFolder={(parentDir, root = fsRoot) => setCreateTarget({ parentDir, kind: 'dir', root })}
          onMove={handleMove}
          onUpload={handleUpload}
          onDownload={handleDownload}
        />
      </div>

      <GlobalSearchPanel
        agentId={agentId}
        open={search}
        onClose={() => setSearch(false)}
        onPick={(path, line, col) => {
          openFileTab(path, fsRoot, { line, col });
          setSearch(false);
        }}
      />

      <div data-id="files-view-main" className="flex-1 min-w-0 flex flex-col">
        <div
          data-id="files-view-topbar"
          // When there are tabs, TabBar provides the h-9 strip + border and the
          // expand button stretches to match. With no tabs TabBar renders
          // nothing, so the row would collapse to a tiny floating button — give
          // the row its own h-9 toolbar strip (only when the button is shown,
          // i.e. explorer collapsed) so the button sits in a proper aligned bar.
          className={`flex items-stretch min-w-0 ${
            explorerCollapsed && tabs.length === 0
              ? 'h-9 border-b border-zinc-800 bg-zinc-900'
              : ''
          }`}
        >
          {explorerCollapsed && (
            <button
              data-id="files-view-explorer-expand"
              className="shrink-0 flex items-center justify-center px-2 border-r border-zinc-800 bg-zinc-900 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200"
              onClick={() => setExplorerCollapsed(false)}
              title={tr('showFileTree')}
            >
              <PanelLeftOpen className="w-4 h-4" />
            </button>
          )}
          <div className="flex-1 min-w-0">
            <TabBar
              tabs={tabs}
              activeId={activeId}
              dirty={dirty}
              onSelect={setActiveId}
              onClose={closeTab}
              onCloseOthers={closeOtherTabs}
              onCloseRight={closeTabsToRight}
              onCloseAll={closeAllTabs}
            />
          </div>
        </div>
        <div data-id="files-view-body" className="flex-1 min-h-0 relative bg-[#0A0A0A]">
          {tabs.length === 0 ? (
            <div data-id="files-view-empty" className="flex items-center justify-center h-full">
              <div className="flex w-full max-w-[280px] flex-col items-center px-6 text-center">
                <div className="flex h-16 w-16 items-center justify-center rounded-2xl border border-white/[0.06] bg-white/[0.02] text-zinc-600">
                  <Files size={28} strokeWidth={1.5} />
                </div>
                <div className="mt-4 text-sm font-medium text-zinc-300">{tr('noOpenFile')}</div>
                <div className="mt-1.5 text-xs leading-relaxed text-zinc-500">
                  {tr('noOpenFileHint')}
                </div>
                <div className="mt-5 w-full space-y-1.5">
                  <div className="flex items-center justify-between gap-3 rounded-lg border border-white/[0.05] bg-white/[0.015] px-3 py-2">
                    <span className="flex items-center gap-2 text-xs text-zinc-400">
                      <FileSearch size={14} strokeWidth={1.75} className="shrink-0 text-zinc-500" />
                      {tr('quickOpenByName')}
                    </span>
                    <span className="flex shrink-0 items-center gap-1">
                      <kbd className={KBD_CLS}>{MOD_KEY}</kbd>
                      <kbd className={KBD_CLS}>P</kbd>
                    </span>
                  </div>
                  <div className="flex items-center justify-between gap-3 rounded-lg border border-white/[0.05] bg-white/[0.015] px-3 py-2">
                    <span className="flex items-center gap-2 text-xs text-zinc-400">
                      <Search size={14} strokeWidth={1.75} className="shrink-0 text-zinc-500" />
                      {tr('fullTextSearch')}
                    </span>
                    <span className="flex shrink-0 items-center gap-1">
                      <kbd className={KBD_CLS}>{MOD_KEY}</kbd>
                      <kbd className={KBD_CLS}>Shift</kbd>
                      <kbd className={KBD_CLS}>F</kbd>
                    </span>
                  </div>
                </div>
              </div>
            </div>
          ) : (
            tabs.map((t) => {
              const isActive = t.id === activeId;
              return (
              <div
                key={t.id}
                data-id="files-view-tab-host"
                data-tab-id={t.id}
                data-tab-active={isActive ? 'true' : 'false'}
                // CodeMirror inside a display:none parent fails to compute
                // viewport size on init; when we flip it back to visible the
                // layout is wrong (squished / blank). So the active host stays
                // laid out and inactives are hidden via visibility:hidden +
                // offscreen (not display:none). To avoid N live editors pegging
                // the CPU, only the kept-alive tabs (active + recent + dirty)
                // actually mount an editor; the rest render an empty host and
                // re-mount on activation (they're visible then, so CM measures).
                style={
                  isActive
                    ? { position: 'absolute', inset: 0 }
                    : {
                        position: 'absolute',
                        inset: 0,
                        visibility: 'hidden',
                        pointerEvents: 'none',
                        zIndex: -1,
                      }
                }
              >
                {!mountedTabIds.has(t.id) ? null : t.kind === 'file' ? (
                  <CodeEditor
                    agentId={agentId}
                    path={t.path}
                    root={t.root}
                    reloadKey={reloadKeys[t.id]}
                    jump={jumps[t.id]}
                    pageClientId={pageClientId}
                    active={t.id === activeId}
                    onDirtyChange={(d) => setDirty((prev) => ({ ...prev, [t.id]: d }))}
                    onCursorChange={(c) => {
                      cursorRef.current[t.id] = { line: c.line, col: c.col };
                    }}
                    onShowDiff={t.root === 'workspace' ? () => openDiffTab(t.path) : undefined}
                  />
                ) : (
                  <DiffView
                    agentId={agentId}
                    path={t.path}
                    base={t.base || 'head'}
                    active={t.id === activeId}
                    onClose={() => closeTab(t.id)}
                  />
                )}
              </div>
              );
            })
          )}
        </div>
      </div>

      <QuickOpenPalette
        agentId={agentId}
        open={quickOpen}
        onClose={() => setQuickOpen(false)}
        onPick={(p) => openFileTab(p)}
      />

      {infoPath && (
        <FileInfoModal
          agentId={agentId}
          path={infoPath.path}
          root={infoPath.root}
          onClose={() => setInfoPath(null)}
        />
      )}

      {renameTarget && (
        <PromptModal
          title={renameTarget.isDir ? tr('renameTitleDir') : tr('renameTitleFile')}
          initialValue={fsBasename(renameTarget.path)}
          okLabel={tr('rename')}
          description={<span className="font-mono text-zinc-500">{renameTarget.path}</span>}
          onCancel={() => setRenameTarget(null)}
          onSubmit={handleRenameSubmit}
        />
      )}

      {dialogsNode}

      {createTarget && (
        <PromptModal
          title={createTarget.kind === 'file' ? tr('newFile') : tr('newFolder')}
          placeholder={createTarget.kind === 'file' ? 'new-file.ts' : 'new-folder'}
          okLabel={tr('create')}
          description={
            <span className="font-mono text-zinc-500">
              {tr('createUnder', { dir: createTarget.parentDir || '/' })}
            </span>
          }
          onCancel={() => setCreateTarget(null)}
          onSubmit={handleCreateSubmit}
        />
      )}
    </div>
  );
}

// --- tab bar --------------------------------------------------------------

interface TabBarProps {
  tabs: Tab[];
  activeId: string;
  dirty: Record<string, boolean>;
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
  /** Close every tab except the one with this id. */
  onCloseOthers: (id: string) => void;
  /** Close every tab to the right of the one with this id. */
  onCloseRight: (id: string) => void;
  /** Close every tab. */
  onCloseAll: () => void;
}

interface TabMenuState {
  x: number;
  y: number;
  id: string;
}

function TabBar({ tabs, activeId, dirty, onSelect, onClose, onCloseOthers, onCloseRight, onCloseAll }: TabBarProps) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const activeBtnRef = useRef<HTMLDivElement | null>(null);
  const [menu, setMenu] = useState<TabMenuState | null>(null);

  // Close the context menu on outside click / Escape.
  useEffect(() => {
    if (!menu) return;
    const onPointer = () => setMenu(null);
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setMenu(null); };
    document.addEventListener('pointerdown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [menu]);

  // Translate vertical wheel to horizontal scroll on the tab strip so the
  // user can use a normal mouse wheel without holding shift.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
      el.scrollLeft += e.deltaY;
      e.preventDefault();
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, []);

  // Keep the active tab in view when it changes (e.g. opening from Cmd+P).
  useEffect(() => {
    activeBtnRef.current?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }, [activeId]);

  if (tabs.length === 0) return null;
  return (
    <div
      data-id="files-tab-bar"
      ref={scrollRef}
      // VS Code-style strip: scroll via wheel or trackpad swipe, no visible
      // scrollbar. The Firefox + WebKit selectors below hide both renderers.
      className="flex items-stretch overflow-x-auto overflow-y-hidden border-b border-zinc-800 bg-zinc-900 text-xs h-9 [&::-webkit-scrollbar]:hidden"
      style={{ scrollbarWidth: 'none' }}
    >
      {tabs.map((t) => {
        const active = t.id === activeId;
        return (
          <div
            key={t.id}
            ref={active ? activeBtnRef : undefined}
            data-id="files-tab"
            data-tab-id={t.id}
            data-tab-kind={t.kind}
            data-tab-active={active ? 'true' : 'false'}
            data-tab-dirty={dirty[t.id] ? 'true' : 'false'}
            onClick={() => onSelect(t.id)}
            onMouseDown={(e) => {
              if (e.button === 1) {
                e.preventDefault();
                onClose(t.id);
              }
            }}
            onContextMenu={(e) => {
              e.preventDefault();
              setMenu({ x: e.clientX, y: e.clientY, id: t.id });
            }}
            className={`group flex items-center gap-1.5 pl-3 pr-1 py-1.5 border-r border-zinc-800 cursor-pointer shrink-0 min-w-[120px] max-w-[200px] ${
              active ? 'bg-[#0A0A0A] text-zinc-100' : 'text-zinc-400 hover:bg-zinc-800/60'
            }`}
            title={t.path}
          >
            {t.kind === 'diff' ? (
              <GitCompare className="w-3 h-3 text-amber-300 shrink-0" />
            ) : (
              <FileText className="w-3 h-3 text-zinc-500 shrink-0" />
            )}
            <span data-id="files-tab-label" className="truncate flex-1 min-w-0">{fsBasename(t.path)}</span>
            {dirty[t.id] && <span data-id="files-tab-dirty" className="shrink-0 text-amber-400">●</span>}
            <button
              data-id="files-tab-close"
              onClick={(e) => {
                e.stopPropagation();
                onClose(t.id);
              }}
              className="shrink-0 opacity-50 group-hover:opacity-100 p-0.5 rounded hover:bg-zinc-700"
              title={tr('closeTabTitle')}
            >
              <X className="w-3 h-3" />
            </button>
          </div>
        );
      })}
      {menu && (
        <TabContextMenu
          x={menu.x}
          y={menu.y}
          id={menu.id}
          tabs={tabs}
          onClose={() => setMenu(null)}
          onCloseTab={onClose}
          onCloseOthers={onCloseOthers}
          onCloseRight={onCloseRight}
          onCloseAll={onCloseAll}
        />
      )}
    </div>
  );
}

interface TabContextMenuProps {
  x: number;
  y: number;
  /** Id of the tab the user right-clicked. */
  id: string;
  tabs: Tab[];
  onClose: () => void;
  onCloseTab: (id: string) => void;
  onCloseOthers: (id: string) => void;
  onCloseRight: (id: string) => void;
  onCloseAll: () => void;
}

// VS Code-style tab context menu: Close / Close Others / Close to the Right
// / Close All. Built on the same viewport-clamped positioning pattern as the
// editor and file-explorer menus so it never spills off-screen.
function TabContextMenu({ x, y, id, tabs, onClose, onCloseTab, onCloseOthers, onCloseRight, onCloseAll }: TabContextMenuProps) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [pos, setPos] = useState({ x, y });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const maxX = window.innerWidth - r.width - 4;
    const maxY = window.innerHeight - r.height - 4;
    setPos({
      x: Math.max(4, Math.min(x, maxX)),
      y: Math.max(4, Math.min(y, maxY)),
    });
  }, [x, y]);
  // Disable Close-to-Right if this tab is the rightmost one already.
  const idx = tabs.findIndex((t) => t.id === id);
  const hasRight = idx >= 0 && idx < tabs.length - 1;
  const hasOthers = tabs.length > 1;
  return (
    <div
      ref={ref}
      data-id="files-tab-context-menu"
      className="fixed z-[2147483647] min-w-[180px] py-1 rounded-md border border-zinc-700 bg-zinc-900 shadow-xl text-xs text-zinc-200"
      style={{ left: pos.x, top: pos.y }}
      // pointerdown bubbles to document BEFORE click fires; without
      // stopPropagation the document-level "close on outside pointerdown"
      // listener unmounts the menu before any item's onClick runs.
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
    >
      <TabMenuItem
        label={tr('closeTab')}
        onClick={() => { onCloseTab(id); onClose(); }}
      />
      <TabMenuItem
        label={tr('closeOthers')}
        disabled={!hasOthers}
        onClick={() => { onCloseOthers(id); onClose(); }}
      />
      <TabMenuItem
        label={tr('closeRight')}
        disabled={!hasRight}
        onClick={() => { onCloseRight(id); onClose(); }}
      />
      <div className="my-1 border-t border-zinc-800" />
      <TabMenuItem
        label={tr('closeAll')}
        onClick={() => { onCloseAll(); onClose(); }}
      />
    </div>
  );
}

function TabMenuItem({ label, onClick, disabled }: { label: string; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      data-id={`files-tab-menu-${label}`}
      disabled={disabled}
      className="w-full text-left px-3 py-1.5 hover:bg-zinc-800 disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:bg-transparent"
      onClick={onClick}
    >
      {label}
    </button>
  );
}
