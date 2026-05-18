import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Circle, CircleDot, CheckCircle2, CircleSlash, Trash2, MoreHorizontal, Sparkles, Loader2, GripVertical, Search, ArrowRight, Pencil, X as XIcon } from 'lucide-react';
import apiService from '../services/api';

// Prompt sent to the current agent when the user clicks "对齐进度".
// Keep it directive: enumerate the exact commands the agent should run, and
// the expected shape of the brief report back.
const ALIGN_PROMPT = `请对齐当前 todo 进度（数据在 \`<workspace>/.cicy/todos.yaml\`，通过 \`cicy-todo\` 操作）：

1. 先用 \`cicy-todo list --all\` 拉到完整列表。
2. 已完成的：\`cicy-todo done <id-prefix>\`
3. 不再需要的：\`cicy-todo drop <id-prefix>\`
4. 没完成的：结合当前情况判断是否还要做——要做的话拆得更具体（必要时 \`cicy-todo edit <id> "<新标题>"\`，把模糊项改成可执行的下一步）；缺的 todo 直接 \`cicy-todo add "<title>"\`。

做完简单汇报一行：完成 N 条 / 调整 N 条 / 新增 N 条。`;

type TodoStatus = 'todo' | 'doing' | 'done' | 'dropped';

interface Todo {
  id: string;
  title: string;
  status: TodoStatus;
  created_at: string;
  updated_at: string;
}

interface Counts {
  all: number;
  todo: number;
  doing: number;
  done: number;
  dropped: number;
}

type Filter = 'all' | TodoStatus;

// Kanban column order — fixed left→right, mirrors the natural task lifecycle.
const COLUMNS: TodoStatus[] = ['todo', 'doing', 'done', 'dropped'];

const POLL_MS = 5000;

interface Props {
  paneId: string;
  active: boolean;
}

