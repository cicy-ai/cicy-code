import { useState, useEffect, useCallback, useMemo } from 'react';
import { Users, Plus, X, Loader2, ExternalLink, RefreshCw, Settings, MoreHorizontal, Trash2 } from 'lucide-react';
import apiService from '../../services/api';
import { useDialog } from '../../contexts/DialogContext';
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
interface Machine {
  id: number;
  machine_key: string;
  instance_key?: string;
  label: string;
  instance_label?: string;
  url: string;
  status: string;
  runtime_kind?: string;
  capabilities?: Record<string, any>;
}
interface Step {
  id: number;
  status?: string;
  target_pane_id?: string;
  target_machine_id?: number;
  title?: string;
  step_kind?: string;
  result_summary?: string;
}

interface Props {
  paneId: string;
  onOpenInCurrentPane?: (paneId: string) => void;
  openedPaneIds?: string[];
  activePaneId?: string;
  onOpenSettingsPane?: (paneId: string) => void;
}

function normalizeAgentType(agentType?: string) {
  switch ((agentType || '').trim().toLowerCase()) {
    case 'openclaw':
    case 'opencraw':
      return 'openclaw';
    case 'codex':
    case 'openai':
    case 'kiro-cli':
    case 'kiro-cli chat':
    case 'gemini':
    case 'copilot':
      return 'codex';
    case 'claude':
    case 'claude code':
    case 'claude-code':
      return 'claude';
    case 'opencode':
    case 'open code':
    case 'open-code':
      return 'opencode';
    default:
      return '';
  }
}

function AgentTypeAvatar({ agentType, title }: { agentType?: string; title: string }) {
  const normalizedAgentType = normalizeAgentType(agentType);
  const baseClassName = 'flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border shadow-sm';

  if (!normalizedAgentType) {
    return (
      <div
        data-id="team-panel-worker-agent-avatar"
        className={`${baseClassName} border-white/[0.08] bg-white/[0.03] text-zinc-400`}
        title={title}
      >
        <span className="text-xs font-semibold uppercase">{title.slice(0, 1) || '?'}</span>
      </div>
    );
  }

  if (normalizedAgentType === 'openclaw') {
    return (
      <div
        data-id="team-panel-worker-agent-avatar"
        className={`${baseClassName} border-zinc-500/40 bg-zinc-300 text-zinc-950`}
        title="OpenClaw"
      >
        <span className="text-[20px] leading-none" aria-label="OpenClaw">🦞</span>
      </div>
    );
  }

  const iconMap: Record<string, { label: string; src: string; className?: string }> = {
    codex: { label: 'Codex', src: '/assets/logos/openai.svg' },
    claude: { label: 'Claude', src: '/assets/logos/claude-symbol.svg' },
    opencode: { label: 'OpenCode', src: '/assets/logos/opencode.svg', className: 'h-7 w-7' },
  };
  const icon = iconMap[normalizedAgentType];
  if (!icon) return null;

  return (
    <div
      data-id="team-panel-worker-agent-avatar"
      className={`${baseClassName} border-zinc-500/40 bg-zinc-300`}
      title={icon.label}
    >
      <img src={icon.src} alt={icon.label} className={`${icon.className || 'h-6 w-6'} object-contain`} />
    </div>
  );
}

