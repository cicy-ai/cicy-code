import React, { useState, useEffect } from 'react';
import { Loader2, Plus } from 'lucide-react';
import apiService from '../services/api';
import { urls } from '../config';
import { WebFrame } from './WebFrame';
import { CaptureModal } from './CaptureModal';

interface Agent {
  id: number;
  name: string;
  status: string;
  title?: string;
}

interface AgentsListViewProps {
  paneId: string;
  token: string | null;
  ttydPreview?: string;
  isDragging?: boolean;
  onAgentsChange?: (agents: string[]) => void;
  onCaptureOpen?: (isOpen: boolean) => void;
}

export const AgentsListView: React.FC<AgentsListViewProps> = ({ paneId, token, ttydPreview, isDragging, onAgentsChange, onCaptureOpen }) => {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [allAgents, setAllAgents] = useState<any[]>([]);
  const [selectedAgent, setSelectedAgent] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [iframeKeys, setIframeKeys] = useState<Record<string, number>>({});
  const [heights, setHeights] = useState<Record<string, number>>(() => {
    const saved = localStorage.getItem(`${paneId}_agentHeights`);
    return saved ? JSON.parse(saved) : {};
  });
  const [resizing, setResizing] = useState<string | null>(null);
  const [startHeight, setStartHeight] = useState<number>(0);
  const [startY, setStartY] = useState<number>(0);
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [captureTarget, setCaptureTarget] = useState<string | null>(null);
  const [paneStatusMap, setPaneStatusMap] = useState<Record<string, any>>({});

  useEffect(() => {
    fetchAllAgents();
  }, [paneId]);

  useEffect(() => {
    if (allAgents.length > 0) {
      fetchAgents();
    }
  }, [paneId, allAgents.length]);

  const fetchAgents = async () => {
    setLoading(true);
    try {
      const { data } = await apiService.getAgentsByPane(paneId);
      const agentsWithTitles = data.map((agent: Agent) => {
        const agentInfo = allAgents.find(a => a.pane_id === agent.name);
        return { ...agent, title: agentInfo?.title || agent.name };
      });
      setAgents(agentsWithTitles);
      onAgentsChange?.(data.map((a: Agent) => a.name));
    } catch (err) {
      console.error('Failed to fetch agents:', err);
    } finally {
      setLoading(false);
    }
  };

  const fetchAllAgents = async () => {
    try {
      const { data } = await apiService.getAllStatus();
      setPaneStatusMap(data || {});
      setAllAgents(Object.values(data || {}).map((v: any) => ({ pane_id: v.pane_id, title: v.title })));
    } catch (err) {
      console.error('Failed to fetch all agents:', err);
    }
  };

  const handleAddAgent = async () => {
    if (!selectedAgent) return;
    try {
      await apiService.bindAgent({ pane_id: paneId, agent_name: selectedAgent });
      fetchAgents();
      setSelectedAgent('');
    } catch (err) {
      console.error('Failed to add agent:', err);
      alert(`Error: ${err}`);
    }
  };

  const handleRemoveAgent = async (agentId: number) => {
    try {
      await apiService.unbindAgent(agentId);
      fetchAgents();
    } catch (err) {
      console.error('Failed to remove agent:', err);
    }
  };

  const handleReloadIframe = (agentName: string) => {
    setIframeKeys(prev => ({ ...prev, [agentName]: (prev[agentName] || 0) + 1 }));
  };


  const handleMouseDown = (agentName: string, e: React.MouseEvent) => {
    e.preventDefault();
    setResizing(agentName);
    setStartY(e.clientY);
    setStartHeight(heights[agentName] || 150);
  };

  useEffect(() => {
    if (resizing === null) return;

    let currentHeight = startHeight;
    const handleMouseMove = (e: MouseEvent) => {
      const delta = e.clientY - startY;
      const newHeight = Math.max(200, startHeight + delta);
      currentHeight = newHeight;
      setHeights(prev => ({ ...prev, [resizing]: newHeight }));
    };

    const handleMouseUp = () => {
      if (resizing !== null) {
        setHeights(prev => {
          const newHeights = { ...prev, [resizing]: currentHeight };
          localStorage.setItem(`${paneId}_agentHeights`, JSON.stringify(newHeights));
          return newHeights;
        });
      }
      setResizing(null);
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [resizing, startY, startHeight]);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full bg-vsc-bg">
        <Loader2 className="animate-spin text-vsc-text-secondary" />
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full bg-vsc-bg p-3">
      <input
        type="text"
        value={searchQuery}
        onChange={(e) => setSearchQuery(e.target.value)}
        placeholder="Search agents..."
        className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-3 py-1.5 mb-3 focus:outline-none focus:border-vsc-accent"
      />
      <div className="flex gap-2 mb-3">
        <select
          value={selectedAgent}
          onChange={(e) => setSelectedAgent(e.target.value)}
          className="w-64 bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-3 py-1 focus:outline-none focus:border-vsc-accent"
        >
          <option value="">Select an agent...</option>
          {allAgents
            .filter(agent => agent.pane_id !== paneId && agent.pane_id !== ttydPreview && !agents.find(a => a.name === agent.pane_id))
            .map(agent => (
              <option key={agent.pane_id} value={agent.pane_id}>{agent.title || agent.pane_id}</option>
            ))}
        </select>
        <button
          onClick={handleAddAgent}
          disabled={!selectedAgent}
          className="bg-vsc-button hover:bg-vsc-button-hover disabled:bg-vsc-bg-active disabled:cursor-not-allowed text-white px-3 py-1 rounded flex items-center gap-1 text-sm"
        >
          <Plus size={14} /> Add
        </button>
        <button
          onClick={async () => {
            try {
              const { data } = await apiService.createPane({
                win_name: `SubAgent(${paneId})`,
                workspace: '',
                init_script: 'pwd'
              });
              if (data.pane_id) {
                await apiService.bindAgent({ pane_id: paneId, agent_name: data.pane_id });
                fetchAgents();
                fetchAllAgents();
              } else {
                alert(`Failed: ${data.detail || data.error || 'Unknown error'}`);
              }
            } catch (err) {
              console.error(err);
              alert(`Error: ${err}`);
            }
          }}
          className="bg-green-600 hover:bg-green-700 text-white px-3 py-1 rounded flex items-center gap-1 text-sm"
        >
          <Plus size={14} /> New Agent
        </button>
      </div>

      {agents.length === 0 ? (
        <div className="flex-1 flex items-center justify-center">
          <p className="text-vsc-text-muted text-sm">No agents bound</p>
        </div>
      ) : (
        <div className="flex flex-col gap-2 h-full overflow-auto">
          {agents.filter(a => a.name !== ttydPreview && (a.title?.toLowerCase().includes(searchQuery.toLowerCase()) || a.name.toLowerCase().includes(searchQuery.toLowerCase()))).map(agent => (
            <div 
              key={agent.id} 
              className="bg-vsc-bg-secondary border border-vsc-border rounded relative" 
              style={{height: `${heights[agent.name] || 150}px`}}
            >
              {resizing !== null && (
                <div className="absolute inset-0 z-20 bg-transparent" />
              )}
              <div className="absolute top-2 right-2 z-10 text-sm">
                {paneStatusMap[agent.name]?.role === 'master' ? '📋' : paneStatusMap[agent.name]?.role === 'worker' ? '🔧' : ''}
              </div>
              <div className="absolute top-2 right-8 z-10 flex flex-col items-end gap-1 bg-vsc-bg/80 rounded p-1">
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => setCaptureTarget(agent.name)}
                    className="text-vsc-text-muted hover:text-vsc-text text-sm px-1"
                    title="Capture pane output"
                  >📋</button>
                  <span className="text-white text-sm font-medium px-2 py-1">
                    {agent.title || agent.name.replace(':main.0', '')}
                  </span>
                </div>
                {paneStatusMap[agent.name]?.default_model && (
                  <div className="text-gray-500 text-xs px-2">{paneStatusMap[agent.name].default_model}</div>
                )}
                {paneStatusMap[agent.name]?.status === 'thinking' && paneStatusMap[agent.name]?.currentTask && (
                  <div className="text-gray-400 text-xs px-2 max-w-xs truncate">
                    {(paneStatusMap[agent.name]?.currentTask ?? '').slice(0, 50)}
                  </div>
                )}
              </div>
              <WebFrame
                loading="lazy"
                key={iframeKeys[agent.name] || 0}
                src={urls.ttyd(agent.name, token)}
                className="w-full h-full rounded align-top"
                style={{verticalAlign: 'top'}}
              />
              {isDragging && <div className="absolute inset-0 z-20"></div>}
              <div
                onMouseDown={(e) => handleMouseDown(agent.name, e)}
                className="absolute bottom-0 left-0 right-0 h-2 cursor-ns-resize hover:bg-vsc-button-hover/20 transition-colors"
              />
            </div>
          ))}
        </div>
      )}
      {captureTarget && <CaptureModal paneId={captureTarget!} onClose={() => setCaptureTarget(null)} />}
    </div>
  );
};
