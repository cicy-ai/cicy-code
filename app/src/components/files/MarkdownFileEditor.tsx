// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import CodeMirror from '@uiw/react-codemirror';
import { markdown } from '@codemirror/lang-markdown';
import { oneDark } from '@codemirror/theme-one-dark';
import { EditorView } from '@codemirror/view';
import { cicySearch } from './cmSearchPanel';

const SEARCH_EXT = cicySearch();

const fileEditorTheme = EditorView.theme({
  '&': { height: '100%', backgroundColor: 'transparent !important', color: '#e4e4e7' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
  '.cm-gutters': { backgroundColor: 'transparent !important', borderRight: '1px solid rgba(255,255,255,.05)', color: 'rgba(228,228,231,.3)' },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'rgba(255,255,255,.03)' },
  '.cm-selectionBackground, ::selection': { backgroundColor: 'rgba(113,113,122,.35) !important' },
});

export default function MarkdownFileEditor({ value, path, onChange, loading = false }: {
  value: string;
  path: string;
  onChange: (value: string) => void;
  loading?: boolean;
}) {
  return (
    <div data-id="markdown-file-editor" className="flex h-full min-h-0 flex-col overflow-hidden bg-[#0d0e11]">
      <div data-id="markdown-file-editor-path" className="h-9 shrink-0 truncate border-b border-white/[0.07] px-3 font-mono text-[11px] leading-9 text-zinc-500">
        {path}
      </div>
      <div data-id="markdown-file-editor-body" className="min-h-0 flex-1">
        {loading ? (
          <div data-id="markdown-file-editor-loading" className="grid h-full place-items-center text-xs text-zinc-500">Loading…</div>
        ) : (
          <CodeMirror
            value={value}
            height="100%"
            theme={oneDark}
            basicSetup={{ lineNumbers: true, highlightActiveLine: true, foldGutter: true, bracketMatching: true, closeBrackets: true, autocompletion: false }}
            extensions={[markdown(), ...SEARCH_EXT, fileEditorTheme]}
            onChange={onChange}
            className="h-full"
            style={{ height: '100%' }}
          />
        )}
      </div>
    </div>
  );
}
