import { useEffect, useMemo, useState } from 'react';
import { Brain, Languages, Wrench } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import apiService from '../../services/api';
import { Spinner } from '../ui/Spinner';

export type RequestViewTab = 'tools' | 'brain' | 'meta' | 'usage' | 'analysis';
type BrainInnerTab = 'instructions' | 'developer';
type TranslationEntry = { translated: string[]; loading: boolean; source: string };
type TranslationState = Record<string, TranslationEntry>;

function formatTime(value?: string) {
  if (!value) return '';
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return value;
  return new Date(parsed).toLocaleString(undefined, { hour12: false });
}

function compactText(value: unknown, fallback = '', limit?: number) {
  const text = String(value || '').trim();
  if (!text) return fallback;
  if (typeof limit === 'number' && limit > 0) {
    const chars = Array.from(text);
    if (chars.length > limit) return `${chars.slice(0, limit).join('')}...`;
  }
  return text;
}

function splitTextForTranslation(text: string) {
  const normalized = String(text || '').replace(/\r\n/g, '\n').trim();
  if (!normalized) return [] as string[];
  return normalized
    .split(/\n{2,}/)
    .map((part) => part.trim())
    .filter(Boolean);
}

function renderMetaItems(items: any[]) {
  return (
    <div data-id="agent-provider-request-meta-items" className="space-y-2">
      {items.map((item: any, index: number) => (
        <div key={index} data-id={`agent-provider-request-meta-item-${index}`} className="flex items-start justify-between gap-3 rounded-lg bg-[#101114] px-3 py-2 text-[12px] leading-5">
          <div data-id={`agent-provider-request-meta-item-key-${index}`} className="shrink-0 text-zinc-500">{compactText(item?.key, '--')}</div>
          <div data-id={`agent-provider-request-meta-item-value-${index}`} className="min-w-0 flex-1 break-all text-right text-zinc-300">{compactText(item?.value, '--', 400)}</div>
        </div>
      ))}
    </div>
  );
}

function renderTranslateToggleButton(baseId: string, translationState: TranslationEntry | undefined, onTranslate: () => void) {
  const safeId = String(baseId || 'translation').replace(/[^a-zA-Z0-9_-]/g, '-');
  const translated = translationState?.translated || [];
  const loading = !!translationState?.loading;
  const hasTranslation = translated.some((part) => String(part || '').trim());
  // Icon-only: the label lives in the tooltip / aria-label, not on screen.
  const label = loading
    ? i18n.t('translating', { ns: 'agentProviderRequest' })
    : hasTranslation
      ? i18n.t('hideTranslation', { ns: 'agentProviderRequest' })
      : i18n.t('translate', { ns: 'agentProviderRequest' });
  return (
    <button
      type="button"
      data-id={`${safeId}-translate`}
      onClick={onTranslate}
      disabled={loading}
      title={label}
      aria-label={label}
      className={`inline-flex items-center justify-center rounded-md p-1 transition-colors disabled:opacity-50 ${hasTranslation ? 'bg-emerald-500/12 text-emerald-300 hover:bg-emerald-500/18' : 'bg-white/[0.06] text-zinc-300 hover:bg-white/[0.10]'}`}
    >
      {loading ? <Spinner size="xs" /> : <Languages className="h-3.5 w-3.5" />}
    </button>
  );
}

