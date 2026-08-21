// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { ArrowDown, ArrowRight, ArrowUpCircle, BookOpen, Check, FileText, FolderKanban, GitBranch, Hand, History, LayoutGrid, Loader2, MessageCircle, Minus, MoreHorizontal, PanelLeftClose, PanelLeftOpen, Paperclip, Pencil, Pin, PinOff, Plus, RefreshCw, Search, SendHorizontal, Square, Trash2, UserPlus, Users, X, Zap } from 'lucide-react';
import { Trans, useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { sendToAgent } from '../../services/agentSend';
import { clearAgentSendTarget, getAgentSendTarget, setAgentSendTarget } from '../../services/agentSendTarget';
import { cn, copyToClipboard } from '../../lib/utils';
import { normalizeAgentType } from '../../lib/agentType';
import type { AgentLiveMetrics } from '../../lib/agentMetrics';
import { metricsFromCurrentReply } from '../../lib/agentMetrics';
import { ModelTag } from '../../lib/modelTag';
import { chatAttachmentMarkdown, replAttachmentMarkdown } from '../../lib/attachmentMarkdown';
import { appendPromptHistory, canNavigatePromptHistory, readPromptHistory } from '../../lib/promptHistory';
import { AppModal, useDialogs } from '../ui/Modal';
import AgentAvatar from '../AgentAvatar';
import CurrentHistoryView from '../chat/CurrentHistoryView';
import { MarkdownImg } from '../chat/history/shared/Markdown';
import TerminalView from '../terminal/TerminalView';
import MarkdownFileEditor from '../files/MarkdownFileEditor';
import UpdateAgentModal from '../layout/UpdateAgentModal';
import WechatBindModal from '../layout/WechatBindModal';
import ForkConfirmModal from '../layout/ForkConfirmModal';

const AgentDocRoleEditor = lazy(() => import('../layout/AgentDocRoleEditor'));
const RemoteAgentRoleEditor = lazy(() => import('./RemoteAgentRoleEditor'));

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
  remote?: boolean;
  instanceId?: string;
  instanceTeam?: string;
  remoteAgentId?: string;
  instanceOnline?: boolean;
}

export interface ProjectCreateAgentRequest {
  projectId: number | string;
  projectTemplate: string;
  onCreated: (paneId: string) => Promise<void>;
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
  project_template?: string;
  project_file?: string;
  project_rules?: string;
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
const PROJECT_AGENT_BODY_TABS_KEY = 'cicy_project_agent_body_tabs:v1';
const PROJECT_LIST_COLLAPSED_KEY = 'cicy_projects_list_collapsed';
type ProjectAgentBodyTab = 'history' | 'terminal' | 'role';
const shortPaneId = (value: string) => String(value || '').replace(/:.*$/, '');
const projectAgentIdentity = (agent: ProjectAgent) => agent.remote
  ? `remote:${String(agent.instanceId || agent.instanceTeam || '')}:${String(agent.remoteAgentId || shortPaneId(agent.paneId))}`
  : `local:${shortPaneId(agent.paneId)}`;
const projectAgentStatusSignature = (status: any) => [
  status?.status,
  status?.updated_at,
  status?.started_at,
  status?.turn_id,
  status?.history_id,
  status?.latest_question,
].map((value) => String(value || '')).join('|');
const projectAgentStatusIsTerminal = (status: any) => /^(completed|complete|done|idle|aborted|error|canceled|cancelled|failed|stopped)$/.test(String(status?.status || '').trim().toLowerCase());
const projectAgentStatusIsBusy = (status: any) => /^(thinking|working|running|streaming)$/.test(String(status?.status || '').trim().toLowerCase());
const projectAgentCanUseTerminal = (agent: ProjectAgent) => !agent.remote
  && String(agent.agentType || '').toLowerCase() !== 'cicy'
  && Boolean(agent.ttydSrc)
  && !agent.isApiOnly;
const readProjectAgentBodyTabs = (): Record<string, ProjectAgentBodyTab> => {
  try {
    const value = JSON.parse(localStorage.getItem(PROJECT_AGENT_BODY_TABS_KEY) || '{}');
    return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  } catch {
    return {};
  }
};
const readProjectAgentBodyTab = (agent: ProjectAgent): ProjectAgentBodyTab => {
  const value = readProjectAgentBodyTabs()[projectAgentIdentity(agent)];
  if (value !== 'history' && value !== 'terminal' && value !== 'role') return 'history';
  return value === 'terminal' && !projectAgentCanUseTerminal(agent) ? 'history' : value;
};
const persistProjectAgentBodyTab = (agent: ProjectAgent, tab: ProjectAgentBodyTab) => {
  try {
    localStorage.setItem(PROJECT_AGENT_BODY_TABS_KEY, JSON.stringify({
      ...readProjectAgentBodyTabs(),
      [projectAgentIdentity(agent)]: tab,
    }));
  } catch {}
};
const projectAgentCompleteness = (agent: ProjectAgent) => [
  agent.title,
  agent.agentType,
  agent.defaultModel,
  agent.workspace,
  agent.ttydSrc,
].filter(Boolean).length + (agent.status && agent.status !== 'offline' ? 2 : 0);
const cloudInstanceOnline = (instance: any) => {
  const seen = Date.parse(String(instance?.lastSeenAt || '').replace(' ', 'T') + 'Z');
  return instance?.status === 'online' && Number.isFinite(seen) && Date.now() - seen < 90_000;
};

const cloudRPC = async (agent: ProjectAgent, op: string, payload: Record<string, unknown> = {}) => {
  if (!agent.instanceId || !agent.remoteAgentId) throw new Error('Cloud Agent address is incomplete');
  const sent = await apiService.sendCiCyCloudMessage(agent.instanceId, agent.remoteAgentId, '', JSON.stringify({ op, ...payload }), 'rpc_request');
  const messageId = String(sent?.data?.message?.id || '');
  if (!messageId) throw new Error('Cloud RPC was not accepted');
  const deadline = Date.now() + 12_000;
  while (Date.now() < deadline) {
    const response = await apiService.getCiCyCloudMessageStatus(messageId);
    const replyText = String(response?.data?.reply?.text || '');
    if (replyText) {
      const envelope = JSON.parse(replyText);
      if (!envelope?.ok) throw new Error(envelope?.error || 'Cloud RPC failed');
      return envelope.data || {};
    }
    await new Promise((resolve) => window.setTimeout(resolve, 400));
  }
  throw new Error('Cloud RPC timed out');
};

const readProjectAgentQueue = (): Record<string, QueuedAgentMessage[]> => {
  try {
    const value = JSON.parse(localStorage.getItem(PROJECT_AGENT_QUEUE_KEY) || '{}');
    return value && typeof value === 'object' ? value : {};
  } catch { return {}; }
};
const persistProjectAgentQueue = (queue: Record<string, QueuedAgentMessage[]>) => {
  const persisted = Object.fromEntries(Object.entries(queue).map(([paneId, messages]) => [paneId, messages.map((message) => ({
    ...message,
    attachments: message.attachments.map(({ previewURL: _previewURL, ...attachment }) => attachment),
  }))]));
  try { localStorage.setItem(PROJECT_AGENT_QUEUE_KEY, JSON.stringify(persisted)); } catch {}
};
const questionWithoutUploadedAttachments = (value: unknown) => String(value || '')
  .replace(/!?\[[^\]]*\]\((?:file:\/\/)?\/?[^)\n]*\/cicy-ai\/assets\/[^)\n]+\)/gi, '')
  .replace(/!?\[[^\]]*\]\(\/?assets\/files\/[^)\n]+\)/gi, '')
  .replace(/\n{3,}/g, '\n\n')
  .trim();
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
      zoom: Math.min(2, Math.max(0.1, Number(value.zoom) || 1)),
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

