import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import config from '../../config'
import { WebFrame } from '../WebFrame'
import { Spinner } from '../ui/Spinner'

type HistoryTurn = {
  q: string
  a?: string
  steps?: Array<{ type: 'text'; text: string } | { type: 'thinking'; text: string } | { type: 'tool'; tools: any[] }>
  status?: string
  ts?: number
  start_ts?: number
  credit?: number
  model?: string
}

type ChatHistoryViewProps = {
  paneId: string
  token: string
  liveStatus?: string
  liveText?: string
  historyVersion?: number
  suggestionText?: string
  suggestionPending?: boolean
  suggestionSending?: boolean
  onExecuteSuggestion?: (text: string) => void | Promise<void>
}

const TOOL_ICONS: Record<string, string> = {
  fs_read: '📄', fs_write: '✏️', execute_bash: '⚡', grep: '🔍', glob: '📂',
  code: '🧠', web_search: '🌐', web_fetch: '🌐', use_aws: '☁️', use_subagent: '🤖',
}
const TOOL_LABELS: Record<string, string> = {
  fs_read: i18n.t('toolReadFile', { ns: 'agentChat' }), fs_write: i18n.t('toolWriteFile', { ns: 'agentChat' }), execute_bash: 'Command', grep: 'Search', glob: 'Glob', code: i18n.t('toolCodeAnalysis', { ns: 'agentChat' }), web_search: i18n.t('toolWebSearch', { ns: 'agentChat' }), web_fetch: i18n.t('toolWebFetch', { ns: 'agentChat' }), use_aws: 'AWS', use_subagent: 'Subagent',
}

const CollapsibleQ = ({ text }: { text: string }) => {
  const { t } = useTranslation('agentChat')
  const ref = useRef<HTMLDivElement>(null)
  const [collapsed, setCollapsed] = useState(false)
  const [needsCollapse, setNeedsCollapse] = useState(false)
  useEffect(() => {
    if (ref.current && ref.current.scrollHeight > 200) { setNeedsCollapse(true); setCollapsed(true) }
  }, [text])
  return (
    <div data-id="chat-history-view-collapsible-q" className="flex justify-end mb-2.5">
      <div data-id="chat-history-view-collapsible-q-wrap" className="max-w-[95%] relative">
        <div data-id="chat-history-view-collapsible-q-body" ref={ref} className={`chat-markdown px-3.5 py-2 rounded-2xl rounded-br-sm text-base leading-relaxed text-white/90 overflow-hidden transition-all ${collapsed ? 'max-h-[80px]' : ''}`} style={{ background: 'rgba(255,255,255,0.08)' }}>
          <Markdown remarkPlugins={[remarkGfm]}>{text.replace(/^-\n/, '')}</Markdown>
        </div>
        {needsCollapse && <button data-id="chat-history-view-collapsible-q-toggle" onClick={() => setCollapsed(v => !v)} className="text-base text-white/30 hover:text-white/60 mt-1 float-right">{collapsed ? t('expand') : t('collapse')}</button>}
      </div>
    </div>
  )
}

