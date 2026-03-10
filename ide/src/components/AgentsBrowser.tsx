import React, { useEffect, useState } from 'react';
import apiService from '../services/api';
import { urls } from '../config';

interface AgentsBrowserProps {
  token: string;
  onAddAgent: (paneId: string, title:string,url: string) => void;
  existingTabs: string[];
  onNewAgent?: () => void;
}

interface Agent {
  pane_id: string;
  status?: string;
  title?: string;
  [key: string]: any;
}

export const AgentsBrowser: React.FC<AgentsBrowserProps> = ({ token, onAddAgent, existingTabs, onNewAgent }) => {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');

  useEffect(() => {
    // Load from cache first
    const cached = localStorage.getItem('agents_cache');
    if (cached) {
      try {
        setAgents(JSON.parse(cached));
      } catch (e) {
        console.error('Failed to parse cache:', e);
      }
    }
    
    setLoading(false);

    // Fetch from API
    apiService.getAllStatus()
      .then(({ data }) => {
        const agentsList = Object.values(data);
        setAgents(agentsList);
        localStorage.setItem('agents_cache', JSON.stringify(agentsList));
      })
      .catch(err => {
        console.error('Failed to fetch:', err);
      });
  }, [token]);

  const filteredAgents = agents
    .filter(agent => !existingTabs.includes(agent.pane_id))
    .filter(agent => {
      if (!search) return true;
      const s = search.toLowerCase();
      return (agent.title?.toLowerCase().includes(s)) || 
             (agent.pane_id?.toLowerCase().includes(s));
    });

  const handleAgentClick = (agent: Agent) => {
    const url = urls.ttyd(agent.pane_id, token);
    onAddAgent(agent.pane_id,agent.title, url);
  };

  return (
    <div className="h-full bg-vsc-bg overflow-auto flex flex-col">
      {loading && agents.length === 0 ? (
        <div className="flex items-center justify-center h-full">
          <div className="text-vsc-text-secondary">Loading agents...</div>
        </div>
      ) : filteredAgents.length === 0 ? (
        <div className="flex items-center justify-center h-full">
          <div className="text-center">
            <div className="text-vsc-text-secondary mb-2">No available agents</div>
            <div className="text-xs text-vsc-text-muted">All agents are already open</div>
          </div>
        </div>
      ) : (
        <div className="flex flex-col h-full">
          <div className="flex-shrink-0 p-4 border-b border-vsc-border flex items-center gap-2">
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search agents..."
              className="flex-1 px-3 py-2 bg-vsc-bg-secondary border border-vsc-border rounded text-sm text-vsc-text placeholder-vsc-text-muted focus:outline-none focus:border-vsc-button"
              autoFocus
            />
            {onNewAgent && (
              <button
                onClick={onNewAgent}
                className="px-3 py-2 bg-green-600 hover:bg-green-700 text-white rounded text-sm flex items-center gap-1 whitespace-nowrap"
              >
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <line x1="12" y1="5" x2="12" y2="19"/>
                  <line x1="5" y1="12" x2="19" y2="12"/>
                </svg>
                New Agent
              </button>
            )}
          </div>
          <div className="flex-1 overflow-auto p-4">
            <div className="grid grid-cols-[repeat(auto-fill,300px)] gap-2 justify-start">
              {filteredAgents.map((agent, idx) => (
                <div 
                  key={agent.pane_id || idx}
                  onClick={() => {
                    const url = urls.idePane(agent.pane_id, token);
                    window.open(url, '_blank');
                  }}
                  className="group relative flex items-center gap-3 bg-vsc-bg-secondary border border-vsc-border rounded-lg p-3 cursor-pointer hover:border-vsc-button hover:bg-vsc-bg-hover transition-all h-[80px]"
                >
                  <button
                    onClick={async (e) => {
                      e.stopPropagation();
                      if (confirm(`Remove ${agent.pane_id}?`)) {
                        try {
                          // First unbind if agent has an ID
                          if (agent.id) {
                            await apiService.unbindAgent(agent.id);
                          }
                          await apiService.deletePane(agent.pane_id);
                          setAgents(agents.filter(a => a.pane_id !== agent.pane_id));
                        } catch (err) {
                          console.error('Failed to remove:', err);
                          alert('Error removing agent');
                        }
                      }
                    }}
                    className="absolute -top-2 -right-2 w-5 h-5 flex items-center justify-center bg-red-500 hover:bg-red-600 text-white rounded-full opacity-0 group-hover:opacity-100 transition-opacity"
                  >
                    ×
                  </button>
                  <div className={`w-2 h-2 rounded-full flex-shrink-0 ${agent.status === 'idle' ? 'bg-green-500' : agent.status === 'thinking' ? 'bg-yellow-500' : 'bg-gray-500'}`}></div>
                  <div className="flex-1 min-w-0">
                    <div className="text-sm font-medium text-vsc-text truncate">
                      {agent.title || agent.pane_id || 'unknown'}
                    </div>
                    <div className="text-xs text-vsc-text-muted truncate">{agent.pane_id}</div>
                  </div>
                  <div className="text-xs text-vsc-text-secondary flex-shrink-0">
                    {agent.status || 'unknown'}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
