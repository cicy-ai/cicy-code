// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useRef, useState } from 'react';
import { MergeView } from '@codemirror/merge';
import { EditorState } from '@codemirror/state';
import { EditorView, lineNumbers } from '@codemirror/view';
import { oneDark } from '@codemirror/theme-one-dark';
import { X, RefreshCw } from 'lucide-react';
import { fsApi, FsDiffResponse, fsBasename } from './api';
import { fsCachePeek, fsCacheSet, fsKey } from './fsCache';
import { languageForPath } from './language';
import i18n from '../../i18n';

const t = (k: string, o?: Record<string, unknown>) =>
  i18n.t(`fileExplorer.${k}`, { ns: 'workspace', ...o }) as string;

// Match CodeEditor: drop oneDark's #282c34/#21252b backgrounds so the diff
// panes blend into the #0A0A0A chrome. !important is required because
// oneDark's same-specificity rules otherwise win the cascade.
const cmBlendTheme = EditorView.theme({
  '&': { backgroundColor: 'transparent !important' },
  '.cm-gutters': {
    backgroundColor: 'transparent !important',
    color: 'rgba(228,228,231,0.35)',
  },
  // Dim line numbers so they read clearly below the diff content; target the
  // number elements directly + !important to beat oneDark's own rule.
  '.cm-lineNumbers .cm-gutterElement': { color: 'rgba(228,228,231,0.26) !important' },
  '.cm-activeLineGutter .cm-gutterElement, .cm-activeLineGutter': { color: 'rgba(228,228,231,0.55) !important' },
});

interface Props {
  agentId: string;
  path: string;
  base?: 'head' | 'index';
  onClose?: () => void;
  /** Whether this diff is the currently-visible tab. Inactive tabs drop
   *  their `data-id="diff-view"` so [data-id=diff-view] uniquely matches
   *  the visible one. */
  active?: boolean;
  className?: string;
}

/**
 * Read-only diff between the on-disk file and a git revision (HEAD by default).
 * Uses @codemirror/merge to render a side-by-side view.
 */
export default function DiffView({ agentId, path, base = 'head', onClose, active = true, className }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const viewRef = useRef<MergeView | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [meta, setMeta] = useState<{ aBytes: number; bBytes: number } | null>(null);

  useEffect(() => {
    if (!agentId || !path || !host.current) return;
    let cancelled = false;
    setError('');

    const renderDiff = (res: FsDiffResponse) => {
      if (cancelled || !host.current) return;
      viewRef.current?.destroy();
      const lang = languageForPath(path);
      const common = [
        oneDark,
        cmBlendTheme,
        lineNumbers(),
        EditorView.editable.of(false),
        EditorState.readOnly.of(true),
        ...lang,
      ];
      const mv = new MergeView({
        parent: host.current,
        a: { doc: res.a || '', extensions: common },
        b: { doc: res.b || '', extensions: common },
        gutter: true,
        highlightChanges: true,
        collapseUnchanged: { margin: 3, minSize: 4 },
      });
      viewRef.current = mv;
      setMeta({ aBytes: (res.a || '').length, bBytes: (res.b || '').length });
      setLoading(false);
    };

    // SWR: render cached diff immediately (if any), then refresh.
    const key = fsKey.diff(agentId, path, base);
    const cached = fsCachePeek<FsDiffResponse>('diff', key);
    if (cached) {
      renderDiff(cached);
    } else {
      setLoading(true);
    }

    fsApi
      .diff(agentId, path, base)
      .then((res) => {
        fsCacheSet('diff', key, res);
        if (
          cached &&
          cached.a === res.a &&
          cached.b === res.b &&
          cached.mode === res.mode
        ) {
          // Fresh matches cache; no need to rebuild the merge view.
          return;
        }
        renderDiff(res);
      })
      .catch((e) => {
        if (!cancelled && !cached) {
          setError((e as Error).message || 'diff failed');
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
      viewRef.current?.destroy();
      viewRef.current = null;
    };
  }, [agentId, path, base]);

  return (
    <div data-id={active ? 'diff-view' : 'diff-view-inactive'} data-path={path} className={`flex flex-col h-full ${className || ''}`}>
      <div data-id={active ? 'diff-view-toolbar' : 'diff-view-inactive-toolbar'} className="flex items-center gap-2 px-3 py-1.5 border-b border-zinc-800 bg-zinc-900/50 text-xs text-zinc-300">
        <span className="font-mono truncate" title={path}>diff: {fsBasename(path)}</span>
        <span className="text-zinc-500">vs {base.toUpperCase()}</span>
        <span className="flex-1" />
        {loading && <RefreshCw className="w-3.5 h-3.5 animate-spin" />}
        {meta && !loading && (
          <span className="text-zinc-500">
            {meta.aBytes}B ↔ {meta.bBytes}B
          </span>
        )}
        {onClose && (
          <button
            className="p-1 rounded hover:bg-zinc-800"
            onClick={onClose}
            title={t('diffClose')}
          >
            <X className="w-3.5 h-3.5" />
          </button>
        )}
      </div>
      {error ? (
        <div data-id="diff-view-error" className="flex-1 flex items-center justify-center text-sm text-red-400 px-4 text-center">
          {error}
        </div>
      ) : (
        <div ref={host} data-id="diff-view-host" className="flex-1 overflow-auto bg-[#0A0A0A]" />
      )}
    </div>
  );
}
