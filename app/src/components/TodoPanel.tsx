import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import {
  Circle, CheckCircle2, CircleSlash, Trash2, MoreHorizontal,
  Sparkles, Loader2, GripVertical, Search, ArrowRight, ArrowUp, Pencil, X as XIcon,
  Send, Users, RefreshCw, FlaskConical, UserPlus, LayoutGrid, List as ListIcon, Check,
} from 'lucide-react';
import apiService from '../services/api';

// The "发给 agent" prompts (promptTodo / promptDoing) and the align prompt
// (alignPrompt) live in the todoPanel locale files so the text the UI sends
// follows the operator's UI language. They use {{id}} / {{title}} interpolation.

export type TodoStatus = 'todo' | 'test' | 'done' | 'dropped';

interface Todo {
  id: string;
  title: string;
  status: TodoStatus;
  creator_id?: string;
  created_at: string;
  updated_at: string;
  pane_id?: string; // only present in all_agents mode
  _pending?: boolean; // optimistic card awaiting server confirmation
}

interface Counts {
  all: number;
  todo: number;
  test: number;
  done: number;
  dropped: number;
}

const COLUMNS: TodoStatus[] = ['todo', 'test', 'done', 'dropped'];
const POLL_MS = 5000;

interface Props {
  paneId: string;       // master pane id
  active: boolean;
  isMaster?: boolean;   // if true, can view all agents' todos
}

