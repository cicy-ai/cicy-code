import { useEffect, useState } from 'react';
import { Loader2, X, Zap } from 'lucide-react';
import { useApp } from '../contexts/AppContext';
import { useDevRegister } from '../lib/devStore';
import AgentTypeSelector from './AgentTypeSelector';

export interface CreateAgentValues {
  title: string;
  agent_type: string;
  allow_all_actions: boolean;
  use_official_auth: boolean;
  use_proxy: boolean;
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
}

const DEFAULT_VALUES: CreateAgentValues = {
  title: '',
  agent_type: '',
  allow_all_actions: true,
  use_official_auth: false,
  use_proxy: false,
};

export default function CreateAgentDialog({
  open,
  submitting = false,
  onClose,
  onSubmit,
  title = '新建员工',
  submitLabel = '创建',
  emptyTitleOnAgentSelect = '',
  dialogClassName = '',
  agentTypeGridClassName = 'grid grid-cols-1 gap-2 sm:grid-cols-2',
}: Props) {
  const { agentTypeOptions } = useApp();
  const [values, setValues] = useState<CreateAgentValues>(DEFAULT_VALUES);

  useEffect(() => {
    if (!open) return;
    setValues({
      ...DEFAULT_VALUES,
      agent_type: agentTypeOptions[0]?.value || 'claude',
    });
  }, [agentTypeOptions, open]);
  useDevRegister('CreateAgentDialog', {
    open,
    submitting,
    values,
    canSubmit: values.title.trim().length > 0 && values.agent_type.trim().length > 0 && !submitting,
  });

  if (!open) return null;

  const canSubmit = values.title.trim().length > 0 && values.agent_type.trim().length > 0 && !submitting;
  const selectedAgent = agentTypeOptions.find((option) => option.value === values.agent_type) || null;

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
        <div className="flex items-center justify-between border-b border-white/[0.06] px-5 py-4">
          <div>
            <h2 className="text-[15px] font-semibold text-white">{title}</h2>
            <p className="mt-0.5 text-[11px] text-zinc-600">设置员工名称、智能体类型和权限</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="cursor-pointer rounded-lg p-1.5 text-zinc-600 transition-colors hover:bg-white/[0.06] hover:text-zinc-300 disabled:opacity-50"
            title="关闭"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-5 px-5 py-5">
          <div>
            <label data-id="create-agent-dialog-title-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">员工名称</label>
            <input
              data-id="create-agent-dialog-title-input"
              autoFocus
              type="text"
              value={values.title}
              onChange={(e) => set({ title: e.target.value })}
              placeholder="输入员工名称，如：营销经理"
              className="w-full rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2 text-sm text-zinc-200 outline-none transition-all placeholder:text-zinc-700 focus:border-blue-500/40 focus:ring-1 focus:ring-blue-500/20"
            />
          </div>

          <div>
            <label data-id="create-agent-dialog-agent-type-label" className="mb-1.5 block text-[13px] font-medium text-zinc-300">智能体类型</label>
            <AgentTypeSelector
              value={values.agent_type}
              options={agentTypeOptions}
              onChange={(agentType) => {
                const option = agentTypeOptions.find((item) => item.value === agentType);
                handleAgentTypeSelect(agentType, option?.label || agentType);
              }}
              dataId="create-agent-dialog-agent-type-options"
              optionDataIdPrefix="create-agent-dialog-agent-type"
              className={agentTypeGridClassName}
            />
            <div className="mt-2 rounded-xl border border-cyan-400/15 bg-cyan-500/[0.06] px-3 py-2 text-[12px] text-cyan-100/90">
              当前选择：<span className="font-medium text-cyan-100">{selectedAgent?.label || '未选择'}</span>
              <span className="ml-1 text-cyan-100/65">{selectedAgent?.description || (agentTypeOptions.length ? '请选择一个智能体类型。' : '当前没有可用智能体类型。')}</span>
            </div>
          </div>

          <div data-id="create-agent-dialog-allow-all-actions" className="flex items-center justify-between py-1">
            <div>
              <p className="text-[13px] font-medium text-zinc-300">启动时允许所有操作</p>
              <p className="mt-0.5 text-[11px] text-zinc-600">Codex/Claude 追加危险参数</p>
            </div>
            <button
              type="button"
              onClick={() => set({ allow_all_actions: !values.allow_all_actions })}
              className={`relative h-6 w-11 cursor-pointer rounded-full transition-colors ${values.allow_all_actions ? 'bg-blue-600' : 'bg-white/[0.08]'}`}
            >
              <div className={`absolute top-1 h-4 w-4 rounded-full bg-white shadow-md transition-transform ${values.allow_all_actions ? 'translate-x-[22px]' : 'translate-x-1'}`} />
            </button>
          </div>

          <div data-id="create-agent-dialog-use-proxy" className="flex items-center justify-between py-1">
            <div>
              <p className="text-[13px] font-medium text-zinc-300">启用代理</p>
              <p className="mt-0.5 text-[11px] text-zinc-600">启动前执行 cicy_proxy_on，并检查 mihome 规则</p>
            </div>
            <button
              type="button"
              onClick={() => set({ use_proxy: !values.use_proxy })}
              className={`relative h-6 w-11 cursor-pointer rounded-full transition-colors ${values.use_proxy ? 'bg-blue-600' : 'bg-white/[0.08]'}`}
            >
              <div className={`absolute top-1 h-4 w-4 rounded-full bg-white shadow-md transition-transform ${values.use_proxy ? 'translate-x-[22px]' : 'translate-x-1'}`} />
            </button>
          </div>

          <div data-id="create-agent-dialog-use-official-auth" className="flex items-center justify-between py-1">
            <div>
              <p className="text-[13px] font-medium text-zinc-300">使用官方认证</p>
              <p className="mt-0.5 text-[11px] text-zinc-600">开启后不注入本地 gateway auth</p>
            </div>
            <button
              type="button"
              onClick={() => set({ use_official_auth: !values.use_official_auth })}
              className={`relative h-6 w-11 cursor-pointer rounded-full transition-colors ${values.use_official_auth ? 'bg-blue-600' : 'bg-white/[0.08]'}`}
            >
              <div className={`absolute top-1 h-4 w-4 rounded-full bg-white shadow-md transition-transform ${values.use_official_auth ? 'translate-x-[22px]' : 'translate-x-1'}`} />
            </button>
          </div>
        </div>

        <div data-id="create-agent-dialog-actions" className="flex justify-end gap-2 border-t border-white/[0.06] px-5 py-3">
          <button
            data-id="create-agent-dialog-cancel"
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="cursor-pointer rounded-lg bg-white/[0.06] px-4 py-2 text-sm text-zinc-300 transition-all hover:bg-white/[0.1] disabled:opacity-50"
          >
            取消
          </button>
          <button
            data-id="create-agent-dialog-submit"
            type="submit"
            disabled={!canSubmit || agentTypeOptions.length === 0}
            className="flex cursor-pointer items-center gap-2 rounded-lg bg-blue-500/20 px-4 py-2 text-sm font-medium text-blue-300 transition-all hover:bg-blue-500/25 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Zap className="h-4 w-4" />}
            {submitting ? '创建中...' : submitLabel}
          </button>
        </div>
      </form>
    </div>
  );
}