function renderTranslatedParagraphs(baseId: string, content: string, translationState: TranslationEntry | undefined, onTranslate?: () => void, showToggle = true) {
  const safeId = String(baseId || 'translation').replace(/[^a-zA-Z0-9_-]/g, '-');
  const parts = splitTextForTranslation(content);
  const translated = translationState?.translated || [];
  if (!parts.length) {
    return null;
  }
  return (
    <div data-id={`${safeId}-blocks`} className="space-y-3">
      {showToggle && onTranslate ? <div data-id={`${safeId}-actions`} className="flex justify-end">{renderTranslateToggleButton(safeId, translationState, onTranslate)}</div> : null}
      {parts.map((part, index) => (
        <div key={index} data-id={`${safeId}-block-${index}`} className="space-y-2">
          <div data-id={`${safeId}-original-${index}`} className="whitespace-pre-wrap break-words text-[12px] leading-5 text-zinc-300">{part}</div>
          {translated[index] ? (
            <div data-id={`${safeId}-translation-wrap-${index}`} className="rounded-lg border border-emerald-400/15 bg-emerald-400/[0.04] px-3 py-2.5">
              <div data-id={`${safeId}-translation-label-${index}`} className="mb-1 text-[10px] uppercase tracking-[0.14em] text-emerald-300/80">{i18n.t('translationLabel', { ns: 'agentProviderRequest' })}</div>
              <div data-id={`${safeId}-translation-${index}`} className="whitespace-pre-wrap break-words text-[12px] leading-5 text-zinc-300">{translated[index]}</div>
            </div>
          ) : null}
        </div>
      ))}
    </div>
  );
}

function renderBrainCard(title: string, text: string, translationState: TranslationEntry | undefined, onTranslate: () => void, cardId: string) {
  return (
    <div data-id={cardId} className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3">
      <div data-id={`${cardId}-header`} className="mb-2 flex items-center justify-between gap-3 text-[11px] uppercase tracking-[0.14em] text-zinc-500">
        <div data-id="agent-provider-request-view-auto-1" className="flex items-center gap-2">
          <Brain data-id={`${cardId}-icon`} className="h-3.5 w-3.5" />
          <span data-id={`${cardId}-title`}>{title}</span>
        </div>
        {renderTranslateToggleButton(cardId, translationState, onTranslate)}
      </div>
      {renderTranslatedParagraphs(cardId, text, translationState, undefined, false)}
    </div>
  );
}

function renderPromptItems(items: any[], translationState: TranslationState, onTranslate: (key: string, text: string) => void) {
  return (
    <div data-id="agent-provider-request-prompt-items" className="space-y-2">
      {items.map((item: any, index: number) => {
        const cardId = `agent-provider-request-prompt-item-${index}`;
        const translationKey = `brain:prompt-item:${index}`;
        const text = compactText(item?.text, i18n.t('fallbackNone', { ns: 'agentProviderRequest' }));
        return (
          <div key={index} data-id={cardId} className="rounded-lg bg-[#101114] px-3 py-2.5 text-[12px] leading-5 text-zinc-300">
            <div data-id={`agent-provider-request-prompt-item-header-${index}`} className="mb-1 flex items-center justify-between gap-3 text-[11px] text-zinc-500">
              <div data-id="agent-provider-request-view-auto-2" className="flex items-center gap-2">
                <span data-id={`agent-provider-request-prompt-item-index-${index}`} className="rounded bg-white/[0.05] px-1.5 py-0.5">#{typeof item?.index === 'number' ? item.index : index}</span>
                {item?.part_type ? <span data-id={`agent-provider-request-prompt-item-type-${index}`} className="rounded bg-white/[0.05] px-1.5 py-0.5">{compactText(item.part_type, 'text')}</span> : null}
              </div>
              {renderTranslateToggleButton(cardId, translationState[translationKey], () => onTranslate(translationKey, text))}
            </div>
            {renderTranslatedParagraphs(cardId, text, translationState[translationKey], undefined, false)}
          </div>
        );
      })}
    </div>
  );
}

function renderDeveloperCards(items: any[], translationState: TranslationState, onTranslate: (key: string, text: string) => void) {
  return (
    <div data-id="agent-provider-request-developer-items" className="space-y-2">
      {items.map((item: any, index: number) => {
        const cardId = `agent-provider-request-developer-item-${index}`;
        const translationKey = `brain:developer-item:${index}`;
        const text = compactText(item?.text, i18n.t('fallbackNone', { ns: 'agentProviderRequest' }));
        return (
          <div key={index} data-id={cardId} className="rounded-lg bg-[#101114] px-3 py-2.5 text-[12px] leading-5 text-zinc-300">
            <div data-id="agent-provider-request-view-auto-3" className="mb-1 flex items-center justify-between gap-3">
              <div data-id={`agent-provider-request-developer-item-role-${index}`} className="text-[11px] text-zinc-500">{compactText(item?.role, 'developer')}</div>
              {renderTranslateToggleButton(cardId, translationState[translationKey], () => onTranslate(translationKey, text))}
            </div>
            {renderTranslatedParagraphs(cardId, text, translationState[translationKey], undefined, false)}
          </div>
        );
      })}
    </div>
  );
}

