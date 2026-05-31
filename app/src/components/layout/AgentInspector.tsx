import { Children, cloneElement, isValidElement, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Brain, Search, Settings } from 'lucide-react';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Trans, useTranslation } from 'react-i18next';
import config from '../../config';
import apiService from '../../services/api';
import { TokenManager } from '../../services/tokenManager';
import type { EditPaneData } from '../../types';
import Select, { type SelectOption } from '../ui/Select';
import { normalizeAgentType } from '../../lib/agentType';
import { useApp } from '../../contexts/AppContext';

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

const tabs: Array<{ id: InspectorTab; labelKey: string }> = [
  { id: 'overview', labelKey: 'tabOverview' },
  { id: 'history', labelKey: 'tabHistory' },
  { id: 'memory', labelKey: 'tabMemory' },
  { id: 'settings', labelKey: 'tabSettings' },
];

const settingsSections = [
  { id: 'general', labelKey: 'sectionGeneral', icon: Settings },
] as const;

type SettingsSectionId = typeof settingsSections[number]['id'];

const memorySections = [
  { id: 'global', labelKey: 'memoryGlobal' },
  { id: 'agent', labelKey: 'memoryAgent' },
  { id: 'rules-files', labelKey: 'memoryRulesFiles' },
  { id: 'preview', labelKey: 'memoryPreview' },
] as const;

type MemorySectionId = typeof memorySections[number]['id'];

function formatTime(value?: string) {
  if (!value) return '';
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleString(undefined, { hour12: false });
}

