import { BookOpen, Braces, Brain, Check, Copy, Folder, History, LineChart, ListTodo, Pencil, Settings, ShieldCheck, X } from 'lucide-react'
import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { defaultWorkerWorkspace } from '../../config'
import { useApp } from '../../contexts/AppContext'
import AgentAvatar from '../AgentAvatar'
import { WebFrame } from '../WebFrame'
import { AgentInstallOverlay } from './AgentInstallOverlay'
import { ShellPanel } from '../terminal/ShellPanel'
import CurrentHistoryView from '../chat/CurrentHistoryView'
import DispatcherChat from '../chat/DispatcherChat'
import TipBelow from '../ui/TipBelow'
import { isCicyLiteAgent } from '../../lib/agentType'
// AgentCanvasItem outlived its namesake: the draggable-canvas component was
// dead code (never rendered, tree-shaken) and was deleted 2026-06-05; the
// item shape lives on as the stack's card model.
export interface AgentCanvasItem {
  paneId: string;
  title: string;
  agentType?: string;
  status?: string;
  contextUsage?: number | null;
  machineLabel?: string;
  ttydSrc?: string;
  workspace?: string;
  isPrimary?: boolean;
  isApiOnly?: boolean;
}

function AgentStack({
  items,
  activePaneId,
  onActivePaneIdChange,
  settingsShortcutActive,
  renderHeaderControls,
  showHeaderButtons = true,
  onOpenPaneSettings,
  onOpenPaneFiles,
  onOpenPaneSession,
  onOpenPaneTodo,
  onOpenPaneMemory,
  onOpenPaneContent,
  onRenamePaneTitle,
  todoCount = 0,
  auditAlertCount = 0,
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
  onOpenPaneTodo?: (paneId: string) => void
  onOpenPaneMemory?: (paneId: string) => void
  // Generic "open this right-panel content tab for the pane" — used for the
  // header buttons that mirror cli-content-tabs (knowledge / 审计日志 / 审计策略).
  onOpenPaneContent?: (paneId: string, tab: string) => void
  onRenamePaneTitle?: (paneId: string, nextTitle: string) => Promise<void> | void
  // Pending-todo count for the active pane; shown as a badge on its todo button.
  todoCount?: number
  // Open (unhandled) audit-alert count; shown as a badge on the 审计日志 button.
  auditAlertCount?: number
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  useTranslation('layout')

  // History is OWNED here (not per-card) so it follows the active agent:
  // switching agents switches the history shown. `historyPaneId` = the agent
  // whose history is open (null = closed). It renders INLINE inside that card's
  // body, layered over the WebFrame (no modal/portal) — see AgentStackCard.
  const [historyPaneId, setHistoryPaneId] = useState<string | null>(null)

  const toggleHistory = useCallback((paneId: string) => {
    setHistoryPaneId((cur) => (cur === paneId ? null : paneId))
  }, [])

  useEffect(() => {
    const container = containerRef.current
    if (!container || !activePaneId) return
    const target = container.querySelector<HTMLElement>(`[data-id="agent-stack-card-${activePaneId}"]`)
    if (!target) return
    target.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' })
  }, [activePaneId])

  // Switching the active agent switches the open history to follow that agent.
  useEffect(() => {
    if (!historyPaneId || !activePaneId || historyPaneId === activePaneId) return
    setHistoryPaneId(activePaneId)
  }, [activePaneId, historyPaneId])

  // Esc closes the history.
  useEffect(() => {
    if (!historyPaneId) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setHistoryPaneId(null) }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [historyPaneId])

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
          onOpenPaneTodo={onOpenPaneTodo}
          onOpenPaneMemory={onOpenPaneMemory}
          onOpenPaneContent={onOpenPaneContent}
          onRenamePaneTitle={onRenamePaneTitle}
          todoCount={activePaneId === item.paneId ? todoCount : 0}
          auditAlertCount={auditAlertCount}
          onClick={() => onActivePaneIdChange(item.paneId)}
          onToggleHistory={() => toggleHistory(item.paneId)}
          historyActive={historyPaneId === item.paneId}
        />
      ))}
    </div>
  )
}

