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
