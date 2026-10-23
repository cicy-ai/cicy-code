// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// 进度 tab of a project agent card: the main agent's todo list on top (kept in
// sync by a hidden 副 agent — a custom cicy agent on a cheap model running the
// agent-progress skill in-process, no CLI) and that sub-agent's conversation
// below.

import { useCallback, useEffect, useRef, useState } from 'react';
import { Check, CheckCircle2, ChevronDown, ChevronRight, Circle, CircleDot, Loader2, RefreshCw, Undo2 } from 'lucide-react';
import apiService from '../../services/api';
import CurrentHistoryView from '../chat/CurrentHistoryView';
import { cn } from '../../lib/utils';

interface ProgressTodo {
  id: string;
  title: string;
  body?: string;
  status: string;
  updated_at?: string;
}

const POLL_MS = 5000;

// List/terminal split, persisted across reloads. Fraction of the panel height
// given to the todo list (the terminal gets the rest).
const SPLIT_KEY = 'agent-progress-split';
const SPLIT_MIN = 0.15;
const SPLIT_MAX = 0.85;
const clampSplit = (v: number) => Math.min(SPLIT_MAX, Math.max(SPLIT_MIN, v));
const readSplit = () => {
  try {
    const v = Number(localStorage.getItem(SPLIT_KEY));
    return Number.isFinite(v) && v > 0 ? clampSplit(v) : 0.55;
  } catch { return 0.55; }
};

const shortId = (id: string) => String(id || '').split(':')[0];


const timeLabel = (iso?: string) => {
  const t = iso ? new Date(iso) : null;
  if (!t || Number.isNaN(t.getTime())) return '';
  const sameDay = t.toDateString() === new Date().toDateString();
  return sameDay
    ? t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : t.toLocaleDateString([], { month: '2-digit', day: '2-digit' });
};

type RowKind = 'doing' | 'review' | 'confirmed';

