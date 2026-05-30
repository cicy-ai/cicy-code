import axios from 'axios';
import config from '../config';
import { TokenManager } from './tokenManager';
import i18n from '../i18n';

const BACKEND_KEY = 'cicy_backend';

const http = axios.create({ baseURL: config.apiBase });
let pendingPanesRequest: Promise<any> | null = null;
const pendingPaneDetailRequests = new Map<string, Promise<any>>();

function shortPaneRouteId(id: string) {
  return String(id || '').replace(/:main\.0$/, '');
}

http.interceptors.request.use((cfg) => {
  const token = TokenManager.getToken();
  if (token) cfg.headers.Authorization = `Bearer ${token}`;
  // Forward the active UI language so backend / worker can localize
  // catalog text (skill description, title, etc). Falls back to 'en'.
  const lang = (i18n.resolvedLanguage || i18n.language || 'en').toString();
  cfg.headers['Accept-Language'] = lang;
  if (!config.isWorkspace) {
    const saved = localStorage.getItem(BACKEND_KEY);
    if (saved && cfg.url && !cfg.url.startsWith('/api/auth/')) {
      cfg.baseURL = saved;
    }
  }
  return cfg;
});

export function setBackend(url: string | null) {
  if (url) localStorage.setItem(BACKEND_KEY, url);
  else localStorage.removeItem(BACKEND_KEY);
}

function unwrapTmuxSend<T extends { success?: boolean; error?: string; detail?: string; pane_updated?: boolean; warning?: string; delivered?: boolean }>(promise: Promise<{ data: T }>) {
  return promise.then((resp) => {
    const errorText = String(resp.data?.error || resp.data?.detail || '').trim();
    const paneUpdated = !!resp.data?.pane_updated;
    if (errorText && !paneUpdated) {
      throw new Error(errorText);
    }
    if (resp.data?.success === false && !paneUpdated) {
      throw new Error('tmux send failed');
    }
    return resp;
  }).catch((err: any) => {
    // Axios throws on non-2xx. Backend returns 409 with pane_updated:true
    // whenever it actually pressed Enter (or had Enter pressed as the
    // forced fallback). That means the prompt is committed to the agent's
    // buffer regardless of whether the post-Enter confirmation timed out —
    // tmux can't "un-Enter." Treat any pane_updated:true response as
    // delivered, and surface the backend's detail string as a non-fatal
    // warning so the caller can show it alongside the success state.
    const data = err?.response?.data;
    if (data?.pane_updated === true) {
      const detail = String(data?.detail || data?.error || '').trim();
      return {
        data: {
          ...data,
          success: true,
          delivered: true,
          warning: detail || undefined,
        },
      } as { data: T };
    }
    throw err;
  });
}

