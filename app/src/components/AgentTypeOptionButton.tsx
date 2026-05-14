import { Check } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import AgentAvatar from './AgentAvatar'

type AgentTypeOptionButtonProps = {
  value: string
  label: string
  description?: string
  selected: boolean
  onClick: () => void
  dataId?: string
  className?: string
}

export default function AgentTypeOptionButton({ value, label, description, selected, onClick, dataId, className }: AgentTypeOptionButtonProps) {
  const { t } = useTranslation('ui');
  return (
    <button
      data-id={dataId}
      type="button"
      onClick={onClick}
      aria-pressed={selected}
      className={`group relative flex items-start gap-3 rounded-xl border px-3 py-3 text-left transition-all ${
        selected
          ? 'border-cyan-400/60 bg-cyan-500/14 text-white shadow-[0_0_0_1px_rgba(34,211,238,0.18)]'
          : 'border-white/[0.08] bg-white/[0.03] text-zinc-300 hover:border-white/[0.16] hover:bg-white/[0.06] hover:text-white'
      } ${className || ''}`}
    >
      <AgentAvatar
        agentType={value}
        title={label}
        variant="option"
        className={selected ? 'ring-1 ring-cyan-300/40' : 'group-hover:border-white/25'}
      />
      <span data-id="agent-type-option-button-body" className="min-w-0 flex-1">
        <span data-id="agent-type-option-button-header" className="flex items-center gap-2">
          <span data-id="agent-type-option-button-label" className={`text-sm font-medium ${selected ? 'text-cyan-100' : 'text-zinc-200'}`}>{label}</span>
          {selected && (
            <span data-id="agent-type-option-button-selected-badge" className="inline-flex items-center gap-1 rounded-full bg-cyan-300/14 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.12em] text-cyan-200">
              <Check className="h-3 w-3" />
              {t('agentTypeSelected')}
            </span>
          )}
        </span>
        {description ? (
          <span data-id="agent-type-option-button-description" className={`mt-1 block text-[11px] leading-4 ${selected ? 'text-cyan-100/75' : 'text-zinc-500 group-hover:text-zinc-400'}`}>
            {description}
          </span>
        ) : null}
      </span>
    </button>
  )
}
