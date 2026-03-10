import React, { useState, useEffect, useRef } from 'react';
import { Loader2 } from 'lucide-react';
import { CommandPanelHandle } from './components/CommandPanel';
import { VoiceFloatingButton } from './components/VoiceFloatingButton';
import { LoginForm } from './components/LoginForm';
import { AgentsBrowser } from './components/AgentsBrowser';
import apiService from './services/api';
import { urls } from './config';
import config from './config';
import { useDialog } from './contexts/DialogContext';
import { usePane } from './contexts/PaneContext';
import { useVoice } from './contexts/VoiceContext';
import { useApp } from './contexts/AppContext';
import LeftSidePanel from './components/LeftSidePanel';
import RightSidePanel from './components/RightSidePanel';
import MainMiddlePanel from './components/MainMiddlePanel';
import Drawer from './components/Drawer';

const App: React.FC = () => {
  const { closeDialog, activeDialog } = useDialog();
  const { currentPaneId, allPanes, loading: appLoading } = useApp();
  const {
    displayPaneId, token, setToken, isCheckingAuth, hasPermission,
    ttydWidth, setTtydWidth, isDragging, setIsDragging,
    leftCollapsed, setLeftCollapsed, rightCollapsed, setRightCollapsed,
    agentTabs, setAgentTabs, setActiveAgentTab, setActiveTab,
    settings, setSettings, isLoaded,
    toast, setToast,
  } = usePane();
  const { isListening, voiceMode, startRecording, stopRecording } = useVoice();

  const [drawerOpen, setDrawerOpen] = useState(false);
  const [boundAgents, setBoundAgents] = useState<string[]>([]);
  const [floatWindow, setFloatWindow] = useState<{paneId: string, x: number, y: number, w: number, h: number} | null>(null);
  const [floatDragging, setFloatDragging] = useState(false);
  const [midWidth, setMidWidth] = useState(() => {
    const saved = localStorage.getItem('midPanelWidth');
    return saved ? parseInt(saved) : 360;
  });
  const [pinnedPanes, setPinnedPanes] = useState<string[]>(() => {
    const saved = localStorage.getItem('pinnedPanes');
    return saved ? JSON.parse(saved) : [];
  });

  const commandPanelRef = useRef<CommandPanelHandle>(null);
  const mainIframeRef = useRef<HTMLIFrameElement>(null);

  // Esc key handler
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && activeDialog) closeDialog();
      if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key.toLowerCase() === 'd') {
        e.preventDefault();
        setDrawerOpen(prev => !prev);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [activeDialog]);

  // Load bound agents
  useEffect(() => {
    if (!token) return;
    apiService.getAgentsByPane(displayPaneId)
      .then(({ data }) => setBoundAgents(data.map((a: any) => a.name)))
      .catch(() => {});
  }, [token]);

  // Listen to pin changes
  useEffect(() => {
    const handlePinChange = () => {
      const saved = localStorage.getItem('pinnedPanes');
      setPinnedPanes(saved ? JSON.parse(saved) : []);
    };
    window.addEventListener('pinnedPanesChanged', handlePinChange);
    return () => window.removeEventListener('pinnedPanesChanged', handlePinChange);
  }, []);

  // Listen to float window toggle
  useEffect(() => {
    const handler = (e: CustomEvent) => {
      const paneId = e.detail?.paneId;
      if (paneId) {
        setFloatWindow(prev => prev?.paneId === paneId ? null : { paneId, x: Math.round(window.innerWidth / 2 - 230), y: Math.round(window.innerHeight / 2 - 300), w: 460, h: 600 });
      }
    };
    window.addEventListener('toggle-float-window', handler as EventListener);
    return () => window.removeEventListener('toggle-float-window', handler as EventListener);
  }, []);

  // SSE notify stream
  useEffect(() => {
    if (!token) return;
    const es = new EventSource(`${config.apiBase}/api/notify/stream?token=${token}`);
    es.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        // Broadcast all notify actions as CustomEvent for any component to listen
        window.dispatchEvent(new CustomEvent('notify', { detail: msg }));
        if (msg.action === 'open_drawer') {
          setDrawerOpen(true);
          if (msg.tab) setActiveTab(msg.tab);
        }
        if (msg.action === 'close_drawer') setDrawerOpen(false);
        if (msg.action === 'toggle_drawer') setDrawerOpen(prev => !prev);
        if (msg.action === 'switch_tab' && msg.tab) setActiveTab(msg.tab);
        if (msg.action === 'open_file' && msg.file) {
          setDrawerOpen(true);
          setActiveTab('Code');
          // file already opened by backend IPC, just close sidebar
          setTimeout(() => {
            const frame = document.querySelector('.code-server-iframe') as HTMLIFrameElement;
            if (frame?.contentWindow) {
              frame.contentWindow.postMessage({ type: 'cs-keys', keys: [
                { key: 'b', code: 'KeyB', ctrlKey: true }
              ]}, '*');
            }
          }, 800);
        }
        if (msg.action === 'refresh_code') {
          const frame = document.querySelector('.code-server-iframe') as HTMLIFrameElement;
          if (frame) { const src = frame.src; frame.src = ''; setTimeout(() => frame.src = src, 100); }
        }
        if (msg.message) {
          setToast(msg.message);
          setTimeout(() => setToast(null), 5000);
        }
      } catch {}
    };
    return () => es.close();
  }, [token]);

  if (isCheckingAuth) return (
    <div className="bg-vsc-bg w-screen h-screen flex items-center justify-center">
      <Loader2 size={48} className="text-vsc-accent animate-spin" />
    </div>
  );
  if (!token) return <LoginForm onLogin={(t) => setToken(t)} />;
  if (!isLoaded) return <div className="bg-vsc-bg w-screen h-screen" />;

  // No pane selected: loading or prompt to create
  if (!currentPaneId) {
    if (appLoading || allPanes.length === 0) {
      return (
        <div className="bg-vsc-bg w-screen h-screen flex flex-col items-center justify-center gap-4">
          {appLoading ? (
            <Loader2 size={48} className="text-vsc-accent animate-spin" />
          ) : (
            <>
              <p className="text-vsc-text-secondary text-sm">No agents found</p>
              <button onClick={() => window.dispatchEvent(new CustomEvent('refresh-panes'))} className="px-4 py-2 bg-vsc-button hover:bg-vsc-button-hover text-white text-sm rounded">Refresh</button>
            </>
          )}
        </div>
      );
    }
  }

  const leftW = leftCollapsed ? 0 : 240;
  const rightW = rightCollapsed ? 0 : Math.max(0, window.innerWidth - leftW - midWidth);

  const handleRightDrag = (e: React.MouseEvent) => {
    e.preventDefault();
    setIsDragging(true);
    const startX = e.clientX;
    const startMid = midWidth;
    
    const onMove = (ev: MouseEvent) => {
      const delta = ev.clientX - startX;
      const newMid = Math.max(360, startMid + delta);
      setMidWidth(newMid);
      localStorage.setItem('midPanelWidth', newMid.toString());
    };
    
    const onUp = () => {
      setIsDragging(false);
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
    
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  return (
    <div className="relative w-screen h-screen overflow-hidden font-sans">
      <div id="main" className="fixed inset-0">
        {!leftCollapsed && (
          <div id="left-side" className="absolute inset-y-0 left-0 bg-vsc-bg-secondary border-r border-vsc-border z-10" style={{width: leftW}}>
            <LeftSidePanel />
          </div>
        )}

        {/* Global drag overlay to prevent iframes from stealing events */}
        {(isDragging || floatDragging) && <div className="fixed inset-0 z-[9999] cursor-col-resize" />}

        <MainMiddlePanel ttydWidth={rightCollapsed ? window.innerWidth - leftW : midWidth} boundAgents={boundAgents} mainIframeRef={mainIframeRef} commandPanelRef={commandPanelRef} pinnedPanes={pinnedPanes} setPinnedPanes={setPinnedPanes} leftWidth={leftW} leftCollapsed={leftCollapsed} rightCollapsed={rightCollapsed} onToggleLeft={() => setLeftCollapsed(!leftCollapsed)} onToggleRight={() => setRightCollapsed(!rightCollapsed)} drawerOpen={drawerOpen} onToggleDrawer={() => setDrawerOpen(!drawerOpen)} />

        {!rightCollapsed && (
          <>
            <div id="drag" className="absolute inset-y-0 cursor-col-resize hover:bg-blue-500/50 z-20" style={{left: leftW + midWidth - 2, width: '4px'}} onMouseDown={handleRightDrag} />
            <div id="right-side" className="absolute inset-y-0 right-0 bg-vsc-bg-secondary border-l border-vsc-border z-10" style={{width: rightW}}>
              <RightSidePanel ttydWidth={0} isDragging={false} setBoundAgents={setBoundAgents} boundAgents={boundAgents} leftWidth={0} onCloseDrawer={() => setRightCollapsed(true)} />
            </div>
          </>
        )}
      </div>

      {floatWindow && token && (
        <FloatTtydWindow
          paneId={floatWindow.paneId}
          title={allPanes.find(p => p.pane_id === floatWindow.paneId)?.title || floatWindow.paneId}
          token={token}
          x={floatWindow.x} y={floatWindow.y} w={floatWindow.w} h={floatWindow.h}
          onClose={() => setFloatWindow(null)}
          onChange={(x, y, w, h) => setFloatWindow(prev => prev ? {...prev, x, y, w, h} : null)}
          onDraggingChange={setFloatDragging}
        />
      )}

      {settings.showVoiceControl && hasPermission('prompt') && (
        <div style={{position:"fixed",zIndex:1111111,top:0,right:0,left:0,height:32,pointerEvents:"none"}}><div style={{pointerEvents:"auto",display:"inline-block"}}>
        <VoiceFloatingButton
          initialPosition={settings.voiceButtonPosition}
          onPositionChange={pos => setSettings(prev => ({ ...prev, voiceButtonPosition: pos }))}
          onRecordStart={() => startRecording('direct')}
          onRecordEnd={(shouldSend) => stopRecording(shouldSend)}
          isRecordingExternal={isListening && voiceMode === 'direct'}
          disabled={false}
        />
        </div></div>
      )}

      <Drawer open={drawerOpen} onClose={() => setDrawerOpen(false)}>
        <div className="p-4 text-vsc-text-secondary">
          Drawer (reserved for future use)
        </div>
      </Drawer>

      {toast && (
        <div className={`fixed top-4 left-1/2 -translate-x-1/2 px-4 py-2.5 text-white text-sm font-medium rounded-lg shadow-lg transition-all ${toast?.startsWith('Failed') || toast?.startsWith('Error') ? 'bg-red-500/90' : 'bg-emerald-500/90'}`} style={{zIndex: 999999999}}>{toast}</div>
      )}

      {activeDialog === 'addAgent' && (
        <div style={{position: 'fixed', inset: 0, backgroundColor: 'rgba(0,0,0,0.5)', backdropFilter: 'blur(4px)', zIndex: 9999999, display: 'flex', alignItems: 'center', justifyContent: 'center'}}>
          <div style={{width: '90%', height: '90%', backgroundColor: '#1e1e1e', borderRadius: '8px', overflow: 'hidden'}}>
            <AgentsBrowser token={token} existingTabs={agentTabs.map(t => t.paneId)} onAddAgent={(paneId, title, url) => {
              if (!agentTabs.find(t => t.paneId === paneId)) setAgentTabs([...agentTabs, {paneId, title, url, closable: true}]);
              setActiveAgentTab(paneId);
              closeDialog();
            }} />
          </div>
        </div>
      )}
    </div>
  );
};

const FloatTtydWindow: React.FC<{
  paneId: string; title: string; token: string; x: number; y: number; w: number; h: number;
  onClose: () => void; onChange: (x: number, y: number, w: number, h: number) => void;
  onDraggingChange: (v: boolean) => void;
}> = ({ paneId, title, token, x, y, w, h, onClose, onChange, onDraggingChange }) => {
  const ref = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState(false);

  const startDrag = (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest('button')) return;
    e.preventDefault();
    setActive(true); onDraggingChange(true);
    const sx = e.clientX - x, sy = e.clientY - y;
    const onMove = (ev: MouseEvent) => onChange(Math.max(0, ev.clientX - sx), Math.max(0, ev.clientY - sy), w, h);
    const onUp = () => { setActive(false); onDraggingChange(false); document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  const startResize = (e: React.MouseEvent) => {
    e.preventDefault(); e.stopPropagation();
    setActive(true); onDraggingChange(true);
    const sx = e.clientX, sy = e.clientY, sw = w, sh = h;
    const onMove = (ev: MouseEvent) => onChange(x, y, Math.max(240, sw + ev.clientX - sx), Math.max(200, sh + ev.clientY - sy));
    const onUp = () => { setActive(false); onDraggingChange(false); document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  const ttydUrl = urls.ttydOpen(paneId, token);

  return (
    <>
      {active && <div className="fixed inset-0 z-[9999997]" />}
      <div ref={ref} className="fixed flex flex-col bg-vsc-bg border border-vsc-border rounded-lg shadow-2xl overflow-hidden" style={{ left: x, top: y, width: w, height: h, zIndex: 9999998 }}>
        <div className="h-8 bg-vsc-bg-titlebar border-b border-vsc-border flex items-center justify-between px-2 cursor-move select-none flex-shrink-0" onMouseDown={startDrag}>
          <span className="text-xs text-vsc-text truncate">{title}</span>
          <button onClick={onClose} className="p-0.5 rounded text-vsc-text-secondary hover:text-red-400 hover:bg-red-500/20">
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <div className="relative flex-1">
          <iframe src={ttydUrl} className="w-full h-full border-0" />
          {active && <div className="absolute inset-0" />}
        </div>
        <div className="absolute bottom-0 right-0 w-4 h-4 cursor-nwse-resize" onMouseDown={startResize} />
      </div>
    </>
  );
};

export default App;
