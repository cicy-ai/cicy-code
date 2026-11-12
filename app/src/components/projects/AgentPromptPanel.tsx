// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0
//
// AgentPromptPanel — the "Prompt" tab of an agent card: every prompt the user
// has sent to THIS agent, newest first, with its time. Prompts only: no
// replies, no tool output. Search, filter by period, preview attached images,
// copy, or drop a prompt back into the card's composer.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Check, Copy, CornerDownLeft, FileText, Loader2, MessageSquareText, RefreshCw, Search, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { cn, copyToClipboard } from '../../lib/utils';
import { MarkdownImg } from '../chat/history/shared/Markdown';
import { assetAbsPathToURL } from '../chat/history/lib/misc';
import {
  filterPromptLog, formatPromptClock, formatPromptDate, formatPromptDateTime, groupPromptLogByDay, mergePromptLogs,
  parsePromptContent, promptLogToText, startOfDay, type PromptLogEntry, type PromptLogRange,
} from '../../lib/promptLog';

const PAGE = 100;
const LONG_PROMPT_CHARS = 420;

function PromptRow({ entry, query, onReuse }: { entry: PromptLogEntry; query: string; onReuse?: (text: string) => void }) {
  const { t } = useTranslation('workspace');
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const { text, attachments } = useMemo(() => parsePromptContent(entry.content), [entry.content]);
  const long = text.length > LONG_PROMPT_CHARS || text.split('\n').length > 8;
  const images = attachments.filter((item) => item.image);
  const files = attachments.filter((item) => !item.image);
  const words = useMemo(() => query.toLowerCase().split(/\s+/).filter(Boolean), [query]);
  // Highlight the search words inside the prompt text.
  const body = useMemo(() => {
    if (!words.length || !text) return text;
    const pattern = new RegExp(`(${words.map((word) => word.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'gi');
    return text.split(pattern).map((part, index) => (index % 2 === 1
      ? <mark key={index} className="rounded-sm bg-amber-400/25 px-0.5 text-amber-100">{part}</mark>
      : part));
  }, [text, words]);
  const copy = () => {
    void copyToClipboard(entry.content);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  };
  const actionClass = 'grid h-7 w-7 place-items-center rounded-md text-zinc-500 hover:bg-white/[0.08] hover:text-zinc-100';
  return (
    <li data-id="agent-prompt-row" className="group relative flex gap-3 rounded-xl px-3 py-2.5 hover:bg-white/[0.035]">
      <time data-id="agent-prompt-row-time" dateTime={entry.ts} title={formatPromptDateTime(entry.time)} className="w-11 shrink-0 pt-0.5 text-right font-mono text-[12px] tabular-nums text-zinc-500">
        {formatPromptClock(entry.time)}
      </time>
      <div className="min-w-0 flex-1">
        {text ? (
          <div data-id="agent-prompt-row-text" className={cn('select-text whitespace-pre-wrap break-words text-[13px] leading-[1.6] text-zinc-200', long && !expanded && 'line-clamp-6')}>{body}</div>
        ) : null}
        {long ? (
          <button type="button" data-id="agent-prompt-row-expand" onClick={() => setExpanded((value) => !value)} className="mt-1 text-[11px] text-blue-300/80 hover:text-blue-200">
            {expanded ? t('promptLogCollapse', { defaultValue: '收起' }) : t('promptLogExpand', { defaultValue: '展开全部' })}
          </button>
        ) : null}
        {images.length ? (
          <div data-id="agent-prompt-row-images" className={cn('flex flex-wrap gap-2', text && 'mt-2')}>
            {images.map((item, index) => (
              <span key={`${item.src}:${index}`} title={item.name} className="block overflow-hidden rounded-lg border border-white/[0.08] bg-black/30 [&>img]:!h-20 [&>img]:!max-h-20 [&>img]:!w-auto [&>img]:!max-w-[200px] [&>img]:!rounded-none">
                <MarkdownImg src={item.src} alt={item.name} />
              </span>
            ))}
          </div>
        ) : null}
        {files.length ? (
          <div data-id="agent-prompt-row-files" className="mt-2 flex flex-wrap gap-1.5">
            {files.map((item, index) => (
              <a key={`${item.src}:${index}`} href={assetAbsPathToURL(item.src)} target="_blank" rel="noreferrer" className="inline-flex max-w-[260px] items-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-1 text-[11px] text-zinc-300 hover:border-white/20 hover:text-white">
                <FileText className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                <span className="truncate">{item.name}</span>
              </a>
            ))}
          </div>
        ) : null}
      </div>
      <div data-id="agent-prompt-row-actions" className="absolute right-2 top-2 flex items-center gap-0.5 rounded-lg border border-white/[0.08] bg-[#1c1d22] p-0.5 opacity-0 shadow-lg transition-opacity focus-within:opacity-100 group-hover:opacity-100">
        <button type="button" data-id="agent-prompt-row-copy" onClick={copy} title={t('promptLogCopy', { defaultValue: '复制' })} aria-label={t('promptLogCopy', { defaultValue: '复制' })} className={actionClass}>
          {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
        </button>
        {onReuse ? (
          <button type="button" data-id="agent-prompt-row-reuse" onClick={() => onReuse(text)} title={t('promptLogReuse', { defaultValue: '填回输入框' })} aria-label={t('promptLogReuse', { defaultValue: '填回输入框' })} className={actionClass}>
            <CornerDownLeft className="h-3.5 w-3.5" />
          </button>
        ) : null}
      </div>
    </li>
  );
}

export default function AgentPromptPanel({ agentId, busy = false, onReuse }: {
  agentId: string;
  /** The agent is working; the list reloads when this flips, i.e. after each prompt lands. */
  busy?: boolean;
  onReuse?: (text: string) => void;
}) {
  const { t } = useTranslation('workspace');
  const [query, setQuery] = useState('');
  const [range, setRange] = useState<PromptLogRange>('all');
  const [entries, setEntries] = useState<PromptLogEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [shown, setShown] = useState(PAGE);
  const [copiedAll, setCopiedAll] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  const requestRef = useRef(0);

  const load = useCallback(async () => {
    const request = ++requestRef.current;
    setLoading(true);
    setError('');
    try {
      const { data } = await apiService.getAgentPromptLog([agentId]);
      if (request !== requestRef.current) return;
      setEntries(mergePromptLogs({ [agentId]: data?.agents?.[agentId] || [] }));
    } catch (err: any) {
      if (request !== requestRef.current) return;
      setError(String(err?.response?.data?.error || err?.message || err || 'failed'));
    } finally {
      if (request === requestRef.current) setLoading(false);
    }
  }, [agentId]);

  useEffect(() => { void load(); }, [load, busy]);
  // Back to the top (newest) whenever the filter changes what the list shows.
  useEffect(() => { setShown(PAGE); if (listRef.current) listRef.current.scrollTop = 0; }, [query, range]);

  const filtered = useMemo(() => filterPromptLog(entries, { query, range }), [entries, query, range]);
  const groups = useMemo(() => groupPromptLogByDay(filtered.slice(0, shown)), [filtered, shown]);
  const filtering = Boolean(query || range !== 'all');

  const today = startOfDay(Date.now());
  const dayLabel = (day: number) => {
    if (!day) return t('promptLogNoTime', { defaultValue: '时间未知' });
    const weekday = new Date(day).toLocaleDateString(undefined, { weekday: 'short' });
    if (day === today) return `${t('promptLogToday', { defaultValue: '今天' })} · ${formatPromptDate(day)} ${weekday}`;
    if (day === today - 86_400_000) return `${t('promptLogYesterday', { defaultValue: '昨天' })} · ${formatPromptDate(day)} ${weekday}`;
    return `${formatPromptDate(day)} ${weekday}`;
  };
  const copyAll = () => {
    void copyToClipboard(promptLogToText(filtered, () => agentId));
    setCopiedAll(true);
    window.setTimeout(() => setCopiedAll(false), 1400);
  };
  const segment = (active: boolean) => cn('h-6 rounded-md px-2 text-[11px] transition-colors', active ? 'bg-white/[0.1] text-zinc-100' : 'text-zinc-500 hover:text-zinc-200');
  const iconButton = 'grid h-7 w-7 shrink-0 place-items-center rounded-md text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-40';

  return (
    <div data-id={`agent-prompt-panel-${agentId}`} className="flex h-full min-h-0 flex-col">
      <div data-id="agent-prompt-toolbar" className="flex shrink-0 flex-wrap items-center gap-2 border-b border-white/[0.06] px-4 py-2">
        <label className="flex h-8 min-w-[140px] flex-1 items-center gap-2 rounded-lg border border-white/[0.08] bg-[#0c0d10] px-2.5 transition-colors focus-within:border-blue-500/45">
          <Search className="h-3.5 w-3.5 shrink-0 text-zinc-600" />
          <input data-id="agent-prompt-search" value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === 'Escape' && query) { event.stopPropagation(); setQuery(''); } }} placeholder={t('promptLogSearch', { defaultValue: '搜索 prompt 内容…' })} className="min-w-0 flex-1 bg-transparent text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600" spellCheck={false} />
          {query ? <button type="button" data-id="agent-prompt-search-clear" onClick={() => setQuery('')} className="grid h-5 w-5 place-items-center rounded text-zinc-600 hover:bg-white/[0.06] hover:text-zinc-300"><X className="h-3 w-3" /></button> : null}
        </label>
        <div data-id="agent-prompt-range" role="radiogroup" className="flex shrink-0 items-center rounded-lg border border-white/[0.08] bg-white/[0.03] p-0.5">
          {([['all', t('promptLogRangeAll', { defaultValue: '全部' })], ['today', t('promptLogToday', { defaultValue: '今天' })], ['week', t('promptLogRangeWeek', { defaultValue: '近 7 天' })]] as Array<[PromptLogRange, string]>).map(([value, label]) => (
            <button key={value} type="button" role="radio" aria-checked={range === value} data-id={`agent-prompt-range-${value}`} onClick={() => setRange(value)} className={segment(range === value)}>{label}</button>
          ))}
        </div>
        <span data-id="agent-prompt-count" className="shrink-0 font-mono text-[11px] tabular-nums text-zinc-500">{filtering ? `${filtered.length} / ${entries.length}` : entries.length}</span>
        <button type="button" data-id="agent-prompt-copy-all" onClick={copyAll} disabled={!filtered.length} title={t('promptLogCopyAll', { defaultValue: '复制这 {{count}} 条', count: filtered.length })} aria-label={t('promptLogCopyAll', { defaultValue: '复制这 {{count}} 条', count: filtered.length })} className={iconButton}>
          {copiedAll ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
        </button>
        <button type="button" data-id="agent-prompt-refresh" onClick={() => { void load(); }} disabled={loading} title={t('promptLogRefresh', { defaultValue: '刷新' })} aria-label={t('promptLogRefresh', { defaultValue: '刷新' })} className={iconButton}>
          <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
        </button>
      </div>

      <div ref={listRef} data-id="agent-prompt-list" onWheel={(event) => event.stopPropagation()} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 pb-3 [scrollbar-gutter:stable]">
        {error ? (
          <div data-id="agent-prompt-error" role="alert" className="m-3 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-[12px] text-red-300">
            {t('promptLogLoadFailed', { defaultValue: '加载失败' })}: {error}
          </div>
        ) : null}
        {loading && !entries.length ? (
          <div data-id="agent-prompt-loading" className="flex h-full items-center justify-center gap-2 text-[12px] text-zinc-500"><Loader2 className="h-4 w-4 animate-spin" />{t('promptLogLoading', { defaultValue: '正在读取 prompt 记录…' })}</div>
        ) : !filtered.length && !error ? (
          <div data-id="agent-prompt-empty" className="flex h-full flex-col items-center justify-center gap-2 text-center">
            <MessageSquareText className="h-7 w-7 text-zinc-700" />
            <p className="text-[13px] text-zinc-400">{filtering ? t('promptLogNoMatch', { defaultValue: '没有符合条件的 prompt' }) : t('promptLogEmpty', { defaultValue: '还没有发送过 prompt' })}</p>
            {filtering ? <button type="button" data-id="agent-prompt-reset" onClick={() => { setQuery(''); setRange('all'); }} className="text-[12px] text-blue-300 hover:text-blue-200">{t('promptLogReset', { defaultValue: '清除筛选' })}</button> : null}
          </div>
        ) : (
          <>
            {groups.map((group) => (
              <section key={group.day} data-id="agent-prompt-day">
                <h3 className="sticky top-0 z-10 flex items-center gap-2 bg-[#0b0b0d]/95 px-3 pb-1.5 pt-3 text-[11px] font-medium text-zinc-500 backdrop-blur">
                  <span>{dayLabel(group.day)}</span>
                  <span className="h-px flex-1 bg-white/[0.06]" />
                </h3>
                <ul>
                  {group.entries.map((entry) => <PromptRow key={entry.key} entry={entry} query={query} onReuse={onReuse} />)}
                </ul>
              </section>
            ))}
            {filtered.length > shown ? (
              <div className="flex justify-center pt-3">
                <button type="button" data-id="agent-prompt-more" onClick={() => setShown((value) => value + PAGE)} className="h-8 rounded-lg border border-white/[0.08] px-3 text-[12px] text-zinc-300 hover:bg-white/[0.05]">
                  {t('promptLogMore', { defaultValue: '更早的 {{count}} 条', count: filtered.length - shown })}
                </button>
              </div>
            ) : null}
          </>
        )}
      </div>
    </div>
  );
}
