// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { isCicyLiteAgent } from '../../lib/agentType';
import CicyHistoryView from './history/CicyHistoryView';
import CodingAgentHistoryView from './history/CodingAgentHistoryView';

export type CurrentHistoryViewProps = {
  paneId: string;
  open: boolean;
  inspectorVersion?: number;
  // Show only the user's prompts (questions); hide assistant answers, thinking,
  // tools, and the live tail. Driven by the AgentStack history-bar toggle.
  promptsOnly?: boolean;
  // Hide tool cards (keep prompts / thinking / answers). Used by the office
  // window view, which only wants the conversation, not tool I/O.
  hideTools?: boolean;
  // 答案(a)左侧的头像用哪个 agent_type 的 logo(claude/codex/dispatcher…),
  // 类比 ChatGPT 回复前的 logo 头像。空串 → 字母兜底。
  agentType?: string;
  // Render the message list at 100% width (no mx-auto centering, no max-w cap).
  // Used by the AgentStack card-view history popover, which is already width-
  // constrained by its container. Default (false) keeps the centered max-w-4xl
  // reading column used by the full-screen DispatcherChat view.
  fullWidth?: boolean;
  // Left-align the question bubbles (default right/chat-style). The inline
  // webframe history sets this; DispatcherChat keeps the default.
  leftAlignQuestions?: boolean;
};

export default function CurrentHistoryView(props: CurrentHistoryViewProps) {
  return isCicyLiteAgent(props.agentType)
    ? <CicyHistoryView {...props} />
    : <CodingAgentHistoryView {...props} />;
}
