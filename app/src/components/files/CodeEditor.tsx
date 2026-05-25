import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { keymap } from '@codemirror/view';
import { oneDark } from '@codemirror/theme-one-dark';
import { Save, Send, RefreshCw, AlertTriangle } from 'lucide-react';
import { fsApi, fsBasename, FsError } from './api';
import { languageForPath } from './language';

interface CodeEditorProps {
  agentId: string;
  path: string;
  /** Bumps to force a reload from disk (e.g. external watcher event). */
  reloadKey?: number;
  className?: string;
}

interface BufferState {
  loading: boolean;
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
  text: '',
  savedText: '',
  mtime: 0,
  size: 0,
  mime: '',
  encoding: 'utf-8',
  base64: '',
  error: '',
};

function isImageMime(mime: string): boolean {
  return /^image\//.test(mime);
}

export default function CodeEditor({
  agentId,
  path,
  workspaceFolder,
  reloadKey,
  className,
}: CodeEditorProps) {
  const [buf, setBuf] = useState<BufferState>(initialBuffer);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState<{ actualMtime: number } | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  // Load the file whenever path or reloadKey changes.
  useEffect(() => {
    if (!agentId || !path) return;
    abortRef.current?.abort();
    const ctl = new AbortController();
    abortRef.current = ctl;
    setBuf({ ...initialBuffer, loading: true });
    setConflict(null);
    fsApi
      .read(agentId, path, { signal: ctl.signal })
      .then((res) => {
        setBuf({
          loading: false,
          text: res.text,
          savedText: res.text,
          mtime: res.mtime,
          size: res.size,
          mime: res.mime,
          encoding: res.encoding,
          base64: res.base64,
          error: '',
        });
      })
      .catch((err) => {
        if ((err as Error)?.name === 'CanceledError') return;
        setBuf({ ...initialBuffer, loading: false, error: (err as Error).message });
      });
    return () => ctl.abort();
  }, [agentId, path, reloadKey]);

  const language = useMemo(() => languageForPath(path), [path]);

  const dirty = !buf.loading && buf.encoding === 'utf-8' && buf.text !== buf.savedText;

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
    } catch (err) {
      if (err instanceof FsError && err.status === 409) {
        setConflict({ actualMtime: err.actualMtime ?? 0 });
      } else {
        setBuf((prev) => ({ ...prev, error: (err as Error).message }));
      }
    } finally {
      setSaving(false);
    }
  }, [agentId, path, buf.text, buf.mtime, buf.encoding, buf.loading, dirty, conflict, saving]);

  const handleForceSave = useCallback(async () => {
    setSaving(true);
    try {
      const res = await fsApi.write(agentId, path, buf.text); // no expected_mtime
      setBuf((prev) => ({
        ...prev,
        savedText: prev.text,
        mtime: res.mtime,
        size: res.size,
      }));
      setConflict(null);
    } catch (err) {
      setBuf((prev) => ({ ...prev, error: (err as Error).message }));
    } finally {
      setSaving(false);
    }
  }, [agentId, path, buf.text]);

  const handleReload = useCallback(async () => {
    try {
      const res = await fsApi.read(agentId, path);
      setBuf({
        loading: false,
        text: res.text,
        savedText: res.text,
        mtime: res.mtime,
        size: res.size,
        mime: res.mime,
        encoding: res.encoding,
        base64: res.base64,
        error: '',
      });
      setConflict(null);
    } catch (err) {
      setBuf((prev) => ({ ...prev, error: (err as Error).message }));
    }
  }, [agentId, path]);

  const handleSendToAgent = useCallback(async () => {
    try {
      await fsApi.sendPathToAgent(agentId, path);
    } catch (err) {
      setBuf((prev) => ({ ...prev, error: (err as Error).message }));
    }
  }, [agentId, path]);

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

  const extensions = useMemo(
    () => [...language, saveKeymap],
    [language, saveKeymap],
  );

  if (!path) {
    return (
      <div
        className={`flex items-center justify-center h-full text-sm text-zinc-500 ${className || ''}`}
      >
        选择一个文件
      </div>
    );
  }

  if (buf.loading) {
    return (
      <div
        className={`flex items-center justify-center h-full text-sm text-zinc-500 ${className || ''}`}
      >
        加载中…
      </div>
    );
  }

  // Image preview branch
  if (buf.encoding === 'base64' && isImageMime(buf.mime)) {
    return (
      <div className={`flex flex-col h-full ${className || ''}`}>
        <Toolbar
          path={path}
          dirty={false}
          saving={false}
          onSave={() => {}}
          onSendToAgent={handleSendToAgent}
          disabled={true}
          mtime={buf.mtime}
          size={buf.size}
        />
        <div className="flex-1 overflow-auto flex items-center justify-center bg-zinc-900">
          <img
            src={`data:${buf.mime};base64,${buf.base64}`}
            alt={fsBasename(path)}
            className="max-w-full max-h-full"
          />
        </div>
      </div>
    );
  }

  // Binary placeholder
  if (buf.encoding === 'base64') {
    return (
      <div className={`flex flex-col h-full ${className || ''}`}>
        <Toolbar
          path={path}
          dirty={false}
          saving={false}
          onSave={() => {}}
          onSendToAgent={handleSendToAgent}
          disabled={true}
          mtime={buf.mtime}
          size={buf.size}
        />
        <div className="flex-1 flex items-center justify-center text-sm text-zinc-500">
          二进制文件 ({buf.mime || 'unknown'}, {buf.size} bytes) — 不在编辑器内打开
        </div>
      </div>
    );
  }

  return (
    <div className={`flex flex-col h-full ${className || ''}`}>
      <Toolbar
        path={path}
        dirty={dirty}
        saving={saving}
        onSave={handleSave}
        onSendToAgent={handleSendToAgent}
        disabled={false}
        mtime={buf.mtime}
        size={buf.size}
      />
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
      {buf.error && (
        <div className="px-3 py-2 bg-red-900/30 border-b border-red-700/40 text-xs text-red-200">
          {buf.error}
        </div>
      )}
      <div className="flex-1 overflow-hidden">
        <CodeMirror
          value={buf.text}
          height="100%"
          theme={oneDark}
          extensions={extensions}
          onChange={(v) => setBuf((prev) => ({ ...prev, text: v }))}
          basicSetup={{
            lineNumbers: true,
            highlightActiveLine: true,
            foldGutter: true,
            bracketMatching: true,
            closeBrackets: true,
            autocompletion: false,
          }}
          style={{ height: '100%' }}
        />
      </div>
    </div>
  );
}

