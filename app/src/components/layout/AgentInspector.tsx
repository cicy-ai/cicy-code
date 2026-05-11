import { Children, cloneElement, isValidElement, type ReactNode, useEffect, useMemo, useRef, useState } from 'react';
import { Brain, Search, Send, Settings } from 'lucide-react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import config from '../../config';
import apiService from '../../services/api';
import { TokenManager } from '../../services/tokenManager';
import type { EditPaneData } from '../EditPaneDialog';
import AgentAvatar from '../AgentAvatar';
import Select, { type SelectOption } from '../ui/Select';
import { normalizeAgentType } from '../../lib/agentType';

export type InspectorTab = 'overview' | 'memory' | 'settings' | 'history';
type InspectorRequestedTab = InspectorTab | 'notes' | 'history';
type PromptRuleKey = 'global' | 'project' | 'agent';
type RuntimeAIProviderOption = {
  key: string;
  label: string;
  protocol?: string;
  models?: string[];
};
type RuntimeAIDefaultSummary = {
  provider_name?: string;
  provider_label?: string;
  model?: string;
};
type PromptRuleDraft = {
  content: string;
  enabled: boolean;
  inject_on_request: boolean;
  updated_at?: string;
  available?: boolean;
  label?: string;
  key?: string;
};
type PromptRulesDraft = Record<PromptRuleKey, PromptRuleDraft>;

const HISTORY_PAGE_SIZE = 30;
const PROMPT_RULE_KEYS: PromptRuleKey[] = ['global', 'project', 'agent'];

const tabs: Array<{ id: InspectorTab; label: string }> = [
  { id: 'overview', label: '概览' },
  { id: 'history', label: 'History' },
  { id: 'memory', label: '运行时记忆' },
  { id: 'settings', label: '设置' },
];

const settingsSections = [
  { id: 'general', label: '常规', icon: Settings },
  { id: 'model', label: '模型', icon: Brain },
  { id: 'telegram', label: 'Telegram', icon: Send },
] as const;

type SettingsSectionId = typeof settingsSections[number]['id'];

const memorySections = [
  { id: 'global', label: 'Global' },
  { id: 'agent', label: 'Agent' },
  { id: 'preview', label: '预览' },
] as const;

type MemorySectionId = typeof memorySections[number]['id'];

function formatTime(value?: string) {
  if (!value) return '';
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleString('zh-CN', { hour12: false });
}

function compactText(value?: string, fallback = '暂无', limit?: number) {
  const text = String(value || '').trim();
  if (!text) return fallback;
  if (typeof limit === 'number' && limit > 0) {
    const chars = Array.from(text);
    if (chars.length > limit) return `${chars.slice(0, limit).join('')}...`;
  }
  return text;
}

function serializeGeneralSettings(value: EditPaneData | null) {
  return JSON.stringify({
    title: String(value?.title || ''),
    active: value?.active !== false,
    allow_all_actions: !!value?.allow_all_actions,
    use_proxy: !!value?.use_proxy,
    proxy: {
      password: String(value?.proxy?.password || ''),
      rule: String(value?.proxy?.rule || ''),
    },
  });
}

function serializeModelSettings(value: EditPaneData | null) {
  return JSON.stringify({
    default_model: String(value?.default_model || ''),
    use_official_auth: !!value?.use_official_auth,
    runtime_ai: value?.use_official_auth ? null : (value?.runtime_ai && String(value.runtime_ai.provider_name || '').trim()
      ? { provider_name: String(value.runtime_ai.provider_name || '').trim() }
      : null),
  });
}

function formatCredit(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--';
  if (value === 0) return '0';
  if (Math.abs(value) < 0.01) return value.toFixed(4);
  return value.toFixed(2);
}

function formatTokens(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--';
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value);
}

function formatCostEstimate(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--';
  return `$${formatCredit(value)}`;
}

function formatPercent(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--';
  return `${value}%`;
}

function formatStatusLabel(value?: string) {
  const text = String(value || '').trim();
  if (!text) return '空闲';
  const parts = text.split(/\s+/).filter(Boolean);
  if (parts.length >= 2) {
    const chinese = parts.find((part) => /[\u4e00-\u9fff]/.test(part));
    if (chinese) return chinese;
  }
  return text;
}

function formatProviderLabel(value?: string) {
  const text = String(value || '').trim();
  if (!text) return '';
  return text;
}

function pickUsageValue(current?: number, cumulative?: number) {
  if (typeof current === 'number' && Number.isFinite(current) && current > 0) return current;
  if (typeof cumulative === 'number' && Number.isFinite(cumulative) && cumulative > 0) return cumulative;
  if (typeof current === 'number' && Number.isFinite(current) && current === 0) return current;
  if (typeof cumulative === 'number' && Number.isFinite(cumulative) && cumulative === 0) return cumulative;
  return undefined;
}

function createEmptyPromptRuleDraft(): PromptRulesDraft {
  return {
    global: { content: '', enabled: false, inject_on_request: false, updated_at: '', available: true, label: '全局', key: 'global' },
    project: { content: '', enabled: false, inject_on_request: false, updated_at: '', available: false, label: '项目', key: '' },
    agent: { content: '', enabled: false, inject_on_request: false, updated_at: '', available: true, label: 'Agent', key: '' },
  };
}

function normalizePromptRuleDraft(raw: any, fallback: Partial<PromptRuleDraft> = {}): PromptRuleDraft {
  return {
    content: String(raw?.content || fallback.content || ''),
    enabled: !!(raw?.enabled ?? fallback.enabled),
    inject_on_request: !!(raw?.inject_on_request ?? fallback.inject_on_request),
    updated_at: String(raw?.updated_at || fallback.updated_at || ''),
    available: raw?.available ?? fallback.available ?? true,
    label: String(raw?.label || fallback.label || ''),
    key: String(raw?.key || fallback.key || ''),
  };
}

