// Shared "handled / feedback" state for audit alerts.
//
// Two sources of truth are merged:
//   1. The server ack ledger — an ack is stored as its own event with
//      meta.category === 'meta_alert_ack' and meta.ack_event_id === <orig id>
//      (see api/mgr/audit/ack.go recordAck). Anything acked there (e.g. via the
//      incident email link) counts as handled.
//   2. A local feedback map (localStorage) — lets the operator mark an alert
//      handled / false-positive straight from the log without the email round
//      trip. Optimistic + survives reload.
//
// AuditLogTab (badges + buttons) reads through these helpers to resolve each
// alert's handled status.

const KEY = 'cicy.auditGuard.handled';

export type HandledStatus = 'done' | 'false_positive';
export interface HandledEntry { status: HandledStatus; ts: number; }
export type HandledMap = Record<string, HandledEntry>;

export function loadHandled(): HandledMap {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return {};
    const o = JSON.parse(raw);
    return o && typeof o === 'object' ? o : {};
  } catch {
    return {};
  }
}

export function saveHandled(map: HandledMap) {
  try { localStorage.setItem(KEY, JSON.stringify(map)); } catch { /* ignore */ }
}

export function setHandled(id: string, status: HandledStatus, nowMs: number): HandledMap {
  const map = loadHandled();
  map[id] = { status, ts: nowMs };
  saveHandled(map);
  return map;
}

export function clearHandled(id: string): HandledMap {
  const map = loadHandled();
  delete map[id];
  saveHandled(map);
  return map;
}

interface EventLike {
  id?: string;
  meta?: { category?: string; ack_event_id?: string };
}

/** Event IDs that have a server-side ack record. */
export function serverAckedIds(events: EventLike[]): Set<string> {
  const s = new Set<string>();
  for (const e of events) {
    if (e.meta?.category === 'meta_alert_ack' && e.meta?.ack_event_id) {
      s.add(e.meta.ack_event_id);
    }
  }
  return s;
}

export type ResolvedStatus = 'open' | 'done' | 'false_positive';

/** Merge local feedback + server acks into a single status for one event. */
export function resolveStatus(id: string, local: HandledMap, serverAcked: Set<string>): ResolvedStatus {
  const l = local[id];
  if (l) return l.status;
  if (serverAcked.has(id)) return 'done';
  return 'open';
}
