// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import {
  CURRENT_HISTORY_TOOL_DB_NAME,
  CURRENT_HISTORY_TOOL_DB_VERSION,
  CURRENT_HISTORY_TOOL_STORE,
  CURRENT_HISTORY_TURN_STORE,
  CURRENT_HISTORY_PROMPTS_STORE,
} from '../constants';
import type { RawHistoryItem, HistoryTurn, HistoryMemSnapshot } from '../types';

export function openCurrentHistoryToolDB(): Promise<IDBDatabase | null> {
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

export function buildHistoryTurnCacheKey(paneId: string, conversationId: string, historyId: number) {
  return `${paneId}:${conversationId}:${historyId}`;
}

// Per-pane row cap for the persisted turn store. The store is written on every
// window/tail fetch and, with rotating conversation_ids (a new one per /clear),
// grows without bound — permanently, since it survives reloads and nothing ever
// evicted it. Cap the rows we keep per pane and trim the least-recently-cached
// beyond it. Trimming is a full index scan for the pane, so amortize it: run it
// once every TRIM_EVERY_N_WRITES writes rather than on each put.
const HISTORY_TURN_ROWS_PER_PANE_MAX = 600;
const TRIM_EVERY_N_WRITES = 20;
let turnWritesSinceTrim = 0;

function trimPaneTurnRows(db: IDBDatabase, paneId: string) {
  if (!db.objectStoreNames.contains(CURRENT_HISTORY_TURN_STORE)) return;
  try {
    const tx = db.transaction(CURRENT_HISTORY_TURN_STORE, 'readwrite');
    const store = tx.objectStore(CURRENT_HISTORY_TURN_STORE);
    const index = store.index('by_pane');
    const rows: { key: IDBValidKey; updatedAt: number }[] = [];
    const cursorReq = index.openCursor(IDBKeyRange.only(paneId));
    cursorReq.onsuccess = () => {
      const cursor = cursorReq.result;
      if (cursor) {
        rows.push({ key: cursor.primaryKey, updatedAt: Number(cursor.value?.updatedAt || 0) });
        cursor.continue();
        return;
      }
      const excess = rows.length - HISTORY_TURN_ROWS_PER_PANE_MAX;
      if (excess > 0) {
        rows.sort((a, b) => a.updatedAt - b.updatedAt); // oldest-cached first
        for (let i = 0; i < excess; i++) store.delete(rows[i].key);
      }
    };
  } catch {}
}

export async function setCurrentHistoryTurnsToIndexedDB(paneId: string, conversationId: string, turns: RawHistoryItem[]) {
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
    if (++turnWritesSinceTrim >= TRIM_EVERY_N_WRITES) {
      turnWritesSinceTrim = 0;
      trimPaneTurnRows(db, paneId);
    }
  } catch {}
}

export async function getCurrentHistoryTurnsByIDsFromIndexedDB(paneId: string, conversationId: string, historyIDs: number[]) {
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

export function historyMemCache(): Map<string, HistoryMemSnapshot> {
  const w = window as unknown as { _cacheHistory?: Map<string, HistoryMemSnapshot> };
  if (!(w._cacheHistory instanceof Map)) w._cacheHistory = new Map();
  return w._cacheHistory;
}

// LRU cap for the in-memory snapshot cache on window._cacheHistory. Each entry
// holds a FULL history window (+ liveTurn) and is keyed by paneId, so a plain
// .set() accumulates one big snapshot per distinct agent ever opened, kept for
// the life of the tab. Bound it to the most-recently-touched panes; the active
// pane is re-written on every poll, so it always stays resident (never evicted).
const HISTORY_MEM_CACHE_MAX = 24;

export function setHistoryMemCache(paneId: string, snapshot: HistoryMemSnapshot) {
  const cache = historyMemCache();
  cache.delete(paneId); // re-insert at the tail so iteration order is LRU
  cache.set(paneId, snapshot);
  let excess = cache.size - HISTORY_MEM_CACHE_MAX;
  if (excess > 0) {
    for (const k of cache.keys()) {
      if (excess-- <= 0) break;
      cache.delete(k);
    }
  }
}