function buildPromptRulesDraft(data: any, paneId: string): PromptRulesDraft {
  const base = createEmptyPromptRuleDraft();
  const promptRules = data?.prompt_rules || {};
  const runtimeMemory = data?.runtime_memory || {};
  return {
    global: normalizePromptRuleDraft(promptRules.global, base.global),
    project: normalizePromptRuleDraft(promptRules.project, {
      ...base.project,
      label: promptRules?.project?.label || '项目',
      key: promptRules?.project_key || promptRules?.project?.key || '',
      available: !!(promptRules?.project?.available),
    }),
    agent: normalizePromptRuleDraft(promptRules.agent, {
      ...base.agent,
      content: runtimeMemory?.content || '',
      enabled: !!runtimeMemory?.enabled,
      inject_on_request: promptRules?.agent?.inject_on_request ?? !!runtimeMemory?.enabled,
      updated_at: runtimeMemory?.updated_at || '',
      label: promptRules?.agent?.label || paneId,
      key: promptRules?.agent?.key || paneId,
      available: true,
    }),
  };
}

function serializePromptRulesDraft(value: PromptRulesDraft) {
  return JSON.stringify(PROMPT_RULE_KEYS.map((key) => ({
    key,
    content: value[key]?.content || '',
    enabled: !!value[key]?.enabled,
    inject_on_request: !!value[key]?.inject_on_request,
    available: value[key]?.available !== false,
  })));
}

