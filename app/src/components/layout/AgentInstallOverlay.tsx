import { memo, useCallback, useEffect, useRef, useState } from 'react'
import config from '../../config'
import apiService from '../../services/api'
import { TokenManager } from '../../services/tokenManager'
import { isCicyLiteAgent } from '../../lib/agentType'

// AgentInstallOverlay sits ON TOP of one un-installed coding-CLI agent's frame.
// Auto-install in the terminal was removed; instead this overlay prompts the user
// to install the CLI (claude/codex/…), drives the backend-managed installer over
// SSE (phased progress bar + live log + CN-mirror retry), and offers to restart
// the agent when done. It is scoped to a single agent (absolute over its body),
// renders nothing once the CLI is installed, and never blocks other agents/pages.

type Phase = 'idle' | 'detect' | 'install' | 'verify' | 'done' | 'error'

interface Props {
  paneId: string
  agentType?: string
  active: boolean
  // Ask the card to re-mount the ttyd terminal iframe — needed after a restart,
  // since the old WebSocket attached to the now-killed pane and won't reconnect.
  onReloadTerminal?: () => void
}

interface InstallStatus {
  installable: boolean
  installed: boolean
  label?: string
  cli?: string
}

function AgentInstallOverlayInner({ paneId, agentType, onReloadTerminal }: Props) {
  const [status, setStatus] = useState<InstallStatus | null>(null)
  const [phase, setPhase] = useState<Phase>('idle')
  const [phaseText, setPhaseText] = useState('')
  const [percent, setPercent] = useState(0)
  const [log, setLog] = useState<string[]>([])
  const [error, setError] = useState('')
  const [restarting, setRestarting] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const lastRegistryRef = useRef<string>('') // which source the last attempt used → retry switches
  const logBoxRef = useRef<HTMLDivElement | null>(null)

  // cicy lite agents have no CLI to install — never engage.
  const skip = isCicyLiteAgent(agentType)

  const refreshStatus = useCallback(async () => {
    if (skip) return
    try {
      const { data } = await apiService.getInstallStatus(paneId)
      if (data?.success) {
        setStatus({ installable: !!data.installable, installed: !!data.installed, label: data.label, cli: data.cli })
      }
    } catch {
      /* leave status null → overlay stays hidden, never blocks */
    }
  }, [paneId, skip])

  useEffect(() => {
    refreshStatus()
  }, [refreshStatus])

  // Tail the log box to the newest line.
  useEffect(() => {
    const el = logBoxRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [log])

  // Tear down any in-flight install stream on unmount.
  useEffect(() => () => abortRef.current?.abort(), [])

  const runInstall = useCallback(async (registry: string) => {
    setPhase('detect')
    setPhaseText('准备安装…')
    setPercent(0)
    setLog([])
    setError('')
    const ctrl = new AbortController()
    abortRef.current = ctrl
    const token = TokenManager.getToken()
    try {
      const resp = await fetch(`${config.apiBase}/api/agents/install`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ agent_id: paneId, registry }),
        signal: ctrl.signal,
      })
      if (!resp.ok || !resp.body) {
        throw new Error(`HTTP ${resp.status}`)
      }
      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        // SSE frames are separated by a blank line.
        let idx: number
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const frame = buf.slice(0, idx)
          buf = buf.slice(idx + 2)
          const line = frame.split('\n').find((l) => l.startsWith('data:'))
          if (!line) continue
          let ev: any
          try { ev = JSON.parse(line.slice(5).trim()) } catch { continue }
          handleEvent(ev)
        }
      }
    } catch (e: any) {
      if (ctrl.signal.aborted) return
      setPhase('error')
      setError(e?.message || '安装失败')
    }
  }, [paneId])

  const handleEvent = useCallback((ev: any) => {
    switch (ev?.type) {
      case 'phase':
        setPhase((ev.phase as Phase) || 'install')
        setPhaseText(ev.text || '')
        if (typeof ev.percent === 'number') setPercent(ev.percent)
        break
      case 'log':
        setLog((prev) => {
          const next = prev.concat(ev.line ?? '')
          return next.length > 300 ? next.slice(next.length - 300) : next
        })
        break
      case 'done':
        if (ev.registry_used) lastRegistryRef.current = ev.registry_used
        // Do NOT refresh status here — that would flip installed:true and hide the
        // whole overlay before the user can hit「重启 agent」, leaving a blank
        // terminal (the pane hasn't relaunched the CLI yet). Stay on the done
        // screen with the restart button; dismiss only after the user restarts.
        setPhase('done')
        setPercent(100)
        setPhaseText('安装完成')
        break
      case 'error':
        if (ev.registry_used) lastRegistryRef.current = ev.registry_used
        setPhase('error')
        setError(ev.error || '安装失败')
        break
    }
  }, [refreshStatus])

  const onInstall = useCallback(() => runInstall(''), [runInstall])
  // Retry switches the source: if the last attempt used the mirror, try official, and vice-versa.
  const onRetry = useCallback(() => {
    const next = lastRegistryRef.current === 'mirror' ? 'official' : lastRegistryRef.current === 'official' ? 'mirror' : ''
    runInstall(next)
  }, [runInstall])

  const onRestart = useCallback(async () => {
    setRestarting(true)
    try {
      // Hard restart: respawn the tmux pane (kills the old process tree, fresh
      // boot.sh run) so the terminal comes up clean — not the soft re-source that
      // left the stale "未安装" text on screen. The blank was caused by the overlay
      // RE-MOUNTING the iframe (removed); a plain restart lets the gotty client
      // auto-reconnect to the respawned pane smoothly, no blank.
      await apiService.restartPane(paneId)
      await new Promise((r) => setTimeout(r, 1200))
    } catch {
      /* fall through — drop the overlay anyway so the user isn't stuck */
    }
    setDismissed(true)
  }, [paneId, onReloadTerminal])

  // Visibility: hidden for cicy agents, after the user restarts, or when the CLI
  // was already installed at load (idle). While installing/awaiting-restart
  // (phase != idle) the overlay stays up even though the CLI is now present —
  // otherwise the「重启 agent」step would vanish and leave a blank terminal.
  if (skip || dismissed || !status || !status.installable) return null
  if (status.installed && phase === 'idle') return null

  const label = status.label || status.cli || 'CLI'
  const busy = phase === 'detect' || phase === 'install' || phase === 'verify'

  return (
    <div
      data-id={`agent-install-overlay-${paneId}`}
      className="absolute inset-0 z-[11] flex flex-col items-center justify-center bg-black/80 backdrop-blur-sm p-6"
    >
      <div
        data-id={`agent-install-overlay-card-${paneId}`}
        className="w-full max-w-md rounded-2xl border border-white/10 bg-zinc-900/90 p-6 shadow-2xl"
      >
        <div data-id={`agent-install-overlay-title-${paneId}`} className="text-base font-medium text-zinc-100">
          {phase === 'done' ? `${label} 安装完成` : `${label} 还没安装`}
        </div>
        <div data-id={`agent-install-overlay-subtitle-${paneId}`} className="mt-1 text-sm text-zinc-400">
          {phase === 'idle' && `这个 agent 需要 ${label}。点击下方按钮安装,装好后重启即可使用。`}
          {busy && (phaseText || '安装中…')}
          {phase === 'done' && '点击「重启 agent」让它使用刚装好的 CLI。'}
          {phase === 'error' && (error || '安装失败,可切换镜像源重试。')}
        </div>

        {(busy || phase === 'done') && (
          <div data-id={`agent-install-overlay-progress-${paneId}`} className="mt-4 h-1.5 w-full overflow-hidden rounded-full bg-zinc-800">
            <div
              data-id={`agent-install-overlay-progress-bar-${paneId}`}
              className="h-full rounded-full bg-blue-500 transition-all duration-500"
              style={{ width: `${percent}%` }}
            />
          </div>
        )}

        {log.length > 0 && (
          <div
            ref={logBoxRef}
            data-id={`agent-install-overlay-log-${paneId}`}
            className="mt-3 h-28 overflow-auto rounded-lg bg-black/60 p-2 font-mono text-[11px] leading-relaxed text-zinc-400"
          >
            {log.map((l, i) => (
              <div key={i} className="whitespace-pre-wrap break-all">{l}</div>
            ))}
          </div>
        )}

        <div data-id={`agent-install-overlay-actions-${paneId}`} className="mt-5 flex gap-2">
          {phase === 'idle' && (
            <button
              data-id={`agent-install-overlay-install-btn-${paneId}`}
              onClick={onInstall}
              className="flex-1 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500"
            >
              安装 {label}
            </button>
          )}
          {busy && (
            <button
              data-id={`agent-install-overlay-installing-btn-${paneId}`}
              disabled
              className="flex-1 cursor-default rounded-lg bg-zinc-700 px-4 py-2 text-sm font-medium text-zinc-300"
            >
              安装中…
            </button>
          )}
          {phase === 'error' && (
            <button
              data-id={`agent-install-overlay-retry-btn-${paneId}`}
              onClick={onRetry}
              className="flex-1 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-blue-500"
            >
              切换镜像源重试
            </button>
          )}
          {phase === 'done' && (
            <button
              data-id={`agent-install-overlay-restart-btn-${paneId}`}
              onClick={onRestart}
              disabled={restarting}
              className="flex-1 rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-emerald-500 disabled:opacity-60"
            >
              {restarting ? '重启中…' : '重启 agent'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

export const AgentInstallOverlay = memo(AgentInstallOverlayInner)
export default AgentInstallOverlay
