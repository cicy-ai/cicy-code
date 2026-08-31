// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// Shared primitives for the 设备 (Devices) panel.
//
// Why these exist: the devices UI grew across three files with three different
// visual languages — six hand-rolled empty/error blocks, four button shapes,
// paddings drifting between px-2 / px-2.5 / px-3, and type sizes between 10 and
// 13px on the same row. Worse, every surface was a hard-coded dark hex, which
// the light theme can only reach through the `[class~="bg-[#0e0e0e]"]` allowlist
// in index.css — so any new colour was invisible in light mode.
//
// Everything here paints from the `--dev-*` tokens (defined per theme in
// index.css), so a component written once is correct in both themes.
import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Loader2, ChevronDown, Check } from 'lucide-react';
import { cn } from '../../../lib/utils';

// ── surfaces ─────────────────────────────────────────────────────────────────
export const surface = 'bg-[var(--dev-bg)] text-[var(--dev-text)]';

/** A panel header bar: fixed height, bottom rule, title + trailing actions. */
export function HeaderBar({ icon, title, subtitle, children, className }: {
  icon?: React.ReactNode;
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  children?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      data-id="dev-header"
      className={cn(
        'flex h-11 shrink-0 items-center gap-2 border-b border-[var(--dev-border)] bg-[var(--dev-surface)] px-2.5',
        className,
      )}
    >
      {icon ? <span className="shrink-0 text-[var(--dev-text-3)]">{icon}</span> : null}
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13px] font-medium leading-tight text-[var(--dev-text)]">{title}</div>
        {subtitle ? <div className="truncate text-[11px] leading-tight text-[var(--dev-text-3)]">{subtitle}</div> : null}
      </div>
      {children}
    </div>
  );
}

/** A secondary bar under a header — holds toolbars/filters. Wraps on narrow columns. */
export function Toolbar({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div
      data-id="dev-toolbar"
      className={cn(
        'flex shrink-0 flex-wrap items-center gap-1.5 border-b border-[var(--dev-border)] bg-[var(--dev-surface)] px-2 py-1.5',
        className,
      )}
    >
      {children}
    </div>
  );
}

// ── buttons ──────────────────────────────────────────────────────────────────
/** Square icon-only button. `tone` colours the resting glyph. */
export function IconBtn({ icon, title, onClick, disabled, busy, tone = 'muted', active, className, dataId }: {
  icon: React.ReactNode;
  title: string;
  onClick?: () => void;
  disabled?: boolean;
  busy?: boolean;
  tone?: 'muted' | 'accent' | 'danger';
  active?: boolean;
  className?: string;
  dataId?: string;
}) {
  const toneCls =
    tone === 'accent' ? 'text-[var(--dev-accent)]'
      : tone === 'danger' ? 'text-[var(--dev-live)]'
        : 'text-[var(--dev-text-3)]';
  return (
    <button
      type="button"
      data-id={dataId}
      onClick={onClick}
      disabled={disabled || busy}
      title={title}
      aria-label={title}
      className={cn(
        'inline-flex h-7 w-7 shrink-0 cursor-pointer items-center justify-center rounded-lg transition-colors',
        'hover:bg-[var(--dev-hover)] hover:text-[var(--dev-text)]',
        'disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent',
        active ? 'bg-[var(--dev-active)] text-[var(--dev-text)]' : toneCls,
        className,
      )}
    >
      {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : icon}
    </button>
  );
}

