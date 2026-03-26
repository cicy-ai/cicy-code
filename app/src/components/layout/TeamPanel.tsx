import { useState, useEffect, useCallback, useMemo } from 'react';
import { Users, Plus, X, Loader2, ExternalLink, Box, RefreshCw } from 'lucide-react';
import apiService from '../../services/api';
import { urls } from '../../config';
import { WebFrame } from '../WebFrame';
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

interface Props { paneId: string; token: string; }

export default function TeamPanel({ paneId, token }: Props) {
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
      const { data } = await apiService.createPane({ role: 'worker', agent_type: 'kiro-cli chat' });
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
    return 'Offline';
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

      <div className="px-3 py-2 border-b border-[var(--vsc-border)] text-xs text-zinc-500 flex items-center gap-2" data-id="team-panel-instance-summary">
        <Box className="w-3 h-3" />
        <span>{instances.length} instances</span>
        {instances.map(instance => (
          <span key={instance.id} className="text-[11px] text-zinc-600">
            {(instance.instance_label || instance.label || instance.instance_key || instance.machine_key)}
            {instance.runtime_kind ? ` · ${instance.runtime_kind}` : ''} · {instance.status}
          </span>
        ))}
      </div>

      <div className="flex-1 overflow-y-auto" data-id="team-panel-worker-list">
        {bindings.length > 0 ? (
          <div className="flex flex-col h-full" data-id="team-panel-groups">
            {groupedBindings.map(group => (
              <div key={group.instance?.id || 'local'} className="border-b border-[var(--vsc-border)]" data-id={`team-panel-group-${group.instance?.instance_key || group.instance?.machine_key || 'local'}`}>
                <div className="px-3 py-2 text-xs text-zinc-500 bg-black/20 flex items-center gap-2" data-id="team-panel-group-header">
                  <Box className="w-3 h-3" />
                  <span>{group.instance?.instance_label || group.instance?.label || 'Local runtime'}</span>
                  <span className="text-zinc-600">{group.instance?.status || 'local'}</span>
                  {isApiOnlyInstance(group.instance) ? <span className="text-amber-500">API-only</span> : null}
                </div>
                {group.items.map(b => {
                  const wid = shortId(b.name);
                  const s = getStatus(wid);
                  const step = latestStepMap.get(`${b.machine_id || 0}:${wid}`);
                  return (
                    <div key={b.id} className="flex flex-col min-h-[220px]" style={{ flex: '1 1 0' }} data-id={`team-panel-worker-${wid}`}>
                      <div className="flex items-center gap-2 px-3 py-1 bg-black/40 border-b border-[var(--vsc-border)] flex-shrink-0" data-id="team-panel-worker-header">
                        <span className={`w-2 h-2 rounded-full flex-shrink-0 ${statusDot(s)}`} />
                        <span className="text-sm text-zinc-300 font-medium truncate">{getName(b)}</span>
                        <span className="text-sm text-zinc-600 font-mono">{wid}</span>
                        <span className="text-sm text-zinc-600">·</span>
                        <span className="text-sm text-zinc-500">{statusLabel(s)}</span>
                        {(b.instance_label || b.machine_label) && <span className="text-xs text-zinc-600">· {b.instance_label || b.machine_label}</span>}
                        {step?.title && <span className="text-xs text-zinc-500 truncate">· {step.title} [{step.status}]</span>}
                        <div className="flex-1" />
                        {!isApiOnlyInstance(group.instance) ? (
                          <button
                            data-id="team-panel-worker-open"
                            onClick={() => window.open(`#/agent/${wid}`, '_blank')}
                            className="text-zinc-600 hover:text-zinc-300 transition-colors"
                            title="Open in new window"
                          >
                            <ExternalLink className="w-3 h-3" />
                          </button>
                        ) : null}
                        <button
                          data-id="team-panel-worker-unbind"
                          onClick={() => confirm(<>Unbind <span className="text-zinc-100 font-medium">{getName(b)}</span>?</>, () => unbind(b))}
                          className="text-zinc-600 hover:text-red-400 transition-colors"
                          title="Unbind"
                        >
                          <X className="w-3 h-3" />
                        </button>
                      </div>
                      {step?.result_summary ? (
                        <div className="px-3 py-2 text-xs text-zinc-400 border-b border-[var(--vsc-border)]" data-id="team-panel-worker-step-summary">
                          {step.result_summary}
                        </div>
                      ) : null}
                      <div className="flex-1 relative" data-id="team-panel-worker-terminal">
                        {!isApiOnlyInstance(group.instance) ? (
                          <WebFrame
                            src={urls.ttydOpen(wid, token)}
                            className="w-full h-full border-0 bg-black"
                            title={`worker-${wid}`}
                          />
                        ) : (
                          <div className="h-full flex items-center justify-center text-xs text-zinc-500" data-id="team-panel-worker-api-only-empty">
                            Cloud Run / API-only node does not support ttyd terminal
                          </div>
                        )}
                      </div>
                    </div>
                  );
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
