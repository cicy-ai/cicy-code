import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import CodeMirror, { ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { keymap, EditorView } from '@codemirror/view';
import { oneDark } from '@codemirror/theme-one-dark';

// Make CodeMirror blend into our zinc-950 chrome — drop the theme-one-dark
// default background and gutter color so the editor area picks up whatever
// bg the parent uses. Keeps syntax colors from oneDark intact.
const cmBlendTheme = EditorView.theme({
  // oneDark paints &/.cm-gutters with #282c34/#21252b. Those rules have the
  // same specificity as ours and CodeMirror's StyleModule source order is not
  // guaranteed, so plain overrides lose intermittently and the editor shows a
  // gray panel against the #0A0A0A chrome. !important forces the blend to win.
  '&': { backgroundColor: 'transparent !important', color: '#e4e4e7' },
  '.cm-gutters': {
    backgroundColor: 'transparent !important',
    borderRight: '1px solid rgba(255,255,255,0.04)',
    color: 'rgba(228,228,231,0.35)',
  },
  '.cm-activeLineGutter, .cm-activeLine': {
    backgroundColor: 'rgba(255,255,255,0.03)',
  },
  '.cm-selectionBackground, ::selection': {
    backgroundColor: 'rgba(59,130,246,0.25) !important',
  },
  '.cm-cursor': { borderLeftColor: '#e4e4e7' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
});
import { Save, Send, RefreshCw, AlertTriangle, GitCompare, Download as DownloadIcon } from 'lucide-react';
import { Download, Eye, FileCode } from 'lucide-react';
import { fsApi, fsBasename, FsError, FsReadResult, FsStatResponse, friendlyFsError } from './api';
import { languageForPath } from './language';
import MarkdownPreview from './MarkdownPreview';

function isMarkdownPath(path: string): boolean {
  return /\.(md|markdown|mdx)$/i.test(path);
}
import {
  fsCachePeek,
  fsCacheSet,
  fsCacheSubscribe,
  fsCacheInvalidatePath,
  fsKey,
} from './fsCache';

// Display-mode thresholds.
//   LARGE_TEXT — beyond this we still load but switch CodeMirror to a
//   stripped-down read-only view so the syntax tree doesn't choke.
//   IMAGE_INLINE_MAX — beyond this we don't base64-load the image; user
//   gets a "download" affordance instead so the page doesn't lock up.
//   The hard read cap (5MB) still lives on the backend; anything above
//   short-circuits to the "too large" placeholder.
const LARGE_TEXT_THRESHOLD = 1024 * 1024;
const IMAGE_INLINE_MAX = 2 * 1024 * 1024;
const HARD_READ_MAX = 5 * 1024 * 1024;

type EditorMode = 'text' | 'text_large' | 'image' | 'binary' | 'too_large' | 'error';

function classifyForRender(stat: FsStatResponse): EditorMode {
  const mime = stat.mime || '';
  const isText =
    mime.startsWith('text/') ||
    /\b(json|javascript|xml|yaml|toml)\b/.test(mime) ||
    mime === '';
  const isImage = mime.startsWith('image/');
  if (stat.size > HARD_READ_MAX) return 'too_large';
  if (isImage) return stat.size > IMAGE_INLINE_MAX ? 'binary' : 'image';
  if (isText) return stat.size > LARGE_TEXT_THRESHOLD ? 'text_large' : 'text';
  return 'binary';
}

// Stable basicSetup objects (module-level so their identity never changes).
// @uiw/react-codemirror reconfigures the editor when basicSetup changes by
// reference, so passing a fresh inline object on every render would force a
// reconfigure each time — catastrophic on large docs.
const BASIC_SETUP_EDIT = {
  lineNumbers: true,
  highlightActiveLine: true,
  foldGutter: true,
  bracketMatching: true,
  closeBrackets: true,
  autocompletion: false,
} as const;
const BASIC_SETUP_READONLY = {
  lineNumbers: true,
  highlightActiveLine: false,
  foldGutter: false,
  bracketMatching: false,
  closeBrackets: false,
  autocompletion: false,
} as const;

interface CodeEditorProps {
  agentId: string;
  path: string;
  /** Filesystem root the path is anchored against (defaults to "workspace").
   *  Anything other than "workspace" makes the editor read-only: writes,
   *  rename, diff, and external-change reconciliation all require workspace
   *  scope on the backend. */
  root?: string;
  /** Bumps to force a reload from disk (e.g. external watcher event). */
  reloadKey?: number;
  /** When `nonce` changes, scroll/cursor jumps to {line, col}. 1-based. */
  jump?: { line: number; col?: number; nonce: number };
  /** Notifies parent (tab bar) when dirty state changes. */
  onDirtyChange?: (dirty: boolean) => void;
  /** Cursor / selection change; used by host.active_file bridge and by
   *  "send with selection". 1-based positions. */
  onCursorChange?: (pos: EditorCursor) => void;
  /** Open this file's diff view in the parent tabs. */
  onShowDiff?: () => void;
  /** Page chat-ws client id; required so the send-path broadcast targets
   *  this exact tab. */
  pageClientId?: string;
  /** Whether this editor is the currently active tab. Inactive editors keep
   *  state but drop their `data-id="code-editor"` so DOM queries like
   *  [data-id=code-editor] match exactly one element. */
  active?: boolean;
  className?: string;
}

export interface EditorCursor {
  line: number;
  col: number;
  /** Empty when nothing is selected. */
  selectionText: string;
  /** Present only when a non-empty range is selected. 1-based, inclusive start. */
  range?: {
    startLine: number;
    startCharacter: number;
    endLine: number;
    endCharacter: number;
  };
}

interface BufferState {
  loading: boolean;
  mode: EditorMode;
  /** Empty when not yet loaded or for binary files. */
  text: string;
  /** Last-saved content; dirty = text !== savedText. */
  savedText: string;
  mtime: number;
  size: number;
  mime: string;
  encoding: 'utf-8' | 'base64';
  /** Base64 body (only set when encoding === 'base64'). */
  base64: string;
  error: string;
}

const initialBuffer: BufferState = {
  loading: true,
  mode: 'text',
  text: '',
  savedText: '',
  mtime: 0,
  size: 0,
  mime: '',
  encoding: 'utf-8',
  base64: '',
  error: '',
};

function bufferFromRead(res: FsReadResult, mode: EditorMode): BufferState {
  return {
    loading: false,
    mode,
    text: res.text,
    savedText: res.text,
    mtime: res.mtime,
    size: res.size,
    mime: res.mime,
    encoding: res.encoding,
    base64: res.base64,
    error: '',
  };
}

function bufferFromStat(stat: FsStatResponse, mode: EditorMode): BufferState {
  return {
    loading: false,
    mode,
    text: '',
    savedText: '',
    mtime: stat.mtime,
    size: stat.size,
    mime: stat.mime || '',
    encoding: 'base64',
    base64: '',
    error: '',
  };
}

function isImageMime(mime: string): boolean {
  return /^image\//.test(mime);
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return `${n}B`;
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v >= 100 ? Math.round(v) : v >= 10 ? v.toFixed(1) : v.toFixed(2)} ${units[i]}`;
}

export default function CodeEditor({
  agentId,
  path,
  root = 'workspace',
  reloadKey,
  jump,
  onDirtyChange,
  onCursorChange,
  onShowDiff,
  pageClientId,
  active = true,
  className,
}: CodeEditorProps) {
  // Inactive editors stay mounted (so buffer state survives tab switches)
  // but advertise a different data-id so [data-id=code-editor] uniquely
  // matches the visible one.
  const rootDataId = active ? 'code-editor' : 'code-editor-inactive';

  const isMarkdown = isMarkdownPath(path);
  // Default to preview when opening a markdown file; user toggles via right-click.
  const [previewMd, setPreviewMd] = useState<boolean>(isMarkdown);
  useEffect(() => {
    setPreviewMd(isMarkdownPath(path));
  }, [path]);

  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const openMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setMenu({ x: e.clientX, y: e.clientY });
  }, []);
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    document.addEventListener('pointerdown', close);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', close);
      document.removeEventListener('keydown', onKey);
    };
  }, [menu]);
  const [buf, setBuf] = useState<BufferState>(initialBuffer);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState<{ actualMtime: number } | null>(null);
  const [externalChange, setExternalChange] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const cmRef = useRef<ReactCodeMirrorRef>(null);
  const lastDirtyRef = useRef(false);

  // Smart merge: clean buffer adopts fresh content; dirty buffer surfaces a
  // banner so the user picks (reload vs keep). Used by both the initial
  // revalidate and external-change events.
  const mergeFreshIntoBuffer = useCallback((res: FsReadResult, mode: EditorMode) => {
    setBuf((prev) => {
      if (prev.text === prev.savedText) {
        return bufferFromRead(res, mode);
      }
      if (prev.savedText === res.text) {
        return { ...prev, mtime: res.mtime, size: res.size };
      }
      setExternalChange(true);
      return prev;
    });
  }, []);

  // Load the file. Two-phase flow:
  //   1. fsApi.stat — cheap; decides what to do based on size + mime
  //   2. fsApi.read — only when we actually want the body (text or
  //      small-enough image). Binary / oversized files skip step 2 entirely
  //      so a 50MB tarball doesn't get base64-loaded into memory.
  // Cached read/stat are peeked first for an instant first paint.
  useEffect(() => {
    if (!agentId || !path) return;
    abortRef.current?.abort();
    const ctl = new AbortController();
    abortRef.current = ctl;
    setConflict(null);
    setExternalChange(false);

    const readKey = fsKey.read(agentId, path);
    const statKey = fsKey.stat(agentId, path);
    const cachedRead = fsCachePeek<FsReadResult>('read', readKey);
    const cachedStat = fsCachePeek<FsStatResponse>('stat', statKey);
    if (cachedRead) {
      // Recover mode from size/mime in the cached read result so the
      // initial render matches the post-revalidate one.
      const fakeStat: FsStatResponse = {
        name: fsBasename(path),
        path,
        is_dir: false,
        size: cachedRead.size,
        mtime: cachedRead.mtime,
        mode: '',
        mime: cachedRead.mime,
      };
      setBuf(bufferFromRead(cachedRead, classifyForRender(fakeStat)));
    } else if (cachedStat) {
      const mode = classifyForRender(cachedStat);
      if (mode === 'text' || mode === 'image') {
        // Read in flight; show loading until body arrives.
        setBuf({ ...initialBuffer, loading: true, mode });
      } else {
        setBuf(bufferFromStat(cachedStat, mode));
      }
    } else {
      setBuf({ ...initialBuffer, loading: true });
    }

    const offSub = fsCacheSubscribe('read', readKey, () => {
      const fresh = fsCachePeek<FsReadResult>('read', readKey);
      if (!fresh) return;
      mergeFreshIntoBuffer(fresh, classifyForRender({
        name: fsBasename(path), path, is_dir: false,
        size: fresh.size, mtime: fresh.mtime, mode: '', mime: fresh.mime,
      }));
    });

    const fetchBody = (mode: EditorMode) => {
      fsApi
        .read(agentId, path, { signal: ctl.signal, root })
        .then((res) => {
          fsCacheSet('read', readKey, res);
          mergeFreshIntoBuffer(res, mode);
        })
        .catch((err) => {
          if ((err as Error)?.name === 'CanceledError') return;
          setBuf((prev) =>
            prev.loading
              ? { ...initialBuffer, loading: false, error: friendlyFsError(err) }
              : { ...prev, error: friendlyFsError(err) },
          );
        });
    };

    fsApi
      .stat(agentId, path, { root })
      .then((stat) => {
        fsCacheSet('stat', statKey, stat);
        const mode = classifyForRender(stat);
        if (mode === 'text' || mode === 'text_large' || mode === 'image') {
          if (mode === 'text_large') {
            // Show a placeholder header even before the (potentially slow)
            // big text body arrives; loading is per-cm under it.
            setBuf((prev) => prev.loading
              ? { ...bufferFromStat(stat, mode), loading: true }
              : prev,
            );
          }
          fetchBody(mode);
        } else {
          // 'binary' or 'too_large' — no body load.
          setBuf(bufferFromStat(stat, mode));
        }
      })
      .catch((err) => {
        if ((err as Error)?.name === 'CanceledError') return;
        setBuf({ ...initialBuffer, loading: false, mode: 'error', error: friendlyFsError(err) });
      });

    return () => {
      ctl.abort();
      offSub();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentId, path, root, reloadKey, mergeFreshIntoBuffer]);

  const language = useMemo(() => languageForPath(path), [path]);

  const dirty = !buf.loading && buf.encoding === 'utf-8' && buf.text !== buf.savedText;

  useEffect(() => {
    if (lastDirtyRef.current !== dirty) {
      lastDirtyRef.current = dirty;
      onDirtyChange?.(dirty);
    }
  }, [dirty, onDirtyChange]);

  // Cursor jump (Cmd+P / global search → open at line:col).
  useEffect(() => {
    if (!jump || buf.loading) return;
    const view = cmRef.current?.view;
    if (!view) return;
    const line = Math.max(1, Math.floor(jump.line));
    const col = Math.max(1, Math.floor(jump.col ?? 1));
    try {
      const total = view.state.doc.lines;
      const lineIdx = Math.min(line, total);
      const lineRef = view.state.doc.line(lineIdx);
      const pos = Math.min(lineRef.from + col - 1, lineRef.to);
      view.dispatch({
        selection: { anchor: pos },
        effects: EditorView.scrollIntoView(pos, { y: 'center' }),
      });
      view.focus();
    } catch {}
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jump?.nonce, buf.loading]);

  const handleSave = useCallback(async () => {
    if (saving || buf.loading || buf.encoding !== 'utf-8') return;
    if (!dirty && !conflict) return;
    setSaving(true);
    try {
      const res = await fsApi.write(agentId, path, buf.text, buf.mtime);
      setBuf((prev) => ({
        ...prev,
        savedText: prev.text,
        mtime: res.mtime,
        size: res.size,
      }));
      setConflict(null);
      // Re-seed the read cache so a sibling diff view or quick re-open
      // sees the just-written content without a round trip.
      fsCacheSet<FsReadResult>('read', fsKey.read(agentId, path), {
        text: buf.text,
        base64: '',
        mtime: res.mtime,
        size: res.size,
        mime: buf.mime,
        encoding: 'utf-8',
      });
      fsCacheInvalidatePath(agentId, path);
    } catch (err) {
      if (err instanceof FsError && err.status === 409) {
        setConflict({ actualMtime: err.actualMtime ?? 0 });
      } else {
        setBuf((prev) => ({ ...prev, error: friendlyFsError(err) }));
      }
    } finally {
      setSaving(false);
    }
  }, [agentId, path, buf.text, buf.mtime, buf.mime, buf.encoding, buf.loading, dirty, conflict, saving]);

  const handleForceSave = useCallback(async () => {
    setSaving(true);
    try {
      const res = await fsApi.write(agentId, path, buf.text);
      setBuf((prev) => ({
        ...prev,
        savedText: prev.text,
        mtime: res.mtime,
        size: res.size,
      }));
      setConflict(null);
      fsCacheSet<FsReadResult>('read', fsKey.read(agentId, path), {
        text: buf.text,
        base64: '',
        mtime: res.mtime,
        size: res.size,
        mime: buf.mime,
        encoding: 'utf-8',
      });
      fsCacheInvalidatePath(agentId, path);
    } catch (err) {
      setBuf((prev) => ({ ...prev, error: friendlyFsError(err) }));
    } finally {
      setSaving(false);
    }
  }, [agentId, path, buf.text, buf.mime]);

  const handleReload = useCallback(async () => {
    try {
      const res = await fsApi.read(agentId, path, { root });
      fsCacheSet('read', fsKey.read(agentId, path), res);
      const synthStat: FsStatResponse = {
        name: fsBasename(path), path, is_dir: false,
        size: res.size, mtime: res.mtime, mode: '', mime: res.mime,
      };
      setBuf(bufferFromRead(res, classifyForRender(synthStat)));
      setConflict(null);
      setExternalChange(false);
    } catch (err) {
      setBuf((prev) => ({ ...prev, error: friendlyFsError(err) }));
    }
  }, [agentId, path, root]);

  const handleDownload = useCallback(() => {
    fsApi.download(agentId, path);
  }, [agentId, path]);

  const handleSendToAgent = useCallback(async () => {
    let range: { startLine: number; startCharacter: number; endLine: number; endCharacter: number } | undefined;
    let selectionText = '';
    const view = cmRef.current?.view;
    if (view) {
      const sel = view.state.selection.main;
      if (sel.from !== sel.to) {
        try {
          const startLine = view.state.doc.lineAt(sel.from);
          const endLine = view.state.doc.lineAt(sel.to);
          range = {
            startLine: startLine.number,
            startCharacter: sel.from - startLine.from + 1,
            endLine: endLine.number,
            endCharacter: sel.to - endLine.from + 1,
          };
          selectionText = view.state.doc.sliceString(sel.from, sel.to);
        } catch {}
      }
    }
    try {
      await fsApi.sendPathToAgent(agentId, path, {
        fileName: fsBasename(path),
        pageClientId,
        selectionText,
        range,
      });
    } catch (err) {
      setBuf((prev) => ({ ...prev, error: friendlyFsError(err) }));
    }
  }, [agentId, path, pageClientId]);

  // Cmd+S / Ctrl+S → save
  const saveKeymap = useMemo(
    () =>
      keymap.of([
        {
          key: 'Mod-s',
          preventDefault: true,
          run: () => {
            handleSave();
            return true;
          },
        },
      ]),
    [handleSave],
  );

  // Track cursor + selection; emit to parent so :code-ext bridge can answer
  // host.active_file and "send with selection" can include the range.
  const cursorExt = useMemo(
    () =>
      EditorView.updateListener.of((u) => {
        if (!onCursorChange) return;
        // React only to selection/content changes — NOT viewportChanged.
        // Firing on scroll bubbled a cursor event up to the parent on every
        // scroll frame, re-rendering the editor (and, for read-only large
        // files, reconfiguring CodeMirror) — which froze the UI on big files.
        if (!u.selectionSet && !u.docChanged) return;
        const sel = u.state.selection.main;
        try {
          const startLine = u.state.doc.lineAt(sel.from);
          const endLine = u.state.doc.lineAt(sel.to);
          const line = endLine.number;
          const col = sel.head - endLine.from + 1;
          const empty = sel.from === sel.to;
          onCursorChange({
            line,
            col,
            selectionText: empty ? '' : u.state.doc.sliceString(sel.from, sel.to),
            range: empty
              ? undefined
              : {
                  startLine: startLine.number,
                  startCharacter: sel.from - startLine.from + 1,
                  endLine: endLine.number,
                  endCharacter: sel.to - endLine.from + 1,
                },
          });
        } catch {}
      }),
    [onCursorChange],
  );

  const extensions = useMemo(
    () => [...language, saveKeymap, cursorExt, cmBlendTheme],
    [language, saveKeymap, cursorExt],
  );
  // Read-only variant (large `text_large` files + non-workspace roots): no
  // language parser, not editable. MUST be memoized — building this array
  // inline in the JSX made @uiw/react-codemirror reconfigure the whole editor
  // on every render, so any re-render of a large read-only file (scroll,
  // cursor, parent update) re-ran a full reconfigure and dragged the UI to a
  // halt. Dropping `language` here is also what makes the "syntax highlight
  // off" banner actually true.
  const readonlyExtensions = useMemo(
    () => [saveKeymap, cursorExt, EditorView.editable.of(false), cmBlendTheme],
    [saveKeymap, cursorExt],
  );

  if (!path) {
    return (
      <div
        data-id={active ? 'code-editor-empty' : 'code-editor-inactive'}
        className={`flex items-center justify-center h-full text-sm text-zinc-500 ${className || ''}`}
      >
        选择一个文件
      </div>
    );
  }

  if (buf.loading) {
    return (
      <div
        data-id={active ? 'code-editor-loading' : 'code-editor-inactive'}
        className={`flex items-center justify-center h-full text-sm text-zinc-500 ${className || ''}`}
      >
        加载中…
      </div>
    );
  }

  // Hard error (file gone, permission denied, etc.) and no text loaded —
  // show a friendly placeholder instead of an empty editor.
  if (buf.error && !buf.text && !buf.base64) {
    return (
      <div
        data-id={active ? 'code-editor-error' : 'code-editor-inactive'}
        className={`flex flex-col h-full ${className || ''}`}
      >
        <div className="flex items-center gap-2 px-3 py-1.5 border-b border-zinc-800 bg-zinc-900/50 text-xs text-zinc-300">
          <span className="font-mono truncate" title={path}>{fsBasename(path)}</span>
        </div>
        <div className="flex-1 flex items-center justify-center text-sm text-zinc-400">
          <div data-id="code-editor-error-message" className="text-center">
            <div className="text-red-400 mb-1">{buf.error}</div>
            <div className="text-xs text-zinc-500 font-mono">{path}</div>
          </div>
        </div>
      </div>
    );
  }

  // Image preview branch — only when we have actual base64 body (small image).
  if (buf.mode === 'image' && buf.base64) {
    return (
      <div
        data-id={active ? 'code-editor-image' : 'code-editor-inactive'}
        data-path={path}
        onContextMenu={openMenu}
        className={`flex flex-col h-full bg-[#0A0A0A] ${className || ''}`}
      >
        <div className="flex-1 overflow-auto flex items-center justify-center bg-zinc-900">
          <img
            data-id="code-editor-image-preview"
            src={`data:${buf.mime};base64,${buf.base64}`}
            alt={fsBasename(path)}
            className="max-w-full max-h-full"
          />
        </div>
        {menu && (
          <EditorContextMenu
            x={menu.x}
            y={menu.y}
            onSendToAgent={handleSendToAgent}
            onDownload={handleDownload}
            onClose={() => setMenu(null)}
          />
        )}
      </div>
    );
  }

  // Binary / too-large placeholder. No body load: just size + mime + download.
  if (buf.mode === 'binary' || buf.mode === 'too_large' || (buf.mode === 'image' && !buf.base64)) {
    const tooLarge = buf.mode === 'too_large';
    return (
      <div
        data-id={active ? 'code-editor-binary' : 'code-editor-inactive'}
        data-path={path}
        onContextMenu={openMenu}
        className={`flex flex-col h-full bg-[#0A0A0A] ${className || ''}`}
      >
        <div className="flex-1 flex items-center justify-center">
          <div data-id="code-editor-binary-card" className="text-center text-sm">
            <div className="text-zinc-300 font-mono truncate max-w-md mx-auto" title={path}>
              {fsBasename(path)}
            </div>
            <div className="mt-2 text-zinc-500">
              {formatBytes(buf.size)}
              {buf.mime && <> · <span className="font-mono">{buf.mime}</span></>}
            </div>
            <div className="mt-1 text-xs text-zinc-600">
              {tooLarge
                ? '文件超过 5MB,无法在编辑器内打开'
                : buf.mode === 'image'
                  ? '图片较大,不内联预览'
                  : '二进制文件,不在编辑器内打开'}
            </div>
            <button
              data-id="code-editor-binary-download"
              onClick={handleDownload}
              className="mt-4 inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-sky-600 hover:bg-sky-500 text-white text-xs"
            >
              <Download className="w-3.5 h-3.5" /> 下载
            </button>
          </div>
        </div>
        {menu && (
          <EditorContextMenu
            x={menu.x}
            y={menu.y}
            onSendToAgent={handleSendToAgent}
            onDownload={handleDownload}
            onClose={() => setMenu(null)}
          />
        )}
      </div>
    );
  }

  const heavy = buf.mode === 'text_large';
  // Non-workspace roots (projects/skills/home) are read-only at the backend.
  // Mirror that in the editor so users don't type changes that can never save.
  const readOnly = heavy || root !== 'workspace';
  return (
    <div
      data-id={rootDataId}
      data-path={path}
      data-mode={buf.mode}
      onContextMenu={openMenu}
      className={`flex flex-col h-full bg-[#0A0A0A] relative ${className || ''}`}
    >
      {dirty && (
        <span
          data-id="code-editor-dirty"
          className="absolute right-2 top-1.5 text-amber-400 text-xs pointer-events-none"
        >
          ●
        </span>
      )}
      {isMarkdown && (
        <button
          data-id="code-editor-md-toggle"
          type="button"
          onClick={(e) => { e.stopPropagation(); setPreviewMd((v) => !v); }}
          // Sits inside the right-click toolbar zone; keep it visually quiet
          // — translucent border, no fill, brightens on hover. Pulls slightly
          // farther from the right edge when dirty-dot is visible.
          className={`absolute top-1 ${dirty ? 'right-6' : 'right-2'} z-10 inline-flex items-center gap-1 px-2 py-0.5 rounded border border-zinc-700/60 bg-zinc-900/70 hover:bg-zinc-800 hover:border-zinc-600 text-[10px] uppercase tracking-wide text-zinc-300 hover:text-zinc-100 transition-colors`}
          title={previewMd ? '切换到源码' : '切换到预览'}
          aria-pressed={previewMd}
        >
          {previewMd ? <FileCode className="w-3 h-3" /> : <Eye className="w-3 h-3" />}
          <span>{previewMd ? 'Source' : 'Preview'}</span>
        </button>
      )}
      {heavy && (
        <div data-id="code-editor-heavy-banner" className="flex items-center gap-2 px-3 py-1.5 bg-amber-900/20 border-b border-amber-800/40 text-xs text-amber-200">
          <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
          <span>大文件 ({formatBytes(buf.size)}) — 只读模式,已关闭语法高亮</span>
        </div>
      )}
      {conflict && (
        <div className="flex items-center gap-3 px-3 py-2 bg-amber-900/30 border-b border-amber-700/40 text-xs text-amber-200">
          <AlertTriangle className="w-4 h-4 shrink-0" />
          <span className="flex-1">
            磁盘上的版本已被外部修改 (mtime {conflict.actualMtime})。覆盖将丢弃外部改动,重新加载将丢弃当前未保存的编辑。
          </span>
          <button
            className="px-2 py-1 rounded bg-amber-700/60 hover:bg-amber-700 text-white"
            onClick={handleForceSave}
          >
            强制覆盖
          </button>
          <button
            className="px-2 py-1 rounded bg-zinc-700 hover:bg-zinc-600 text-white"
            onClick={handleReload}
          >
            重新加载
          </button>
        </div>
      )}
      {externalChange && !conflict && (
        <div className="flex items-center gap-3 px-3 py-2 bg-sky-900/30 border-b border-sky-700/40 text-xs text-sky-200">
          <AlertTriangle className="w-4 h-4 shrink-0" />
          <span className="flex-1">
            文件已被外部修改,你当前还有未保存的改动。
          </span>
          <button
            className="px-2 py-1 rounded bg-sky-700/60 hover:bg-sky-700 text-white"
            onClick={handleReload}
          >
            放弃我的改动并重新加载
          </button>
          <button
            className="px-2 py-1 rounded bg-zinc-700 hover:bg-zinc-600 text-white"
            onClick={() => setExternalChange(false)}
          >
            忽略
          </button>
        </div>
      )}
      {buf.error && (
        <div className="px-3 py-2 bg-red-900/30 border-b border-red-700/40 text-xs text-red-200">
          {buf.error}
        </div>
      )}
      <div className="flex-1 overflow-hidden">
        {isMarkdown && previewMd ? (
          <MarkdownPreview source={buf.text || ''} />
        ) : (
          <CodeMirror
            ref={cmRef}
            value={buf.text}
            height="100%"
            theme={oneDark}
            extensions={readOnly ? readonlyExtensions : extensions}
            onChange={(v) => setBuf((prev) => ({ ...prev, text: v }))}
            editable={!readOnly}
            basicSetup={readOnly ? BASIC_SETUP_READONLY : BASIC_SETUP_EDIT}
            style={{ height: '100%' }}
          />
        )}
      </div>
      {menu && (
        <EditorContextMenu
          x={menu.x}
          y={menu.y}
          canSave={!heavy && dirty}
          canDiff={!!onShowDiff}
          canPreviewMd={isMarkdown}
          previewing={previewMd}
          onSave={handleSave}
          onSendToAgent={handleSendToAgent}
          onDownload={handleDownload}
          onShowDiff={onShowDiff}
          onReload={handleReload}
          onTogglePreview={isMarkdown ? () => setPreviewMd((v) => !v) : undefined}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  );
}

interface EditorContextMenuProps {
  x: number;
  y: number;
  canSave?: boolean;
  canDiff?: boolean;
  canPreviewMd?: boolean;
  previewing?: boolean;
  onSave?: () => void;
  onSendToAgent: () => void;
  onDownload: () => void;
  onShowDiff?: () => void;
  onReload?: () => void;
  onTogglePreview?: () => void;
  onClose: () => void;
}

function EditorContextMenu({
  x, y, canSave, canDiff, canPreviewMd, previewing,
  onSave, onSendToAgent, onDownload, onShowDiff, onReload, onTogglePreview,
  onClose,
}: EditorContextMenuProps) {
  const ref = useRef<HTMLDivElement | null>(null);
  // After the menu mounts, measure its actual size and shift it back inside
  // the viewport if it overflowed (e.g. right-click near the right edge).
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
      data-id="code-editor-context-menu"
      className="fixed z-[2147483647] min-w-[180px] py-1 rounded-md border border-zinc-700 bg-zinc-900 shadow-xl text-xs text-zinc-200"
      style={{ left: pos.left, top: pos.top }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      {canSave && onSave && (
        <Item
          icon={<Save className="w-3.5 h-3.5" />}
          onClick={() => { onSave(); onClose(); }}
          shortcut="Cmd+S"
        >
          保存
        </Item>
      )}
      {canPreviewMd && onTogglePreview && (
        <Item
          icon={previewing ? <FileCode className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
          onClick={() => { onTogglePreview(); onClose(); }}
        >
          {previewing ? '显示源码' : '预览 Markdown'}
        </Item>
      )}
      <Item
        icon={<Send className="w-3.5 h-3.5" />}
        onClick={() => { onSendToAgent(); onClose(); }}
      >
        发送给当前 agent
      </Item>
      <Item
        icon={<DownloadIcon className="w-3.5 h-3.5" />}
        onClick={() => { onDownload(); onClose(); }}
      >
        下载
      </Item>
      {canDiff && onShowDiff && (
        <Item
          icon={<GitCompare className="w-3.5 h-3.5" />}
          onClick={() => { onShowDiff(); onClose(); }}
        >
          对比 HEAD
        </Item>
      )}
      {onReload && (
        <Item
          icon={<RefreshCw className="w-3.5 h-3.5" />}
          onClick={() => { onReload(); onClose(); }}
        >
          重新加载
        </Item>
      )}
    </div>
  );
}

function Item({
  icon,
  onClick,
  children,
  shortcut,
}: {
  icon?: React.ReactNode;
  onClick: () => void;
  children: React.ReactNode;
  shortcut?: string;
}) {
  return (
    <button
      data-id={`code-editor-context-${typeof children === 'string' ? children.replace(/[^\w]/g, '-') : 'item'}`}
      className="w-full text-left flex items-center gap-2 px-3 py-1.5 hover:bg-zinc-800"
      onClick={onClick}
    >
      {icon}
      <span className="flex-1">{children}</span>
      {shortcut && <span className="text-[10px] text-zinc-500">{shortcut}</span>}
    </button>
  );
}
