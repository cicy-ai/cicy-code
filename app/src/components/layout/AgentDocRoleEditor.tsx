import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { EditorView, keymap } from '@codemirror/view';
import { oneDark } from '@codemirror/theme-one-dark';
import { markdown } from '@codemirror/lang-markdown';
import { Loader2, Check, AlertCircle, Users } from 'lucide-react';
import apiService from '../../services/api';

// The roster drawer's per-agent editor: THREE tabs across the top —
//   [ CLAUDE.md / AGENTS.md ] [ meta.yaml ] [ system.md ]
// Tab 1 is the agent's OWN guidance doc (per-agent, DB-resolved via scope 'agent').
// Tabs 2–3 are the SHARED role-template files (scope 'role', <slug>/<file>) — many
// agents share one role dir, so editing them affects every agent on that role.
// meta.yaml/system.md only appear when the agent selects a role template.
// One CodeMirror shows the active tab; switching flushes the prior buffer.

interface Props {
  paneId: string;
  className?: string;
}

const cmBlendTheme = EditorView.theme({
  '&': { backgroundColor: 'transparent !important', color: '#e4e4e7' },
  '.cm-gutters': {
    backgroundColor: 'transparent !important',
    borderRight: '1px solid rgba(255,255,255,0.04)',
    color: 'rgba(228,228,231,0.35)',
  },
  '.cm-lineNumbers .cm-gutterElement': { color: 'rgba(228,228,231,0.26) !important' },
  '.cm-activeLineGutter .cm-gutterElement, .cm-activeLineGutter': { color: 'rgba(228,228,231,0.55) !important' },
  '.cm-activeLineGutter, .cm-activeLine': { backgroundColor: 'rgba(255,255,255,0.03)' },
  '.cm-selectionBackground, ::selection': { backgroundColor: 'rgba(59,130,246,0.25) !important' },
  '.cm-cursor': { borderLeftColor: '#e4e4e7' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
});

const BASIC_SETUP = {
  lineNumbers: true,
  highlightActiveLine: true,
  foldGutter: true,
  bracketMatching: true,
  closeBrackets: true,
  autocompletion: false,
} as const;

// One template file's edit lifecycle (load + autosave + ⌘S + flush-on-switch),
// keyed by (scope,name). Flushes the prior file before loading the next.
function useTemplateFile(scope: string | null, name: string | null) {
  const [content, setContent] = useState('');
  const [saved, setSaved] = useState('');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [missing, setMissing] = useState(false);

  const key = scope && name ? `${scope}::${name}` : '';
  const contentRef = useRef('');
  const savedRef = useRef('');
  const timer = useRef<number | null>(null);
  const owner = useRef<{ scope: string; name: string } | null>(null);

  useEffect(() => { contentRef.current = content; }, [content]);
  useEffect(() => { savedRef.current = saved; }, [saved]);

  const saveOwner = useCallback(async () => {
    if (timer.current) { clearTimeout(timer.current); timer.current = null; }
    const o = owner.current;
    if (!o) return;
    if (contentRef.current === savedRef.current) return;
    setSaving(true);
    setError('');
    try {
      await apiService.saveMemoryTemplate(o.scope, o.name, contentRef.current);
      setSaved(contentRef.current);
    } catch (e: any) {
      setError(e?.message || 'save failed');
    } finally {
      setSaving(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void saveOwner();
    if (!scope || !name) {
      owner.current = null;
      setContent(''); setSaved(''); setMissing(false); setError('');
      return;
    }
    setLoading(true); setError(''); setMissing(false);
    apiService.getMemoryTemplate(scope, name)
      .then(({ data }: any) => {
        if (cancelled) return;
        const text = typeof data?.content === 'string' ? data.content : '';
        owner.current = { scope, name };
        setContent(text); setSaved(text);
        if (data?.exists === false) setMissing(true);
      })
      .catch((e: any) => {
        if (cancelled) return;
        setError(e?.message || 'load failed');
        setContent(''); setSaved('');
      })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  useEffect(() => {
    if (loading) return;
    if (content === saved) return;
    const id = window.setTimeout(() => { void saveOwner(); }, 700);
    timer.current = id;
    return () => clearTimeout(id);
  }, [content, saved, loading, saveOwner]);

  useEffect(() => () => { void saveOwner(); }, [saveOwner]);

  return { content, setContent, loading, saving, error, missing, dirty: content !== saved, save: saveOwner };
}

interface TabDef { key: string; label: string; scope: 'agent' | 'role'; name: string; shared: boolean; }

export default function AgentDocRoleEditor({ paneId, className }: Props) {
  const [agentType, setAgentType] = useState('');
  const [roleSlug, setRoleSlug] = useState('');
  const [tab, setTab] = useState('doc');

  // Resolve this agent's type (→ doc filename) + role template slug (→ meta/system).
  useEffect(() => {
    let cancelled = false;
    setTab('doc');
    apiService.getPane(paneId)
      .then(({ data }: any) => {
        if (cancelled) return;
        setAgentType(String(data?.agent_type || ''));
        setRoleSlug(String(data?.role_template || '').trim());
      })
      .catch(() => { if (!cancelled) { setAgentType(''); setRoleSlug(''); } });
    return () => { cancelled = true; };
  }, [paneId]);

  const docName = agentType.toLowerCase() === 'claude' ? 'CLAUDE.md' : 'AGENTS.md';
  // Every agent gets a role template: agents without their own role_template fall
  // back to the universal `assistant` role, so meta.yaml / system.md are always
  // editable (they edit the shared assistant template).
  const slug = roleSlug.trim() || 'assistant';

  const tabs: TabDef[] = useMemo(() => [
    { key: 'doc', label: docName, scope: 'agent', name: paneId, shared: false },
    { key: 'meta', label: 'meta.yaml', scope: 'role', name: `${slug}/meta.yaml`, shared: true },
    { key: 'system', label: 'system.md', scope: 'role', name: `${slug}/system.md`, shared: true },
  ], [paneId, docName, slug]);

  const active = tabs.find((x) => x.key === tab) || tabs[0];
  const file = useTemplateFile(active?.scope || null, active?.name || null);
  const saveKeymap = useMemo(() => keymap.of([{ key: 'Mod-s', preventDefault: true, run: () => { void file.save(); return true; } }]), [file]);

  return (
    <div data-id="agent-doc-role-editor" className={`flex h-full w-full flex-col overflow-hidden bg-[#0b0b0d] ${className || ''}`}>
      {/* top: 3 tabs + save state */}
      <div data-id="agent-doc-role-tabs" className="flex h-9 shrink-0 items-center justify-between border-b border-zinc-800 px-2">
        <div className="flex min-w-0 items-center gap-1">
          {tabs.map((tb) => (
            <button
              key={tb.key}
              data-id={`agent-doc-role-tab-${tb.key}`}
              type="button"
              onClick={() => setTab(tb.key)}
              className={`rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors ${
                active?.key === tb.key ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
              }`}
            >
              {tb.label}
            </button>
          ))}
        </div>
        {file.saving ? (
          <span className="flex items-center gap-1 text-[11px] text-zinc-500"><Loader2 className="h-3 w-3 animate-spin" /> 保存中</span>
        ) : file.dirty ? (
          <span className="text-[11px] text-amber-400/80">未保存</span>
        ) : (
          <span className="flex items-center gap-1 text-[11px] text-emerald-400/70"><Check className="h-3 w-3" /> 已保存</span>
        )}
      </div>

      {/* shared-template notice for meta.yaml / system.md */}
      {active?.shared ? (
        <div className="flex items-center gap-1.5 border-b border-amber-500/15 bg-amber-500/[0.06] px-3 py-1 text-[11px] text-amber-300/80">
          <Users className="h-3 w-3 shrink-0" /> 共享模板 {slug} · 改动影响所有选用此角色的 agent
        </div>
      ) : null}
      {file.error ? (
        <div className="flex items-center gap-1.5 border-b border-rose-500/20 bg-rose-500/10 px-3 py-1 text-[11px] text-rose-300">
          <AlertCircle className="h-3 w-3 shrink-0" /> {file.error}
        </div>
      ) : file.missing ? (
        <div className="flex items-center gap-1.5 border-b border-amber-500/20 bg-amber-500/10 px-3 py-1 text-[11px] text-amber-300">
          <AlertCircle className="h-3 w-3 shrink-0" /> 该文件还不存在;保存即创建。
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-auto">
        {file.loading ? (
          <div className="flex h-full items-center justify-center text-[12px] text-zinc-600"><Loader2 className="mr-2 h-4 w-4 animate-spin" /> 加载中…</div>
        ) : (
          <CodeMirror
            value={file.content}
            onChange={file.setContent}
            theme={oneDark}
            extensions={[markdown(), EditorView.lineWrapping, cmBlendTheme, saveKeymap]}
            basicSetup={BASIC_SETUP}
            className="h-full text-[13px]"
          />
        )}
      </div>
    </div>
  );
}
