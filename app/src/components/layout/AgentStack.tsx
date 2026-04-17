import { Check, ChevronRight, Copy } from 'lucide-react'
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

function statusTone(status?: string) {
  switch ((status || '').trim().toLowerCase()) {
    case 'thinking':
    case 'tool_use':
      return { dot: 'bg-amber-400', text: 'thinking' }
    case 'restarting':
      return { dot: 'bg-sky-400', text: 'restarting' }
    case 'idle':
    case 'text':
    default:
      return { dot: 'bg-emerald-400', text: status || 'idle' }
  }
}

function AgentWindowAvatar({ agentType, title }: { agentType?: string; title: string }) {
  const normalizedAgentType = normalizeAgentType(agentType)
  const baseClassName = 'flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border shadow-sm'

  if (!normalizedAgentType) {
    return <div className={`${baseClassName} border-white/[0.08] bg-white/[0.03] text-zinc-400`}><span className="text-[11px] font-semibold uppercase">{title.slice(0, 1) || '?'}</span></div>
  }
  if (normalizedAgentType === 'openclaw') {
    return <div className={`${baseClassName} border-zinc-500/40 bg-zinc-300 text-zinc-950`}><span className="text-[16px] leading-none">🦞</span></div>
  }
  const iconMap: Record<string, { label: string; src: string; className?: string }> = {
    codex: { label: 'Codex', src: assetUrl('/assets/logos/openai.svg') },
    claude: { label: 'Claude', src: assetUrl('/assets/logos/claude-symbol.svg') },
    cicy: { label: 'CiCy', src: 'https://cicy-ai.com/logo.svg' },
    opencode: { label: 'OpenCode', src: assetUrl('/assets/logos/opencode.svg'), className: 'h-5 w-5' },
  }
  const icon = iconMap[normalizedAgentType]
  if (!icon) return null
  return <div className={`${baseClassName} border-zinc-500/40 bg-zinc-300`}><img src={icon.src} alt={icon.label} className={`${icon.className || 'h-4 w-4'} object-contain`} /></div>
}

export default function AgentStack({
  items,
  activePaneId,
  onActivePaneIdChange,
  showHistoryShortcut,
  onOpenPaneHistory,
}: {
  items: AgentCanvasItem[]
  activePaneId: string
  onActivePaneIdChange: (paneId: string) => void
  showHistoryShortcut: boolean
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
          showHistoryShortcut={showHistoryShortcut}
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
  showHistoryShortcut,
  onOpenPaneHistory,
  onClick,
}: {
  item: AgentCanvasItem;
  active: boolean;
  showHistoryShortcut: boolean;
  onOpenPaneHistory: (paneId: string) => void;
  onClick: () => void;
}) {
  const tone = statusTone(item.status)
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
      <div className="flex h-12 shrink-0 items-center border-b border-white/[0.07] bg-[linear-gradient(180deg,rgba(255,255,255,0.06),rgba(255,255,255,0.02))] px-3">
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <AgentWindowAvatar agentType={item.agentType} title={item.title || item.paneId} />
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium text-zinc-100">{item.title || item.paneId}</div>
            <div className="mt-0.5 flex items-center gap-2 text-[11px] text-zinc-500">
              <span className="font-mono">{item.paneId}</span>
              <button type="button" onClick={copyPaneId} className="rounded p-0.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-300">
                {copiedPaneId ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
              </button>
              <span className={`inline-block h-2 w-2 rounded-full ${tone.dot}`} />
              <span>{tone.text}</span>
              {item.contextUsage != null ? <span>{item.contextUsage}%</span> : null}
            </div>
          </div>
        </div>
        {showHistoryShortcut ? (
          <button
            type="button"
            onClick={handleOpenHistory}
            className="ml-3 inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
            title="查看历史"
            aria-label="查看历史"
          >
            <ChevronRight className="h-4 w-4" />
          </button>
        ) : null}
      </div>
      <div className="relative min-h-0 flex-1 bg-black">
        {!item.isApiOnly && item.ttydSrc ? (
          <WebFrame src={item.ttydSrc} className="h-full w-full border-0 bg-black" title={`stack-${item.paneId}`} />
        ) : (
          <div className="absolute inset-0 flex flex-col justify-between bg-[radial-gradient(circle_at_top,rgba(59,130,246,0.12),transparent_35%),linear-gradient(180deg,rgba(255,255,255,0.03),rgba(255,255,255,0.01))] p-4">
            <div>
              <div className="text-xs uppercase tracking-[0.24em] text-zinc-600">workspace</div>
              <div className="mt-2 truncate text-sm text-zinc-300">{item.workspace || `~/workers/${item.paneId}`}</div>
            </div>
            <div className="rounded-xl border border-white/[0.06] bg-white/[0.03] p-3 text-sm text-zinc-300">
              {item.isApiOnly ? '该成员当前只支持 API 能力。' : active ? '实时终端已激活。' : '点击切换到该成员，在右侧查看完整历史。'}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
