import { useState, useEffect, useCallback, useMemo } from 'react';
import { Users, Plus, X, Loader2, ExternalLink, MoreHorizontal, Trash2, RefreshCw } from 'lucide-react';
import type { SelectOptionAction } from '../ui/Select';
import apiService from '../../services/api';
import { useDialog } from '../../contexts/DialogContext';
import { normalizeAgentType } from '../../lib/agentType';
import { useDevRegister } from '../../lib/devStore';
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
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  const { confirm } = useDialog();

  const shortId = (id: string) => (id || '').replace(/:.*$/, '');
  const fullId = (id: string) => id.includes(':') ? id : `${id}:main.0`;

  useEffect(() => {
    const closeMenu = () => setOpenMenuId(null);
    document.addEventListener('pointerdown', closeMenu);
    return () => document.removeEventListener('pointerdown', closeMenu);
  }, []);
  useDevRegister(`TeamPanel:${paneId}`, {
    paneId,
    creating,
    createDialogOpen,
    openMenuId,
    boundCount: bindings.length,
    availableCount: panes.length,
    activePaneId: activePaneId || paneId,
  });

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
      const { data } = await apiService.createPane({
        role: 'worker',
        title: values.title,
        agent_type: values.agent_type,
        allow_all_actions: values.allow_all_actions,
      });
      const newId = data?.pane_id || data?.session;
      if (newId) {
        await apiService.bindAgent({ pane_id: paneId, agent_name: shortId(newId) });
        setCreateDialogOpen(false);
        await onRefreshPanes();
        onRefreshPoll();
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '创建并绑定员工失败' }));
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
    if (s.isThinking || s.status === 'thinking') return '思考中';
    if (s.status === 'tool_use') return '执行中';
    if (s.status === 'idle' || s.status === 'text') return '空闲';
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

  const restartPane = useCallback((wid: string, title: string, disabled?: boolean) => {
    if (disabled) {
      showToast(`${title} 当前运行时不支持重启`);
      return;
    }
    confirm(
      <>重启 <span className="text-zinc-100 font-medium">{title}</span>？</>,
      async () => {
        try {
          await apiService.restartPane(wid);
          showToast(`${title} 正在重启...`);
          onRefreshPanes();
          onRefreshPoll();
        } catch {
          showToast(`错误：${title} 重启失败`);
        }
      }
    );
  }, [confirm, onRefreshPanes, onRefreshPoll, showToast]);
  const deletePane = useCallback((binding: Binding, title: string) => {
    const wid = shortId(binding.name);
    confirm(
      <>删除 <span className="text-zinc-100 font-medium">{title}</span>？</>,
      async () => {
        try {
          await apiService.unbindAgent(binding.id);
          await apiService.deletePane(wid);
          onOpenInCurrentPane?.(paneId);
          await onRefreshPanes();
          onRefreshPoll();
        } catch {
          showToast(`错误：${title} 删除失败`);
        }
      }
    );
  }, [confirm, onOpenInCurrentPane, onRefreshPanes, onRefreshPoll, paneId, showToast]);
  const deleteUnboundPane = useCallback((agent: Agent) => {
    const wid = shortId(agent.pane_id);
    const title = agent.title || wid;
    confirm(
      <>删除 <span className="text-zinc-100 font-medium">{title}</span>？</>,
      async () => {
        try {
          await apiService.deletePane(wid);
          await onRefreshPanes();
          onRefreshPoll();
        } catch {
          showToast(`错误：${title} 删除失败`);
        }
      }
    );
  }, [confirm, onRefreshPanes, onRefreshPoll, showToast]);
  const groupedBindings = useMemo(() => {
    const groups = new Map<string, { machineId?: number; machineLabel?: string; items: Binding[] }>();
    for (const binding of bindings) {
      const key = binding.machine_id ? String(binding.machine_id) : 'local';
      if (!groups.has(key)) groups.set(key, { machineId: binding.machine_id, machineLabel: binding.instance_label || binding.machine_label, items: [] });
      groups.get(key)!.items.push(binding);
    }
    return Array.from(groups.values());
  }, [bindings]);

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
    onOpenSettings,
    canRestart = true,
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
    onOpenSettings?: () => void;
    canRestart?: boolean;
  }) => (
    <div
      key={wid}
      data-id={`team-panel-worker-${wid}`}
      onClick={onClick}
      className={`w-full mb-2 flex items-center gap-3 border p-3 rounded-xl transition-all group relative cursor-pointer ${
        active
          ? 'border-blue-500/50 bg-blue-500/[0.08] ring-1 ring-blue-500/20'
          : 'bg-white/[0.02] border-[var(--vsc-border)] hover:border-white/[0.08]'
      }`}
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
          title="菜单"
        >
          <MoreHorizontal className="w-3.5 h-3.5" />
        </button>
        {openMenuId === wid ? (
          <div
            data-id="team-panel-worker-menu-dropdown"
            className="absolute right-0 top-9 min-w-[190px] overflow-hidden rounded-xl border border-white/[0.08] bg-[#111113]/98 p-1.5 shadow-2xl backdrop-blur-xl"
          >
            <button
              type="button"
              data-id="team-panel-worker-menu-open"
              onClick={() => {
                setOpenMenuId(null);
                window.open(`#/agent/${wid}`, '_blank');
              }}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm text-zinc-300 transition-colors cursor-pointer hover:bg-white/[0.06]"
            >
              <ExternalLink className="w-3.5 h-3.5 shrink-0" />
              <span>新窗口打开</span>
            </button>
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
                <span>解绑</span>
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
              <span>重启</span>
            </button>
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
                <span>删除</span>
              </button>
            ) : null}
          </div>
        ) : null}
      </div>
      <div className="flex items-center gap-3 flex-1 min-w-0 text-left">
        <AgentAvatar
          agentType={agentType}
          title={title}
          dataId="team-panel-worker-agent-avatar"
          variant="panel"
        />
	        <div className="flex-1 min-w-0 pr-7">
	          <div className="flex items-center gap-1.5">
	            <h3 className={`text-sm font-medium truncate ${active ? 'text-blue-300' : 'text-zinc-300'}`}>{title}</h3>
	          </div>
	          <p className={`text-xs font-mono mt-0.5 truncate ${active ? 'text-blue-400/50' : 'text-zinc-600'}`}>
	            {subtitle}
          </p>
        </div>
      </div>
    </div>
  );

  return (
    <div className="h-full w-full min-w-0 flex flex-col overflow-hidden" data-id="team-panel-root">
      <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-center gap-2 flex-shrink-0" data-id="team-panel-toolbar">
        <Select
          options={available.map(a => ({
            value: a.pane_id,
            label: a.title || shortId(a.pane_id),
            sub: shortId(a.pane_id),
            icon: <AgentAvatar agentType={a.agent_type} title={a.title || shortId(a.pane_id)} variant="select" />,
            actions: [
              {
                id: 'open',
                label: '新窗口打开',
                icon: <ExternalLink className="w-3.5 h-3.5" />,
                onClick: () => window.open(`#/agent/${shortId(a.pane_id)}`, '_blank'),
              },
              {
                id: 'delete',
                label: '删除',
                icon: <Trash2 className="w-3.5 h-3.5" />,
                danger: true,
                onClick: () => deleteUnboundPane(a),
              },
            ] as SelectOptionAction[],
          }))}
          onChange={v => bind(v)}
          onOpenChange={open => { if (open) void onRefreshPanes(); }}
          placeholder="+ 绑定团队成员..."
          searchable
          className="flex-1"
        />
        <button
          data-id="team-panel-create-worker"
          onClick={() => setCreateDialogOpen(true)}
          disabled={creating}
          className="flex items-center text-sm px-2 py-1.5 rounded border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50"
          title="创建并绑定新员工"
        >
          {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Plus className="w-3.5 h-3.5" />}
        </button>
      </div>

      <CreateAgentDialog
        open={createDialogOpen}
        submitting={creating}
        onClose={() => setCreateDialogOpen(false)}
        onSubmit={createAndBind}
        title="创建并绑定新员工"
        submitLabel="创建并绑定"
        emptyTitleOnAgentSelect="全栈软件开发工程师"
        dialogClassName="w-[960px] max-w-[96vw]"
        agentTypeGridClassName="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3"
      />


      <div className="flex-1 min-w-0 overflow-x-hidden overflow-y-auto hide-scrollbar" data-id="team-panel-worker-list">
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
            {groupedBindings.map(group => (
              <div
                key={group.machineId || 'local'}
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
                    agentType: agentTypeById.get(wid) || '',
                    status: s,
                    subtitle: subtitleParts.join(' · '),
                    active: activePaneId === wid,
                    opened: openedPaneIds.includes(wid),
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
                    onRemove: () => confirm(<>解绑 <span className="text-zinc-100 font-medium">{getName(b)}</span>？</>, () => unbind(b)),
                    onDelete: () => deletePane(b, getName(b)),
                  });
                })}
              </div>
            ))}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full text-zinc-600" data-id="team-panel-empty">
            <Users className="w-8 h-8 mb-2 opacity-20" />
            <p className="text-sm">先绑定一个团队成员开始</p>
          </div>
        )}
      </div>
    </div>
  );
}
