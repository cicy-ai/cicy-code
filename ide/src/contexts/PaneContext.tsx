import React, { createContext, useContext, useState, useEffect, useRef, useCallback, ReactNode } from 'react';
import { AppSettings, Position, Size } from '../types';
import { urls } from '../config';
import apiService from '../services/api';
import { TokenManager } from '../services/tokenManager';
import { useApp } from './AppContext';

const CurrentPaneId = decodeURIComponent(window.location.href.split("/")[4] || '');
const STORAGE_KEY = `ttyd_app_settings_v1_${CurrentPaneId || 'default'}`;

const DEFAULT_SETTINGS: AppSettings = {
  panelPosition: { x: Math.max(20, window.innerWidth - 380), y: Math.max(60, window.innerHeight - 240) },
  panelSize: { width: 360, height: 220 },
  forwardEvents: true,
  lastDraft: '',
  showPrompt: true,
  showVoiceControl: false,
  voiceButtonPosition: { x: 40, y: 36 },
  commandHistory: [],
  agent_duty: ''
};

interface AgentTab {
  paneId: string;
  title: string;
  url: string;
  closable: boolean;
}

interface PaneContextType {
  // Identity
  displayPaneId: string;
  displayPaneTitle: string;

  // Auth
  token: string | null;
  setToken: (t: string | null) => void;
  userPerms: string[];
  isCheckingAuth: boolean;
  hasPermission: (perm: string) => boolean;

  // Layout
  ttydWidth: number;
  setTtydWidth: (w: number) => void;
  isDragging: boolean;
  setIsDragging: (v: boolean) => void;
  isInteracting: boolean;
  setIsInteracting: (v: boolean) => void;
  commandPanelHeight: number;
  leftCollapsed: boolean;
  setLeftCollapsed: (v: boolean) => void;
  rightCollapsed: boolean;
  setRightCollapsed: (v: boolean) => void;

  // Tabs
  activeTab: string;
  setActiveTab: (tab: any) => void;
  agentsSubTab: 'All' | 'Binded';
  setAgentsSubTab: (t: 'All' | 'Binded') => void;
  previewTab: number;
  setPreviewTab: (t: number) => void;

  // Agent state
  agentStatus: string;
  contextUsage: number | null;
  mouseMode: 'on' | 'off';
  readOnly: boolean;
  setReadOnly: (v: boolean) => void;
  isRestarting: boolean;

  // Agent tabs
  agentTabs: AgentTab[];
  setAgentTabs: (tabs: AgentTab[]) => void;
  activeAgentTab: string;
  setActiveAgentTab: (id: string) => void;

  // Visited panes
  visitedPanes: string[];

  // Settings
  settings: AppSettings;
  setSettings: (s: AppSettings | ((prev: AppSettings) => AppSettings)) => void;
  isLoaded: boolean;

  // Network
  networkLatency: number | null;
  networkStatus: 'excellent' | 'good' | 'poor' | 'offline';
  toast: string | null;
  setToast: (t: string | null) => void;

  // Operations
  handleRestart: (paneId?: string) => Promise<void>;
  handleCapturePane: (paneId?: string, lines?: number) => Promise<void>;
  handleToggleMouse: () => Promise<void>;
  handlePanelChange: (pos: Position, size: Size) => void;

  // Capture
  captureOutput: string | null;
  setCaptureOutput: (v: string | null) => void;
  isCapturing: boolean;
}

const PaneContext = createContext<PaneContextType | undefined>(undefined);

