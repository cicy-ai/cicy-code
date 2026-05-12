import { Brain, Check, Copy, FileText, Folder, History, Settings, Wrench } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { defaultWorkerWorkspace } from '../../config'
import AgentAvatar from '../AgentAvatar'
import { WebFrame } from '../WebFrame'
import type { AgentCanvasItem } from './AgentCanvas'

export default function AgentStack({
  items,
  activePaneId,
  onActivePaneIdChange,
  settingsShortcutActive,
  renderHeaderControls,
  showHeaderButtons = true,
  onOpenPaneSettings,
  onOpenPaneFiles,
  onOpenPaneHistory,
  onOpenPaneTools,
  onOpenPaneBrain,
  onOpenPaneMeta,
}: {
  items: AgentCanvasItem[]
  activePaneId: string
  onActivePaneIdChange: (paneId: string) => void
  settingsShortcutActive: boolean
  renderHeaderControls?: (paneId: string) => React.ReactNode
  showHeaderButtons?: boolean
  onOpenPaneSettings: (paneId: string) => void
  onOpenPaneFiles: (paneId: string) => void
  onOpenPaneHistory: (paneId: string) => void
  onOpenPaneTools: (paneId: string) => void
  onOpenPaneBrain: (paneId: string) => void
  onOpenPaneMeta: (paneId: string) => void
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
          settingsShortcutActive={settingsShortcutActive}
          headerControls={renderHeaderControls?.(item.paneId)}
          showHeaderButtons={showHeaderButtons}
          onOpenPaneSettings={onOpenPaneSettings}
          onOpenPaneFiles={onOpenPaneFiles}
          onOpenPaneHistory={onOpenPaneHistory}
          onOpenPaneTools={onOpenPaneTools}
          onOpenPaneBrain={onOpenPaneBrain}
          onOpenPaneMeta={onOpenPaneMeta}
          onClick={() => onActivePaneIdChange(item.paneId)}
        />
      ))}
    </div>
  )
}

function AgentStackCard({
  item,
  active,
  settingsShortcutActive,
  headerControls,
  showHeaderButtons,
  onOpenPaneSettings,
  onOpenPaneFiles,
  onOpenPaneHistory,
  onOpenPaneTools,
  onOpenPaneBrain,
  onOpenPaneMeta,
  onClick,
}: {
  item: AgentCanvasItem;
  active: boolean;
  settingsShortcutActive: boolean;
  headerControls?: React.ReactNode;
  showHeaderButtons: boolean;
  onOpenPaneSettings: (paneId: string) => void;
  onOpenPaneFiles: (paneId: string) => void;
  onOpenPaneHistory: (paneId: string) => void;
  onOpenPaneTools: (paneId: string) => void;
  onOpenPaneBrain: (paneId: string) => void;
  onOpenPaneMeta: (paneId: string) => void;
  onClick: () => void;
}) {
  const { t } = useTranslation('layout')
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

    window.dispatchEvent(new CustomEvent('show-toast', { detail: t('agentStackCopyFailed', { value }) }))
  }, [handlePaneIdCopied, item.paneId])

  const handleOpenSettings = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneSettings(item.paneId)
  }, [item.paneId, onOpenPaneSettings])

  const handleOpenFiles = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneFiles(item.paneId)
  }, [item.paneId, onOpenPaneFiles])

  const handleOpenHistory = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneHistory(item.paneId)
  }, [item.paneId, onOpenPaneHistory])

  const handleOpenTools = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneTools(item.paneId)
  }, [item.paneId, onOpenPaneTools])

  const handleOpenBrain = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneBrain(item.paneId)
  }, [item.paneId, onOpenPaneBrain])

  const handleOpenMeta = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneMeta(item.paneId)
  }, [item.paneId, onOpenPaneMeta])

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
          <AgentAvatar
            agentType={item.agentType}
            title={item.title || item.paneId}
            dataId="agent-stack-card-avatar"
            variant="stack"
          />
          <div data-id={`agent-stack-card-info-${item.paneId}`} className="min-w-0 flex-1">
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
        <div className="ml-3 flex items-center gap-2">
          {headerControls ? (
            <div data-id={`agent-stack-card-header-controls-${item.paneId}`} className="flex h-full items-center gap-3">
              {headerControls}
            </div>
          ) : null}
          {showHeaderButtons ? (
            <div data-id="agent-stack-card-header-buttons" className="flex items-center gap-2">
              <button
                data-id="agent-stack-card-files"
                type="button"
                onClick={handleOpenFiles}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="Open Files"
                aria-label="Open Files"
              >
                <Folder className="h-4 w-4" />
              </button>
              <button
                data-id="agent-stack-card-history"
                type="button"
                onClick={handleOpenHistory}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="History"
                aria-label="History"
              >
                <History className="h-4 w-4" />
              </button>
              <button
                data-id="agent-stack-card-tools"
                type="button"
                onClick={handleOpenTools}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="Tools"
                aria-label="Tools"
              >
                <Wrench className="h-4 w-4" />
              </button>
              <button
                data-id="agent-stack-card-brain"
                type="button"
                onClick={handleOpenBrain}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="Brain"
                aria-label="Brain"
              >
                <Brain className="h-4 w-4" />
              </button>
              <button
                data-id="agent-stack-card-meta"
                type="button"
                onClick={handleOpenMeta}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="Meta"
                aria-label="Meta"
              >
                <FileText className="h-4 w-4" />
              </button>
              <button
                data-id="agent-stack-card-settings"
                type="button"
                onClick={handleOpenSettings}
                aria-pressed={settingsShortcutActive}
                className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors ${settingsShortcutActive ? 'bg-white/[0.08] text-zinc-100 ring-1 ring-white/[0.12]' : 'text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100'}`}
                title="Settings"
                aria-label="Settings"
              >
                <Settings className="h-4 w-4" />
              </button>
            </div>
          ) : null}
        </div>
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
              <div data-id={`agent-stack-card-workspace-value-${item.paneId}`} className="mt-2 truncate text-sm text-zinc-300">{item.workspace || defaultWorkerWorkspace(item.paneId)}</div>
            </div>
            <div data-id={`agent-stack-card-empty-message-${item.paneId}`} className="rounded-xl border border-white/[0.06] bg-white/[0.03] p-3 text-sm text-zinc-300">
              {item.isApiOnly ? t('agentStackApiOnly') : active ? t('agentStackActive') : t('agentStackInactiveHint')}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
