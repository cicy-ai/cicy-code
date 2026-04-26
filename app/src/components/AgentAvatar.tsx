import { cn } from '../lib/utils'
import { getAgentTypeIconMeta } from '../lib/agentType'

type AgentAvatarProps = {
  agentType?: string
  title: string
  dataId?: string
  className?: string
  fallbackClassName?: string
  iconClassName?: string
  textClassName?: string
}

export default function AgentAvatar({
  agentType,
  title,
  dataId,
  className,
  fallbackClassName,
  iconClassName,
  textClassName,
}: AgentAvatarProps) {
  const icon = getAgentTypeIconMeta(agentType)

  if (!icon) {
    return (
      <div
        data-id={dataId}
        className={cn('flex shrink-0 items-center justify-center border text-zinc-400', fallbackClassName, className)}
        title={title}
      >
        <span className={cn('font-semibold uppercase', textClassName)}>{title.slice(0, 1) || '?'}</span>
      </div>
    )
  }

  return (
    <div
      data-id={dataId}
      className={cn('flex shrink-0 items-center justify-center border text-zinc-950', className)}
      title={icon.label}
    >
      {icon.src ? (
        <img src={icon.src} alt={icon.label} className={cn('object-contain', iconClassName)} />
      ) : (
        <span className={cn(textClassName)} aria-label={icon.label}>{icon.text}</span>
      )}
    </div>
  )
}
