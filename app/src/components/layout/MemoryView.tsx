// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import CodeMirror, { ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { EditorView, keymap } from '@codemirror/view';
import { oneDark } from '@codemirror/theme-one-dark';
import { markdown } from '@codemirror/lang-markdown';
import { CLIPBOARD_KEYMAP, cmCopySelection, cmCutSelection, cmPasteSelection } from '../files/cmClipboard';
import {
  Globe,
  FolderGit2,
  Folder,
  FolderOpen,
  FileText,
  Plus,
  Trash2,
  Pencil,
  Loader2,
  ChevronDown,
  ChevronRight,
  Send,
  Eye,
  FileCode,
  Copy,
  Scissors,
  ClipboardPaste,
  WrapText,
  Check,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { sendToAgent as dispatchToAgent } from '../../services/agentSend';
import MarkdownPreview from '../files/MarkdownPreview';

// Memory editor for the inspector's Memory tab. Unlike the old file-explorer
// embed, this is a semantic, sectioned editor over the layered memory model:
//
//   本 Agent  — this agent's own CLAUDE.md / AGENTS.md (in its workspace)
//   Global    — ~/cicy-ai/memory/global.md          (single)
//   Project   — ~/cicy-ai/memory/projects/<slug>.md (add multiple)
//   Role      — ~/cicy-ai/memory/agents/<slug>.md   (add multiple)
//
// Left = sectioned list with add/delete for project & role; right = a markdown
// editor (same form as the file editor: edit + Save + dirty indicator), backed
// by the /api/memory/templates API.

type Scope = 'agent' | 'global' | 'project' | 'role';

interface Selection {
  scope: Scope;
  /** slug for project/role, the agent's paneId for agent, 'global' for global. */
  name: string;
}

interface MemoryViewProps {
  agentId: string;
  className?: string;
}

/** A role folder under ~/cicy-ai/memory/agents/<slug>/ and the files in it
 *  (role.md, role.zh.md, meta.yaml, …). A role file's Selection.name is the
 *  `<slug>/<file>` pair so the API addresses the exact file. */
interface RoleDir {
  slug: string;
  files: string[];
}

const NEW_PROJECT_SEED = `# Project Memory

<!-- Agents that select this project template inherit the project rules below. -->
`;
const NEW_ROLE_SEED = `# Role Charter

<!-- Agents that select this role template inherit the responsibilities below. -->
`;

// Blend CodeMirror into the zinc-950 chrome (drop the default panel bg). Kept in
// lock-step with the file editor's cmBlendTheme (components/files/CodeEditor.tsx)
// so the memory editor and the file editor render identically — same gutter,
// font, and selection. No height/font-size overrides here: oneDark's defaults
// (16px) carry through, exactly like the file editor.
const cmBlendTheme = EditorView.theme({
  '&': { backgroundColor: 'transparent !important', color: '#e4e4e7' },
  '.cm-gutters': {
    backgroundColor: 'transparent !important',
    borderRight: '1px solid rgba(255,255,255,0.04)',
    color: 'rgba(228,228,231,0.35)',
  },
  // Dim line numbers so they read clearly below the content (#e4e4e7); target
  // the number elements directly + !important to beat oneDark's own rule.
  '.cm-lineNumbers .cm-gutterElement': { color: 'rgba(228,228,231,0.26) !important' },
  '.cm-activeLineGutter .cm-gutterElement, .cm-activeLineGutter': { color: 'rgba(228,228,231,0.55) !important' },
  '.cm-activeLineGutter, .cm-activeLine': { backgroundColor: 'rgba(255,255,255,0.03)' },
  '.cm-selectionBackground, ::selection': { backgroundColor: 'rgba(59,130,246,0.25) !important' },
  '.cm-cursor': { borderLeftColor: '#e4e4e7' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
});

// Matches the file editor's BASIC_SETUP_EDIT so the two editors share gutter,
// fold, and bracket behavior.
const BASIC_SETUP = {
  lineNumbers: true,
  highlightActiveLine: true,
  foldGutter: true,
  bracketMatching: true,
  closeBrackets: true,
  autocompletion: false,
} as const;

function selKey(s: Selection): string {
  return `${s.scope}:${s.name}`;
}

export default function MemoryView({ agentId, className }: MemoryViewProps) {
  const { t } = useTranslation('agentInspector');
  const [projects, setProjects] = useState<string[]>([]);
  const [roles, setRoles] = useState<string[]>([]);
  const [roleDirs, setRoleDirs] = useState<RoleDir[]>([]);
  const [listLoading, setListLoading] = useState(true);

  const [selected, setSelected] = useState<Selection>({ scope: 'global', name: 'global' });
  const [content, setContent] = useState('');
  const [savedContent, setSavedContent] = useState('');
  const [docLoading, setDocLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  // Memory docs are markdown — default to rendered preview, same as the file
  // editor; toggle to the CodeMirror source via the header button.
  const [previewMd, setPreviewMd] = useState(false);
  // Soft-wrap toggle (default off — long lines scroll), matching the file editor.
  const [wrap, setWrap] = useState(false);
  // Live CodeMirror handle for the editor-content context menu's clipboard ops.
  const cmRef = useRef<ReactCodeMirrorRef>(null);
  // Right-click menu over the editor CONTENT (copy/cut/paste/wrap), distinct from
  // ctxMenu which is the file-list send-to-agent menu.
  const [editorMenu, setEditorMenu] = useState<{ x: number; y: number } | null>(null);

  // Inline slug editor — creating a new template, or renaming an existing one.
  const [editing, setEditing] = useState<
    { mode: 'new' | 'rename'; scope: 'project' | 'role'; oldName?: string } | null
  >(null);
  const [draftSlug, setDraftSlug] = useState('');
  const addInputRef = useRef<HTMLInputElement | null>(null);

  // Per-section collapse (matches the file explorer's collapsible sections).
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const toggleSection = useCallback((k: string) => {
    setCollapsed((c) => ({ ...c, [k]: !c[k] }));
  }, []);

  // Right-click → "发送给当前 agent" (mirrors the file explorer). Resolves the
  // memory file's real path from the template API, then types `file://<path>`
  // into the agent's tmux pane WITHOUT submitting (no Enter). It does NOT use
  // /api/fs/send-path: that endpoint clamps paths to the agent's workspace, but
  // memory files (global/project/role) live under ~/cicy-ai/memory, outside it.
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; sel: Selection } | null>(null);
  useEffect(() => {
    if (!ctxMenu) return;
    const close = () => setCtxMenu(null);
    window.addEventListener('click', close);
    window.addEventListener('scroll', close, true);
    window.addEventListener('blur', close);
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('blur', close);
      window.removeEventListener('keydown', onKey);
    };
  }, [ctxMenu]);

  // Dismiss the editor-content menu on any click/scroll/blur/Escape.
  useEffect(() => {
    if (!editorMenu) return;
    const close = () => setEditorMenu(null);
    window.addEventListener('click', close);
    window.addEventListener('scroll', close, true);
    window.addEventListener('blur', close);
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('blur', close);
      window.removeEventListener('keydown', onKey);
    };
  }, [editorMenu]);

  // Clipboard ops for the editor-content menu, sharing the file editor's helpers.
  const editorCopy = () => { const v = cmRef.current?.view; if (v) cmCopySelection(v); };
  const editorCut = () => { const v = cmRef.current?.view; if (v) cmCutSelection(v); };
  const editorPaste = () => { const v = cmRef.current?.view; if (v) cmPasteSelection(v); };

  const sendToAgent = useCallback(async (sel: Selection) => {
    try {
      const name = sel.scope === 'global' ? undefined : sel.name;
      const { data } = await apiService.getMemoryTemplate(sel.scope, name);
      const path = String(data?.path || '').trim();
      if (!path) return;
      // Same `file://<abs-path-without-leading-slash>` form the file explorer's
      // code.send_path handler emits, so the agent resolves it identically.
      const promptText = `file://${path.replace(/^\/+/, '')}`;
      window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: agentId, q: promptText } }));
      // Routes by agent type — cicy agents get it in their chat composer, terminal
      // agents typed into tmux (both WITHOUT auto-submit, for the user to review).
      await dispatchToAgent(agentId, promptText, { submit: false });
    } catch {
      /* best-effort */
    }
  }, [agentId]);

  const dirty = content !== savedContent;
  // Live refs so the debounced auto-save always persists the CURRENT selection
  // and buffer, even if state changed since the timer was armed.
  const selectedRef = useRef(selected);
  selectedRef.current = selected;
  const contentRef = useRef(content);
  contentRef.current = content;
  const savedRef = useRef(savedContent);
  savedRef.current = savedContent;
  const saveTimer = useRef<number | null>(null);

  const refreshList = useCallback(async () => {
    setListLoading(true);
    try {
      const { data } = await apiService.listMemoryTemplates();
      setProjects(Array.isArray(data?.projects) ? data.projects : []);
      setRoles(Array.isArray(data?.roles) ? data.roles : []);
      setRoleDirs(
        Array.isArray(data?.roleDirs)
          ? data.roleDirs
              .filter((d: any) => d && typeof d.slug === 'string')
              .map((d: any) => ({ slug: d.slug, files: Array.isArray(d.files) ? d.files : [] }))
          : [],
      );
    } catch {
      /* keep stale list on transient failure */
    } finally {
      setListLoading(false);
    }
  }, []);

  useEffect(() => {
    refreshList();
  }, [refreshList]);

  // Reset selection to the global file whenever the agent changes.
  useEffect(() => {
    setSelected({ scope: 'global', name: 'global' });
  }, [agentId]);

  // Load the selected document.
  const loadDoc = useCallback(async (sel: Selection) => {
    setDocLoading(true);
    setError('');
    try {
      const name = sel.scope === 'global' ? undefined : sel.name;
      const { data } = await apiService.getMemoryTemplate(sel.scope, name);
      const text = typeof data?.content === 'string' ? data.content : '';
      setContent(text);
      setSavedContent(text);
    } catch (e: any) {
      setError(e?.message || 'load failed');
      setContent('');
      setSavedContent('');
    } finally {
      setDocLoading(false);
    }
  }, []);

  useEffect(() => {
    loadDoc(selected);
  }, [selected, loadDoc]);

  // Persist the current buffer immediately (used by the debounce, by Cmd/Ctrl+S,
  // and before switching files). Reads refs so it always saves the selection the
  // edits belong to, never a stale one.
  const flushSave = useCallback(async () => {
    if (saveTimer.current) {
      clearTimeout(saveTimer.current);
      saveTimer.current = null;
    }
    const sel = selectedRef.current;
    const body = contentRef.current;
    if (body === savedRef.current) return;
    setSaving(true);
    setError('');
    try {
      const name = sel.scope === 'global' ? '' : sel.name;
      await apiService.saveMemoryTemplate(sel.scope, name, body);
      // Only the selection these edits belong to gets its saved-mark advanced.
      if (selKey(selectedRef.current) === selKey(sel)) {
        setSavedContent(body);
      }
    } catch (e: any) {
      setError(e?.message || 'save failed');
    } finally {
      setSaving(false);
    }
  }, []);

  // Debounced auto-save: persist ~700ms after typing stops.
  useEffect(() => {
    if (docLoading) return;
    if (content === savedContent) return;
    const id = window.setTimeout(() => {
      void flushSave();
    }, 700);
    saveTimer.current = id;
    return () => clearTimeout(id);
  }, [content, savedContent, docLoading, flushSave]);

  // Flush any pending edits when the component unmounts.
  useEffect(() => {
    return () => {
      void flushSave();
    };
  }, [flushSave]);

  const handleSelect = useCallback(
    async (sel: Selection) => {
      if (selKey(sel) === selKey(selected)) return;
      await flushSave(); // persist the current file before switching away
      setEditing(null);
      setSelected(sel);
    },
    [selected, flushSave],
  );

  // External jump (the team roster's "role" button → cicy:open-role {slug}): open
  // that role's primary file directly and expand its folder, so the roster can hop
  // between agents' role templates in this drawer. Defers until roleDirs has loaded
  // (so the file list is known); a slug arriving early is parked in pendingRoleRef.
  const pendingRoleRef = useRef<string | null>(null);
  const openRoleSlug = useCallback((slug: string) => {
    const rd = roleDirs.find((r) => r.slug === slug);
    if (!rd || rd.files.length === 0) { pendingRoleRef.current = slug; return; }
    pendingRoleRef.current = null;
    const file = rd.files.includes('role.md') ? 'role.md'
      : rd.files.includes('role.zh.md') ? 'role.zh.md'
      : rd.files[0];
    setCollapsed((prev) => ({ ...prev, [`rolefolder:${slug}`]: false }));
    void handleSelect({ scope: 'role', name: `${slug}/${file}` });
  }, [roleDirs, handleSelect]);

  useEffect(() => {
    const onOpenRole = (e: Event) => {
      const slug = (e as CustomEvent).detail?.slug;
      if (typeof slug === 'string' && slug.trim()) openRoleSlug(slug.trim());
    };
    window.addEventListener('cicy:open-role', onOpenRole as EventListener);
    return () => window.removeEventListener('cicy:open-role', onOpenRole as EventListener);
  }, [openRoleSlug]);

  // Resolve a parked jump once roleDirs finishes loading.
  useEffect(() => {
    if (pendingRoleRef.current) openRoleSlug(pendingRoleRef.current);
  }, [roleDirs, openRoleSlug]);

  // Cmd/Ctrl+S forces an immediate save.
  const saveKeymap = useMemo(
    () =>
      keymap.of([
        {
          key: 'Mod-s',
          run: () => {
            void flushSave();
            return true;
          },
          preventDefault: true,
        },
      ]),
    [flushSave],
  );

  const beginAdd = useCallback((scope: 'project' | 'role') => {
    setCollapsed((c) => ({ ...c, [scope]: false }));
    setEditing({ mode: 'new', scope });
    setDraftSlug('');
    setTimeout(() => addInputRef.current?.focus(), 0);
  }, []);

  const beginRename = useCallback((scope: 'project' | 'role', oldName: string) => {
    setEditing({ mode: 'rename', scope, oldName });
    setDraftSlug(oldName);
    setTimeout(() => {
      addInputRef.current?.focus();
      addInputRef.current?.select();
    }, 0);
  }, []);

  const cancelEdit = useCallback(() => {
    setEditing(null);
    setDraftSlug('');
  }, []);

  const commitEdit = useCallback(async () => {
    const ed = editing;
    if (!ed) return;
    const slug = draftSlug.trim();
    setEditing(null);
    setDraftSlug('');
    if (!slug) return;
    if (ed.mode === 'rename' && slug === ed.oldName) return; // unchanged
    const list = ed.scope === 'project' ? projects : roles;
    if (slug !== ed.oldName && list.includes(slug)) {
      setError(t('memNameTaken', { name: slug }));
      return;
    }
    try {
      let finalName = slug;
      if (ed.mode === 'new') {
        const seed = ed.scope === 'project' ? NEW_PROJECT_SEED : NEW_ROLE_SEED;
        const res = await apiService.saveMemoryTemplate(ed.scope, slug, seed);
        finalName = res?.data?.name || slug;
      } else {
        // Rename = copy the file's content to the new slug, drop the old one.
        const { data } = await apiService.getMemoryTemplate(ed.scope, ed.oldName!);
        const body = typeof data?.content === 'string' ? data.content : '';
        const res = await apiService.saveMemoryTemplate(ed.scope, slug, body);
        finalName = res?.data?.name || slug;
        if (finalName !== ed.oldName) {
          await apiService.deleteMemoryTemplate(ed.scope, ed.oldName!);
        }
      }
      await refreshList();
      setSelected({ scope: ed.scope, name: finalName });
    } catch (e: any) {
      setError(e?.message || 'operation failed');
    }
  }, [editing, draftSlug, projects, roles, refreshList, t]);

  const remove = useCallback(
    async (scope: 'project' | 'role', name: string) => {
      if (!window.confirm(t('memDeleteConfirm', { name }))) return;
      try {
        await apiService.deleteMemoryTemplate(scope, name);
        await refreshList();
        if (selected.scope === scope && selected.name === name) {
          setSelected({ scope: 'global', name: 'global' });
        }
      } catch (e: any) {
        setError(e?.message || 'delete failed');
      }
    },
    [t, refreshList, selected, agentId],
  );

  const renderItem = (
    sel: Selection,
    label: string,
    icon: React.ReactNode,
    actions?: { onDelete: () => void; onRename?: () => void; placeholder: string },
  ) => {
    // While this exact row is being renamed, swap it for the inline input.
    if (
      actions?.onRename &&
      editing?.mode === 'rename' &&
      editing.scope === sel.scope &&
      editing.oldName === sel.name
    ) {
      return (
        <SlugInput
          key={selKey(sel)}
          dataId={`memory-view-item-rename-input-${selKey(sel)}`}
          inputRef={addInputRef}
          value={draftSlug}
          placeholder={actions.placeholder}
          onChange={setDraftSlug}
          onCommit={commitEdit}
          onCancel={cancelEdit}
        />
      );
    }
    const active = selKey(sel) === selKey(selected);
    return (
      <div
        key={selKey(sel)}
        data-id={`memory-view-item-${selKey(sel)}`}
        className={`group flex items-center gap-1.5 px-2 py-0.5 cursor-pointer text-[13px] ${
          active ? 'bg-blue-500/15 text-blue-200' : 'text-zinc-300 hover:bg-zinc-800/60'
        }`}
        onClick={() => handleSelect(sel)}
        onDoubleClick={() => actions?.onRename?.()}
        onContextMenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setCtxMenu({ x: e.clientX, y: e.clientY, sel });
        }}
      >
        <span data-id={`memory-view-item-icon-${selKey(sel)}`} className="shrink-0 text-zinc-500">{icon}</span>
        <span data-id={`memory-view-item-label-${selKey(sel)}`} className="flex-1 truncate">{label}</span>
        {actions && (
          <>
            {actions.onRename && (
              <button
                data-id={`memory-view-item-rename-${selKey(sel)}`}
                className="opacity-0 group-hover:opacity-100 text-zinc-500 hover:text-zinc-200 transition"
                title={t('memRename')}
                onClick={(e) => {
                  e.stopPropagation();
                  actions.onRename?.();
                }}
              >
                <Pencil size={12} />
              </button>
            )}
            <button
              data-id={`memory-view-item-delete-${selKey(sel)}`}
              className="opacity-0 group-hover:opacity-100 text-zinc-500 hover:text-red-400 transition"
              title={t('memDelete')}
              onClick={(e) => {
                e.stopPropagation();
                actions.onDelete();
              }}
            >
              <Trash2 size={12} />
            </button>
          </>
        )}
      </div>
    );
  };

  // Collapsible bordered section, matching the file explorer's multi-root
  // sections (border-t separator + chevron + uppercase label + count).
  const renderSection = (
    key: string,
    label: string,
    count: number | undefined,
    onAdd: (() => void) | undefined,
    children: React.ReactNode,
    first?: boolean,
  ) => {
    const isCollapsed = !!collapsed[key];
    return (
      <div data-id={`memory-view-section-${key}`} className={`${first ? '' : 'border-t border-zinc-800'} mt-1 pt-1`}>
        <div data-id={`memory-view-section-header-${key}`} className="flex items-center px-2 pt-1 pb-0.5">
          <button
            data-id={`memory-view-section-toggle-${key}`}
            onClick={() => toggleSection(key)}
            className="flex-1 flex items-center gap-1 text-[10px] uppercase tracking-wide text-zinc-500 hover:text-zinc-300 font-medium select-none"
          >
            {isCollapsed ? <ChevronRight className="w-3 h-3" /> : <ChevronDown className="w-3 h-3" />}
            <span data-id={`memory-view-section-label-${key}`}>{label}</span>
            {typeof count === 'number' && (
              <span data-id={`memory-view-section-count-${key}`} className="ml-1 text-zinc-600 normal-case">({count})</span>
            )}
          </button>
          {onAdd && (
            <button
              data-id={`memory-view-section-add-${key}`}
              className="text-zinc-500 hover:text-zinc-200 transition"
              title={t('memNew')}
              onClick={onAdd}
            >
              <Plus size={13} />
            </button>
          )}
        </div>
        {!isCollapsed && children}
      </div>
    );
  };

  // A role is a FOLDER (~/cicy-ai/memory/agents/<slug>/) holding role.md /
  // role.zh.md / meta.yaml. Render it as a collapsible folder whose children are
  // its files; each file's Selection.name is `<slug>/<file>` so the API edits the
  // exact file. Deleting role.md removes the role's English persona, so per-file
  // delete is offered on every file.
  const renderRoleFolder = (rd: RoleDir) => {
    const fkey = `rolefolder:${rd.slug}`;
    const folderCollapsed = !!collapsed[fkey];
    return (
      <div data-id={`memory-view-role-folder-${rd.slug}`} key={rd.slug}>
        <button
          data-id={`memory-view-role-folder-toggle-${rd.slug}`}
          onClick={() => toggleSection(fkey)}
          className="w-full flex items-center gap-1 px-2 py-0.5 text-[13px] text-zinc-300 hover:bg-zinc-800/60 select-none"
        >
          {folderCollapsed ? <ChevronRight className="w-3 h-3 shrink-0 text-zinc-500" /> : <ChevronDown className="w-3 h-3 shrink-0 text-zinc-500" />}
          {folderCollapsed ? <Folder size={14} className="shrink-0 text-amber-400" /> : <FolderOpen size={14} className="shrink-0 text-amber-400" />}
          <span className="flex-1 truncate text-left">{rd.slug}</span>
        </button>
        {!folderCollapsed && (
          <div data-id={`memory-view-role-folder-files-${rd.slug}`} className="pl-4">
            {rd.files.map((file) =>
              renderItem(
                { scope: 'role', name: `${rd.slug}/${file}` },
                file,
                <FileText size={14} />,
                {
                  onDelete: () => remove('role', `${rd.slug}/${file}`),
                  onRename: () => {},
                  placeholder: '',
                },
              ),
            )}
          </div>
        )}
      </div>
    );
  };

  return (
    <div data-id="memory-view" className={`flex h-full w-full overflow-hidden ${className || ''}`}>
      {/* Left: sectioned list (file-explorer style) */}
      <div data-id="memory-view-list" className="w-56 shrink-0 border-r border-zinc-800 overflow-y-auto py-1 bg-zinc-900/30">
        {renderSection(
          'global',
          t('memSectionGlobal'),
          undefined,
          undefined,
          renderItem({ scope: 'global', name: 'global' }, 'global.md', <Globe size={14} />),
          true,
        )}

        {renderSection(
          'project',
          t('memSectionProject'),
          projects.length,
          () => beginAdd('project'),
          <>
            {projects.map((slug) =>
              renderItem({ scope: 'project', name: slug }, `${slug}.md`, <FolderGit2 size={14} />, {
                onDelete: () => remove('project', slug),
                onRename: () => beginRename('project', slug),
                placeholder: t('memNewProjectPlaceholder'),
              }),
            )}
            {editing?.mode === 'new' && editing.scope === 'project' && (
              <SlugInput
                dataId="memory-view-new-project-input"
                inputRef={addInputRef}
                value={draftSlug}
                placeholder={t('memNewProjectPlaceholder')}
                onChange={setDraftSlug}
                onCommit={commitEdit}
                onCancel={cancelEdit}
              />
            )}
          </>,
        )}

        {renderSection(
          'role',
          t('memSectionRole'),
          roleDirs.length,
          () => beginAdd('role'),
          <>
            {roleDirs.map((rd) => renderRoleFolder(rd))}
            {editing?.mode === 'new' && editing.scope === 'role' && (
              <SlugInput
                dataId="memory-view-new-role-input"
                inputRef={addInputRef}
                value={draftSlug}
                placeholder={t('memNewRolePlaceholder')}
                onChange={setDraftSlug}
                onCommit={commitEdit}
                onCancel={cancelEdit}
              />
            )}
          </>,
        )}

        {listLoading && (
          <div data-id="memory-view-list-loading" className="px-2 py-2 text-xs text-zinc-600 flex items-center gap-1">
            <Loader2 size={12} className="animate-spin" /> {t('memLoading')}
          </div>
        )}
      </div>

      {/* Right: editor (no toolbar — auto-saves on edit) */}
      <div data-id="memory-view-editor" className="relative flex-1 min-w-0 flex flex-col">
        {/* Unobtrusive auto-save status — only while saving or when unsaved;
            nothing is shown once the buffer is persisted. */}
        {(saving || dirty) && (
          <div data-id="memory-view-save-status" className="pointer-events-none absolute top-1.5 right-10 z-10 text-[11px] select-none">
            {saving ? (
              <span data-id="memory-view-saving" className="flex items-center gap-1 text-zinc-500">
                <Loader2 size={11} className="animate-spin" /> {t('memSaving')}
              </span>
            ) : (
              <span data-id="memory-view-unsaved" className="text-amber-400/70">{t('memUnsaved')}</span>
            )}
          </div>
        )}

        {error && <div data-id="memory-view-error" className="px-3 py-1.5 text-xs text-red-400 bg-red-500/5">{error}</div>}

        {/* Markdown preview / source toggle — same affordance as the file editor. */}
        {!docLoading && (
          <button
            data-id="memory-view-preview-toggle"
            type="button"
            onClick={() => setPreviewMd((v) => !v)}
            title={previewMd ? t('memToSource', { defaultValue: '切换到源码' }) : t('memToPreview', { defaultValue: '切换到预览' })}
            aria-pressed={previewMd}
            className="absolute top-1 right-2 z-20 flex items-center rounded p-1 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 transition-colors"
          >
            {previewMd ? <FileCode className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
          </button>
        )}

        <div data-id="memory-view-editor-body" className="flex-1 min-h-0 overflow-hidden">
          {docLoading ? (
            <div data-id="memory-view-doc-loading" className="h-full flex items-center justify-center text-zinc-600 text-[13px] gap-2">
              <Loader2 size={14} className="animate-spin" /> {t('memLoading')}
            </div>
          ) : previewMd ? (
            <div data-id="memory-view-preview" className="h-full overflow-y-auto">
              <MarkdownPreview source={content} />
            </div>
          ) : (
            <div
              className="h-full"
              onContextMenu={(e) => {
                e.preventDefault();
                e.stopPropagation();
                setEditorMenu({ x: e.clientX, y: e.clientY });
              }}
            >
              <CodeMirror
                ref={cmRef}
                value={content}
                height="100%"
                theme={oneDark}
                basicSetup={BASIC_SETUP}
                extensions={[markdown(), CLIPBOARD_KEYMAP, saveKeymap, cmBlendTheme, ...(wrap ? [EditorView.lineWrapping] : [])]}
                onChange={setContent}
                className="h-full"
                style={{ height: '100%' }}
              />
            </div>
          )}
        </div>
      </div>

      {ctxMenu && (
        <div
          data-id="memory-view-context-menu"
          className="fixed z-[200] min-w-[160px] rounded-md border border-zinc-700 bg-zinc-900 py-1 shadow-lg"
          style={{ top: ctxMenu.y, left: ctxMenu.x }}
          onClick={(e) => e.stopPropagation()}
          onContextMenu={(e) => e.preventDefault()}
        >
          <button
            data-id="memory-view-context-send-to-agent"
            type="button"
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[13px] text-zinc-200 hover:bg-zinc-800"
            onClick={() => {
              const sel = ctxMenu.sel;
              setCtxMenu(null);
              void sendToAgent(sel);
            }}
          >
            <Send className="h-3.5 w-3.5 text-zinc-400" />
            {t('memSendToAgent', { defaultValue: '发送给当前 agent' })}
          </button>
        </div>
      )}

      {editorMenu && (
        <div
          data-id="memory-view-editor-context-menu"
          className="fixed z-[200] min-w-[160px] rounded-md border border-zinc-700 bg-zinc-900 py-1 shadow-lg text-[13px] text-zinc-200"
          style={{ top: editorMenu.y, left: editorMenu.x }}
          onClick={(e) => e.stopPropagation()}
          onContextMenu={(e) => e.preventDefault()}
        >
          <button
            data-id="memory-view-editor-context-copy"
            type="button"
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-zinc-800"
            onClick={() => { editorCopy(); setEditorMenu(null); }}
          >
            <Copy className="h-3.5 w-3.5 text-zinc-400" />
            <span className="flex-1">{t('memCopy', { defaultValue: '复制' })}</span>
            <span className="text-[10px] text-zinc-500">Cmd+C</span>
          </button>
          <button
            data-id="memory-view-editor-context-cut"
            type="button"
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-zinc-800"
            onClick={() => { editorCut(); setEditorMenu(null); }}
          >
            <Scissors className="h-3.5 w-3.5 text-zinc-400" />
            <span className="flex-1">{t('memCut', { defaultValue: '剪切' })}</span>
            <span className="text-[10px] text-zinc-500">Cmd+X</span>
          </button>
          <button
            data-id="memory-view-editor-context-paste"
            type="button"
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-zinc-800"
            onClick={() => { editorPaste(); setEditorMenu(null); }}
          >
            <ClipboardPaste className="h-3.5 w-3.5 text-zinc-400" />
            <span className="flex-1">{t('memPaste', { defaultValue: '粘贴' })}</span>
            <span className="text-[10px] text-zinc-500">Cmd+V</span>
          </button>
          <div className="my-1 border-t border-zinc-700/70" />
          <button
            data-id="memory-view-editor-context-wrap"
            type="button"
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-zinc-800"
            onClick={() => { setWrap((v) => !v); setEditorMenu(null); }}
          >
            {wrap ? <Check className="h-3.5 w-3.5 text-zinc-400" /> : <WrapText className="h-3.5 w-3.5 text-zinc-400" />}
            <span className="flex-1">{t('memWordWrap', { defaultValue: '自动换行' })}</span>
          </button>
        </div>
      )}
    </div>
  );
}

