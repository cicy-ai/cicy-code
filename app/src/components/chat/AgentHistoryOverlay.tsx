import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next';
import { Loader2, X } from 'lucide-react'
import i18n from '../../i18n'
import apiService from '../../services/api'
import { appendHistoryItems, getHistoryMeta, getRecentHistoryItems, replaceHistoryBase, type HistorySyncItem } from '../../lib/historyCache'

type ReplySnapshot = {
  turn_id?: string
  status?: string
  answer?: string
  thinking?: string
  updated_at?: string
  model?: string
  tool_calls?: Array<{ tool_name?: string; arguments?: string }>
}

function formatTime(value?: string) {
  if (!value) return ''
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) return value
  return new Date(parsed).toLocaleString(undefined, { hour12: false })
}

function statusLabel(status?: string) {
  switch (String(status || '').toLowerCase()) {
    case 'thinking': return i18n.t('statusThinking', { ns: 'agentChat' })
    case 'working':
    case 'tool_call':
    case 'streaming': return i18n.t('statusProcessing', { ns: 'agentChat' })
    case 'failed':
    case 'error': return i18n.t('statusError', { ns: 'agentChat' })
    case 'idle':
    case 'completed':
    case 'done': return i18n.t('statusDone', { ns: 'agentChat' })
    default: return status || i18n.t('statusDone', { ns: 'agentChat' })
  }
}

function groupTurns(items: HistorySyncItem[]) {
  const groups: Array<{ id: string; q?: HistorySyncItem; extras: HistorySyncItem[]; a?: HistorySyncItem }> = []
  let current: { id: string; q?: HistorySyncItem; extras: HistorySyncItem[]; a?: HistorySyncItem } | null = null
  for (const item of items) {
    if (item.role === 'user') {
      if (current) groups.push(current)
      current = { id: item.id, q: item, extras: [], a: undefined }
      continue
    }
    if (!current) {
      current = { id: item.id, extras: [] }
    }
    if (item.role === 'assistant') {
      current.a = item
    } else {
      current.extras.push(item)
    }
  }
  if (current) groups.push(current)
  return groups
}

export default function AgentHistoryOverlay({ paneId, open, onClose }: { paneId: string; open: boolean; onClose: () => void }) {
  const { t } = useTranslation('agentChat')
  const [items, setItems] = useState<HistorySyncItem[]>([])
  const [loading, setLoading] = useState(false)
  const [syncing, setSyncing] = useState(false)
  const [error, setError] = useState('')
  const [reply, setReply] = useState<ReplySnapshot | null>(null)
  const [liveReply, setLiveReply] = useState('')
  const [liveStatus, setLiveStatus] = useState('idle')
  const tokenRef = useRef('')

  useEffect(() => {
    tokenRef.current = TokenManager.getToken() || ''
  }, [])

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true)
    setError('')
    ;(async () => {
      const cached = await getRecentHistoryItems(paneId, 80)
      if (!cancelled && cached.length) setItems(cached)
      try {
        const meta = await getHistoryMeta(paneId)
        const { data } = await apiService.getAgentHistorySync(paneId, { cursor: meta?.cursor || '', limit: 80 })
        if (cancelled) return
        const nextItems = Array.isArray(data?.items) ? data.items : []
        const conversationId = String(data?.conversation_id || '')
        const cursor = String(data?.cursor || '')
        const shouldReplace = data?.base_complete === false || !meta || meta.conversationId !== conversationId
        if (shouldReplace) {
          await replaceHistoryBase(paneId, conversationId, nextItems, cursor)
        } else if (nextItems.length) {
          await appendHistoryItems(paneId, conversationId, nextItems, cursor)
        }
        const refreshed = await getRecentHistoryItems(paneId, 120)
        if (!cancelled) {
          setItems(refreshed)
          setReply(data?.reply || null)
          setLiveReply(String(data?.reply?.answer || ''))
          setLiveStatus(String(data?.reply?.status || 'idle'))
        }
      } catch (err: any) {
        if (!cancelled) setError(err?.message || t('overlayHistoryReadFailed'))
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => { cancelled = true }
  }, [open, paneId])

  useEffect(() => {
    if (!open) return
    if (liveStatus) setLiveStatus(liveStatus)
  }, [liveStatus, open])

  useEffect(() => {
    if (!open) return
    if (liveReply !== undefined) setLiveReply(liveReply)
  }, [liveReply, open])

  const turns = useMemo(() => groupTurns(items), [items])
  if (!open) return null

  return (
    <div data-id="agent-history-overlay" className="absolute inset-0 z-20 flex flex-col bg-[#09090bf2] backdrop-blur-sm">
      <div className="flex items-center justify-between border-b border-white/[0.06] px-4 py-3">
        <div>
          <div className="text-[11px] uppercase tracking-[0.18em] text-zinc-500">History</div>
          <div className="text-sm text-zinc-200">{statusLabel(liveStatus)}{syncing ? t('overlaySyncing') : ''}</div>
        </div>
        <button type="button" onClick={onClose} className="rounded p-1 text-zinc-500 transition-colors hover:bg-white/[0.06] hover:text-zinc-200" title={t('overlayCloseTitle')}>
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        {loading ? <div className="flex items-center gap-2 text-sm text-zinc-500"><Loader2 className="h-4 w-4 animate-spin" />{t('overlayLoading')}</div> : null}
        {error ? <div className="mb-3 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-sm text-red-300">{error}</div> : null}
        <div className="space-y-4">
          {turns.map((turn) => (
            <div key={turn.id} className="space-y-2">
              {turn.q ? (
                <div className="ml-auto max-w-[92%] rounded-2xl rounded-br-sm bg-white/[0.08] px-3.5 py-2 text-sm leading-6 text-white/90 whitespace-pre-wrap break-words">{turn.q.text}</div>
              ) : null}
              {turn.extras.map((extra) => (
                <div key={extra.id} className="rounded-xl border border-white/[0.06] bg-white/[0.03] px-3 py-2 text-xs text-zinc-400 whitespace-pre-wrap break-words">
                  <div className="mb-1 uppercase tracking-[0.14em] text-zinc-500">{extra.role}</div>
                  {extra.role === 'tool' ? (extra.content_json?.name || extra.text) : extra.text}
                </div>
              ))}
              {turn.a ? (
                <div className="rounded-2xl rounded-bl-sm border border-white/[0.06] bg-[#111215] px-3.5 py-2.5 text-sm leading-6 text-zinc-200 whitespace-pre-wrap break-words">{turn.a.text}</div>
              ) : null}
            </div>
          ))}
          {liveReply ? (
            <div className="rounded-2xl rounded-bl-sm border border-blue-500/20 bg-blue-500/10 px-3.5 py-2.5 text-sm leading-6 text-blue-100 whitespace-pre-wrap break-words">
              <div className="mb-1 text-[11px] uppercase tracking-[0.14em] text-blue-300/80">{t('overlayLiveReply')}</div>
              {liveReply}
            </div>
          ) : null}
        </div>
      </div>
      <div className="border-t border-white/[0.06] px-4 py-2 text-[11px] text-zinc-500">
        {reply?.updated_at ? t('overlayLastUpdated', { time: formatTime(reply.updated_at) }) : t('overlayWaitingData')}
      </div>
    </div>
  )
}