function renderToolProperty(item: any, requiredNames: string[], translatedDescription?: string) {
  const name = compactText(item?.name, '--');
  const safeName = String(item?.name || 'tool').replace(/[^a-zA-Z0-9_-]/g, '-');
  const isRequired = requiredNames.includes(String(item?.name || ''));
  const description = String(item?.description || '').trim();
  return (
    <div key={name} data-id={`agent-provider-request-tool-property-${safeName}`} className="rounded-lg bg-[#101114] px-3 py-2.5 text-[12px] leading-5 text-zinc-300">
      <div data-id={`agent-provider-request-tool-property-header-${safeName}`} className="mb-1 flex items-center gap-2 text-[11px] text-zinc-500">
        <span data-id={`agent-provider-request-tool-property-name-${safeName}`} className="rounded bg-white/[0.05] px-1.5 py-0.5">{name}</span>
        {item?.type ? <span data-id={`agent-provider-request-tool-property-type-${safeName}`}>{compactText(item.type, '')}</span> : null}
        {isRequired ? <span data-id={`agent-provider-request-tool-property-required-${safeName}`} className="text-amber-300">required</span> : <span data-id={`agent-provider-request-tool-property-optional-${safeName}`}>optional</span>}
      </div>
      {description ? <div data-id={`agent-provider-request-tool-property-description-${safeName}`} className="mb-1 text-zinc-400">{compactText(description, '', 400)}</div> : null}
      {translatedDescription ? (
        <div data-id={`agent-provider-request-tool-property-translation-wrap-${safeName}`} className="mb-1 rounded-lg border border-emerald-400/15 bg-emerald-400/[0.04] px-3 py-2.5">
          <div data-id={`agent-provider-request-tool-property-translation-label-${safeName}`} className="mb-1 text-[10px] uppercase tracking-[0.14em] text-emerald-300/80">{i18n.t('translationLabel', { ns: 'agentProviderRequest' })}</div>
          <div data-id={`agent-provider-request-tool-property-translation-${safeName}`} className="whitespace-pre-wrap break-words text-[12px] leading-5 text-zinc-300">{translatedDescription}</div>
        </div>
      ) : null}
      {Array.isArray(item?.enum) && item.enum.length > 0 ? <div data-id={`agent-provider-request-tool-property-enum-${safeName}`} className="text-zinc-500">enum: {item.enum.join(', ')}</div> : null}
      {item?.items?.type ? <div data-id={`agent-provider-request-tool-property-items-${safeName}`} className="text-zinc-500">items: {compactText(item.items.type, '--')}</div> : null}
      {typeof item?.additional_properties !== 'undefined' ? <div data-id={`agent-provider-request-tool-property-additional-${safeName}`} className="text-zinc-500">additionalProperties: {String(item.additional_properties)}</div> : null}
    </div>
  );
}

