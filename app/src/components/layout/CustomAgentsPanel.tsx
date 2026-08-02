// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from 'react';
import { Sparkles, Pencil, Trash2, Zap, Bot, Cpu, Store, Download, RefreshCw, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Spinner } from '../ui/Spinner';
import { useDialogs } from '../ui/Modal';
import { useDevRegister } from '../../lib/devStore';
import apiService from '../../services/api';

interface CustomAgent {
  slug: string;
  name: string;
  tools: string[];
  model: string;
  body: string;
}

interface MarketRole {
  slug: string;
  version: string;
  name: string;
  name_zh: string;
  description: string;
  description_zh: string;
  tags: string[];
  installed: boolean;
  installed_version?: string;
  has_update: boolean;
  modified: boolean;
  conflicts?: string[];
}

interface Props {
  /** Target agent pane that drives conversational create/edit (reads the
   *  agent-creator skill, then runs `agent-creator ...`). */
  paneId: string;
  /** Called after a new instance is created from a custom agent (refresh panes). */
  onCreated?: () => void;
  /** Select the newly created agent's pane (short id). */
  onSelectAgent?: (shortId: string) => void;
  /** Dedicated activity-bar entry: open the public role catalog directly. */
  marketOnly?: boolean;
}

// Authoring is conversational, NOT a UI form: clicking "create" sends a prompt to
// the active agent telling it to read the agent-creator skill and walk the user
// through `agent-creator create ...`. This panel only LISTS/manages the result
// (delete, spin up an instance). Mounted in the activity-bar left panel below
// Skills. State lives server-side at ~/cicy-ai/agents/<slug>/AGENT.md.

// Prompt sent to the active agent to drive a fresh custom-agent creation. The
// agent first reads the skill docs, then conversationally collects the spec and
// runs the CLI. (Default Chinese, matching the product's primary language.)
const CREATE_PROMPT = [
  '请帮我创建一个自定义 Agent(人设 + 工具 + 模型)。',
  '步骤:',
  '1. 先运行 `agent-creator --help` 了解用法,并 `agent-creator tools` 查看可选的工具组。',
  '2. 通过对话依次问我:这个 agent 叫什么、人设/职责是什么、需要哪些工具组、默认用哪个模型(可留空)。',
  '3. 我确认后,用 `agent-creator create "<名称>" --tools <a,b> --model <模型> --prompt "<人设>"` 创建。',
  '4. 创建完成后告诉我:去左侧「自定义 Agent」面板点「创建」开实例,或在「新建员工」里选 ★<名称>。',
  '请一步一步来——先读文档,再开始问我,不要一次问完。',
].join('\n');

const editPrompt = (name: string) => [
  `请帮我修改自定义 Agent「${name}」。`,
  `先运行 \`agent-creator show "${name}"\` 看当前配置,再问我要改什么(人设 / 工具 / 默认模型),`,
  `然后用 \`agent-creator create "${name}" ...\` 覆盖保存(同名即覆盖)。`,
].join('\n');

