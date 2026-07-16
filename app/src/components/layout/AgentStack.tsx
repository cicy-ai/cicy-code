// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { BookOpen, Braces, Brain, Check, Columns2, Copy, Folder, History, LineChart, ListTodo, Loader2, MoreHorizontal, Paperclip, Pencil, Settings, ShieldCheck, X } from 'lucide-react'
import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { defaultWorkerWorkspace } from '../../config'
import { useApp } from '../../contexts/AppContext'
import AgentAvatar from '../AgentAvatar'
import { WebFrame } from '../WebFrame'
import { AgentInstallOverlay } from './AgentInstallOverlay'
import { ShellPanel } from '../terminal/ShellPanel'
import TerminalView, { shouldUseTerminalView } from '../terminal/TerminalView'
import CurrentHistoryView from '../chat/CurrentHistoryView'
import DispatcherChat from '../chat/DispatcherChat'
import { isCicyLiteAgent } from '../../lib/agentType'
import { replAttachmentMarkdown } from '../../lib/attachmentMarkdown'
import apiService from '../../services/api'

// Header attach button for NON-cicy agents (claude/codex/opencode run in tmux).
// Like dispatcher-chat-attach but "upload → send immediately": pick file(s) →
// upload to the pane's asset store → send them straight into the agent's REPL
// as a markdown LINK — [name](abs) — via the same /api/tmux/send pipe, so the
// agent can Read the real host path. No staging, no text box.
//
// NOT the image form `![name](abs)`, even for images. This goes into a CLI
// agent's REPL, and in Claude Code a line that STARTS with `!` is the run-a-shell
// -command prefix. Each attachment is its own line, so `![x.png](/path)` was
// being executed as the shell command `[x.png](/path)` — zsh reads `[...]` as a
// glob and answers `bad pattern: [x.png](…)`. The image never reached the agent.
// The `!` only ever bought an inline thumbnail in the web history; it cost the
// send. The agent Reads the path either way.
function AttachSendButton({ paneId }: { paneId: string }) {
  const { t } = useTranslation('chat')
  const inputRef = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const onFiles = useCallback(async (files: FileList | null) => {
    const list = files ? Array.from(files) : []
    if (!list.length) return
    setBusy(true)
    try {
      const parts: string[] = []
      for (const file of list) {
        const resp: any = await apiService.uploadAssetFile(paneId, file)
        const f = resp?.data?.file || {}
        const ref = String(f.file_ref || f.fileRef || '')
        const url = String(f.url || f.URL || '')
        const abs = ref ? '/' + ref.replace(/^file:\/\//, '').replace(/^\/+/, '') : url
        if (!abs) continue
        parts.push(replAttachmentMarkdown(file.name, abs))
      }
      if (parts.length) await apiService.sendCommand(paneId, parts.join('\n\n'), true)
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: '附件上传失败' }))
    } finally {
      setBusy(false)
    }
  }, [paneId])
  return (
    <div data-id={`agent-stack-attach-${paneId}`} className="inline-flex">
      <input
        ref={inputRef}
        data-id={`agent-stack-attach-input-${paneId}`}
        type="file"
        multiple
        accept="*"
        className="hidden"
        onChange={(e) => { void onFiles(e.target.files); e.target.value = '' }}
      />
      <button
        type="button"
        data-id={`agent-stack-attach-button-${paneId}`}
        disabled={busy}
        onClick={() => inputRef.current?.click()}
        title={t('attachPasteHint', { defaultValue: '你可以直接 Ctrl / Cmd + V 复制图片和文档给 Agent' })}
        aria-label="Attach and send"
        className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-zinc-400 transition-colors hover:bg-white/[0.08] hover:text-zinc-200 disabled:opacity-50"
      >
        {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Paperclip className="h-4 w-4" />}
      </button>
    </div>
  )
}
// One entry in the card's ⋯ menu.
interface CardMenuItem {
  id: string
  label: string
  icon: React.ReactNode
  onClick: (event: React.MouseEvent<HTMLButtonElement>) => void
  badge?: number
  active?: boolean
}