function AgentTypeMiniAvatar({ agentType, title }: { agentType?: string; title: string }) {
  const normalizedAgentType = normalizeAgentType(agentType);
  const baseClassName = 'flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border';

  if (!normalizedAgentType) {
    return (
      <div
        className={`${baseClassName} border-white/[0.08] bg-white/[0.03] text-zinc-400`}
        title={title}
      >
        <span className="text-[10px] font-semibold uppercase">{title.slice(0, 1) || '?'}</span>
      </div>
    );
  }

  if (normalizedAgentType === 'openclaw') {
    return (
      <div
        className={`${baseClassName} border-zinc-500/30 bg-zinc-200 text-zinc-950`}
        title="OpenClaw"
      >
        <span className="text-[15px] leading-none" aria-label="OpenClaw">🦞</span>
      </div>
    );
  }

  const iconMap: Record<string, { label: string; src: string; className?: string }> = {
    codex: { label: 'Codex', src: '/assets/logos/openai.svg', className: 'h-4.5 w-4.5' },
    claude: { label: 'Claude', src: '/assets/logos/claude-symbol.svg', className: 'h-4.5 w-4.5' },
    opencode: { label: 'OpenCode', src: '/assets/logos/opencode.svg', className: 'h-5 w-5' },
  };
  const icon = iconMap[normalizedAgentType];
  if (!icon) return null;

  return (
    <div
      className={`${baseClassName} border-zinc-500/30 bg-zinc-200`}
      title={icon.label}
    >
      <img src={icon.src} alt={icon.label} className={`${icon.className || 'h-4.5 w-4.5'} object-contain`} />
    </div>
  );
}

