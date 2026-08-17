// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react';
import { ArrowDown, ArrowRight, Atom, Check, FileText, FolderKanban, History, Loader2, Maximize2, Minus, MoreHorizontal, Paperclip, Pencil, Pin, PinOff, Plus, Search, SendHorizontal, Square, SquareTerminal, Trash2, UserPlus, Users, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { sendToAgent } from '../../services/agentSend';
import { cn, copyToClipboard } from '../../lib/utils';
import type { AgentLiveMetrics } from '../../lib/agentMetrics';
import { metricsFromCurrentReply } from '../../lib/agentMetrics';
import { ModelTag } from '../../lib/modelTag';
import { chatAttachmentMarkdown, replAttachmentMarkdown } from '../../lib/attachmentMarkdown';
import { appendPromptHistory, canNavigatePromptHistory, readPromptHistory } from '../../lib/promptHistory';
import { AppModal, useDialogs } from '../ui/Modal';
import AgentAvatar from '../AgentAvatar';
import { MarkdownBlock, MarkdownImg } from '../chat/history/shared/Markdown';
import { toolHeadline } from '../chat/history/lib/toolFormat';
import { isTechnicalTransportFailureText } from '../chat/history/lib/normalizeItem';
import TerminalView from '../terminal/TerminalView';

const AgentDocRoleEditor = lazy(() => import('../layout/AgentDocRoleEditor'));

export interface ProjectAgent {
  paneId: string;
  title: string;
  agentType?: string;
  status?: string;
  defaultModel?: string;
  workspace?: string;
  machineLabel?: string;
  ttydSrc?: string;
  isApiOnly?: boolean;
}

interface AgentProject {
  id: number | string;
  api_id: number | string;
  name: string;
  description?: string;
  pane_ids: string[];
  pane_count: number;
  builtin?: boolean;
  pinned?: boolean;
}

interface ProjectAgentLayout {
  x: number;
  y: number;
  z: number;
  width: number;
  height: number;
}

interface ProjectAttachment {
  id: string;
  name: string;
  size: number;
  isImage: boolean;
  mediaType?: 'image' | 'video' | 'audio';
  previewURL?: string;
  fileRef?: string;
  status: 'uploading' | 'done' | 'error';
  progress: number;
}

interface QueuedAgentMessage {
  id: string;
  display: string;
  payload: string;
  attachments: ProjectAttachment[];
}

const DEFAULT_PROJECT_ID = 'default';
const PROJECT_VIEW_CACHE_PREFIX = 'cicy_project_view:';
const PROJECT_AGENT_QUEUE_KEY = 'cicy_project_agent_queue:v1';
const shortPaneId = (value: string) => String(value || '').replace(/:.*$/, '');

const readProjectAgentQueue = (): Record<string, QueuedAgentMessage[]> => {
  try {
    const value = JSON.parse(localStorage.getItem(PROJECT_AGENT_QUEUE_KEY) || '{}');
    return value && typeof value === 'object' ? value : {};
  } catch { return {}; }
};
const previewableMarkdown = (value: unknown) => String(value || '').replace(/\(file:\/\/(\/?[^)]+)\)/g, (_match, path: string) => `(/${path.replace(/^\/+/, '')})`);
const questionWithoutUploadedAttachments = (value: unknown) => String(value || '')
  .replace(/!?\[[^\]]*\]\((?:file:\/\/)?\/?[^)\n]*\/cicy-ai\/assets\/[^)\n]+\)/gi, '')
  .replace(/!?\[[^\]]*\]\(\/?assets\/files\/[^)\n]+\)/gi, '')
  .replace(/\n{3,}/g, '\n\n')
  .trim();
