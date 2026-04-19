import { Check, Copy, MoreHorizontal } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { assetUrl } from '../../lib/assets'
import { WebFrame } from '../WebFrame'
import type { AgentCanvasItem } from './AgentCanvas'

function normalizeAgentType(agentType?: string) {
  switch ((agentType || '').trim().toLowerCase()) {
    case 'openclaw':
    case 'opencraw':
      return 'openclaw'
    case 'codex':
    case 'openai':
    case 'kiro-cli':
    case 'kiro-cli chat':
    case 'gemini':
    case 'copilot':
      return 'codex'
    case 'claude':
    case 'claude code':
    case 'claude-code':
      return 'claude'
    case 'cicy':
      return 'cicy'
    case 'opencode':
    case 'open code':
    case 'open-code':
      return 'opencode'
    default:
      return ''
  }
}

function AgentWindowAvatar({ agentType, title }: { agentType?: string; title: string }) {
  const normalizedAgentType = normalizeAgentType(agentType)
  const baseClassName = 'flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border shadow-sm'

  if (!normalizedAgentType) {
    return <div data-id="agent-stack-card-avatar" className={`${baseClassName} border-white/[0.08] bg-white/[0.03] text-zinc-400`}><span className="text-[11px] font-semibold uppercase">{title.slice(0, 1) || '?'}</span></div>
  }
  if (normalizedAgentType === 'openclaw') {
    return <div data-id="agent-stack-card-avatar" className={`${baseClassName} border-zinc-500/40 bg-zinc-300 text-zinc-950`}><span className="text-[16px] leading-none">🦞</span></div>
  }
  const iconMap: Record<string, { label: string; src: string; className?: string }> = {
    codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
    claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
    cicy: { label: 'CiCy', src: 'https://cicy-ai.com/logo.svg' },
    opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg'), className: 'h-5 w-5' },
  }
  const icon = iconMap[normalizedAgentType]
  if (!icon) return null
  return <div data-id="agent-stack-card-avatar" className={`${baseClassName} border-zinc-500/40 bg-zinc-300`}><img data-id="agent-stack-card-avatar-image" src={icon.src} alt={icon.label} className={`${icon.className || 'h-4 w-4'} object-contain`} /></div>
}

export default function AgentStack({
  items,
  activePaneId,
  onActivePaneIdChange,
  historyShortcutActive,
  onOpenPaneHistory,
}: {
  items: AgentCanvasItem[]
  activePaneId: string
  onActivePaneIdChange: (paneId: string) => void
  historyShortcutActive: boolean
  onOpenPaneHistory: (paneId: string) => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const container = containerRef.current
    if (!container || !activePaneId) return
    const target = container.querySelector<HTMLElement>(`[data-id="agent-stack-card-${activePaneId}"]`)
    if (!target) return
    target.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' })
  }, [activePaneId])

  return (
    <div data-id="agent-stack" ref={containerRef} className="relative h-full overflow-hidden bg-[#09090b]">
      {items.map((item) => (
        <AgentStackCard
          key={item.paneId}
          item={item}
          active={activePaneId === item.paneId}
          historyShortcutActive={historyShortcutActive}
          onOpenPaneHistory={onOpenPaneHistory}
          onClick={() => onActivePaneIdChange(item.paneId)}
        />
      ))}
    </div>
  )
}

