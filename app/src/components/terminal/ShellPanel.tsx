import { useEffect, useRef, useState } from 'react'
import { Plus, Terminal, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import apiService from '../../services/api'
import { useApp } from '../../contexts/AppContext'
import { WebFrame } from '../WebFrame'

interface Win { index: string; name: string; active: boolean }

// ShellPanel sits below an agent card's header-controls and shows a second
// terminal next to the agent's main view. It attaches to the agent's grouped
// tmux session (w-<n>-sh), which shares the agent's window list but keeps an
// independent current-window — so switching/adding shell windows here never
// moves the agent's main.0 view above. All window ops therefore target the
// grouped session name.
//
// Visibility is driven by a single global toggle (AppContext.shellPanelOpen,
// flipped by the icon button left of the system-resource indicator). Only the
// active card's panel goes live, so we don't spin up a grouped session + ttyd
// for every agent at once.
export function ShellPanel({ agentId, ttydSrc, active }: { agentId: string; ttydSrc: string; active?: boolean }) {
  const { t } = useTranslation('ui')
  const { shellPanelOpen } = useApp()
  const open = !!shellPanelOpen && !!active
  const grouped = `${agentId}-sh`
  // Same token/lang as the agent terminal, just a different proxy route. The
  // bottom=1 flag tells the gotty page to hide its own floating tab bar
  // (#cp-win-float) — this panel supplies its own tabs.
  const shellSrc = `${ttydSrc.replace('/ttyd/', '/ttyd-shell/')}&bottom=1`
  const [mounted, setMounted] = useState(false) // lazily mount WebFrame on first open, keep alive after
  const [wins, setWins] = useState<Win[]>([])
  const [height, setHeight] = useState(256) // terminal height (px), drag the bottom-left grip
  const [resizing, setResizing] = useState(false)
  const heightRef = useRef(256)
  useEffect(() => { heightRef.current = height }, [height])
  const correctedRef = useRef(false) // one-shot: steer grouped off the agent's main window on open

  // window 0 / "main" hosts the agent (shown in the card above) — the shell
  // panel never lists or attaches to it.
  const isAgentWin = (wn: Win) => wn.index === '0' || wn.name === 'main'

  const load = () => {
    apiService.listWindows(grouped).then(({ data }) => {
      const ws: Win[] = data.windows || []
      setWins(ws)
      // If the grouped session is parked on main, it would mirror the agent.
      // Steer it to a real shell window (creating one if none exists). Once.
      if (!correctedRef.current) {
        const act = ws.find(w => w.active)
        const shells = ws.filter(w => !isAgentWin(w))
        if (!act || isAgentWin(act)) {
          correctedRef.current = true
          if (shells.length > 0) apiService.selectWindow(grouped, shells[0].index).then(() => setTimeout(load, 200)).catch(() => {})
          else apiService.createWindow(grouped).then(() => setTimeout(load, 300)).catch(() => {})
        }
      }
    }).catch(() => {})
  }
  useEffect(() => {
    if (open) setMounted(true)
  }, [open])
  useEffect(() => {
    if (!open) { correctedRef.current = false; return }
    load()
    const id = setInterval(load, 3000)
    return () => clearInterval(id)
  }, [open, grouped])

  const select = async (idx: string) => { try { await apiService.selectWindow(grouped, idx) } catch { /* ignore */ } setTimeout(load, 300) }
  const create = async () => { try { await apiService.createWindow(grouped) } catch { /* ignore */ } setTimeout(load, 300) }
  const del = async (e: React.MouseEvent, idx: string) => {
    e.stopPropagation()
    try { await apiService.deleteWindow(grouped, idx) } catch { /* ignore */ }
    setTimeout(load, 300)
  }
  const tabs = wins.filter(w => !isAgentWin(w))

  // Drag the bottom-left grip to resize the terminal height. The terminal's
  // bottom edge is pinned to the card bottom, so dragging up grows it (eating
  // into the agent view above), dragging down shrinks it. A full-screen overlay
  // during the drag stops the ttyd iframe from swallowing pointer events.
  const startResize = (e: React.PointerEvent) => {
    e.preventDefault()
    e.stopPropagation()
    const startY = e.clientY
    const startH = heightRef.current
    setResizing(true)
    const onMove = (ev: PointerEvent) => {
      const max = Math.max(160, window.innerHeight - 160)
      setHeight(Math.max(120, Math.min(startH - (ev.clientY - startY), max)))
    }
    const onUp = () => {
      setResizing(false)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }

  // Only the active card hosts a live shell dock; collapse to nothing when the
  // global toggle is off (keep the WebFrame mounted-but-hidden to preserve its
  // ttyd WebSocket across quick toggles).
  if (!active) return null

  return (
    <div
      data-id={`agent-stack-shell-dock-${agentId}`}
      className="shrink-0 border-t border-white/[0.06] bg-black/[0.28]"
      style={open ? undefined : { display: 'none' }}
    >
      <div data-id={`agent-stack-shell-bar-${agentId}`} className="flex h-9 items-center gap-1 px-2">
        <span data-id={`agent-stack-shell-label-${agentId}`} className="flex shrink-0 items-center gap-1.5 px-1 text-xs font-medium text-zinc-500">
          <Terminal className="h-3.5 w-3.5" />
          Shell
        </span>
        <div data-id={`agent-stack-shell-tabs-${agentId}`} className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto">
          {tabs.map(wn => (
            <button
              type="button"
              key={wn.index}
              onClick={() => select(wn.index)}
              className={`group/tab flex shrink-0 items-center gap-1 rounded-t px-2 py-1 font-mono text-xs transition-colors ${wn.active ? 'bg-white/[0.10] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.05] hover:text-zinc-300'}`}
            >
              <span className="text-zinc-600">{wn.index}</span>{wn.name}
              <span
                role="button"
                tabIndex={-1}
                onClick={(e) => del(e, wn.index)}
                className="ml-0.5 hidden rounded p-0.5 hover:bg-white/[0.12] hover:text-red-400 group-hover/tab:inline-flex"
              >
                <X className="h-3 w-3" />
              </span>
            </button>
          ))}
          <button
            type="button"
            onClick={create}
            title={t('shellPanelNew', { defaultValue: 'New window' })}
            className="shrink-0 rounded p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200"
          >
            <Plus className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>
      {mounted ? (
        <div data-id={`agent-stack-shell-terminal-${agentId}`} className="relative w-full" style={{ height }}>
          <WebFrame src={shellSrc} className="h-full w-full border-0 bg-black" title={`shell-${agentId}`} />
          {/* resize grip — bottom-left corner; drag up to enlarge */}
          <div
            data-id={`agent-stack-shell-resize-${agentId}`}
            onPointerDown={startResize}
            title={t('shellPanelResize', { defaultValue: 'Drag to resize' })}
            className="absolute bottom-0 left-0 z-10 flex h-4 w-4 cursor-sw-resize items-end justify-start p-1"
            style={{ touchAction: 'none' }}
          >
            <svg width="8" height="8" viewBox="0 0 8 8" className="text-zinc-500"><path d="M0 0v8h8" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" /></svg>
          </div>
        </div>
      ) : null}
      {resizing ? <div data-id={`agent-stack-shell-resize-overlay-${agentId}`} className="fixed inset-0 z-[200] cursor-sw-resize" style={{ touchAction: 'none' }} /> : null}
    </div>
  )
}
