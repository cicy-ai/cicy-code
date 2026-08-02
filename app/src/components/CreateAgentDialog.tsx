// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { X, Zap } from 'lucide-react';
import { Spinner } from './ui/Spinner';
import Select from './ui/Select';
import { useTranslation } from 'react-i18next';
import { useApp } from '../contexts/AppContext';
import { useDevRegister } from '../lib/devStore';
import AgentTypeSelector from './AgentTypeSelector';
import apiService from '../services/api';

export interface CreateAgentValues {
  title: string;
  agent_type: string;
  allow_all_actions: boolean;
  use_custom_gateway: boolean;
  use_proxy: boolean;
  inherit_guidance: boolean;
  /** Project-template slug to seed the agent's memory file with (global is
   *  always added). Empty = global only. */
  project_template: string;
  /** Role-library template slug (架构 §5). Empty = none. */
  role_template: string;
  /** UI language for the agent's role persona + greeting (e.g. "en", "zh-CN").
   *  Defaults to the current browser/UI language. */
  lang: string;
  api_style: 'openai' | 'anthropic';
}

interface Props {
  open: boolean;
  submitting?: boolean;
  onClose: () => void;
  onSubmit: (values: CreateAgentValues) => Promise<void> | void;
  title?: string;
  submitLabel?: string;
  emptyTitleOnAgentSelect?: string;
  dialogClassName?: string;
  agentTypeGridClassName?: string;
  // When set, the dialog assumes the new pane will be bound under a master of
  // this agent_type and shows a "继承 master 的 X.md" toggle (X is derived from
  // the master's agent_type). When unset, the toggle is hidden.
  masterAgentType?: string;
  initialValues?: Partial<CreateAgentValues>;
}

const DEFAULT_VALUES: CreateAgentValues = {
  title: '',
  agent_type: '',
  allow_all_actions: true,
  use_custom_gateway: true,
  use_proxy: false,
  inherit_guidance: false,
  project_template: 'default', // 开箱即用:新 agent 默认归属 default 项目(共享记忆)
  role_template: 'assistant', // role library 默认 = assistant(通用基座/默认人设)
  lang: '', // set to the current UI language when the dialog opens
  api_style: 'openai',
};