function normalizeHistoryText(value?: string) {
  return String(value || '').trim().replace(/\r\n/g, '\n');
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function highlightTextNode(text: string, query: string) {
  const keyword = String(query || '').trim();
  if (!keyword) return text;
  const regex = new RegExp(`(${escapeRegExp(keyword)})`, 'ig');
  const parts = text.split(regex);
  if (parts.length <= 1) return text;
  return parts.map((part, index) => (
    index % 2 === 1
      ? <mark key={`${part}-${index}`} className="inspector-history-hit">{part}</mark>
      : part
  ));
}

function highlightReactNode(node: ReactNode, query: string): ReactNode {
  const keyword = String(query || '').trim();
  if (!keyword) return node;
  if (typeof node === 'string') {
    return highlightTextNode(node, keyword);
  }
  if (typeof node === 'number' || typeof node === 'boolean' || node == null) {
    return node;
  }
  if (Array.isArray(node)) {
    return Children.map(node, (child) => highlightReactNode(child, keyword));
  }
  if (!isValidElement(node)) {
    return node;
  }
  const childProps = (node.props || {}) as { children?: ReactNode };
  if (childProps.children == null) {
    return node;
  }
  return cloneElement(node as any, {
    ...childProps,
    children: highlightReactNode(childProps.children, keyword),
  });
}

function createInspectorMarkdownComponents(query: string) {
  const withHighlight = (Tag: any, extraProps?: Record<string, any>) => ({ children, ...props }: any) => (
    <Tag {...extraProps} {...props}>{highlightReactNode(children, query)}</Tag>
  );

  return {
    a: ({ href, children, ...props }: any) => (
      <a {...props} href={href} target="_blank" rel="noreferrer noopener">
        {highlightReactNode(children, query)}
      </a>
    ),
    p: withHighlight('p'),
    li: withHighlight('li'),
    strong: withHighlight('strong'),
    em: withHighlight('em'),
    del: withHighlight('del'),
    blockquote: withHighlight('blockquote'),
    code: withHighlight('code'),
    h1: withHighlight('h1'),
    h2: withHighlight('h2'),
    h3: withHighlight('h3'),
    h4: withHighlight('h4'),
    td: withHighlight('td'),
    th: withHighlight('th'),
  };
}

function shouldShowHistorySnippet(item: any) {
  const snippet = normalizeHistoryText(item?.snippet);
  if (!snippet) return false;
  const q = normalizeHistoryText(item?.q);
  const a = normalizeHistoryText(item?.a);
  if (snippet === q || snippet === a) return false;
  return item?.match_field && item.match_field !== 'q';
}

function InspectorMarkdown({
  text,
  className = '',
  fallback = '暂无',
  dataId,
  highlightQuery = '',
}: {
  text?: string;
  className?: string;
  fallback?: string;
  dataId: string;
  highlightQuery?: string;
}) {
  const content = String(text || '').trim();
  const components = useMemo(() => createInspectorMarkdownComponents(highlightQuery), [highlightQuery]);
  if (!content) {
    return <div data-id={dataId} className={className}>{fallback}</div>;
  }
  return (
    <div data-id={dataId} className={`chat-markdown ${className}`.trim()}>
      <Markdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </Markdown>
    </div>
  );
}

export default function AgentInspector({
  paneId,
  paneTitle,
  open,
  onClose,
  requestedTab,
  onPanePatch,
  embedded = false,
  liveStatus,
  inspectorVersion = 0,
}: {
  paneId: string;
  paneTitle?: string;
  open: boolean;
  onClose: () => void;
  requestedTab?: InspectorRequestedTab;
  onPanePatch?: (paneId: string, patch: any) => void;
  embedded?: boolean;
  liveStatus?: string;
  inspectorVersion?: number;
}) {
  const [tab, setTab] = useState<InspectorTab>('overview');
  const [refreshNonce, setRefreshNonce] = useState(0);
  const [queryDraft, setQueryDraft] = useState('');
  const [query, setQuery] = useState('');
  const [historyOffset, setHistoryOffset] = useState(0);
  const [data, setData] = useState<any>(null);
  const [notesDraft, setNotesDraft] = useState('');
  const [notesSaving, setNotesSaving] = useState(false);
  const [promptRulesDraft, setPromptRulesDraft] = useState<PromptRulesDraft>(() => createEmptyPromptRuleDraft());
  const [memorySaving, setMemorySaving] = useState(false);
  const [memorySection, setMemorySection] = useState<MemorySectionId>('global');
  const [settingsSection, setSettingsSection] = useState<SettingsSectionId>('general');
  const [settingsData, setSettingsData] = useState<EditPaneData | null>(null);
  const [settingsLoading, setSettingsLoading] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [runtimeAIProviderOptions, setRuntimeAIProviderOptions] = useState<RuntimeAIProviderOption[]>([]);
  const [runtimeAIDefault, setRuntimeAIDefault] = useState<RuntimeAIDefaultSummary | null>(null);
  const settingsPaneLoadedRef = useRef<string>('');
  const [generalSettingsBaseline, setGeneralSettingsBaseline] = useState('null');
  const [modelSettingsBaseline, setModelSettingsBaseline] = useState('null');

  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(queryDraft.trim()), 220);
    return () => window.clearTimeout(timer);
  }, [queryDraft]);

  useEffect(() => {
    setHistoryOffset(0);
  }, [query, paneId]);

  useEffect(() => {
    if (!requestedTab) return;
    setTab(requestedTab === 'notes' || requestedTab === 'history' ? 'overview' : requestedTab);
  }, [paneId, requestedTab]);

  useEffect(() => {
    setMemorySection('global');
  }, [paneId]);

  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    const target = `${paneId}:main.0`;
    settingsPaneLoadedRef.current = target;
    setSettingsLoading(true);
    apiService.getPane(paneId).then(({ data: detail }) => {
      if (cancelled || settingsPaneLoadedRef.current !== target) return;
      const next: EditPaneData = {
        target,
        title: String(detail?.title || paneTitle || paneId),
        agent_duty: String(detail?.agent_duty || ''),
        agent_type: String(detail?.agent_type || ''),
        allow_all_actions: !!detail?.allow_all_actions,
        use_official_auth: !!detail?.use_official_auth,
        use_proxy: !!detail?.use_proxy,
        proxy: detail?.proxy && typeof detail.proxy === 'object'
          ? {
              password: String(detail.proxy.password || ''),
              rule: String(detail.proxy.rule || ''),
            }
          : null,
        workspace: String(detail?.workspace || ''),
        active: detail?.active !== false && detail?.active !== 0,
        init_script: String(detail?.init_script || ''),
        tg_enable: !!detail?.tg_enable,
        tg_token: String(detail?.tg_token || ''),
        tg_chat_id: String(detail?.tg_chat_id || ''),
        config: String(detail?.config || '{}'),
        ttyd_preview: String(detail?.ttyd_preview || ''),
        role: String(detail?.role || ''),
        default_model: String(detail?.default_model || ''),
        runtime_ai: detail?.runtime_ai && typeof detail.runtime_ai === 'object'
          ? {
              provider_name: String(detail.runtime_ai.provider_name || ''),
            }
          : null,
      };
      const providerOptions = Array.isArray(detail?.runtime_ai_provider_options) ? detail.runtime_ai_provider_options : [];
      setRuntimeAIProviderOptions(providerOptions.map((item: any) => ({
        key: String(item?.key || ''),
        label: String(item?.label || item?.key || ''),
        protocol: String(item?.protocol || ''),
        models: Array.isArray(item?.models) ? item.models.map((model: any) => String(model)) : undefined,
      })).filter((item: RuntimeAIProviderOption) => item.key));
      setRuntimeAIDefault(detail?.runtime_ai_default && typeof detail.runtime_ai_default === 'object' ? {
        provider_name: String(detail.runtime_ai_default.provider_name || ''),
        provider_label: String(detail.runtime_ai_default.provider_label || ''),
        model: String(detail.runtime_ai_default.model || ''),
      } : null);
      setSettingsData(next);
      setGeneralSettingsBaseline(serializeGeneralSettings(next));
      setModelSettingsBaseline(serializeModelSettings(next));
    }).catch(() => {
      if (cancelled || settingsPaneLoadedRef.current !== target) return;
      const fallback: EditPaneData = {
        target,
        title: paneTitle || paneId,
        use_proxy: false,
        proxy: null,
        runtime_ai: null,
      };
      setSettingsData(fallback);
      setGeneralSettingsBaseline(serializeGeneralSettings(fallback));
      setModelSettingsBaseline(serializeModelSettings(fallback));
    }).finally(() => {
      if (!cancelled && settingsPaneLoadedRef.current === target) {
        setSettingsLoading(false);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [open, paneId, paneTitle]);

  useEffect(() => {
    if (!open || !paneId) return;
    setData({
      pane_id: paneId,
      overview: {
        pane_id: paneId,
        status: '',
        status_label: '--',
        active_request_count: 0,
        conversation_count: 0,
        model: '',
        provider: '',
      },
      history: { total: 0, items: [], offset: 0, limit: HISTORY_PAGE_SIZE, has_more: false },
      history_view: [],
      notes: { content: '', updated_at: '' },
      runtime_memory: { content: '', enabled: false, updated_at: '' },
      prompt_rules: {
        global: { content: '', enabled: false, inject_on_request: false, available: true, label: '全局', key: 'global' },
        project: { content: '', enabled: false, inject_on_request: false, available: false, label: '项目', key: '' },
        agent: { content: '', enabled: false, inject_on_request: false, available: true, label: paneId, key: paneId },
      },
    });
  }, [open, paneId, paneTitle, query, historyOffset]);

  useEffect(() => {
    if (!open || !paneId) return;
    const detail = {
      status: liveStatus || '',
      updated_at: new Date().toISOString(),
      agent_id: paneId,
    };
    if (!detail.status) return;
    setData((prev: any) => {
      const current = prev || {};
      const overview = current.overview || {};
      const nextOverview = {
        ...overview,
        status: detail.status || overview.status,
        reply_updated_at: detail.updated_at || overview.reply_updated_at,
      };
      return { ...current, overview: nextOverview };
    });
  }, [liveStatus, open, paneId]);


  useEffect(() => {
    setNotesDraft(data?.notes?.content || '');
  }, [data?.notes?.content, paneId]);

  useEffect(() => {
    setPromptRulesDraft(buildPromptRulesDraft(data, paneId));
  }, [data?.prompt_rules, data?.runtime_memory?.content, data?.runtime_memory?.enabled, data?.runtime_memory?.updated_at, paneId]);

  useEffect(() => {
    setSettingsSection('general');
  }, [paneId]);

  const overview = data?.overview || {};
  const promptRules = data?.prompt_rules || {};
  const history = data?.history || { total: 0, items: [], offset: 0, limit: HISTORY_PAGE_SIZE, has_more: false };
  const normalizedAgentType = normalizeAgentType(settingsData?.agent_type);
  const modelSettingsEnabled = normalizedAgentType === 'codex' || normalizedAgentType === 'claude';
  const historyStart = history.total > 0 ? Number(history.offset || 0) + 1 : 0;
  const historyEnd = history.total > 0 ? Number(history.offset || 0) + (history.items || []).length : 0;
  const projectKey = String(promptRules?.project_key || promptRulesDraft.project.key || '').trim();
  const displayInputTokens = pickUsageValue(overview.input_tokens, overview.cumulative_input_tokens);
  const displayOutputTokens = pickUsageValue(overview.output_tokens, overview.cumulative_output_tokens);
  const displayTotalTokens = pickUsageValue(overview.total_tokens, overview.cumulative_total_tokens);
  const displayCostCredit = pickUsageValue(overview.cost_credit, overview.cumulative_cost_credit);

  const dirtyNotes = useMemo(() => notesDraft !== (data?.notes?.content || ''), [data?.notes?.content, notesDraft]);
  const dirtyMemory = useMemo(() => {
    return serializePromptRulesDraft(promptRulesDraft) !== serializePromptRulesDraft(buildPromptRulesDraft(data, paneId));
  }, [data, paneId, promptRulesDraft]);
  const dirtyGeneralSettings = useMemo(() => {
    return serializeGeneralSettings(settingsData) !== generalSettingsBaseline;
  }, [generalSettingsBaseline, settingsData]);
  const dirtyModelSettings = useMemo(() => {
    return serializeModelSettings(settingsData) !== modelSettingsBaseline;
  }, [modelSettingsBaseline, settingsData]);

  useEffect(() => {
    if (settingsSection === 'model' && !modelSettingsEnabled) {
      setSettingsSection('general');
    }
  }, [modelSettingsEnabled, settingsSection]);

  const saveNotes = async () => {
    if (notesSaving || !dirtyNotes) return;
    setNotesSaving(true);
    try {
      const { data: next } = await apiService.updateAgentInspectorNotes(paneId, notesDraft);
      setData((prev: any) => ({ ...(prev || {}), notes: next.notes }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: `${paneId} 备注已保存` }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: `错误：${paneId} 备注保存失败` }));
    } finally {
      setNotesSaving(false);
    }
  };

  const saveRuntimeMemory = async () => {
    if (memorySaving || !dirtyMemory) return;
    setMemorySaving(true);
    try {
      const { data: next } = await apiService.updateAgentPromptRules(paneId, {
        global: promptRulesDraft.global,
        project: promptRulesDraft.project,
        agent: promptRulesDraft.agent,
      });
      setData((prev: any) => ({
        ...(prev || {}),
        prompt_rules: next.prompt_rules,
        runtime_memory: next.runtime_memory,
        overview: {
          ...(prev?.overview || {}),
          overlay_preview: next.prompt_rules?.overlay_preview || '',
          runtime_memory_enabled: next.runtime_memory?.enabled,
          runtime_memory_updated: next.runtime_memory?.updated_at,
          runtime_memory_preview: compactText(next.runtime_memory?.content, '暂无', 160),
        },
      }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: `${paneId} Prompt Rules 已保存` }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: `错误：${paneId} Prompt Rules 保存失败` }));
    } finally {
      setMemorySaving(false);
    }
  };

  const patchPromptRule = (scope: PromptRuleKey, patch: Partial<PromptRuleDraft>) => {
    setPromptRulesDraft((prev) => ({
      ...prev,
      [scope]: {
        ...prev[scope],
        ...patch,
      },
    }));
  };

  const runtimeAISelectOptions = useMemo<SelectOption[]>(() => {
    return runtimeAIProviderOptions.map((option) => ({
      value: option.key,
      label: option.label,
      sub: option.protocol ? `${option.key} · ${option.protocol}` : option.key,
    }));
  }, [runtimeAIProviderOptions]);

  const runtimeAIEnabled = useMemo(() => {
    return settingsData?.runtime_ai != null;
  }, [settingsData?.runtime_ai]);

  const patchSettingsData = (patch: Partial<EditPaneData>) => {
    setSettingsData((prev) => ({ ...(prev || { target: `${paneId}:main.0`, title: paneTitle || paneId, runtime_ai: null }), ...patch }));
  };

  const hasDuplicateTelegramToken = async () => {
    const token = String(settingsData?.tg_token || '').trim();
    if (!token) return false;
    try {
      const { data: panesData } = await apiService.getPanes();
      const panes = Array.isArray(panesData) ? panesData : panesData?.panes || [];
      const otherPaneIds = panes
        .map((pane: any) => String(pane?.pane_id || ''))
        .filter((id: string) => id && id !== `${paneId}:main.0` && id !== paneId);
      const details = await Promise.all(
        otherPaneIds.map(async (id: string) => {
          try {
            const { data: detail } = await apiService.getPane(id);
            return detail;
          } catch {
            return null;
          }
        }),
      );
      return details.some((detail: any) => String(detail?.tg_token || '').trim() === token);
    } catch {
      return false;
    }
  };

  const saveSettings = async () => {
    if (!settingsData || settingsSaving || !dirtyGeneralSettings) return;
    setSettingsSaving(true);
    try {
      if (await hasDuplicateTelegramToken()) {
        window.dispatchEvent(new CustomEvent('show-toast', { detail: '这个 token 已绑，请更换其他 token' }));
        return;
      }
      const payload = {
        ...settingsData,
        runtime_ai: settingsData.use_official_auth ? null : (settingsData.runtime_ai && String(settingsData.runtime_ai.provider_name || '').trim()
          ? { provider_name: String(settingsData.runtime_ai.provider_name || '').trim() }
          : null),
        proxy: settingsData.proxy && (String(settingsData.proxy.password || '').trim() || String(settingsData.proxy.rule || '').trim())
          ? {
              password: String(settingsData.proxy.password || '').trim(),
              rule: String(settingsData.proxy.rule || '').trim(),
            }
          : null,
      };
      await apiService.updatePane(paneId, payload);
      onPanePatch?.(paneId, payload);
      setSettingsData(payload);
      setGeneralSettingsBaseline(serializeGeneralSettings(payload));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: { message: '已保存', variant: 'success' } }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: `错误：${paneId} 设置保存失败` }));
    } finally {
      setSettingsSaving(false);
    }
  };

  const saveModelSettings = async () => {
    if (!settingsData || settingsSaving || !dirtyModelSettings) return;
    setSettingsSaving(true);
    try {
      const runtimeAI = settingsData.use_official_auth ? null : (settingsData.runtime_ai && String(settingsData.runtime_ai.provider_name || '').trim()
        ? { provider_name: String(settingsData.runtime_ai.provider_name || '').trim() }
        : null);
      const payload = {
        default_model: settingsData.default_model || '',
        use_official_auth: !!settingsData.use_official_auth,
        runtime_ai: runtimeAI,
      };
      await apiService.updatePane(paneId, payload);
      const next = { ...settingsData, runtime_ai: runtimeAI };
      onPanePatch?.(paneId, payload);
      setSettingsData(next);
      setModelSettingsBaseline(serializeModelSettings(next));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: { message: '已保存', variant: 'success' } }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: `错误：${paneId} 模型设置保存失败` }));
    } finally {
      setSettingsSaving(false);
    }
  };

  if (!open) return null;

  return (
    <aside
      data-id={embedded ? 'agent-inspector-embedded' : 'agent-inspector'}
      className={embedded
        ? 'h-full min-w-0 flex-1 bg-[#0b0b0d]'
        : 'h-full w-[360px] min-w-[360px] max-w-[360px] shrink-0 border-l border-[var(--vsc-border)] bg-[#0b0b0d]'}
    >
      <div data-id="agent-inspector-shell" className="flex h-full flex-col">
        {tab === 'history' && (
          <div data-id="agent-inspector-history-search-wrap" className="relative z-20 border-b border-white/[0.06] bg-[#09090b] px-1 pb-2 pt-2">
            <div className="relative overflow-hidden rounded-xl border border-white/[0.08] bg-[#111215] shadow-[0_10px_24px_rgba(0,0,0,0.28)]">
              <div className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-zinc-500">
                <Search className="h-4 w-4" />
              </div>
              <input
                data-id="agent-inspector-history-search"
                value={queryDraft}
                onChange={(event) => setQueryDraft(event.target.value)}
                placeholder="搜索 history..."
                className="h-10 w-full bg-transparent pl-10 pr-3 text-sm leading-10 text-zinc-100 outline-none placeholder:text-zinc-600"
              />
            </div>
          </div>
        )}

        <div
          data-id="agent-inspector-content"
          className={`relative flex-1 overflow-y-auto px-[10px] pb-4 ${tab === 'history' ? 'pt-0' : 'pt-2'}`}
        >
          {tab === 'overview' && (
            <div data-id="agent-inspector-overview" className="space-y-4">
              <div data-id="agent-inspector-overview-summary-grid" className="space-y-2 px-1">
                <div data-id="agent-inspector-overview-card-status" className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3">
                  <div className="min-w-0">
                    <div data-id="agent-inspector-overview-card-status-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">状态</div>
                    <div data-id="agent-inspector-overview-card-status-value" className="mt-1 text-base font-medium text-zinc-100">{formatStatusLabel(overview.status_label)}</div>
                  </div>
                  <div className="mt-3 min-w-0">
                    <div data-id="agent-inspector-overview-card-model-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">模型</div>
                    <div data-id="agent-inspector-overview-card-model-value" className="mt-1 break-all text-sm font-medium leading-5 text-zinc-100">
                      {compactText(overview.model, '未知')}
                    </div>
                    {formatProviderLabel(overview.provider) ? (
                      <div data-id="agent-inspector-overview-card-model-meta" className="mt-1 text-[11px] text-zinc-500">
                        {formatProviderLabel(overview.provider)}
                      </div>
                    ) : null}
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-2">
                  <div data-id="agent-inspector-overview-card-input-tokens" className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div data-id="agent-inspector-overview-card-input-tokens-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">In</div>
                    <div data-id="agent-inspector-overview-card-input-tokens-value" className="mt-1 text-sm font-medium text-zinc-100">{formatTokens(displayInputTokens)}</div>
                  </div>
                  <div className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">Out</div>
                    <div className="mt-1 text-sm font-medium text-zinc-100">{formatTokens(displayOutputTokens)}</div>
                  </div>
                  <div data-id="agent-inspector-overview-card-total-tokens" className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div data-id="agent-inspector-overview-card-total-tokens-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">Total</div>
                    <div data-id="agent-inspector-overview-card-total-tokens-value" className="mt-1 text-sm font-medium text-zinc-100">{formatTokens(displayTotalTokens)}</div>
                  </div>
                  <div className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">Cost</div>
                    <div className="mt-1 text-sm font-medium text-zinc-100">
                      {formatCostEstimate(displayCostCredit)}
                    </div>
                  </div>
                </div>
              </div>

              <div data-id="agent-inspector-overview-notes-section" className="space-y-2 px-1">
                <div className="flex items-center justify-between gap-3 text-[11px] text-zinc-500">
                  <span data-id="agent-inspector-overview-notes-label" className="uppercase tracking-[0.14em]">备注</span>
                  <span data-id="agent-inspector-overview-notes-updated" className="truncate">
                    {notesSaving ? '保存中...' : formatTime(data?.notes?.updated_at)}
                  </span>
                </div>
                <textarea
                  data-id="agent-inspector-overview-notes"
                  value={notesDraft}
                  onChange={(event) => setNotesDraft(event.target.value)}
                  onBlur={() => { void saveNotes(); }}
                  rows={8}
                  placeholder="备注..."
                  className="w-full resize-none rounded-lg bg-[#101114] px-3 py-2.5 text-sm leading-6 text-zinc-100 outline-none placeholder:text-zinc-600"
                />
              </div>


            </div>
          )}

          {tab === 'history' && (
            <>
              <div data-id="agent-inspector-history-list" className="pb-4">
                {(history.items || []).length === 0 ? (
                  <EmptyState text={history.reason || (query ? '没有匹配的历史记录' : '这个 agent 还没有 history')} />
                ) : (
                  (history.items || []).map((item: any, index: number) => (
                    <article
                      key={`${item.id}-${item.qTime || item.aTime || 'history'}`}
                      className={`px-1.5 py-2 ${index < (history.items || []).length - 1 ? 'border-b border-white/[0.05]' : ''}`}
                    >
                      <div data-id="agent-inspector-history-item-body" className="space-y-1.5 text-[12px] text-zinc-500">
                        <div
                          data-id="agent-inspector-history-item-question-wrap"
                          className="rounded-md bg-[#1d2636] px-2 py-1.5"
                        >
                          <InspectorMarkdown
                            dataId="agent-inspector-history-item-question-markdown"
                            text={item.q}
                            fallback="暂无"
                            highlightQuery={query}
                            className="inspector-markdown inspector-markdown-question break-words [overflow-wrap:anywhere] text-[12px] leading-5 text-zinc-50"
                          />
                        </div>
                        <div
                          data-id="agent-inspector-history-item-answer-wrap"
                          className="rounded-md bg-[#0d0e11] px-2 py-1.5"
                        >
                          <InspectorMarkdown
                            dataId="agent-inspector-history-item-answer-markdown"
                            text={item.a}
                            fallback="暂无"
                            highlightQuery={query}
                            className="inspector-markdown inspector-markdown-answer break-words [overflow-wrap:anywhere] text-[12px] leading-5 text-zinc-300"
                          />
                        </div>
                      </div>
                    </article>
                  ))
                )}
              </div>
              {history.total > 0 ? (
                <div data-id="agent-inspector-history-pagination" className="flex items-center justify-between gap-2 px-1.5 text-[11px] text-zinc-500">
                  <div data-id="agent-inspector-history-pagination-meta" className="truncate">
                    {historyStart}-{historyEnd} / {history.total}
                  </div>
                  <div className="flex items-center gap-1.5">
                    <button
                      type="button"
                      data-id="agent-inspector-history-pagination-prev"
                      disabled={(history.offset || 0) <= 0}
                      onClick={() => setHistoryOffset((value) => Math.max(0, value - HISTORY_PAGE_SIZE))}
                      className="rounded-md bg-white/[0.04] px-2 py-1 text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:cursor-not-allowed disabled:opacity-30"
                    >
                      上一页
                    </button>
                    <button
                      type="button"
                      data-id="agent-inspector-history-pagination-next"
                      disabled={!history.has_more}
                      onClick={() => setHistoryOffset((value) => value + HISTORY_PAGE_SIZE)}
                      className="rounded-md bg-white/[0.04] px-2 py-1 text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:cursor-not-allowed disabled:opacity-30"
                    >
                      下一页
                    </button>
                  </div>
                </div>
              ) : null}
            </>
          )}

          {tab === 'memory' && (
            <div data-id="agent-inspector-memory-tab" className="space-y-4">
              <div data-id="agent-inspector-memory-summary" className="space-y-1 rounded-lg bg-[#101114] px-3 py-2.5">
                <div className="flex items-center gap-2 text-[11px] uppercase tracking-[0.14em] text-zinc-500">
                  <Brain className="h-3.5 w-3.5" />
                  Prompt Rules
                </div>
                <div className="text-[12px] leading-5 text-zinc-400">
                  请求前会按 <code>global -&gt; project -&gt; agent</code> 合并；中文回复由 gateway 统一注入，不再依赖 pane 配置。
                </div>
              </div>
              <div data-id="agent-inspector-memory-sections" className="scrollbar-zero flex gap-1 overflow-x-auto whitespace-nowrap">
                {memorySections.map((item) => (
                  <button
                    key={item.id}
                    type="button"
                    data-id={`agent-inspector-memory-section-${item.id}`}
                    onClick={() => setMemorySection(item.id)}
                    className={`flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 text-[11px] leading-5 transition-colors ${
                      memorySection === item.id ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                    }`}
                  >
                    <span>{item.label}</span>
                  </button>
                ))}
              </div>
              <div data-id="agent-inspector-memory-rules" className="space-y-3">
                {memorySection === 'global' && (
                  <PromptRuleEditor
                    dataId="agent-inspector-memory-global"
                    title="Global"
                    subtitle="所有 agent 共用"
                    value={promptRulesDraft.global}
                    placeholder="例如：默认中文回复；输出简洁；优先直接给结论。"
                    onChange={(patch) => patchPromptRule('global', patch)}
                  />
                )}
                {memorySection === 'agent' && (
                  <PromptRuleEditor
                    dataId="agent-inspector-memory-agent"
                    title="Agent"
                    subtitle={promptRulesDraft.agent.label || paneId}
                    value={promptRulesDraft.agent}
                    placeholder="例如：只负责 w-20010；先查再改；不要碰 UI。"
                    onChange={(patch) => patchPromptRule('agent', patch)}
                  />
                )}
                {memorySection === 'preview' && (
                  <div data-id="agent-inspector-memory-overlay-preview" className="rounded-lg bg-[#101114] px-3 py-2.5 text-[12px] leading-5 text-zinc-400">
                    <div className="mb-1 text-[11px] uppercase tracking-[0.14em] text-zinc-500">最终注入预览</div>
                    {compactText(overview.overlay_preview, '当前没有可注入的运行时记忆内容')}
                  </div>
                )}
              </div>
            </div>
          )}

          {tab === 'settings' && (
            <div data-id="agent-inspector-settings-tab" className="space-y-4">
              <div data-id="agent-inspector-settings-sections" className="scrollbar-zero flex gap-1 overflow-x-auto whitespace-nowrap">
                {settingsSections.filter((item) => item.id !== 'model' || modelSettingsEnabled).map((item) => {
                  const Icon = item.icon;
                  return (
                    <button
                      key={item.id}
                      type="button"
                      data-id={`agent-inspector-settings-section-${item.id}`}
                      onClick={() => setSettingsSection(item.id)}
                      className={`flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 text-[11px] leading-5 transition-colors ${
                        settingsSection === item.id ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                      }`}
                    >
                      <Icon className="h-3.5 w-3.5" />
                      <span>{item.label}</span>
                    </button>
                  );
                })}
              </div>

              {settingsSection === 'general' && (
                <div data-id="agent-inspector-settings-general" className="space-y-5">
                  <InspectorField label="标题">
                    <InspectorInput value={settingsData?.title || ''} onChange={(value) => patchSettingsData({ title: value })} onBlur={() => { void saveSettings(); }} placeholder="窗格标题" />
                  </InspectorField>
                  <InspectorField label="工作目录" desc="worker 工作区目录" mono>
                    <InspectorInput value={settingsData?.workspace || ''} onChange={(value) => patchSettingsData({ workspace: value })} placeholder="/home/user/project" mono readOnly />
                  </InspectorField>
                  <InspectorField label="智能体类型">
                    <div
                      data-id="agent-inspector-settings-agent-types"
                      className="flex items-center gap-3 rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-zinc-200"
                    >
                      <AgentAvatar
                        agentType={settingsData?.agent_type}
                        title={settingsData?.agent_type || '未设置'}
                        variant="select"
                        dataId="agent-inspector-settings-agent-type-avatar"
                      />
                      <span className="truncate">{settingsData?.agent_type || '未设置'}</span>
                    </div>
                  </InspectorField>
                  <InspectorToggle
                    label="自动启动"
                    desc="服务重启后恢复"
                    checked={settingsData?.active !== false}
                    onChange={(value) => patchSettingsData({ active: value })}
                    onBlur={() => { void saveSettings(); }}
                  />
                  <InspectorToggle
                    label="启动时允许所有操作"
                    desc="Codex / Claude / Opencode 追加全部允许操作"
                    checked={!!settingsData?.allow_all_actions}
                    onChange={(value) => patchSettingsData({ allow_all_actions: value })}
                    onBlur={() => { void saveSettings(); }}
                  />
                  <InspectorToggle
                    label="启用代理"
                    desc="启动前执行 cicy_proxy_on，并检查 mihome IN-USER 规则"
                    checked={!!settingsData?.use_proxy}
                    onChange={(value) => patchSettingsData({ use_proxy: value })}
                    onBlur={() => { void saveSettings(); }}
                  />
                  {settingsData?.use_proxy && (
                    <>
                      <InspectorField label="代理密码" desc="留空时优先 api_token，不存在时回退 user==pass">
                        <InspectorInput value={settingsData?.proxy?.password || ''} onChange={(value) => patchSettingsData({ proxy: { ...(settingsData?.proxy || {}), password: value } })} onBlur={() => { void saveSettings(); }} placeholder="可选，自定义代理密码" mono />
                      </InspectorField>
                      <InspectorField label="mihome 规则" desc="可选，指定必须存在的完整规则，例如 IN-USER,w-10001,proxy-a">
                        <InspectorInput value={settingsData?.proxy?.rule || ''} onChange={(value) => patchSettingsData({ proxy: { ...(settingsData?.proxy || {}), rule: value } })} onBlur={() => { void saveSettings(); }} placeholder="IN-USER,w-10001,proxy-a" mono />
                      </InspectorField>
                    </>
                  )}
                </div>
              )}

              {settingsSection === 'model' && (
                <div data-id="agent-inspector-settings-model" className="space-y-5">
                  <div className="space-y-3 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3">
                    <div>
                      <div className="text-sm font-medium text-zinc-100">启动默认模型</div>
                      <div className="mt-1 text-xs leading-5 text-zinc-500">同时作为 agent 启动默认模型和网关默认模型。留空时跟随当前 provider 默认值。</div>
                    </div>
                    <InspectorField label="Default Model" desc="例如：gpt-5.5 / claude-opus-4-7">
                      <InspectorInput value={settingsData?.default_model || ''} onChange={(value) => patchSettingsData({ default_model: value })} onBlur={() => { void saveModelSettings(); }} placeholder="留空则跟随当前 agent 默认" mono />
                    </InspectorField>
                  </div>

                  <div className="space-y-3 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3">
                    <InspectorToggle
                      label="使用官方认证"
                      desc="开启后不注入本地 gateway auth"
                      checked={!!settingsData?.use_official_auth}
                      onChange={(value) => patchSettingsData({ use_official_auth: value, runtime_ai: value ? null : settingsData?.runtime_ai || null })}
                      onBlur={() => { void saveModelSettings(); }}
                    />
                  </div>

                  {!settingsData?.use_official_auth && (
                    <div className="space-y-3 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3">
                      <div>
                        <div className="text-sm font-medium text-zinc-100">网关运行时覆盖</div>
                        <div className="mt-1 text-xs leading-5 text-zinc-500">运行时只覆盖 provider；模型跟随上方默认模型。</div>
                      </div>
                      <div className="rounded-xl border border-white/[0.06] bg-black/20 px-3 py-2 text-xs text-zinc-400">
                        <div>默认 Provider：{runtimeAIDefault?.provider_label || runtimeAIDefault?.provider_name || '未配置'}</div>
                      </div>
                      <InspectorToggle
                        label="启用自定义覆盖"
                        desc="关闭时完全跟随当前 agent 默认 provider"
                        checked={runtimeAIEnabled}
                        onChange={(value) => patchSettingsData({ runtime_ai: value ? (settingsData?.runtime_ai || { provider_name: '' }) : null })}
                        onBlur={() => { void saveModelSettings(); }}
                      />
                      {runtimeAIEnabled && (
                        <InspectorField label="Provider" desc="按当前 agent 协议过滤后的可用 provider">
                          <Select
                            value={settingsData?.runtime_ai?.provider_name || ''}
                            onChange={(value) => {
                              patchSettingsData({
                                runtime_ai: {
                                  ...(settingsData?.runtime_ai || {}),
                                  provider_name: value,
                                },
                              });
                            }}
                            onOpenChange={(open) => {
                              if (!open) {
                                void saveModelSettings();
                              }
                            }}
                            options={runtimeAISelectOptions}
                            placeholder="选择 provider"
                            searchable
                          />
                        </InspectorField>
                      )}
                    </div>
                  )}
                </div>
              )}

              {settingsSection === 'telegram' && (
                <div data-id="agent-inspector-settings-telegram" className="space-y-5">
                  <InspectorToggle
                    label="启用 Telegram"
                    desc="通过 Telegram 机器人发送通知"
                    checked={!!settingsData?.tg_enable}
                    onChange={(value) => patchSettingsData({ tg_enable: value })}
                  />
                  <InspectorField label="机器人令牌" desc="保存时会检查这个 token 是否已绑定到其他 agent">
                    <InspectorInput value={settingsData?.tg_token || ''} onChange={(value) => patchSettingsData({ tg_token: value, tg_chat_id: '' })} mono placeholder="1234567890:ABCdef..." />
                  </InspectorField>
                  <InspectorField label="聊天 ID" desc="配置好 token 后，在 bot 里发一条消息，chat_id 会自动获取。">
                    <InspectorInput value={settingsData?.tg_chat_id || ''} onChange={() => {}} mono readOnly placeholder="等待 bot 首条消息后自动绑定" />
                  </InspectorField>
                </div>
              )}

            </div>
          )}
        </div>
      </div>
    </aside>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div data-id="agent-inspector-info-row" className="flex items-start justify-between gap-3">
      <span className="text-zinc-500">{label}</span>
      <span className="max-w-[180px] text-right text-zinc-200">{value}</span>
    </div>
  );
}