function AgentStackCard({
  item,
  active,
  historyShortcutActive,
  onOpenPaneHistory,
  onClick,
}: {
  item: AgentCanvasItem;
  active: boolean;
  historyShortcutActive: boolean;
  onOpenPaneHistory: (paneId: string) => void;
  onClick: () => void;
}) {
  const [copiedPaneId, setCopiedPaneId] = useState(false)
  const copiedPaneTimerRef = useRef<number | null>(null)

  useEffect(() => () => {
    if (copiedPaneTimerRef.current !== null) window.clearTimeout(copiedPaneTimerRef.current)
  }, [])

  const handlePaneIdCopied = useCallback(() => {
    setCopiedPaneId(true)
    if (copiedPaneTimerRef.current !== null) window.clearTimeout(copiedPaneTimerRef.current)
    copiedPaneTimerRef.current = window.setTimeout(() => {
      setCopiedPaneId(false)
      copiedPaneTimerRef.current = null
    }, 1200)
  }, [])

  const copyPaneId = useCallback(async (event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    const value = item.paneId
    let ok = false
    try {
      if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
        await navigator.clipboard.writeText(value)
        ok = true
      }
    } catch {}

    if (!ok) {
      try {
        const textarea = document.createElement('textarea')
        textarea.value = value
        textarea.setAttribute('readonly', 'true')
        textarea.style.position = 'fixed'
        textarea.style.opacity = '0'
        textarea.style.pointerEvents = 'none'
        document.body.appendChild(textarea)
        textarea.focus()
        textarea.select()
        ok = document.execCommand('copy')
        document.body.removeChild(textarea)
      } catch {}
    }

    if (ok) {
      handlePaneIdCopied()
      return
    }

    window.dispatchEvent(new CustomEvent('show-toast', { detail: `复制失败：${value}` }))
  }, [handlePaneIdCopied, item.paneId])

  const handleOpenHistory = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneHistory(item.paneId)
  }, [item.paneId, onOpenPaneHistory])

  return (
    <div
      role="button"
      tabIndex={0}
      data-id={`agent-stack-card-${item.paneId}`}
      onClick={onClick}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          onClick()
        }
      }}
      className={`absolute inset-0 overflow-hidden text-left transition-colors ${active ? 'flex flex-col bg-[#0c0d10]' : 'hidden'}`}
      style={{ display: active ? 'flex' : 'none' }}
    >
      <div data-id={`agent-stack-card-header-${item.paneId}`} className="flex h-12 shrink-0 items-center border-b border-white/[0.07] bg-[linear-gradient(180deg,rgba(255,255,255,0.06),rgba(255,255,255,0.02))] px-3">
        <div data-id={`agent-stack-card-header-main-${item.paneId}`} className="flex items-center gap-3 min-w-0 flex-1">
          <AgentWindowAvatar agentType={item.agentType} title={item.title || item.paneId} />
          <div data-id={`agent-stack-card-meta-${item.paneId}`} className="min-w-0 flex-1">
            <div data-id={`agent-stack-card-title-${item.paneId}`} className="truncate text-sm font-medium text-zinc-100">{item.title || item.paneId}</div>
            <div data-id={`agent-stack-card-status-row-${item.paneId}`} className="mt-0.5 flex items-center gap-2 text-[11px] text-zinc-500">
              <span data-id={`agent-stack-card-pane-id-${item.paneId}`} className="font-mono">{item.paneId}</span>
              <button data-id={`agent-stack-card-copy-pane-${item.paneId}`} type="button" onClick={copyPaneId} className="rounded p-0.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-300">
                {copiedPaneId ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
              </button>
              {item.contextUsage != null ? <span data-id={`agent-stack-card-context-usage-${item.paneId}`}>{item.contextUsage}%</span> : null}
            </div>
          </div>
        </div>
        <button
          data-id={`agent-stack-card-history-${item.paneId}`}
          type="button"
          onClick={handleOpenHistory}
          aria-pressed={historyShortcutActive}
          className={`ml-3 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors ${historyShortcutActive ? 'bg-white/[0.08] text-zinc-100 ring-1 ring-white/[0.12]' : 'text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100'}`}
          title="More"
          aria-label="More"
        >
          <MoreHorizontal className="h-4 w-4" />
        </button>
      </div>
      <div data-id={`agent-stack-card-body-${item.paneId}`} className="relative min-h-0 flex-1 bg-black">
        {!item.isApiOnly && item.ttydSrc ? (
          <div data-id={`agent-stack-card-terminal-${item.paneId}`} className="h-full w-full">
            <WebFrame src={item.ttydSrc} className="h-full w-full border-0 bg-black" title={`stack-${item.paneId}`} />
          </div>
        ) : (
          <div data-id={`agent-stack-card-empty-${item.paneId}`} className="absolute inset-0 flex flex-col justify-between bg-[radial-gradient(circle_at_top,rgba(59,130,246,0.12),transparent_35%),linear-gradient(180deg,rgba(255,255,255,0.03),rgba(255,255,255,0.01))] p-4">
            <div data-id={`agent-stack-card-workspace-${item.paneId}`}>
              <div data-id={`agent-stack-card-workspace-label-${item.paneId}`} className="text-xs uppercase tracking-[0.24em] text-zinc-600">workspace</div>
              <div data-id={`agent-stack-card-workspace-value-${item.paneId}`} className="mt-2 truncate text-sm text-zinc-300">{item.workspace || `~/workers/${item.paneId}`}</div>
            </div>
            <div data-id={`agent-stack-card-empty-message-${item.paneId}`} className="rounded-xl border border-white/[0.06] bg-white/[0.03] p-3 text-sm text-zinc-300">
              {item.isApiOnly ? '该成员当前只支持 API 能力。' : active ? '实时终端已激活。' : '点击切换到该成员，在右侧查看完整历史。'}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