export const PaneProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const { currentPaneId, currentPane, paneDetail } = useApp();

  // Auth
  const [token, setToken] = useState<string | null>(null);
  const [userPerms, setUserPerms] = useState<string[]>([]);
  const [isCheckingAuth, setIsCheckingAuth] = useState(true);

  // Layout
  const [ttydWidth, setTtydWidth] = useState(() => {
    const saved = localStorage.getItem(`${CurrentPaneId}_ttydWidth`);
    return saved ? parseInt(saved) : 360;
  });
  const [isDragging, setIsDragging] = useState(false);
  const [isInteracting, setIsInteracting] = useState(false);
  const [commandPanelHeight] = useState(220);
  const [leftCollapsed, setLeftCollapsed] = useState(() => localStorage.getItem('leftCollapsed') === 'true');
  const [rightCollapsed, setRightCollapsed] = useState(() => localStorage.getItem('rightCollapsed') === 'true');

  // Tabs
  const [activeTab, setActiveTab] = useState<string>(() => {
    return localStorage.getItem('activeTab') || 'Agents';
  });
  const [agentsSubTab, setAgentsSubTab] = useState<'All' | 'Binded'>('All');
  const [previewTab, setPreviewTab] = useState<number>(() => {
    const saved = localStorage.getItem(`${CurrentPaneId}_previewTab`);
    return saved ? parseInt(saved) : 0;
  });

  // Agent state
  const [agentStatus, setAgentStatus] = useState('idle');
  const [contextUsage, setContextUsage] = useState<number | null>(null);
  const [mouseMode, setMouseMode] = useState<'on' | 'off'>('off');
  const [readOnly, setReadOnly] = useState(true);
  const [isRestarting, setIsRestarting] = useState(false);
  const [isTogglingMouse, setIsTogglingMouse] = useState(false);

  // Agent tabs
  const [agentTabs, setAgentTabs] = useState<AgentTab[]>([]);
  const [activeAgentTab, setActiveAgentTab] = useState('');

  // Visited panes
  const [visitedPanes, setVisitedPanes] = useState<string[]>([]);

  // Settings
  const [settings, setSettings] = useState<AppSettings>(DEFAULT_SETTINGS);
  const [isLoaded, setIsLoaded] = useState(false);

  // Network
  const [networkLatency, setNetworkLatency] = useState<number | null>(null);
  const [networkStatus, setNetworkStatus] = useState<'excellent' | 'good' | 'poor' | 'offline'>('good');
  const [toast, setToast] = useState<string | null>(null);

  // Capture
  const [captureOutput, setCaptureOutput] = useState<string | null>(null);
  const [isCapturing, setIsCapturing] = useState(false);

  // Derived
  const displayPaneId = currentPaneId!;
  const displayPaneTitle = paneDetail?.title || currentPane?.title || displayPaneId || 'No pane selected';
  const hasPermission = useCallback((perm: string) => userPerms.includes('api_full') || userPerms.includes(perm), [userPerms]);

  // Track visited panes
  useEffect(() => {
    if (displayPaneId && displayPaneId !== '' && displayPaneId !== 'undefined') {
      setVisitedPanes(prev => {
        if (prev.includes(displayPaneId)) return prev;
        return [...prev.filter(id => id && id !== '' && id !== 'undefined'), displayPaneId];
      });
    }
  }, [displayPaneId]);

  // Update document title
  useEffect(() => {
    document.title = displayPaneTitle && displayPaneTitle !== 'No pane selected'
      ? `${displayPaneTitle} - CiCy` : 'CiCy IDE';
  }, [displayPaneTitle]);

  // Init auth
  useEffect(() => {
    const init = async () => {
      const urlToken = new URLSearchParams(window.location.search).get('token');
      if (urlToken) {
        try {
          await apiService.verifyAuth(urlToken);
          TokenManager.saveToken(urlToken);
          setToken(urlToken);
        } catch {
          TokenManager.clearToken();
        }
        setIsCheckingAuth(false);
      } else {
        const savedToken = TokenManager.getToken();
        if (savedToken) {
          try {
            await apiService.verifyAuth(savedToken);
            setToken(savedToken);
          } catch {
            TokenManager.clearToken();
          }
        }
        setIsCheckingAuth(false);
      }

      // Load settings
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved) {
        try {
          const parsed = JSON.parse(saved);
          if (!parsed.commandHistory) parsed.commandHistory = [];
          setSettings({ ...DEFAULT_SETTINGS, ...parsed });
        } catch {}
      }
      setIsLoaded(true);
    };
    init();
  }, []);

  // Verify token & get perms
  useEffect(() => {
    if (!token) return;
    apiService.verifyAuth(token)
      .then(({ data }) => { if (data.perms) setUserPerms(data.perms); })
      .catch(() => {});
  }, [token]);

  // Persist settings
  useEffect(() => {
    if (isLoaded) localStorage.setItem(STORAGE_KEY, JSON.stringify(settings));
  }, [settings, isLoaded]);

  // Init main agent tab
  useEffect(() => {
    if (token && agentTabs.length === 0 && displayPaneId) {
      setAgentTabs([{ paneId: displayPaneId, title: displayPaneTitle, url: urls.ttyd(displayPaneId, token), closable: false }]);
    }
  }, [token, displayPaneId]);

  // Sync agent status from polling
  useEffect(() => {
    if (currentPane) {
      setAgentStatus(currentPane.status || 'idle');
      if (currentPane.contextUsage != null) setContextUsage(currentPane.contextUsage);
    }
  }, [currentPane]);

  // Network latency listener
  useEffect(() => {
    const handler = (e: CustomEvent) => {
      const { latency } = e.detail;
      if (latency === null) { setNetworkStatus('offline'); setNetworkLatency(null); }
      else { setNetworkLatency(latency); setNetworkStatus(latency < 100 ? 'excellent' : latency < 300 ? 'good' : 'poor'); }
    };
    window.addEventListener('network-latency', handler as EventListener);
    return () => window.removeEventListener('network-latency', handler as EventListener);
  }, []);

  // Toast auto-dismiss
  useEffect(() => {
    if (toast) { const t = setTimeout(() => setToast(null), 3000); return () => clearTimeout(t); }
  }, [toast]);

  // Listen for show-toast events
  useEffect(() => {
    const handler = (e: CustomEvent) => setToast(e.detail);
    window.addEventListener('show-toast', handler as EventListener);
    return () => window.removeEventListener('show-toast', handler as EventListener);
  }, []);

  // Operations
  const handleToggleMouse = useCallback(async () => {
    if (isTogglingMouse) return;
    setIsTogglingMouse(true);
    const newMode = mouseMode === 'on' ? 'off' : 'on';
    try { await apiService.toggleMouse(newMode, displayPaneId); setMouseMode(newMode); } catch {}
    setIsTogglingMouse(false);
  }, [isTogglingMouse, mouseMode, displayPaneId]);

  const handleCapturePane = useCallback(async (paneId?: string, lines?: number) => {
    if (isCapturing) return;
    setIsCapturing(true);
    try {
      const { data } = await apiService.capturePane(paneId || displayPaneId, lines);
      setCaptureOutput(data.output || '');
    } catch (e) { console.error(e); }
    finally { setIsCapturing(false); }
  }, [isCapturing, displayPaneId]);

  const handleRestart = useCallback(async (paneId?: string) => {
    const target = paneId || displayPaneId;
    if (!confirm(`Restart tmux and ttyd for ${target}?`)) return;
    setIsRestarting(true);
    try {
      const clean = target.replace(':main.0', '');
      await apiService.restartPane(clean);
      for (let i = 0; i < 30; i++) {
        await new Promise(r => setTimeout(r, 1000));
        try {
          const { data } = await apiService.getTtydStatus(clean);
          if (data.status === 'running') { setTimeout(() => location.reload(), 500); return; }
        } catch {}
      }
      setTimeout(() => location.reload(), 500);
    } catch (e) { console.error(e); alert('Restart failed'); }
    finally { setIsRestarting(false); }
  }, [displayPaneId]);

  const handlePanelChange = useCallback((pos: Position, size: Size) => {
    setSettings(prev => ({ ...prev, panelPosition: pos, panelSize: size }));
  }, []);

  const value: PaneContextType = {
    displayPaneId, displayPaneTitle,
    token, setToken, userPerms, isCheckingAuth, hasPermission,
    ttydWidth, setTtydWidth, isDragging, setIsDragging, isInteracting, setIsInteracting, commandPanelHeight,
    leftCollapsed, setLeftCollapsed: (v: boolean) => { setLeftCollapsed(v); localStorage.setItem('leftCollapsed', String(v)); },
    rightCollapsed, setRightCollapsed: (v: boolean) => { setRightCollapsed(v); localStorage.setItem('rightCollapsed', String(v)); },
    activeTab, setActiveTab, agentsSubTab, setAgentsSubTab, previewTab, setPreviewTab,
    agentStatus, contextUsage, mouseMode, readOnly, setReadOnly, isRestarting,
    agentTabs, setAgentTabs, activeAgentTab, setActiveAgentTab,
    visitedPanes,
    settings, setSettings, isLoaded,
    networkLatency, networkStatus, toast, setToast,
    handleRestart, handleCapturePane, handleToggleMouse, handlePanelChange,
    captureOutput, setCaptureOutput, isCapturing,
  };

  return <PaneContext.Provider value={value}>{children}</PaneContext.Provider>;
};

export const usePane = () => {
  const ctx = useContext(PaneContext);
  if (!ctx) throw new Error('usePane must be used within PaneProvider');
  return ctx;
};