// CardMoreMenu collapses the card header's action row into a single ⋯ button.
//
// The badges are the reason this isn't a pure visual change: todo and audit
// carry unread counts, and hiding a button hides its badge. So the counts are
// SUMMED onto the trigger — otherwise "collapse the header" would quietly mean
// "stop telling me there's something to look at".
function CardMoreMenu({ paneId, items }: { paneId: string; items: CardMenuItem[] }) {
  const { t } = useTranslation('workspace')
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const totalBadge = items.reduce((sum, it) => sum + (it.badge || 0), 0)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div data-id={`agent-stack-card-more-${paneId}`} ref={rootRef} className="relative">
      <button
        data-id={`agent-stack-card-more-button-${paneId}`}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t('more', { defaultValue: '更多' })}
        onClick={(event) => { event.stopPropagation(); setOpen((v) => !v) }}
        className={`relative inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors ${open ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100'}`}
      >
        <MoreHorizontal className="h-4 w-4" />
        {totalBadge > 0 && (
          <span
            data-id={`agent-stack-card-more-badge-${paneId}`}
            className="absolute -right-0.5 -top-0.5 inline-flex h-[15px] min-w-[15px] items-center justify-center rounded-full bg-red-500 px-1 text-[9px] font-semibold leading-none text-white tabular-nums"
          >
            {totalBadge > 99 ? '99+' : totalBadge}
          </span>
        )}
      </button>

      {open && (
        <div
          data-id={`agent-stack-card-more-menu-${paneId}`}
          role="menu"
          onClick={(event) => event.stopPropagation()}
          className="absolute right-0 top-full z-30 mt-1 min-w-[168px] overflow-hidden rounded-lg border border-white/[0.08] bg-[#141417] py-1 shadow-[0_8px_28px_rgba(0,0,0,0.6)]"
        >
          {items.map((it) => (
            <button
              key={it.id}
              data-id={it.id}
              type="button"
              role="menuitem"
              onClick={(event) => { setOpen(false); it.onClick(event) }}
              className={`flex w-full items-center gap-2.5 px-3 py-2 text-left text-[13px] leading-none transition-colors ${it.active ? 'bg-white/[0.06] text-zinc-100' : 'text-zinc-300 hover:bg-white/[0.06] hover:text-zinc-100'}`}
            >
              <span className="text-zinc-500">{it.icon}</span>
              <span className="flex-1">{it.label}</span>
              {(it.badge || 0) > 0 && (
                <span
                  data-id={`${it.id}-badge`}
                  className="inline-flex h-[15px] min-w-[15px] items-center justify-center rounded-full bg-red-500 px-1 text-[9px] font-semibold leading-none text-white tabular-nums"
                >
                  {(it.badge as number) > 99 ? '99+' : it.badge}
                </span>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

// 分屏持久化(localStorage):钉住的右侧 pane、左侧 pane、宽度比例。
// 刷新页面后据此还原分屏。
const SPLIT_PANE_KEY = 'agent-stack-split-pane'
const SPLIT_LEFT_KEY = 'agent-stack-split-left'
const SPLIT_RATIO_KEY = 'agent-stack-split-ratio'

// SplitMenuButton opens the side-by-side (分屏) view: pick another agent to pin
// on the right half, swap it, or close the split. Rendered in the card header
// next to the ⋯ menu on every visible card, so either half can drive the split.
function SplitMenuButton({ paneId, candidates, isSplit, onPick, onClose }: {
  paneId: string
  candidates: { paneId: string; title: string }[]
  isSplit: boolean
  onPick: (paneId: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation('layout')
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => { if (!rootRef.current?.contains(e.target as Node)) setOpen(false) }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])
  return (
    <div data-id={`agent-stack-card-split-${paneId}`} ref={rootRef} className="relative">
      <button
        data-id={`agent-stack-card-split-button-${paneId}`}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        title={t('agentStackSplit', { defaultValue: '分屏' })}
        aria-label={t('agentStackSplit', { defaultValue: '分屏' })}
        onClick={(event) => { event.stopPropagation(); setOpen((v) => !v) }}
        className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors ${isSplit || open ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-100'}`}
      >
        <Columns2 className="h-4 w-4" />
      </button>
      {open && (
        <div
          data-id={`agent-stack-card-split-menu-${paneId}`}
          role="menu"
          onClick={(event) => event.stopPropagation()}
          className="absolute right-0 top-full z-30 mt-1 max-h-72 min-w-[190px] overflow-y-auto rounded-lg border border-white/[0.08] bg-[#141417] py-1 shadow-[0_8px_28px_rgba(0,0,0,0.6)]"
        >
          {isSplit ? (
            <button
              data-id={`agent-stack-card-split-close-${paneId}`}
              type="button"
              role="menuitem"
              onClick={() => { setOpen(false); onClose() }}
              className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-[13px] leading-none text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
            >
              <span className="text-zinc-500"><X className="h-4 w-4" /></span>
              <span className="flex-1">{t('agentStackSplitClose', { defaultValue: '关闭分屏' })}</span>
            </button>
          ) : null}
          {candidates.length > 0 ? (
            <div data-id={`agent-stack-card-split-menu-hint-${paneId}`} className="px-3 pb-1 pt-1.5 text-[11px] text-zinc-600">
              {t('agentStackSplitPick', { defaultValue: '右侧显示…' })}
            </div>
          ) : null}
          {candidates.map((c) => (
            <button
              key={c.paneId}
              data-id={`agent-stack-card-split-pick-${c.paneId}`}
              type="button"
              role="menuitem"
              onClick={() => { setOpen(false); onPick(c.paneId) }}
              className="flex w-full items-center gap-2.5 px-3 py-2 text-left text-[13px] leading-none text-zinc-300 transition-colors hover:bg-white/[0.06] hover:text-zinc-100"
            >
              <span className="min-w-0 flex-1 truncate">{c.title || c.paneId}</span>
              <span className="shrink-0 font-mono text-[10px] text-zinc-600">{c.paneId}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

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

  // 分屏:右半固定钉住 splitPaneId,左半跟随 active agent(团队里点别的
  // agent 只换左边)。宽度比例 + 左右两个坑位都持久化,刷新后分屏还原。
  const [splitPaneId, setSplitPaneId] = useState<string | null>(() => {
    try { return localStorage.getItem(SPLIT_PANE_KEY) } catch { return null }
  })
  const [splitRatio, setSplitRatio] = useState(() => {
    const v = Number(localStorage.getItem(SPLIT_RATIO_KEY))
    return Number.isFinite(v) && v >= 0.2 && v <= 0.8 ? v : 0.5
  })
  const [resizingSplit, setResizingSplit] = useState(false)
  // The LEFT slot: last active pane that isn't the pinned right pane. Focusing
  // (clicking) the right card makes it active without stealing the left slot.
  const [leftPaneId, setLeftPaneId] = useState<string>(() => {
    try { return localStorage.getItem(SPLIT_LEFT_KEY) || activePaneId } catch { return activePaneId }
  })
  useEffect(() => {
    if (activePaneId && activePaneId !== splitPaneId) setLeftPaneId(activePaneId)
  }, [activePaneId, splitPaneId])
  // Persist both slots while a split is pinned. There is deliberately NO
  // "close when the pane disappears" effect: right after a refresh the items
  // list starts without the async-loaded bound agents, and such an effect
  // would kill the restored split in that window. Rendering guards via
  // splitOn instead — the split simply lies dormant until its pane exists.
  useEffect(() => {
    if (!splitPaneId) return
    try { localStorage.setItem(SPLIT_PANE_KEY, splitPaneId) } catch {}
  }, [splitPaneId])
  useEffect(() => {
    if (!splitPaneId || !leftPaneId) return
    try { localStorage.setItem(SPLIT_LEFT_KEY, leftPaneId) } catch {}
  }, [leftPaneId, splitPaneId])
  const openSplit = useCallback((target: string) => { setSplitPaneId(target) }, [])
  const closeSplit = useCallback(() => {
    setSplitPaneId(null)
    try {
      localStorage.removeItem(SPLIT_PANE_KEY)
      localStorage.removeItem(SPLIT_LEFT_KEY)
    } catch {}
  }, [])
  const startSplitResize = useCallback((event: React.PointerEvent) => {
    event.preventDefault()
    event.stopPropagation()
    const el = containerRef.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    const clamp = (x: number) => Math.min(0.8, Math.max(0.2, (x - rect.left) / rect.width))
    setResizingSplit(true)
    const onMove = (ev: PointerEvent) => { setSplitRatio(clamp(ev.clientX)) }
    const onUp = (ev: PointerEvent) => {
      setResizingSplit(false)
      try { localStorage.setItem(SPLIT_RATIO_KEY, String(clamp(ev.clientX))) } catch {}
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }, [])
  // Keep the pinned pane in the terminal warm pool so closing the split
  // doesn't cold-drop it.
  useEffect(() => {
    if (!splitPaneId) return
    setWarmPanes((prev) => {
      const next = new Map(prev)
      next.set(splitPaneId, Date.now())
      return next
    })
  }, [splitPaneId])

  // 终端热池:最近激活过的 pane 在切走后保留终端挂载(WS + tmux attach 不断),
  // 90 秒内切回 = 瞬时(旧 iframe 全常驻的速度),久不碰或超过 3 个才真正断流。
  // 这是"全常驻(N 条流白烧)"和"切走即断(每次冷启动 ~0.5s)"之间的折中。
  const WARM_MAX = 3
  const WARM_TTL_MS = 90_000
  const [warmPanes, setWarmPanes] = useState<Map<string, number>>(() => new Map())
  useEffect(() => {
    if (!activePaneId) return
    setWarmPanes((prev) => {
      const next = new Map(prev)
      next.set(activePaneId, Date.now())
      while (next.size > WARM_MAX) {
        let oldestKey = ''
        let oldestTs = Infinity
        for (const [k, ts] of next) {
          if (k !== activePaneId && ts < oldestTs) { oldestTs = ts; oldestKey = k }
        }
        if (!oldestKey) break
        next.delete(oldestKey)
      }
      return next
    })
  }, [activePaneId])
  useEffect(() => {
    const id = window.setInterval(() => {
      setWarmPanes((prev) => {
        const now = Date.now()
        let changed = false
        const next = new Map(prev)
        for (const [k, ts] of prev) {
          if (k !== activePaneId && now - ts > WARM_TTL_MS) { next.delete(k); changed = true }
        }
        return changed ? next : prev
      })
    }, 15_000)
    return () => window.clearInterval(id)
  }, [activePaneId])

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

  // Effective slots. Falling back to activePaneId covers the first render
  // (leftPaneId not tracked yet) and a left pane that got deleted.
  const effLeft = leftPaneId && items.some((it) => it.paneId === leftPaneId) ? leftPaneId : activePaneId
  const splitOn = !!splitPaneId && splitPaneId !== effLeft && items.some((it) => it.paneId === splitPaneId)
  // Both shown panes are excluded from the picker; picking always (re)pins the
  // right half, from whichever card's menu.
  const splitCandidates = items
    .filter((o) => o.paneId !== effLeft && o.paneId !== splitPaneId)
    .map((o) => ({ paneId: o.paneId, title: o.title }))

  return (
    <div data-id="agent-stack" ref={containerRef} className="relative h-full overflow-hidden bg-[#09090b]">
      {items.map((item) => {
        const isLeft = item.paneId === effLeft
        const isRight = splitOn && item.paneId === splitPaneId
        const visible = splitOn ? isLeft || isRight : item.paneId === activePaneId
        const layoutStyle: React.CSSProperties = !visible
          ? { display: 'none' }
          : !splitOn
            ? { display: 'flex', left: 0, right: 0, top: 0, bottom: 0 }
            : isLeft
              ? { display: 'flex', left: 0, top: 0, bottom: 0, width: `${splitRatio * 100}%` }
              : { display: 'flex', left: `${splitRatio * 100}%`, right: 0, top: 0, bottom: 0, borderLeft: '1px solid rgba(255,255,255,0.08)' }
        return (
          <AgentStackCard
            key={item.paneId}
            item={item}
            active={visible}
            layoutStyle={layoutStyle}
            // 分屏时只有右半保留分屏按钮(换人/关闭);左半不出现,避免两个
            // 入口迷惑。非分屏时每张卡都有,用来开分屏。
            splitControl={{ isSplit: splitOn, show: !splitOn || isRight, candidates: splitCandidates, onPick: openSplit, onClose: closeSplit }}
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
            termWarm={warmPanes.has(item.paneId)}
          />
        )
      })}
      {splitOn ? (
        <div
          data-id="agent-stack-split-divider"
          onPointerDown={startSplitResize}
          className="group absolute bottom-0 top-0 z-20 w-2 -translate-x-1/2 cursor-col-resize"
          style={{ left: `${splitRatio * 100}%` }}
          aria-label="拖拽调整分屏宽度"
        >
          <div className="mx-auto h-full w-[2px] bg-white/10 transition-colors group-hover:bg-blue-500/60" />
        </div>
      ) : null}
      {/* Drag mask over BOTH halves while resizing — without it the pointer
          dies the moment it crosses into a terminal iframe/webview. Same trick
          as the history-height drag inside the card. */}
      {splitOn && resizingSplit ? (
        <div data-id="agent-stack-split-resize-mask" className="absolute inset-0 z-40 cursor-col-resize" />
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
  layoutStyle,
  splitControl,
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
  termWarm = false,
}: {
  item: AgentCanvasItem;
  active: boolean;
  // Position/size within the stack container (full / left half / right half /
  // hidden) — owned by AgentStack so the split layout lives in one place.
  layoutStyle: React.CSSProperties;
  // 分屏控制:候选列表 + 钉住/关闭回调。show=false 时不渲染按钮(分屏中的左半)。
  splitControl: { isSplit: boolean; show: boolean; candidates: { paneId: string; title: string }[]; onPick: (paneId: string) => void; onClose: () => void };
  // 热池:切走后的保活窗口内保持终端挂载(WS 不断),切回瞬时。
  termWarm?: boolean;
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
      // No role="button"/tabIndex/keyboard activation: only visible cards
      // matter (display:none switching), so key-activating was a no-op — and
      // its Space/Enter preventDefault swallowed keystrokes from inputs inside
      // the card (e.g. the dispatcher prompt).
      // Position + visibility come from layoutStyle (full width, or one half
      // of the split) — owned by AgentStack.
      className={`absolute overflow-hidden text-left transition-colors ${active ? 'flex-col bg-[#0c0d10]' : ''}`}
      style={layoutStyle}
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
          {showHeaderButtons && splitControl.show && (splitControl.candidates.length > 0 || splitControl.isSplit) ? (
            <SplitMenuButton
              paneId={item.paneId}
              candidates={splitControl.candidates}
              isSplit={splitControl.isSplit}
              onPick={splitControl.onPick}
              onClose={splitControl.onClose}
            />
          ) : null}
          {showHeaderButtons ? (
            // The eight icon buttons that used to sit here inline now live in a
            // single ⋯ menu. Every data-id is preserved, so anything that
            // addressed a button by id still finds it.
            <CardMoreMenu
              paneId={item.paneId}
              items={[
                ...(onOpenPaneTodo ? [{
                  id: 'agent-stack-card-todo',
                  label: t('tabTodo', { ns: 'workspace' }),
                  icon: <ListTodo className="h-4 w-4" />,
                  onClick: handleOpenTodo,
                  badge: todoCount,
                }] : []),
                {
                  id: 'agent-stack-card-files',
                  label: t('tabFiles', { ns: 'workspace' }),
                  icon: <Folder className="h-4 w-4" />,
                  onClick: handleOpenFiles,
                },
                {
                  id: 'agent-stack-card-session',
                  label: t('tabSession', { ns: 'workspace' }),
                  icon: <LineChart className="h-4 w-4" />,
                  onClick: handleOpenSession,
                },
                ...(onOpenPaneContent ? [{
                  id: 'agent-stack-card-request',
                  label: t('tabRequest', { ns: 'workspace' }),
                  icon: <Braces className="h-4 w-4" />,
                  onClick: handleOpenRequest,
                }, {
                  id: 'agent-stack-card-knowledge',
                  label: t('tabKnowledge', { ns: 'workspace' }),
                  icon: <BookOpen className="h-4 w-4" />,
                  onClick: handleOpenKnowledge,
                }] : []),
                ...(onOpenPaneMemory ? [{
                  id: 'agent-stack-card-memory',
                  label: t('tabMemory', { ns: 'workspace' }),
                  icon: <Brain className="h-4 w-4" />,
                  onClick: handleOpenMemory,
                }] : []),
                ...(onOpenPaneContent ? [{
                  id: 'agent-stack-card-audit',
                  label: t('tabAudit', { ns: 'audit', defaultValue: '审计' }),
                  icon: <ShieldCheck className="h-4 w-4" />,
                  onClick: handleOpenAudit,
                  badge: auditAlertCount,
                }] : []),
                {
                  id: 'agent-stack-card-settings',
                  label: t('tabSettings', { ns: 'workspace' }),
                  icon: <Settings className="h-4 w-4" />,
                  onClick: handleOpenSettings,
                  active: settingsShortcutActive,
                },
              ]}
            />
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
            className="hidden absolute left-1/2 top-2 z-20 -translate-x-1/2"
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
        ) : !item.isApiOnly && item.ttydSrc && (active || termWarm) ? (
          // Visible-only streaming: only the ACTIVE card mounts its terminal.
          // Hidden cards used to keep N live ttyd WebSockets + tmux attaches
          // running behind display:none — pure cost plus the breeding ground
          // for stale mouse/size state. Switching back re-attaches and the
          // server backfills capture-pane history, so nothing is lost.
          // History toggling keeps `active` true, so the terminal stays
          // mounted underneath the history overlay (WS not torn down).
          //
          // TerminalView = iframe-free xterm speaking the webtty WS directly.
          // codex agents / the `cicy.term.iframe` escape hatch keep the legacy
          // gotty-page iframe (see shouldUseTerminalView).
          <div
            data-id={`agent-stack-card-terminal-${item.paneId}`}
            className="h-full w-full"
          >
            {shouldUseTerminalView(item.agentType) ? (
              <TerminalView key={`${item.paneId}-${termReloadNonce}`} ttydSrc={item.ttydSrc} />
            ) : (
              <WebFrame key={`${item.paneId}-${termReloadNonce}`} src={item.ttydSrc} className="h-full w-full border-0 bg-black" title={`stack-${item.paneId}`} />
            )}
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
      {(headerControls || !isCicyLiteAgent(item.agentType)) ? (
        <div data-id={`agent-stack-card-header-controls-${item.paneId}`} className="flex h-10 shrink-0 items-center gap-3 border-t border-white/[0.04] bg-black/[0.18] px-3">
          {/* attach sits immediately left of the model picker (headerControls'
              first item) as one group; the spacer inside headerControls pushes
              the remaining controls to the right. */}
          {!isCicyLiteAgent(item.agentType) ? <AttachSendButton paneId={item.paneId} /> : null}
          {headerControls}
        </div>
      ) : null}
      {!item.isApiOnly && item.ttydSrc ? (
        <ShellPanel agentId={item.paneId} ttydSrc={item.ttydSrc} active={active} />
      ) : null}
    </div>
  )
}
