// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { cn } from '../lib/utils'
import { getAgentTypeIconMeta, normalizeAgentType } from '../lib/agentType'

type AgentAvatarVariant = 'option' | 'panel' | 'stack' | 'select'

type AgentAvatarProps = {
  agentType?: string
  title: string
  dataId?: string
  variant?: AgentAvatarVariant
  className?: string
  style?: React.CSSProperties
}

const VARIANT_STYLES: Record<AgentAvatarVariant, { container: string; fallback: string; icon: string; hermes: string; text: string; style?: React.CSSProperties }> = {
  option: {
    container: 'h-5 w-5 rounded bg-zinc-300',
    fallback: 'bg-zinc-300',
    icon: 'h-4 w-4',
    hermes: 'px-1 text-[9px] font-semibold tracking-[0.08em]',
    text: 'text-[10px] font-semibold uppercase',
  },
  panel: {
    container: 'h-10 w-10 rounded-xl border-zinc-500/40 bg-zinc-300 shadow-sm',
    fallback: 'border-white/[0.08] bg-white/[0.03]',
    icon: 'h-8 w-8',
    hermes: 'text-[15px] font-semibold tracking-[0.08em]',
    text: 'text-xs font-semibold uppercase',
  },
  stack: {
    container: 'h-10 w-10 rounded-xl border-zinc-500/40 bg-zinc-300 shadow-sm',
    fallback: 'border-white/[0.08] bg-white/[0.03]',
    icon: 'h-8 w-8',
    hermes: 'text-[15px] font-semibold tracking-[0.08em]',
    text: 'text-xs font-semibold uppercase',
    style: { zoom: 0.85 },
  },
  select: {
    container: 'h-7 w-7 rounded-lg border-zinc-500/30 bg-zinc-200',
    fallback: 'border-white/[0.08] bg-white/[0.03]',
    icon: 'h-4 w-4',
    hermes: 'text-[9px] font-semibold tracking-[0.08em]',
    text: 'text-[9px] font-semibold uppercase',
  },
}

export default function AgentAvatar({
  agentType,
  title,
  dataId,
  variant = 'panel',
  className,
  style,
}: AgentAvatarProps) {
  const icon = getAgentTypeIconMeta(agentType)
  const normalizedAgentType = normalizeAgentType(agentType)
  const variantStyle = VARIANT_STYLES[variant]
  const mergedStyle = variantStyle.style ? { ...variantStyle.style, ...style } : style
  const textClassName = normalizedAgentType === 'hermes'
    ? variantStyle.hermes
    : variantStyle.text

  if (!icon) {
    return (
      <div
        data-id={dataId}
        className={cn('flex shrink-0 items-center justify-center border text-zinc-400', variantStyle.fallback, variantStyle.container, className)}
        style={mergedStyle}
        title={title}
      >
        <span data-id="agent-avatar-fallback-letter" className={cn('font-semibold uppercase', textClassName)}>{title.slice(0, 1) || '?'}</span>
      </div>
    )
  }

  // kiro's logo already IS a rounded purple tile. Framing it inside our light
  // avatar chip (and shrinking it to the icon size) shows a small purple square
  // inside a grey square at small sizes. Render it full-bleed instead: no chip
  // background/border, the image fills and is clipped to the avatar's corners.
  const fullBleed = normalizedAgentType === 'kiro-cli'

  return (
    <div
      data-id={dataId}
      className={cn(
        'flex shrink-0 items-center justify-center border text-zinc-950',
        variantStyle.container,
        fullBleed && 'overflow-hidden border-transparent bg-transparent shadow-none',
        className,
      )}
      style={mergedStyle}
      title={icon.label}
    >
      {icon.src ? (
        <img
          data-id="agent-avatar-icon"
          src={icon.src}
          alt={icon.label}
          className={cn(
            'object-contain',
            fullBleed ? 'h-full w-full object-cover' : variantStyle.icon,
            normalizedAgentType === 'cicy-claude' && 'scale-125',
          )}
        />
      ) : (
        <span data-id="agent-avatar-icon-text" className={cn(textClassName)} aria-label={icon.label}>{icon.text}</span>
      )}
    </div>
  )
}
