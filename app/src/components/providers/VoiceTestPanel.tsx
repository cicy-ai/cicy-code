// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// VoiceTestPanel — the 「语音测试」 section on a protocol:"voice" provider's
// detail page. Every capability block is testable on its own tab:
//   合成     POST /api/voice/tts (mp3)          — pick a speaker, hear the clip
//   流式合成 POST /api/voice/tts (stream, pcm)   — Web-Audio queued playback,
//            shows time-to-first-byte (the number streaming exists for)
//   识别     WS /api/voice/asr                   — mic → PCM16@16k → transcripts
//            (bridged server-side: the browser can't set X-Api-* headers)
//   克隆     honest placeholder — no first-hand API data; S_* speaker ids from
//            the console work as TTS speakers once trained there.
import { CircleStop, Loader2, Mic, Play } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { TokenManager } from '../../services/tokenManager'

const VOICE_LABELS: Record<string, string> = {
  zh_female_shuangkuaisisi_uranus_bigtts: '爽快思思 · 活泼',
  zh_female_tianmeixiaoyuan_uranus_bigtts: '甜美小源',
  zh_female_cancan_uranus_bigtts: '知性灿灿',
  zh_female_xiaohe_uranus_bigtts: '小何 · 通用',
  zh_female_vv_uranus_bigtts: 'Vivi · 多语种',
  zh_male_taocheng_uranus_bigtts: '小天 · 男声',
}

const TABS = [
  { id: 'tts', label: '语音合成' },
  { id: 'tts_stream', label: '流式合成' },
  { id: 'asr', label: '语音识别' },
  { id: 'clone', label: '语音克隆' },
] as const

type TabId = (typeof TABS)[number]['id']

async function readErrorDetail(resp: Response): Promise<string> {
  try {
    const text = await resp.text()
    try { return JSON.parse(text)?.error || text } catch { return text }
  } catch { return `HTTP ${resp.status}` }
}

