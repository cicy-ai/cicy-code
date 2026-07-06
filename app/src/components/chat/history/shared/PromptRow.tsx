// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo, useCallback, useEffect, useRef, useState } from 'react';
import { ChevronRight } from 'lucide-react';
import apiService from '../../../../services/api';
import type { HistoryTurn } from '../types';
import { ANSWER_RENDER_CAP } from '../constants';
import { getCurrentHistory } from '../lib/dataAccess';
import { buildTurnsFromRawItems, replyItemsToSteps, turnSig } from '../lib/turns';
import { isActiveAssistantStatus, formatPromptTimeAgo } from '../lib/misc';
import { historyMemCache } from '../lib/cache';
import { AssistantTurnView } from './AssistantTurnView';
import { CollapsibleQ } from './CollapsibleQ';

// PromptRow is ONE question in prompts-only mode: the q text with a 小箭头 on its
// RIGHT. Expand state + the answer live LOCALLY here (not in the parent list
// memo), so expanding one q re-renders only this row — no whole-list churn (the
// jank). The answer (turn id+1) is loaded LAZILY on first expand: reuse the
// already-loaded window turn if present, else fetch via getCurrentHistory. The
// answer renders through the shared AssistantTurnView.
export const PromptRow = memo(function PromptRow({ turn, qid, nextQid, dataId, paneId, conversationId, agentType, hideTools }: {
  turn: HistoryTurn; qid: number; nextQid?: number; dataId?: string;
  paneId: string; conversationId: string; agentType: string; hideTools: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [answer, setAnswer] = useState<HistoryTurn[] | 'loading' | 'none' | undefined>(undefined);
  const [showAll, setShowAll] = useState(false); // an answer can span 40+ turns; render the latest few, rest behind a button (bounds DOM → no jank)
  const inFlightRef = useRef(false); // a request for THIS q is in progress → don't fire a duplicate
  // Per-id stable turn objects: a poll re-builds turns every 1.5s, but unchanged
  // (already-finished) turns must keep the SAME reference so memo(AssistantTurnView)
  // skips them — only the streaming tail re-renders. Without this, the last q's
  // whole multi-tool answer re-rendered every poll → lag (the "limit" jank).
  const turnCacheRef = useRef<Map<number, { sig: string; turn: HistoryTurn }>>(new Map());
  // Committed turns from the last FULL load — the live poll reuses these instead of
  // re-fetching + re-building the whole answer window every tick (that rebuild on
  // up to 400 items every 1.5s was the real jank). Poll only refreshes the tail.
  const committedRef = useRef<HistoryTurn[]>([]);
  const toggle = useCallback(() => setOpen((o) => !o), []);
  // Load the answer LAZILY on first expand. The answer is the agent's FULL
  // response: EVERY assistant turn after this q up to the NEXT prompt (a real
  // reply spans many assistant messages — one per tool round). For the LAST q
  // whose reply hasn't migrated into current.json yet, there are no committed
  // turns → fall back to reply.json's live items. Each turn is rendered with the
  // SAME AssistantTurnView the main history uses (no new component).
  //
  // The answer MUST be read from the SAME snapshot the q's id came from. history
  // ids are POSITIONAL in current.json and DRIFT across snapshots (compaction),
  // so any id-keyed cross-snapshot cache (IndexedDB by id) can return a DIFFERENT
  // turn's content → q↔a misalignment. We therefore resolve only against:
  //   1) SYNC  — the in-memory window snapshot loaded THIS open (fresh, same ids)
  //              → instant, no spinner, aligned. Covers prompts in the window.
  //   2) NET   — a FRESH current.json fetch by conversation (not the id cache) for
  //              older prompts → consistent ids with the q list → aligned.
  //   3) reply.json — the last q whose answer hasn't migrated into current.json yet.
  // startedRef bridges the gap before the first setAnswer so a parent re-render
  // (live poll) mid-read can't kick off a duplicate fetch while answer===undefined.
  // Resolve THIS q's full answer = every assistant turn in (qid, nextQid). The
  // LAST q (no nextQid) is open-ended and also folds in the live reply.json tail
  // (the still-streaming part not yet migrated to current.json); it returns
  // {live} so the poller knows to keep refreshing until the turn finishes.
  const loadAnswer = useCallback(async (tailOnly = false): Promise<{ live: boolean }> => {
    if (inFlightRef.current) return { live: false }; // 已在请求 → 不重复发
    inFlightRef.current = true;
    const collect = (turns: HistoryTurn[]): HistoryTurn[] =>
      turns
        .filter((t) => t?.role === 'assistant' && Number(t?.history_id || 0) > qid && (!nextQid || Number(t?.history_id || 0) < nextQid))
        .sort((a, b) => Number(a?.history_id || 0) - Number(b?.history_id || 0));
    const upperExclusive = nextQid && nextQid > qid ? nextQid : qid + 400;
    try {
      // Committed turns: fetched+rebuilt only on a FULL load; the poll (tailOnly)
      // reuses the cached set so it doesn't re-build the whole window every 1.5s.
      let out: HistoryTurn[];
      if (tailOnly) {
        out = committedRef.current.slice();
      } else {
        const limit = Math.max(16, upperExclusive - qid);
        const data: any = await getCurrentHistory(paneId, { before: upperExclusive, limit, conversation_id: conversationId });
        out = collect(buildTurnsFromRawItems(Array.isArray(data?.items) ? data.items : []));
        committedRef.current = out.slice();
      }
      let live = false;
      if (!nextQid) {
        const r: any = (await apiService.getAgentCurrentReply(paneId, conversationId ? { conversation_id: conversationId } : undefined))?.data;
        if (r && Number(r.history_id || 0) > qid) {
          live = isActiveAssistantStatus(String(r.status || ''));
          const steps = replyItemsToSteps(r.items, r.thinking, r.answer);
          const maxCommitted = out.reduce((m, t) => Math.max(m, Number(t?.history_id || 0)), qid);
          // Append the live tail only if it sits AFTER the committed turns (avoids
          // double-rendering a turn that already migrated into current.json).
          if ((steps.length || String(r.answer || '').trim()) && Number(r.history_id || 0) > maxCommitted) {
            out.push({ role: 'assistant', history_id: Number(r.history_id || 0), q: '', text: '', a: String(r.answer || ''), steps, status: String(r.status || '') } as HistoryTurn);
          }
        }
      }
      // Stabilize refs: reuse the cached turn object when its content signature is
      // unchanged, so memo(AssistantTurnView) skips re-rendering finished turns.
      const stable = out.map((t) => {
        const id = Number(t?.history_id || 0);
        const s = turnSig(t);
        const cached = turnCacheRef.current.get(id);
        if (cached && cached.sig === s) return cached.turn;
        turnCacheRef.current.set(id, { sig: s, turn: t });
        return t;
      });
      setAnswer(stable.length ? stable : 'none');
      return { live };
    } catch {
      // revalidate failed → keep the cached answer if we have one
      setAnswer((prev) => (Array.isArray(prev) ? prev : 'none'));
      return { live: false };
    } finally {
      inFlightRef.current = false;
    }
  }, [qid, nextQid, paneId, conversationId]);

  // On every expand: CACHE-FIRST paint, then revalidate.
  //  1) keep a cached answer (no flash); else paint the in-memory snapshot
  //     instantly; else show 加载回复….
  //  2) fire a FRESH API request to update this q's a — unless one is already in
  //     flight (loadAnswer self-dedups via inFlightRef).
  //  3) nudge the parent to refresh the q text too (promptList), so q & a update together.
  useEffect(() => {
    if (!open) return;
    let snapTurns: HistoryTurn[] | null = null;
    if (nextQid) {
      const snap = historyMemCache().get(paneId);
      const ids = (snap?.items || []).map((t) => Number(t?.history_id || 0)).filter((n) => n > 0);
      if (snap && ids.length) {
        const minId = Math.min(...ids);
        const maxId = Math.max(...ids);
        if (qid >= minId && maxId >= nextQid - 1) {
          const turns = (snap.items || [])
            .filter((t) => t?.role === 'assistant' && Number(t?.history_id || 0) > qid && Number(t?.history_id || 0) < nextQid)
            .sort((a, b) => Number(a?.history_id || 0) - Number(b?.history_id || 0));
          if (turns.length) snapTurns = turns;
        }
      }
    }
    setAnswer((prev) => (Array.isArray(prev) ? prev : (snapTurns ?? 'loading')));
    void loadAnswer();
    window.dispatchEvent(new CustomEvent('cicy:current-history-refresh'));
  }, [open, qid, nextQid, paneId, conversationId, loadAnswer]);

  // LAST q only: keep the in-progress answer live. Stream deltas drive snappy
  // updates (tail-only = just reply.json, cheap), with a 1.5s timer as fallback.
  // Only this one row re-renders; it stops once the reply goes terminal.
  useEffect(() => {
    if (!open || nextQid) return;
    let stop = false;
    let timer: number | undefined;
    let debounce: number | undefined;
    const tick = async () => {
      if (stop) return;
      const { live } = await loadAnswer(true); // tail-only: just reply.json, reuse committed
      if (!stop && live) timer = window.setTimeout(tick, 1500);
    };
    // stream chunk → refresh the live tail fast (debounced; tail-only is cheap)
    const onDelta = () => {
      if (debounce) window.clearTimeout(debounce);
      debounce = window.setTimeout(() => { void loadAnswer(true); }, 200);
    };
    window.addEventListener('cicy:agent-stream-delta', onDelta as EventListener);
    timer = window.setTimeout(tick, 1500);
    return () => {
      stop = true;
      if (timer) window.clearTimeout(timer);
      if (debounce) window.clearTimeout(debounce);
      window.removeEventListener('cicy:agent-stream-delta', onDelta as EventListener);
    };
  }, [open, nextQid, loadAnswer]);
  const ts = (turn as any)?.ts as string | undefined;
  return (
    <div data-id={dataId} data-turn-key={String(qid)} className="mb-3">
      {/* Whole row is the toggle: caret on the LEFT, q in the middle, relative
          time on the right. Click anywhere on the row to expand/collapse a. */}
      <div
        role="button"
        tabIndex={0}
        data-id={`current-history-q-toggle-${qid}`}
        onClick={toggle}
        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(); } }}
        aria-expanded={open}
        aria-label={open ? '收起回复' : '展开回复'}
        className="group -mx-1.5 flex cursor-pointer items-start gap-1.5 rounded-lg px-1.5 py-1 transition-colors hover:bg-white/[0.035]"
      >
        <ChevronRight
          data-id={`current-history-q-caret-${qid}`}
          className={`mt-1 h-3.5 w-3.5 shrink-0 text-zinc-500 transition-transform group-hover:text-zinc-300 ${open ? 'rotate-90' : ''}`}
        />
        <div className="min-w-0 flex-1"><CollapsibleQ text={turn.text || turn.q} bare /></div>
        {ts ? (
          <span data-id={`current-history-q-time-${qid}`} className="mt-1 shrink-0 text-[11px] leading-none text-zinc-600 tabular-nums" title={ts}>
            {formatPromptTimeAgo(ts)}
          </span>
        ) : null}
      </div>
      {open ? (
        <div data-id={`current-history-q-answer-${qid}`} className="mt-1 pl-5">
          {answer === undefined ? null : answer === 'loading' ? (
            <span className="text-xs text-zinc-600">加载回复…</span>
          ) : answer === 'none' ? (
            <span className="text-xs text-zinc-600">（无回复内容）</span>
          ) : (() => {
            // Render only the LATEST ANSWER_RENDER_CAP turns by default (a reply can
            // be 40+ tool rounds → rendering all at once is the jank). The streaming
            // tail (last q) is at the end, so it's always shown; earlier rounds are
            // behind "展开更早". One avatar on the first shown turn.
            const cap = ANSWER_RENDER_CAP;
            const hidden = !showAll && answer.length > cap ? answer.length - cap : 0;
            const shown = hidden ? answer.slice(hidden) : answer;
            return (
              <>
                {hidden ? (
                  <button
                    type="button"
                    data-id={`current-history-q-answer-more-${qid}`}
                    onClick={(e) => { e.stopPropagation(); setShowAll(true); }}
                    className="mb-1.5 text-[11px] text-zinc-500 hover:text-zinc-300"
                  >
                    展开更早的 {hidden} 轮
                  </button>
                ) : null}
                {shown.map((t, i) => (
                  <AssistantTurnView
                    key={`${t.history_id || i}`}
                    turn={t}
                    turnKey={`qa-${qid}-${t.history_id || i}`}
                    isLatestTurn={false}
                    showAvatar={i === 0}
                    agentType={agentType}
                    paneId={paneId}
                    hideTools={hideTools}
                  />
                ))}
              </>
            );
          })()}
        </div>
      ) : null}
    </div>
  );
});
