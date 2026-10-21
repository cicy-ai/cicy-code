// SPDX-License-Identifier: Apache-2.0
//
// ImageAnnotator — opens when an image is pasted into a terminal. Lets the user
// crop, add text, translucent overlays (highlight / mask), extra images,
// arrows and boxes before the image is uploaded and referenced in the pane.
// Pure canvas, no dependencies. All shapes live in the base image's pixel
// space; the canvas is only scaled for display.

import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ArrowUpRight, ChevronDown, ChevronUp, Crop, Eye, EyeOff, ImagePlus, Layers, Minus, MousePointer2, PanelRight, Square, Trash2, Type, Undo2, X } from 'lucide-react'

type Tool = 'move' | 'crop' | 'rect' | 'arrow' | 'text' | 'overlay' | 'image'
export type Box = { x: number; y: number; w: number; h: number }
type Shape =
  | ({ type: 'rect' | 'overlay'; color: string; width: number } & Box)
  | { type: 'arrow'; x1: number; y1: number; x2: number; y2: number; color: string; width: number }
  | { type: 'text'; x: number; y: number; text: string; color: string; size: number }
  | ({ type: 'image'; img: HTMLImageElement } & Box)
// A shape on the layer stack. Index 0 sits right above the base image.
type Layer = Shape & { id: string; hidden?: boolean }

const LAYER_LABEL: Record<Shape['type'], string> = { rect: '方框', overlay: '浮层', arrow: '箭头', text: '文字', image: '图片' }
const LAYER_KIND: Record<Shape['type'], string> = { rect: '形状', overlay: '形状', arrow: '形状', text: '文字', image: '图片' }
let layerSeq = 0
const newLayerId = () => `layer-${Date.now().toString(36)}-${(layerSeq += 1)}`

/** Display names per layer: text shows its content, others "方框 2". */
export function layerNames(layers: Array<Pick<Layer, 'id' | 'type'> & { text?: string }>): Record<string, string> {
  const counts: Record<string, number> = {}
  const names: Record<string, string> = {}
  for (const layer of layers) {
    counts[layer.type] = (counts[layer.type] || 0) + 1
    names[layer.id] = layer.type === 'text' && layer.text ? layer.text.split('\n')[0] : `${LAYER_LABEL[layer.type]} ${counts[layer.type]}`
  }
  return names
}

const COLORS = ['#ef4444', '#f59e0b', '#22c55e', '#3b82f6', '#ffffff', '#111111']

const TOOLS: Array<{ id: Tool; label: string; Icon: typeof Square }> = [
  { id: 'move', label: '移动', Icon: MousePointer2 },
  { id: 'crop', label: '裁剪', Icon: Crop },
  { id: 'rect', label: '方框', Icon: Square },
  { id: 'arrow', label: '箭头', Icon: ArrowUpRight },
  { id: 'text', label: '文字', Icon: Type },
  { id: 'overlay', label: '浮层', Icon: Layers },
  { id: 'image', label: '图片', Icon: ImagePlus },
]

export function normalizeBox(x1: number, y1: number, x2: number, y2: number): Box {
  return { x: Math.min(x1, x2), y: Math.min(y1, y2), w: Math.abs(x2 - x1), h: Math.abs(y2 - y1) }
}

export function annotatedFileName(name: string): string {
  const base = (name || '').replace(/\.[^./\\]+$/, '') || `pasted-${Date.now()}`
  return `${base}.png`
}