export default function TodoPanel({ paneId, active, isMaster }: Props) {
  // i18next's `t` is renamed to `tr` because this file uses `t` for the todo
  // object inside several `.map((t) => ...)` callbacks. Picking a different
  // name avoids shadowing without renaming dozens of `t.id` / `t.title`
  // references.
  const { t: tr } = useTranslation('todoPanel');
  const [todos, setTodos] = useState<Todo[]>([]);
  const [counts, setCounts] = useState<Counts>({ all: 0, todo: 0, test: 0, done: 0, dropped: 0 });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingDraft, setEditingDraft] = useState('');
  const [menuOpenId, setMenuOpenId] = useState<string | null>(null);
  const [menuPos, setMenuPos] = useState<{ top: number; right: number } | null>(null);
  const [aligning, setAligning] = useState<'idle' | 'sending' | 'sent'>('idle');
  const [manualRefreshing, setManualRefreshing] = useState(false);
  const [sendingTodoId, setSendingTodoId] = useState<string | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [batchSending, setBatchSending] = useState(false);
  const [draggingId, setDraggingId] = useState<string | null>(null);
  const [dragOverCol, setDragOverCol] = useState<TodoStatus | null>(null);
  const [showAllAgents, setShowAllAgents] = useState(false);
  const [filterAgent, setFilterAgent] = useState<string | null>(null); // null = all
  const [assignMenuId, setAssignMenuId] = useState<string | null>(null);
  const [assignMenuPos, setAssignMenuPos] = useState<{ top: number; right: number } | null>(null);
  const [workerPanes, setWorkerPanes] = useState<{ id: string; title: string }[]>([]);
  const [assigningId, setAssigningId] = useState<string | null>(null);
  const [isComposing, setIsComposing] = useState(false);
  const [viewMode, setViewMode] = useState<'kanban' | 'list'>(
    () => ((typeof localStorage !== 'undefined' && localStorage.getItem('cicy:todo-view')) as 'kanban' | 'list') || 'kanban',
  );
  const addInputRef = useRef<HTMLTextAreaElement>(null);
  // Auto-grow the composer textarea up to 2 lines (24px line-height), then scroll.
  const autoGrowComposer = (el: HTMLTextAreaElement | null) => {
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 48)}px`;
  };
  useEffect(() => { try { localStorage.setItem('cicy:todo-view', viewMode); } catch { /* ignore */ } }, [viewMode]);
  // Keep the composer's height in sync with its value — covers both typing and
  // the programmatic clear after submit (controlled value → '' collapses to 1 line).
  useEffect(() => { autoGrowComposer(addInputRef.current); }, [draftTitle]);

  const refresh = useCallback(async () => {
    if (!paneId) return;
    try {
      const [listRes, countsRes] = await Promise.all([
        (apiService as any).listTodos(paneId, showAllAgents),
        (apiService as any).getTodoCounts(paneId),
      ]);
      setTodos(listRes.data?.todos || []);
      const c = countsRes.data || { all: 0, todo: 0, test: 0, done: 0, dropped: 0 };
      setCounts(c);
      setError(null);
      // Let the Todo-tab badge (in Workspace) update immediately after any
      // load/mutation, carrying the fresh counts so it needn't refetch.
      window.dispatchEvent(new CustomEvent('cicy:todos-changed', {
        detail: { paneId, todo: c.todo || 0 },
      }));
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'failed to load todos');
    } finally {
      setLoading(false);
    }
  }, [paneId, showAllAgents]);

  // The 5s poll replaces `todos` and re-sorts by updated_at. If that lands
  // while the user is mid-edit or mid-drag, the card jumps under them. Freeze
  // the poll during those interactions; the next tick (or any explicit refresh
  // after the mutation) picks the changes back up.
  const interactingRef = useRef(false);
  useEffect(() => { interactingRef.current = editingId !== null || draggingId !== null; }, [editingId, draggingId]);

  useEffect(() => {
    if (!active) return;
    setLoading(true);
    refresh();
    const t = setInterval(() => {
      if (interactingRef.current) return;
      refresh();
    }, POLL_MS);
    return () => clearInterval(t);
  }, [active, refresh]);

  useEffect(() => {
    if (active) requestAnimationFrame(() => addInputRef.current?.focus());
  }, [active]);

  const bucketed = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    let visible = q ? todos.filter((t) => t.title.toLowerCase().includes(q)) : todos;
    if (showAllAgents && filterAgent) {
      visible = visible.filter((t) => (t.pane_id || paneId) === filterAgent);
    }
    const out: Record<TodoStatus, Todo[]> = { todo: [], test: [], done: [], dropped: [] };
    for (const t of visible) {
      if (out[t.status]) out[t.status].push(t);
    }
    for (const k of COLUMNS) {
      // Sort by created_at (stable), NOT updated_at. updated_at changes on every
      // edit / status flip / assign, which would yank the card to the top of its
      // column mid-interaction ("编辑时位移"). created_at never changes, so a card
      // keeps its position when you edit it. New todos (created_at = now) and the
      // optimistic insert both sort to the top, which matches the prepend.
      out[k].sort((a, b) => (b.created_at || '').localeCompare(a.created_at || ''));
    }
    return out;
  }, [todos, searchQuery, showAllAgents, filterAgent, paneId]);

  // Worker list for the "assign" picker (master only). These are the master's
  // bound team workers — the same set shown in the Team panel (via
  // /api/agents/pane/<master>), NOT every pane on the host. Each entry's `name`
  // is the worker's short pane id; `pane_id` here is the master, so we key off
  // `name`.
  useEffect(() => {
    if (!active || !isMaster) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await (apiService as any).getAgentsByPane(paneId);
        const raw = Array.isArray(res?.data) ? res.data : res?.data?.agents || [];
        const list = raw
          .map((b: any) => ({ id: shortId(String(b.name || '')), title: String(b.title || '') }))
          .filter((p: any) => p.id && p.id !== paneId);
        // de-dupe by short id
        const seen = new Set<string>();
        const uniq = list.filter((p: any) => (seen.has(p.id) ? false : (seen.add(p.id), true)));
        if (!cancelled) setWorkerPanes(uniq);
      } catch { /* picker just shows empty */ }
    })();
    return () => { cancelled = true; };
  }, [active, isMaster, paneId]);

  const assignTodo = async (todo: Todo, workerPane: string) => {
    setAssignMenuId(null); setAssignMenuPos(null);
    if (assigningId) return;
    setAssigningId(todo.id);
    const prompt = (todo.status === 'test')
      ? tr('promptDoing', { id: todo.id, title: todo.title })
      : tr('promptTodo', { id: todo.id, title: todo.title });
    try {
      // Move ownership to the worker, then dispatch the task to its pane.
      await (apiService as any).updateTodo(paneId, todo.id, { assignee: workerPane });
      await (apiService as any).sendCommand(workerPane, prompt, true);
      await refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'assign failed');
    } finally {
      setAssigningId(null);
    }
  };

  // distinct agent ids from loaded todos (all_agents mode)
  const agentIds = useMemo(() => {
    if (!showAllAgents) return [];
    const ids = new Set<string>();
    ids.add(paneId); // master always first
    for (const t of todos) if (t.pane_id) ids.add(t.pane_id);
    return Array.from(ids);
  }, [todos, showAllAgents, paneId]);

  const submitAdd = async () => {
    const title = draftTitle.trim();
    if (!title) return;
    setDraftTitle('');
    // Optimistic insert: show the card immediately with a pending marker so
    // there's no perceptible lag between Enter and the card appearing. The
    // temp id (tmp-*) is replaced wholesale when the next refresh returns the
    // server's real list (which carries the real auto-increment id).
    const nowIso = new Date().toISOString();
    const tmpId = `tmp-${Date.now()}`;
    const optimistic: Todo = {
      id: tmpId, title, status: 'todo', pane_id: paneId,
      created_at: nowIso, updated_at: nowIso, _pending: true,
    };
    setTodos((cur) => [optimistic, ...cur]);
    try {
      await (apiService as any).addTodo(paneId, title, paneId);
      await refresh();
    } catch (e: any) {
      // Roll back the optimistic card on failure.
      setTodos((cur) => cur.filter((x) => x.id !== tmpId));
      setError(e?.response?.data?.detail || e?.message || 'add failed');
    }
  };

  const sendAlignPrompt = async () => {
    if (aligning !== 'idle' || !paneId) return;
    setAligning('sending');
    const ALIGN_PROMPT = tr('alignPrompt');
    try {
      await (apiService as any).sendCommand(paneId, ALIGN_PROMPT, true);
      setAligning('sent');
      setTimeout(() => setAligning('idle'), 2500);
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'send align prompt failed');
      setAligning('idle');
    }
  };

  const toggleSelect = (id: string) => setSelectedIds((cur) => {
    const next = new Set(cur);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });
  const clearSelection = () => setSelectedIds(new Set());

  // Drop selections whose todo disappeared (deleted elsewhere / poll refresh),
  // so the batch bar count never references a card that's no longer on screen.
  useEffect(() => {
    setSelectedIds((cur) => {
      if (cur.size === 0) return cur;
      const live = new Set(todos.map((t) => t.id));
      let changed = false;
      const next = new Set<string>();
      cur.forEach((id) => { if (live.has(id)) next.add(id); else changed = true; });
      return changed ? next : cur;
    });
  }, [todos]);

  // Batch-dispatch: bundle every selected todo into ONE prompt so the agent
  // plans and works them together, rather than firing one message per todo.
  const batchSendToAgent = async () => {
    if (batchSending || !paneId || selectedIds.size === 0) return;
    setBatchSending(true);
    const chosen = todos.filter((t) => selectedIds.has(t.id));
    const list = chosen.map((t, i) => `${i + 1}. [${t.id}] ${t.title}`).join('\n');
    const prompt = tr('promptBatch', { count: chosen.length, list });
    try {
      await (apiService as any).sendCommand(paneId, prompt, true);
      clearSelection();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'batch send failed');
    } finally {
      setBatchSending(false);
    }
  };

  const sendTodoToAgent = async (todo: Todo) => {
    if (sendingTodoId || !paneId) return;
    setSendingTodoId(todo.id);
    const prompt = (todo.status === 'test')
      ? tr('promptDoing', { id: todo.id, title: todo.title })
      : tr('promptTodo', { id: todo.id, title: todo.title });
    try {
      await (apiService as any).sendCommand(paneId, prompt, true);
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'send failed');
    } finally {
      setSendingTodoId(null);
    }
  };

  const setStatus = async (id: string, status: TodoStatus, targetPaneId?: string) => {
    try {
      await (apiService as any).updateTodo(targetPaneId || paneId, id, { status });
      setMenuOpenId(null); setMenuPos(null);
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || tr('updateFailed'));
    }
  };

  const remove = async (id: string, targetPaneId?: string) => {
    try {
      await (apiService as any).deleteTodo(targetPaneId || paneId, id);
      setMenuOpenId(null); setMenuPos(null);
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'delete failed');
    }
  };

  const submitEdit = async (id: string, targetPaneId?: string) => {
    const title = editingDraft.trim();
    if (!title) { setEditingId(null); setEditingDraft(''); return; }
    try {
      await (apiService as any).updateTodo(targetPaneId || paneId, id, { title });
      setEditingId(null); setEditingDraft('');
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'rename failed');
    }
  };

  const handleDropOn = (status: TodoStatus) => async (e: React.DragEvent) => {
    e.preventDefault();
    setDragOverCol(null);
    const id = e.dataTransfer.getData('text/plain');
    setDraggingId(null);
    if (!id) return;
    const t = todos.find((x) => x.id === id);
    if (!t || t.status === status) return;
    try {
      setTodos((cur) => cur.map((x) => (x.id === id ? { ...x, status, updated_at: new Date().toISOString() } : x)));
      await (apiService as any).updateTodo(t.pane_id || paneId, id, { status });
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || tr('updateFailed'));
      refresh();
    }
  };

  const totalVisible = COLUMNS.reduce((s, k) => s + bucketed[k].length, 0);
  const isEmpty = counts.all === 0 && !searchQuery && !loading;

  return (
    <div data-id="todo-panel" className="absolute inset-0 flex flex-col bg-[#0A0A0A] text-zinc-100">
      {/* TOOLBAR */}
      <div className="flex h-11 shrink-0 items-center gap-2 border-b border-white/[0.06] px-3">
        <div className="flex flex-1 items-center gap-2 rounded-md bg-white/[0.04] px-2.5 py-1">
          <Search className="h-3.5 w-3.5 shrink-0 text-zinc-600" />
          <input
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Escape') setSearchQuery(''); }}
            placeholder={tr('searchPlaceholder')}
            className="m-0 h-5 w-full border-0 bg-transparent p-0 text-[12px] leading-5 text-zinc-200 outline-none placeholder:text-zinc-600"
          />
          {searchQuery && (
            <button onClick={() => setSearchQuery('')} className="text-zinc-500 hover:text-zinc-200">
              <XIcon className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <span className="text-[11px] tabular-nums text-zinc-600">{totalVisible}/{counts.all}</span>
        {isMaster && (
          <button
            onClick={() => setShowAllAgents(!showAllAgents)}
            title={showAllAgents ? tr('viewMineOnly') : tr('viewAllAgents')}
            className={`flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-[11px] transition-colors ${
              showAllAgents ? 'bg-purple-500/20 text-purple-300' : 'bg-white/[0.04] text-zinc-400 hover:text-zinc-200'
            }`}
          >
            <Users className="h-3.5 w-3.5" />
            <span>{tr('all')}</span>
          </button>
        )}
        <button
          onClick={() => setViewMode((m) => (m === 'kanban' ? 'list' : 'kanban'))}
          title={viewMode === 'kanban' ? tr('viewListTooltip') : tr('viewKanbanTooltip')}
          className="flex shrink-0 items-center justify-center rounded-md bg-white/[0.04] p-1.5 text-zinc-400 transition-colors hover:bg-white/[0.1] hover:text-zinc-100"
        >
          {viewMode === 'kanban' ? <ListIcon className="h-3.5 w-3.5" /> : <LayoutGrid className="h-3.5 w-3.5" />}
        </button>
        <button
          onClick={async () => {
            if (manualRefreshing) return;
            setManualRefreshing(true);
            try { await refresh(); } finally { setManualRefreshing(false); }
          }}
          disabled={manualRefreshing}
          title={tr('refreshTooltip')}
          className="flex shrink-0 items-center justify-center rounded-md bg-white/[0.04] p-1.5 text-zinc-400 transition-colors hover:bg-white/[0.1] hover:text-zinc-100 disabled:opacity-50"
        >
          <RefreshCw className={`h-3.5 w-3.5 ${manualRefreshing ? 'animate-spin' : ''}`} />
        </button>
        <button
          onClick={sendAlignPrompt}
          disabled={aligning !== 'idle'}
          title={tr('alignTooltip')}
          className={`flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] transition-colors ${
            aligning === 'sent' ? 'bg-emerald-500/[0.12] text-emerald-300'
            : aligning === 'sending' ? 'bg-white/[0.06] text-zinc-400'
            : 'bg-white/[0.04] text-zinc-300 hover:bg-white/[0.1] hover:text-zinc-100'
          }`}
        >
          {aligning === 'sending' ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
            : aligning === 'sent' ? <CheckCircle2 className="h-3.5 w-3.5" />
            : <Sparkles className="h-3.5 w-3.5" />}
          <span>{aligning === 'sent' ? tr('alignSent') : aligning === 'sending' ? tr('alignSending') : tr('alignProgress')}</span>
        </button>
      </div>

      {showAllAgents && agentIds.length > 1 && (
        <div className="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b border-white/[0.06] px-3 py-2">
          <button
            onClick={() => setFilterAgent(null)}
            className={`shrink-0 rounded-full px-2.5 py-0.5 text-[11px] transition-colors ${
              filterAgent === null ? 'bg-purple-500/25 text-purple-200' : 'bg-white/[0.04] text-zinc-400 hover:text-zinc-200'
            }`}
          >{tr('all')}</button>
          {agentIds.map((id) => (
            <button
              key={id}
              onClick={() => setFilterAgent(filterAgent === id ? null : id)}
              className={`shrink-0 rounded-full px-2.5 py-0.5 text-[11px] transition-colors ${
                filterAgent === id ? 'bg-purple-500/25 text-purple-200' : 'bg-white/[0.04] text-zinc-400 hover:text-zinc-200'
              }`}
            >{shortId(id)}</button>
          ))}
        </div>
      )}

      {error && (
        <div className="mx-3 mt-3 shrink-0 rounded-md border border-red-500/30 bg-red-500/[0.08] px-3 py-2 text-[12px] text-red-300">
          {error}
          <button onClick={() => { setError(null); refresh(); }} className="ml-2 underline">retry</button>
        </div>
      )}

      {/* BOARD — kanban (columns side by side) or list (sections stacked) */}
      <div className={viewMode === 'kanban'
        ? 'flex flex-1 gap-3 overflow-x-auto overflow-y-hidden px-3 py-3'
        : 'flex flex-1 flex-col gap-3 overflow-y-auto px-3 py-3'}>
        {COLUMNS.map((status) => {
          const cls = statusClasses(status);
          const cards = bucketed[status];
          const isDropTarget = dragOverCol === status;
          // In list mode, hide empty sections to keep the single column tidy.
          if (viewMode === 'list' && cards.length === 0) return null;
          return (
            <div
              key={status}
              onDragOver={(e) => { e.preventDefault(); setDragOverCol(status); }}
              onDragLeave={(e) => {
                if ((e.relatedTarget as Node | null) && (e.currentTarget as Node).contains(e.relatedTarget as Node)) return;
                setDragOverCol(null);
              }}
              onDrop={handleDropOn(status)}
              className={`flex flex-col rounded-lg border bg-white/[0.015] transition-colors ${
                viewMode === 'kanban' ? 'min-w-[280px] flex-1' : 'w-full shrink-0'
              } ${isDropTarget ? 'border-white/20 bg-white/[0.04]' : 'border-white/[0.05]'}`}
            >
              <div className={`flex items-center justify-between rounded-t-lg px-3 py-2 ${cls.active}`}>
                <div className="flex items-center gap-2 text-[12px] font-medium">
                  {statusIcon(status)}
                  <span>{tr(`status.${status}`)}</span>
                </div>
                <span className="text-[11px] tabular-nums opacity-70">{cards.length}</span>
              </div>

              <div
                className={`space-y-2 p-2 ${viewMode === 'kanban' ? 'flex-1 overflow-y-auto' : ''}`}
                onScroll={() => { setMenuOpenId(null); setMenuPos(null); setAssignMenuId(null); setAssignMenuPos(null); }}
              >
                {cards.length === 0 ? (
                  <div className="px-2 py-6 text-center text-[11px] text-zinc-700">
                    {searchQuery ? tr('emptyMatch') : status === 'todo' ? tr('emptyTodo') : tr('emptyOther')}
                  </div>
                ) : cards.map((t) => {
                  const isEditing = editingId === t.id;
                  const isBeingDragged = draggingId === t.id;
                  const isSending = sendingTodoId === t.id;
                  const isSelected = selectedIds.has(t.id);
                  const cardPane = t.pane_id || paneId;
                  const canSendToAgent = status === 'todo' || status === 'test';
                  return (
                    <article
                      key={t.id}
                      draggable={!isEditing && !t._pending}
                      onDragStart={(e) => { e.dataTransfer.setData('text/plain', t.id); e.dataTransfer.effectAllowed = 'move'; setDraggingId(t.id); }}
                      onDragEnd={() => { setDraggingId(null); setDragOverCol(null); }}
                      className={`group relative overflow-hidden rounded-md border bg-[#141417] transition-all ${
                        isBeingDragged ? 'opacity-40' : t._pending ? 'opacity-60' : 'opacity-100'
                      } ${isSelected ? 'border-blue-500/60 ring-1 ring-blue-500/40' : `${cls.cardBorder} hover:border-white/15`}`}
                    >
                      <div className={`absolute left-0 top-0 bottom-0 w-[3px] ${cls.stripe}`} />

                      <div className="flex items-start gap-2 pl-2.5 pr-2 pt-2.5">
                        {/* Big multi-select checkbox for batch dispatch */}
                        <button
                          data-id={`todo-panel-card-checkbox-${t.id}`}
                          onClick={(e) => { e.stopPropagation(); toggleSelect(t.id); }}
                          aria-pressed={isSelected}
                          title={tr('batchSendTooltip')}
                          className={`mt-px flex h-[18px] w-[18px] shrink-0 items-center justify-center rounded-[5px] border transition-colors ${
                            isSelected
                              ? 'border-blue-500 bg-blue-500 text-white'
                              : 'border-white/25 bg-white/[0.02] text-transparent hover:border-blue-400/70'
                          }`}
                        >
                          <Check className="h-3 w-3" strokeWidth={3} />
                        </button>
                        <GripVertical className="mt-0.5 h-3.5 w-3.5 shrink-0 cursor-grab text-zinc-700 group-hover:text-zinc-500" />
                        <div className="min-w-0 flex-1">
                          {isEditing ? (
                            <textarea
                              autoFocus
                              rows={1}
                              value={editingDraft}
                              onChange={(e) => {
                                setEditingDraft(e.target.value);
                                const el = e.target as HTMLTextAreaElement;
                                el.style.height = 'auto';
                                el.style.height = `${el.scrollHeight}px`;
                              }}
                              ref={(el) => { if (!el) return; el.style.height = 'auto'; el.style.height = `${el.scrollHeight}px`; }}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submitEdit(t.id, cardPane); }
                                if (e.key === 'Escape') { e.preventDefault(); setEditingId(null); setEditingDraft(''); }
                              }}
                              onBlur={() => submitEdit(t.id, cardPane)}
                              className="m-0 block w-full resize-none border-0 bg-transparent p-0 text-[12px] leading-5 text-zinc-100 outline-none"
                            />
                          ) : (
                            <div
                              onDoubleClick={() => { setEditingId(t.id); setEditingDraft(t.title); }}
                              title={t.title}
                              className={`line-clamp-3 break-words text-[12px] leading-5 ${
                                t.status === 'done' ? 'text-zinc-500 line-through'
                                : t.status === 'dropped' ? 'text-zinc-600 line-through'
                                : 'text-zinc-100'
                              }`}
                            >
                              {t.title}
                            </div>
                          )}
                        </div>
                      </div>

                      <div className="flex items-center justify-between gap-2 px-3 pb-2 pt-1">
                        <div className="flex items-center gap-1.5">
                          <span className="text-[10px] tabular-nums text-zinc-600" title={t.updated_at}>{humanTime(t.updated_at, tr)}</span>
                          {t._pending ? (
                            <span className="flex items-center gap-1 rounded bg-white/[0.04] px-1 py-0.5 text-[9px] text-zinc-500">
                              <Loader2 className="h-2.5 w-2.5 animate-spin" />
                              {tr('saving')}
                            </span>
                          ) : (
                            <span
                              className="cursor-default select-all rounded bg-white/[0.04] px-1 py-0.5 font-mono text-[9px] text-zinc-600"
                              title={t.id}
                            >{t.id}</span>
                          )}
                          {t.creator_id && (
                            <span className="rounded bg-white/[0.05] px-1 py-0.5 text-[9px] text-zinc-600" title={tr('creatorTooltip', { name: t.creator_id })}>
                              {shortId(t.creator_id)}
                            </span>
                          )}
                          {t.pane_id && showAllAgents && (
                            <span className="rounded bg-purple-500/10 px-1 py-0.5 text-[9px] text-purple-400">
                              {shortId(t.pane_id)}
                            </span>
                          )}
                        </div>
                        <div className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
                          {!isEditing && (
                            <>
                              {canSendToAgent && (
                                <CardAction
                                  onClick={() => sendTodoToAgent(t)}
                                  title={status === 'test' ? tr('sendToAgentDoing') : tr('sendToAgentTodo')}
                                  disabled={isSending}
                                >
                                  {isSending ? <Loader2 className="h-3 w-3 animate-spin" /> : <Send className="h-3 w-3" />}
                                </CardAction>
                              )}
                              {isMaster && canSendToAgent && (
                                <CardAction
                                  onClick={(e) => {
                                    if (assignMenuId === t.id) { setAssignMenuId(null); setAssignMenuPos(null); }
                                    else {
                                      const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
                                      const menuH = Math.min(280, 36 + workerPanes.length * 30 + 8);
                                      const below = r.bottom + 4;
                                      const flipUp = below + menuH > window.innerHeight - 8;
                                      const top = flipUp ? Math.max(8, r.top - 4 - menuH) : below;
                                      setAssignMenuPos({ top, right: window.innerWidth - r.right });
                                      setAssignMenuId(t.id);
                                    }
                                  }}
                                  title={tr('assignTooltip')}
                                  disabled={assigningId === t.id}
                                >
                                  {assigningId === t.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <UserPlus className="h-3 w-3" />}
                                </CardAction>
                              )}
                              {assignMenuId === t.id && assignMenuPos && createPortal(
                                <>
                                  <div className="fixed inset-0 z-[100]" onClick={() => { setAssignMenuId(null); setAssignMenuPos(null); }} />
                                  <div
                                    className="fixed z-[101] max-h-[280px] min-w-[180px] overflow-y-auto rounded-md border border-white/[0.08] bg-[#161616] py-1 text-[12px] shadow-xl"
                                    style={{ top: assignMenuPos.top, right: assignMenuPos.right }}
                                  >
                                    <div className="px-3 py-1 text-[10px] uppercase tracking-wide text-zinc-600">{tr('assignHeader')}</div>
                                    {workerPanes.length === 0 ? (
                                      <div className="px-3 py-1.5 text-zinc-600">{tr('assignNoWorkers')}</div>
                                    ) : workerPanes.map((wp) => (
                                      <button
                                        key={wp.id}
                                        onClick={() => assignTodo(t, wp.id)}
                                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-zinc-200 transition-colors hover:bg-white/[0.06]"
                                      >
                                        <span className="font-mono text-[11px] text-purple-300">{wp.id}</span>
                                        {wp.title && wp.title !== '未命名' && <span className="truncate text-[11px] text-zinc-500">{wp.title}</span>}
                                      </button>
                                    ))}
                                  </div>
                                </>,
                                document.body
                              )}
                              <CardAction onClick={() => { setEditingId(t.id); setEditingDraft(t.title); }} title={tr('editTooltip')}>
                                <Pencil className="h-3 w-3" />
                              </CardAction>
                              {t.status !== 'done' && (
                                <CardAction onClick={() => setStatus(t.id, advanceStatus(t.status), cardPane)} title={tr('advanceStatusHint', { label: tr(`status.${advanceStatus(t.status)}`) })}>
                                  <ArrowRight className="h-3 w-3" />
                                </CardAction>
                              )}
                              <CardAction
                                onClick={(e) => {
                                  if (menuOpenId === t.id) { setMenuOpenId(null); setMenuPos(null); }
                                  else {
                                    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
                                    // Menu has (3 move items + 1 delete) rows + a divider; estimate its
                                    // height and flip it above the button when there isn't room below,
                                    // so cards near the bottom edge don't get their menu clipped.
                                    const menuH = 4 * 30 + 9 + 8;
                                    const below = r.bottom + 4;
                                    const flipUp = below + menuH > window.innerHeight - 8;
                                    const top = flipUp ? Math.max(8, r.top - 4 - menuH) : below;
                                    setMenuPos({ top, right: window.innerWidth - r.right });
                                    setMenuOpenId(t.id);
                                  }
                                }}
                                title={tr('moreTooltip')}
                              >
                                <MoreHorizontal className="h-3 w-3" />
                              </CardAction>
                              {menuOpenId === t.id && menuPos && createPortal(
                                <>
                                  <div className="fixed inset-0 z-[100]" onClick={() => { setMenuOpenId(null); setMenuPos(null); }} />
                                  <div
                                    className="fixed z-[101] min-w-[140px] rounded-md border border-white/[0.08] bg-[#161616] py-1 text-[12px] shadow-xl"
                                    style={{ top: menuPos.top, right: menuPos.right }}
                                  >
                                    {COLUMNS.filter((s) => s !== t.status).map((s) => (
                                      <MenuItem key={s} onClick={() => setStatus(t.id, s, cardPane)} icon={statusIcon(s)}>{tr('moveToStatus', { label: tr(`status.${s}`) })}</MenuItem>
                                    ))}
                                    <div className="my-1 border-t border-white/[0.06]" />
                                    <MenuItem onClick={() => remove(t.id, cardPane)} icon={<Trash2 className="h-3.5 w-3.5" />} danger>{tr('deleteAction')}</MenuItem>
                                  </div>
                                </>,
                                document.body
                              )}
                            </>
                          )}
                        </div>
                      </div>
                    </article>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>

      {isEmpty && (
        <div className="pointer-events-none absolute inset-x-0 bottom-1/2 text-center">
          <p className="text-[13px] text-zinc-700">{tr('emptyTitle')}</p>
          <p className="mt-1 text-[11px] text-zinc-800">{tr('emptyHint')}</p>
        </div>
      )}

      {/* BATCH BAR — appears when ≥1 todo is checked, dispatches them together */}
      {selectedIds.size > 0 && (
        <div
          data-id="todo-panel-batch-bar"
          className="flex shrink-0 items-center gap-3 border-t border-blue-500/20 bg-blue-500/[0.07] px-3 py-2.5"
        >
          <span className="text-[12px] font-medium text-blue-200">{tr('batchSelected', { n: selectedIds.size })}</span>
          <button
            data-id="todo-panel-batch-clear"
            onClick={clearSelection}
            className="text-[12px] text-zinc-400 transition-colors hover:text-zinc-200"
          >{tr('batchClear')}</button>
          <div className="flex-1" />
          <button
            data-id="todo-panel-batch-send"
            onClick={batchSendToAgent}
            disabled={batchSending}
            title={tr('batchSendTooltip')}
            className="flex items-center gap-1.5 rounded-lg bg-blue-500 px-3 py-1.5 text-[12px] font-medium text-white transition-colors hover:bg-blue-400 disabled:opacity-60"
          >
            {batchSending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
            <span>{tr('batchSend')}</span>
          </button>
        </div>
      )}

      {/* COMPOSER — prompt-style task input, pinned to the bottom */}
      <div data-id="todo-panel-composer" className="shrink-0 border-t border-white/[0.06] bg-[#0A0A0A] px-3 py-3">
        <div
          data-id="todo-panel-composer-field"
          className="flex items-end gap-2 rounded-2xl border border-white/[0.08] bg-white/[0.03] px-3 py-2 transition-colors focus-within:border-white/20 focus-within:bg-white/[0.05]"
        >
          <textarea
            ref={addInputRef}
            data-id="todo-panel-composer-input"
            rows={1}
            value={draftTitle}
            onChange={(e) => setDraftTitle(e.target.value)}
            onCompositionStart={() => setIsComposing(true)}
            onCompositionEnd={() => setIsComposing(false)}
            onKeyDown={(e) => {
              // Enter sends, Shift+Enter inserts a newline (prompt-style).
              if (e.key === 'Enter' && !e.shiftKey && !isComposing) { e.preventDefault(); submitAdd(); }
              if (e.key === 'Escape') { e.preventDefault(); setDraftTitle(''); }
            }}
            placeholder={tr('quickAddPlaceholder')}
            className="m-0 max-h-12 min-h-[24px] flex-1 resize-none border-0 bg-transparent p-0 text-[14px] leading-6 text-zinc-100 outline-none placeholder:text-zinc-600"
          />
          <button
            data-id="todo-panel-composer-send"
            onClick={submitAdd}
            disabled={!draftTitle.trim()}
            title={tr('quickAddPlaceholder')}
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded-xl bg-blue-500 text-white transition-colors hover:bg-blue-400 disabled:bg-white/[0.06] disabled:text-zinc-600"
          >
            <ArrowUp className="h-4 w-4" />
          </button>
        </div>
      </div>
    </div>
  );
}

function CardAction({ children, onClick, title, disabled }: {
  children: React.ReactNode;
  onClick: (e: React.MouseEvent<HTMLButtonElement>) => void;
  title?: string;
  disabled?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      disabled={disabled}
      className="rounded p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-40"
    >
      {children}
    </button>
  );
}

export function advanceStatus(s: TodoStatus): TodoStatus {
  return s === 'todo' ? 'test' : s === 'test' ? 'done' : 'todo';
}

function MenuItem({ children, onClick, icon, danger = false }: {
  children: React.ReactNode; onClick: () => void; icon: React.ReactNode; danger?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors ${
        danger ? 'text-red-400 hover:bg-red-500/[0.08]' : 'text-zinc-200 hover:bg-white/[0.06]'
      }`}
    >
      <span className="shrink-0 text-zinc-500">{icon}</span>
      <span>{children}</span>
    </button>
  );
}

