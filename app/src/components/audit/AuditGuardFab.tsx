import { useEffect, useState } from 'react';
import { useApp } from '../../contexts/AppContext';
import { TokenManager } from '../../services/tokenManager';
import { useAuditHits } from './useAuditHits';
import AuditDashboard from './AuditDashboard';

// The audit entry lives in the left activity bar (a SideBtn below btn-windows),
// which dispatches TOGGLE_EVENT to open/close this console. The old floating
// draggable FAB and the embedded w-6001 chat WebFrame were both removed (w-6001
// no longer exists) — the console now shows ONLY 日志 (logs) and 策略 (policy).
const TOGGLE_EVENT = 'cicy:toggle-audit-guard';
const OPEN_KEY = 'cicy.auditGuard.open';

// Gate: only acts when the audit master switch (global audit_enabled) is on —
// audit off ⇒ no collection/scanning, so the entry must do nothing.
export default function AuditGuardFab() {
  const { globalVar } = useApp();
  if (!globalVar?.audit_enabled) return null;
  return <AuditGuardFabInner />;
}

function AuditGuardFabInner() {
  const token = TokenManager.getToken() || '';
  const [open, setOpen] = useState<boolean>(() => {
    try { return localStorage.getItem(OPEN_KEY) === '1'; } catch { return false; }
  });
  const { markRead } = useAuditHits(!!token);

  // Open/close driven by the activity-bar SideBtn (below btn-windows).
  useEffect(() => {
    const onToggle = () => setOpen((v) => !v);
    window.addEventListener(TOGGLE_EVENT, onToggle);
    return () => window.removeEventListener(TOGGLE_EVENT, onToggle);
  }, []);

  useEffect(() => { try { localStorage.setItem(OPEN_KEY, open ? '1' : '0'); } catch { /* ignore */ } }, [open]);
  // Opening the console acknowledges outstanding hits.
  useEffect(() => { if (open) markRead(); }, [open, markRead]);

  if (!token || !open) return null;

  return (
    <div data-id="audit-guard-console" className="fixed inset-0 z-[100001] bg-[var(--vsc-bg)]">
      <AuditDashboard
        variant="embedded"
        tabs={['live', 'agent']}
        defaultTab="live"
        onClose={() => setOpen(false)}
        onMinimize={() => setOpen(false)}
      />
    </div>
  );
}
