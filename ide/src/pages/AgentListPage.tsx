import React, { useEffect, useState, useRef } from 'react';
import { LogOut, Plus, Search, Edit2, Trash2, X, Zap, Brain, Activity } from 'lucide-react';
import apiService from '../services/api';
import { useAuth } from '../contexts/AuthContext';

interface Agent {
  pane_id: string;
  title?: string;
  status?: string;
  active?: boolean;
  contextUsage?: number;
}

type TagFilter = 'All' | 'Master' | 'Workers';

const AgentListPage: React.FC = () => {
  const { logout } = useAuth();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [tag, setTag] = useState<TagFilter>('All');
  const [showModal, setShowModal] = useState(false);
  const [editAgent, setEditAgent] = useState<Agent | null>(null);
  const [formTitle, setFormTitle] = useState('');
  const searchRef = useRef<HTMLInputElement>(null);

  const fetchAgents = async () => {
    try {
      const [panesRes, statusRes] = await Promise.all([apiService.getPanes(), apiService.getAllStatus()]);
      const panes: Agent[] = panesRes.data?.panes || [];
      const statusMap = (statusRes.data || {}) as Record<string, any>;
      setAgents(panes.map(p => {
        const shortId = p.pane_id.replace(':main.0', '');
        return { ...p, ...statusMap[p.pane_id], pane_id: shortId, title: p.title || statusMap[p.pane_id]?.title };
      }));
    } catch {}
    setLoading(false);
  };

  useEffect(() => {
    fetchAgents();
    const id = setInterval(fetchAgents, 5000);
    return () => clearInterval(id);
  }, []);

  // Keyboard shortcut: / to focus search
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === '/' && document.activeElement?.tagName !== 'INPUT') { e.preventDefault(); searchRef.current?.focus(); } };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, []);

  const filtered = agents.filter(a => {
    const q = search.toLowerCase();
    const matchSearch = !q || a.pane_id.toLowerCase().includes(q) || a.title?.toLowerCase().includes(q);
    const matchTag = tag === 'All' || (tag === 'Master' && a.pane_id.startsWith('w-1')) || (tag === 'Workers' && !a.pane_id.startsWith('w-1'));
    return matchSearch && matchTag;
  });

  const stats = {
    total: agents.length,
    thinking: agents.filter(a => a.status === 'thinking').length,
    idle: agents.filter(a => a.status === 'idle').length,
  };

  const openCreate = () => { setEditAgent(null); setFormTitle(''); setShowModal(true); };
  const openEdit = (a: Agent) => { setEditAgent(a); setFormTitle(a.title || ''); setShowModal(true); };

  const handleSave = async () => {
    if (!formTitle.trim()) return;
    try {
      if (editAgent) await apiService.updatePane(editAgent.pane_id, { title: formTitle });
      else await apiService.createPane({ title: formTitle });
      setShowModal(false);
      fetchAgents();
    } catch (e: any) { alert(e.response?.data?.error || 'Failed'); }
  };

  const handleDelete = async (pane_id: string) => {
    if (!confirm(`Delete ${pane_id}?`)) return;
    try { await apiService.deletePane(pane_id); fetchAgents(); } catch (e: any) { alert(e.response?.data?.error || 'Failed'); }
  };

  return (
    <div className="bg-vsc-bg min-h-screen relative overflow-hidden">
      {/* Ambient glow */}
      <div className="fixed top-0 left-1/2 -translate-x-1/2 w-[600px] h-[300px] bg-vsc-accent/5 rounded-full blur-[120px] pointer-events-none" />

      <div className="max-w-5xl mx-auto px-6 py-8 relative z-10">
        {/* Header */}
        <div className="flex items-center justify-between mb-10">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-vsc-accent to-blue-600 flex items-center justify-center">
              <Brain size={20} className="text-white" />
            </div>
            <div>
              <h1 className="text-xl font-semibold text-white tracking-tight">CiCy IDE</h1>
              <p className="text-vsc-text-muted text-xs">AI Agent Control Center</p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button onClick={openCreate} className="h-8 px-3 bg-vsc-accent hover:bg-vsc-accent-hover text-white text-xs rounded-lg flex items-center gap-1.5 transition-all hover:shadow-lg hover:shadow-vsc-accent/20">
              <Plus size={14} /> New Agent
            </button>
            <button onClick={logout} className="h-8 w-8 rounded-lg text-vsc-text-muted hover:text-white hover:bg-white/5 flex items-center justify-center transition-colors" title="Logout">
              <LogOut size={15} />
            </button>
          </div>
        </div>

        {/* Live Stats Bar */}
        <div className="flex gap-3 mb-8">
          {[
            { label: 'Total', value: stats.total, icon: <Activity size={14} />, color: 'text-vsc-text' },
            { label: 'Thinking', value: stats.thinking, icon: <Zap size={14} />, color: 'text-yellow-400', pulse: stats.thinking > 0 },
            { label: 'Idle', value: stats.idle, icon: <div className="w-2 h-2 rounded-full bg-emerald-400" />, color: 'text-emerald-400' },
          ].map(s => (
            <div key={s.label} className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-white/[0.03] border border-white/[0.06]">
              <span className={`${s.color} ${s.pulse ? 'animate-pulse' : ''}`}>{s.icon}</span>
              <span className={`text-sm font-mono font-medium ${s.color}`}>{s.value}</span>
              <span className="text-[10px] text-vsc-text-muted uppercase tracking-wider">{s.label}</span>
            </div>
          ))}
        </div>

        {/* Search + Tags */}
        <div className="flex gap-3 mb-6 items-center">
          <div className="relative flex-1">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-vsc-text-muted" />
            <input
              ref={searchRef}
              type="text"
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="Search agents...  /"
              className="w-full pl-9 pr-3 h-9 bg-white/[0.04] border border-white/[0.08] rounded-lg text-sm text-white placeholder-vsc-text-muted focus:outline-none focus:border-vsc-accent/50 focus:bg-white/[0.06] transition-all"
            />
          </div>
          <div className="flex bg-white/[0.04] rounded-lg border border-white/[0.06] p-0.5">
            {(['All', 'Master', 'Workers'] as TagFilter[]).map(t => (
              <button
                key={t}
                onClick={() => setTag(t)}
                className={`px-3 py-1.5 text-xs rounded-md transition-all ${tag === t ? 'bg-vsc-accent text-white shadow-sm' : 'text-vsc-text-muted hover:text-white'}`}
              >
                {t}
              </button>
            ))}
          </div>
        </div>

        {/* Agent List */}
        {loading ? (
          <div className="flex flex-col items-center justify-center py-20 gap-3">
            <div className="w-8 h-8 border-2 border-vsc-accent/30 border-t-vsc-accent rounded-full animate-spin" />
            <span className="text-xs text-vsc-text-muted">Connecting to agents...</span>
          </div>
        ) : filtered.length === 0 ? (
          <div className="text-center py-20">
            <div className="text-3xl mb-3 opacity-30">🤖</div>
            <p className="text-vsc-text-muted text-sm">{search ? 'No matching agents' : 'No agents yet'}</p>
          </div>
        ) : (
          <div className="space-y-2">
            {filtered.map(a => {
              const isThinking = a.status === 'thinking';
              const isIdle = a.status === 'idle';
              return (
                <div
                  key={a.pane_id}
                  className={`group relative flex items-center gap-4 px-4 py-3 rounded-xl border transition-all cursor-pointer hover:bg-white/[0.03] ${isThinking ? 'border-yellow-500/20 bg-yellow-500/[0.03]' : 'border-white/[0.06] bg-white/[0.02]'}`}
                  onClick={() => { window.location.hash = `#/agent/${encodeURIComponent(a.pane_id)}`; }}
                >
                  {/* Status indicator */}
                  <div className="relative flex-shrink-0">
                    <div className={`w-9 h-9 rounded-lg flex items-center justify-center text-sm font-mono font-bold ${isThinking ? 'bg-yellow-500/15 text-yellow-400' : isIdle ? 'bg-emerald-500/15 text-emerald-400' : 'bg-white/5 text-vsc-text-muted'}`}>
                      {(a.title || a.pane_id).charAt(0).toUpperCase()}
                    </div>
                    {isThinking && <div className="absolute -top-0.5 -right-0.5 w-2.5 h-2.5 bg-yellow-400 rounded-full animate-pulse" />}
                    {isIdle && <div className="absolute -top-0.5 -right-0.5 w-2.5 h-2.5 bg-emerald-400 rounded-full" />}
                  </div>

                  {/* Info */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-white truncate">{a.title || a.pane_id}</span>
                      {isThinking && <span className="text-[10px] px-1.5 py-0.5 rounded bg-yellow-500/15 text-yellow-400 font-medium animate-pulse">thinking</span>}
                    </div>
                    <span className="text-xs text-vsc-text-muted font-mono">{a.pane_id}</span>
                  </div>

                  {/* Actions */}
                  <div className="flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity" onClick={e => e.stopPropagation()}>
                    <button onClick={() => openEdit(a)} className="p-1.5 rounded-md hover:bg-white/10 text-vsc-text-muted hover:text-white transition-colors" title="Edit">
                      <Edit2 size={13} />
                    </button>
                    <button onClick={() => handleDelete(a.pane_id)} className="p-1.5 rounded-md hover:bg-red-500/15 text-vsc-text-muted hover:text-red-400 transition-colors" title="Delete">
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50" onClick={() => setShowModal(false)}>
          <div className="bg-vsc-bg-secondary border border-white/10 rounded-2xl p-6 w-full max-w-sm shadow-2xl" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between mb-5">
              <h2 className="text-base font-semibold text-white">{editAgent ? 'Edit Agent' : 'New Agent'}</h2>
              <button onClick={() => setShowModal(false)} className="text-vsc-text-muted hover:text-white transition-colors"><X size={18} /></button>
            </div>
            {editAgent && <div className="text-xs text-vsc-text-muted font-mono mb-3 px-3 py-2 bg-white/[0.04] rounded-lg">{editAgent.pane_id}</div>}
            <input
              autoFocus
              type="text"
              value={formTitle}
              onChange={e => setFormTitle(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleSave()}
              placeholder="Agent name..."
              className="w-full px-3 py-2.5 bg-white/[0.04] border border-white/[0.08] rounded-xl text-sm text-white placeholder-vsc-text-muted focus:outline-none focus:border-vsc-accent/50 transition-all"
            />
            <div className="flex justify-end gap-2 mt-5">
              <button onClick={() => setShowModal(false)} className="px-4 py-2 text-xs text-vsc-text-muted hover:text-white transition-colors rounded-lg">Cancel</button>
              <button onClick={handleSave} className="px-4 py-2 bg-vsc-accent hover:bg-vsc-accent-hover text-white text-xs rounded-lg transition-all hover:shadow-lg hover:shadow-vsc-accent/20">Save</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default AgentListPage;