const api = {
  verifyToken: (token?: string) => http.post('/api/auth/verify-token', token ? { token } : null, { baseURL: config.mgrBase }),
  verifyTokenAt: (baseUrl: string, token: string) => http.post('/api/auth/verify-token', { token }, { baseURL: baseUrl.replace(/\/$/, ''), timeout: 5000 }),
  verifyAuth: (token: string) => http.get('/api/auth/verify', { baseURL: config.isWorkspace ? config.apiBase : config.mgrBase, headers: { Authorization: `Bearer ${token}` } }),
  exchangeOAuthCode: (code: string) => http.get('/api/auth/exchange', { baseURL: config.mgrBase, params: { code } }),

  getRuntimeFlags: () => http.get('/api/runtime/flags'),

  getPanes: () => {
    if (pendingPanesRequest) return pendingPanesRequest;
    pendingPanesRequest = http.get('/api/tmux/panes').finally(() => {
      pendingPanesRequest = null;
    });
    return pendingPanesRequest;
  },
  poll: (paneId?: string, cfg?: any) => http.get('/api/poll', { ...cfg, params: { ...(cfg?.params || {}), ...(paneId ? { pane_id: paneId } : {}) } }),
  getAllStatus: (cfg?: any) => http.get('/api/tmux/status', cfg),
  getPane: (id: string) => {
    const routeId = shortPaneRouteId(id);
    const cached = pendingPaneDetailRequests.get(routeId);
    if (cached) return cached;
    const request = http.get(`/api/tmux/panes/${encodeURIComponent(routeId)}`).finally(() => {
      pendingPaneDetailRequests.delete(routeId);
    });
    pendingPaneDetailRequests.set(routeId, request);
    return request;
  },
  updatePane: (id: string, data: any) => http.patch(`/api/tmux/panes/${encodeURIComponent(id)}`, data),
  deletePane: (id: string) => http.delete(`/api/tmux/panes/${encodeURIComponent(id)}`),
  createPane: (data: any) => http.post('/api/tmux/create', data),
  forkPane: (data: { source_pane_id: string; title?: string; master_pane_id?: string }) => http.post('/api/tmux/fork', data),
  restartPane: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/restart`),
  capturePane: (id: string, lines = 100) => http.post('/api/tmux/capture_pane', { pane_id: id, lines }),

  // MITM CA install status (whether this node's CA is trusted in the OS store).
  // Used by the inspector to nudge non-gateway codex/kiro users to install it.
  getMitmCaStatus: () => http.get('/api/mitm/ca-status'),

  sendCommand: (winId: string, text: string, submit = true) => unwrapTmuxSend(http.post('/api/tmux/send', { win_id: winId, text, submit })),
  sendKeys: (winId: string, keys: string) => unwrapTmuxSend(http.post('/api/tmux/send-keys', { win_id: winId, keys })),
  toggleMouse: (mode: string, paneId: string) => http.post(`/api/tmux/mouse/${mode}`, null, { params: { pane_id: paneId } }),
  chooseSession: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/choose-session`),
  splitPane: (id: string, dir: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/split`, null, { params: { direction: dir } }),
  unsplitPane: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/unsplit`),

  deleteAgent: (id: string) => http.delete(`/api/agents/${encodeURIComponent(id)}`),
  getAgentsByPane: (id: string) => http.get(`/api/agents/pane/${encodeURIComponent(id)}`),
  getAgentInspector: (id: string, params?: { q?: string; limit?: number; offset?: number }) => http.get(`/api/agents/inspector/${encodeURIComponent(id)}`, { params }),
  getAgentHistoryIDs: (id: string, params?: { conversation_id?: string }) => http.get(`/api/agents/history-ids/${encodeURIComponent(id)}`, { params }),
  getAgentCurrentHistory: (id: string, params?: { limit?: number; before?: number; conversation_id?: string }) => http.get(`/api/agents/current-history/${encodeURIComponent(id)}`, { params }),
  getAgentCurrentHistoryTool: (id: string, params: { history_id?: number; step_index: number; tool_index: number; live?: 1 }) => http.get(`/api/agents/current-history-tool/${encodeURIComponent(id)}`, { params }),
  getAgentHistorySync: (id: string, params?: { cursor?: string; limit?: number }) => http.get(`/api/agents/history-sync/${encodeURIComponent(id)}`, { params }),
  getAgentHistoryView: (id: string) => http.get(`/api/agents/history-view/${encodeURIComponent(id)}`),
  updateAgentInspectorNotes: (id: string, content: string) => http.put(`/api/agents/inspector/${encodeURIComponent(id)}/notes`, { content }),
  updateAgentRuntimeMemory: (id: string, data: { content: string; enabled: boolean }) => http.put(`/api/agents/inspector/${encodeURIComponent(id)}/runtime-memory`, data),
  updateAgentPromptRules: (id: string, data: any) => http.put(`/api/agents/inspector/${encodeURIComponent(id)}/prompt-rules`, data),
  bindAgent: (data: any) => http.post('/api/agents/bind', data),
  unbindAgent: (agentId: number) => http.delete(`/api/agents/unbind/${agentId}`),
  reorderAgents: (paneId: string, agentNames: string[]) => http.post('/api/agents/reorder', { pane_id: paneId, agent_names: agentNames }),

  registerMachine: (data: any) => http.post('/api/machines/register', data),
  syncMachines: (data?: any) => http.post('/api/machines/sync', data || {}),
  getMachinePanes: (id: number | string) => http.get(`/api/machines/${id}/panes`),

  getSkills: () => http.get('/api/skills'),
  runSkill: (data: any) => http.post('/api/skills/run', data),

  listMarketSkills: (params?: { category?: string; installed?: boolean; q?: string }) =>
    http.get('/api/skill-market', { params }),
  getMarketSkill: (name: string) => http.get(`/api/skill-market/${encodeURIComponent(name)}`),
  installMarketSkill: (name: string, opts?: { force?: boolean }) =>
    http.post(`/api/skill-market/${encodeURIComponent(name)}/install`, opts || {}),
  uninstallMarketSkill: (name: string, opts?: { purge_config?: boolean }) =>
    http.post(`/api/skill-market/${encodeURIComponent(name)}/uninstall`, opts || {}),
  updateMarketSkill: (name: string) =>
    http.post(`/api/skill-market/${encodeURIComponent(name)}/update`, {}),
  ejectMarketSkill: (name: string) =>
    http.post(`/api/skill-market/${encodeURIComponent(name)}/eject`, {}),

  getGoogleSkillConfig: () => http.get('/api/skill-config/google'),
  connectGoogleSkillConfig: () => http.post('/api/skill-config/google/connect', {}),
  deviceConnectGoogleSkillConfig: () => http.post('/api/skill-config/google/device-connect', {}),
  devicePollGoogleSkillConfig: (state: string) => http.post('/api/skill-config/google/device-poll', { state }),
  disconnectGoogleSkillConfig: () => http.delete('/api/skill-config/google'),

  getTtydStatus: (id: string) => http.get(`/api/tmux/ttyd/status/${encodeURIComponent(id)}`),
  correctEnglish: (text: string, target = 'zh-CN') => http.post('/api/correctEnglish', { text, target }),
  translateText: (text: string, target = 'zh-CN') => http.post('/api/utils/translateText', { text, target }),
  fileExists: (path: string) => http.get('/api/utils/file/exists', { params: { path } }),
  stt: (formData: FormData) => http.post('/stt', formData, { baseURL: config.sttBase, headers: { 'Content-Type': 'multipart/form-data' } }),

  getGlobalSettings: (token?: string) => http.get('/api/settings/global', token ? { headers: { Authorization: `Bearer ${token}` } } : undefined),
  updateGlobalSettings: (data: any) => http.post('/api/settings/global', data),
  getOpenClawGateway: () => http.get('/api/openclaw/gateway'),

  getProviders: () => http.get('/api/providers'),
  createProvider: (data: any) => http.post('/api/providers', data),
  updateProvider: (key: string, data: any) => http.patch(`/api/providers/${encodeURIComponent(key)}`, data),
  deleteProvider: (key: string) => http.delete(`/api/providers/${encodeURIComponent(key)}`),
  updateProviderDefaults: (defaults: Record<string, string>) => http.put('/api/providers/defaults', { default: defaults }),
  testProvider: (data: any) => http.post('/api/providers/test', data),

  // IM (Telegram / WeChat) — restored 2026-05-18
  getIMPlatforms: () => http.get('/api/im/platforms'),
  getIMAccounts: () => http.get('/api/im/accounts'),
  getIMAccount: (id: number) => http.get(`/api/im/accounts/${id}`),
  createIMAccount: (data: any) => http.post('/api/im/accounts', data),
  updateIMAccount: (id: number, data: any) => http.patch(`/api/im/accounts/${id}`, data),
  deleteIMAccount: (id: number) => http.delete(`/api/im/accounts/${id}`),
  testIMAccount: (id: number) => http.post(`/api/im/accounts/${id}/test`),
  bindIMAccount: (id: number, paneId: string) => http.post(`/api/im/accounts/${id}/bind`, { pane_id: paneId }),
  unbindIMAccount: (id: number) => http.post(`/api/im/accounts/${id}/unbind`),
  getIMAccountQR: (id: number) => http.get(`/api/im/accounts/${id}/qr`),
  reloginIMAccount: (id: number) => http.post(`/api/im/accounts/${id}/relogin`),
  startWeChatLogin: () => http.post('/api/im/wechat/login'),
  getWeChatLoginStatus: (sessionId: string) => http.get(`/api/im/wechat/login/${encodeURIComponent(sessionId)}`),
  cancelWeChatLogin: (sessionId: string) => http.post(`/api/im/wechat/login/${encodeURIComponent(sessionId)}/cancel`),

  getTokens: () => http.get('/api/auth/tokens'),
  createToken: (data: any) => http.post('/api/auth/tokens', data),
  deleteToken: (id: number) => http.delete(`/api/auth/tokens/${id}`),

  listGroups: () => http.get('/api/groups'),

  getTrafficStats: (pane: string, minutes = 60, interval = 1) => http.get(`/api/stats/traffic?pane=${pane}&minutes=${minutes}&interval=${interval}`),
  getTrafficRaw: (pane: string) => http.get(`/api/stats/traffic/raw?pane=${pane}`),
  getChatHistory: (pane: string) => http.get(`/api/stats/chat?pane=${pane}`),
  getChatStatsByAgent: (agentId: string) => http.get('/api/stats/chat', { params: { agent_id: agentId } }),
  getSystemResources: (cfg?: any) => http.get('/api/system/resources', cfg),

  getCicyFiles: (pane: string) => http.get(`/api/cicy/files?pane=${pane}`),
  getCicyFile: (pane: string, name: string) => http.get(`/api/cicy/file?pane=${pane}&name=${name}`, { transformResponse: [(d: any) => d] }),

  getPair: (pane: string) => http.get(`/api/tmux/pair?pane=${pane}`),

  getQueue: (pane: string, workflowId?: number | string) => http.get('/api/workers/queue', { params: { pane, workflow_id: workflowId } }),
  pushQueue: (data: any) => http.post('/api/workers/queue', data),
  updateQueueItem: (id: number, data: any) => http.patch(`/api/workers/queue/${id}`, data),
  deleteQueueItem: (id: number) => http.delete(`/api/workers/queue/${id}`),

  listWindows: (session: string) => http.get(`/api/tmux/windows?session=${session}`),
  createWindow: (session: string, name?: string) => http.post('/api/tmux/windows', { session, name }),
  renameWindow: (session: string, index: string, name: string) => http.patch('/api/tmux/windows', { session, index, name }),
  deleteWindow: (session: string, index: string) => http.delete('/api/tmux/windows', { data: { session, index } }),
  selectWindow: (session: string, index: string) => http.put('/api/tmux/windows', { session, index }),

  getAuditDashboard: (user: string, days = 7) => http.get(`/api/audit/dashboard?user=${user}&days=${days}`),
  getAuditUsage: (user: string, limit = 100) => http.get(`/api/audit/usage?user=${user}&limit=${limit}`),
  getAuditAdminOverview: () => http.get('/api/audit/admin/overview'),
  getAuditStatus: () => http.get('/api/audit/status'),
  registerAuditToken: (userId: string, plan = 'free') => http.post('/api/audit/register', { user_id: userId, plan }),
  getSetupGuide: () => http.get('/setup'),

  // audit-v2 — autonomous policy agent
  auditAutonomyDecisions: (limit = 100) => http.get(`/api/audit/decisions?limit=${limit}`),
  auditAutonomyRunNow: () => http.post('/api/audit/decisions/run'),
  auditAutonomyExplain: (id: string) =>
    http.post(`/api/audit/decisions/explain/${encodeURIComponent(id)}`),
  auditAutonomyRevert: (id: string) =>
    http.post(`/api/audit/decisions/revert/${encodeURIComponent(id)}`),

  // desktop "apps"
  getApps: () => http.get('/api/apps'),
  createApp: (prompt: string) => http.post('/api/apps/create', { prompt }),
  aiChat: (messages: Array<{ role: string; content: string }>) => http.post('/api/ai/chat/stream', { messages }),

  // page <-> page client bridge
  chatPush: (data: any) => http.post('/api/chat/push', data),

  // mihomo controller pass-through + lifecycle
  getProxyList: () => http.get('/api/proxy/list'),
  testProxy: (name: string, urls?: string[]) => http.post('/api/proxy/test', { name, urls }),
  getProxyStatus: () => http.get('/api/proxy/status'),
  proxyLifecycle: (action: 'start' | 'stop' | 'restart' | 'reload') => http.post('/api/proxy/lifecycle', { action }),
  getProxyBindMode: () => http.get('/api/proxy/bind-mode'),
  setProxyBindMode: (allowLan: boolean) => http.patch('/api/proxy/bind-mode', { allow_lan: allowLan }),
  getProxyExport: (params?: { ip?: 'local' | 'lan' | 'public' | string; user?: string }) => http.get('/api/proxy/export', { params }),
  // 🌍 global-proxy panel: exit-IP comparison (direct vs via mihomo) + the switch
  getProxyExitInfo: () => http.get('/api/proxy/exit-info'),
  selectProxy: (member: string, group?: string) => http.post('/api/proxy/select', { member, group }),
  listProxySsh: () => http.get('/api/proxy-ssh/list'),
  showProxySsh: (name: string) => http.get('/api/proxy-ssh/show', { params: { name } }),
  proxySshLifecycle: (name: string, action: 'start' | 'stop' | 'restart') => http.post('/api/proxy-ssh/lifecycle', { name, action }),
  testProxySsh: (name: string) => http.post('/api/proxy-ssh/test', { name }),
  getFrpServerStatus: () => http.get('/api/frp-server/status'),
  frpServerLifecycle: (action: 'start' | 'stop' | 'restart' | 'reload') => http.post('/api/frp-server/lifecycle', { action }),
  getFrpServerConnections: () => http.get('/api/frp-server/connections'),
  getFrpServerClients: () => http.get('/api/frp-server/clients'),
  getFrpServerLogs: (lines?: number) => http.get('/api/frp-server/logs', { params: lines ? { lines } : {} }),
  getFrpServerInstallInfo: () => http.get('/api/frp-server/install-info'),

  // webpage ws-client management
  getChatClients: () => http.get('/api/chat/clients'),
  pingChatClient: (clientId: string) => http.post('/api/chat/ping-client', { client_id: clientId }),

  listTodos: (paneId: string, allAgents?: boolean, params?: { status?: string; q?: string }) => {
    const pid = shortPaneRouteId(paneId);
    if (!pid) return Promise.reject(new Error('paneId required for listTodos'));
    return http.get('/api/todo/list', {
      headers: { 'X-Agent-Show-Id': pid },
      params: { pane_id: paneId, all_agents: allAgents ? 'true' : undefined, ...(params || {}) },
    });
  },
  getTodoCounts: (paneId: string) => {
    const pid = shortPaneRouteId(paneId);
    if (!pid) return Promise.reject(new Error('paneId required for getTodoCounts'));
    return http.get('/api/todo/counts', {
      headers: { 'X-Agent-Show-Id': pid },
      params: { pane_id: paneId },
    });
  },
  addTodo: (paneId: string, title: string, creatorId?: string) => {
    const pid = shortPaneRouteId(paneId);
    if (!pid) return Promise.reject(new Error('paneId required for addTodo'));
    return http.post('/api/todo/add', { pane_id: paneId, title, creator_id: creatorId }, {
      headers: { 'X-Agent-Show-Id': pid },
    });
  },
  updateTodo: (paneId: string, id: string, patch: { status?: string; title?: string }) => {
    const pid = shortPaneRouteId(paneId);
    if (!pid) return Promise.reject(new Error('paneId required for updateTodo'));
    return http.patch(`/api/todo/${encodeURIComponent(id)}`, { pane_id: paneId, ...patch }, {
      headers: { 'X-Agent-Show-Id': pid },
    });
  },
  deleteTodo: (paneId: string, id: string) => {
    const pid = shortPaneRouteId(paneId);
    if (!pid) return Promise.reject(new Error('paneId required for deleteTodo'));
    return http.delete(`/api/todo/${encodeURIComponent(id)}`, {
      headers: { 'X-Agent-Show-Id': pid },
      params: { pane_id: paneId },
    });
  },
};

export default api;