export default function VoiceTestPanel({ providerKey, models, defaultModel }: {
  providerKey: string
  models: string[]
  defaultModel: string
}) {
  const { t } = useTranslation('provider')
  const [tab, setTab] = useState<TabId>('tts')
  const [speaker, setSpeaker] = useState(defaultModel || models[0] || '')
  const [text, setText] = useState('你好,我是 CiCy,这是一条语音合成测试。')
  const [busy, setBusy] = useState(false)
  const [stat, setStat] = useState('')
  const [error, setError] = useState('')
  // ASR state
  const [recording, setRecording] = useState(false)
  const [segs, setSegs] = useState<Map<string, { text: string; definite: boolean }>>(new Map())
  const asrRef = useRef<{ ws: WebSocket; ctx: AudioContext; stream: MediaStream; node: ScriptProcessorNode } | null>(null)
  const audioCtxRef = useRef<AudioContext | null>(null)

  useEffect(() => () => { stopAsr(); audioCtxRef.current?.close().catch(() => {}) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!speaker && (defaultModel || models[0])) setSpeaker(defaultModel || models[0])
  }, [defaultModel, models, speaker])

  const runTTS = useCallback(async () => {
    setBusy(true); setError(''); setStat('')
    const started = performance.now()
    try {
      const resp = await fetch('/api/voice/tts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${TokenManager.getToken() || ''}` },
        body: JSON.stringify({ key: providerKey, speaker, text, stream: false }),
      })
      if (!resp.ok) throw new Error(await readErrorDetail(resp))
      const blob = await resp.blob()
      const ms = Math.round(performance.now() - started)
      setStat(`合成 ${ms}ms · ${(blob.size / 1024).toFixed(1)} KB`)
      const url = URL.createObjectURL(blob)
      const audio = new Audio(url)
      audio.onended = () => URL.revokeObjectURL(url)
      await audio.play()
    } catch (e: any) {
      setError(String(e?.message || e))
    } finally { setBusy(false) }
  }, [providerKey, speaker, text])

  // Streaming PCM16LE mono 24k → Web Audio queued playback. TTFB is the point.
  const runTTSStream = useCallback(async () => {
    setBusy(true); setError(''); setStat('')
    const started = performance.now()
    let firstMs = 0
    try {
      const resp = await fetch('/api/voice/tts', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${TokenManager.getToken() || ''}` },
        body: JSON.stringify({ key: providerKey, speaker, text, stream: true }),
      })
      if (!resp.ok) throw new Error(await readErrorDetail(resp))
      if (!resp.body) throw new Error('no stream body')
      const sampleRate = Number(resp.headers.get('X-Sample-Rate')) || 24000
      const ctx = audioCtxRef.current && audioCtxRef.current.state !== 'closed'
        ? audioCtxRef.current
        : new AudioContext()
      audioCtxRef.current = ctx
      await ctx.resume()
      let nextTime = ctx.currentTime
      let leftover = new Uint8Array(0)
      let total = 0
      const reader = resp.body.getReader()
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        if (!firstMs) {
          firstMs = Math.round(performance.now() - started)
          setStat(`首包 ${firstMs}ms …`)
        }
        total += value.byteLength
        // stitch to an even byte count (Int16 alignment across chunks)
        const merged = new Uint8Array(leftover.length + value.length)
        merged.set(leftover); merged.set(value, leftover.length)
        const even = merged.length - (merged.length % 2)
        leftover = merged.slice(even)
        if (even === 0) continue
        const pcm = new Int16Array(merged.buffer.slice(0, even))
        const buf = ctx.createBuffer(1, pcm.length, sampleRate)
        const ch = buf.getChannelData(0)
        for (let i = 0; i < pcm.length; i++) ch[i] = pcm[i] / 32768
        const src = ctx.createBufferSource()
        src.buffer = buf
        src.connect(ctx.destination)
        nextTime = Math.max(nextTime, ctx.currentTime)
        src.start(nextTime)
        nextTime += buf.duration
      }
      const ms = Math.round(performance.now() - started)
      setStat(`首包 ${firstMs}ms · 共 ${ms}ms · ${(total / 1024).toFixed(0)} KB PCM`)
    } catch (e: any) {
      setError(String(e?.message || e))
    } finally { setBusy(false) }
  }, [providerKey, speaker, text])

  const stopAsr = useCallback(() => {
    const cur = asrRef.current
    asrRef.current = null
    if (!cur) return
    try { cur.ws.send('flush') } catch {}
    try { cur.node.disconnect() } catch {}
    try { cur.stream.getTracks().forEach((tr) => tr.stop()) } catch {}
    try { cur.ctx.close() } catch {}
    // leave ws open briefly so the final (definite) transcript arrives
    window.setTimeout(() => { try { cur.ws.close() } catch {} }, 4000)
    setRecording(false)
  }, [])

  const startAsr = useCallback(async () => {
    setError(''); setSegs(new Map())
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(`${proto}://${location.host}/api/voice/asr?key=${encodeURIComponent(providerKey)}&token=${encodeURIComponent(TokenManager.getToken() || '')}`)
      ws.binaryType = 'arraybuffer'
      ws.onmessage = (ev) => {
        try {
          const d = JSON.parse(String(ev.data))
          if (d.type === 'transcript') {
            setSegs((prev) => {
              const next = new Map(prev)
              next.set(String(d.seg), { text: d.text, definite: !!d.definite })
              return next
            })
          } else if (d.type === 'error') {
            setError(String(d.error))
            stopAsr()
          }
        } catch {}
      }
      ws.onerror = () => setError('ASR websocket 连接失败')
      const ctx = new AudioContext()
      const source = ctx.createMediaStreamSource(stream)
      // ScriptProcessor: deprecated but universally available; fine for a test
      // panel. Downsample ctx.sampleRate → 16k Int16 by linear resampling.
      const node = ctx.createScriptProcessor(4096, 1, 1)
      const ratio = ctx.sampleRate / 16000
      node.onaudioprocess = (ev) => {
        if (ws.readyState !== WebSocket.OPEN) return
        const input = ev.inputBuffer.getChannelData(0)
        const outLen = Math.floor(input.length / ratio)
        const out = new Int16Array(outLen)
        for (let i = 0; i < outLen; i++) {
          const pos = i * ratio
          const i0 = Math.floor(pos)
          const i1 = Math.min(i0 + 1, input.length - 1)
          const sample = input[i0] + (input[i1] - input[i0]) * (pos - i0)
          out[i] = Math.max(-32768, Math.min(32767, Math.round(sample * 32767)))
        }
        ws.send(out.buffer)
      }
      source.connect(node)
      node.connect(ctx.destination)
      asrRef.current = { ws, ctx, stream, node }
      setRecording(true)
    } catch (e: any) {
      setError(String(e?.message || e))
    }
  }, [providerKey, stopAsr])

  const speakerOptions = models.length ? models : Object.keys(VOICE_LABELS)
  const transcriptText = [...segs.entries()]
    .sort((a, b) => a[0].localeCompare(b[0], undefined, { numeric: true }))
    .map(([, v]) => v)

  const speakerRow = (
    <div data-id="voice-test-speaker-row" className="flex items-center gap-2">
      <span className="shrink-0 text-[11px] text-zinc-500">{t('voiceSpeaker', { defaultValue: '音色' })}</span>
      <select
        data-id="voice-test-speaker-select"
        value={speaker}
        onChange={(e) => setSpeaker(e.target.value)}
        className="h-8 min-w-0 flex-1 rounded-md border border-white/[0.08] bg-black/40 px-2 text-[12px] text-zinc-200 focus:border-blue-500/40 focus:outline-none"
      >
        {speakerOptions.map((m) => (
          <option key={m} value={m}>{VOICE_LABELS[m] ? `${VOICE_LABELS[m]} (${m})` : m}</option>
        ))}
      </select>
    </div>
  )

  return (
    <div data-id="voice-test-panel" className="mb-5 rounded-xl border border-white/[0.08] bg-white/[0.02] p-3.5">
      <div data-id="voice-test-tabs" className="mb-3 flex items-center gap-1">
        {TABS.map((it) => (
          <button
            key={it.id}
            type="button"
            data-id={`voice-test-tab-${it.id}`}
            onClick={() => { setTab(it.id); setError(''); setStat('') }}
            className={`rounded-md px-2.5 py-1.5 text-[12px] leading-none transition-colors ${tab === it.id ? 'bg-white/[0.1] text-zinc-100' : 'text-zinc-500 hover:bg-white/[0.05] hover:text-zinc-200'}`}
          >
            {it.label}
          </button>
        ))}
        <span className="flex-1" />
        <span className="text-[10px] uppercase tracking-[0.14em] text-zinc-600">{t('voiceTestTitle', { defaultValue: '语音测试' })}</span>
      </div>

      {(tab === 'tts' || tab === 'tts_stream') && (
        <div data-id="voice-test-tts" className="space-y-2.5">
          {speakerRow}
          <textarea
            data-id="voice-test-text-input"
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={2}
            className="w-full resize-none rounded-md border border-white/[0.08] bg-black/40 px-2.5 py-1.5 text-[12px] leading-5 text-zinc-200 focus:border-blue-500/40 focus:outline-none"
          />
          <div className="flex items-center gap-3">
            <button
              type="button"
              data-id="voice-test-play-button"
              disabled={busy || !text.trim() || !speaker}
              onClick={() => void (tab === 'tts' ? runTTS() : runTTSStream())}
              className="inline-flex h-8 items-center gap-1.5 rounded-md bg-indigo-500/80 px-3 text-[12px] text-white transition-colors hover:bg-indigo-500 disabled:opacity-50"
            >
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              {tab === 'tts' ? t('voiceTestPlay', { defaultValue: '试听' }) : t('voiceTestPlayStream', { defaultValue: '流式试听' })}
            </button>
            {stat && <span data-id="voice-test-stat" className="text-[11px] tabular-nums text-emerald-300">{stat}</span>}
          </div>
        </div>
      )}

      {tab === 'asr' && (
        <div data-id="voice-test-asr" className="space-y-2.5">
          <div className="flex items-center gap-3">
            <button
              type="button"
              data-id="voice-test-asr-button"
              onClick={() => void (recording ? stopAsr() : startAsr())}
              className={`inline-flex h-8 items-center gap-1.5 rounded-md px-3 text-[12px] text-white transition-colors ${recording ? 'bg-rose-500/80 hover:bg-rose-500' : 'bg-indigo-500/80 hover:bg-indigo-500'}`}
            >
              {recording ? <CircleStop className="h-3.5 w-3.5" /> : <Mic className="h-3.5 w-3.5" />}
              {recording ? t('voiceAsrStop', { defaultValue: '停止并定稿' }) : t('voiceAsrStart', { defaultValue: '开始说话' })}
            </button>
            {recording && <span className="inline-flex items-center gap-1.5 text-[11px] text-rose-300"><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-rose-400" />{t('voiceAsrRecording', { defaultValue: '录音中,对着麦克风说话…' })}</span>}
          </div>
          <div data-id="voice-test-asr-transcript" className="min-h-[54px] rounded-md border border-white/[0.06] bg-black/40 px-2.5 py-2 text-[13px] leading-6">
            {transcriptText.length === 0
              ? <span className="text-zinc-600">{t('voiceAsrEmpty', { defaultValue: '识别结果会实时出现在这里;灰色为中间结果,白色为已定稿。' })}</span>
              : transcriptText.map((v, i) => (
                  <span key={i} className={v.definite ? 'text-zinc-100' : 'text-zinc-500'}>{v.text}</span>
                ))}
          </div>
        </div>
      )}

      {tab === 'clone' && (
        <div data-id="voice-test-clone" className="space-y-2 text-[12px] leading-relaxed text-zinc-400">
          <p>{t('voiceCloneNote1', { defaultValue: '声音复刻(训练自定义音色)在火山控制台完成,训练产出 S_ 开头的音色 id;把它填进上方「可用模型」列表后,就能在「语音合成 / 流式合成」页签里当音色测试。' })}</p>
          <p>
            {t('voiceCloneNote2', { defaultValue: '训练 API 尚未接入(需以官方「声音复刻」文档为准,Resource-Id 与 TTS 不同、按音色数量计费)。' })}{' '}
            <a data-id="voice-test-clone-console-link" href="https://console.volcengine.com/speech/new/setting/apikeys?projectName=default" target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:text-blue-300">
              {t('voiceCloneConsole', { defaultValue: '前往火山控制台「声音复刻」' })}
            </a>
          </p>
        </div>
      )}

      {error && (
        <div data-id="voice-test-error" className="mt-2.5 rounded-md border border-rose-500/25 bg-rose-500/[0.08] px-2.5 py-2 text-[11px] leading-relaxed text-rose-300">
          {error}
        </div>
      )}
    </div>
  )
}
