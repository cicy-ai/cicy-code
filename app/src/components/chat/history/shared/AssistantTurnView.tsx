// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo } from 'react';
import AgentAvatar from '../../../AgentAvatar';
import type { HistoryTurn } from '../types';
import { getVisibleHistorySteps } from '../lib/turns';
import { buildToolCardId, normalizeToolStepsForDisplay } from '../lib/toolFormat';
import { ThinkingBlock } from './ThinkingBlock';
import { MarkdownBlock } from './Markdown';
import { ToolCard } from './ToolCard';
import { PendingThinkingPlaceholder } from './notices';

// AssistantTurnView renders ONE assistant turn (avatar + ordered thinking/text/
// tool steps + fallback answer + pending placeholder) — lifted out of the main
// list so the prompts-only q-expand reuses the EXACT same rendering as the full
// history (no parallel answer renderer).
export const AssistantTurnView = memo(function AssistantTurnView({ turn, turnKey, isLatestTurn, showAvatar, agentType, paneId, hideTools }: {
  turn: HistoryTurn; turnKey: string | number; isLatestTurn: boolean; showAvatar: boolean;
  agentType: string; paneId: string; hideTools: boolean;
}) {
  const allSteps = getVisibleHistorySteps(turn, isLatestTurn);
  const displaySteps = normalizeToolStepsForDisplay(allSteps || []);
  const steps = hideTools ? displaySteps.filter((s: any) => s?.type !== 'tool') : displaySteps;
  const showThinkingPlaceholder = isLatestTurn && String(turn?.status || '').trim() === 'thinking' && !String(turn?.a || '').trim() && !steps.length;
  // A tool with no result in the LAST tool step of a still-active latest turn is
  // running right now (the CLI is executing it) — render a spinner, not a ✓.
  const turnActive = isLatestTurn && /^(thinking|streaming|pending|tool_use|running|in_progress|working)$/.test(String(turn?.status || '').trim());
  const lastToolStepIndex = turnActive ? steps.reduce((acc: number, s: any, i: number) => (s?.type === 'tool' ? i : acc), -1) : -1;
  const toolOnlyTurn = steps.length > 0 && steps.every((step: any) => step?.type === 'tool');
  const hasRenderableAssistantStep = steps.some((step: any) => {
    if (step?.type === 'thinking' && String(step?.text || '').trim()) return true;
    if (step?.type === 'text' && String(step?.text || '').trim()) return true;
    if (step?.type === 'tool' && Array.isArray(step?.tools) && step.tools.length > 0) return true;
    return false;
  });
  const fallbackAnswer = String(turn?.a || '').trim();
  if (!hasRenderableAssistantStep && !fallbackAnswer && !showThinkingPlaceholder) return null;
  return (
    <div data-id={`current-history-turn-assistant-${turnKey}`} className="flex items-start gap-2.5">
      {showAvatar
        ? <AgentAvatar agentType={agentType} title={paneId} variant="select" dataId={`current-history-assistant-avatar-${turnKey}`} className="mt-0.5 h-7 w-7 rounded-full" />
        : <div aria-hidden="true" className="h-7 w-7 shrink-0" />}
      <div data-id={`current-history-turn-assistant-body-${turnKey}`} className="min-w-0 flex-1">
        {steps.map((step: any, stepIndex: number) => {
          if (step.type === 'thinking') {
            return <div key={stepIndex} data-id={`current-history-turn-step-thinking-${turnKey}-${stepIndex}`}><ThinkingBlock text={step.text} /></div>;
          }
          if (step.type === 'text') {
            return <div key={stepIndex} data-id={`current-history-turn-step-text-${turnKey}-${stepIndex}`} className="chat-markdown current-history-markdown text-sm leading-[1.7] text-zinc-300"><MarkdownBlock text={step.text} /></div>;
          }
          const tools = Array.isArray(step.tools) ? step.tools : [];
          return <div key={stepIndex} data-id={`current-history-turn-step-tools-${turnKey}-${stepIndex}`} className={`${toolOnlyTurn ? '' : 'my-2'} space-y-1.5`}>{tools.map((tool: any, toolIndex: number) => {
            const toolId = buildToolCardId(turnKey, stepIndex, tool, toolIndex);
            const running = stepIndex === lastToolStepIndex && !String(tool?.result || '').trim() && tool?.isError !== true;
            return <ToolCard key={toolId} tool={tool} toolId={toolId} running={running} />;
          })}</div>;
        })}
        {!hasRenderableAssistantStep && fallbackAnswer ? (
          <div data-id={`current-history-turn-fallback-${turnKey}`} className="chat-markdown current-history-markdown text-sm leading-[1.7] text-zinc-300">
            <MarkdownBlock text={fallbackAnswer} />
          </div>
        ) : null}
        {!hasRenderableAssistantStep && !fallbackAnswer && showThinkingPlaceholder ? <PendingThinkingPlaceholder /> : null}
      </div>
    </div>
  );
});
