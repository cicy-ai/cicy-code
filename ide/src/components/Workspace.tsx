import { useState, useEffect, useRef, useCallback } from 'react';
import {
  Terminal, MessageSquare, Code2, X, Settings, Brain, Search,
  Monitor, LayoutList, Users, RotateCcw
} from 'lucide-react';
import { cn } from '../lib/utils';
import { Panel, Group, Separator } from 'react-resizable-panels';
import { useAuth } from '../contexts/AuthContext';
import ChatView from './chat/ChatView';
import { CommandPanel } from './terminal/CommandPanel';
import { VoiceFloatingButton } from './VoiceFloatingButton';
import { WebFrame } from './WebFrame';
import DesktopCanvas from './layout/DesktopCanvas';
import TeamPanel from './layout/TeamPanel';
import SettingsFloat from './layout/SettingsFloat';
import useDesktopEvents from './layout/useDesktopEvents';
import config, { urls } from '../config';
import apiService from '../services/api';
import { sendCommandToTmux } from '../services/mockApi';

const cache = {
  get: (k: string, def: any) => { try { const v = JSON.parse(localStorage.getItem(k)!); return v ?? def; } catch { return def; } },
  set: (k: string, v: any) => localStorage.setItem(k, JSON.stringify(v)),
};

interface Props { agentId: string; onSelectAgent: (id: string) => void; }

