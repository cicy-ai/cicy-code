// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import axios from 'axios';
import config from '../config';
import { TokenManager } from './tokenManager';
import i18n from '../i18n';

const BACKEND_KEY = 'cicy_backend';

const http = axios.create({ baseURL: config.apiBase });
let pendingPanesRequest: Promise<any> | null = null;
const pendingPaneDetailRequests = new Map<string, Promise<any>>();
// MemoryView mounts two effects that both read the agent's own template on
// open (one for the filename label, one for the content) — without coalescing
// that's two identical GET /api/memory/templates/agent/:id requests. Share the
// in-flight promise so concurrent identical reads collapse to one round-trip.
const pendingMemoryTemplateRequests = new Map<string, Promise<any>>();

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
  openSystemNotificationSettings: () => http.post('/api/desktop/open-notification-settings', {}),
  getKouboStatus: (paneId: string) => http.get('/api/koubo/status', { params: { pane_id: paneId } }),
  startOpenKoubo: (paneId: string) => http.post('/api/koubo/start-open', { pane_id: paneId }),
  deletePane: (id: string) => http.delete(`/api/tmux/panes/${encodeURIComponent(id)}`),
  createPane: (data: any) => http.post('/api/tmux/create', data),
  forkPane: (data: { source_pane_id: string; title?: string; master_pane_id?: string; prompt?: string }) => http.post('/api/tmux/fork', data),
  // Read-only preview for the fork-confirm modal: regenerates the source's
  // summary and returns current.json / reply.json / summary content + token use
  // + compression ratio + the default inherit prompt. Does NOT create a pane.
  forkPreview: (data: { source_pane_id: string }) => http.post('/api/tmux/fork/preview', data),
  restartPane: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/restart`),
  capturePane: (id: string, lines = 100) => http.post('/api/tmux/capture_pane', { pane_id: id, lines }),

  // MITM CA install status (whether this node's CA is trusted in the OS store).
  // Used by the inspector to nudge non-gateway codex/kiro users to install it.
  getMitmCaStatus: () => http.get('/api/mitm/ca-status'),

  // Projects (first-class: name + rules). Same-project claude agents share memory.
  listProjects: () => http.get('/api/projects'),
  createProject: (name: string, rules?: string) =>
    http.post('/api/projects', { name, rules }),
  // Memory templates (global + project) — backs the create-agent dialog.
  listMemoryTemplates: () => http.get('/api/memory/templates'),
  getMemoryTemplate: (scope: string, name?: string) => {
    const url = `/api/memory/templates/${scope}${name ? `/${encodeURIComponent(name)}` : ''}`;
    const cached = pendingMemoryTemplateRequests.get(url);
    if (cached) return cached;
    const request = http.get(url).finally(() => {
      pendingMemoryTemplateRequests.delete(url);
    });
    pendingMemoryTemplateRequests.set(url, request);
    return request;
  },
  saveMemoryTemplate: (scope: string, name: string, content: string) =>
    http.put(`/api/memory/templates/${scope}${name ? `/${encodeURIComponent(name)}` : ''}`, { content }),
  deleteMemoryTemplate: (scope: string, name: string) =>
    http.delete(`/api/memory/templates/${scope}/${encodeURIComponent(name)}`),

  // Custom agents (user-authored cicy personas, ~/cicy-ai/agents/<slug>/AGENT.md)
  listCustomAgents: () => http.get('/api/custom-agents'),
  saveCustomAgent: (data: { name: string; tools: string[]; model?: string; body: string }) =>
    http.post('/api/custom-agents', data),
  deleteCustomAgent: (slug: string) =>
    http.delete(`/api/custom-agents/${encodeURIComponent(slug)}`),

  listAgentRoleMarket: (q?: string) =>
    http.get('/api/agent-role-market', { params: q ? { q } : {} }),
  installAgentRole: (slug: string) =>
    http.post(`/api/agent-role-market/${encodeURIComponent(slug)}/install`, {}),
  updateAgentRole: (slug: string) =>
    http.post(`/api/agent-role-market/${encodeURIComponent(slug)}/update`, {}),

  sendCommand: (winId: string, text: string, submit = true) => unwrapTmuxSend(http.post('/api/tmux/send', { win_id: winId, text, submit })),
  sendKeys: (winId: string, keys: string) => unwrapTmuxSend(http.post('/api/tmux/send-keys', { win_id: winId, keys })),
  // 打断 headless cicy 正在跑的 turn(它没有 tmux pane,send-keys 够不着,走专用端点)。
  cancelCicyReply: (paneId: string) => http.post('/api/cicy/cancel', { pane_id: paneId }),
  // 重跑 headless cicy 最近一次取消/失败的 turn(聊天里「重试」按钮)。
  retryCicyReply: (paneId: string) => http.post('/api/cicy/retry', { pane_id: paneId }),
  toggleMouse: (mode: string, paneId: string) => http.post(`/api/tmux/mouse/${mode}`, null, { params: { pane_id: paneId } }),
  chooseSession: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/choose-session`),
  splitPane: (id: string, dir: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/split`, null, { params: { direction: dir } }),
  unsplitPane: (id: string) => http.post(`/api/tmux/panes/${encodeURIComponent(id)}/unsplit`),

  deleteAgent: (id: string) => http.delete(`/api/agents/${encodeURIComponent(id)}`),
  getAgentsByPane: (id: string) => http.get(`/api/agents/pane/${encodeURIComponent(id)}`),
  getAgentInspector: (id: string, params?: { q?: string; limit?: number; offset?: number }) => http.get(`/api/agents/inspector/${encodeURIComponent(id)}`, { params }),
  getAgentUsageLog: (id: string, limit = 200) => http.get(`/api/agents/usage-log/${encodeURIComponent(id)}`, { params: { limit } }),
  getAgentUsageAnalysis: (id: string) => http.get(`/api/agents/usage-analysis/${encodeURIComponent(id)}`),
  getAgentUsageBlock: (id: string, idx: number) => http.get(`/api/agents/usage-block/${encodeURIComponent(id)}`, { params: { idx } }),
  getAgentHistoryIDs: (id: string, params?: { conversation_id?: string }) => http.get(`/api/agents/history-ids/${encodeURIComponent(id)}`, { params }),
  getAgentCurrentReply: (id: string, params?: { conversation_id?: string }) => http.get(`/api/agents/current-reply/${encodeURIComponent(id)}`, { params }),
  // Lite header metrics for many agents in ONE request — the dual-channel
  // fallback when the chat WS push is down/stale, so the team panel never fans
  // out N× /current-reply. Returns { success, metrics: { id: {status,model,…} } }.
  getAgentCurrentReplyBatch: (ids: string[]) => http.get('/api/agents/current-reply-batch', { params: { ids: ids.join(',') } }),
  getAgentGreeting: (id: string) => http.get(`/api/agents/greeting/${encodeURIComponent(id)}`),
  getInstallStatus: (id: string) => http.get('/api/agents/install-status', { params: { agent_id: id } }),
  getAgentCurrentHistory: (id: string, params?: { limit?: number; before?: number; conversation_id?: string }) => http.get(`/api/agents/current-history/${encodeURIComponent(id)}`, { params }),
  getAgentCurrentHistoryTool: (id: string, params: { history_id?: number; step_index: number; tool_index: number; live?: 1 }) => http.get(`/api/agents/current-history-tool/${encodeURIComponent(id)}`, { params }),
  getAgentHistoryView: (id: string) => http.get(`/api/agents/history-view/${encodeURIComponent(id)}`),
  updateAgentInspectorNotes: (id: string, content: string) => http.put(`/api/agents/inspector/${encodeURIComponent(id)}/notes`, { content }),
  bindAgent: (data: any) => http.post('/api/agents/bind', data),
  unbindAgent: (agentId: number) => http.delete(`/api/agents/unbind/${agentId}`),
  reorderAgents: (paneId: string, agentNames: string[]) => http.post('/api/agents/reorder', { pane_id: paneId, agent_names: agentNames }),
  reparentAgent: (paneId: string, newParent: string, contextPaneId: string) => http.post('/api/agents/reparent', { pane_id: paneId, new_parent: newParent, context_pane_id: contextPaneId }),

  registerMachine: (data: any) => http.post('/api/machines/register', data),
  syncMachines: (data?: any) => http.post('/api/machines/sync', data || {}),
  getMachinePanes: (id: number | string) => http.get(`/api/machines/${id}/panes`),

  // {current, latest, has_update} — is a newer cicy-code published on npm (cached).
  getCicyUpdateStatus: () => http.get('/api/cicy-update'),
  // Trigger an in-place update to the latest version (server restarts itself).
  applyCicyUpdate: () => http.post('/api/cicy-update', {}),

  getSkills: () => http.get('/api/skills'),
  // Locally installed cicy-skills only (name+version) — fast, no remote registry.
  // Use this for install checks instead of listMarketSkills() (~2s remote).
  getInstalledSkills: () => http.get('/api/skills/installed'),
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

  // Private registry sources (~/cicy-ai/registries.json)
  listSkillRegistries: () => http.get('/api/skill-registries'),
  addSkillRegistry: (data: { name?: string; url: string; token?: string }) =>
    http.post('/api/skill-registries', data),
  removeSkillRegistry: (nameOrUrl: string) =>
    http.delete(`/api/skill-registries/${encodeURIComponent(nameOrUrl)}`),

  // The node's always-on self-hosted registry (mounted at :8008/registry).
  getLocalRegistry: () => http.get('/api/local-registry'),
  rotateLocalRegistry: () => http.post('/api/local-registry/rotate', {}),
  publishLocalRegistry: (name: string) =>
    http.post('/api/local-registry/publish', { name }),
  unpublishLocalRegistry: (name: string) =>
    http.post('/api/local-registry/unpublish', { name }),

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

  // 上传聊天附件(图片/PDF/文档)到 agent 工作区,返回 {ok, file:{Name,Size,ContentType,
  // IsImage,URL,Path,FileRef}}。onProgress 驱动 UI 上传进度条。沿用现成的 /assets/files。
  uploadAssetFile: (pane: string, file: File, onProgress?: (loaded: number, total: number) => void) => {
    const form = new FormData();
    form.append('file', file);
    return http.post(`/assets/files?pane=${encodeURIComponent(pane)}`, form, {
      onUploadProgress: (evt) => { if (onProgress && evt.total) onProgress(evt.loaded, evt.total); },
    });
  },

  getGlobalSettings: (token?: string) => http.get('/api/settings/global', token ? { headers: { Authorization: `Bearer ${token}` } } : undefined),
  updateGlobalSettings: (data: any) => http.post('/api/settings/global', data),
  // Settings → General: email (SMTP) config + API token show / rotate-and-email.
  getEmailConfig: () => http.get('/api/settings/email'),
  saveEmailConfig: (cfg: any) => http.post('/api/settings/email', cfg),
  getApiToken: () => http.get('/api/settings/token'),
  refreshApiToken: (body?: { to?: string }) => http.post('/api/settings/token/refresh', body || {}),

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
  syncFeishuAccountName: (id: number) => http.post(`/api/im/accounts/${id}/sync-name`),
  getFeishuChatBindings: (paneId: string) => http.get('/api/im/chat-bindings', { params: { pane_id: paneId } }),
  createFeishuChat: (id: number, paneId: string, mode: 'direct' | 'group' = 'group') => http.post(`/api/im/accounts/${id}/create-chat`, { pane_id: paneId, mode }),
  unbindFeishuChat: (accountId: number, chatId: string, paneId: string) => http.delete('/api/im/chat-bindings', { params: { account_id: accountId, chat_id: chatId, pane_id: paneId } }),
  bindIMAccount: (id: number, paneId: string) => http.post(`/api/im/accounts/${id}/bind`, { pane_id: paneId }),
  unbindIMAccount: (id: number) => http.post(`/api/im/accounts/${id}/unbind`),
  getIMAccountSecret: (id: number) => http.get(`/api/im/accounts/${id}/secret`),
  getIMAccountQR: (id: number) => http.get(`/api/im/accounts/${id}/qr`),
  reloginIMAccount: (id: number) => http.post(`/api/im/accounts/${id}/relogin`),
  startWeChatLogin: () => http.post('/api/im/wechat/login'),
  getWeChatLoginStatus: (sessionId: string) => http.get(`/api/im/wechat/login/${encodeURIComponent(sessionId)}`),
  cancelWeChatLogin: (sessionId: string) => http.post(`/api/im/wechat/login/${encodeURIComponent(sessionId)}/cancel`),
  startCiCyCloudLogin: (email: string, team: string) => http.post('/api/im/cicy-cloud/login', { email, team }),
  getCiCyCloudLoginStatus: (state: string) => http.get(`/api/im/cicy-cloud/login/${encodeURIComponent(state)}`),
  getCiCyCloudInstances: () => http.get('/api/im/cicy-cloud/instances'),
  enableCiCyCloudTunnel: () => http.post('/api/im/cicy-cloud/tunnel'),
  getPublishedPorts: () => http.get('/api/ports'),
  savePublishedPort: (port: number, name: string, visibility: 'private' | 'public') =>
    http.post('/api/ports', { port, name, visibility }),
  deletePublishedPort: (port: number) => http.delete('/api/ports', { params: { port } }),
  getCiCyCloudAgents: () => http.get('/api/im/cicy-cloud/agents'),
  sendCiCyCloudMessage: (targetInstanceId: string, targetAgentId: string, senderAgentId: string, text: string) => http.post('/api/im/cicy-cloud/send', {
    target_instance_id: targetInstanceId, target_agent_id: targetAgentId,
    sender_agent_id: senderAgentId, text,
  }),

  getTokens: () => http.get('/api/auth/tokens'),
  createToken: (data: any) => http.post('/api/auth/tokens', data),
  deleteToken: (id: number) => http.delete(`/api/auth/tokens/${id}`),

  listGroups: () => http.get('/api/groups'),
  getGroup: (id: number | string) => http.get(`/api/groups/${encodeURIComponent(String(id))}`),
  createGroup: (data: { name: string; description?: string }) => http.post('/api/groups', data),
  updateGroup: (id: number | string, data: { name?: string; description?: string; is_pinned?: boolean; project_rules?: string }) => http.patch(`/api/groups/${encodeURIComponent(String(id))}`, data),
  deleteGroup: (id: number | string) => http.delete(`/api/groups/${encodeURIComponent(String(id))}`),
  addGroupPane: (id: number | string, paneId: string) => http.post(`/api/groups/${encodeURIComponent(String(id))}/panes/${encodeURIComponent(paneId)}`),
  removeGroupPane: (id: number | string, paneId: string) => http.delete(`/api/groups/${encodeURIComponent(String(id))}/panes/${encodeURIComponent(paneId)}`),
  updateGroupPaneLayout: (id: number | string, paneId: string, data: { pos_x: number; pos_y: number; width?: number; height?: number; z_index?: number }) => http.patch(`/api/groups/${encodeURIComponent(String(id))}/panes/${encodeURIComponent(paneId)}/layout`, data),
  getSystemResources: (cfg?: any) => http.get('/api/system/resources', cfg),
  getCrontab: () => http.get('/api/crontab'),
  saveCrontab: (content: string) => http.put('/api/crontab', { content }),

  getCicyFiles: (pane: string) => http.get(`/api/cicy/files?pane=${pane}`),
  getCicyFile: (pane: string, name: string) => http.get(`/api/cicy/file?pane=${pane}&name=${name}`, { transformResponse: [(d: any) => d] }),

  getPair: (pane: string) => http.get(`/api/tmux/pair?pane=${pane}`),

  getQueue: (pane: string, workflowId?: number | string) => http.get('/api/workers/queue', { params: { pane, workflow_id: workflowId } }),
  pushQueue: (data: any) => http.post('/api/workers/queue', data),
  updateQueueItem: (id: number, data: any) => http.patch(`/api/workers/queue/${id}`, data),
  deleteQueueItem: (id: number) => http.delete(`/api/workers/queue/${id}`),

  // codex-on-gateway panes: the model token the terminal must mask out of the
  // PTY stream ("" for everything else). Consumed by TerminalView.
  getTtydMaskModel: (paneId: string) => http.get(`/api/tmux/ttyd/mask/${paneId}`),
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

  // audit-v2 — local audit events (findings + decision/action) for the guard widget
  getAuditEvents: (params: { agent_id?: string; severity?: string; rule_id?: string; from?: string; to?: string; limit?: number; offset?: number } = {}) => {
    const q = new URLSearchParams();
    Object.entries(params).forEach(([k, v]) => {
      if (v !== undefined && v !== null && String(v) !== '') q.set(k, String(v));
    });
    const qs = q.toString();
    return http.get(`/api/audit/events${qs ? `?${qs}` : ''}`);
  },
  // audit-v2 — the effective policy.json (authored by the w-6001 SecOps agent)
  getAuditPolicy: () => http.get('/api/audit/policy'),
  saveAuditPolicy: (policy: any) => http.post('/api/audit/policy', policy),
  getAuditRules: () => http.get('/api/audit/rules'),
  testAuditRule: (body: { match_type: string; pattern: string; text: string }) => http.post('/api/audit/rules/test', body),
  // audit-v2 — adjudicate one alert (real leak vs false positive)
  auditTriage: (input: Record<string, any>) => http.post('/api/audit/triage', input),
  // audit-v2 — fetch the snapshot archived for an alert
  getAuditSnapshot: (ref: string) => http.get('/api/audit/snapshot', { params: { ref } }),
  // audit-v2 — mark a content SHA as false-positive → add to allow_list.content_hashes
  allowlistContent: (sha256: string, reason: string) => http.post('/api/audit/allowlist/content', { sha256, reason }),
  // audit-v2 — view / manage the allow_list (content_hashes / paths / agents)
  getAllowlist: () => http.get('/api/audit/allowlist'),
  removeAllowlist: (category: 'content_hash' | 'path' | 'agent', value: string) =>
    http.delete('/api/audit/allowlist', { data: { category, value } }),

  // audit-v2 — autonomous policy agent
  auditAutonomyDecisions: (limit = 100) => http.get(`/api/audit/decisions?limit=${limit}`),
  auditAutonomyRunNow: () => http.post('/api/audit/decisions/run'),
  auditAutonomyExplain: (id: string) =>
    http.post(`/api/audit/decisions/explain/${encodeURIComponent(id)}`),
  auditAutonomyRevert: (id: string) =>
    http.post(`/api/audit/decisions/revert/${encodeURIComponent(id)}`),

  // desktop "apps"
  getApps: () => http.get('/api/apps'),

  getGithubAccounts: () => http.get('/api/github/accounts'),
  saveGithubAccount: (body: { name: string; old_name?: string; email?: string; api_token?: string; "2fa"?: string; profile?: string; password?: string }) => http.put('/api/github/accounts', body),
  deleteGithubAccount: (name: string) => http.delete('/api/github/accounts', { params: { name } }),
  testGithubAccount: (name: string) => http.post('/api/github/accounts/test', { name }),
  getGithubAccountTOTP: (name: string) => http.post('/api/github/accounts/totp', { name }),
  getGithubAccountUsage: (name: string) => http.post('/api/github/accounts/usage', { name }),
  getGithubAccountToken: (name: string) => http.get('/api/github/accounts', { params: { reveal_token: name } }),
  getCloudflareAccounts: () => http.get('/api/cloudflare/accounts'),
  saveCloudflareAccount: (body: { name: string; old_name?: string; label?: string; account_id?: string; api_token?: string; username?: string; email?: string; password?: string; profile?: string; is_default?: boolean; details?: Record<string, string> }) => http.put('/api/cloudflare/accounts', body),
  deleteCloudflareAccount: (name: string) => http.delete('/api/cloudflare/accounts', { params: { name } }),
  testCloudflareAccount: (name: string) => http.post('/api/cloudflare/accounts/test', { name }),
  getCloudflareAccountToken: (name: string) => http.get('/api/cloudflare/accounts', { params: { reveal_token: name } }),
  getGoogleAccounts: () => http.get('/api/google/accounts'),
  getGoogleAccountSecrets: (profile: string) => http.get('/api/google/accounts', { params: { reveal_secrets: profile } }),
  saveGoogleAccount: (body: { profile: string; email?: string; password?: string; "2fa"?: string; mobile?: string; recovery_email?: string }) => http.put('/api/google/accounts', body),
  getGoogleAccountTOTP: (profile: string) => http.post('/api/google/accounts/totp', { profile }),
  getChatGPTAccounts: () => http.get('/api/chatgpt/accounts'),
  getChatGPTAccountSecrets: (name: string) => http.get('/api/chatgpt/accounts', { params: { reveal_secrets: name } }),
  saveChatGPTAccount: (body: { name: string; old_name?: string; email?: string; password?: string; mobile?: string; "2fa"?: string; profile?: string }) => http.put('/api/chatgpt/accounts', body),
  deleteChatGPTAccount: (name: string) => http.delete('/api/chatgpt/accounts', { params: { name } }),
  getChatGPTAccountTOTP: (name: string) => http.post('/api/chatgpt/accounts/totp', { name }),
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
  getProxyNodeConfig: (name: string) => http.get('/api/proxy/node-config', { params: { name } }),
  // Provider balance / per-model availability for the model picker. Cached server-side.
  getProviderBalance: (provider: string) => http.get('/api/ai-gateway/provider-balance', { params: { provider } }),
  resetProxyConfig: () => http.post('/api/proxy/config/reset'),
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

  // desktop snapshots (periodic win/mac/linux screen captures)
  getDesktopSnapshots: (clientId: string) => http.get('/api/desktop/snapshots', { params: { client_id: clientId } }),
  desktopSnapshotNow: (clientId: string) => http.post('/api/desktop/snapshot-now', { client_id: clientId }),
  desktopSnapshotImageUrl: (clientId: string, name: string): string => {
    const token = TokenManager.getToken() || '';
    const base = (config.apiBase || '').replace(/\/$/, '');
    const qs = new URLSearchParams({ client_id: clientId, name, token }).toString();
    return `${base}/api/desktop/snapshot-image?${qs}`;
  },

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
  // Team knowledge store (file-backed). list filters by status/tag/q/domain.
  listKnowledge: (params?: { status?: string; tag?: string; q?: string; domain?: string; view?: string }) =>
    http.get('/api/knowledge', { params: params || {} }),
  getKnowledge: (id: string) => http.get(`/api/knowledge/${encodeURIComponent(id)}`),
  getKnowledgeConfig: (revealToken = false) => http.get('/api/knowledge/config', { params: revealToken ? { reveal_token: '1' } : {} }),
  saveKnowledgeConfig: (body: { origin: string; token?: string; clear_token?: boolean }) =>
    http.post('/api/knowledge/config', body),
  addTodo: (paneId: string, title: string, creatorId?: string) => {
    const pid = shortPaneRouteId(paneId);
    if (!pid) return Promise.reject(new Error('paneId required for addTodo'));
    return http.post('/api/todo/add', { pane_id: paneId, title, creator_id: creatorId }, {
      headers: { 'X-Agent-Show-Id': pid },
    });
  },
  updateTodo: (paneId: string, id: string, patch: { status?: string; title?: string; assignee?: string }) => {
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
