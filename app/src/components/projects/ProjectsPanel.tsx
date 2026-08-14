// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react';
import { Bot, Check, FolderKanban, Loader2, Maximize2, Minus, MoreHorizontal, Pencil, Pin, PinOff, Plus, Search, Square, Trash2, UserPlus, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { sendToAgent } from '../../services/agentSend';
import { cn, copyToClipboard } from '../../lib/utils';
import type { AgentLiveMetrics } from '../../lib/agentMetrics';
import { metricsFromCurrentReply } from '../../lib/agentMetrics';
import { ModelTag } from '../../lib/modelTag';
import { AppModal, useDialogs } from '../ui/Modal';
import AgentAvatar from '../AgentAvatar';

export interface ProjectAgent {
  paneId: string;
  title: string;
  agentType?: string;
  status?: string;
  defaultModel?: string;
  workspace?: string;
  machineLabel?: string;
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

const DEFAULT_PROJECT_ID = 'default';
const shortPaneId = (value: string) => String(value || '').replace(/:.*$/, '');

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
    <svg width="12" height="12" viewBox="0 0 12 12" className="-rotate-90 shrink-0">
      <circle cx="6" cy="6" r={radius} fill="none" stroke="rgba(255,255,255,0.10)" strokeWidth="2.5" />
      <circle cx="6" cy="6" r={radius} fill="none" stroke={color} strokeWidth="2.5" strokeDasharray={`${Math.max(0.5, (pct / 100) * circumference)} ${circumference}`} strokeLinecap="round" />
    </svg>
  );
}

