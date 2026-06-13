import { useCallback, useEffect, useState } from 'react';
import { Sparkles, Pencil, Trash2, Zap, Bot, Cpu } from 'lucide-react';
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

interface Props {
  /** Target agent pane that drives conversational create/edit (reads the
   *  agent-creator skill, then runs `agent-creator ...`). */
  paneId: string;
  /** Called after a new instance is created from a custom agent (refresh panes). */
  onCreated?: () => void;
  /** Select the newly created agent's pane (short id). */
  onSelectAgent?: (shortId: string) => void;
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

export default function CustomAgentsPanel({ paneId, onCreated, onSelectAgent }: Props) {
  const { t } = useTranslation('createAgent');
  const { confirm } = useDialogs();
  const [agents, setAgents] = useState<CustomAgent[]>([]);
  const [loading, setLoading] = useState(true);
  const [creatingSlug, setCreatingSlug] = useState('');

  const refresh = useCallback(() => {
    return apiService.listCustomAgents()
      .then((res) => setAgents(Array.isArray(res.data?.agents) ? res.data.agents : []))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { refresh(); }, [refresh]);
  useDevRegister('CustomAgentsPanel', { count: agents.length, loading, paneId });

  const toolLabel = (g: string) => t(`toolGroups.${g}`, { defaultValue: g }) || g;

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
      <div data-id="custom-agents-panel-toolbar" className="flex items-center justify-between gap-2 border-b border-[var(--vsc-border)] px-3 py-2.5">
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
      </div>

      <div data-id="custom-agents-panel-list" className="flex-1 overflow-auto p-3">
        {loading ? (
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
