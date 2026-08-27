// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

// TerminalView — the iframe-free agent terminal. One xterm.js instance speaking
// the gotty/webtty protocol straight at /ttyd/<pane>/ws. Replaces the WebFrame
// (iframe → gotty page) path in AgentStack; the standalone /ttyd/ page remains
// for external links.
//
// Protocol (api/webtty/message_types.go):
//   client → server  '1'+text input · '2' ping · '3'+JSON resize
//   server → client  '1'+base64 output · '2' pong · '3' title · '4' prefs · '5' reconnect
// The server consumes any '{'-leading handshake message (5cbed6e), so the gotty
// init JSON is sent for fidelity but optional.
//
// Anti-flash: on (re)connect the previous frame stays visible (dimmed); the
// terminal is reset+rewritten only when the new connection's FIRST output frame
// (the server-side capture-pane backfill) arrives — no blank gap.

import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { replAttachmentMarkdown } from '../../lib/attachmentMarkdown'
import apiService from '../../services/api'
import { scanLinksOnText, type LinkKind } from './linkDetect'
import { getCicyTheme } from '../../lib/theme'

// ── 逻辑行重建(移植自 api/js/src/xterm.ts)──
// xterm 的 link provider 按"物理行"询问;URL/路径经常跨软换行,必须把 wrap
// 链拼回一条逻辑行再扫,并保留每个字符到 buffer 单元格的映射用于回标区间。
const MAX_WRAP_ROWS = 32

interface LogicalLine {
  text: string
  cellMap: Array<{ y: number; x: number }>
}

function findAnchorY(term: Terminal, y: number): number {
  const buf = term.buffer.active
  let cur = y
  while (cur > 0) {
    const line = buf.getLine(cur)
    if (!line || !line.isWrapped) break
    cur -= 1
  }
  return cur
}

function buildLogicalLine(term: Terminal, anchorY: number): LogicalLine | null {
  const buf = term.buffer.active
  if (!buf.getLine(anchorY)) return null
  const cell = buf.getNullCell()
  let text = ''
  const cellMap: Array<{ y: number; x: number }> = []
  for (let i = 0; i < MAX_WRAP_ROWS; i += 1) {
    const ry = anchorY + i
    const line = buf.getLine(ry)
    if (!line) break
    if (i > 0 && !line.isWrapped) break
    for (let x = 0; x < line.length; x += 1) {
      line.getCell(x, cell)
      if (cell.getWidth() === 0) continue // wide-char trail half
      const chars = cell.getChars() || ' '
      for (let k = 0; k < chars.length; k += 1) {
        text += chars[k]
        cellMap.push({ y: ry, x })
      }
    }
    const next = buf.getLine(ry + 1)
    if (!next || !next.isWrapped) break
  }
  return { text, cellMap }
}

// Same look as the gotty page (api/js/src/xterm.ts + font.ts): soft warm-gray
// foreground — NOT pure white — and the platform mono stack with color-emoji
// fallbacks.
const EMOJI_FALLBACK = '"Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", "Noto Color Emoji", "EmojiOne Color"'
const isWindows = /windows/i.test(navigator.userAgent)
// CJK glyphs are not in any Latin mono font; without an explicit fallback the
// DOM renderer picks the platform default mono CJK (SimSun on Windows — the
// bitmap-looking "ugly" Chinese). Prefer real mono CJK faces when installed,
// then the platform's UI CJK sans, which xterm still lays out on 2 cells.
const CJK_FALLBACK = isWindows
  ? '"Sarasa Mono SC", "Sarasa Term SC", "Maple Mono NF CN", "Noto Sans Mono CJK SC", "Microsoft YaHei", "微软雅黑"'
  : '"Sarasa Mono SC", "Noto Sans Mono CJK SC", "PingFang SC", "Hiragino Sans GB", "Noto Sans CJK SC", "WenQuanYi Micro Hei"'
// "JetBrains Mono Variable" is bundled with the app (@fontsource-variable), so
// the Latin part of the terminal looks the same on every platform.
const MONO_FONT = isWindows
  ? `"JetBrains Mono Variable", "Cascadia Mono", "Cascadia Code", Consolas, ${CJK_FALLBACK}, ${EMOJI_FALLBACK}, monospace`
  : `"JetBrains Mono Variable", "SF Mono", Menlo, Consolas, ${CJK_FALLBACK}, ${EMOJI_FALLBACK}, monospace`