export default function TodoPanel({ paneId, active }: Props) {
  const [todos, setTodos] = useState<Todo[]>([]);
  const [counts, setCounts] = useState<Counts>({ all: 0, todo: 0, doing: 0, done: 0, dropped: 0 });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingDraft, setEditingDraft] = useState('');
  const [menuOpenId, setMenuOpenId] = useState<string | null>(null);
  const [menuPos, setMenuPos] = useState<{ top: number; right: number } | null>(null);
  const [aligning, setAligning] = useState<'idle' | 'sending' | 'sent'>('idle');
  // DnD state — id of the card the user is currently dragging, plus the
  // column it's being hovered over. The latter is used purely to highlight
  // the target column, not to gate the drop itself.
  const [draggingId, setDraggingId] = useState<string | null>(null);
  const [dragOverCol, setDragOverCol] = useState<TodoStatus | null>(null);
  const addInputRef = useRef<HTMLInputElement>(null);

  const refresh = useCallback(async () => {
    if (!paneId) return;
    try {
      const [listRes, countsRes] = await Promise.all([
        apiService.listTodos(paneId),
        apiService.getTodoCounts(paneId),
      ]);
      setTodos(listRes.data?.todos || []);
      setCounts(countsRes.data || { all: 0, todo: 0, doing: 0, done: 0, dropped: 0 });
      setError(null);
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'failed to load todos');
    } finally {
      setLoading(false);
    }
  }, [paneId]);

  useEffect(() => {
    if (!active) return;
    setLoading(true);
    refresh();
    const t = setInterval(refresh, POLL_MS);
    return () => clearInterval(t);
  }, [active, refresh]);

  useEffect(() => {
    if (active) requestAnimationFrame(() => addInputRef.current?.focus());
  }, [active]);

  // Filtered & bucketed by column. Search is a case-insensitive substring
  // match against the title — cheap and predictable on the typical scale
  // (tens-of-todos per pane).
  const bucketed = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    const visible = q
      ? todos.filter((t) => t.title.toLowerCase().includes(q))
      : todos;
    const out: Record<TodoStatus, Todo[]> = { todo: [], doing: [], done: [], dropped: [] };
    for (const t of visible) out[t.status].push(t);
    // Sort within each column: most-recently-updated first.
    for (const k of COLUMNS) {
      out[k].sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''));
    }
    return out;
  }, [todos, searchQuery]);

  const submitAdd = async () => {
    const title = draftTitle.trim();
    if (!title) return;
    setDraftTitle('');
    try {
      await apiService.addTodo(paneId, title);
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'add failed');
    }
  };

  const sendAlignPrompt = async () => {
    if (aligning !== 'idle' || !paneId) return;
    setAligning('sending');
    try {
      await apiService.sendCommand(paneId, ALIGN_PROMPT, true);
      setAligning('sent');
      setTimeout(() => setAligning('idle'), 2500);
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'send align prompt failed');
      setAligning('idle');
    }
  };

  const setStatus = async (id: string, status: TodoStatus) => {
    try {
      await apiService.updateTodo(paneId, id, { status });
      setMenuOpenId(null);
      setMenuPos(null);
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'update failed');
    }
  };

  const remove = async (id: string) => {
    try {
      await apiService.deleteTodo(paneId, id);
      setMenuOpenId(null);
      setMenuPos(null);
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'delete failed');
    }
  };

  const submitEdit = async (id: string) => {
    const title = editingDraft.trim();
    if (!title) { setEditingId(null); setEditingDraft(''); return; }
    try {
      await apiService.updateTodo(paneId, id, { title });
      setEditingId(null);
      setEditingDraft('');
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'rename failed');
    }
  };

  // DnD handlers — wired on the column body. Native HTML5 DnD, no library.
  const handleDropOn = (status: TodoStatus) => async (e: React.DragEvent) => {
    e.preventDefault();
    setDragOverCol(null);
    const id = e.dataTransfer.getData('text/plain');
    setDraggingId(null);
    if (!id) return;
    const t = todos.find((x) => x.id === id);
    if (!t || t.status === status) return;
    try {
      // Optimistic update — keep the card stuck in its new column while
      // the request flies. refresh() will reconcile if the server rejects.
      setTodos((cur) => cur.map((x) => (x.id === id ? { ...x, status, updated_at: new Date().toISOString() } : x)));
      await apiService.updateTodo(paneId, id, { status });
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'update failed');
      refresh();
    }
  };

  const totalVisible = bucketed.todo.length + bucketed.doing.length + bucketed.done.length + bucketed.dropped.length;

  return (
    <div data-id="todo-panel" className="absolute inset-0 flex flex-col bg-[#0A0A0A] text-zinc-100">
      {/* HEADER — quick-add input + search + align-with-agent action */}
      <div className="flex h-14 shrink-0 items-center gap-3 border-b border-white/[0.06] px-4">
        <Circle className="h-4 w-4 shrink-0 text-zinc-600" />
        <input
          ref={addInputRef}
          autoFocus
          value={draftTitle}
          onChange={(e) => setDraftTitle(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') { e.preventDefault(); submitAdd(); }
            if (e.key === 'Escape') { e.preventDefault(); setDraftTitle(''); }
          }}
          placeholder="想到啥写啥，Enter 即加入"
          className="m-0 h-6 flex-1 border-0 bg-transparent p-0 text-[14px] leading-6 text-zinc-100 outline-none placeholder:text-zinc-600"
        />
      </div>

      <div className="flex h-11 shrink-0 items-center gap-2 border-b border-white/[0.06] px-3">
        <div className="flex flex-1 items-center gap-2 rounded-md bg-white/[0.04] px-2.5 py-1">
          <Search className="h-3.5 w-3.5 shrink-0 text-zinc-600" />
          <input
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Escape') setSearchQuery(''); }}
            placeholder="搜索 todo…"
            className="m-0 h-5 w-full border-0 bg-transparent p-0 text-[12px] leading-5 text-zinc-200 outline-none placeholder:text-zinc-600"
          />
          {searchQuery && (
            <button onClick={() => setSearchQuery('')} className="text-zinc-500 hover:text-zinc-200" title="清空搜索">
              <XIcon className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <span className="text-[11px] tabular-nums text-zinc-600">{totalVisible}/{counts.all}</span>
        <button
          onClick={sendAlignPrompt}
          disabled={aligning !== 'idle'}
          title="让当前 agent 对照实际进度更新这份 todo 清单"
          className={`flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] transition-colors ${
            aligning === 'sent'
              ? 'bg-emerald-500/[0.12] text-emerald-300'
              : aligning === 'sending'
                ? 'bg-white/[0.06] text-zinc-400'
                : 'bg-white/[0.04] text-zinc-300 hover:bg-white/[0.1] hover:text-zinc-100'
          }`}
        >
          {aligning === 'sending'
            ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
            : aligning === 'sent'
              ? <CheckCircle2 className="h-3.5 w-3.5" />
              : <Sparkles className="h-3.5 w-3.5" />}
          <span>{aligning === 'sent' ? '已发送' : aligning === 'sending' ? '发送中' : '对齐进度'}</span>
        </button>
      </div>

      {error && (
        <div className="mx-3 mt-3 shrink-0 rounded-md border border-red-500/30 bg-red-500/[0.08] px-3 py-2 text-[12px] text-red-300">
          {error}
          <button onClick={() => { setError(null); refresh(); }} className="ml-2 underline">retry</button>
        </div>
      )}

      {/* KANBAN — 4 columns. flex-row with min-width on each column so we
          gracefully horizontal-scroll on narrow panes instead of squishing
          cards to unreadable width. */}
      <div className="flex flex-1 gap-3 overflow-x-auto overflow-y-hidden px-3 py-3">
        {COLUMNS.map((status) => {
          const cls = statusClasses(status);
          const cards = bucketed[status];
          const isDropTarget = dragOverCol === status;
          return (
            <div
              key={status}
              data-id={`todo-col-${status}`}
              onDragOver={(e) => { e.preventDefault(); setDragOverCol(status); }}
              onDragLeave={(e) => {
                // Only clear when the pointer actually leaves the column,
                // not when it crosses between child cards.
                if ((e.relatedTarget as Node | null) && (e.currentTarget as Node).contains(e.relatedTarget as Node)) return;
                setDragOverCol(null);
              }}
              onDrop={handleDropOn(status)}
              className={`flex min-w-[240px] flex-1 flex-col rounded-lg border bg-white/[0.015] transition-colors ${
                isDropTarget ? 'border-white/20 bg-white/[0.04]' : 'border-white/[0.05]'
              }`}
            >
              {/* Column header — colored stripe + icon + label + count */}
              <div className={`flex items-center justify-between rounded-t-lg px-3 py-2 ${cls.active}`}>
                <div className="flex items-center gap-2 text-[12px] font-medium">
                  {statusIcon(status)}
                  <span>{statusLabel(status)}</span>
                </div>
                <span className="text-[11px] tabular-nums opacity-70">{cards.length}</span>
              </div>

              {/* Card list */}
              <div
                className="flex-1 space-y-2 overflow-y-auto p-2"
                onScroll={() => { setMenuOpenId(null); setMenuPos(null); }}
              >
                {cards.length === 0 ? (
                  <div className="px-2 py-6 text-center text-[11px] text-zinc-700">{searchQuery ? '无匹配' : '空'}</div>
                ) : (
                  cards.map((t) => {
                    const isEditing = editingId === t.id;
                    const isBeingDragged = draggingId === t.id;
                    return (
                      <article
                        key={t.id}
                        draggable={!isEditing}
                        onDragStart={(e) => { e.dataTransfer.setData('text/plain', t.id); e.dataTransfer.effectAllowed = 'move'; setDraggingId(t.id); }}
                        onDragEnd={() => { setDraggingId(null); setDragOverCol(null); }}
                        className={`group relative overflow-hidden rounded-md border bg-[#141417] transition-all ${
                          isBeingDragged ? 'opacity-40' : 'opacity-100'
                        } ${cls.cardBorder} hover:border-white/15`}
                      >
                        {/* Left status stripe */}
                        <div className={`absolute left-0 top-0 bottom-0 w-[3px] ${cls.stripe}`} />

                        <div className="flex items-start gap-1.5 pl-3 pr-2 pt-2">
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
                                  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submitEdit(t.id); }
                                  if (e.key === 'Escape') { e.preventDefault(); setEditingId(null); setEditingDraft(''); }
                                }}
                                onBlur={() => submitEdit(t.id)}
                                className="m-0 block w-full resize-none border-0 bg-transparent p-0 text-[12px] leading-5 text-zinc-100 outline-none"
                              />
                            ) : (
                              <div
                                onDoubleClick={() => { setEditingId(t.id); setEditingDraft(t.title); }}
                                title={t.title}
                                className={`line-clamp-3 break-words text-[12px] leading-5 ${
                                  t.status === 'done' ? 'text-zinc-500 line-through' :
                                  t.status === 'dropped' ? 'text-zinc-600 line-through' :
                                  'text-zinc-100'
                                }`}
                              >
                                {t.title}
                              </div>
                            )}
                          </div>
                        </div>

                        <div className="flex items-center justify-between gap-2 px-3 pb-2 pt-1.5">
                          <span className="text-[10px] tabular-nums text-zinc-600" title={t.updated_at}>{humanTime(t.updated_at)}</span>
                          <div className="flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
                            {!isEditing && (
                              <>
                                <CardAction onClick={() => { setEditingId(t.id); setEditingDraft(t.title); }} title="编辑">
                                  <Pencil className="h-3 w-3" />
                                </CardAction>
                                {t.status !== 'done' && (
                                  <CardAction onClick={() => setStatus(t.id, advanceStatus(t.status))} title={`→ ${statusLabel(advanceStatus(t.status))}`}>
                                    <ArrowRight className="h-3 w-3" />
                                  </CardAction>
                                )}
                                <CardAction
                                  onClick={(e) => {
                                    if (menuOpenId === t.id) {
                                      setMenuOpenId(null);
                                      setMenuPos(null);
                                    } else {
                                      const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
                                      setMenuPos({ top: r.bottom + 4, right: window.innerWidth - r.right });
                                      setMenuOpenId(t.id);
                                    }
                                  }}
                                  title="更多"
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
                                        <MenuItem key={s} onClick={() => setStatus(t.id, s)} icon={statusIcon(s)}>移到 {statusLabel(s)}</MenuItem>
                                      ))}
                                      <div className="my-1 border-t border-white/[0.06]" />
                                      <MenuItem onClick={() => remove(t.id)} icon={<Trash2 className="h-3.5 w-3.5" />} danger>删除</MenuItem>
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
                  })
                )}
              </div>
            </div>
          );
        })}
      </div>

      {!loading && counts.all === 0 && !searchQuery && (
        <div className="pointer-events-none absolute inset-x-0 bottom-1/2 text-center text-[13px] text-zinc-700">
          还没有待办 — 在上方输入框直接打字按 Enter
        </div>
      )}
    </div>
  );
}

