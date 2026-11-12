// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0
//
// PromptLogModal — every prompt the user has sent to the project's agents, in
// one time-ordered list. Prompts only: no replies, no tool output. Search,
// filter by agent or period, preview attached images, copy, or drop a prompt
// back into its agent's composer.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Check, Copy, CornerDownLeft, FileText, Loader2, LocateFixed, MessageSquareText, RefreshCw, Search, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { cn, copyToClipboard } from '../../lib/utils';
import AgentAvatar from '../AgentAvatar';
import { MarkdownImg } from '../chat/history/shared/Markdown';
import { assetAbsPathToURL } from '../chat/history/lib/misc';
import {
  filterPromptLog, formatPromptClock, formatPromptDate, formatPromptDateTime, groupPromptLogByDay, mergePromptLogs,
  parsePromptContent, promptLogToText, startOfDay, type PromptLogEntry, type PromptLogRange,
} from '../../lib/promptLog';

export interface PromptLogAgent {
  id: string;
  title: string;
  agentType?: string;
  /** In the open project: its card can be focused and its composer filled. */
  inProject: boolean;
}

const PAGE = 150;
const LONG_PROMPT_CHARS = 420;
const LIGHTBOX = '[data-id="current-history-md-img-lightbox"]';

function PromptLogRow({ entry, agent, query, onFilterAgent, onReuse, onLocate }: {
  entry: PromptLogEntry;
  agent?: PromptLogAgent;
  query: string;
  onFilterAgent: (id: string) => void;
  onReuse?: (entry: PromptLogEntry) => void;
  onLocate?: (entry: PromptLogEntry) => void;
}) {
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
    <li data-id="prompt-log-row" className="group relative flex gap-3 rounded-xl px-3 py-2.5 hover:bg-white/[0.035]">
      <time data-id="prompt-log-row-time" dateTime={entry.ts} title={formatPromptDateTime(entry.time)} className="w-11 shrink-0 pt-0.5 text-right font-mono text-[12px] tabular-nums text-zinc-500">
        {formatPromptClock(entry.time)}
      </time>
      <div className="min-w-0 flex-1">
        <button
          type="button"
          data-id="prompt-log-row-agent"
          onClick={() => onFilterAgent(entry.agentId)}
          title={t('promptLogFilterAgent', { defaultValue: '只看这个 Agent' })}
          className="mb-1 inline-flex max-w-full items-center gap-1.5 rounded-md text-[11px] text-zinc-500 hover:text-zinc-200"
        >
          <span className="grid h-4 w-4 shrink-0 place-items-center overflow-hidden rounded [&>*]:!h-4 [&>*]:!w-4"><AgentAvatar agentType={agent?.agentType} title={agent?.title || entry.agentId} variant="option" /></span>
          <span className="truncate">{agent?.title || entry.agentId}</span>
          <span className="shrink-0 font-mono text-[10px] text-zinc-600">{entry.agentId}</span>
        </button>
        {text ? (
          <div data-id="prompt-log-row-text" className={cn('select-text whitespace-pre-wrap break-words text-[13px] leading-[1.6] text-zinc-200', long && !expanded && 'line-clamp-6')}>{body}</div>
        ) : null}
        {long ? (
          <button type="button" data-id="prompt-log-row-expand" onClick={() => setExpanded((value) => !value)} className="mt-1 text-[11px] text-blue-300/80 hover:text-blue-200">
            {expanded ? t('promptLogCollapse', { defaultValue: '收起' }) : t('promptLogExpand', { defaultValue: '展开全部' })}
          </button>
        ) : null}
        {images.length ? (
          <div data-id="prompt-log-row-images" className="mt-2 flex flex-wrap gap-2">
            {images.map((item, index) => (
              <span key={`${item.src}:${index}`} title={item.name} className="block overflow-hidden rounded-lg border border-white/[0.08] bg-black/30 [&>img]:!h-20 [&>img]:!max-h-20 [&>img]:!w-auto [&>img]:!max-w-[200px] [&>img]:!rounded-none">
                <MarkdownImg src={item.src} alt={item.name} />
              </span>
            ))}
          </div>
        ) : null}
        {files.length ? (
          <div data-id="prompt-log-row-files" className="mt-2 flex flex-wrap gap-1.5">
            {files.map((item, index) => (
              <a key={`${item.src}:${index}`} href={assetAbsPathToURL(item.src)} target="_blank" rel="noreferrer" className="inline-flex max-w-[260px] items-center gap-1.5 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-1 text-[11px] text-zinc-300 hover:border-white/20 hover:text-white">
                <FileText className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                <span className="truncate">{item.name}</span>
              </a>
            ))}
          </div>
        ) : null}
      </div>
      <div data-id="prompt-log-row-actions" className="absolute right-2 top-2 flex items-center gap-0.5 rounded-lg border border-white/[0.08] bg-[#1c1d22] p-0.5 opacity-0 shadow-lg transition-opacity focus-within:opacity-100 group-hover:opacity-100">
        <button type="button" data-id="prompt-log-row-copy" onClick={copy} title={t('promptLogCopy', { defaultValue: '复制' })} aria-label={t('promptLogCopy', { defaultValue: '复制' })} className={actionClass}>
          {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
        </button>
        {onReuse ? (
          <button type="button" data-id="prompt-log-row-reuse" onClick={() => onReuse(entry)} title={t('promptLogReuse', { defaultValue: '填回这个 Agent 的输入框' })} aria-label={t('promptLogReuse', { defaultValue: '填回这个 Agent 的输入框' })} className={actionClass}>
            <CornerDownLeft className="h-3.5 w-3.5" />
          </button>
        ) : null}
        {onLocate ? (
          <button type="button" data-id="prompt-log-row-locate" onClick={() => onLocate(entry)} title={t('promptLogLocate', { defaultValue: '定位到这个 Agent' })} aria-label={t('promptLogLocate', { defaultValue: '定位到这个 Agent' })} className={actionClass}>
            <LocateFixed className="h-3.5 w-3.5" />
          </button>
        ) : null}
      </div>
    </li>
  );
}