const TERM_THEMES = {
  dark: { background: '#000000', foreground: '#d4d4d8', cursor: '#e4e4e7', selectionBackground: '#3b82f640' },
  light: {
    background: '#fafafa', foreground: '#27272a', cursor: '#18181b', selectionBackground: '#93c5fd80', selectionForeground: '#18181b',
    black: '#18181b', red: '#b91c1c', green: '#047857', yellow: '#a16207', blue: '#1d4ed8', magenta: '#a21caf', cyan: '#0e7490', white: '#d4d4d8',
    brightBlack: '#71717a', brightRed: '#dc2626', brightGreen: '#059669', brightYellow: '#ca8a04', brightBlue: '#2563eb', brightMagenta: '#c026d3', brightCyan: '#0891b2', brightWhite: '#18181b',
  },
}
const TERM_MINIMUM_CONTRAST = { dark: 1, light: 4.5 } as const

const MSG_IN_INPUT = '1'
const MSG_IN_PING = '2'
const MSG_IN_RESIZE = '3'
const MSG_OUT_OUTPUT = '1'

const PING_INTERVAL_MS = 30_000
const RECONNECT_MAX = 8
const RECONNECT_BASE_MS = 1_000

// Client-side defense in depth, mirroring api/js/src/xterm.ts: the server
// (webtty) already strips DA responses and alt-screen/mouse DECSET, but a
// second copy here keeps a raw/misbehaving upstream from flipping xterm into
// mouse-tracking or alt-screen (either would kill local scrollback).
const STRIP_RES = [
  /\x1b\[\?(?:100[0-3]|100[56]|101[56])[hl]/g, // mouse tracking
  /\x1b\[\?(?:47|104[789])[hl]/g,              // alternate screen
  /\x1b\[3J/g,                                 // clear-scrollback
  /\x1b\[[?>][\d;]*c/g,                        // DA responses
]

// ttydSrc is the iframe URL (`<base>/ttyd/<pane>/?token=...`); the WS lives at
// the same mount with `/ws` appended and the query dropped.
function wsUrlFromTtydSrc(ttydSrc: string): string {
  const u = new URL(ttydSrc, window.location.href)
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
  u.search = ''
  u.pathname = u.pathname.replace(/\/?$/, '/ws')
  return u.toString()
}

// `<base>/ttyd/<pane>/…` → pane id, for the mask-model lookup.
function paneIdFromTtydSrc(ttydSrc: string): string {
  const m = /\/ttyd(?:-shell)?\/([^/?#]+)/.exec(ttydSrc)
  return m ? decodeURIComponent(m[1]) : ''
}

// Replaces every full occurrence of each mask token with spaces, holding back a
// trailing partial (suffix of the chunk that is a prefix of a token) so a
// token split across two WS frames still gets masked. Port of the gotty page's
// applyModelMask (api/js/src/xterm.ts).
// Besides the model name itself, the startup banner's "model:" label and
// "/model to change" hint are masked too — with just the name blanked the line
// showed a bare label and an ugly gap; blanking all three renders it as an
// empty line (the opacity-0 look) without shifting the TUI layout.
// Matching must tolerate ANSI escapes BETWEEN the token's characters: codex
// prints "/model" cyan and " to change" dim (SGR), and the bottom status row
// interleaves cursor/erase CSI sequences between its styled segments — so a
// phrase never occurs contiguously in the raw stream. Only visible characters
// become spaces; every escape sequence is kept in place so styling state and
// cursor positioning are unchanged (zero-width, so the layout doesn't shift).
const ANSI_BETWEEN = '(?:\\x1b\\[[0-9;:?]*[ -/]?[@-~])*'
const ANSI_OR_CHAR = /(\x1b\[[0-9;:?]*[ -/]?[@-~])|[\s\S]/g
function maskPhraseRe(token: string): RegExp {
  // A space in the token is flexible: the TUI may render a gap as literal
  // spaces, more than one of them, or a cursor-forward escape (which
  // ANSI_BETWEEN already tolerates) — so match zero or more literal spaces.
  const escaped = token.split('').map((c) => (c === ' ' ? '[ ]*' : c.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')))
  return new RegExp(escaped.join(ANSI_BETWEEN), 'g')
}
function makeModelMasker(): { setToken: (t: string, paneId?: string) => void; apply: (chunk: string) => string } {
  let tokens: string[] = []
  let res: RegExp[] = []
  let carry = ''
  return {
    setToken(t: string, paneId?: string) {
      const model = (t || '').trim()
      tokens = model ? [model, 'model:', '/model to change'] : []
      // The bottom status row ("default · ~/cicy-ai/workers/<pane>") is blanked
      // as ONE phrase: the banner's legitimate "directory: ~/…" line shares the
      // path string, so a standalone path token would blank it there too. The
      // cwd is the default worker-workspace convention; a custom-workspace pane
      // won't match and simply keeps its row.
      if (model && paneId) tokens.push(`default · ~/cicy-ai/workers/${paneId}`)
      res = tokens.map(maskPhraseRe)
      carry = ''
    },
    apply(chunk: string): string {
      if (!tokens.length) return chunk
      // debug tap (masked panes only — unmasked panes would flood the ring):
      // localStorage `cicy.maskdebug`=1 keeps the last raw chunks on
      // window.__maskChunks so a real stream can be replayed against the regex
      // offline (the ONLY way to diagnose a mask miss — tmux capture-pane
      // reconstructs different bytes than the live PTY stream).
      try {
        if (window.localStorage.getItem('cicy.maskdebug') === '1') {
          const w = window as any
          w.__maskChunks = w.__maskChunks || []
          w.__maskChunks.push(chunk)
          if (w.__maskChunks.length > 400) w.__maskChunks.shift()
        }
      } catch { /* storage blocked */ }
      let s = carry + chunk
      carry = ''
      for (const re of res) {
        re.lastIndex = 0
        s = s.replace(re, (m) => m.replace(ANSI_OR_CHAR, (ch, esc) => (esc ? esc : ' ')))
      }
      // Escape-aware cross-chunk carry: a phrase split over two WS frames must
      // still mask, and the frame boundary can fall between styled segments or
      // even MID-escape — so (1) a trailing incomplete escape sequence is
      // withheld outright, and (2) the held-back tail is judged by its VISIBLE
      // characters (escapes skipped) but carried as raw bytes, escapes
      // included. Fuzzed against every split point of the real codex status
      // row — all masked.
      const pe = /\x1b(?:\[[0-9;:?]*[ -/]?)?$/.exec(s)
      let pending = ''
      if (pe) {
        pending = s.slice(pe.index)
        s = s.slice(0, pe.index)
      }
      const maxV = Math.max(...tokens.map((tok) => tok.length)) - 1
      const it = /(\x1b\[[0-9;:?]*[ -/]?[@-~])|[\s\S]/g
      const vis: Array<{ ch: string; idx: number }> = []
      let m: RegExpExecArray | null
      while ((m = it.exec(s))) {
        if (m[1]) continue
        vis.push({ ch: m[0], idx: m.index })
        if (vis.length > maxV) vis.shift()
      }
      outer: for (let k = vis.length; k > 0; k--) {
        const tailArr = vis.slice(vis.length - k)
        const visSuffix = tailArr.map((v) => v.ch).join('')
        for (const tok of tokens) {
          if (tok.length > k && tok.startsWith(visSuffix)) {
            carry = s.slice(tailArr[0].idx)
            s = s.slice(0, tailArr[0].idx)
            break outer
          }
        }
      }
      carry += pending
      return s
    },
  }
}

function isMac(): boolean {
  return /mac/i.test(navigator.userAgent)
}

// Component terminal is the default everywhere (codex included — the model
// mask is fetched via /api/tmux/ttyd/mask/). Escape hatch while it bakes:
// localStorage `cicy.term.iframe` = '1' forces the legacy gotty-page iframe.
export function shouldUseTerminalView(_agentType?: string): boolean {
  try {
    if (window.localStorage.getItem('cicy.term.iframe') === '1') return false
  } catch { /* storage blocked → default on */ }
  return true
}

export function TerminalView({ ttydSrc, className }: { ttydSrc: string; className?: string }) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [connState, setConnState] = useState<'connecting' | 'open' | 'retrying' | 'dead'>('connecting')

  useEffect(() => {
    const host = hostRef.current
    if (!host || !ttydSrc) return

    const initialTheme = getCicyTheme()
    const term = new Terminal({
      scrollback: 5000,
      fontSize: 13,
      fontFamily: MONO_FONT,
      theme: TERM_THEMES[initialTheme],
      // ANSI truecolor output can bypass our 16-color palette. Keep normal
      // text readable without flattening intentional terminal colors.
      minimumContrastRatio: TERM_MINIMUM_CONTRAST[initialTheme],
      allowProposedApi: true,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    // Unicode 11 width tables — wide emoji / ZWJ sequences measure correctly
    // instead of rendering half-cut (same as the gotty page).
    term.loadAddon(new Unicode11Addon())
    term.unicode.activeVersion = '11'
    term.open(host)
    const onThemeChange = () => {
      const theme = getCicyTheme()
      term.options.theme = TERM_THEMES[theme]
      term.options.minimumContrastRatio = TERM_MINIMUM_CONTRAST[theme]
    }
    window.addEventListener('cicy-theme-change', onThemeChange)
    fit.fit()
    term.focus()

    // ── 链接:URL 新开标签;file:// / image:// / 裸文件路径 → app 内置编辑器。
    // 用与 gotty 页相同的扫描器 + 逻辑行重建(跨软换行的链接才点得中);比
    // iframe 时代干净的地方:直接调 __cicyOpenCodeFile,不再走 console 哨兵桥。
    const activateLink = (event: MouseEvent, uri: string, kind: LinkKind) => {
      event.preventDefault()
      event.stopPropagation()
      term.clearSelection()
      if (kind === 'url') {
        window.open(uri, '_blank', 'noopener,noreferrer')
        return
      }
      const path = uri.replace(/^(?:file|image):\/\//, '').replace(/(?::\d+){1,2}$/, '')
      const openFn = (window as any).__cicyOpenCodeFile
      if (path && typeof openFn === 'function') openFn(path)
    }
    term.registerLinkProvider({
      provideLinks(bufferLineNumber: number, callback: (links: any[] | undefined) => void): void {
        const y = bufferLineNumber - 1
        const logical = buildLogicalLine(term, findAnchorY(term, y))
        if (!logical || !logical.text) { callback(undefined); return }
        const matches = scanLinksOnText(logical.text)
        if (!matches.length) { callback(undefined); return }
        const cols = term.cols
        const links: any[] = []
        for (const match of matches) {
          const startCell = logical.cellMap[match.start]
          const endCell = logical.cellMap[match.end - 1]
          if (!startCell || !endCell) continue
          if (startCell.y > y || endCell.y < y) continue
          // 链接跨软换行时,把区间裁剪到本物理行,装饰才能逐段盖住。
          const startX = startCell.y === y ? startCell.x : 0
          const endX = endCell.y === y ? endCell.x : Math.max(0, cols - 1)
          links.push({
            range: { start: { x: startX + 1, y: y + 1 }, end: { x: endX + 1, y: y + 1 } },
            text: match.uri,
            decorations: { underline: true, pointerCursor: true },
            activate: (event: MouseEvent) => activateLink(event, match.uri, match.kind),
          })
        }
        callback(links.length ? links : undefined)
      },
    })

    // codex-on-gateway panes leak the launch model name into the PTY stream;
    // fetch the mask token and blank it out (chunk-boundary safe). Fire and
    // forget — non-codex panes get "" and the masker stays a no-op.
    const masker = makeModelMasker()
    const paneId = paneIdFromTtydSrc(ttydSrc)
    // Belt-and-braces DOM layer on top of the stream masker: the DOM renderer
    // is in use here, so any row that still shows a leaked fragment (a stream
    // repaint the masker's chunk heuristics missed) gets `visibility:hidden`.
    // Row divs are recycled on scroll, so visibility is recomputed on every
    // mutation — a row un-hides itself the moment its content stops matching.
    let rowHideObserver: MutationObserver | null = null
    const startRowHider = (model: string) => {
      const rowsEl = host.querySelector('.xterm-rows')
      if (!rowsEl) return
      const applyRowHide = () => {
        for (const r of rowsEl.children) {
          const t = r.textContent || ''
          const leak = (model !== '' && t.includes(model))
            || /^\s*default\s*·/.test(t)
            || t.includes('/model to change')
            || /^\s*│?\s*model:/.test(t)
          ;(r as HTMLElement).style.visibility = leak ? 'hidden' : ''
        }
      }
      rowHideObserver = new MutationObserver(applyRowHide)
      rowHideObserver.observe(rowsEl, { subtree: true, childList: true, characterData: true })
      applyRowHide()
    }
    if (paneId) {
      apiService.getTtydMaskModel(paneId)
        .then((resp: any) => {
          const model = String(resp?.data?.model || '')
          masker.setToken(model, paneId)
          if (model) startRowHider(model)
        })
        .catch(() => { /* no mask */ })
    }

    // Selection-drag guard (port of gotty xterm.ts): while the user is dragging
    // a selection, fit()/resize must not reflow rows out from under the anchor.
    // Cleared on any document mouseup (capture) + a 10s safety timeout so a
    // missed mouseup can never freeze resizing forever.
    let isSelecting = false
    let selTimer = 0
    let pendingFit = false
    const endSelecting = () => {
      if (!isSelecting) return
      isSelecting = false
      window.clearTimeout(selTimer)
      if (pendingFit) {
        pendingFit = false
        if (host.clientWidth > 0 && host.clientHeight > 0) { fit.fit(); sendResize() }
      }
    }
    const onHostMouseDown = () => {
      isSelecting = true
      window.clearTimeout(selTimer)
      selTimer = window.setTimeout(endSelecting, 10_000)
    }
    host.addEventListener('mousedown', onHostMouseDown)

    // CSS-transform scale compensation. The project canvas scales cards with
    // `transform: scale(z)`; xterm maps clientX/Y to cells by subtracting the
    // screen's bounding rect and dividing by its UNscaled cell size, so at any
    // zoom != 100% the selection lands on the wrong column/row (error grows
    // with distance from the top-left). Rewrite mouse events in the capture
    // phase into the terminal's own coordinate space and replay them.
    const SCALED = '__cicyScaled'
    const rescaleMouse = (event: MouseEvent) => {
      if ((event as any)[SCALED]) return
      const screen = host.querySelector<HTMLElement>('.xterm-screen')
      if (!screen || !screen.offsetWidth) return
      const rect = screen.getBoundingClientRect()
      const scale = rect.width / screen.offsetWidth
      if (!Number.isFinite(scale) || scale <= 0 || Math.abs(scale - 1) < 0.001) return
      const clone = new MouseEvent(event.type, {
        bubbles: event.bubbles, cancelable: event.cancelable, composed: event.composed,
        view: event.view, detail: event.detail, button: event.button, buttons: event.buttons,
        ctrlKey: event.ctrlKey, shiftKey: event.shiftKey, altKey: event.altKey, metaKey: event.metaKey,
        relatedTarget: event.relatedTarget,
        clientX: rect.left + (event.clientX - rect.left) / scale,
        clientY: rect.top + (event.clientY - rect.top) / scale,
        screenX: event.screenX, screenY: event.screenY,
      })
      ;(clone as any)[SCALED] = true
      event.stopImmediatePropagation()
      ;(event.target as EventTarget).dispatchEvent(clone)
      if (clone.defaultPrevented) event.preventDefault()
    }
    // mousedown/dblclick originate inside the host; xterm listens for the
    // drag's mousemove/mouseup on document, so those are rescaled there but
    // only while a selection drag that started in this terminal is running.
    const onDocMouseScaled = (event: MouseEvent) => { if (isSelecting) rescaleMouse(event) }
    host.addEventListener('mousedown', rescaleMouse, true)
    host.addEventListener('dblclick', rescaleMouse, true)
    document.addEventListener('mousemove', onDocMouseScaled, true)
    document.addEventListener('mouseup', onDocMouseScaled, true)
    // Registered AFTER the rescaler: it must still see isSelecting=true on the
    // original mouseup so the replayed (rescaled) mouseup is what ends the drag.
    document.addEventListener('mouseup', endSelecting, true)

    // Cmd/Ctrl+C must COPY when there's a selection (and fall through to the
    // terminal otherwise) — same rule the gotty page uses (api/js/src/xterm.ts).
    term.attachCustomKeyEventHandler((event: KeyboardEvent) => {
      const isCopy = event.key.toLowerCase() === 'c' && !event.altKey &&
        (isMac() ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey)
      if (isCopy && term.hasSelection()) return false // let the browser copy
      // Windows 的 Ctrl+V 必须走浏览器的 paste 事件,不能落到 pane 里。xterm.js 默认
      // 把 Ctrl+V 变成 \x16 并 preventDefault,于是:①隐藏 textarea 收不到 paste 事件,
      // 下面那个「上传附件再打链接」的 onPaste 永远不触发;②\x16 直达 CLI agent,
      // codex 收到 ^V 会去读容器里的系统剪贴板 —— 容器里没有 X11,只能报
      // 「clipboard unavailable: X11 server connection timed out」。返回 false 跳过
      // xterm 的处理,两个问题一起消失。与 gotty 页(api/js/src/xterm.ts)同一条规则。
      // Mac 的 Cmd+V 本来就不被拦截;Linux 保持原样(Ctrl+Shift+V / 中键仍是粘贴路径)。
      const isPaste = event.key.toLowerCase() === 'v' &&
        event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey
      if (isWindows && isPaste) return false
      return true
    })

    let ws: WebSocket | null = null
    let pingTimer = 0
    let reconnectTimer = 0
    let switchVisualTimer = 0
    let attempts = 0
    let disposed = false

    const sendResize = () => {
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(MSG_IN_RESIZE + JSON.stringify({ columns: term.cols, rows: term.rows }))
      }
    }

    const connect = () => {
      if (disposed) return
      setConnState(attempts > 0 ? 'retrying' : 'connecting')
      host.classList.add('cicy-term-switching')
      window.clearTimeout(switchVisualTimer)
      // An idle/brand-new shell may have no tty backfill at all. Waiting only
      // for the first output frame leaves the light terminal permanently under
      // brightness(0.45), which renders as a solid gray canvas. Keep the
      // no-flash handoff for active terminals, but release the dimmer shortly
      // after a successful connection even when the pane is silent.
      switchVisualTimer = window.setTimeout(() => {
        if (!disposed && ws?.readyState === WebSocket.OPEN) {
          host.classList.remove('cicy-term-switching')
        }
      }, 500)
      const sock = new WebSocket(wsUrlFromTtydSrc(ttydSrc), 'webtty')
      ws = sock
      let first = true

      sock.onopen = () => {
        if (ws !== sock) return
        attempts = 0
        setConnState('open')
        sock.send(JSON.stringify({ Arguments: '', AuthToken: '' }))
        sendResize()
        window.clearInterval(pingTimer)
        pingTimer = window.setInterval(() => {
          if (sock.readyState === WebSocket.OPEN) sock.send(MSG_IN_PING)
        }, PING_INTERVAL_MS)
      }
      sock.onmessage = (ev) => {
        if (ws !== sock) return // late frame from a superseded connection
        const data = String(ev.data)
        if (data[0] !== MSG_OUT_OUTPUT) return
        let text = atob(data.slice(1))
        for (const re of STRIP_RES) text = text.replace(re, '')
        text = masker.apply(text)
        if (!text) return
        const bytes = Uint8Array.from(text, (c) => c.charCodeAt(0))
        if (first) {
          first = false
          // First frame = server backfill (or full repaint): swap the old
          // picture for the new one in a single tick — no blank flash.
          term.reset()
          window.clearTimeout(switchVisualTimer)
          host.classList.remove('cicy-term-switching')
        }
        term.write(bytes)
      }
      sock.onclose = () => {
        if (ws !== sock || disposed) return
        window.clearInterval(pingTimer)
        if (attempts >= RECONNECT_MAX) {
          setConnState('dead')
          host.classList.remove('cicy-term-switching')
          term.write('\r\n\x1b[31m[连接已断开 — 点击终端重试]\x1b[0m\r\n')
          return
        }
        attempts += 1
        setConnState('retrying')
        reconnectTimer = window.setTimeout(connect, Math.min(RECONNECT_BASE_MS * attempts, 8_000))
      }
      sock.onerror = () => { /* onclose follows and owns retry */ }
    }

    const dataSub = term.onData((d) => {
      if (ws?.readyState === WebSocket.OPEN) ws.send(MSG_IN_INPUT + d)
    })

    const ro = new ResizeObserver(() => {
      // A hidden/zero-size host makes fit() compute garbage; skip until visible.
      // While a selection drag is in flight, defer — reflow would yank the
      // anchored rows out from under the user (runs at drag end instead).
      if (isSelecting) { pendingFit = true; return }
      if (host.clientWidth > 0 && host.clientHeight > 0) {
        fit.fit()
        sendResize()
      }
    })
    ro.observe(host)

    // ── 粘贴图片/文档:上传到该 agent 的资产库,再把 markdown 引用打进当前
    // 终端的输入流(不自动回车,用户过目后自己发)。纯文本粘贴不拦截,走
    // xterm 原生通道。与头部回形针按钮(AttachSendButton)同一条上传链路。
    const onPaste = (e: ClipboardEvent) => {
      const files = e.clipboardData?.files
      if (!files || files.length === 0) return
      e.preventDefault()
      e.stopPropagation()
      const uploadPane = paneIdFromTtydSrc(ttydSrc)
      if (!uploadPane) return
      const list = Array.from(files)
      void (async () => {
        const parts: string[] = []
        for (const file of list) {
          try {
            const resp: any = await apiService.uploadAssetFile(uploadPane, file)
            const f = resp?.data?.file || {}
            const ref = String(f.file_ref || f.fileRef || '')
            const url = String(f.url || f.URL || '')
            const abs = ref ? '/' + ref.replace(/^file:\/\//, '').replace(/^\/+/, '') : url
            if (!abs) continue
            // 打进 CLI agent 的 REPL —— 一律用链接形式,图片也不加 `!`。
            // 为什么见 replAttachmentMarkdown 的注释(行首 `!` 会被当 shell 命令跑)。
            parts.push(replAttachmentMarkdown(file.name, abs))
          } catch { /* 单个文件失败跳过,其余继续 */ }
        }
        if (parts.length && ws?.readyState === WebSocket.OPEN) {
          ws.send(MSG_IN_INPUT + parts.join('\n\n'))
        }
      })()
    }
    host.addEventListener('paste', onPaste, true)

    // Manual retry once the backoff budget is spent.
    const onClickRetry = () => {
      if (!disposed && (ws === null || ws.readyState === WebSocket.CLOSED) && attempts >= RECONNECT_MAX) {
        attempts = 0
        connect()
      }
    }
    host.addEventListener('mousedown', onClickRetry)

    connect()

    return () => {
      disposed = true
      if (rowHideObserver) rowHideObserver.disconnect()
      window.clearInterval(pingTimer)
      window.clearTimeout(reconnectTimer)
      window.clearTimeout(switchVisualTimer)
      window.clearTimeout(selTimer)
      host.removeEventListener('mousedown', onClickRetry)
      host.removeEventListener('mousedown', onHostMouseDown)
      host.removeEventListener('mousedown', rescaleMouse, true)
      host.removeEventListener('dblclick', rescaleMouse, true)
      document.removeEventListener('mousemove', onDocMouseScaled, true)
      document.removeEventListener('mouseup', onDocMouseScaled, true)
      host.removeEventListener('paste', onPaste, true)
      window.removeEventListener('cicy-theme-change', onThemeChange)
      document.removeEventListener('mouseup', endSelecting, true)
      if (ws) { ws.onclose = null; ws.close() }
      ro.disconnect()
      dataSub.dispose()
      term.dispose()
    }
  }, [ttydSrc])

  return (
    <div data-id="terminal-view" className={`relative h-full w-full bg-black ${className || ''}`}>
      <div ref={hostRef} className="cicy-term-host h-full w-full pl-1 pt-1" />
      {connState === 'retrying' ? (
        <div data-id="terminal-view-retrying" role="status" className="pointer-events-none absolute right-3 top-3 flex items-center gap-1.5 rounded-full border border-amber-400/25 bg-black/80 px-2.5 py-1 text-[11px] font-medium text-amber-300 shadow-lg backdrop-blur-sm">
          <span data-id="terminal-view-retrying-dot" className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400" />
          <span>重连中…</span>
        </div>
      ) : null}
    </div>
  )
}

export default TerminalView