interface ToolbarProps {
  path: string;
  dirty: boolean;
  saving: boolean;
  disabled: boolean;
  mtime: number;
  size: number;
  onSave: () => void;
  onSendToAgent: () => void;
}

function Toolbar({
  path,
  dirty,
  saving,
  disabled,
  mtime,
  size,
  onSave,
  onSendToAgent,
}: ToolbarProps) {
  return (
    <div className="flex items-center gap-2 px-3 py-1.5 border-b border-zinc-800 bg-zinc-900/50 text-xs text-zinc-300">
      <span className="font-mono">{path}</span>
      {dirty && <span className="text-amber-400">●</span>}
      <span className="flex-1" />
      <span className="text-zinc-500">
        {size}B · {mtime ? new Date(mtime * 1000).toLocaleTimeString() : ''}
      </span>
      <button
        className="flex items-center gap-1 px-2 py-1 rounded hover:bg-zinc-800 disabled:opacity-40"
        onClick={onSendToAgent}
        title="发送给当前 agent"
      >
        <Send className="w-3.5 h-3.5" /> 发送给 agent
      </button>
      <button
        className="flex items-center gap-1 px-2 py-1 rounded hover:bg-zinc-800 disabled:opacity-40"
        onClick={onSave}
        disabled={disabled || saving || !dirty}
        title={'保存 (Cmd+S / Ctrl+S)'}
      >
        {saving ? (
          <RefreshCw className="w-3.5 h-3.5 animate-spin" />
        ) : (
          <Save className="w-3.5 h-3.5" />
        )}
        保存
      </button>
    </div>
  );
}