export default function CreateAgentDialog({
  open,
  submitting = false,
  onClose,
  onSubmit,
  title,
  submitLabel,
  emptyTitleOnAgentSelect = '',
  dialogClassName = '',
  agentTypeGridClassName = 'grid grid-cols-1 gap-2 sm:grid-cols-2',
  initialValues,
}: Props) {
  const { t, i18n } = useTranslation('createAgent');
  const { t: ts } = useTranslation('settings');
  const { t: tc } = useTranslation('common');
  const { agentTypeOptions } = useApp();
  const [values, setValues] = useState<CreateAgentValues>(DEFAULT_VALUES);
  const resolvedTitle = title ?? t('title');
  const resolvedSubmitLabel = submitLabel ?? t('submit');

  // Template selection only — management lives in the Inspector 记忆 tab. Global
  // is always added; project/role are optional. (~/cicy-ai/memory/{projects,agents}/*.md)
  const [projects, setProjects] = useState<{ slug: string; name: string }[]>([]);
  const [roleTemplates, setRoleTemplates] = useState<string[]>([]);
  // Projects are NOT created from this dialog — only chosen. New projects are
  // authored elsewhere; here you pick an existing one (default if unassigned).
  const loadProjects = () =>
    apiService.listProjects()
      .then((res) => setProjects(Array.isArray(res.data?.projects) ? res.data.projects : []))
      .catch(() => {});

  // User-authored custom agents appear as their own agent-type options
  // ("custom:<slug>"), alongside claude/codex/cicy. Picking one creates an
  // instance of that persona (backend maps custom:<slug> → cicy + role_template).
  const [customOptions, setCustomOptions] = useState<{ value: string; label: string; description?: string }[]>([]);
  const mergedAgentTypeOptions = [...agentTypeOptions, ...customOptions];

  useEffect(() => {
    if (!open) return;
    setValues({
      ...DEFAULT_VALUES,
      agent_type: agentTypeOptions[0]?.value || 'claude',
      lang: (i18n.language || '').toLowerCase().startsWith('zh') ? 'zh-CN' : 'en', // default to the current browser/UI language
      ...initialValues,
    });
    void loadProjects();
    apiService.listMemoryTemplates()
      .then((res) => {
        setRoleTemplates(Array.isArray(res.data?.roles) ? res.data.roles : []);
      })
      .catch(() => {});
    apiService.listCustomAgents()
      .then((res) => {
        const list = Array.isArray(res.data?.agents) ? res.data.agents : [];
        setCustomOptions(list.map((ca: any) => ({
          value: `custom:${ca.slug}`,
          label: `★ ${ca.name}`,
          description: typeof ca.body === 'string' ? ca.body.slice(0, 60) : undefined,
        })));
      })
      .catch(() => setCustomOptions([]));
  }, [agentTypeOptions, open, initialValues]);
  useDevRegister('CreateAgentDialog', {
    open,
    submitting,
    values,
    canSubmit: values.title.trim().length > 0 && values.agent_type.trim().length > 0 && !submitting,
  });

  if (!open) return null;

  const canSubmit = values.title.trim().length > 0 && values.agent_type.trim().length > 0 && !submitting;
  const selectedAgent = mergedAgentTypeOptions.find((option) => option.value === values.agent_type) || null;
  const supportsAPIStyle = values.agent_type === 'cicy' || values.agent_type.startsWith('custom:');

  const set = (patch: Partial<CreateAgentValues>) => {
    setValues((prev) => ({ ...prev, ...patch }));
  };

  const handleAgentTypeSelect = (agentType: string, agentLabel: string) => {
    setValues((prev) => ({
      ...prev,
      agent_type: agentType,
      title: prev.title.trim() ? prev.title : (emptyTitleOnAgentSelect || agentLabel),
    }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    await onSubmit({
      ...values,
      title: values.title.trim(),
    });
  };

  return (
    <div data-id="create-agent-dialog-overlay" className="fixed inset-0 z-[100000] flex items-center justify-center cursor-pointer" onClick={submitting ? undefined : onClose}>
      <div data-id="create-agent-dialog-backdrop" className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <form
        data-id="create-agent-dialog"
        className={`relative w-[560px] max-w-[92vw] cursor-default overflow-hidden rounded-2xl border border-white/[0.08] bg-[#161618] shadow-2xl ${dialogClassName}`}
        onClick={(e) => e.stopPropagation()}
        onSubmit={handleSubmit}
      >
        <div data-id="create-agent-dialog-header" className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
          <div data-id="create-agent-dialog-header-titles">
            <h2 data-id="create-agent-dialog-title" className="text-[15px] font-semibold text-white">{resolvedTitle}</h2>
            <p data-id="create-agent-dialog-title-hint" className="mt-0.5 text-[11px] text-zinc-600">{t('titleHint')}</p>
          </div>
          <button
            data-id="create-agent-dialog-close"
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="cursor-pointer rounded-lg p-1.5 text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300 disabled:opacity-50"
            title={tc('close')}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div data-id="create-agent-dialog-body" className="space-y-5 px-5 py-5">
          <div data-id="create-agent-dialog-title-field">
            <label data-id="create-agent-dialog-title-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('nameLabel')}</label>
            <input
              data-id="create-agent-dialog-title-input"
              autoFocus
              type="text"
              value={values.title}
              onChange={(e) => set({ title: e.target.value })}
              placeholder={t('namePlaceholder')}
              className="w-full rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-zinc-200 outline-none transition-all placeholder:text-zinc-700 focus:border-blue-500/40 focus:ring-1 focus:ring-blue-500/20"
            />
          </div>

          <div data-id="create-agent-dialog-agent-type-field">
            <label data-id="create-agent-dialog-agent-type-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('agentTypeLabel')}</label>
            <AgentTypeSelector
              value={values.agent_type}
              options={mergedAgentTypeOptions}
              onChange={(agentType) => {
                const option = mergedAgentTypeOptions.find((item) => item.value === agentType);
                handleAgentTypeSelect(agentType, (option?.label || agentType).replace(/^★\s*/, ''));
              }}
              dataId="create-agent-dialog-agent-type-options"
              optionDataIdPrefix="create-agent-dialog-agent-type"
              className={agentTypeGridClassName}
            />
            <div data-id="create-agent-dialog-agent-type-hint" className="mt-2 rounded-xl border border-cyan-400/15 bg-cyan-500/[0.06] px-3 py-2 text-[12px] text-cyan-100/90">
              {t('currentSelection')}<span data-id="create-agent-dialog-agent-type-hint-name" className="font-medium text-cyan-100">{selectedAgent?.label || t('noSelection')}</span>
              <span data-id="create-agent-dialog-agent-type-hint-desc" className="ml-1 text-cyan-100/65">{selectedAgent?.description || (agentTypeOptions.length ? t('selectAgentTypeHint') : t('noAgentTypesAvailable'))}</span>
            </div>
          </div>

          <div data-id="create-agent-dialog-templates-field" className="grid grid-cols-2 gap-2">
            <div data-id="create-agent-dialog-project-field">
              <label className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('templateProjectLabel')}</label>
              <Select
                dataId="create-agent-dialog-project-template-select"
                className="w-full"
                value={values.project_template}
                onChange={(v) => set({ project_template: v })}
                options={projects.map((p) => ({ value: p.slug, label: p.name }))}
              />
            </div>
            <div>
              <label className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('templateRoleLabel')}</label>
              <Select
                dataId="create-agent-dialog-role-template-select"
                className="w-full"
                value={values.role_template}
                onChange={(v) => set({ role_template: v })}
                options={roleTemplates.map((slug) => ({ value: slug, label: slug }))}
              />
            </div>
            <div data-id="create-agent-dialog-lang-field">
              <label className="mb-1.5 block text-[13px] font-medium text-zinc-300">{t('languageLabel', 'Language')}</label>
              <Select
                dataId="create-agent-dialog-lang-select"
                className="w-full"
                value={values.lang}
                onChange={(v) => set({ lang: v })}
                options={[
                  { value: 'en', label: 'English' },
                  { value: 'zh-CN', label: '中文' },
                ]}
              />
            </div>
            {supportsAPIStyle && (
              <div data-id="create-agent-dialog-api-style-field" title={t('apiStyleHint')}>
                <label data-id="create-agent-dialog-api-style-label" className="mb-1.5 flex items-center gap-1.5 text-[13px] font-medium text-zinc-300">
                  {t('apiStyleLabel')}
                  <span data-id="create-agent-dialog-api-style-locked" className="text-[10px] font-normal text-amber-300/65">{t('apiStyleLocked')}</span>
                </label>
                <div data-id="create-agent-dialog-api-style-options" className="grid h-[34px] grid-cols-2 rounded-lg border border-white/[0.08] bg-white/[0.03] p-0.5">
                  {(['openai', 'anthropic'] as const).map((style) => (
                    <button
                      data-id={`create-agent-dialog-api-style-${style}`}
                      key={style}
                      type="button"
                      onClick={() => set({ api_style: style })}
                      className={`rounded-md px-2 text-[12px] transition-colors ${
                        values.api_style === style
                          ? 'bg-blue-500/20 font-medium text-blue-200 shadow-sm'
                          : 'text-zinc-500 hover:bg-white/[0.05] hover:text-zinc-300'
                      }`}
                    >
                      {t(style === 'openai' ? 'apiStyleOpenAI' : 'apiStyleClaude')}
                    </button>
                  ))}
                </div>
              </div>
            )}
            <p className="col-span-2 text-[11px] text-zinc-600">{t('templateHint')}</p>
          </div>

          <div data-id="create-agent-dialog-allow-all-actions" className="flex items-center justify-between py-1">
            <div data-id="create-agent-dialog-allow-all-actions-copy">
              <p data-id="create-agent-dialog-allow-all-actions-title" className="text-[13px] font-medium text-zinc-300">{ts('allowAllActionsTitle')}</p>
              <p data-id="create-agent-dialog-allow-all-actions-hint" className="mt-0.5 text-[11px] text-zinc-600">{ts('allowAllActionsHint')}</p>
            </div>
            <button
              data-id="create-agent-dialog-allow-all-actions-toggle"
              type="button"
              onClick={() => set({ allow_all_actions: !values.allow_all_actions })}
              className={`relative h-6 w-11 cursor-pointer rounded-full transition-colors ${values.allow_all_actions ? 'bg-blue-600' : 'bg-white/[0.08]'}`}
            >
              <div data-id="create-agent-dialog-allow-all-actions-toggle-thumb" className={`absolute top-1 h-4 w-4 rounded-full bg-white shadow-md transition-transform ${values.allow_all_actions ? 'translate-x-[22px]' : 'translate-x-1'}`} />
            </button>
          </div>

          {/* use_proxy toggle hidden per product decision — defaults to false. */}

          <div data-id="create-agent-dialog-use-custom-gateway" className="flex items-center justify-between py-1">
            <div data-id="create-agent-dialog-use-custom-gateway-copy">
              <p data-id="create-agent-dialog-use-custom-gateway-title" className="text-[13px] font-medium text-zinc-300">{ts('customGatewayTitle')}</p>
              <p data-id="create-agent-dialog-use-custom-gateway-hint" className="mt-0.5 text-[11px] text-zinc-600">{ts('customGatewayHint')}</p>
            </div>
            <button
              data-id="create-agent-dialog-use-custom-gateway-toggle"
              type="button"
              onClick={() => set({ use_custom_gateway: !values.use_custom_gateway })}
              className={`relative h-6 w-11 cursor-pointer rounded-full transition-colors ${values.use_custom_gateway ? 'bg-blue-600' : 'bg-white/[0.08]'}`}
            >
              <div data-id="create-agent-dialog-use-custom-gateway-toggle-thumb" className={`absolute top-1 h-4 w-4 rounded-full bg-white shadow-md transition-transform ${values.use_custom_gateway ? 'translate-x-[22px]' : 'translate-x-1'}`} />
            </button>
          </div>

          {/* Inheritance retired: every agent is seeded with a self-contained
              guidance file from the global template, so there is no master-rules
              inheritance toggle. */}
        </div>

        <div data-id="create-agent-dialog-actions" className="flex justify-end gap-2 border-t border-white/[0.06] px-5 py-3">
          <button
            data-id="create-agent-dialog-cancel"
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="cursor-pointer rounded-lg bg-white/[0.06] px-4 py-2 text-sm text-zinc-300 transition-all hover:bg-white/[0.1] disabled:opacity-50"
          >
            {tc('cancel')}
          </button>
          <button
            data-id="create-agent-dialog-submit"
            type="submit"
            disabled={!canSubmit || mergedAgentTypeOptions.length === 0}
            className="flex cursor-pointer items-center gap-2 rounded-lg bg-blue-500/20 px-4 py-2 text-sm font-medium text-blue-300 transition-all hover:bg-blue-500/25 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {submitting ? <Spinner size="sm" /> : <Zap className="h-4 w-4" />}
            {submitting ? t('submitting') : resolvedSubmitLabel}
          </button>
        </div>
      </form>
    </div>
  );
}
