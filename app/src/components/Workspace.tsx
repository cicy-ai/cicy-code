import { useState, useEffect, useRef, useCallback } from 'react';
import {
  Terminal, MessageSquare, Home, Folder, FolderOpen, X, Settings, Brain, Search,
  LayoutList, Users, RotateCcw, Plus, Pin, PinOff, Menu, ExternalLink, Key, Bug, Server
} from 'lucide-react';
import { cn } from '../lib/utils';
import { lockPointer, unlockPointer } from '../lib/pointerLock';
import { useDevRegister } from '../lib/devStore';
import { Panel, Group, Separator } from 'react-resizable-panels';
import { useAuth } from '../contexts/AuthContext';
import { SendingProvider } from '../contexts/SendingContext';
import ChatView from './chat/ChatView';
import { CommandPanel } from './terminal/CommandPanel';
import { WindowManager } from './terminal/WindowManager';
import { VoiceFloatingButton } from './VoiceFloatingButton';
import { WebFrame } from './WebFrame';
import FloatingCodeWindow from './FloatingCodeWindow';
import DesktopCanvas from './layout/DesktopCanvas';
import TeamPanel from './layout/TeamPanel';
import SkillPanel from './layout/SkillPanel';
import SettingsFloat from './layout/SettingsFloat';
import TokenDialog from './layout/TokenDialog';
import useDesktopEvents from './layout/useDesktopEvents';
import { useDialog } from '../contexts/DialogContext';
import config, { urls } from '../config';
import apiService from '../services/api';
import { sendCommandToTmux } from '../services/mockApi';
import { ApiSwitchDialog } from './layout/ApiSwitchDialog';

const cache = {
  get: (k: string, def: any) => { try { const v = JSON.parse(localStorage.getItem(k)!); return v ?? def; } catch { return def; } },
  set: (k: string, v: any) => localStorage.setItem(k, JSON.stringify(v)),
};

const getFloatingOpenKey = (paneId: string) => `ws_floatingCodeOpen:${paneId}`;
const TEAM_TERMINAL_CHILDREN_KEY = 'ws_teamTerminalChildren';
const TEAM_TERMINAL_ACTIVE_KEY = 'ws_teamTerminalActive';

interface Props { agentId: string; onSelectAgent: (id: string) => void; }

