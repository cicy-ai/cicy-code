// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import apiService from '../../../services/api';
import type { HistoryTurn } from './types';
import {
  CURRENT_HISTORY_WINDOW,
  CURRENT_HISTORY_POLL_ACTIVE_MS,
  CURRENT_HISTORY_POLL_IDLE_MS,
  CURRENT_HISTORY_POLL_WAIT_MS,
  OPTIMISTIC_Q_TIMEOUT_MS,
} from './constants';
import { historyMemCache, setHistoryMemCache } from './lib/cache';
import { getHistoryIDs, loadWindowItems } from './lib/dataAccess';
import { buildTurnsFromRawItems, normalizeHistoryTurns } from './lib/turns';
import { splitLeadingHarnessBlocks, cicyCompactSummaryOf } from './lib/normalizeItem';
import { liveStepsContentSize, scheduleScrollToBottom } from './lib/misc';

export function useCurrentHistory(opts: {
  paneId: string;
  open: boolean;
  promptsOnly: boolean;
  hideTools: boolean;
  agentType: string;
  consumeWsDeltas: boolean;
}) {
  const { paneId, open, promptsOnly, consumeWsDeltas } = opts;
  const [items, setItems] = useState<HistoryTurn[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [conversationId, setConversationId] = useState('');
  const [model, setModel] = useState('');
  // Prompts-only q list, served clean & aligned from the backend (current.json's
  // `prompts`: real human questions only, de-noised + de-duplicated at write
  // time, ids matching the snapshot's positional history ids). This is the SOLE
  // source for the prompts-only view — no fragile client-side scaffold filtering,
  // no cross-snapshot id-cache drift. See aiGatewayBuildCurrentPrompts (backend).
  const [promptList, setPromptList] = useState<{ id: number; ts: string; content: string }[]>([]);
  const [pendingUrl, setPendingUrl] = useState<string | null>(null);  // external link awaiting confirm (#25)
  // Which outcome-notice turn is mid-retry (spinner on its 重试 button). Keyed by
  // the turn key so only the clicked one spins.
  const [retryingKey, setRetryingKey] = useState<string | null>(null);
  // The in-flight turn (polled from reply.json) lives OUTSIDE `items` as a
  // temporary trailing group, so refreshing it never re-renders the committed
  // history list. It's reconciled into `items` (via a tail fetch) once the turn
  // completes, then dropped.
  const [liveTurn, setLiveTurn] = useState<HistoryTurn | null>(null);
  const liveTurnRef = useRef<HistoryTurn | null>(null);
  const liveTurnIdRef = useRef('');         // backend turn_id of the live turn
  const maxLoadedIdRef = useRef(0);         // largest history id currently in `items`
  const committedReadyRef = useRef(false);  // Part 1 (committed window) finished loading
  const firstReplyDoneRef = useRef(false);  // Part 2's first poll resolved → safe to reveal
  const scrollRef = useRef<HTMLDivElement>(null);
  const loadMoreRef = useRef<HTMLDivElement>(null);   // sentinel: when it scrolls into view, auto-load earlier
  const loadMoreFnRef = useRef<() => void>(() => {});  // latest loadMore, so the observer never calls a stale closure
  const didInitialScrollRef = useRef(false);
  // ChatGPT 式跟随:用户贴在底部时,新内容 / reply 流式增长就自动滚到底;一旦往上滚离开
  // 底部就放手,不再拽回。没有 spacer、不把问题钉到顶 —— 就是一条普通从下往上长的聊天流。
  const shouldStickBottomRef = useRef(true);
  // 上一次观测到的 scrollTop,用来判方向:用户**向上**滚一下就放手(disengage),
  // 直到自己滚回底部才重新跟随。靠方向而不是"距底阈值",否则流式期每 700ms 一次的
  // 强制落底会把人按在底部——小幅上滚还没出阈值就被拽回。
  const lastScrollTopRef = useRef(0);
  const preserveScrollOffsetRef = useRef(false);
  // 「加载更早」前插时的滚动补偿数据(setItems 前一刻的 scrollTop/scrollHeight)。
  // 补偿必须在 useLayoutEffect(paint 前)同步完成 —— 之前放在 requestAnimationFrame
  // (paint 后)里,向上滚动触发自动翻页时会先画一帧被顶下去的内容再跳回来。
  const preservedScrollMetricsRef = useRef<{ top: number; height: number } | null>(null);
  const requestSeqRef = useRef(0);
  // 乐观占位:用户点发送的瞬间就先塞一个 q 气泡(sending 态)+ 一个 a 占位(thinking),
  // 不等后端往返。baseline 记录占位时的最大 user history_id,真 q 落库后(committed user
  // turn 的 id 超过 baseline)就把锚点交接给真 q 并撤掉占位 —— 同一位置,不闪不跳。
  const [optimisticQ, setOptimisticQ] = useState<{ text: string; ts: number } | null>(null);
  const optimisticBaselineUserIdRef = useRef(0);
  // /compact in-flight:发出瞬间置 true(onNudge)→ HistoryList 画「压缩中…」分隔线;
  // 追加的摘要落地(最后一项命中 cicyCompactSummaryOf)或超时(100s,后端 90s 上限)清除。
  const [compacting, setCompacting] = useState(false);
  const compactingTsRef = useRef(0);
  const compactingRef = useRef(false);
  useEffect(() => { compactingRef.current = compacting; }, [compacting]);
  // /clear 发出即清:onNudge 里立刻把视图重置为空(不等后端轮转),并记住被清的
  // 会话 id —— 该会话的任何轮询数据(旧 tail / 旧 reply)一律不收,防止后端 rotate
  // 完成前老内容闪回。轮询观测到不同的 cid(新会话)时解除。
  const clearedConvIdRef = useRef('');
  const conversationIdRef = useRef('');
  useEffect(() => { conversationIdRef.current = conversationId; }, [conversationId]);
  useEffect(() => {
    if (!compacting) return;
    const last = items[items.length - 1] as any;
    if (last && cicyCompactSummaryOf(last.text || last.q) !== null) { setCompacting(false); return; }
    const remaining = Math.max(1000, 100000 - (Date.now() - compactingTsRef.current));
    const t = window.setTimeout(() => setCompacting(false), remaining);
    return () => window.clearTimeout(t);
  }, [compacting, items]);
  // 给输入框广播"是否还在等回复"。busy = 有占位 q(刚发出) 或 轮询发现 in-flight 回复
  // (未 complete 且非 fail/error)。只在变化时 emit。供 DispatcherChat 锁发送、显示 waiting。
  const optimisticActiveRef = useRef(false);
  // 轮询观测到的"回复是否进行中"。锚定逻辑用它区分:真正的新回合(in-flight,该钉顶)
  // vs 切换/重开/softRebind 后 positional history_id 变化导致的"伪新问题"(完成态,
  // 钉顶会演出一段"打字进场",让人误以为 agent 还在工作)。
  const replyInFlightRef = useRef(false);
  const lastBusyEmitRef = useRef<boolean | null>(null);
  const emitBusy = (busy: boolean) => {
    if (lastBusyEmitRef.current === busy) return;
    lastBusyEmitRef.current = busy;
    window.dispatchEvent(new CustomEvent('cicy:dispatcher-busy', { detail: { paneId, busy } }));
  };
  // items 的最新快照,供轮询 effect 的 onNudge(闭包里的 items 是旧的)读取 baseline。
  const itemsRef = useRef<HistoryTurn[]>([]);
  const scheduledScrollRafRef = useRef<number | null>(null);
  const scheduledScrollTimersRef = useRef<number[]>([]);

  const clearScheduledScrolls = () => {
    if (scheduledScrollRafRef.current != null) {
      window.cancelAnimationFrame(scheduledScrollRafRef.current);
      scheduledScrollRafRef.current = null;
    }
    for (const timer of scheduledScrollTimersRef.current) {
      window.clearTimeout(timer);
    }
    scheduledScrollTimersRef.current = [];
  };

  const runScheduledScroll = (scheduled: { raf: number; timers: number[] }) => {
    clearScheduledScrolls();
    scheduledScrollRafRef.current = scheduled.raf;
    scheduledScrollTimersRef.current = scheduled.timers;
  };

  const clearLiveTurn = () => {
    liveTurnRef.current = null;
    liveTurnIdRef.current = '';
    setLiveTurn(null);
  };

  // Fetch everything current.json now holds beyond our committed boundary —
  // ONLY the new tail (committedMaxId, newMax], never re-pull below it. This
  // fires when the boundary advances (a new turn started, or a tool round
  // reseeded current.json mid-turn). Returns the built turns WITHOUT touching
  // state: the poll commits items + live tail in ONE synchronous batch (one
  // render). Applying them across separate awaits paints an inconsistent frame
  // (boundary moved but the tail not yet re-attached → committed thinking flips
  // collapsed/expanded → the list visibly jumps once per round).
  const fetchTailBeyondBoundary = async (): Promise<{ tail: HistoryTurn[]; newMax: number } | null> => {
    try {
      const ids = await getHistoryIDs(paneId);
      const cid = String(ids?.conversation_id || '').trim();
      const newMax = Number(ids?.id || 0);
      if (!cid || newMax <= maxLoadedIdRef.current) return null;
      // /clear 发出即清后,被清会话的 tail 不收(防老内容闪回)。
      if (clearedConvIdRef.current && cid === clearedConvIdRef.current) return null;
      const size = Math.min(newMax - maxLoadedIdRef.current, 100);
      // fresh: the just-completed tail's slots may collide with stale cache
      // from an earlier conversation at the same positional history_id.
      const { items: raw } = await loadWindowItems(paneId, cid, newMax, size, { fresh: true });
      const tail = buildTurnsFromRawItems(raw);
      if (!tail.length) return null;
      return { tail, newMax };
    } catch {
      return null;
    }
  };

  // Seamless conversation rotation. Some agents rotate conversation_id on EVERY
  // turn while resending the full context (opencode does this — see docs §11).
  // The hard rebind (setRebindKey → reset effect → open effect with setLoading)
  // blanks the whole list and force-scrolls to bottom on every single round —
  // very visible to the user. Instead swap the new conversation's window IN
  // PLACE: keep the old turns mounted, let React diff by history_id so only the
  // genuinely-new turn paints (no skeleton, no scroll jump). conversationId is
  // NOT a dependency of the reset/open effects, so updating it here only
  // re-subscribes the poll against the new conversation — it does not reload.
  const softRebind = async (nextCid: string) => {
    const seq = ++requestSeqRef.current;
    try {
      const ids = await getHistoryIDs(paneId);
      if (seq !== requestSeqRef.current) return;
      const cid = String(ids?.conversation_id || '').trim() || nextCid;
      const newMax = Number(ids?.id || 0);
      // /clear 发出即清后,被清会话不重载(等 rotate 到新会话再 rebind)。
      if (clearedConvIdRef.current && cid === clearedConvIdRef.current) return;
      // 新会话是空的(/clear 后):必须把视图完整重置成空,而不是只换 id。
      // 否则旧 turn 留在屏上,且 maxLoadedIdRef 仍停在旧会话的 max —— 新会话里
      // 任何小 id 的答案/报错都会被判在边界下方而永不 attach(错误也出不来)。
      if (!cid || newMax <= 0) {
        maxLoadedIdRef.current = 0;
        clearLiveTurn();
        setItems([]);
        setHasMore(false);
        setNextBefore(null);
        setConversationId(cid);
        return;
      }
      const { items: raw, lo } = await loadWindowItems(paneId, cid, newMax, CURRENT_HISTORY_WINDOW, { fresh: true });
      if (seq !== requestSeqRef.current) return;
      const turns = buildTurnsFromRawItems(raw);
      maxLoadedIdRef.current = newMax;
      setHasMore(lo > 1);
      setNextBefore(lo);
      setModel(String(ids?.model || '').trim());
      // The just-finished answer (old live tail) is now a committed turn in the
      // new window; clear the tail in the same batch as setItems so it never
      // renders twice. The live tail re-attaches for the new in-progress turn.
      clearLiveTurn();
      setItems(turns);
      setConversationId(cid);
    } catch {}
  };

  useEffect(() => {
    shouldStickBottomRef.current = true;
    lastScrollTopRef.current = 0;
    preserveScrollOffsetRef.current = false;
    maxLoadedIdRef.current = 0;
    committedReadyRef.current = false;
    firstReplyDoneRef.current = false;
    setHasMore(false);
    setNextBefore(null);
    setConversationId('');
    setModel('');
    clearLiveTurn();
    clearScheduledScrolls();
    setOptimisticQ(null);
    optimisticBaselineUserIdRef.current = 0;
    replyInFlightRef.current = false;
    setCompacting(false);
    clearedConvIdRef.current = '';
  }, [paneId, open]);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    // Position-based (authoritative) follow: the view sticks to the bottom ONLY
    // while the user is within STICK_THRESHOLD_PX of it. Scrolling up past that
    // detaches. Crucially the streaming auto-scroll below is gated on this flag,
    // so once detached NO programmatic scroll runs — the user is never yanked back
    // mid-output while reading earlier history; they re-attach by scrolling to the
    // bottom themselves. Direction-sniffing (`top < last-2`) was replaced because
    // it went stale under reflow / slow trackpad scrolls and let the yank through.
    const STICK_THRESHOLD_PX = 48;
    const updateStickBottom = () => {
      const top = el.scrollTop;
      const distanceToBottom = el.scrollHeight - top - el.clientHeight;
      const atBottom = distanceToBottom <= STICK_THRESHOLD_PX;
      shouldStickBottomRef.current = atBottom;
      if (!atBottom) clearScheduledScrolls();
      lastScrollTopRef.current = top;
    };
    updateStickBottom();
    // 用户意图优先:滚轮/触摸板向上滚一格就立即放手。scroll 事件的方向检测对触摸板
    // 慢速上滚不可靠(单次事件 <2px 不触发),流式期每次内容变更的强制落底会把人
    // 反复拽回底部 ——「滚动的时候也在跳」就是这个。wheel/touch 直接表达意图,不丢。
    const disengage = () => {
      shouldStickBottomRef.current = false;
      clearScheduledScrolls();
    };
    const onWheel = (e: WheelEvent) => {
      if (e.deltaY < 0) disengage();
    };
    let touchY = 0;
    const onTouchStart = (e: TouchEvent) => {
      touchY = e.touches[0]?.clientY ?? 0;
      disengage();
    };
    const onPointerDown = () => disengage();
    const onTouchMove = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY ?? 0;
      if (y > touchY + 2) disengage(); // 手指向下拖 = 内容向上滚
      touchY = y;
    };
    el.addEventListener('scroll', updateStickBottom, { passive: true });
    el.addEventListener('wheel', onWheel, { passive: true });
    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: true });
    el.addEventListener('pointerdown', onPointerDown, { passive: true });
    return () => {
      el.removeEventListener('scroll', updateStickBottom);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
      el.removeEventListener('pointerdown', onPointerDown);
    };
  }, [open, paneId]);

  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    const requestSeq = ++requestSeqRef.current;
    didInitialScrollRef.current = false;
    shouldStickBottomRef.current = true;
    // 内存快照先上屏(window._cacheHistory):同一 pane 上次打开的整页内容**同步**渲染,
    // 不出 loading 骨架;下面的 fresh 加载照常进行,回来后整体覆盖。快照只填渲染态,不置
    // committedReadyRef —— live tail 的轮询仍等真实窗口落定,避免旧快照和新 live 混拼。
    const snap = historyMemCache().get(paneId);
    const hasSnap = !!(snap && snap.items.length);
    if (hasSnap && snap) {
      setItems(snap.items);
      setConversationId(snap.conversationId);
      setModel(snap.model);
      setHasMore(snap.hasMore);
      setNextBefore(snap.nextBefore);
      maxLoadedIdRef.current = snap.maxId;
      // live 尾巴(最后一轮答案)同步还原,首帧就完整;首次 poll 会用最新 reply 覆盖。
      if (snap.liveTurn) {
        liveTurnRef.current = snap.liveTurn;
        setLiveTurn(snap.liveTurn);
      }
    }
    if (!hasSnap) setLoading(true);
    getHistoryIDs(paneId)
      .then(async (data) => {
        if (cancelled || requestSeq !== requestSeqRef.current) return [] as HistoryTurn[];
        const nextConversationId = String(data?.conversation_id || '').trim();
        const nextMaxHistoryId = Number(data?.id || 0);
        setConversationId(nextConversationId);
        // 切 pane 会按 key={paneId} 重挂载,先用 B 自己的 mem 快照秒绘(含上一会话的 live
        // 尾巴)。但若 B 的会话已轮换/清过(conversation_id 变了),那条旧 live 尾巴不属于这
        // 个新窗口——它会以 renderedLiveTurn 排在用户新发的 q **之前**(=「a 在 q 上面」),
        // 直到下一次 poll 才纠正。这里一拿到真实会话就比对:不一致即丢掉残留的 live 尾巴和
        // 乐观占位,让新窗口从干净尾巴开始。
        if (hasSnap && snap && snap.conversationId && snap.conversationId !== nextConversationId) {
          clearLiveTurn();
          setOptimisticQ(null);
          optimisticBaselineUserIdRef.current = 0;
        }
        setModel(String(data?.model || '').trim());
        // Clean prompts list straight from current.json (the prompts-only source).
        setPromptList(Array.isArray(data?.prompts) ? data.prompts : []);
        if (!nextConversationId || nextMaxHistoryId <= 0) {
          setHasMore(false);
          setNextBefore(null);
          return [] as HistoryTurn[];
        }
        // Atomic, ordered, complete window load. fresh:true so the open always
        // pulls THIS conversation's current window from the server (bypassing
        // possibly-stale cache for the mutable visible window) — every open
        // re-resolves history by conversation. (docs §11)
        const { items: rawItems, lo } = await loadWindowItems(
          paneId,
          nextConversationId,
          nextMaxHistoryId,
          CURRENT_HISTORY_WINDOW,
          { fresh: true },
        );
        maxLoadedIdRef.current = nextMaxHistoryId;
        setHasMore(lo > 1);
        setNextBefore(lo);
        return buildTurnsFromRawItems(rawItems);
      })
      .then((latestItems) => {
        if (cancelled || requestSeq !== requestSeqRef.current || !latestItems) return;
        setItems(latestItems);
      })
      .catch(() => {
        if (cancelled || requestSeq !== requestSeqRef.current) return;
        setItems([]);
        setHasMore(false);
        setNextBefore(null);
        setConversationId('');
      })
      .finally(() => {
        // Gate on `cancelled` ONLY — not requestSeq. A concurrent loadMore /
        // softRebind bumps requestSeqRef while this load is in flight; bailing
        // here would strand committedReadyRef=false forever (the poll then
        // spins in its early-return branch doing zero network → the view is
        // permanently dead). A genuinely superseded load (paneId/open change)
        // always has cancelled=true via the cleanup.
        if (cancelled) return;
        // Part 1 done → poll may attach the tail. Do NOT reveal yet: keep the
        // skeleton until Part 2's first poll resolves, so committed + the live
        // tail paint together (single scroll-to-bottom, no open-time flash).
        committedReadyRef.current = true;
        if (firstReplyDoneRef.current) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, paneId]);

  // 快照写回:渲染态(items/cid/model/分页)每次变化都同步进 window._cacheHistory,
  // 供下次打开同 pane 时秒出首屏。loading 中(骨架/空态)不写,避免把空页存成快照。
  useEffect(() => {
    if (!open || !paneId || loading || !items.length || !conversationId) return;
    setHistoryMemCache(paneId, {
      items,
      conversationId,
      model,
      hasMore,
      nextBefore,
      maxId: maxLoadedIdRef.current,
      updatedAt: Date.now(),
      liveTurn,
    });
  }, [open, paneId, items, conversationId, model, hasMore, nextBefore, loading, liveTurn]);

  // Part 2 polls reply.json (WS events only pull the next poll forward — see
  // requestPollSoon below). The reply's ANSWER occupies
  // history_id = current.maxID + 1, so it attaches right after committed's last
  // turn (q_last, id == committedMaxId): answerId == committedMaxId + 1. We
  // render it as the live tail (answer-only; the q comes from committed). When a
  // NEW turn starts, current.json's maxID advances (the prior answer migrated in
  // + new q_last appended) → fetchTailBeyondBoundary pulls ONLY that new tail.
  // We never re-pull the committed window.
  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    let timer: number | null = null;
    let lastSig = '';
    let pollInFlight = false;
    let lastPollStartAt = 0;
    // 连续"快照比本地短"的次数(WS 直推领先是常态,但持续领先可能是拼重了)。
    let regressStreak = 0;

    // 单槽定时器:任何新的排程都顶替 pending 的那个,绝不并存两个轮询链。
    const schedule = (ms: number) => {
      if (cancelled) return;
      if (timer != null) window.clearTimeout(timer);
      timer = window.setTimeout(() => { timer = null; void poll(); }, ms);
    };

    // Reveal (drop the skeleton) once Part 2's first poll has resolved AND the
    // tail it produced is already in state — so committed + tail paint together.
    const revealOnce = () => {
      if (firstReplyDoneRef.current) return;
      firstReplyDoneRef.current = true;
      if (committedReadyRef.current) setLoading(false);
    };

    const poll = async () => {
      if (cancelled || pollInFlight) return;
      // Wait for Part 1 (committed window) so the tail attaches to a real
      // boundary and the poll never races the open load.
      if (!committedReadyRef.current) { schedule(CURRENT_HISTORY_POLL_WAIT_MS); return; }
      pollInFlight = true;
      lastPollStartAt = Date.now();
      try {
        // Do NOT pin to the committed conversationId: pinning would make the
        // endpoint always resolve the old conversation, so a rotation could
        // never be observed. Poll the agent's CURRENT conversation instead.
        const { data } = await apiService.getAgentCurrentReply(paneId);
        if (cancelled) return;
        const cid = String(data?.conversation_id || '').trim();

        // 忙/闲只取决于「agent 当前这条 reply 是否在生成」(就是刚拉到的 data),
        // 跟 committed 视图绑的是哪个会话无关。必须在下面 mismatch 早返回之前 emit:
        // 否则 /clear 建新会话 → 走 softRebind 后 return,会跳过 emit,开场白后端早
        // 完成了 composer 却永久锁在「回复生成中」(发不出下一条)。answerId 等也在此
        // 一次性算好,供后续 tail 逻辑复用(不再在下面重复声明)。
        const answerId = Number(data?.history_id || 0); // = current.maxID + 1
        const complete = !!data?.complete;
        const replyStatus = String(data?.status || '').trim().toLowerCase();
        const replyFailed = replyStatus === 'failed' || replyStatus === 'fail' || replyStatus === 'error';
        const replyMaxId = answerId > 0 ? answerId - 1 : 0; // current.json maxID == q_last id
        const replyInFlight = answerId > 0 && !complete && !replyFailed;
        replyInFlightRef.current = replyInFlight;
        emitBusy(optimisticActiveRef.current || replyInFlight);

        // /clear 发出即清后:被清会话的任何数据一律不收(否则 rotate 完成前老内容
        // 闪回)。快速轮询等 rotate;观测到新会话 cid 时解除封锁、走正常 rebind。
        if (clearedConvIdRef.current) {
          if (cid && cid === clearedConvIdRef.current) {
            schedule(CURRENT_HISTORY_POLL_ACTIVE_MS);
            return;
          }
          clearedConvIdRef.current = '';
        }

        // The agent rotated to a DIFFERENT conversation than committed is
        // showing → rebind onto the new conversation. (docs §11)
        if (conversationId && cid && cid !== conversationId) {
          // Seamless in-place swap (no blank reload / scroll jump). The poll
          // effect re-subscribes once softRebind updates conversationId.
          revealOnce();
          await softRebind(cid);
          // ALWAYS reschedule: softRebind can silently bail (seq race) or fail
          // (network error / server restart → catch{}), leaving conversationId
          // unchanged — without this the effect never re-subscribes and the
          // poll loop dies permanently (UI stuck on "thinking" forever). If the
          // rebind DID succeed, the dep change re-subscribes and the cleanup
          // cancels this timer — scheduling is always safe.
          schedule(CURRENT_HISTORY_POLL_WAIT_MS);
          return;
        }
        if (cid && !conversationId) setConversationId((prev) => prev || cid);
        // The conversation the live-tail ANSWER actually belongs to. Only attach
        // the tail when it matches committed (or backend didn't supply it).
        const replyCid = String(data?.reply_conversation_id || '').trim();

        // No conversation / no turn yet → nothing to attach.
        if (answerId <= 0) {
          if (liveTurnRef.current) { clearLiveTurn(); lastSig = ''; }
          revealOnce();
          schedule(CURRENT_HISTORY_POLL_IDLE_MS);
          return;
        }

        // The boundary moved past our window: either a new turn started (the
        // previous answer migrated into current.json + new q_last appended), or
        // a tool round reseeded current.json MID-turn. Pull ONLY the new tail
        // (committedMaxId, replyMaxId] — never re-pull below it. Don't commit
        // yet: items + live tail must land in the same render (see below).
        let pendingTail: { tail: HistoryTurn[]; newMax: number } | null = null;
        if (replyMaxId > maxLoadedIdRef.current) {
          pendingTail = await fetchTailBeyondBoundary();
          if (cancelled) return;
        }
        const boundary = pendingTail ? pendingTail.newMax : maxLoadedIdRef.current;

        // Attach the reply's ANSWER as the live tail of q_last. Render only when
        // it sits beyond the committed boundary (answerId == committedMaxId + 1)
        // and there's something to show (content, or still streaming).
        const answer = String(data?.answer || '');
        const thinking = String(data?.thinking || '');
        const hasContent = !!(answer || thinking);
        // /compact 失败:后端把 reply finalize 成「终态空」(complete、无内容)而不追加
        // 摘要 → 撤掉压缩中 marker(成功路径由摘要落地清除)。1.5s 宽限:防止发出瞬间
        // 上一条残留的终态空 reply(如 /clear 后)在后端开始压缩前就误杀 marker。
        if (compactingRef.current && complete && !hasContent
          && Date.now() - compactingTsRef.current > 1500) {
          setCompacting(false);
        }
        // Guard: never attach a tail whose answer belongs to a different
        // conversation (transient during rotation, before rebind reloads).
        const sameConversation = !replyCid || !conversationId || replyCid === conversationId;
        // cicy reseeds current.json every tool round, so mid-turn the boundary
        // can advance PAST the answer slot this poll snapshotted (answerId is
        // one round stale). The reply is still THIS in-flight turn — keep the
        // tail attached at the new slot instead of clear→reattach, which used to
        // unhide the committed copy (thinking collapsed) for one poll and then
        // hide it again (live thinking expanded) = the list jumping every round.
        const effectiveAnswerId = (!complete && !replyFailed)
          ? Math.max(answerId, boundary + 1)
          : answerId;
        // 乐观 q(刚发出的新 q)还在、真 answer 还没来时,reply.json 里任何「终结态」回复
        // (complete / failed,含 /clear /compact 写的 slash-ack ✅Conversation cleared /
        // ✅Compacted)都属于上一轮,不是这条新 q 的答案。/clear 后或失败轮后,committed
        // 边界停在命令/失败之前,这些旧回复的 answerId 会越过边界被贴成新 q 的答案,表现为
        // 「新 q 的 a 先显示上一条 error / 已clear,等后端推真 a 才覆盖」。乐观期间一律不贴
        // 终结态回复,显示 thinking,直到这条 q 自己的在途(streaming)回复到来。
        // 只挡「该挡的」终结态:slash-ack(/clear、/compact 回执)和 failed(上一轮 error)——
        // 这两种在乐观期间会被错贴成新 q 的答案。绝不挡正常 complete 的上一条答案 a1:否则
        // 发 q2 时 a1 会被 clearLiveTurn 清掉(覆盖)、等迁进 committed 才重现 = a1 闪一下。
        const isSlashAck = String(data?.turn_id || '') === 'slash-ack';
        const staleTerminal = (isSlashAck || replyFailed) && optimisticActiveRef.current;
        const attach = sameConversation && effectiveAnswerId > boundary && (hasContent || !complete)
          && !staleTerminal;
        // ONE synchronous commit for boundary + tail: React batches these
        // setStates into a single render, so no frame ever shows the moved
        // boundary without the matching live tail.
        if (pendingTail) {
          const tail = pendingTail.tail;
          setItems((prev) => normalizeHistoryTurns([...prev, ...tail]));
          maxLoadedIdRef.current = pendingTail.newMax;
        }
        if (attach) {
          // Only touch state when something actually changed (the "unfetched
          // item" signal), so an idle poll never re-renders.
          const turnId = String(data?.turn_id || '');
          const status = String(data?.status || 'thinking').trim() || 'thinking';
          const evModel = String(data?.model || '').trim();
          // The whole in-flight turn as ORDERED blocks from reply.json (serial SSE
          // order): thinking → tool_use → … → text. Rendering this in order is what
          // keeps a multi-round turn correct instead of splitting tools into a
          // committed block above the live thinking.
          const liveItems: any[] = Array.isArray((data as any)?.items) ? (data as any).items : [];
          // 签名必须带上 output 长度:工具结果是后一拍才注入 items 的,不计进签名的话
          // 快照"看起来没变"→ 不重建 steps → 结果永远不显示。
          const sig = `${turnId}:${effectiveAnswerId}:${status}:${String(data?.updated_at || '')}:${thinking.length}:${answer.length}:${liveItems.length}:${JSON.stringify(liveItems.map((it: any) => [it?.type, String(it?.thinking || it?.text || '').length, it?.name || '', String(it?.output ?? '').length]))}`;
          if (sig !== lastSig) {
            lastSig = sig;
            const steps: NonNullable<HistoryTurn['steps']> = [];
            for (const it of liveItems) {
              const ty = String(it?.type || '').trim();
              if (ty === 'thinking') {
                const tx = String(it?.thinking || '');
                if (tx) steps.push({ type: 'thinking', text: tx });
              } else if (ty === 'text') {
                const tx = String(it?.text || '');
                if (tx) steps.push({ type: 'text', text: tx });
              } else if (ty === 'tool_use') {
                const inp = it?.input;
                // reply.json 的 tool_use 条目本来没有结果:result 是续传请求到达时由
                // aiGatewayInjectToolResultsIntoItems 注入到 **output** 字段的(见知识库
                // tool-result-进-reply-json)。不读 output,工具卡展开就只有命令没有输出。
                const rawOut = it?.output ?? it?.result;
                const out = rawOut == null ? '' : (typeof rawOut === 'string' ? rawOut : JSON.stringify(rawOut));
                const tool = {
                  name: String(it?.name || ''),
                  arg: inp == null ? '' : (typeof inp === 'string' ? inp : JSON.stringify(inp)),
                  tool_id: String(it?.tool_id || ''),
                  result: out,
                  isError: it?.output_is_error === true || it?.is_error === true,
                };
                const last = steps[steps.length - 1];
                if (last && (last as any).type === 'tool') (last as any).tools.push(tool);
                else steps.push({ type: 'tool', tools: [tool] } as any);
              }
            }
            // Fallback for providers/paths that don't expose ordered items.
            if (!steps.length) {
              if (thinking) steps.push({ type: 'thinking', text: thinking });
              if (answer) steps.push({ type: 'text', text: answer });
            }
            // WS 直推(cicy)可能让本地尾巴比这份 poll 快照更超前(delta 先到)。
            // 同一 turn 的内容只前进不回退:快照文本更短且没带来新工具时,跳过整体
            // 替换、只同步槽位 id(边界可能已前移);下一次 poll 追平后再正常替换。
            const prevLive = liveTurnRef.current;
            let regressed = false;
            if (!complete && prevLive && turnId && turnId === liveTurnIdRef.current) {
              const prevSize = liveStepsContentSize(prevLive.steps);
              const nextSize = liveStepsContentSize(steps);
              regressed = nextSize.textLen < prevSize.textLen && nextSize.toolCount <= prevSize.toolCount;
            }
            // 防持久分叉:WS 直推领先快照一两拍是常态;但连续 3 次仍领先,大概率是
            // 竞态下拼重了 —— 强制以快照为准(自愈窗口 ~1.5s)。turn 完成时(上面
            // !complete)也总是以快照定稿。
            if (regressed) {
              regressStreak += 1;
              if (regressStreak >= 3) regressed = false;
            } else {
              regressStreak = 0;
            }
            liveTurnIdRef.current = turnId;
            if (regressed && prevLive) {
              if (Number(prevLive.history_id || 0) !== effectiveAnswerId) {
                liveTurnRef.current = { ...prevLive, history_id: effectiveAnswerId };
                setLiveTurn(liveTurnRef.current);
              }
            } else {
              // Answer-only: the question is rendered by committed's q_last turn.
              liveTurnRef.current = {
                history_id: effectiveAnswerId,
                conversation_id: cid || conversationId,
                role: 'assistant',
                q: '',
                text: '',
                a: answer,
                steps,
                status,
                model: evModel || model,
              };
              setLiveTurn(liveTurnRef.current);
            }
          }
        } else if (liveTurnRef.current) {
          clearLiveTurn();
          lastSig = '';
        }
        // First poll done and tail (if any) now in state → reveal both at once.
        revealOnce();
        // Streaming → poll fast; complete/idle → poll slow (just watching for the
        // next turn). The completed answer stays as the tail until the next turn
        // migrates it into committed.
        schedule(complete ? CURRENT_HISTORY_POLL_IDLE_MS : CURRENT_HISTORY_POLL_ACTIVE_MS);
      } catch {
        if (!cancelled) { revealOnce(); schedule(CURRENT_HISTORY_POLL_IDLE_MS); }
      } finally {
        pollInFlight = false;
      }
    };

    // ===== WS 流式直推(仅 agent_type=cicy)=====
    // 后端每个 delta 批次都 publish ai_chunk(text)/ thinking_chunk(thinking)/
    // status_change(hub.publishAgent),Workspace 转成 cicy:agent-stream-delta 窗口
    // 事件。cicy:delta 直接追加进 live 尾巴渲染,零轮询延迟;reply.json 轮询降级为
    // 校正锚 —— 中途打开页面、WS 丢包/重连、工具卡(名字/参数/结果)与多回合结构都由
    // 下一次 poll 对齐(后端先写 reply.json 再 publish,poll 快照永远 ⊇ 已推 delta)。
    // 非 cicy:保持原 reply.json 轮询 loop,WS 事件一概不消费。
    const requestPollSoon = () => {
      if (cancelled) return;
      const since = Date.now() - lastPollStartAt;
      schedule(since >= 180 ? 0 : 180 - since);
    };
    const appendStreamDelta = (kind: 'text' | 'thinking', delta: string) => {
      const lt = liveTurnRef.current;
      // 还没有 poll 基线(刚发送/刚打开页面)→ 先让 poll 把尾巴挂上,避免从流的
      // 半中间开始拼出残句。
      if (!lt) { requestPollSoon(); return; }
      const steps = Array.isArray(lt.steps) ? [...lt.steps] : [];
      const last: any = steps[steps.length - 1];
      if (last && last.type === kind) {
        const prevText = String(last.text || '');
        // 竞态去重:poll 整体替换与 WS 送达赛跑时,同一段 delta 可能已经包含在刚
        // 应用的快照里。较长的 delta 恰为当前块后缀 → 视为重复丢弃(短 delta 不判,
        // 合法重复词太多;万一漏判,poll 侧的 regressStreak 自愈兜底)。
        if (delta.length >= 6 && prevText.endsWith(delta)) return;
        steps[steps.length - 1] = { ...last, text: `${prevText}${delta}` };
      } else {
        steps.push({ type: kind, text: delta } as any);
      }
      liveTurnRef.current = { ...lt, steps, status: kind === 'thinking' ? 'thinking' : 'streaming' };
      setLiveTurn(liveTurnRef.current);
    };
    const onStreamDelta = (e: Event) => {
      const d: any = (e as CustomEvent)?.detail || {};
      const aid = String(d.agent_id || '').trim();
      if (!aid) return;
      if (aid !== paneId && !paneId.endsWith(aid) && !aid.endsWith(paneId)) return;
      if (!consumeWsDeltas) return; // 非 cicy:原轮询 loop,不消费 WS
      // 换轮/换对话的迟到 delta 不能拼进当前尾巴 —— 槽位对不上就交给 poll。
      const turnId = String(d.turn_id || '').trim();
      if (turnId && liveTurnIdRef.current && turnId !== liveTurnIdRef.current) { requestPollSoon(); return; }
      const delta = String(d.delta || '');
      const kind = d.kind === 'thinking' ? 'thinking' : (d.kind === 'text' ? 'text' : '');
      if (delta && kind) { appendStreamDelta(kind as 'text' | 'thinking', delta); return; }
      // status_change:tool_use 的工具卡内容(名字/参数)只在 reply.json 里,催 poll;
      // 其余状态只在尾巴还没挂上时催一把。
      const status = String(d.status || '').toLowerCase();
      if (status === 'tool_use' || status === 'tool_call' || status === 'working') requestPollSoon();
      else if (!liveTurnRef.current) requestPollSoon();
    };

    // 外部"立即刷新"信号(如办公室发完指令)→ 取消等待中的 idle 轮询,马上拉一次,
    // 这样刚发出去的消息不用等满 2.5s 才出现在窗口里。
    const onNudge = (e: Event) => {
      const detail = (e as CustomEvent)?.detail || {};
      const id = String(detail.paneId || '').trim();
      if (id && id !== paneId) return;
      // The sender passed the q text → reserve the two optimistic slots NOW, so
      // the bubble paints on this frame instead of after the poll round-trip.
      // Slash commands (/clear, /compact) are intercepted server-side and never
      // become a committed user turn — an optimistic bubble would never see its
      // teardown signal and would lock the composer until the 60s timeout. Skip
      // the placeholder; the command's ack arrives via the reply.json poll.
      const qText = String(detail.text || '').trim();
      const isSlashCommand = /^\/\w+(\s|$)/.test(qText);
      // /compact → paint the "压缩中…" timeline marker THIS frame (before the POST
      // round-trips), so the user sees the backend is working. Cleared when the
      // appended summary lands (or by timeout / send-failure teardown).
      if (/^\/compact(\s|$)/.test(qText)) { compactingTsRef.current = Date.now(); setCompacting(true); }
      // /clear → 发出即清:立刻重置成空视图,不等后端 rotate + 轮询往返。被清会话
      // 记进 clearedConvIdRef,它的旧数据在 poll / fetchTail / softRebind 里一律不收。
      if (/^\/clear(\s|$)/.test(qText)) {
        clearedConvIdRef.current = conversationIdRef.current;
        requestSeqRef.current += 1; // 丢弃在途的窗口加载
        maxLoadedIdRef.current = 0;
        clearLiveTurn();
        setOptimisticQ(null);
        setItems([]);
        setHasMore(false);
        setNextBefore(null);
      }
      if (qText && !isSlashCommand) {
        // 上一轮若是「生成失败」→ 新 q 就地覆盖它(后端 dropTrailingFailedTurnLocked 会丢掉
        // 失败的 q 并让新 q 复用同一个 id)。前端必须配合:删掉失败的 q + 它的 a,并把
        // committed 边界 maxLoadedIdRef 回退到失败 q 之前 —— 否则新 q 复用旧 id 后,poll 的
        // fetchTailBeyondBoundary 判 `newMax > maxLoadedIdRef` 不成立(同 id,边界只增不减),
        // 那个 slot 永不重拉 = UI 一直卡着旧 q(就是"后端 hi3、UI 还显示 hi2")。
        // 失败可能是 committed 的 error 标记,也可能还挂在 live tail(失败回复未提交),两者都要清。
        let base = itemsRef.current;
        const lastLive = liveTurnRef.current;
        const liveFailed = !!lastLive && /fail|error/i.test(String((lastLive as any)?.status || ''));
        const committedFailed = base.length > 0 && String(base[base.length - 1]?.outcome || '') === 'error';
        if (committedFailed || liveFailed) {
          let cut = base.length;
          for (let i = base.length - 1; i >= 0; i--) { if (base[i]?.role === 'user') { cut = i; break; } }
          base = base.slice(0, cut);
          setItems(base);
          maxLoadedIdRef.current = Number(base[base.length - 1]?.history_id || 0);
          clearLiveTurn();
        }
        let maxUserId = 0;
        for (const it of base) if (it?.role === 'user') maxUserId = Math.max(maxUserId, Number(it?.history_id || 0));
        optimisticBaselineUserIdRef.current = maxUserId;
        setOptimisticQ({ text: qText, ts: Date.now() });
        // 自己发消息 → 重新贴底跟随(ChatGPT:发送后视图回到底部看自己的问题和回复)。
        shouldStickBottomRef.current = true;
      }
      if (timer != null) { window.clearTimeout(timer); timer = null; }
      void poll();
    };
    // Send failed → retract the optimistic q/a slots painted on click, and the
    // "压缩中…" marker (a /compact whose POST never landed must not leave it hanging).
    const onCancelOptimistic = (e: Event) => {
      const id = String((e as CustomEvent)?.detail?.paneId || '').trim();
      if (id && id !== paneId) return;
      setOptimisticQ(null);
      setCompacting(false);
    };
    window.addEventListener('cicy:current-history-refresh', onNudge as EventListener);
    window.addEventListener('cicy:current-history-cancel-optimistic', onCancelOptimistic as EventListener);
    window.addEventListener('cicy:agent-stream-delta', onStreamDelta as EventListener);
    window.addEventListener('agent-status-change', onStreamDelta as EventListener);
    // Hidden tabs throttle chained setTimeout (Chrome intensive throttling →
    // ~1/min), so the window can be minutes stale when the user returns. Kick
    // an immediate poll on tab-visible so the view catches up on this frame.
    const onVisible = () => {
      if (document.hidden) return;
      if (timer != null) { window.clearTimeout(timer); timer = null; }
      void poll();
    };
    document.addEventListener('visibilitychange', onVisible);

    void poll();
    return () => {
      cancelled = true;
      if (timer != null) window.clearTimeout(timer);
      window.removeEventListener('cicy:current-history-refresh', onNudge as EventListener);
      window.removeEventListener('cicy:current-history-cancel-optimistic', onCancelOptimistic as EventListener);
      window.removeEventListener('cicy:agent-stream-delta', onStreamDelta as EventListener);
      window.removeEventListener('agent-status-change', onStreamDelta as EventListener);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [conversationId, open, paneId, model, consumeWsDeltas]);

  useEffect(() => {
    return () => {
      clearScheduledScrolls();
    };
  }, []);

  // ChatGPT 式跟随滚动(就是一条普通从下往上长的聊天流,没有 spacer、不钉问题到顶):
  // - 首屏:落底显示最新一轮(scheduleScrollToBottom 多次补滚,扛 markdown/图片异步 reflow)。
  // - 之后:用户贴在底部时,内容一变(committed 增量 / reply 流式增长 / 占位 q+a)就在 paint
  //   前同步钉到底,逐字增长不跳;用户往上滚离开底部(scroll 监听置 shouldStickBottom=false)
  //   就放手,绝不拽回。
  // - 「加载更早」前插内容:保持用户原滚动位置(loadMore 自己补偿 scrollTop)。
  useLayoutEffect(() => {
    if (!open || loading) return;
    const el = scrollRef.current;
    if (!el) return;
    if (preserveScrollOffsetRef.current) {
      preserveScrollOffsetRef.current = false;
      didInitialScrollRef.current = true;
      // 前插补偿:paint 前同步把视口钉回原来的内容位置,翻页瞬间画面纹丝不动。
      const saved = preservedScrollMetricsRef.current;
      preservedScrollMetricsRef.current = null;
      if (saved) el.scrollTop = saved.top + Math.max(0, el.scrollHeight - saved.height);
      return;
    }
    if (!didInitialScrollRef.current) {
      runScheduledScroll(scheduleScrollToBottom(el));
      shouldStickBottomRef.current = true;
      didInitialScrollRef.current = true;
      return;
    }
    if (shouldStickBottomRef.current) el.scrollTop = el.scrollHeight;
  }, [open, loading, items, liveTurn, optimisticQ]);

  // prompts-only 过滤瞬间换掉大半内容 → 重新定位:只看问题时滚到顶(从头读问题),
  // 关掉时回到底部(回到最新一轮)。
  useEffect(() => {
    if (!open) return;
    const frame = window.requestAnimationFrame(() => {
      const node = scrollRef.current;
      if (node) node.scrollTop = node.scrollHeight;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [promptsOnly, open]);

  const loadMore = async () => {
    if (loadingMore || loading || !nextBefore || Number(nextBefore) <= 1 || !conversationId) return;
    const requestPaneId = paneId;
    const requestSeq = ++requestSeqRef.current;
    setLoadingMore(true);
    try {
      // fresh: history_id is a POSITIONAL index into current.json, so for an
      // actively-mutating/compacting agent (e.g. w-1001 itself) an old slot's
      // content changes over time — the IndexedDB cache for that slot goes stale
      // and scroll-up would resurrect a turn that no longer lives there (e.g. a
      // skill-context block surfacing as a "/loop …" bubble). Always re-fetch
      // earlier windows by conversation rather than trusting positional cache.
      // (docs §11 / INV-9 — extends the fresh rule to loadEarlier.)
      const { items: rawItems, lo } = await loadWindowItems(
        paneId,
        conversationId,
        Number(nextBefore) - 1,
        CURRENT_HISTORY_WINDOW,
        { fresh: true },
      );
      if (requestPaneId !== paneId || requestSeq !== requestSeqRef.current) return;
      if (!rawItems.length) {
        setHasMore(false);
        setNextBefore(null);
        return;
      }
      const older = buildTurnsFromRawItems(rawItems);
      // 在 setItems 前一刻才采样滚动位置并置位补偿标记(而不是 fetch 前):fetch 期间
      // poll 的任何渲染都可能消费 preserveScrollOffsetRef,补偿就丢了 → 翻页跳屏。
      // 实际补偿由 useLayoutEffect 在 paint 前同步执行。
      const el = scrollRef.current;
      if (el) {
        preservedScrollMetricsRef.current = { top: el.scrollTop, height: el.scrollHeight };
        preserveScrollOffsetRef.current = true;
      }
      // normalizeHistoryTurns dedups by history_id and re-sorts ascending, so
      // the prepend stays ordered/complete even if windows overlap or a live
      // WS turn already inserted one of these ids.
      setItems((prev) => normalizeHistoryTurns([...older, ...prev]));
      setHasMore(lo > 1);
      setNextBefore(lo);
    } catch {
      setHasMore(false);
    } finally {
      setLoadingMore(false);
    }
  };

  const canLoadMore = Number(nextBefore || 0) > 1;
  loadMoreFnRef.current = () => { void loadMore(); };

  // Infinite scroll: auto-load earlier turns when the "加载更早" sentinel scrolls
  // into view (root = the scroll container), so the user doesn't have to click.
  // loadMore() self-guards against re-entrancy / no-more, so repeated intersection
  // callbacks during the fetch are harmless. rootMargin pre-loads a bit early.
  useEffect(() => {
    // Prompts-only: NO auto-load-earlier. The filtered q list can be shorter than
    // the viewport, which would keep this sentinel permanently intersecting and
    // page the whole (huge) history in → freeze. Initial fill is handled by the
    // eager-paging effect (to PROMPTS_ONLY_MIN_QUESTIONS); older prompts load via
    // the manual 加载更早 button.
    if (!open || !canLoadMore || promptsOnly) return;
    const root = scrollRef.current;
    const target = loadMoreRef.current;
    if (!root || !target) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) loadMoreFnRef.current();
      },
      { root, rootMargin: '200px 0px 0px 0px', threshold: 0 },
    );
    io.observe(target);
    return () => io.disconnect();
  }, [open, canLoadMore, conversationId, nextBefore, promptsOnly]);

  // committedMaxId == max history id in the loaded window (== q_last id). Defined
  // here (before the prompts-only effects) so they can gate on it.
  const committedMaxId = useMemo(
    () => items.reduce((m, t) => Math.max(m, Number(t?.history_id || 0)), 0),
    [items],
  );

  // Keep a live snapshot of `items` for the poll effect's onNudge (its closure
  // captures a stale `items` — deps don't include it).
  useEffect(() => { itemsRef.current = items; }, [items]);
  // Mirror optimisticQ into a ref so the poll closure reads the current value
  // (and keep the busy signal honest while the optimistic q is still showing).
  useEffect(() => {
    optimisticActiveRef.current = !!optimisticQ;
    if (optimisticQ) emitBusy(true);
  }, [optimisticQ]);

  // Optimistic q teardown. The real committed q has landed once a user turn with
  // an id beyond the send-time baseline shows up in `items` (reconcileTail pulled
  // q_last in) → drop the placeholder; the real q renders in its place. Also a
  // hard timeout so a send the backend never honored can't strand the bubble.
  useEffect(() => {
    if (!optimisticQ) return;
    let maxUserId = 0;
    for (const it of items) if (it?.role === 'user') maxUserId = Math.max(maxUserId, Number(it?.history_id || 0));
    if (maxUserId > optimisticBaselineUserIdRef.current) {
      setOptimisticQ(null);
      return;
    }
    const elapsed = Date.now() - optimisticQ.ts;
    const remaining = Math.max(0, OPTIMISTIC_Q_TIMEOUT_MS - elapsed);
    const timer = window.setTimeout(() => setOptimisticQ(null), remaining);
    return () => window.clearTimeout(timer);
  }, [items, optimisticQ]);

  // Prompts-only display list: union of the cached question turns and the live
  // window's user turns (live wins on id collision), deduped by history_id and
  // ordered. The cache supplies older questions instantly; `items` keeps the
  // newest ones fresh. Non-prompts-only renders the window as-is.
  // live 尾巴是否正占着最新一轮(用它隐藏 committed 里同轮的 assistant,避免重复)。只取这个
  // **布尔值**当依赖 —— liveTurn 每次 poll 都变,若直接依赖整个对象,displayItems 会每 poll 重算
  // 成新数组 → renderedTurns 跟着重算 → 所有 committed 轮(含过往 thinking)每 poll 重渲染 → 闪。
  const liveActive = !!liveTurn && Number(liveTurn.history_id || 0) > committedMaxId;
  // Prompts-only list, memoized on promptList ALONE so its identity is STABLE
  // across live polls. If it were recomputed inside displayItems (whose deps
  // include items/liveActive/committedMaxId, all of which churn every poll while
  // the agent works), every PromptRow would get a fresh `turn` prop each poll →
  // memo breaks → every expanded answer (which can be the agent's whole 40+ tool
  // round response) re-renders on every poll → the panel freezes. (卡死 root cause.)
  const promptOnlyItems = useMemo(() =>
    promptList
      .filter((p) => Number(p?.id || 0) > 0 && String(p?.content || '').trim() !== '')
      .map((p) => ({ role: 'user', history_id: p.id, text: p.content, q: p.content, ts: p.ts } as unknown as HistoryTurn)),
    [promptList]);
  const displayItems = useMemo(() => {
    if (!promptsOnly) {
      // While the live turn renders the in-flight assistant response (now WITH its
      // tool steps, in serial order), hide the committed assistant turn(s) of that
      // SAME turn — else round-0's tools render BOTH committed (above) and in the
      // live turn (below) = duplicate + out-of-order. The live turn owns the full
      // ordered render until the turn completes and migrates into committed.
      if (!liveActive) return items;
      let lastUserId = 0;
      for (const t of items) if (t?.role === 'user') lastUserId = Math.max(lastUserId, Number(t?.history_id || 0));
      return items.filter((t) => !(t?.role === 'assistant' && Number(t?.history_id || 0) > lastUserId));
    }
    // Prompts-only: the q list comes straight from the backend `prompts` (clean,
    // de-duplicated, scaffold/recap-free, ids = this snapshot's positional history
    // ids). No client-side filtering, no id-cache merge → no drift, no dups, no
    // empty bubbles. Each entry is a minimal user HistoryTurn; the answer is
    // resolved lazily per-row from the SAME snapshot (PromptRow), so q↔a always align.
    return promptOnlyItems; // stable ref → no per-poll re-render of expanded answers
  }, [promptsOnly, items, promptOnlyItems, liveActive, committedMaxId]);

  // Recap-on-return is system noise: a harness-only user turn ("The user stepped
  // away… Recap…" / continuation banner) and the assistant recap it triggers.
  // CollapsibleQ already folds the instruction into a SystemNoticeCard; this
  // drops its assistant response too, so the whole recap exchange disappears from
  // the conversation (empty turns in between keep the pending flag alive).
  const recapResponses = useMemo(() => {
    const drop = new Set<HistoryTurn>();
    let pendingRecap = false;
    for (const t of displayItems) {
      const q = String((t as any)?.text || (t as any)?.q || '');
      if (t?.role === 'user' && q.trim()) {
        const { blocks, remaining } = splitLeadingHarnessBlocks(q);
        pendingRecap = !remaining && blocks.length > 0;
        continue;
      }
      const hasContent =
        String((t as any)?.a || '').trim().length > 0 ||
        (Array.isArray((t as any)?.steps) && (t as any).steps.length > 0);
      if (t?.role === 'assistant' && pendingRecap && hasContent) {
        pendingRecap = false;
        drop.add(t);
      }
    }
    return drop;
  }, [displayItems]);

  // Refresh the prompt list while open so a newly-sent (or revoked) q stays in
  // sync — and crucially, IN LOCKSTEP with the reply/answer. The q list is
  // refreshed on the SAME signals that drive the live reply (current-history-
  // refresh / agent-stream-delta / agent-status-change), so q and a never show an
  // inconsistent transient (a sent-then-revoked q used to linger until the next
  // slow tick). Debounced (per-chunk deltas are frequent); updates only when the
  // id-signature changes (else keep the ref → no promptOnlyItems churn). A slow
  // timer is just the fallback when no signal fires.
  useEffect(() => {
    if (!open || !promptsOnly) return;
    let stop = false;
    let timer: number | undefined;
    let debounce: number | undefined;
    const sig = (ps: { id: number }[]) => ps.map((p) => p.id).join(',');
    const refetch = async () => {
      if (stop) return;
      try {
        const data: any = await getHistoryIDs(paneId);
        const ps = Array.isArray(data?.prompts) ? data.prompts : [];
        if (!stop) setPromptList((prev) => (sig(prev) === sig(ps) ? prev : ps));
      } catch { /* keep last list */ }
    };
    const onSignal = () => {
      if (debounce) window.clearTimeout(debounce);
      debounce = window.setTimeout(() => { void refetch(); }, 350);
    };
    // Only turn-boundary signals (NOT per-chunk agent-stream-delta — the q list
    // never changes mid-chunk; refetching history-ids every chunk = transcript
    // parse storm). status-change fires when a new q starts; current-history-refresh
    // is the explicit nudge (e.g. on expand).
    window.addEventListener('cicy:current-history-refresh', onSignal as EventListener);
    window.addEventListener('agent-status-change', onSignal as EventListener);
    const tick = () => { if (stop) return; void refetch(); timer = window.setTimeout(tick, 4000); };
    timer = window.setTimeout(tick, 4000);
    return () => {
      stop = true;
      if (timer) window.clearTimeout(timer);
      if (debounce) window.clearTimeout(debounce);
      window.removeEventListener('cicy:current-history-refresh', onSignal as EventListener);
      window.removeEventListener('agent-status-change', onSignal as EventListener);
    };
  }, [open, promptsOnly, paneId]);

  // Prompts-only keeps just the user questions. The committed window is only the
  // last CURRENT_HISTORY_WINDOW raw items, which holds at most a question or two
  // (and sometimes none — a long assistant/tool round fills it). The "加载更早"
  // IntersectionObserver above can't backfill an empty/short list: a
  // permanently-intersecting sentinel never re-fires. So eagerly page earlier
  // windows until PROMPTS_ONLY_MIN_QUESTIONS prompts are loaded (or we hit the
  // start) — BUT skip that entirely when the cache built at the live maxId
  // already lists the questions (the whole point of caching: don't re-page).
  useEffect(() => {
    // Obsolete in prompts-only: the q list now comes complete from the backend
    // `prompts` (promptList), so there's nothing to backfill by paging windows.
    return;
  }, [open, promptsOnly, displayItems, loading, loadingMore, canLoadMore, committedMaxId]);

  // Obsolete: the prompts-only q list is no longer assembled/cached client-side
  // (it comes clean from the backend `prompts` each open). The old id-keyed
  // IndexedDB prompt cache was a source of cross-snapshot drift — don't write it.
  useEffect(() => {
    return;
  }, [open, promptsOnly, paneId, conversationId, committedMaxId, displayItems]);

  // Re-run the latest cancelled/failed turn. Fire the retry, stick to bottom, and
  // nudge the live-tail poll so the new reply streams straight in; the spinner
  // clears once the poll surfaces a fresh turn (or after a short fallback).
  const handleOutcomeRetry = (key: string) => {
    if (!paneId || retryingKey) return;
    setRetryingKey(key);
    shouldStickBottomRef.current = true;
    Promise.resolve(apiService.retryCicyReply(paneId))
      .catch(() => {})
      .finally(() => {
        window.dispatchEvent(new CustomEvent('cicy:current-history-refresh'));
        window.setTimeout(() => setRetryingKey(null), 2000);
      });
  };

  return {
    items,
    liveTurn,
    optimisticQ,
    compacting,
    displayItems,
    committedMaxId,
    promptList,
    loading,
    loadingMore,
    hasMore,
    nextBefore,
    conversationId,
    model,
    pendingUrl,
    setPendingUrl,
    retryingKey,
    recapResponses,
    handleOutcomeRetry,
    loadMore,
    canLoadMore,
    scrollRef,
    loadMoreRef,
    shouldStickBottomRef,
    optimisticBaselineUserIdRef,
  };
}