function ProjectAgentCard({ agent, metrics, latest, teamId, selected, removable, footer, width, height, onSelect, onOpen, onRemove, onResizePointerDown, onResizePointerMove, onResizePointerUp }: {
  agent: ProjectAgent;
  metrics?: AgentLiveMetrics;
  latest?: any;
  teamId?: string;
  selected: boolean;
  removable: boolean;
  footer?: ReactNode;
  width: number;
  height: number;
  onSelect: () => void;
  onOpen: () => void;
  onRemove: () => void;
  onResizePointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizePointerMove: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onResizePointerUp: (event: ReactPointerEvent<HTMLDivElement>) => void;
}) {
  const { t } = useTranslation('workspace');
  const [menuOpen, setMenuOpen] = useState(false);
  const [identityCopied, setIdentityCopied] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const status = String(agent.status || 'idle').toLowerCase();
  const unhealthy = /failed|error|offline|stopped/.test(status);
  const busy = /running|working|thinking|streaming/.test(status);
  const identity = teamId ? `${teamId}.${shortPaneId(agent.paneId)}` : shortPaneId(agent.paneId);

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
    setMenuOpen((value) => !value);
  };

  return (
    <article
      data-id={`project-agent-card-${shortPaneId(agent.paneId)}`}
      onClick={onSelect}
      aria-pressed={selected}
      style={{ width, height }}
      className={cn(
        'relative flex min-h-[240px] min-w-[260px] cursor-pointer flex-col rounded-2xl border bg-[#111216] shadow-[0_12px_30px_rgba(0,0,0,0.28)] transition-[border-color,box-shadow] hover:border-white/20',
        selected ? 'border-blue-500 ring-1 ring-blue-500/60' : 'border-white/[0.08]',
      )}
    >
      <div data-id="project-agent-card-body" className="flex flex-1 flex-col p-5">
      <div data-id="project-agent-card-header" className="flex items-start gap-3">
        <div data-id="project-agent-card-heading" className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            <h3 data-id="project-agent-card-title" className="truncate text-[17px] font-semibold text-zinc-100">{agent.title || agent.paneId}</h3>
            {agent.agentType ? <span data-id="project-agent-card-agent-type" className="shrink-0 font-mono text-[12px] text-zinc-500">{agent.agentType}</span> : null}
          </div>
        </div>
        <div data-id="project-agent-card-menu-wrap" ref={menuRef} className="relative">
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
              <button
                type="button"
                data-id="project-agent-card-open"
                onClick={(event) => { event.stopPropagation(); setMenuOpen(false); onOpen(); }}
                className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-[12px] text-zinc-300 hover:bg-white/[0.06]"
              >
                <Bot className="h-3.5 w-3.5" />{t('projectOpenAgent')}
              </button>
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

      <div data-id="project-agent-card-metrics" className="mt-5 flex h-9 min-w-0 items-start gap-2 border-b border-white/[0.08] pb-4 font-mono text-xs text-zinc-500">
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
          title={identity}
        >
          {identityCopied ? t('copied', { defaultValue: '已复制' }) : identity}
        </button>
        {metrics?.model ? <ModelTag model={metrics.model} className="shrink-0" /> : null}
        {metrics && metrics.ctx > 0 ? (
          <span data-id="project-agent-card-context" className="flex shrink-0 items-center" title={`Context ${metrics.ctx}% / ${metrics.ctxK}k`}>
            <CtxRing pct={metrics.ctx} />
          </span>
        ) : null}
        {metrics && metrics.cost > 0 ? <span data-id="project-agent-card-cost" className="shrink-0 text-sky-500">{fmtCost(metrics.cost)}</span> : null}
      </div>
      <div data-id="project-agent-card-live-body" className="mt-3 min-h-0 flex-1 space-y-2 overflow-hidden text-[11px] leading-4">
        <div data-id="project-agent-card-latest-question" className="grid grid-cols-[52px_minmax(0,1fr)] gap-2">
          <span className="font-mono text-zinc-600">1. Q</span>
          <span className="line-clamp-2 whitespace-pre-wrap text-zinc-300">{String(latest?.latest_question || '—')}</span>
        </div>
        <div data-id="project-agent-card-latest-response" className="grid grid-cols-[52px_minmax(0,1fr)] gap-2">
          <span className="font-mono text-zinc-600">2. {latest?.latest_response_type === 'thinking' ? 'THINK' : 'REPLY'}</span>
          <span className="line-clamp-3 whitespace-pre-wrap text-zinc-400">{String(latest?.latest_response || '—')}</span>
        </div>
        <div data-id="project-agent-card-latest-tool" className="grid grid-cols-[52px_minmax(0,1fr)] gap-2">
          <span className="font-mono text-zinc-600">3. TOOL</span>
          <span className="min-w-0 line-clamp-2 font-mono text-zinc-500">
            {latest?.latest_tool?.name ? `${latest.latest_tool.name}${latest.latest_tool.input ? ` ${latest.latest_tool.input}` : ''}` : '—'}
          </span>
        </div>
      </div>
      </div>
      {footer}
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

export default function ProjectsPanel({ agents, statuses = {}, onOpenAgent }: {
  agents: ProjectAgent[];
  statuses?: Record<string, any>;
  onOpenAgent: (paneId: string) => void;
}) {
  const { t } = useTranslation('workspace');
  const { confirm, prompt, node: dialogsNode } = useDialogs();
  const [groups, setGroups] = useState<AgentProject[]>([]);
  const [teamId, setTeamId] = useState('');
  const [selectedId, setSelectedId] = useState<number | string>(DEFAULT_PROJECT_ID);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState('');
  const [createDescription, setCreateDescription] = useState('');
  const [createError, setCreateError] = useState('');
  const [creating, setCreating] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [addSearch, setAddSearch] = useState('');
  const [addError, setAddError] = useState('');
  const [selectedToAdd, setSelectedToAdd] = useState<Set<string>>(new Set());
  const [selectedAgentIds, setSelectedAgentIds] = useState<Set<string>>(new Set());
  const [agentMessages, setAgentMessages] = useState<Record<string, string>>({});
  const [sendingAgentIds, setSendingAgentIds] = useState<Set<string>>(new Set());
  const [cancelingAgentIds, setCancelingAgentIds] = useState<Set<string>>(new Set());
  const [sendFeedbackByAgent, setSendFeedbackByAgent] = useState<Record<string, string>>({});
  const [agentLayouts, setAgentLayouts] = useState<Record<string, ProjectAgentLayout>>({});
  const [canvasPan, setCanvasPan] = useState({ x: 60, y: 60 });
  const [canvasZoom, setCanvasZoom] = useState(1);
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
  const agentDragRef = useRef<{ id: string; pointerId: number; startX: number; startY: number; originX: number; originY: number; moved: boolean } | null>(null);
  const agentResizeRef = useRef<{ id: string; pointerId: number; startX: number; startY: number; originWidth: number; originHeight: number } | null>(null);
  const panDragRef = useRef<{ pointerId: number; startX: number; startY: number; originX: number; originY: number } | null>(null);
  const draggedAgentRef = useRef('');

  const load = useCallback(async () => {
    setLoading(true);
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
      setLoading(false);
    }
  }, [t]);

  useEffect(() => { void load(); }, [load]);

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

  useEffect(() => {
    setSelectedAgentIds(new Set());
    setAgentMessages({});
    setSendingAgentIds(new Set());
    setSendFeedbackByAgent({});
    setCanvasPan({ x: 60, y: 60 });
    setCanvasZoom(1);
  }, [selectedProject.id]);

  useEffect(() => {
    let cancelled = false;
    const loadLayout = async () => {
      if (!selectedProject.api_id) {
        setAgentLayouts({});
        return;
      }
      try {
        const response = await apiService.getGroup(selectedProject.api_id);
        const rows = Array.isArray(response?.data?.panes) ? response.data.panes : [];
        const next: Record<string, ProjectAgentLayout> = {};
        const occupied = new Set<string>();
        rows.forEach((row: any, index: number) => {
          const id = shortPaneId(String(row?.pane_id || ''));
          if (!id) return;
          let x = Number.isFinite(Number(row?.pos_x)) ? Number(row.pos_x) : 40 + (index % 4) * 340;
          let y = Number.isFinite(Number(row?.pos_y)) ? Number(row.pos_y) : 40 + Math.floor(index / 4) * 260;
          let key = `${Math.round(x)}:${Math.round(y)}`;
          while (occupied.has(key)) {
            x += 340;
            if (x > 1400) {
              x = 40;
              y += 260;
            }
            key = `${Math.round(x)}:${Math.round(y)}`;
          }
          occupied.add(key);
          const storedWidth = Number(row?.width || 300);
          const storedHeight = Number(row?.height || 180);
          const legacyDefaultSize = storedWidth === 480 && storedHeight === 320;
          next[id] = {
            x, y,
            z: Number(row?.z_index || index + 1),
            width: legacyDefaultSize ? 300 : Math.max(260, storedWidth),
            height: legacyDefaultSize ? 320 : Math.max(240, storedHeight),
          };
        });
        if (!cancelled) setAgentLayouts(next);
      } catch {
        if (!cancelled) setAgentLayouts({});
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
      for (const [offset, paneId] of paneIds.entries()) {
        await apiService.addGroupPane(selectedProject.api_id, paneId);
        const index = visibleAgents.length + offset;
        void apiService.updateGroupPaneLayout(selectedProject.api_id, paneId, {
          pos_x: 40 + (index % 4) * 340,
          pos_y: 40 + Math.floor(index / 4) * 260,
          z_index: index + 1,
        }).catch(() => undefined);
      }
      setAddOpen(false);
      setSelectedToAdd(new Set());
      await load();
    } catch (cause: any) {
      setAddError(cause?.response?.data?.error || cause?.message || t('projectAddAgentFailed'));
    } finally {
      setBusy(false);
    }
  };

  const removeAgent = async (agent: ProjectAgent) => {
    if (!selectedProject.api_id) return;
    await apiService.removeGroupPane(selectedProject.api_id, agent.paneId);
    await load();
  };

  const toggleAgentSelection = (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    if (draggedAgentRef.current === id) {
      draggedAgentRef.current = '';
      return;
    }
    setSelectedAgentIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
    setSendFeedbackByAgent((current) => ({ ...current, [id]: '' }));
  };

  const sendAgentMessage = async (agent: ProjectAgent) => {
    const id = shortPaneId(agent.paneId);
    const text = String(agentMessages[id] || '').trim();
    if (!text || sendingAgentIds.has(id)) return;
    setSendingAgentIds((current) => new Set(current).add(id));
    setSendFeedbackByAgent((current) => ({ ...current, [id]: '' }));
    try {
      await sendToAgent(agent.paneId, text, {
        submit: true,
        agentType: agent.agentType,
      });
      setAgentMessages((current) => ({ ...current, [id]: '' }));
      setSendFeedbackByAgent((current) => ({ ...current, [id]: t('projectMessageSentToAgent') }));
    } catch (cause: any) {
      setSendFeedbackByAgent((current) => ({ ...current, [id]: cause?.message || t('projectMessageFailed') }));
    } finally {
      setSendingAgentIds((current) => {
        const next = new Set(current);
        next.delete(id);
        return next;
      });
      window.setTimeout(() => {
        document.querySelector<HTMLInputElement>(`[data-id="project-agent-prompt-input-${id}"]`)?.focus();
      }, 0);
    }
  };

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
      // Pointer capture may emit a click after pointerup. Suppress that click
      // so a single press cannot immediately select and then deselect the card.
      draggedAgentRef.current = drag.id;
      return;
    }
    draggedAgentRef.current = drag.id;
    const x = drag.originX + (event.clientX - drag.startX) / canvasZoom;
    const y = drag.originY + (event.clientY - drag.startY) / canvasZoom;
    setAgentLayouts((current) => ({ ...current, [drag.id]: { ...(current[drag.id] || { x, y, z: 1, width: 300, height: 320 }), x, y } }));
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
    setAgentLayouts((layouts) => ({ ...layouts, [resize.id]: layout }));
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
    if (panDragRef.current?.pointerId === event.pointerId) panDragRef.current = null;
  };

  const changeZoom = (delta: number) => setCanvasZoom((current) => Math.min(2, Math.max(0.35, Number((current + delta).toFixed(2)))));
  const resetCanvasView = () => {
    setCanvasPan({ x: 60, y: 60 });
    setCanvasZoom(1);
  };

  return (
    <section data-id="projects-panel" className="flex h-full min-w-0 flex-1 bg-[#090a0d] text-zinc-300">
      <aside data-id="projects-list" className="flex w-[280px] shrink-0 flex-col border-r border-white/[0.07] bg-[#0d0e12]">
        <header data-id="projects-list-header" className="flex h-14 shrink-0 items-center border-b border-white/[0.07] px-4">
          <FolderKanban className="mr-2 h-4 w-4 text-zinc-500" />
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
                className={cn('group mb-1 flex h-12 cursor-pointer items-center gap-2 rounded-xl px-3 transition-colors', active ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-400 hover:bg-white/[0.04] hover:text-zinc-200')}
              >
                {project.pinned ? <Pin data-id="project-list-item-pinned" className="h-3 w-3 shrink-0 text-amber-400" /> : null}
                <span data-id="project-list-item-name" className="min-w-0 flex-1 truncate text-[13px] font-medium">{project.name}</span>
                <div data-id="project-list-item-actions" className="flex w-[78px] shrink-0 items-center justify-end gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
                    <button type="button" data-id="project-pin" onClick={(event) => { event.stopPropagation(); void toggleProjectPinned(project); }} className="grid h-6 w-6 place-items-center rounded text-zinc-500 hover:bg-white/[0.08] hover:text-amber-300" title={project.pinned ? t('projectUnpin') : t('projectPin')}>{project.pinned ? <PinOff className="h-3 w-3" /> : <Pin className="h-3 w-3" />}</button>
                    <button type="button" data-id="project-rename" onClick={(event) => { event.stopPropagation(); void renameProject(project); }} className="grid h-6 w-6 place-items-center rounded text-zinc-500 hover:bg-white/[0.08] hover:text-zinc-200" title={t('projectRename')}><Pencil className="h-3 w-3" /></button>
                    {!project.builtin ? (
                    <button type="button" data-id="project-delete" onClick={(event) => { event.stopPropagation(); void deleteProject(project); }} className="grid h-6 w-6 place-items-center rounded text-zinc-500 hover:bg-red-500/10 hover:text-red-300" title={t('projectDelete')}><Trash2 className="h-3 w-3" /></button>
                    ) : <span className="h-6 w-6" aria-hidden="true" />}
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
          <button
            type="button"
            data-id="project-add-agent"
            onClick={() => openAddAgents(selectedProject)}
            disabled={!selectedProject.api_id || availableAgents.length === 0}
            className="mr-2 inline-flex h-8 items-center gap-1.5 rounded-lg bg-blue-500/15 px-3 text-[12px] text-blue-200 hover:bg-blue-500/25 disabled:cursor-not-allowed disabled:opacity-40"
          >
            <UserPlus className="h-3.5 w-3.5" />{t('projectAddAgent')}
          </button>
          <div data-id="project-canvas-controls" className="flex items-center gap-1 rounded-lg border border-white/[0.06] bg-black/20 p-1">
            <button type="button" data-id="project-canvas-zoom-out" onClick={() => changeZoom(-0.1)} className="grid h-6 w-6 place-items-center rounded text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200" title={t('projectZoomOut')}><Minus className="h-3.5 w-3.5" /></button>
            <span data-id="project-canvas-zoom-value" className="w-11 text-center font-mono text-[10px] text-zinc-500">{Math.round(canvasZoom * 100)}%</span>
            <button type="button" data-id="project-canvas-zoom-in" onClick={() => changeZoom(0.1)} className="grid h-6 w-6 place-items-center rounded text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200" title={t('projectZoomIn')}><Plus className="h-3.5 w-3.5" /></button>
            <button type="button" data-id="project-canvas-reset" onClick={resetCanvasView} className="grid h-6 w-6 place-items-center rounded text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200" title={t('projectResetView')}><Maximize2 className="h-3.5 w-3.5" /></button>
          </div>
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
            else setCanvasPan((current) => ({ x: current.x - event.deltaX, y: current.y - event.deltaY }));
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
            className="pointer-events-none absolute inset-0 origin-top-left"
            style={{ transform: `translate(${canvasPan.x}px, ${canvasPan.y}px) scale(${canvasZoom})` }}
          >
            {visibleAgents.map((agent, index) => {
              const layout = layoutForAgent(agent, index);
              const cardMetrics = liveMetrics[shortPaneId(agent.paneId)];
              const cardShortId = shortPaneId(agent.paneId);
              const cardLatest = statuses[agent.paneId] || statuses[`${cardShortId}:main.0`] || statuses[cardShortId] || {};
              const cardBusy = sendingAgentIds.has(shortPaneId(agent.paneId)) || Boolean(cardMetrics?.working) || /running|working|thinking|streaming/.test(String(agent.status || '').toLowerCase());
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
                    teamId={teamId}
                selected={selectedAgentIds.has(shortPaneId(agent.paneId))}
                removable={Boolean(selectedProject.api_id)}
                width={layout.width}
                height={layout.height}
                footer={selectedAgentIds.has(shortPaneId(agent.paneId)) ? (
                  <footer
                    data-id={`project-agent-card-footer-${shortPaneId(agent.paneId)}`}
                    onClick={(event) => event.stopPropagation()}
                    className="flex h-11 items-center border-t border-blue-500/60 bg-[#15161b] px-3 rounded-b-2xl"
                  >
                    {cardBusy ? <Loader2 data-id={`project-agent-prompt-sending-${shortPaneId(agent.paneId)}`} className="mr-2 h-3.5 w-3.5 shrink-0 animate-spin text-blue-400" /> : null}
                    <input
                      data-id={`project-agent-prompt-input-${shortPaneId(agent.paneId)}`}
                      value={agentMessages[shortPaneId(agent.paneId)] || ''}
                      onChange={(event) => {
                        const id = shortPaneId(agent.paneId);
                        setAgentMessages((current) => ({ ...current, [id]: event.target.value }));
                        setSendFeedbackByAgent((current) => ({ ...current, [id]: '' }));
                      }}
                      onKeyDown={(event) => {
                        if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                        if (event.key === 'Enter') {
                          event.preventDefault();
                          void sendAgentMessage(agent);
                        }
                      }}
                      placeholder={t('projectMessagePlaceholder')}
                      autoFocus
                      disabled={cardBusy}
                      className="min-w-0 flex-1 bg-transparent text-[12px] text-zinc-200 outline-none placeholder:text-zinc-600 disabled:opacity-60"
                    />
                    {cardBusy ? (
                      <button
                        type="button"
                        data-id={`project-agent-prompt-cancel-${shortPaneId(agent.paneId)}`}
                        onClick={() => { void cancelAgentMessage(agent); }}
                        disabled={cancelingAgentIds.has(shortPaneId(agent.paneId))}
                        className="ml-2 grid h-7 w-7 shrink-0 place-items-center rounded-full bg-white/[0.10] text-zinc-200 hover:bg-white/[0.16] disabled:opacity-50"
                        title={t('composerStop', { ns: 'chat', defaultValue: '停止' })}
                      >
                        {cancelingAgentIds.has(shortPaneId(agent.paneId)) ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3 w-3 fill-current" />}
                      </button>
                    ) : null}
                    {sendFeedbackByAgent[shortPaneId(agent.paneId)] ? <span data-id={`project-agent-prompt-feedback-${shortPaneId(agent.paneId)}`} className="ml-2 max-w-20 truncate text-[10px] text-zinc-500" title={sendFeedbackByAgent[shortPaneId(agent.paneId)]}>{sendFeedbackByAgent[shortPaneId(agent.paneId)]}</span> : null}
                  </footer>
                ) : null}
                onSelect={() => toggleAgentSelection(agent)}
                onOpen={() => onOpenAgent(agent.paneId)}
                onRemove={() => { void removeAgent(agent); }}
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
