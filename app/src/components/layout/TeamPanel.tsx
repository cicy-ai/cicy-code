import { useState, useEffect, useCallback, useMemo } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation, Trans } from 'react-i18next';
import i18n from '../../i18n';
import { Users, Plus, X, MoreHorizontal, Trash2, RefreshCw, UserPlus, GitBranch } from 'lucide-react';
import { Spinner } from '../ui/Spinner';
import type { SelectOptionAction } from '../ui/Select';
import apiService from '../../services/api';
import { useDialogs } from '../ui/Modal';
import { normalizeAgentType, guidanceFilenameForAgentType } from '../../lib/agentType';
import AgentAvatar from '../AgentAvatar';
import Select from '../ui/Select';
import CreateAgentDialog, { CreateAgentValues } from '../CreateAgentDialog';

interface Agent {
  pane_id: string;
  title?: string;
  agent_type?: string;
  role?: string;
  ttyd_port?: number;
  active?: number;
  machine_id?: number;
  machine_label?: string;
  source_kind?: string;
  source_ref?: string;
}

interface StatusInfo { status?: string; isThinking?: boolean; title?: string; }
interface Binding {
  id: number;
  pane_id: string;
  name: string;
  title?: string;
  status?: string;
  agent_type?: string;
  machine_id?: number;
  machine_label?: string;
  instance_label?: string;
  source_kind?: string;
  source_ref?: string;
}
interface Props {
  paneId: string;
  panes: Agent[];
  bindings: Binding[];
  statuses?: Record<string, StatusInfo>;
  onOpenInCurrentPane?: (paneId: string) => void;
  onLocatePane?: (paneId: string) => void;
  openedPaneIds?: string[];
  activePaneId?: string;
  onOpenSettingsPane?: (paneId: string) => void;
  onRefreshPanes: () => Promise<void>;
  onRefreshPoll: () => void;
}

