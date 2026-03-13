import React, { useEffect, useState, useRef } from 'react';
import { useAuth } from '../contexts/AuthContext';
import apiService from '../services/api';
import TerminalFrame from '../components/terminal/TerminalFrame';
import { CommandPanel, CommandPanelHandle } from '../components/terminal/CommandPanel';
import ChatView from '../components/chat/ChatView';
import { SettingsView } from '../components/SettingsView';
import { EditPaneData } from '../components/EditPaneDialog';
import { ArrowLeft, RotateCcw, Zap, Trash2, Settings, MessageSquare, Terminal, X } from 'lucide-react';
import { useDesktopApps, openInElectron } from '../components/desktop/useDesktopApps';

/* ── Settings floating window (iPhone style) ── */
const SettingsFloat: React.FC<{ paneId: string; fullPaneId: string; onClose: () => void }> = ({ paneId, fullPaneId, onClose }) => {
  const [paneData, setPaneData] = useState<EditPaneData>({ target: fullPaneId, title: paneId });
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');
  useEffect(() => { apiService.getPane(fullPaneId).then(({ data }) => setPaneData(prev => ({ ...prev, ...data }))).catch(() => {}); }, [fullPaneId]);
  const save = async () => { setSaving(true); try { await apiService.updatePane(paneId, paneData); setMsg('Saved'); } catch { setMsg('Failed'); } finally { setSaving(false); setTimeout(() => setMsg(''), 1500); } };
  return (
    <div className="fixed inset-0 z-[99999] flex items-start justify-center pt-12" onClick={onClose}>
      <div className="w-[340px] max-h-[70vh] bg-[#1c1c1e]/95 backdrop-blur-2xl rounded-2xl shadow-2xl border border-white/[0.08] overflow-hidden" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/[0.06]">
          <span className="text-[13px] font-semibold text-white">Settings</span>
          <button onClick={onClose} className="p-1 rounded-full hover:bg-white/10"><X size={14} className="text-white/60" /></button>
        </div>
        <div className="p-3 overflow-y-auto max-h-[calc(70vh-48px)]">
          <SettingsView pane={paneData} onChange={setPaneData} onSave={save} isSaving={saving} />
          {msg && <div className="mt-2 text-center text-xs text-emerald-400">{msg}</div>}
        </div>
      </div>
    </div>
  );
};

/* ── Right drawer (History / Terminal) ── */
const Drawer: React.FC<{ tab: string; onTabChange: (t: string) => void; children: React.ReactNode[] }> = ({ tab, onTabChange, children }) => (
  <div className="h-full flex flex-col bg-[#1c1c1e]/90 backdrop-blur-xl border-l border-white/[0.06]">
    <div className="flex items-center px-2 h-9 shrink-0 border-b border-white/[0.04]">
      <div className="flex gap-0.5">
        {['history', 'terminal'].map(t => (
          <button key={t} onClick={() => onTabChange(t)} className={`px-2.5 py-1 text-[11px] rounded-md transition-all ${tab === t ? 'text-white bg-white/[0.08]' : 'text-white/40 hover:text-white/70'}`}>
            {t === 'history' ? <span className="flex items-center gap-1"><MessageSquare size={11} />History</span> : <span className="flex items-center gap-1"><Terminal size={11} />Terminal</span>}
          </button>
        ))}
      </div>
    </div>
    <div className="flex-1 overflow-hidden relative">
      <div className="absolute inset-0" style={{ display: tab === 'history' ? 'block' : 'none' }}>{children[0]}</div>
      <div className="absolute inset-0" style={{ display: tab === 'terminal' ? 'block' : 'none' }}>{children[1]}</div>
    </div>
  </div>
);

