import React, { useState, useEffect, useCallback } from 'react';
import { Users, Plus, X, Loader2, ExternalLink } from 'lucide-react';
import apiService from '../../services/api';
import { urls } from '../../config';
import { WebFrame } from '../WebFrame';
import { useDialog } from '../../contexts/DialogContext';

import Select from '../ui/Select';

interface Agent { pane_id: string; title?: string; role?: string; ttyd_port?: number; active?: number; }
interface StatusInfo { status?: string; isThinking?: boolean; title?: string; }
interface Binding { id: number; pane_id: string; name: string; status?: string; }

interface Props { paneId: string; token: string; }

export default function TeamPanel({ paneId, token }: Props) {
  const [allAgents, setAllAgents] = useState<Agent[]>([]);
  const [bindings, setBindings] = useState<Binding[]>([]);
  const [statuses, setStatuses] = useState<Record<string, StatusInfo>>({});
  const [creating, setCreating] = useState(false);
  const { confirm } = useDialog();

  const shortId = (id: string) => (id || '').replace(/:.*$/, '');
  const fullId = (id: string) => id.includes(':') ? id : `${id}:main.0`;

  const load = useCallback(async () => {
    try {
      const [pRes, bRes, sRes] = await Promise.all([
        apiService.getPanes(),
        apiService.getAgentsByPane(paneId),
        apiService.getAllStatus(),
      ]);
      setAllAgents(Array.isArray(pRes.data) ? pRes.data : pRes.data?.panes || []);
      setBindings(Array.isArray(bRes.data) ? bRes.data : []);
      if (sRes.data) setStatuses(sRes.data);
    } catch {}
  }, [paneId]);

  useEffect(() => { load(); const id = setInterval(load, 5000); return () => clearInterval(id); }, [load]);

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
    try { await apiService.unbindAgent(binding.id); load(); } catch {}
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
    } catch {} finally { setCreating(false); }
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

  const getName = (wid: string) => {
    const s = getStatus(wid);
    return s.title || allAgents.find(a => shortId(a.pane_id) === wid)?.title || wid;
  };

  return (
    <div className="h-full flex flex-col">
      {/* Top bar: select + create */}
      <div className="px-3 py-2 border-b border-[var(--vsc-border)] flex items-center gap-2 flex-shrink-0">
        <Select
          options={available.map(a => ({ value: a.pane_id, label: a.title || shortId(a.pane_id), sub: shortId(a.pane_id) }))}
          onChange={v => bind(v)}
          placeholder="+ Bind worker..."
          searchable
          className="flex-1"
        />
        <button
          onClick={createAndBind}
          disabled={creating}
          className="flex items-center text-sm px-2 py-1.5 rounded border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50"
          title="Create new worker & bind"
        >
          {creating ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Plus className="w-3.5 h-3.5" />}
        </button>
      </div>

      {/* Workers grid — all visible at once */}
      <div className="flex-1 overflow-y-auto">
        {bindings.length > 0 ? (
          <div className="flex flex-col h-full">
            {bindings.map(b => {
              const wid = shortId(b.name);
              const s = getStatus(wid);
              return (
                <div key={b.id} className="flex flex-col min-h-[200px]" style={{ flex: '1 1 0' }}>
                  {/* Worker header */}
                  <div className="flex items-center gap-2 px-3 py-1 bg-black/40 border-b border-[var(--vsc-border)] flex-shrink-0">
                    <span className={`w-2 h-2 rounded-full flex-shrink-0 ${statusDot(s)}`} />
                    <span className="text-sm text-zinc-300 font-medium truncate">{getName(wid)}</span>
                    <span className="text-sm text-zinc-600 font-mono">{wid}</span>
                    <span className="text-sm text-zinc-600">·</span>
                    <span className="text-sm text-zinc-500">{statusLabel(s)}</span>
                    <div className="flex-1" />
                    <button
                      onClick={() => window.open(`#/agent/${wid}`, '_blank')}
                      className="text-zinc-600 hover:text-zinc-300 transition-colors"
                      title="Open in new window"
                    >
                      <ExternalLink className="w-3 h-3" />
                    </button>
                    <button
                      onClick={() => confirm(<>Unbind <span className="text-zinc-100 font-medium">{getName(shortId(b.name))}</span>?</>, () => unbind(b))}
                      className="text-zinc-600 hover:text-red-400 transition-colors"
                      title="Unbind"
                    >
                      <X className="w-3 h-3" />
                    </button>
                  </div>
                  {/* Worker ttyd */}
                  <div className="flex-1 relative">
                    <WebFrame
                      src={urls.ttydOpen(wid, token)}
                      className="w-full h-full border-0 bg-black"
                      title={`worker-${wid}`}
                    />
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center h-full text-zinc-600">
            <Users className="w-8 h-8 mb-2 opacity-20" />
            <p className="text-sm">Bind a worker to start</p>
          </div>
        )}
      </div>
    </div>
  );
}
