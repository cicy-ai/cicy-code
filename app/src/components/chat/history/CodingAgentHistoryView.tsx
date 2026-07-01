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
}: CurrentHistoryViewProps) {
  // Coding agents (claude/codex/…) ignore WS deltas and rely on polling, and
  // start with a blank empty-history state (no opening greeting).
  const state = useCurrentHistory({ paneId, open, promptsOnly, hideTools, agentType, consumeWsDeltas: false });
  return (
    <HistoryList
      {...state}
      paneId={paneId}
      agentType={agentType}
      promptsOnly={promptsOnly}
      hideTools={hideTools}
      fullWidth={fullWidth}
      leftAlignQuestions={leftAlignQuestions}
      greeting=""
    />
  );
}