function SlugInput({
  inputRef,
  value,
  placeholder,
  onChange,
  onCommit,
  onCancel,
  dataId,
}: {
  inputRef: React.RefObject<HTMLInputElement | null>;
  value: string;
  placeholder: string;
  onChange: (v: string) => void;
  onCommit: () => void;
  onCancel: () => void;
  dataId?: string;
}) {
  // Track IME composition so an Enter that confirms a Chinese candidate does
  // NOT also submit the slug. nativeEvent.isComposing covers most browsers;
  // the keyCode === 229 check is the legacy/Safari fallback.
  const composingRef = useRef(false);
  return (
    <div data-id={dataId || 'memory-view-slug-input'} className="px-2 py-0.5">
      <input
        data-id={`${dataId || 'memory-view-slug-input'}-field`}
        ref={inputRef}
        value={value}
        placeholder={placeholder}
        spellCheck={false}
        className="block w-full rounded-sm bg-zinc-950 border border-blue-500/50 px-1.5 text-[13px] leading-5 text-zinc-100 outline-none"
        onChange={(e) => onChange(e.target.value)}
        onCompositionStart={() => {
          composingRef.current = true;
        }}
        onCompositionEnd={() => {
          composingRef.current = false;
        }}
        onKeyDown={(e) => {
          if (composingRef.current || e.nativeEvent.isComposing || e.keyCode === 229) return;
          if (e.key === 'Enter') {
            e.preventDefault();
            onCommit();
          } else if (e.key === 'Escape') {
            e.preventDefault();
            onCancel();
          }
        }}
        onBlur={onCommit}
      />
    </div>
  );
}