const uploadedAttachmentsFromQuestion = (value: unknown) => String(value || '').match(
  /!?\[[^\]]*\]\((?:file:\/\/)?\/?[^)\n]*(?:\/cicy-ai\/assets\/|\/assets\/files\/)[^)\n]+\)/gi,
) || [];
const decodeJSString = (literal: string) => {
  const value = String(literal || '');
  if (value.startsWith('"')) {
    try { return JSON.parse(value); } catch { return value.slice(1, -1); }
  }
  return value.slice(1, -1)
    .replace(/\\n/g, '\n')
    .replace(/\\r/g, '\r')
    .replace(/\\t/g, '\t')
    .replace(/\\([\\'"`])/g, '$1');
};
const codexExecCommands = (input: unknown) => {
  const source = String(input || '');
  const commands: string[] = [];
  const pattern = /tools\.exec_command\s*\(\s*\{[\s\S]*?\bcmd\s*:\s*("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`)/g;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(source))) commands.push(decodeJSString(match[1]).trim());
  return commands.filter(Boolean);
};
const projectIdFromURL = () => {
  const match = window.location.hash.match(/^#\/project\/([^/?#]+)/);
  return match ? decodeURIComponent(match[1]) : DEFAULT_PROJECT_ID;
};

interface ProjectViewCache {
  zoom: number;
  pan: { x: number; y: number };
  layouts: Record<string, ProjectAgentLayout>;
}

const projectViewCacheKey = (projectId: number | string) => `${PROJECT_VIEW_CACHE_PREFIX}${projectId}`;
const readProjectViewCache = (projectId: number | string): ProjectViewCache | null => {
  try {
    const value = JSON.parse(localStorage.getItem(projectViewCacheKey(projectId)) || 'null');
    if (!value || typeof value !== 'object') return null;
    return {
      zoom: Math.min(2, Math.max(0.35, Number(value.zoom) || 1)),
      pan: { x: Number(value.pan?.x) || 0, y: Number(value.pan?.y) || 0 },
      layouts: value.layouts && typeof value.layouts === 'object' ? value.layouts : {},
    };
  } catch { return null; }
};
const writeProjectViewCache = (projectId: number | string, patch: Partial<ProjectViewCache>) => {
  const current = readProjectViewCache(projectId) || { zoom: 1, pan: { x: 60, y: 60 }, layouts: {} };
  try { localStorage.setItem(projectViewCacheKey(projectId), JSON.stringify({ ...current, ...patch })); } catch {}
};

const fmtCost = (cost: number) =>
  cost <= 0 ? '$0'
    : cost >= 100 ? `$${Math.round(cost)}`
      : cost >= 1 ? `$${cost.toFixed(2)}`
        : cost >= 0.05 ? `$${cost.toFixed(2)}`
          : cost >= 0.001 ? `$${cost.toFixed(3)}`
            : `$${cost.toFixed(4)}`;

const fmtElapsed = (elapsedMs: number) => {
  const seconds = Math.max(0, Math.floor(elapsedMs / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
};

function CtxRing({ pct }: { pct: number }) {
  const radius = 4.5;
  const circumference = 2 * Math.PI * radius;
  const color = pct > 80 ? '#b91c1c' : pct > 50 ? '#ca8a04' : '#71717a';
  return (
    <svg data-id="project-context-ring" width="12" height="12" viewBox="0 0 12 12" className="-rotate-90 shrink-0">
      <circle cx="6" cy="6" r={radius} fill="none" stroke="rgba(255,255,255,0.10)" strokeWidth="2.5" />
      <circle cx="6" cy="6" r={radius} fill="none" stroke={color} strokeWidth="2.5" strokeDasharray={`${Math.max(0.5, (pct / 100) * circumference)} ${circumference}`} strokeLinecap="round" />
    </svg>
  );
}

function ProjectAgentCard({ agent, metrics, latest, reply, optimisticQuestion, terminalOpen, working, teamId, selected, removable, footer, width, height, onSelect, onRemove, onOpenHistory, onToggleTerminal, onResizePointerDown, onResizePointerMove, onResizePointerUp }: {
  agent: ProjectAgent;
  metrics?: AgentLiveMetrics;
  latest?: any;
  reply?: any;
  optimisticQuestion?: string;
  terminalOpen?: boolean;
  working: boolean;
  teamId?: string;
  selected: boolean;
  removable: boolean;
  footer?: ReactNode;
  width: number;
  height: number;
  onSelect: () => void;
  onRemove: () => void;
  onOpenHistory: () => void;
  onToggleTerminal: () => void;
  onResizePointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizePointerMove: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizePointerUp: (event: ReactPointerEvent<HTMLDivElement>) => void;
}) {
  const { t } = useTranslation('workspace');
  const [menuOpen, setMenuOpen] = useState(false);
  const [activeBodyTab, setActiveBodyTab] = useState<'history' | 'terminal' | 'role'>('history');
  const [identityCopied, setIdentityCopied] = useState(false);
  const [showScrollToBottom, setShowScrollToBottom] = useState(false);
  const [clockNow, setClockNow] = useState(Date.now());
  const localTurnStartRef = useRef({ question: '', startedAt: 0 });
  const menuRef = useRef<HTMLDivElement>(null);
  const bodyScrollRef = useRef<HTMLDivElement>(null);
  const loadingRef = useRef<HTMLDivElement>(null);
  const loadingVisibleRef = useRef(false);
  const loadingDetachedRef = useRef(false);
  const status = String(agent.status || 'idle').toLowerCase();
  const unhealthy = /failed|error|offline|stopped/.test(status);
  const busy = /running|working|thinking|streaming/.test(status);
  const identity = teamId ? `${teamId}.${shortPaneId(agent.paneId)}` : shortPaneId(agent.paneId);
  const replyItems = Array.isArray(reply?.items) ? reply.items : [];
  const toolGroupsByLastIndex = new Map<number, number>();
  let currentToolGroup: number[] = [];
  const finishToolGroup = () => {
    if (currentToolGroup.length) toolGroupsByLastIndex.set(currentToolGroup[currentToolGroup.length - 1], currentToolGroup.length);
    currentToolGroup = [];
  };
  replyItems.forEach((item: any, index: number) => {
    if (String(item?.type || '') === 'tool_use') {
      if (String(item?.name || '').toLowerCase() !== 'wait') currentToolGroup.push(index);
      return;
    }
    finishToolGroup();
  });
  finishToolGroup();
  const latestQuestion = String(latest?.latest_question || '');
  const replyQuestion = String(reply?.question || '');
  const latestUpdatedAt = Date.parse(String(latest?.updated_at || '')) || 0;
  const replyUpdatedAt = Date.parse(String(reply?.updated_at || '')) || 0;
  const freshestQuestion = latestQuestion && latestUpdatedAt >= replyUpdatedAt ? latestQuestion : replyQuestion || latestQuestion;
  const rawQuestion = String(optimisticQuestion || freshestQuestion);
  const visibleQuestion = questionWithoutUploadedAttachments(rawQuestion);
  const visibleQuestionAttachments = uploadedAttachmentsFromQuestion(rawQuestion);
  const replyStatus = String(reply?.status || latest?.status || status).toLowerCase();
  const completed = /completed|complete|done/.test(replyStatus);
  const serverStartedAt = Date.parse(String(reply?.started_at || latest?.started_at || '')) || 0;
  if (serverStartedAt && rawQuestion) localTurnStartRef.current = { question: rawQuestion, startedAt: serverStartedAt };
  else if (working && rawQuestion && localTurnStartRef.current.question !== rawQuestion) localTurnStartRef.current = { question: rawQuestion, startedAt: Date.now() };
  const startedAt = serverStartedAt || (localTurnStartRef.current.question === rawQuestion ? localTurnStartRef.current.startedAt : 0);
  const finishedAt = Date.parse(String(reply?.updated_at || latest?.updated_at || '')) || 0;
  const elapsedMs = startedAt ? Math.max(0, (working ? clockNow : finishedAt || clockNow) - startedAt) : 0;

  useEffect(() => {
    if (!working || !startedAt) return;
    setClockNow(Date.now());
    const timer = window.setInterval(() => setClockNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [working, startedAt]);

  useEffect(() => {
    if (!menuOpen) return;
    const close = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setMenuOpen(false);
    };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [menuOpen]);

  const updateScrollToBottomButton = useCallback(() => {
    const node = bodyScrollRef.current;
    if (!node) return;
    const overflow = node.scrollHeight > node.clientHeight + 2;
    const atBottom = node.scrollHeight - node.scrollTop - node.clientHeight < 8;
    setShowScrollToBottom(overflow && !atBottom);
  }, []);

  const updateLoadingVisibility = useCallback(() => {
    const node = bodyScrollRef.current;
    const loading = loadingRef.current;
    if (!node || !loading) {
      loadingVisibleRef.current = false;
      return;
    }
    const viewport = node.getBoundingClientRect();
    const marker = loading.getBoundingClientRect();
    const visible = marker.bottom > viewport.top && marker.top < viewport.bottom;
    loadingVisibleRef.current = visible;
  }, []);

  useEffect(() => {
    const node = bodyScrollRef.current;
    if (!node) return;
    const observer = new ResizeObserver(updateScrollToBottomButton);
    observer.observe(node);
    if (node.firstElementChild) observer.observe(node.firstElementChild);
    updateScrollToBottomButton();
    return () => observer.disconnect();
  }, [reply, latest, updateScrollToBottomButton]);

  useEffect(() => {
    const node = bodyScrollRef.current;
    const content = node?.firstElementChild;
    if (!node || !content || !working) {
      loadingVisibleRef.current = false;
      loadingDetachedRef.current = false;
      return;
    }
    loadingDetachedRef.current = false;
    updateLoadingVisibility();
    if (loadingVisibleRef.current) node.scrollTop = node.scrollHeight;
    const observer = new ResizeObserver(() => {
      if (loadingVisibleRef.current && !loadingDetachedRef.current) node.scrollTop = node.scrollHeight;
      updateLoadingVisibility();
    });
    observer.observe(content);
    return () => observer.disconnect();
  }, [working, rawQuestion, updateLoadingVisibility]);

  const toggleMenu = (event: ReactMouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    setMenuOpen((value) => !value);
  };

  const selectBodyTab = (tab: 'history' | 'terminal' | 'role') => {
    if (tab === 'terminal' && !terminalOpen) onToggleTerminal();
    if (tab !== 'terminal' && terminalOpen) onToggleTerminal();
    setActiveBodyTab(tab);
  };

  return (
    <article
      data-id={`project-agent-card-${shortPaneId(agent.paneId)}`}
      aria-pressed={selected}
      onClick={onSelect}
      style={{ width, height }}
      className={cn(
        'relative flex min-h-[240px] min-w-[260px] cursor-pointer flex-col rounded-2xl border bg-[#111216] shadow-[0_12px_30px_rgba(0,0,0,0.28)] transition-[border-color,box-shadow] hover:border-white/20',
        selected ? 'border-blue-500 ring-1 ring-blue-500/60' : 'border-white/[0.08]',
      )}
    >
      <div data-id="project-agent-card-body" className="flex min-h-0 flex-1 flex-col overflow-hidden px-5 pb-4 pt-5">
      <div data-id="project-agent-card-header" className="flex items-start gap-3">
        <div data-id="project-agent-card-heading" className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            <h3 data-id="project-agent-card-title" className="truncate text-[18px] font-semibold tracking-[-0.01em] text-zinc-100">{agent.title || agent.paneId}</h3>
            {agent.agentType ? <span data-id="project-agent-card-agent-type" className="shrink-0 font-mono text-[12px] text-zinc-500">{agent.agentType}</span> : null}
            {metrics?.model ? <ModelTag model={metrics.model} className="shrink-0" /> : null}
          </div>
        </div>
        <div data-id="project-agent-card-header-actions" className="flex shrink-0 items-center gap-0.5">
        <div data-id="project-agent-card-menu-wrap" ref={menuRef} className="relative order-2">
          <button
            type="button"
            data-id={`project-agent-card-menu-${shortPaneId(agent.paneId)}`}
            onClick={toggleMenu}
            className="grid h-8 w-8 place-items-center rounded-lg text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
            title={t('projectMore')}
          >
            <MoreHorizontal className="h-4 w-4" />
          </button>
          {menuOpen ? (
            <div data-id="project-agent-card-menu" className="absolute right-0 top-9 z-20 min-w-[150px] overflow-hidden rounded-xl border border-white/10 bg-[#1a1b20] p-1 shadow-2xl">
              {removable ? (
                <button
                  type="button"
                  data-id="project-agent-card-remove"
                  onClick={(event) => { event.stopPropagation(); setMenuOpen(false); onRemove(); }}
                  className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[12px] text-red-300 hover:bg-red-500/10"
                >
                  <X className="h-3.5 w-3.5" />{t('projectRemoveAgent')}
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
        </div>
      </div>

      <div data-id="project-agent-card-metrics" className="mt-2.5 flex h-8 min-w-0 items-center gap-2 border-b border-white/[0.08] pb-2.5 font-mono text-[13px] text-zinc-500">
        <span className={cn('h-2.5 w-2.5 shrink-0 rounded-full', unhealthy ? 'bg-red-400' : busy || metrics?.working ? 'bg-amber-500' : metrics ? 'bg-emerald-700' : 'bg-zinc-700')} title={status} />
        <button
          type="button"
          data-id="project-agent-card-identity"
          onClick={(event) => {
            event.stopPropagation();
            void copyToClipboard(identity).then((copied) => {
              if (!copied) return;
              setIdentityCopied(true);
              window.setTimeout(() => setIdentityCopied(false), 1500);
            });
          }}
          className={cn('min-w-0 truncate hover:text-zinc-300', identityCopied ? 'text-emerald-400' : 'text-zinc-500')}
        >
          {identityCopied ? t('copied', { defaultValue: '已复制' }) : identity}
        </button>
        {metrics && metrics.ctx > 0 ? (
          <span data-id="project-agent-card-context" className="flex shrink-0 items-center" title={`Context ${metrics.ctx}% / ${metrics.ctxK}k`}>
            <CtxRing pct={metrics.ctx} />
          </span>
        ) : null}
        {metrics && metrics.cost > 0 ? <span data-id="project-agent-card-cost" className="shrink-0 text-sky-500">{fmtCost(metrics.cost)}</span> : null}
      </div>
      <div data-id={`project-agent-card-tabs-${shortPaneId(agent.paneId)}`} role="tablist" className="flex h-9 shrink-0 items-end gap-5 border-b border-white/[0.08]">
        {([
          ['history', '会话'],
          ...(String(agent.agentType || '').toLowerCase() === 'cicy' ? [] : [['terminal', 'Terminal']]),
          ['role', '角色'],
        ] as Array<['history' | 'terminal' | 'role', string]>).map(([tab, label]) => {
          const unavailable = tab === 'terminal' && (!agent.ttydSrc || agent.isApiOnly);
          return (
            <button
              key={tab}
              type="button"
              role="tab"
              aria-selected={activeBodyTab === tab}
              disabled={unavailable}
              data-id={`project-agent-card-tab-${tab}-${shortPaneId(agent.paneId)}`}
              onClick={(event) => { event.stopPropagation(); selectBodyTab(tab); }}
              className={cn('relative h-9 text-[12px] font-medium transition-colors after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:rounded-full', activeBodyTab === tab ? 'text-zinc-100 after:bg-blue-500' : 'text-zinc-500 after:bg-transparent hover:text-zinc-300', unavailable && 'cursor-not-allowed opacity-30 hover:text-zinc-500')}
            >
              {label}
            </button>
          );
        })}
      </div>
      {activeBodyTab === 'terminal' && terminalOpen && agent.ttydSrc ? (
        <div data-id={`project-agent-card-terminal-body-${shortPaneId(agent.paneId)}`} onPointerDown={(event) => event.stopPropagation()} className="-mx-4 mt-3 min-h-0 flex-1 overflow-hidden rounded-lg bg-black">
          <TerminalView ttydSrc={agent.ttydSrc} className="h-full w-full" />
        </div>
      ) : activeBodyTab === 'role' ? (
        <div data-id={`project-agent-card-role-body-${shortPaneId(agent.paneId)}`} onPointerDown={(event) => event.stopPropagation()} onWheel={(event) => event.stopPropagation()} onTouchStart={(event) => event.stopPropagation()} onTouchMove={(event) => event.stopPropagation()} className="-mx-5 flex min-h-0 flex-1 flex-col overflow-hidden bg-[#0b0b0d] overscroll-contain">
          <Suspense fallback={<div data-id="project-agent-card-role-loading" className="flex h-full items-center justify-center text-[11px] text-zinc-600">Loading…</div>}>
            <AgentDocRoleEditor paneId={agent.paneId} className="min-h-0 flex-1" />
          </Suspense>
        </div>
      ) : (
      <div data-id="project-agent-card-live-body-wrap" className="relative -mr-4 mt-3 flex min-h-0 flex-1 flex-col">
      <div data-id="project-agent-card-question-fixed" onPointerDown={(event) => event.stopPropagation()} className="shrink-0 space-y-2 pr-[18px] pb-3">
        <div data-id="project-agent-card-history-link-row" className="flex justify-end">
          <button type="button" data-id={`project-agent-card-history-${shortPaneId(agent.paneId)}`} onClick={(event) => { event.stopPropagation(); onOpenHistory(); }} className="flex items-center gap-1 rounded-md px-1.5 py-1 text-[11px] text-zinc-500 transition hover:bg-white/[0.06] hover:text-zinc-200" aria-label="完整历史">完整历史<ArrowRight className="h-3 w-3" /></button>
        </div>
        {visibleQuestion || visibleQuestionAttachments.length ? (
          <div data-id="project-agent-card-latest-question" className="chat-markdown current-history-markdown mr-auto w-fit max-w-[92%] rounded-xl rounded-bl-sm border border-[var(--chat-question-border)] bg-[var(--chat-question-bg)] px-3 py-2 text-left text-zinc-200 [&_[data-id=current-history-attachment]]:my-0 [&_[data-id=current-history-attachment]]:w-fit [&_[data-id=current-history-attachment]]:max-w-full [&_[data-id=current-history-attachment-actions]]:py-1 [&_[data-id=current-history-attachment-download]]:hidden [&_[data-id=current-history-md-img]]:!h-auto [&_[data-id=current-history-md-img]]:!max-h-40 [&_[data-id=current-history-md-img]]:!w-auto [&_[data-id=current-history-md-img]]:!max-w-full [&_[data-id=current-history-md-img]]:rounded-md [&_[data-id=current-history-md-img]]:object-contain">
            {visibleQuestion ? <MarkdownBlock text={previewableMarkdown(visibleQuestion)} /> : null}
            {visibleQuestionAttachments.length ? (
              <div data-id="project-agent-card-question-attachments" className="mt-2 flex max-w-full flex-wrap gap-2">
                {visibleQuestionAttachments.map((attachment, index) => (
                  <div key={`${attachment}-${index}`} data-id="project-agent-card-question-attachment" className="min-w-0">
                    <MarkdownBlock text={previewableMarkdown(attachment)} />
                  </div>
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>
      <div
        ref={bodyScrollRef}
        data-id="project-agent-card-live-body"
        onPointerDown={(event) => event.stopPropagation()}
        onWheel={(event) => { event.stopPropagation(); if (event.deltaY < 0) loadingDetachedRef.current = true; }}
        onScroll={() => { updateScrollToBottomButton(); updateLoadingVisibility(); }}
        className="min-h-0 w-full flex-1 cursor-text select-text touch-auto space-y-3.5 overflow-y-auto overscroll-contain pr-[18px] text-left text-[14px] leading-[22px] [scrollbar-width:thin]"
      >
        <div data-id="project-agent-card-current-turn" className="space-y-3.5">
        {replyItems.length ? replyItems.map((item: any, index: number) => {
          const type = String(item?.type || '');
          if (type === 'thinking' && item?.thinking) return (
            <div key={`thinking-${index}`} data-id="project-agent-card-reply-thinking" className="flex min-w-0 items-start gap-2 text-zinc-500">
              <Atom className="mt-0.5 h-4 w-4 shrink-0" />
              <span className="shrink-0 font-medium text-zinc-400">Think</span>
              <span aria-hidden="true">·</span>
              <div className="chat-markdown current-history-markdown min-w-0 flex-1"><MarkdownBlock text={previewableMarkdown(String(item.thinking))} /></div>
            </div>
          );
          if (type === 'text' && item?.text) return (
            isTechnicalTransportFailureText(item.text) ? null :
            <div key={`text-${index}`} data-id="project-agent-card-latest-response" className="chat-markdown current-history-markdown text-zinc-300"><MarkdownBlock text={previewableMarkdown(String(item.text))} /></div>
          );
          if (type === 'tool_use') {
            const toolGroupCount = toolGroupsByLastIndex.get(index);
            if (!toolGroupCount) return null;
            const input = item?.input == null ? '' : typeof item.input === 'string' ? item.input : JSON.stringify(item.input);
            const commands = String(item?.name || '') === 'exec' ? codexExecCommands(input) : [];
            const tool = { name: commands.length ? 'exec_command' : String(item?.name || 'Tool'), arg: commands.length ? JSON.stringify({ command: commands.join('\n\n') }) : input };
            const headline = toolHeadline(tool);
            return (
              <button key={`tool-${index}`} type="button" data-id="project-agent-card-reply-tool" onClick={(event) => { event.stopPropagation(); onOpenHistory(); }} className="group/tool flex w-full cursor-pointer items-center gap-2 rounded-md px-1 py-1 text-left text-zinc-500 transition hover:bg-white/[0.035] hover:text-zinc-300">
                  <SquareTerminal className="h-4 w-4 shrink-0" />
                  <span className="shrink-0 font-medium text-zinc-400">{tool.name}</span>
                  {headline ? <><span aria-hidden="true">·</span><span className="min-w-0 truncate">{headline}</span></> : null}
                  <span data-id="project-agent-card-tool-count" className="ml-auto inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded bg-white/[0.07] px-1 font-mono text-[9px] font-semibold leading-none text-zinc-400">{toolGroupCount}</span>
                  <ArrowRight className="h-3.5 w-3.5 shrink-0 opacity-50 transition group-hover/tool:translate-x-0.5 group-hover/tool:opacity-100" />
              </button>
            );
          }
          return null;
        }) : !reply?.question && latest?.latest_response && !isTechnicalTransportFailureText(latest.latest_response) ? (
          <div data-id="project-agent-card-latest-response" className="chat-markdown current-history-markdown text-zinc-400 [&_[data-id=current-history-attachment]]:my-0 [&_[data-id=current-history-attachment]]:w-fit [&_[data-id=current-history-attachment]]:max-w-full [&_[data-id=current-history-attachment-actions]]:py-1 [&_[data-id=current-history-attachment-download]]:hidden [&_[data-id=current-history-md-img]]:!h-auto [&_[data-id=current-history-md-img]]:!max-h-40 [&_[data-id=current-history-md-img]]:!w-auto [&_[data-id=current-history-md-img]]:!max-w-full [&_[data-id=current-history-md-img]]:object-contain">
            <MarkdownBlock text={previewableMarkdown(latest.latest_response)} />
          </div>
        ) : null}
        {!reply?.question && !replyItems.length && latest?.latest_tool?.name ? (
          <div data-id="project-agent-card-latest-tool" className="min-w-0 line-clamp-2 font-mono text-[13px] leading-5 text-zinc-500">
            {`${latest.latest_tool.name}${latest.latest_tool.input ? ` ${latest.latest_tool.input}` : ''}`}
          </div>
        ) : null}
        {working ? (
          <div ref={loadingRef} data-id="project-agent-card-stream-loading" className="flex h-5 items-center gap-1 pt-1" aria-label="Loading reply">
            {[0, 1, 2].map((index) => (
              <span key={index} data-id="project-agent-card-stream-loading-dot" className="h-1.5 w-1.5 animate-bounce rounded-full bg-zinc-500" style={{ animationDelay: `${index * 140}ms` }} />
            ))}
          </div>
        ) : null}
        </div>
      </div>
      {showScrollToBottom ? (
        <button type="button" data-id="project-agent-card-scroll-bottom" aria-label={t('scrollToBottom', { defaultValue: '滚动到底部' })} title={t('scrollToBottom', { defaultValue: '滚动到底部' })} onPointerDown={(event) => event.stopPropagation()} onClick={(event) => { event.stopPropagation(); loadingDetachedRef.current = false; bodyScrollRef.current?.scrollTo({ top: bodyScrollRef.current.scrollHeight, behavior: 'smooth' }); }} className="absolute bottom-2 right-3 grid h-7 w-7 place-items-center rounded-full border border-white/10 bg-[#202126]/95 text-zinc-300 shadow-lg backdrop-blur hover:bg-[#292a30] hover:text-white">
          <ArrowDown className="h-3.5 w-3.5" />
        </button>
      ) : null}
      {rawQuestion && (working || completed) ? (
        <div data-id="project-agent-card-output-loading" className="flex h-6 shrink-0 translate-y-1 items-center gap-1.5 pr-[18px] font-mono text-[11px] text-zinc-500" aria-label={working ? 'Loading' : 'Worked'}>
          {working ? <span data-id="project-agent-card-loading-dot" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400" /> : <Check className="h-3.5 w-3.5 text-emerald-500" />}
          <span className="font-medium text-zinc-400">{working ? 'Working' : 'Worked'}</span>
          {startedAt ? <span className="tabular-nums text-zinc-500">{working ? `· ${fmtElapsed(elapsedMs)}` : `for ${fmtElapsed(elapsedMs)}`}</span> : null}
        </div>
      ) : null}
      </div>
      )}
      </div>
      {activeBodyTab === 'history' ? footer : null}
      <div
        data-id={`project-agent-card-resize-${shortPaneId(agent.paneId)}`}
        onPointerDown={onResizePointerDown}
        onPointerMove={onResizePointerMove}
        onPointerUp={onResizePointerUp}
        onPointerCancel={onResizePointerUp}
        className="absolute bottom-0 right-0 h-5 w-5 cursor-nwse-resize touch-none rounded-br-2xl"
      />
    </article>
  );
}

export default function ProjectsPanel({ agents, statuses = {}, topRightControls, footerControls, shellPanel, dockOpen = false, onOpenAgent, onCreateAgent = () => {}, onOpenGuidance: _onOpenGuidance = () => {}, onOpenHistory = () => {} }: {
  agents: ProjectAgent[];
  statuses?: Record<string, any>;
  topRightControls?: ReactNode;
  footerControls?: ReactNode;
  shellPanel?: ReactNode;
  dockOpen?: boolean;
  onOpenAgent: (paneId: string) => void;
  onCreateAgent?: () => void;
  onOpenGuidance?: (paneId: string) => void;
  onOpenHistory?: (paneId: string) => void;
}) {
  const { t } = useTranslation('workspace');
  const { confirm, prompt, node: dialogsNode } = useDialogs();
  const [groups, setGroups] = useState<AgentProject[]>([]);
  const [teamId, setTeamId] = useState('');
  const [selectedId, setSelectedId] = useState<number | string>(projectIdFromURL);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState('');
  const [createDescription, setCreateDescription] = useState('');
  const [createError, setCreateError] = useState('');
  const [creating, setCreating] = useState(false);
  const [projectMenuId, setProjectMenuId] = useState<string>('');
  const [projectMenuAnchor, setProjectMenuAnchor] = useState<string>('');
  const [addOpen, setAddOpen] = useState(false);
  const [fabOpen, setFabOpen] = useState(false);
  const [addSearch, setAddSearch] = useState('');
  const [addError, setAddError] = useState('');
  const [selectedToAdd, setSelectedToAdd] = useState<Set<string>>(new Set());
  const [selectedAgentIds, setSelectedAgentIds] = useState<Set<string>>(new Set());
  const [agentMessages, setAgentMessages] = useState<Record<string, string>>({});
  const [agentAttachments, setAgentAttachments] = useState<Record<string, ProjectAttachment[]>>({});
  const [agentReplies, setAgentReplies] = useState<Record<string, any>>({});
  const [queuedAgentMessages, setQueuedAgentMessages] = useState<Record<string, QueuedAgentMessage[]>>(readProjectAgentQueue);
  const [sendingAgentIds, setSendingAgentIds] = useState<Set<string>>(new Set());
  const [cancelingAgentIds, setCancelingAgentIds] = useState<Set<string>>(new Set());
  const [terminalAgentIds, setTerminalAgentIds] = useState<Set<string>>(new Set());
  const [agentLayouts, setAgentLayouts] = useState<Record<string, ProjectAgentLayout>>({});
  const [layoutReadyProjectId, setLayoutReadyProjectId] = useState('');
  const [layoutVisibilityRevision, setLayoutVisibilityRevision] = useState(0);
  const [canvasPan, setCanvasPan] = useState({ x: 60, y: 60 });
  const [canvasZoom, setCanvasZoom] = useState(1);
  const [dockHeight, setDockHeight] = useState(0);
  const liveMetrics = useMemo(() => {
    const result: Record<string, AgentLiveMetrics> = {};
    for (const agent of agents) {
      const fullId = String(agent.paneId || '');
      const shortId = shortPaneId(fullId);
      const reply = statuses[fullId] || statuses[shortId];
      if (reply && typeof reply === 'object') result[shortId] = metricsFromCurrentReply(reply);
    }
    return result;
  }, [agents, statuses]);
  const canvasRef = useRef<HTMLDivElement>(null);
  const visibilityCheckedKeyRef = useRef('');
  const promptHistoryIndexRef = useRef<Record<string, number | null>>({});
  const promptHistoryDraftRef = useRef<Record<string, string>>({});

  useEffect(() => {
    const persisted = Object.fromEntries(Object.entries(queuedAgentMessages).map(([paneId, messages]) => [paneId, messages.map((message) => ({
      ...message,
      attachments: message.attachments.map(({ previewURL: _previewURL, ...attachment }) => attachment),
    }))]));
    try { localStorage.setItem(PROJECT_AGENT_QUEUE_KEY, JSON.stringify(persisted)); } catch {}
  }, [queuedAgentMessages]);
  const agentDragRef = useRef<{ id: string; pointerId: number; startX: number; startY: number; originX: number; originY: number; moved: boolean } | null>(null);
  const agentResizeRef = useRef<{ id: string; pointerId: number; startX: number; startY: number; originWidth: number; originHeight: number } | null>(null);
  const panDragRef = useRef<{ pointerId: number; startX: number; startY: number; originX: number; originY: number } | null>(null);
  const optimisticQuestionsRef = useRef<Record<string, string>>({});

  useEffect(() => {
    if (dockOpen) setFabOpen(false);
  }, [dockOpen]);

  useEffect(() => {
    if (!dockOpen) { setDockHeight(0); return; }
    const canvas = canvasRef.current;
    if (!canvas) return;
    const resizeObserver = new ResizeObserver(() => measure());
    const measure = () => {
      const docks = Array.from(canvas.querySelectorAll('[data-id="ports-panel"], [data-id="project-canvas-shell-panel"]'));
      setDockHeight(Math.ceil(Math.max(0, ...docks.map((dock) => dock.getBoundingClientRect().height))));
      resizeObserver.disconnect();
      docks.forEach((dock) => resizeObserver.observe(dock));
    };
    measure();
    const observer = new MutationObserver(measure);
    observer.observe(canvas, { childList: true, subtree: true });
    return () => { observer.disconnect(); resizeObserver.disconnect(); };
  }, [dockOpen]);

  const load = useCallback(async (showLoading = true) => {
    if (showLoading) setLoading(true);
    setError('');
    try {
      const response = await apiService.listGroups();
      const rows = Array.isArray(response?.data?.groups) ? response.data.groups : [];
      setGroups(rows.map((group: any) => {
        const isDefault = Boolean(group.is_default);
        return {
          id: isDefault ? DEFAULT_PROJECT_ID : group.id,
          api_id: group.id,
          name: isDefault && !group.name_customized ? t('projectDefault') : String(group.name || ''),
          description: isDefault ? t('projectDefaultDescription') : String(group.description || ''),
          pane_ids: Array.isArray(group.pane_ids) ? group.pane_ids.map(String) : [],
          pane_count: Number(group.pane_count || 0),
          builtin: isDefault,
          pinned: Boolean(group.is_pinned),
        };
      }));
    } catch (cause: any) {
      setError(cause?.message || t('projectLoadFailed'));
    } finally {
      if (showLoading) setLoading(false);
    }
  }, [t]);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (!projectMenuId) return;
    const close = () => { setProjectMenuId(''); setProjectMenuAnchor(''); };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [projectMenuId]);

  useEffect(() => {
    const encodedId = encodeURIComponent(String(selectedId));
    const nextHash = `#/project/${encodedId}`;
    if (window.location.hash !== nextHash) window.history.replaceState(null, '', nextHash);
  }, [selectedId]);

  useEffect(() => {
    if (typeof apiService.getIMAccounts !== 'function') return;
    let cancelled = false;
    apiService.getIMAccounts().then((response) => {
      if (cancelled) return;
      const rows = Array.isArray(response?.data?.accounts) ? response.data.accounts : [];
      const cloud = rows.find((account: any) => account?.platform === 'cicy_cloud');
      setTeamId(String(cloud?.config?.team_id || ''));
    }).catch(() => { if (!cancelled) setTeamId(''); });
    return () => { cancelled = true; };
  }, []);

  const projects = groups;

  const selectedProject = projects.find((project) => String(project.id) === String(selectedId)) || projects[0] || {
    id: DEFAULT_PROJECT_ID,
    api_id: '',
    name: t('projectDefault'),
    description: t('projectDefaultDescription'),
    pane_ids: [],
    pane_count: 0,
    builtin: true,
  };
  const memberIds = useMemo(() => new Set(selectedProject.pane_ids.map(shortPaneId)), [selectedProject.pane_ids]);
  const visibleAgents = agents.filter((agent) => memberIds.has(shortPaneId(agent.paneId)));
  const assignedAgentIds = useMemo(() => new Set(groups.flatMap((group) => group.pane_ids.map(shortPaneId))), [groups]);
  const availableAgents = agents.filter((agent) => !assignedAgentIds.has(shortPaneId(agent.paneId)));
  const normalizedAddSearch = addSearch.trim().toLowerCase();
  const filteredAvailableAgents = availableAgents.filter((agent) => !normalizedAddSearch || [
    agent.title,
    agent.paneId,
    agent.agentType,
    agent.defaultModel,
    agent.workspace,
  ].some((value) => String(value || '').toLowerCase().includes(normalizedAddSearch)));
  const paneMembershipKey = selectedProject.pane_ids.map(shortPaneId).sort().join('|');
  const visibleAgentKey = visibleAgents.map((agent) => shortPaneId(agent.paneId)).sort().join('|');
  useEffect(() => {
    let cancelled = false;
    const ids = visibleAgents.map((agent) => shortPaneId(agent.paneId));
    if (!ids.length) { setAgentReplies({}); return () => { cancelled = true; }; }
    let polling = false;
    const pollReplies = async () => {
      if (polling || cancelled) return;
      polling = true;
      const rows = await Promise.all(ids.map(async (id) => {
        try {
          const response = await apiService.getAgentCurrentReply(id);
          return [id, response?.data || {}] as const;
        } catch {
          return [id, null] as const;
        }
      }));
      polling = false;
      if (cancelled) return;
      setAgentReplies((current) => {
        let changed = false;
        const next = { ...current };
        for (const [id, reply] of rows) {
          if (!reply) continue;
          const optimistic = optimisticQuestionsRef.current[id];
          if (optimistic && String(reply.question || '').trim() !== optimistic) continue;
          if (optimistic) delete optimisticQuestionsRef.current[id];
          if (JSON.stringify(current[id] || {}) === JSON.stringify(reply)) continue;
          next[id] = reply;
          changed = true;
        }
        return changed ? next : current;
      });
    };
    void pollReplies();
    const timer = window.setInterval(() => { void pollReplies(); }, 500);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [visibleAgentKey]);

  useEffect(() => {
    const checkKey = `${selectedProject.id}:${paneMembershipKey}:${visibleAgentKey}:${layoutVisibilityRevision}`;
    if (layoutReadyProjectId !== String(selectedProject.id) || visibilityCheckedKeyRef.current === checkKey || !visibleAgents.length) return;
    const frame = window.requestAnimationFrame(() => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      visibilityCheckedKeyRef.current = checkKey;
      const layouts = visibleAgents.map((agent, index) => agentLayouts[shortPaneId(agent.paneId)] || {
        x: 40 + (index % 4) * 340,
        y: 40 + Math.floor(index / 4) * 260,
        z: index + 1,
        width: 300,
        height: 320,
      });
      const viewportWidth = canvas.clientWidth;
      const viewportHeight = Math.max(1, canvas.clientHeight - 48);
      const anyVisible = layouts.some((layout) => {
        const left = canvasPan.x + layout.x * canvasZoom;
        const top = canvasPan.y + layout.y * canvasZoom;
        return left < viewportWidth && left + layout.width * canvasZoom > 0 && top < viewportHeight && top + layout.height * canvasZoom > 0;
      });
      if (anyVisible) return;
      const minX = Math.min(...layouts.map((layout) => layout.x));
      const minY = Math.min(...layouts.map((layout) => layout.y));
      const maxX = Math.max(...layouts.map((layout) => layout.x + layout.width));
      const maxY = Math.max(...layouts.map((layout) => layout.y + layout.height));
      const margin = 40;
      const zoom = Math.min(1, Math.max(0.35, Math.min((viewportWidth - margin * 2) / Math.max(1, maxX - minX), (viewportHeight - margin * 2) / Math.max(1, maxY - minY))));
      const nextZoom = Number(zoom.toFixed(2));
      const nextPan = { x: margin - minX * zoom, y: margin - minY * zoom };
      setCanvasZoom(nextZoom);
      setCanvasPan(nextPan);
      writeProjectViewCache(selectedProject.id, { zoom: nextZoom, pan: nextPan });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [layoutReadyProjectId, layoutVisibilityRevision, paneMembershipKey, selectedProject.id, visibleAgentKey]);

  useEffect(() => {
    visibilityCheckedKeyRef.current = '';
    const cached = readProjectViewCache(selectedProject.id);
    setAgentLayouts(cached?.layouts || {});
    setCanvasPan(cached?.pan || { x: 60, y: 60 });
    setCanvasZoom(cached?.zoom || 1);
    setLayoutReadyProjectId(cached ? String(selectedProject.id) : '');
    setSelectedAgentIds(new Set());
    setAgentMessages({});
    setQueuedAgentMessages({});
    setSendingAgentIds(new Set());
    setTerminalAgentIds(new Set());
    optimisticQuestionsRef.current = {};
  }, [selectedProject.id]);

  useEffect(() => {
    let cancelled = false;
    const loadLayout = async () => {
      const cached = readProjectViewCache(selectedProject.id);
      if (cached) {
        setAgentLayouts(cached.layouts);
        setCanvasPan(cached.pan);
        setCanvasZoom(cached.zoom);
        setLayoutReadyProjectId(String(selectedProject.id));
        setLayoutVisibilityRevision((value) => value + 1);
      }
      if (!selectedProject.api_id) {
        if (!cached) setAgentLayouts({});
        setLayoutReadyProjectId(String(selectedProject.id));
        return;
      }
      try {
        const response = await apiService.getGroup(selectedProject.api_id);
        const rows = Array.isArray(response?.data?.panes) ? response.data.panes : [];
        const next: Record<string, ProjectAgentLayout> = {};
        const placed: ProjectAgentLayout[] = [];
        rows.forEach((row: any, index: number) => {
          const id = shortPaneId(String(row?.pane_id || ''));
          if (!id) return;
          const storedWidth = Number(row?.width || 300);
          const storedHeight = Number(row?.height || 180);
          const legacyDefaultSize = storedWidth === 480 && storedHeight === 320;
          const width = legacyDefaultSize ? 300 : Math.max(260, storedWidth);
          const height = legacyDefaultSize ? 320 : Math.max(240, storedHeight);
          const originalX = Number.isFinite(Number(row?.pos_x)) ? Number(row.pos_x) : 40 + (index % 4) * 340;
          const originalY = Number.isFinite(Number(row?.pos_y)) ? Number(row.pos_y) : 40 + Math.floor(index / 4) * 360;
          let x = originalX;
          let y = originalY;
          const sameRow = placed.filter((item) => y < item.y + item.height && y + height > item.y);
          if (sameRow.length) {
            const rowRight = Math.max(...sameRow.map((item) => item.x + item.width));
            if (x - rowRight > 120) x = rowRight + 24;
          }
          const overlaps = () => placed.some((item) => x < item.x + item.width + 20 && x + width + 20 > item.x && y < item.y + item.height + 20 && y + height + 20 > item.y);
          while (overlaps()) {
            x += 340;
            if (x > 1400) { x = 40; y += 360; }
          }
          const layout = {
            x, y,
            z: Number(row?.z_index || index + 1),
            width, height,
          };
          next[id] = layout;
          placed.push(layout);
        });
        if (!cancelled) {
          // A late server layout replaces the initially visible/default layout.
          // Re-run the one-shot visibility check against these real coordinates;
          // otherwise an offscreen server layout makes cards flash, then vanish.
          setAgentLayouts(next);
          setLayoutReadyProjectId(String(selectedProject.id));
          setLayoutVisibilityRevision((value) => value + 1);
          writeProjectViewCache(selectedProject.id, { layouts: next });
        }
      } catch {
        if (!cancelled) {
          setAgentLayouts({});
          setLayoutReadyProjectId(String(selectedProject.id));
          setLayoutVisibilityRevision((value) => value + 1);
        }
      }
    };
    void loadLayout();
    return () => { cancelled = true; };
  }, [selectedProject.api_id, paneMembershipKey]);

  const openAddAgents = (project: AgentProject) => {
    setSelectedId(project.id);
    setSelectedToAdd(new Set());
    setAddSearch('');
    setAddError('');
    setAddOpen(true);
  };

  const openCreateProject = () => {
    setCreateName('');
    setCreateDescription('');
    setCreateError('');
    setCreateOpen(true);
  };

  const closeCreateProject = () => {
    if (creating) return;
    setCreateOpen(false);
    setCreateError('');
  };

  const createProject = async () => {
    const name = createName.trim();
    if (!name) {
      setCreateError(t('projectNameRequired'));
      return;
    }
    if (projects.some((project) => project.name.trim().toLocaleLowerCase() === name.toLocaleLowerCase())) {
      setCreateError(t('projectNameExists'));
      return;
    }
    setCreating(true);
    setCreateError('');
    try {
      const response = await apiService.createGroup({ name, description: createDescription.trim() });
      await load();
      if (response?.data?.id != null) setSelectedId(response.data.id);
      setCreateOpen(false);
      setCreateName('');
      setCreateDescription('');
    } catch (cause: any) {
      setCreateError(cause?.message || t('projectCreateFailed'));
    } finally {
      setCreating(false);
    }
  };

  const renameProject = async (project: AgentProject) => {
    const value = await prompt({ title: t('projectRenameTitle'), defaultValue: project.name, required: true, confirmLabel: t('projectSave') });
    const name = String(value || '').trim();
    if (!name || name === project.name) return;
    setBusy(true);
    setAddError('');
    try {
      await apiService.updateGroup(project.id, { name });
      await load();
    } finally {
      setBusy(false);
    }
  };

  const toggleProjectPinned = async (project: AgentProject) => {
    setBusy(true);
    try {
      await apiService.updateGroup(project.api_id, { is_pinned: !project.pinned });
      await load();
    } finally {
      setBusy(false);
    }
  };

  const deleteProject = async (project: AgentProject) => {
    if (project.builtin) return;
    const ok = await confirm({
      title: t('projectDeleteTitle', { name: project.name }),
      body: t('projectDeleteBody'),
      confirmLabel: t('projectDelete'),
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    try {
      await apiService.deleteGroup(project.id);
      setSelectedId(DEFAULT_PROJECT_ID);
      await load();
    } finally {
      setBusy(false);
    }
  };

  const addSelectedAgents = async () => {
    if (!selectedProject.api_id || selectedToAdd.size === 0) return;
    setBusy(true);
    try {
      const paneIds = [...selectedToAdd];
      const occupied = Object.values(agentLayouts);
      for (const [offset, paneId] of paneIds.entries()) {
        await apiService.addGroupPane(selectedProject.api_id, paneId);
        const index = visibleAgents.length + offset;
        const width = 300;
        const height = 320;
        let x = 40;
        let y = 40;
        const overlaps = () => occupied.some((item) => x < item.x + item.width + 20 && x + width + 20 > item.x && y < item.y + item.height + 20 && y + height + 20 > item.y);
        while (overlaps()) {
          x += 340;
          if (x > 1400) { x = 40; y += 360; }
        }
        const layout = { x, y, width, height, z: index + 1 };
        await apiService.updateGroupPaneLayout(selectedProject.api_id, paneId, {
          pos_x: x, pos_y: y, width, height, z_index: layout.z,
        });
        occupied.push(layout);
        setAgentLayouts((current) => {
          const next = { ...current, [shortPaneId(paneId)]: layout };
          writeProjectViewCache(selectedProject.id, { layouts: next });
          return next;
        });
        setGroups((current) => current.map((group) => String(group.id) === String(selectedProject.id)
          ? { ...group, pane_ids: [...group.pane_ids, paneId], pane_count: group.pane_count + 1 }
          : group));
      }
      setAddOpen(false);
      setSelectedToAdd(new Set());
      await load(false);
    } catch (cause: any) {
      setAddError(cause?.response?.data?.error || cause?.message || t('projectAddAgentFailed'));
    } finally {
      setBusy(false);
    }
  };

  const removeAgent = async (agent: ProjectAgent) => {
    if (!selectedProject.api_id) return;
    await apiService.removeGroupPane(selectedProject.api_id, agent.paneId);
    const id = shortPaneId(agent.paneId);
    setGroups((current) => current.map((group) => String(group.id) === String(selectedProject.id)
      ? { ...group, pane_ids: group.pane_ids.filter((paneId) => shortPaneId(paneId) !== id), pane_count: Math.max(0, group.pane_count - 1) }
      : group));
    setAgentLayouts((current) => {
      const next = { ...current };
      delete next[id];
      writeProjectViewCache(selectedProject.id, { layouts: next });
      return next;
    });
    await load(false);
  };

  const toggleAgentSelection = (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    setSelectedAgentIds((current) => current.has(id) ? current : new Set([id]));
  };

  const addAgentFiles = (agent: ProjectAgent, files: FileList | File[]) => {
    const agentId = shortPaneId(agent.paneId);
    Array.from(files).forEach((file) => {
      const id = `project-att-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const mediaType = file.type.startsWith('image/') ? 'image' : file.type.startsWith('video/') ? 'video' : file.type.startsWith('audio/') ? 'audio' : undefined;
      const attachment: ProjectAttachment = { id, name: file.name, size: file.size, isImage: mediaType === 'image', mediaType, previewURL: mediaType ? URL.createObjectURL(file) : undefined, status: 'uploading', progress: 0 };
      setAgentAttachments((current) => ({ ...current, [agentId]: [...(current[agentId] || []), attachment] }));
      void apiService.uploadAssetFile(agent.paneId, file, (loaded, total) => {
        setAgentAttachments((current) => ({ ...current, [agentId]: (current[agentId] || []).map((item) => item.id === id ? { ...item, progress: Math.max(1, Math.round((loaded / total) * 100)) } : item) }));
      }).then((response: any) => {
        const uploaded = response?.data?.file || {};
        setAgentAttachments((current) => ({ ...current, [agentId]: (current[agentId] || []).map((item) => item.id === id ? { ...item, status: 'done', progress: 100, fileRef: String(uploaded.file_ref || uploaded.fileRef || '') } : item) }));
      }).catch(() => {
        setAgentAttachments((current) => ({ ...current, [agentId]: (current[agentId] || []).map((item) => item.id === id ? { ...item, status: 'error' } : item) }));
      });
    });
  };

  const removeAgentAttachment = (agentId: string, attachmentId: string) => {
    setAgentAttachments((current) => {
      const attachment = (current[agentId] || []).find((item) => item.id === attachmentId);
      if (attachment?.previewURL) URL.revokeObjectURL(attachment.previewURL);
      return { ...current, [agentId]: (current[agentId] || []).filter((item) => item.id !== attachmentId) };
    });
  };

  const agentIsThinking = (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    const summary = statuses[agent.paneId] || statuses[`${id}:main.0`] || statuses[id] || {};
    // poll/status is the freshest source. Do not OR it with stale agent/reply
    // snapshots: an old "thinking" there would keep queued prompts stuck forever
    // after the live status has already reached completed/idle.
    const status = String(summary?.status || agentReplies[id]?.status || agent.status || '').toLowerCase();
    return sendingAgentIds.has(id) || Boolean(liveMetrics[id]?.working) || /thinking|working|running|streaming|pending|tool_use|tool_call|in_progress/.test(status);
  };

  const deliverAgentMessage = async (agent: ProjectAgent, message: string, displayQuestion: string, previousReply: any, sentAttachments: ProjectAttachment[] = [], restoreText = '') => {
    const id = shortPaneId(agent.paneId);
    setSendingAgentIds((current) => new Set(current).add(id));
    optimisticQuestionsRef.current[id] = displayQuestion;
    setAgentReplies((current) => ({
      ...current,
      [id]: { question: displayQuestion, items: [], answer: '', thinking: '', status: 'pending', started_at: new Date().toISOString() },
    }));
    try {
      await sendToAgent(agent.paneId, message, { submit: true, agentType: agent.agentType });
      sentAttachments.forEach((item) => { if (item.previewURL) URL.revokeObjectURL(item.previewURL); });
    } catch (cause: any) {
      delete optimisticQuestionsRef.current[id];
      setAgentMessages((current) => ({ ...current, [id]: [restoreText, current[id]].filter(Boolean).join('\n') }));
      setAgentAttachments((current) => ({ ...current, [id]: [...sentAttachments, ...(current[id] || [])] }));
      setAgentReplies((current) => ({ ...current, [id]: previousReply || {} }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: cause?.message || t('projectMessageFailed') }));
    } finally {
      setSendingAgentIds((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
      window.requestAnimationFrame(() => {
        window.requestAnimationFrame(() => {
          document.querySelector<HTMLInputElement>(`[data-id="project-agent-prompt-input-${id}"]`)?.focus({ preventScroll: true });
        });
      });
    }
  };

  const sendAgentMessage = async (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    const text = String(agentMessages[id] || '').trim();
    const attachments = agentAttachments[id] || [];
    if ((!text && !attachments.some((item) => item.status === 'done')) || attachments.some((item) => item.status === 'uploading')) return;
    appendPromptHistory(id, text);
    promptHistoryIndexRef.current[id] = null;
    promptHistoryDraftRef.current[id] = '';
    const attachmentText = attachments.filter((item) => item.status === 'done' && item.fileRef).map((item) => agent.agentType === 'cicy' ? chatAttachmentMarkdown(item.name, item.fileRef!, item.isImage) : replAttachmentMarkdown(item.name, item.fileRef!)).join('\n\n');
    const message = [text, attachmentText].filter(Boolean).join('\n\n');
    const displayQuestion = message;
    const previousReply = agentReplies[id];
    setAgentMessages((current) => ({ ...current, [id]: '' }));
    if (agentIsThinking(agent)) {
      setQueuedAgentMessages((current) => ({
        ...current,
        [id]: [...(current[id] || []), { id: `queued-${Date.now()}-${Math.random().toString(36).slice(2)}`, display: text, payload: message, attachments: attachments.filter((item) => item.status === 'done') }],
      }));
      // The queue owns these attachment previews until it is released. Merely
      // detach them from the composer so a later text message cannot erase them.
      setAgentAttachments((current) => ({ ...current, [id]: [] }));
      return;
    }
    // Clear the composer immediately, in lockstep with the optimistic question.
    // Keep the attachment objects alive until delivery succeeds so a failure can
    // restore both text and files without asking the user to upload again.
    setAgentAttachments((current) => ({ ...current, [id]: [] }));
    await deliverAgentMessage(agent, message, displayQuestion, previousReply, attachments, text);
  };

  const editQueuedAgentMessage = (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    const queued = queuedAgentMessages[id] || [];
    if (!queued.length) return;
    const queuedText = queued.map((item) => questionWithoutUploadedAttachments(item.display)).filter(Boolean).join('\n');
    const currentText = String(agentMessages[id] || '').trim();
    const queuedAttachments = queued.flatMap((item) => item.attachments);
    setAgentMessages((current) => ({ ...current, [id]: [queuedText, currentText].filter(Boolean).join('\n') }));
    setAgentAttachments((current) => ({ ...current, [id]: [...queuedAttachments, ...(current[id] || [])] }));
    setQueuedAgentMessages((current) => ({ ...current, [id]: [] }));
    window.requestAnimationFrame(() => {
      document.querySelector<HTMLInputElement>(`[data-id="project-agent-prompt-input-${id}"]`)?.focus({ preventScroll: true });
    });
  };

  const deleteQueuedAgentMessage = (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    const queued = queuedAgentMessages[id] || [];
    queued.flatMap((item) => item.attachments).forEach((item) => { if (item.previewURL) URL.revokeObjectURL(item.previewURL); });
    setQueuedAgentMessages((current) => ({ ...current, [id]: [] }));
  };

  useEffect(() => {
    for (const agent of agents) {
      const id = shortPaneId(agent.paneId);
      const queued = queuedAgentMessages[id] || [];
      if (!queued.length || agentIsThinking(agent)) continue;
      const payload = queued.map((item) => item.payload).join('\n\n');
      const displayQuestion = payload;
      const previousReply = agentReplies[id];
      queued.flatMap((item) => item.attachments).forEach((item) => { if (item.previewURL) URL.revokeObjectURL(item.previewURL); });
      setQueuedAgentMessages((current) => ({ ...current, [id]: [] }));
      void deliverAgentMessage(agent, payload, displayQuestion, previousReply);
    }
  }, [agents, agentReplies, queuedAgentMessages, sendingAgentIds, statuses]);

  const cancelAgentMessage = async (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    if (cancelingAgentIds.has(id)) return;
    setCancelingAgentIds((current) => new Set(current).add(id));
    try {
      if (agent.agentType === 'cicy') await apiService.cancelCicyReply(agent.paneId);
      else await apiService.sendKeys(agent.paneId, 'Escape');
      setSendingAgentIds((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
    } finally {
      setCancelingAgentIds((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
      window.setTimeout(() => document.querySelector<HTMLInputElement>(`[data-id="project-agent-prompt-input-${id}"]`)?.focus(), 0);
    }
  };

  const layoutForAgent = (agent: ProjectAgent, index: number): ProjectAgentLayout => agentLayouts[shortPaneId(agent.paneId)] || {
    x: 40 + (index % 4) * 340,
    y: 40 + Math.floor(index / 4) * 260,
    z: index + 1,
    width: 300,
    height: 320,
  };

  const beginAgentDrag = (event: ReactPointerEvent<HTMLDivElement>, agent: ProjectAgent, index: number) => {
    if (event.button !== 0 || (event.target as HTMLElement).closest('button, input, textarea')) return;
    event.stopPropagation();
    const id = shortPaneId(agent.paneId);
    const layout = layoutForAgent(agent, index);
    agentDragRef.current = {
      id,
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX: layout.x,
      originY: layout.y,
      moved: false,
    };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };

  const moveAgent = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = agentDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    const dx = (event.clientX - drag.startX) / canvasZoom;
    const dy = (event.clientY - drag.startY) / canvasZoom;
    if (Math.abs(dx) > 3 || Math.abs(dy) > 3) drag.moved = true;
    setAgentLayouts((current) => ({
      ...current,
      [drag.id]: { ...(current[drag.id] || { x: drag.originX, y: drag.originY, z: 1, width: 300, height: 320 }), x: drag.originX + dx, y: drag.originY + dy },
    }));
  };

  const endAgentDrag = (event: ReactPointerEvent<HTMLDivElement>, agent: ProjectAgent) => {
    const drag = agentDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    agentDragRef.current = null;
    if (!drag.moved) {
      toggleAgentSelection(agent);
      return;
    }
    const x = drag.originX + (event.clientX - drag.startX) / canvasZoom;
    const y = drag.originY + (event.clientY - drag.startY) / canvasZoom;
    setAgentLayouts((current) => {
      const next = { ...current, [drag.id]: { ...(current[drag.id] || { x, y, z: 1, width: 300, height: 320 }), x, y } };
      writeProjectViewCache(selectedProject.id, { layouts: next });
      return next;
    });
    if (selectedProject.api_id) {
      void apiService.updateGroupPaneLayout(selectedProject.api_id, agent.paneId, { pos_x: x, pos_y: y });
    }
  };

  const beginAgentResize = (event: ReactPointerEvent<HTMLDivElement>, agent: ProjectAgent, index: number) => {
    event.preventDefault();
    event.stopPropagation();
    const layout = layoutForAgent(agent, index);
    agentResizeRef.current = {
      id: shortPaneId(agent.paneId), pointerId: event.pointerId,
      startX: event.clientX, startY: event.clientY,
      originWidth: layout.width, originHeight: layout.height,
    };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };

  const resizeAgent = (event: ReactPointerEvent<HTMLDivElement>) => {
    const resize = agentResizeRef.current;
    if (!resize || resize.pointerId !== event.pointerId) return;
    event.stopPropagation();
    const width = Math.max(260, Math.min(900, resize.originWidth + (event.clientX - resize.startX) / canvasZoom));
    const height = Math.max(240, Math.min(700, resize.originHeight + (event.clientY - resize.startY) / canvasZoom));
    setAgentLayouts((current) => ({ ...current, [resize.id]: { ...current[resize.id], width, height } }));
  };

  const endAgentResize = (event: ReactPointerEvent<HTMLDivElement>, agent: ProjectAgent) => {
    const resize = agentResizeRef.current;
    if (!resize || resize.pointerId !== event.pointerId) return;
    event.stopPropagation();
    agentResizeRef.current = null;
    const current = agentLayouts[resize.id] || layoutForAgent(agent, 0);
    const layout = {
      ...current,
      width: Math.max(260, Math.min(900, resize.originWidth + (event.clientX - resize.startX) / canvasZoom)),
      height: Math.max(240, Math.min(700, resize.originHeight + (event.clientY - resize.startY) / canvasZoom)),
    };
    setAgentLayouts((layouts) => {
      const next = { ...layouts, [resize.id]: layout };
      writeProjectViewCache(selectedProject.id, { layouts: next });
      return next;
    });
    if (selectedProject.api_id) {
      void apiService.updateGroupPaneLayout(selectedProject.api_id, agent.paneId, {
        pos_x: layout.x, pos_y: layout.y, width: layout.width, height: layout.height, z_index: layout.z,
      });
    }
  };

  const beginCanvasPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || event.target !== event.currentTarget) return;
    panDragRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX: canvasPan.x,
      originY: canvasPan.y,
    };
    event.currentTarget.setPointerCapture(event.pointerId);
  };

  const moveCanvas = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = panDragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    setCanvasPan({ x: drag.originX + event.clientX - drag.startX, y: drag.originY + event.clientY - drag.startY });
  };

  const endCanvasPan = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (panDragRef.current?.pointerId !== event.pointerId) return;
    const drag = panDragRef.current;
    panDragRef.current = null;
    writeProjectViewCache(selectedProject.id, { pan: { x: drag.originX + event.clientX - drag.startX, y: drag.originY + event.clientY - drag.startY } });
  };

  const changeZoom = (delta: number) => setCanvasZoom((current) => {
    const next = Math.min(2, Math.max(0.35, Number((current + delta).toFixed(2))));
    writeProjectViewCache(selectedProject.id, { zoom: next });
    return next;
  });
  const resetCanvasView = () => {
    const pan = { x: 60, y: 60 };
    setCanvasPan(pan);
    setCanvasZoom(1);
    writeProjectViewCache(selectedProject.id, { pan, zoom: 1 });
  };

  return (
    <section data-id="projects-panel" className="flex h-full min-w-0 flex-1 bg-[#090a0d] text-zinc-300">
      <aside data-id="projects-list" className="flex w-[280px] shrink-0 flex-col border-r border-white/[0.07] bg-[#0d0e12] max-[700px]:w-[180px]">
        <header data-id="projects-list-header" className="flex h-14 shrink-0 items-center border-b border-white/[0.07] px-4">
          <h2 data-id="projects-list-title" className="flex-1 text-[15px] font-semibold text-zinc-100">{t('projectsTitle')}</h2>
          <button
            type="button"
            data-id="projects-create"
            onClick={openCreateProject}
            disabled={busy || creating}
            className="grid h-8 w-8 place-items-center rounded-lg text-zinc-400 hover:bg-white/[0.06] hover:text-white disabled:opacity-40"
            title={t('projectCreate')}
            aria-label={t('projectCreate')}
          >
            <Plus className="h-5 w-5" />
          </button>
        </header>
        <div data-id="projects-list-body" className="min-h-0 flex-1 overflow-y-auto p-2">
          {loading ? (
            <div data-id="projects-loading" className="flex items-center justify-center py-8 text-xs text-zinc-600"><Loader2 className="mr-2 h-4 w-4 animate-spin" />{t('projectLoading')}</div>
          ) : null}
          {error ? <div data-id="projects-error" className="m-2 rounded-lg bg-red-500/10 p-3 text-xs text-red-300">{error}</div> : null}
          {projects.map((project) => {
            const active = String(project.id) === String(selectedProject.id);
            return (
              <div
                key={String(project.id)}
                data-id={`project-list-item-${project.id}`}
                role="button"
                tabIndex={0}
                onClick={() => setSelectedId(project.id)}
                onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') setSelectedId(project.id); }}
                className={cn('group relative mb-1 flex h-12 cursor-pointer items-center gap-2 px-3 transition-colors', active ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-400 hover:bg-white/[0.04] hover:text-zinc-200')}
              >
                {project.pinned ? <Pin data-id="project-list-item-pinned" className="h-3 w-3 shrink-0 text-amber-400" /> : null}
                <span data-id="project-list-item-name" className="min-w-0 flex-1 truncate text-[13px] font-medium">{project.name}</span>
                <div data-id="project-list-item-actions" className="relative w-7 shrink-0">
                  <button type="button" data-id="project-more" onMouseDown={(event) => event.stopPropagation()} onClick={(event) => { event.stopPropagation(); setProjectMenuId(String(project.id)); setProjectMenuAnchor(String(project.id)); }} className="grid h-7 w-7 place-items-center rounded-lg text-zinc-500 opacity-0 transition-opacity hover:bg-white/[0.08] hover:text-zinc-200 group-hover:opacity-100 group-focus-within:opacity-100" title={t('projectMore')}><MoreHorizontal className="h-4 w-4" /></button>
                  {projectMenuId === String(project.id) && projectMenuAnchor === String(project.id) ? (
                    <div data-id="project-more-menu" onMouseDown={(event) => event.stopPropagation()} onClick={(event) => event.stopPropagation()} className="absolute right-0 top-8 z-50 min-w-[150px] rounded-xl border border-white/10 bg-[#18191e] p-1 shadow-2xl">
                      <button type="button" data-id="project-pin" onClick={() => { setProjectMenuId(''); void toggleProjectPinned(project); }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-xs text-zinc-300 hover:bg-white/[0.06]">{project.pinned ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}{project.pinned ? t('projectUnpin') : t('projectPin')}</button>
                      <button type="button" data-id="project-rename" onClick={() => { setProjectMenuId(''); void renameProject(project); }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-xs text-zinc-300 hover:bg-white/[0.06]"><Pencil className="h-3.5 w-3.5" />{t('projectRename')}</button>
                      {!project.builtin ? <button type="button" data-id="project-delete" onClick={() => { setProjectMenuId(''); void deleteProject(project); }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-xs text-red-300 hover:bg-red-500/10"><Trash2 className="h-3.5 w-3.5" />{t('projectDelete')}</button> : null}
                    </div>
                  ) : null}
                </div>
              </div>
            );
          })}
        </div>
      </aside>

      <main data-id="projects-agent-canvas" className="relative flex min-w-0 flex-1 flex-col overflow-hidden bg-[radial-gradient(circle_at_1px_1px,rgba(255,255,255,0.07)_1px,transparent_0)] bg-[size:32px_32px]">
        <header data-id="projects-agent-header" className="z-10 flex h-14 shrink-0 items-center border-b border-white/[0.06] bg-[#090a0d]/90 px-5 backdrop-blur">
          <div data-id="projects-agent-heading" className="min-w-0 flex-1">
            <h2 data-id="projects-agent-title" className="truncate text-[15px] font-semibold text-zinc-100">{selectedProject.name}</h2>
            <p data-id="projects-agent-count" className="text-[11px] text-zinc-600">{t('projectAgentCount', { count: visibleAgents.length })}</p>
          </div>
          {topRightControls}
        </header>

        <div
          ref={canvasRef}
          data-id="project-infinite-canvas"
          onPointerDown={beginCanvasPan}
          onPointerMove={moveCanvas}
          onPointerUp={endCanvasPan}
          onPointerCancel={endCanvasPan}
          onWheel={(event) => {
            event.preventDefault();
            if (event.ctrlKey || event.metaKey) changeZoom(event.deltaY > 0 ? -0.08 : 0.08);
            else setCanvasPan((current) => {
              const next = { x: current.x - event.deltaX, y: current.y - event.deltaY };
              writeProjectViewCache(selectedProject.id, { pan: next });
              return next;
            });
          }}
          className="relative min-h-0 flex-1 touch-none overflow-hidden cursor-grab active:cursor-grabbing"
          style={{
            backgroundImage: 'radial-gradient(circle, rgba(255,255,255,0.09) 1px, transparent 1px)',
            backgroundPosition: `${canvasPan.x}px ${canvasPan.y}px`,
            backgroundSize: `${32 * canvasZoom}px ${32 * canvasZoom}px`,
          }}
        >
        {visibleAgents.length ? (
          <div
            data-id="project-canvas-world"
            className="pointer-events-none absolute inset-0 z-20 origin-top-left"
            style={{ transform: `translate(${canvasPan.x}px, ${canvasPan.y}px) scale(${canvasZoom})` }}
          >
            {visibleAgents.map((agent, index) => {
              const layout = layoutForAgent(agent, index);
              const cardMetrics = liveMetrics[shortPaneId(agent.paneId)];
              const cardShortId = shortPaneId(agent.paneId);
              const cardLatest = statuses[agent.paneId] || statuses[`${cardShortId}:main.0`] || statuses[cardShortId] || {};
              // Keep the loading sentinel continuous across the send handoff:
              // sendToAgent resolves once input reaches the terminal, before the
              // server's live status necessarily flips to working. The optimistic
              // reply is already `pending`, so it bridges that gap until the
              // current-reply poll replaces it with an authoritative terminal state.
              const observedReplyStatus = String(agentReplies[cardShortId]?.status || cardLatest?.status || agent.status || '').toLowerCase();
              const cardBusy = sendingAgentIds.has(cardShortId) || Boolean(cardMetrics?.working) || /running|working|thinking|streaming|pending|tool_use|tool_call|in_progress/.test(observedReplyStatus);
              return (
              <div
                key={agent.paneId}
                data-id={`project-canvas-node-${shortPaneId(agent.paneId)}`}
                className="pointer-events-auto absolute touch-none cursor-move"
                style={{ left: layout.x, top: layout.y, zIndex: selectedAgentIds.has(shortPaneId(agent.paneId)) ? 1000 : layout.z }}
                onPointerDown={(event) => beginAgentDrag(event, agent, index)}
                onPointerMove={moveAgent}
                onPointerUp={(event) => endAgentDrag(event, agent)}
                onPointerCancel={(event) => endAgentDrag(event, agent)}
              >
                  <ProjectAgentCard
                    agent={agent}
                    metrics={cardMetrics}
                    latest={cardLatest}
                    reply={agentReplies[cardShortId]}
                    optimisticQuestion={optimisticQuestionsRef.current[cardShortId]}
                    terminalOpen={terminalAgentIds.has(cardShortId)}
                    working={cardBusy}
                    teamId={teamId}
                selected={selectedAgentIds.has(shortPaneId(agent.paneId))}
                removable={Boolean(selectedProject.api_id)}
                width={layout.width}
                height={layout.height}
                onSelect={() => toggleAgentSelection(agent)}
                footer={(
                  <footer
                    data-id={`project-agent-card-footer-${shortPaneId(agent.paneId)}`}
                    onPointerDown={(event) => event.stopPropagation()}
                    onClick={(event) => event.stopPropagation()}
                    className="flex shrink-0 flex-col rounded-b-2xl border-t border-white/[0.08] bg-[#15161b]"
                  >
                    {(queuedAgentMessages[cardShortId] || []).length ? (
                      <div data-id={`project-agent-message-queue-${cardShortId}`} className="max-h-28 overflow-y-auto border-b border-white/[0.06] px-3 py-2">
                        <div data-id="project-agent-message-queue-item" className="relative whitespace-pre-wrap break-words rounded-lg bg-amber-500/[0.07] px-2.5 py-1.5 pr-16 text-[12px] leading-5 text-zinc-300">
                          <div data-id="project-agent-message-queue-actions" className="absolute right-1.5 top-1.5 flex items-center gap-0.5">
                            <button
                              type="button"
                              data-id={`project-agent-message-queue-edit-${cardShortId}`}
                              aria-label="编辑队列消息"
                              title="编辑队列消息"
                              onClick={() => editQueuedAgentMessage(agent)}
                              className="grid h-6 w-6 place-items-center rounded-md text-zinc-500 transition hover:bg-white/[0.08] hover:text-zinc-200"
                            >
                              <Pencil className="h-3.5 w-3.5" />
                            </button>
                            <button
                              type="button"
                              data-id={`project-agent-message-queue-delete-${cardShortId}`}
                              aria-label="删除队列消息"
                              title="删除队列消息"
                              onClick={() => deleteQueuedAgentMessage(agent)}
                              className="grid h-6 w-6 place-items-center rounded-md text-zinc-500 transition hover:bg-red-500/10 hover:text-red-400"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </button>
                          </div>
                          {(queuedAgentMessages[cardShortId] || []).flatMap((queued) => queued.attachments).length ? (
                            <div data-id="project-agent-message-queue-attachments" className="mb-1.5 flex gap-2 overflow-x-auto">
                              {(queuedAgentMessages[cardShortId] || []).flatMap((queued) => queued.attachments).map((attachment) => (
                                <div key={attachment.id} data-id={`project-agent-message-queue-attachment-${attachment.id}`} className="flex h-12 w-fit min-w-12 max-w-24 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-white/10 bg-white/[0.04]">
                                  {attachment.mediaType === 'image' && (attachment.previewURL || attachment.fileRef) ? (
                                    <span data-id="project-agent-message-queue-attachment-media" className="inline-flex h-full max-w-full items-center justify-center [&_[data-id=current-history-md-img]]:!m-0 [&_[data-id=current-history-md-img]]:!h-full [&_[data-id=current-history-md-img]]:!w-auto [&_[data-id=current-history-md-img]]:!max-w-24 [&_[data-id=current-history-md-img]]:object-contain">
                                      <MarkdownImg src={attachment.previewURL || attachment.fileRef || ''} alt={attachment.name} />
                                    </span>
                                  ) : (
                                    <span data-id="project-agent-message-queue-attachment-file" className="grid h-full w-full place-items-center"><FileText className="h-5 w-5 text-zinc-500" /></span>
                                  )}
                                </div>
                              ))}
                            </div>
                          ) : null}
                          <div
                            data-id="project-agent-message-queue-text"
                            onPointerDown={(event) => event.stopPropagation()}
                            className="cursor-text select-text"
                            style={{ userSelect: 'text', WebkitUserSelect: 'text' }}
                          >
                            {(queuedAgentMessages[cardShortId] || []).map((queued) => questionWithoutUploadedAttachments(queued.display)).filter(Boolean).join('\n')}
                          </div>
                        </div>
                      </div>
                    ) : null}
                    {(agentAttachments[cardShortId] || []).length ? (
                      <div data-id="project-agent-card-attachments" className="flex w-full gap-2 overflow-x-auto border-b border-white/[0.06] px-3 py-2">
                        {(agentAttachments[cardShortId] || []).map((attachment) => (
                          <div key={attachment.id} data-id={`project-agent-card-attachment-${attachment.id}`} className="group relative flex h-12 w-fit min-w-12 max-w-24 shrink-0 items-center justify-center overflow-visible rounded-lg border border-white/10 bg-white/[0.04]">
                            {attachment.mediaType === 'image' && attachment.previewURL ? (
                              <span data-id="project-agent-card-attachment-media" className="inline-flex h-full max-w-full items-center justify-center overflow-hidden rounded-lg [&_[data-id=current-history-md-img]]:!m-0 [&_[data-id=current-history-md-img]]:!h-full [&_[data-id=current-history-md-img]]:!w-auto [&_[data-id=current-history-md-img]]:!max-w-24 [&_[data-id=current-history-md-img]]:!rounded-lg [&_[data-id=current-history-md-img]]:object-contain">
                                <MarkdownImg src={attachment.previewURL} alt={attachment.name} />
                              </span>
                            ) : (
                              <span className="grid h-full w-full place-items-center"><FileText className="h-5 w-5 text-zinc-500" /></span>
                            )}
                            {attachment.status === 'uploading' ? <span className="absolute inset-x-1 bottom-1 rounded bg-black/70 text-center text-[9px] text-white">{attachment.progress}%</span> : null}
                            <button type="button" data-id="project-agent-card-attachment-remove" aria-label="Remove attachment" onClick={(event) => { event.stopPropagation(); removeAgentAttachment(cardShortId, attachment.id); }} className="absolute -right-1.5 -top-1.5 grid h-5 w-5 place-items-center rounded-full bg-zinc-800 text-zinc-200 shadow-md"><X className="h-3 w-3" /></button>
                          </div>
                        ))}
                      </div>
                    ) : null}
                    <div data-id={`project-agent-card-prompt-row-${cardShortId}`} className="flex min-h-10 w-full items-center px-3 py-2">
                    <textarea
                      data-id={`project-agent-prompt-input-${shortPaneId(agent.paneId)}`}
                      rows={1}
                      value={agentMessages[shortPaneId(agent.paneId)] || ''}
                      onChange={(event) => {
                        const id = shortPaneId(agent.paneId);
                        promptHistoryIndexRef.current[id] = null;
                        setAgentMessages((current) => ({ ...current, [id]: event.target.value }));
                      }}
                      onPaste={(event) => {
                        const files = Array.from(event.clipboardData?.files || []);
                        if (files.length) { event.preventDefault(); addAgentFiles(agent, files); }
                      }}
                      onKeyDown={(event) => {
                        if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                        if ((event.key === 'ArrowUp' || event.key === 'ArrowDown') && canNavigatePromptHistory(event.currentTarget, event.key === 'ArrowUp' ? 'up' : 'down')) {
                          const id = shortPaneId(agent.paneId);
                          const history = readPromptHistory(id);
                          if (history.length) {
                            event.preventDefault();
                            let index = promptHistoryIndexRef.current[id];
                            if (index == null) {
                              promptHistoryDraftRef.current[id] = event.currentTarget.value;
                              index = history.length;
                            }
                            index = event.key === 'ArrowUp' ? Math.max(0, index - 1) : Math.min(history.length, index + 1);
                            promptHistoryIndexRef.current[id] = index;
                            setAgentMessages((current) => ({ ...current, [id]: index === history.length ? (promptHistoryDraftRef.current[id] || '') : history[index] }));
                          }
                          return;
                        }
                        if (event.key === 'Enter' && !event.shiftKey) {
                          event.preventDefault();
                          void sendAgentMessage(agent);
                        }
                      }}
                      placeholder={t('projectMessagePlaceholder')}
                      autoFocus={selectedAgentIds.has(cardShortId)}
                      className="min-h-5 max-h-24 min-w-0 flex-1 resize-none overflow-y-auto bg-transparent text-[14px] leading-5 text-zinc-200 outline-none [field-sizing:content] placeholder:text-zinc-500/40"
                    />
                    </div>
                    <div data-id={`project-agent-card-prompt-actions-${cardShortId}`} className="flex h-10 min-h-10 w-full items-center justify-between px-3 pb-1">
                    <input
                      data-id={`project-agent-prompt-file-input-${cardShortId}`}
                      type="file"
                      multiple
                      className="hidden"
                      onChange={(event) => { if (event.target.files?.length) addAgentFiles(agent, event.target.files); event.target.value = ''; }}
                    />
                    <button
                      type="button"
                      data-id={`project-agent-prompt-attach-${cardShortId}`}
                      onClick={() => document.querySelector<HTMLInputElement>(`[data-id="project-agent-prompt-file-input-${cardShortId}"]`)?.click()}
                      aria-label="Attach"
                      className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-zinc-400 transition hover:bg-white/[0.08] hover:text-zinc-100"
                    >
                      <Paperclip className="h-4 w-4" strokeWidth={2.25} />
                    </button>
                    <div className="flex items-center">
                    {cardBusy && !(agentMessages[cardShortId] || '').trim() && !(agentAttachments[cardShortId] || []).length ? (
                      <button
                        type="button"
                        data-id={`project-agent-prompt-cancel-${shortPaneId(agent.paneId)}`}
                        onClick={() => { void cancelAgentMessage(agent); }}
                        disabled={cancelingAgentIds.has(shortPaneId(agent.paneId))}
                        className="grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-amber-400/20 bg-amber-400/10 text-amber-300 transition hover:bg-amber-400/15 disabled:opacity-50"
                        title={t('composerStop', { ns: 'chat', defaultValue: '停止' })}
                      >
                        {cancelingAgentIds.has(shortPaneId(agent.paneId)) ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : (
                          <span data-id={`project-agent-prompt-sending-${cardShortId}`} className="relative grid h-4 w-4 place-items-center">
                            <Loader2 className="absolute h-4 w-4 animate-spin text-amber-300" />
                            <Square className="h-2 w-2 fill-current" />
                          </span>
                        )}
                      </button>
                    ) : (
                      <button
                      type="button"
                      data-id={`project-agent-prompt-send-${shortPaneId(agent.paneId)}`}
                      onClick={() => { void sendAgentMessage(agent); }}
                      disabled={!(agentMessages[shortPaneId(agent.paneId)] || '').trim() && !(agentAttachments[cardShortId] || []).length}
                      className="grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-blue-400/20 bg-blue-500/15 text-blue-300 transition hover:bg-blue-500/25 disabled:border-transparent disabled:bg-white/[0.04] disabled:text-zinc-600"
                      title={cardBusy ? t('projectQueueMessage', { defaultValue: '加入队列' }) : t('send', { defaultValue: '发送' })}
                      aria-label={cardBusy ? t('projectQueueMessage', { defaultValue: '加入队列' }) : t('send', { defaultValue: '发送' })}
                    >
                      <SendHorizontal className="h-4 w-4" />
                    </button>
                    )}
                    </div>
                    </div>
                  </footer>
                )}
                onRemove={() => { void removeAgent(agent); }}
                    onOpenHistory={() => onOpenHistory(agent.paneId)}
                onToggleTerminal={() => setTerminalAgentIds((current) => {
                  const next = new Set(current);
                  if (next.has(cardShortId)) next.delete(cardShortId);
                  else next.add(cardShortId);
                  return next;
                })}
                onResizePointerDown={(event) => beginAgentResize(event, agent, index)}
                onResizePointerMove={resizeAgent}
                onResizePointerUp={(event) => endAgentResize(event, agent)}
              />
              </div>
            );})}
          </div>
        ) : (
          <div data-id="projects-agent-empty" className="flex min-h-[420px] flex-col items-center justify-center text-center text-zinc-600">
            <FolderKanban className="mb-3 h-10 w-10 opacity-40" />
            <p className="text-sm" data-id="projects-agent-empty-title">{t('projectNoAgents')}</p>
          </div>
        )}
        {shellPanel ? <div data-id="project-canvas-shell-panel" className="absolute inset-x-0 bottom-12 z-40 max-h-[260px] overflow-hidden [&_[data-id^=agent-stack-shell-terminal]]:!h-52">{shellPanel}</div> : null}
        <div data-id="project-canvas-footer" className="absolute inset-x-0 bottom-0 z-[70] h-12 border-t border-white/[0.08] bg-[#111216]/95 backdrop-blur">
        <div data-id="project-canvas-controls" className="absolute bottom-1.5 left-4 flex items-center gap-0.5 p-1">
          <button type="button" data-id="project-canvas-zoom-out" onClick={() => changeZoom(-0.1)} className="grid h-7 w-7 place-items-center rounded-md text-zinc-400 hover:bg-white/[0.08] hover:text-white" title={t('projectZoomOut')}><Minus className="h-3.5 w-3.5" /></button>
          <span data-id="project-canvas-zoom-value" className="w-9 text-center font-mono text-[9px] text-zinc-500">{Math.round(canvasZoom * 100)}%</span>
          <button type="button" data-id="project-canvas-zoom-in" onClick={() => changeZoom(0.1)} className="grid h-7 w-7 place-items-center rounded-md text-zinc-400 hover:bg-white/[0.08] hover:text-white" title={t('projectZoomIn')}><Plus className="h-3.5 w-3.5" /></button>
          <button type="button" data-id="project-canvas-reset" onClick={resetCanvasView} className="grid h-7 w-7 place-items-center rounded-md text-zinc-500 hover:bg-white/[0.08] hover:text-white" title={t('projectResetView')}><Maximize2 className="h-3.5 w-3.5" /></button>
        </div>
        {footerControls ? (
          <div data-id="project-canvas-footer-controls" className="absolute bottom-1.5 right-4 flex items-center gap-2 px-2 py-1">
            {footerControls}
          </div>
        ) : null}
        </div>
        {!dockOpen ? <div data-id="project-fab-wrap" className="absolute right-5 z-[60] flex flex-col items-end gap-2" style={{ bottom: 64 }}>
          <div
            data-id="project-fab-menu"
            className={cn(
              'flex origin-bottom-right flex-col items-end gap-2 transition-all duration-200',
              fabOpen ? 'pointer-events-auto translate-y-0 scale-100 opacity-100' : 'pointer-events-none translate-y-2 scale-95 opacity-0',
            )}
          >
            <button
              type="button"
              data-id="project-fab-create-agent"
              onClick={() => { setFabOpen(false); onCreateAgent(); }}
              className="flex h-9 items-center gap-2 rounded-full border border-white/[0.10] bg-[#202126] px-3 text-[12px] text-zinc-100 shadow-xl hover:bg-[#292a30]"
            >
              <UserPlus data-id="project-fab-create-agent-icon" className="h-4 w-4" />
              <span data-id="project-fab-create-agent-label">{t('projectCreateAgent')}</span>
            </button>
            <button
              type="button"
              data-id="project-fab-add-existing"
              onClick={() => { setFabOpen(false); openAddAgents(selectedProject); }}
              disabled={!selectedProject.api_id || availableAgents.length === 0}
              className="flex h-9 items-center gap-2 rounded-full border border-white/[0.10] bg-[#202126] px-3 text-[12px] text-zinc-100 shadow-xl hover:bg-[#292a30] disabled:cursor-not-allowed disabled:opacity-40"
            >
              <Users data-id="project-fab-add-existing-icon" className="h-4 w-4" />
              <span data-id="project-fab-add-existing-label">{t('projectAddExistingAgent')}</span>
            </button>
          </div>
          <button
            type="button"
            data-id="project-add-agent"
            onClick={() => setFabOpen((open) => !open)}
            className="grid h-10 w-10 place-items-center rounded-full bg-blue-600 text-white shadow-[0_8px_20px_rgba(37,99,235,0.3)] transition-transform hover:scale-105 hover:bg-blue-500 active:scale-95"
            title={t('projectAddAgent')}
            aria-label={t('projectAddAgent')}
            aria-expanded={fabOpen}
          >
            <Plus data-id="project-add-agent-icon" className={cn('h-5 w-5 transition-transform duration-200', fabOpen && 'rotate-45')} />
          </button>
        </div> : null}
        </div>

      </main>

      <AppModal open={createOpen} title={t('projectCreateTitle')} onClose={closeCreateProject} maxWidth="480px">
        <div data-id="project-create-modal" className="space-y-5">
          <div data-id="project-create-intro" className="flex items-start gap-3 rounded-xl border border-blue-500/15 bg-blue-500/[0.06] p-3.5">
            <span data-id="project-create-icon" className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-blue-500/15 text-blue-300">
              <FolderKanban className="h-[18px] w-[18px]" />
            </span>
            <p data-id="project-create-description" className="pt-0.5 text-[12px] leading-5 text-zinc-400">{t('projectCreateDescription')}</p>
          </div>

          <label data-id="project-create-name-wrap" className="block">
            <span data-id="project-create-name-label" className="mb-2 block text-[12px] font-medium text-zinc-300">
              {t('projectNameLabel')} <span className="text-red-400">*</span>
            </span>
            <input
              data-id="project-create-name"
              value={createName}
              onChange={(event) => { setCreateName(event.target.value); if (createError) setCreateError(''); }}
              onKeyDown={(event) => {
                if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                if (event.key === 'Enter') {
                  event.preventDefault();
                  void createProject();
                }
              }}
              placeholder={t('projectNamePlaceholder')}
              maxLength={64}
              autoComplete="off"
              autoFocus
              aria-invalid={Boolean(createError)}
              aria-describedby={createError ? 'project-create-error' : undefined}
              className={cn(
                'h-10 w-full rounded-xl border bg-black/20 px-3 text-[13px] text-zinc-100 outline-none transition-colors placeholder:text-zinc-600',
                createError ? 'border-red-500/50 focus:border-red-400' : 'border-white/[0.09] focus:border-blue-500/60 focus:ring-1 focus:ring-blue-500/15',
              )}
            />
            <span data-id="project-create-name-count" className="mt-1.5 block text-right font-mono text-[10px] text-zinc-600">{createName.length}/64</span>
          </label>

          <label data-id="project-create-description-wrap" className="block">
            <span data-id="project-create-description-label" className="mb-2 block text-[12px] font-medium text-zinc-300">{t('projectDescriptionLabel')}</span>
            <textarea
              data-id="project-create-description-input"
              value={createDescription}
              onChange={(event) => setCreateDescription(event.target.value)}
              placeholder={t('projectDescriptionPlaceholder')}
              maxLength={160}
              rows={3}
              className="w-full resize-none rounded-xl border border-white/[0.09] bg-black/20 px-3 py-2.5 text-[13px] leading-5 text-zinc-100 outline-none transition-colors placeholder:text-zinc-600 focus:border-blue-500/60 focus:ring-1 focus:ring-blue-500/15"
            />
            <span data-id="project-create-description-count" className="mt-1.5 block text-right font-mono text-[10px] text-zinc-600">{createDescription.length}/160</span>
          </label>

          {createError ? <p id="project-create-error" data-id="project-create-error" role="alert" className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-[12px] text-red-300">{createError}</p> : null}

          <div data-id="project-create-actions" className="flex justify-end gap-2 border-t border-white/[0.07] pt-4">
            <button type="button" data-id="project-create-cancel" onClick={closeCreateProject} disabled={creating} className="h-9 rounded-lg px-3.5 text-[12px] text-zinc-400 hover:bg-white/[0.05] hover:text-zinc-200 disabled:opacity-40">{t('cancel', { ns: 'common' })}</button>
            <button type="button" data-id="project-create-confirm" onClick={() => { void createProject(); }} disabled={!createName.trim() || creating} className="inline-flex h-9 min-w-[96px] items-center justify-center gap-2 rounded-lg bg-blue-500 px-4 text-[12px] font-medium text-white hover:bg-blue-400 disabled:cursor-not-allowed disabled:opacity-40">
              {creating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
              {creating ? t('projectSaving') : t('projectCreateConfirm')}
            </button>
          </div>
        </div>
      </AppModal>

      <AppModal open={addOpen} title={t('projectAddAgentTitle', { name: selectedProject.name })} onClose={() => setAddOpen(false)} maxWidth="620px">
        <div data-id="project-add-agent-modal" className="space-y-2">
          <label data-id="project-add-agent-search-wrap" className="mb-3 flex h-9 items-center gap-2 rounded-lg border border-white/[0.08] bg-black/20 px-3 focus-within:border-sky-500/40">
            <Search className="h-3.5 w-3.5 shrink-0 text-zinc-600" />
            <input
              data-id="project-add-agent-search"
              value={addSearch}
              onChange={(event) => setAddSearch(event.target.value)}
              placeholder={t('projectSearchAgent')}
              className="min-w-0 flex-1 bg-transparent text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600"
              autoFocus
            />
          </label>
          <div data-id="project-add-agent-results" className="max-h-[420px] space-y-2 overflow-y-auto pr-1">
          {filteredAvailableAgents.length ? filteredAvailableAgents.map((agent) => {
            const checked = selectedToAdd.has(agent.paneId);
            return (
              <button
                key={agent.paneId}
                type="button"
                data-id={`project-add-agent-${shortPaneId(agent.paneId)}`}
                onClick={() => setSelectedToAdd((current) => {
                  const next = new Set(current);
                  if (next.has(agent.paneId)) next.delete(agent.paneId); else next.add(agent.paneId);
                  return next;
                })}
                className={cn('flex w-full items-center gap-3 rounded-xl border p-3 text-left', checked ? 'border-blue-500/50 bg-blue-500/10' : 'border-white/[0.07] hover:bg-white/[0.04]')}
              >
                <AgentAvatar agentType={agent.agentType} title={agent.title || agent.paneId} dataId="project-add-agent-avatar" variant="stack" />
                <span className="min-w-0 flex-1"><span className="block truncate text-[13px] text-zinc-200">{agent.title}</span><span className="block font-mono text-[10px] text-zinc-600">{shortPaneId(agent.paneId)}</span></span>
                <span className={cn('grid h-5 w-5 place-items-center rounded border', checked ? 'border-blue-400 bg-blue-500 text-white' : 'border-white/15')}><Check className={cn('h-3 w-3', checked ? 'opacity-100' : 'opacity-0')} /></span>
              </button>
            );
          }) : <div data-id="project-add-agent-empty" className="py-10 text-center text-sm text-zinc-600">{t('projectNoAvailableAgents')}</div>}
          </div>
          {addError ? <p data-id="project-add-agent-error" className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-[12px] text-red-300">{addError}</p> : null}
          <div data-id="project-add-agent-actions" className="flex justify-end gap-2 pt-4">
            <button type="button" data-id="project-add-agent-cancel" onClick={() => setAddOpen(false)} className="h-8 rounded-lg px-3 text-[12px] text-zinc-400 hover:bg-white/[0.05]">{t('cancel', { ns: 'common' })}</button>
            <button type="button" data-id="project-add-agent-confirm" onClick={() => { void addSelectedAgents(); }} disabled={selectedToAdd.size === 0 || busy} className="h-8 rounded-lg bg-blue-500 px-3 text-[12px] font-medium text-white hover:bg-blue-400 disabled:opacity-40">{busy ? t('projectSaving') : t('projectAddSelected', { count: selectedToAdd.size })}</button>
          </div>
        </div>
      </AppModal>
      {dialogsNode}
    </section>
  );
}
