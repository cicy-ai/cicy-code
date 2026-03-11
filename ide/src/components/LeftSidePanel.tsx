import React, { useState } from 'react';
import ReactDOM from 'react-dom';
import { useApp } from '../contexts/AppContext';
import { useDialog } from '../contexts/DialogContext';

const statusConfig: Record<string, { color: string; label: string; sort: number }> = {
  thinking:     { color: 'bg-yellow-500 animate-pulse', label: 'thinking', sort: 0 },
  compacting:   { color: 'bg-purple-500 animate-pulse', label: 'compact', sort: 1 },
  wait_auth:    { color: 'bg-red-500', label: 'auth', sort: 2 },
  wait_startup: { color: 'bg-blue-500 animate-pulse', label: 'starting', sort: 3 },
  idle:         { color: 'bg-green-500', label: 'idle', sort: 4 },
};

const LeftSidePanel: React.FC = () => {
  const { allPanes, currentPaneId, selectPane } = useApp();
  const { openDialog, closeDialog, activeDialog } = useDialog();
  const [searchQuery, setSearchQuery] = useState('');
  const [newTitle, setNewTitle] = useState('');
  const [pinnedPanes, setPinnedPanes] = useState<string[]>(() => {
    const saved = localStorage.getItem('pinnedPanes');
    return saved ? JSON.parse(saved) : [];
  });
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; paneId: string } | null>(null);

  React.useEffect(() => {
    const handler = () => {
      const saved = localStorage.getItem('pinnedPanes');
      setPinnedPanes(saved ? JSON.parse(saved) : []);
    };
    window.addEventListener('pinnedPanesChanged', handler);
    return () => window.removeEventListener('pinnedPanesChanged', handler);
  }, []);

  // Close context menu on click anywhere
  React.useEffect(() => {
    if (!ctxMenu) return;
    const close = () => setCtxMenu(null);
    window.addEventListener('click', close);
    return () => window.removeEventListener('click', close);
  }, [ctxMenu]);

  const getStatusInfo = (pane: any) => {
    if (pane.isThinking) return statusConfig.thinking;
    if (pane.isWaitingAuth) return statusConfig.wait_auth;
    if (pane.isWaitStartup) return statusConfig.wait_startup;
    if (pane.isCompacting) return statusConfig.compacting;
    if (pane.status) return statusConfig[pane.status] || { color: 'bg-gray-500', label: pane.status, sort: 5 };
    return { color: 'bg-gray-600', label: '', sort: 6 };
  };

  const formatTimeAgo = (ts: number | null) => {
    if (!ts) return '';
    if (ts < 60) return `${ts}s`;
    if (ts < 3600) return `${Math.floor(ts / 60)}m`;
    if (ts < 86400) return `${Math.floor(ts / 3600)}h`;
    return `${Math.floor(ts / 86400)}d`;
  };

  const togglePin = (paneId: string) => {
    const next = pinnedPanes.includes(paneId) ? pinnedPanes.filter(p => p !== paneId) : [...pinnedPanes, paneId];
    setPinnedPanes(next);
    localStorage.setItem('pinnedPanes', JSON.stringify(next));
    window.dispatchEvent(new Event('pinnedPanesChanged'));
  };

  const handleCreate = async () => {
    if (!newTitle.trim()) return;
    closeDialog();
    setNewTitle('');
  };

  const [roleFilter, setRoleFilter] = useState<'all' | 'master' | 'worker'>('master');

  // Counts for status summary
  const counts = allPanes.reduce((acc: Record<string, number>, p: any) => {
    const s = getStatusInfo(p).label || 'offline';
    acc[s] = (acc[s] || 0) + 1;
    return acc;
  }, {});

  const filtered = allPanes.filter((p: any) => {
    const matchSearch = p.title?.toLowerCase().includes(searchQuery.toLowerCase()) ||
      p.pane_id?.toLowerCase().includes(searchQuery.toLowerCase());
    if (!matchSearch) return false;
    if (roleFilter === 'master') return p.role === 'master';
    if (roleFilter === 'worker') return p.role === 'worker';
    return true;
  }).sort((a: any, b: any) => {
    // Pinned first
    const ap = pinnedPanes.includes(a.pane_id) ? 0 : 1;
    const bp = pinnedPanes.includes(b.pane_id) ? 0 : 1;
    if (ap !== bp) return ap - bp;
    // Active status first (thinking > compacting > auth > starting > idle > offline)
    const sa = getStatusInfo(a).sort ?? 6;
    const sb = getStatusInfo(b).sort ?? 6;
    if (sa !== sb) return sa - sb;
    return (b.created_at || '').localeCompare(a.created_at || '');
  });

  const roleIcon = (role: string) => role === 'master' ? '📋' : role === 'worker' ? '🔧' : '';

  return (
    <div className="h-full flex flex-col bg-vsc-bg-secondary">
      {/* Header */}
      <div className="h-10 flex items-center justify-between px-3 border-b border-vsc-border flex-shrink-0">
        <div className="flex items-center gap-2">
          <span className="text-[11px] font-medium text-vsc-text-secondary tracking-wide">Agents</span>
          <span className="text-[10px]">
            {counts.thinking ? <span className="text-yellow-400/80">{counts.thinking}⚡</span> : null}
            {counts.thinking && counts.idle ? ' ' : null}
            {counts.idle ? <span className="text-green-400/70">{counts.idle}✓</span> : null}
          </span>
        </div>
        <button onClick={() => openDialog('createAgent')} className="cicy-btn" title="New Master">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        </button>
      </div>

      {/* Search */}
      <div className="px-2 py-1.5 flex-shrink-0 relative">
        <input type="text" value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} placeholder="Search..." className="w-full bg-vsc-bg border border-vsc-border text-vsc-text text-xs rounded px-2.5 py-1.5 pr-6 focus:outline-none focus:border-vsc-accent transition-colors" />
        {searchQuery && <button onClick={() => setSearchQuery('')} className="absolute right-4 top-1/2 -translate-y-1/2 text-vsc-text-secondary hover:text-vsc-text text-xs">✕</button>}
      </div>

      {/* Role filter */}
      <div className="flex px-2 pb-1.5 gap-1 flex-shrink-0">
        {(['master', 'worker', 'all'] as const).map(f => (
          <button key={f} onClick={() => setRoleFilter(f)}
            className={`px-2 py-0.5 text-[10px] rounded transition-all duration-150 ${roleFilter === f ? 'bg-vsc-accent/20 text-vsc-accent border border-vsc-accent/30' : 'text-vsc-text-muted hover:text-vsc-text-secondary hover:bg-vsc-bg-hover border border-transparent'}`}>
            {f === 'all' ? 'All' : f === 'master' ? '📋 Master' : '🔧 Worker'}
          </button>
        ))}
      </div>

      {/* Agent list */}
      <div className="flex-1 overflow-auto">
        {filtered.length === 0 && (
          <div className="px-3 py-6 text-center text-xs text-vsc-text-secondary">
            {searchQuery ? 'No matches' : 'No agents'}
          </div>
        )}
        {filtered.map((pane: any) => {
          const isActive = currentPaneId === pane.pane_id;
          const si = getStatusInfo(pane);
          const title = pane.title || pane.pane_id;
          const shortId = pane.pane_id?.replace(':main.0', '');
          const isThinking = si.label === 'thinking';
          const isPinned = pinnedPanes.includes(pane.pane_id);
          const ctxPct = pane.contextUsage;

          return (
            <div key={pane.pane_id}
              onClick={() => selectPane(pane.pane_id)}
              onContextMenu={(e) => { e.preventDefault(); setCtxMenu({ x: e.clientX, y: e.clientY, paneId: pane.pane_id }); }}
              className={`group flex items-center gap-2.5 px-3 py-2 cursor-pointer border-l-2 transition-all duration-150 ${isThinking ? 'bg-yellow-500/10 border-l-yellow-500' : isActive ? 'bg-blue-500/10 border-l-blue-500' : isPinned ? 'bg-vsc-bg-hover/50 border-l-vsc-text-secondary' : 'border-l-transparent hover:bg-vsc-bg-hover/60'}`}>
              <div className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ring-2 ring-opacity-30 ${si.color} ${isThinking ? 'ring-yellow-500 animate-pulse' : isActive ? 'ring-blue-500' : 'ring-transparent'}`} title={si.label} />
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-1">
                  {isPinned && <span className="text-[9px] leading-none opacity-60">📌</span>}
                  <span className={`text-[12px] truncate block ${isActive ? 'text-vsc-text font-medium' : 'text-vsc-text-secondary'}`}>{title}</span>
                  <span className="text-[10px] text-vsc-text-muted ml-auto flex-shrink-0">{pane.timeAgo != null ? formatTimeAgo(pane.timeAgo) : ''}</span>
                </div>
                <div className="flex items-center gap-1.5 text-[10px] text-vsc-text-muted mt-0.5">
                  <span className="opacity-70">{shortId}</span>
                  {si.label && si.label !== 'idle' && (
                    <span className={`cicy-badge ${isThinking ? 'bg-yellow-500/15 text-yellow-400' : si.label === 'auth' ? 'bg-red-500/15 text-red-400' : 'bg-vsc-bg-active text-vsc-text-secondary'}`}>{si.label}</span>
                  )}
                  {ctxPct != null && (
                    <div className="flex items-center gap-1 ml-auto">
                      <div className="w-10 h-1 bg-vsc-bg rounded-full overflow-hidden">
                        <div className={`h-full rounded-full transition-all duration-300 ${ctxPct > 80 ? 'bg-red-500' : ctxPct > 50 ? 'bg-yellow-500' : 'bg-green-500/70'}`} style={{ width: `${Math.min(ctxPct, 100)}%` }} />
                      </div>
                      <span className="text-[9px] opacity-60">{ctxPct}%</span>
                    </div>
                  )}
                </div>
              </div>
            </div>
          );
        })}
      </div>

      {/* Context menu */}
      {ctxMenu && (
        <div className="fixed z-[9999] bg-vsc-bg border border-vsc-border rounded shadow-lg py-1 min-w-[140px]" style={{ left: ctxMenu.x, top: ctxMenu.y }}>
          <button onClick={() => { togglePin(ctxMenu.paneId); setCtxMenu(null); }}
            className="w-full text-left px-3 py-1 text-xs text-vsc-text hover:bg-vsc-bg-hover">
            {pinnedPanes.includes(ctxMenu.paneId) ? '📌 Unpin' : '📌 Pin'}
          </button>
          <button onClick={() => { navigator.clipboard.writeText(ctxMenu.paneId.replace(':main.0', '')); setCtxMenu(null); }}
            className="w-full text-left px-3 py-1 text-xs text-vsc-text hover:bg-vsc-bg-hover">
            📋 Copy ID
          </button>
        </div>
      )}

      {/* Create dialog */}
      {activeDialog === 'createAgent' && ReactDOM.createPortal(
        <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-[9999999]" onClick={closeDialog}>
          <div className="bg-vsc-bg border border-vsc-border rounded-lg p-4 max-w-sm w-full shadow-xl" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-vsc-text text-sm font-semibold mb-3">New Master</h3>
            <input type="text" value={newTitle} onChange={(e) => setNewTitle(e.target.value)} placeholder="Agent title..." className="w-full bg-vsc-bg-secondary border border-vsc-border text-vsc-text text-sm rounded px-3 py-2 mb-3 focus:outline-none focus:border-vsc-accent" autoFocus onKeyDown={(e) => e.key === 'Enter' && handleCreate()} />
            <div className="flex justify-end gap-2">
              <button onClick={() => { closeDialog(); setNewTitle(''); }} className="px-3 py-1.5 text-xs bg-vsc-bg-secondary hover:bg-vsc-bg-active text-vsc-text rounded">Cancel</button>
              <button onClick={handleCreate} disabled={!newTitle.trim()} className="px-3 py-1.5 text-xs bg-vsc-button hover:bg-vsc-button-hover disabled:opacity-40 text-white rounded">Create</button>
            </div>
          </div>
        </div>,
        document.body
      )}
    </div>
  );
};

export default LeftSidePanel;