export default function Workspace({ agentId, onSelectAgent }: Props) {
  const { token, hasPermission } = useAuth();
  const paneId = agentId || 'w-10001';
  const fullPaneId = `${paneId}:main.0`;

  const [mainTab, setMainTab] = useState<'chat' | 'cli'>(() => cache.get('ws_mainTab', 'chat'));
  const [leftPanel, setLeftPanel] = useState<'code' | 'desktop' | 'team' | null>(() => cache.get('ws_leftPanel', null));
  const [isAgentDrawerOpen, setIsAgentDrawerOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [panelSizes, setPanelSizes] = useState<Record<string, number>>(() => cache.get('ws_panelSizes', { 'left-panel': 50, 'right-panel': 50 }));
  const [codeEverOpened, setCodeEverOpened] = useState(false);

  const [title, setTitle] = useState(paneId);
  const [status, setStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode, setMouseMode] = useState<'on' | 'off'>('off');
  const [isRestarting, setIsRestarting] = useState(false);
  const [agents, setAgents] = useState<any[]>([]);
  const [workspace, setWorkspace] = useState(`${config.hostHome}/Private/workers/${paneId}`);

  const [showVoiceControl, setShowVoiceControl] = useState(false);
  const [voiceLoading, setVoiceLoading] = useState(false);
  const [voiceBtnPos, setVoiceBtnPos] = useState(() => cache.get('ws_voiceBtnPos', { x: 20, y: Math.max(60, window.innerHeight - 400) }));

  const [panelPos, setPanelPos] = useState(() => cache.get('agent_panelPos', { x: 20, y: Math.max(60, window.innerHeight - 280) }));
  const [panelSize, setPanelSize] = useState(() => cache.get('agent_panelSize', { width: 360, height: 220 }));

  const addApp = (window as any).__desktopAddApp || (() => {});
  useDesktopEvents(addApp);

  useEffect(() => { cache.set('ws_mainTab', mainTab); }, [mainTab]);
  useEffect(() => { cache.set('ws_leftPanel', leftPanel); }, [leftPanel]);
  useEffect(() => { cache.set('ws_voiceBtnPos', voiceBtnPos); }, [voiceBtnPos]);
  useEffect(() => { cache.set('agent_panelPos', panelPos); }, [panelPos]);
  useEffect(() => { cache.set('agent_panelSize', panelSize); }, [panelSize]);

  const onPanelLayout = useCallback((layout: Record<string, number>) => { setPanelSizes(layout); cache.set('ws_panelSizes', layout); }, []);

  useEffect(() => { if (!token) return; apiService.getPanes().then(({ data }) => setAgents(Array.isArray(data) ? data : data?.panes || [])).catch(() => {}); }, [token]);
  useEffect(() => { apiService.getPane(fullPaneId).then(({ data }) => { if (data?.title) setTitle(data.title); if (data?.workspace) setWorkspace((data.workspace as string).replace('~', config.hostHome)); }).catch(() => {}); }, [fullPaneId]);
  useEffect(() => {
    const poll = async () => { try { const { data } = await apiService.getAllStatus(); const st = data?.[fullPaneId]; if (st?.status) setStatus(st.status); if (st?.title) setTitle(st.title); if (st?.contextUsage != null) setContextUsage(st.contextUsage); } catch {} };
    poll(); const id = setInterval(poll, 5000); return () => clearInterval(id);
  }, [fullPaneId]);

  const handleRestart = async () => {
    if (!confirm(`Restart ${paneId}?`)) return; setIsRestarting(true);
    try { await apiService.restartPane(paneId); for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); try { const { data } = await apiService.getTtydStatus(paneId); if (data.status === 'running') { setTimeout(() => location.reload(), 500); return; } } catch {} } setTimeout(() => location.reload(), 500); } catch { alert('Restart failed'); } finally { setIsRestarting(false); }
  };
  const handleCapture = async () => { try { const { data } = await apiService.capturePane(paneId, 100); if (data.output) await navigator.clipboard.writeText(data.output); } catch {} };
  const handleToggleMouse = async () => { const n = mouseMode === 'on' ? 'off' : 'on'; try { await apiService.toggleMouse(n, fullPaneId); setMouseMode(n); } catch {} };

  const toggleLeft = (p: 'code' | 'desktop' | 'team') => { setIsAgentDrawerOpen(false); if (p === 'code') setCodeEverOpened(true); setLeftPanel(prev => prev === p ? null : p); };
  const isThinking = status === 'thinking';
  const codeServerUrl = token ? urls.codeServer(workspace, token) : '';

  const ttydUrl = token ? urls.ttydOpen(paneId, token) : '';

  const rightContent = (
    <div data-id="right-content" className="h-full flex flex-col relative">
      <div data-id="right-tabs" className="flex-1 relative overflow-hidden">
        <div data-id="chat-tab" className="absolute inset-0 flex justify-center" style={{ display: mainTab === 'chat' ? 'flex' : 'none' }}>
          <div className="w-full max-w-5xl h-full">
            <ChatView paneId={paneId} token={token!} commandPanel={
            <CommandPanel paneTarget={paneId} title={title} token={token}
              panelPosition={panelPos} panelSize={panelSize} readOnly={false}
              onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}}
              onChange={(pos, size) => { setPanelPos(pos); setPanelSize(size); }}
              canSend={true} agentStatus={status} contextUsage={contextUsage}
              mouseMode={mouseMode} onToggleMouse={handleToggleMouse} onRestart={handleRestart}
              isRestarting={isRestarting} onCapturePane={handleCapture}
              hasEditPermission={hasPermission('edit')} hasRestartPermission={hasPermission('restart')}
              hasCapturePermission={hasPermission('capture')} showVoiceControl={showVoiceControl}
              onToggleVoiceControl={() => setShowVoiceControl(v => !v)} />
          } />
          </div>
        </div>
        <div data-id="cli-tab" className="absolute inset-0 flex justify-center" style={{ display: mainTab === 'cli' ? 'flex' : 'none' }}>
          <div data-id="cli-terminal-area" className="w-full max-w-5xl h-full relative">
            {ttydUrl && <WebFrame src={ttydUrl} className="w-full h-full border-0 bg-black" title={`terminal-${paneId}`} />}
            <FloatCommand paneId={paneId} token={token} agentStatus={status} mouseMode={mouseMode}
              showVoiceControl={showVoiceControl} onToggleVoiceControl={() => setShowVoiceControl(v => !v)} />
          </div>
        </div>
      </div>
    </div>
  );

  return (
    <div data-id="workspace-root" className="flex h-screen overflow-hidden bg-[#0A0A0A] text-zinc-400 relative">
      {/* Activity Bar */}
      <div data-id="activity-bar" className="w-14 border-r border-[var(--vsc-border)] flex flex-col items-center py-4 justify-between bg-[#0A0A0A] shrink-0 z-50">
        <div data-id="activity-bar-top" className="flex flex-col gap-4 w-full items-center">
          <SideBtn dataId="btn-agents" active={isAgentDrawerOpen} icon={<LayoutList className="w-5 h-5" />} title="Agents" onClick={() => setIsAgentDrawerOpen(!isAgentDrawerOpen)} />
          <SideBtn dataId="btn-code" active={leftPanel === 'code'} icon={<Code2 className="w-5 h-5" />} title="Code Server" onClick={() => toggleLeft('code')} />
          <SideBtn dataId="btn-desktop" active={leftPanel === 'desktop'} icon={<Monitor className="w-5 h-5" />} title="Desktop" onClick={() => toggleLeft('desktop')} />
          <SideBtn dataId="btn-team" active={leftPanel === 'team'} icon={<Users className="w-5 h-5" />} title="Team" onClick={() => toggleLeft('team')} />
        </div>
        <div data-id="activity-bar-bottom" className="flex flex-col gap-4 w-full items-center">
          <SideBtn dataId="btn-settings" active={settingsOpen} icon={<Settings className="w-5 h-5" />} title="Settings" onClick={() => { setIsAgentDrawerOpen(false); setSettingsOpen(true); }} />
        </div>
      </div>

      {/* Main */}
      <div data-id="main-area" className="flex-1 flex flex-col min-w-0">
        {/* Top Bar */}
        <header data-id="top-bar" className="h-12 border-b border-[var(--vsc-border)] bg-[#0A0A0A] flex items-center justify-between px-4 shrink-0 z-10">
          <div data-id="top-bar-left" className="flex items-center gap-3 w-1/3 min-w-0">
            <span data-id="agent-title" className="text-sm text-zinc-400 truncate max-w-[160px]">{title}</span>
            <span data-id="pane-id-badge" className="text-xs font-mono text-zinc-600 bg-white/[0.03] px-2 py-1 rounded shrink-0">{paneId}</span>
          </div>
          <div data-id="top-bar-center" className="flex items-center justify-center w-1/3">
            <div data-id="tab-switcher" className="flex items-center bg-white/[0.02] border border-[var(--vsc-border)] rounded-lg p-0.5">
              {(['chat', 'cli'] as const).map(tab => (
                <button key={tab} data-id={`tab-${tab}`} onClick={() => setMainTab(tab)}
                  className={cn("flex items-center gap-2 px-5 py-1.5 text-sm font-medium rounded-md transition-all cursor-pointer",
                    mainTab === tab ? "bg-white/[0.06] text-zinc-200 shadow-sm" : "text-zinc-600 hover:text-zinc-400")}>
                  {tab === 'chat' ? <MessageSquare className="w-3.5 h-3.5" /> : <Terminal className="w-3.5 h-3.5" />}
                  {tab === 'chat' ? 'Chat' : 'CLI'}
                </button>
              ))}
            </div>
          </div>
          <div data-id="top-bar-right" className="flex items-center justify-end w-1/3 gap-3">
            {contextUsage != null && (
              <div data-id="context-usage" className="flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-white/[0.02]">
                <div data-id="context-bar" className="w-12 h-1 rounded-full bg-white/[0.04] overflow-hidden">
                  <div className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400/60' : contextUsage > 50 ? 'bg-yellow-400/60' : 'bg-emerald-400/60'}`} style={{ width: `${contextUsage}%` }} />
                </div>
                <span data-id="context-pct" className="text-xs text-zinc-600 font-mono">{contextUsage}%</span>
              </div>
            )}
            <div data-id="status-indicator" className="flex items-center gap-2 text-xs text-zinc-600">
              <span className={cn("w-1.5 h-1.5 rounded-full", isThinking ? "bg-yellow-500/60 animate-pulse" : "bg-emerald-500/60")} />
              {isThinking ? 'Thinking' : 'Active'}
            </div>
            {mainTab === 'cli' && (
              <button onClick={handleRestart} disabled={isRestarting} title="Restart agent"
                className="p-1.5 rounded-md text-zinc-600 hover:text-zinc-300 hover:bg-white/[0.05] transition-colors disabled:opacity-30">
                <RotateCcw className={cn("w-3.5 h-3.5", isRestarting && "animate-spin")} />
              </button>
            )}
          </div>
        </header>

        {/* Content */}
        <main data-id="content-area" className="flex-1 relative overflow-hidden">
          {/* Persistent hidden code-server iframe — survives panel close */}
          {codeEverOpened && codeServerUrl && leftPanel !== 'code' && (
            <div data-id="code-server-persist" className="absolute" style={{ width: 1, height: 1, overflow: 'hidden', opacity: 0, pointerEvents: 'none' }}>
              <WebFrame src={codeServerUrl} codeServer className="w-full h-full border-0" title="Code Server" />
            </div>
          )}

          {leftPanel ? (
            <Group id="main-layout" orientation="horizontal" defaultLayout={panelSizes} onLayoutChanged={onPanelLayout}>
              <Panel id="left-panel" defaultSize={50} minSize={25}>
                <div data-id="left-panel-wrap" className="h-full flex flex-col bg-[#0A0A0A] border-r border-[var(--vsc-border)]">
                  <div data-id="left-panel-header" className="h-9 border-b border-[var(--vsc-border)] flex items-center px-4 bg-[#0e0e0e] shrink-0">
                    {leftPanel === 'code' ? <Code2 className="w-3.5 h-3.5 text-zinc-600 mr-2" /> : leftPanel === 'team' ? <Users className="w-3.5 h-3.5 text-zinc-600 mr-2" /> : <Monitor className="w-3.5 h-3.5 text-zinc-600 mr-2" />}
                    <span data-id="left-panel-title" className="text-xs font-medium text-zinc-500">{leftPanel === 'code' ? 'Code Server' : leftPanel === 'team' ? 'Team' : 'Desktop'}</span>
                    <div className="flex-1" />
                    <button data-id="left-panel-close" onClick={() => setLeftPanel(null)} className="p-1 text-zinc-600 hover:text-zinc-400 rounded transition-colors cursor-pointer"><X className="w-3.5 h-3.5" /></button>
                  </div>
                  <div data-id="left-panel-body" className="flex-1 relative overflow-hidden">
                    {leftPanel === 'code' ? (
                      codeServerUrl ? <WebFrame src={codeServerUrl} codeServer className="w-full h-full border-0" title="Code Server" /> : <Placeholder icon={<Code2 className="w-10 h-10 opacity-10" />} text="Login required" />
                    ) : leftPanel === 'team' ? (
                      <TeamPanel paneId={paneId} token={token!} />
                    ) : (
                      <div data-id="desktop-wrap" className="absolute inset-0">
                        <DesktopCanvas paneId={paneId} codeDrawerOpen={false} onToggleCodeDrawer={() => setLeftPanel('code')} />
                      </div>
                    )}
                  </div>
                </div>
              </Panel>
              <Separator className="w-1 bg-white/[0.02] hover:bg-blue-500/30 active:bg-blue-500/50 transition-colors cursor-col-resize" />
              <Panel id="right-panel" defaultSize={50} minSize={30}>
                {rightContent}
              </Panel>
            </Group>
          ) : rightContent}
        </main>
      </div>

      {/* Voice */}
      {showVoiceControl && (
        <div data-id="voice-float">
          <VoiceFloatingButton initialPosition={voiceBtnPos} onPositionChange={setVoiceBtnPos}
            onRecordStart={() => {
              navigator.mediaDevices.getUserMedia({ audio: true }).then(s => {
                (window as any).__voiceStream = s;
                const rec = new MediaRecorder(s, { mimeType: 'audio/webm;codecs=opus' });
                (window as any).__voiceChunks = [] as Blob[];
                rec.ondataavailable = e => { if (e.data.size > 0) (window as any).__voiceChunks.push(e.data); };
                rec.start(); (window as any).__voiceRec = rec;
              });
            }}
            onRecordEnd={(shouldSend) => {
              const rec = (window as any).__voiceRec as MediaRecorder | undefined;
              if (rec && rec.state !== 'inactive') {
                rec.onstop = async () => {
                  (window as any).__voiceStream?.getTracks().forEach((t: any) => t.enabled = false);
                  if (!shouldSend) return;
                  const blob = new Blob((window as any).__voiceChunks || [], { type: 'audio/webm' });
                  if (blob.size < 100) return;
                  const fd = new FormData(); fd.append('file', blob, 'voice.webm'); fd.append('engine', 'google');
                  setVoiceLoading(true);
                  try { const { data } = await apiService.stt(fd); if (data.text) { window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneId, q: data.text } })); sendCommandToTmux(data.text, paneId); } } catch {} finally { setVoiceLoading(false); }
                };
                rec.stop();
              }
            }}
            isRecordingExternal={false} isLoading={voiceLoading}
          />
        </div>
      )}

      {/* Agent Drawer */}
      {isAgentDrawerOpen && (
        <div data-id="agent-drawer-overlay" className="absolute inset-0 z-40 flex justify-start">
          <div data-id="agent-drawer-backdrop" className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={() => setIsAgentDrawerOpen(false)} />
          <div data-id="agent-drawer" className="relative w-80 max-w-[85vw] h-full bg-[#0e0e0e] border-r border-[var(--vsc-border)] flex flex-col shadow-2xl animate-slide-in-left ml-14">
            <div data-id="agent-drawer-header" className="p-4 border-b border-[var(--vsc-border)] flex items-center justify-between">
              <div className="flex items-center gap-2"><Brain className="w-5 h-5 text-blue-500/70" /><span className="font-medium text-zinc-300">Agents</span></div>
              <button data-id="agent-drawer-close" onClick={() => setIsAgentDrawerOpen(false)} className="p-1.5 text-zinc-600 hover:text-zinc-400 hover:bg-white/[0.04] rounded-md cursor-pointer"><X className="w-4 h-4" /></button>
            </div>
            <div data-id="agent-drawer-body" className="p-4 flex-1 overflow-y-auto">
              <div data-id="agent-search" className="mb-4 relative">
                <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600" />
                <input type="text" placeholder="Search agents..." className="w-full bg-white/[0.02] border border-[var(--vsc-border)] rounded-lg pl-9 pr-3 py-2 text-sm focus:outline-none focus:border-white/[0.08] placeholder:text-zinc-700 text-zinc-400" />
              </div>
              <div data-id="agent-list" className="space-y-2">
                {agents.map((agent: any) => {
                  const id = agent.pane_id || agent.id;
                  const isMaster = id?.includes('10001');
                  return (
                    <button key={id} data-id={`agent-${id}`} onClick={() => { onSelectAgent(id); setIsAgentDrawerOpen(false); }}
                      className={cn("w-full flex items-center gap-3 bg-white/[0.02] border p-3 rounded-xl transition-all text-left cursor-pointer",
                        id === paneId ? "border-blue-500/30" : "border-[var(--vsc-border)] hover:border-white/[0.08]")}>
                      <div className="relative shrink-0">
                        <div data-id={`agent-badge-${id}`} className={cn("w-8 h-8 rounded-lg flex items-center justify-center font-mono font-bold text-sm",
                          isMaster ? "bg-emerald-500/[0.08] text-emerald-500/70" : "bg-amber-500/[0.08] text-amber-500/70")}>{isMaster ? 'M' : 'W'}</div>
                        <div className="absolute -top-1 -right-1 w-2.5 h-2.5 bg-[#0e0e0e] rounded-full flex items-center justify-center">
                          <div data-id={`agent-status-${id}`} className={cn("w-1.5 h-1.5 rounded-full", agent.active ? "bg-emerald-500/60" : "bg-zinc-700")} />
                        </div>
                      </div>
                      <div className="flex-1 min-w-0">
                        <h3 className="text-zinc-300 text-sm font-medium truncate">{agent.title || id}</h3>
                        <p className="text-zinc-600 text-xs font-mono mt-0.5 truncate">{id}</p>
                      </div>
                    </button>
                  );
                })}
              </div>
            </div>
          </div>
        </div>
      )}

      {settingsOpen && <div data-id="settings-overlay"><SettingsFloat paneId={paneId} fullPaneId={fullPaneId} onClose={() => setSettingsOpen(false)} /></div>}
    </div>
  );
}

