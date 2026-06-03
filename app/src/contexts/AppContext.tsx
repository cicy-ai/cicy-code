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

  // Active agent — what the user is currently focused on in the UI. May differ from
  // currentPaneId when the workspace's card stack has a non-master card focused.
  // Stored as short id (e.g. "w-10001"), not the ":main.0" form.
  activeAgentId: string | null;
  setActiveAgentId: (id: string | null) => void;
  // Cross-component cache of pane detail rows, keyed by short pane id. Inspector
  // saves push patches in via patchAgentDetail so any component reading via
  // useAgentDetail / activeAgentDetail sees the new value without re-fetching.
  agentDetails: Record<string, any>;
  setAgentDetail: (paneId: string, detail: any | null) => void;
  patchAgentDetail: (paneId: string, patch: any) => void;
  activeAgentDetail: any | null;
  // Serialize PATCHes per pane so two rapid clicks (e.g. ModelPicker → Inspector)
  // don't race and leave the server with stale state. Returns the wrapped fn's result.
  runPaneSaveSerially: <T>(paneId: string, fn: () => Promise<T>) => Promise<T>;

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
  // chatWsLiveText is intentionally NOT exposed: it changes on every streamed
  // token and has no render consumer, so threading it through the provider
  // value would re-render every useApp() consumer per token. It still lives in
  // the internal chatWsState (written via setChatWsState).
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
  const [activeAgentId, setActiveAgentIdState] = useState<string | null>(null);
  const [agentDetails, setAgentDetailsState] = useState<Record<string, any>>({});
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

  const login = useCallback((newToken: string) => authLogin(newToken), [authLogin]);

  const logout = useCallback(() => {
    authLogout();
    PaneManager.clearCurrentPane();
    setCurrentPaneId(null);
    setApi(null);
    setAgents([]);
    setSystemResources(null);
  }, [authLogout]);

  const selectPane = useCallback(async (paneId: string) => {
    PaneManager.setCurrentPane(paneId);
    setCurrentPaneId(paneId);
  }, []);

  const clearPane = useCallback(() => {
    PaneManager.clearCurrentPane();
    setCurrentPaneId(null);
  }, []);

  const updatePane = useCallback((paneId: string, updates: Partial<Agent>) => {
    setAllPanes(prev => prev.map(p =>
      p.pane_id === paneId ? { ...p, ...updates } : p
    ));
  }, []);

  const loadAgents = useCallback(async () => {
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
  }, [api, token]);

  const removeAgent = useCallback(async (paneId: string, agentId?: number) => {
    if (!api || !token) return;
    try {
      // Unbind if has agent ID
      if (agentId) {
        await api.unbindAgent(agentId);
      }
      await api.deleteAgent(paneId);
      // Update local state
      setAgents(prev => prev.filter(a => a.pane_id !== paneId));
      setError(null);
    } catch (err: any) {
      setError(err.message);
      throw err;
    }
  }, [api, token]);

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

  const normalizePaneIdShort = useCallback((id: string | null | undefined): string | null => {
    if (!id) return null;
    const trimmed = String(id).trim();
    if (!trimmed) return null;
    return trimmed.split(':')[0];
  }, []);

  const setActiveAgentId = useCallback((id: string | null) => {
    setActiveAgentIdState(normalizePaneIdShort(id));
  }, [normalizePaneIdShort]);

  const setAgentDetail = useCallback((paneId: string, detail: any | null) => {
    const key = normalizePaneIdShort(paneId);
    if (!key) return;
    setAgentDetailsState((prev) => {
      if (detail == null) {
        if (!(key in prev)) return prev;
        const { [key]: _, ...rest } = prev;
        return rest;
      }
      return { ...prev, [key]: detail };
    });
  }, [normalizePaneIdShort]);

  const patchAgentDetail = useCallback((paneId: string, patch: any) => {
    const key = normalizePaneIdShort(paneId);
    if (!key || !patch || typeof patch !== 'object') return;
    setAgentDetailsState((prev) => {
      const current = prev[key] || {};
      return { ...prev, [key]: { ...current, ...patch } };
    });
  }, [normalizePaneIdShort]);

  const activeAgentDetail = useMemo(() => {
    if (!activeAgentId) return null;
    return agentDetails[activeAgentId] || null;
  }, [activeAgentId, agentDetails]);

  // Per-pane save serialization. Any component that PATCHes pane settings
  // (Inspector / footer ModelPicker / card-title rename / ...) should wrap its
  // call so concurrent edits to the same pane never get applied out-of-order
  // on the server. We keep a small map of in-flight Promise chains keyed by
  // short paneId — each new save awaits the previous one before running.
  const paneSaveQueuesRef = useRef<Map<string, Promise<unknown>>>(new Map());
  const runPaneSaveSerially = useCallback(<T,>(paneId: string, fn: () => Promise<T>): Promise<T> => {
    const key = normalizePaneIdShort(paneId) || String(paneId || '').trim() || '__global__';
    const prev = paneSaveQueuesRef.current.get(key) || Promise.resolve();
    const next = prev.then(fn, fn);
    paneSaveQueuesRef.current.set(key, next.catch(() => undefined));
    return next as Promise<T>;
  }, [normalizePaneIdShort]);

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

  // Memoized so the provider value keeps a stable identity across renders that
  // don't change any consumed field. Critically, chatWsState.chatWsLiveText is
  // NOT a dependency: it changes on every streamed token but is not part of the
  // value, so a live conversation no longer re-renders every useApp() consumer
  // (Workspace, panels, ...) per token. Real changes (status, history/inspector
  // version, agents, settings) still flow through because they ARE deps.
  const currentPane = allPanes.find(p => p.pane_id === currentPaneId);
  const value = useMemo<AppContextType>(() => ({
    token,
    isAuthenticated: !!token,
    login,
    logout,
    currentPaneId,
    currentPane,
    paneDetail,
    setPaneDetail,
    selectPane,
    clearPane,
    activeAgentId,
    setActiveAgentId,
    agentDetails,
    setAgentDetail,
    patchAgentDetail,
    activeAgentDetail,
    runPaneSaveSerially,
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
  }), [
    token, login, logout, currentPaneId, currentPane, paneDetail, setPaneDetail,
    selectPane, clearPane, activeAgentId, setActiveAgentId, agentDetails,
    setAgentDetail, patchAgentDetail, activeAgentDetail, runPaneSaveSerially,
    api, agents, loadAgents, removeAgent, allPanes, updatePane, globalVar,
    agentTypeOptions, setGlobalVar, loadGlobalVar, updateGlobalVar, globalLoaded,
    globalLoadError,
    chatWsState.activeChatPaneId, chatWsState.chatWsConnected,
    chatWsState.chatWsClientId, chatWsState.chatWsLiveStatus,
    chatWsState.chatWsHistoryVersion, chatWsState.chatWsInspectorVersion,
    setChatWsState, setChatWsSender, sendChatWsMessage, subscribeChatWs,
    broadcastChatWsMessage, systemResources, setSystemResources, loading, error,
    setError,
  ]);

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
    loading,
    globalLoaded,
    isDev: !!globalVar?.dev,
    agentsCount: agents.length,
    activeAgentId,
    activeAgentDetail: activeAgentDetail
      ? {
          pane_id: activeAgentDetail.pane_id,
          title: activeAgentDetail.title,
          agent_type: activeAgentDetail.agent_type,
          default_model: activeAgentDetail.default_model,
          use_custom_gateway: activeAgentDetail.use_custom_gateway,
          runtime_ai: activeAgentDetail.runtime_ai,
          active: activeAgentDetail.active,
        }
      : null,
    agentDetailsKeys: Object.keys(agentDetails),
    systemResources,
  });

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