// Memoized so a live conversation's per-token Workspace re-renders (driven by
// streaming chat state the panel doesn't consume) don't re-render the agent
// stack and its embedded ttyd iframes. All props are referentially stable
// across those renders (items via useMemo, callbacks via useCallback), so the
// shallow compare bails out; real changes (pane switch, status, contextUsage)
// still flow through because those props change identity.
export default memo(AgentStack)

function AgentStackCard({
  item,
  active,
  settingsShortcutActive,
  headerControls,
  showHeaderButtons,
  onOpenPaneSettings,
  onOpenPaneFiles,
  onOpenPaneSession,
  onOpenPaneTodo,
  onOpenPaneMemory,
  onOpenPaneContent,
  onRenamePaneTitle,
  todoCount = 0,
  auditAlertCount = 0,
  onClick,
  onToggleHistory,
  historyActive,
}: {
  item: AgentCanvasItem;
  active: boolean;
  settingsShortcutActive: boolean;
  headerControls?: React.ReactNode;
  showHeaderButtons: boolean;
  onOpenPaneSettings: (paneId: string) => void;
  onOpenPaneFiles: (paneId: string) => void;
  onOpenPaneSession: (paneId: string) => void;
  onOpenPaneTodo?: (paneId: string) => void;
  onOpenPaneMemory?: (paneId: string) => void;
  onOpenPaneContent?: (paneId: string, tab: string) => void;
  onRenamePaneTitle?: (paneId: string, nextTitle: string) => Promise<void> | void;
  todoCount?: number;
  auditAlertCount?: number;
  onClick: () => void;
  onToggleHistory: () => void;
  historyActive: boolean;
}) {
  const { t } = useTranslation(['layout', 'workspace'])
  const { globalVar } = useApp()  // helper_mode → hide the card header-right controls
  // History opens as a single shared popover owned by AgentStack (so switching
  // the active agent switches the history too). This card only toggles it and
  // reflects whether its own history is the one currently shown.
  const [copiedPaneId, setCopiedPaneId] = useState(false)
  // Bumped to force the ttyd terminal iframe to re-mount (e.g. after an install
  // overlay restarts the agent — the old WebSocket attached to the killed pane).
  const [termReloadNonce, setTermReloadNonce] = useState(0)
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

  // Inline history height as a fraction of the card body (default 2/3 → bottom
  // 1/3 keeps the live tmux visible). Draggable via the bottom-edge handle.
  const [historyHeightFrac, setHistoryHeightFrac] = useState(2 / 3)
  // While dragging, a transparent mask covers the body so the WebFrame's
  // <webview>/<iframe> can't swallow the pointer (which would kill the drag the
  // moment the cursor crosses into it).
  const [resizingHistory, setResizingHistory] = useState(false)
  const startHistoryResize = useCallback((event: React.PointerEvent) => {
    event.preventDefault()
    event.stopPropagation()
    const bodyEl = document.querySelector<HTMLElement>(`[data-id="agent-stack-card-body-${item.paneId}"]`)
    if (!bodyEl) return
    const rect = bodyEl.getBoundingClientRect()
    // The handle no longer sits on the overlay's bottom edge (the close footer is
    // below it). Capture the pointer→bottom-edge gap at grab time and keep it, so
    // the overlay tracks the pointer without a jump when the drag starts.
    const overlayEl = document.querySelector<HTMLElement>(`[data-id="agent-stack-card-history-inline-${item.paneId}"]`)
    const grabOffset = overlayEl ? event.clientY - overlayEl.getBoundingClientRect().bottom : 0
    setResizingHistory(true)
    const onMove = (ev: PointerEvent) => {
      const frac = (ev.clientY - grabOffset - rect.top) / rect.height
      // clamp so neither history nor the tmux strip below ever fully collapses.
      setHistoryHeightFrac(Math.min(0.9, Math.max(0.2, frac)))
    }
    const onUp = () => {
      setResizingHistory(false)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }, [item.paneId])

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

  const handleOpenTodo = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneTodo?.(item.paneId)
  }, [item.paneId, onOpenPaneTodo])

  const handleOpenRequest = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneContent?.(item.paneId, 'tools')
  }, [item.paneId, onOpenPaneContent])
  const handleOpenKnowledge = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneContent?.(item.paneId, 'knowledge')
  }, [item.paneId, onOpenPaneContent])
  const handleOpenAudit = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneContent?.(item.paneId, 'audit')
  }, [item.paneId, onOpenPaneContent])
  const handleOpenMemory = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneMemory?.(item.paneId)
  }, [item.paneId, onOpenPaneMemory])

  return (
    <div
      data-id={`agent-stack-card-${item.paneId}`}
      onClick={onClick}
      // No role="button"/tabIndex/keyboard activation: only the ACTIVE card is
      // ever visible (display:none switching), so key-activating it was a
      // no-op — and its Space/Enter preventDefault swallowed keystrokes from
      // inputs inside the card (e.g. the dispatcher prompt).
      className={`absolute inset-0 overflow-hidden text-left transition-colors ${active ? 'flex flex-col bg-[#0c0d10]' : 'hidden'}`}
      style={{ display: active ? 'flex' : 'none' }}
    >
      <div data-id={`agent-stack-card-header-${item.paneId}`} className="flex h-12 shrink-0 items-center border-b border-[var(--vsc-border)] px-3">
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
        {/* The terminal/history view switch lives floating at the top-center of
            the card body now (see agent-stack-card-view-tabs below). */}
        {!globalVar?.helper_mode && (
        <div data-id={`agent-stack-card-header-right-${item.paneId}`} className="ml-2 flex items-center gap-1">
          {showHeaderButtons ? (
          <div data-id="agent-stack-card-header-buttons" className="flex items-center gap-1">
            {onOpenPaneTodo && (
              <TipBelow label={t('tabTodo', { ns: 'workspace' })}>
              <button
                data-id="agent-stack-card-todo"
                type="button"
                onClick={handleOpenTodo}
                className="relative inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                aria-label={t('tabTodo', { ns: 'workspace' })}
              >
                <ListTodo className="h-4 w-4" />
                {todoCount > 0 && (
                  <span
                    data-id="agent-stack-card-todo-badge"
                    className="absolute -right-0.5 -top-0.5 inline-flex h-[15px] min-w-[15px] items-center justify-center rounded-full bg-red-500 px-1 text-[9px] font-semibold leading-none text-white tabular-nums"
                  >
                    {todoCount > 99 ? '99+' : todoCount}
                  </span>
                )}
              </button>
              </TipBelow>
            )}
            <TipBelow label={t('tabFiles', { ns: 'workspace' })}>
            <button
              data-id="agent-stack-card-files"
              type="button"
              onClick={handleOpenFiles}
              className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
              aria-label={t('tabFiles', { ns: 'workspace' })}
            >
              <Folder className="h-4 w-4" />
            </button>
            </TipBelow>
            <TipBelow label={t('tabSession', { ns: 'workspace' })}>
            <button
              data-id="agent-stack-card-session"
              type="button"
              onClick={handleOpenSession}
              className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
              aria-label={t('tabSession', { ns: 'workspace' })}
            >
              <LineChart className="h-4 w-4" />
            </button>
            </TipBelow>
            {onOpenPaneContent && (
              <TipBelow label={t('tabRequest', { ns: 'workspace' })}>
              <button
                data-id="agent-stack-card-request"
                type="button"
                onClick={handleOpenRequest}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                aria-label={t('tabRequest', { ns: 'workspace' })}
              >
                <Braces className="h-4 w-4" />
              </button>
              </TipBelow>
            )}
            {onOpenPaneContent && (
              <TipBelow label={t('tabKnowledge', { ns: 'workspace' })}>
              <button
                data-id="agent-stack-card-knowledge"
                type="button"
                onClick={handleOpenKnowledge}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                aria-label={t('tabKnowledge', { ns: 'workspace' })}
              >
                <BookOpen className="h-4 w-4" />
              </button>
              </TipBelow>
            )}
            {onOpenPaneMemory && (
              <TipBelow label={t('tabMemory', { ns: 'workspace' })}>
              <button
                data-id="agent-stack-card-memory"
                type="button"
                onClick={handleOpenMemory}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                aria-label={t('tabMemory', { ns: 'workspace' })}
              >
                <Brain className="h-4 w-4" />
              </button>
              </TipBelow>
            )}
            {onOpenPaneContent && (
              <TipBelow label={t('tabAudit', { ns: 'audit', defaultValue: '审计' })}>
              <button
                data-id="agent-stack-card-audit"
                type="button"
                onClick={handleOpenAudit}
                className="relative inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                aria-label={t('tabAudit', { ns: 'audit', defaultValue: '审计' })}
              >
                <ShieldCheck className="h-4 w-4" />
                {auditAlertCount > 0 && (
                  <span
                    data-id="agent-stack-card-audit-badge"
                    className="absolute -right-0.5 -top-0.5 inline-flex h-[15px] min-w-[15px] items-center justify-center rounded-full bg-red-500 px-1 text-[9px] font-semibold leading-none text-white tabular-nums"
                  >
                    {auditAlertCount > 99 ? '99+' : auditAlertCount}
                  </span>
                )}
              </button>
              </TipBelow>
            )}
            <TipBelow label={t('tabSettings', { ns: 'workspace' })}>
            <button
              data-id="agent-stack-card-settings"
              type="button"
              onClick={handleOpenSettings}
              aria-pressed={settingsShortcutActive}
              className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors ${settingsShortcutActive ? 'bg-white/[0.08] text-zinc-100 ring-1 ring-white/[0.12]' : 'text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100'}`}
              aria-label={t('tabSettings', { ns: 'workspace' })}
            >
              <Settings className="h-4 w-4" />
            </button>
            </TipBelow>
          </div>
          ) : null}
        </div>
        )}
      </div>
      <div data-id={`agent-stack-card-body-${item.paneId}`} className="relative min-h-0 flex-1 bg-black">
        {/* Terminal ⇄ History view switch — floats at the top-center of the body
            for every non-cicy card (cicy/dispatcher cards are chat-first already). */}
        {!isCicyLiteAgent(item.agentType) && (
          <div
            data-id={`agent-stack-card-view-tabs-${item.paneId}`}
            className="absolute left-1/2 top-2 z-20 -translate-x-1/2"
            onClick={(event) => event.stopPropagation()}
          >
            <button
              data-id={`agent-stack-card-view-tab-history-${item.paneId}`}
              type="button"
              onClick={(event) => { event.stopPropagation(); onToggleHistory() }}
              className="inline-flex items-center gap-1.5 rounded-full border border-white/[0.08] bg-black/70 px-3 py-1 text-[11px] font-semibold leading-none tracking-[0.02em] text-zinc-300 shadow-[0_2px_8px_rgba(0,0,0,0.45)] backdrop-blur transition-colors hover:bg-white/[0.08] hover:text-zinc-100"
            >
              <History className="h-3 w-3" />
              {t('agentStackViewSession', { defaultValue: '历史' })}
            </button>
          </div>
        )}
        {isCicyLiteAgent(item.agentType) ? (
          // Dispatcher (PM) agents are chat-first on the web: history view +
          // prompt bar instead of the raw REPL terminal. The input feeds the
          // same /api/tmux/send pipe, so the terminal/TG channels stay in sync.
          <DispatcherChat paneId={item.paneId} active={active} agentType={item.agentType || 'cicy'} title={item.title} />
        ) : !item.isApiOnly && item.ttydSrc ? (
          // Keep the terminal mounted while History is showing so its ttyd
          // WebSocket isn't torn down (and re-attached) on every toggle.
          <div
            data-id={`agent-stack-card-terminal-${item.paneId}`}
            className="h-full w-full"
          >
            <WebFrame key={`${item.paneId}-${termReloadNonce}`} src={item.ttydSrc} className="h-full w-full border-0 bg-black" title={`stack-${item.paneId}`} />
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
        {/* History — INLINE overlay over the TOP 2/3 of the WebFrame (no modal).
            The terminal stays mounted underneath (its ttyd WS isn't torn down);
            history covers the top two-thirds with the clean q list, and the
            bottom 1/3 keeps the live tmux (its latest output) visible so you can
            still see what the agent is doing. Owned by the parent (follows the
            active agent), state passed down. */}
        {historyActive && !isCicyLiteAgent(item.agentType) ? (
          <div
            data-id={`agent-stack-card-history-inline-${item.paneId}`}
            onClick={(event) => event.stopPropagation()}
            className="absolute inset-x-0 top-0 z-30 flex flex-col border-b border-white/[0.1] bg-[#0c0d10] shadow-[0_8px_24px_rgba(0,0,0,0.5)]"
            style={{ height: `${historyHeightFrac * 100}%` }}
          >
            <div data-id={`agent-stack-card-history-inline-body-${item.paneId}`} className="min-h-0 flex-1 overflow-hidden">
              {/* Always prompts-only (no toggle) — this view IS the prompt list. */}
              <CurrentHistoryView key={item.paneId} paneId={item.paneId} open promptsOnly fullWidth leftAlignQuestions agentType={item.agentType || ''} />
            </div>
            {/* Close centered; ESC hint pinned to the left. */}
            <div data-id={`agent-stack-card-history-inline-header-${item.paneId}`} className="relative flex shrink-0 items-center justify-center border-t border-white/[0.06] px-4 py-2">
              <span data-id={`agent-stack-card-history-inline-esc-hint-${item.paneId}`} className="absolute left-4 select-none text-[11px] text-zinc-500">按 ESC 关闭</span>
              <button
                type="button"
                data-id={`agent-stack-card-history-inline-close-${item.paneId}`}
                onClick={(event) => { event.stopPropagation(); onToggleHistory() }}
                className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-rose-500/15 text-rose-300 ring-1 ring-rose-500/30 transition-colors hover:bg-rose-500/30 hover:text-rose-100"
                aria-label="Close"
                title="关闭 (Esc)"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            {/* Draggable edge at the VERY bottom (below close): resize history vs the
                live tmux strip below. Sitting on the bottom edge keeps the handle
                under the pointer, so the drag starts with no jump. */}
            <div
              data-id={`agent-stack-card-history-inline-resize-${item.paneId}`}
              onPointerDown={startHistoryResize}
              className="group relative z-10 flex h-3 shrink-0 cursor-row-resize items-center justify-center"
              aria-label="拖拽调整历史高度"
            >
              <div className="h-1 w-10 rounded-full bg-white/20 transition-colors group-hover:bg-white/40" />
            </div>
          </div>
        ) : null}
        {/* Drag mask: over the WHOLE body (incl. the WebFrame) only while resizing,
            so the webview/iframe can't capture the pointer and break the drag. */}
        {historyActive && resizingHistory ? (
          <div
            data-id={`agent-stack-card-history-resize-mask-${item.paneId}`}
            className="absolute inset-0 z-40 cursor-row-resize"
          />
        ) : null}
        {/* Install prompt for an un-installed coding CLI — overlays just this
            agent's body, self-hides when the CLI is present. */}
        <AgentInstallOverlay paneId={item.paneId} agentType={item.agentType} active={active} onReloadTerminal={() => setTermReloadNonce((n) => n + 1)} />
      </div>
      {headerControls ? (
        <div data-id={`agent-stack-card-header-controls-${item.paneId}`} className="flex h-10 shrink-0 items-center justify-end gap-3 border-t border-white/[0.04] bg-black/[0.18] px-3">
          {headerControls}
        </div>
      ) : null}
      {!item.isApiOnly && item.ttydSrc ? (
        <ShellPanel agentId={item.paneId} ttydSrc={item.ttydSrc} active={active} />
      ) : null}
    </div>
  )
}
