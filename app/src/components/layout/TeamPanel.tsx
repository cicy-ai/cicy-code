import { useState, useEffect, useCallback, useMemo } from 'react';
import { Users, Plus, X, Loader2, ExternalLink, Box, RefreshCw } from 'lucide-react';
import apiService from '../../services/api';
import { useDialog } from '../../contexts/DialogContext';
import Select from '../ui/Select';

interface Agent {
  pane_id: string;
  title?: string;
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
}

export default function TeamPanel({ paneId, onOpenInCurrentPane, openedPaneIds = [] }: Props) {
  const [allAgents, setAllAgents] = useState<Agent[]>([]);
  const [bindings, setBindings] = useState<Binding[]>([]);
  const [statuses, setStatuses] = useState<Record<string, StatusInfo>>({});
  const [instances, setInstances] = useState<Machine[]>([]);
  const [steps, setSteps] = useState<Step[]>([]);
  const [creating, setCreating] = useState(false);
  const [syncingInstances, setSyncingInstances] = useState(false);
  const { confirm } = useDialog();

  const shortId = (id: string) => (id || '').replace(/:.*$/, '');
  const fullId = (id: string) => id.includes(':') ? id : `${id}:main.0`;

  const load = useCallback(async () => {
    try {
      const [pRes, bRes, sRes, mRes, qRes] = await Promise.all([
        apiService.getPanes(),
        apiService.getAgentsByPane(paneId),
        apiService.getAllStatus(),
        apiService.getMachines(),
        apiService.getCollabSteps(),
      ]);
      setAllAgents(Array.isArray(pRes.data) ? pRes.data : pRes.data?.panes || []);
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

  const boundIds = new Set(bindings.map(b => shortId(b.name)));
  const available = allAgents.filter(a => {
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
      load();
    } catch {}
  };

  const createAndBind = async () => {
    setCreating(true);
    try {
      const { data } = await apiService.createPane({ role: 'worker', agent_type: 'codex' });
      const newId = data?.pane_id || data?.session;
      if (newId) {
        await apiService.bindAgent({ pane_id: paneId, agent_name: shortId(newId) });
        load();
      }
    } catch {} finally {
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
    if (s.isThinking || s.status === 'thinking') return 'Thinking';
    if (s.status === 'tool_use') return 'Running';
    if (s.status === 'idle' || s.status === 'text') return 'Idle';
    return '';
  };

  const getName = (binding: Binding) => {
    const wid = shortId(binding.name);
    const s = getStatus(wid);
    return binding.title || s.title || allAgents.find(a => shortId(a.pane_id) === wid)?.title || wid;
  };

  const instanceMap = useMemo(() => new Map(instances.map(m => [m.id, m])), [instances]);
  const isApiOnlyInstance = useCallback((instance?: Machine) => {
    if (!instance) return false;
    if (instance.runtime_kind === 'cloudrun') return true;
    return instance.capabilities?.supports_tmux === false;
  }, []);
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

  const currentAgent = useMemo(() => {
    const agent = allAgents.find(a => shortId(a.pane_id) === paneId);
    const machine = agent?.machine_id ? instanceMap.get(agent.machine_id) : undefined;
    const step = latestStepMap.get(`${agent?.machine_id || 0}:${paneId}`);
    const status = getStatus(paneId);
    const subtitleParts = [paneId, statusLabel(status)];
    if (machine?.instance_label || machine?.label) subtitleParts.push(machine?.instance_label || machine?.label || '');
    if (step?.title) subtitleParts.push(`${step.title}${step.status ? ` [${step.status}]` : ''}`);
    return {
      title: agent?.title || status.title || paneId,
      status,
      subtitle: subtitleParts.join(' · '),
      resultSummary: step?.result_summary || '',
    };
  }, [allAgents, instanceMap, latestStepMap, paneId, statuses]);

  const renderAgentCard = ({
    wid,
    title,
    status,
    subtitle,
    resultSummary,
    active = false,
    opened = false,
    onClick,
    onRemove,
  }: {
    wid: string;
    title: string;
    status: StatusInfo;
    subtitle: string;
    resultSummary?: string;
    active?: boolean;
    opened?: boolean;
    onClick: () => void;
    onRemove?: () => void;
  }) => (
    <div
      key={wid}
      data-id={`team-panel-worker-${wid}`}
      onClick={onClick}
      className={`w-full flex items-center gap-3 border p-3 rounded-xl transition-all group relative cursor-pointer ${
        active
          ? 'border-blue-500/50 bg-blue-500/[0.08] ring-1 ring-blue-500/20'
          : opened
            ? 'border-cyan-500/40 bg-cyan-500/[0.06] ring-1 ring-cyan-500/10'
          : 'bg-white/[0.02] border-[var(--vsc-border)] hover:border-white/[0.08]'
      }`}
    >
      {onRemove ? (
        <button
          data-id="team-panel-worker-unbind"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="absolute right-0 top-0 w-5 h-5 rounded-full bg-red-500/80 flex items-center justify-center transition-colors cursor-pointer opacity-0 group-hover:opacity-100 hover:bg-red-500 z-10"
          title="Unbind"
        >
          <X className="w-3 h-3 text-white" />
        </button>
      ) : null}
      <div className="flex items-center gap-3 flex-1 min-w-0 text-left">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-1.5">
            <h3 className={`text-sm font-medium truncate ${active ? 'text-blue-300' : opened ? 'text-cyan-200' : 'text-zinc-300'}`}>{title}</h3>
            <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${statusDot(status)}`} />
          </div>
          <p className={`text-xs font-mono mt-0.5 truncate ${active ? 'text-blue-400/50' : opened ? 'text-cyan-400/60' : 'text-zinc-600'}`}>
            {subtitle}
          </p>
          {resultSummary ? (
            <p className="text-xs text-zinc-500 mt-1 truncate" data-id="team-panel-worker-step-summary">
              {resultSummary}
            </p>
          ) : null}
        </div>
      </div>
      <button
        data-id="team-panel-worker-open"
        onClick={(e) => {
          e.stopPropagation();
          window.open(`#/agent/${wid}`, '_blank');
        }}
        className="p-1 rounded transition-colors shrink-0 cursor-pointer text-zinc-700 opacity-0 group-hover:opacity-100 hover:text-zinc-400"
        title="Open in new window"
      >
        <ExternalLink className="w-3.5 h-3.5" />
      </button>
    </div>
  );

  return (
    <div className="h-full flex flex-col" data-id="team-panel-root">
      <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-center gap-2 flex-shrink-0" data-id="team-panel-toolbar">
        <Select
          options={available.map(a => ({ value: a.pane_id, label: a.title || shortId(a.pane_id), sub: shortId(a.pane_id) }))}
          onChange={v => bind(v)}
          placeholder="+ Bind worker..."
          searchable
          className="flex-1"
        />
        <button
          data-id="team-panel-sync-instances"
          onClick={syncInstances}
          disabled={syncingInstances}
          className="flex items-center text-sm px-2 py-1.5 rounded border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50"
          title="Sync instances"
        >
          {syncingInstances ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <RefreshCw className="w-3.5 h-3.5" />}
        </button>
        <button
          data-id="team-panel-create-worker"
          onClick={createAndBind}
          disabled={creating}
          className="flex items-center text-sm px-2 py-1.5 rounded border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50"
          title="Create new worker & bind"
        >
          {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Plus className="w-3.5 h-3.5" />}
        </button>
      </div>

    

      <div className="flex-1 overflow-y-auto" data-id="team-panel-worker-list">
        <div className="p-1.5 border-b border-[var(--vsc-border)]" data-id="team-panel-current-agent">
          {renderAgentCard({
            wid: paneId,
            title: currentAgent.title,
            status: currentAgent.status,
            subtitle: currentAgent.subtitle,
            resultSummary: currentAgent.resultSummary,
            active: true,
            onClick: () => { window.location.hash = `#/agent/${paneId}`; },
          })}
        </div>
        {bindings.length > 0 ? (
          <div className="flex flex-col h-full" data-id="team-panel-groups">
            {groupedBindings.map(group => (
              <div key={group.instance?.id || 'local'} style={{padding:4}} className="border-b border-[var(--vsc-border)]" data-id={`team-panel-group-${group.instance?.instance_key || group.instance?.machine_key || 'local'}`}>
                
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
                    status: s,
                    subtitle: subtitleParts.join(' · '),
                    resultSummary: step?.result_summary,
                    opened: openedPaneIds.includes(wid),
                    onClick: () => {
                      if (onOpenInCurrentPane) {
                        onOpenInCurrentPane(wid);
                        return;
                      }
                      window.location.hash = `#/agent/${wid}`;
                    },
                    onRemove: () => confirm(<>Unbind <span className="text-zinc-100 font-medium">{getName(b)}</span>?</>, () => unbind(b)),
                  });
                })}
              </div>
            ))}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full text-zinc-600" data-id="team-panel-empty">
            <Users className="w-8 h-8 mb-2 opacity-20" />
            <p className="text-sm">Bind a worker to start</p>
          </div>
        )}
      </div>
    </div>
  );
}
