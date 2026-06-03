import { useEffect, useMemo, useRef, useState } from 'react';
import { ShieldCheck, ShieldAlert, ChevronDown, Maximize2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useApp } from '../../contexts/AppContext';
import { urls } from '../../config';
import { TokenManager } from '../../services/tokenManager';
import apiService from '../../services/api';
import Select, { type SelectOption } from '../ui/Select';
import { useAuditHits } from './useAuditHits';
import AuditDashboard from './AuditDashboard';

const GUARD_PANE = 'w-6001:main.0';
const OPEN_KEY = 'cicy.auditGuard.open';
const PANE_KEY = 'cicy.auditGuard.pane';
const POS_KEY = 'cicy.auditGuard.pos';

const FAB_SIZE = 56;       // h-14 / w-14
const DRAG_THRESHOLD = 4;  // px moved before a press becomes a drag (vs a click)

// Position is stored as right/bottom px offsets so the panel keeps growing
// up-left from the FAB regardless of where it is parked.
interface Offset { right: number; bottom: number; }

function loadOffset(): Offset {
  try {
    const raw = localStorage.getItem(POS_KEY);
    if (raw) {
      const o = JSON.parse(raw);
      if (Number.isFinite(o?.right) && Number.isFinite(o?.bottom)) return { right: o.right, bottom: o.bottom };
    }
  } catch { /* ignore */ }
  return { right: 16, bottom: 16 };
}

function clampOffset(o: Offset): Offset {
  const maxRight = Math.max(0, window.innerWidth - FAB_SIZE);
  const maxBottom = Math.max(0, window.innerHeight - FAB_SIZE);
  return {
    right: Math.min(Math.max(0, o.right), maxRight),
    bottom: Math.min(Math.max(0, o.bottom), maxBottom),
  };
}

function fullPaneId(id: string): string {
  if (!id) return GUARD_PANE;
  return id.includes(':') ? id : `${id}:main.0`;
}

interface ProviderOption {
  key: string;
  label?: string;
  models?: string[];
}

/**
 * Persistent floating "data guardian" for the workspace. Collapsed it is a
 * round shield FAB with a working-ring animation; on a block/redact hit it
 * turns red with a count. Tapping it opens a panel that embeds the audit
 * agent (pane w-6001) terminal chat plus a conversation-target picker
 * (agent + model). Hiding only collapses — the iframe stays mounted so the
 * terminal session survives.
 */
// Gate: the floating guardian only mounts when the audit master switch is on.
// The switch lives in global settings (/api/settings/global → useApp().globalVar
// .audit_enabled), so it follows a UI/API toggle reactively. When audit is off
// there is no collection/scanning, so the icon must not show.
export default function AuditGuardFab() {
  const { globalVar } = useApp();
  if (!globalVar?.audit_enabled) return null;
  return <AuditGuardFabInner />;
}