export default function TeamPanel({ paneId, onOpenInCurrentPane, openedPaneIds = [], activePaneId, onOpenSettingsPane }: Props) {
  const [all智能体, setAll智能体] = useState<Agent[]>([]);
  const [bindings, setBindings] = useState<Binding[]>([]);
  const [statuses, setStatuses] = useState<Record<string, StatusInfo>>({});
  const [instances, setInstances] = useState<Machine[]>([]);
  const [steps, setSteps] = useState<Step[]>([]);
  const [creating, setCreating] = useState(false);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [syncingInstances, setSyncingInstances] = useState(false);
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);
  const { confirm } = useDialog();

  const shortId = (id: string) => (id || '').replace(/:.*$/, '');
  const fullId = (id: string) => id.includes(':') ? id : `${id}:main.0`;

  const load = useCallback(async () => {
    try {
      const [pRes, bRes, sRes, mRes, qRes] = await Promise.all([
        apiService.getPanes(),
        apiService.get智能体ByPane(paneId),
        apiService.getAllStatus(),
        apiService.getMachines(),
        apiService.getCollabSteps(),
      ]);
      setAll智能体(Array.isArray(pRes.data) ? pRes.data : pRes.data?.panes || []);
      setBindings(Array.isArray(bRes.data) ? bRes.data : []);
      if (sRes.data) setStatuses(sRes.data);
      setInstances(Array.isArray(mRes.data?.instances) ? mRes.data.instances : (Array.isArray(mRes.data?.machines) ? mRes.data.machines : []));
      setSteps(Array.isArray(qRes.data?.steps) ? qRes.data.steps : []);
    } catch {}
  }, [paneId]);

  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  useEffect(() => {
    const closeMenu = () => setOpenMenuId(null);
    document.addEventListener('pointerdown', closeMenu);
    return () => document.removeEventListener('pointerdown', closeMenu);
  }, []);

  const boundIds = new Set(bindings.map(b => shortId(b.name)));
  const available = all智能体.filter(a => {
    const sid = shortId(a.pane_id);
    return sid !== paneId && !boundIds.has(sid);
  });

  const bind = async (agentPaneId: string) => {
    try {
      await apiService.bindAgent({ pane_id: paneId, agent_name: shortId(agentPaneId) });
      load();
    } catch {}
  };

  const unbind = async (binding: Binding) => {
    try {
      await apiService.unbindAgent(binding.id);
      onOpenInCurrentPane?.(paneId);
      load();
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
        load();
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '创建并绑定工作实例失败' }));
    } finally {
      setCreating(false);
    }
  };

  const syncInstances = async () => {
    setSyncingInstances(true);
    try {
      await apiService.syncMachines();
      load();
    } catch {} finally {
      setSyncingInstances(false);
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
    return binding.title || s.title || all智能体.find(a => shortId(a.pane_id) === wid)?.title || wid;
  };

  const showToast = useCallback((detail: string) => {
    window.dispatchEvent(new CustomEvent('show-toast', { detail }));
  }, []);

  const instanceMap = useMemo(() => new Map(instances.map(m => [m.id, m])), [instances]);
  const isApiOnlyInstance = useCallback((instance?: Machine) => {
    if (!instance) return false;
    if (instance.runtime_kind === 'cloudrun') return true;
    return instance.capabilities?.supports_tmux === false;
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
          load();
        } catch {
          showToast(`错误：${title} 重启失败`);
        }
      }
    );
  }, [confirm, load, showToast]);
  const deletePane = useCallback((binding: Binding, title: string) => {
    const wid = shortId(binding.name);
    confirm(
      <>删除 <span className="text-zinc-100 font-medium">{title}</span>？</>,
      async () => {
        try {
          await apiService.unbindAgent(binding.id);
          await apiService.deletePane(wid);
          onOpenInCurrentPane?.(paneId);
          load();
        } catch {
          showToast(`错误：${title} 删除失败`);
        }
      }
    );
  }, [confirm, load, onOpenInCurrentPane, paneId, showToast]);
  const latestStepMap = useMemo(() => {
    const map = new Map<string, Step>();
    for (const step of steps) {
      const key = `${step.target_machine_id || 0}:${step.target_pane_id || ''}`;
      if (!map.has(key)) map.set(key, step);
    }
    return map;
  }, [steps]);

  const groupedBindings = useMemo(() => {
    const groups = new Map<string, { instance?: Machine; items: Binding[] }>();
    for (const binding of bindings) {
      const instance = binding.machine_id ? instanceMap.get(binding.machine_id) : undefined;
      const key = instance ? String(instance.id) : 'local';
      if (!groups.has(key)) groups.set(key, { instance, items: [] });
      groups.get(key)!.items.push(binding);
    }
    return Array.from(groups.values());
  }, [bindings, instanceMap]);

  const agentTypeById = useMemo(
    () => new Map(all智能体.map(agent => [shortId(agent.pane_id), normalizeAgentType(agent.agent_type)])),
    [all智能体]
  );

  const currentAgent = useMemo(() => {
    const agent = all智能体.find(a => shortId(a.pane_id) === paneId);
    const machine = agent?.machine_id ? instanceMap.get(agent.machine_id) : undefined;
    const step = latestStepMap.get(`${agent?.machine_id || 0}:${paneId}`);
    const status = getStatus(paneId);
    const subtitleParts = [paneId, statusLabel(status)];
    if (machine?.instance_label || machine?.label) subtitleParts.push(machine?.instance_label || machine?.label || '');
    if (step?.title) subtitleParts.push(`${step.title}${step.status ? ` [${step.status}]` : ''}`);
    return {
      title: agent?.title || status.title || paneId,
      agentType: normalizeAgentType(agent?.agent_type),
      status,
      subtitle: subtitleParts.join(' · '),
      resultSummary: step?.result_summary || '',
    };
  }, [all智能体, instanceMap, latestStepMap, paneId, statuses]);

  const renderAgentCard = ({
    wid,
    title,
    agentType,
    status,
    subtitle,
    resultSummary,
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
    resultSummary?: string;
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
              <span>打开</span>
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
            <button
              type="button"
              data-id="team-panel-worker-menu-settings"
              disabled={!onOpenSettings}
              onClick={() => {
                if (!onOpenSettings) return;
                setOpenMenuId(null);
                onOpenSettings();
              }}
              className={`flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-sm transition-colors ${
                onOpenSettings
                  ? 'cursor-pointer text-zinc-300 hover:bg-white/[0.06]'
                  : 'cursor-not-allowed text-zinc-600'
              }`}
            >
              <Settings className="w-3.5 h-3.5 shrink-0" />
              <span>设置</span>
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
        <AgentTypeAvatar agentType={agentType} title={title} />
        <div className="flex-1 min-w-0 pr-7">
          <div className="flex items-center gap-1.5">
            <h3 className={`text-sm font-medium truncate ${active ? 'text-blue-300' : 'text-zinc-300'}`}>{title}</h3>
            <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${statusDot(status)}`} />
          </div>
          <p className={`text-xs font-mono mt-0.5 truncate ${active ? 'text-blue-400/50' : 'text-zinc-600'}`}>
            {subtitle}
          </p>
          {resultSummary ? (
            <p className="text-xs text-zinc-500 mt-1 truncate" data-id="team-panel-worker-step-summary">
              {resultSummary}
            </p>
          ) : null}
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
            icon: <AgentTypeMiniAvatar agentType={a.agent_type} title={a.title || shortId(a.pane_id)} />,
          }))}
          onChange={v => bind(v)}
          placeholder="+ 绑定工作实例..."
          searchable
          className="flex-1"
        />
        <button
          data-id="team-panel-sync-instances"
          onClick={syncInstances}
          disabled={syncingInstances}
          className="flex items-center text-sm px-2 py-1.5 rounded border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50"
          title="同步实例"
        >
          {syncingInstances ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
        </button>
        <button
          data-id="team-panel-create-worker"
          onClick={() => setCreateDialogOpen(true)}
          disabled={creating}
          className="flex items-center text-sm px-2 py-1.5 rounded border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50"
          title="创建并绑定新工作实例"
        >
          {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Plus className="w-3.5 h-3.5" />}
        </button>
      </div>

      <CreateAgentDialog
        open={createDialogOpen}
        submitting={creating}
        onClose={() => setCreateDialogOpen(false)}
        onSubmit={createAndBind}
        title="创建并绑定工作实例"
        submitLabel="创建并绑定"
      />


      <div className="flex-1 min-w-0 overflow-x-hidden overflow-y-auto hide-scrollbar" data-id="team-panel-worker-list">
        <div className="p-1.5 border-b border-[var(--vsc-border)]" data-id="team-panel-current-agent">
          {renderAgentCard({
            wid: paneId,
            title: currentAgent.title,
            agentType: currentAgent.agentType,
            status: currentAgent.status,
            subtitle: currentAgent.subtitle,
            resultSummary: currentAgent.resultSummary,
            active: (activePaneId || paneId) === paneId,
            onClick: () => {
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
          <div className="flex h-full w-full min-w-0 flex-col" data-id="team-panel-groups">
            {groupedBindings.map(group => (
              <div
                key={group.instance?.id || 'local'}
                style={{ padding: 4 }}
                className={group.instance ? 'border-b border-[var(--vsc-border)]' : ''}
                data-id={`team-panel-group-${group.instance?.instance_key || group.instance?.machine_key || 'local'}`}
              >
                
                {group.items.map(b => {
                  const wid = shortId(b.name);
                  const s = getStatus(wid);
                  const step = latestStepMap.get(`${b.machine_id || 0}:${wid}`);
                  const subtitleParts = [wid, statusLabel(s)];
                  if (b.instance_label || b.machine_label) subtitleParts.push(b.instance_label || b.machine_label || '');
                  if (step?.title) subtitleParts.push(`${step.title}${step.status ? ` [${step.status}]` : ''}`);
                  return renderAgentCard({
                    wid,
                    title: getName(b),
                    agentType: agentTypeById.get(wid) || '',
                    status: s,
                    subtitle: subtitleParts.join(' · '),
                    resultSummary: step?.result_summary,
                    active: activePaneId === wid,
                    opened: openedPaneIds.includes(wid),
                    onClick: () => {
                      if (onOpenInCurrentPane) {
                        onOpenInCurrentPane(wid);
                        return;
                      }
                      window.location.hash = `#/agent/${wid}`;
                    },
                    onRestart: () => restartPane(wid, getName(b), isApiOnlyInstance(b.machine_id ? instanceMap.get(b.machine_id) : undefined)),
                    onOpenSettings: () => onOpenSettingsPane?.(wid),
                    canRestart: !isApiOnlyInstance(b.machine_id ? instanceMap.get(b.machine_id) : undefined),
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
            <p className="text-sm">先绑定一个工作实例开始</p>
          </div>
        )}
      </div>
    </div>
  );
}
