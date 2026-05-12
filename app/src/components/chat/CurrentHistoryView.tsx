import { useEffect, useRef, useState } from 'react';
import Markdown from 'react-markdown';
import rehypeHighlight from 'rehype-highlight';
import remarkGfm from 'remark-gfm';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { useApp } from '../../contexts/AppContext';

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
const CURRENT_HISTORY_TOOL_DB_VERSION = 2;
const CURRENT_HISTORY_TOOL_STORE = 'tool_details';
const CURRENT_HISTORY_TURN_STORE = 'history_turns';
const CURRENT_HISTORY_PAGE_SIZE = 5;
const CURRENT_HISTORY_ENABLE_LIVE_WS = true;

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

function buildHistoryWindow(maxID: number, count: number, beforeExclusive?: number | null) {
  const size = Math.max(0, Math.floor(count));
  if (size <= 0) return [] as number[];
  const upper = beforeExclusive && beforeExclusive > 0 ? beforeExclusive - 1 : maxID;
  if (upper <= 0) return [] as number[];
  const lower = Math.max(1, upper - size + 1);
  const out: number[] = [];
  for (let id = lower; id <= upper; id += 1) {
    out.push(id);
  }
  return out;
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

async function getCurrentHistoryTurnByID(paneId: string, conversationId: string, historyID: number) {
  const cached = await getCurrentHistoryTurnsByIDsFromIndexedDB(paneId, conversationId, [historyID]);
  if (cached.length > 0) return cached[0] || null;
  const data = await getCurrentHistory(paneId, {
    limit: 1,
    before: historyID + 1,
    conversation_id: conversationId,
  });
  const rawItems = Array.isArray(data?.items) ? data.items : [];
  const matched = rawItems.find((item: any) => Number(item?.history_id || item?.id || 0) === historyID) || null;
  if (matched) {
    await setCurrentHistoryTurnsToIndexedDB(paneId, conversationId, [matched]);
  }
  return matched;
}

async function loadRawItemsByCount(
  paneId: string,
  conversationId: string,
  upperBoundId: number,
  beforeExclusive?: number | null,
  count = CURRENT_HISTORY_PAGE_SIZE,
) {
  const upper = beforeExclusive && beforeExclusive > 0 ? beforeExclusive - 1 : upperBoundId;
  if (upper <= 0) return [] as RawHistoryItem[];
  const rawItems: RawHistoryItem[] = [];
  let cardCount = 0;
  for (let id = upper; id >= 1 && cardCount < count; id -= 1) {
    const item = await getCurrentHistoryTurnByID(paneId, conversationId, id);
    if (!item) continue;
    const role = String(item?.role || '').trim();
    const isToolResult = role === 'user' && Array.isArray(item?.content) && item.content.some((p: any) => String(p?.type || '').trim() === 'tool_result' || String(p?.type || '').trim() === 'function_call_output');
    if (isToolResult) {
      rawItems.unshift(item);
      continue;
    }
    rawItems.unshift(item);
    cardCount += 1;
  }
  return rawItems;
}

function buildTurnsFromRawItems(rawItems: RawHistoryItem[]): HistoryTurn[] {
  const toolNameByCallId = new Map<string, string>();
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
        const currentToolUse = currentContent.find((p: any) => String(p?.type || '').trim() === 'tool_use');
        const nextToolResult = nextContent.find((p: any) => String(p?.type || '').trim() === 'tool_result' || String(p?.type || '').trim() === 'function_call_output');
        const callId = String(currentToolUse?.id || '').trim();
        if (callId && callId === String(nextToolResult?.tool_use_id || nextToolResult?.tool_id || '').trim()) {
          const item = JSON.parse(JSON.stringify(current));
          const itemContent = Array.isArray(item?.content) ? item.content : [];
          for (let j = 0; j < itemContent.length; j += 1) {
            if (String(itemContent[j]?.type || '').trim() === 'tool_use' && String(itemContent[j]?.id || '').trim() === callId) {
              const result = typeof nextToolResult?.content === 'string' ? nextToolResult.content : (nextToolResult?.content ? JSON.stringify(nextToolResult.content) : '');
              if (result) {
                itemContent[j] = { ...itemContent[j], _tool_result: result };
              }
              break;
            }
          }
          item.content = itemContent;
          item._has_tool_result = true;
          merged.push(item);
          i += 1;
          continue;
        }
      }
    }
    merged.push(current);
  }
  return merged
    .map((raw) => normalizeRawHistoryItem(raw, toolNameByCallId))
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