export default function CustomAgentsPanel({ paneId, onCreated, onSelectAgent, marketOnly = false }: Props) {
  const { t } = useTranslation('createAgent');
  const { confirm } = useDialogs();
  const [agents, setAgents] = useState<CustomAgent[]>([]);
  const [loading, setLoading] = useState(true);
  const [creatingSlug, setCreatingSlug] = useState('');
  const [view, setView] = useState<'custom' | 'market'>(marketOnly ? 'market' : 'custom');
  const [marketRoles, setMarketRoles] = useState<MarketRole[]>([]);
  const [marketLoading, setMarketLoading] = useState(false);
  const [marketQuery, setMarketQuery] = useState('');
  const [marketAction, setMarketAction] = useState('');

  const refresh = useCallback(() => {
    return apiService.listCustomAgents()
      .then((res) => setAgents(Array.isArray(res.data?.agents) ? res.data.agents : []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { refresh(); }, [refresh]);
  useDevRegister('CustomAgentsPanel', { count: agents.length, loading, paneId });

  const toolLabel = (g: string) => t(`toolGroups.${g}`, { defaultValue: g }) || g;

  const refreshMarket = useCallback(async (query = marketQuery) => {
    setMarketLoading(true);
    try {
      const res = await apiService.listAgentRoleMarket(query);
      setMarketRoles(Array.isArray(res.data?.roles) ? res.data.roles : []);
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('roleMarketLoadFailed', { defaultValue: '角色市场加载失败' }) }));
    } finally { setMarketLoading(false); }
  }, [marketQuery, t]);

  useEffect(() => { if (view === 'market') refreshMarket(); }, [view]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleMarketAction = async (role: MarketRole) => {
    setMarketAction(role.slug);
    try {
      if (role.installed) await apiService.updateAgentRole(role.slug);
      else await apiService.installAgentRole(role.slug);
      await refreshMarket();
      window.dispatchEvent(new CustomEvent('show-toast', { detail: role.installed ? t('roleUpdated', { defaultValue: '角色已更新' }) : t('roleInstalled', { defaultValue: '角色已安装' }) }));
    } catch (error: any) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: error?.response?.data?.error || t('roleActionFailed', { defaultValue: '角色操作失败' }) }));
    } finally { setMarketAction(''); }
  };

  // Hand off to the active agent: it reads the agent-creator skill and drives the
  // create/edit conversation. No UI form.
  const dispatchToAgent = (prompt: string) => {
    const target = (paneId || '').trim();
    if (!target) {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('noActiveAgent') }));
      return;
    }
    apiService.sendCommand(target, prompt, true)
      .then(() => window.dispatchEvent(new CustomEvent('show-toast', { detail: t('sentToAgent') })))
      .catch(() => window.dispatchEvent(new CustomEvent('show-toast', { detail: t('sentToAgentFailed') })));
  };

  const handleDelete = async (ca: CustomAgent) => {
    const ok = await confirm({ body: t('customDeleteConfirm', { name: ca.name }), danger: true });
    if (!ok) return;
    try { await apiService.deleteCustomAgent(ca.slug); } catch { /* ignore */ }
    await refresh();
  };

  const handleCreateInstance = async (ca: CustomAgent) => {
    setCreatingSlug(ca.slug);
    try {
      const { data } = await apiService.createPane({
        role: 'worker',
        title: ca.name,
        agent_type: 'cicy',
        role_template: ca.slug,
      });
      const id = data?.pane_id || data?.id;
      if (id) {
        window.dispatchEvent(new CustomEvent('show-toast', { detail: t('instanceCreated', { name: ca.name }) }));
        onCreated?.();
        onSelectAgent?.(String(id).split(':')[0]);
      }
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('createInstanceFailed') }));
    } finally {
      setCreatingSlug('');
    }
  };

  return (
    <div data-id="custom-agents-panel" className="flex h-full flex-col bg-[#0A0A0A]">
      {!marketOnly && <div data-id="custom-agents-panel-toolbar" className="flex items-center justify-between gap-2 border-b border-[var(--vsc-border)] px-3 py-2.5">
        <p data-id="custom-agents-panel-hint" className="text-[11px] leading-4 text-zinc-600">{t('panelHint')}</p>
        <button
          data-id="custom-agents-panel-new"
          type="button"
          onClick={() => dispatchToAgent(CREATE_PROMPT)}
          title={t('createViaAgentTip')}
          className="flex shrink-0 cursor-pointer items-center gap-1 rounded-lg bg-blue-500/20 px-2.5 py-1.5 text-[12px] font-medium text-blue-300 transition-all hover:bg-blue-500/25"
        >
          <Sparkles className="h-3.5 w-3.5" /> {t('createViaAgent')}
        </button>
      </div>}

      {!marketOnly && <div className="flex gap-1 border-b border-[var(--vsc-border)] px-3 py-2">
        <button type="button" onClick={() => setView('custom')} className={`rounded-md px-2.5 py-1 text-[11px] ${view === 'custom' ? 'bg-blue-500/20 text-blue-300' : 'text-zinc-500 hover:text-zinc-300'}`}>
          <Bot className="mr-1 inline h-3 w-3" />{t('myAgents', { defaultValue: '我的 Agent' })}
        </button>
        <button data-id="agent-role-market-tab" type="button" onClick={() => setView('market')} className={`rounded-md px-2.5 py-1 text-[11px] ${view === 'market' ? 'bg-emerald-500/20 text-emerald-300' : 'text-zinc-500 hover:text-zinc-300'}`}>
          <Store className="mr-1 inline h-3 w-3" />{t('roleMarket', { defaultValue: '角色市场' })}
        </button>
      </div>}

      <div data-id="custom-agents-panel-list" className="flex-1 overflow-auto p-3">
        {view === 'market' ? (
          <div data-id="agent-role-market" className="space-y-2.5">
            <form className="flex gap-1.5" onSubmit={(event) => { event.preventDefault(); refreshMarket(); }}>
              <div className="relative min-w-0 flex-1">
                <Search className="absolute left-2 top-2 h-3.5 w-3.5 text-zinc-600" />
                <input value={marketQuery} onChange={(event) => setMarketQuery(event.target.value)} placeholder={t('roleMarketSearch', { defaultValue: '搜索角色…' })} className="w-full rounded-lg border border-white/[0.08] bg-black/30 py-1.5 pl-7 pr-2 text-[12px] text-zinc-300 outline-none focus:border-emerald-500/40" />
              </div>
              <button type="submit" className="rounded-lg bg-white/[0.06] px-2 text-zinc-400 hover:text-zinc-200"><RefreshCw className={`h-3.5 w-3.5 ${marketLoading ? 'animate-spin' : ''}`} /></button>
            </form>
            {marketLoading ? <div className="flex justify-center py-8"><Spinner size="sm" /></div> : marketRoles.length === 0 ? (
              <p className="py-8 text-center text-[12px] text-zinc-600">{t('roleMarketEmpty', { defaultValue: '没有匹配的公共角色' })}</p>
            ) : marketRoles.map((role) => (
              <div key={role.slug} data-id={`agent-role-market-${role.slug}`} className="rounded-xl border border-white/[0.07] bg-white/[0.02] p-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0"><p className="truncate text-[13px] font-medium text-zinc-200">{role.name_zh || role.name}</p><p className="text-[10px] text-zinc-600">{role.slug} · v{role.version}</p></div>
                  {role.modified ? <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-300">{t('roleModified', { defaultValue: '有本地修改' })}</span> : role.installed ? <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] text-emerald-300">{t('roleInstalled', { defaultValue: '已安装' })}</span> : null}
                </div>
                <p className="mt-2 text-[11px] leading-5 text-zinc-500">{role.description_zh || role.description}</p>
                <div className="mt-2 flex flex-wrap gap-1">{(role.tags || []).map(tag => <span key={tag} className="rounded bg-white/[0.05] px-1.5 py-0.5 text-[10px] text-zinc-500">{tag}</span>)}</div>
                <div className="mt-3 flex gap-1.5">
                  <button type="button" onClick={() => handleMarketAction(role)} disabled={marketAction === role.slug || (role.installed && !role.has_update)} className="flex flex-1 items-center justify-center gap-1 rounded-lg bg-emerald-500/15 py-1.5 text-[11px] text-emerald-300 disabled:opacity-40">
                    {marketAction === role.slug ? <Spinner size="sm" /> : role.installed ? <RefreshCw className="h-3 w-3" /> : <Download className="h-3 w-3" />}
                    {role.installed ? (role.has_update ? t('updateRole', { defaultValue: '更新' }) : t('roleCurrent', { defaultValue: '已是最新版' })) : t('installRole', { defaultValue: '安装' })}
                  </button>
                  {role.installed ? <button type="button" onClick={() => handleCreateInstance({ slug: role.slug, name: role.name_zh || role.name, tools: [], model: '', body: '' })} className="rounded-lg bg-blue-500/15 px-2.5 text-[11px] text-blue-300">{t('createInstance')}</button> : null}
                </div>
              </div>
            ))}
          </div>
        ) : loading ? (
          <div data-id="custom-agents-panel-loading" className="flex justify-center py-8"><Spinner size="sm" /></div>
        ) : agents.length === 0 ? (
          <div data-id="custom-agents-panel-empty" className="flex flex-col items-center gap-2 py-10 text-center">
            <Bot className="h-8 w-8 text-zinc-700" />
            <p className="text-[12px] text-zinc-600">{t('customEmpty')}</p>
            <button
              data-id="custom-agents-panel-empty-new"
              type="button"
              onClick={() => dispatchToAgent(CREATE_PROMPT)}
              className="mt-1 flex cursor-pointer items-center gap-1 rounded-lg bg-blue-500/20 px-3 py-1.5 text-[12px] font-medium text-blue-300 transition-all hover:bg-blue-500/25"
            >
              <Sparkles className="h-3.5 w-3.5" /> {t('createViaAgent')}
            </button>
          </div>
        ) : (
          <div className="space-y-2.5">
            {agents.map((ca) => (
              <div
                data-id={`custom-agents-panel-card-${ca.slug}`}
                key={ca.slug}
                className="group rounded-xl border border-white/[0.07] bg-white/[0.02] p-3 transition-colors hover:border-white/[0.12]"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-blue-500/15 text-blue-300"><Bot className="h-4 w-4" /></div>
                    <div className="min-w-0">
                      <p data-id={`custom-agents-panel-name-${ca.slug}`} className="truncate text-[13px] font-medium text-zinc-200">{ca.name}</p>
                      {ca.model ? (
                        <p data-id={`custom-agents-panel-model-${ca.slug}`} className="mt-0.5 flex items-center gap-1 text-[11px] text-zinc-600"><Cpu className="h-3 w-3" />{ca.model}</p>
                      ) : null}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100">
                    <button
                      data-id={`custom-agents-panel-edit-${ca.slug}`}
                      type="button"
                      onClick={() => dispatchToAgent(editPrompt(ca.name))}
                      className="cursor-pointer rounded-md p-1.5 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
                      title={t('editViaAgentTip')}
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </button>
                    <button
                      data-id={`custom-agents-panel-delete-${ca.slug}`}
                      type="button"
                      onClick={() => handleDelete(ca)}
                      className="cursor-pointer rounded-md p-1.5 text-zinc-500 transition-colors hover:bg-red-500/10 hover:text-red-400"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>

                {ca.body ? (
                  <p data-id={`custom-agents-panel-body-${ca.slug}`} className="mt-2 line-clamp-2 text-[11px] leading-5 text-zinc-500">{ca.body}</p>
                ) : null}

                {ca.tools?.length ? (
                  <div data-id={`custom-agents-panel-tools-${ca.slug}`} className="mt-2 flex flex-wrap gap-1">
                    {ca.tools.map((g) => (
                      <span key={g} className="rounded bg-white/[0.05] px-1.5 py-0.5 text-[10px] text-zinc-400">{toolLabel(g)}</span>
                    ))}
                  </div>
                ) : null}

                <button
                  data-id={`custom-agents-panel-create-${ca.slug}`}
                  type="button"
                  onClick={() => handleCreateInstance(ca)}
                  disabled={creatingSlug === ca.slug}
                  className="mt-3 flex w-full cursor-pointer items-center justify-center gap-1.5 rounded-lg bg-blue-500/15 py-1.5 text-[12px] font-medium text-blue-300 transition-all hover:bg-blue-500/25 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {creatingSlug === ca.slug ? <Spinner size="sm" /> : <Zap className="h-3.5 w-3.5" />}
                  {t('createInstance')}
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
