import React, { useState, useEffect } from 'react';
import { Loader2, Plus } from 'lucide-react';
import apiService from '../services/api';

interface Agent {
  id: number;
  name: string;
  status: string;
}

interface AgentsViewProps {
  paneId: string;
  token: string | null;
}

export const AgentsView: React.FC<AgentsViewProps> = ({ paneId, token }) => {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [allAgents, setAllAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [selectedAgent, setSelectedAgent] = useState<string>('');

  useEffect(() => {
    fetchAgents();
    fetchAllAgents();
  }, [paneId]);

  const fetchAgents = async () => {
    try {
      const { data } = await apiService.getAgentsByPane(paneId);
      setAgents(data);
    } catch (err) {
      console.error('Failed to fetch agents:', err);
    } finally {
      setLoading(false);
    }
  };

  const fetchAllAgents = async () => {
    try {
      const { data } = await apiService.listPanes();
      setAllAgents(data);
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

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full bg-vsc-bg">
        <Loader2 className="animate-spin text-vsc-text-secondary" />
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full bg-vsc-bg p-4">
      <h2 className="text-lg font-semibold text-white mb-4">Bound Agents</h2>
      
      <div className="flex gap-2 mb-4">
        <select
          value={selectedAgent}
          onChange={(e) => setSelectedAgent(e.target.value)}
          className="flex-1 bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-3 py-2 focus:outline-none focus:border-vsc-accent"
        >
          <option value="">Select an agent...</option>
          {allAgents.filter(agent => !agent.name.startsWith('ttyd_preview')).map(agent => (
            <option key={agent.name} value={agent.name}>{agent.name}</option>
          ))}
        </select>
        <button
          onClick={handleAddAgent}
          disabled={!selectedAgent}
          className="bg-vsc-button hover:bg-vsc-button-hover disabled:bg-vsc-bg-active disabled:cursor-not-allowed text-white px-4 py-2 rounded flex items-center gap-2"
        >
          <Plus size={16} /> Add
        </button>
      </div>

      <div className="space-y-2 overflow-y-auto">
        {agents.filter(agent => !agent.name.startsWith('ttyd_preview')).length === 0 ? (
          <div className="flex flex-col items-center justify-center h-64 text-vsc-text-muted">
            <svg className="w-16 h-16 mb-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" />
            </svg>
            <p className="text-sm">No agents bound to this pane</p>
          </div>
        ) : (
          agents.filter(agent => !agent.name.startsWith('ttyd_preview')).map(agent => (
            <div key={agent.id} className="bg-vsc-bg-secondary border border-vsc-border rounded p-3 flex items-center justify-between">
              <div>
                <p className="text-white font-medium">{agent.name}</p>
                <p className="text-xs text-vsc-text-secondary">{agent.status}</p>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
};
