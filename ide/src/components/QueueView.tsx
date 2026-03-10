import React, { useState, useEffect, useCallback } from 'react';
import { Loader2, Trash2, Plus } from 'lucide-react';
import api from '../services/api';

interface QueueItem {
  id: number;
  pane_id: string;
  message: string;
  status: string;
  priority: number;
  created_at?: string;
  sent_at?: string;
}

interface QueueViewProps {
  paneId: string;
  agents?: string[];
}

export const QueueView: React.FC<QueueViewProps> = ({ paneId, agents = [] }) => {
  const [queues, setQueues] = useState<Record<string, QueueItem[]>>({});
  const [loading, setLoading] = useState(true);
  const [newMsg, setNewMsg] = useState('');
  const [targetPane, setTargetPane] = useState('');
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editMsg, setEditMsg] = useState('');

  const targets = agents.length > 0 ? agents : [paneId];

  const fetchAll = useCallback(async () => {
    const result: Record<string, QueueItem[]> = {};
    for (const t of targets) {
      try {
        const { data } = await api.getQueue(t);
        result[t] = (data.queue || []).filter((q: QueueItem) => q.status === 'pending');
      } catch { result[t] = []; }
    }
    setQueues(result);
    setLoading(false);
  }, [targets.join(',')]);

  useEffect(() => { fetchAll(); const iv = setInterval(fetchAll, 5000); return () => clearInterval(iv); }, [fetchAll]);

  const handlePush = async () => {
    const target = targetPane || targets[0];
    if (!newMsg.trim()) return;
    await api.pushQueue({ pane_id: target, message: newMsg.trim() });
    setNewMsg('');
    fetchAll();
  };

  const handleDelete = async (id: number) => {
    await api.deleteQueueItem(id);
    fetchAll();
  };

  const handleSaveEdit = async (id: number) => {
    await api.updateQueueItem(id, { message: editMsg });
    setEditingId(null);
    fetchAll();
  };

  if (loading) return <div className="flex items-center justify-center h-32"><Loader2 className="animate-spin text-vsc-text-secondary" size={18} /></div>;

  const allItems = targets.flatMap(t => (queues[t] || []).map(q => ({ ...q, _target: t })));

  return (
    <div className="flex flex-col h-full bg-vsc-bg p-3 text-sm">
      <div className="flex gap-2 mb-3">
        {targets.length > 1 && (
          <select value={targetPane} onChange={e => setTargetPane(e.target.value)}
            className="bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-xs rounded px-2 py-1 focus:outline-none focus:border-vsc-accent">
            {targets.map(t => <option key={t} value={t}>{t.replace(':main.0', '')}</option>)}
          </select>
        )}
        <input value={newMsg} onChange={e => setNewMsg(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && handlePush()}
          placeholder="Enter task message..."
          className="flex-1 bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-xs rounded px-2 py-1.5 focus:outline-none focus:border-vsc-accent" />
        <button onClick={handlePush} disabled={!newMsg.trim()}
          className="bg-vsc-button hover:bg-vsc-button-hover disabled:opacity-50 text-white px-2 py-1 rounded flex items-center gap-1 text-xs">
          <Plus size={12} /> Add
        </button>
      </div>

      {allItems.length === 0 ? (
        <p className="text-vsc-text-muted text-xs text-center py-4">Queue empty</p>
      ) : (
        <div className="flex-1 overflow-auto space-y-1">
          {allItems.map(item => (
            <div key={item.id} className="flex items-start gap-2 bg-vsc-bg-secondary border border-vsc-border rounded px-2 py-1.5 group">
              {targets.length > 1 && <span className="text-vsc-text-muted text-xs shrink-0">{item._target.replace(':main.0', '')}</span>}
              {editingId === item.id ? (
                <input value={editMsg} onChange={e => setEditMsg(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter') handleSaveEdit(item.id); if (e.key === 'Escape') setEditingId(null); }}
                  onBlur={() => handleSaveEdit(item.id)} autoFocus
                  className="flex-1 bg-vsc-bg border border-vsc-accent text-vsc-text text-xs rounded px-1 py-0.5 focus:outline-none" />
              ) : (
                <span className="flex-1 text-vsc-text text-xs cursor-pointer truncate"
                  onClick={() => { setEditingId(item.id); setEditMsg(item.message); }}
                  title={item.message}>
                  {item.priority > 0 && <span className="text-yellow-400 mr-1">★{item.priority}</span>}
                  {item.message}
                </span>
              )}
              <button onClick={() => handleDelete(item.id)}
                className="text-vsc-text-muted hover:text-red-400 opacity-0 group-hover:opacity-100 shrink-0">
                <Trash2 size={12} />
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};