function renderToolDetail(tool: any, summaryTranslationState: TranslationState[string] | undefined, parameterTranslationState: TranslationState[string] | undefined, onTranslateSummary: (tool: any) => void, onTranslateParameters: (tool: any) => void) {
  const requiredNames = Array.isArray(tool?.required) ? tool.required : [];
  const properties = Array.isArray(tool?.properties) ? tool.properties : [];
  const parameterDescriptions = properties.map((item: any) => String(item?.description || '').trim()).filter(Boolean);
  const safeName = String(tool?.name || 'tool').replace(/[^a-zA-Z0-9_-]/g, '-');
  let propertyTranslationIndex = 0;
  return (
    <div data-id={`agent-provider-request-tool-detail-${safeName}`} className="space-y-3">
      <div data-id={`agent-provider-request-tool-summary-${safeName}`} className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3">
        <div data-id={`agent-provider-request-tool-summary-header-${safeName}`} className="mb-2 flex items-center justify-between gap-3 text-[11px] uppercase tracking-[0.14em] text-zinc-500">
          <div data-id="agent-provider-request-view-auto-4" className="flex items-center gap-2">
            <Wrench data-id={`agent-provider-request-tool-summary-icon-${safeName}`} className="h-3.5 w-3.5" />
            <span data-id={`agent-provider-request-tool-summary-title-${safeName}`}>Summary</span>
          </div>
          {String(tool?.description || '').trim() ? renderTranslateToggleButton(`agent-provider-request-tool-summary-${safeName}`, summaryTranslationState, () => onTranslateSummary(tool)) : null}
        </div>
        <div data-id={`agent-provider-request-tool-name-${safeName}`} className="text-sm font-medium text-zinc-100">{compactText(tool?.name, 'tool')}</div>
        {renderTranslatedParagraphs(`agent-provider-request-tool-summary-${safeName}`, String(tool?.description || '').trim(), summaryTranslationState, undefined, false)}
      </div>

      <div data-id={`agent-provider-request-tool-schema-${safeName}`} className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3">
        <div data-id={`agent-provider-request-tool-schema-title-${safeName}`} className="mb-2 text-[11px] uppercase tracking-[0.14em] text-zinc-500">Schema</div>
        <div data-id={`agent-provider-request-tool-schema-body-${safeName}`} className="space-y-1 text-[12px] leading-5 text-zinc-300">
          <div data-id={`agent-provider-request-tool-schema-type-${safeName}`}>type: {compactText(tool?.schema_type, '--')}</div>
          <div data-id={`agent-provider-request-tool-schema-properties-${safeName}`}>properties: {typeof tool?.property_count === 'number' ? tool.property_count : 0}</div>
          <div data-id={`agent-provider-request-tool-schema-required-${safeName}`}>required: {requiredNames.length ? requiredNames.join(', ') : '--'}</div>
          {typeof tool?.additional_properties !== 'undefined' ? <div data-id={`agent-provider-request-tool-schema-additional-${safeName}`}>additionalProperties: {String(tool.additional_properties)}</div> : null}
        </div>
      </div>

      <div data-id={`agent-provider-request-tool-parameters-${safeName}`} className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3">
        <div data-id={`agent-provider-request-tool-parameters-header-${safeName}`} className="mb-2 flex items-center justify-between gap-3">
          <div data-id={`agent-provider-request-tool-parameters-title-${safeName}`} className="text-[11px] uppercase tracking-[0.14em] text-zinc-500">Parameters</div>
          {parameterDescriptions.length ? renderTranslateToggleButton(`agent-provider-request-tool-parameters-${safeName}`, parameterTranslationState, () => onTranslateParameters(tool)) : null}
        </div>
        {properties.length ? <div data-id={`agent-provider-request-tool-parameters-list-${safeName}`} className="space-y-2">{properties.map((item: any) => {
          const hasDescription = String(item?.description || '').trim();
          const translatedDescription = hasDescription ? parameterTranslationState?.translated?.[propertyTranslationIndex++] : undefined;
          return renderToolProperty(item, requiredNames, translatedDescription);
        })}</div> : <div data-id={`agent-provider-request-tool-parameters-empty-${safeName}`} className="text-sm text-zinc-500">{i18n.t('noParameters', { ns: 'agentProviderRequest' })}</div>}
      </div>
    </div>
  );
}

