// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import { ChevronDown, ChevronRight, AlertTriangle, Square, RotateCcw, Ban, Sparkles, Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { EnvironmentContextData } from '../types';

export function EnvironmentContextCard({ context }: { context: EnvironmentContextData }) {
  const items = [
    { key: 'cwd', label: 'cwd', value: context.cwd },
    { key: 'shell', label: 'shell', value: context.shell },
    { key: 'current_date', label: 'date', value: context.current_date },
    { key: 'timezone', label: 'tz', value: context.timezone },
  ].filter((item) => String(item.value || '').trim());
  return (
    <div data-id="current-history-view-env-context" className="w-full max-w-[95%] rounded-2xl rounded-br-sm border border-sky-300/[0.10] bg-sky-400/[0.075] px-3 py-2.5 shadow-[0_8px_24px_rgba(0,0,0,0.16)]">
      <div data-id="current-history-view-env-context-label" className="mb-2 text-[11px] uppercase tracking-[0.08em] text-sky-200/55">Environment</div>
      <div data-id="current-history-view-env-context-rows" className="space-y-1.5">
        {items.map((item) => (
          <div key={item.key} data-id={`current-history-view-env-context-row-${item.key}`} className="flex flex-col gap-1">
            <div data-id={`current-history-view-env-context-row-${item.key}-label`} className="text-[11px] text-sky-200/60">{item.label}</div>
            <div data-id={`current-history-view-env-context-row-${item.key}-value`} className="rounded-md border border-sky-200/[0.08] bg-black/[0.12] px-2 py-1 font-mono text-xs leading-relaxed text-sky-50/85 break-all">
              {item.value}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// Harness-injected system notices (system-reminders, task notifications, date
// changes). Rendered as a compact, collapsed-by-default chip so the repeated
// ones don't read as duplicated AI replies.
export function SystemNoticeCard({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  // Collapsed: a tiny centered "system" pill — no preview text, so the many
  // repeated reminders read as subtle separators and never clutter the
  // conversation. Click to expand the full text.
  return (
    <div data-id="current-history-system-notice" className="flex flex-col items-center">
      <button
        type="button"
        data-id="current-history-system-notice-toggle"
        onClick={() => setOpen((o) => !o)}
        className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] uppercase tracking-wide text-zinc-700 transition-colors hover:bg-white/[0.04] hover:text-zinc-400"
      >
        {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        system
      </button>
      {open ? (
        <div data-id="current-history-system-notice-body" className="mt-1 w-full whitespace-pre-wrap rounded-md border border-white/[0.05] bg-white/[0.02] px-2.5 py-1.5 text-[11px] leading-relaxed text-zinc-500">
          {text}
        </div>
      ) : null}
    </div>
  );
}

// CompactionMarker renders /compact as a STATE TRANSITION in the timeline, not a
// Q&A: `live` is the in-progress divider ("压缩中…", painted the instant /compact
// is sent); the default is the landed ✨已压缩 divider whose summary folds open.
export function CompactionMarker({ summary, live }: { summary?: string; live?: boolean }) {
  const { t } = useTranslation('chat');
  const [open, setOpen] = useState(false);
  if (live) {
    return (
      <div data-id="current-history-compaction-marker" data-compacting="1" className="my-3 flex items-center gap-2 text-[11px] text-amber-200/70">
        <span className="h-px flex-1 bg-white/[0.07]" />
        <span data-id="current-history-compaction-marker-label" className="inline-flex items-center gap-1.5 whitespace-nowrap">
          <Loader2 className="h-3 w-3 animate-spin" />
          {t('compacting', { defaultValue: '压缩中…' })}
        </span>
        <span className="h-px flex-1 bg-white/[0.07]" />
      </div>
    );
  }
  return (
    <div data-id="current-history-compaction-marker" className="my-3 flex flex-col items-center">
      <button
        type="button"
        data-id="current-history-compaction-marker-toggle"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-2 text-[11px] text-zinc-500 transition-colors hover:text-zinc-300"
      >
        <span className="h-px flex-1 bg-white/[0.07]" />
        <span data-id="current-history-compaction-marker-label" className="inline-flex items-center gap-1.5 whitespace-nowrap">
          <Sparkles className="h-3 w-3" />
          {open ? t('compactionMarkerOpen', { defaultValue: '已压缩 · 收起摘要' }) : t('compactionMarker', { defaultValue: '已压缩 · 上文已归纳（点开看摘要）' })}
          {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        </span>
        <span className="h-px flex-1 bg-white/[0.07]" />
      </button>
      {open && summary ? (
        <div data-id="current-history-compaction-marker-body" className="mt-2 w-full whitespace-pre-wrap rounded-md border border-white/[0.05] bg-white/[0.02] px-3 py-2 text-xs leading-relaxed text-zinc-400">
          {summary}
        </div>
      ) : null}
    </div>
  );
}

// OutcomeNoticeCard renders a cicy "turn produced no reply" record: the user
// cancelled the turn (grey ⏹) or the gateway failed after auto-retry (red ⚠).
// Unlike the generic SystemNoticeCard it is NOT folded away — the whole point is
// that the user SEES that their message wasn't silently dropped. On the latest
// turn it offers 重试 to re-run that same prompt.
export function OutcomeNoticeCard({
  text,
  outcome,
  detail,
  canRetry,
  retrying,
  onRetry,
}: {
  text: string;
  outcome: string;
  detail?: string;
  canRetry: boolean;
  retrying: boolean;
  onRetry: () => void;
}) {
  // Left-aligned, in the assistant body column (avatar sits to its left): reads as
  // a normal assistant output saying the turn failed / was stopped, with 重试 on the
  // latest turn. Subtle red for failures, grey for cancellations.
  // blocked(出站审计拦截)与 error 一样用红色,但图标用 Ban 表示"被拦下",且不给重试。
  const { t } = useTranslation('chat');
  const isBlocked = outcome === 'blocked';
  const isError = outcome === 'error';
  const isRed = isError || isBlocked;
  // The outcome label is i18n'd by KIND in the UI — the backend marker text
  // (干净中文 e.g. 「已停止生成」) is only the detection signal / wire text; `text`
  // stays the fallback so any unknown kind still shows something readable.
  const label = isBlocked ? t('outcomeBlocked', { defaultValue: '已拦截' })
    : isError ? t('outcomeError', { defaultValue: '生成失败' })
    : outcome === 'cancelled' ? t('outcomeCancelled', { defaultValue: '已停止生成' })
    : text;
  return (
    <div data-id="current-history-outcome-notice" data-outcome={outcome} className="flex flex-col items-start gap-1.5">
      <div
        data-id="current-history-outcome-notice-chip"
        className={`flex items-start gap-1.5 text-sm leading-[1.7] ${isRed ? 'text-rose-300/85' : 'text-zinc-400'}`}
      >
        {isBlocked ? <Ban className="mt-[3px] h-3.5 w-3.5 shrink-0" /> : isError ? <AlertTriangle className="mt-[3px] h-3.5 w-3.5 shrink-0" /> : <Square className="mt-[3px] h-3.5 w-3.5 shrink-0" />}
        <span data-id="current-history-outcome-notice-label" className="break-all">{label}</span>
      </div>
      {detail && detail !== text ? (
        // 具体原因(blocked:命中规则/事件ID;像余额不足卡那样把原因显出来)。缩进对齐到 label 下。
        <div
          data-id="current-history-outcome-notice-detail"
          className={`whitespace-pre-wrap break-words pl-[20px] text-xs leading-relaxed ${isRed ? 'text-rose-200/70' : 'text-zinc-500'}`}
        >
          {detail}
        </div>
      ) : null}
      {canRetry ? (
        <button
          type="button"
          data-id="current-history-outcome-notice-retry"
          onClick={onRetry}
          disabled={retrying}
          className={`inline-flex shrink-0 items-center gap-1 rounded-md border px-2 py-0.5 text-xs transition-colors ${
            isRed ? 'border-rose-500/25 text-rose-200/90 hover:bg-rose-500/10' : 'border-white/10 text-zinc-300 hover:bg-white/[0.06]'
          } disabled:opacity-60`}
        >
          <RotateCcw className={`h-3 w-3 ${retrying ? 'animate-spin' : ''}`} />
          {retrying ? t('retrying', { defaultValue: '重试中…' }) : t('retry', { defaultValue: '重试' })}
        </button>
      ) : null}
    </div>
  );
}

export function PendingThinkingPlaceholder() {
  return (
    <div data-id="current-history-view-pending-placeholder" className="flex items-center gap-2 py-1 pl-2 pr-0.5 text-sm text-amber-100/65">
      <div data-id="current-history-view-pending-placeholder-dots" className="flex items-center gap-1">
        <span data-id="current-history-view-pending-placeholder-dot-1" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/70 [animation-delay:0ms]" />
        <span data-id="current-history-view-pending-placeholder-dot-2" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/55 [animation-delay:180ms]" />
        <span data-id="current-history-view-pending-placeholder-dot-3" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300/40 [animation-delay:360ms]" />
      </div>
    </div>
  );
}