function normalizeRawHistoryItem(raw: any, toolNameByCallId?: Map<string, string>): HistoryTurn | null {
  if (!raw || typeof raw !== 'object') return null;
  const item = raw as RawHistoryItem;
  const historyId = Number(item.history_id || item.id || 0);
  const conversationId = String(item.conversation_id || '').trim();
  const role = String(item.role || '').trim();
  const itemType = String(item.type || '').trim();
  const model = String(item.model || '').trim();
  const status = String(item.status || '').trim() || 'text';
  if (role === 'user') {
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
  const steps: NonNullable<HistoryTurn['steps']> = [];
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
        result: '',
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
    <div className="w-full max-w-[95%] rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3 py-2.5 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
      <div className="mb-2 text-[11px] uppercase tracking-[0.08em] text-sky-200/55">Environment</div>
      <div className="space-y-1.5">
        {items.map((item) => (
          <div key={item.key} className="flex flex-col gap-1">
            <div className="text-[11px] text-sky-200/60">{item.label}</div>
            <div className="rounded-md border border-sky-200/[0.08] bg-black/[0.12] px-2 py-1 font-mono text-xs leading-relaxed text-sky-50/85 break-all">
              {item.value}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function CollapsibleQ({ text }: { text: string }) {
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
      <div className="mb-2.5 flex justify-end">
        <div className="max-w-[95%] flex flex-col gap-2">
          <pre className="overflow-x-auto rounded-lg border border-sky-300/[0.12] bg-black/[0.25] px-3 py-2 font-mono text-xs leading-relaxed text-sky-100/70 whitespace-pre-wrap">{xmlBlocks.join('\n')}</pre>
          {remaining ? (
            <div className="overflow-hidden rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3.5 py-2 text-base leading-relaxed text-sky-50/90 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
              <Markdown remarkPlugins={[remarkGfm]}>{remaining}</Markdown>
            </div>
          ) : null}
        </div>
      </div>
    );
  }
  return (
    <div className="mb-2.5 flex justify-end">
      {environmentContext ? (
        <EnvironmentContextCard context={environmentContext} />
      ) : (
        <div className="max-w-[95%] overflow-hidden rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3.5 py-2 text-base leading-relaxed text-sky-50/90 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
          <Markdown remarkPlugins={[remarkGfm]}>{String(text || '').replace(/^\-\n/, '')}</Markdown>
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
    return <div key={index} className={`${commonClass} bg-emerald-500/[0.08] text-emerald-300/85`}>{line}</div>;
  }
  if (line.startsWith('-')) {
    return <div key={index} className={`${commonClass} bg-red-500/[0.08] text-red-300/85`}>{line}</div>;
  }
  if (line.startsWith('@@')) {
    return null;
  }
  if (line.startsWith('*** Update File:')) {
    return <div key={index} className={`${commonClass} bg-white/[0.03] text-zinc-300/90`}>{line.replace('*** Update File:', 'Update:')}</div>;
  }
  if (line.startsWith('*** ')) {
    return <div key={index} className={`${commonClass} bg-white/[0.03] text-zinc-300/90`}>{line}</div>;
  }
  return <div key={index} className={`${commonClass} text-zinc-400/80`}>{line}</div>;
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
    if (raw.includes('Process exited with code 0')) {
      return '';
    }
  }
  if (name === 'exec_command' && raw.includes('Process exited with code 0')) {
    return '';
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
    <div className="chat-markdown current-history-markdown">
      <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]}>
        {`~~~bash\n${content}\n~~~`}
      </Markdown>
    </div>
  );
}

function ToolCard({ tool, toolId }: { tool: any; toolId: string }) {
  const { t } = useTranslation('chat');
  const [open, setOpen] = useState(() => toolCardOpenState.get(toolId) ?? false);
  const toolName = String(tool?.name || '').trim();
  const effectiveTool = tool;
  const hasDiff = !!effectiveTool?.diff?.old || !!effectiveTool?.diff?.new;
  const patchArg = String(effectiveTool?.arg || '');
  const displayArg = formatToolArg(effectiveTool);
  const displayResult = formatToolResult(effectiveTool);
  const showPatchArg = open && patchArg && isPatchText(patchArg);
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
        <span className="text-xs text-emerald-400/70">✓</span>
        <span className="rounded border border-white/[0.04] bg-white/[0.035] px-1.5 py-0.5 text-xs text-zinc-300">{toolName || 'tool'}</span>
        {!open && displayArg ? <span className="min-w-0 flex-1 truncate text-xs text-zinc-500">{displayArg}</span> : <span className="flex-1" />}
        <span className="text-[11px] text-zinc-600">{open ? t('collapse') : t('expand')}</span>
      </div>
      {showPatchArg ? (
        <div data-id="current-history-tool-arg" className="border-b border-white/[0.04] overflow-x-auto overflow-y-hidden">
          <div className="min-w-max">
            {patchArg.split('\n').map((line: string, index: number) => renderPatchLine(line, index))}
          </div>
        </div>
      ) : open && displayArg ? (
        <div data-id="current-history-tool-arg" className="border-b border-white/[0.04] px-3 py-1.5">
          {toolName === 'exec_command' ? (
            <ShellCommandBlock text={displayArg} />
          ) : (
            <div className="font-mono text-sm text-zinc-400/80 whitespace-pre-wrap break-all">{displayArg}</div>
          )}
        </div>
      ) : null}
      {open && hasDiff ? (
        <div data-id="current-history-tool-result" className="mx-2 mb-2 max-h-[300px] overflow-y-auto overflow-x-auto rounded border border-white/[0.06] font-mono text-xs">
          {tool.diff.old ? tool.diff.old.split('\n').map((line: string, index: number) => <div key={`old-${index}`} className="bg-red-500/[0.08] px-2 leading-relaxed whitespace-pre text-red-400/80">- {line}</div>) : null}
          {tool.diff.new ? tool.diff.new.split('\n').map((line: string, index: number) => <div key={`new-${index}`} className="bg-emerald-500/[0.08] px-2 leading-relaxed whitespace-pre text-emerald-400/80">+ {line}</div>) : null}
        </div>
      ) : open && displayResult ? (
        <pre data-id="current-history-tool-result" className="mx-2 mb-2 max-h-[200px] overflow-y-auto overflow-x-auto rounded bg-emerald-500/[0.04] px-2.5 py-1.5 font-mono text-xs leading-relaxed whitespace-pre text-emerald-300/70">{displayResult}</pre>
      ) : null}
    </div>
  );
}

function HistoryTurnIdBadge({ historyId }: { historyId?: number }) {
  const value = Number(historyId || 0);
  if (value <= 0) return null;
  return (
    <div className="mb-1 px-0.5 font-mono text-[11px] text-zinc-600">
      #{value}
    </div>
  );
}

function ThinkingBlock({ text }: { text: string }) {
  return (
    <div className="mb-2 rounded-lg border border-amber-300/[0.08] bg-amber-500/[0.05] px-3 py-2">
      <div className="chat-markdown current-history-markdown text-sm leading-[1.7] text-amber-50/75">
        <Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown>
      </div>
    </div>
  );
}

function PendingThinkingPlaceholder() {
  return (
    <div className="flex items-center gap-2 px-0.5 py-1 text-sm text-amber-100/65">
      <div className="flex items-center gap-1">
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/70 [animation-delay:0ms]" />
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/55 [animation-delay:180ms]" />
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/40 [animation-delay:360ms]" />
      </div>
      <span>Thinking...</span>
    </div>
  );
}

function isActiveAssistantStatus(status: string) {
  const value = String(status || '').trim().toLowerCase();
  return value === 'thinking' || value === 'working' || value === 'tool_use' || value === 'tool_call' || value === 'streaming';
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

function scheduleScrollTurnToTop(container: HTMLDivElement, turnKey: string) {
  const apply = () => {
    const target = container.querySelector(`[data-turn-key="${CSS.escape(turnKey)}"]`) as HTMLDivElement | null;
    if (!target) return;
    const containerRect = container.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    const top = container.scrollTop + (targetRect.top - containerRect.top) - 8;
    container.scrollTop = Math.max(0, top);
  };
  apply();
  const raf = window.requestAnimationFrame(apply);
  const timers = [80, 240, 600].map((delay) => window.setTimeout(apply, delay));
  return { raf, timers };
}

export default function CurrentHistoryView({
  paneId,
  open,
}: {
  paneId: string;
  open: boolean;
  inspectorVersion?: number;
}) {
  const { t } = useTranslation('chat');
  const { subscribeChatWs } = useApp();
  const [items, setItems] = useState<HistoryTurn[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [nextBefore, setNextBefore] = useState<number | null>(null);
  const [conversationId, setConversationId] = useState('');
  const [anchorSpacerHeight, setAnchorSpacerHeight] = useState(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const anchorSpacerRef = useRef<HTMLDivElement>(null);
  const didInitialScrollRef = useRef(false);
  const shouldStickBottomRef = useRef(true);
  const preserveScrollOffsetRef = useRef(false);
  const forceScrollBottomRef = useRef(true);
  const requestSeqRef = useRef(0);
  const lastUpdateEventKeyRef = useRef('');
  const lastLatestTurnKeyRef = useRef('');
  const pendingAnchorTurnKeyRef = useRef('');
  const lastAnchoredHistoryIDRef = useRef(0);
  const activeSpacerTurnKeyRef = useRef('');
  const anchorSpacerHeightRef = useRef(0);
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

  const applyAnchorSpacerHeight = (nextHeight: number) => {
    const normalized = Math.max(0, Math.round(nextHeight));
    if (anchorSpacerHeightRef.current === normalized) return;
    anchorSpacerHeightRef.current = normalized;
    setAnchorSpacerHeight(normalized);
  };

  useEffect(() => {
    lastUpdateEventKeyRef.current = '';
    lastLatestTurnKeyRef.current = '';
    pendingAnchorTurnKeyRef.current = '';
    lastAnchoredHistoryIDRef.current = 0;
    activeSpacerTurnKeyRef.current = '';
    shouldStickBottomRef.current = true;
    preserveScrollOffsetRef.current = false;
    forceScrollBottomRef.current = true;
    setHasMore(false);
    setNextBefore(null);
    setConversationId('');
    applyAnchorSpacerHeight(0);
    clearScheduledScrolls();
  }, [paneId, open]);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const updateStickBottom = () => {
      const distanceToBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
      shouldStickBottomRef.current = distanceToBottom <= 80;
    };
    updateStickBottom();
    el.addEventListener('scroll', updateStickBottom, { passive: true });
    return () => {
      el.removeEventListener('scroll', updateStickBottom);
    };
  }, [open, paneId]);

  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    const requestSeq = ++requestSeqRef.current;
    didInitialScrollRef.current = false;
    shouldStickBottomRef.current = true;
    forceScrollBottomRef.current = true;
    setLoading(true);
    getHistoryIDs(paneId)
      .then(async (data) => {
        if (cancelled || requestSeq !== requestSeqRef.current) return [] as HistoryTurn[];
        const nextConversationId = String(data?.conversation_id || '').trim();
        const nextMaxHistoryId = Number(data?.id || 0);
        const ids = buildHistoryWindow(nextMaxHistoryId, CURRENT_HISTORY_PAGE_SIZE);
        setConversationId(nextConversationId);
        setHasMore(ids.length > 0 && ids[0] > 1);
        setNextBefore(ids.length > 0 ? ids[0] : null);
        if (!nextConversationId || !ids.length) {
          return [] as HistoryTurn[];
        }
        const rawItems = await loadRawItemsByCount(
          paneId,
          nextConversationId,
          nextMaxHistoryId,
          null,
          CURRENT_HISTORY_PAGE_SIZE,
        );
        if (rawItems.length) {
          const firstLoadedId = Number(rawItems[0]?.history_id || rawItems[0]?.id || 0) || 0;
          setHasMore(firstLoadedId > 1);
          setNextBefore(firstLoadedId > 0 ? firstLoadedId : null);
        }
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
        if (cancelled || requestSeq !== requestSeqRef.current) return;
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, paneId]);

  useEffect(() => {
    if (!CURRENT_HISTORY_ENABLE_LIVE_WS) return;
    if (!open || !paneId) return;
    const unsubscribe = subscribeChatWs(async (msg) => {
      try {
      const type = String(msg?.type || '').trim();
      if (type !== 'current_updated' && type !== 'status_change' && type !== 'ai_chunk') return;
      const data = (msg?.data || {}) as any;
      const agentID = String(data?.agent_id || '').trim();
      if (agentID && agentID !== paneId) return;
      const historyID = Number(data?.history_id || 0);
      if (historyID <= 0) return;
      const eventConversationId = String(data?.conversation_id || '').trim();
      if (conversationId && eventConversationId && eventConversationId !== conversationId) return;
      if (eventConversationId) {
        setConversationId((prev) => prev || eventConversationId);
      }
      const eventKey = [type, agentID, eventConversationId, String(data?.turn_id || ''), String(historyID), String(data?.updated_at || ''), String(data?.delta || '')].join(':');
      if (eventKey && eventKey === lastUpdateEventKeyRef.current) return;
      lastUpdateEventKeyRef.current = eventKey;
      if (type === 'current_updated') {
        if (historyID !== lastAnchoredHistoryIDRef.current) {
          pendingAnchorTurnKeyRef.current = String(historyID);
          lastAnchoredHistoryIDRef.current = historyID;
        }
        setItems((prev) => {
          const next = ensureLatestStreamingTurn(prev, {
            historyId: historyID,
            conversationId: eventConversationId,
            status: String(data?.status || 'thinking').trim() || 'thinking',
            question: String(data?.question || '').trim(),
          });
          return next;
        });
        const rawItem = await getCurrentHistoryTurnByID(paneId, eventConversationId, historyID);
        if (rawItem) {
          await setCurrentHistoryTurnsToIndexedDB(paneId, eventConversationId, [rawItem]);
        }
        return;
      }
      if (type === 'status_change') {
        const status = String(data?.status || '').trim();
        setItems((prev) => updateLatestStreamingTurn(prev, {
          historyId: historyID,
          conversationId: eventConversationId,
          status,
          thinking: String(data?.thinking || ''),
        }));
        return;
      }
      if (type === 'ai_chunk') {
        const delta = String(data?.delta || '');
        if (!delta) return;
        setItems((prev) => updateLatestStreamingTurn(prev, {
          historyId: historyID,
          conversationId: eventConversationId,
          status: 'streaming',
          delta,
        }));
      }
      } catch {}
    });
    return () => {
      unsubscribe();
    };
  }, [conversationId, open, paneId, subscribeChatWs]);

  useEffect(() => {
    return () => {
      clearScheduledScrolls();
    };
  }, []);

  useEffect(() => {
    if (!open || loading) return;
    const frame = window.requestAnimationFrame(() => {
      const el = scrollRef.current;
      if (!el) return;
      const latest = items[items.length - 1];
      const latestTurnKey = latest ? String(latest?.history_id || `${latest?.text || latest?.q || 'turn'}-${Math.max(0, items.length - 1)}`) : '';
      const prevLatestTurnKey = lastLatestTurnKeyRef.current;
      const latestIsActive = isActiveAssistantStatus(String(latest?.status || ''));
      if (!pendingAnchorTurnKeyRef.current && latestTurnKey && latestTurnKey !== prevLatestTurnKey && latestIsActive) {
        pendingAnchorTurnKeyRef.current = latestTurnKey;
        lastAnchoredHistoryIDRef.current = Number(latest?.history_id || 0);
      }
      if (latestTurnKey && latestTurnKey !== prevLatestTurnKey) lastLatestTurnKeyRef.current = latestTurnKey;
      if (preserveScrollOffsetRef.current) {
        preserveScrollOffsetRef.current = false;
        didInitialScrollRef.current = true;
        return;
      }
      if (pendingAnchorTurnKeyRef.current) {
        const target = el.querySelector(`[data-turn-key="${CSS.escape(pendingAnchorTurnKeyRef.current)}"]`) as HTMLDivElement | null;
        if (!target) {
          didInitialScrollRef.current = true;
          return;
        }
        const nextSpacerHeight = target ? Math.max(0, el.clientHeight - target.offsetHeight - 16) : 0;
        applyAnchorSpacerHeight(nextSpacerHeight);
        runScheduledScroll(scheduleScrollTurnToTop(el, pendingAnchorTurnKeyRef.current));
        activeSpacerTurnKeyRef.current = pendingAnchorTurnKeyRef.current;
        pendingAnchorTurnKeyRef.current = '';
        forceScrollBottomRef.current = false;
        shouldStickBottomRef.current = false;
        didInitialScrollRef.current = true;
        return;
      }
      if (activeSpacerTurnKeyRef.current && anchorSpacerRef.current) {
        if (latestTurnKey !== activeSpacerTurnKeyRef.current) {
          activeSpacerTurnKeyRef.current = '';
        } else {
          if (!isActiveAssistantStatus(String(latest?.status || ''))) {
            didInitialScrollRef.current = true;
            return;
          }
          const target = el.querySelector(`[data-turn-key="${CSS.escape(activeSpacerTurnKeyRef.current)}"]`) as HTMLDivElement | null;
          if (target) {
            const currentSpacerHeight = anchorSpacerHeightRef.current;
            const measuredSpacerHeight = Math.max(0, el.clientHeight - target.offsetHeight - 16);
            const nextSpacerHeight = currentSpacerHeight > 0 ? Math.min(currentSpacerHeight, measuredSpacerHeight) : measuredSpacerHeight;
            applyAnchorSpacerHeight(nextSpacerHeight);
            if (nextSpacerHeight <= 0) {
              activeSpacerTurnKeyRef.current = '';
            }
          }
          didInitialScrollRef.current = true;
          return;
        }
      }
      if (!didInitialScrollRef.current) {
        if (!activeSpacerTurnKeyRef.current) {
          applyAnchorSpacerHeight(0);
        }
        runScheduledScroll(scheduleScrollToBottom(el));
        forceScrollBottomRef.current = false;
        shouldStickBottomRef.current = true;
      }
      didInitialScrollRef.current = true;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [open, loading, items]);

  const loadMore = async () => {
    if (loadingMore || loading || !nextBefore || Number(nextBefore) <= 1 || !conversationId) return;
    const requestPaneId = paneId;
    const requestSeq = ++requestSeqRef.current;
    const el = scrollRef.current;
    const prevScrollHeight = el?.scrollHeight || 0;
    const prevScrollTop = el?.scrollTop || 0;
    preserveScrollOffsetRef.current = true;
    setLoadingMore(true);
    try {
      const rawItems = await loadRawItemsByCount(
        paneId,
        conversationId,
        Number(nextBefore) - 1,
        null,
        CURRENT_HISTORY_PAGE_SIZE,
      );
      if (requestPaneId !== paneId || requestSeq !== requestSeqRef.current) return;
      if (!rawItems.length) {
        setHasMore(false);
        setNextBefore(null);
        return;
      }
      const older = buildTurnsFromRawItems(rawItems);
      const firstLoadedId = Number(rawItems[0]?.history_id || rawItems[0]?.id || 0) || 0;
      setItems((prev) => [...older, ...prev]);
      setHasMore(firstLoadedId > 1);
      setNextBefore(firstLoadedId > 0 ? firstLoadedId : null);
      window.requestAnimationFrame(() => {
        const nextEl = scrollRef.current;
        if (!nextEl) return;
        nextEl.scrollTop = prevScrollTop + Math.max(0, nextEl.scrollHeight - prevScrollHeight);
      });
    } catch {
      setHasMore(false);
    } finally {
      setLoadingMore(false);
    }
  };

  const canLoadMore = Number(nextBefore || 0) > 1;

  return (
    <div data-id="current-history-view" className="flex h-full flex-col">
      <div data-id="current-history-scroll" ref={scrollRef} className="flex-1 overflow-y-auto pb-6">
        <div data-id="current-history-list" data-agent-id={paneId || ''} className="mx-auto max-w-full px-2 py-4 font-sans text-zinc-300">
          {loading ? (
            <div data-id="current-history-loading" className="flex flex-col items-center justify-center gap-3 pt-20">
              <div className="h-6 w-6 animate-spin rounded-full border-2 border-vsc-accent/30 border-t-vsc-accent" />
              <span className="text-base text-zinc-500">{t('loadingHistory')}</span>
            </div>
          ) : items.length === 0 ? (
            <div data-id="current-history-empty" className="pt-20 text-center">
              <div className="mb-2 text-2xl text-zinc-700">✦</div>
              <p className="text-xs text-zinc-500">{t('emptyHistory')}</p>
            </div>
          ) : <>
            {canLoadMore ? (
              <div data-id="current-history-load-more-wrap" className="mb-4 flex justify-center">
                <button
                  type="button"
                  data-id="current-history-load-more"
                  onClick={() => { void loadMore(); }}
                  disabled={loadingMore}
                  className="rounded-md border border-white/[0.07] bg-white/[0.025] px-3 py-1.5 text-xs text-zinc-500 transition-colors hover:border-white/[0.12] hover:text-zinc-300 disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {loadingMore ? t('loadingMore') : t('loadEarlier')}
                </button>
              </div>
            ) : null}
            {items.map((turn, index) => {
            const turnKey = turn?.history_id || `${turn?.text || turn?.q || 'turn'}-${index}`;
            const isLatestTurn = index === items.length - 1;
            const steps = getVisibleHistorySteps(turn, isLatestTurn);
            const itemId = Number(turn?.history_id || 0);
            if (turn?.role === 'user') {
              return (
                <div
                  data-id={itemId > 0 ? String(itemId) : undefined}
                  key={turnKey}
                  className="mb-5"
                >
                  <CollapsibleQ text={turn.text || turn.q} />
                </div>
              );
            }
            if (turn?.role === 'assistant') {
              const showThinkingPlaceholder = isLatestTurn && String(turn?.status || '').trim() === 'thinking' && !String(turn?.a || '').trim() && !steps.length;
              const hasRenderableAssistantStep = steps.some((step: any) => {
                if (step?.type === 'thinking' && String(step?.text || '').trim()) return true;
                if (step?.type === 'text' && String(step?.text || '').trim()) return true;
                if (step?.type === 'tool' && Array.isArray(step?.tools) && step.tools.length > 0) return true;
                return false;
              });
              const fallbackAnswer = String(turn?.a || '').trim();
              return (
                <div
                  data-id={itemId > 0 ? String(itemId) : undefined}
                  key={turnKey}
                  className="mb-5"
                >
                  <div>
                    {steps.map((step: any, stepIndex: number) => {
                      if (step.type === 'thinking') {
                        return <div key={stepIndex}><ThinkingBlock text={step.text} /></div>;
                      }
                      if (step.type === 'text') {
                        return <div key={stepIndex} className="chat-markdown current-history-markdown text-sm leading-[1.7] text-zinc-300"><Markdown remarkPlugins={[remarkGfm]}>{step.text}</Markdown></div>;
                      }
                      const tools = Array.isArray(step.tools) ? step.tools : [];
                    return <div key={stepIndex} className="my-2 space-y-1.5">{tools.map((tool: any, toolIndex: number) => {
                        const toolId = buildToolCardId(turnKey, stepIndex, tool, toolIndex);
                        return <ToolCard key={toolId} tool={tool} toolId={toolId} />;
                      })}</div>;
                    })}
                    {!hasRenderableAssistantStep && fallbackAnswer ? (
                      <div className="chat-markdown current-history-markdown text-sm leading-[1.7] text-zinc-300">
                        <Markdown remarkPlugins={[remarkGfm]}>{fallbackAnswer}</Markdown>
                      </div>
                    ) : null}
                    {!hasRenderableAssistantStep && !fallbackAnswer && showThinkingPlaceholder ? <PendingThinkingPlaceholder /> : null}
                  </div>
                </div>
              );
            }
            return null;
          })}
          <div data-id="current-history-anchor-spacer" ref={anchorSpacerRef} aria-hidden="true" style={{ height: `${anchorSpacerHeight}px` }} />
          </>}
        </div>
      </div>
    </div>
  );
}