export default function Workspace({ agentId, onSelectAgent }: Props) {
  const { token, hasPermission } = useAuth();
  const { confirm } = useDialog();
  const paneId = agentId || 'w-10001';
  const fullPaneId = `${paneId}:main.0`;
  const initialPaneIdRef = useRef(paneId);
  const floatingOpenKey = getFloatingOpenKey(initialPaneIdRef.current);

  const mainTab = 'cli' as const;
  const [leftPanel, setLeftPanel] = useState<'team' | 'skills' | null>(() => {
    const v = cache.get('ws_leftPanel', null);
    if (v === 'team' || v === 'skills') return v;
    return 'team';
  });
  const [agentsOpen, set智能体Open] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [apiOpen, setApiOpen] = useState(false);
  const [panelSizes, setPanelSizes] = useState<Record<string, number>>(() => cache.get('ws_panelSizes', { 'left-panel': 50, 'right-panel': 50 }));
  const [toast, setToast] = useState<string | null>(null);

  const [status, setStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode, setMouseMode] = useState<'on' | 'off'>('off');
  const [isRestarting, setIsRestarting] = useState(false);
  const [agents, set智能体] = useState<any[]>([]);
  const [paneDetails, setPaneDetails] = useState<Record<string, any>>({});
  const [codeServerSrc, setCodeServerSrc] = useState('');
  const [floatingCodeOpen, setFloatingCodeOpen] = useState(() => cache.get(floatingOpenKey, false));
  const [codeFolder, setCodeFolder] = useState('');
  const [teamTerminalChildren, set团队TerminalChildren] = useState<Record<string, string[]>>(() => cache.get(TEAM_TERMINAL_CHILDREN_KEY, {}));
  const [teamTerminalActive, set团队TerminalActive] = useState<Record<string, string>>(() => cache.get(TEAM_TERMINAL_ACTIVE_KEY, {}));
  const codeWindowInitializedRef = useRef(false);
  const agentWorkspaceRef = useRef(`~/workers/${paneId}`);

  const handleCodeHome = () => {
    const next = urls.codeServer(config.hostHome, token!);
    window.open(next, '_blank');
    if (next !== codeServerSrc) { setCodeServerSrc(next); setCodeFolder(config.hostHome); }
  };
  const isOpenClawPane = paneId === 'w-10001';
  const handleCodeServiceOpen = () => {
    if (codeServerSrc) window.open(codeServerSrc, '_blank');
  };
  const handleOpenClawOpen = () => {
    if (!token) return;
    window.open(urls.openClaw(token), '_blank');
  };

  const navigateToFolder = (folder: string) => {
    const next = urls.codeServer(folder, token!);
    if (next !== codeServerSrc) { setCodeServerSrc(next); setCodeFolder(folder); }
  };
  const [agentDetail, setAgentDetail] = useState<any>(null);
  const title = agentDetail?.title || '-';
  const [netLatency, setNetLatency] = useState<number | null>(null);
  const isApiOnlyRuntime = !!(agentDetail && (agentDetail.runtime_kind === 'cloudrun' || agentDetail.capabilities?.supports_tmux === false));

  const [showVoiceControl, setShowVoiceControl] = useState(false);
  const [voiceLoading, setVoiceLoading] = useState(false);
  const [voiceBtnPos, setVoiceBtnPos] = useState(() => cache.get('ws_voiceBtnPos', { x: 20, y: Math.max(60, window.innerHeight - 400) }));

  const [panelPos, setPanelPos] = useState(() => cache.get('agent_panelPos', { x: 20, y: Math.max(60, window.innerHeight - 280) }));
  const [panelSize, setPanelSize] = useState(() => cache.get('agent_panelSize', { width: 360, height: 220 }));
  const [activeWinIdx, setActiveWinIdx] = useState('0');
  const groupRef = useRef<any>(null);
  const mainAreaRef = useRef<HTMLDivElement>(null);
  const activityBarRef = useRef<HTMLDivElement>(null);

  const addApp = (window as any).__desktopAddApp || (() => {});
  useDesktopEvents(addApp);

  const leftActive = agentsOpen ? 'agents' : leftPanel;

  useEffect(() => { cache.set('ws_leftPanel', leftPanel); if (groupRef.current) { groupRef.current.setLayout(leftActive ? { 'left-panel': panelSizes['left-panel'] || 50, 'right-panel': panelSizes['right-panel'] || 50 } : { 'left-panel': 0, 'right-panel': 100 }); } }, [leftActive, leftPanel, panelSizes]);
  useEffect(() => { cache.set(floatingOpenKey, floatingCodeOpen); }, [floatingCodeOpen, floatingOpenKey]);
  useEffect(() => { cache.set(TEAM_TERMINAL_CHILDREN_KEY, teamTerminalChildren); }, [teamTerminalChildren]);
  useEffect(() => { cache.set(TEAM_TERMINAL_ACTIVE_KEY, teamTerminalActive); }, [teamTerminalActive]);
  useEffect(() => { cache.set('ws_voiceBtnPos', voiceBtnPos); }, [voiceBtnPos]);
  useEffect(() => { cache.set('agent_panelPos', panelPos); }, [panelPos]);
  useEffect(() => { cache.set('agent_panelSize', panelSize); }, [panelSize]);
  useEffect(() => {
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prevOverflow;
    };
  }, []);
  const onPanelLayout = useCallback((layout: Record<string, number>) => {
    setPanelSizes(layout);
    cache.set('ws_panelSizes', layout);
    if (layout['left-panel'] < 5) {
      if (leftActive === 'agents') set智能体Open(false);
      else setLeftPanel(null);
    }
  }, [leftActive]);

  useEffect(() => { if (!token) return; apiService.getPanes().then(({ data }) => set智能体(Array.isArray(data) ? data : data?.panes || [])).catch(() => {}); }, [token]);
  useEffect(() => { 
    apiService.getPane(fullPaneId).then(({ data }) => { 
      setAgentDetail(data);
      setPaneDetails(prev => ({ ...prev, [paneId]: data }));
      const workspace = data?.workspace || `~/workers/${paneId}`;
      agentWorkspaceRef.current = workspace;
      if (!codeWindowInitializedRef.current && token && !(data?.runtime_kind === 'cloudrun' || data?.capabilities?.supports_code_server === false || data?.capabilities?.supports_tmux === false)) {
        setCodeFolder(workspace);
        setCodeServerSrc(urls.codeServer(workspace, token));
        codeWindowInitializedRef.current = true;
      }
    }).catch(() => {}); 
  }, [fullPaneId, paneId, token]);
  const prevPaneId = useRef(paneId);
  useEffect(() => {
    if (prevPaneId.current !== paneId) {
      setAgentDetail(null); setStatus('idle'); setContextUsage(null);
      prevPaneId.current = paneId;
    }
  }, [paneId]);
  useEffect(() => {
    document.title = `${title} (${paneId}) | CiCy Code`;
  }, [title, paneId]);
  useEffect(() => {
    const poll = async () => {
      const t0 = performance.now();
      try {
        const { data } = await apiService.getAllStatus({ timeout: 1000 });
        setNetLatency(Math.round(performance.now() - t0));
        const st = data?.[fullPaneId];
        if (st?.status) setStatus(st.status);
        if (st?.title) setAgentDetail((prev: any) => prev ? { ...prev, title: st.title } : { title: st.title });
        if (st?.contextUsage != null) setContextUsage(st.contextUsage);
      } catch { setNetLatency(null); }
    };
    poll(); const id = setInterval(poll, 2000); return () => clearInterval(id);
  }, [fullPaneId]);

  // Toast listener
  useEffect(() => {
    const handler = (e: CustomEvent) => { setToast(e.detail); setTimeout(() => setToast(null), 5000); };
    window.addEventListener('show-toast', handler as EventListener);
    return () => window.removeEventListener('show-toast', handler as EventListener);
  }, []);

  // Status change listener (from WebSocket)
  useEffect(() => {
    const handler = (e: CustomEvent) => { if (e.detail?.status) setStatus(e.detail.status); };
    window.addEventListener('agent-status-change', handler as EventListener);
    return () => window.removeEventListener('agent-status-change', handler as EventListener);
  }, []);

  const handleRestart = () => {
    if (isApiOnlyRuntime) return;
    confirm(`Restart ${paneId}?`, async () => {
      setIsRestarting(true);
      try { await apiService.restartPane(paneId); for (let i = 0; i < 30; i++) { await new Promise(r => setTimeout(r, 1000)); try { const { data } = await apiService.getTtydStatus(paneId); if (data.status === 'running') break; } catch {} } } catch { alert('重启失败'); } finally { setIsRestarting(false); }
    });
  };
  const handleCapture = async () => { if (isApiOnlyRuntime) return; try { const { data } = await apiService.capturePane(paneId, 100); if (data.output) await navigator.clipboard.writeText(data.output); } catch {} };
  const handleToggleMouse = async () => { if (isApiOnlyRuntime) return; const n = mouseMode === 'on' ? 'off' : 'on'; try { await apiService.toggleMouse(n, fullPaneId); setMouseMode(n); } catch {} };

  const toggleLeft = (p: 'team' | 'skills') => {
    set智能体Open(false);
    setLeftPanel(prev => prev === p ? null : p);
  };

  const toggle智能体 = () => {
    if (agentsOpen) {
      set智能体Open(false);
      return;
    }
    setLeftPanel(null);
    set智能体Open(true);
  };

  const [loadedTtyds, setLoadedTtyds] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    if (token && paneId && !isApiOnlyRuntime) setLoadedTtyds(prev => new Set(prev).add(paneId));
  }, [paneId, token, isApiOnlyRuntime]);
  const current团队Children = teamTerminalChildren[paneId] || [];
  const currentTerminalTabs = [paneId, ...current团队Children];
  const current团队Active = currentTerminalTabs.includes(teamTerminalActive[paneId]) ? teamTerminalActive[paneId] : paneId;
  const settingsTargetPaneId = current团队Active || paneId;
  const settingsTargetFullPaneId = `${settingsTargetPaneId}:main.0`;
  const settingsTargetDetail = paneDetails[settingsTargetPaneId] || (settingsTargetPaneId === paneId ? agentDetail : null);
  const getPaneTitle = useCallback((id: string) => {
    const clean = id.replace(/:.*$/, '');
    const agent = agents.find((item: any) => {
      const agentId = (item.pane_id || item.id || '').replace(/:.*$/, '');
      return agentId === clean;
    });
    return agent?.title || clean;
  }, [agents]);
  const openPaneInCurrentTerminal = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    if (clean === paneId) {
      set团队TerminalActive(prev => ({ ...prev, [paneId]: clean }));
      return;
    }
    setLoadedTtyds(prev => {
      const next = new Set(prev);
      next.add(clean);
      return next;
    });
    set团队TerminalChildren(prev => {
      const current = prev[paneId] || [];
      if (current.includes(clean)) return prev;
      return { ...prev, [paneId]: [...current, clean] };
    });
    set团队TerminalActive(prev => ({ ...prev, [paneId]: clean }));
  }, [paneId]);
  const selectPaneInCurrentTerminal = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    if (!clean) return;
    set团队TerminalActive(prev => ({ ...prev, [paneId]: clean }));
  }, [paneId]);
  const closePaneInCurrentTerminal = useCallback((targetPaneId: string) => {
    const clean = targetPaneId.replace(/:.*$/, '');
    set团队TerminalChildren(prev => {
      const current = prev[paneId] || [];
      if (!current.includes(clean)) return prev;
      const nextChildren = current.filter(id => id !== clean);
      if (nextChildren.length === 0) {
        const next = { ...prev };
        delete next[paneId];
        return next;
      }
      return { ...prev, [paneId]: nextChildren };
    });
    set团队TerminalActive(prev => {
      if (prev[paneId] !== clean) return prev;
      const remaining = (teamTerminalChildren[paneId] || []).filter(id => id !== clean);
      return { ...prev, [paneId]: remaining[0] || paneId };
    });
  }, [paneId, teamTerminalChildren]);
  useEffect(() => {
    if (!token || !settingsTargetPaneId || settingsTargetPaneId === paneId || paneDetails[settingsTargetPaneId]) return;
    apiService.getPane(`${settingsTargetPaneId}:main.0`).then(({ data }) => {
      setPaneDetails(prev => ({ ...prev, [settingsTargetPaneId]: data }));
    }).catch(() => {});
  }, [paneId, paneDetails, settingsTargetPaneId, token]);

  const rightContent = (
    <div data-id="right-content" className="h-full flex flex-col relative">
      <header data-id="top-bar" className="h-12 border-b border-[var(--vsc-border)] bg-[#0A0A0A] flex items-center justify-between px-4 shrink-0 z-10">
        <div data-id="top-bar-left" className="flex items-center gap-3 w-1/3 min-w-0">
          {!isApiOnlyRuntime && (
            <button
              type="button"
              onClick={handleCodeHome}
              className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer shrink-0"
              title={agentDetail?.workspace || `~/workers/${paneId}`}
            >
              <Home className="w-3.5 h-3.5" />
            </button>
          )}
          <span data-id="agent-title" className="text-sm text-zinc-100 font-medium truncate max-w-[160px] bg-white/[0.12] px-2 py-0.5 rounded">{title}</span>
          <span data-id="pane-id-badge" className="text-xs font-mono text-zinc-600 bg-white/[0.03] px-2 py-1 rounded shrink-0">{paneId}</span>
          {!isApiOnlyRuntime && (
            <button
              type="button"
              onClick={handleCodeServiceOpen}
              className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer shrink-0"
              title="代码服务"
            >
              {floatingCodeOpen ? (
                <FolderOpen className="w-3.5 h-3.5" />
              ) : (
                <Folder className="w-3.5 h-3.5" />
              )}
            </button>
          )}
          {!isApiOnlyRuntime && isOpenClawPane && (
            <button
              type="button"
              onClick={handleOpenClawOpen}
              className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer shrink-0"
              title="OpenClaw"
            >
              <span className="text-[13px] leading-none" aria-label="OpenClaw">🦞</span>
            </button>
          )}
        </div>
        <div data-id="top-bar-center" className="flex items-center justify-center w-1/3" />
        <div data-id="top-bar-right" className="flex items-center justify-end w-1/3 gap-3">
          <NetworkSignal latency={netLatency} />
          <button onClick={() => setTokenOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title="API 令牌"><Key className="w-3.5 h-3.5" /></button>
          <button onClick={() => setApiOpen(true)} className="hidden p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title="API 服务器"><Server className="w-3.5 h-3.5" /></button>
          <button onClick={() => window.dispatchEvent(new Event('open-devtools-panel'))} className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer" title="开发工具"><Bug className="w-3.5 h-3.5" /></button>
          <span id="version" className="text-[10px] font-mono text-zinc-600">0.2.18</span>
          {contextUsage != null && (
            <div data-id="context-usage" className="flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-white/[0.02]">
              <div data-id="context-bar" className="w-12 h-1 rounded-full bg-white/[0.04] overflow-hidden">
                <div className={`h-full rounded-full ${contextUsage > 80 ? 'bg-red-400/60' : contextUsage > 50 ? 'bg-yellow-400/60' : 'bg-emerald-400/60'}`} style={{ width: `${contextUsage}%` }} />
              </div>
              <span data-id="context-pct" className="text-xs text-zinc-600 font-mono">{contextUsage}%</span>
            </div>
          )}
        </div>
      </header>
      <div data-id="right-tabs" className="flex-1 relative overflow-hidden">
        <div data-id="chat-tab" className="absolute inset-0 flex justify-center" style={{ display: mainTab === 'chat' ? 'flex' : 'none' }}>
          <div className="w-full max-w-5xl h-full">
            <ChatView paneId={paneId} token={token!} apiOnly={isApiOnlyRuntime} commandPanel={
            !isApiOnlyRuntime ? <CommandPanel paneTarget={paneId} title={title} token={token}
              panelPosition={panelPos} panelSize={panelSize} readOnly={false}
              onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}}
              onChange={(pos, size) => { setPanelPos(pos); setPanelSize(size); }}
              canSend={status === 'idle'} agentStatus={status} contextUsage={contextUsage}
              mouseMode={mouseMode} onToggleMouse={handleToggleMouse} onRestart={handleRestart}
              isRestarting={isRestarting} onCapturePane={handleCapture}
              hasEditPermission={hasPermission('edit')} hasRestartPermission={hasPermission('restart')}
              hasCapturePermission={hasPermission('capture')} showVoiceControl={showVoiceControl}
              onToggleVoiceControl={() => setShowVoiceControl(v => !v)} /> : null
          } />
          </div>
        </div>
        <div data-id="cli-tab" className="absolute inset-0 flex" style={{ display: mainTab === 'cli' ? 'flex' : 'none' }}>
          <div data-id="cli-terminal-area" className="w-full h-full relative bg-black">
            {!isApiOnlyRuntime ? current团队Children.length > 0 ? (
              <div className="absolute inset-0 flex flex-col">
                <div data-id="cli-terminal-tabs" className="h-11 shrink-0 border-b border-white/[0.08] bg-[#0b0b0b] px-3 flex items-center gap-2 overflow-x-auto">
                  {currentTerminalTabs.map(id => {
                    const isRoot = id === paneId;
                    const isActive = current团队Active === id;
                    return (
                      <button
                        type="button"
                        key={id}
                        className={cn(
                          "group flex items-center gap-2 min-w-0 max-w-[240px] px-3 py-1.5 rounded-lg border transition-colors cursor-pointer",
                          isActive
                            ? "border-blue-500/50 bg-blue-500/[0.08] text-blue-300 ring-1 ring-blue-500/20"
                            : "border-white/[0.08] bg-white/[0.03] text-zinc-400 hover:text-zinc-200 hover:border-white/[0.14]"
                        )}
                        onMouseDown={() => selectPaneInCurrentTerminal(id)}
                        onClick={() => selectPaneInCurrentTerminal(id)}
                        title={id}
                      >
                        <span className="text-[11px] font-medium truncate">{isRoot ? (title || getPaneTitle(id)) : getPaneTitle(id)}</span>
                        <span className="text-[10px] font-mono text-zinc-600 truncate">{id}</span>
                        {!isRoot ? (
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              closePaneInCurrentTerminal(id);
                            }}
                            className="p-0.5 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer shrink-0"
                            title="Remove from current pane"
                          >
                            <X className="w-3 h-3" />
                          </button>
                        ) : null}
                      </button>
                    );
                  })}
                </div>
                <div className="relative flex-1 bg-black">
                  {[...loadedTtyds].map(id => (
                    <div key={id} className="absolute inset-0" style={{ display: currentTerminalTabs.includes(id) && id === current团队Active ? 'block' : 'none' }}>
                      <WebFrame src={urls.ttydOpen(id, token!)} className="w-full h-full border-0 bg-black" title={`terminal-${id}`} />
                    </div>
                  ))}
                </div>
              </div>
            ) : [...loadedTtyds].map(id => (
              <div key={id} className="absolute inset-0" style={{ display: id === paneId ? 'block' : 'none' }}>
                <WebFrame src={urls.ttydOpen(id, token!)} className="w-full h-full border-0 bg-black" title={`terminal-${id}`} />
              </div>
            )) : (
              <div className="absolute inset-0 flex items-center justify-center text-sm text-zinc-500" data-id="workspace-api-only-terminal-empty">
                Cloud Run / API-only node does not support ttyd terminal
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );

  useDevRegister('Workspace', {
    paneId: fullPaneId, title, status, contextUsage, mouseMode, isRestarting,
    agentDetail, netLatency,
    agentsCount: agents.length,
    agents: agents.map((a: any) => ({ pane_id: a.pane_id, title: a.title, status: a.status, active: a.active })),
    leftPanel, floatingCodeOpen, activeWinIdx,
  });

  return (
    <SendingProvider>
    <div data-id="workspace-root" className="flex h-screen overflow-hidden bg-[#0A0A0A] text-zinc-400 relative">
      {/* Activity Bar */}
      <div data-id="activity-bar" ref={activityBarRef} className="w-14 border-r border-[var(--vsc-border)] flex flex-col items-center py-4 justify-between bg-[#0A0A0A] shrink-0 z-50">
        <div data-id="activity-bar-top" className="flex flex-col gap-4 w-full items-center">
          <SideBtn dataId="btn-team" active={leftPanel === 'team'} icon={<Users className="w-5 h-5" />} title="团队" onClick={() => toggleLeft('team')} />
          <SideBtn
            dataId="btn-code-window"
            active={leftActive === 'agents'}
            icon={<LayoutList className="w-5 h-5" />}
            title="智能体"
            onClick={toggle智能体}
          />
        </div>
        <div data-id="activity-bar-bottom" className="flex flex-col gap-4 w-full items-center">
          <SideBtn dataId="btn-settings" active={settingsOpen} icon={<Menu className="w-5 h-5" />} title="Menu" onClick={() => { setSettingsOpen(true); }} />
        </div>
      </div>

      {/* Main */}
      <div data-id="main-area" ref={mainAreaRef} className="flex-1 flex flex-col min-w-0">
        {/* Content */}
        <main data-id="content-area" className="flex-1 relative overflow-hidden">
          <Group id="main-layout" orientation="horizontal" groupRef={groupRef} defaultLayout={leftActive ? panelSizes : { 'left-panel': 0, 'right-panel': 100 }} onLayoutChanged={onPanelLayout}>
            <Panel id="left-panel" defaultSize={leftActive ? 50 : 0} minSize={0}>
              <div data-id="left-panel-wrap" className="h-full flex flex-col bg-[#0A0A0A] border-r border-[var(--vsc-border)] relative z-[130]" style={{ display: leftActive ? 'flex' : 'none' }}>
                <div data-id="left-panel-header" className="h-12 border-b border-[var(--vsc-border)] flex items-center px-2 bg-[#0e0e0e] shrink-0 gap-1">
                  {leftActive === 'agents' ? <>
                    <LayoutList className="w-3.5 h-3.5 text-zinc-600" />
                    <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">智能体</span>
                  </> : leftPanel === 'skills' ? <>
                    <Brain className="w-3.5 h-3.5 text-zinc-600" />
                    <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">技能</span>
                  </> : <>
                    <Users className="w-3.5 h-3.5 text-zinc-600" />
                    <span className="text-xs font-medium text-zinc-500 flex-1 ml-1">团队</span>
                  </>}
                  <button data-id="left-panel-close" onClick={() => { if (leftActive === 'agents') set智能体Open(false); else setLeftPanel(null); }} className="p-1 text-zinc-600 hover:text-zinc-300 rounded transition-colors cursor-pointer"><X className="w-3.5 h-3.5" /></button>
                </div>
                <div data-id="left-panel-body" className="flex-1 relative overflow-hidden bg-[#0A0A0A] z-[131]">
                  <div className="absolute inset-0 overflow-auto" style={{ display: leftActive === 'agents' ? 'block' : 'none' }}>
                    <AgentDrawer agents={agents} paneId={paneId}
                      onSelectAgent={onSelectAgent} on智能体Change={set智能体} />
                  </div>
                  <div className="absolute inset-0" style={{ display: leftPanel === 'team' ? 'block' : 'none' }}>
                    <TeamPanel
                      paneId={paneId}
                      onOpenInCurrentPane={openPaneInCurrentTerminal}
                      openedPaneIds={current团队Children}
                      activePaneId={current团队Active}
                    />
                  </div>
                  <div className="absolute inset-0 overflow-auto" style={{ display: leftPanel === 'skills' ? 'block' : 'none' }}>
                    <SkillPanel paneId={fullPaneId} />
                  </div>
                </div>
              </div>
            </Panel>
            {leftActive && <Separator className="w-1 bg-white/[0.02] hover:bg-blue-500/30 active:bg-blue-500/50 transition-colors cursor-col-resize" />}
            <Panel id="right-panel" defaultSize={leftActive ? 50 : 100} minSize={30}>
              {rightContent}
            </Panel>
          </Group>
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
            onRecordEnd={(should发送) => {
              const rec = (window as any).__voiceRec as MediaRecorder | undefined;
              if (rec && rec.state !== 'inactive') {
                rec.onstop = async () => {
                  (window as any).__voiceStream?.getTracks().forEach((t: any) => t.enabled = false);
                  if (!should发送) return;
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

      {!isApiOnlyRuntime && (
        <FloatingCodeWindow
          open={floatingCodeOpen}
          src={codeServerSrc}
          folderLabel={codeFolder.replace(config.hostHome, '~')}
          storageScopeId={initialPaneIdRef.current}
          onNavigate={navigateToFolder}
          onClose={() => setFloatingCodeOpen(false)}
        />
      )}

      {settingsOpen && (
        <div data-id="settings-overlay">
          <SettingsFloat
            paneId={settingsTargetPaneId}
            fullPaneId={settingsTargetFullPaneId}
            agentDetail={settingsTargetDetail}
            onAgentDetailChange={(d) => {
              setPaneDetails(prev => ({ ...prev, [settingsTargetPaneId]: { ...(prev[settingsTargetPaneId] || {}), ...d } }));
              if (settingsTargetPaneId === paneId) setAgentDetail((prev: any) => ({ ...(prev || {}), ...d }));
              set智能体(prev => prev.map(a => {
                const id = (a.pane_id || a.id || '').split(':')[0];
                return id === settingsTargetPaneId ? { ...a, ...d } : a;
              }));
            }}
            onClose={() => setSettingsOpen(false)}
          />
        </div>
      )}
      {tokenOpen && <TokenDialog onClose={() => setTokenOpen(false)} />}
      {apiOpen && <ApiSwitchDialog onClose={() => setApiOpen(false)} />}
      {toast && <div className="fixed top-4 left-1/2 -translate-x-1/2 z-[9999] px-4 py-2 bg-zinc-800 text-white text-sm rounded-lg shadow-lg">{toast}</div>}
    </div>
    </SendingProvider>
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

const PINNED_KEY = 'agent_pinned';
function getPinned(): string[] { try { return JSON.parse(localStorage.getItem(PINNED_KEY)!) || []; } catch { return []; } }
function setPinnedStorage(ids: string[]) { localStorage.setItem(PINNED_KEY, JSON.stringify(ids)); }

function AgentDrawer({ agents, paneId, onSelectAgent, on智能体Change }: {
  agents: any[]; paneId: string;
  onSelectAgent: (id: string) => void; on智能体Change: (a: any[]) => void;
}) {
  const [search, setSearch] = useState('');
  const [pinned, setPinned] = useState<string[]>(getPinned);
  const [adding, setAdding] = useState(false);
  const { confirm } = useDialog();

  const handleQuickAddMaster = async () => {
    setAdding(true);
    try {
      const { data } = await apiService.createPane({ role: 'worker', agent_type: 'codex' });
      const id = data?.pane_id || data?.id;
      if (id) {
        const { data: fresh } = await apiService.getPanes();
        on智能体Change(Array.isArray(fresh) ? fresh : fresh?.panes || []);
        onSelectAgent(id.split(':')[0]);
      }
    } catch {} finally { setAdding(false); }
  };

  const togglePin = (id: string) => {
    const next = pinned.includes(id) ? pinned.filter(p => p !== id) : [...pinned, id];
    setPinned(next);
    setPinnedStorage(next);
  };

  const handleAdd = async () => {
    setAdding(true);
    try {
      const { data } = await apiService.createPane({ role: 'worker', agent_type: 'codex' });
      const id = data?.pane_id || data?.id;
      if (id) {
        const { data: fresh } = await apiService.getPanes();
        on智能体Change(Array.isArray(fresh) ? fresh : fresh?.panes || []);
        onSelectAgent(id.split(':')[0]);
      }
    } catch {} finally { setAdding(false); }
  };

  const handleDelete = (id: string) => {
    const sid = id.split(':')[0];
    if (sid === 'w-10001') return;
    confirm(<>Delete <span className="text-zinc-100 font-medium">{sid}</span>?</>, async () => {
      try {
        await apiService.deletePane(id);
        const { data: fresh } = await apiService.getPanes();
        const list = Array.isArray(fresh) ? fresh : fresh?.panes || [];
        on智能体Change(list);
        if (sid === paneId) {
          const idx = agents.findIndex(a => (a.pane_id || a.id) === id);
          const next = agents[idx + 1] || agents[idx - 1];
          onSelectAgent(next ? (next.pane_id || next.id).split(':')[0] : 'w-10001');
        }
      } catch {}
    });
  };

  const q = search.toLowerCase();
  const filtered = agents.filter(a => {
    if (!q) return true;
    const id = (a.pane_id || a.id || '').toLowerCase();
    const title = (a.title || '').toLowerCase();
    return id.includes(q) || title.includes(q);
  });

  const pinned智能体 = filtered.filter(a => pinned.includes(a.pane_id || a.id));
  const unpinned智能体 = filtered.filter(a => !pinned.includes(a.pane_id || a.id));
  const sorted = [...pinned智能体, ...unpinned智能体];

  return (
    <div data-id="agent-drawer" className="h-full flex flex-col bg-[#0A0A0A]">
        <div data-id="agent-drawer-body" className="p-3 flex-1 overflow-y-auto bg-[#0A0A0A]">
          <div data-id="agent-search" className="mb-3 relative flex gap-2">
            <div className="relative flex-1">
              <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-zinc-600" />
              <input
                type="search"
                placeholder="搜索 ID 或标题..."
                value={search}
                onChange={e => setSearch(e.target.value)}
                className="w-full bg-white/[0.02] border border-[var(--vsc-border)] rounded-lg pl-9 pr-3 py-2 text-sm focus:outline-none focus:border-white/[0.08] placeholder:text-zinc-700 text-zinc-400"
              />
            </div>
            <button onClick={handleQuickAddMaster} disabled={adding}
              className="flex items-center gap-1 px-2 py-1.5 rounded-lg border border-[var(--vsc-border)] text-zinc-400 hover:text-zinc-200 hover:border-zinc-500 transition-colors cursor-pointer disabled:opacity-50 shrink-0"
              title="添加工作实例">
              <Plus className="w-4 h-4" />
            </button>
          </div>
          <div data-id="agent-list" className="space-y-2">
            {sorted.map((agent: any) => {
              const id = agent.pane_id || agent.id;
              const shortId = id?.replace(':main.0', '') || id;
              const isMaster = id?.includes('10001');
              const isActive = id === paneId || id?.startsWith(paneId + ':') || paneId?.startsWith(id + ':');
              const isPinned = pinned.includes(id);
              return (
                <div key={id} data-id={`agent-${id}`}
                  onClick={() => onSelectAgent(id)}
                  className={cn("w-full flex items-center gap-3 border p-3 rounded-xl transition-all group relative cursor-pointer",
                    isActive ? "border-blue-500/50 bg-blue-500/[0.08] ring-1 ring-blue-500/20" : "bg-white/[0.02] border-[var(--vsc-border)] hover:border-white/[0.08]")}>
                  {!isMaster && (
                    <button onClick={e => { e.stopPropagation(); handleDelete(id); }}
                      className="absolute right-0 top-0 w-5 h-5 rounded-full bg-red-500/80 flex items-center justify-center transition-colors cursor-pointer opacity-0 group-hover:opacity-100 hover:bg-red-500 z-10"
                      title="Delete">
                      <X className="w-3 h-3 text-white" />
                    </button>
                  )}
                  <div className="flex items-center gap-3 flex-1 min-w-0 text-left cursor-pointer">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-1.5">
                        <h3 className={cn("text-sm font-medium truncate", isActive ? "text-blue-300" : "text-zinc-300")}>{agent.title || shortId}</h3>
                        <span className={cn("w-1.5 h-1.5 rounded-full shrink-0", agent.active ? "bg-emerald-500/60" : "bg-zinc-700")} />
                      </div>
                      <p className={cn("text-xs font-mono mt-0.5 truncate", isActive ? "text-blue-400/50" : "text-zinc-600")}>{shortId}</p>
                    </div>
                  </div>
                  <button onClick={e => { e.stopPropagation(); window.open(`#/agent/${id.split(':')[0]}`, '_blank'); }}
                    className="p-1 rounded transition-colors shrink-0 cursor-pointer text-zinc-700 opacity-0 group-hover:opacity-100 hover:text-zinc-400"
                    title="在新窗口打开">
                    <ExternalLink className="w-3.5 h-3.5" />
                  </button>
                  <button onClick={e => { e.stopPropagation(); togglePin(id); }}
                    className={cn("p-1 rounded transition-colors shrink-0 cursor-pointer",
                      isPinned ? "text-amber-500/70 hover:text-amber-400" : "text-zinc-700 opacity-0 group-hover:opacity-100 hover:text-zinc-400")}
                    title={isPinned ? "Unpin" : "Pin"}>
                    {isPinned ? <Pin className="w-3.5 h-3.5" /> : <PinOff className="w-3.5 h-3.5" />}
                  </button>
                </div>
              );
            })}
          </div>
        </div>
    </div>
  );
}

function FloatCommand({ paneId, token, agentStatus, mouseMode, showVoiceControl, onToggleVoiceControl }: any) {
  const W = 420, H = 140;
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState(() => cache.get('terminal_drag_pos', { x: -1, y: -1 }));
  const [isDragging, setIsDragging] = useState(false);
  const startRef = useRef({ mx: 0, my: 0, px: 0, py: 0 });

  useEffect(() => {
    const init = () => {
      if (!ref.current?.parentElement) return;
      const pr = ref.current.parentElement.getBoundingClientRect();
      setPos(p => p.x >= 0 ? p : { x: (pr.width - W) / 2, y: pr.height - H - 36 });
    };
    requestAnimationFrame(init);
  }, []);

  const onDown = (e: React.MouseEvent) => {
    if (pos.x < 0) return;
    startRef.current = { mx: e.clientX, my: e.clientY, px: pos.x, py: pos.y };
    setIsDragging(true);
    lockPointer();
    const onMove = (ev: MouseEvent) => {
      const parent = ref.current?.parentElement;
      if (!parent) return;
      const pr = parent.getBoundingClientRect();
      setPos({ x: Math.max(0, Math.min(pr.width - W, startRef.current.px + ev.clientX - startRef.current.mx)), y: Math.max(0, Math.min(pr.height - H, startRef.current.py + ev.clientY - startRef.current.my)) });
    };
    const onUp = () => { setIsDragging(false); unlockPointer(); window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp); setPos(p => { cache.set('terminal_drag_pos', p); return p; }); };
    window.addEventListener('mousemove', onMove); window.addEventListener('mouseup', onUp);
    e.preventDefault();
  };

  if (pos.x < 0) return <div ref={ref} style={{ position: 'absolute', opacity: 0 }} />;
  return (
    <>
      {isDragging && <div style={{ position: 'absolute', inset: 0, zIndex: 49 }} />}
      <div data-id="float-command" ref={ref} onMouseDown={e => { if (e.clientY - ref.current!.getBoundingClientRect().top < 36 && !(e.target as HTMLElement).closest('button, select, input, [role="button"]')) onDown(e); }}
        style={{ position: 'absolute', left: pos.x, top: pos.y, width: W, height: H, borderRadius: 8, zIndex: 50 }}>
        <CommandPanel paneTarget={paneId} title="" token={token} panelPosition={{ x: 0, y: 0 }} panelSize={{ width: W, height: H }} readOnly={false} onReadOnlyToggle={() => {}} onInteractionStart={() => {}} onInteractionEnd={() => {}} onChange={() => {}} canSend={agentStatus === 'idle'} agentStatus={agentStatus} mouseMode={mouseMode} showVoiceControl={showVoiceControl} onToggleVoiceControl={onToggleVoiceControl} />
      </div>
    </>
  );
}

function NetworkSignal({ latency }: { latency: number | null }) {
  const bars = latency === null ? 0 : latency < 100 ? 4 : latency < 200 ? 3 : latency < 500 ? 2 : 1;
  const color = bars >= 4 ? 'bg-emerald-400' : bars === 3 ? 'bg-emerald-400' : bars === 2 ? 'bg-yellow-400' : bars === 1 ? 'bg-red-400' : 'bg-zinc-700';
  const label = latency === null ? 'offline' : `${latency}ms`;
  return (
    <div data-id="network-signal" className="flex items-end gap-[2px] h-4 cursor-default" title={label}>
      {[6, 8, 10, 12].map((h, i) => (
        <div key={i} className={`w-[3px] rounded-sm transition-colors ${i < bars ? color : 'bg-zinc-800'}`} style={{ height: h }} />
      ))}
      <span className="text-[10px] font-mono text-zinc-600 ml-1">{label}</span>
    </div>
  );
}
