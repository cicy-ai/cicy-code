import React, { createContext, useContext, useState, useEffect, useCallback, ReactNode, useRef, useMemo } from 'react';
import { useDevRegister } from '../lib/devStore';
import { AgentTypeOption, AGENT_TYPE_OPTIONS } from '../lib/agentType';
import { PaneManager } from '../services/paneManager';
import apiService from '../services/api';
import config from '../config';
import { useAuth } from './AuthContext';

const APP_VERSION = config.version;
const URL_PANE_ID = (() => {
  const m = window.location.hash.match(/[#/]agent\/([^/?#]+)/);
  return m ? decodeURIComponent(m[1]) : null;
})();

interface Agent {
  pane_id: string;
  status?: string;
  title?: string;
  id?: number;
  [key: string]: any;
}

interface ChatWsState {
  activeChatPaneId: string | null;
  chatWsConnected: boolean;
  chatWsClientId: string | null;
  chatWsLiveStatus: string;
  chatWsLiveText: string;
  chatWsHistoryVersion: number;
  chatWsInspectorVersion: number;
}

interface ChatWsMessage {
  type?: string;
  data?: any;
  [key: string]: any;
}

export interface SystemResourceSnapshot {
  cpu_usage_pct: number;
  cpu_cores: number;
  mem_usage_pct: number;
  mem_total_bytes: number;
  mem_used_bytes: number;
  disk_usage_pct: number;
  disk_total_bytes: number;
  disk_used_bytes: number;
  load_1: number;
  load_5: number;
  load_15: number;
  updated_at: string;
}

interface AppContextType {
  // Auth
  token: string | null;
  isAuthenticated: boolean;
  login: (token: string) => Promise<boolean>;
  logout: () => void;

  // Pane Selection
  currentPaneId: string | null;
  currentPane: Agent | undefined;
  paneDetail: any | null;
  setPaneDetail: (detail: any) => void;
  selectPane: (paneId: string) => void;
  clearPane: () => void;

  // API Client
  api: typeof apiService | null;

  // Agents
  agents: Agent[];
  loadAgents: () => Promise<void>;
  removeAgent: (paneId: string, agentId?: number) => Promise<void>;
  
  // All Panes
  allPanes: Agent[];
  updatePane: (paneId: string, updates: Partial<Agent>) => void;
  
  // Global Settings
  globalVar: any;
  agentTypeOptions: AgentTypeOption[];
  setGlobalVar: React.Dispatch<React.SetStateAction<any>>;
  loadGlobalVar: () => Promise<void>;
  globalLoaded: boolean;
  globalLoadError: string | null;
  isDev: boolean;
  updateGlobalVar: (data: any) => Promise<void>;
  
  // Shared chat websocket
  activeChatPaneId: string | null;
  chatWsConnected: boolean;
  chatWsClientId: string | null;
  chatWsLiveStatus: string;
  chatWsLiveText: string;
  chatWsHistoryVersion: number;
  chatWsInspectorVersion: number;
  setChatWsState: (next: Partial<ChatWsState>) => void;
  setChatWsSender: (sender: (payload: unknown) => boolean) => void;
  sendChatWsMessage: (payload: unknown) => boolean;
  subscribeChatWs: (listener: (msg: ChatWsMessage) => void) => () => void;
  broadcastChatWsMessage: (msg: ChatWsMessage) => void;
  systemResources: SystemResourceSnapshot | null;
  setSystemResources: (snapshot: SystemResourceSnapshot | null) => void;

  // UI State
  loading: boolean;
  error: string | null;
  setError: (error: string | null) => void;
}

const AppContext = createContext<AppContextType | undefined>(undefined);

export const AppProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const { token, isChecking, login: authLogin, logout: authLogout } = useAuth();
  const [currentPaneId, setCurrentPaneId] = useState<string | null>(() => {
    return PaneManager.getCurrentPane() || URL_PANE_ID;
  });
  const [paneDetail, setPaneDetail] = useState<any | null>(null);
  const [api, setApi] = useState<typeof apiService | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [allPanes, setAllPanes] = useState<Agent[]>([]);
  const [globalVar, setGlobalVar] = useState<any>({});
  const [globalLoaded, setGlobalLoaded] = useState(false);
  const [globalLoadError, setGlobalLoadError] = useState<string | null>(null);
  const [systemResources, setSystemResources] = useState<SystemResourceSnapshot | null>(null);
  const agentTypeOptions = useMemo<AgentTypeOption[]>(() => {
    const raw = Array.isArray(globalVar?.agents) ? globalVar.agents : [];
    const mapped = raw
      .map((item: any) => ({
        value: String(item?.value || '').trim(),
        label: String(item?.label || item?.value || '').trim(),
        description: typeof item?.description === 'string' ? item.description : undefined,
      }))
      .filter((item) => item.value && item.label);
    return mapped.length ? mapped : AGENT_TYPE_OPTIONS;
  }, [globalVar]);
  const [chatWsState, setChatWsStateValue] = useState<ChatWsState>({
    activeChatPaneId: null,
    chatWsConnected: false,
    chatWsClientId: null,
    chatWsLiveStatus: 'idle',
    chatWsLiveText: '',
    chatWsHistoryVersion: 0,
    chatWsInspectorVersion: 0,
  });
  const chatWsSendRef = useRef<(payload: unknown) => boolean>(() => false);
  const chatWsListenersRef = useRef(new Set<(msg: ChatWsMessage) => void>());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Keep API client lifecycle aligned with AuthContext to avoid unauthenticated
  // requests racing ahead of token verification.
  useEffect(() => {
    const cachedPane = PaneManager.getCurrentPane();

    if (cachedPane) {
      setCurrentPaneId(cachedPane);
    }

    if (isChecking) {
      return;
    }

    if (token) {
      setApi(apiService);
      setLoading(false);
      return;
    }

    setApi(null);
    setAllPanes([]);
    setPaneDetail(null);
    setGlobalVar({});
    setGlobalLoaded(false);
    setGlobalLoadError(null);
    setAgents([]);
    setError(null);
    setLoading(false);
  }, [isChecking, token]);

  useEffect(() => {
    if (!isChecking && !token) {
      setLoading(false);
    }
  }, [isChecking, token]);

  // Load global settings when api is ready
  useEffect(() => {
    if (!isChecking && api && token) {
      loadGlobalVar();
    }
  }, [api, isChecking, token]);
  // Auto-load paneDetail when currentPaneId changes
  useEffect(() => {
    if (isChecking || !api || !token || !currentPaneId) { setPaneDetail(null); return; }
    api.getPane(currentPaneId).then(({ data }) => setPaneDetail(data)).catch(() => setPaneDetail(null));
  }, [api, currentPaneId, isChecking, token]);

  const login = (newToken: string) => authLogin(newToken);

  const logout = () => {
    authLogout();
    PaneManager.clearCurrentPane();
    setCurrentPaneId(null);
    setApi(null);
    setAgents([]);
    setSystemResources(null);
  };

  const selectPane = async (paneId: string) => {
    PaneManager.setCurrentPane(paneId);
    setCurrentPaneId(paneId);
  };

  const clearPane = () => {
    PaneManager.clearCurrentPane();
    setCurrentPaneId(null);
  };

  const updatePane = (paneId: string, updates: Partial<Agent>) => {
    setAllPanes(prev => prev.map(p => 
      p.pane_id === paneId ? { ...p, ...updates } : p
    ));
  };

  const loadAgents = async () => {
    if (!api || !token) return;
    try {
      setLoading(true);
      const { data } = await api.getPanes();
      setAgents(Array.isArray(data) ? data : data?.panes || []);
      setError(null);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const removeAgent = async (paneId: string, agentId?: number) => {
    if (!api || !token) return;
    try {
      // Unbind if has agent ID
      if (agentId) {
        await api.unbindAgent(agentId);
      }
      await api.deleteAgent(paneId);
      // Update local state
      setAgents(agents.filter(a => a.pane_id !== paneId));
      setError(null);
    } catch (err: any) {
      setError(err.message);
      throw err;
    }
  };

  const setChatWsState = useCallback((next: Partial<ChatWsState>) => {
    setChatWsStateValue(prev => ({ ...prev, ...next }));
  }, []);

  const setChatWsSender = useCallback((sender: (payload: unknown) => boolean) => {
    chatWsSendRef.current = sender;
  }, []);

  const sendChatWsMessage = useCallback((payload: unknown) => {
    return chatWsSendRef.current(payload);
  }, []);

  const subscribeChatWs = useCallback((listener: (msg: ChatWsMessage) => void) => {
    chatWsListenersRef.current.add(listener);
    return () => {
      chatWsListenersRef.current.delete(listener);
    };
  }, []);

  const broadcastChatWsMessage = useCallback((msg: ChatWsMessage) => {
    for (const listener of chatWsListenersRef.current) {
      try {
        listener(msg);
      } catch {}
    }
  }, []);

  const loadGlobalVar = useCallback(async () => {
    if (!api || !token) return;
    try {
      const { data } = await apiService.getGlobalSettings();
      setGlobalVar(data);
      setGlobalLoaded(true);
      setGlobalLoadError(null);
    } catch (err: any) {
      console.error('Failed to load global settings:', err);
      setGlobalLoadError(err?.message || 'failed');
    }
  }, [api, token]);

  const updateGlobalVar = useCallback(async (data: any) => {
    if (!api || !token) return;
    try {
      await apiService.updateGlobalSettings(data);
      const { data: fresh } = await apiService.getGlobalSettings();
      setGlobalVar(fresh);
    } catch (err: any) {
      console.error('Failed to update global settings:', err);
      throw err;
    }
  }, [api, token]);

  const value: AppContextType = {
    token,
    isAuthenticated: !!token,
    login,
    logout,
    currentPaneId,
    currentPane: allPanes.find(p => p.pane_id === currentPaneId),
    paneDetail,
    setPaneDetail,
    selectPane,
    clearPane,
    api,
    agents,
    loadAgents,
    removeAgent,
    allPanes,
    updatePane,
    globalVar,
    agentTypeOptions,
    setGlobalVar,
    loadGlobalVar,
    updateGlobalVar,
    globalLoaded,
    globalLoadError,
    isDev: !!globalVar?.dev,
    activeChatPaneId: chatWsState.activeChatPaneId,
    chatWsConnected: chatWsState.chatWsConnected,
    chatWsClientId: chatWsState.chatWsClientId,
    chatWsLiveStatus: chatWsState.chatWsLiveStatus,
    chatWsLiveText: chatWsState.chatWsLiveText,
    chatWsHistoryVersion: chatWsState.chatWsHistoryVersion,
    chatWsInspectorVersion: chatWsState.chatWsInspectorVersion,
    setChatWsState,
    setChatWsSender,
    sendChatWsMessage,
    subscribeChatWs,
    broadcastChatWsMessage,
    systemResources,
    setSystemResources,
    loading,
    error,
    setError,
  };

  // Debug: Log context changes
  React.useEffect(() => {
    console.debug('[AppContext] State updated:', {
      currentPaneId,
      currentPane: allPanes.find(p => p.pane_id === currentPaneId),
      allPanesCount: allPanes.length,
      allPanes: allPanes.map(p => ({ pane_id: p.pane_id, title: p.title }))
    });
  }, [currentPaneId, allPanes]);

  useDevRegister('App', {
    currentPaneId, allPanesCount: allPanes.length, loading, error,
    paneDetail,
    agents: agents.map(a => ({ id: a.pane_id, title: a.title, status: a.status })),
    agentsCount: agents.length,
    allPanes: allPanes.map(p => ({ pane_id: p.pane_id, title: p.title, status: p.status })),
    globalVar,
    globalLoaded,
    isDev: !!globalVar?.dev,
    agentTypeOptions,
    systemResources,
  }, { currentPaneId: (v: string) => selectPane(v), error: setError });

  return (
    <AppContext.Provider value={value}>
      {children}
    </AppContext.Provider>
  );
};

export const useApp = () => {
  const context = useContext(AppContext);
  if (!context) {
    throw new Error('useApp must be used within AppProvider');
  }
  
  // Expose to window for debugging
  if (typeof window !== 'undefined') {
    (window as any).__APP_CONTEXT__ = { ...context, version: APP_VERSION };
  }
  
  return context;
};