/** Text button with an optional leading icon. */
export function Btn({ children, icon, onClick, disabled, busy, variant = 'ghost', title, className, dataId }: {
  children: React.ReactNode;
  icon?: React.ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  busy?: boolean;
  variant?: 'ghost' | 'solid' | 'accent';
  title?: string;
  className?: string;
  dataId?: string;
}) {
  const variantCls =
    variant === 'accent' ? 'bg-[var(--dev-accent-bg)] text-[var(--dev-accent)] hover:brightness-110'
      : variant === 'solid' ? 'bg-[var(--dev-raise)] text-[var(--dev-text)] hover:bg-[var(--dev-active)]'
        : 'text-[var(--dev-text-2)] hover:bg-[var(--dev-hover)] hover:text-[var(--dev-text)]';
  return (
    <button
      type="button"
      data-id={dataId}
      onClick={onClick}
      disabled={disabled || busy}
      title={title}
      className={cn(
        'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-lg px-2.5 text-[12px] font-medium transition-colors',
        'disabled:cursor-not-allowed disabled:opacity-40',
        variantCls,
        className,
      )}
    >
      {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : icon}
      {children}
    </button>
  );
}

// ── chips / badges ───────────────────────────────────────────────────────────
export function Chip({ children, tone = 'neutral', title, className, dataId }: {
  children: React.ReactNode;
  tone?: 'neutral' | 'ok' | 'warn' | 'accent' | 'live';
  title?: string;
  className?: string;
  dataId?: string;
}) {
  const toneCls = {
    neutral: 'bg-[var(--dev-raise)] text-[var(--dev-text-3)]',
    ok: 'bg-[var(--dev-ok-bg)] text-[var(--dev-ok)]',
    warn: 'bg-[var(--dev-warn-bg)] text-[var(--dev-warn)]',
    accent: 'bg-[var(--dev-accent-bg)] text-[var(--dev-accent)]',
    live: 'bg-[var(--dev-live-bg)] text-[var(--dev-live)]',
  }[tone];
  return (
    <span
      data-id={dataId}
      title={title}
      className={cn('inline-flex max-w-full items-center gap-1 truncate rounded px-1.5 py-0.5 text-[10px] font-medium leading-4', toneCls, className)}
    >
      {children}
    </span>
  );
}

/** Small pulsing dot — online state, live-capture indicator. */
export function Dot({ tone = 'neutral', pulse }: { tone?: 'neutral' | 'ok' | 'live' | 'warn'; pulse?: boolean }) {
  const color = { neutral: 'var(--dev-text-3)', ok: 'var(--dev-ok)', live: 'var(--dev-live)', warn: 'var(--dev-warn)' }[tone];
  return (
    <span className="relative inline-flex h-1.5 w-1.5 shrink-0">
      {pulse ? <span className="absolute inline-flex h-full w-full animate-ping rounded-full opacity-70" style={{ background: color }} /> : null}
      <span className="relative inline-flex h-1.5 w-1.5 rounded-full" style={{ background: color }} />
    </span>
  );
}

// ── segmented tabs ───────────────────────────────────────────────────────────
export interface SegItem<K extends string> {
  k: K;
  label: string;
  icon: React.ReactNode;
  /** Optional count badge; `undefined` renders nothing, 0 renders a dim zero. */
  count?: number;
}

/**
 * Segmented control that spends its width where it is needed: the SELECTED tab
 * expands to show its label (and count), the rest stay icon-only.
 *
 * The old strip was icon-only for all five, with three near-identical monitor-ish
 * glyphs — you could not tell Electron from 桌面 without clicking each one. Showing
 * every label instead does not fit either: in a 280px column five labels truncate
 * to "C… E… A… iOS D…", which is worse than icons. Labelling only the active tab
 * fits, and answers the question you actually have ("where am I?") while the
 * count badge says where the devices are.
 */
