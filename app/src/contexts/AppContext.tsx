import React, { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react';
import { useDevRegister } from '../lib/devStore';
import { TokenManager } from '../services/tokenManager';
import { PaneManager } from '../services/paneManager';
import apiService from '../services/api';
import config from '../config';

const APP_VERSION = config.version;
const URL_PANE_ID = window.location.href.split("/")[4] ? decodeURIComponent(window.location.href.split("/")[4]) : null;

interface Agent {
  pane_id: string;
  status?: string;
  title?: string;
  id?: number;
  [key: string]: any;
}

interface AppContextType {
  // Auth
  token: string | null;
  isAuthenticated: boolean;
  login: (token: string) => void;
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

  // 智能体
  agents: Agent[];
  load智能体: () => Promise<void>;
  removeAgent: (paneId: string, agentId?: number) => Promise<void>;
  
  // All Panes
  allPanes: Agent[];
  updatePane: (paneId: string, updates: Partial<Agent>) => void;
  
  // Global Settings
  globalVar: any;
  loadGlobalVar: () => Promise<void>;
  updateGlobalVar: (data: any) => Promise<void>;
  
  // UI State
  loading: boolean;
  error: string | null;
  setError: (error: string | null) => void;
}

const AppContext = createContext<AppContextType | undefined>(undefined);

export const AppProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
  const [token, setToken] = useState<string | null>(null);
  const [currentPaneId, setCurrentPaneId] = useState<string | null>(() => {
    return PaneManager.getCurrentPane() || URL_PANE_ID;
  });
  const [paneDetail, setPaneDetail] = useState<any | null>(null);
  const [api, setApi] = useState<typeof apiService | null>(null);
  const [agents, set智能体] = useState<Agent[]>([]);
  const [allPanes, setAllPanes] = useState<Agent[]>([]);
  const [globalVar, setGlobalVar] = useState<any>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Initialize token, pane, and API client
  useEffect(() => {
    const cachedToken = TokenManager.init();
    const cachedPane = PaneManager.getCurrentPane();
    
    if (cachedToken) {
      setToken(cachedToken);
      setApi(apiService);
    }
    
    if (cachedPane) {
      setCurrentPaneId(cachedPane);
    }
    
    // If no token, stop loading immediately; otherwise wait for fetchAllPanes
    if (!cachedToken) {
      setLoading(false);
    }
  }, []);

  // Load global settings when api is ready
  useEffect(() => {
    if (api) {
      loadGlobalVar();
    }
  }, [api]);
  useEffect(() => {
    if (!api) return;
    const fetchAllPanes = async () => {
      try {
        const panesRes = await api.getPanes();
        mergePanes(panesRes.data?.panes || []);
      } catch (err) {
        console.error('获取窗格失败：', err);
        setLoading(false);
      }
    };
    const mergePanes = (panes: any[]) => {
      const panesArray = panes.map((p: any) => ({ ...p, pane_id: p.pane_id, active: p.active }));
      if (panesArray.length === 0) return;
      setAllPanes(prev => {
        const prevJson = JSON.stringify(prev);
        const nextJson = JSON.stringify(panesArray);
        return prevJson === nextJson ? prev : panesArray;
      });
      setLoading(false);
      if (panesArray.length > 0 && !currentPaneId && !PaneManager.getCurrentPane()) {
        const firstPane = panesArray[0];
        PaneManager.setCurrentPane(firstPane.pane_id);
        setCurrentPaneId(firstPane.pane_id);
      }
    };
    fetchAllPanes();
    const onRefresh = () => { fetchAllPanes(); };
    window.addEventListener('refresh-panes', onRefresh);
    const onVisible = () => { if (document.visibilityState === 'visible') fetchAllPanes(); };
    document.addEventListener('visibilitychange', onVisible);
    return () => { window.removeEventListener('refresh-panes', onRefresh); document.removeEventListener('visibilitychange', onVisible); };
  }, [api]);

  // Auto-load paneDetail when currentPaneId changes
  useEffect(() => {
    if (!api || !currentPaneId) { setPaneDetail(null); return; }
    api.getPane(currentPaneId).then(({ data }) => setPaneDetail(data)).catch(() => setPaneDetail(null));
  }, [api, currentPaneId]);

  const login = (newToken: string) => {
    TokenManager.saveToken(newToken);
    setToken(newToken);
    setApi(apiService);
  };

  const logout = () => {
    TokenManager.clearToken();
    PaneManager.clearCurrentPane();
    setToken(null);
    setCurrentPaneId(null);
    setApi(null);
    set智能体([]);
  };

  const selectPane = async (paneId: string) => {
    PaneManager.setCurrentPane(paneId);
    setCurrentPaneId(paneId);
    window.dispatchEvent(new CustomEvent('refresh-panes'));
    
    // Fetch detailed pane config
    if (api) {
      try {
        const { data: detail } = await api.getPane(paneId);
        setPaneDetail(detail);
      } catch (err) {
        console.error('获取窗格详情失败：', err);
        setPaneDetail(null);
      }
    }
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

  const load智能体 = async () => {
    if (!api) return;
    try {
      setLoading(true);
      const { data } = await api.getPanes();
      set智能体(Array.isArray(data) ? data : data?.panes || []);
      setError(null);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  const removeAgent = async (paneId: string, agentId?: number) => {
    if (!api) return;
    try {
      // Unbind if has agent ID
      if (agentId) {
        await api.unbindAgent(agentId);
      }
      await api.deleteAgent(paneId);
      // Update local state
      set智能体(agents.filter(a => a.pane_id !== paneId));
      setError(null);
    } catch (err: any) {
      setError(err.message);
      throw err;
    }
  };

  const loadGlobalVar = useCallback(async () => {
    if (!api) return;
    try {
      const { data } = await apiService.getGlobalSettings();
      setGlobalVar(data);
    } catch (err: any) {
      console.error('加载全局设置失败：', err);
    }
  }, [api]);

  const updateGlobalVar = useCallback(async (data: any) => {
    if (!api) return;
    try {
      await apiService.updateGlobalSettings(data);
      setGlobalVar(data);
    } catch (err: any) {
      console.error('更新全局设置失败：', err);
      throw err;
    }
  }, [api]);

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
    load智能体,
    removeAgent,
    allPanes,
    updatePane,
    globalVar,
    loadGlobalVar,
    updateGlobalVar,
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
