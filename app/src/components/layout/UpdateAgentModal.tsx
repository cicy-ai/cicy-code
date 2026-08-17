// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// UpdateAgentModal — 团队面板 worker 菜单「更新」的执行弹窗。
// 复用 AgentInstallOverlay 同一条后端链路:POST /api/agents/install(SSE 流式
// npm 日志),完成后一键重启(/api/tmux/panes/<id>/restart,真正的 pane 重生,
// 不是 send-keys)。gotty 页 cpModalUpdateAgent 的 in-app 移植:失败可换
// 源重试(官方 registry ↔ npmmirror)。

import { useCallback, useEffect, useRef, useState } from 'react'
import { Loader2, RotateCcw, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import config from '../../config'
import { TokenManager } from '../../services/tokenManager'
import apiService from '../../services/api'

type Phase = 'running' | 'done' | 'error'

const MIRROR = 'https://registry.npmmirror.com'

export function UpdateAgentModal({ paneId, title, onClose }: {
  paneId: string
  title: string
  onClose: () => void
}) {
  const { t } = useTranslation('teamPanel')
  const [phase, setPhase] = useState<Phase>('running')
  const [percent, setPercent] = useState(0)
  const [log, setLog] = useState<string[]>([])
  const [error, setError] = useState('')
  const [restarting, setRestarting] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const lastRegistryRef = useRef('')

  const run = useCallback(async (registry: string) => {
    lastRegistryRef.current = registry
    setPhase('running')
    setPercent(0)
    setError('')
    setLog([])
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
      if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`)
      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        let idx: number
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const frame = buf.slice(0, idx)
          buf = buf.slice(idx + 2)
          const line = frame.split('\n').find((l) => l.startsWith('data:'))
          if (!line) continue
          let ev: any
          try { ev = JSON.parse(line.slice(5).trim()) } catch { continue }
          if (ev?.type === 'phase' && typeof ev.percent === 'number') setPercent(ev.percent)
          if (ev?.type === 'log') {
            setLog((prev) => {
              const next = prev.concat(String(ev.line ?? ev.code ?? ''))
              return next.length > 300 ? next.slice(next.length - 300) : next
            })
          }
          if (ev?.type === 'done') { setPercent(100); setPhase('done') }
          if (ev?.type === 'error') { setError(String(ev.message || ev.error || 'update failed')); setPhase('error') }
        }
      }
      // 流正常收尾但没给 done/error 事件 → 按完成处理(和 overlay 同语义)。
      setPhase((p) => (p === 'running' ? 'done' : p))
    } catch (e: any) {
      if (ctrl.signal.aborted) return
      setError(e?.message || 'update failed')
      setPhase('error')
    }
  }, [paneId])

  useEffect(() => {
    void run('')
    return () => abortRef.current?.abort()
  }, [run])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && phase !== 'running') onClose()
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [phase, onClose])

  const restart = useCallback(async () => {
    setRestarting(true)
    try { await apiService.restartPane(paneId) } catch { /* pane 可能正在重生 */ }
    onClose()
  }, [paneId, onClose])

  return (
    <div
      data-id={`update-agent-modal-overlay-${paneId}`}
      className="fixed inset-0 z-[300] flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onMouseDown={(e) => { if (e.target === e.currentTarget && phase !== 'running') onClose() }}
    >
      <div
        data-id={`update-agent-modal-${paneId}`}
        className="w-[520px] max-w-[92vw] overflow-hidden rounded-2xl border border-white/[0.08] bg-[#111113] shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 pt-4">
          <div data-id="update-agent-modal-title" className="text-sm font-semibold text-zinc-100">
            {t('updateTitle', { defaultValue: '更新 {{title}}', title })}
          </div>
          <button
            type="button"
            data-id="update-agent-modal-close"
            disabled={phase === 'running'}
            onClick={onClose}
            className="rounded-md p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200 disabled:cursor-not-allowed disabled:opacity-40"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="px-5 pb-2 pt-1 text-xs text-zinc-500">
          {phase === 'running' && (
            <span className="inline-flex items-center gap-1.5">
              <Loader2 className="h-3 w-3 animate-spin" />
              {t('updateRunning', { defaultValue: '正在更新 CLI…(不打断正在运行的会话)' })}
            </span>
          )}
          {phase === 'done' && <span className="text-emerald-400">{t('updateDone', { defaultValue: '更新完成。重启后新版本生效。' })}</span>}
          {phase === 'error' && <span className="text-red-400">{error}</span>}
        </div>
        <div className="mx-5 h-1 overflow-hidden rounded-full bg-white/[0.06]">
          <div
            data-id="update-agent-modal-bar"
            className={`h-full rounded-full transition-[width] duration-300 ${phase === 'error' ? 'bg-red-500' : 'bg-blue-500'}`}
            style={{ width: `${phase === 'done' ? 100 : percent}%` }}
          />
        </div>
        <div
          data-id="update-agent-modal-log"
          className="mx-5 mb-3 mt-3 h-48 overflow-y-auto rounded-lg bg-black/50 p-2.5 font-mono text-[11px] leading-relaxed text-zinc-400"
        >
          {log.map((l, i) => <div key={i}>{l}</div>)}
        </div>
        <div className="flex items-center justify-end gap-2 border-t border-white/[0.06] px-5 py-3">
          {phase === 'error' && (
            <button
              type="button"
              data-id="update-agent-modal-retry"
              onClick={() => void run(lastRegistryRef.current === MIRROR ? '' : MIRROR)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.1] px-3 py-1.5 text-xs text-zinc-200 transition-colors hover:bg-white/[0.06]"
            >
              <RotateCcw className="h-3 w-3" />
              {t('updateRetrySwitchSource', { defaultValue: '换源重试' })}
            </button>
          )}
          {phase === 'done' && (
            <button
              type="button"
              data-id="update-agent-modal-restart"
              disabled={restarting}
              onClick={() => void restart()}
              className="inline-flex items-center gap-1.5 rounded-lg bg-blue-600 px-3.5 py-1.5 text-xs font-medium text-white transition-colors hover:bg-blue-500 disabled:opacity-60"
            >
              {restarting ? <Loader2 className="h-3 w-3 animate-spin" /> : <RotateCcw className="h-3 w-3" />}
              {t('updateRestartNow', { defaultValue: '重启 Agent 生效' })}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

export default UpdateAgentModal
