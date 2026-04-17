const DB_NAME = 'chat_cache'
const ITEM_STORE = 'history_items'
const META_STORE = 'history_meta'

export interface HistorySyncItem {
  id: string
  seq: number
  role: 'system' | 'user' | 'assistant' | 'tool' | 'thinking' | string
  text?: string
  content_json?: Record<string, any>
  ts?: string
  turn_id?: string
  status?: string
  model?: string
}

interface HistoryMetaRecord {
  paneId: string
  conversationId: string
  cursor: string
  updatedAt: number
}

interface HistoryItemRecord {
  key: string
  paneId: string
  conversationId: string
  seq: number
  item: HistorySyncItem
}

function openHistoryDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 2)
    req.onerror = () => reject(req.error)
    req.onsuccess = () => resolve(req.result)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(ITEM_STORE)) {
        const store = db.createObjectStore(ITEM_STORE, { keyPath: 'key' })
        store.createIndex('byPaneSeq', ['paneId', 'seq'], { unique: false })
      }
      if (!db.objectStoreNames.contains(META_STORE)) {
        db.createObjectStore(META_STORE, { keyPath: 'paneId' })
      }
      if (!db.objectStoreNames.contains('history')) {
        db.createObjectStore('history', { keyPath: 'paneId' })
      }
    }
  })
}

export async function getHistoryMeta(paneId: string): Promise<HistoryMetaRecord | null> {
  try {
    const db = await openHistoryDB()
    return await new Promise((resolve) => {
      const req = db.transaction(META_STORE, 'readonly').objectStore(META_STORE).get(paneId)
      req.onsuccess = () => resolve(req.result || null)
      req.onerror = () => resolve(null)
    })
  } catch {
    return null
  }
}

export async function getRecentHistoryItems(paneId: string, limit = 50): Promise<HistorySyncItem[]> {
  try {
    const db = await openHistoryDB()
    return await new Promise((resolve) => {
      const tx = db.transaction(ITEM_STORE, 'readonly')
      const index = tx.objectStore(ITEM_STORE).index('byPaneSeq')
      const range = IDBKeyRange.bound([paneId, 0], [paneId, Number.MAX_SAFE_INTEGER])
      const req = index.getAll(range)
      req.onsuccess = () => {
        const rows = (req.result || []) as HistoryItemRecord[]
        const items = rows.sort((a, b) => a.seq - b.seq).slice(-limit).map((row) => row.item)
        resolve(items)
      }
      req.onerror = () => resolve([])
    })
  } catch {
    return []
  }
}

export async function replaceHistoryBase(paneId: string, conversationId: string, items: HistorySyncItem[], cursor: string): Promise<void> {
  try {
    const db = await openHistoryDB()
    await new Promise<void>((resolve) => {
      const tx = db.transaction([ITEM_STORE, META_STORE], 'readwrite')
      const store = tx.objectStore(ITEM_STORE)
      const index = store.index('byPaneSeq')
      const range = IDBKeyRange.bound([paneId, 0], [paneId, Number.MAX_SAFE_INTEGER])
      const getReq = index.getAllKeys(range)
      getReq.onsuccess = () => {
        for (const key of getReq.result || []) store.delete(key)
        for (const item of items) {
          const key = `${paneId}:${conversationId}:${item.seq}:${item.id}`
          store.put({ key, paneId, conversationId, seq: item.seq, item })
        }
        tx.objectStore(META_STORE).put({ paneId, conversationId, cursor, updatedAt: Date.now() })
      }
      tx.oncomplete = () => resolve()
      tx.onerror = () => resolve()
    })
  } catch {}
}

export async function appendHistoryItems(paneId: string, conversationId: string, items: HistorySyncItem[], cursor: string): Promise<void> {
  try {
    const db = await openHistoryDB()
    await new Promise<void>((resolve) => {
      const tx = db.transaction([ITEM_STORE, META_STORE], 'readwrite')
      const store = tx.objectStore(ITEM_STORE)
      for (const item of items) {
        const key = `${paneId}:${conversationId}:${item.seq}:${item.id}`
        store.put({ key, paneId, conversationId, seq: item.seq, item })
      }
      tx.objectStore(META_STORE).put({ paneId, conversationId, cursor, updatedAt: Date.now() })
      tx.oncomplete = () => resolve()
      tx.onerror = () => resolve()
    })
  } catch {}
}