function CardAction({ children, onClick, title }: { children: React.ReactNode; onClick: (e: React.MouseEvent<HTMLButtonElement>) => void; title?: string }) {
  return (
    <button
      onClick={onClick}
      title={title}
      className="rounded p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
    >
      {children}
    </button>
  );
}

function advanceStatus(s: TodoStatus): TodoStatus {
  return s === 'todo' ? 'doing' : s === 'doing' ? 'done' : 'todo';
}

function MenuItem({ children, onClick, icon, danger = false }: { children: React.ReactNode; onClick: () => void; icon: React.ReactNode; danger?: boolean }) {
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
    case 'doing':   return <CircleDot    className="h-4 w-4 text-amber-400" />;
    case 'done':    return <CheckCircle2 className="h-4 w-4 text-emerald-400" />;
    case 'dropped': return <CircleSlash  className="h-4 w-4" />;
  }
}

function statusLabel(s: TodoStatus) {
  switch (s) {
    case 'todo':    return '待办';
    case 'doing':   return '进行中';
    case 'done':    return '已完成';
    case 'dropped': return '废弃';
  }
}

// Tailwind class fragments per status — used for the filter chips at the top
// and the small status badge on each row. Each variant returns:
//   - active:   filled style for selected filter chip
//   - idle:     muted text for the unselected filter chip
//   - badge:    pill style for the per-row status indicator
function statusClasses(s: Filter): { active: string; idle: string; badge: string; stripe: string; cardBorder: string } {
  switch (s) {
    case 'todo':
      return {
        active:     'bg-blue-500/15 text-blue-300 ring-1 ring-blue-500/30',
        idle:       'text-blue-400/70 hover:bg-blue-500/[0.08] hover:text-blue-300',
        badge:      'bg-blue-500/10 text-blue-300 ring-1 ring-blue-500/20',
        stripe:     'bg-blue-400',
        cardBorder: 'border-white/[0.06]',
      };
    case 'doing':
      return {
        active:     'bg-amber-500/15 text-amber-300 ring-1 ring-amber-500/30',
        idle:       'text-amber-400/70 hover:bg-amber-500/[0.08] hover:text-amber-300',
        badge:      'bg-amber-500/10 text-amber-300 ring-1 ring-amber-500/20',
        stripe:     'bg-amber-400',
        cardBorder: 'border-amber-500/20',
      };
    case 'done':
      return {
        active:     'bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-500/30',
        idle:       'text-emerald-400/70 hover:bg-emerald-500/[0.08] hover:text-emerald-300',
        badge:      'bg-emerald-500/10 text-emerald-300 ring-1 ring-emerald-500/20',
        stripe:     'bg-emerald-400',
        cardBorder: 'border-white/[0.06]',
      };
    case 'dropped':
      return {
        active:     'bg-rose-500/15 text-rose-300 ring-1 ring-rose-500/30',
        idle:       'text-rose-400/60 hover:bg-rose-500/[0.08] hover:text-rose-300',
        badge:      'bg-rose-500/10 text-rose-400 ring-1 ring-rose-500/20',
        stripe:     'bg-rose-400',
        cardBorder: 'border-white/[0.06]',
      };
    case 'all':
    default:
      return {
        active:     'bg-white/[0.10] text-zinc-100 ring-1 ring-white/15',
        idle:       'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300',
        badge:      'bg-white/[0.05] text-zinc-300 ring-1 ring-white/10',
        stripe:     'bg-zinc-500',
        cardBorder: 'border-white/[0.06]',
      };
  }
}

function humanTime(iso: string): string {
  if (!iso) return '-';
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (diff < 60)      return '刚刚';
  if (diff < 3600)    return `${Math.floor(diff / 60)}m前`;
  if (diff < 86400)   return `${Math.floor(diff / 3600)}h前`;
  if (diff < 604800)  return `${Math.floor(diff / 86400)}天前`;
  const d = new Date(iso);
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}