function EmptyState({ text }: { text: string }) {
  return (
    <div data-id="agent-inspector-empty-state" className="rounded-2xl border border-dashed border-white/[0.08] px-4 py-8 text-center text-sm text-zinc-600">
      {text}
    </div>
  );
}

function PromptRuleEditor({
  dataId,
  title,
  subtitle,
  value,
  onChange,
  placeholder,
  disabled,
}: {
  dataId: string;
  title: string;
  subtitle?: string;
  value: PromptRuleDraft;
  onChange: (patch: Partial<PromptRuleDraft>) => void;
  placeholder?: string;
  disabled?: boolean;
}) {
  const unavailable = disabled || value.available === false;
  return (
    <section data-id={dataId} className="space-y-2 rounded-lg bg-[#101114] px-3 py-2.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-[12px] font-medium text-zinc-100">{title}</div>
          <div className="truncate text-[11px] text-zinc-500">{subtitle || '--'}</div>
        </div>
        <div className="shrink-0 text-[11px] text-zinc-600">{formatTime(value.updated_at)}</div>
      </div>
      <div className="grid grid-cols-2 gap-2">
        <CompactToggle
          label="启用"
          checked={!!value.enabled}
          disabled={unavailable}
          onChange={(checked) => onChange({ enabled: checked })}
        />
        <CompactToggle
          label="请求时注入"
          checked={!!value.inject_on_request}
          disabled={unavailable}
          onChange={(checked) => onChange({ inject_on_request: checked })}
        />
      </div>
      <textarea
        data-id={`${dataId}-textarea`}
        value={value.content}
        disabled={unavailable}
        onChange={(event) => onChange({ content: event.target.value })}
        rows={6}
        placeholder={unavailable ? '当前 agent 还没有 project key，暂时不能配置 project scope。' : placeholder}
        className="w-full resize-none rounded-lg bg-[#09090b] px-3 py-2.5 text-sm leading-6 text-zinc-100 outline-none placeholder:text-zinc-600 disabled:cursor-not-allowed disabled:text-zinc-600"
      />
    </section>
  );
}