/* ── Resizer ── */
const Resizer: React.FC<{ width: number; onChange: (w: number) => void; onDragging: (d: boolean) => void }> = ({ width, onChange, onDragging }) => {
  const onDown = (e: React.MouseEvent) => {
    e.preventDefault();
    onDragging(true);
    const startX = e.clientX, startW = width;
    const onMove = (ev: MouseEvent) => onChange(Math.max(280, Math.min(window.innerWidth * 0.6, startW - (ev.clientX - startX))));
    const onUp = () => { onDragging(false); document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
    document.addEventListener('mousemove', onMove); document.addEventListener('mouseup', onUp);
  };
  return <div className="w-1 cursor-col-resize hover:bg-white/10 active:bg-white/20 transition-colors shrink-0" onMouseDown={onDown} />;
};

/* ── Main ── */
const AgentPage: React.FC<{ paneId: string }> = ({ paneId }) => {
  const { token, hasPermission } = useAuth();
  const fullPaneId = paneId.includes(':') ? paneId : `${paneId}:main.0`;
  const [title, setTitle] = useState(paneId);
  const [status, setStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode, setMouseMode] = useState<'on' | 'off'>('off');
  const [isRestarting, setIsRestarting] = useState(false);
  const [toast, setToast] = useState<string | null>(null);
  const commandPanelRef = useRef<CommandPanelHandle>(null);
  const [panelPos, setPanelPos] = useState(() => { try { const c = JSON.parse(localStorage.getItem('agent_panelPos')!); return c && c.x != null ? c : { x: 20, y: Math.max(60, window.innerHeight - 280) }; } catch { return { x: 20, y: Math.max(60, window.innerHeight - 280) }; } });
  const [panelSize, setPanelSize] = useState(() => { try { const c = JSON.parse(localStorage.getItem('agent_panelSize')!); return c && c.width ? c : { width: 360, height: 220 }; } catch { return { width: 360, height: 220 }; } });
  const [isDragging, setIsDragging] = useState(false);
  const [ctxMenu, setCtxMenu] = useState<{ x: number; y: number; appId: string } | null>(null);
  const { apps, addApp, removeApp } = useDesktopApps(paneId);
  const [editMode, setEditMode] = useState(false);

  // Drawer
  const [drawerTab, setDrawerTab] = useState(() => localStorage.getItem('agent_drawerTab') || 'history');
  const [drawerW, setDrawerW] = useState(() => parseInt(localStorage.getItem('agent_drawerW') || '360'));
  const [settingsOpen, setSettingsOpen] = useState(false);

  useEffect(() => { localStorage.setItem('agent_drawerW', drawerW.toString()); }, [drawerW]);
  useEffect(() => { localStorage.setItem('agent_drawerTab', drawerTab); }, [drawerTab]);
  useEffect(() => { localStorage.setItem('agent_panelPos', JSON.stringify(panelPos)); }, [panelPos]);
  useEffect(() => { localStorage.setItem('agent_panelSize', JSON.stringify(panelSize)); }, [panelSize]);

  // Fetch title
  useEffect(() => { apiService.getPane(fullPaneId).then(({ data }) => { if (data?.title) setTitle(data.title); }).catch(() => {}); }, [fullPaneId]);

  // Poll status
  useEffect(() => {
    const poll = async () => { try { const { data } = await apiService.getAllStatus(); const st = data?.[fullPaneId]; if (st?.status) setStatus(st.status); if (st?.title) setTitle(st.title); if (st?.contextUsage != null) setContextUsage(st.contextUsage); } catch {} };
    poll(); const id = setInterval(poll, 5000); return () => clearInterval(id);
  }, [fullPaneId]);

  // Handlers
  const handleToggleMouse = async () => { const n = mouseMode === 'on' ? 'off' : 'on'; try { await apiService.toggleMouse(n, fullPaneId); setMouseMode(n); } catch {} };
  const handleRestart = async () => {
    if (!confirm(`Restart ${paneId}?`)) return; setIsRestarting(true);
    try { await apiService.restartPane(paneId); for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); try { const { data } = await apiService.getTtydStatus(paneId); if (data.status === 'running') { setTimeout(() => location.reload(), 500); return; } } catch {} } setTimeout(() => location.reload(), 500); }
    catch { alert('Restart failed'); } finally { setIsRestarting(false); }
  };
  const handleCapture = async () => { try { const { data } = await apiService.capturePane(paneId, 100); if (data.output) { await navigator.clipboard.writeText(data.output); showToast('Captured'); } } catch {} };
  const showToast = (m: string) => { setToast(m); setTimeout(() => setToast(null), 2000); };

  // Agent desktop events
  useEffect(() => {
    const handler = async (e: CustomEvent) => {
      const d = e.detail || {};
      if (d.type === 'add_app') { addApp({ id: d.id || `app-${Date.now()}`, label: d.label || 'App', emoji: d.emoji || '📦', url: d.url || 'about:blank' }); if (d.autoOpen !== false) openInElectron(d.url, d.label); }
      else if (d.type === 'open_window' && d.url) openInElectron(d.url, d.title);
      else if (d.type === 'gemini_vision_request') {
        // Call electronRPC
        try {
          const result = await (window as any).electronRPC('gemini_vision', { image: d.image, prompt: d.prompt || 'Describe this image' });
          // Send result back via WS
          window.dispatchEvent(new CustomEvent('gemini-vision-result', { detail: { requestId: d.requestId, result } }));
        } catch (err: any) {
          window.dispatchEvent(new CustomEvent('gemini-vision-result', { detail: { requestId: d.requestId, error: err.message } }));
        }
      }
    };
    window.addEventListener('agent-desktop-event', handler as EventListener);
    return () => window.removeEventListener('agent-desktop-event', handler as EventListener);
  }, [addApp]);

  // Context menu dismiss
  useEffect(() => { if (!ctxMenu) return; const h = () => setCtxMenu(null); window.addEventListener('click', h); return () => window.removeEventListener('click', h); }, [ctxMenu]);

  const isThinking = status === 'thinking';

  return (
    <div className="w-screen h-screen flex flex-col bg-[#0a0a0f] overflow-hidden">
      {/* ── Top bar: minimal, floating feel ── */}
      <div className="h-10 flex items-center justify-between px-3 shrink-0 bg-black/40 backdrop-blur-xl border-b border-white/[0.04]">
        <div className="flex items-center gap-2 min-w-0">
          <button onClick={() => { window.location.hash = '#/'; }} className="p-1 rounded-lg text-white/30 hover:text-white hover:bg-white/5"><ArrowLeft size={14} /></button>
          {isThinking && <Zap size={12} className="text-yellow-400 animate-pulse" />}
          <span className="text-[13px] font-medium text-white/90 truncate">{title}</span>
          <span className="text-[10px] text-white/20 font-mono">{paneId}</span>
        </div>
        <div className="flex items-center gap-0.5">
          {/* Context bar */}
          {contextUsage != null && (
            <div className="flex items-center gap-1.5 mr-1.5 px-2 py-0.5 rounded-full bg-white/[0.04]">
              <div className="w-12 h-1 rounded-full bg-white/[0.06] overflow-hidden">
                <div className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400' : contextUsage > 50 ? 'bg-yellow-400' : 'bg-emerald-400'}`} style={{ width: `${contextUsage}%` }} />
              </div>
              <span className="text-[9px] text-white/30 font-mono">{contextUsage}%</span>
            </div>
          )}
          {isThinking && <span className="text-[9px] px-1.5 py-0.5 rounded-full bg-yellow-500/10 text-yellow-400/80 animate-pulse mr-1">thinking</span>}
          <button onClick={() => setSettingsOpen(true)} className="p-1.5 rounded-lg text-white/20 hover:text-white/60 hover:bg-white/5"><Settings size={13} /></button>
          <button onClick={handleRestart} disabled={isRestarting} className="p-1.5 rounded-lg text-white/20 hover:text-orange-400 hover:bg-white/5 disabled:opacity-20"><RotateCcw size={13} className={isRestarting ? 'animate-spin' : ''} /></button>
        </div>
      </div>

      {/* ── Main area ── */}
      <div className="flex-1 flex overflow-hidden">
        {/* Desktop canvas */}
        <div className="flex-1 min-w-0 relative" onClick={() => { setCtxMenu(null); if (editMode) setEditMode(false); }}>
          {/* Background */}
          <div className="absolute inset-0 bg-gradient-to-br from-[#0f0f1a] via-[#111827] to-[#0c1222]" />
          <div className="absolute inset-0 opacity-[0.03]" style={{ backgroundImage: 'radial-gradient(circle at 1px 1px, white 1px, transparent 0)', backgroundSize: '32px 32px' }} />

          {/* App icons - iPhone grid */}
          <div className="absolute inset-0 z-10 p-6 pt-4 flex flex-wrap content-start gap-5 pointer-events-none overflow-y-auto" onClick={() => editMode && setEditMode(false)}>
            {apps.map(app => (
              <div key={app.id} className={`w-[68px] flex flex-col items-center gap-1.5 select-none pointer-events-auto relative ${editMode ? 'animate-wiggle' : ''}`}
                onClick={() => { if (!editMode) openInElectron(app.url, app.label); }}
                onContextMenu={e => { e.preventDefault(); setEditMode(true); }}>
                {editMode && (
                  <button onClick={e => { e.stopPropagation(); removeApp(app.id); if (apps.length <= 1) setEditMode(false); }}
                    className="absolute -top-1 -left-1 z-20 w-5 h-5 rounded-full bg-red-500 flex items-center justify-center shadow-lg hover:bg-red-400">
                    <X size={10} className="text-white" />
                  </button>
                )}
                <div className="w-[52px] h-[52px] rounded-[14px] bg-gradient-to-br from-white/[0.12] to-white/[0.04] backdrop-blur-md flex items-center justify-center text-[28px] shadow-lg shadow-black/20 group-hover:scale-110 active:scale-95 transition-all duration-150 border border-white/[0.06]">
                  {app.emoji}
                </div>
                <span className="text-[10px] text-white/60 truncate w-full text-center leading-tight">{app.label}</span>
              </div>
            ))}
          </div>

          {/* Empty state */}
          {apps.length === 0 && (
            <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
              <div className="text-center">
                <div className="text-5xl mb-3 opacity-20">✨</div>
                <div className="text-[11px] text-white/15">Ask your agent to build something</div>
              </div>
            </div>
          )}

          {/* Context menu */}
          {ctxMenu && (
            <div className="fixed z-[99999] bg-[#2a2a2e]/95 backdrop-blur-xl border border-white/[0.08] rounded-xl shadow-2xl py-1 min-w-[130px]" style={{ left: ctxMenu.x, top: ctxMenu.y }} onClick={e => e.stopPropagation()}>
              <button onClick={() => { const a = apps.find(x => x.id === ctxMenu.appId); if (a) openInElectron(a.url, a.label); setCtxMenu(null); }} className="w-full px-3 py-1.5 text-left text-[11px] text-white/80 hover:bg-white/10 rounded-md mx-0.5" style={{ width: 'calc(100% - 4px)' }}>Open</button>
              <div className="h-px bg-white/[0.06] my-0.5 mx-2" />
              <button onClick={() => { removeApp(ctxMenu.appId); setCtxMenu(null); }} className="w-full px-3 py-1.5 text-left text-[11px] text-red-400/80 hover:bg-white/10 rounded-md mx-0.5 flex items-center gap-1.5" style={{ width: 'calc(100% - 4px)' }}><Trash2 size={11} />Remove</button>
            </div>
          )}

          {/* Command panel */}
          <CommandPanel ref={commandPanelRef} paneTarget={paneId} title={title} token={token} panelPosition={panelPos} panelSize={panelSize} readOnly={false} onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}} onChange={(pos, size) => { setPanelPos(pos); setPanelSize(size); }} onDraggingChange={setIsDragging} canSend={true} agentStatus={status} contextUsage={contextUsage} mouseMode={mouseMode} onToggleMouse={handleToggleMouse} onRestart={handleRestart} isRestarting={isRestarting} onCapturePane={handleCapture} hasEditPermission={hasPermission('edit')} hasRestartPermission={hasPermission('restart')} hasCapturePermission={hasPermission('capture')} disableDrag={false} />
        </div>

        {/* Right drawer - always open */}
        <Resizer width={drawerW} onChange={w => setDrawerW(w)} onDragging={setIsDragging} />
        <div className="shrink-0" style={{ width: drawerW }}>
          <Drawer tab={drawerTab} onTabChange={setDrawerTab}>
            <ChatView paneId={paneId} token={token!} />
            <div className="h-full"><TerminalFrame paneId={paneId} token={token!} /></div>
          </Drawer>
        </div>
      </div>

      {/* Drag mask - covers everything including iframes */}
      {isDragging && <div className="fixed inset-0 z-[99998]" />}

      {/* Settings float */}
      {settingsOpen && <SettingsFloat paneId={paneId} fullPaneId={fullPaneId} onClose={() => setSettingsOpen(false)} />}

      {/* Toast */}
      {toast && <div className="fixed top-3 left-1/2 -translate-x-1/2 px-4 py-1.5 text-white text-[11px] font-medium rounded-full shadow-2xl bg-white/10 backdrop-blur-xl border border-white/[0.06] z-[999999]">{toast}</div>}
    </div>
  );
};

export default AgentPage;