function hexAlpha(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

function textBox(ctx: CanvasRenderingContext2D | null, s: Extract<Shape, { type: 'text' }>): Box {
  if (ctx) ctx.font = `600 ${s.size}px system-ui, sans-serif`
  const lines = s.text.split('\n')
  const w = Math.max(...lines.map((line) => (ctx ? ctx.measureText(line).width : [...line].length * s.size * 0.6)))
  return { x: s.x, y: s.y, w, h: lines.length * s.size * 1.25 }
}

function shapeBox(ctx: CanvasRenderingContext2D | null, s: Shape): Box {
  if (s.type === 'arrow') {
    const pad = s.width * 3
    const b = normalizeBox(s.x1, s.y1, s.x2, s.y2)
    return { x: b.x - pad, y: b.y - pad, w: b.w + pad * 2, h: b.h + pad * 2 }
  }
  if (s.type === 'text') return textBox(ctx, s)
  return normalizeBox(s.x, s.y, s.x + s.w, s.y + s.h)
}

function moveShape<T extends Shape>(s: T, dx: number, dy: number): T {
  if (s.type === 'arrow') return { ...s, x1: s.x1 + dx, y1: s.y1 + dy, x2: s.x2 + dx, y2: s.y2 + dy }
  return { ...s, x: (s as Box).x + dx, y: (s as Box).y + dy }
}

function drawShape(ctx: CanvasRenderingContext2D, s: Shape) {
  ctx.save()
  if (s.type === 'rect') {
    ctx.strokeStyle = s.color
    ctx.lineWidth = s.width
    ctx.lineJoin = 'round'
    const b = normalizeBox(s.x, s.y, s.x + s.w, s.y + s.h)
    ctx.strokeRect(b.x, b.y, b.w, b.h)
  } else if (s.type === 'overlay') {
    const b = normalizeBox(s.x, s.y, s.x + s.w, s.y + s.h)
    ctx.fillStyle = hexAlpha(s.color, 0.35)
    ctx.fillRect(b.x, b.y, b.w, b.h)
  } else if (s.type === 'arrow') {
    const angle = Math.atan2(s.y2 - s.y1, s.x2 - s.x1)
    const head = Math.max(12, s.width * 4)
    ctx.strokeStyle = s.color
    ctx.fillStyle = s.color
    ctx.lineWidth = s.width
    ctx.lineCap = 'round'
    ctx.beginPath()
    ctx.moveTo(s.x1, s.y1)
    ctx.lineTo(s.x2 - Math.cos(angle) * head * 0.6, s.y2 - Math.sin(angle) * head * 0.6)
    ctx.stroke()
    ctx.beginPath()
    ctx.moveTo(s.x2, s.y2)
    ctx.lineTo(s.x2 - head * Math.cos(angle - Math.PI / 7), s.y2 - head * Math.sin(angle - Math.PI / 7))
    ctx.lineTo(s.x2 - head * Math.cos(angle + Math.PI / 7), s.y2 - head * Math.sin(angle + Math.PI / 7))
    ctx.closePath()
    ctx.fill()
  } else if (s.type === 'text') {
    ctx.font = `600 ${s.size}px system-ui, sans-serif`
    ctx.textBaseline = 'top'
    ctx.lineJoin = 'round'
    ctx.lineWidth = Math.max(2, s.size / 7)
    // Dark/light outline keeps the label readable on any background.
    ctx.strokeStyle = s.color === '#111111' ? 'rgba(255,255,255,0.85)' : 'rgba(0,0,0,0.7)'
    ctx.fillStyle = s.color
    s.text.split('\n').forEach((line, i) => {
      ctx.strokeText(line, s.x, s.y + i * s.size * 1.25)
      ctx.fillText(line, s.x, s.y + i * s.size * 1.25)
    })
  } else if (s.type === 'image') {
    ctx.drawImage(s.img, s.x, s.y, s.w, s.h)
  }
  ctx.restore()
}

function loadImage(file: Blob): Promise<{ img: HTMLImageElement; url: string }> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => resolve({ img, url })
    img.onerror = () => { URL.revokeObjectURL(url); reject(new Error('image load failed')) }
    img.src = url
  })
}