function CompactToggle({
  label,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className="flex items-center justify-between rounded-lg bg-[#09090b] px-2.5 py-2 text-left text-[12px] text-zinc-300 disabled:cursor-not-allowed disabled:opacity-40"
    >
      <span>{label}</span>
      <span className={`relative h-5 w-9 rounded-full transition-colors ${checked ? 'bg-blue-600' : 'bg-white/[0.08]'}`}>
        <span className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${checked ? 'translate-x-4' : 'translate-x-0.5'}`} />
      </span>
    </button>
  );
}

function InspectorField({
  label,
  desc,
  mono,
  children,
}: {
  label: string;
  desc?: string;
  mono?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label className={`mb-1.5 block text-[13px] font-medium text-zinc-300 ${mono ? 'font-mono' : ''}`}>{label}</label>
      {children}
      {desc ? <p className="mt-1 text-[11px] text-zinc-600">{desc}</p> : null}
    </div>
  );
}

function InspectorInput({
  value,
  onChange,
  onBlur,
  placeholder,
  mono,
  readOnly,
}: {
  value: string;
  onChange: (value: string) => void;
  onBlur?: () => void;
  placeholder?: string;
  mono?: boolean;
  readOnly?: boolean;
}) {
  return (
    <input
      data-id="agent-inspector-settings-input"
      type="text"
      value={value}
      onChange={(event) => onChange(event.target.value)}
      onBlur={onBlur}
      placeholder={placeholder}
      readOnly={readOnly}
      className={`w-full rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-zinc-200 transition-all placeholder:text-zinc-700 focus:border-blue-500/40 focus:outline-none focus:ring-1 focus:ring-blue-500/20 ${mono ? 'font-mono' : ''} ${readOnly ? 'cursor-not-allowed opacity-70' : ''}`}
    />
  );
}

function InspectorTextarea({
  value,
  onChange,
  rows,
  placeholder,
  mono,
}: {
  value: string;
  onChange: (value: string) => void;
  rows?: number;
  placeholder?: string;
  mono?: boolean;
}) {
  return (
    <textarea
      data-id="agent-inspector-settings-textarea"
      value={value}
      onChange={(event) => onChange(event.target.value)}
      rows={rows}
      placeholder={placeholder}
      className={`w-full resize-none rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-zinc-200 transition-all placeholder:text-zinc-700 focus:border-blue-500/40 focus:outline-none focus:ring-1 focus:ring-blue-500/20 ${mono ? 'font-mono' : ''}`}
    />
  );
}

function InspectorToggle({
  label,
  desc,
  checked,
  onChange,
  onBlur,
}: {
  label: string;
  desc?: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  onBlur?: () => void;
}) {
  return (
    <div data-id="agent-inspector-settings-toggle" className="flex items-center justify-between py-1">
      <div data-id="agent-inspector-settings-toggle-copy">
        <p className="text-[13px] font-medium text-zinc-300">{label}</p>
        {desc ? <p className="mt-0.5 text-[11px] text-zinc-600">{desc}</p> : null}
      </div>
      <button
        type="button"
        data-id="agent-inspector-settings-toggle-button"
        onClick={() => onChange(!checked)}
        onBlur={onBlur}
        className={`relative h-6 w-11 rounded-full transition-colors ${checked ? 'bg-blue-600' : 'bg-white/[0.08]'}`}
      >
        <div data-id="agent-inspector-settings-toggle-thumb" className={`absolute top-1 h-4 w-4 rounded-full bg-white shadow-md transition-transform ${checked ? 'translate-x-[22px]' : 'translate-x-1'}`} />
      </button>
    </div>
  );
}