export default function TeamPanel({ paneId, panes = [], bindings = [], statuses = {}, onOpenInCurrentPane, onLocatePane, openedPaneIds = [], activePaneId, onOpenSettingsPane, onRefreshPanes, onRefreshPoll }: Props) {
  const [creating, setCreating] = useState(false);
  const [forkingId, setForkingId] = useState<string | null>(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  const [dragOrder, setDragOrder] = useState<string[] | null>(null);
  const [draggingWid, setDraggingWid] = useState<string | null>(null);
  const [dragOverWid, setDragOverWid] = useState<string | null>(null);
  const { confirm, node: dialogsNode } = useDialogs();

  const shortId = (id: string) => (id || '').replace(/:.*$/, '');
  const fullId = (id: string) => id.includes(':') ? id : `${id}:main.0`;

  useEffect(() => {
    const closeMenu = () => setOpenMenuId(null);
    document.addEventListener('pointerdown', closeMenu);
    return () => document.removeEventListener('pointerdown', closeMenu);
  }, []);
  const boundIds = new Set(bindings.map(b => shortId(b.name)));
  const available = panes.filter(a => {
    const sid = shortId(a.pane_id);
    return sid !== paneId && !boundIds.has(sid);
  });

  const bind = async (agentPaneId: string) => {
    try {
      await apiService.bindAgent({ pane_id: paneId, agent_name: shortId(agentPaneId) });
      onRefreshPoll();
    } catch {}
  };

  const unbind = async (binding: Binding) => {
    const removedPaneId = shortId(binding.name);
    const nextSelectedPaneId = (
      activePaneId && activePaneId !== removedPaneId
        ? activePaneId
        : bindings
            .map((item) => shortId(item.name))
            .find((id) => id && id !== removedPaneId)
    ) || paneId;
    try {
      await apiService.unbindAgent(binding.id);
      onOpenInCurrentPane?.(nextSelectedPaneId);
      onRefreshPoll();
    } catch {}
  };

  const createAndBind = async (values: CreateAgentValues) => {
    setCreating(true);
    try {
      // master_pane_id 让后端 create 时直接完成绑定 + 写入 master 引用到子 agent 的 CLAUDE.md/AGENTS.md。
      const { data } = await apiService.createPane({
        role: 'worker',
        title: values.title,
        agent_type: values.agent_type,
        allow_all_actions: values.allow_all_actions,
        use_custom_gateway: values.use_custom_gateway,
        use_proxy: values.use_proxy,
        master_pane_id: paneId,
        inherit_guidance: values.inherit_guidance,
      });
      if (data?.pane_id || data?.session) {
        setCreateDialogOpen(false);
        await onRefreshPanes();
        onRefreshPoll();
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: i18n.t('toastCreateBindFailed', { ns: 'teamPanel' }) }));
    } finally {
      setCreating(false);
    }
  };

  const getStatus = (id: string): StatusInfo => statuses[fullId(id)] || statuses[id] || {};

  const statusDot = (s: StatusInfo) => {
    if (s.isThinking || s.status === 'thinking') return 'bg-yellow-500 animate-pulse';
    if (s.status === 'tool_use') return 'bg-blue-500 animate-pulse';
    if (s.status === 'idle' || s.status === 'text') return 'bg-emerald-500';
    return 'bg-zinc-600';
  };

  const statusLabel = (s: StatusInfo) => {
    if (s.isThinking || s.status === 'thinking') return i18n.t('statusThinking', { ns: 'teamPanel' });
    if (s.status === 'tool_use') return i18n.t('statusRunning', { ns: 'teamPanel' });
    if (s.status === 'idle' || s.status === 'text') return i18n.t('statusIdle', { ns: 'teamPanel' });
    return '';
  };

  const getName = (binding: Binding) => {
    const wid = shortId(binding.name);
    const s = getStatus(wid);
    return binding.title || s.title || panes.find(a => shortId(a.pane_id) === wid)?.title || wid;
  };

  const showToast = useCallback((detail: string) => {
    window.dispatchEvent(new CustomEvent('show-toast', { detail }));
  }, []);

  const restartPane = useCallback(async (wid: string, title: string, disabled?: boolean) => {
    if (disabled) {
      showToast(i18n.t('toastRestartUnsupported', { ns: 'teamPanel', title }));
      return;
    }
    const ok = await confirm({
      body: <Trans i18nKey="confirmRestart" ns="teamPanel" values={{ title }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
    });
    if (!ok) return;
    try {
      await apiService.restartPane(wid);
      showToast(i18n.t('toastRestarting', { ns: 'teamPanel', title }));
      onRefreshPanes();
      onRefreshPoll();
    } catch {
      showToast(i18n.t('toastRestartFailed', { ns: 'teamPanel', title }));
    }
  }, [confirm, onRefreshPanes, onRefreshPoll, showToast]);
  const deletePane = useCallback(async (binding: Binding, title: string) => {
    const wid = shortId(binding.name);
    const ok = await confirm({
      body: <Trans i18nKey="confirmDelete" ns="teamPanel" values={{ title }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
      danger: true,
    });
    if (!ok) return;
    try {
      await apiService.unbindAgent(binding.id);
      await apiService.deletePane(wid);
      onOpenInCurrentPane?.(paneId);
      await onRefreshPanes();
      onRefreshPoll();
    } catch {
      showToast(i18n.t('toastDeleteFailed', { ns: 'teamPanel', title }));
    }
  }, [confirm, onOpenInCurrentPane, onRefreshPanes, onRefreshPoll, paneId, showToast]);
  const deleteUnboundPane = useCallback(async (agent: Agent) => {
    const wid = shortId(agent.pane_id);
    const title = agent.title || wid;
    const ok = await confirm({
      body: <Trans i18nKey="confirmDelete" ns="teamPanel" values={{ title }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
      danger: true,
    });
    if (!ok) return;
    try {
      await apiService.deletePane(wid);
      await onRefreshPanes();
      onRefreshPoll();
    } catch {
      showToast(i18n.t('toastDeleteFailed', { ns: 'teamPanel', title }));
    }
  }, [confirm, onRefreshPanes, onRefreshPoll, showToast]);
  const orderedBindings = useMemo(() => {
    if (!dragOrder || dragOrder.length === 0) return bindings;
    const byWid = new Map(bindings.map(b => [shortId(b.name), b] as const));
    const result: Binding[] = [];
    const seen = new Set<string>();
    for (const wid of dragOrder) {
      const b = byWid.get(wid);
      if (b && !seen.has(wid)) {
        result.push(b);
        seen.add(wid);
      }
    }
    for (const b of bindings) {
      const wid = shortId(b.name);
      if (!seen.has(wid)) result.push(b);
    }
    return result;
  }, [bindings, dragOrder]);

  const groupedBindings = useMemo(() => {
    const groups = new Map<string, { machineId?: number; machineLabel?: string; items: Binding[] }>();
    for (const binding of orderedBindings) {
      const key = binding.machine_id ? String(binding.machine_id) : 'local';
      if (!groups.has(key)) groups.set(key, { machineId: binding.machine_id, machineLabel: binding.instance_label || binding.machine_label, items: [] });
      groups.get(key)!.items.push(binding);
    }
    return Array.from(groups.values());
  }, [orderedBindings]);

  const handleReorderDrop = useCallback((groupKey: string, fromWid: string, toWid: string) => {
    if (!fromWid || !toWid || fromWid === toWid) return;
    const group = groupedBindings.find(g => (g.machineId ? String(g.machineId) : 'local') === groupKey);
    if (!group) return;
    const widsInGroup = group.items.map(b => shortId(b.name));
    const fromIdx = widsInGroup.indexOf(fromWid);
    const toIdx = widsInGroup.indexOf(toWid);
    if (fromIdx < 0 || toIdx < 0) return;
    const nextGroupOrder = [...widsInGroup];
    nextGroupOrder.splice(fromIdx, 1);
    nextGroupOrder.splice(toIdx, 0, fromWid);
    const overall = orderedBindings.map(b => shortId(b.name));
    const newOrder: string[] = [];
    let cursor = 0;
    for (const wid of overall) {
      if (widsInGroup.includes(wid)) {
        newOrder.push(nextGroupOrder[cursor++]);
      } else {
        newOrder.push(wid);
      }
    }
    setDragOrder(newOrder);
    void apiService.reorderAgents(paneId, newOrder).then(() => {
      onRefreshPoll();
    }).catch(() => {
      // best-effort: revert by clearing optimistic order
      setDragOrder(null);
    });
  }, [groupedBindings, orderedBindings, paneId, onRefreshPoll]);

  const agentTypeById = useMemo(
    () => new Map(panes.map(agent => [shortId(agent.pane_id), normalizeAgentType(agent.agent_type)])),
    [panes]
  );

  const currentAgent = useMemo(() => {
    const agent = panes.find(a => shortId(a.pane_id) === paneId);
    const status = getStatus(paneId);
    const subtitleParts = [paneId, statusLabel(status)].filter(Boolean);
    if (agent?.machine_label) subtitleParts.push(agent.machine_label);
    return {
      title: agent?.title || status.title || paneId,
      agentType: normalizeAgentType(agent?.agent_type),
      status,
      subtitle: subtitleParts.join(' · '),
    };
  }, [paneId, panes, statuses]);

  const renderAgentCard = ({
    wid,
    title,
    agentType,
    status,
    subtitle,
    active = false,
    opened = false,
    onClick,
    onRemove,
    onDelete,
    onRestart,
    onFork,
    forking = false,
    onOpenSettings,
    canRestart = true,
    groupKey,
    draggable = false,
  }: {
    wid: string;
    title: string;
    agentType?: string;
    status: StatusInfo;
    subtitle: string;
    active?: boolean;
    opened?: boolean;
    onClick: () => void;
    onRemove?: () => void;
    onDelete?: () => void;
    onRestart?: () => void;
    onFork?: () => void;
    forking?: boolean;
    onOpenSettings?: () => void;
    canRestart?: boolean;
    groupKey?: string;
    draggable?: boolean;
  }) => (
    <div
      key={wid}
      data-id={`team-panel-worker-${wid}`}
      onClick={onClick}
      draggable={draggable}
      onDragStart={draggable ? (e) => {
        setDraggingWid(wid);
        e.dataTransfer.effectAllowed = 'move';
        try { e.dataTransfer.setData('text/plain', wid); } catch {}
      } : undefined}
      onDragOver={draggable ? (e) => {
        if (!draggingWid || draggingWid === wid) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        if (dragOverWid !== wid) setDragOverWid(wid);
      } : undefined}
      onDragLeave={draggable ? () => {
        if (dragOverWid === wid) setDragOverWid(null);
      } : undefined}
      onDrop={draggable ? (e) => {
        e.preventDefault();
        const fromWid = draggingWid || e.dataTransfer.getData('text/plain');
        if (fromWid && fromWid !== wid && groupKey) {
          handleReorderDrop(groupKey, fromWid, wid);
        }
        setDraggingWid(null);
        setDragOverWid(null);
      } : undefined}
      onDragEnd={draggable ? () => {
        setDraggingWid(null);
        setDragOverWid(null);
      } : undefined}
      className={`w-full mb-2 flex items-center gap-3 border p-3 rounded-xl transition-all group relative cursor-pointer ${
        active
          ? 'border-blue-500/50 bg-blue-500/[0.08] ring-1 ring-blue-500/20'
          : 'bg-white/[0.02] border-[var(--vsc-border)] hover:border-white/[0.08]'
      } ${draggingWid === wid ? 'opacity-40' : ''} ${dragOverWid === wid && draggingWid && draggingWid !== wid ? 'ring-2 ring-blue-500/40' : ''}`}
    >
      <div
        data-id={`team-panel-worker-menu-${wid}`}
        className="absolute right-2 top-2 z-20"
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      >
        <button
          type="button"
          data-id="team-panel-worker-menu-button"
          onClick={() => setOpenMenuId(prev => prev === wid ? null : wid)}
          className={`flex h-7 w-7 items-center justify-center rounded-lg transition-all cursor-pointer ${
            openMenuId === wid
              ? 'bg-white/[0.08] text-zinc-200'
              : 'text-zinc-700 opacity-0 group-hover:opacity-100 hover:bg-white/[0.05] hover:text-zinc-300'
          }`}
          title={i18n.t('menu', { ns: 'teamPanel' })}
        >
          <MoreHorizontal className="w-3.5 h-3.5" />
        </button>
        {openMenuId === wid ? (
          <div
            data-id="team-panel-worker-menu-dropdown"
            className="absolute right-0 top-9 min-w-[220px] overflow-hidden whitespace-nowrap rounded-xl border border-white/[0.08] bg-[#111113]/98 p-1.5 shadow-2xl backdrop-blur-xl"
          >
            {onRemove ? (
              <button
                type="button"
                data-id="team-panel-worker-menu-unbind"
                onClick={() => {
                  setOpenMenuId(null);
                  onRemove();
                }}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors cursor-pointer text-zinc-300 hover:bg-red-500/10 hover:text-red-300"
              >
                <X className="w-3.5 h-3.5 shrink-0" />
                <span data-id="team-panel-worker-menu-unbind-label">{i18n.t('unbind', { ns: 'teamPanel' })}</span>
              </button>
            ) : null}
            <button
              type="button"
              data-id="team-panel-worker-menu-restart"
              disabled={!onRestart || !canRestart}
              onClick={() => {
                if (!onRestart || !canRestart) return;
                setOpenMenuId(null);
                onRestart();
              }}
              className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors ${
                onRestart && canRestart
                  ? 'cursor-pointer text-zinc-300 hover:bg-white/[0.06]'
                  : 'cursor-not-allowed text-zinc-600'
              }`}
            >
              <RefreshCw className="w-3.5 h-3.5 shrink-0" />
              <span data-id="team-panel-worker-menu-restart-label">{i18n.t('restart', { ns: 'teamPanel' })}</span>
            </button>
            {onFork ? (
              <button
                type="button"
                data-id="team-panel-worker-menu-fork"
                disabled={forking}
                onClick={() => {
                  if (forking) return;
                  onFork();
                }}
                className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors ${
                  forking ? 'cursor-not-allowed text-zinc-600' : 'cursor-pointer text-zinc-300 hover:bg-white/[0.06]'
                }`}
              >
                {forking ? (
                  <RefreshCw className="w-3.5 h-3.5 shrink-0 animate-spin" />
                ) : (
                  <GitBranch className="w-3.5 h-3.5 shrink-0" />
                )}
                <span data-id="team-panel-worker-menu-fork-label">{forking ? i18n.t('toastForkStarted', { ns: 'teamPanel' }) : i18n.t('fork', { ns: 'teamPanel' })}</span>
              </button>
            ) : null}
            {onDelete ? (
              <button
                type="button"
                data-id="team-panel-worker-menu-delete"
                onClick={() => {
                  setOpenMenuId(null);
                  onDelete();
                }}
                className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm text-red-300 transition-colors cursor-pointer hover:bg-red-500/10 hover:text-red-200"
              >
                <Trash2 className="w-3.5 h-3.5 shrink-0" />
                <span data-id="team-panel-worker-menu-delete-label">{i18n.t('delete', { ns: 'teamPanel' })}</span>
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
      <div data-id="team-panel-worker-body" className="flex items-center gap-3 flex-1 min-w-0 text-left">
        <AgentAvatar
          agentType={agentType}
          title={title}
          dataId="team-panel-worker-agent-avatar"
          variant="panel"
        />
	        <div data-id="team-panel-worker-info" className="flex-1 min-w-0 pr-7">
	          <div data-id="team-panel-worker-title-row" className="flex items-center gap-1.5">
	            <h3 data-id="team-panel-worker-title" className={`text-sm font-medium truncate ${active ? 'text-blue-300' : 'text-zinc-300'}`}>{title}</h3>
	          </div>
	          <p data-id="team-panel-worker-subtitle" className={`text-xs font-mono mt-0.5 truncate ${active ? 'text-blue-400/50' : 'text-zinc-600'}`}>
	            {subtitle}
          </p>
        </div>
      </div>
    </div>
  );

  return (
    <div className="h-full w-full min-w-0 flex flex-col overflow-hidden" data-id="team-panel-root">
      {forkingId ? createPortal(
        <div data-id="team-panel-fork-loading" className="fixed inset-0 z-[9999] flex items-center justify-center bg-black/50 backdrop-blur-sm">
          <div className="flex flex-col items-center gap-3 rounded-2xl border border-white/[0.08] bg-[#141416] px-8 py-6 shadow-2xl">
            <RefreshCw className="w-8 h-8 text-blue-400 animate-spin" />
            <span className="text-sm text-zinc-200">{i18n.t('toastForkStarted', { ns: 'teamPanel' })}</span>
            <span className="text-xs text-zinc-500 font-mono">{forkingId}</span>
          </div>
        </div>,
        document.body
      ) : null}
      <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-center gap-2 flex-shrink-0" data-id="team-panel-toolbar">
        <div data-id="team-panel-bind-select" className="flex-1 min-w-0">
        <Select
          options={available.map(a => ({
            value: a.pane_id,
            label: a.title || shortId(a.pane_id),
            sub: shortId(a.pane_id),
            icon: <AgentAvatar agentType={a.agent_type} title={a.title || shortId(a.pane_id)} variant="select" />,
            actions: [
              {
                id: 'delete',
                label: i18n.t('delete', { ns: 'teamPanel' }),
                icon: <Trash2 className="w-3.5 h-3.5" />,
                danger: true,
                onClick: () => deleteUnboundPane(a),
              },
            ] as SelectOptionAction[],
          }))}
          onChange={v => bind(v)}
          onOpenChange={open => { if (open) void onRefreshPanes(); }}
          placeholder={i18n.t('bindMemberPlaceholder', { ns: 'teamPanel' })}
          searchable
          className="flex-1"
          triggerIcon={<UserPlus className="w-3.5 h-3.5" />}
          dropdownMatchSelector='[data-id="left-panel-team-view"]'
        />
        </div>
        <button
          data-id="team-panel-create-worker"
          onClick={() => setCreateDialogOpen(true)}
          disabled={creating}
          className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-[var(--vsc-border)] bg-white/[0.02] text-zinc-400 transition-colors duration-150 hover:bg-white/[0.05] hover:border-white/[0.10] hover:text-zinc-200 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/25"
          title={i18n.t('createBindShortLabel', { ns: 'teamPanel' })}
        >
          {creating ? <Spinner size="xs" /> : <Plus className="w-3.5 h-3.5" />}
        </button>
      </div>

      <CreateAgentDialog
        open={createDialogOpen}
        submitting={creating}
        onClose={() => setCreateDialogOpen(false)}
        onSubmit={createAndBind}
        title={i18n.t('createTitle', { ns: 'teamPanel' })}
        submitLabel={i18n.t('createSubmit', { ns: 'teamPanel' })}
        emptyTitleOnAgentSelect={i18n.t('createEmptyTitleAgent', { ns: 'teamPanel' })}
        dialogClassName="w-[960px] max-w-[96vw]"
        agentTypeGridClassName="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3"
        masterAgentType={panes.find(p => shortId(p.pane_id) === paneId)?.agent_type}
      />


      <div className="flex-1 min-w-0 overflow-x-hidden overflow-y-auto hide-scrollbar select-none" data-id="team-panel-worker-list">
        <div className="p-1.5 border-b border-[var(--vsc-border)]" data-id="team-panel-current-agent">
          {renderAgentCard({
            wid: paneId,
            title: currentAgent.title,
            agentType: currentAgent.agentType,
            status: currentAgent.status,
            subtitle: currentAgent.subtitle,
            active: (activePaneId || paneId) === paneId,
            onClick: () => {
              if (onLocatePane) {
                onLocatePane(paneId);
                return;
              }
              if (onOpenInCurrentPane) {
                onOpenInCurrentPane(paneId);
                return;
              }
              window.location.hash = `#/agent/${paneId}`;
            },
            onRestart: () => restartPane(paneId, currentAgent.title),
            onOpenSettings: () => onOpenSettingsPane?.(paneId),
            canRestart: true,
          })}
        </div>
        {bindings.length > 0 ? (
          <div className="flex w-full min-w-0 flex-col" data-id="team-panel-groups">
            {groupedBindings.map(group => {
              const groupKey = group.machineId ? String(group.machineId) : 'local';
              return (
              <div
                key={groupKey}
                style={{ padding: 4 }}
                className={group.machineId ? 'border-b border-[var(--vsc-border)]' : ''}
                data-id={`team-panel-group-${group.machineLabel || group.machineId || 'local'}`}
              >

                {group.items.map(b => {
                  const wid = shortId(b.name);
                  const s = getStatus(wid);
                  const subtitleParts = [wid, statusLabel(s)].filter(Boolean);
                  if (b.instance_label || b.machine_label) subtitleParts.push(b.instance_label || b.machine_label || '');
                  return renderAgentCard({
                    wid,
                    title: getName(b),
                    agentType: normalizeAgentType(b.agent_type) || agentTypeById.get(wid) || '',
                    status: s,
                    subtitle: subtitleParts.join(' · '),
                    active: activePaneId === wid,
                    opened: openedPaneIds.includes(wid),
                    draggable: true,
                    groupKey,
                    onClick: () => {
                      if (onLocatePane) {
                        onLocatePane(wid);
                        return;
                      }
                      if (onOpenInCurrentPane) {
                        onOpenInCurrentPane(wid);
                        return;
                      }
                      window.location.hash = `#/agent/${wid}`;
                    },
                    onRestart: () => restartPane(wid, getName(b)),
                    onOpenSettings: () => onOpenSettingsPane?.(wid),
                    canRestart: true,
                    onFork: async () => {
                      setForkingId(wid);
                      try {
                        const { data } = await apiService.forkPane({ source_pane_id: wid, master_pane_id: paneId });
                        if (data?.pane_id) {
                          await onRefreshPanes();
                          onRefreshPoll();
                        }
                      } catch {
                        window.dispatchEvent(new CustomEvent('show-toast', { detail: i18n.t('toastForkFailed', { ns: 'teamPanel' }) }));
                      } finally {
                        setForkingId(null);
                        setOpenMenuId(null);
                      }
                    },
                    forking: forkingId === wid,
                    onRemove: async () => {
                      const ok = await confirm({
                        body: <Trans i18nKey="confirmUnbind" ns="teamPanel" values={{ name: getName(b) }} components={{ strong: <span className="text-zinc-100 font-medium" /> }} />,
                        danger: true,
                      });
                      if (ok) unbind(b);
                    },
                    onDelete: () => deletePane(b, getName(b)),
                  });
                })}
              </div>
              );
            })}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full text-zinc-600" data-id="team-panel-empty">
            <Users className="w-8 h-8 mb-2 opacity-20" />
            <p className="text-sm">{i18n.t('emptyState', { ns: 'teamPanel' })}</p>
          </div>
        )}
      </div>
      {dialogsNode}
    </div>
  );
}