function TodoRow({ todo, kind, busy, onConfirm, onUndo }: {
  todo: ProgressTodo; kind: RowKind; busy: boolean; onConfirm?: () => void; onUndo?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const hasBody = Boolean(todo.body && todo.body.trim());
  const Icon = kind === 'confirmed' ? CheckCircle2 : kind === 'review' ? CircleDot : Circle;
  return (
    <li data-id={`agent-progress-todo-${todo.id}`} className="group flex items-start gap-1 pr-1">
      <div className="min-w-0 flex-1">
        <button
          type="button"
          onClick={() => hasBody && setOpen((v) => !v)}
          className={cn('flex w-full items-start gap-2 rounded-md px-2 py-1 text-left', hasBody ? 'hover:bg-white/[0.04]' : 'cursor-default')}
          aria-expanded={hasBody ? open : undefined}
        >
          <Icon className={cn('mt-[2px] h-3.5 w-3.5 shrink-0', kind === 'confirmed' ? 'text-emerald-500' : kind === 'review' ? 'text-sky-400' : 'text-amber-400')} aria-hidden />
          <span className={cn('min-w-0 flex-1 text-[12px] leading-5', kind === 'confirmed' ? 'text-zinc-500' : 'text-zinc-100')}>{todo.title}</span>
          <span className="shrink-0 pt-[1px] text-[10px] tabular-nums text-zinc-600">{timeLabel(todo.updated_at)}</span>
          {hasBody ? (open ? <ChevronDown className="mt-[3px] h-3 w-3 shrink-0 text-zinc-600" /> : <ChevronRight className="mt-[3px] h-3 w-3 shrink-0 text-zinc-600" />) : <span className="w-3 shrink-0" />}
        </button>
        {open && hasBody ? (
          <p className="ml-[30px] mr-2 whitespace-pre-wrap pb-1 text-[11px] leading-[18px] text-zinc-500">{todo.body}</p>
        ) : null}
      </div>
      {kind !== 'confirmed' ? (
        <button
          type="button"
          data-id={`agent-progress-confirm-${todo.id}`}
          onClick={(event) => { event.stopPropagation(); onConfirm?.(); }}
          disabled={busy}
          title="我已验收，确认完成"
          className={cn('mt-[3px] flex h-[22px] shrink-0 items-center gap-1 rounded-md border px-1.5 text-[11px] disabled:opacity-40',
            kind === 'review' ? 'border-sky-500/40 text-sky-300 hover:bg-sky-500/10' : 'border-white/10 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-300')}
        >
          {busy ? <Loader2 className="h-3 w-3 animate-spin" /> : <Check className="h-3 w-3" />}
          确认
        </button>
      ) : (
        <button
          type="button"
          data-id={`agent-progress-undo-${todo.id}`}
          onClick={(event) => { event.stopPropagation(); onUndo?.(); }}
          disabled={busy}
          title="撤销确认"
          className="mt-[3px] grid h-[22px] w-[22px] shrink-0 place-items-center rounded-md text-zinc-600 opacity-0 hover:bg-white/[0.06] hover:text-zinc-300 group-hover:opacity-100 disabled:opacity-40"
        >
          <Undo2 className="h-3 w-3" />
        </button>
      )}
    </li>
  );
}

interface Props {
  mainId: string;
  mainBusy: boolean;
}

export default function AgentProgressPanel({ mainId, mainBusy }: Props) {
  const main = shortId(mainId);
  const [helper, setHelper] = useState('');
  const [helperError, setHelperError] = useState('');
  const [todos, setTodos] = useState<ProgressTodo[] | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [showDone, setShowDone] = useState(false);
  const [pending, setPending] = useState<string | null>(null);
  const refreshTimer = useRef<number | null>(null);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [split, setSplit] = useState(readSplit);
  const [dragging, setDragging] = useState(false);
  useEffect(() => { try { localStorage.setItem(SPLIT_KEY, String(split)); } catch {} }, [split]);

  // Pointer capture keeps the drag on the handle even when the cursor passes
  // over the terminal (xterm would otherwise eat the move events).
  const onSplitPointerDown = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    setDragging(true);
  }, []);
  const onSplitPointerMove = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    if (!dragging || !rootRef.current) return;
    const rect = rootRef.current.getBoundingClientRect();
    if (rect.height <= 0) return;
    setSplit(clampSplit((event.clientY - rect.top) / rect.height));
  }, [dragging]);
  const endSplitDrag = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    setDragging(false);
  }, []);
  const onSplitKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 0.1 : 0.03;
    if (event.key === 'ArrowUp') { event.preventDefault(); setSplit((v) => clampSplit(v - step)); }
    else if (event.key === 'ArrowDown') { event.preventDefault(); setSplit((v) => clampSplit(v + step)); }
    else if (event.key === 'Home') { event.preventDefault(); setSplit(SPLIT_MIN); }
    else if (event.key === 'End') { event.preventDefault(); setSplit(SPLIT_MAX); }
  }, []);

  // Create (first open) or look up the helper. Creating it also starts the
  // first summary so the list fills without a click.
  useEffect(() => {
    let alive = true;
    setHelper(''); setHelperError('');
    (async () => {
      try {
        const got = await apiService.getAgentProgress(main);
        let id = String(got.data?.helper || '');
        if (!id) {
          const made = await apiService.refreshAgentProgress(main);
          if (!made.data?.success) throw new Error(made.data?.error || 'create failed');
          id = String(made.data.helper || '');
        }
        if (alive) setHelper(id);
      } catch (err: any) {
        if (alive) setHelperError(String(err?.response?.data?.detail || err?.message || err));
      }
    })();
    return () => { alive = false; };
  }, [main]);

  const loadTodos = useCallback(async () => {
    try {
      const res = await apiService.listTodos(main, false, { status: 'all' });
      const list: ProgressTodo[] = (res.data?.todos || []).filter((t: ProgressTodo) => t.status !== 'dropped');
      setTodos(list);
    } catch {
      setTodos((cur) => cur || []);
    }
  }, [main]);

  useEffect(() => {
    void loadTodos();
    const id = window.setInterval(() => { void loadTodos(); }, POLL_MS);
    return () => window.clearInterval(id);
  }, [loadTodos]);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    try {
      const res = await apiService.refreshAgentProgress(main);
      if (res.data?.helper) setHelper(String(res.data.helper));
    } catch {}
    // The helper works asynchronously; keep the spinner briefly as feedback.
    if (refreshTimer.current) window.clearTimeout(refreshTimer.current);
    refreshTimer.current = window.setTimeout(() => setRefreshing(false), 1500);
  }, [main]);

  useEffect(() => () => { if (refreshTimer.current) window.clearTimeout(refreshTimer.current); }, []);

  // Auto-update when the main agent finishes a turn (busy → idle). The server
  // skips the request while the helper is still working on a previous one.
  const wasBusy = useRef(mainBusy);
  useEffect(() => {
    if (wasBusy.current && !mainBusy && helper) void refresh();
    wasBusy.current = mainBusy;
  }, [mainBusy, helper, refresh]);

  // Manual confirmation: the user marks a task as verified (status done).
  // Undo puts it back to "待确认" (test).
  const setStatus = useCallback(async (todo: ProgressTodo, status: 'done' | 'test') => {
    setPending(todo.id);
    setTodos((cur) => (cur || []).map((t) => (t.id === todo.id ? { ...t, status, updated_at: new Date().toISOString() } : t)));
    try {
      await apiService.updateTodo(main, todo.id, { status });
    } catch {
      setTodos((cur) => (cur || []).map((t) => (t.id === todo.id ? todo : t)));
    } finally {
      setPending(null);
      void loadTodos();
    }
  }, [main, loadTodos]);

  const byRecent = (a: ProgressTodo, b: ProgressTodo) => String(b.updated_at || '').localeCompare(String(a.updated_at || ''));
  const doing = (todos || []).filter((t) => t.status === 'todo');
  const review = (todos || []).filter((t) => t.status === 'test').sort(byRecent);
  const done = (todos || []).filter((t) => t.status === 'done').sort(byRecent);

  return (
    <div ref={rootRef} data-id={`agent-progress-${main}`} className={cn('flex h-full min-h-0 flex-col', dragging && 'cursor-row-resize select-none')}>
      <div data-id={`agent-progress-list-${main}`} className="flex min-h-0 shrink-0 flex-col" style={{ height: `${split * 100}%` }}>
        <div className="flex h-8 shrink-0 items-center gap-2 px-3 text-[11px] text-zinc-500">
          <span>进行中 <b className="font-medium text-amber-400">{doing.length}</b></span>
          <span>待确认 <b className="font-medium text-sky-400">{review.length}</b></span>
          <span>已确认 <b className="font-medium text-emerald-500">{done.length}</b></span>
          <button
            type="button"
            data-id={`agent-progress-refresh-${main}`}
            onClick={(event) => { event.stopPropagation(); void refresh(); }}
            disabled={!helper || refreshing}
            title="让进度助手根据最新对话更新列表"
            className="ml-auto flex h-6 items-center gap-1 rounded-md px-2 text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-40"
          >
            <RefreshCw className={cn('h-3 w-3', refreshing && 'animate-spin')} />
            更新
          </button>
        </div>
        <div className="fade-scroll-y min-h-0 flex-1 overflow-y-auto px-1 pb-2" onWheel={(e) => e.stopPropagation()}>
          {todos === null ? (
            <div className="grid h-full place-items-center"><Loader2 className="h-4 w-4 animate-spin text-zinc-600" /></div>
          ) : todos.length === 0 ? (
            <p data-id={`agent-progress-empty-${main}`} className="px-3 py-6 text-center text-[12px] text-zinc-600">
              {helper ? '进度助手正在整理对话，稍后这里会列出已完成和进行中的任务。' : '正在启动进度助手…'}
            </p>
          ) : (
            <>
              {doing.length ? (
                <ul data-id={`agent-progress-doing-${main}`} className="space-y-0.5">
                  {doing.map((t) => <TodoRow key={t.id} todo={t} kind="doing" busy={pending === t.id} onConfirm={() => setStatus(t, 'done')} />)}
                </ul>
              ) : null}
              {review.length ? (
                <>
                  <div className="mt-2 px-2 text-[11px] text-zinc-600">待确认</div>
                  <ul data-id={`agent-progress-review-${main}`} className="space-y-0.5">
                    {review.map((t) => <TodoRow key={t.id} todo={t} kind="review" busy={pending === t.id} onConfirm={() => setStatus(t, 'done')} />)}
                  </ul>
                </>
              ) : null}
              {done.length ? (
                <>
                  <button type="button" onClick={() => setShowDone((v) => !v)} className="mt-2 flex items-center gap-1 px-2 text-[11px] text-zinc-600 hover:text-zinc-400">
                    {showDone ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                    已确认 {done.length}
                  </button>
                  {showDone ? (
                    <ul data-id={`agent-progress-done-${main}`} className="space-y-0.5">
                      {done.map((t) => <TodoRow key={t.id} todo={t} kind="confirmed" busy={pending === t.id} onUndo={() => setStatus(t, 'test')} />)}
                    </ul>
                  ) : null}
                </>
              ) : null}
            </>
          )}
        </div>
      </div>
      <div
        data-id={`agent-progress-splitter-${main}`}
        role="separator"
        aria-orientation="horizontal"
        aria-label="拖动调整进度列表与终端的高度"
        aria-valuemin={Math.round(SPLIT_MIN * 100)}
        aria-valuemax={Math.round(SPLIT_MAX * 100)}
        aria-valuenow={Math.round(split * 100)}
        tabIndex={0}
        title="上下拖动调整高度，双击恢复默认"
        onPointerDown={onSplitPointerDown}
        onPointerMove={onSplitPointerMove}
        onPointerUp={endSplitDrag}
        onPointerCancel={endSplitDrag}
        onDoubleClick={() => setSplit(0.55)}
        onKeyDown={onSplitKeyDown}
        className={cn(
          'group relative z-10 flex h-2 shrink-0 cursor-row-resize touch-none items-center justify-center border-y border-white/[0.06] bg-white/[0.02] outline-none transition-colors',
          'hover:bg-sky-500/10 focus-visible:bg-sky-500/15',
          dragging && 'bg-sky-500/20'
        )}
      >
        <span className={cn('h-[3px] w-10 rounded-full bg-white/20 transition-colors group-hover:bg-sky-400/70', dragging && 'bg-sky-400')} />
      </div>
      <div data-id={`agent-progress-helper-${main}`} onWheel={(e) => e.stopPropagation()} className={cn('min-h-0 flex-1 overflow-hidden', dragging && 'pointer-events-none')}>
        {helperError ? (
          <p className="grid h-full place-items-center px-4 text-center text-[11px] text-red-400">进度助手启动失败：{helperError}</p>
        ) : helper ? (
          <CurrentHistoryView key={helper} paneId={helper} open pollLive fullWidth leftAlignQuestions agentType="cicy" />
        ) : (
          <div className="grid h-full place-items-center"><Loader2 className="h-4 w-4 animate-spin text-zinc-600" /></div>
        )}
      </div>
    </div>
  );
}
