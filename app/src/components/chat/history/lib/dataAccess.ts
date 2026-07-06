// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import apiService from '../../../../services/api';
import type { RawHistoryItem } from '../types';
import { CURRENT_HISTORY_WINDOW } from '../constants';
import { buildIdRange } from './turns';
import { getCurrentHistoryTurnsByIDsFromIndexedDB, setCurrentHistoryTurnsToIndexedDB } from './cache';

const currentHistoryPendingRequests = new Map<string, Promise<any>>();
const currentHistoryRecentResponses = new Map<string, { at: number; data: any }>();

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
export async function loadWindowItems(
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

export function buildCurrentHistoryRequestKey(paneId: string, params: { limit?: number; before?: number; conversation_id?: string } = {}) {
  return JSON.stringify({
    paneId: String(paneId || '').trim(),
    limit: Number(params.limit || 0),
    before: Number(params.before || 0),
    conversation_id: String(params.conversation_id || '').trim(),
  });
}

export async function getCurrentHistory(paneId: string, params: { limit?: number; before?: number; conversation_id?: string } = {}) {
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

export async function getHistoryIDs(paneId: string, params: { conversation_id?: string } = {}) {
  const { data } = await apiService.getAgentHistoryIDs(paneId, params as any);
  return data;
}
