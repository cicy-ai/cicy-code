import AgentAvatar from './AgentAvatar'

type AgentTypeOptionButtonProps = {
  value: string
  label: string
  selected: boolean
  onClick: () => void
  dataId?: string
}

export default function AgentTypeOptionButton({ value, label, selected, onClick, dataId }: AgentTypeOptionButtonProps) {
  return (
    <button
      data-id={dataId}
      type="button"
      onClick={onClick}
      className={`flex items-center gap-2 rounded-lg border px-3 py-2 text-sm transition-all ${
        selected
          ? 'border-blue-500/40 bg-blue-500/20 text-blue-300'
          : 'border-white/[0.08] bg-white/[0.03] text-zinc-400 hover:border-white/[0.12] hover:bg-white/[0.06]'
      }`}
    >
      <AgentAvatar
        agentType={value}
        title={label}
        className="h-5 w-5 rounded bg-zinc-300"
        fallbackClassName="bg-zinc-300"
        iconClassName="h-4 w-4"
        textClassName={value === 'openclaw' ? 'text-[13px] leading-none' : value === 'hermes' ? 'px-1 text-[9px] font-semibold tracking-[0.08em]' : 'text-[10px] font-semibold uppercase'}
      />
      <span>{label}</span>
    </button>
  )
}
