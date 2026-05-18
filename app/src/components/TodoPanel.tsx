import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Circle, CircleDot, CheckCircle2, CircleSlash, Trash2, MoreHorizontal, Sparkles, Loader2 } from 'lucide-react';
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

const filterMeta: { key: Filter; label: string }[] = [
  { key: 'all',     label: '全部' },
  { key: 'todo',    label: '待办' },
  { key: 'doing',   label: '进行中' },
  { key: 'done',    label: '已完成' },
  { key: 'dropped', label: '废弃' },
];

const POLL_MS = 5000;

interface Props {
  paneId: string;
  active: boolean;
}

export default function TodoPanel({ paneId, active }: Props) {
  const [todos, setTodos] = useState<Todo[]>([]);
  const [counts, setCounts] = useState<Counts>({ all: 0, todo: 0, doing: 0, done: 0, dropped: 0 });
  const [filter, setFilter] = useState<Filter>('all');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [draftTitle, setDraftTitle] = useState('');
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingDraft, setEditingDraft] = useState('');
  const [menuOpenId, setMenuOpenId] = useState<string | null>(null);
  const [aligning, setAligning] = useState<'idle' | 'sending' | 'sent'>('idle');
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

  const filtered = useMemo(() => {
    return todos.filter((t) => filter === 'all' || t.status === filter);
  }, [todos, filter]);

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

  const cycleStatus = async (t: Todo) => {
    const next: TodoStatus = t.status === 'todo' ? 'doing' : t.status === 'doing' ? 'done' : 'todo';
    try {
      await apiService.updateTodo(paneId, t.id, { status: next });
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'update failed');
    }
  };

  const setStatus = async (id: string, status: TodoStatus) => {
    try {
      await apiService.updateTodo(paneId, id, { status });
      setMenuOpenId(null);
      refresh();
    } catch (e: any) {
      setError(e?.response?.data?.detail || e?.message || 'update failed');
    }
  };

  const remove = async (id: string) => {
    try {
      await apiService.deleteTodo(paneId, id);
      setMenuOpenId(null);
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

  return (
    <div data-id="todo-panel" className="absolute inset-0 flex flex-col bg-[#0A0A0A] text-zinc-100">
      {/* top bar: always-on autofocus input — Enter adds */}
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
          className="m-0 h-6 w-full border-0 bg-transparent p-0 text-[14px] leading-6 text-zinc-100 outline-none placeholder:text-zinc-600"
        />
      </div>

      {/* filter chips + align-with-agent action */}
      <div className="flex h-11 shrink-0 items-center gap-1 border-b border-white/[0.06] px-3">
        <div className="flex flex-1 items-center gap-1 overflow-x-auto whitespace-nowrap scrollbar-none">
          {filterMeta.map(({ key, label }) => {
            const c = counts[key];
            const active = filter === key;
            const cls = statusClasses(key);
            return (
              <button
                key={key}
                onClick={() => setFilter(key)}
                className={`flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-[12px] transition-colors ${
                  active ? cls.active : cls.idle
                }`}
              >
                <span>{label}</span>
                <span className={`text-[11px] tabular-nums ${active ? 'text-zinc-400' : 'text-zinc-600'}`}>{c}</span>
              </button>
            );
          })}
        </div>
        <button
          onClick={sendAlignPrompt}
          disabled={aligning !== 'idle'}
          title="让当前 agent 对照实际进度更新这份 todo 清单"
          className={`flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-[12px] transition-colors ${
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

      {/* list */}
      <div className="flex-1 overflow-y-auto">
        {error && (
          <div className="mx-3 mt-3 rounded-md border border-red-500/30 bg-red-500/[0.08] px-3 py-2 text-[12px] text-red-300">
            {error}
            <button onClick={() => { setError(null); refresh(); }} className="ml-2 underline">retry</button>
          </div>
        )}

        {!loading && filtered.length === 0 && (
          <div className="px-8 py-16 text-center text-[13px] text-zinc-600">
            {todos.length === 0 ? '还没有待办 — 在上方输入框直接打字按 Enter' : '当前筛选下没有匹配的待办'}
          </div>
        )}

        {filtered.map((t) => {
          const isEditing = editingId === t.id;
          return (
            <div
              key={t.id}
              className="group flex items-center gap-3 border-b border-white/[0.04] px-4 hover:bg-white/[0.02]"
              style={{ height: 56 }}
            >
              <button
                onClick={() => cycleStatus(t)}
                title={`状态: ${t.status}  (点击切换)`}
                className="flex h-6 w-6 shrink-0 items-center justify-center text-zinc-500 transition-colors hover:text-zinc-200"
              >
                {statusIcon(t.status)}
              </button>

              <div className="min-w-0 flex-1">
                {isEditing ? (
                  <input
                    autoFocus
                    value={editingDraft}
                    onChange={(e) => setEditingDraft(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') { e.preventDefault(); submitEdit(t.id); }
                      if (e.key === 'Escape') { e.preventDefault(); setEditingId(null); setEditingDraft(''); }
                    }}
                    onBlur={() => submitEdit(t.id)}
                    className="m-0 block h-5 w-full border-0 bg-transparent p-0 text-[13px] leading-5 text-zinc-100 outline-none"
                  />
                ) : (
                  <div
                    onClick={() => { setEditingId(t.id); setEditingDraft(t.title); }}
                    title={t.title}
                    className={`line-clamp-2 break-words text-[13px] leading-5 ${
                      t.status === 'done' ? 'text-zinc-500 line-through' :
                      t.status === 'dropped' ? 'text-zinc-600 line-through' :
                      'text-zinc-100'
                    } cursor-text`}
                  >
                    {t.title}
                  </div>
                )}
                <div className="mt-1 flex items-center gap-2 text-[11px] text-zinc-600">
                  <span className={`inline-flex items-center rounded-full px-1.5 py-px text-[10px] font-medium leading-4 ${statusClasses(t.status).badge}`}>
                    {statusLabel(t.status)}
                  </span>
                  <span title={t.updated_at}>{humanTime(t.updated_at)}</span>
                </div>
              </div>

              <div className="relative shrink-0 opacity-0 transition-opacity group-hover:opacity-100">
                <button
                  onClick={() => setMenuOpenId(menuOpenId === t.id ? null : t.id)}
                  className="rounded p-1 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200"
                >
                  <MoreHorizontal className="h-4 w-4" />
                </button>
                {menuOpenId === t.id && (
                  <>
                    <div className="fixed inset-0 z-10" onClick={() => setMenuOpenId(null)} />
                    <div className="absolute right-0 top-7 z-20 min-w-[140px] rounded-md border border-white/[0.08] bg-[#161616] py-1 text-[12px] shadow-xl">
                      {t.status !== 'todo'    && <MenuItem onClick={() => setStatus(t.id, 'todo')}    icon={<Circle      className="h-3.5 w-3.5" />}>标记为 待办</MenuItem>}
                      {t.status !== 'doing'   && <MenuItem onClick={() => setStatus(t.id, 'doing')}   icon={<CircleDot   className="h-3.5 w-3.5" />}>开始 (doing)</MenuItem>}
                      {t.status !== 'done'    && <MenuItem onClick={() => setStatus(t.id, 'done')}    icon={<CheckCircle2 className="h-3.5 w-3.5" />}>标记为 已完成</MenuItem>}
                      {t.status !== 'dropped' && <MenuItem onClick={() => setStatus(t.id, 'dropped')} icon={<CircleSlash className="h-3.5 w-3.5" />}>废弃</MenuItem>}
                      <div className="my-1 border-t border-white/[0.06]" />
                      <MenuItem onClick={() => remove(t.id)} icon={<Trash2 className="h-3.5 w-3.5" />} danger>删除</MenuItem>
                    </div>
                  </>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
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
function statusClasses(s: Filter): { active: string; idle: string; badge: string } {
  switch (s) {
    case 'todo':
      return {
        active: 'bg-blue-500/15 text-blue-300 ring-1 ring-blue-500/30',
        idle:   'text-blue-400/70 hover:bg-blue-500/[0.08] hover:text-blue-300',
        badge:  'bg-blue-500/10 text-blue-300 ring-1 ring-blue-500/20',
      };
    case 'doing':
      return {
        active: 'bg-amber-500/15 text-amber-300 ring-1 ring-amber-500/30',
        idle:   'text-amber-400/70 hover:bg-amber-500/[0.08] hover:text-amber-300',
        badge:  'bg-amber-500/10 text-amber-300 ring-1 ring-amber-500/20',
      };
    case 'done':
      return {
        active: 'bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-500/30',
        idle:   'text-emerald-400/70 hover:bg-emerald-500/[0.08] hover:text-emerald-300',
        badge:  'bg-emerald-500/10 text-emerald-300 ring-1 ring-emerald-500/20',
      };
    case 'dropped':
      return {
        active: 'bg-rose-500/15 text-rose-300 ring-1 ring-rose-500/30',
        idle:   'text-rose-400/60 hover:bg-rose-500/[0.08] hover:text-rose-300',
        badge:  'bg-rose-500/10 text-rose-400 ring-1 ring-rose-500/20',
      };
    case 'all':
    default:
      return {
        active: 'bg-white/[0.10] text-zinc-100 ring-1 ring-white/15',
        idle:   'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300',
        badge:  'bg-white/[0.05] text-zinc-300 ring-1 ring-white/10',
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

