import { Check, Copy, Folder, MessageSquare, Pencil, Settings } from 'lucide-react'
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
  onOpenPaneSession,
  onRenamePaneTitle,
}: {
  items: AgentCanvasItem[]
  activePaneId: string
  onActivePaneIdChange: (paneId: string) => void
  settingsShortcutActive: boolean
  renderHeaderControls?: (paneId: string) => React.ReactNode
  showHeaderButtons?: boolean
  onOpenPaneSettings: (paneId: string) => void
  onOpenPaneFiles: (paneId: string) => void
  onOpenPaneSession: (paneId: string) => void
  onRenamePaneTitle?: (paneId: string, nextTitle: string) => Promise<void> | void
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
          onOpenPaneSession={onOpenPaneSession}
          onRenamePaneTitle={onRenamePaneTitle}
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
  onOpenPaneSession,
  onRenamePaneTitle,
  onClick,
}: {
  item: AgentCanvasItem;
  active: boolean;
  settingsShortcutActive: boolean;
  headerControls?: React.ReactNode;
  showHeaderButtons: boolean;
  onOpenPaneSettings: (paneId: string) => void;
  onOpenPaneFiles: (paneId: string) => void;
  onOpenPaneSession: (paneId: string) => void;
  onRenamePaneTitle?: (paneId: string, nextTitle: string) => Promise<void> | void;
  onClick: () => void;
}) {
  const { t } = useTranslation('layout')
  const [copiedPaneId, setCopiedPaneId] = useState(false)
  const copiedPaneTimerRef = useRef<number | null>(null)
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState('')
  const titleInputRef = useRef<HTMLInputElement | null>(null)
  // IME composition tracking. On Mac's built-in Pinyin IME pressing Enter
  // emits compositionend → input → keydown in that order, so the keydown
  // arrives with isComposing=false even though the user's intent was to
  // confirm the composition. We mark a brief "just-composed" window so the
  // next Enter keydown is ignored for commit purposes.
  const composingRef = useRef(false)
  const justComposedRef = useRef(false)
  const justComposedTimerRef = useRef<number | null>(null)

  const displayTitle = item.title || item.paneId

  const beginTitleEdit = useCallback(() => {
    if (!onRenamePaneTitle) return
    setTitleDraft(displayTitle)
    setEditingTitle(true)
  }, [displayTitle, onRenamePaneTitle])

  const commitTitleEdit = useCallback(async () => {
    const next = titleDraft.trim()
    setEditingTitle(false)
    if (!onRenamePaneTitle) return
    if (!next || next === displayTitle) return
    try { await onRenamePaneTitle(item.paneId, next) } catch {}
  }, [titleDraft, displayTitle, onRenamePaneTitle, item.paneId])

  const cancelTitleEdit = useCallback(() => {
    setEditingTitle(false)
    setTitleDraft('')
  }, [])

  useEffect(() => {
    if (!editingTitle) return
    const el = titleInputRef.current
    if (!el) return
    el.focus()
    el.select()
  }, [editingTitle])

  useEffect(() => () => {
    if (copiedPaneTimerRef.current !== null) window.clearTimeout(copiedPaneTimerRef.current)
    if (justComposedTimerRef.current !== null) window.clearTimeout(justComposedTimerRef.current)
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

  const handleOpenSession = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneSession(item.paneId)
  }, [item.paneId, onOpenPaneSession])

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
            <div
              data-id={`agent-stack-card-title-${item.paneId}`}
              className="group/title flex h-5 min-w-0 items-center select-none"
              onDoubleClick={(event) => {
                if (!onRenamePaneTitle) return
                event.preventDefault()
                event.stopPropagation()
                beginTitleEdit()
              }}
              onClick={(event) => event.stopPropagation()}
            >
              {editingTitle ? (
                <input
                  ref={titleInputRef}
                  data-id={`agent-stack-card-title-input-${item.paneId}`}
                  type="text"
                  value={titleDraft}
                  onChange={(event) => setTitleDraft(event.target.value)}
                  onBlur={() => { void commitTitleEdit() }}
                  onCompositionStart={() => {
                    composingRef.current = true
                    justComposedRef.current = false
                    if (justComposedTimerRef.current !== null) {
                      window.clearTimeout(justComposedTimerRef.current)
                      justComposedTimerRef.current = null
                    }
                  }}
                  onCompositionEnd={() => {
                    composingRef.current = false
                    justComposedRef.current = true
                    if (justComposedTimerRef.current !== null) {
                      window.clearTimeout(justComposedTimerRef.current)
                    }
                    // 80ms covers the Mac Pinyin IME case where the keydown
                    // that ended composition fires AFTER compositionend.
                    justComposedTimerRef.current = window.setTimeout(() => {
                      justComposedRef.current = false
                      justComposedTimerRef.current = null
                    }, 80)
                  }}
                  onKeyDown={(event) => {
                    // The parent card has its own onKeyDown that turns Enter
                    // and Space into "activate pane". If we let those bubble
                    // while the user is typing, the card's preventDefault +
                    // re-render interrupts IME composition and the raw pinyin
                    // ends up committed instead of the candidate. Stop them
                    // at the input level unconditionally.
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.stopPropagation()
                    }
                    const imeBusy =
                      composingRef.current ||
                      justComposedRef.current ||
                      event.nativeEvent.isComposing ||
                      event.keyCode === 229
                    if (event.key === 'Enter') {
                      if (imeBusy) return
                      event.preventDefault()
                      ;(event.currentTarget as HTMLInputElement).blur()
                    } else if (event.key === 'Escape') {
                      if (imeBusy) return
                      event.preventDefault()
                      cancelTitleEdit()
                    }
                  }}
                  onClick={(event) => event.stopPropagation()}
                  onMouseDown={(event) => event.stopPropagation()}
                  onPointerDown={(event) => event.stopPropagation()}
                  className="m-0 block w-full min-w-0 truncate rounded-[3px] border-0 bg-blue-500/[0.10] p-0 text-sm font-medium leading-5 text-zinc-100 outline-none ring-1 ring-blue-500/40 focus:ring-blue-500/60"
                />
              ) : (
                <>
                  <span data-id={`agent-stack-card-title-text-${item.paneId}`} className="min-w-0 truncate text-sm font-medium leading-5 text-zinc-100">
                    {displayTitle}
                  </span>
                  {onRenamePaneTitle ? (
                    <button
                      type="button"
                      data-id={`agent-stack-card-title-edit-${item.paneId}`}
                      onClick={(event) => {
                        event.preventDefault()
                        event.stopPropagation()
                        beginTitleEdit()
                      }}
                      onDoubleClick={(event) => event.stopPropagation()}
                      onMouseDown={(event) => event.stopPropagation()}
                      title={t('agentStackEditTitle', { defaultValue: 'Rename' })}
                      aria-label={t('agentStackEditTitle', { defaultValue: 'Rename' })}
                      className="ml-1 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded text-zinc-600 opacity-0 transition-opacity group-hover/title:opacity-100 hover:bg-white/[0.06] hover:text-zinc-200 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500/40"
                    >
                      <Pencil className="h-3 w-3" />
                    </button>
                  ) : null}
                </>
              )}
            </div>
            <div data-id={`agent-stack-card-status-row-${item.paneId}`} className="mt-0.5 flex items-center gap-2 text-[11px] text-zinc-500">
              <span data-id={`agent-stack-card-pane-id-${item.paneId}`} className="font-mono">{item.paneId}</span>
              <button data-id={`agent-stack-card-copy-pane-${item.paneId}`} type="button" onClick={copyPaneId} className="rounded p-0.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-300">
                {copiedPaneId ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
              </button>
              {item.contextUsage != null ? <span data-id={`agent-stack-card-context-usage-${item.paneId}`}>{item.contextUsage}%</span> : null}
            </div>
          </div>
        </div>
        <div data-id={`agent-stack-card-header-right-${item.paneId}`} className="ml-3 flex items-center gap-2">
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
                data-id="agent-stack-card-session"
                type="button"
                onClick={handleOpenSession}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="Session"
                aria-label="Session"
              >
                <MessageSquare className="h-4 w-4" />
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
      {headerControls ? (
        <div data-id={`agent-stack-card-header-controls-${item.paneId}`} className="flex h-10 shrink-0 items-center justify-end gap-3 border-t border-white/[0.04] bg-black/[0.18] px-3">
          {headerControls}
        </div>
      ) : null}
    </div>
  )
}