export default function PromptLogModal({ open, agents, projectName, onClose, onReuse, onLocate }: {
  open: boolean;
  agents: PromptLogAgent[];
  projectName?: string;
  onClose: () => void;
  onReuse: (agentId: string, text: string) => void;
  onLocate: (agentId: string) => void;
}) {
  const { t } = useTranslation('workspace');
  const [scope, setScope] = useState<'project' | 'all'>('project');
  const [query, setQuery] = useState('');
  const [agentId, setAgentId] = useState('');
  const [range, setRange] = useState<PromptLogRange>('all');
  const [entries, setEntries] = useState<PromptLogEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [shown, setShown] = useState(PAGE);
  const [copiedAll, setCopiedAll] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const requestRef = useRef(0);

  const agentById = useMemo(() => new Map(agents.map((agent) => [agent.id, agent])), [agents]);
  const hasOutside = agents.some((agent) => !agent.inProject);
  const scopeIds = useMemo(
    () => agents.filter((agent) => scope === 'all' || agent.inProject).map((agent) => agent.id),
    [agents, scope],
  );
  const scopeKey = scopeIds.join(',');

  const load = useCallback(async () => {
    const ids = scopeKey ? scopeKey.split(',') : [];
    const request = ++requestRef.current;
    if (!ids.length) { setEntries([]); setError(''); return; }
    setLoading(true);
    setError('');
    try {
      const { data } = await apiService.getAgentPromptLog(ids);
      if (request !== requestRef.current) return;
      setEntries(mergePromptLogs(data?.agents || {}));
    } catch (err: any) {
      if (request !== requestRef.current) return;
      setError(String(err?.response?.data?.error || err?.message || err || 'failed'));
    } finally {
      if (request === requestRef.current) setLoading(false);
    }
  }, [scopeKey]);

  useEffect(() => { if (open) void load(); }, [open, load]);
  useEffect(() => {
    if (!open) return;
    setShown(PAGE);
    const id = window.requestAnimationFrame(() => searchRef.current?.focus());
    return () => window.cancelAnimationFrame(id);
  }, [open]);
  // Back to the top (newest) whenever the filter changes what the list shows.
  useEffect(() => { setShown(PAGE); if (listRef.current) listRef.current.scrollTop = 0; }, [query, agentId, range, scope]);
  useEffect(() => { if (agentId && !scopeIds.includes(agentId)) setAgentId(''); }, [agentId, scopeIds]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        if (document.querySelector(LIGHTBOX)) return; // the image preview closes first
        event.stopPropagation();
        if (query) setQuery(''); else onClose();
      } else if (event.key === '/' && document.activeElement !== searchRef.current) {
        event.preventDefault();
        searchRef.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [open, query, onClose]);

  const inPeriod = useMemo(() => filterPromptLog(entries, { query, range }), [entries, query, range]);
  const filtered = useMemo(() => (agentId ? inPeriod.filter((entry) => entry.agentId === agentId) : inPeriod), [inPeriod, agentId]);
  const counts = useMemo(() => {
    const map = new Map<string, number>();
    for (const entry of inPeriod) map.set(entry.agentId, (map.get(entry.agentId) || 0) + 1);
    return map;
  }, [inPeriod]);
  // Agents that have prompts, busiest first; the rest are not worth a chip.
  const chips = useMemo(
    () => scopeIds.filter((id) => counts.get(id) || id === agentId).sort((a, b) => (counts.get(b) || 0) - (counts.get(a) || 0)),
    [scopeIds, counts, agentId],
  );
  const groups = useMemo(() => groupPromptLogByDay(filtered.slice(0, shown)), [filtered, shown]);

  if (!open) return null;

  const today = startOfDay(Date.now());
  const dayLabel = (day: number) => {
    if (!day) return t('promptLogNoTime', { defaultValue: '时间未知' });
    const weekday = new Date(day).toLocaleDateString(undefined, { weekday: 'short' });
    if (day === today) return `${t('promptLogToday', { defaultValue: '今天' })} · ${formatPromptDate(day)} ${weekday}`;
    if (day === today - 86_400_000) return `${t('promptLogYesterday', { defaultValue: '昨天' })} · ${formatPromptDate(day)} ${weekday}`;
    return `${formatPromptDate(day)} ${weekday}`;
  };
  const copyAll = () => {
    void copyToClipboard(promptLogToText(filtered, (id) => agentById.get(id)?.title || id));
    setCopiedAll(true);
    window.setTimeout(() => setCopiedAll(false), 1400);
  };
  const segment = (active: boolean) => cn('h-7 rounded-md px-2.5 text-[12px] transition-colors', active ? 'bg-white/[0.1] text-zinc-100' : 'text-zinc-500 hover:text-zinc-200');
  const filtering = Boolean(query || agentId || range !== 'all');

  return createPortal(
    <div data-id="prompt-log-overlay" className="fixed inset-0 z-[9999999] flex items-center justify-center bg-black/55 p-4 backdrop-blur-sm" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <div data-id="prompt-log-modal" role="dialog" aria-modal="true" aria-label={t('promptLogTitle', { defaultValue: '我的 Prompt' })} className="flex h-[min(780px,88vh)] w-full max-w-[860px] flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl shadow-black/60">
        <header data-id="prompt-log-header" className="flex h-12 shrink-0 items-center gap-2.5 border-b border-white/[0.07] px-4">
          <MessageSquareText className="h-4 w-4 shrink-0 text-blue-300" />
          <h2 className="min-w-0 truncate text-[14px] font-semibold text-white">{t('promptLogTitle', { defaultValue: '我的 Prompt' })}</h2>
          <span data-id="prompt-log-count" className="shrink-0 rounded-full bg-white/[0.06] px-2 py-0.5 font-mono text-[11px] tabular-nums text-zinc-400">
            {filtering ? `${filtered.length} / ${entries.length}` : entries.length}
          </span>
          <div className="flex-1" />
          {hasOutside ? (
            <div data-id="prompt-log-scope" role="radiogroup" className="flex items-center rounded-lg border border-white/[0.08] bg-white/[0.03] p-0.5">
              <button type="button" role="radio" aria-checked={scope === 'project'} data-id="prompt-log-scope-project" onClick={() => setScope('project')} className={segment(scope === 'project')} title={projectName}>{t('promptLogScopeProject', { defaultValue: '当前项目' })}</button>
              <button type="button" role="radio" aria-checked={scope === 'all'} data-id="prompt-log-scope-all" onClick={() => setScope('all')} className={segment(scope === 'all')}>{t('promptLogScopeAll', { defaultValue: '全部 Agent' })}</button>
            </div>
          ) : null}
          <button type="button" data-id="prompt-log-refresh" onClick={() => { void load(); }} disabled={loading} title={t('promptLogRefresh', { defaultValue: '刷新' })} aria-label={t('promptLogRefresh', { defaultValue: '刷新' })} className="grid h-7 w-7 place-items-center rounded-md text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-50">
            <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
          </button>
          <button type="button" data-id="prompt-log-close" onClick={onClose} aria-label={t('close', { ns: 'common', defaultValue: '关闭' })} className="grid h-7 w-7 place-items-center rounded-md text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200"><X className="h-4 w-4" /></button>
        </header>

        <div data-id="prompt-log-toolbar" className="shrink-0 space-y-2.5 border-b border-white/[0.07] px-4 py-3">
          <div className="flex items-center gap-2">
            <label className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-xl border border-white/[0.08] bg-[#0c0d10] px-3 transition-colors focus-within:border-blue-500/45 focus-within:ring-2 focus-within:ring-blue-500/10">
              <Search className="h-3.5 w-3.5 shrink-0 text-zinc-600" />
              <input ref={searchRef} data-id="prompt-log-search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('promptLogSearch', { defaultValue: '搜索 prompt 内容…' })} className="min-w-0 flex-1 bg-transparent text-[13px] text-zinc-200 outline-none placeholder:text-zinc-600" autoFocus spellCheck={false} />
              {query ? <button type="button" data-id="prompt-log-search-clear" onClick={() => { setQuery(''); searchRef.current?.focus(); }} className="grid h-6 w-6 place-items-center rounded-md text-zinc-600 hover:bg-white/[0.06] hover:text-zinc-300"><X className="h-3.5 w-3.5" /></button> : null}
            </label>
            <div data-id="prompt-log-range" role="radiogroup" className="flex shrink-0 items-center rounded-lg border border-white/[0.08] bg-white/[0.03] p-0.5">
              {([['all', t('promptLogRangeAll', { defaultValue: '全部' })], ['today', t('promptLogToday', { defaultValue: '今天' })], ['week', t('promptLogRangeWeek', { defaultValue: '近 7 天' })]] as Array<[PromptLogRange, string]>).map(([value, label]) => (
                <button key={value} type="button" role="radio" aria-checked={range === value} data-id={`prompt-log-range-${value}`} onClick={() => setRange(value)} className={segment(range === value)}>{label}</button>
              ))}
            </div>
          </div>
          {chips.length > 1 || agentId ? (
            <div data-id="prompt-log-agents" className="flex gap-1.5 overflow-x-auto pb-0.5">
              <button type="button" data-id="prompt-log-agent-all" aria-pressed={!agentId} onClick={() => setAgentId('')} className={cn('flex h-7 shrink-0 items-center gap-1.5 rounded-full border px-2.5 text-[12px] transition-colors', !agentId ? 'border-blue-500/40 bg-blue-500/15 text-blue-100' : 'border-white/[0.08] text-zinc-400 hover:text-zinc-200')}>
                {t('promptLogAllAgents', { defaultValue: '全部' })}<span className="font-mono text-[10px] tabular-nums opacity-70">{inPeriod.length}</span>
              </button>
              {chips.map((id) => {
                const agent = agentById.get(id);
                const active = agentId === id;
                return (
                  <button key={id} type="button" data-id="prompt-log-agent-chip" data-agent={id} aria-pressed={active} onClick={() => setAgentId(active ? '' : id)} title={id} className={cn('flex h-7 max-w-[200px] shrink-0 items-center gap-1.5 rounded-full border pl-1.5 pr-2.5 text-[12px] transition-colors', active ? 'border-blue-500/40 bg-blue-500/15 text-blue-100' : 'border-white/[0.08] text-zinc-400 hover:text-zinc-200')}>
                    <span className="grid h-4 w-4 shrink-0 place-items-center overflow-hidden rounded [&>*]:!h-4 [&>*]:!w-4"><AgentAvatar agentType={agent?.agentType} title={agent?.title || id} variant="option" /></span>
                    <span className="truncate">{agent?.title || id}</span>
                    <span className="font-mono text-[10px] tabular-nums opacity-70">{counts.get(id) || 0}</span>
                  </button>
                );
              })}
            </div>
          ) : null}
        </div>

        <div ref={listRef} data-id="prompt-log-list" className="min-h-0 flex-1 overflow-y-auto px-2 pb-3 [scrollbar-gutter:stable]">
          {error ? (
            <div data-id="prompt-log-error" role="alert" className="m-3 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-[12px] text-red-300">
              {t('promptLogLoadFailed', { defaultValue: '加载失败' })}: {error}
            </div>
          ) : null}
          {loading && !entries.length ? (
            <div data-id="prompt-log-loading" className="flex h-full items-center justify-center gap-2 text-[12px] text-zinc-500"><Loader2 className="h-4 w-4 animate-spin" />{t('promptLogLoading', { defaultValue: '正在读取 prompt 记录…' })}</div>
          ) : !filtered.length && !error ? (
            <div data-id="prompt-log-empty" className="flex h-full flex-col items-center justify-center gap-2 text-center">
              <MessageSquareText className="h-7 w-7 text-zinc-700" />
              <p className="text-[13px] text-zinc-400">{filtering ? t('promptLogNoMatch', { defaultValue: '没有符合条件的 prompt' }) : t('promptLogEmpty', { defaultValue: '还没有发送过 prompt' })}</p>
              {filtering ? <button type="button" data-id="prompt-log-reset" onClick={() => { setQuery(''); setAgentId(''); setRange('all'); }} className="text-[12px] text-blue-300 hover:text-blue-200">{t('promptLogReset', { defaultValue: '清除筛选' })}</button> : null}
            </div>
          ) : (
            <>
              {groups.map((group) => (
                <section key={group.day} data-id="prompt-log-day">
                  <h3 className="sticky top-0 z-10 flex items-center gap-2 bg-[#161618]/95 px-3 pb-1.5 pt-3 text-[11px] font-medium text-zinc-500 backdrop-blur">
                    <span>{dayLabel(group.day)}</span>
                    <span className="h-px flex-1 bg-white/[0.06]" />
                  </h3>
                  <ul>
                    {group.entries.map((entry) => {
                      const agent = agentById.get(entry.agentId);
                      return (
                        <PromptLogRow
                          key={entry.key}
                          entry={entry}
                          agent={agent}
                          query={query}
                          onFilterAgent={(id) => setAgentId((current) => (current === id ? '' : id))}
                          onReuse={agent?.inProject ? (item) => { onReuse(item.agentId, parsePromptContent(item.content).text); onClose(); } : undefined}
                          onLocate={agent?.inProject ? (item) => { onLocate(item.agentId); onClose(); } : undefined}
                        />
                      );
                    })}
                  </ul>
                </section>
              ))}
              {filtered.length > shown ? (
                <div className="flex justify-center pt-3">
                  <button type="button" data-id="prompt-log-more" onClick={() => setShown((value) => value + PAGE)} className="h-8 rounded-lg border border-white/[0.08] px-3 text-[12px] text-zinc-300 hover:bg-white/[0.05]">
                    {t('promptLogMore', { defaultValue: '更早的 {{count}} 条', count: filtered.length - shown })}
                  </button>
                </div>
              ) : null}
            </>
          )}
        </div>

        <footer data-id="prompt-log-footer" className="flex h-11 shrink-0 items-center gap-3 border-t border-white/[0.07] px-4 text-[11px] text-zinc-600">
          <span className="min-w-0 truncate">{t('promptLogHint', { defaultValue: '只列出已送达 Agent 的 prompt · 按 / 搜索 · 点图片预览' })}</span>
          <div className="flex-1" />
          <button type="button" data-id="prompt-log-copy-all" onClick={copyAll} disabled={!filtered.length} className="inline-flex h-7 shrink-0 items-center gap-1.5 rounded-lg border border-white/[0.08] px-2.5 text-[12px] text-zinc-300 hover:bg-white/[0.05] disabled:opacity-40">
            {copiedAll ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
            {t('promptLogCopyAll', { defaultValue: '复制这 {{count}} 条', count: filtered.length })}
          </button>
        </footer>
      </div>
    </div>,
    document.body,
  );
}