function statusIcon(s: TodoStatus) {
  switch (s) {
    case 'todo':    return <Circle       className="h-4 w-4" />;
    case 'test':    return <FlaskConical className="h-4 w-4 text-cyan-400" />;
    case 'done':    return <CheckCircle2 className="h-4 w-4 text-emerald-400" />;
    case 'dropped': return <CircleSlash  className="h-4 w-4" />;
  }
}

export function statusClasses(s: string): { active: string; idle: string; badge: string; stripe: string; cardBorder: string } {
  switch (s) {
    case 'todo':
      return { active: 'bg-blue-500/15 text-blue-300 ring-1 ring-blue-500/30', idle: 'text-blue-400/70', badge: 'bg-blue-500/10 text-blue-300', stripe: 'bg-blue-400', cardBorder: 'border-white/[0.06]' };
    case 'test':
      return { active: 'bg-cyan-500/15 text-cyan-300 ring-1 ring-cyan-500/30', idle: 'text-cyan-400/70', badge: 'bg-cyan-500/10 text-cyan-300', stripe: 'bg-cyan-400', cardBorder: 'border-cyan-500/20' };
    case 'done':
      return { active: 'bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-500/30', idle: 'text-emerald-400/70', badge: 'bg-emerald-500/10 text-emerald-300', stripe: 'bg-emerald-400', cardBorder: 'border-white/[0.06]' };
    case 'dropped':
      return { active: 'bg-rose-500/15 text-rose-300 ring-1 ring-rose-500/30', idle: 'text-rose-400/60', badge: 'bg-rose-500/10 text-rose-400', stripe: 'bg-rose-400', cardBorder: 'border-white/[0.06]' };
    default:
      return { active: 'bg-white/[0.10] text-zinc-100', idle: 'text-zinc-500', badge: 'bg-white/[0.05] text-zinc-300', stripe: 'bg-zinc-500', cardBorder: 'border-white/[0.06]' };
  }
}

export function humanTime(iso: string, tr: (key: string, opts?: Record<string, unknown>) => string): string {
  if (!iso) return '-';
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (diff < 60)     return tr('timeJustNow');
  if (diff < 3600)   return tr('timeMinutesAgo', { n: Math.floor(diff / 60) });
  if (diff < 86400)  return tr('timeHoursAgo',   { n: Math.floor(diff / 3600) });
  if (diff < 604800) return tr('timeDaysAgo',    { n: Math.floor(diff / 86400) });
  const d = new Date(iso);
  return tr('timeDateMonth', { month: d.getMonth() + 1, day: d.getDate() });
}

export function shortId(id: string): string {
  // w-10001:main.0 → w-10001, or just trim long ids
  return id.split(':')[0];
}
