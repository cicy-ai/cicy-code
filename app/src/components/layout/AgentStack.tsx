import { Brain, Check, Copy, Folder, History, LineChart, ListTodo, Package, Pencil, Settings, X } from 'lucide-react'
import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { defaultWorkerWorkspace } from '../../config'
import apiService from '../../services/api'
import AgentAvatar from '../AgentAvatar'
import { WebFrame } from '../WebFrame'
import { ShellPanel } from '../terminal/ShellPanel'
import CurrentHistoryView from '../chat/CurrentHistoryView'
import DispatcherChat from '../chat/DispatcherChat'
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
  onOpenPaneArtifact,
  onRenamePaneTitle,
  todoCount = 0,
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
  onOpenPaneArtifact?: (paneId: string) => void
  onRenamePaneTitle?: (paneId: string, nextTitle: string) => Promise<void> | void
  // Pending-todo count for the active pane; shown as a badge on its todo button.
  todoCount?: number
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const { t } = useTranslation('layout')

  // History popover is OWNED here (not per-card) so it follows the active agent:
  // switching agents switches the history shown. `historyPaneId` = the agent
  // whose history is open (null = closed).
  const [historyPaneId, setHistoryPaneId] = useState<string | null>(null)
  // Popover is snapped INSIDE the card's terminal area: left/width/top from that
  // terminal's rect, with its bottom inset 88px from the terminal's bottom.
  const [historyPos, setHistoryPos] = useState<{ top: number; left: number; width: number; bottom: number } | null>(null)
  const [promptsOnly, setPromptsOnly] = useState(false)

  // 吸附在该卡片终端 agent-stack-card-terminal-<paneId> 里,按 inset 内缩:
  // top 20 / bottom 120 / left 20 / right 20(相对终端矩形)。终端没挂载返回 null。
  const computeHistoryPos = useCallback((paneId: string) => {
    const panel = document.querySelector<HTMLElement>(`[data-id="agent-stack-card-terminal-${paneId}"]`)
    if (!panel) return null
    const rect = panel.getBoundingClientRect()
    return {
      top: rect.top + 20,
      left: rect.left + 20,
      width: Math.max(0, rect.width - 40),
      bottom: Math.max(0, window.innerHeight - rect.bottom + 120),
    }
  }, [])

  const toggleHistory = useCallback((paneId: string) => {
    setHistoryPaneId((cur) => {
      if (cur === paneId) return null
      const pos = computeHistoryPos(paneId)
      if (pos) setHistoryPos(pos)
      return paneId
    })
  }, [computeHistoryPos])

  useEffect(() => {
    const container = containerRef.current
    if (!container || !activePaneId) return
    const target = container.querySelector<HTMLElement>(`[data-id="agent-stack-card-${activePaneId}"]`)
    if (!target) return
    target.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' })
  }, [activePaneId])

  // Switching the active agent switches the open history to that agent, and
  // re-anchors to the panel once the active card has scrolled into view.
  useEffect(() => {
    if (!historyPaneId || !activePaneId || historyPaneId === activePaneId) return
    setHistoryPaneId(activePaneId)
    const id = window.setTimeout(() => {
      const pos = computeHistoryPos(activePaneId)
      if (pos) setHistoryPos(pos)
    }, 320)
    return () => window.clearTimeout(id)
  }, [activePaneId, historyPaneId, computeHistoryPos])

  // Popover is pinned to the terminal rect → re-anchor on window resize while
  // it's open so left/width/top/bottom stay aligned to that terminal.
  useEffect(() => {
    if (!historyPaneId) return
    const onResize = () => { const pos = computeHistoryPos(historyPaneId); if (pos) setHistoryPos(pos) }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [historyPaneId, computeHistoryPos])

  // Esc closes the popover.
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
          onOpenPaneArtifact={onOpenPaneArtifact}
          onRenamePaneTitle={onRenamePaneTitle}
          todoCount={activePaneId === item.paneId ? todoCount : 0}
          onClick={() => onActivePaneIdChange(item.paneId)}
          onToggleHistory={() => toggleHistory(item.paneId)}
          historyActive={historyPaneId === item.paneId}
        />
      ))}
      {historyPaneId && historyPos ? createPortal(
        <>
          {/* invisible catcher: click outside the popover closes it */}
          <div data-id="agent-stack-history-popover-catcher" className="fixed inset-0 z-[120]" onClick={() => setHistoryPaneId(null)} />
          <div
            data-id={`agent-stack-card-history-popover-${historyPaneId}`}
            onClick={(event) => event.stopPropagation()}
            className="fixed z-[121] flex flex-col rounded-2xl border border-white/[0.1] bg-[#0b0b0d] shadow-2xl"
            // 吸附在卡片终端里:left/width/top 取自该终端,bottom 距终端底部 88px。
            style={{ top: historyPos.top, left: historyPos.left, width: historyPos.width, bottom: historyPos.bottom }}
          >
            <div data-id="agent-stack-card-history-popover-header" className="relative flex shrink-0 items-center gap-2.5 rounded-t-2xl border-b border-white/[0.06] px-4 py-2.5">
              <History className="h-4 w-4 shrink-0 text-zinc-400" />
              <span className="text-sm font-semibold text-zinc-200">{t('agentStackViewSession', { defaultValue: '历史' })}</span>
              <span className="truncate text-xs text-zinc-600">{historyPaneId}</span>
              <div className="flex-1" />
              <label
                data-id={`agent-stack-card-history-prompts-only-${historyPaneId}`}
                className="inline-flex cursor-pointer select-none items-center gap-1.5 text-xs font-medium text-zinc-400 transition-colors hover:text-zinc-200"
              >
                <input
                  type="checkbox"
                  data-id={`agent-stack-card-history-prompts-only-input-${historyPaneId}`}
                  checked={promptsOnly}
                  onChange={(event) => setPromptsOnly(event.target.checked)}
                  className="h-3.5 w-3.5 cursor-pointer accent-blue-500"
                />
                {t('agentStackPromptsOnly', { defaultValue: '只显示 prompt' })}
              </label>
              <button
                type="button"
                data-id="agent-stack-card-history-popover-close"
                onClick={() => setHistoryPaneId(null)}
                className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                aria-label="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <div data-id="agent-stack-card-history-popover-body" className="min-h-0 flex-1 overflow-hidden rounded-b-2xl">
              <CurrentHistoryView key={historyPaneId} paneId={historyPaneId} open promptsOnly={promptsOnly} />
            </div>
          </div>
        </>,
        document.body,
      ) : null}
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
  onOpenPaneArtifact,
  onRenamePaneTitle,
  todoCount = 0,
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
  onOpenPaneArtifact?: (paneId: string) => void;
  onRenamePaneTitle?: (paneId: string, nextTitle: string) => Promise<void> | void;
  todoCount?: number;
  onClick: () => void;
  onToggleHistory: () => void;
  historyActive: boolean;
}) {
  const { t } = useTranslation('layout')
  // History opens as a single shared popover owned by AgentStack (so switching
  // the active agent switches the history too). This card only toggles it and
  // reflects whether its own history is the one currently shown.
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

  const handleOpenTodo = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneTodo?.(item.paneId)
  }, [item.paneId, onOpenPaneTodo])

  const handleOpenMemory = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneMemory?.(item.paneId)
  }, [item.paneId, onOpenPaneMemory])

  const handleOpenArtifact = useCallback((event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    onOpenPaneArtifact?.(item.paneId)
  }, [item.paneId, onOpenPaneArtifact])

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
        {/* Dispatcher cards ARE the history view — the popover toggle is
            redundant there. */}
        {item.agentType !== 'dispatcher' ? (
        <div
          data-id={`agent-stack-card-view-tabs-${item.paneId}`}
          className="ml-2 inline-flex shrink-0 items-center"
          onClick={(event) => event.stopPropagation()}
        >
          <button
            data-id={`agent-stack-card-view-tab-history-${item.paneId}`}
            type="button"
            onClick={(event) => { event.stopPropagation(); onToggleHistory() }}
            className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-1 text-[11px] font-semibold leading-none tracking-[0.02em] transition-all ${
              historyActive
                ? 'border-blue-400/50 bg-gradient-to-b from-blue-500 to-blue-600 text-white shadow-[0_1px_3px_rgba(37,99,235,0.5)]'
                : 'border-white/[0.08] bg-black/40 text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-100'
            }`}
          >
            <History className="h-3 w-3" />
            {t('agentStackViewSession', { defaultValue: '历史' })}
          </button>
        </div>
        ) : null}
        <div data-id={`agent-stack-card-header-right-${item.paneId}`} className="ml-2 flex items-center gap-1">
          {showHeaderButtons ? (
          <div data-id="agent-stack-card-header-buttons" className="flex items-center gap-1">
            {onOpenPaneTodo && (
              <button
                data-id="agent-stack-card-todo"
                type="button"
                onClick={handleOpenTodo}
                className="relative inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="Todo"
                aria-label="Todo"
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
            )}
            <button
              data-id="agent-stack-card-files"
              type="button"
              onClick={handleOpenFiles}
              className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
              title="Files"
              aria-label="Files"
            >
              <Folder className="h-4 w-4" />
            </button>
            <button
              data-id="agent-stack-card-session"
              type="button"
              onClick={handleOpenSession}
              className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
              title="分析"
              aria-label="分析"
            >
              <LineChart className="h-4 w-4" />
            </button>
            {onOpenPaneMemory && (
              <button
                data-id="agent-stack-card-memory"
                type="button"
                onClick={handleOpenMemory}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="Memory"
                aria-label="Memory"
              >
                <Brain className="h-4 w-4" />
              </button>
            )}
            {onOpenPaneArtifact && (
              <button
                data-id="agent-stack-card-artifact"
                type="button"
                onClick={handleOpenArtifact}
                className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
                title="产物"
                aria-label="Artifact"
              >
                <Package className="h-4 w-4" />
              </button>
            )}
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
        {item.agentType === 'dispatcher' ? (
          // Dispatcher (PM) agents are chat-first on the web: history view +
          // prompt bar instead of the raw REPL terminal. The input feeds the
          // same /api/tmux/send pipe, so the terminal/TG channels stay in sync.
          <DispatcherChat paneId={item.paneId} active={active} />
        ) : !item.isApiOnly && item.ttydSrc ? (
          // Keep the terminal mounted while History is showing so its ttyd
          // WebSocket isn't torn down (and re-attached) on every toggle.
          <div
            data-id={`agent-stack-card-terminal-${item.paneId}`}
            className="h-full w-full"
          >
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
      {!item.isApiOnly && item.ttydSrc ? (
        <ShellPanel agentId={item.paneId} ttydSrc={item.ttydSrc} active={active} />
      ) : null}
    </div>
  )
}