function compactText(value?: string, fallback = '', limit?: number) {
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
    use_custom_gateway: !!value?.use_custom_gateway,
    runtime_ai: value?.use_custom_gateway && value?.runtime_ai && String(value.runtime_ai.provider_name || '').trim()
      ? { provider_name: String(value.runtime_ai.provider_name || '').trim() }
      : null,
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

function formatStatusLabel(value: string | undefined, idleLabel: string) {
  const text = String(value || '').trim();
  if (!text) return idleLabel;
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
    global: { content: '', enabled: false, inject_on_request: false, updated_at: '', available: true, label: 'global', key: 'global' },
    project: { content: '', enabled: false, inject_on_request: false, updated_at: '', available: false, label: 'project', key: '' },
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
      label: promptRules?.project?.label || 'project',
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
      <a data-id="agent-inspector-auto-1" {...props} href={href} target="_blank" rel="noreferrer noopener">
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
  fallback = '',
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
  const { t } = useTranslation('agentInspector');
  const {
    agentDetails,
    activeAgentDetail,
    setActiveAgentId,
    patchAgentDetail: patchSharedAgentDetail,
    runPaneSaveSerially,
  } = useApp();
  // Keep a ref to the live agentDetails map so the fetch effect can decide
  // whether the cache is "complete enough to skip the fetch" without taking
  // agentDetails as a dep (which would cause it to refire on every patch).
  const agentDetailsRef = useRef(agentDetails);
  useEffect(() => { agentDetailsRef.current = agentDetails; }, [agentDetails]);
  // Inspector binds its read source to the global activeAgentDetail — same
  // object the footer ModelPicker / card title / sidebar all read from. There
  // is ONE state for "the agent currently being inspected", not two parallel
  // copies. Inspector pushes its paneId into the context's activeAgentId so
  // opening an inspector for a non-focused pane still routes shared reads here.
  useEffect(() => {
    if (!open || !paneId) return;
    setActiveAgentId(paneId);
  }, [open, paneId, setActiveAgentId]);
  const settingsData: any = activeAgentDetail;
  const runtimeAIProviderOptions = useMemo<RuntimeAIProviderOption[]>(() => {
    const list = Array.isArray(settingsData?.runtime_ai_provider_options) ? settingsData.runtime_ai_provider_options : [];
    return list.map((item: any) => ({
      key: String(item?.key || ''),
      label: String(item?.label || item?.key || ''),
      protocol: String(item?.protocol || ''),
      models: Array.isArray(item?.models) ? item.models.map((m: any) => String(m)) : undefined,
    })).filter((item: RuntimeAIProviderOption) => item.key);
  }, [settingsData?.runtime_ai_provider_options]);
  const runtimeAIDefault = useMemo<RuntimeAIDefaultSummary | null>(() => {
    const raw = settingsData?.runtime_ai_default;
    if (!raw || typeof raw !== 'object') return null;
    return {
      provider_name: String(raw.provider_name || ''),
      provider_label: String(raw.provider_label || ''),
      model: String(raw.model || ''),
    };
  }, [settingsData?.runtime_ai_default]);
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
  const [expandedRulesFile, setExpandedRulesFile] = useState<string | null>(null);
  const [settingsSection, setSettingsSection] = useState<SettingsSectionId>('general');
  const [settingsLoading, setSettingsLoading] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
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
    setGeneralSettingsBaseline('null');
    setModelSettingsBaseline('null');
    // (B) Skip fetch if context already has a complete entry (Workspace's
    // fetch effect populates it for the active pane). Avoids the race where
    // an in-flight fetch lands AFTER an optimistic patch and wipes it.
    const shortPaneId = paneId.split(':')[0];
    const cached = agentDetailsRef.current[shortPaneId];
    if (cached && cached.agent_type && cached.runtime_ai_provider_options) {
      setGeneralSettingsBaseline(serializeGeneralSettings(cached));
      setModelSettingsBaseline(serializeModelSettings(cached));
      return () => { cancelled = true; };
    }
    setSettingsLoading(true);
    apiService.getPane(paneId).then(({ data: detail }) => {
      if (cancelled || settingsPaneLoadedRef.current !== target) return;
      // (B) Merge fetch into existing entry instead of wholesale replace, so
      // any optimistic patches that landed during the in-flight window are
      // preserved on overlapping keys (caller's value wins on the second pass
      // because patch was applied AFTER fetch was kicked off).
      patchSharedAgentDetail(paneId, detail);
      setGeneralSettingsBaseline(serializeGeneralSettings(detail));
      setModelSettingsBaseline(serializeModelSettings(detail));
      // Legacy callback so Workspace's paneDetails / agents / boundAgents caches
      // also pick up the full detail.
      onPanePatch?.(paneId, detail);
    }).catch(() => {
      if (cancelled || settingsPaneLoadedRef.current !== target) return;
      const fallback: any = {
        title: paneTitle || paneId,
        use_proxy: false,
        proxy: null,
        runtime_ai: null,
      };
      patchSharedAgentDetail(paneId, fallback);
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
    // paneTitle is intentionally excluded — it's a cosmetic prop and a
    // title-only change should not refetch settings (would clobber unsaved edits).
  }, [open, paneId, patchSharedAgentDetail]);

  // Force a fresh pane-detail fetch (incl. runtime_ai_provider_options) on
  // demand, bypassing the cache guard above. Wired to the provider/model
  // Selects' open handlers so edits to providers/models in global.json appear
  // the moment the dropdown is opened, not only after a full page reload.
  const refreshDetailNow = useCallback(() => {
    if (!paneId) return;
    apiService.getPane(paneId).then(({ data: detail }) => {
      if (!detail) return;
      patchSharedAgentDetail(paneId, detail);
      onPanePatch?.(paneId, detail);
    }).catch(() => {});
  }, [paneId, patchSharedAgentDetail, onPanePatch]);


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
        global: { content: '', enabled: false, inject_on_request: false, available: true, label: 'global', key: 'global' },
        project: { content: '', enabled: false, inject_on_request: false, available: false, label: 'project', key: '' },
        agent: { content: '', enabled: false, inject_on_request: false, available: true, label: paneId, key: paneId },
      },
      rules_files: { items: [], file_count: 0, total_bytes: 0 },
      inject_rules_files: true,
      inject_rules_global: true,
      overlay_full: '',
    });
  }, [open, paneId, paneTitle, query, historyOffset]);

  // Fetch the full inspector bundle so memory preview / rules-files section
  // have real data. Refires on inspectorVersion bump for live refresh after
  // saves. Tolerates 404 (pane not yet known to backend).
  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    apiService.getAgentInspector(paneId)
      .then(({ data: next }) => {
        if (cancelled || !next) return;
        setData((prev: any) => ({
          ...(prev || {}),
          prompt_rules: next.prompt_rules || prev?.prompt_rules,
          runtime_memory: next.runtime_memory || prev?.runtime_memory,
          rules_files: next.rules_files || prev?.rules_files,
          inject_rules_files: next.inject_rules_files ?? prev?.inject_rules_files,
          inject_rules_global: next.inject_rules_global ?? prev?.inject_rules_global,
          overlay_full: next.overlay_full ?? prev?.overlay_full ?? '',
          overview: {
            ...(prev?.overview || {}),
            overlay_preview: next.prompt_rules?.overlay_preview || prev?.overview?.overlay_preview || '',
          },
        }));
      })
      .catch(() => { /* tolerated */ });
    return () => { cancelled = true; };
  }, [open, paneId, inspectorVersion, refreshNonce]);

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
  const modelSettingsEnabled = normalizedAgentType === 'codex' || normalizedAgentType === 'claude' || normalizedAgentType === 'opencode';

  // Cert-install nudge: codex / kiro-cli are Rust and read the OS trust store, so
  // when they run NON-gateway (official login → MITM) the host needs the MITM CA
  // installed. node agents (claude / opencode) trust it via NODE_EXTRA_CA_CERTS,
  // so they don't need this. Check the install status on demand + on a refresh.
  const certNudgeRelevant = !settingsData?.use_custom_gateway &&
    (normalizedAgentType === 'codex' || normalizedAgentType === 'kiro-cli');
  const [caStatus, setCaStatus] = useState<{ enabled: boolean; installed: boolean; platform: string; command: string } | null>(null);
  const [caChecking, setCaChecking] = useState(false);
  const checkCaStatus = useCallback(async () => {
    setCaChecking(true);
    try {
      const { data } = await apiService.getMitmCaStatus();
      setCaStatus(data || null);
    } catch {
      setCaStatus(null);
    } finally {
      setCaChecking(false);
    }
  }, []);
  useEffect(() => {
    if (certNudgeRelevant) void checkCaStatus();
    else setCaStatus(null);
  }, [certNudgeRelevant, checkCaStatus]);
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


  const saveNotes = async () => {
    if (notesSaving || !dirtyNotes) return;
    setNotesSaving(true);
    try {
      const { data: next } = await apiService.updateAgentInspectorNotes(paneId, notesDraft);
      setData((prev: any) => ({ ...(prev || {}), notes: next.notes }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastNotesSaved', { paneId }) }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastNotesSaveFailed', { paneId }) }));
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
          runtime_memory_preview: compactText(next.runtime_memory?.content, t('fallbackNone'), 160),
        },
      }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastPromptRulesSaved', { paneId }) }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastPromptRulesSaveFailed', { paneId }) }));
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


  const patchSettingsData = (patch: Partial<EditPaneData>) => {
    patchSharedAgentDetail(paneId, patch);
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

  const saveSettings = async (overrides?: Partial<EditPaneData>) => {
    if (!settingsData) return;
    if (!overrides && !dirtyGeneralSettings) return;
    // Guard: if the inspector is mid-switch to a new paneId, settingsData may
    // still hold the previous pane's data. Refuse to save until the fetch for
    // the current paneId has populated settingsData — otherwise we'd PATCH the
    // wrong pane with another pane's title/agent_type/etc.
    const expectedTarget = `${paneId}:main.0`;
    if (settingsPaneLoadedRef.current !== expectedTarget) return;
    const merged: EditPaneData = { ...settingsData, ...(overrides || {}) };
    setSettingsSaving(true);
    try {
      if (await hasDuplicateTelegramToken()) {
        window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastTokenAlreadyBound') }));
        return;
      }
      const proxy = merged.proxy && (String(merged.proxy.password || '').trim() || String(merged.proxy.rule || '').trim())
        ? {
            password: String(merged.proxy.password || '').trim(),
            rule: String(merged.proxy.rule || '').trim(),
          }
        : null;
      // Whitelist editable fields only. Spreading the whole settingsData here
      // would silently overwrite immutable identity fields (agent_type / workspace
      // / role / init_script / config) on the server. See bug report 2026-05-14
      // where switching panes mid-edit corrupted agent_type/title across panes.
      const payload: Record<string, any> = {
        title: String(merged.title || '').trim(),
        active: merged.active !== false,
        allow_all_actions: !!merged.allow_all_actions,
        tg_enable: !!merged.tg_enable,
        tg_token: String(merged.tg_token || '').trim(),
        tg_chat_id: String(merged.tg_chat_id || '').trim(),
        use_proxy: !!merged.use_proxy,
        proxy,
      };
      // Optimistic: broadcast the patch BEFORE the PATCH round-trip so any other
      // component subscribed to shared agent detail (footer ModelPicker, card title,
      // etc.) re-renders with the new value immediately instead of waiting for the
      // server to confirm. If the PATCH fails we toast an error; we don't bother
      // reverting since the inspector's own local state is the user's intent.
      onPanePatch?.(paneId, payload);
      setGeneralSettingsBaseline(serializeGeneralSettings({ ...merged, proxy }));
      // (A) Per-pane save serialization. Two rapid edits on the same pane will be
      // sent in click order; if PATCH 1 reorders ahead of PATCH 2 on the wire the
      // server would otherwise end with PATCH 1's value while the UI shows PATCH 2.
      await runPaneSaveSerially(paneId, () => apiService.updatePane(paneId, payload));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastSettingsSaveFailed', { paneId }) }));
    } finally {
      setSettingsSaving(false);
    }
  };

  const saveModelSettings = async (overrides?: Partial<{ default_model: string; use_custom_gateway: boolean; runtime_ai: any }>) => {
    if (!settingsData) return;
    if (!overrides && !dirtyModelSettings) return;
    // Same paneId guard as saveSettings — never PATCH with stale data after a switch.
    const expectedTarget = `${paneId}:main.0`;
    if (settingsPaneLoadedRef.current !== expectedTarget) return;
    const merged = { ...settingsData, ...(overrides || {}) };
    setSettingsSaving(true);
    try {
      const runtimeAI = merged.use_custom_gateway && merged.runtime_ai && String(merged.runtime_ai.provider_name || '').trim()
        ? { provider_name: String(merged.runtime_ai.provider_name || '').trim() }
        : null;
      const payload = {
        default_model: String(merged.default_model || '').trim(),
        use_custom_gateway: !!merged.use_custom_gateway,
        runtime_ai: runtimeAI,
      };
      // Optimistic broadcast first — see saveSettings for rationale. Toggling
      // use_custom_gateway here updates the footer ModelPicker / card UI instantly
      // instead of waiting for the PATCH round-trip.
      onPanePatch?.(paneId, payload);
      setModelSettingsBaseline(serializeModelSettings({ ...merged, runtime_ai: runtimeAI }));
      // (A) Serialize alongside saveSettings + ModelPicker — shares the same per-pane queue.
      await runPaneSaveSerially(paneId, () => apiService.updatePane(paneId, payload));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('toastModelSettingsSaveFailed', { paneId }) }));
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
            <div data-id="agent-inspector-auto-2" className="relative overflow-hidden rounded-xl border border-white/[0.08] bg-[#111215] shadow-[0_10px_24px_rgba(0,0,0,0.28)]">
              <div data-id="agent-inspector-auto-3" className="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-zinc-500">
                <Search className="h-4 w-4" />
              </div>
              <input
                data-id="agent-inspector-history-search"
                value={queryDraft}
                onChange={(event) => setQueryDraft(event.target.value)}
                placeholder={t('historySearchPlaceholder')}
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
                  <div data-id="agent-inspector-auto-4" className="min-w-0">
                    <div data-id="agent-inspector-overview-card-status-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">{t('overviewStatus')}</div>
                    <div data-id="agent-inspector-overview-card-status-value" className="mt-1 text-base font-medium text-zinc-100">{formatStatusLabel(overview.status_label, t('statusIdle'))}</div>
                  </div>
                  <div data-id="agent-inspector-auto-5" className="mt-3 min-w-0">
                    <div data-id="agent-inspector-overview-card-model-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">{t('overviewModel')}</div>
                    <div data-id="agent-inspector-overview-card-model-value" className="mt-1 break-all text-sm font-medium leading-5 text-zinc-100">
                      {compactText(overview.model, t('modelUnknown'))}
                    </div>
                    {formatProviderLabel(overview.provider) ? (
                      <div data-id="agent-inspector-overview-card-model-meta" className="mt-1 text-[11px] text-zinc-500">
                        {formatProviderLabel(overview.provider)}
                      </div>
                    ) : null}
                  </div>
                </div>

                <div data-id="agent-inspector-auto-6" className="grid grid-cols-2 gap-2">
                  <div data-id="agent-inspector-overview-card-input-tokens" className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div data-id="agent-inspector-overview-card-input-tokens-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">In</div>
                    <div data-id="agent-inspector-overview-card-input-tokens-value" className="mt-1 text-sm font-medium text-zinc-100">{formatTokens(displayInputTokens)}</div>
                  </div>
                  <div data-id="agent-inspector-auto-7" className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div data-id="agent-inspector-auto-8" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">Out</div>
                    <div data-id="agent-inspector-auto-9" className="mt-1 text-sm font-medium text-zinc-100">{formatTokens(displayOutputTokens)}</div>
                  </div>
                  <div data-id="agent-inspector-overview-card-total-tokens" className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div data-id="agent-inspector-overview-card-total-tokens-label" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">Total</div>
                    <div data-id="agent-inspector-overview-card-total-tokens-value" className="mt-1 text-sm font-medium text-zinc-100">{formatTokens(displayTotalTokens)}</div>
                  </div>
                  <div data-id="agent-inspector-auto-10" className="rounded-xl bg-white/[0.02] px-3 py-2.5">
                    <div data-id="agent-inspector-auto-11" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">Cost</div>
                    <div data-id="agent-inspector-auto-12" className="mt-1 text-sm font-medium text-zinc-100">
                      {formatCostEstimate(displayCostCredit)}
                    </div>
                  </div>
                </div>
              </div>

              <div data-id="agent-inspector-overview-notes-section" className="space-y-2 px-1">
                <div data-id="agent-inspector-auto-13" className="flex items-center justify-between gap-3 text-[11px] text-zinc-500">
                  <span data-id="agent-inspector-overview-notes-label" className="uppercase tracking-[0.14em]">{t('notesLabel')}</span>
                  <span data-id="agent-inspector-overview-notes-updated" className="truncate">
                    {notesSaving ? t('notesSaving') : formatTime(data?.notes?.updated_at)}
                  </span>
                </div>
                <textarea
                  data-id="agent-inspector-overview-notes"
                  value={notesDraft}
                  onChange={(event) => setNotesDraft(event.target.value)}
                  onBlur={() => { void saveNotes(); }}
                  rows={8}
                  placeholder={t('notesPlaceholder')}
                  className="w-full resize-none rounded-lg bg-[#101114] px-3 py-2.5 text-sm leading-6 text-zinc-100 outline-none placeholder:text-zinc-600"
                />
              </div>


            </div>
          )}

          {tab === 'history' && (
            <>
              <div data-id="agent-inspector-history-list" className="pb-4">
                {(history.items || []).length === 0 ? (
                  <EmptyState text={history.reason || (query ? t('historyNoMatch') : t('historyEmpty'))} />
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
                            fallback={t('fallbackNone')}
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
                            fallback={t('fallbackNone')}
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
                  <div data-id="agent-inspector-auto-14" className="flex items-center gap-1.5">
                    <button
                      type="button"
                      data-id="agent-inspector-history-pagination-prev"
                      disabled={(history.offset || 0) <= 0}
                      onClick={() => setHistoryOffset((value) => Math.max(0, value - HISTORY_PAGE_SIZE))}
                      className="rounded-md bg-white/[0.04] px-2 py-1 text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:cursor-not-allowed disabled:opacity-30"
                    >
                      {t('historyPrev')}
                    </button>
                    <button
                      type="button"
                      data-id="agent-inspector-history-pagination-next"
                      disabled={!history.has_more}
                      onClick={() => setHistoryOffset((value) => value + HISTORY_PAGE_SIZE)}
                      className="rounded-md bg-white/[0.04] px-2 py-1 text-zinc-300 transition-colors hover:bg-white/[0.08] disabled:cursor-not-allowed disabled:opacity-30"
                    >
                      {t('historyNext')}
                    </button>
                  </div>
                </div>
              ) : null}
            </>
          )}

          {tab === 'memory' && (
            <div data-id="agent-inspector-memory-tab" className="space-y-4">
              <div data-id="agent-inspector-memory-summary" className="space-y-1 rounded-lg bg-[#101114] px-3 py-2.5">
                <div data-id="agent-inspector-auto-15" className="flex items-center gap-2 text-[11px] uppercase tracking-[0.14em] text-zinc-500">
                  <Brain className="h-3.5 w-3.5" />
                  Prompt Rules
                </div>
                <div data-id="agent-inspector-auto-16" className="text-[12px] leading-5 text-zinc-400">
                  <Trans i18nKey="promptRulesIntro" ns="agentInspector" components={{ code: <code /> }} />
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
                    <span data-id="agent-inspector-auto-17">{t(item.labelKey)}</span>
                  </button>
                ))}
              </div>
              <div data-id="agent-inspector-memory-rules" className="space-y-3">
                {memorySection === 'global' && (
                  <PromptRuleEditor
                    dataId="agent-inspector-memory-global"
                    title="Global"
                    subtitle={t('globalSharedHint')}
                    value={promptRulesDraft.global}
                    placeholder={t('globalPlaceholder')}
                    onChange={(patch) => patchPromptRule('global', patch)}
                  />
                )}
                {memorySection === 'agent' && (
                  <PromptRuleEditor
                    dataId="agent-inspector-memory-agent"
                    title="Agent"
                    subtitle={promptRulesDraft.agent.label || paneId}
                    value={promptRulesDraft.agent}
                    placeholder={t('agentPlaceholder')}
                    onChange={(patch) => patchPromptRule('agent', patch)}
                  />
                )}
                {memorySection === 'rules-files' && (() => {
                  const rf = data?.rules_files || {};
                  const items = Array.isArray(rf.items) ? rf.items : [];
                  const totalBytes = Number(rf.total_bytes || 0);
                  const globalOn = data?.inject_rules_global !== false;
                  const paneOn = data?.inject_rules_files !== false;
                  return (
                    <div data-id="agent-inspector-memory-rules-files" className="space-y-3">
                      <div data-id="agent-inspector-rules-files-summary" className="rounded-lg bg-[#101114] px-3 py-2.5 text-[12px] leading-5 text-zinc-400">
                        <div className="mb-1 text-[11px] uppercase tracking-[0.14em] text-zinc-500">{t('memoryRulesFilesTitle')}</div>
                        <div className="flex flex-wrap items-center gap-3 text-[12px]">
                          <span>{t('memoryRulesFilesSummary', { count: items.length, bytes: totalBytes })}</span>
                          <span className={globalOn ? 'text-emerald-400' : 'text-zinc-500'}>{t('memoryRulesFilesGlobal', { state: globalOn ? 'on' : 'off' })}</span>
                          <button
                            type="button"
                            onClick={async () => {
                              try {
                                await apiService.updatePane(paneId, { inject_rules_files: paneOn ? 0 : 1 });
                                setRefreshNonce((n) => n + 1);
                              } catch { /* ignored */ }
                            }}
                            className={`rounded-md px-2 py-1 text-[11px] transition-colors ${paneOn ? 'bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25' : 'bg-white/[0.06] text-zinc-400 hover:bg-white/[0.1]'}`}
                          >
                            {t('memoryRulesFilesPane', { state: paneOn ? 'on' : 'off' })}
                          </button>
                        </div>
                      </div>
                      {items.length === 0 ? (
                        <div className="rounded-lg bg-[#101114] px-3 py-3 text-[12px] text-zinc-500">
                          {t('memoryRulesFilesEmpty')}
                        </div>
                      ) : (
                        <div className="space-y-1.5">
                          {items.map((item: any, idx: number) => {
                            const isExpanded = expandedRulesFile === item.source;
                            return (
                              <div
                                key={`${item.source}-${idx}`}
                                className="rounded-md bg-[#101114] text-[12px] text-zinc-400 overflow-hidden"
                              >
                                <button
                                  type="button"
                                  className="w-full px-3 py-2 text-left hover:bg-white/[0.03] transition-colors"
                                  onClick={() => setExpandedRulesFile(isExpanded ? null : item.source)}
                                >
                                  <div className="flex items-center justify-between gap-2">
                                    <span className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] uppercase tracking-[0.1em] text-zinc-400">{item.scope || 'rule'}</span>
                                    <span className="text-[11px] text-zinc-500">
                                      {item.injected || item.bytes || 0} B
                                      {item.truncated ? ` · ${t('memoryRulesFilesTruncated')}` : ''}
                                      <span className="ml-1.5 text-zinc-600">{isExpanded ? '▲' : '▼'}</span>
                                    </span>
                                  </div>
                                  <div className="mt-1 break-all font-mono text-[11px] text-zinc-300">{item.source}</div>
                                  {item.pane_id ? <div className="mt-0.5 text-[10px] text-zinc-500">pane: {item.pane_id}</div> : null}
                                </button>
                                {isExpanded && item.content ? (
                                  <pre className="border-t border-white/[0.06] px-3 py-2 max-h-[300px] overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-5 text-zinc-300">
                                    {item.content}
                                  </pre>
                                ) : null}
                              </div>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  );
                })()}
                {memorySection === 'preview' && (
                  <div data-id="agent-inspector-memory-overlay-preview" className="rounded-lg bg-[#101114] px-3 py-2.5 text-[12px] leading-5 text-zinc-400">
                    <div className="mb-1 flex items-center justify-between text-[11px] uppercase tracking-[0.14em] text-zinc-500">
                      <span>{t('memoryPreviewTitle')}</span>
                      {data?.overlay_full ? (
                        <button
                          type="button"
                          onClick={() => {
                            navigator.clipboard?.writeText(String(data.overlay_full)).catch(() => {});
                            window.dispatchEvent(new CustomEvent('show-toast', { detail: t('memoryPreviewCopied') }));
                          }}
                          className="rounded bg-white/[0.06] px-1.5 py-0.5 text-[10px] normal-case tracking-normal text-zinc-300 hover:bg-white/[0.1]"
                        >
                          {t('memoryPreviewCopy')}
                        </button>
                      ) : null}
                    </div>
                    {data?.overlay_full ? (
                      <pre className="max-h-[400px] overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-5 text-zinc-300">{String(data.overlay_full)}</pre>
                    ) : (
                      compactText(overview.overlay_preview, t('memoryPreviewEmpty'))
                    )}
                  </div>
                )}
              </div>
            </div>
          )}

          {tab === 'settings' && (
            <div data-id="agent-inspector-settings-tab" className="space-y-4">
              <div data-id="agent-inspector-settings-sections" className="scrollbar-zero flex gap-1 overflow-x-auto whitespace-nowrap">
                {settingsSections.map((item) => {
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
                      <span data-id="agent-inspector-auto-19">{t(item.labelKey)}</span>
                    </button>
                  );
                })}
              </div>

              {settingsSection === 'general' && (
                <div data-id="agent-inspector-settings-general" className="space-y-5">
                  <div data-id="agent-inspector-settings-general-identity" className="space-y-3 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3">
                    <InspectorField label={t('fieldTitle')} mutedLabel>
                      <InspectorInput value={settingsData?.title || ''} onChange={(value) => patchSettingsData({ title: value })} onBlur={() => { void saveSettings(); }} placeholder={t('fieldTitlePlaceholder')} />
                    </InspectorField>
                  </div>

                  <div data-id="agent-inspector-settings-general-behavior" className="space-y-3 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3">
                    <InspectorToggle
                      label={t('allowAllActions')}
                      desc={t('allowAllActionsHint')}
                      checked={!!settingsData?.allow_all_actions}
                      onChange={(value) => {
                        patchSettingsData({ allow_all_actions: value });
                        void saveSettings({ allow_all_actions: value });
                      }}
                    />
                  </div>

                </div>
              )}

              {settingsSection === 'general' && modelSettingsEnabled && (
                <div data-id="agent-inspector-settings-model" className="space-y-5">
                  {(['codex', 'claude', 'opencode'].includes(normalizedAgentType)) && (
                  <div data-id="agent-inspector-settings-model-custom-gateway" className="space-y-3 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3">
                    <InspectorToggle
                      label={t('customGateway')}
                      desc={t('customGatewayHint')}
                      checked={!!settingsData?.use_custom_gateway}
                      onChange={(value) => {
                        if (!value) {
                          // Gateway OFF: keep the configured provider/model so re-enabling restores it.
                          patchSettingsData({ use_custom_gateway: false });
                          void saveModelSettings({ use_custom_gateway: false });
                          return;
                        }
                        // Gateway ON: seed runtime_ai with the agent's default provider/model when
                        // unset; keep it untouched when the user already configured one.
                        const currentProvider = String(settingsData?.runtime_ai?.provider_name || '').trim();
                        const nextProvider = currentProvider || String(runtimeAIDefault?.provider_name || '').trim();
                        const currentModel = String(settingsData?.default_model || '').trim();
                        const nextModel = currentModel || String(runtimeAIDefault?.model || '').trim();
                        const nextRuntimeAi = nextProvider ? { provider_name: nextProvider } : (settingsData?.runtime_ai || null);
                        patchSettingsData({ use_custom_gateway: true, runtime_ai: nextRuntimeAi, default_model: nextModel });
                        void saveModelSettings({ use_custom_gateway: true, runtime_ai: nextRuntimeAi, default_model: nextModel });
                      }}
                    />
                  </div>
                  )}

                  {certNudgeRelevant && (
                  <div data-id="agent-inspector-settings-ca-nudge" className="space-y-2 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3">
                    <div className="flex items-center justify-between gap-2">
                      <div data-id="agent-inspector-settings-ca-nudge-title" className="text-sm font-medium text-zinc-100">{t('caNudgeTitle')}</div>
                      <button
                        data-id="agent-inspector-settings-ca-refresh"
                        onClick={() => void checkCaStatus()}
                        disabled={caChecking}
                        className="shrink-0 rounded-lg border border-white/10 px-2 py-1 text-[11px] text-zinc-300 transition-colors hover:bg-white/5 disabled:opacity-50"
                      >{caChecking ? t('caNudgeChecking') : t('caNudgeRefresh')}</button>
                    </div>
                    {caStatus?.installed ? (
                      <div data-id="agent-inspector-settings-ca-ok" className="flex items-center gap-2 rounded-xl border border-emerald-500/20 bg-emerald-500/[0.06] px-3 py-2 text-[11px] text-emerald-200/90">
                        <span>✓</span><span>{t('caNudgeInstalled')}</span>
                      </div>
                    ) : (
                      <div data-id="agent-inspector-settings-ca-missing" className="space-y-2 rounded-xl border border-amber-500/20 bg-amber-500/[0.06] px-3 py-2 text-[11px] leading-5 text-amber-200/90">
                        <div data-id="agent-inspector-settings-ca-missing-text">{t('caNudgeMissing')}</div>
                        <div className="flex items-center gap-2 rounded-lg bg-black/30 px-2 py-1 font-mono text-[11px] text-zinc-200">
                          <span data-id="agent-inspector-settings-ca-command" className="flex-1 break-all">{caStatus?.command || 'cicy-code mitm install-ca'}</span>
                          <button
                            data-id="agent-inspector-settings-ca-copy"
                            onClick={() => { try { void navigator.clipboard.writeText(caStatus?.command || 'cicy-code mitm install-ca'); } catch { /* noop */ } }}
                            className="shrink-0 rounded border border-white/10 px-1.5 py-0.5 text-[10px] text-zinc-300 hover:bg-white/5"
                          >{t('caNudgeCopy')}</button>
                        </div>
                        {caStatus?.platform === 'darwin' && (
                          <div data-id="agent-inspector-settings-ca-mac-note" className="text-[10px] text-amber-200/70">{t('caNudgeMacNote')}</div>
                        )}
                      </div>
                    )}
                  </div>
                  )}

                  {(
                    <div data-id="agent-inspector-settings-model-gateway-override" className={`space-y-3 rounded-2xl border border-white/[0.06] bg-white/[0.02] p-3 ${!settingsData?.use_custom_gateway ? 'opacity-50 pointer-events-none select-none' : ''}`}>
                      <div data-id="agent-inspector-settings-model-gateway-override-header">
                        <div data-id="agent-inspector-settings-model-gateway-override-title" className="text-sm font-medium text-zinc-100">{t('gatewayOverrideTitle')}</div>
                        <div data-id="agent-inspector-settings-model-gateway-override-desc" className="mt-1 text-xs leading-5 text-zinc-500">{t('gatewayOverrideDesc')}</div>
                      </div>
                      <InspectorField label={t('providerFieldLabel')} desc={t('providerFieldDesc')}>
                        <Select
                          value={settingsData?.runtime_ai?.provider_name || runtimeAIDefault?.provider_name || ''}
                          onChange={(value) => {
                            const nextRuntimeAi = { ...(settingsData?.runtime_ai || {}), provider_name: value };
                            // Switching provider: if the current model isn't in the new provider's list, reset to its first model
                            const newProvider = runtimeAIProviderOptions.find((p) => p.key === value);
                            const newProviderModels = newProvider?.models || [];
                            const currentModel = String(settingsData?.default_model || '').trim();
                            const keepModel = currentModel && newProviderModels.includes(currentModel) ? currentModel : (newProviderModels[0] || currentModel);
                            patchSettingsData({ runtime_ai: nextRuntimeAi, default_model: keepModel });
                            void saveModelSettings({ runtime_ai: nextRuntimeAi, default_model: keepModel });
                          }}
                          options={runtimeAISelectOptions}
                          placeholder={t('providerSelectPlaceholder')}
                          searchable
                          onOpenChange={(o) => { if (o) refreshDetailNow(); }}
                        />
                      </InspectorField>
                      <InspectorField label={t('modelDefaultFieldLabel')} desc={t('modelDefaultFieldDesc')}>
                        {(() => {
                          const activeProviderKey = String(settingsData?.runtime_ai?.provider_name || '').trim() || runtimeAIDefault?.provider_name || '';
                          const activeProvider = runtimeAIProviderOptions.find((p) => p.key === activeProviderKey);
                          const baseModels = activeProvider?.models || [];
                          const currentValue = settingsData?.default_model || runtimeAIDefault?.model || '';
                          const optionValues = currentValue && !baseModels.includes(currentValue)
                            ? [currentValue, ...baseModels]
                            : baseModels;
                          return (
                            <Select
                              searchable
                              placeholder={t('modelDefaultPlaceholder')}
                              value={currentValue}
                              options={optionValues.map((m) => ({ value: m, label: m }))}
                              onChange={(v) => { patchSettingsData({ default_model: v }); void saveModelSettings({ default_model: v }); }}
                              onOpenChange={(o) => { if (o) refreshDetailNow(); }}
                            />
                          );
                        })()}
                      </InspectorField>
                    </div>
                  )}

                  <div data-id="agent-inspector-settings-model-restart-hint" className="space-y-1.5">
                    <div data-id="agent-inspector-settings-model-restart-hint-gateway" className="flex items-start gap-2 rounded-xl border border-amber-500/20 bg-amber-500/[0.06] px-3 py-2 text-[11px] leading-5 text-amber-200/90">
                      <svg viewBox="0 0 16 16" fill="currentColor" className="mt-0.5 h-3.5 w-3.5 shrink-0">
                        <path d="M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13Zm.75 9.75a.75.75 0 1 1-1.5 0 .75.75 0 0 1 1.5 0ZM7.25 4.5a.75.75 0 0 1 1.5 0v4a.75.75 0 0 1-1.5 0v-4Z"/>
                      </svg>
                      <span>{t('modelRestartHintGateway')}</span>
                    </div>
                    <div data-id="agent-inspector-settings-model-restart-hint-live" className="flex items-start gap-2 rounded-xl border border-emerald-500/20 bg-emerald-500/[0.06] px-3 py-2 text-[11px] leading-5 text-emerald-200/90">
                      <svg viewBox="0 0 16 16" fill="currentColor" className="mt-0.5 h-3.5 w-3.5 shrink-0">
                        <path d="M13.78 4.22a.75.75 0 0 1 0 1.06l-6.5 6.5a.75.75 0 0 1-1.06 0l-3-3a.75.75 0 1 1 1.06-1.06L6.75 10.19l5.97-5.97a.75.75 0 0 1 1.06 0Z"/>
                      </svg>
                      <span>{t('modelRestartHintLive')}</span>
                    </div>
                  </div>
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
      <span data-id="agent-inspector-auto-21" className="text-zinc-500">{label}</span>
      <span data-id="agent-inspector-auto-22" className="max-w-[180px] text-right text-zinc-200">{value}</span>
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
  const { t } = useTranslation('agentInspector');
  const unavailable = disabled || value.available === false;
  return (
    <section data-id={dataId} className="space-y-2 rounded-lg bg-[#101114] px-3 py-2.5">
      <div data-id="agent-inspector-auto-23" className="flex items-start justify-between gap-3">
        <div data-id="agent-inspector-auto-24" className="min-w-0">
          <div data-id="agent-inspector-auto-25" className="text-[12px] font-medium text-zinc-100">{title}</div>
          <div data-id="agent-inspector-auto-26" className="truncate text-[11px] text-zinc-500">{subtitle || '--'}</div>
        </div>
        <div data-id="agent-inspector-auto-27" className="shrink-0 text-[11px] text-zinc-600">{formatTime(value.updated_at)}</div>
      </div>
      <div data-id="agent-inspector-auto-28" className="grid grid-cols-2 gap-2">
        <CompactToggle
          labelKey="promptRuleEnable"
          checked={!!value.enabled}
          disabled={unavailable}
          onChange={(checked) => onChange({ enabled: checked })}
        />
        <CompactToggle
          labelKey="promptRuleInject"
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
        placeholder={unavailable ? t('promptRuleUnavailable', { ns: 'agentInspector' }) : placeholder}
        className="w-full resize-none rounded-lg bg-[#09090b] px-3 py-2.5 text-sm leading-6 text-zinc-100 outline-none placeholder:text-zinc-600 disabled:cursor-not-allowed disabled:text-zinc-600"
      />
    </section>
  );
}

function CompactToggle({
  labelKey,
  checked,
  onChange,
  disabled,
}: {
  labelKey: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation('agentInspector');
  return (
    <button data-id="agent-inspector-auto-29"
      type="button"
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className="flex items-center justify-between rounded-lg bg-[#09090b] px-2.5 py-2 text-left text-[12px] text-zinc-300 disabled:cursor-not-allowed disabled:opacity-40"
    >
      <span data-id="agent-inspector-auto-30">{t(labelKey)}</span>
      <span data-id="agent-inspector-auto-31" className={`relative h-5 w-9 rounded-full transition-colors ${checked ? 'bg-blue-600' : 'bg-white/[0.08]'}`}>
        <span data-id="agent-inspector-auto-32" className={`absolute top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${checked ? 'translate-x-4' : 'translate-x-0.5'}`} />
      </span>
    </button>
  );
}

function InspectorField({
  label,
  desc,
  mono,
  mutedLabel,
  children,
}: {
  label: string;
  desc?: string;
  mono?: boolean;
  mutedLabel?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div data-id="agent-inspector-auto-33">
      <label data-id="agent-inspector-auto-34" className={`mb-1.5 block ${mutedLabel ? 'text-[11px] font-normal text-zinc-500' : 'text-[13px] font-medium text-zinc-300'} ${mono ? 'font-mono' : ''}`}>{label}</label>
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