function SideBtn({ dataId, active, icon, title, onClick }: { dataId: string; active: boolean; icon: React.ReactNode; title: string; onClick: () => void }) {
  return (
    <button data-id={dataId} onClick={onClick} className={cn("p-2.5 rounded-xl transition-all relative cursor-pointer", active ? "text-zinc-300 bg-white/[0.06]" : "text-zinc-600 hover:text-zinc-400 hover:bg-white/[0.03]")} title={title}>
      {icon}
      {active && <div className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-5 bg-blue-500/60 rounded-r" />}
    </button>
  );
}

function Placeholder({ icon, text }: { icon: React.ReactNode; text: string }) {
  return <div data-id="placeholder" className="absolute inset-0 flex items-center justify-center text-zinc-600 flex-col gap-4 pointer-events-none">{icon}<p className="text-sm">{text}</p></div>;
}

function FloatCommand({ paneId, token, agentStatus, mouseMode, showVoiceControl, onToggleVoiceControl }: any) {
  const W = 420, H = 140;
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState(() => cache.get('terminal_drag_pos', { x: -1, y: -1 }));
  const [isDragging, setIsDragging] = useState(false);
  const startRef = useRef({ mx: 0, my: 0, px: 0, py: 0 });

  useEffect(() => {
    if (pos.x >= 0 || !ref.current?.parentElement) return;
    const pr = ref.current.parentElement.getBoundingClientRect();
    setPos({ x: (pr.width - W) / 2, y: pr.height - H - 36 });
  }, [pos]);

  const onDown = (e: React.MouseEvent) => {
    if (pos.x < 0) return;
    startRef.current = { mx: e.clientX, my: e.clientY, px: pos.x, py: pos.y };
    setIsDragging(true);
    const onMove = (ev: MouseEvent) => {
      const parent = ref.current?.parentElement;
      if (!parent) return;
      const pr = parent.getBoundingClientRect();
      setPos({ x: Math.max(0, Math.min(pr.width - W, startRef.current.px + ev.clientX - startRef.current.mx)), y: Math.max(0, Math.min(pr.height - H, startRef.current.py + ev.clientY - startRef.current.my)) });
    };
    const onUp = () => { setIsDragging(false); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); setPos(p => { cache.set('terminal_drag_pos', p); return p; }); };
    window.addEventListener('mousemove', onMove); window.addEventListener('mouseup', onUp);
    e.preventDefault();
  };

  if (pos.x < 0) return <div ref={ref} style={{ position: 'absolute', opacity: 0 }} />;
  return (
    <>
      {isDragging && <div style={{ position: 'absolute', inset: 0, zIndex: 49 }} />}
      <div data-id="float-command" ref={ref} onMouseDown={e => { if (e.clientY - ref.current!.getBoundingClientRect().top < 36 && !(e.target as HTMLElement).closest('button, select, input, [role="button"]')) onDown(e); }}
        style={{ position: 'absolute', left: pos.x, top: pos.y, width: W, height: H, borderRadius: 8, zIndex: 50 }}>
        <CommandPanel paneTarget={paneId} title="" token={token} panelPosition={{ x: 0, y: 0 }} panelSize={{ width: W, height: H }} readOnly={false} onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}} onChange={() => {}} canSend={true} agentStatus={agentStatus} mouseMode={mouseMode} showVoiceControl={showVoiceControl} onToggleVoiceControl={onToggleVoiceControl} />
      </div>
    </>
  );
}
