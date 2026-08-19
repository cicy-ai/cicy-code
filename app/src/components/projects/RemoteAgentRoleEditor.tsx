// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useMemo, useRef, useState } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { EditorView } from '@codemirror/view';
import { oneDark } from '@codemirror/theme-one-dark';
import { markdown } from '@codemirror/lang-markdown';
import { yaml } from '@codemirror/lang-yaml';
import { Check, Loader2 } from 'lucide-react';
import { cn } from '../../lib/utils';
import { cicySearch } from '../files/cmSearchPanel';

const SEARCH_EXT = cicySearch();

export type RemotePersonaField = 'guidance' | 'systemPrompt' | 'meta';
export interface RemotePersona {
  filename?: string;
  guidance?: string;
  systemPrompt?: string;
  meta?: string;
}

interface Props {
  cacheKey: string;
  agentType?: string;
  load: () => Promise<RemotePersona>;
  save: (field: RemotePersonaField, content: string) => Promise<RemotePersona>;
}

const CACHE_PREFIX = 'cicy_remote_agent_persona:v1:';
const memoryCache = new Map<string, RemotePersona>();
const emptyPersona = (): Required<RemotePersona> => ({ filename: 'AGENTS.md', guidance: '', systemPrompt: '', meta: '' });

const readCache = (key: string): RemotePersona | null => {
  const memory = memoryCache.get(key);
  if (memory) return memory;
  try {
    const parsed = JSON.parse(localStorage.getItem(`${CACHE_PREFIX}${key}`) || 'null');
    if (parsed && typeof parsed === 'object') {
      memoryCache.set(key, parsed);
      return parsed;
    }
  } catch { /* ignore invalid cache */ }
  return null;
};

const writeCache = (key: string, persona: RemotePersona) => {
  memoryCache.set(key, persona);
  try { localStorage.setItem(`${CACHE_PREFIX}${key}`, JSON.stringify(persona)); } catch { /* storage may be unavailable */ }
};

const editorTheme = EditorView.theme({
  '&': { height: '100%', backgroundColor: 'transparent !important', color: '#e4e4e7' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
  '.cm-gutters': { backgroundColor: 'transparent !important', borderRight: '1px solid rgba(255,255,255,0.04)', color: 'rgba(228,228,231,0.3)' },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'rgba(255,255,255,0.03)' },
  '.cm-cursor': { borderLeftColor: '#e4e4e7' },
});

export default function RemoteAgentRoleEditor({ cacheKey, agentType, load, save }: Props) {
  const cached = useMemo(() => readCache(cacheKey), [cacheKey]);
  const [persona, setPersona] = useState<Required<RemotePersona>>(() => ({ ...emptyPersona(), ...cached }));
  const [active, setActive] = useState<RemotePersonaField>('guidance');
  const [loading, setLoading] = useState(!cached);
  const [refreshing, setRefreshing] = useState(Boolean(cached));
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState('');
  const dirtyRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    const initial = readCache(cacheKey);
    dirtyRef.current = false;
    setActive('guidance');
    setSaved(false);
    setError('');
    if (initial) {
      setPersona({ ...emptyPersona(), ...initial });
      setLoading(false);
      setRefreshing(true);
    } else {
      setPersona(emptyPersona());
      setLoading(true);
      setRefreshing(false);
    }
    load()
      .then((data) => {
        if (cancelled) return;
        const next = { ...emptyPersona(), ...data };
        writeCache(cacheKey, next);
        if (!dirtyRef.current) setPersona(next);
      })
      .catch((cause: any) => { if (!cancelled) setError(cause?.message || '加载角色失败'); })
      .finally(() => { if (!cancelled) { setLoading(false); setRefreshing(false); } });
    return () => { cancelled = true; };
    // load is intentionally keyed by the stable remote address, not callback identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cacheKey]);

  const tabs: Array<[RemotePersonaField, string]> = String(agentType || '').toLowerCase() === 'cicy'
    ? [['guidance', persona.filename], ['systemPrompt', 'system.md'], ['meta', 'meta.yaml']]
    : [['guidance', persona.filename]];

  const onSave = async () => {
    setSaving(true);
    setSaved(false);
    setError('');
    try {
      const data = await save(active, persona[active]);
      const next = { ...persona, ...data };
      setPersona(next);
      writeCache(cacheKey, next);
      dirtyRef.current = false;
      setSaved(true);
    } catch (cause: any) {
      setError(cause?.message || '保存角色失败');
    } finally {
      setSaving(false);
    }
  };

  const update = (value: string) => {
    dirtyRef.current = true;
    setSaved(false);
    setPersona((current) => {
      const next = { ...current, [active]: value };
      writeCache(cacheKey, next);
      return next;
    });
  };

  return (
    <div data-id="remote-agent-role-editor" className="flex h-full min-h-0 flex-col bg-[#0b0b0d]">
      <div className="flex h-9 shrink-0 items-center justify-between border-b border-zinc-800 px-2">
        <div className="flex min-w-0 items-center gap-1">
          {tabs.map(([key, label]) => <button key={key} type="button" data-id={`remote-agent-role-tab-${key}`} onClick={() => setActive(key)} className={cn('rounded-md px-2.5 py-1 text-[12px] font-medium transition-colors', active === key ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300')}>{label}</button>)}
        </div>
        <div className="flex items-center gap-2">
          {refreshing ? <span data-id="remote-agent-role-refreshing" className="inline-flex items-center gap-1 text-[10px] text-zinc-600"><Loader2 className="h-3 w-3 animate-spin" />刷新中</span> : null}
          <button type="button" data-id="remote-agent-role-save" disabled={loading || saving} onClick={() => void onSave()} className="inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[11px] text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-100 disabled:opacity-40">
            {saving ? <Loader2 className="h-3 w-3 animate-spin" /> : <Check className="h-3 w-3" />}{saving ? '保存中…' : saved ? '已保存' : '保存'}
          </button>
        </div>
      </div>
      {error ? <div data-id="remote-agent-role-error" className="shrink-0 border-b border-rose-500/20 bg-rose-500/10 px-3 py-1.5 text-[11px] text-rose-300">{error}</div> : null}
      {loading ? <div data-id="remote-agent-role-loading" className="grid min-h-0 flex-1 place-items-center text-zinc-500"><Loader2 className="h-4 w-4 animate-spin" /></div> : (
        <div className="min-h-0 flex-1">
          <CodeMirror aria-label={tabs.find(([key]) => key === active)?.[1]} data-id={`remote-agent-role-content-${active}`} value={persona[active]} onChange={update} extensions={[active === 'meta' ? yaml() : markdown(), ...SEARCH_EXT, EditorView.lineWrapping, editorTheme]} theme={oneDark} basicSetup={{ lineNumbers: true, highlightActiveLine: true, foldGutter: true, bracketMatching: true, closeBrackets: true, autocompletion: false }} height="100%" />
        </div>
      )}
    </div>
  );
}
