// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { memo, useEffect, useRef, useState } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { buildToolCardId } from '../lib/toolFormat';
import { ToolCard } from './ToolCard';

export type ToolRunEntry = {
  tool: any;
  toolId: string;
  running?: boolean;
};

export type ToolRun = {
  groupIndex: number;
  stepIndex: number;
  entries: ToolRunEntry[];
};

const toolRunOpenState = new Map<string, boolean>();
const TOOL_RUN_OPEN_STATE_MAX = 500;

function setToolRunOpen(groupId: string, open: boolean) {
  toolRunOpenState.delete(groupId);
  toolRunOpenState.set(groupId, open);
  let excess = toolRunOpenState.size - TOOL_RUN_OPEN_STATE_MAX;
  if (excess <= 0) return;
  for (const key of toolRunOpenState.keys()) {
    if (excess-- <= 0) break;
    toolRunOpenState.delete(key);
  }
}

export function buildToolRunGroupId(turnKey: string | number, groupIndex = 0) {
  const base = `tool-run:${String(turnKey).replace(/^live-/, '')}`;
  return groupIndex > 0 ? `${base}:${groupIndex}` : base;
}

export function collectToolRuns(steps: any[], turnKey: string | number, runningStepIndex = -1): ToolRun[] {
  const runs: ToolRun[] = [];
  let current: ToolRun | null = null;
  (steps || []).forEach((step: any, stepIndex: number) => {
    if (step?.type !== 'tool') {
      current = null;
      return;
    }
    if (!current) {
      current = { groupIndex: runs.length, stepIndex, entries: [] };
      runs.push(current);
    }
    (Array.isArray(step.tools) ? step.tools : []).forEach((tool: any, toolIndex: number) => {
      current!.entries.push({
        tool,
        toolId: buildToolCardId(turnKey, stepIndex, tool, toolIndex),
        running: stepIndex === runningStepIndex && !String(tool?.result || '').trim() && tool?.isError !== true,
      });
    });
  });
  return runs.filter((run) => run.entries.length > 0);
}

export const ToolRunGroup = memo(function ToolRunGroup({ entries, groupId, className = '' }: {
  entries: ToolRunEntry[];
  groupId: string;
  className?: string;
}) {
  const { t } = useTranslation('chat');
  const [expanded, setExpanded] = useState(() => toolRunOpenState.get(groupId) ?? false);
  const count = entries.length;
  const latest = entries[count - 1];
  const previousCountRef = useRef(count);
  const previousLatestIdRef = useRef(latest?.toolId || '');
  const countIncreased = count > previousCountRef.current;
  const latestChanged = !!latest && previousLatestIdRef.current !== latest.toolId;

  useEffect(() => {
    setExpanded(toolRunOpenState.get(groupId) ?? false);
  }, [groupId]);

  useEffect(() => {
    previousCountRef.current = count;
    previousLatestIdRef.current = latest?.toolId || '';
  }, [count, latest?.toolId]);

  if (!latest) return null;

  const toggleRun = (event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    setExpanded((value) => {
      const next = !value;
      setToolRunOpen(groupId, next);
      return next;
    });
  };
  // Keep the newest card (and its group toggle) in the same first-visible
  // position when opening the run. Older cards expand underneath it, so the
  // user can always close the run without chasing a control pushed off-screen.
  const visibleEntries = expanded ? [latest, ...entries.slice(0, -1)] : [latest];

  return (
    <div
      data-id="current-history-tool-run-group"
      data-tool-run-id={groupId}
      data-expanded={expanded ? 'true' : 'false'}
      className={`${className} space-y-1.5`}
    >
      {visibleEntries.map((entry) => {
        const isLatest = entry.toolId === latest.toolId;
        const runControl = isLatest ? (
          <span data-id="current-history-tool-run-control" className="flex shrink-0 items-center gap-1">
            <span
              key={count}
              data-id="current-history-tool-run-count"
              className={`min-w-5 rounded-full bg-white/[0.055] px-1.5 py-0.5 text-center text-[10px] tabular-nums text-zinc-500 ${countIncreased ? 'tool-run-count-pop' : ''}`}
            >
              ×{count}
            </span>
            {count > 1 ? (
              <button
                type="button"
                data-id="current-history-tool-run-toggle"
                aria-expanded={expanded}
                aria-label={expanded ? t('collapse') : t('expand')}
                onClick={toggleRun}
                className="grid h-5 w-5 place-items-center rounded text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300"
              >
                {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
              </button>
            ) : null}
          </span>
        ) : null;
        return (
          <div
            key={entry.toolId}
            data-id={isLatest ? 'current-history-tool-run-latest' : 'current-history-tool-run-item'}
            className={isLatest && latestChanged ? 'tool-run-latest-in' : ''}
          >
            <ToolCard
              tool={entry.tool}
              toolId={entry.toolId}
              running={entry.running}
              runControl={runControl}
              hideExpandIndicator={isLatest && count > 1}
            />
          </div>
        );
      })}
    </div>
  );
});
