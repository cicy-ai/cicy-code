// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

export type HistoryTurn = {
  history_id?: number;
  conversation_id?: string;
  role?: string;
  text?: string;
  q: string;
  a?: string;
  steps?: Array<{ type: 'text'; text: string } | { type: 'thinking'; text: string } | { type: 'tool'; tools: any[] }>;
  status?: string;
  ts?: number;
  start_ts?: number;
  credit?: number;
  model?: string;
  raw_items?: RawHistoryItem[];
  // Set on a cicy "turn produced no reply" system notice: "cancelled" | "error".
  // The serving layer (cicyTagOutcomeAsSystem) relabels the marker → role:system
  // and attaches this so the UI can offer 重试 on the latest turn.
  outcome?: string;
  // 具体原因(目前 blocked 用:命中规则/事件ID 的人类可读文案)。展示在 OutcomeNoticeCard
  // 里(像余额不足卡显示原因)。来自 current.json marker 的 cicy_outcome_detail(display-only)。
  outcomeDetail?: string;
  // Client-only optimistic-send placeholder (never comes from the backend).
  _optimistic?: boolean;
};

export type RawHistoryItem = Record<string, any>;

export type EnvironmentContextData = {
  cwd?: string;
  shell?: string;
  current_date?: string;
  timezone?: string;
};

// ---- window._cacheHistory:整页历史的内存快照缓存(排在 IndexedDB / 网络之前)----
// key = paneId,值 = 该 pane 上次渲染的整页状态。打开历史面板时先用快照**同步**渲染
// 首屏(0 网络等待、不出 loading 骨架),随后照常 fresh 拉服务器、整体覆盖(React 按
// history_id key diff,内容没变就不动)。挂在 window 上便于调试:window._cacheHistory。
export type HistoryMemSnapshot = {
  items: HistoryTurn[];
  conversationId: string;
  model: string;
  hasMore: boolean;
  nextBefore: number | null;
  maxId: number;
  updatedAt: number;
  // 最后一轮答案在迁入 committed 前住在 reply.json(live 尾巴)里。快照不带它的话,
  // 切回来时最后一个答案要等首次 poll 才"啪"地补进来,看着像刚生成完。
  liveTurn: HistoryTurn | null;
};
