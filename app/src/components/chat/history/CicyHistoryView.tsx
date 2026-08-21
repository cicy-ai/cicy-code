// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import apiService from '../../../services/api';
import { useCurrentHistory } from './useCurrentHistory';
import { HistoryList } from './HistoryList';
import type { CurrentHistoryViewProps } from '../CurrentHistoryView';

export default function CicyHistoryView({
  paneId,
  open,
  promptsOnly = false,
  hideTools = false,
  agentType = '',
  fullWidth = false,
  leftAlignQuestions = false,
  pollLive = true,
}: CurrentHistoryViewProps) {
  const state = useCurrentHistory({ paneId, open, promptsOnly, hideTools, agentType, consumeWsDeltas: true, pollLive });
  // Opening greeting shown on the empty-history state — role agents draw it from
  // their role template's 开场白 (GET /api/agents/greeting/{id}); falls back to the
  // static placeholder when empty/unfetched. Keyed by paneId so switching agents
  // re-fetches and never shows a stale role's line.
  const [greeting, setGreeting] = useState('');
  // Fetch the role-specific opening greeting for the empty-history state. Reset
  // first so switching agents never flashes the previous role's line; ignore
  // failures (the render falls back to the static placeholder). This IS the cicy
  // view, so always fetch (coding agents use CodingAgentHistoryView with no greeting).
  useEffect(() => {
    setGreeting('');
    const id = String(paneId || '').trim();
    if (!id) return;
    let alive = true;
    apiService.getAgentGreeting(id)
      .then((res: any) => { if (alive) setGreeting(String(res?.data?.greeting || '').trim()); })
      .catch(() => {});
    return () => { alive = false; };
  }, [paneId, agentType]);
  return (
    <HistoryList
      {...state}
      paneId={paneId}
      agentType={agentType}
      promptsOnly={promptsOnly}
      hideTools={hideTools}
      fullWidth={fullWidth}
      leftAlignQuestions={leftAlignQuestions}
      greeting={greeting}
    />
  );
}
