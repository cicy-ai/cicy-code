import axios from 'axios';
import config from '../config';
import { TokenManager } from './tokenManager';

const BACKEND_KEY = 'cicy_backend';

const http = axios.create({ baseURL: config.apiBase });

http.interceptors.request.use((cfg) => {
  const token = TokenManager.getToken();
  if (token) cfg.headers.Authorization = `Bearer ${token}`;
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

export function getBackend(): string {
  return localStorage.getItem(BACKEND_KEY) || config.apiBase;
}

const api = {
  verifyToken: (token?: string) => http.post('/api/auth/verify-token', token ? { token } : null, { baseURL: config.mgrBase }),
  verifyAuth: (token: string) => http.get('/api/auth/verify', { baseURL: config.isWorkspace ? config.apiBase : config.mgrBase, headers: { Authorization: `Bearer ${token}` } }),

  getPanes: () => http.get('/api/tmux/panes'),
  getAllStatus: (cfg?: any) => http.get('/api/tmux/status', cfg),
  getPane: (id: string) => http.get(`/api/tmux/panes/${encodeURIComponent(id)}`),
  updatePane: (id: string, data: any) => http.patch(`/api/tmux/panes/${encodeURIComponent(id)}`, data),
  deletePane: (id: string) => http.delete(`/api/tmux/panes/${encodeURIComponent(id)}`),
  createPane: (data: any) => http.post('/api/tmux/create', data),
  restartPane: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/restart`),
  capturePane: (id: string, lines = 100) => http.post('/api/tmux/capture_pane', { pane_id: id, lines }),

  sendCommand: (winId: string, text: string) => http.post('/api/tmux/send', { win_id: winId, text }),
  sendKeys: (winId: string, keys: string) => http.post('/api/tmux/send-keys', { win_id: winId, keys }),
  toggleMouse: (mode: string, paneId: string) => http.post(`/api/tmux/mouse/${mode}`, null, { params: { pane_id: paneId } }),
  chooseSession: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/choose-session`),
  splitPane: (id: string, dir: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/split`, null, { params: { direction: dir } }),
  unsplitPane: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/unsplit`),

  deleteAgent: (id: string) => http.delete(`/api/agents/${encodeURIComponent(id)}`),
  getAgentsByPane: (id: string) => http.get(`/api/agents/pane/${encodeURIComponent(id)}`),
  bindAgent: (data: any) => http.post('/api/agents/bind', data),
  unbindAgent: (agentId: number) => http.delete(`/api/agents/unbind/${agentId}`),

  getMachines: () => http.get('/api/machines'),
  registerMachine: (data: any) => http.post('/api/machines/register', data),
  syncMachines: (data?: any) => http.post('/api/machines/sync', data || {}),
  getMachinePanes: (id: number | string) => http.get(`/api/machines/${id}/panes`),

  getSkills: () => http.get('/api/skills'),
  runSkill: (data: any) => http.post('/api/skills/run', data),

  getCollabSteps: (params?: any) => http.get('/api/collab/steps', { params }),
  createCollabStep: (data: any) => http.post('/api/collab/steps', data),
  updateCollabStep: (id: number, data: any) => http.patch(`/api/collab/steps/${id}`, data),
  createWorkflow: (data: any) => http.post('/api/collab/workflows', data),
  getWorkflow: (id: number | string) => http.get(`/api/collab/workflows/${id}`),

  getTtydStatus: (id: string) => http.get(`/api/tmux/ttyd/status/${encodeURIComponent(id)}`),
  correctEnglish: (text: string) => http.post('/api/correctEnglish', { text }),
  fileExists: (path: string) => http.get('/api/utils/file/exists', { params: { path } }),
  stt: (formData: FormData) => http.post('/stt', formData, { baseURL: config.sttBase, headers: { 'Content-Type': 'multipart/form-data' } }),

  getGlobalSettings: () => http.get('/api/settings/global'),
  updateGlobalSettings: (data: any) => http.post('/api/settings/global', data),

  getTokens: () => http.get('/api/auth/tokens'),
  createToken: (data: any) => http.post('/api/auth/tokens', data),
  deleteToken: (id: number) => http.delete(`/api/auth/tokens/${id}`),

  listGroups: () => http.get('/api/groups'),

  getTrafficStats: (pane: string, minutes = 60, interval = 1) => http.get(`/api/stats/traffic?pane=${pane}&minutes=${minutes}&interval=${interval}`),
  getTrafficRaw: (pane: string) => http.get(`/api/stats/traffic/raw?pane=${pane}`),
  getChatHistory: (pane: string) => http.get(`/api/stats/chat?pane=${pane}`),

  getCicyFiles: (pane: string) => http.get(`/api/cicy/files?pane=${pane}`),
  getCicyFile: (pane: string, name: string) => http.get(`/api/cicy/file?pane=${pane}&name=${name}`, { transformResponse: [(d: any) => d] }),

  getPair: (pane: string) => http.get(`/api/tmux/pair?pane=${pane}`),

  getQueue: (pane: string, workflowId?: number | string) => http.get('/api/workers/queue', { params: { pane, workflow_id: workflowId } }),
  pushQueue: (data: any) => http.post('/api/workers/queue', data),
  updateQueueItem: (id: number, data: any) => http.patch(`/api/workers/queue/${id}`, data),
  deleteQueueItem: (id: number) => http.delete(`/api/workers/queue/${id}`),

  getPaneList: () => http.get('/api/tmux/panes'),

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
};

export default api;