const ToolCard = ({ tool, running }: { tool: any; running?: boolean }) => {
  const [open, setOpen] = useState(false)
  const icon = TOOL_ICONS[tool.name] || '⚙️'
  const label = TOOL_LABELS[tool.name] || tool.name
  const arg = tool.arg?.replace(/^\/home\/\w+\//, '~/') || ''
  const isError = tool.result?.startsWith('exit ') || tool.result?.startsWith('❌')
  const hasDiff = !!tool.diff?.old || !!tool.diff?.new
  const statusIcon = running ? '⏳' : isError ? '✗' : '✓'
  const statusColor = running ? 'text-yellow-400' : isError ? 'text-red-400' : 'text-emerald-400'
  const borderColor = running ? 'border-yellow-500/20' : isError ? 'border-red-500/15' : 'border-white/[0.06]'
  return (
    <div data-id="chat-history-view-tool-card" className={`rounded-lg bg-[#1a1a2e]/60 border ${borderColor} overflow-hidden`}>
      <div data-id="chat-history-view-tool-card-header" className="flex items-center gap-2 px-3 py-1.5 cursor-pointer select-none hover:bg-white/[0.03] transition-colors" onClick={() => setOpen(p => !p)}>
        <span data-id="chat-history-view-tool-card-status" className={`text-xs ${statusColor}`}>{running ? <span className="inline-block w-2.5 h-2.5 border border-yellow-400/40 border-t-yellow-400 rounded-full animate-spin" /> : statusIcon}</span>
        <span data-id="chat-history-view-tool-card-icon" className="text-xs">{icon}</span>
        <span data-id="chat-history-view-tool-card-label" className="text-xs px-1 py-0.5 rounded bg-white/[0.04] text-vsc-text-muted/50">{label}</span>
        {!open && <span data-id="chat-history-view-tool-card-arg-preview" className="text-xs font-mono text-vsc-text/40 truncate flex-1" title={arg}>{arg}</span>}
        <span data-id="chat-history-view-tool-card-caret" className="text-xs text-vsc-text-muted/30">{open ? '▼' : '▶'}</span>
      </div>
      {open && arg && <div data-id="chat-history-view-tool-card-arg" className="px-3 py-1.5 text-sm font-mono text-vsc-text/50 whitespace-pre-wrap break-all border-b border-white/[0.04]">{arg}</div>}
      {open && hasDiff ? (
        <div data-id="chat-history-view-tool-card-diff" className="mx-2 mb-2 rounded overflow-hidden border border-white/[0.06] text-xs font-mono max-h-[300px] overflow-auto">
          {tool.diff.old && tool.diff.old.split('\n').map((line: string, i: number) => <div key={`o${i}`} data-id={`chat-history-view-tool-card-diff-old-${i}`} className="px-2 bg-red-500/[0.08] text-red-400/80 whitespace-pre-wrap break-all leading-relaxed">- {line}</div>)}
          {tool.diff.new && tool.diff.new.split('\n').map((line: string, i: number) => <div key={`n${i}`} data-id={`chat-history-view-tool-card-diff-new-${i}`} className="px-2 bg-emerald-500/[0.08] text-emerald-400/80 whitespace-pre-wrap break-all leading-relaxed">+ {line}</div>)}
        </div>
      ) : open && tool.result ? (
        <pre data-id="chat-history-view-tool-card-result" className={`text-xs mx-2 mb-2 px-2.5 py-1.5 rounded font-mono whitespace-pre-wrap break-all max-h-[200px] overflow-auto leading-relaxed ${isError ? 'bg-red-500/[0.06] text-red-400' : 'bg-emerald-500/[0.04] text-emerald-400'}`}>{tool.result}</pre>
      ) : null}
    </div>
  )
}

const ThinkingBlock = ({ text }: { text: string }) => (
  <div data-id="chat-history-view-thinking-block" className="my-2 rounded-lg border border-amber-300/[0.08] bg-amber-500/[0.05] px-3 py-2">
    <div data-id="chat-history-view-thinking-block-label" className="mb-1 text-[11px] uppercase text-amber-300/60">Thinking</div>
    <div data-id="chat-history-view-thinking-block-body" className="chat-markdown text-sm leading-[1.7] text-amber-50/75"><Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown></div>
  </div>
)

export default function ChatHistoryView({ paneId, token, liveStatus = 'idle', liveText = '', historyVersion = 0, suggestionText = '', suggestionPending = false, suggestionSending = false, onExecuteSuggestion }: ChatHistoryViewProps) {
  const { t } = useTranslation('agentChat')
  const [chatData, setChatData] = useState<HistoryTurn[]>([])
  const [loading, setLoading] = useState(true)
  const scrollRef = useRef<HTMLDivElement>(null)
  const latestQRef = useRef<HTMLDivElement>(null)
  const [showTtyd, setShowTtyd] = useState(false)
  const ttydUrl = token ? `${config.ttydBase}/ttyd/${paneId}/?token=${token}` : ''

  useEffect(() => {
    setChatData([])
    setLoading(false)
  }, [paneId, token, historyVersion])

  const groups = useMemo(() => {
    const base = chatData.filter((c) => c.q && !/\[SUGGESTION MODE:/i.test(c.q) && !/\[SUGGESTION MODE:/i.test(c.a || '') && !/suggestion mode/i.test((c.steps || []).map((step: any) => step.type === 'text' ? step.text : '').join('\n')))
    if (!base.length || !liveText || (liveStatus !== 'streaming' && liveStatus !== 'tool_use' && liveStatus !== 'pending')) return base
    const next = [...base]
    const last = { ...next[next.length - 1] }
    const steps = Array.isArray(last.steps) ? [...last.steps] : []
    if (!steps.length || steps[steps.length - 1].type !== 'text') steps.push({ type: 'text', text: liveText })
    else steps[steps.length - 1] = { type: 'text', text: liveText }
    last.steps = steps
    last.status = liveStatus
    next[next.length - 1] = last
    return next
  }, [chatData, liveStatus, liveText])

  const lastTurnStatus = groups.length ? String(groups[groups.length - 1]?.status || '').toLowerCase() : ''
  const stickyWorking = liveStatus === 'pending' || liveStatus === 'tool_use' || liveStatus === 'streaming' || lastTurnStatus === 'pending' || lastTurnStatus === 'tool_use' || lastTurnStatus === 'streaming' || suggestionPending
  const stickyLabel = suggestionPending
    ? t('statusSuggesting')
    : liveStatus === 'pending' || lastTurnStatus === 'pending'
      ? t('statusThinking')
      : liveStatus === 'tool_use' || lastTurnStatus === 'tool_use'
        ? t('statusRunning')
        : t('statusStreaming')

  useEffect(() => {
    requestAnimationFrame(() => {
      const el = latestQRef.current
      const container = scrollRef.current
      if (el && container) container.scrollTo({ top: el.offsetTop - 8, behavior: 'auto' })
    })
  }, [groups.length])

  return (
    <div data-id="chat-history-view" className="flex flex-col h-full">
      <div data-id="chat-history-scroll" ref={scrollRef} className="flex-1 overflow-y-auto pb-20">
        <div data-id="chat-history-list" className="max-w-full mx-auto px-2 py-4">
          {loading ? (
            <div data-id="chat-history-loading" className="flex flex-col items-center justify-center pt-20 gap-3"><Spinner size="lg" /><span className="text-base text-vsc-text-muted">{t('loadingHistory')}</span></div>
          ) : groups.length === 0 ? (
            <div data-id="chat-history-empty" className="text-center pt-20"><div className="text-2xl mb-2 opacity-20">✦</div><p className="text-xs text-vsc-text-muted">{t('waitingConversation')}</p></div>
          ) : groups.map((r, gi) => {
            const isLatest = gi === groups.length - 1
            const steps = Array.isArray(r?.steps) ? r.steps : []
            const ranSec = r?.ts && r?.start_ts ? Math.max(0, Math.round(r.ts - r.start_ts)) : 0
            const isRunning = r?.status === 'tool_use'
            const isPending = r?.status === 'pending'
            const isStreaming = r?.status === 'streaming'
            const model = r?.model || ''
            const timeStr = ranSec >= 60 ? `${Math.floor(ranSec / 60)}m${ranSec % 60}s` : ranSec > 0 ? `${ranSec}s` : ''
            const hasToolStep = steps.some((s: any) => s.type === 'tool')
            const toolCount = steps.filter((s: any) => s.type === 'tool').reduce((n: number, s: any) => n + (s.tools?.filter((t: any) => t.arg)?.length || 0), 0)
            const credit = r?.credit || 0
            return (
              <div data-id="chat-history-turn" key={gi} className="mb-5" ref={isLatest ? latestQRef : undefined}>
                <CollapsibleQ text={r.q} />
                <div data-id="chat-history-turn-card" className="rounded-xl border border-white/[0.06] bg-white/[0.02] overflow-hidden">
                  <div data-id="chat-history-turn-card-header" className="flex items-center gap-1.5 px-3.5 py-1 border-b border-white/[0.03] flex-wrap">
                    <span data-id="chat-history-turn-card-header-tag" className="text-vsc-accent text-sm font-medium opacity-60">✦ AI</span>
                    {model && <span data-id="chat-history-turn-card-header-model" className="text-xs px-1 py-0.5 rounded bg-white/[0.03] text-vsc-text-muted/40">{model}</span>}
                    {timeStr && <span data-id="chat-history-turn-card-header-time" className="text-xs text-vsc-text-muted/30">⏱{timeStr}</span>}
                    {toolCount > 0 && <span data-id="chat-history-turn-card-header-toolcount" className="text-xs text-vsc-text-muted/30">🔧×{toolCount}</span>}
                    <span data-id="chat-history-turn-card-header-spacer" className="flex-1" />
                    {credit > 0 && <span data-id="chat-history-turn-card-header-credit" className="text-xs text-vsc-text-muted/25 font-mono">${credit.toFixed(3)}</span>}
                  </div>
                  <div data-id="chat-history-turn-card-body" className="px-3.5 py-2.5">
                    {steps.map((s: any, si: number) => {
                      const isLast = si === steps.length - 1
                      if (s.type === 'thinking') {
                        return <ThinkingBlock key={si} text={s.text} />
                      }
                      if (s.type === 'text') {
                        const isFinal = isLast && (r?.status === 'text' || r?.status === 'done')
                        if (!isFinal && hasToolStep) return <div key={si} data-id={`chat-history-turn-step-text-secondary-${si}`} className="chat-markdown text-base text-vsc-text-muted/80 my-2 pl-3 leading-relaxed border-l-2 border-white/[0.06]"><Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown></div>
                        return <div key={si} data-id={`chat-history-turn-step-text-${si}`} className={`chat-markdown text-base text-vsc-text leading-[1.7] ${si > 0 ? 'mt-2 pt-2 border-t border-white/[0.04]' : ''}`}><Markdown remarkPlugins={[remarkGfm]}>{s.text}</Markdown></div>
                      }
                      const toolsWithArg = s.tools || []
                      if (!toolsWithArg.length) return null
                      return <div key={si} data-id={`chat-history-turn-step-tools-${si}`} className="my-2 space-y-1.5">{toolsWithArg.map((t: any, ti: number) => <ToolCard key={ti} tool={t} running={isLast && isRunning && ti === toolsWithArg.length - 1} />)}</div>
                    })}
                    {isPending && <div data-id="chat-history-turn-pending" className="flex items-center gap-2 py-1.5"><div data-id="chat-history-turn-pending-dot" className="w-2 h-2 rounded-full bg-yellow-500 animate-pulse" /><span data-id="chat-history-turn-pending-label" className="text-base text-yellow-400/80 animate-pulse">{t('statusThinking')}</span></div>}
                    {isRunning && <div data-id="chat-history-turn-running" className="flex items-center gap-2 py-1.5"><div data-id="chat-history-turn-running-spinner" className="w-3 h-3 border border-yellow-400/40 border-t-yellow-400 rounded-full animate-spin" /><span data-id="chat-history-turn-running-label" className="text-base text-yellow-400/80">{t('runningTools', { toolPart: toolCount > 0 ? t('runningTool', { count: toolCount }) : '' })}</span></div>}
                    {isStreaming && <div data-id="chat-history-turn-streaming" className="flex items-center gap-2 py-1.5"><div data-id="chat-history-turn-streaming-dot" className="w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" /><span data-id="chat-history-turn-streaming-label" className="text-base text-blue-400/80">{t('statusStreaming')}</span></div>}
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      </div>
      <div data-id="chat-history-sticky-bar" className="shrink-0 border-t border-white/[0.06] bg-[#0f1014] px-3 py-2">
          <div data-id="chat-history-sticky-bar-inner" className="flex items-center gap-3 rounded-lg border border-white/[0.06] bg-white/[0.03] px-3 py-2">
            {stickyWorking ? (
              <>
                <div data-id="chat-history-sticky-bar-status-dot" className={`h-2 w-2 rounded-full ${(liveStatus === 'streaming' || lastTurnStatus === 'streaming') ? 'bg-blue-400 animate-pulse' : 'bg-yellow-400 animate-pulse'}`} />
                <div data-id="chat-history-sticky-bar-status-label" className="text-sm text-zinc-200">{stickyLabel}</div>
              </>
            ) : null}
            {suggestionText ? (
              <>
                <div data-id="chat-history-sticky-bar-suggestion-text" className="min-w-0 flex-1 text-sm text-zinc-200 whitespace-pre-wrap break-words">{suggestionText}</div>
                <button
                  type="button"
                  data-id="chat-history-suggestion-execute"
                  disabled={suggestionSending}
                  onClick={() => { if (onExecuteSuggestion) void onExecuteSuggestion(suggestionText) }}
                  className="shrink-0 rounded-md bg-vsc-accent px-3 py-1.5 text-sm text-white disabled:opacity-60"
                >
                  {suggestionSending ? t('executingSending') : t('executing')}
                </button>
              </>
            ) : null}
            {!stickyWorking && !suggestionText ? <div data-id="chat-history-sticky-bar-idle" className="text-sm text-zinc-500">{t('idle')}</div> : null}
          </div>
        </div>
      {showTtyd ? (
        <div data-id="chat-history-mini-ttyd" className="shrink-0 h-[160px] mx-2 mb-1 rounded-lg overflow-hidden border border-white/[0.08] relative">
          <button data-id="chat-history-mini-ttyd-close" onClick={() => setShowTtyd(false)} className="absolute top-1 right-1 z-10 w-5 h-5 flex items-center justify-center rounded bg-black/60 text-white/40 hover:text-white/80 text-xs">✕</button>
          <WebFrame src={ttydUrl} className="w-full h-full border-0 bg-black" title="terminal-mini" />
        </div>
      ) : null}
    </div>
  )
}