function renderToolsWorkspace(items: any[], query: string, selectedName: string | null, onSelect: (name: string) => void, translationState: TranslationState, onTranslateSummary: (tool: any) => void, onTranslateParameters: (tool: any) => void) {
  const filtered = items.filter((item: any) => {
    const haystack = `${item?.name || ''}\n${item?.summary || ''}\n${item?.description || ''}`.toLowerCase();
    return !query || haystack.includes(query.toLowerCase());
  });
  const selected = filtered.find((item: any) => item?.name === selectedName) || filtered[0] || null;
  return (
    <div data-id="agent-provider-request-tools-workspace" className="min-h-full">
      <div
        data-id="agent-provider-request-tools-sidebar"
        className="absolute overflow-y-auto rounded-xl border border-white/[0.06] bg-white/[0.02] p-3"
        style={{ position: 'absolute', left: 12, top: 12, bottom: 12, width: 200 }}
      >
        <div data-id="agent-provider-request-tools-list" className="space-y-2">
          {filtered.map((item: any) => {
            const active = selected?.name === item?.name;
            const safeName = String(item?.name || 'tool').replace(/[^a-zA-Z0-9_-]/g, '-');
            return (
              <button
                key={item?.name}
                type="button"
                data-id={`agent-provider-request-tool-item-${safeName}`}
                onClick={() => onSelect(String(item?.name || ''))}
                className={`w-full rounded-lg border px-3 py-2 text-left transition-colors ${active ? 'border-white/[0.14] bg-white/[0.08] text-zinc-100' : 'border-white/[0.06] bg-[#101114] text-zinc-300 hover:bg-white/[0.04]'}`}
              >
                <div data-id={`agent-provider-request-tool-item-name-${safeName}`} className="text-[12px] font-medium">{compactText(item?.name, 'tool')}</div>
                <div data-id={`agent-provider-request-tool-item-meta-${safeName}`} className="mt-1 text-[11px] text-zinc-500">{typeof item?.property_count === 'number' ? item.property_count : 0} props · {Array.isArray(item?.required) ? item.required.length : 0} required</div>
              </button>
            );
          })}
        </div>
      </div>
      <div
        data-id="agent-provider-request-tools-detail"
        className="overflow-y-auto"
        style={{ position: 'absolute', left: 224, right: 12, top: 12, bottom: 12 }}
      >
        {selected ? renderToolDetail(selected, translationState[`tool:${String(selected?.name || '')}`], translationState[`tool-params:${String(selected?.name || '')}`], onTranslateSummary, onTranslateParameters) : <div data-id="agent-provider-request-tools-detail-empty" className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3 text-sm text-zinc-500">{i18n.t('noTools', { ns: 'agentProviderRequest' })}</div>}
      </div>
    </div>
  );
}

export default function AgentProviderRequestView({
  paneId,
  open,
  tab,
}: {
  paneId: string;
  open: boolean;
  tab: RequestViewTab;
  // Still accepted from callers for API compatibility, but intentionally
  // unused: the snapshot is navigation-driven, not refreshed per chat turn.
  inspectorVersion?: number;
}) {
  const { t } = useTranslation('agentProviderRequest');
  const [data, setData] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [brainTab, setBrainTab] = useState<BrainInnerTab>('instructions');
  const [selectedToolName, setSelectedToolName] = useState<string | null>(null);
  const [translationState, setTranslationState] = useState<TranslationState>({});

  // Only fetch the provider-request snapshot when the user navigates to a view
  // that needs it (open becomes true for the tools/brain/meta tabs) or switches
  // agent (paneId changes). It deliberately does NOT depend on inspectorVersion:
  // the server bumps that on every status_change / current_updated / ai_done, so
  // depending on it fired a fresh GET /api/agents/inspector/:id (×3 at end-of-turn)
  // throughout a live conversation. The snapshot is navigation-driven, not live.
  useEffect(() => {
    if (!open || !paneId) return;
    let cancelled = false;
    setLoading(true);
    apiService.getAgentInspector(paneId)
      .then(({ data: next }) => {
        if (cancelled) return;
        setData(next?.provider_request_view || null);
      })
      .catch(() => {
        if (cancelled) return;
        setData(null);
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, paneId]);

  const sections = useMemo(() => Array.isArray(data?.sections) ? data.sections : [], [data]);
  const promptSection = useMemo(() => sections.find((section: any) => section?.type === 'prompt') || null, [sections]);
  const developerSection = useMemo(() => sections.find((section: any) => section?.type === 'developer_messages') || null, [sections]);
  const toolSection = useMemo(() => sections.find((section: any) => section?.type === 'tools') || null, [sections]);
  const metaSection = useMemo(() => sections.find((section: any) => section?.type === 'meta') || null, [sections]);
  const isOpenAIStyle = data?.request_kind === 'openai_responses';
  const toolItems = Array.isArray(toolSection?.items) ? toolSection.items : [];

  useEffect(() => {
    if (!isOpenAIStyle) {
      setBrainTab('instructions');
      return;
    }
    if (brainTab === 'developer' && !developerSection?.items?.length) {
      setBrainTab('instructions');
    }
  }, [brainTab, developerSection, isOpenAIStyle]);

  useEffect(() => {
    if (!toolItems.length) {
      setSelectedToolName(null);
      return;
    }
    if (!selectedToolName || !toolItems.some((item: any) => item?.name === selectedToolName)) {
      setSelectedToolName(String(toolItems[0]?.name || ''));
    }
  }, [selectedToolName, toolItems]);

  const handleTranslateContent = async (key: string, sourceText: string, errorMessage?: string) => {
    const source = String(sourceText || '').trim();
    const parts = splitTextForTranslation(source);
    if (!key || parts.length === 0) return;
    const current = translationState[key];
    if (current?.loading) {
      return;
    }
    if (current?.source === source && current?.translated?.some((part) => String(part || '').trim())) {
      setTranslationState((prev) => ({
        ...prev,
        [key]: {
          translated: [],
          loading: false,
          source,
        },
      }));
      return;
    }
    const translatedParts = new Array(parts.length).fill('');
    setTranslationState((prev) => ({
      ...prev,
      [key]: {
        translated: translatedParts,
        loading: true,
        source,
      },
    }));
    try {
      for (let index = 0; index < parts.length; index += 1) {
        const part = parts[index];
        const { data } = await apiService.translateText(part, 'zh-CN');
        translatedParts[index] = String(data?.text || '').trim();
        setTranslationState((prev) => ({
          ...prev,
          [key]: {
            translated: [...translatedParts],
            loading: true,
            source,
          },
        }));
      }
      setTranslationState((prev) => ({
        ...prev,
        [key]: {
          translated: [...translatedParts],
          loading: false,
          source,
        },
      }));
    } catch {
      setTranslationState((prev) => ({
        ...prev,
        [key]: {
          translated: prev[key]?.source === source && prev[key]?.translated?.length ? prev[key].translated : [...translatedParts],
          loading: false,
          source,
        },
      }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: errorMessage || t('errorTranslate') }));
    }
  };

  const handleTranslateTool = async (tool: any) => {
    const name = String(tool?.name || '');
    if (!name) return;
    await handleTranslateContent(`tool:${name}`, String(tool?.description || ''), t('errorTranslateToolSummary'));
  };

  const handleTranslateToolParameters = async (tool: any) => {
    const name = String(tool?.name || '');
    if (!name) return;
    const source = (Array.isArray(tool?.properties) ? tool.properties : [])
      .map((item: any) => String(item?.description || '').trim())
      .filter(Boolean)
      .join('\n\n');
    await handleTranslateContent(`tool-params:${name}`, source, t('errorTranslateParameters'));
  };

  if (loading) {
    return (
      <div data-id="agent-provider-request-loading" className="flex h-full items-center justify-center gap-2 text-sm text-zinc-500">
        <Spinner size="sm" />
        {t('loading')}
      </div>
    );
  }

  if (!data || sections.length === 0) {
    return (
      <div data-id="agent-provider-request-empty" className="flex h-full items-center justify-center text-sm text-zinc-500">
        {t('noRequestView')}
      </div>
    );
  }

  return (
    <div
      data-id="agent-provider-request-view"
      className="relative min-h-0 overflow-hidden px-3 py-3"
      style={{ height: 'calc(100vh - 48px)' }}
    >
      {tab === 'tools' ? (
        <div data-id="agent-provider-request-tools-panel" className="h-full overflow-hidden">
          {toolItems.length ? renderToolsWorkspace(toolItems, '', selectedToolName, setSelectedToolName, translationState, handleTranslateTool, handleTranslateToolParameters) : <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3 text-sm text-zinc-500">{t('noTools')}</div>}
        </div>
      ) : null}

      {tab === 'brain' ? (
        <div data-id="agent-provider-request-brain-panel" className="h-full overflow-y-auto space-y-3">
          {isOpenAIStyle && (promptSection || developerSection?.items?.length) ? (
            <div data-id="agent-provider-request-brain-tabs" className="flex gap-1 overflow-x-auto whitespace-nowrap scrollbar-none">
              <button
                type="button"
                data-id="agent-provider-request-brain-tab-instructions"
                onClick={() => setBrainTab('instructions')}
                className={`flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 text-[11px] leading-5 transition-colors ${
                  brainTab === 'instructions' ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                }`}
              >
                Instructions
              </button>
              <button
                type="button"
                data-id="agent-provider-request-brain-tab-developer"
                onClick={() => setBrainTab('developer')}
                className={`flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 text-[11px] leading-5 transition-colors ${
                  brainTab === 'developer' ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.04] hover:text-zinc-300'
                }`}
              >
                Developer
              </button>
            </div>
          ) : null}

          {isOpenAIStyle ? (
            brainTab === 'developer'
              ? (developerSection?.items?.length ? renderDeveloperCards(developerSection.items, translationState, (key, text) => handleTranslateContent(key, text, t('errorTranslateDeveloper'))) : <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3 text-sm text-zinc-500">{t('noDeveloperContent')}</div>)
              : (promptSection?.items?.length ? renderPromptItems(promptSection.items, translationState, (key, text) => handleTranslateContent(key, text, t('errorTranslateInstructions'))) : (promptSection ? renderBrainCard(compactText(promptSection?.label, 'Instructions'), compactText(promptSection?.text, t('fallbackNone')), translationState['brain:instructions-card'], () => handleTranslateContent('brain:instructions-card', compactText(promptSection?.text, t('fallbackNone')), t('errorTranslateInstructions')), 'agent-provider-request-brain-card-instructions') : <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3 text-sm text-zinc-500">{t('noInstructionsContent')}</div>))
          ) : (
            promptSection || developerSection ? (
              <div data-id="agent-provider-request-view-auto-5" className="space-y-3">
                {promptSection?.items?.length ? renderPromptItems(promptSection.items, translationState, (key, text) => handleTranslateContent(key, text, t('errorTranslateBrain'))) : (promptSection ? renderBrainCard(compactText(promptSection?.label, 'Brain'), compactText(promptSection?.text, t('fallbackNone')), translationState['brain:prompt-card'], () => handleTranslateContent('brain:prompt-card', compactText(promptSection?.text, t('fallbackNone')), t('errorTranslateBrain')), 'agent-provider-request-brain-card-prompt') : null)}
                {developerSection?.items?.length ? renderDeveloperCards(developerSection.items, translationState, (key, text) => handleTranslateContent(key, text, t('errorTranslateDeveloper'))) : null}
              </div>
            ) : <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3 text-sm text-zinc-500">{t('noBrainContent')}</div>
          )}
        </div>
      ) : null}

      {tab === 'meta' ? (
        <div data-id="agent-provider-request-meta-panel" className="space-y-3">
          <div data-id="agent-provider-request-header" className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3">
            <div data-id="agent-provider-request-view-auto-6" className="text-[10px] uppercase tracking-[0.14em] text-zinc-500">{t('requestView')}</div>
            <div data-id="agent-provider-request-view-auto-7" className="mt-1 text-sm font-medium text-zinc-100">{compactText(data.model, t('unknownModel'))}</div>
            <div data-id="agent-provider-request-view-auto-8" className="mt-1 text-[11px] text-zinc-500">{compactText(data.provider, 'unknown')} · {compactText(data.request_kind, 'generic')}</div>
            <div data-id="agent-provider-request-view-auto-9" className="mt-2 text-[11px] text-zinc-500">{t('updatedAt', { time: formatTime(data.updated_at) || '--' })} · Tools {typeof data.tool_count === 'number' ? data.tool_count : 0}</div>
          </div>
          {metaSection?.items?.length ? renderMetaItems(metaSection.items) : <div className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3 text-sm text-zinc-500">{t('noMeta')}</div>}
        </div>
      ) : null}
    </div>
  );
}
