// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Spinner } from '../../ui/Spinner';
import AgentAvatar from '../../AgentAvatar';
import type { HistoryTurn } from './types';
import { OPTIMISTIC_Q_KEY } from './constants';
import { OpenUrlContext, QAlignContext } from './contexts';
import { isActiveAssistantStatus } from './lib/misc';
import { buildToolCardId } from './lib/toolFormat';
import { MarkdownBlock, LinkConfirmModal } from './shared/Markdown';
import { CollapsibleQ, UserTurnAvatar } from './shared/CollapsibleQ';
import { ToolCard } from './shared/ToolCard';
import { LiveStreamStep } from './shared/LiveStreamStep';
import { SystemNoticeCard, OutcomeNoticeCard, PendingThinkingPlaceholder, CompactionMarker } from './shared/notices';
import { cicyCompactSummaryOf } from './lib/normalizeItem';
import { AssistantTurnView } from './shared/AssistantTurnView';
import { PromptRow } from './shared/PromptRow';
import type { useCurrentHistory } from './useCurrentHistory';

type HistoryListProps = ReturnType<typeof useCurrentHistory> & {
  paneId: string;
  agentType: string;
  promptsOnly: boolean;
  hideTools: boolean;
  fullWidth: boolean;
  leftAlignQuestions: boolean;
  greeting: string;
};

