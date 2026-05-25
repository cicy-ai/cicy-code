// Stale-while-revalidate cache for /api/fs/* responses.
//
// Pattern: consumers peek the cache for an instant first render, then call the
// network. The fresh response updates the cache; subscribers re-render. Writes
// and fsnotify events invalidate affected keys.
//
// Namespaces are LRU-capped independently so a single huge `read` doesn't
// evict a thousand small `list` rows.

type Namespace = 'list' | 'read' | 'stat' | 'diff' | 'search' | 'grep';

interface CacheEntry<T> {
  data: T;
  fetchedAt: number;
}

const stores: Record<Namespace, Map<string, CacheEntry<any>>> = {
  list: new Map(),
  read: new Map(),
  stat: new Map(),
  diff: new Map(),
  search: new Map(),
  grep: new Map(),
};

const caps: Record<Namespace, number> = {
  list: 256,
  read: 32,
  stat: 256,
  diff: 32,
  search: 64,
  grep: 32,
};

const subs = new Map<string, Set<() => void>>();

function fullKey(ns: Namespace, key: string): string {
  return `${ns}::${key}`;
}

function notify(ns: Namespace, key: string): void {
  const fns = subs.get(fullKey(ns, key));
  if (fns) fns.forEach((fn) => fn());
}

export function fsCacheSubscribe(
  ns: Namespace,
  key: string,
  fn: () => void,
): () => void {
  const fk = fullKey(ns, key);
  let s = subs.get(fk);
  if (!s) {
    s = new Set();
    subs.set(fk, s);
  }
  s.add(fn);
  return () => {
    const cur = subs.get(fk);
    if (!cur) return;
    cur.delete(fn);
    if (cur.size === 0) subs.delete(fk);
  };
}

export function fsCachePeek<T>(ns: Namespace, key: string): T | undefined {
  return stores[ns]?.get(key)?.data as T | undefined;
}

export function fsCacheSet<T>(ns: Namespace, key: string, data: T): void {
  const store = stores[ns];
  if (!store) return;
  // Touch: remove + re-insert so Map's insertion order doubles as LRU.
  if (store.has(key)) store.delete(key);
  store.set(key, { data, fetchedAt: Date.now() });
  // Evict oldest until under cap.
  const cap = caps[ns] || 100;
  while (store.size > cap) {
    const it = store.keys();
    const k = it.next().value;
    if (k === undefined) break;
    store.delete(k);
  }
  notify(ns, key);
}

export function fsCacheInvalidate(ns: Namespace, key: string): void {
  const store = stores[ns];
  if (!store?.has(key)) return;
  store.delete(key);
  notify(ns, key);
}

export function fsCacheInvalidatePrefix(ns: Namespace, prefix: string): void {
  const store = stores[ns];
  if (!store) return;
  const removed: string[] = [];
  for (const k of store.keys()) {
    if (k.startsWith(prefix)) removed.push(k);
  }
  for (const k of removed) {
    store.delete(k);
    notify(ns, k);
  }
}

// --- composite helpers --------------------------------------------------

/**
 * Invalidate every cached entry that may have been affected by a
 * file-system mutation at (agentId, path). Used by:
 *   - successful writes
 *   - incoming fsnotify watch events
 * Hits read/stat for the file, the diff entries, and the list for the parent dir.
 */
export function fsCacheInvalidatePath(agentId: string, path: string): void {
  const idx = path.lastIndexOf('/');
  const parent = idx <= 0 ? '' : path.slice(0, idx);
  fsCacheInvalidate('read', `${agentId}:${path}`);
  fsCacheInvalidate('stat', `${agentId}:${path}`);
  fsCacheInvalidatePrefix('diff', `${agentId}:${path}:`);
  // list cache keys include the hidden flag suffix
  fsCacheInvalidatePrefix('list', `${agentId}:${parent}:`);
}

/**
 * Drop every cached entry tied to an agent. Useful when an agent is
 * disposed or rebound, so a future visit doesn't show stale rows.
 */
export function fsCacheClearAgent(agentId: string): void {
  (Object.keys(stores) as Namespace[]).forEach((ns) => {
    fsCacheInvalidatePrefix(ns, `${agentId}:`);
  });
}

// --- key builders -------------------------------------------------------

export const fsKey = {
  list: (agentId: string, path: string, hidden: boolean) =>
    `${agentId}:${path}:${hidden ? 1 : 0}`,
  read: (agentId: string, path: string) => `${agentId}:${path}`,
  stat: (agentId: string, path: string) => `${agentId}:${path}`,
  diff: (agentId: string, path: string, base: string) =>
    `${agentId}:${path}:${base}`,
};