function ProjectAgentCard({ agent, metrics, terminalOpen, working, teamId, selected, removable, footer, projectOptions = [], width, height, onSelect, onRemove, onMoveProject, onAddProject, onRestart, onUpdate, onBindWechat, onBindFeishu, onFork, onTerminalOpenChange, onResizePointerDown, onResizePointerMove, onResizePointerUp }: {
  agent: ProjectAgent;
  metrics?: AgentLiveMetrics;
  terminalOpen?: boolean;
  working: boolean;
  teamId?: string;
  selected: boolean;
  removable: boolean;
  footer?: ReactNode;
  projectOptions?: Array<{ id: number | string; name: string; checked: boolean }>;
  width: number;
  height: number;
  onSelect: () => void;
  onRemove: () => void;
  onMoveProject: (projectId: number | string) => void;
  onAddProject: (projectId: number | string) => void;
  onRestart?: () => void;
  onUpdate?: () => void;
  onBindWechat?: () => void;
  onBindFeishu?: () => void;
  onFork?: () => void;
  onTerminalOpenChange: (open: boolean) => void;
  onResizePointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizePointerMove: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizePointerUp: (event: ReactPointerEvent<HTMLDivElement>) => void;
}) {
  const { t } = useTranslation('workspace');
  const [menuOpen, setMenuOpen] = useState(false);
  const [menuDropUp, setMenuDropUp] = useState(false);
  const [projectSubmenu, setProjectSubmenu] = useState<'move' | 'add' | null>(null);
  const [activeBodyTab, setActiveBodyTab] = useState<ProjectAgentBodyTab>(() => readProjectAgentBodyTab(agent));
  const [identityCopied, setIdentityCopied] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const legacyStatus = String(agent.remote && agent.instanceOnline === false ? 'offline' : agent.status || 'idle').toLowerCase();
  const legacyUnhealthy = /failed|error|offline|stopped/.test(legacyStatus);
  const legacyBusy = /running|working|thinking|streaming/.test(legacyStatus);
  const identity = agent.remote ? agent.paneId : (teamId ? `${teamId}.${shortPaneId(agent.paneId)}` : shortPaneId(agent.paneId));
  const hasAgentActions = Boolean(onRestart || onUpdate || onBindWechat || onBindFeishu || onFork);

  useEffect(() => {
    if (activeBodyTab !== 'terminal' || terminalOpen || !projectAgentCanUseTerminal(agent)) return;
    onTerminalOpenChange(true);
  }, [activeBodyTab, agent, onTerminalOpenChange, terminalOpen]);

  useEffect(() => {
    if (!menuOpen) return;
    const close = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setMenuOpen(false);
    };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [menuOpen]);

  const toggleMenu = (event: ReactMouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    setMenuDropUp(window.innerHeight - rect.bottom < 390);
    setMenuOpen((value) => {
      if (value) setProjectSubmenu(null);
      return !value;
    });
  };

  const runMenuAction = (event: ReactMouseEvent<HTMLButtonElement>, action?: () => void) => {
    event.stopPropagation();
    if (!action) return;
    setMenuOpen(false);
    setProjectSubmenu(null);
    action();
  };

  const selectBodyTab = (tab: ProjectAgentBodyTab) => {
    if (tab === 'terminal' && !terminalOpen) onTerminalOpenChange(true);
    if (tab !== 'terminal' && terminalOpen) onTerminalOpenChange(false);
    setActiveBodyTab(tab);
    persistProjectAgentBodyTab(agent, tab);
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
      <div data-id="project-agent-card-body" className="flex min-h-0 flex-1 flex-col overflow-visible px-5 pb-4 pt-5">
      <div data-id="project-agent-card-header" className="flex items-start gap-3">
        <div data-id="project-agent-card-heading" className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            <h3 data-id="project-agent-card-title" className="truncate text-[18px] font-semibold tracking-[-0.01em] text-zinc-100">{agent.title || agent.paneId}</h3>
            {agent.agentType ? <span data-id="project-agent-card-agent-type" className="shrink-0 font-mono text-[12px] text-zinc-500">{agent.agentType}</span> : null}
            <span
              data-id="project-agent-card-model-slot"
              aria-hidden={!metrics?.model}
              className="inline-flex w-24 shrink-0 overflow-hidden"
            >
              {metrics?.model ? <ModelTag model={metrics.model} className="max-w-full" /> : null}
            </span>
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
            <div data-id="project-agent-card-menu" className={cn('absolute right-0 z-[220] min-w-[210px] overflow-visible rounded-xl border border-white/10 bg-[#1a1b20] p-1 shadow-2xl', menuDropUp ? 'bottom-9' : 'top-9')}>
              {onRestart ? (
                <button type="button" data-id={`project-agent-card-action-restart-${shortPaneId(agent.paneId)}`} onClick={(event) => runMenuAction(event, onRestart)} className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100">
                  <RefreshCw className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                  <span>{t('restart', { ns: 'teamPanel', defaultValue: '重启' })}</span>
                </button>
              ) : null}
              {onUpdate ? (
                <button type="button" data-id={`project-agent-card-action-update-${shortPaneId(agent.paneId)}`} onClick={(event) => runMenuAction(event, onUpdate)} className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100">
                  <ArrowUpCircle className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                  <span>{t('update', { ns: 'teamPanel', defaultValue: '更新' })}</span>
                </button>
              ) : null}
              {onBindWechat ? (
                <button type="button" data-id={`project-agent-card-action-wechat-${shortPaneId(agent.paneId)}`} onClick={(event) => runMenuAction(event, onBindWechat)} className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100">
                  <MessageCircle className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                  <span>{t('wechatBindTitle', { ns: 'teamPanel', defaultValue: '绑定微信' })}</span>
                </button>
              ) : null}
              {onBindFeishu ? (
                <button type="button" data-id={`project-agent-card-action-feishu-${shortPaneId(agent.paneId)}`} onClick={(event) => runMenuAction(event, onBindFeishu)} className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100">
                  <Zap className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                  <span>{t('feishuBindTitle', { ns: 'teamPanel', defaultValue: '飞书会话' })}</span>
                </button>
              ) : null}
              {onFork ? (
                <button type="button" data-id={`project-agent-card-action-fork-${shortPaneId(agent.paneId)}`} onClick={(event) => runMenuAction(event, onFork)} className="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100">
                  <GitBranch className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                  <span>{t('fork', { ns: 'teamPanel', defaultValue: 'Fork' })}</span>
                </button>
              ) : null}
              <button type="button" data-id="project-agent-card-move-to" onMouseEnter={() => setProjectSubmenu((current) => current === null ? null : 'move')} onClick={(event) => { event.stopPropagation(); setProjectSubmenu('move'); }} className={cn('flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 hover:bg-white/[0.06]', hasAgentActions && 'mt-1 border-t border-white/[0.07]', projectSubmenu === 'move' && 'bg-white/[0.06] text-zinc-100')}>
                <ArrowRight className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                <span className="min-w-0 flex-1">移动到</span>
                <span className="text-zinc-600">›</span>
              </button>
              {projectOptions.some((project) => !project.checked) ? <button type="button" data-id="project-agent-card-add-to" onMouseEnter={() => setProjectSubmenu((current) => current === null ? null : 'add')} onClick={(event) => { event.stopPropagation(); setProjectSubmenu('add'); }} className={cn('flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 hover:bg-white/[0.06]', projectSubmenu === 'add' && 'bg-white/[0.06] text-zinc-100')}>
                <Plus className="h-3.5 w-3.5 shrink-0 text-zinc-500" />
                <span className="min-w-0 flex-1">添加到</span>
                <span className="text-zinc-600">›</span>
              </button> : null}
              {projectSubmenu ? <div data-id={`project-agent-card-${projectSubmenu}-submenu`} className="absolute right-[calc(100%+4px)] top-0 min-w-[190px] overflow-hidden rounded-xl border border-white/10 bg-[#1a1b20] p-1 shadow-2xl">
                <div data-id="project-agent-card-submenu-label" className="px-3 pb-1 pt-1.5 text-[10px] font-semibold tracking-wide text-zinc-600">{projectSubmenu === 'move' ? '移动到' : '添加到'}</div>
                {(projectSubmenu === 'move' ? projectOptions : projectOptions.filter((project) => !project.checked)).map((project) => (
                  <button key={`${projectSubmenu}-${String(project.id)}`} type="button" data-id={`project-agent-card-${projectSubmenu}-project-${project.id}`} onClick={(event) => { event.stopPropagation(); setMenuOpen(false); setProjectSubmenu(null); if (projectSubmenu === 'move') onMoveProject(project.id); else onAddProject(project.id); }} className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 hover:bg-white/[0.06]">
                    <span className="min-w-0 flex-1 truncate">{project.name}</span>
                  </button>
                ))}
              </div> : null}
              {removable ? (
                <button
                  type="button"
                  data-id="project-agent-card-remove"
                  onClick={(event) => { event.stopPropagation(); setMenuOpen(false); onRemove(); }}
                  className="mt-1 flex w-full items-center gap-2 border-t border-white/[0.07] rounded-lg px-3 py-2 text-left text-[12px] text-red-300 hover:bg-red-500/10"
                >
                  <X className="h-3.5 w-3.5" />{t('projectRemoveAgent')}
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
        </div>
      </div>

      <div data-id="project-agent-card-metrics" className="mt-2.5 flex h-8 min-w-0 items-center gap-2 pb-2.5 font-mono text-[13px] text-zinc-500">
        <span data-id={`project-agent-card-status-${shortPaneId(agent.paneId)}`} className={cn('h-2.5 w-2.5 shrink-0 rounded-full', legacyUnhealthy ? 'bg-red-400' : legacyBusy || metrics?.working ? 'bg-amber-500' : metrics ? 'bg-emerald-700' : 'bg-zinc-700')} title={legacyStatus} />
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
        <span
          data-id="project-agent-card-context-slot"
          aria-hidden={!metrics || metrics.ctx <= 0}
          className="flex w-3 shrink-0 items-center"
        >
          {metrics && metrics.ctx > 0 ? (
            <span data-id="project-agent-card-context" className="flex items-center" title={`Context ${metrics.ctx}% / ${metrics.ctxK}k`}>
              <CtxRing pct={metrics.ctx} />
            </span>
          ) : null}
        </span>
        <span
          data-id="project-agent-card-cost-slot"
          aria-hidden={!metrics || metrics.cost <= 0}
          className="w-16 shrink-0 truncate text-sky-500"
        >
          {metrics && metrics.cost > 0 ? <span data-id="project-agent-card-cost">{fmtCost(metrics.cost)}</span> : null}
        </span>
      </div>
      <div
        data-id={`project-agent-card-tabs-${shortPaneId(agent.paneId)}`}
        role="tablist"
        className="flex h-9 shrink-0 items-end gap-5 border-b border-white/[0.08]"
      >
        {([
          ['history', '会话'],
          ...(agent.remote || String(agent.agentType || '').toLowerCase() === 'cicy' ? [] : [['terminal', 'Terminal']]),
          ['role', '角色'],
        ] as Array<[ProjectAgentBodyTab, string]>).map(([tab, label]) => {
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
      {activeBodyTab === 'history' ? (
        <div
          data-id={`project-agent-card-history-body-${shortPaneId(agent.paneId)}`}
          onPointerDown={(event) => event.stopPropagation()}
          onWheel={(event) => event.stopPropagation()}
          className="-mx-5 -mb-4 min-h-0 flex-1 overflow-hidden bg-[#0b0b0d]"
        >
          <CurrentHistoryView
            paneId={shortPaneId(agent.paneId)}
            open
            pollLive={selected}
            fullWidth
            leftAlignQuestions
            agentType={agent.agentType}
          />
        </div>
      ) : activeBodyTab === 'terminal' ? (
        <div data-id={`project-agent-card-terminal-body-${shortPaneId(agent.paneId)}`} onPointerDown={(event) => event.stopPropagation()} className="-mx-4 mt-3 min-h-0 flex-1 overflow-hidden rounded-lg bg-black">
          {terminalOpen && agent.ttydSrc ? <TerminalView ttydSrc={agent.ttydSrc} className="h-full w-full" /> : (
            <div data-id={`project-agent-card-terminal-loading-${shortPaneId(agent.paneId)}`} className="grid h-full place-items-center text-[11px] text-zinc-600"><Loader2 className="h-4 w-4 animate-spin" /></div>
          )}
        </div>
      ) : activeBodyTab === 'role' ? (
        <div data-id={`project-agent-card-role-body-${shortPaneId(agent.paneId)}`} onPointerDown={(event) => event.stopPropagation()} onWheel={(event) => event.stopPropagation()} onTouchStart={(event) => event.stopPropagation()} onTouchMove={(event) => event.stopPropagation()} className="-mx-5 flex min-h-0 flex-1 flex-col overflow-hidden bg-[#0b0b0d] overscroll-contain">
          <Suspense fallback={<div data-id="project-agent-card-role-loading" className="flex h-full items-center justify-center text-[11px] text-zinc-600">Loading…</div>}>
            {agent.remote ? (
              <RemoteAgentRoleEditor
                cacheKey={`${agent.instanceId || ''}:${agent.remoteAgentId || ''}`}
                agentType={agent.agentType}
                load={() => cloudRPC(agent, 'persona')}
                save={(field, content) => cloudRPC(agent, 'persona_save', { [field]: content })}
              />
            ) : <AgentDocRoleEditor paneId={agent.paneId} className="min-h-0 flex-1" />}
          </Suspense>
        </div>
      ) : null}
      </div>
      {!selected || activeBodyTab !== 'role' ? (
        <div
          data-id={`project-agent-card-footer-slot-${shortPaneId(agent.paneId)}`}
          aria-hidden={!(selected || working) || activeBodyTab === 'role'}
          className={cn('shrink-0', (!(selected || working) || activeBodyTab === 'role') && 'invisible pointer-events-none')}
        >
          {footer}
        </div>
      ) : null}
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

export default function ProjectsPanel({ agents, statuses = {}, topRightControls, footerControls, shellPanel, dockOpen = false, activeAgentId = '', masterPaneId = 'w-1001', onActiveAgentChange = () => {}, onOpenAgent, onCreateAgent = () => {}, onOpenGuidance: _onOpenGuidance = () => {}, onOpenHistory: _onOpenHistory = () => {}, onActiveProjectChange = () => {}, onAgentsRefresh = () => {}, onOpenAgentFile = () => {} }: {
  agents: ProjectAgent[];
  statuses?: Record<string, any>;
  topRightControls?: ReactNode;
  footerControls?: ReactNode;
  shellPanel?: ReactNode;
  dockOpen?: boolean;
  activeAgentId?: string;
  masterPaneId?: string;
  onActiveAgentChange?: (paneId: string) => void;
  onOpenAgent: (paneId: string) => void;
  onCreateAgent?: (request: ProjectCreateAgentRequest) => void;
  onOpenGuidance?: (paneId: string) => void;
  onOpenHistory?: (paneId: string) => void;
  onActiveProjectChange?: (project: { id: number | string; name: string }) => void;
  onAgentsRefresh?: () => void | Promise<void>;
  onOpenAgentFile?: (paneId: string, relativePath: string) => void;
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
  const [definitionProject, setDefinitionProject] = useState<AgentProject | null>(null);
  const [definitionRules, setDefinitionRules] = useState('');
  const [definitionGlobalRules, setDefinitionGlobalRules] = useState('');
  const [definitionGlobalPath, setDefinitionGlobalPath] = useState('~/cicy-ai/memory/global.md');
  const [definitionFile, setDefinitionFile] = useState<'global' | 'project'>('project');
  const [definitionLoading, setDefinitionLoading] = useState(false);
  const [definitionSaving, setDefinitionSaving] = useState(false);
  const [creating, setCreating] = useState(false);
  const [projectMenuId, setProjectMenuId] = useState<string>('');
  const [projectMenuAnchor, setProjectMenuAnchor] = useState<string>('');
  const [updateTarget, setUpdateTarget] = useState<ProjectAgent | null>(null);
  const [wechatTarget, setWechatTarget] = useState<ProjectAgent | null>(null);
  const [feishuTarget, setFeishuTarget] = useState<ProjectAgent | null>(null);
  const [forkTarget, setForkTarget] = useState<ProjectAgent | null>(null);
  const [projectListCollapsed, setProjectListCollapsed] = useState(() => localStorage.getItem(PROJECT_LIST_COLLAPSED_KEY) === '1');
  const setProjectListVisibility = (collapsed: boolean) => {
    setProjectListCollapsed(collapsed);
    localStorage.setItem(PROJECT_LIST_COLLAPSED_KEY, collapsed ? '1' : '0');
  };
  const [addOpen, setAddOpen] = useState(false);
  const [fabOpen, setFabOpen] = useState(false);
  const [addSearch, setAddSearch] = useState('');
  const [addAgentType, setAddAgentType] = useState('all');
  const [addInstanceId, setAddInstanceId] = useState('local');
  const [cloudInstances, setCloudInstances] = useState<any[]>([]);
  const [cloudDirectoryAgents, setCloudDirectoryAgents] = useState<any[]>([]);
  const [addError, setAddError] = useState('');
  const [selectedToAdd, setSelectedToAdd] = useState<Set<string>>(new Set());
  const addResultsRef = useRef<HTMLDivElement>(null);
  const [selectedAgentIds, setSelectedAgentIds] = useState<Set<string>>(new Set());
  const agentMembershipKey = agents.map((agent) => agent.paneId).sort().join('|');
  const [agentMessages, setAgentMessages] = useState<Record<string, string>>({});
  const [agentAttachments, setAgentAttachments] = useState<Record<string, ProjectAttachment[]>>({});
  const [agentReplies, setAgentReplies] = useState<Record<string, any>>({});
  const [queuedAgentMessages, setQueuedAgentMessagesState] = useState<Record<string, QueuedAgentMessage[]>>(readProjectAgentQueue);
  const [sendingAgentIds, setSendingAgentIds] = useState<Set<string>>(new Set());
  const [cancelingAgentIds, setCancelingAgentIds] = useState<Set<string>>(new Set());
  const [canceledAgentIds, setCanceledAgentIds] = useState<Set<string>>(new Set());
  const [terminalAgentIds, setTerminalAgentIds] = useState<Set<string>>(new Set());
  const [agentLayouts, setAgentLayouts] = useState<Record<string, ProjectAgentLayout>>({});
  const [layoutReadyProjectId, setLayoutReadyProjectId] = useState('');
  const [layoutVisibilityRevision, setLayoutVisibilityRevision] = useState(0);
  const [canvasPan, setCanvasPan] = useState({ x: 60, y: 60 });
  const [canvasZoom, setCanvasZoom] = useState(1);
  const [wheelZoomActive, setWheelZoomActive] = useState(false);
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
  const cancelReleaseTimersRef = useRef<Record<string, number>>({});

  useEffect(() => {
    if (addResultsRef.current) addResultsRef.current.scrollTop = 0;
  }, [addSearch]);

  const setQueuedAgentMessages = useCallback((update: (current: Record<string, QueuedAgentMessage[]>) => Record<string, QueuedAgentMessage[]>) => {
    setQueuedAgentMessagesState((current) => {
      const next = update(current);
      persistProjectAgentQueue(next);
      return next;
    });
  }, []);
  const agentDragRef = useRef<{ id: string; pointerId: number; startX: number; startY: number; originX: number; originY: number; moved: boolean } | null>(null);
  const agentResizeRef = useRef<{ id: string; pointerId: number; startX: number; startY: number; originWidth: number; originHeight: number } | null>(null);
  const panDragRef = useRef<{ pointerId: number; startX: number; startY: number; originX: number; originY: number } | null>(null);
  const optimisticQuestionsRef = useRef<Record<string, string>>({});

  useEffect(() => () => {
    Object.values(cancelReleaseTimersRef.current).forEach((timer) => window.clearTimeout(timer));
  }, []);

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
          project_template: String(group.project_template || ''),
          project_file: String(group.project_file || ''),
          project_rules: String(group.project_rules || ''),
        };
      }));
    } catch (cause: any) {
      setError(cause?.message || t('projectLoadFailed'));
    } finally {
      if (showLoading) setLoading(false);
    }
  }, [t]);

  useEffect(() => { void load(); }, [load, agentMembershipKey]);

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

  useEffect(() => {
    let cancelled = false;
    const loadCloudDirectory = async () => {
      try {
        const [instancesResponse, agentsResponse] = await Promise.all([
          apiService.getCiCyCloudInstances(),
          apiService.getCiCyCloudAgents(),
        ]);
        if (cancelled) return;
        setCloudInstances(Array.isArray(instancesResponse?.data?.instances) ? instancesResponse.data.instances : []);
        setCloudDirectoryAgents(Array.isArray(agentsResponse?.data?.agents) ? agentsResponse.data.agents : []);
      } catch {
        if (!cancelled) { setCloudInstances([]); setCloudDirectoryAgents([]); }
      }
    };
    void loadCloudDirectory();
    const timer = window.setInterval(() => { void loadCloudDirectory(); }, 5000);
    return () => { cancelled = true; window.clearInterval(timer); };
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
  useEffect(() => {
    onActiveProjectChange({ id: selectedProject.id, name: selectedProject.name });
  }, [onActiveProjectChange, selectedProject.id, selectedProject.name]);
  const cloudProjectAgents = useMemo<ProjectAgent[]>(() => {
    const instanceById = new Map(cloudInstances.map((instance) => [String(instance.instanceId), instance]));
    return cloudDirectoryAgents.flatMap((cloudAgent) => {
      const instance = instanceById.get(String(cloudAgent.instanceId));
      const instanceTeam = String(instance?.teamId || cloudAgent.teamId || '');
      const remoteAgentId = String(cloudAgent.agentId || '');
      if (!instance || !instanceTeam || !remoteAgentId || instanceTeam === teamId) return [];
      const online = cloudInstanceOnline(instance);
      return [{
        paneId: `${instanceTeam}.${remoteAgentId}`,
        title: String(cloudAgent.title || remoteAgentId),
        agentType: String(cloudAgent.agentType || ''),
        status: online ? String(cloudAgent.status || 'idle') : 'offline',
        defaultModel: String(cloudAgent.defaultModel || ''),
        machineLabel: instanceTeam,
        isApiOnly: true,
        remote: true,
        instanceId: String(instance.instanceId),
        instanceTeam,
        remoteAgentId,
        instanceOnline: online,
      }];
    });
  }, [cloudDirectoryAgents, cloudInstances, teamId]);
  const allAgents = useMemo(() => {
    const unique = new Map<string, ProjectAgent>();
    for (const agent of [...agents, ...cloudProjectAgents]) {
      const key = projectAgentIdentity(agent);
      const existing = unique.get(key);
      if (!existing || projectAgentCompleteness(agent) > projectAgentCompleteness(existing)) unique.set(key, agent);
    }
    return [...unique.values()];
  }, [agents, cloudProjectAgents]);
  const memberIds = useMemo(() => new Set(selectedProject.pane_ids.map(shortPaneId)), [selectedProject.pane_ids]);
  const visibleAgents = allAgents.filter((agent) => memberIds.has(shortPaneId(agent.paneId)));
  const assignedAgentIds = useMemo(() => new Set(groups.flatMap((group) => group.pane_ids.map(shortPaneId))), [groups]);
  const availableAgents = allAgents.filter((agent) => !assignedAgentIds.has(shortPaneId(agent.paneId)));
  const availableAgentsForInstance = availableAgents.filter((agent) => addInstanceId === 'local' ? !agent.remote : agent.instanceId === addInstanceId);
  const availableAgentTypesForInstance = [...new Set(availableAgentsForInstance.map((agent) => String(agent.agentType || '').trim().toLowerCase()).filter(Boolean))].sort();
  const normalizedAddSearch = addSearch.trim().toLowerCase();
  const filteredAvailableAgents = availableAgentsForInstance.filter((agent) => addAgentType === 'all' || String(agent.agentType || '').trim().toLowerCase() === addAgentType).filter((agent) => !normalizedAddSearch || [
    agent.title,
    agent.paneId,
    agent.agentType,
    agent.defaultModel,
    agent.workspace,
  ].some((value) => String(value || '').toLowerCase().includes(normalizedAddSearch)));
  const addInstanceOptions = [
    { id: 'local', label: t('projectLocalInstance', { defaultValue: '本地' }), online: true, count: availableAgents.filter((agent) => !agent.remote).length },
    ...cloudInstances.filter((instance) => String(instance.teamId || '') !== teamId).map((instance) => ({
      id: String(instance.instanceId || ''),
      label: String(instance.teamId || instance.instanceId || ''),
      online: cloudInstanceOnline(instance),
      count: availableAgents.filter((agent) => agent.remote && agent.instanceId === String(instance.instanceId || '')).length,
    })),
  ];
  const selectedAddInstance = addInstanceOptions.find((instance) => instance.id === addInstanceId) || addInstanceOptions[0];
  const selectedToAddHasOfflineAgent = [...selectedToAdd].some((paneId) => {
    const agent = allAgents.find((item) => item.paneId === paneId);
    return Boolean(agent?.remote && !agent.instanceOnline);
  });
  const paneMembershipKey = selectedProject.pane_ids.map(shortPaneId).sort().join('|');
  const visibleAgentKey = visibleAgents.map((agent) => shortPaneId(agent.paneId)).sort().join('|');
  useEffect(() => {
    const receiveCloudReply = (event: Event) => {
      const detail = (event as CustomEvent)?.detail || {};
      if (String(detail.kind || '') !== 'agent_reply') return;
      const remoteAgentId = String(detail.senderAgentId || '').trim();
      if (!remoteAgentId) return;
      const agent = visibleAgents.find((item) => item.remote && item.remoteAgentId === remoteAgentId
        && (!detail.senderInstanceId || item.instanceId === String(detail.senderInstanceId)));
      if (!agent) return;
      const id = shortPaneId(agent.paneId);
      const answer = String(detail.text || '');
      setAgentReplies((current) => ({
        ...current,
        [id]: {
          ...(current[id] || {}),
          question: optimisticQuestionsRef.current[id] || current[id]?.question || '',
          answer,
          status: 'completed',
          completed_at: new Date(Number(detail.receivedAtMs) || Date.now()).toISOString(),
        },
      }));
      delete optimisticQuestionsRef.current[id];
    };
    window.addEventListener('cicy:cloud-reply', receiveCloudReply);
    return () => window.removeEventListener('cicy:cloud-reply', receiveCloudReply);
  }, [visibleAgentKey]);

  // A local send installs an optimistic `pending` reply before the terminal
  // status has advanced. Keep that bridge while poll_data still matches the
  // pre-send snapshot, then discard it as soon as a newer terminal snapshot
  // arrives. Without this reconciliation the project footer stays in its stop /
  // loading state forever after a completed Codex turn.
  useEffect(() => {
    setAgentReplies((current) => {
      let next = current;
      for (const [id, reply] of Object.entries(current)) {
        const baseline = String(reply?._status_baseline || '');
        if (!baseline) continue;
        const live = statuses[`${id}:main.0`] || statuses[id];
        const signature = projectAgentStatusSignature(live);
        if (!live || signature === baseline || !projectAgentStatusIsTerminal(live)) continue;
        const reconciled = { ...reply, ...live, status: live.status, complete: true };
        delete reconciled._status_baseline;
        if (next === current) next = { ...current };
        next[id] = reconciled;
        delete optimisticQuestionsRef.current[id];
      }
      return next;
    });
  }, [statuses]);

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
      const zoom = Math.min(1, Math.max(0.1, Math.min((viewportWidth - margin * 2) / Math.max(1, maxX - minX), (viewportHeight - margin * 2) / Math.max(1, maxY - minY))));
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
          // Never let a late server response move cards that were already
          // painted from the local cache (or dragged while this request was in
          // flight). Server coordinates only fill agents missing from the
          // latest cache, so hydration cannot cause a second layout pass.
          const latestCached = readProjectViewCache(selectedProject.id);
          const stableNext = latestCached ? { ...next, ...latestCached.layouts } : next;
          setAgentLayouts(stableNext);
          setLayoutReadyProjectId(String(selectedProject.id));
          setLayoutVisibilityRevision((value) => value + 1);
          writeProjectViewCache(selectedProject.id, { layouts: stableNext });
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
    setAddInstanceId('local');
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

  const openProjectDefinition = (project: AgentProject) => {
    setProjectMenuId('');
    setDefinitionProject(project);
    setDefinitionRules(String(project.project_rules || ''));
    setDefinitionFile('project');
    setDefinitionLoading(true);
    void apiService.getMemoryTemplate('global').then((response: any) => {
      setDefinitionGlobalRules(String(response?.data?.content || ''));
      setDefinitionGlobalPath(String(response?.data?.path || '~/cicy-ai/memory/global.md'));
    }).catch((cause: any) => {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: cause?.message || '加载 global.md 失败' }));
    }).finally(() => setDefinitionLoading(false));
  };

  const saveProjectDefinition = async () => {
    if (!definitionProject) return;
    setDefinitionSaving(true);
    try {
      await apiService.saveMemoryTemplate('global', '', definitionGlobalRules);
      await apiService.updateGroup(definitionProject.api_id, { project_rules: definitionRules });
      await load(false);
      setDefinitionProject(null);
    } catch (cause: any) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: cause?.message || '保存角色定义失败' }));
    } finally {
      setDefinitionSaving(false);
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
    if (selectedToAddHasOfflineAgent) {
      setAddError(t('projectOfflineAgentCannotAdd', { defaultValue: '离线 Instance 的 Agent 无法添加，请取消选择后重试。' }));
      return;
    }
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

  const restartAgent = async (agent: ProjectAgent) => {
    if (agent.remote) return;
    const id = shortPaneId(agent.paneId);
    const title = agent.title || id;
    const ok = await confirm({
      body: <Trans i18nKey="confirmRestart" ns="teamPanel" values={{ title }} components={{ strong: <span className="font-medium text-zinc-100" /> }} />,
    });
    if (!ok) return;
    try {
      await apiService.restartPane(id);
      window.dispatchEvent(new CustomEvent('show-toast', {
        detail: t('toastRestarting', { ns: 'teamPanel', title, defaultValue: `${title} 正在重启…` }),
      }));
      await onAgentsRefresh();
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', {
        detail: t('toastRestartFailed', { ns: 'teamPanel', title, defaultValue: `${title} 重启失败` }),
      }));
    }
  };

  const changeAgentProject = async (agent: ProjectAgent, projectId: number | string, mode: 'move' | 'add') => {
    const project = projects.find((item) => String(item.id) === String(projectId));
    if (!project?.api_id) return;
    const id = shortPaneId(agent.paneId);
    try {
      await apiService.addGroupPane(project.api_id, agent.paneId, mode);
      setGroups((current) => current.map((group) => {
        if (String(group.id) === String(project.id)) {
          if (group.pane_ids.some((paneId) => shortPaneId(paneId) === id)) return group;
          return { ...group, pane_ids: [...group.pane_ids, agent.paneId], pane_count: group.pane_count + 1 };
        }
        if (mode === 'move' && String(group.id) !== String(project.id)) {
          if (!group.pane_ids.some((paneId) => shortPaneId(paneId) === id)) return group;
          return { ...group, pane_ids: group.pane_ids.filter((paneId) => shortPaneId(paneId) !== id), pane_count: Math.max(0, group.pane_count - 1) };
        }
        return group;
      }));
      window.dispatchEvent(new CustomEvent('show-toast', {
        detail: mode === 'add' ? `已添加到「${project.name}」` : `已移动到「${project.name}」`,
      }));
    } catch (cause: any) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: cause?.response?.data?.error || cause?.message || t('projectSaving') }));
    }
  };

  const toggleAgentSelection = (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    setSelectedAgentIds(new Set([id]));
    setAgentSendTarget({ source: 'project', paneId: agent.paneId });
    if (!agent.remote) onActiveAgentChange(id);
  };

  useEffect(() => {
    const id = Array.from(selectedAgentIds)[0] || '';
    const agent = visibleAgents.find((item) => shortPaneId(item.paneId) === id);
    if (agent) {
      setAgentSendTarget({ source: 'project', paneId: agent.paneId });
      return;
    }
    if (getAgentSendTarget().source === 'project') clearAgentSendTarget('project');
  }, [selectedAgentIds, visibleAgentKey]);

  useEffect(() => {
    const id = shortPaneId(activeAgentId);
    if (!id || !visibleAgents.some((agent) => shortPaneId(agent.paneId) === id)) return;
    setSelectedAgentIds((current) => current.size === 1 && current.has(id) ? current : new Set([id]));
  }, [activeAgentId, visibleAgentKey]);

  useEffect(() => {
    const routeToSelectedPrompt = (event: Event) => {
      const routed = event as CustomEvent<{ paneId?: string; text?: string }>;
      const text = String(routed.detail?.text || '').trim();
      if (!text) return;
      routed.preventDefault();
      const id = Array.from(selectedAgentIds)[0] || '';
      if (!id) {
        window.dispatchEvent(new CustomEvent('show-toast', {
          detail: t('projectSelectAgentFirst', { defaultValue: '请先选择 Agent' }),
        }));
        return;
      }
      setAgentMessages((current) => ({
        ...current,
        [id]: [current[id], text].filter(Boolean).join('\n'),
      }));
      window.requestAnimationFrame(() => {
        document.querySelector<HTMLTextAreaElement>(`[data-id="project-agent-prompt-input-${id}"]`)?.focus({ preventScroll: true });
      });
    };
    window.addEventListener('cicy:route-agent-prompt', routeToSelectedPrompt as EventListener);
    return () => window.removeEventListener('cicy:route-agent-prompt', routeToSelectedPrompt as EventListener);
  }, [selectedAgentIds, t]);

  useLayoutEffect(() => {
    const fillTargetPrompt = (event: Event) => {
      const detail = (event as CustomEvent<{ paneId?: string; text?: string }>).detail || {};
      const id = shortPaneId(String(detail.paneId || ''));
      const text = String(detail.text || '').trim();
      if (!id || !text || !visibleAgents.some((agent) => shortPaneId(agent.paneId) === id)) return;
      setSelectedAgentIds(new Set([id]));
      setAgentMessages((current) => ({
        ...current,
        [id]: [current[id], text].filter(Boolean).join('\n'),
      }));
      window.requestAnimationFrame(() => {
        document.querySelector<HTMLTextAreaElement>(`[data-id="project-agent-prompt-input-${id}"]`)?.focus({ preventScroll: true });
      });
    };
    window.addEventListener('cicy:fill-project-composer', fillTargetPrompt as EventListener);
    return () => window.removeEventListener('cicy:fill-project-composer', fillTargetPrompt as EventListener);
  }, [visibleAgentKey]);

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
    // Keep new prompts queued briefly while Ctrl+C settles and the CLI restores
    // its input prompt. Sending immediately can type the text before the shell is
    // ready and have the accompanying Enter swallowed by the cancel transition.
    if (canceledAgentIds.has(id)) return true;
    const status = String(agent.status || '').toLowerCase();
    return sendingAgentIds.has(id) || projectAgentStatusIsBusy({ status });
  };

  const deliverAgentMessage = async (agent: ProjectAgent, message: string, displayQuestion: string, previousReply: any, sentAttachments: ProjectAttachment[] = [], restoreText = '') => {
    const id = shortPaneId(agent.paneId);
    const statusBeforeSend = statuses[agent.paneId] || statuses[`${id}:main.0`] || statuses[id];
    const statusBaseline = projectAgentStatusSignature(statusBeforeSend);
    setCanceledAgentIds((current) => {
      if (!current.has(id)) return current;
      const next = new Set(current);
      next.delete(id);
      return next;
    });
    setSendingAgentIds((current) => new Set(current).add(id));
    optimisticQuestionsRef.current[id] = displayQuestion;
    setAgentReplies((current) => ({
      ...current,
      [id]: {
        question: displayQuestion,
        items: [],
        answer: '',
        thinking: '',
        status: 'pending',
        started_at: new Date().toISOString(),
        _status_baseline: statusBaseline,
      },
    }));
    window.dispatchEvent(new CustomEvent('cicy:current-history-refresh', { detail: { paneId: id, text: displayQuestion } }));
    try {
      if (agent.remote) {
        if (!agent.instanceOnline) throw new Error(`${agent.instanceTeam || 'Instance'} is offline`);
        if (!agent.instanceId || !agent.remoteAgentId) throw new Error('Cloud Agent address is incomplete');
        await apiService.sendCiCyCloudMessage(agent.instanceId, agent.remoteAgentId, '', message);
      } else {
        await sendToAgent(agent.paneId, message, { submit: true, agentType: agent.agentType, fromComposer: true });
      }
      sentAttachments.forEach((item) => { if (item.previewURL) URL.revokeObjectURL(item.previewURL); });
    } catch (cause: any) {
      window.dispatchEvent(new CustomEvent('cicy:current-history-cancel-optimistic', { detail: { paneId: id } }));
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
    for (const agent of allAgents) {
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
  }, [allAgents, agentReplies, queuedAgentMessages, sendingAgentIds, canceledAgentIds, statuses]);

  const cancelAgentMessage = async (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    if (cancelingAgentIds.has(id)) return;
    setCancelingAgentIds((current) => new Set(current).add(id));
    try {
      if (agent.remote) await cloudRPC(agent, 'cancel');
      else if (agent.agentType === 'cicy') await apiService.cancelCicyReply(agent.paneId);
      else await apiService.sendKeys(agent.paneId, 'C-c');
      delete optimisticQuestionsRef.current[id];
      setSendingAgentIds((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
      setAgentReplies((current) => ({
        ...current,
        [id]: {
          ...(current[id] || {}),
          status: 'canceled',
          complete: true,
          updated_at: new Date().toISOString(),
        },
      }));
      setCanceledAgentIds((current) => new Set(current).add(id));
      window.clearTimeout(cancelReleaseTimersRef.current[id]);
      cancelReleaseTimersRef.current[id] = window.setTimeout(() => {
        setCanceledAgentIds((current) => {
          const next = new Set(current);
          next.delete(id);
          return next;
        });
        delete cancelReleaseTimersRef.current[id];
      }, 1200);
    } catch (cause: any) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: cause?.message || t('projectCancelFailed', { defaultValue: '取消失败' }) }));
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
    const height = Math.max(240, resize.originHeight + (event.clientY - resize.startY) / canvasZoom);
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
      height: Math.max(240, resize.originHeight + (event.clientY - resize.startY) / canvasZoom),
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
    const next = Math.min(2, Math.max(0.1, Number((current + delta).toFixed(2))));
    writeProjectViewCache(selectedProject.id, { zoom: next });
    return next;
  });
  const zoomCanvasAt = (clientX: number, clientY: number, delta: number) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const rect = canvas.getBoundingClientRect();
    const pointerX = clientX - rect.left;
    const pointerY = clientY - rect.top;
    setCanvasZoom((currentZoom) => {
      const nextZoom = Math.min(2, Math.max(0.1, Number((currentZoom + delta).toFixed(2))));
      if (nextZoom === currentZoom) return currentZoom;
      setCanvasPan((currentPan) => {
        const worldX = (pointerX - currentPan.x) / currentZoom;
        const worldY = (pointerY - currentPan.y) / currentZoom;
        const nextPan = {
          x: pointerX - worldX * nextZoom,
          y: pointerY - worldY * nextZoom,
        };
        writeProjectViewCache(selectedProject.id, { zoom: nextZoom, pan: nextPan });
        return nextPan;
      });
      return nextZoom;
    });
  };
  const resetCanvasView = () => {
    const canvas = canvasRef.current;
    if (!canvas || !visibleAgents.length) return;
    const gap = 24;
    const columnCount = Math.max(1, Math.ceil(Math.sqrt(visibleAgents.length)));
    let cursorX = 0;
    let cursorY = 0;
    let rowHeight = 0;
    const arrangedLayouts = { ...agentLayouts };
    const layouts = visibleAgents.map((agent, index) => {
      const current = layoutForAgent(agent, index);
      if (index > 0 && index % columnCount === 0) {
        cursorX = 0;
        cursorY += rowHeight + gap;
        rowHeight = 0;
      }
      const layout = { ...current, x: cursorX, y: cursorY };
      arrangedLayouts[shortPaneId(agent.paneId)] = layout;
      cursorX += current.width + gap;
      rowHeight = Math.max(rowHeight, current.height);
      return layout;
    });
    const minX = Math.min(...layouts.map((layout) => layout.x));
    const minY = Math.min(...layouts.map((layout) => layout.y));
    const maxX = Math.max(...layouts.map((layout) => layout.x + layout.width));
    const maxY = Math.max(...layouts.map((layout) => layout.y + layout.height));
    const margin = 40;
    const availableWidth = Math.max(1, canvas.clientWidth - margin * 2);
    const availableHeight = Math.max(1, canvas.clientHeight - margin * 2);
    const zoom = Number(Math.min(1, Math.max(0.1, Math.min(
      availableWidth / Math.max(1, maxX - minX),
      availableHeight / Math.max(1, maxY - minY),
    ))).toFixed(2));
    const pan = { x: margin - minX * zoom, y: margin - minY * zoom };
    setAgentLayouts(arrangedLayouts);
    setCanvasPan(pan);
    setCanvasZoom(zoom);
    writeProjectViewCache(selectedProject.id, { layouts: arrangedLayouts, pan, zoom });
    if (selectedProject.api_id) {
      for (let index = 0; index < visibleAgents.length; index += 1) {
        const agent = visibleAgents[index];
        const layout = layouts[index];
        void apiService.updateGroupPaneLayout(selectedProject.api_id, agent.paneId, {
          pos_x: layout.x, pos_y: layout.y, width: layout.width, height: layout.height, z_index: layout.z,
        });
      }
    }
  };

  return (
    <section data-id="projects-panel" className="relative flex h-full min-w-0 flex-1 bg-[#090a0d] text-zinc-300">
      <aside data-id="projects-list" className={cn('shrink-0 flex-col border-r border-white/[0.07] bg-[#0d0e12]', projectListCollapsed ? 'hidden' : 'flex w-[280px] max-[700px]:w-[180px]')}>
        <header data-id="projects-list-header" className="flex h-12 shrink-0 items-center border-b border-white/[0.07] px-4">
          <h2 data-id="projects-list-title" className="flex-1 text-[15px] font-semibold text-zinc-100">{t('projectsTitle')}</h2>
          <button type="button" data-id="projects-list-collapse" onClick={() => setProjectListVisibility(true)} className="mr-1 grid h-8 w-8 place-items-center rounded-lg text-zinc-400 hover:bg-white/[0.06] hover:text-white" title={t('collapse', { ns: 'common', defaultValue: '折叠' })} aria-label={t('collapse', { ns: 'common', defaultValue: '折叠' })}><PanelLeftClose className="h-4 w-4" /></button>
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
                  <span data-id="project-list-item-agent-count" className="grid h-7 w-7 place-items-center text-[11px] font-medium tabular-nums text-zinc-600 transition-opacity group-hover:opacity-0 group-focus-within:opacity-0">{project.pane_count}</span>
                  <button type="button" data-id="project-more" onMouseDown={(event) => event.stopPropagation()} onClick={(event) => { event.stopPropagation(); setProjectMenuId(String(project.id)); setProjectMenuAnchor(String(project.id)); }} className="absolute inset-0 grid h-7 w-7 place-items-center rounded-lg text-zinc-500 opacity-0 transition-opacity hover:bg-white/[0.08] hover:text-zinc-200 group-hover:opacity-100 group-focus-within:opacity-100" title={t('projectMore')}><MoreHorizontal className="h-4 w-4" /></button>
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
        <header data-id="projects-agent-header" className="z-[200] flex h-12 shrink-0 items-center border-b border-white/[0.06] bg-[#090a0d]/90 px-5 backdrop-blur">
          {projectListCollapsed ? <button type="button" data-id="projects-list-expand" onClick={() => setProjectListVisibility(false)} className="mr-3 grid h-8 w-8 shrink-0 place-items-center rounded-lg text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100" title={t('expand', { ns: 'common', defaultValue: '展开' })} aria-label={t('expand', { ns: 'common', defaultValue: '展开' })}><PanelLeftOpen className="h-4 w-4" /></button> : null}
          <div data-id="projects-agent-heading" className="min-w-0 flex-1">
            <h2 data-id="projects-agent-title" className="truncate text-[15px] font-semibold text-zinc-100">{selectedProject.name}</h2>
            <p data-id="projects-agent-count" className="text-[11px] text-zinc-600">{t('projectAgentCount', { count: visibleAgents.length })}</p>
          </div>
          <button type="button" data-id={`project-definition-edit-${selectedProject.api_id}`} onClick={(event) => { event.stopPropagation(); openProjectDefinition(selectedProject); }} className="mr-1 grid h-8 w-8 shrink-0 place-items-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100" title={t('projectDefinitionEdit')} aria-label={t('projectDefinitionEdit')}><BookOpen className="h-4 w-4" /></button>
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
            if (!wheelZoomActive) return;
            event.preventDefault();
            zoomCanvasAt(event.clientX, event.clientY, event.deltaY > 0 ? -0.02 : 0.02);
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
              const cardMetrics = liveMetrics[shortPaneId(agent.paneId)] || (agentReplies[shortPaneId(agent.paneId)] ? metricsFromCurrentReply(agentReplies[shortPaneId(agent.paneId)]) : undefined);
              const cardShortId = shortPaneId(agent.paneId);
              const cardBusy = !canceledAgentIds.has(cardShortId) && (sendingAgentIds.has(cardShortId) || projectAgentStatusIsBusy({ status: agent.status }));
              const hasLocalActions = !agent.remote;
              const hasCliLifecycleActions = hasLocalActions && normalizeAgentType(agent.agentType) !== 'cicy';
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
                    terminalOpen={terminalAgentIds.has(cardShortId)}
                    working={cardBusy}
                    teamId={teamId}
                selected={selectedAgentIds.has(shortPaneId(agent.paneId))}
                removable={Boolean(selectedProject.api_id)}
                projectOptions={projects.filter((project) => String(project.id) !== String(selectedProject.id)).map((project) => ({
                  id: project.id,
                  name: project.name,
                  checked: project.pane_ids.some((paneId) => shortPaneId(paneId) === cardShortId),
                }))}
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
                                <div key={attachment.id} data-id={`project-agent-message-queue-attachment-${attachment.id}`} className="h-16 w-16 shrink-0 overflow-hidden rounded-lg border border-white/10 bg-white/[0.04]">
                                  {attachment.mediaType === 'image' && (attachment.previewURL || attachment.fileRef) ? (
                                    <span data-id="project-agent-message-queue-attachment-media" className="block h-full w-full [&_[data-id=current-history-md-img]]:!m-0 [&_[data-id=current-history-md-img]]:!h-full [&_[data-id=current-history-md-img]]:!w-full [&_[data-id=current-history-md-img]]:object-cover">
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
                          <div key={attachment.id} data-id={`project-agent-card-attachment-${attachment.id}`} className="group relative h-16 w-16 shrink-0 overflow-visible rounded-lg border border-white/10 bg-white/[0.04]">
                            {attachment.mediaType === 'image' && attachment.previewURL ? (
                              <span data-id="project-agent-card-attachment-media" className="block h-full w-full overflow-hidden rounded-lg [&_[data-id=current-history-md-img]]:!m-0 [&_[data-id=current-history-md-img]]:!h-full [&_[data-id=current-history-md-img]]:!w-full [&_[data-id=current-history-md-img]]:!rounded-lg [&_[data-id=current-history-md-img]]:object-cover">
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
                onMoveProject={(projectId) => { void changeAgentProject(agent, projectId, 'move'); }}
                onAddProject={(projectId) => { void changeAgentProject(agent, projectId, 'add'); }}
                onRestart={hasCliLifecycleActions ? () => { void restartAgent(agent); } : undefined}
                onUpdate={hasCliLifecycleActions ? () => setUpdateTarget(agent) : undefined}
                onBindWechat={hasLocalActions ? () => setWechatTarget(agent) : undefined}
                onBindFeishu={hasLocalActions ? () => setFeishuTarget(agent) : undefined}
                onFork={hasLocalActions ? () => setForkTarget(agent) : undefined}
                onTerminalOpenChange={(open) => setTerminalAgentIds((current) => {
                  if (current.has(cardShortId) === open) return current;
                  const next = new Set(current);
                  if (open) next.add(cardShortId);
                  else next.delete(cardShortId);
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
          <button type="button" data-id="project-canvas-reset" onClick={resetCanvasView} className="grid h-7 w-7 place-items-center rounded-md text-zinc-500 hover:bg-white/[0.08] hover:text-white" title={t('projectResetView')}><LayoutGrid className="h-3.5 w-3.5" /></button>
          <button
            type="button"
            data-id="project-canvas-wheel-zoom-toggle"
            aria-pressed={wheelZoomActive}
            aria-label={t(wheelZoomActive ? 'projectWheelZoomDisable' : 'projectWheelZoomEnable')}
            title={t(wheelZoomActive ? 'projectWheelZoomDisable' : 'projectWheelZoomEnable')}
            onClick={() => setWheelZoomActive((active) => !active)}
            className={cn(
              'grid h-7 w-7 place-items-center rounded-md transition-colors',
              wheelZoomActive
                ? 'bg-blue-500/15 text-blue-300 hover:bg-blue-500/25 hover:text-blue-200'
                : 'text-zinc-500 hover:bg-white/[0.08] hover:text-white',
            )}
          >
            <Hand className="h-3.5 w-3.5" />
          </button>
        </div>
        {footerControls ? (
          <div data-id="project-canvas-footer-controls" className="absolute bottom-1.5 right-4 flex items-center gap-2 px-2 py-1">
            {footerControls}
          </div>
        ) : null}
        </div>
        {!dockOpen ? <div data-id="project-fab-wrap" className="absolute bottom-16 right-5 z-[80] flex flex-col items-end gap-2">
          <div
            data-id="project-fab-menu"
            className={cn(
              'flex min-w-[184px] origin-bottom-right flex-col rounded-xl border border-white/[0.10] bg-[#202126] p-1 shadow-2xl transition-all duration-200',
              fabOpen ? 'pointer-events-auto translate-y-0 scale-100 opacity-100' : 'pointer-events-none translate-y-2 scale-95 opacity-0',
            )}
          >
            <button
              type="button"
              data-id="project-fab-create-agent"
              onClick={() => {
                setFabOpen(false);
                onCreateAgent({
                  projectId: selectedProject.api_id,
                  projectTemplate: selectedProject.project_template || (selectedProject.builtin ? 'default' : ''),
                  onCreated: async (createdPaneId: string) => {
                    await apiService.addGroupPane(selectedProject.api_id, createdPaneId);
                  },
                });
              }}
              className="flex h-9 w-full items-center gap-2 rounded-lg px-3 text-left text-[12px] text-zinc-100 transition-colors hover:bg-white/[0.07]"
            >
              <UserPlus data-id="project-fab-create-agent-icon" className="h-4 w-4" />
              <span data-id="project-fab-create-agent-label">{t('projectCreateAgent')}</span>
            </button>
            <button
              type="button"
              data-id="project-fab-add-existing"
              onClick={() => { setFabOpen(false); openAddAgents(selectedProject); }}
              disabled={!selectedProject.api_id || availableAgents.length === 0}
              className="flex h-9 w-full items-center gap-2 rounded-lg px-3 text-left text-[12px] text-zinc-100 transition-colors hover:bg-white/[0.07] disabled:cursor-not-allowed disabled:opacity-40"
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

      <AppModal open={Boolean(definitionProject)} title={t('projectDefinitionTitle', { name: definitionProject?.name || '' })} onClose={() => { if (!definitionSaving) setDefinitionProject(null); }} maxWidth="960px">
        <div data-id="project-definition-modal" className="flex h-[min(680px,calc(82vh-88px))] min-h-[480px] flex-col gap-3">
          <p data-id="project-definition-hint" className="shrink-0 text-[12px] leading-5 text-zinc-500">Global 与 Project 文件会共同组成项目内 Agent 的共享角色设定。</p>
          <div data-id="project-definition-file-workbench" className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-white/[0.09] bg-black/20">
            <div data-id="project-definition-file-tabs" role="tablist" className="flex h-10 shrink-0 items-end gap-1 border-b border-white/[0.08] bg-[#121317] px-2">
              <button type="button" role="tab" aria-selected={definitionFile === 'project'} data-id="project-definition-file-project" onClick={() => setDefinitionFile('project')} className={cn('flex h-9 items-center gap-2 border-b-2 px-3 font-mono text-[12px] transition-colors', definitionFile === 'project' ? 'border-zinc-300 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300')}>
                <FileText className="h-3.5 w-3.5" /> {String(definitionProject?.project_file || 'project.md').split('/').pop()}
              </button>
              <button type="button" role="tab" aria-selected={definitionFile === 'global'} data-id="project-definition-file-global" onClick={() => setDefinitionFile('global')} className={cn('flex h-9 items-center gap-2 border-b-2 px-3 font-mono text-[12px] transition-colors', definitionFile === 'global' ? 'border-zinc-300 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300')}>
                <FileText className="h-3.5 w-3.5" /> global.md
              </button>
            </div>
            <div data-id={`project-definition-tab-help-${definitionFile}`} className="shrink-0 border-b border-white/[0.07] bg-white/[0.025] px-3 py-2 text-[11px] leading-5 text-zinc-500">
              {definitionFile === 'project' ? (
                <><span className="font-medium text-zinc-300">Project 定义：</span>只对当前项目内的 Agent 生效。用来编写项目目标、业务背景、技术约束、协作方式和验收标准；不要重复 global.md 里的全局通用规则。</>
              ) : (
                <><span className="font-medium text-zinc-300">global.md：</span>对所有项目和 Agent 生效。用来编写稳定的全局原则、沟通风格、安全要求和通用工作流；不要放入某个项目才需要的细节。</>
              )}
            </div>
            <div data-id="project-definition-editor" className="min-h-0 min-w-0 flex-1">
              <MarkdownFileEditor
                value={definitionFile === 'global' ? definitionGlobalRules : definitionRules}
                path={definitionFile === 'global' ? definitionGlobalPath : String(definitionProject?.project_file || '')}
                loading={definitionFile === 'global' && definitionLoading}
                onChange={definitionFile === 'global' ? setDefinitionGlobalRules : setDefinitionRules}
              />
            </div>
          </div>
          <div data-id="project-definition-tips" className="shrink-0 rounded-lg border border-amber-400/15 bg-amber-400/[0.05] px-3 py-2 text-[11px] leading-5 text-zinc-400">
            <span className="font-medium text-zinc-300">Tips：</span>Agent 角色人设按 <code className="text-amber-300/80">global.md → Project 定义 → Agent 角色</code> 的顺序组合。global.md 提供全局共通原则，Project 定义补充当前项目的目标、约束和上下文，Agent 角色再补充个体职责。更具体的后续定义用于细化前面的通用规则。
          </div>
          <div data-id="project-definition-actions" className="flex shrink-0 justify-end gap-2 border-t border-white/[0.07] pt-3">
            <button type="button" data-id="project-definition-cancel" onClick={() => setDefinitionProject(null)} disabled={definitionSaving} className="h-9 rounded-lg px-3.5 text-[12px] text-zinc-400 hover:bg-white/[0.05]">{t('cancel', { ns: 'common' })}</button>
            <button type="button" data-id="project-definition-save" onClick={() => { void saveProjectDefinition(); }} disabled={definitionSaving || definitionLoading} className="h-9 rounded-lg bg-zinc-200 px-4 text-[12px] font-medium text-zinc-900 hover:bg-white disabled:opacity-50">{definitionSaving ? t('projectSaving') : t('projectSave')}</button>
          </div>
        </div>
      </AppModal>

      <AppModal open={addOpen} title={t('projectAddAgentTitle', { name: selectedProject.name })} onClose={() => setAddOpen(false)} maxWidth="760px">
        <div data-id="project-add-agent-modal" className="flex h-[620px] max-h-[calc(82vh-88px)] min-h-0 overflow-hidden rounded-xl border border-white/[0.07] bg-[#111216]">
          <aside data-id="project-add-agent-instance-tabs" role="tablist" aria-label="Instance" className="flex w-[190px] shrink-0 flex-col border-r border-white/[0.07] bg-black/[0.12] p-2.5">
            <div className="mb-2 px-2 text-[10px] font-medium uppercase tracking-[0.12em] text-zinc-600">Instance</div>
            <div className="min-h-0 flex-1 space-y-1 overflow-y-auto">
              {addInstanceOptions.map((instance) => (
                <button
                  key={instance.id}
                  type="button"
                  role="tab"
                  aria-selected={addInstanceId === instance.id}
                  data-id={instance.id === 'local' ? 'project-add-agent-instance-local' : `project-add-agent-instance-${instance.id}`}
                  onClick={() => { setAddInstanceId(instance.id); setAddSearch(''); setAddAgentType('all'); }}
                  className={cn('group flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors', addInstanceId === instance.id ? 'bg-blue-500/12 text-zinc-100 ring-1 ring-blue-500/25' : 'text-zinc-500 hover:bg-white/[0.05] hover:text-zinc-300')}
                >
                  <span className={cn('h-2 w-2 shrink-0 rounded-full', instance.online ? 'bg-emerald-500' : 'bg-zinc-600')} />
                  <span className="min-w-0 flex-1 truncate text-[11px] font-medium">{instance.label}</span>
                  <span className="rounded-md bg-white/[0.05] px-1.5 py-0.5 text-[9px] tabular-nums text-zinc-500">{instance.count}</span>
                </button>
              ))}
            </div>
          </aside>
          <div className="flex min-w-0 flex-1 flex-col p-4">
            <div className="mb-3 flex shrink-0 items-end justify-between gap-3">
              <div><div className="text-[12px] font-medium text-zinc-200">{selectedAddInstance?.label}</div><div className="mt-0.5 text-[10px] text-zinc-600">{filteredAvailableAgents.length} / {availableAgentsForInstance.length} Agents</div></div>
              {selectedToAdd.size > 0 ? <span className="rounded-full bg-blue-500/12 px-2 py-1 text-[10px] font-medium text-blue-400">{t('projectAddSelected', { count: selectedToAdd.size })}</span> : null}
            </div>
            <div data-id="project-add-agent-type-filter" className="mb-2.5 flex shrink-0 gap-1.5 overflow-x-auto pb-0.5">
              {['all', ...availableAgentTypesForInstance].map((agentType) => {
                const count = agentType === 'all' ? availableAgentsForInstance.length : availableAgentsForInstance.filter((agent) => String(agent.agentType || '').trim().toLowerCase() === agentType).length;
                return <button key={agentType} type="button" data-id={`project-add-agent-type-${agentType}`} aria-pressed={addAgentType === agentType} onClick={() => setAddAgentType(agentType)} className={cn('flex h-7 shrink-0 items-center gap-1.5 rounded-lg border px-2.5 text-[10px] font-medium transition-colors', addAgentType === agentType ? 'border-blue-500/35 bg-blue-500/12 text-blue-400' : 'border-white/[0.07] bg-white/[0.02] text-zinc-500 hover:bg-white/[0.05] hover:text-zinc-300')}><span>{agentType === 'all' ? t('all', { ns: 'common', defaultValue: '全部' }) : agentType}</span><span className="text-[9px] tabular-nums opacity-65">{count}</span></button>;
              })}
            </div>
            <label data-id="project-add-agent-search-wrap" className="mb-3 flex h-10 shrink-0 items-center gap-2 rounded-xl border border-white/[0.08] bg-[#0c0d10] px-3 shadow-sm transition-colors focus-within:border-blue-500/45 focus-within:ring-2 focus-within:ring-blue-500/10">
              <Search className="h-4 w-4 shrink-0 text-zinc-500" />
              <input data-id="project-add-agent-search" value={addSearch} onChange={(event) => setAddSearch(event.target.value)} placeholder={`${t('projectSearchAgent')} · ${selectedAddInstance?.label || ''}`} className="min-w-0 flex-1 bg-transparent text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600" autoFocus />
              {addSearch ? <button type="button" data-id="project-add-agent-search-clear" onClick={() => setAddSearch('')} className="grid h-6 w-6 place-items-center rounded-md text-zinc-600 hover:bg-white/[0.06] hover:text-zinc-300"><X className="h-3.5 w-3.5" /></button> : null}
            </label>
            <div ref={addResultsRef} data-id="project-add-agent-results" className="min-h-0 flex-1 space-y-1.5 overflow-y-auto pr-1 [scrollbar-gutter:stable]">
          {filteredAvailableAgents.length ? filteredAvailableAgents.map((agent) => {
            const checked = selectedToAdd.has(agent.paneId);
            const disabled = Boolean(agent.remote && !agent.instanceOnline);
            return (
              <button
                key={agent.paneId}
                type="button"
                data-id={`project-add-agent-${shortPaneId(agent.paneId)}`}
                disabled={disabled}
                onClick={() => !disabled && setSelectedToAdd((current) => {
                  const next = new Set(current);
                  if (next.has(agent.paneId)) next.delete(agent.paneId); else next.add(agent.paneId);
                  return next;
                })}
                className={cn('group flex w-full items-center gap-3 rounded-xl border px-3 py-2.5 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-45', checked ? 'border-blue-500/45 bg-blue-500/10 ring-1 ring-blue-500/10' : 'border-white/[0.06] bg-white/[0.015] hover:border-white/[0.11] hover:bg-white/[0.04]')}
              >
                <AgentAvatar agentType={agent.agentType} title={agent.title || agent.paneId} dataId="project-add-agent-avatar" variant="stack" />
                <span className="min-w-0 flex-1"><span className="block truncate text-[13px] text-zinc-200">{agent.title}</span><span className="block font-mono text-[10px] text-zinc-600">{shortPaneId(agent.paneId)}</span></span>
                {agent.remote ? <span className={cn('h-2 w-2 shrink-0 rounded-full', agent.instanceOnline ? 'bg-emerald-500' : 'bg-zinc-600')} /> : null}
                <span className={cn('grid h-5 w-5 place-items-center rounded border', checked ? 'border-blue-400 bg-blue-500 text-white' : 'border-white/15')}><Check className={cn('h-3 w-3', checked ? 'opacity-100' : 'opacity-0')} /></span>
              </button>
            );
          }) : <div data-id="project-add-agent-empty" className="py-10 text-center text-sm text-zinc-600">{t('projectNoAvailableAgents')}</div>}
            </div>
            {addError ? <p data-id="project-add-agent-error" className="mt-2 shrink-0 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-[12px] text-red-300">{addError}</p> : null}
            <div data-id="project-add-agent-actions" className="mt-3 flex shrink-0 justify-end gap-2 border-t border-white/[0.07] pt-3">
            <button type="button" data-id="project-add-agent-cancel" onClick={() => setAddOpen(false)} className="h-8 rounded-lg px-3 text-[12px] text-zinc-400 hover:bg-white/[0.05]">{t('cancel', { ns: 'common' })}</button>
            <button type="button" data-id="project-add-agent-confirm" onClick={() => { void addSelectedAgents(); }} disabled={selectedToAdd.size === 0 || selectedToAddHasOfflineAgent || busy} className="h-8 rounded-lg bg-blue-500 px-3 text-[12px] font-medium text-white hover:bg-blue-400 disabled:opacity-40">{busy ? t('projectSaving') : t('projectAddSelected', { count: selectedToAdd.size })}</button>
            </div>
          </div>
        </div>
      </AppModal>
      {updateTarget ? createPortal(
        <UpdateAgentModal
          paneId={shortPaneId(updateTarget.paneId)}
          title={updateTarget.title || shortPaneId(updateTarget.paneId)}
          onClose={() => {
            setUpdateTarget(null);
            void onAgentsRefresh();
          }}
        />,
        document.body,
      ) : null}
      {wechatTarget ? createPortal(
        <WechatBindModal
          paneId={shortPaneId(wechatTarget.paneId)}
          title={wechatTarget.title || shortPaneId(wechatTarget.paneId)}
          onClose={() => setWechatTarget(null)}
        />,
        document.body,
      ) : null}
      {feishuTarget ? createPortal(
        <WechatBindModal
          platform="feishu"
          paneId={shortPaneId(feishuTarget.paneId)}
          title={feishuTarget.title || shortPaneId(feishuTarget.paneId)}
          onClose={() => setFeishuTarget(null)}
        />,
        document.body,
      ) : null}
      {forkTarget ? (
        <ForkConfirmModal
          sourcePaneId={shortPaneId(forkTarget.paneId)}
          masterPaneId={shortPaneId(masterPaneId)}
          onClose={() => setForkTarget(null)}
          onForked={() => { void onAgentsRefresh(); }}
          onOpenAgentFile={onOpenAgentFile}
        />
      ) : null}
      {dialogsNode}
    </section>
  );
}