export function HistoryList(props: HistoryListProps) {
  const {
    items,
    liveTurn,
    optimisticQ,
    compacting,
    displayItems,
    committedMaxId,
    loading,
    loadingMore,
    conversationId,
    pendingUrl,
    setPendingUrl,
    retryingKey,
    recapResponses,
    handleOutcomeRetry,
    loadMore,
    canLoadMore,
    scrollRef,
    loadMoreRef,
    streamLoadingVisibleRef,
    optimisticBaselineUserIdRef,
    paneId,
    agentType,
    promptsOnly,
    hideTools,
    fullWidth,
    leftAlignQuestions,
    greeting,
  } = props;
  const { t } = useTranslation('chat');
  // Content column width: full-bleed when embedded (AgentStack popover), else a
  // centered reading column.
  const listWidthClass = fullWidth ? 'w-full' : 'mx-auto w-full max-w-4xl';

  // Memoized on `displayItems`: while a turn streams (only `liveTurn` changes),
  // these element refs stay identical, so React skips re-rendering every
  // committed history row (no Markdown re-parse per token).
  const renderedTurns = useMemo(() => displayItems.map((turn, index) => {
    const turnKey = turn?.history_id || `${turn?.text || turn?.q || 'turn'}-${index}`;
    const isLatestTurn = index === displayItems.length - 1;
    const itemId = Number(turn?.history_id || 0);
    // Prompts-only: render just the user questions, drop everything else.
    if (promptsOnly && turn?.role !== 'user') return null;
    // Drop the assistant recap that answers a harness-only recap-on-return turn.
    if (recapResponses.has(turn)) return null;
    // A cicy "turn produced no reply" record (cancel / post-retry failure) is just
    // an assistant output: agent avatar on the left + the failed/stopped notice and
    // a 重试 on the latest turn — same column/avatar layout as a normal reply.
    if (turn?.outcome) {
      return (
        <div data-id={itemId > 0 ? String(itemId) : undefined} data-turn-key={String(turnKey)} key={turnKey} className="mb-5">
          <div data-id={`current-history-turn-assistant-${turnKey}`} className="flex items-start gap-2.5">
            <AgentAvatar agentType={agentType} title={paneId} variant="select" dataId={`current-history-assistant-avatar-${turnKey}`} className="mt-0.5 h-7 w-7 rounded-full" />
            <div data-id={`current-history-turn-assistant-body-${turnKey}`} className="min-w-0 flex-1">
              <OutcomeNoticeCard
                text={turn.text || ''}
                outcome={turn.outcome}
                detail={turn.outcomeDetail}
                canRetry={isLatestTurn && !!paneId && turn.outcome !== 'blocked'}
                retrying={retryingKey === String(turnKey)}
                onRetry={() => handleOutcomeRetry(String(turnKey))}
              />
            </div>
          </div>
        </div>
      );
    }
    if (turn?.role === 'system') {
      return (
        <div data-id={itemId > 0 ? String(itemId) : undefined} data-turn-key={String(turnKey)} key={turnKey} className="my-1">
          <SystemNoticeCard text={turn.text || ''} />
        </div>
      );
    }
    if (turn?.role === 'user') {
      // /compact's appended summary renders as the ✨已压缩 timeline marker (a
      // foldable divider) — never as a user bubble / prompt row.
      const compactSummary = cicyCompactSummaryOf(turn.text || turn.q || '');
      if (compactSummary !== null) {
        return (
          <div data-id={itemId > 0 ? String(itemId) : undefined} data-turn-key={String(turnKey)} key={turnKey}>
            <CompactionMarker summary={compactSummary} />
          </div>
        );
      }
      // Prompts-only: each real q (scaffold already filtered out in displayItems)
      // renders as a self-managed PromptRow — local expand + lazy answer load on
      // caret click, so expanding one q never re-renders the whole list.
      if (promptsOnly) {
        if (itemId > 0) {
          return (
            <PromptRow
              key={turnKey}
              turn={turn}
              qid={itemId}
              nextQid={Number(displayItems[index + 1]?.history_id || 0) || undefined}
              dataId={String(itemId)}
              paneId={paneId}
              conversationId={conversationId}
              agentType={agentType}
              hideTools={hideTools}
              isLatest={isLatestTurn}
            />
          );
        }
      }
      return (
        <div data-id={itemId > 0 ? String(itemId) : undefined} data-turn-key={String(turnKey)} key={turnKey} className="mb-5">
          {leftAlignQuestions ? (
            <div data-id={`current-history-turn-user-${turnKey}`} className="flex items-start gap-2.5">
              <UserTurnAvatar />
              <div className="min-w-0 flex-1"><CollapsibleQ text={turn.text || turn.q} /></div>
            </div>
          ) : (
            <CollapsibleQ text={turn.text || turn.q} />
          )}
        </div>
      );
    }
    if (turn?.role === 'assistant') {
      // 头像 = 每"轮回答"一个(ChatGPT 语义),不是每个 assistant item 一个:一次回答
      // 会拆成多个 assistant item(工具循环),只有紧跟 q 的第一条出头像,其余只留
      // 同宽的左侧空位保持内容列对齐。往回看时跳过 system 通知。
      let prevRole = '';
      for (let j = index - 1; j >= 0; j -= 1) {
        const r = String(displayItems[j]?.role || '');
        if (r === 'system') continue;
        prevRole = r;
        break;
      }
      const showAvatar = prevRole !== 'assistant';
      // Providers may persist one tool loop as several consecutive assistant
      // records, while the live/reply path keeps the same tools in one record.
      // Use the same 6px rhythm as ToolCard's `space-y-1.5` between consecutive
      // assistant records; reserve the larger turn gap for the final record.
      let nextRole = '';
      for (let j = index + 1; j < displayItems.length; j += 1) {
        const r = String(displayItems[j]?.role || '');
        if (r === 'system') continue;
        nextRole = r;
        break;
      }
      const continuesAssistant = nextRole === 'assistant';
      return (
        <div data-id={itemId > 0 ? String(itemId) : undefined} data-turn-key={String(turnKey)} data-assistant-continuation={continuesAssistant ? 'true' : 'false'} key={turnKey} className={continuesAssistant ? 'mb-1.5' : 'mb-5'}>
          {/* ChatGPT 式回复头像:agent_type 的 logo 在答案左侧,与首行顶对齐;
              同一轮的后续 assistant item 不重复头像,用同宽空位对齐内容列 */}
          <AssistantTurnView turn={turn} turnKey={turnKey} isLatestTurn={isLatestTurn} showAvatar={showAvatar} agentType={agentType} paneId={paneId} hideTools={hideTools} />
        </div>
      );
    }
    return null;
  }), [displayItems, promptsOnly, hideTools, recapResponses, agentType, paneId, retryingKey, conversationId, leftAlignQuestions]);

  // Part 2 — the live tail (reply.json's answer for the latest turn). It is the
  // ANSWER to committed's last turn (q_last), so it renders answer-only and sits
  // right after the committed list. committedMaxId (defined above) == q_last id;
  // the tail's id == committedMaxId + 1, so `> committedMaxId` gates it (and
  // dedups against an already-migrated turn after switching away and back).
  const liveVisible = !promptsOnly && !!liveTurn && Number(liveTurn.history_id || 0) > committedMaxId;
  // 乐观 q 的真身已落库(committed 里出现了 id > 乐观基线的 user 轮)→ 这一帧就别再渲染
  // 乐观占位。清乐观 state 的 setOptimisticQ(null) 在 useEffect 里晚一帧,若不在渲染层先
  // 压住,中间会有一帧「committed 真 q + 乐观 q」两个一样的气泡并存 → 高度抖一下再收回 =
  // 发送瞬间的「跳」,还连带把贴底滚动算歪。这里渲染层同帧抹掉,保证零重叠帧。
  const optimisticLanded = !!optimisticQ && items.some((t) => t?.role === 'user' && Number(t?.history_id || 0) > optimisticBaselineUserIdRef.current);
  const showOptimistic = !!optimisticQ && !optimisticLanded;
  const liveTurnSteps = liveVisible && Array.isArray(liveTurn?.steps) ? liveTurn!.steps : [];
  // 本轮还在流式输出 → 最后一个 thinking/text 块走平滑生长(useSmoothStreamText)。
  const liveStreaming = liveVisible && isActiveAssistantStatus(String(liveTurn?.status || ''));
  // 平滑生长的每个 tick 都发生在 LiveStreamStep 内部 state,父级贴底 effect(依赖
  // liveTurn)看不到 —— 用这个回调在每次生长后跟一次底(仅当用户本来就贴底)。
  // useCallback([]):引用恒定,memo 化的 LiveStreamStep 对未生长的块才能命中缓存。
  const pinBottomIfSticking = useCallback(() => {
    const el = scrollRef.current;
    if (el && streamLoadingVisibleRef.current) el.scrollTop = el.scrollHeight;
  }, [streamLoadingVisibleRef]);
  // live 尾巴的头像同样按"每轮一个":它前面(committed 末尾,跳过 system)已是同轮的
  // assistant item 时不重复,只留空位对齐。
  const liveShowAvatar = (() => {
    for (let j = displayItems.length - 1; j >= 0; j -= 1) {
      const r = String(displayItems[j]?.role || '');
      if (r === 'system') continue;
      return r !== 'assistant';
    }
    return true;
  })();
  const renderedLiveTurn = liveVisible && liveTurn ? (
    <div data-id="current-history-live-turn" className="mb-5">
      <div data-id="current-history-live-turn-assistant" className="flex items-start gap-2.5">
        {liveShowAvatar
          ? <AgentAvatar agentType={agentType} title={paneId} variant="select" dataId="current-history-live-turn-avatar" className="mt-0.5 h-7 w-7 rounded-full" />
          : <div aria-hidden="true" className="h-7 w-7 shrink-0" />}
        <div data-id="current-history-live-turn-body" className="min-w-0 flex-1">
        {liveTurnSteps.map((step: any, i: number) => {
          {/* live 尾巴 = 最新一轮回复(其后还没有新 q)→ thinking 全程展开,不折叠。
              折叠的触发是「出现新 q、本轮迁入 committed」,而不是「流式刚结束」——届时它
              改走 committed 渲染(ThinkingBlock 默认 live=false)自动塌成小标。 */}
          if (step?.type === 'thinking') return <div key={i}><LiveStreamStep kind="thinking" text={step.text} smooth={liveStreaming && i === liveTurnSteps.length - 1} onGrow={pinBottomIfSticking} /></div>;
          if (step?.type === 'text') return <div key={i}><LiveStreamStep kind="text" text={step.text} smooth={liveStreaming && i === liveTurnSteps.length - 1} onGrow={pinBottomIfSticking} /></div>;
          if (step?.type === 'tool' && !hideTools && Array.isArray(step?.tools) && step.tools.length > 0) {
            return <div key={i} data-id={`current-history-live-turn-step-tools-${i}`} className="my-2 space-y-1.5">{step.tools.map((tool: any, toolIndex: number) => {
              const toolId = buildToolCardId(`live-${liveTurn!.history_id}`, i, tool, toolIndex);
              return <ToolCard key={toolId} tool={tool} toolId={toolId} />;
            })}</div>;
          }
          return null;
        })}
        {!liveTurnSteps.length ? (
          <div data-id="current-history-stream-loading"><PendingThinkingPlaceholder /></div>
        ) : null}
        {liveStreaming && liveTurnSteps.length ? (
          <div data-id="current-history-stream-loading" className="flex h-6 items-center gap-1 pt-1" aria-label="Loading reply">
            {[0, 1, 2].map((index) => (
              <span key={index} data-id="current-history-stream-loading-dot" className="h-1.5 w-1.5 animate-bounce rounded-full bg-zinc-500" style={{ animationDelay: `${index * 140}ms` }} />
            ))}
          </div>
        ) : null}
        </div>
      </div>
    </div>
  ) : null;

  return (
    <QAlignContext.Provider value={leftAlignQuestions ? 'left' : 'right'}>
    <OpenUrlContext.Provider value={setPendingUrl}>
    <div data-id="current-history-view" className="flex h-full flex-col bg-[#0b0b0d]">
      {pendingUrl ? <LinkConfirmModal url={pendingUrl} onClose={() => setPendingUrl(null)} /> : null}
      {!loading && displayItems.length === 0 && !liveVisible && !optimisticQ && !compacting ? (
        greeting ? (
          // 开场白渲染成一条正常的 assistant reply:左上角、带 agent 头像 + markdown
          // 内容列,与真实答案同布局(不再居中占位)。
          <div data-id="current-history-empty-greeting" className="min-h-0 flex-1 overflow-y-auto fade-scroll-y">
            <div className={`${listWidthClass} px-4 py-6 font-sans text-zinc-300`}>
              <div data-id="current-history-empty-greeting-turn" className="flex items-start gap-2.5">
                <AgentAvatar agentType={agentType} title={paneId} variant="select" dataId="current-history-empty-greeting-avatar" className="mt-0.5 h-7 w-7 rounded-full" />
                <div data-id="current-history-empty-greeting-text" className="chat-markdown current-history-markdown min-w-0 flex-1 text-sm leading-[1.7] text-zinc-300">
                  <MarkdownBlock text={greeting} />
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div data-id="current-history-empty" className="flex flex-1 flex-col items-center justify-center px-4">
            <div className="w-full max-w-2xl">
              <div className="mb-5 text-center">
                <div data-id="current-history-empty-icon" className="mb-3 text-3xl text-zinc-600">✦</div>
                <p data-id="current-history-empty-text" className="mt-1 text-xs text-zinc-500">{t('emptyHistory')}</p>
              </div>
            </div>
          </div>
        )
      ) : (
      <>
      <div data-id="current-history-scroll" ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto fade-scroll-y">
        <div data-id="current-history-list" data-agent-id={paneId || ''} className={`${listWidthClass} px-4 py-6 font-sans text-zinc-300`}>
          {loading ? (
            <div data-id="current-history-loading" className="space-y-6 py-2" aria-busy="true">
              {[0, 1, 2].map((row) => (
                <div key={row} data-id={`current-history-loading-row-${row}`} className="space-y-3">
                  {leftAlignQuestions ? (
                    <div className="flex items-start gap-2.5">
                      <div className="mt-0.5 h-7 w-7 shrink-0 animate-pulse rounded-full bg-white/[0.05]" />
                      <div className="h-8 w-1/2 animate-pulse rounded-2xl bg-white/[0.05]" />
                    </div>
                  ) : (
                    <div className="flex justify-end">
                      <div className="h-8 w-1/2 animate-pulse rounded-2xl bg-white/[0.05]" />
                    </div>
                  )}
                  {/* 答案骨架与真实布局一致:左侧 28px 圆形头像位 + 右侧内容列 */}
                  <div className="flex items-start gap-2.5">
                    <div data-id={`current-history-loading-avatar-${row}`} className="mt-0.5 h-7 w-7 shrink-0 animate-pulse rounded-full bg-white/[0.05]" />
                    <div className="min-w-0 flex-1 space-y-2">
                      <div className="h-3.5 w-11/12 animate-pulse rounded bg-white/[0.04]" />
                      <div className="h-3.5 w-4/5 animate-pulse rounded bg-white/[0.04]" />
                      <div className="h-3.5 w-2/3 animate-pulse rounded bg-white/[0.04]" />
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : <>
            {canLoadMore ? (
              <div ref={loadMoreRef} data-id="current-history-load-more-wrap" className="mb-4 flex justify-center">
                <button
                  type="button"
                  data-id="current-history-load-more"
                  onClick={() => { void loadMore(); }}
                  disabled={loadingMore}
                  className="inline-flex items-center gap-1.5 rounded-md border border-white/[0.07] bg-white/[0.025] px-3 py-1.5 text-xs text-zinc-500 transition-colors hover:border-white/[0.12] hover:text-zinc-300 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {loadingMore ? <Spinner size="xs" /> : null}
                  {loadingMore ? t('loadingMore') : t('loadEarlier')}
                </button>
              </div>
            ) : null}
            {renderedTurns}
            {renderedLiveTurn}
            {/* /compact live marker: painted the instant it's sent (onNudge) so the
                user SEES the backend is working; cleared when the summary lands. */}
            {compacting ? <CompactionMarker live /> : null}
            {/* 占位 q + a 必须排在 renderedLiveTurn 之后 —— 新问题 q2 是最新的一轮,而上一轮
                的答案 a1 在被 reconcileTail 迁进 committed 之前仍以 renderedLiveTurn(live 尾巴)
                渲染。若把 q2 排在它前面,顺序会变成 q1 → q2 → a1(q2 把 q1 的答案挤开、硬钉到顶
                又把 q1 顶出屏幕),这就是"q2 覆盖 q1"。放到最后,顺序恒为 …q1, a1, q2, a2占位。 */}
            {showOptimistic ? (
              <>
                {/* q 占位:独立块渲染,塞进/撤掉绝不触发 committed 列表(renderedTurns memo)
                    重算 → 历史 Markdown 不重渲染,q 点发送瞬间即现、不卡。sending 态(略透明),
                    真 q 落库后此块消失、committed 里的真 q 顶到同一位置。 */}
                <div data-turn-key={OPTIMISTIC_Q_KEY} className="mb-5">
                  <div data-id="current-history-optimistic-q" className="opacity-60 transition-opacity">
                    {leftAlignQuestions ? (
                      <div className="flex items-start gap-2.5">
                        <UserTurnAvatar />
                        <div className="min-w-0 flex-1"><CollapsibleQ text={optimisticQ.text} /></div>
                      </div>
                    ) : (
                      <CollapsibleQ text={optimisticQ.text} />
                    )}
                  </div>
                </div>
                {/* a 占位:先撑出答案位(thinking),真答案一开始流式就由 renderedLiveTurn
                    接管 —— 占位 → 真 a,无新建、不跳。
                    闸门用 `!liveStreaming`(不是 `!liveVisible`):
                    - 新一轮真在流(thinking/streaming)→ liveStreaming=true → 撤乐观占位,
                      交给 renderedLiveTurn 自己的占位,任一帧只一个「Thinking…」(原防重复意图)。
                    - 但上一轮 completed 的答案会赖在 live 尾巴(cicy 懒迁移,liveVisible=true 但
                      status=completed → liveStreaming=false)。此时发新 Q 必须**立刻**画 thinking,
                      不能被上一轮的尾巴误杀 —— 否则 thinking 要等 poll 拉回新一轮才出(“a 半天才显示”)。 */}
                {!liveStreaming ? (
                  <div data-id="current-history-optimistic-a" className="mb-5 flex items-start gap-2.5">
                    <AgentAvatar agentType={agentType} title={paneId} variant="select" dataId="current-history-optimistic-a-avatar" className="mt-0.5 h-7 w-7 rounded-full" />
                    <div data-id="current-history-stream-loading" className="min-w-0 flex-1">
                      <PendingThinkingPlaceholder />
                    </div>
                  </div>
                ) : null}
              </>
            ) : null}
            {/* Keep a permanent tail slot after the latest assistant answer.
                During retry the latest a rapidly swaps between pending, failed
                and live snapshots; without a stable final node the scroll
                height briefly collapses and the whole conversation jumps. */}
            <div
              data-id="current-history-final-answer-placeholder"
              aria-hidden="true"
              className="h-8 shrink-0"
            />
          </>}
        </div>
      </div>
      </>
      )}
    </div>
    </OpenUrlContext.Provider>
    </QAlignContext.Provider>
  );
}