export function SegTabs<K extends string>({ items, value, onChange, compact }: {
  items: SegItem<K>[];
  value: K;
  onChange: (k: K) => void;
  /** Force icon-only for every tab (used where even one label will not fit). */
  compact?: boolean;
}) {
  return (
    <div
      data-id="dev-segtabs"
      role="tablist"
      className="flex shrink-0 items-center gap-0.5 rounded-xl bg-[var(--dev-surface-2)] p-0.5"
    >
      {items.map((it) => {
        const on = it.k === value;
        return (
          <button
            key={it.k}
            type="button"
            role="tab"
            aria-selected={on}
            data-id={`dev-segtab-${it.k}`}
            onClick={() => onChange(it.k)}
            title={it.label}
            className={cn(
              'flex cursor-pointer items-center justify-center gap-1 rounded-[10px] py-1.5 text-[11px] font-medium transition-all',
              // The active tab takes the slack; the others shrink to their glyph.
              on
                ? 'min-w-0 flex-1 bg-[var(--dev-surface)] px-2 text-[var(--dev-text)] shadow-sm'
                : 'shrink-0 px-2 text-[var(--dev-text-3)] hover:bg-[var(--dev-hover)] hover:text-[var(--dev-text-2)]',
            )}
          >
            <span className="shrink-0">{it.icon}</span>
            {on && !compact && <span className="truncate">{it.label}</span>}
            {on && it.count !== undefined && it.count > 0 && (
              <span className="shrink-0 rounded bg-[var(--dev-accent-bg)] px-1 text-[9px] leading-4 text-[var(--dev-accent)]">
                {it.count}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

// ── dropdown menu ────────────────────────────────────────────────────────────
export interface MenuOption<V> { value: V; label: string; hint?: string }

/**
 * Anchored dropdown, portaled to <body> so it escapes the panel's `overflow:auto`
 * (the old inline dropdowns were clipped by the scroll container).
 */
export function Menu<V extends string | number>({ label, icon, value, options, onChange, title, tone = 'ghost', dataId }: {
  label: React.ReactNode;
  icon?: React.ReactNode;
  value: V;
  options: MenuOption<V>[];
  onChange: (v: V) => void;
  title?: string;
  tone?: 'ghost' | 'solid' | 'accent';
  dataId?: string;
}) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number; width: number } | null>(null);
  const ref = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = () => setOpen(false);
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    window.addEventListener('resize', close);
    window.addEventListener('scroll', close, true);
    document.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('resize', close);
      window.removeEventListener('scroll', close, true);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const toggle = () => {
    if (open) { setOpen(false); return; }
    const r = ref.current?.getBoundingClientRect();
    if (r) setPos({ top: r.bottom + 4, left: r.left, width: Math.max(r.width, 148) });
    setOpen(true);
  };

  const variantCls =
    tone === 'accent' ? 'bg-[var(--dev-accent-bg)] text-[var(--dev-accent)]'
      : tone === 'solid' ? 'bg-[var(--dev-raise)] text-[var(--dev-text)]'
        : 'text-[var(--dev-text-2)] hover:bg-[var(--dev-hover)]';

  return (
    <>
      <button
        ref={ref}
        type="button"
        data-id={dataId}
        onClick={toggle}
        title={title}
        className={cn(
          'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-lg px-2 text-[11px] font-medium transition-colors',
          variantCls,
        )}
      >
        {icon}
        <span className="max-w-[92px] truncate">{label}</span>
        <ChevronDown className={cn('h-3 w-3 shrink-0 transition-transform', open ? 'rotate-180' : '')} />
      </button>
      {open && pos && createPortal(
        <>
          <button
            type="button"
            aria-label="close"
            className="fixed inset-0 z-[200] cursor-default"
            onClick={() => setOpen(false)}
          />
          <div
            data-id={dataId ? `${dataId}-menu` : undefined}
            className="fixed z-[201] overflow-hidden rounded-xl border border-[var(--dev-border)] bg-[var(--dev-surface)] py-1"
            style={{ top: pos.top, left: pos.left, minWidth: pos.width, boxShadow: 'var(--dev-shadow)' }}
          >
            {options.map((o) => {
              const on = o.value === value;
              return (
                <button
                  key={String(o.value)}
                  type="button"
                  onClick={() => { onChange(o.value); setOpen(false); }}
                  className={cn(
                    'flex w-full cursor-pointer items-center gap-2 px-2.5 py-1.5 text-left text-[12px] transition-colors',
                    on ? 'text-[var(--dev-text)]' : 'text-[var(--dev-text-2)]',
                    'hover:bg-[var(--dev-hover)]',
                  )}
                >
                  <span className="min-w-0 flex-1 truncate">{o.label}</span>
                  {o.hint ? <span className="shrink-0 text-[10px] text-[var(--dev-text-3)]">{o.hint}</span> : null}
                  {on ? <Check className="h-3.5 w-3.5 shrink-0 text-[var(--dev-accent)]" /> : <span className="w-3.5 shrink-0" />}
                </button>
              );
            })}
          </div>
        </>,
        document.body,
      )}
    </>
  );
}

// ── state blocks (loading / empty / error) ───────────────────────────────────
/**
 * One component for every non-content state. Replaces six divergent blocks that
 * variously rendered a bare grey sentence ("electronRPC not available") with no
 * explanation and no way forward. Every state now gets an icon, a title, a plain
 * hint, and — where one exists — the action that fixes it.
 */
export function StateBlock({ icon, title, hint, action, tone = 'neutral', dataId }: {
  icon: React.ReactNode;
  title: string;
  hint?: React.ReactNode;
  action?: React.ReactNode;
  tone?: 'neutral' | 'warn' | 'error';
  dataId?: string;
}) {
  const ring = {
    neutral: 'border-[var(--dev-border)] text-[var(--dev-text-3)]',
    warn: 'border-[var(--dev-warn-bg)] text-[var(--dev-warn)]',
    error: 'border-[var(--dev-live-bg)] text-[var(--dev-live)]',
  }[tone];
  return (
    <div data-id={dataId} className="flex flex-1 flex-col items-center justify-center gap-2.5 px-6 py-10 text-center">
      <div className={cn('flex h-11 w-11 items-center justify-center rounded-2xl border bg-[var(--dev-surface-2)]', ring)}>
        {icon}
      </div>
      <div className="text-[13px] font-medium text-[var(--dev-text-2)]">{title}</div>
      {hint ? <div className="max-w-[240px] text-[11px] leading-relaxed text-[var(--dev-text-3)]">{hint}</div> : null}
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}

/** Inline (non-blocking) error strip — used when content is still on screen. */
export function ErrorStrip({ icon, children, action, dataId }: {
  icon?: React.ReactNode;
  children: React.ReactNode;
  action?: React.ReactNode;
  dataId?: string;
}) {
  return (
    <div
      data-id={dataId}
      className="m-2 mb-0 flex items-start gap-2 rounded-lg border border-[var(--dev-warn-bg)] bg-[var(--dev-warn-bg)] px-2.5 py-2 text-[11px] leading-relaxed text-[var(--dev-warn)]"
    >
      {icon ? <span className="mt-0.5 shrink-0">{icon}</span> : null}
      <span className="min-w-0 flex-1 break-words">{children}</span>
      {action ? <span className="shrink-0">{action}</span> : null}
    </div>
  );
}

/** Shimmer rows for list loading. */
export function ListSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div data-id="dev-skeleton" className="py-1">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-2.5 px-3 py-2.5">
          <span className="h-1.5 w-1.5 rounded-full bg-[var(--dev-skeleton)]" />
          <div className="min-w-0 flex-1">
            <div className="h-2.5 animate-pulse rounded bg-[var(--dev-skeleton)]" style={{ width: `${58 - i * 8}%` }} />
            <div className="mt-1.5 h-2 animate-pulse rounded bg-[var(--dev-skeleton)] opacity-60" style={{ width: `${40 - i * 5}%` }} />
          </div>
        </div>
      ))}
    </div>
  );
}

// ── relative time ────────────────────────────────────────────────────────────
/** "3 秒前" / "just now", re-rendering on its own so it never goes stale. */
export function RelTime({ ts, prefix, fmt }: { ts: number | null; prefix?: string; fmt: (ms: number) => string }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);
  if (!ts) return null;
  return <span>{prefix}{fmt(Date.now() - ts)}</span>;
}
