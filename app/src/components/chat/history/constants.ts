// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

export const CURRENT_HISTORY_TOOL_DB_NAME = 'cicy-current-history-tool-cache';
export const CURRENT_HISTORY_TOOL_DB_VERSION = 3;
export const CURRENT_HISTORY_TOOL_STORE = 'tool_details';
export const CURRENT_HISTORY_TURN_STORE = 'history_turns';
// Prompts-only question list, cached per conversation so reopening the
// prompts-only view paints instantly instead of re-paging the whole history.
// Unlike the turn cache (positional ids drift → read-untrusted, INV-9), this
// entry stores the live `maxId` it was built at; a mismatch (new turns /
// compaction) invalidates it, so it's safe to read-trust while maxId holds.
export const CURRENT_HISTORY_PROMPTS_STORE = 'prompts_only';
// Number of contiguous item ids loaded per page (one ranged fetch). Kept under
// the backend's max page limit (100). Each turn spans a few ids, so this is
// ~10-20 turns per page.
// Initial window = enough to fill the card viewport + a bit; the rest loads
// lazily via "load earlier" on scroll-up. current.json can be huge — never read
// it whole. A turn spans a few ids, so ~16 ids ≈ a screenful of turns.
export const CURRENT_HISTORY_WINDOW = 16;
// Prompts-only: how many user questions to eagerly backfill on open before
// leaving the rest to scroll-up paging.
// Max assistant turns rendered in an expanded answer before "展开更早" — a single
// reply can be 40+ tool rounds; rendering them all at once is what causes the jank.
export const ANSWER_RENDER_CAP = 8;
// Streaming model(双通道):
// - agent_type=cicy:WS 直推。ai_chunk / thinking_chunk 的 delta 直接追加进 live
//   尾巴渲染(零轮询延迟);reply.json 轮询降级为校正锚 —— 中途打开、WS 丢包/重连、
//   工具卡与多回合结构由 poll 对齐(后端先写 reply.json 再 publish,poll 快照永远
//   ⊇ 已推 delta;替换守卫保证同一 turn 内容只前进不回退)。
// - 非 cicy:不自行拼 WS delta；每个 WS 信号会提前触发一次 reply.json 重读，
//   读取期间的新信号会合并成一次 follow-up read，确保内容/终态不丢。轮询仍作为
//   WS 丢包兜底：in-flight 时 ACTIVE，空闲时退回 IDLE。
export const CURRENT_HISTORY_POLL_ACTIVE_MS = 500;
export const CURRENT_HISTORY_POLL_IDLE_MS = 2500;
// Short retry while the committed window is still loading on open, so the live
// tail attaches as soon as Part 1 is ready (and the poll never races it).
export const CURRENT_HISTORY_POLL_WAIT_MS = 150;
// Optimistic-send placeholder. The moment the user hits send we reserve TWO
// slots — a q bubble (showing what they typed, in a "sending" state) and an a
// placeholder (thinking dots) right below it — BEFORE the backend round-trips.
// When the real committed q lands the q flips "sending"→confirmed in place (same
// top-anchor, no new div); when the real answer streams it fills the reserved a
// slot (renderedLiveTurn). So q never lags behind the keypress. The synthetic q
// carries this stable turn-key so the anchor machinery can pin it like a real q.
export const OPTIMISTIC_Q_KEY = '__optimistic_q__';
// Drop a stuck optimistic bubble if the backend never produces a turn (send
// silently dropped, agent crashed) so it can't linger forever.
export const OPTIMISTIC_Q_TIMEOUT_MS = 60000;
