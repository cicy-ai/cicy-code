import React, { useState, useEffect } from 'react';
import { Plus } from 'lucide-react';
import apiService from '../services/api';

interface AgentControlsProps {
  paneId: string;
  token: string;
  boundAgents?: string[];
  onAgentAdded?: () => void;
}

export const AgentControls: React.FC<AgentControlsProps> = ({ paneId, token, boundAgents = [], onAgentAdded }) => {
  const [allAgents, setAllAgents] = useState<Array<{ pane_id: string; title?: string }>>([]);
  const [selectedAgent, setSelectedAgent] = useState('');
  const [isCreating, setIsCreating] = useState(false);

  useEffect(() => {
    fetchAllAgents();
  }, [paneId]);

  const fetchAllAgents = async () => {
    try {
      const { data } = await apiService.getPaneList();
      setAllAgents(Array.isArray(data) ? data : (data.panes || []));
    } catch (err) {
      console.error('Failed to fetch all agents:', err);
    }
  };

  const handleAddAgent = async () => {
    if (!selectedAgent) return;
    try {
      const { data: newAgent } = await apiService.bindAgent({ pane_id: paneId, agent_name: selectedAgent });
      setSelectedAgent('');
      window.dispatchEvent(new CustomEvent('addAgent', { detail: { 
        id: newAgent.id, 
        name: selectedAgent, 
        status: newAgent.status || 'active',
        title: selectedAgent
      }}));
      onAgentAdded?.();
    } catch (err: any) {
      const detail = err.response?.data?.detail;
      if (detail?.includes('already bound')) {
        alert(`This agent is already bound. Please unbind it first or refresh the page.`);
      } else {
        alert(`Failed to add agent: ${detail || err.message}`);
      }
    }
  };

  const handleNewAgent = async () => {
    if (!confirm('Create a new agent?')) return;
    setIsCreating(true);
    try {
      const { data } = await apiService.createPane({
        win_name: `SubAgent(${paneId})`,
        workspace: '',
        init_script: 'pwd'
      });
      if (data.pane_id) {
        const { data: newAgent } = await apiService.bindAgent({ pane_id: paneId, agent_name: data.pane_id });
        await fetchAllAgents();
        window.dispatchEvent(new CustomEvent('addAgent', { detail: { 
          id: newAgent.id, 
          name: data.pane_id, 
          status: newAgent.status || 'active',
          title: `SubAgent(${paneId})`
        }}));
        onAgentAdded?.();
      } else {
        alert(`Failed: ${data.detail || data.error || 'Unknown error'}`);
      }
    } catch (err: any) {
      alert(`Error: ${err.response?.data?.detail || err.message}`);
    } finally {
      setIsCreating(false);
    }
  };

  return (
    <>
      {isCreating && (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center">
          <div className="bg-vsc-bg border border-vsc-border rounded p-4 flex items-center gap-3">
            <div className="animate-spin rounded-full h-5 w-5 border-2 border-vsc-text border-t-transparent"></div>
            <span className="text-vsc-text">Creating agent...</span>
          </div>
        </div>
      )}
      <div className="flex gap-2 items-center">
        <select
          value={selectedAgent}
          onChange={(e) => setSelectedAgent(e.target.value)}
          className="bg-vsc-bg border border-vsc-border text-vsc-text px-2 py-0.5 rounded text-xs max-w-[120px]"
        >
          <option value="">Select agent</option>
          {allAgents.filter(a => a.pane_id !== paneId && !boundAgents.includes(a.pane_id)).map(agent => (
            <option key={agent.pane_id} value={agent.pane_id}>
              {agent.title || agent.pane_id}
            </option>
          ))}
        </select>
        <button
          onClick={handleAddAgent}
          disabled={!selectedAgent}
          className="bg-vsc-button hover:bg-vsc-button-hover disabled:bg-vsc-bg-active disabled:cursor-not-allowed text-white px-2 py-0.5 rounded flex items-center gap-1 text-xs"
        >
          <Plus size={12} /> Bind
        </button>
        <button
          onClick={handleNewAgent}
          className="bg-green-600 hover:bg-green-700 text-white px-2 py-0.5 rounded flex items-center gap-1 text-xs"
        >
          <Plus size={12} /> New Agent
        </button>
      </div>
    </>
  );
};
