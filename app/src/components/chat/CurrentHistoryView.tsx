import { createContext, memo, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown, ChevronRight, AlertTriangle, Square, RotateCcw } from 'lucide-react';
import Markdown from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import remarkGfm from 'remark-gfm';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import apiService from '../../services/api';
import { Spinner } from '../ui/Spinner';
import AgentAvatar from '../AgentAvatar';
import { isCicyLiteAgent } from '../../lib/agentType';

type HistoryTurn = {
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
  // Client-only optimistic-send placeholder (never comes from the backend).
  _optimistic?: boolean;
};

type RawHistoryItem = Record<string, any>;

type EnvironmentContextData = {
  cwd?: string;
  shell?: string;
  current_date?: string;
  timezone?: string;
};

const toolCardOpenState = new Map<string, boolean>();
const currentHistoryPendingRequests = new Map<string, Promise<any>>();
const currentHistoryRecentResponses = new Map<string, { at: number; data: any }>();
const CURRENT_HISTORY_TOOL_DB_NAME = 'cicy-current-history-tool-cache';
const CURRENT_HISTORY_TOOL_DB_VERSION = 3;
const CURRENT_HISTORY_TOOL_STORE = 'tool_details';
const CURRENT_HISTORY_TURN_STORE = 'history_turns';
// Prompts-only question list, cached per conversation so reopening the
// prompts-only view paints instantly instead of re-paging the whole history.
// Unlike the turn cache (positional ids drift → read-untrusted, INV-9), this
// entry stores the live `maxId` it was built at; a mismatch (new turns /
// compaction) invalidates it, so it's safe to read-trust while maxId holds.
const CURRENT_HISTORY_PROMPTS_STORE = 'prompts_only';
const CURRENT_HISTORY_PAGE_SIZE = 5;
// Number of contiguous item ids loaded per page (one ranged fetch). Kept under
// the backend's max page limit (100). Each turn spans a few ids, so this is
// ~10-20 turns per page.
// Initial window = enough to fill the card viewport + a bit; the rest loads
// lazily via "load earlier" on scroll-up. current.json can be huge — never read
// it whole. A turn spans a few ids, so ~16 ids ≈ a screenful of turns.
const CURRENT_HISTORY_WINDOW = 16;
const CURRENT_HISTORY_MIN_QUESTIONS = 8;
// Prompts-only: how many user questions to eagerly backfill on open before
// leaving the rest to scroll-up paging.
const PROMPTS_ONLY_MIN_QUESTIONS = 5;
// Streaming model(双通道):
// - agent_type=cicy:WS 直推。ai_chunk / thinking_chunk 的 delta 直接追加进 live
//   尾巴渲染(零轮询延迟);reply.json 轮询降级为校正锚 —— 中途打开、WS 丢包/重连、
//   工具卡与多回合结构由 poll 对齐(后端先写 reply.json 再 publish,poll 快照永远
//   ⊇ 已推 delta;替换守卫保证同一 turn 内容只前进不回退)。
// - 非 cicy:维持原 reply.json 轮询 loop,WS 事件一概不消费。in-flight 时 ACTIVE
//   节拍,空闲时退回 IDLE 节拍,只为发现下一轮开始。
const CURRENT_HISTORY_POLL_ACTIVE_MS = 500;
const CURRENT_HISTORY_POLL_IDLE_MS = 2500;
// Short retry while the committed window is still loading on open, so the live
// tail attaches as soon as Part 1 is ready (and the poll never races it).
const CURRENT_HISTORY_POLL_WAIT_MS = 150;
// Optimistic-send placeholder. The moment the user hits send we reserve TWO
// slots — a q bubble (showing what they typed, in a "sending" state) and an a
// placeholder (thinking dots) right below it — BEFORE the backend round-trips.
// When the real committed q lands the q flips "sending"→confirmed in place (same
// top-anchor, no new div); when the real answer streams it fills the reserved a
// slot (renderedLiveTurn). So q never lags behind the keypress. The synthetic q
// carries this stable turn-key so the anchor machinery can pin it like a real q.
const OPTIMISTIC_Q_KEY = '__optimistic_q__';
// Drop a stuck optimistic bubble if the backend never produces a turn (send
// silently dropped, agent crashed) so it can't linger forever.
const OPTIMISTIC_Q_TIMEOUT_MS = 60000;

