// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import apiService from '../../../services/api';
import { useCurrentHistory } from './useCurrentHistory';
import { HistoryList } from './HistoryList';
import type { CurrentHistoryViewProps } from '../CurrentHistoryView';

export default function CodingAgentHistoryView({
  paneId,
  open,
  promptsOnly = false,
  hideTools = false,
  agentType = '',
  fullWidth = false,
  leftAlignQuestions = false,
  pollLive = true,
}: CurrentHistoryViewProps) {
  // Coding agents (claude/codex/…) ignore WS deltas and rely on polling.
  const state = useCurrentHistory({ paneId, open, promptsOnly, hideTools, agentType, consumeWsDeltas: false, pollLive });
  const [greeting, setGreeting] = useState('');
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