function AuditGuardFabInner() {
  const { t } = useTranslation('audit');
  const { allPanes } = useApp();
  const token = TokenManager.getToken() || '';

  const [open, setOpen] = useState<boolean>(() => {
    try { return localStorage.getItem(OPEN_KEY) === '1'; } catch { return false; }
  });
  const [maximized, setMaximized] = useState(false);
  const [selectedPane, setSelectedPane] = useState<string>(() => {
    try { return localStorage.getItem(PANE_KEY) || GUARD_PANE; } catch { return GUARD_PANE; }
  });
  const [providers, setProviders] = useState<ProviderOption[]>([]);
  const [currentModel, setCurrentModel] = useState<string>('');

  const [offset, setOffset] = useState<Offset>(loadOffset);
  const dragRef = useRef<{ startX: number; startY: number; right: number; bottom: number; moved: boolean } | null>(null);

  const { unread, latest, connected, loaded, markRead } = useAuditHits(!!token);

  // Keep the FAB on-screen across viewport resizes / rotation.
  useEffect(() => {
    const onResize = () => setOffset((o) => clampOffset(o));
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => { try { localStorage.setItem(POS_KEY, JSON.stringify(offset)); } catch { /* ignore */ } }, [offset]);

  const handlePointerDown = (e: React.PointerEvent) => {
    dragRef.current = { startX: e.clientX, startY: e.clientY, right: offset.right, bottom: offset.bottom, moved: false };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  };

  const handlePointerMove = (e: React.PointerEvent) => {
    const d = dragRef.current;
    if (!d) return;
    const dx = e.clientX - d.startX;
    const dy = e.clientY - d.startY;
    if (!d.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
    d.moved = true;
    // Pointer moving right/down shrinks the right/bottom offsets.
    setOffset(clampOffset({ right: d.right - dx, bottom: d.bottom - dy }));
  };

  const handlePointerUp = (e: React.PointerEvent) => {
    const d = dragRef.current;
    dragRef.current = null;
    try { (e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId); } catch { /* ignore */ }
    // A genuine click (no drag) toggles the panel.
    if (d && !d.moved) setOpen((v) => !v);
  };

  useEffect(() => { try { localStorage.setItem(OPEN_KEY, open ? '1' : '0'); } catch { /* ignore */ } }, [open]);
  useEffect(() => { try { localStorage.setItem(PANE_KEY, selectedPane); } catch { /* ignore */ } }, [selectedPane]);

  // Opening the panel acknowledges outstanding hits.
  useEffect(() => { if (open) markRead(); }, [open, markRead]);

  // Pull the selected pane's available models + current default.
  useEffect(() => {
    if (!token) return;
    let alive = true;
    apiService.getPane(selectedPane)
      .then((res: any) => {
        if (!alive) return;
        const d = res?.data || {};
        const list = Array.isArray(d.runtime_ai_provider_options) ? d.runtime_ai_provider_options : [];
        setProviders(list.map((p: any) => ({
          key: String(p?.key || ''),
          label: String(p?.label || p?.key || ''),
          models: Array.isArray(p?.models) ? p.models.map((m: any) => String(m)) : [],
        })).filter((p: ProviderOption) => p.key));
        setCurrentModel(String(d.default_model || ''));
      })
      .catch(() => { if (alive) { setProviders([]); setCurrentModel(''); } });
    return () => { alive = false; };
  }, [selectedPane, token]);

  const agentOptions = useMemo<SelectOption[]>(() => {
    const opts: SelectOption[] = [{ value: GUARD_PANE, label: t('guardAgentLabel') }];
    allPanes.forEach((p) => {
      const id = fullPaneId(p.pane_id);
      if (id === GUARD_PANE) return;
      opts.push({ value: id, label: p.title || p.pane_id });
    });
    return opts;
  }, [allPanes, t]);

  const modelOptions = useMemo<SelectOption[]>(() => {
    const out: SelectOption[] = [];
    providers.forEach((pv) => {
      (pv.models || []).forEach((m) => out.push({ value: `${pv.key}::${m}`, label: m, sub: pv.label }));
    });
    return out;
  }, [providers]);

  const modelValue = useMemo(() => {
    const hit = modelOptions.find((o) => o.value.split('::')[1] === currentModel);
    return hit?.value || '';
  }, [modelOptions, currentModel]);

  const handleModelChange = (val: string) => {
    const [providerKey, model] = val.split('::');
    setCurrentModel(model || '');
    apiService.updatePane(selectedPane, {
      default_model: model || '',
      use_custom_gateway: true,
      runtime_ai: providerKey ? { provider_name: providerKey } : null,
    }).catch(() => { /* best-effort */ });
  };

  const src = useMemo(() => (token ? urls.ttydOpen(selectedPane, token) : ''), [token, selectedPane]);

  if (!token) return null;

  const alerting = unread > 0;
  const actionLabel = latest?.action === 'block' ? t('guardBlocked') : t('guardRedacted');

  return (
    <>
      {/* Maximized console — trimmed dashboard tabs (logs / policy / setup). */}
      {maximized && (
        <div data-id="audit-guard-max" className="fixed inset-0 z-[100001] bg-[var(--vsc-bg)]">
          <AuditDashboard
            variant="embedded"
            tabs={['assistant', 'live', 'agent', 'setup']}
            onMinimize={() => setMaximized(false)}
            onClose={() => { setMaximized(false); setOpen(false); }}
          />
        </div>
      )}

      <div
        data-id="audit-guard-root"
        className="fixed z-[99999] flex flex-col items-end gap-3"
        style={{ right: offset.right, bottom: offset.bottom }}
      >
      {/* Panel — always mounted so the terminal session survives hide. */}
      <div
        data-id="audit-guard-panel"
        className={`${open && !maximized ? 'flex' : 'hidden'} flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl
          w-[min(380px,calc(100vw-2rem))] h-[min(560px,calc(100vh-6rem))]`}
      >
        <div data-id="audit-guard-panel-header" className="flex items-center justify-between border-b border-white/[0.06] px-4 py-3 shrink-0">
          <div className="flex items-center gap-2 min-w-0">
            {alerting
              ? <ShieldAlert className="h-5 w-5 text-red-400 shrink-0" />
              : <ShieldCheck className="h-5 w-5 text-emerald-400 shrink-0" />}
            <div className="min-w-0">
              <div data-id="audit-guard-panel-title" className="text-[14px] font-semibold text-white truncate">{t('guardTitle')}</div>
              <div data-id="audit-guard-panel-status" className={`text-[11px] truncate ${alerting ? 'text-red-300' : connected ? 'text-emerald-300/80' : 'text-zinc-500'}`}>
                {alerting && latest
                  ? t('guardLatestHit', { action: actionLabel, rule: latest.ruleIds[0] || '—' })
                  : (!loaded || connected) ? t('guardStatusGuarding') : t('disconnected')}
              </div>
            </div>
          </div>
          <div className="flex items-center gap-0.5">
            <button
              data-id="audit-guard-panel-maximize"
              type="button"
              onClick={() => setMaximized(true)}
              title={t('guardMaximize')}
              className="cursor-pointer rounded-lg p-1.5 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            >
              <Maximize2 className="h-[15px] w-[15px]" />
            </button>
            <button
              data-id="audit-guard-panel-hide"
              type="button"
              onClick={() => setOpen(false)}
              title={t('guardHide')}
              className="cursor-pointer rounded-lg p-1.5 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            >
              <ChevronDown className="h-4 w-4" />
            </button>
          </div>
        </div>

        <div data-id="audit-guard-panel-selectors" className="grid grid-cols-2 gap-2 border-b border-white/[0.06] px-4 py-3 shrink-0">
          <div className="min-w-0">
            <label className="mb-1 block text-[11px] font-medium text-zinc-400">{t('guardAgentField')}</label>
            <Select options={agentOptions} value={selectedPane} onChange={setSelectedPane} />
          </div>
          <div className="min-w-0">
            <label className="mb-1 block text-[11px] font-medium text-zinc-400">{t('guardModelField')}</label>
            <Select
              options={modelOptions}
              value={modelValue}
              onChange={handleModelChange}
              placeholder={modelOptions.length ? undefined : t('guardModelNone')}
            />
          </div>
        </div>

        <div data-id="audit-guard-panel-frame-wrap" className="flex-1 min-h-0 bg-black">
          {src && (
            <iframe
              data-id="audit-guard-panel-frame"
              src={src}
              className="h-full w-full border-0 bg-black"
              title="audit-guard-chat"
            />
          )}
        </div>
      </div>

      {/* FAB — round shield with a guarding-ring animation. */}
      <button
        data-id="audit-guard-fab"
        type="button"
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        title={open ? t('guardHide') : t('guardOpenTitle')}
        style={{
          touchAction: 'none',
          boxShadow: alerting
            ? '0 0 30px rgba(248,113,113,0.5), 0 14px 34px -8px rgba(0,0,0,0.78), inset 0 1px 0 rgba(255,255,255,0.09), inset 0 -6px 14px rgba(0,0,0,0.45)'
            : '0 14px 34px -8px rgba(0,0,0,0.72), 0 3px 10px rgba(0,0,0,0.45), inset 0 1px 0 rgba(255,255,255,0.10), inset 0 -6px 14px rgba(0,0,0,0.45)',
        }}
        className={`group relative flex h-14 w-14 cursor-grab touch-none items-center justify-center rounded-full bg-gradient-to-b ring-1 ring-inset transition-transform duration-150 ease-out active:scale-95 active:cursor-grabbing hover:scale-[1.05]
          ${alerting
            ? 'from-red-500/30 to-red-700/10 border border-red-400/50 ring-red-200/15'
            : 'from-[#26262b] to-[#141416] border border-white/[0.12] ring-white/[0.06]'}`}
      >
        {/* Working ring */}
        <svg data-id="audit-guard-fab-ring" className="absolute inset-0 h-full w-full animate-spin" style={{ animationDuration: '6s' }} viewBox="0 0 56 56" fill="none">
          <circle
            cx="28" cy="28" r="25"
            stroke={alerting ? 'rgba(248,113,113,0.85)' : 'rgba(52,211,153,0.7)'}
            strokeWidth="2"
            strokeLinecap="round"
            strokeDasharray="40 120"
          />
        </svg>
        {alerting
          ? <ShieldAlert className="relative h-6 w-6 text-red-400 animate-pulse" />
          : <ShieldCheck className="relative h-6 w-6 text-emerald-400" />}
        {alerting && (
          <span
            data-id="audit-guard-fab-badge"
            className="absolute -top-0.5 -right-0.5 flex h-5 min-w-[20px] items-center justify-center rounded-full bg-red-500 px-1 text-[11px] font-semibold text-white shadow-[0_2px_6px_rgba(0,0,0,0.5)] ring-2 ring-[#161618]"
          >
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>
      </div>
    </>
  );
}