function openCurrentHistoryToolDB(): Promise<IDBDatabase | null> {
  if (typeof window === 'undefined' || !window.indexedDB) {
    return Promise.resolve(null);
  }
  return new Promise((resolve) => {
    const request = window.indexedDB.open(CURRENT_HISTORY_TOOL_DB_NAME, CURRENT_HISTORY_TOOL_DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(CURRENT_HISTORY_TOOL_STORE)) {
        db.createObjectStore(CURRENT_HISTORY_TOOL_STORE, { keyPath: 'key' });
      }
      if (!db.objectStoreNames.contains(CURRENT_HISTORY_TURN_STORE)) {
        const store = db.createObjectStore(CURRENT_HISTORY_TURN_STORE, { keyPath: 'key' });
        store.createIndex('by_pane', 'paneId', { unique: false });
      }
      if (!db.objectStoreNames.contains(CURRENT_HISTORY_PROMPTS_STORE)) {
        db.createObjectStore(CURRENT_HISTORY_PROMPTS_STORE, { keyPath: 'key' });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => resolve(null);
  });
}

function buildHistoryTurnCacheKey(paneId: string, conversationId: string, historyId: number) {
  return `${paneId}:${conversationId}:${historyId}`;
}

async function setCurrentHistoryTurnsToIndexedDB(paneId: string, conversationId: string, turns: RawHistoryItem[]) {
  const db = await openCurrentHistoryToolDB();
  if (!db || !db.objectStoreNames.contains(CURRENT_HISTORY_TURN_STORE)) return;
  try {
    await new Promise<void>((resolve) => {
      const tx = db.transaction(CURRENT_HISTORY_TURN_STORE, 'readwrite');
      const store = tx.objectStore(CURRENT_HISTORY_TURN_STORE);
      for (const turn of turns) {
        const historyId = Number(turn?.history_id || 0);
        if (historyId <= 0) continue;
        const turnConversationId = String(turn?.conversation_id || conversationId || '').trim();
        if (!turnConversationId) continue;
        store.put({
          key: buildHistoryTurnCacheKey(paneId, turnConversationId, historyId),
          paneId,
          conversationId: turnConversationId,
          historyId,
          ts: Number(turn?.ts || 0),
          data: turn,
          updatedAt: Date.now(),
        });
      }
      tx.oncomplete = () => resolve();
      tx.onerror = () => resolve();
      tx.onabort = () => resolve();
    });
  } catch {}
}

async function getCurrentHistoryTurnsByIDsFromIndexedDB(paneId: string, conversationId: string, historyIDs: number[]) {
  if (!historyIDs.length) return [] as RawHistoryItem[];
  const db = await openCurrentHistoryToolDB();
  if (!db || !db.objectStoreNames.contains(CURRENT_HISTORY_TURN_STORE)) return [];
  try {
    return await new Promise<RawHistoryItem[]>((resolve) => {
      const tx = db.transaction(CURRENT_HISTORY_TURN_STORE, 'readonly');
      const store = tx.objectStore(CURRENT_HISTORY_TURN_STORE);
      const out = new Map<number, RawHistoryItem>();
      let remaining = historyIDs.length;
      for (const historyID of historyIDs) {
        const request = store.get(buildHistoryTurnCacheKey(paneId, conversationId, historyID));
        const done = () => {
          remaining -= 1;
          if (remaining === 0) {
            resolve(historyIDs.map((id) => out.get(id)).filter(Boolean) as RawHistoryItem[]);
          }
        };
        request.onsuccess = () => {
          if (request.result?.data) out.set(historyID, request.result.data as HistoryTurn);
          done();
        };
        request.onerror = done;
      }
      tx.onabort = () => resolve([]);
    });
  } catch {
    return [];
  }
}

function buildPromptsCacheKey(paneId: string, conversationId: string) {
  return `${paneId}:${conversationId}`;
}

interface PromptsCacheEntry { maxId: number; prompts: HistoryTurn[]; }

async function getPromptsCacheFromIndexedDB(paneId: string, conversationId: string): Promise<PromptsCacheEntry | null> {
  if (!conversationId) return null;
  const db = await openCurrentHistoryToolDB();
  if (!db || !db.objectStoreNames.contains(CURRENT_HISTORY_PROMPTS_STORE)) return null;
  try {
    return await new Promise<PromptsCacheEntry | null>((resolve) => {
      const tx = db.transaction(CURRENT_HISTORY_PROMPTS_STORE, 'readonly');
      const store = tx.objectStore(CURRENT_HISTORY_PROMPTS_STORE);
      const request = store.get(buildPromptsCacheKey(paneId, conversationId));
      request.onsuccess = () => {
        const r = request.result;
        if (r && Array.isArray(r.prompts)) resolve({ maxId: Number(r.maxId || 0), prompts: r.prompts as HistoryTurn[] });
        else resolve(null);
      };
      request.onerror = () => resolve(null);
      tx.onabort = () => resolve(null);
    });
  } catch {
    return null;
  }
}

async function setPromptsCacheToIndexedDB(paneId: string, conversationId: string, maxId: number, prompts: HistoryTurn[]) {
  if (!conversationId || maxId <= 0) return;
  const db = await openCurrentHistoryToolDB();
  if (!db || !db.objectStoreNames.contains(CURRENT_HISTORY_PROMPTS_STORE)) return;
  try {
    await new Promise<void>((resolve) => {
      const tx = db.transaction(CURRENT_HISTORY_PROMPTS_STORE, 'readwrite');
      const store = tx.objectStore(CURRENT_HISTORY_PROMPTS_STORE);
      store.put({ key: buildPromptsCacheKey(paneId, conversationId), paneId, conversationId, maxId, prompts, updatedAt: Date.now() });
      tx.oncomplete = () => resolve();
      tx.onerror = () => resolve();
      tx.onabort = () => resolve();
    });
  } catch {}
}

// ---- window._cacheHistory:整页历史的内存快照缓存(排在 IndexedDB / 网络之前)----
// key = paneId,值 = 该 pane 上次渲染的整页状态。打开历史面板时先用快照**同步**渲染
// 首屏(0 网络等待、不出 loading 骨架),随后照常 fresh 拉服务器、整体覆盖(React 按
// history_id key diff,内容没变就不动)。挂在 window 上便于调试:window._cacheHistory。
type HistoryMemSnapshot = {
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

function historyMemCache(): Map<string, HistoryMemSnapshot> {
  const w = window as unknown as { _cacheHistory?: Map<string, HistoryMemSnapshot> };
  if (!(w._cacheHistory instanceof Map)) w._cacheHistory = new Map();
  return w._cacheHistory;
}

function buildIdRange(lo: number, hi: number): number[] {
  const out: number[] = [];
  for (let id = lo; id <= hi; id += 1) out.push(id);
  return out;
}

// Atomically load one contiguous id window [lo..hi] (lo = max(1, hi-size+1)).
//
// Guarantees the three properties the history view needs:
//  - completeness: the whole contiguous range is resolved (no per-id gaps);
//    whatever the cache is missing is fetched in a SINGLE ranged API call that
//    spans the gap, instead of one request per id.
//  - ordering: items are returned strictly ascending by id.
//  - atomicity: the function resolves to one fully-built snapshot; the caller
//    commits it in a single setState (guarded by a request sequence), so a
//    re-open / pane switch mid-flight can never interleave a partial window.
//
// Both formats (Anthropic role-based items and OpenAI tool-call items) share
// the same numeric `id`, so the id range is format-agnostic.
async function loadWindowItems(
  paneId: string,
  conversationId: string,
  hi: number,
  size = CURRENT_HISTORY_WINDOW,
  opts: { fresh?: boolean } = {},
): Promise<{ items: RawHistoryItem[]; lo: number }> {
  if (hi <= 0 || !conversationId) return { items: [], lo: 0 };
  const lo = Math.max(1, hi - Math.max(1, size) + 1);
  const wantedIds = buildIdRange(lo, hi);
  const byId = new Map<number, RawHistoryItem>();
  // 1) cache-first: pull whatever this window already has from IndexedDB.
  //
  // EXCEPT on a fresh open (opts.fresh): history_id is POSITIONAL in current.json
  // (the request-body snapshot), so for an actively-mutating / compacting agent
  // the same (conversationId, historyId) can map to DIFFERENT content over time.
  // cache-first never revalidates a hit → it would serve stale/wrong turns (e.g.
  // another turn's content at that slot). So the open window always fetches fresh
  // by conversation and overwrites the cache; cache-first is kept only for
  // "load earlier" pagination of older, settled turns. (docs §11)
  if (!opts.fresh) {
    const cached = await getCurrentHistoryTurnsByIDsFromIndexedDB(paneId, conversationId, wantedIds);
    for (const item of cached) {
      const id = Number(item?.history_id || item?.id || 0);
      if (id > 0) byId.set(id, item);
    }
  }
  // 2) one ranged fetch covering only the missing span [missLo..missHi].
  const missing = wantedIds.filter((id) => !byId.has(id));
  if (missing.length) {
    const missLo = missing[0];
    const missHi = missing[missing.length - 1];
    const data = await getCurrentHistory(paneId, {
      before: missHi + 1,
      limit: missHi - missLo + 1,
      conversation_id: conversationId,
    });
    const fetched = Array.isArray(data?.items) ? data.items : [];
    if (fetched.length) {
      await setCurrentHistoryTurnsToIndexedDB(paneId, conversationId, fetched);
      for (const item of fetched) {
        const id = Number(item?.history_id || item?.id || 0);
        if (id >= lo && id <= hi) byId.set(id, item);
      }
    }
  }
  // 3) assemble strictly ascending; ids that genuinely don't exist drop out.
  const items = wantedIds.map((id) => byId.get(id)).filter(Boolean) as RawHistoryItem[];
  return { items, lo };
}

async function syncCurrentHistoryTurnsToIndexedDB(paneId: string, conversationId: string, turns: RawHistoryItem[]) {
  if (!conversationId || !turns.length) {
    return turns;
  }
  await setCurrentHistoryTurnsToIndexedDB(paneId, conversationId, turns);
  const historyIDs = turns
    .map((turn) => Number(turn?.history_id || turn?.id || 0))
    .filter((historyID) => historyID > 0);
  if (!historyIDs.length) {
    return turns;
  }
  const cached = await getCurrentHistoryTurnsByIDsFromIndexedDB(paneId, conversationId, historyIDs);
  const byID = new Map<number, RawHistoryItem>();
  for (const turn of turns) {
    const historyID = Number(turn?.history_id || turn?.id || 0);
    if (historyID > 0) byID.set(historyID, turn);
  }
  for (const turn of cached) {
    const historyID = Number(turn?.history_id || turn?.id || 0);
    if (historyID > 0) byID.set(historyID, turn);
  }
  return historyIDs.map((historyID) => byID.get(historyID)).filter(Boolean) as RawHistoryItem[];
}

function toolArgText(input: any) {
  if (typeof input === 'string') return input.trim();
  try {
    return JSON.stringify(input);
  } catch {
    return String(input || '').trim();
  }
}

function flattenPartText(part: any): string {
  if (!part) return '';
  if (typeof part === 'string') return part.trim();
  if (typeof part !== 'object') return '';
  if (typeof part.text === 'string' && part.text.trim()) return part.text.trim();
  if (typeof part.thinking === 'string' && part.thinking.trim()) return part.thinking.trim();
  if (typeof part.content === 'string' && part.content.trim()) return part.content.trim();
  if (Array.isArray(part.content)) {
    return part.content.map((item: any) => flattenPartText(item)).filter(Boolean).join('\n').trim();
  }
  return '';
}

function countQuestionBoundaries(rawItems: RawHistoryItem[]) {
  let count = 0;
  for (const item of rawItems) {
    if (isQuestionBoundary(item)) count += 1;
  }
  return count;
}

function isQuestionBoundary(item: RawHistoryItem | undefined | null) {
  const role = String(item?.role || '').trim();
  if (role !== 'user') return false;
  const content = Array.isArray(item?.content) ? item.content : [];
  const nonToolText = content
    .filter((part: any) => String(part?.type || '').trim() !== 'tool_result')
    .map((part: any) => flattenPartText(part))
    .filter(Boolean)
    .join('\n')
    .trim();
  return !!nonToolText;
}

function trimToRecentQuestionWindow(rawItems: RawHistoryItem[], minQuestions = CURRENT_HISTORY_MIN_QUESTIONS) {
  if (!rawItems.length) return rawItems;
  const questionIndexes: number[] = [];
  for (let i = 0; i < rawItems.length; i += 1) {
    if (isQuestionBoundary(rawItems[i])) questionIndexes.push(i);
  }
  if (questionIndexes.length <= minQuestions) return rawItems;
  const startIndex = questionIndexes[questionIndexes.length - minQuestions];
  return rawItems.slice(startIndex);
}

function buildTurnsFromRawItems(rawItems: RawHistoryItem[]): HistoryTurn[] {
  const toolNameByCallId = new Map<string, string>();
  // OpenAI Responses (non-gateway codex) emits each tool call as TWO top-level
  // items: a `function_call` (name + args) and a separate `function_call_output`
  // (the result). Rendered as-is that's TWO cards for one logical call. Collect
  // outputs + call-ids here so the merge below folds the output INTO the
  // function_call and drops the standalone output → one card (arg + result).
  const fnOutputByCallId = new Map<string, string>();
  const fnCallIds = new Set<string>();
  // OpenAI Chat (gateway codex) is the same split, different shape: an assistant
  // message with `tool_calls[]` (the call) followed by `role:tool` messages (the
  // results, keyed by tool_call_id). Collect results + call-ids so the result
  // folds into the assistant tool_calls[] card and the standalone tool message
  // is dropped → one card per tool, matching the non-gateway path.
  const chatToolResultByCallId = new Map<string, string>();
  const chatToolCallIds = new Set<string>();
  for (const raw of rawItems) {
    const item = raw || {};
    const it = String(item?.type || '').trim();
    // function_call (OpenAI Responses) AND custom_tool_call (codex apply_patch)
    // share the same call+output split, paired by call_id → fold identically.
    if (it === 'function_call' || it === 'custom_tool_call') {
      const cid = String(item?.call_id || item?.id || '').trim();
      if (cid) fnCallIds.add(cid);
    }
    if (it === 'function_call_output' || it === 'custom_tool_call_output') {
      const cid = String(item?.call_id || item?.tool_id || item?.id || '').trim();
      const rawOut = (item as any)?.output ?? (item as any)?.result;
      const out = typeof rawOut === 'string' ? rawOut : (rawOut != null ? JSON.stringify(rawOut) : '');
      if (cid && out) fnOutputByCallId.set(cid, out);
    }
    if (Array.isArray((item as any)?.tool_calls)) {
      for (const tc of (item as any).tool_calls as any[]) {
        const cid = String(tc?.id || '').trim();
        if (cid) chatToolCallIds.add(cid);
      }
    }
    const role = String(item?.role || '').trim();
    if (role === 'tool' || role === 'function') {
      const cid = String((item as any)?.tool_call_id || (item as any)?.tool_id || (item as any)?.call_id || '').trim();
      const rawC = (item as any)?.content ?? (item as any)?.output;
      const res = typeof rawC === 'string' ? rawC : (rawC != null ? JSON.stringify(rawC) : '');
      if (cid && res) chatToolResultByCallId.set(cid, res);
    }
  }
  for (const raw of rawItems) {
    const item = raw || {};
    if (Array.isArray(item.content)) {
      for (const part of item.content as any[]) {
        if (String(part?.type || '').trim() === 'tool_use' && String(part?.name || '').trim()) {
          const callId = String(part?.id || part?.call_id || '').trim();
          if (callId) toolNameByCallId.set(callId, String(part.name).trim());
        }
      }
    }
    if (String(item?.type || '').trim() === 'custom_tool_call' && String(item?.name || '').trim()) {
      const callId = String(item?.call_id || item?.id || '').trim();
      if (callId) toolNameByCallId.set(callId, String(item.name).trim());
    }
    // OpenAI Responses: top-level function_call carries name + call_id, so its
    // function_call_output (a separate item, name-less) can resolve the name.
    if (String(item?.type || '').trim() === 'function_call' && String(item?.name || '').trim()) {
      const callId = String(item?.call_id || item?.id || '').trim();
      if (callId) toolNameByCallId.set(callId, String(item.name).trim());
    }
    // OpenAI Chat: assistant.tool_calls[].function.name keyed by tool_call id, so
    // the matching role:tool result message can resolve the name.
    if (Array.isArray((item as any)?.tool_calls)) {
      for (const tc of (item as any).tool_calls as any[]) {
        const callId = String(tc?.id || '').trim();
        const name = String(tc?.function?.name || tc?.name || '').trim();
        if (callId && name) toolNameByCallId.set(callId, name);
      }
    }
  }
  const merged: RawHistoryItem[] = [];
  for (let i = 0; i < rawItems.length; i += 1) {
    const current = rawItems[i] || {};
    if (i + 1 < rawItems.length) {
      const next = rawItems[i + 1] || {};
      const currentRole = String(current?.role || '').trim();
      const nextRole = String(next?.role || '').trim();
      if (currentRole === 'assistant' && nextRole === 'user') {
        const currentContent = Array.isArray(current?.content) ? current.content : [];
        const nextContent = Array.isArray(next?.content) ? next.content : [];
        // Index EVERY tool_result in the next user message by its id. A turn can
        // issue PARALLEL tool calls (several tool_use blocks in one assistant
        // message → several tool_result blocks in one user message). The old code
        // matched only the FIRST tool_use ↔ FIRST tool_result and `break`ed, so
        // every parallel call after the first lost its result. Whether the agent
        // batches calls (Anthropic native, e.g. non-gateway) or serializes them
        // (one per turn, e.g. gateway-translated) is provider-dependent — folding
        // ALL results makes both render identically.
        const resultById = new Map<string, string>();
        for (const p of nextContent) {
          const t = String(p?.type || '').trim();
          if (t !== 'tool_result' && t !== 'function_call_output') continue;
          const rid = String(p?.tool_use_id || p?.tool_id || p?.call_id || '').trim();
          if (!rid) continue;
          const raw = p?.content ?? p?.output;
          const result = typeof raw === 'string' ? raw : (raw != null ? JSON.stringify(raw) : '');
          if (result) resultById.set(rid, result);
        }
        const toolUseIds = currentContent
          .filter((p: any) => String(p?.type || '').trim() === 'tool_use')
          .map((p: any) => String(p?.id || '').trim());
        if (toolUseIds.some((id) => id && resultById.has(id))) {
          const item = JSON.parse(JSON.stringify(current));
          const itemContent = Array.isArray(item?.content) ? item.content : [];
          for (let j = 0; j < itemContent.length; j += 1) {
            if (String(itemContent[j]?.type || '').trim() !== 'tool_use') continue;
            const id = String(itemContent[j]?.id || '').trim();
            const result = id ? resultById.get(id) : '';
            if (result) itemContent[j] = { ...itemContent[j], _tool_result: result };
          }
          item.content = itemContent;
          item._has_tool_result = true;
          merged.push(item);
          i += 1;
          continue;
        }
      }
    }
    // OpenAI Responses tool pairing: fold the output into its function_call and
    // drop the standalone function_call_output, so one tool = one card.
    const ct = String(current?.type || '').trim();
    if (ct === 'function_call' || ct === 'custom_tool_call') {
      const cid = String(current?.call_id || current?.id || '').trim();
      const out = cid ? fnOutputByCallId.get(cid) : '';
      if (out) { merged.push({ ...current, _tool_output: out }); continue; }
    }
    if (ct === 'function_call_output' || ct === 'custom_tool_call_output') {
      const cid = String(current?.call_id || current?.tool_id || current?.id || '').trim();
      // Folded into its call above → skip the duplicate card. Keep only orphan
      // outputs (no matching call).
      if (cid && fnCallIds.has(cid)) continue;
    }
    // OpenAI Chat: drop the standalone role:tool result message whose result was
    // folded into the assistant tool_calls[] card. Keep orphans (no matching call).
    const cRole = String(current?.role || '').trim();
    if (cRole === 'tool' || cRole === 'function') {
      const cid = String(current?.tool_call_id || current?.tool_id || current?.call_id || '').trim();
      if (cid && chatToolCallIds.has(cid)) continue;
    }
    merged.push(current);
  }
  return merged
    .map((raw) => normalizeRawHistoryItem(raw, toolNameByCallId, chatToolResultByCallId))
    .filter(Boolean) as HistoryTurn[];
}

function historyTurnScore(turn: HistoryTurn): number {
  const answerLen = String(turn?.a || '').trim().length;
  const stepLen = Array.isArray(turn?.steps)
    ? turn.steps.reduce((sum, step) => sum + String((step as any)?.text || '').trim().length, 0)
    : 0;
  return answerLen + stepLen;
}

function historyTurnOrderValue(turn: HistoryTurn): number {
  const historyID = Number(turn?.history_id || 0);
  if (historyID > 0) return historyID;
  return Number(turn?.ts || turn?.start_ts || 0);
}

function mergeHistoryTurnVersions(prev: HistoryTurn | undefined, incoming: HistoryTurn): HistoryTurn {
  if (!prev) return incoming;
  const prevScore = historyTurnScore(prev);
  const incomingScore = historyTurnScore(incoming);
  const base = incomingScore >= prevScore ? incoming : prev;
  const fallback = incomingScore >= prevScore ? prev : incoming;
  return {
    ...fallback,
    ...base,
    history_id: Number(base?.history_id || fallback?.history_id || 0) || undefined,
    conversation_id: String(base?.conversation_id || fallback?.conversation_id || ''),
    q: String(base?.q || fallback?.q || ''),
    role: String(base?.role || fallback?.role || ''),
    text: String(base?.text || fallback?.text || ''),
    a: String(base?.a || fallback?.a || ''),
    steps: Array.isArray(base?.steps) && base.steps.length ? base.steps : fallback?.steps,
    status: String(base?.status || fallback?.status || ''),
    ts: Number(base?.ts || fallback?.ts || 0) || undefined,
    start_ts: Number(base?.start_ts || fallback?.start_ts || 0) || undefined,
    credit: Number(base?.credit || fallback?.credit || 0) || undefined,
    model: String(base?.model || fallback?.model || ''),
  };
}

function normalizeHistoryTurns(items: HistoryTurn[]): HistoryTurn[] {
  const byHistoryID = new Map<number, HistoryTurn>();
  const withoutHistoryID: HistoryTurn[] = [];
  for (const item of items) {
    const historyID = Number(item?.history_id || 0);
    if (historyID > 0) {
      byHistoryID.set(historyID, mergeHistoryTurnVersions(byHistoryID.get(historyID), item));
      continue;
    }
    withoutHistoryID.push(item);
  }
  const ordered = Array.from(byHistoryID.values()).sort((a, b) => historyTurnOrderValue(a) - historyTurnOrderValue(b));
  if (!withoutHistoryID.length) return ordered;
  return [...withoutHistoryID.sort((a, b) => historyTurnOrderValue(a) - historyTurnOrderValue(b)), ...ordered];
}

function extractContentText(content: any): string {
  if (Array.isArray(content)) {
    const parts = content
      .map((part) => {
        if (part && typeof part === 'object') {
          return String((part as any).text || '').trim();
        }
        return '';
      })
      .filter(Boolean);
    return parts.join('\n').trim();
  }
  return String(content || '').trim();
}

// Harness-injected wrappers that ride along at the START of a role:user message
// (Claude puts system-reminders / slash-command echoes in the user turn). They
// are NOT real user input but the user still wants them available — so we peel
// the LEADING blocks off and render them in a small collapsed fold, leaving the
// real question as the bubble. The \1 backreference matches each block to its
// OWN closing tag, so a stray inner </tag> can't cut a block short.
// fork-inherited-context: a cicy fork's first user message carries the source
// agent's conversation summary in this wrapper — fold it like other harness
// blocks so the (potentially huge) inherited dump shows as a collapsed chip.
const HARNESS_BLOCK_RE = /^\s*<(system-reminder|task-notification|local-command-caveat|local-command-stdout|command-name|command-message|command-args|fork-inherited-context)>([\s\S]*?)<\/\1>\s*/;
// Codex prepends its memory file to the FIRST user message as
// `# AGENTS.md instructions for <path>\n\n<INSTRUCTIONS>…</INSTRUCTIONS>` (a
// leading markdown `#` header; CLAUDE.md uses the same shape) and often follows
// it with an `<environment_context>…</environment_context>` block. Both are
// harness-injected guidance, not the real question — peel them into the fold.
const AGENTS_PREFIX_RE = /^\s*#*\s*(?:AGENTS|CLAUDE)\.md instructions for [^\n]*\n+<INSTRUCTIONS>[\s\S]*?<\/INSTRUCTIONS>\s*/;
const ENV_CONTEXT_RE = /^\s*<environment_context>[\s\S]*?<\/environment_context>\s*/;
// Claude Code's "recap on return" injects a fixed instruction as a user turn
// when the user comes back after stepping away ("The user stepped away and is
// coming back. Recap in under N words …"). It rides as plain text (no tag) at
// the START of the user message. Two shapes occur:
//   (a) bundled: {instruction}\n\n{generated recap}  {real user message}
//       — the harness joins the recap to the user's typed message with a
//         double space, so we peel through the instruction + recap up to that
//         "  " join, leaving the real question as the bubble.
//   (b) standalone: just the instruction (no bundled recap / message).
// The opening is distinctive enough that no genuine user message starts with it.
const RECAP_BUNDLED_RE = /^\s*The user (?:stepped away and is coming back|is back)\.\s*Recap[\s\S]*?\n\s*\n[\s\S]*?\.\s{2,}(?=\S)/;
const RECAP_PREFIX_RE = /^\s*The user (?:stepped away and is coming back|is back)\.\s*Recap[\s\S]*?(?:\n\s*\n|$)/;
// Post-/compact continuation banner, injected as a user turn at the start of a
// resumed session ("This session is being continued from a previous
// conversation … Summary: …"). It rides as its OWN text block (the real
// conversation resumes in separate messages — the banner even says "Continue …
// without asking the user"), so the ENTIRE block (banner + the long Summary) is
// harness context, not the user's question — fold all of it. Greedy to the end
// of the block; in current.json it sits in a self-contained content block right
// after the system-reminder, so this won't swallow a real question.
const CONTINUATION_PREFIX_RE = /^\s*This session is being continued from a previous conversation[\s\S]*$/;

function splitLeadingHarnessBlocks(text: string): { blocks: string[]; remaining: string } {
  let remaining = String(text || '');
  const blocks: string[] = [];
  // Guard against pathological inputs with a hard cap.
  for (let i = 0; i < 50; i += 1) {
    const m = remaining.match(HARNESS_BLOCK_RE)
      || remaining.match(AGENTS_PREFIX_RE)
      || remaining.match(ENV_CONTEXT_RE)
      || remaining.match(RECAP_BUNDLED_RE)
      || remaining.match(RECAP_PREFIX_RE)
      || remaining.match(CONTINUATION_PREFIX_RE);
    if (!m) break;
    blocks.push(m[0].trim());
    remaining = remaining.slice(m[0].length);
  }
  return { blocks, remaining: remaining.trim() };
}

// cicy 失败/取消记录:后端通常已带 cicy_outcome + 干净 label。这里再兜底——任何带
// cicy_outcome 字段、或文本以 outcome 标记(新的干净前缀 / 旧的 ⟦…⟧ 形态)开头的项,
// 一律按 outcome 渲染(头像左对齐 + 重试),与 role 无关。标记文本本身已是干净中文短句
// (不含符号/JSON),不再解析 detail。
const CICY_OUTCOME_MARK = '（本轮未生成回复';
const CICY_OUTCOME_LEGACY = '⟦cicy-turn-outcome⟧';
function parseCicyOutcome(item: any, contentText: string): { kind: string; label: string } | null {
  const tagged = String(item?.cicy_outcome || '').trim();
  if (tagged) {
    return { kind: tagged, label: tagged === 'cancelled' ? '已停止生成' : '生成失败' };
  }
  if (contentText.startsWith(CICY_OUTCOME_MARK) || contentText.startsWith(CICY_OUTCOME_LEGACY)) {
    const kind = (contentText.includes('已停止') || contentText.includes('cancelled')) ? 'cancelled' : 'error';
    return { kind, label: kind === 'cancelled' ? '已停止生成' : '生成失败' };
  }
  return null;
}

function normalizeRawHistoryItem(raw: any, toolNameByCallId?: Map<string, string>, toolResultByCallId?: Map<string, string>): HistoryTurn | null {
  if (!raw || typeof raw !== 'object') return null;
  const item = raw as RawHistoryItem;
  const historyId = Number(item.history_id || item.id || 0);
  const conversationId = String(item.conversation_id || '').trim();
  const role = String(item.role || '').trim();
  const itemType = String(item.type || '').trim();
  const model = String(item.model || '').trim();
  const status = String(item.status || '').trim() || 'text';
  // Outcome record (cancel / post-retry failure) → a normal assistant output
  // (avatar, left-aligned) carrying an `outcome` tag so the render styles it
  // (failed/cancelled) + offers 重试. Detected by the cicy_outcome field OR the raw
  // marker, regardless of the role any serving/cache path gave it.
  {
    const outcomeText = extractContentText(item.content) || String(item.text || '').trim();
    const outcome = parseCicyOutcome(item, outcomeText);
    if (outcome) {
      return {
        history_id: historyId || undefined,
        conversation_id: conversationId,
        role: 'assistant',
        q: '',
        text: outcome.label,
        a: outcome.label,
        steps: [],
        status,
        model,
        outcome: outcome.kind,
      };
    }
  }
  // System / developer items are harness-injected notices (system-reminders,
  // task notifications, date changes, and codex's `<permissions instructions>`
  // / sandbox preamble which rides in a `developer` role message). They are NOT
  // assistant output — without this branch they fall through to the assistant
  // path and render as AI bubbles. Fold the whole message into the collapsed
  // SystemNoticeCard. (codex uses `developer`; Anthropic/others use `system`.)
  if (role === 'system' || role === 'developer') {
    const sysText = extractContentText(item.content) || String(item.text || '').trim();
    if (!sysText) return null;
    return {
      history_id: historyId || undefined,
      conversation_id: conversationId,
      role: 'system',
      q: '',
      text: sysText,
      a: '',
      steps: [],
      status,
      model,
      outcome: String((item as any).cicy_outcome || '').trim() || undefined,
    };
  }
  if (role === 'user') {
    // Keep the FULL text (including any harness-injected <system-reminder> /
    // command echoes). CollapsibleQ separates those leading markers into a small
    // collapsed fold — they must stay visible-on-demand, not be stripped.
    const question = extractContentText(item.content) || String(item.text || item.q || '').trim();

    if (question) {
      return {
        history_id: historyId || undefined,
        conversation_id: conversationId,
        role: 'user',
        q: question,
        text: question,
        a: '',
        steps: [],
        status,
        model,
      };
    }
    const toolSteps: any[] = [];
    if (Array.isArray(item.content)) {
      for (const part of item.content as any[]) {
        const pt = String(part?.type || '').trim();
        if (pt === 'tool_result' || pt === 'function_call_output') {
          const callId = String(part?.tool_use_id || part?.tool_id || '').trim();
          let name = String(part?.name || part?.tool_name || '').trim();
          if (!name && callId && toolNameByCallId?.has(callId)) {
            name = toolNameByCallId.get(callId) || 'tool_result';
          }
          if (!name) name = 'tool_result';
          toolSteps.push({
            type: 'tool',
            tools: [{
              name,
              arg: '',
              result: typeof part.content === 'string' ? part.content.trim() : (part.content ? JSON.stringify(part.content).trim() : ''),
            }],
          });
        }
      }
    }
    if (toolSteps.length) {
      return {
        history_id: historyId || undefined,
        conversation_id: conversationId,
        role: 'assistant',
        q: '',
        text: '',
        a: '',
        steps: toolSteps,
        status,
        model,
      };
    }
    return null;
  }
  // OpenAI Chat tool-result message (role:tool / role:function). Its content is
  // the raw tool output — render it as a tool RESULT card, NOT as assistant text,
  // otherwise the output (e.g. "Chunk ID: … Process exited with code 0 …") leaks
  // in as a chat bubble. (Anthropic puts tool_result inside a role:user message,
  // handled above; this is the OpenAI Chat shape.)
  if (role === 'tool' || role === 'function') {
    const callId = String((item as any).tool_call_id || (item as any).tool_id || (item as any).call_id || '').trim();
    let name = String((item as any).name || '').trim();
    if (!name && callId && toolNameByCallId?.has(callId)) name = toolNameByCallId.get(callId) || '';
    if (!name) name = 'tool_result';
    const result = typeof item.content === 'string'
      ? item.content.trim()
      : (item.content ? JSON.stringify(item.content).trim() : '');
    if (!result) return null;
    return {
      history_id: historyId || undefined,
      conversation_id: conversationId,
      role: 'assistant',
      q: '',
      text: '',
      a: '',
      steps: [{ type: 'tool', tools: [{ name, arg: '', result }] }],
      status,
      model,
    };
  }
  const steps: NonNullable<HistoryTurn['steps']> = [];
  // Anthropic extended-thinking blocks lead the assistant message (before text /
  // tool_use). The committed history dropped them — only the live tail rendered
  // thinking — so multi-round reasoning vanished once a turn was committed. Push
  // them first to preserve the real order: thinking → text → tools.
  if (Array.isArray(item.content)) {
    const thinkingText = (item.content as any[])
      .filter((p) => p && typeof p === 'object' && String(p.type || '').trim() === 'thinking')
      .map((p) => String(p.thinking || '').trim())
      .filter(Boolean)
      .join('\n\n');
    if (thinkingText) steps.push({ type: 'thinking', text: thinkingText });
  }
  // OpenAI Chat / opencode: committed reasoning lives in a top-level
  // `reasoning_content` string (not a content block). Anthropic uses content[]
  // thinking blocks (above); without this the thinking shows in the live tail
  // but vanishes the moment the turn commits to current.json. Push before text
  // to keep the real order: thinking → text → tools.
  const reasoningText = String((item as any).reasoning_content || (item as any).reasoning || '').trim();
  if (reasoningText) steps.push({ type: 'thinking', text: reasoningText });
  const assistantText = extractContentText(item.content);
  if (assistantText) {
    steps.push({ type: 'text', text: assistantText });
  }
  if (itemType === 'custom_tool_call') {
    steps.push({
      type: 'tool',
      tools: [{
        name: String(item.name || 'tool'),
        arg: String(item.input || '').trim(),
        // Folded from the paired custom_tool_call_output (apply_patch result).
        result: String((item as any)._tool_output || '').trim(),
      }],
    });
  }
  if (itemType === 'custom_tool_call_output') {
    steps.push({
      type: 'tool',
      tools: [{
        name: String(item.name || item.tool_name || 'tool'),
        arg: '',
        result: String(item.output || item.result || '').trim(),
      }],
    });
  }
  // OpenAI Responses: top-level function_call (e.g. exec_command). Unlike
  // Anthropic tool_use (a content block) or codex apply_patch (custom_tool_call),
  // its name + arguments sit at the item top level — without this it has no
  // matching case, produces no step, and the whole item is dropped.
  if (itemType === 'function_call') {
    steps.push({
      type: 'tool',
      tools: [{
        name: String(item.name || 'tool'),
        arg: typeof (item as any).arguments === 'string'
          ? (item as any).arguments.trim()
          : ((item as any).arguments ? JSON.stringify((item as any).arguments).trim()
            : ((item as any).input ? JSON.stringify((item as any).input).trim() : '')),
        // Folded by buildTurnsFromRawItems from the paired function_call_output,
        // so the call + its result render as ONE tool card.
        result: String((item as any)._tool_output || '').trim(),
      }],
    });
  }
  // OpenAI Responses: top-level function_call_output (the tool result, name-less).
  if (itemType === 'function_call_output') {
    const callId = String((item as any).call_id || (item as any).tool_id || '').trim();
    let name = '';
    if (callId && toolNameByCallId?.has(callId)) name = toolNameByCallId.get(callId) || '';
    if (!name) name = 'tool';
    steps.push({
      type: 'tool',
      tools: [{
        name,
        arg: '',
        result: String((item as any).output || (item as any).result || '').trim(),
      }],
    });
  }
  // OpenAI Chat: assistant message carries tool_calls[] (name + arguments under
  // .function), separate from any text content.
  if (Array.isArray((item as any).tool_calls)) {
    for (const tc of (item as any).tool_calls as any[]) {
      const fn = tc?.function || {};
      const callId = String(tc?.id || '').trim();
      // Fold the matching role:tool result (collected by buildTurnsFromRawItems)
      // so the call + its result render as ONE tool card (gateway codex).
      const result = (callId && toolResultByCallId?.get(callId)) || '';
      steps.push({
        type: 'tool',
        tools: [{
          name: String(fn.name || tc?.name || 'tool'),
          arg: typeof fn.arguments === 'string'
            ? fn.arguments.trim()
            : (fn.arguments ? JSON.stringify(fn.arguments).trim() : ''),
          result: String(result).trim(),
        }],
      });
    }
  }
  if (itemType !== 'custom_tool_call' && itemType !== 'custom_tool_call_output' && Array.isArray(item.content)) {
    for (const part of item.content as any[]) {
      const pt = String(part?.type || '').trim();
      if (pt === 'tool_use') {
        const toolResult = String(part?._tool_result || '').trim();
        steps.push({
          type: 'tool',
          tools: [{
            name: String(part.name || 'tool'),
            arg: typeof part.input === 'string' ? part.input.trim() : (part.input ? JSON.stringify(part.input).trim() : ''),
            result: toolResult,
          }],
        });
      }
      if (pt === 'tool_result' || pt === 'function_call_output') {
        const callId = String(part?.tool_use_id || part?.tool_id || '').trim();
        let name = String(part?.name || part?.tool_name || '').trim();
        if (!name && callId && toolNameByCallId?.has(callId)) {
          name = toolNameByCallId.get(callId) || 'tool';
        }
        if (!name) name = 'tool_result';
        steps.push({
          type: 'tool',
          tools: [{
            name,
            arg: '',
            result: typeof part.content === 'string' ? part.content.trim() : (part.content ? JSON.stringify(part.content).trim() : ''),
          }],
        });
      }
    }
  }
  if (!steps.length) return null;
  const answer = steps
    .filter((step) => step.type === 'text')
    .map((step) => String((step as any).text || '').trim())
    .filter(Boolean)
    .join('\n\n');
  return {
    history_id: historyId || undefined,
    conversation_id: conversationId,
    role: 'assistant',
    q: '',
    text: '',
    a: answer,
    steps,
    status,
    model,
  };
}

function buildCurrentHistoryRequestKey(paneId: string, params: { limit?: number; before?: number; conversation_id?: string } = {}) {
  return JSON.stringify({
    paneId: String(paneId || '').trim(),
    limit: Number(params.limit || 0),
    before: Number(params.before || 0),
    conversation_id: String(params.conversation_id || '').trim(),
  });
}

async function getCurrentHistory(paneId: string, params: { limit?: number; before?: number; conversation_id?: string } = {}) {
  const key = buildCurrentHistoryRequestKey(paneId, params);
  const now = Date.now();
  const recent = currentHistoryRecentResponses.get(key);
  if (recent && now - recent.at < 800) {
    return recent.data;
  }
  const pending = currentHistoryPendingRequests.get(key);
  if (pending) {
    return pending;
  }
  const request = apiService.getAgentCurrentHistory(paneId, params)
    .then(({ data }) => {
      currentHistoryRecentResponses.set(key, { at: Date.now(), data });
      return data;
    })
    .finally(() => {
      currentHistoryPendingRequests.delete(key);
    });
  currentHistoryPendingRequests.set(key, request);
  return request;
}

async function getHistoryIDs(paneId: string, params: { conversation_id?: string } = {}) {
  const { data } = await apiService.getAgentHistoryIDs(paneId, params as any);
  return data;
}

async function ensureRawHistoryItemsByIDs(
  paneId: string,
  conversationId: string,
  historyIDs: number[],
): Promise<RawHistoryItem[]> {
  if (!historyIDs.length || !conversationId) return [];
  const cached = await getCurrentHistoryTurnsByIDsFromIndexedDB(paneId, conversationId, historyIDs);
  const byID = new Map<number, RawHistoryItem>();
  for (const item of cached) {
    const id = Number(item?.history_id || item?.id || 0);
    if (id > 0) byID.set(id, item);
  }
  const missingIDs = historyIDs.filter((id) => !byID.has(id));
  if (missingIDs.length) {
    const before = Math.max(...missingIDs) + 1;
    const lower = Math.min(...missingIDs);
    const limit = before - lower;
    const data = await getCurrentHistory(paneId, {
      limit,
      before,
      conversation_id: conversationId,
    });
    const rawItems = Array.isArray(data?.items) ? data.items : [];
    const syncedItems = await syncCurrentHistoryTurnsToIndexedDB(paneId, conversationId, rawItems);
    for (const item of syncedItems) {
      const id = Number(item?.history_id || item?.id || 0);
      if (id > 0) byID.set(id, item);
    }
  }
  return historyIDs.map((id) => byID.get(id)).filter(Boolean) as RawHistoryItem[];
}

function ensureLatestStreamingTurn(prev: HistoryTurn[], payload: { historyId: number; conversationId: string; status?: string; model?: string; question?: string }) {
  const next = prev.slice();
  const existingIndex = next.findIndex((item) => Number(item?.history_id || 0) === payload.historyId);
  if (existingIndex >= 0) {
    const current = next[existingIndex];
    next[existingIndex] = {
      ...current,
      conversation_id: payload.conversationId || current.conversation_id,
      q: payload.question || current.q,
      text: payload.question || current.text,
      status: payload.status || current.status || 'thinking',
      model: payload.model || current.model,
      role: current.role || 'pair',
    };
    return next;
  }
  next.push({
    history_id: payload.historyId,
    conversation_id: payload.conversationId,
    role: 'pair',
    q: payload.question || '',
    text: payload.question || '',
    a: '',
    steps: [],
    status: payload.status || 'thinking',
    model: payload.model,
  });
  return normalizeHistoryTurns(next);
}

function updateLatestStreamingTurn(prev: HistoryTurn[], payload: { historyId: number; conversationId: string; status?: string; delta?: string; thinking?: string; answer?: string }) {
  const next = prev.slice();
  const index = next.findIndex((item) => Number(item?.history_id || 0) === payload.historyId);
  if (index < 0) return prev;
  const current = { ...next[index] } as HistoryTurn;
  const steps = Array.isArray(current.steps) ? [...current.steps] : [];
  const status = String(payload.status || current.status || 'thinking').trim() || 'thinking';
  if (payload.delta) {
    const text = String(payload.delta).trim();
    if (text) {
      const textIdx = steps.findIndex((step: any) => step?.type === 'text');
      if (textIdx >= 0) {
        const prevText = String((steps[textIdx] as any)?.text || '');
        steps[textIdx] = { type: 'text', text: `${prevText}${payload.delta}` };
      } else {
        steps.push({ type: 'text', text: payload.delta });
      }
    }
  }
  if (payload.answer != null) {
    const answer = String(payload.answer || '');
    const textIdx = steps.findIndex((step: any) => step?.type === 'text');
    if (textIdx >= 0) {
      steps[textIdx] = { type: 'text', text: answer };
    } else if (answer.trim()) {
      steps.push({ type: 'text', text: answer });
    }
    current.a = answer;
  } else {
    current.a = steps.filter((step: any) => step?.type === 'text').map((step: any) => String(step?.text || '')).join('\n\n').trim();
  }
  current.steps = steps;
  current.status = status;
  current.conversation_id = payload.conversationId || current.conversation_id;
  next[index] = current;
  return normalizeHistoryTurns(next);
}

function mergeHistoryTurnList(prev: HistoryTurn[], incoming: HistoryTurn, limit = CURRENT_HISTORY_PAGE_SIZE) {
  const incomingID = Number(incoming?.history_id || 0);
  const next = prev.slice();
  const existingIndex = incomingID > 0 ? next.findIndex((item) => Number(item?.history_id || 0) === incomingID) : -1;
  if (existingIndex >= 0) {
    next[existingIndex] = mergeHistoryTurnVersions(next[existingIndex], incoming);
  } else {
    next.push(incoming);
  }
  const normalized = normalizeHistoryTurns(next);
  return limit > 0 && normalized.length > limit ? normalized.slice(normalized.length - limit) : normalized;
}

function getVisibleHistorySteps(turn: HistoryTurn, isLatestTurn: boolean) {
  const steps = Array.isArray(turn?.steps) ? turn.steps : [];
  if (!steps.length) return [] as HistoryTurn['steps'];
  return steps.filter((step: any) => {
    const stepType = String(step?.type || '').trim();
    if (stepType === 'thinking' || stepType === 'text') {
      return String(step?.text || '').trim() !== '';
    }
    if (stepType === 'tool') {
      return Array.isArray(step?.tools) && step.tools.length > 0;
    }
    return false;
  });
}

function parseEnvironmentContext(text: string): EnvironmentContextData | null {
  const trimmed = String(text || '').trim();
  if (!trimmed.startsWith('<environment_context>') || !trimmed.endsWith('</environment_context>')) {
    return null;
  }
  const read = (tag: keyof EnvironmentContextData) => {
    const match = trimmed.match(new RegExp(`<${tag}>([\\s\\S]*?)</${tag}>`));
    return match?.[1]?.trim() || '';
  };
  const context: EnvironmentContextData = {
    cwd: read('cwd'),
    shell: read('shell'),
    current_date: read('current_date'),
    timezone: read('timezone'),
  };
  if (!context.cwd && !context.shell && !context.current_date && !context.timezone) {
    return null;
  }
  return context;
}

function EnvironmentContextCard({ context }: { context: EnvironmentContextData }) {
  const items = [
    { key: 'cwd', label: 'cwd', value: context.cwd },
    { key: 'shell', label: 'shell', value: context.shell },
    { key: 'current_date', label: 'date', value: context.current_date },
    { key: 'timezone', label: 'tz', value: context.timezone },
  ].filter((item) => String(item.value || '').trim());
  return (
    <div data-id="current-history-view-env-context" className="w-full max-w-[95%] rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3 py-2.5 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
      <div data-id="current-history-view-env-context-label" className="mb-2 text-[11px] uppercase tracking-[0.08em] text-sky-200/55">Environment</div>
      <div data-id="current-history-view-env-context-rows" className="space-y-1.5">
        {items.map((item) => (
          <div key={item.key} data-id={`current-history-view-env-context-row-${item.key}`} className="flex flex-col gap-1">
            <div data-id={`current-history-view-env-context-row-${item.key}-label`} className="text-[11px] text-sky-200/60">{item.label}</div>
            <div data-id={`current-history-view-env-context-row-${item.key}-value`} className="rounded-md border border-sky-200/[0.08] bg-black/[0.12] px-2 py-1 font-mono text-xs leading-relaxed text-sky-50/85 break-all">
              {item.value}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// Markdown link handling. URLs (http/https/mailto) are confirmed via a modal then
// opened in a NEW window (#25). Path-like links open in the workspace editor via
// the existing code-ext bridge — dispatched as a window event FilesView listens
// for (#24). Provided per CurrentHistoryView instance so multi-card cards don't
// fight over one global handler.
const OpenUrlContext = createContext<((url: string) => void) | null>(null);

function isExternalUrl(href: string): boolean {
  return /^(https?:)?\/\//i.test(href) || /^mailto:/i.test(href);
}

function MarkdownLink({ href, children, ...props }: any) {
  const requestOpenUrl = useContext(OpenUrlContext);
  const url = String(href || '').trim();
  return (
    <a
      {...props}
      data-id="current-history-md-link"
      href={url || undefined}
      className="text-sky-400/90 underline decoration-sky-400/30 underline-offset-2 hover:text-sky-300 hover:decoration-sky-300/60"
      onClick={(e) => {
        if (!url) return;
        e.preventDefault();
        e.stopPropagation();
        if (isExternalUrl(url)) {
          requestOpenUrl?.(url);
        } else {
          // path-like link → open in the workspace editor (FilesView listens)
          window.dispatchEvent(new CustomEvent('cicy:open-file', { detail: { path: url } }));
        }
      }}
    >
      {children}
    </a>
  );
}

const markdownComponents = { a: MarkdownLink } as const;

// react-markdown 没有内置 memo,每次渲染整篇重新 parse;且 remarkPlugins={[remarkGfm]}
// 内联数组每次都是新引用,就算外面包 memo 也会失效。流式期 live 尾巴每个 tick 重渲染,
// 不 memo 的话所有已完成段落 + thinking 全部跟着整篇重 parse。这里用稳定的 plugins
// 引用 + memo:只有文本真变的那个块才重 parse。
const REMARK_PLUGINS = [remarkGfm];
const MarkdownBlock = memo(function MarkdownBlock({ text }: { text: string }) {
  return <Markdown remarkPlugins={REMARK_PLUGINS} components={markdownComponents}>{text}</Markdown>;
});

// Confirm-before-leaving modal for external URLs. Opening goes to a NEW window.
function LinkConfirmModal({ url, onClose }: { url: string; onClose: () => void }) {
  return (
    <div
      data-id="current-history-link-modal"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4"
      onClick={onClose}
    >
      <div
        data-id="current-history-link-modal-box"
        className="w-full max-w-md overflow-hidden rounded-xl border border-white/[0.08] bg-[#16161a] shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div data-id="current-history-link-modal-title" className="border-b border-white/[0.06] px-4 py-3 text-sm font-medium text-zinc-200">打开外部链接?</div>
        <div data-id="current-history-link-modal-url" className="break-all px-4 py-3 font-mono text-xs leading-relaxed text-sky-300/80">{url}</div>
        <div data-id="current-history-link-modal-actions" className="flex justify-end gap-2 border-t border-white/[0.06] px-4 py-3">
          <button
            type="button"
            data-id="current-history-link-modal-cancel"
            onClick={onClose}
            className="rounded-md border border-white/[0.08] px-3 py-1.5 text-xs text-zinc-400 transition-colors hover:bg-white/[0.04] hover:text-zinc-200"
          >取消</button>
          <button
            type="button"
            data-id="current-history-link-modal-open"
            onClick={() => { window.open(url, '_blank', 'noopener,noreferrer'); onClose(); }}
            className="rounded-md bg-sky-500/90 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-sky-500"
          >在新窗口打开</button>
        </div>
      </div>
    </div>
  );
}

function CollapsibleQ({ text }: { text: string }) {
  // Peel leading harness blocks (system-reminder / command echoes) into a small
  // collapsed fold, then render the real question below. Recurse on the rest so
  // the existing env-context / xml-block / bubble logic runs on the clean text.
  const { blocks: harnessBlocks, remaining: afterHarness } = splitLeadingHarnessBlocks(text);
  if (harnessBlocks.length) {
    return (
      <div data-id="current-history-view-q-harness" className="mb-2.5 flex flex-col gap-1.5">
        <SystemNoticeCard text={harnessBlocks.join('\n\n')} />
        {afterHarness ? <CollapsibleQ text={afterHarness} /> : null}
      </div>
    );
  }
  const environmentContext = parseEnvironmentContext(text);
  let remaining = text;
  const xmlBlocks: string[] = [];
  while (/^<[\w-]+>[\s\S]*?<\/[\w-]+>/.test(remaining)) {
    const match = remaining.match(/^<[\w-]+>[\s\S]*?<\/[\w-]+>/);
    if (!match) break;
    xmlBlocks.push(match[0]);
    remaining = remaining.slice(match[0].length).trim();
  }
  if (xmlBlocks.length) {
    return (
      <div data-id="current-history-view-q-xml" className="mb-2.5 flex justify-end">
        <div data-id="current-history-view-q-xml-wrap" className="max-w-[95%] flex flex-col gap-2">
          <pre data-id="current-history-view-q-xml-block" className="overflow-x-auto rounded-lg border border-sky-300/[0.12] bg-black/[0.25] px-3 py-2 font-mono text-xs leading-relaxed text-sky-100/70 whitespace-pre-wrap">{xmlBlocks.join('\n')}</pre>
          {remaining ? (
            <div data-id="current-history-view-q-xml-trailing" className="overflow-hidden rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3.5 py-2 text-base leading-relaxed text-sky-50/90 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
              <MarkdownBlock text={remaining} />
            </div>
          ) : null}
        </div>
      </div>
    );
  }
  return (
    <div data-id="current-history-view-q" className="mb-2.5 flex justify-end">
      {environmentContext ? (
        <EnvironmentContextCard context={environmentContext} />
      ) : (
        <div data-id="current-history-view-q-body" className="max-w-[95%] overflow-hidden rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3.5 py-2 text-base leading-relaxed text-sky-50/90 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
          <MarkdownBlock text={String(text || '').replace(/^\-\n/, '')} />
        </div>
      )}
    </div>
  );
}

function isPatchText(text: string) {
  const value = String(text || '');
  return value.includes('*** Begin Patch');
}

function renderPatchLine(line: string, index: number) {
  const commonClass = 'px-3 py-0.5 font-mono text-sm leading-relaxed whitespace-pre';
  if (line.startsWith('*** Begin Patch') || line.startsWith('*** End Patch')) {
    return null;
  }
  if (line.startsWith('+')) {
    return <div key={index} data-id={`current-history-view-patch-line-add-${index}`} className={`${commonClass} bg-emerald-500/[0.08] text-emerald-300/85`}>{line}</div>;
  }
  if (line.startsWith('-')) {
    return <div key={index} data-id={`current-history-view-patch-line-remove-${index}`} className={`${commonClass} bg-red-500/[0.08] text-red-300/85`}>{line}</div>;
  }
  if (line.startsWith('@@')) {
    return null;
  }
  if (line.startsWith('*** Update File:')) {
    return <div key={index} data-id={`current-history-view-patch-line-update-${index}`} className={`${commonClass} bg-white/[0.03] text-zinc-300/90`}>{line.replace('*** Update File:', 'Update:')}</div>;
  }
  if (line.startsWith('*** ')) {
    return <div key={index} data-id={`current-history-view-patch-line-marker-${index}`} className={`${commonClass} bg-white/[0.03] text-zinc-300/90`}>{line}</div>;
  }
  return <div key={index} data-id={`current-history-view-patch-line-context-${index}`} className={`${commonClass} text-zinc-400/80`}>{line}</div>;
}

function shortenToolPath(text: string) {
  return String(text || '').replace(/^\/home\/cicy\/cicy-ai\/workers\//, '~/cicy-ai/workers/');
}

function tryParseJSONObject(text: string) {
  const value = String(text || '').trim();
  if (!value.startsWith('{') && !value.startsWith('[')) return null;
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

function humanizeToolPayload(value: any, depth = 0): string {
  if (value == null) return '';
  if (typeof value === 'string') return shortenToolPath(value);
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    const parts = value.map((item) => humanizeToolPayload(item, depth + 1)).filter(Boolean);
    return parts.join('\n').trim();
  }
  if (typeof value !== 'object') return String(value || '').trim();

  const preferredKeys = ['file_path', 'command', 'subject', 'description', 'text', 'content', 'input', 'output', 'result', 'old_string', 'new_string', 'question', 'label', 'name'];
  const lines: string[] = [];
  for (const key of preferredKeys) {
    if (!(key in value)) continue;
    const formatted = humanizeToolPayload(value[key], depth + 1);
    if (!formatted) continue;
    if (depth === 0 && (key === 'command' || key === 'file_path' || key === 'subject' || key === 'text')) {
      lines.push(formatted);
    } else {
      lines.push(`${key.replace(/_/g, ' ')}: ${formatted}`);
    }
  }
  if (lines.length) return lines.join('\n').trim();

  for (const [key, raw] of Object.entries(value)) {
    const formatted = humanizeToolPayload(raw, depth + 1);
    if (!formatted) continue;
    lines.push(`${key.replace(/_/g, ' ')}: ${formatted}`);
  }
  return lines.join('\n').trim();
}

function formatToolArg(tool: any) {
  const raw = String(tool?.arg || '').trim();
  if (!raw) return '';
  const parsed = tryParseJSONObject(raw);
  if (parsed != null) {
    const pretty = humanizeToolPayload(parsed);
    if (pretty) return pretty;
  }
  return shortenToolPath(raw);
}

// Parse a tool's input back into its object form (arg is JSON-stringified input).
function parseToolInput(tool: any): Record<string, any> | null {
  const raw = String(tool?.arg || '').trim();
  if (!raw.startsWith('{') && !raw.startsWith('[')) return null;
  try {
    const v = JSON.parse(raw);
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null;
  } catch {
    return null;
  }
}

// The ONE identifier a user scans for: which file / what command / what pattern.
// Shown in the (always-visible) header so the row is meaningful without expanding.
const TOOL_HEADLINE_KEYS = ['file_path', 'filePath', 'path', 'notebook_path', 'command', 'cmd', 'pattern', 'url', 'description', 'prompt', 'query', 'subject'];
function toolHeadline(tool: any): string {
  const input = parseToolInput(tool);
  if (input) {
    for (const k of TOOL_HEADLINE_KEYS) {
      const v = input[k];
      if (v != null && String(v).trim()) return shortenToolPath(String(v).trim());
    }
  }
  const raw = String(tool?.arg || '').trim();
  // codex apply_patch:arg 是 patch 文本(非 JSON)。headline 显示被改的文件,
  // 而不是无意义的 "*** Begin Patch"。
  if (isPatchText(raw)) {
    const m = raw.match(/\*\*\*\s*(Update|Add|Delete|Move) File:\s*(.+)/);
    if (m) return `${m[1]} ${shortenToolPath(m[2].trim())}`;
  }
  return shortenToolPath((formatToolArg(tool).split('\n')[0] || '').trim());
}

// The body of a file write (Write/NotebookEdit) — large, shown only when expanded,
// in a no-wrap horizontal-scroll block (never break-all the source).
function toolBodyContent(tool: any): string {
  const input = parseToolInput(tool);
  if (!input) return '';
  // new_string is intentionally excluded — Edit's old/new render as a diff.
  const c = input.content ?? input.new_source;
  return typeof c === 'string' ? c : '';
}

// Build an old→new diff for edit-style tools. Claude Edit uses old_string/new_string,
// MultiEdit uses edits[], and some paths precompute tool.diff — without surfacing
// any of these the tool card showed no diff at all ("edit diff 没出来").
function toolEditDiff(tool: any): { old: string; new: string } | null {
  if (tool?.diff?.old || tool?.diff?.new) {
    return { old: String(tool.diff.old || ''), new: String(tool.diff.new || '') };
  }
  const input = parseToolInput(tool);
  if (!input) return null;
  if (typeof input.old_string === 'string' || typeof input.new_string === 'string') {
    return { old: String(input.old_string || ''), new: String(input.new_string || '') };
  }
  if (Array.isArray(input.edits) && input.edits.length) {
    const oldT = input.edits.map((e: any) => String(e?.old_string || '')).filter(Boolean).join('\n');
    const newT = input.edits.map((e: any) => String(e?.new_string || '')).filter(Boolean).join('\n');
    if (oldT || newT) return { old: oldT, new: newT };
  }
  return null;
}

// Strip Claude Code's internal annotations from a tool result so history shows
// only the meaningful output. "(file state is current … no need to Read it back)"
// is a note to the model, not the user.
function cleanToolResult(text: string): string {
  return String(text || '')
    .replace(/\s*\(file state is current in your context[^)]*\)/gi, '')
    .replace(/\s*\(no content\)\s*/gi, '')
    .trim();
}

// exec_command that exits cleanly with no stdout used to render as an empty
// result → the expanded card looked like the click did nothing. Show a concise
// status instead (no command duplication, never an empty body).
function exitNoOutputNote(raw: string): string {
  const m = raw.match(/Process exited with code (\d+)/);
  return m
    ? i18n.t('toolExitCodeNoOutput', { ns: 'chat', code: m[1], defaultValue: '退出码 {{code}} · 无输出' })
    : i18n.t('toolExitNoOutput', { ns: 'chat', defaultValue: '退出 · 无输出' });
}

function formatToolResult(tool: any) {
  const name = String(tool?.name || '').trim();
  const raw = String(tool?.result || '').trim();
  if (!raw) {
    return '';
  }
  const parsed = tryParseJSONObject(raw);
  if (parsed != null) {
    const pretty = humanizeToolPayload(parsed);
    if (pretty) return pretty;
  }
  const marker = '\nOutput:\n';
  const index = raw.indexOf(marker);
  if (index >= 0) {
    const suffix = raw.slice(index + marker.length).trim();
    if (suffix) {
      const parsedSuffix = tryParseJSONObject(suffix);
      if (parsedSuffix != null) {
        const pretty = humanizeToolPayload(parsedSuffix);
        if (pretty) return pretty;
      }
      return shortenToolPath(suffix);
    }
    if (/Process exited with code 0\b/.test(raw)) {
      return exitNoOutputNote(raw);
    }
  }
  if (name === 'exec_command' && /Process exited with code 0\b/.test(raw)) {
    return exitNoOutputNote(raw);
  }
  return shortenToolPath(raw);
}

function buildToolCardId(turnKey: string | number, stepIndex: number, tool: any, toolIndex: number) {
  const name = String(tool?.name || 'tool').trim();
  return `${turnKey}:${stepIndex}:${toolIndex}:${name}`;
}

function ShellCommandBlock({ text }: { text: string }) {
  const content = String(text || '').trim();
  if (!content) {
    return null;
  }
  return (
    <div data-id="current-history-view-shell-command" className="chat-markdown current-history-markdown">
      <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={markdownComponents}>
        {`~~~bash\n${content}\n~~~`}
      </Markdown>
    </div>
  );
}

// memo:WS 直推下 live 尾巴每个 delta 都重渲染一次,工具卡的 body 解析(input/diff/
// result 格式化)不便宜;WS 追加路径只换最后一个生长中的 step 对象,其余 step 的
// tool 引用不变 → memo 直接命中,只有真变的卡才重算。
const ToolCard = memo(function ToolCard({ tool, toolId }: { tool: any; toolId: string }) {
  const { t } = useTranslation('chat');
  const [open, setOpen] = useState(() => toolCardOpenState.get(toolId) ?? false);
  const toolName = String(tool?.name || '').trim();
  const effectiveTool = tool;
  const input = parseToolInput(effectiveTool);
  const editDiff = toolEditDiff(effectiveTool);
  const hasDiff = !!editDiff && !!(editDiff.old || editDiff.new);
  const patchArg = String(effectiveTool?.arg || '');
  const showPatchArg = open && patchArg && isPatchText(patchArg);
  // 用户关心的:是哪个文件 / 什么命令 / 什么 pattern —— 永远显示在头部
  const headline = toolHeadline(effectiveTool);
  const command = input ? String(input.command ?? input.cmd ?? '').trim() : '';
  const bodyContent = toolBodyContent(effectiveTool);
  const displayResult = cleanToolResult(formatToolResult(effectiveTool));
  // For a short single-line command the headline already shows it in full, so
  // repeating it in the expanded body is pure duplication ("rm -rf rt/r2" twice).
  // BUT only suppress it when the body has something ELSE to show (a result,
  // diff, or content) — otherwise expanding would render an empty body and the
  // card looks like the click did nothing. So: redundant only if there's a body.
  const hasOtherBody = !!displayResult || hasDiff || !!bodyContent || showPatchArg;
  const commandRedundant = hasOtherBody && !!command && !command.includes('\n') && command.length <= 80 && shortenToolPath(command) === headline;
  // 兜底参数:只在没有 command / content / diff / patch 这些专门渲染时,才平铺剩余参数(如 Grep/Glob/Task)
  const genericArg = (!command && !bodyContent && !hasDiff && !showPatchArg) ? formatToolArg(effectiveTool) : '';
  const historyId = Number(tool?.history_id || 0);
  const stepIndex = Number(tool?.step_index || 0);
  const toolIndex = Number(tool?.tool_index || 0);

  useEffect(() => {
    setOpen(toolCardOpenState.get(toolId) ?? false);
  }, [toolId]);

  const toggleOpen = () => {
    setOpen((value) => {
      const next = !value;
      toolCardOpenState.set(toolId, next);
      return next;
    });
  };

  // 大段文字一律横向滚动、不换行(whitespace-pre,绝不 break-all)
  const scrollBlock = 'mx-2 mb-2 max-h-[280px] overflow-auto rounded bg-black/[0.18] px-2.5 py-1.5 font-mono text-xs leading-relaxed whitespace-pre';

  return (
    <div
      data-id="current-history-tool-card"
      data-tool-id={toolId}
      data-history-id={historyId > 0 ? String(historyId) : ''}
      data-step-index={String(stepIndex)}
      data-tool-index={String(toolIndex)}
      data-tool-name={toolName || 'tool'}
      data-open={open ? 'true' : 'false'}
      className="overflow-hidden rounded-lg border border-emerald-300/[0.08] bg-emerald-950/[0.12]"
    >
      <div
        data-id="current-history-tool-toggle"
        className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-zinc-500 hover:bg-white/[0.025] hover:text-zinc-400"
        onClick={toggleOpen}
      >
        <span data-id="current-history-tool-toggle-status" className="shrink-0 text-xs text-emerald-400/70">✓</span>
        <span data-id="current-history-tool-toggle-name" className="shrink-0 rounded border border-white/[0.04] bg-white/[0.035] px-1.5 py-0.5 text-xs text-zinc-300">{toolName || 'tool'}</span>
        {headline ? (
          <span data-id="current-history-tool-toggle-arg-preview" className="min-w-0 flex-1 truncate font-mono text-xs text-zinc-400/90" title={headline}>{headline}</span>
        ) : <span data-id="current-history-tool-toggle-spacer" className="flex-1" />}
        <span data-id="current-history-tool-toggle-expand" className="shrink-0 text-zinc-600" aria-label={open ? t('collapse') : t('expand')}>
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        </span>
      </div>
      {open ? (
        <>
          {showPatchArg ? (
            <div data-id="current-history-tool-arg" className="border-t border-white/[0.04] overflow-x-auto overflow-y-hidden">
              <div data-id="current-history-tool-arg-patch" className="min-w-max">
                {patchArg.split('\n').map((line: string, index: number) => renderPatchLine(line, index))}
              </div>
            </div>
          ) : (command && !commandRedundant) ? (
            <div data-id="current-history-tool-arg" className="border-t border-white/[0.04] px-3 py-1.5">
              <ShellCommandBlock text={command} />
            </div>
          ) : bodyContent ? (
            <pre data-id="current-history-tool-arg" className={`${scrollBlock} mt-2 text-zinc-400/85`}>{bodyContent}</pre>
          ) : genericArg ? (
            <pre data-id="current-history-tool-arg" className={`${scrollBlock} mt-2 text-zinc-400/85`}>{shortenToolPath(genericArg)}</pre>
          ) : null}
          {hasDiff && editDiff ? (
            <div data-id="current-history-tool-result" className="mx-2 mb-2 max-h-[300px] overflow-auto rounded border border-white/[0.06] font-mono text-xs">
              {editDiff.old ? editDiff.old.split('\n').map((line: string, index: number) => <div key={`old-${index}`} data-id={`current-history-tool-diff-old-${index}`} className="bg-red-500/[0.08] px-2 leading-relaxed whitespace-pre text-red-400/80">- {line}</div>) : null}
              {editDiff.new ? editDiff.new.split('\n').map((line: string, index: number) => <div key={`new-${index}`} data-id={`current-history-tool-diff-new-${index}`} className="bg-emerald-500/[0.08] px-2 leading-relaxed whitespace-pre text-emerald-400/80">+ {line}</div>) : null}
            </div>
          ) : displayResult ? (
            <pre data-id="current-history-tool-result" className={`${scrollBlock} bg-emerald-500/[0.04] text-emerald-300/70`}>{displayResult}</pre>
          ) : null}
        </>
      ) : null}
    </div>
  );
});

function HistoryTurnIdBadge({ historyId }: { historyId?: number }) {
  const value = Number(historyId || 0);
  if (value <= 0) return null;
  return (
    <div data-id="current-history-view-turn-id-badge" className="mb-1 px-0.5 font-mono text-[11px] text-zinc-600">
      #{value}
    </div>
  );
}

function ThinkingBlock({ text, live = false }: { text: string; live?: boolean }) {
  // 两态,不再做 3 行折叠:
  // - live(正在流式输出这一轮):强制全展开、无折叠 —— 实时看它思考。
  // - 完成态(committed / 新 q 之前):整块折叠成一个 "thinking" 小标,默认收起,点开看全文。
  const [expanded, setExpanded] = useState(false);
  const open = live || expanded;
  return (
    <div data-id="current-history-view-thinking-block" className="mb-2 border-l-2 border-amber-300/25 pl-3">
      {/* 完成态留一个可点开的小标;流式期不显示开关(强制展开,不可折叠)。 */}
      {!live ? (
        <button
          type="button"
          data-id="current-history-view-thinking-block-toggle"
          onClick={() => setExpanded((v) => !v)}
          aria-label={expanded ? 'collapse thinking' : 'expand thinking'}
          className="inline-flex items-center gap-1 rounded px-0.5 py-0.5 text-[11px] italic text-zinc-600 transition-colors hover:bg-white/[0.04] hover:text-zinc-400"
        >
          <ChevronRight className={`h-3 w-3 transition-transform ${expanded ? 'rotate-90' : ''}`} />
          thinking
        </button>
      ) : null}
      {/* thinking 要和正文区分:小一号(text-xs)、斜体、更暗的颜色。颜色用内联 style 强制 ——
          .chat-markdown{color:#d4d4d8} 是非分层规则,会盖掉 Tailwind 的 text-zinc-* 工具类,
          所以必须内联(内联优先级高于样式表类规则),<p> 子元素再继承这个颜色。 */}
      {open ? (
        <div
          data-id="current-history-view-thinking-block-body"
          className="chat-markdown current-history-markdown text-xs italic leading-[1.7]"
          style={{ color: '#52525b' }}
        >
          <MarkdownBlock text={text} />
        </div>
      ) : null}
    </div>
  );
}

// 把"每 ~500ms 轮询一次、整块替换"的流式文本平滑成逐字增长 —— 否则每个 poll 一大坨
// 文字"啪"地出现,看着像蹦字。poll 给的是「目标全文」,这里用 rAF 让显示长度按指数
// 逼近目标(剩得越多走得越快,poll 间隔内必然追平,绝不越拉越远),在两次 poll 之间
// 把那一坨摊成连续生长,观感连续。
// 关键:只对「正在流式输出的 live 尾巴的块」用。挂载瞬间直接 snap 到当前全文;换流
// (切 agent / 新一轮 / 重开)或非流式(已完成)一律瞬时显示(snap),绝不补演历史
// 回放 —— 那正是之前修掉的"打字进场"坑。
const SMOOTH_TICK_MS = 33;
function useSmoothStreamText(target: string, smooth: boolean): string {
  const [shown, setShown] = useState(target);
  const shownRef = useRef(target);
  useEffect(() => {
    // 非流式 / 目标不是当前显示的前缀延伸(换流、整块替换、回退)→ 瞬时 snap。
    if (!smooth || !target.startsWith(shownRef.current)) {
      shownRef.current = target;
      setShown(target);
      return;
    }
    if (target === shownRef.current) return;
    let raf = 0;
    let last = 0;
    const tick = (now: number) => {
      if (now - last >= SMOOTH_TICK_MS) {
        last = now;
        const cur = shownRef.current.length;
        const remain = target.length - cur;
        if (remain <= 0) return;
        const step = Math.max(2, Math.ceil(remain * 0.12));
        const next = target.slice(0, cur + step);
        shownRef.current = next;
        setShown(next);
        if (next.length >= target.length) return;
      }
      raf = window.requestAnimationFrame(tick);
    };
    raf = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(raf);
  }, [target, smooth]);
  return smooth ? shown : target;
}

// live 尾巴里的一个 thinking / text 块。smooth=true(本轮仍在流式且是最后一个块)时
// 文本走平滑生长;每长一点回调 onGrow 让父级在贴底时跟一次底 —— 生长发生在本组件
// 内部 state,父级的贴底 useLayoutEffect(依赖 liveTurn)看不到这些 tick。
const LiveStreamStep = memo(function LiveStreamStep({ kind, text, smooth, dataId, onGrow }: {
  kind: 'text' | 'thinking';
  text: string;
  smooth: boolean;
  dataId?: string;
  onGrow?: () => void;
}) {
  const shown = useSmoothStreamText(text, smooth);
  useLayoutEffect(() => { onGrow?.(); }, [shown, onGrow]);
  if (kind === 'thinking') return <ThinkingBlock text={shown} live={true} />;
  return (
    <div data-id={dataId} className="chat-markdown current-history-markdown text-sm leading-[1.7] text-zinc-300">
      <MarkdownBlock text={shown} />
    </div>
  );
});

// Harness-injected system notices (system-reminders, task notifications, date
// changes). Rendered as a compact, collapsed-by-default chip so the repeated
// ones don't read as duplicated AI replies.
function SystemNoticeCard({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  // Collapsed: a tiny centered "system" pill — no preview text, so the many
  // repeated reminders read as subtle separators and never clutter the
  // conversation. Click to expand the full text.
  return (
    <div data-id="current-history-system-notice" className="flex flex-col items-center">
      <button
        type="button"
        data-id="current-history-system-notice-toggle"
        onClick={() => setOpen((o) => !o)}
        className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] uppercase tracking-wide text-zinc-700 transition-colors hover:bg-white/[0.04] hover:text-zinc-400"
      >
        {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        system
      </button>
      {open ? (
        <div data-id="current-history-system-notice-body" className="mt-1 w-full whitespace-pre-wrap rounded-md border border-white/[0.05] bg-white/[0.02] px-2.5 py-1.5 text-[11px] leading-relaxed text-zinc-500">
          {text}
        </div>
      ) : null}
    </div>
  );
}

// OutcomeNoticeCard renders a cicy "turn produced no reply" record: the user
// cancelled the turn (grey ⏹) or the gateway failed after auto-retry (red ⚠).
// Unlike the generic SystemNoticeCard it is NOT folded away — the whole point is
// that the user SEES that their message wasn't silently dropped. On the latest
// turn it offers 重试 to re-run that same prompt.
function OutcomeNoticeCard({
  text,
  outcome,
  canRetry,
  retrying,
  onRetry,
}: {
  text: string;
  outcome: string;
  canRetry: boolean;
  retrying: boolean;
  onRetry: () => void;
}) {
  // Left-aligned, in the assistant body column (avatar sits to its left): reads as
  // a normal assistant output saying the turn failed / was stopped, with 重试 on the
  // latest turn. Subtle red for failures, grey for cancellations.
  const isError = outcome === 'error';
  return (
    <div data-id="current-history-outcome-notice" className="flex flex-col items-start gap-1.5">
      <div
        data-id="current-history-outcome-notice-chip"
        className={`flex items-start gap-1.5 text-sm leading-[1.7] ${isError ? 'text-rose-300/85' : 'text-zinc-400'}`}
      >
        {isError ? <AlertTriangle className="mt-[3px] h-3.5 w-3.5 shrink-0" /> : <Square className="mt-[3px] h-3.5 w-3.5 shrink-0" />}
        <span data-id="current-history-outcome-notice-label" className="break-all">{text}</span>
      </div>
      {canRetry ? (
        <button
          type="button"
          data-id="current-history-outcome-notice-retry"
          onClick={onRetry}
          disabled={retrying}
          className={`inline-flex shrink-0 items-center gap-1 rounded-md border px-2 py-0.5 text-xs transition-colors ${
            isError ? 'border-rose-500/25 text-rose-200/90 hover:bg-rose-500/10' : 'border-white/10 text-zinc-300 hover:bg-white/[0.06]'
          } disabled:opacity-60`}
        >
          <RotateCcw className={`h-3 w-3 ${retrying ? 'animate-spin' : ''}`} />
          {retrying ? '重试中…' : '重试'}
        </button>
      ) : null}
    </div>
  );
}

function PendingThinkingPlaceholder() {
  return (
    <div data-id="current-history-view-pending-placeholder" className="flex items-center gap-2 px-0.5 py-1 text-sm text-amber-100/65">
      <div data-id="current-history-view-pending-placeholder-dots" className="flex items-center gap-1">
        <span data-id="current-history-view-pending-placeholder-dot-1" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/70 [animation-delay:0ms]" />
        <span data-id="current-history-view-pending-placeholder-dot-2" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/55 [animation-delay:180ms]" />
        <span data-id="current-history-view-pending-placeholder-dot-3" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/40 [animation-delay:360ms]" />
      </div>
      <span data-id="current-history-view-pending-placeholder-label">Thinking...</span>
    </div>
  );
}

function isActiveAssistantStatus(status: string) {
  const value = String(status || '').trim().toLowerCase();
  return value === 'thinking' || value === 'working' || value === 'tool_use' || value === 'tool_call' || value === 'streaming';
}

// live 尾巴步骤的内容规模(text/thinking 总字符数 + tool 数)。WS 直推(cicy)下本地
// 尾巴可能比 reply.json 快照更超前 —— 替换守卫用它实现"同一 turn 内容只前进不回退"。
function liveStepsContentSize(steps: HistoryTurn['steps']): { textLen: number; toolCount: number } {
  let textLen = 0;
  let toolCount = 0;
  for (const s of (steps || []) as any[]) {
    if (s?.type === 'text' || s?.type === 'thinking') textLen += String(s?.text || '').length;
    else if (s?.type === 'tool') toolCount += Array.isArray(s?.tools) ? s.tools.length : 0;
  }
  return { textLen, toolCount };
}

function scheduleScrollToBottom(el: HTMLDivElement) {
  const apply = () => {
    el.scrollTop = el.scrollHeight;
  };
  apply();
  const raf = window.requestAnimationFrame(apply);
  const timers = [80, 240, 600, 1200, 2000].map((delay) => window.setTimeout(apply, delay));
  return { raf, timers };
}

export default function CurrentHistoryView({
  paneId,
  open,
  promptsOnly = false,
  hideTools = false,
  agentType = '',
}: {
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
}) {
  const { t } = useTranslation('chat');
  const [items, setItems] = useState<HistoryTurn[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [conversationId, setConversationId] = useState('');
  const [model, setModel] = useState('');
  // Prompts-only: question turns hydrated from IndexedDB for instant paint.
  // cachedPromptMaxIdRef holds the maxId the cache was built at — when it still
  // matches the live maxId, the cache is authoritative and the eager backfill
  // is skipped (no re-paging the history just to show the questions again).
  const [cachedPromptTurns, setCachedPromptTurns] = useState<HistoryTurn[]>([]);
  const cachedPromptMaxIdRef = useRef(0);
  const [pendingUrl, setPendingUrl] = useState<string | null>(null);  // external link awaiting confirm (#25)
  // Which outcome-notice turn is mid-retry (spinner on its 重试 button). Keyed by
  // the turn key so only the clicked one spins.
  const [retryingKey, setRetryingKey] = useState<string | null>(null);
  // Opening greeting shown on the empty-history state — role agents draw it from
  // their role template's 开场白 (GET /api/agents/greeting/{id}); falls back to the
  // static placeholder when empty/unfetched. Keyed by paneId so switching agents
  // re-fetches and never shows a stale role's line.
  const [greeting, setGreeting] = useState('');
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
      if (!cid || newMax <= 0) { setConversationId(cid); return; }
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
  }, [paneId, open]);

  // Fetch the role-specific opening greeting for the empty-history state. Reset
  // first so switching agents never flashes the previous role's line; ignore
  // failures (the render falls back to the static placeholder).
  useEffect(() => {
    setGreeting('');
    const id = String(paneId || '').trim();
    if (!id) return;
    let alive = true;
    apiService.getAgentGreeting(id)
      .then((res: any) => { if (alive) setGreeting(String(res?.data?.greeting || '').trim()); })
      .catch(() => {});
    return () => { alive = false; };
  }, [paneId]);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const updateStickBottom = () => {
      const top = el.scrollTop;
      const distanceToBottom = el.scrollHeight - top - el.clientHeight;
      if (top < lastScrollTopRef.current - 2) {
        // 向上滚 → 放手(并取消开屏排队中的补滚),让用户安心读前文,流式期也不拽回。
        shouldStickBottomRef.current = false;
        clearScheduledScrolls();
      } else if (distanceToBottom <= 8) {
        // 自己滚回到底部 → 重新跟随。程序化的"落底"也走这条,方向是向下、不会被误判为上滚。
        shouldStickBottomRef.current = true;
      }
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
    const onTouchStart = (e: TouchEvent) => { touchY = e.touches[0]?.clientY ?? 0; };
    const onTouchMove = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY ?? 0;
      if (y > touchY + 2) disengage(); // 手指向下拖 = 内容向上滚
      touchY = y;
    };
    el.addEventListener('scroll', updateStickBottom, { passive: true });
    el.addEventListener('wheel', onWheel, { passive: true });
    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: true });
    return () => {
      el.removeEventListener('scroll', updateStickBottom);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
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
        setModel(String(data?.model || '').trim());
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
    historyMemCache().set(paneId, {
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

        const answerId = Number(data?.history_id || 0); // = current.maxID + 1
        const complete = !!data?.complete;
        const replyStatus = String(data?.status || '').trim().toLowerCase();
        const replyFailed = replyStatus === 'failed' || replyStatus === 'fail' || replyStatus === 'error';
        const replyMaxId = answerId > 0 ? answerId - 1 : 0; // current.json maxID == q_last id

        // 广播忙/闲:有 in-flight 回复(有答案槽、未 complete、非 fail)就是 busy;占位 q 还在也算
        // busy。complete / fail 才解锁发送。
        const replyInFlight = answerId > 0 && !complete && !replyFailed;
        replyInFlightRef.current = replyInFlight;
        emitBusy(optimisticActiveRef.current || replyInFlight);

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
        const attach = sameConversation && effectiveAnswerId > boundary && (hasContent || !complete);
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
          const sig = `${turnId}:${effectiveAnswerId}:${status}:${String(data?.updated_at || '')}:${thinking.length}:${answer.length}:${liveItems.length}:${JSON.stringify(liveItems.map((it: any) => [it?.type, String(it?.thinking || it?.text || '').length, it?.name || '']))}`;
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
                const tool = { name: String(it?.name || ''), arg: inp == null ? '' : (typeof inp === 'string' ? inp : JSON.stringify(inp)), tool_id: String(it?.tool_id || '') };
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
      if (!isCicyLiteAgent(agentType)) return; // 非 cicy:原轮询 loop,不消费 WS
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
      if (qText && !isSlashCommand) {
        let maxUserId = 0;
        for (const it of itemsRef.current) if (it?.role === 'user') maxUserId = Math.max(maxUserId, Number(it?.history_id || 0));
        optimisticBaselineUserIdRef.current = maxUserId;
        setOptimisticQ({ text: qText, ts: Date.now() });
        // 自己发消息 → 重新贴底跟随(ChatGPT:发送后视图回到底部看自己的问题和回复)。
        shouldStickBottomRef.current = true;
      }
      if (timer != null) { window.clearTimeout(timer); timer = null; }
      void poll();
    };
    // Send failed → retract the optimistic q/a slots painted on click.
    const onCancelOptimistic = (e: Event) => {
      const id = String((e as CustomEvent)?.detail?.paneId || '').trim();
      if (id && id !== paneId) return;
      setOptimisticQ(null);
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
  }, [conversationId, open, paneId, model, agentType]);

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
      if (node) node.scrollTop = promptsOnly ? 0 : node.scrollHeight;
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
    if (!open || !canLoadMore) return;
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
  }, [open, canLoadMore, conversationId, nextBefore]);

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
    const map = new Map<number, HistoryTurn>();
    for (const t of cachedPromptTurns) {
      const id = Number(t?.history_id || 0);
      if (id > 0 && t?.role === 'user') map.set(id, t);
    }
    for (const t of items) {
      const id = Number(t?.history_id || 0);
      if (id > 0 && t?.role === 'user') map.set(id, t);
    }
    return Array.from(map.values()).sort((a, b) => Number(a?.history_id || 0) - Number(b?.history_id || 0));
  }, [promptsOnly, items, cachedPromptTurns, liveActive, committedMaxId]);

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

  // Hydrate the prompts cache (instant paint) whenever prompts-only is on for the
  // current conversation. Cleared when prompts-only is off / no conversation.
  useEffect(() => {
    if (!open || !promptsOnly || !conversationId) {
      setCachedPromptTurns([]);
      cachedPromptMaxIdRef.current = 0;
      return;
    }
    let cancelled = false;
    (async () => {
      const cache = await getPromptsCacheFromIndexedDB(paneId, conversationId);
      if (cancelled) return;
      setCachedPromptTurns(cache?.prompts || []);
      cachedPromptMaxIdRef.current = cache?.maxId || 0;
    })();
    return () => { cancelled = true; };
  }, [open, promptsOnly, paneId, conversationId]);

  // Prompts-only keeps just the user questions. The committed window is only the
  // last CURRENT_HISTORY_WINDOW raw items, which holds at most a question or two
  // (and sometimes none — a long assistant/tool round fills it). The "加载更早"
  // IntersectionObserver above can't backfill an empty/short list: a
  // permanently-intersecting sentinel never re-fires. So eagerly page earlier
  // windows until PROMPTS_ONLY_MIN_QUESTIONS prompts are loaded (or we hit the
  // start) — BUT skip that entirely when the cache built at the live maxId
  // already lists the questions (the whole point of caching: don't re-page).
  useEffect(() => {
    if (!open || !promptsOnly || loading || loadingMore || !canLoadMore) return;
    if (cachedPromptMaxIdRef.current > 0 && cachedPromptMaxIdRef.current === committedMaxId) return;
    const questionCount = displayItems.reduce((n, t) => (t?.role === 'user' ? n + 1 : n), 0);
    if (questionCount >= PROMPTS_ONLY_MIN_QUESTIONS) return;
    loadMoreFnRef.current();
  }, [open, promptsOnly, displayItems, loading, loadingMore, canLoadMore, committedMaxId]);

  // Persist the assembled question list, keyed by the live maxId so new turns /
  // a compaction (maxId shifts) invalidate it on the next open (docs §11/INV-9).
  useEffect(() => {
    if (!open || !promptsOnly || !conversationId || committedMaxId <= 0) return;
    const prompts = displayItems.filter((t) => t?.role === 'user');
    if (!prompts.length) return;
    cachedPromptMaxIdRef.current = committedMaxId;
    void setPromptsCacheToIndexedDB(paneId, conversationId, committedMaxId, prompts);
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

  // Memoized on `displayItems`: while a turn streams (only `liveTurn` changes),
  // these element refs stay identical, so React skips re-rendering every
  // committed history row (no Markdown re-parse per token).
  const renderedTurns = useMemo(() => displayItems.map((turn, index) => {
    const turnKey = turn?.history_id || `${turn?.text || turn?.q || 'turn'}-${index}`;
    const isLatestTurn = index === displayItems.length - 1;
    const allSteps = getVisibleHistorySteps(turn, isLatestTurn);
    const steps = hideTools ? (allSteps || []).filter((s: any) => s?.type !== 'tool') : allSteps;
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
                canRetry={isLatestTurn && !!paneId}
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
      return (
        <div data-id={itemId > 0 ? String(itemId) : undefined} data-turn-key={String(turnKey)} key={turnKey} className="mb-5">
          <CollapsibleQ text={turn.text || turn.q} />
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
      const showThinkingPlaceholder = isLatestTurn && String(turn?.status || '').trim() === 'thinking' && !String(turn?.a || '').trim() && !steps.length;
      const hasRenderableAssistantStep = steps.some((step: any) => {
        if (step?.type === 'thinking' && String(step?.text || '').trim()) return true;
        if (step?.type === 'text' && String(step?.text || '').trim()) return true;
        if (step?.type === 'tool' && Array.isArray(step?.tools) && step.tools.length > 0) return true;
        return false;
      });
      const fallbackAnswer = String(turn?.a || '').trim();
      return (
        <div data-id={itemId > 0 ? String(itemId) : undefined} data-turn-key={String(turnKey)} key={turnKey} className="mb-5">
          {/* ChatGPT 式回复头像:agent_type 的 logo 在答案左侧,与首行顶对齐;
              同一轮的后续 assistant item 不重复头像,用同宽空位对齐内容列 */}
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
              return <div key={stepIndex} data-id={`current-history-turn-step-tools-${turnKey}-${stepIndex}`} className="my-2 space-y-1.5">{tools.map((tool: any, toolIndex: number) => {
                const toolId = buildToolCardId(turnKey, stepIndex, tool, toolIndex);
                return <ToolCard key={toolId} tool={tool} toolId={toolId} />;
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
        </div>
      );
    }
    return null;
  }), [displayItems, promptsOnly, hideTools, recapResponses, agentType, paneId, retryingKey]);

  // Part 2 — the live tail (reply.json's answer for the latest turn). It is the
  // ANSWER to committed's last turn (q_last), so it renders answer-only and sits
  // right after the committed list. committedMaxId (defined above) == q_last id;
  // the tail's id == committedMaxId + 1, so `> committedMaxId` gates it (and
  // dedups against an already-migrated turn after switching away and back).
  const liveVisible = !promptsOnly && !!liveTurn && Number(liveTurn.history_id || 0) > committedMaxId;
  const liveTurnSteps = liveVisible && Array.isArray(liveTurn?.steps) ? liveTurn!.steps : [];
  // 本轮还在流式输出 → 最后一个 thinking/text 块走平滑生长(useSmoothStreamText)。
  const liveStreaming = liveVisible && isActiveAssistantStatus(String(liveTurn?.status || ''));
  // 平滑生长的每个 tick 都发生在 LiveStreamStep 内部 state,父级贴底 effect(依赖
  // liveTurn)看不到 —— 用这个回调在每次生长后跟一次底(仅当用户本来就贴底)。
  // useCallback([]):引用恒定,memo 化的 LiveStreamStep 对未生长的块才能命中缓存。
  const pinBottomIfSticking = useCallback(() => {
    const el = scrollRef.current;
    if (el && shouldStickBottomRef.current) el.scrollTop = el.scrollHeight;
  }, []);
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
        {!liveTurnSteps.length ? <PendingThinkingPlaceholder /> : null}
        </div>
      </div>
    </div>
  ) : null;

  return (
    <OpenUrlContext.Provider value={setPendingUrl}>
    <div data-id="current-history-view" className="flex h-full flex-col bg-[#0b0b0d]">
      {pendingUrl ? <LinkConfirmModal url={pendingUrl} onClose={() => setPendingUrl(null)} /> : null}
      {!loading && displayItems.length === 0 && !liveVisible && !optimisticQ ? (
        greeting ? (
          // 开场白渲染成一条正常的 assistant reply:左上角、带 agent 头像 + markdown
          // 内容列,与真实答案同布局(不再居中占位)。
          <div data-id="current-history-empty-greeting" className="flex-1 overflow-y-auto fade-scroll-y">
            <div className="mx-auto w-full max-w-4xl px-4 py-6 font-sans text-zinc-300">
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
      <div data-id="current-history-scroll" ref={scrollRef} className="flex-1 overflow-y-auto fade-scroll-y">
        <div data-id="current-history-list" data-agent-id={paneId || ''} className="mx-auto w-full max-w-4xl px-4 py-6 font-sans text-zinc-300">
          {loading ? (
            <div data-id="current-history-loading" className="space-y-6 py-2" aria-busy="true">
              {[0, 1, 2].map((row) => (
                <div key={row} data-id={`current-history-loading-row-${row}`} className="space-y-3">
                  <div className="flex justify-end">
                    <div className="h-8 w-1/2 animate-pulse rounded-2xl bg-white/[0.05]" />
                  </div>
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
            {/* 占位 q + a 必须排在 renderedLiveTurn 之后 —— 新问题 q2 是最新的一轮,而上一轮
                的答案 a1 在被 reconcileTail 迁进 committed 之前仍以 renderedLiveTurn(live 尾巴)
                渲染。若把 q2 排在它前面,顺序会变成 q1 → q2 → a1(q2 把 q1 的答案挤开、硬钉到顶
                又把 q1 顶出屏幕),这就是"q2 覆盖 q1"。放到最后,顺序恒为 …q1, a1, q2, a2占位。 */}
            {optimisticQ ? (
              <>
                {/* q 占位:独立块渲染,塞进/撤掉绝不触发 committed 列表(renderedTurns memo)
                    重算 → 历史 Markdown 不重渲染,q 点发送瞬间即现、不卡。sending 态(略透明),
                    真 q 落库后此块消失、committed 里的真 q 顶到同一位置。 */}
                <div data-turn-key={OPTIMISTIC_Q_KEY} className="mb-5">
                  <div data-id="current-history-optimistic-q" className="opacity-60 transition-opacity">
                    <CollapsibleQ text={optimisticQ.text} />
                  </div>
                </div>
                {/* a 占位:先撑出答案位(thinking),真答案一开始流式就由 renderedLiveTurn
                    接管 —— 占位 → 真 a,无新建、不跳。
                    ⚠️ 必须 `!liveVisible` 闸:poll 在同一帧 setLiveTurn(新轮)+setItems,而清
                    optimisticQ 在另一个 useEffect(晚一帧)→ 中间一帧 optimistic-a 占位 与
                    renderedLiveTurn 的占位(2724)并存 = 两个「Thinking…」脉冲动画。live 既已
                    接管答案位,乐观占位即冗余,直接撤掉,保证任一帧只有一个占位动画。 */}
                {!liveVisible ? (
                  <div data-id="current-history-optimistic-a" className="mb-5 flex items-start gap-2.5">
                    <AgentAvatar agentType={agentType} title={paneId} variant="select" dataId="current-history-optimistic-a-avatar" className="mt-0.5 h-7 w-7 rounded-full" />
                    <div className="min-w-0 flex-1">
                      <PendingThinkingPlaceholder />
                    </div>
                  </div>
                ) : null}
              </>
            ) : null}
          </>}
        </div>
      </div>
      </>
      )}
    </div>
    </OpenUrlContext.Provider>
  );
}