export default function ImageAnnotator({ file, index = 1, total = 1, onDone }: {
  file: File
  index?: number
  total?: number
  /** Edited (or original) file to upload, or null to drop this image. */
  /**
   * Edited (or original) file, or null to drop this image. `prompt` is the
   * optional note typed in the editor footer, sent together with the image.
   */
  onDone: (result: File | null, prompt?: string) => void
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const urlsRef = useRef<string[]>([])
  const [base, setBase] = useState<HTMLImageElement | null>(null)
  const [loadError, setLoadError] = useState(false)
  const [tool, setTool] = useState<Tool>('rect')
  const [color, setColor] = useState(COLORS[0])
  const [shapes, setShapesState] = useState<Layer[]>([])
  const shapesRef = useRef<Layer[]>([])
  const historyRef = useRef<Layer[][]>([])
  const [historySize, setHistorySize] = useState(0)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [baseHidden, setBaseHidden] = useState(false)
  const [layersOpen, setLayersOpen] = useState(true)
  const setShapesRaw = (next: Layer[]) => { shapesRef.current = next; setShapesState(next) }
  // Every user-visible change goes through here so Ctrl+Z can step back.
  const mutateShapes = (fn: (list: Layer[]) => Layer[]) => {
    historyRef.current.push(shapesRef.current)
    if (historyRef.current.length > 100) historyRef.current.shift()
    setHistorySize(historyRef.current.length)
    setShapesRaw(fn(shapesRef.current))
  }
  const addLayer = (shape: Shape) => {
    const layer = { ...shape, id: newLayerId() } as Layer
    mutateShapes((list) => [...list, layer])
    setSelectedId(layer.id)
  }
  const [draft, setDraft] = useState<Shape | null>(null)
  const [crop, setCrop] = useState<Box | null>(null)
  const [cropDraft, setCropDraft] = useState<Box | null>(null)
  const [textDraft, setTextDraftState] = useState<{ x: number; y: number; left: number; top: number; value: string; editId?: string; color?: string; size: number; scale: number } | null>(null)
  // Mirror of textDraft so commit/blur handlers never act on a stale copy (and
  // never commit twice), plus when it opened so the opening click's focus
  // shuffle can't close it.
  const textDraftRef = useRef<typeof textDraft>(null)
  const textOpenedAtRef = useRef(0)
  const textInputRef = useRef<HTMLTextAreaElement>(null)
  const setTextDraft = (next: typeof textDraft) => {
    textDraftRef.current = next
    setTextDraftState(next)
  }
  const [exporting, setExporting] = useState(false)
  const [prompt, setPrompt] = useState('')
  const promptValue = () => prompt.trim() || undefined
  const dragRef = useRef<
    | { kind: 'draw'; x: number; y: number }
    | { kind: 'move'; id: string; x: number; y: number; orig: Layer }
    | null
  >(null)

  const W = base?.naturalWidth || 1
  const H = base?.naturalHeight || 1
  const strokeWidth = Math.max(3, Math.round(Math.max(W, H) / 320))
  const textSize = Math.max(18, Math.round(Math.max(W, H) / 36))

  useEffect(() => {
    let alive = true
    loadImage(file).then(({ img, url }) => {
      urlsRef.current.push(url)
      if (alive) setBase(img)
    }).catch(() => { if (alive) setLoadError(true) })
    return () => {
      alive = false
      urlsRef.current.forEach((url) => URL.revokeObjectURL(url))
      urlsRef.current = []
    }
  }, [file])

  const drawScene = useCallback((ctx: CanvasRenderingContext2D, list: Array<Shape & { hidden?: boolean }>) => {
    if (!base) return
    if (!baseHidden) ctx.drawImage(base, 0, 0)
    list.forEach((s) => { if (!s.hidden) drawShape(ctx, s) })
  }, [base, baseHidden])

  // Repaint the editing canvas (scene + live draft + crop shade).
  useEffect(() => {
    const canvas = canvasRef.current
    const ctx = canvas?.getContext('2d')
    if (!canvas || !ctx || !base) return
    ctx.clearRect(0, 0, W, H)
    const editingId = textDraft?.editId
    const visible = editingId ? shapes.filter((layer) => layer.id !== editingId) : shapes
    // Text being typed is drawn by the canvas too (same renderer as when it is
    // committed), so it never shifts between editing and not editing. The
    // textarea on top is transparent and only supplies the caret/IME.
    const liveText: Shape | null = textDraft && textDraft.value
      ? { type: 'text', x: textDraft.x, y: textDraft.y, text: textDraft.value, color: textDraft.color || color, size: textDraft.size }
      : null
    const overlay = [draft, liveText].filter(Boolean) as Shape[]
    drawScene(ctx, overlay.length ? [...visible, ...overlay] : visible)
    const selected = shapes.find((layer) => layer.id === selectedId && !layer.hidden)
    if (selected && !draft && !editingId) {
      const b = shapeBox(ctx, selected)
      const pad = strokeWidth
      ctx.save()
      ctx.strokeStyle = '#60a5fa'
      ctx.lineWidth = Math.max(1, strokeWidth / 2)
      ctx.setLineDash([strokeWidth * 2, strokeWidth])
      ctx.strokeRect(b.x - pad, b.y - pad, b.w + pad * 2, b.h + pad * 2)
      ctx.restore()
    }
    const area = cropDraft || crop
    if (area && area.w > 0 && area.h > 0) {
      ctx.save()
      ctx.fillStyle = 'rgba(0,0,0,0.55)'
      ctx.beginPath()
      ctx.rect(0, 0, W, H)
      ctx.rect(area.x, area.y, area.w, area.h)
      ctx.fill('evenodd')
      ctx.strokeStyle = '#ffffff'
      ctx.setLineDash([strokeWidth * 2, strokeWidth * 2])
      ctx.lineWidth = Math.max(1, strokeWidth / 2)
      ctx.strokeRect(area.x, area.y, area.w, area.h)
      ctx.restore()
    }
  }, [base, shapes, draft, crop, cropDraft, drawScene, W, H, strokeWidth, selectedId, textDraft, color])

  const toImagePoint = (event: { clientX: number; clientY: number }) => {
    const canvas = canvasRef.current!
    const rect = canvas.getBoundingClientRect()
    const scale = rect.width / canvas.width
    return {
      x: (event.clientX - rect.left) / scale,
      y: (event.clientY - rect.top) / scale,
      left: event.clientX - rect.left,
      top: event.clientY - rect.top,
    }
  }

  const addImageLayer = useCallback(async (blob: Blob) => {
    try {
      const { img, url } = await loadImage(blob)
      urlsRef.current.push(url)
      const scale = Math.min(1, (W * 0.4) / img.naturalWidth, (H * 0.4) / img.naturalHeight)
      const w = Math.round(img.naturalWidth * scale)
      const h = Math.round(img.naturalHeight * scale)
      addLayer({ type: 'image', img, x: Math.round((W - w) / 2), y: Math.round((H - h) / 2), w, h })
      setTool('move')
    } catch { /* unreadable image: ignore */ }
  }, [W, H])

  const commitText = () => {
    const current = textDraftRef.current
    if (!current) return
    setTextDraft(null)
    const value = current.value.trim()
    if (current.editId) {
      const id = current.editId
      if (!value) { deleteLayer(id); return }
      mutateShapes((list) => list.map((layer) => (layer.id === id && layer.type === 'text' ? { ...layer, text: value } : layer)))
      setSelectedId(id)
      return
    }
    if (value) addLayer({ type: 'text', x: current.x, y: current.y, text: value, color, size: textSize })
  }

  // Topmost visible layer under an image-space point.
  const hitLayer = (p: { x: number; y: number }, only?: Shape['type']): Layer | null => {
    const ctx = canvasRef.current?.getContext('2d') || null
    for (let i = shapesRef.current.length - 1; i >= 0; i -= 1) {
      const layer = shapesRef.current[i]
      if (layer.hidden || (only && layer.type !== only)) continue
      const b = shapeBox(ctx, layer)
      const slop = Math.max(4, strokeWidth)
      if (p.x >= b.x - slop && p.x <= b.x + b.w + slop && p.y >= b.y - slop && p.y <= b.y + b.h + slop) return layer
    }
    return null
  }

  const startMove = (event: React.PointerEvent<HTMLCanvasElement>, layer: Layer, p: { x: number; y: number }) => {
    setSelectedId(layer.id)
    historyRef.current.push(shapesRef.current)
    setHistorySize(historyRef.current.length)
    dragRef.current = { kind: 'move', id: layer.id, x: p.x, y: p.y, orig: layer }
    event.currentTarget.setPointerCapture?.(event.pointerId)
  }

  // Double-click a text layer to edit it in place.
  const onDoubleClick = (event: React.MouseEvent<HTMLCanvasElement>) => {
    const p = toImagePoint(event)
    const layer = hitLayer(p, 'text')
    if (!layer || layer.type !== 'text') return
    event.preventDefault()
    // A dblclick is two pointerdowns; drop the empty-move history entry they left.
    dragRef.current = null
    const canvas = canvasRef.current!
    const scale = canvas.getBoundingClientRect().width / canvas.width
    textOpenedAtRef.current = Date.now()
    setSelectedId(layer.id)
    setTextDraft({ x: layer.x, y: layer.y, left: layer.x * scale, top: layer.y * scale, value: layer.text, editId: layer.id, color: layer.color, size: layer.size, scale })
  }

  // Focus the box after it has mounted and the opening click is fully handled.
  const textDraftOpen = Boolean(textDraft)
  useEffect(() => {
    if (!textDraftOpen) return
    const frame = requestAnimationFrame(() => textInputRef.current?.focus())
    return () => cancelAnimationFrame(frame)
  }, [textDraftOpen])

  const onPointerDown = (event: React.PointerEvent<HTMLCanvasElement>) => {
    if (!base || event.button !== 0) return
    if (textDraftRef.current) { commitText(); return }
    const p = toImagePoint(event)
    if (tool === 'text') {
      // Clicking existing text selects (and drags) it instead of starting a
      // new one; double-click edits it.
      const existing = hitLayer(p, 'text')
      if (existing) { startMove(event, existing, p); return }
      // The click that opens the box would otherwise move focus to <body>
      // right after the textarea mounts, blur it, and close it empty.
      event.preventDefault()
      textOpenedAtRef.current = Date.now()
      const canvas = canvasRef.current!
      const scale = canvas.getBoundingClientRect().width / canvas.width || 1
      setTextDraft({ x: p.x, y: p.y, left: p.left, top: p.top, value: '', size: textSize, scale })
      return
    }
    if (tool === 'image') { fileInputRef.current?.click(); return }
    if (tool === 'move') {
      const layer = hitLayer(p)
      if (layer) startMove(event, layer, p)
      else setSelectedId(null)
      return
    }
    dragRef.current = { kind: 'draw', x: p.x, y: p.y }
    event.currentTarget.setPointerCapture?.(event.pointerId)
  }

  const onPointerMove = (event: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current
    if (!drag) return
    const p = toImagePoint(event)
    if (drag.kind === 'move') {
      const moved = moveShape(drag.orig, p.x - drag.x, p.y - drag.y)
      setShapesRaw(shapesRef.current.map((layer) => (layer.id === drag.id ? moved : layer)))
      return
    }
    if (tool === 'crop') { setCropDraft(normalizeBox(drag.x, drag.y, p.x, p.y)); return }
    if (tool === 'arrow') setDraft({ type: 'arrow', x1: drag.x, y1: drag.y, x2: p.x, y2: p.y, color, width: strokeWidth })
    else if (tool === 'rect' || tool === 'overlay') setDraft({ type: tool, x: drag.x, y: drag.y, w: p.x - drag.x, h: p.y - drag.y, color, width: strokeWidth })
  }

  const onPointerUp = () => {
    const drag = dragRef.current
    dragRef.current = null
    if (drag?.kind === 'move') {
      const now = shapesRef.current.find((layer) => layer.id === drag.id)
      if (now && JSON.stringify(moveShape(now, 0, 0)) === JSON.stringify(drag.orig)) {
        historyRef.current.pop()
        setHistorySize(historyRef.current.length)
      }
      return
    }
    if (!drag) return
    if (tool === 'crop') {
      if (cropDraft && cropDraft.w > 8 && cropDraft.h > 8) {
        const x = Math.max(0, Math.round(cropDraft.x))
        const y = Math.max(0, Math.round(cropDraft.y))
        setCrop({ x, y, w: Math.min(W - x, Math.round(cropDraft.w)), h: Math.min(H - y, Math.round(cropDraft.h)) })
      }
      setCropDraft(null)
      return
    }
    if (draft) {
      const tiny = draft.type === 'arrow'
        ? Math.hypot(draft.x2 - draft.x1, draft.y2 - draft.y1) < 6
        : draft.type !== 'text' && Math.abs((draft as Box).w) < 4 && Math.abs((draft as Box).h) < 4
      if (!tiny) addLayer(draft)
    }
    setDraft(null)
  }

  const undo = () => {
    const previous = historyRef.current.pop()
    setHistorySize(historyRef.current.length)
    if (previous) {
      setShapesRaw(previous)
      if (selectedId && !previous.some((layer) => layer.id === selectedId)) setSelectedId(null)
    } else if (crop) setCrop(null)
  }
  const toggleLayerHidden = (id: string) => mutateShapes((list) => list.map((layer) => (layer.id === id ? { ...layer, hidden: !layer.hidden } : layer)))
  const deleteLayer = (id: string) => {
    mutateShapes((list) => list.filter((layer) => layer.id !== id))
    if (selectedId === id) setSelectedId(null)
  }
  // delta +1 = one step closer to the top of the stack.
  const moveLayer = (id: string, delta: 1 | -1) => mutateShapes((list) => {
    const from = list.findIndex((layer) => layer.id === id)
    const to = from + delta
    if (from < 0 || to < 0 || to >= list.length) return list
    const next = [...list]
    ;[next[from], next[to]] = [next[to], next[from]]
    return next
  })

  const finish = async () => {
    if (!base || exporting) return
    if (!shapes.length && !crop && !baseHidden) { onDone(file, promptValue()); return }
    setExporting(true)
    try {
      const area = crop || { x: 0, y: 0, w: W, h: H }
      const out = document.createElement('canvas')
      out.width = area.w
      out.height = area.h
      const ctx = out.getContext('2d')
      if (!ctx) { onDone(file, promptValue()); return }
      ctx.translate(-area.x, -area.y)
      drawScene(ctx, shapes)
      const blob = await new Promise<Blob | null>((resolve) => out.toBlob(resolve, 'image/png'))
      onDone(blob ? new File([blob], annotatedFileName(file.name), { type: 'image/png' }) : file, promptValue())
    } finally {
      setExporting(false)
    }
  }

  // Shortcuts + pasting another image into the editor adds it as a layer.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (textDraftRef.current) return
      const target = event.target as HTMLElement | null
      const typing = !!target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
      if (event.key === 'Escape') {
        event.preventDefault()
        // Esc in the prompt field just leaves the field; a second Esc cancels.
        if (typing) (target as HTMLElement).blur()
        else onDone(null)
      }
      else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z' && !typing) { event.preventDefault(); undo() }
      else if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') { event.preventDefault(); void finish() }
      else if ((event.key === 'Delete' || event.key === 'Backspace') && selectedId && !typing) { event.preventDefault(); event.stopPropagation(); deleteLayer(selectedId) }
    }
    const onPaste = (event: ClipboardEvent) => {
      const image = Array.from(event.clipboardData?.files || []).find((f) => f.type.startsWith('image/'))
      if (!image) return
      event.preventDefault()
      event.stopPropagation()
      void addImageLayer(image)
    }
    window.addEventListener('keydown', onKey, true)
    window.addEventListener('paste', onPaste, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      window.removeEventListener('paste', onPaste, true)
    }
  })

  const cursor = tool === 'move' ? 'cursor-move' : tool === 'text' ? 'cursor-text' : tool === 'image' ? 'cursor-copy' : 'cursor-crosshair'

  return createPortal(
    <div
      data-id="image-annotator"
      className="fixed inset-0 z-[10050] flex items-center justify-center bg-black/70 p-4"
      onPointerDown={(event) => event.stopPropagation()}
      onMouseDown={(event) => event.stopPropagation()}
    >
      <div className="flex h-[min(88vh,860px)] w-[min(1180px,96vw)] flex-col overflow-hidden rounded-2xl border border-white/[0.12] bg-[#141416] shadow-2xl">
        <div data-id="image-annotator-header" className="flex shrink-0 items-center justify-between border-b border-white/[0.06] px-4 py-2.5">
          <div className="text-sm font-semibold text-zinc-100">
            编辑图片
            {total > 1 ? <span className="ml-2 font-mono text-xs text-zinc-500">{index}/{total}</span> : null}
            <span className="ml-3 text-xs font-normal text-zinc-500">{file.name}{base ? ` · ${W}×${H}` : ''}</span>
          </div>
          <button type="button" data-id="image-annotator-close" onClick={() => onDone(null)} className="rounded-lg p-1.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200" title="取消（Esc）">
            <X className="h-4 w-4" />
          </button>
        </div>

        <div data-id="image-annotator-toolbar" className="flex shrink-0 flex-wrap items-center gap-1 border-b border-white/[0.06] px-3 py-2">
          {TOOLS.map(({ id, label, Icon }) => (
            <button
              key={id}
              type="button"
              data-id={`image-annotator-tool-${id}`}
              aria-pressed={tool === id}
              onClick={() => { commitText(); if (id === 'image') fileInputRef.current?.click(); else setTool(id) }}
              className={`flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-xs transition-colors ${tool === id ? 'bg-blue-500/20 text-blue-200' : 'text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200'}`}
            >
              <Icon className="h-4 w-4" />{label}
            </button>
          ))}
          <span className="mx-2 h-5 w-px bg-white/[0.08]" />
          {COLORS.map((c) => (
            <button
              key={c}
              type="button"
              data-id={`image-annotator-color-${c.slice(1)}`}
              aria-label={c}
              aria-pressed={color === c}
              onClick={() => setColor(c)}
              className={`h-6 w-6 rounded-full border-2 ${color === c ? 'border-white' : 'border-white/20'}`}
              style={{ background: c }}
            />
          ))}
          <span className="mx-2 h-5 w-px bg-white/[0.08]" />
          <button type="button" data-id="image-annotator-undo" onClick={undo} disabled={!historySize && !crop} className="flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-xs text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-30" title="撤销（Ctrl+Z）">
            <Undo2 className="h-4 w-4" />撤销
          </button>
          {crop ? (
            <button type="button" data-id="image-annotator-crop-reset" onClick={() => setCrop(null)} className="h-8 rounded-lg px-2.5 text-xs text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200">
              取消裁剪
            </button>
          ) : null}
          <span className="ml-auto text-[11px] text-zinc-600">Ctrl+V 粘贴图片作为图层 · 点选元素后拖动 · 双击文字编辑 · Delete 删除选中</span>
          <button
            type="button"
            data-id="image-annotator-layers-toggle"
            aria-pressed={layersOpen}
            onClick={() => setLayersOpen((open) => !open)}
            className={`ml-2 flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-xs transition-colors ${layersOpen ? 'bg-white/[0.08] text-zinc-200' : 'text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200'}`}
            title="图层面板"
          >
            <PanelRight className="h-4 w-4" />图层
          </button>
        </div>

        <div data-id="image-annotator-body" className="flex min-h-0 flex-1">
        <div data-id="image-annotator-stage" className="relative flex min-h-0 min-w-0 flex-1 items-center justify-center overflow-auto bg-[#0b0b0d] p-4">
          {loadError ? (
            <div className="text-sm text-red-400">图片读取失败</div>
          ) : !base ? (
            <div className="text-sm text-zinc-500">加载中…</div>
          ) : (
            <div className="relative inline-block max-h-full max-w-full">
              <canvas
                ref={canvasRef}
                data-id="image-annotator-canvas"
                width={W}
                height={H}
                onPointerDown={onPointerDown}
                onMouseDown={(event) => event.preventDefault()}
                onPointerMove={onPointerMove}
                onPointerUp={onPointerUp}
                onPointerCancel={onPointerUp}
                onDoubleClick={onDoubleClick}
                className={`block max-h-[calc(88vh-170px)] max-w-full touch-none select-none rounded ${cursor}`}
                style={{ background: 'repeating-conic-gradient(#222 0% 25%, #2a2a2a 0% 50%) 50% / 16px 16px' }}
              />
              {textDraft ? (
                <textarea
                  ref={textInputRef}
                  data-id="image-annotator-text-input"
                  value={textDraft.value}
                  onChange={(event) => {
                    const current = textDraftRef.current
                    if (current) setTextDraft({ ...current, value: event.target.value })
                  }}
                  onPointerDown={(event) => event.stopPropagation()}
                  onKeyDown={(event) => {
                    event.stopPropagation()
                    if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); commitText() }
                    if (event.key === 'Escape') { event.preventDefault(); setTextDraft(null) }
                  }}
                  onBlur={() => {
                    // The opening click can still bounce focus once; take it back.
                    if (Date.now() - textOpenedAtRef.current < 400) {
                      requestAnimationFrame(() => textInputRef.current?.focus())
                      return
                    }
                    commitText()
                  }}
                  placeholder="输入文字，Enter 确认"
                  wrap="off"
                  rows={Math.max(1, textDraft.value.split('\n').length)}
                  // Same metrics as drawShape('text') at the current display
                  // scale, so the text doesn't jump in size when it's committed.
                  className="absolute m-0 resize-none overflow-hidden whitespace-pre border-0 bg-transparent p-0 outline outline-1 outline-dashed outline-blue-400/70 placeholder:text-white/40"
                  style={(() => {
                    const fontPx = textDraft.size * textDraft.scale
                    const longest = Math.max(4, ...textDraft.value.split('\n').map((line) => [...line].length))
                    return {
                      left: textDraft.left,
                      // CSS centres the glyphs in a 1.25 line box (half-leading on
                      // top); canvas draws from the em top. Shift up so the caret
                      // lines up with the canvas-rendered text.
                      top: textDraft.top - fontPx * 0.125,
                      color: 'transparent',
                      caretColor: textDraft.color || color,
                      fontFamily: 'system-ui, sans-serif',
                      fontWeight: 600,
                      fontSize: `${fontPx}px`,
                      lineHeight: 1.25,
                      width: `calc(${longest}ch + ${fontPx}px)`,
                      minWidth: `${Math.max(120, fontPx * 4)}px`,
                    }
                  })()}
                />
              ) : null}
            </div>
          )}
        </div>
        {layersOpen ? (
          <aside data-id="image-annotator-layers" className="flex w-60 shrink-0 flex-col border-l border-white/[0.06] bg-[#17181b]">
            <div className="flex h-9 shrink-0 items-center justify-between border-b border-white/[0.06] px-3">
              <span className="text-xs font-semibold text-zinc-200">图层 <span className="font-normal text-zinc-500">{shapes.length + 1}</span></span>
              <button type="button" data-id="image-annotator-layers-close" onClick={() => setLayersOpen(false)} className="rounded p-1 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200" title="收起">
                <Minus className="h-3.5 w-3.5" />
              </button>
            </div>
            <div data-id="image-annotator-layer-list" className="min-h-0 flex-1 overflow-y-auto py-1">
              {(() => {
                const names = layerNames(shapes.map((layer) => ({ id: layer.id, type: layer.type, text: layer.type === 'text' ? layer.text : undefined })))
                const rowClass = (active: boolean) => `group flex w-full cursor-pointer items-center gap-2 px-2 py-1.5 text-left ${active ? 'bg-sky-600 text-white' : 'text-zinc-300 hover:bg-white/[0.05]'}`
                const thumbStyle = { background: 'repeating-conic-gradient(#3a3a3a 0% 25%, #2a2a2a 0% 50%) 50% / 8px 8px' }
                return (
                  <>
                    {[...shapes].reverse().map((layer, reversedIndex) => {
                      const stackIndex = shapes.length - 1 - reversedIndex
                      const active = layer.id === selectedId
                      const Icon = layer.type === 'rect' ? Square : layer.type === 'arrow' ? ArrowUpRight : layer.type === 'text' ? Type : layer.type === 'overlay' ? Layers : ImagePlus
                      const swatch = layer.type === 'image' ? undefined : (layer as { color: string }).color
                      return (
                        <div
                          key={layer.id}
                          role="option"
                          aria-selected={active}
                          data-id={`image-annotator-layer-${layer.id}`}
                          onClick={() => { setSelectedId(layer.id); setTool('move') }}
                          className={rowClass(active)}
                        >
                          <span className="grid h-8 w-8 shrink-0 place-items-center overflow-hidden rounded border border-white/10" style={thumbStyle}>
                            {layer.type === 'image'
                              ? <img src={layer.img.src} alt="" className="h-full w-full object-contain" />
                              : <Icon className="h-4 w-4" style={{ color: swatch }} />}
                          </span>
                          <span className={`min-w-0 flex-1 ${layer.hidden ? 'opacity-40' : ''}`}>
                            <span className="block truncate text-xs font-semibold">{names[layer.id]}</span>
                            <span className={`block text-[10px] italic ${active ? 'text-sky-100' : 'text-zinc-500'}`}>{LAYER_KIND[layer.type]}</span>
                          </span>
                          <span className="flex shrink-0 items-center opacity-0 group-hover:opacity-100 group-aria-selected:opacity-100">
                            <button type="button" data-id={`image-annotator-layer-up-${layer.id}`} disabled={stackIndex === shapes.length - 1} onClick={(event) => { event.stopPropagation(); moveLayer(layer.id, 1) }} className="rounded p-0.5 hover:bg-black/20 disabled:opacity-30" title="上移一层"><ChevronUp className="h-3.5 w-3.5" /></button>
                            <button type="button" data-id={`image-annotator-layer-down-${layer.id}`} disabled={stackIndex === 0} onClick={(event) => { event.stopPropagation(); moveLayer(layer.id, -1) }} className="rounded p-0.5 hover:bg-black/20 disabled:opacity-30" title="下移一层"><ChevronDown className="h-3.5 w-3.5" /></button>
                            <button type="button" data-id={`image-annotator-layer-delete-${layer.id}`} onClick={(event) => { event.stopPropagation(); deleteLayer(layer.id) }} className="rounded p-0.5 hover:bg-black/20" title="删除图层"><Trash2 className="h-3.5 w-3.5" /></button>
                          </span>
                          <button type="button" data-id={`image-annotator-layer-visibility-${layer.id}`} aria-pressed={!layer.hidden} onClick={(event) => { event.stopPropagation(); toggleLayerHidden(layer.id) }} className="shrink-0 rounded p-1 hover:bg-black/20" title={layer.hidden ? '显示' : '隐藏'}>
                            {layer.hidden ? <EyeOff className="h-4 w-4 opacity-60" /> : <Eye className="h-4 w-4" />}
                          </button>
                        </div>
                      )
                    })}
                    <div
                      role="option"
                      aria-selected={selectedId === null}
                      data-id="image-annotator-layer-base"
                      onClick={() => setSelectedId(null)}
                      className={rowClass(selectedId === null)}
                    >
                      <span className="h-8 w-8 shrink-0 overflow-hidden rounded border border-white/10" style={thumbStyle}>
                        {base ? <img src={base.src} alt="" className="h-full w-full object-contain" /> : null}
                      </span>
                      <span className={`min-w-0 flex-1 ${baseHidden ? 'opacity-40' : ''}`}>
                        <span className="block truncate text-xs font-semibold">背景</span>
                        <span className={`block text-[10px] italic ${selectedId === null ? 'text-sky-100' : 'text-zinc-500'}`}>图片 · 锁定</span>
                      </span>
                      <button type="button" data-id="image-annotator-layer-visibility-base" aria-pressed={!baseHidden} onClick={(event) => { event.stopPropagation(); setBaseHidden((hidden) => !hidden) }} className="shrink-0 rounded p-1 hover:bg-black/20" title={baseHidden ? '显示' : '隐藏'}>
                        {baseHidden ? <EyeOff className="h-4 w-4 opacity-60" /> : <Eye className="h-4 w-4" />}
                      </button>
                    </div>
                  </>
                )
              })()}
            </div>
          </aside>
        ) : null}
        </div>

        <div data-id="image-annotator-footer" className="flex shrink-0 items-end gap-2 border-t border-white/[0.06] px-4 py-2.5">
          <textarea
            data-id="image-annotator-prompt"
            rows={1}
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return
              // Enter sends (like the composers); Shift+Enter for a new line.
              if (event.key === 'Enter' && !event.shiftKey && !event.ctrlKey && !event.metaKey) { event.preventDefault(); void finish() }
            }}
            placeholder="附带说明（可选），和图片一起发送 · Enter 发送，Shift+Enter 换行"
            className="max-h-28 min-h-[36px] min-w-0 flex-1 resize-none rounded-lg border border-white/[0.08] bg-[#0d0d0f] px-3 py-2 text-sm leading-5 text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-blue-400/50"
            style={{ height: `${Math.min(112, 20 * Math.max(1, prompt.split('\n').length) + 16)}px` }}
          />
          <button type="button" data-id="image-annotator-cancel" onClick={() => onDone(null)} className="shrink-0 rounded-lg px-3 py-1.5 text-sm text-zinc-400 hover:text-zinc-200">
            取消
          </button>
          <button type="button" data-id="image-annotator-original" onClick={() => onDone(file, promptValue())} className="shrink-0 rounded-lg border border-white/[0.1] px-3 py-1.5 text-sm text-zinc-300 hover:bg-white/[0.06]">
            发送原图
          </button>
          <button type="button" data-id="image-annotator-done" onClick={() => void finish()} disabled={!base || exporting} className="shrink-0 rounded-lg bg-blue-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-blue-500 disabled:opacity-40" title="Ctrl+Enter">
            {exporting ? '处理中…' : prompt.trim() ? '发送' : '完成并发送'}
          </button>
        </div>
      </div>
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        className="hidden"
        data-id="image-annotator-file-input"
        onChange={(event) => {
          const picked = event.target.files?.[0]
          event.target.value = ''
          if (picked) void addImageLayer(picked)
        }}
      />
    </div>,
    document.body,
  )
}
