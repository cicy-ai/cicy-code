import { useCallback, useEffect, useRef, useState } from 'react';
import apiService from '../../services/api';
import { loadHandled, serverAckedIds, resolveStatus } from './auditHandled';

// A "hit" = the audit pipeline actually acted on a request: blocked it or
// redacted it. Plain log/notify findings are intentionally ignored so the
// guard badge only lights up when data was really protected.
export interface AuditHit {
  id: string;
  ts: string;          // RFC3339
  tsMs: number;        // parsed Date.parse(ts)
  action: string;      // "block" | "redact"
  agentId: string;
  ruleIds: string[];
}

interface AuditEventLike {
  id?: string;
  ts?: string;
  identity?: { agent_id?: string };
  meta?: { category?: string; ack_event_id?: string };
  findings?: Array<{ rule_id?: string }>;
  decision?: { action?: string; applied?: boolean };
}

const ACK_KEY = 'cicy.auditGuard.lastAckMs';
const POLL_MS = 5000;

function loadAck(): number {
  try {
    const v = Number(localStorage.getItem(ACK_KEY));
    return Number.isFinite(v) && v > 0 ? v : 0;
  } catch {
    return 0;
  }
}

function saveAck(ms: number) {
  try {
    localStorage.setItem(ACK_KEY, String(ms));
  } catch {
    /* ignore quota / privacy-mode errors */
  }
}

function isHit(ev: AuditEventLike): boolean {
  const a = ev.decision?.action;
  return !!ev.decision?.applied && (a === 'block' || a === 'redact');
}

function toHit(ev: AuditEventLike): AuditHit {
  return {
    id: String(ev.id || ''),
    ts: String(ev.ts || ''),
    tsMs: Date.parse(String(ev.ts || '')) || 0,
    action: String(ev.decision?.action || ''),
    agentId: String(ev.identity?.agent_id || ''),
    ruleIds: Array.isArray(ev.findings) ? ev.findings.map((f) => String(f?.rule_id || '')).filter(Boolean) : [],
  };
}

/**
 * Polls /api/audit/events and surfaces block/redact hits for the guard FAB.
 * - `unread`: count of hits newer than the last acknowledged timestamp.
 * - `latest`: the most recent hit overall (for the panel's status line).
 * - `markRead()`: acknowledge everything up to the newest hit (clears badge).
 *
 * On first ever mount (no stored ack) we baseline the ack to the newest
 * existing hit so the historical backlog does not flood the badge — only
 * hits that occur while the widget is alive raise an alert.
 */
export function useAuditHits(enabled: boolean = true) {
  const [unread, setUnread] = useState(0);
  const [latest, setLatest] = useState<AuditHit | null>(null);
  const [connected, setConnected] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const ackRef = useRef<number>(loadAck());
  const baselinedRef = useRef<boolean>(ackRef.current > 0);

  const recompute = useCallback((hits: AuditHit[]) => {
    if (!hits.length) {
      setLatest(null);
      setUnread(0);
      return;
    }
    const sorted = [...hits].sort((a, b) => b.tsMs - a.tsMs);
    const newest = sorted[0];
    setLatest(newest);
    if (!baselinedRef.current) {
      // First load: treat the existing backlog as already-seen.
      ackRef.current = newest.tsMs;
      baselinedRef.current = true;
      saveAck(ackRef.current);
      setUnread(0);
      return;
    }
    setUnread(sorted.filter((h) => h.tsMs > ackRef.current).length);
  }, []);

  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const tick = async () => {
      try {
        const res: any = await apiService.getAuditEvents({ limit: 100 });
        if (!alive) return;
        const events: AuditEventLike[] = Array.isArray(res?.data?.events) ? res.data.events : [];
        // Exclude alerts already handled (locally marked) or acked (server
        // ledger) so the FAB stops alerting once the operator deals with them.
        const handled = loadHandled();
        const acked = serverAckedIds(events);
        const hits = events
          .filter(isHit)
          .filter((e) => resolveStatus(String(e.id || ''), handled, acked) === 'open')
          .map(toHit);
        setConnected(true);
        recompute(hits);
      } catch {
        if (alive) setConnected(false);
      } finally {
        if (alive) { setLoaded(true); timer = setTimeout(tick, POLL_MS); }
      }
    };
    tick();

    return () => {
      alive = false;
      if (timer) clearTimeout(timer);
    };
  }, [enabled, recompute]);

  const markRead = useCallback(() => {
    const ms = latest?.tsMs || Date.now();
    ackRef.current = ms;
    saveAck(ms);
    setUnread(0);
  }, [latest]);

  return { unread, latest, connected, loaded, markRead };
}

export default useAuditHits;
