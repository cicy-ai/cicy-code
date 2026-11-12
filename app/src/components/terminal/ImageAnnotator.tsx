// SPDX-License-Identifier: Apache-2.0
//
// ImageAnnotator — opens when an image is pasted into a terminal or a card
// composer. The pasted image sits on a canvas whose size the user controls:
// shrink it to crop, grow it to get room for notes. On top go layers — boxes,
// ellipses, arrows, freehand strokes, text, numbered markers, highlights,
// mosaic and extra images — that can be moved, resized, restacked and undone.
// Pure canvas, no dependencies. Geometry and rendering live in
// imageAnnotatorModel.ts; this file is the interaction and the chrome.

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  ArrowUpRight, ChevronDown, ChevronUp, Circle, Copy, Crop, Eye, EyeOff, Frame, Grid3x3, Highlighter, ImagePlus,
  Keyboard, ListOrdered, Maximize, Minus, MousePointer2, PanelRight, Pencil, Plus, Redo2, Square, Trash2, Type, Undo2, X,
} from 'lucide-react'
import {
  BOX_HANDLES, CANVAS_MIN, LAYER_KIND, annotatedFileName, clampCanvas, contentBounds, expandToRatio, fitView, handlePoint,
  isBoxShape, layerNames, moveShape, normalizeBox, padBox, renderDocument, resizeBox, resizeCanvasCentered, sameBox, shapeBox,
  type Box, type Doc, type Handle, type Layer, type Point, type Shape,
} from './imageAnnotatorModel'

export { annotatedFileName, layerNames, normalizeBox }
export type { Box }

type Tool = 'move' | 'canvas' | 'crop' | 'rect' | 'ellipse' | 'arrow' | 'pen' | 'text' | 'number' | 'overlay' | 'mosaic' | 'image'
type View = { zoom: number; x: number; y: number }

let layerSeq = 0
const newLayerId = () => `layer-${Date.now().toString(36)}-${(layerSeq += 1)}`

const COLORS = ['#ef4444', '#f59e0b', '#22c55e', '#3b82f6', '#ffffff', '#111111']
const SIZE_SCALE = [0.6, 1, 1.8]
const SIZE_LABEL = ['小', '中', '大']
const ZOOM_MIN = 0.05
const ZOOM_MAX = 16

const TOOL_GROUPS: Array<Array<{ id: Tool; label: string; key: string; Icon: typeof Square }>> = [
  [
    { id: 'move', label: '移动', key: 'V', Icon: MousePointer2 },
    { id: 'canvas', label: '画布', key: 'F', Icon: Frame },
    { id: 'crop', label: '裁剪', key: 'C', Icon: Crop },
  ],
  [
    { id: 'rect', label: '方框', key: 'R', Icon: Square },
    { id: 'ellipse', label: '椭圆', key: 'O', Icon: Circle },
    { id: 'arrow', label: '箭头', key: 'A', Icon: ArrowUpRight },
    { id: 'pen', label: '画笔', key: 'P', Icon: Pencil },
    { id: 'text', label: '文字', key: 'T', Icon: Type },
    { id: 'number', label: '序号', key: 'N', Icon: ListOrdered },
  ],
  [
    { id: 'overlay', label: '高亮', key: 'H', Icon: Highlighter },
    { id: 'mosaic', label: '马赛克', key: 'M', Icon: Grid3x3 },
    { id: 'image', label: '图片', key: 'I', Icon: ImagePlus },
  ],
]
const TOOL_BY_KEY: Record<string, Tool> = Object.fromEntries(TOOL_GROUPS.flat().map((t) => [t.key.toLowerCase(), t.id]))
const LAYER_ICON: Record<Shape['type'], typeof Square> = {
  rect: Square, ellipse: Circle, overlay: Highlighter, mosaic: Grid3x3, arrow: ArrowUpRight, pen: Pencil, text: Type, number: ListOrdered, image: ImagePlus,
}

const TOOL_HINT: Record<Tool, string> = {
  move: '点选元素后拖动，拖控制点缩放 · 拖空白处平移 · 滚轮缩放',
  canvas: '拖画布四周的控制点改变大小：向外留白，向内裁切 · Shift 等比',
  crop: '拖出要保留的区域',
  rect: '拖动画方框',
  ellipse: '拖动画椭圆',
  arrow: '从起点拖到要指向的位置',
  pen: '按住拖动，随手画',
  text: '点击空白处输入文字，Enter 确认 · 双击已有文字可修改',
  number: '依次点击放置序号，自动递增',
  overlay: '拖出一块半透明色块，用来高亮重点',
  mosaic: '拖出要遮挡的区域，适合打码密钥、隐私',
  image: '选择图片作为新图层，也可以直接 Ctrl+V 粘贴或拖入',
}

const SHORTCUTS: Array<[string, string]> = [
  ['Enter', '发送（焦点在说明框时）'],
  ['Ctrl + Enter', '任意位置发送'],
  ['Ctrl + Z / Ctrl + Shift + Z', '撤销 / 重做'],
  ['Ctrl + V', '粘贴图片为新图层'],
  ['Ctrl + D', '复制选中图层'],
  ['Delete', '删除选中图层'],
  ['方向键', '微调选中图层（Shift ×10）'],
  ['[ / ]', '选中图层下移 / 上移一层'],
  ['滚轮', '以鼠标为中心缩放'],
  ['空格 + 拖动', '平移画面'],
  ['Ctrl + 0 / Ctrl + 1', '适应窗口 / 100%'],
  ['Esc', '取消选中 → 关闭编辑器'],
]

const HANDLE_CURSOR: Record<Handle, string> = {
  nw: 'nwse-resize', se: 'nwse-resize', ne: 'nesw-resize', sw: 'nesw-resize', n: 'ns-resize', s: 'ns-resize', e: 'ew-resize', w: 'ew-resize', p1: 'crosshair', p2: 'crosshair',
}

type Drag =
  | { kind: 'draw'; x: number; y: number }
  | { kind: 'move'; id: string; x: number; y: number; orig: Layer; snapshot: Doc; changed: boolean }
  | { kind: 'resize'; id: string; handle: Handle; x: number; y: number; orig: Layer; snapshot: Doc; changed: boolean }
  | { kind: 'canvas'; handle: Handle; x: number; y: number; orig: Box; snapshot: Doc; changed: boolean }
  | { kind: 'pan'; cx: number; cy: number; vx: number; vy: number }

function loadImage(file: Blob): Promise<{ img: HTMLImageElement; url: string }> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => resolve({ img, url })
    img.onerror = () => { URL.revokeObjectURL(url); reject(new Error('image load failed')) }
    img.src = url
  })
}

/** Number box that only commits a valid value on Enter or blur. */
function NumField({ value, onCommit, dataId, label }: { value: number; onCommit: (n: number) => void; dataId: string; label: string }) {
  const [text, setText] = useState(String(value))
  useEffect(() => { setText(String(value)) }, [value])
  const commit = () => {
    const n = Math.round(Number(text))
    if (Number.isFinite(n) && n > 0 && n !== value) onCommit(n)
    else setText(String(value))
  }
  return (
    <label className="flex h-7 items-center overflow-hidden rounded-md border border-white/[0.08] bg-[#0d0d0f] text-xs focus-within:border-blue-400/50">
      <span className="pl-2 text-zinc-500">{label}</span>
      <input
        data-id={dataId}
        inputMode="numeric"
        value={text}
        onChange={(event) => setText(event.target.value.replace(/[^\d]/g, ''))}
        onBlur={commit}
        onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); commit(); event.currentTarget.blur() } }}
        className="w-14 bg-transparent px-1.5 py-1 text-right font-mono text-zinc-200 outline-none"
      />
    </label>
  )
}

const chipClass = 'h-7 rounded-md px-2 text-xs text-zinc-300 transition-colors hover:bg-white/[0.08] hover:text-white disabled:opacity-30 disabled:hover:bg-transparent'
const iconBtnClass = 'grid h-8 w-8 place-items-center rounded-lg text-zinc-400 transition-colors hover:bg-white/[0.06] hover:text-zinc-200 disabled:opacity-30 disabled:hover:bg-transparent'

export default function ImageAnnotator({ file, index = 1, total = 1, onDone }: {
  file: File
  index?: number
  total?: number
  /**
   * Edited (or original) file, or null to drop this image. `prompt` is the
   * optional note typed in the editor footer, sent together with the image.
   */
  onDone: (result: File | null, prompt?: string) => void
}) {
  const rootRef = useRef<HTMLDivElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const docCanvasRef = useRef<HTMLCanvasElement | null>(null)
  const checkerRef = useRef<CanvasPattern | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const promptRef = useRef<HTMLTextAreaElement>(null)
  const urlsRef = useRef<string[]>([])
  const [base, setBase] = useState<HTMLImageElement | null>(null)
  const [loadError, setLoadError] = useState(false)
  const [tool, setTool] = useState<Tool>('rect')
  const [color, setColor] = useState(COLORS[0])
  const [sizeLevel, setSizeLevel] = useState(1)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [layersOpen, setLayersOpen] = useState(true)
  const [helpOpen, setHelpOpen] = useState(false)

  // The document (layers + canvas) and its undo/redo history.
  const [doc, setDocState] = useState<Doc>({ shapes: [], canvas: { x: 0, y: 0, w: 1, h: 1 }, bg: null, baseHidden: false })
  const docRef = useRef(doc)
  const undoRef = useRef<Doc[]>([])
  const redoRef = useRef<Doc[]>([])
  const [history, setHistory] = useState({ undo: 0, redo: 0 })
  const syncHistory = () => setHistory({ undo: undoRef.current.length, redo: redoRef.current.length })
  const setDocRaw = (next: Doc) => { docRef.current = next; setDocState(next) }
  const pushUndo = (snapshot: Doc) => {
    undoRef.current.push(snapshot)
    if (undoRef.current.length > 100) undoRef.current.shift()
    redoRef.current = []
    syncHistory()
  }
  // Every user-visible change goes through here so Ctrl+Z can step back.
  const commit = (fn: (current: Doc) => Doc) => {
    const next = fn(docRef.current)
    if (next === docRef.current) return
    pushUndo(docRef.current)
    setDocRaw(next)
  }
  const mutateShapes = (fn: (list: Layer[]) => Layer[]) => commit((current) => {
    const shapes = fn(current.shapes)
    return shapes === current.shapes ? current : { ...current, shapes }
  })
  const addLayer = (shape: Shape) => {
    const layer = { ...shape, id: newLayerId() } as Layer
    mutateShapes((list) => [...list, layer])
    setSelectedId(layer.id)
  }
  const shapes = doc.shapes

  const [draft, setDraft] = useState<Shape | null>(null)
  const [cropDraft, setCropDraft] = useState<Box | null>(null)
  const [textDraft, setTextDraftState] = useState<{ x: number; y: number; value: string; editId?: string; color?: string; size: number } | null>(null)
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
  const dragRef = useRef<Drag | null>(null)
  const spaceRef = useRef(false)
  const [hoverCursor, setHoverCursor] = useState<string | null>(null)
  const [escArmed, setEscArmed] = useState(false)

  // View: screen = world * zoom + (x, y), in CSS pixels of the stage.
  const [view, setViewState] = useState<View>({ zoom: 1, x: 0, y: 0 })
  const viewRef = useRef(view)
  const setView = (next: View) => { viewRef.current = next; setViewState(next) }
  // Keep the canvas fitted to the stage until the user zooms or pans.
  const autoFitRef = useRef(true)
  const [stage, setStage] = useState({ w: 0, h: 0 })

  const W = base?.naturalWidth || 1
  const H = base?.naturalHeight || 1
  const strokeBase = Math.max(3, Math.round(Math.max(W, H) / 320))
  const textBase = Math.max(18, Math.round(Math.max(W, H) / 36))
  const strokeFor = (level: number) => Math.max(2, Math.round(strokeBase * SIZE_SCALE[level]))
  const textFor = (level: number) => Math.max(10, Math.round(textBase * SIZE_SCALE[level]))
  const blockFor = (level: number) => Math.max(6, Math.round(strokeBase * 3.5 * SIZE_SCALE[level]))
  const strokeWidth = strokeFor(sizeLevel)
  const textSize = textFor(sizeLevel)
  const imageBox: Box = { x: 0, y: 0, w: W, h: H }
  const dirty = shapes.length > 0 || !sameBox(doc.canvas, imageBox) || doc.baseHidden || Boolean(doc.bg)
  const selected = shapes.find((layer) => layer.id === selectedId) || null

  useEffect(() => {
    let alive = true
    loadImage(file).then(({ img, url }) => {
      urlsRef.current.push(url)
      if (!alive) return
      setDocRaw({ shapes: [], canvas: { x: 0, y: 0, w: img.naturalWidth || 1, h: img.naturalHeight || 1 }, bg: null, baseHidden: false })
      setBase(img)
    }).catch(() => { if (alive) setLoadError(true) })
    return () => {
      alive = false
      urlsRef.current.forEach((url) => URL.revokeObjectURL(url))
      urlsRef.current = []
    }
  }, [file])

  // The note field is where most people go first: focus it as soon as the
  // editor opens, and once more in case the terminal grabs focus back.
  useEffect(() => {
    const focusPrompt = () => promptRef.current?.focus({ preventScroll: true })
    const frame = requestAnimationFrame(focusPrompt)
    const retry = window.setTimeout(() => {
      if (!rootRef.current?.contains(document.activeElement)) focusPrompt()
    }, 150)
    return () => { cancelAnimationFrame(frame); window.clearTimeout(retry) }
  }, [])

  // Track the stage size.
  useLayoutEffect(() => {
    const node = stageRef.current
    if (!node) return
    const measure = () => setStage((prev) => (prev.w === node.clientWidth && prev.h === node.clientHeight ? prev : { w: node.clientWidth, h: node.clientHeight }))
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    return () => observer.disconnect()
  }, [])

  const fit = () => {
    autoFitRef.current = true
    setView(fitView(docRef.current.canvas, stage.w, stage.h))
  }
  useLayoutEffect(() => {
    if (!base || !autoFitRef.current || dragRef.current?.kind === 'canvas') return
    setView(fitView(doc.canvas, stage.w, stage.h))
  }, [base, doc.canvas, stage.w, stage.h])

  const zoomAt = (factor: number, sx = stage.w / 2, sy = stage.h / 2) => {
    const v = viewRef.current
    const zoom = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, v.zoom * factor))
    if (zoom === v.zoom) return
    autoFitRef.current = false
    const k = zoom / v.zoom
    setView({ zoom, x: sx - (sx - v.x) * k, y: sy - (sy - v.y) * k })
  }
  const zoomTo = (zoom: number) => zoomAt(zoom / viewRef.current.zoom)

  // Repaint: document → offscreen canvas at 1:1, then onto the stage with the
  // current zoom, plus the editing chrome (selection, handles, crop shade).
  useEffect(() => {
    const viewport = canvasRef.current
    if (!viewport || !base || !stage.w || !stage.h) return
    if (!docCanvasRef.current) docCanvasRef.current = document.createElement('canvas')
    const docCanvas = docCanvasRef.current
    const c = doc.canvas
    if (docCanvas.width !== c.w) docCanvas.width = c.w
    if (docCanvas.height !== c.h) docCanvas.height = c.h
    const dctx = docCanvas.getContext('2d')
    const ctx = viewport.getContext('2d')
    if (!dctx || !ctx) return

    const editingId = textDraft?.editId
    const visible = editingId ? shapes.filter((layer) => layer.id !== editingId) : shapes
    // Text being typed is drawn by the canvas too (same renderer as when it is
    // committed), so it never shifts between editing and not editing. The
    // textarea on top is transparent and only supplies the caret/IME.
    const liveText: Shape | null = textDraft && textDraft.value
      ? { type: 'text', x: textDraft.x, y: textDraft.y, text: textDraft.value, color: textDraft.color || color, size: textDraft.size }
      : null
    const extra = [draft, liveText].filter(Boolean) as Shape[]
    renderDocument(dctx, doc, base, extra.length ? [...visible, ...extra] : visible)

    const dpr = window.devicePixelRatio || 1
    const pw = Math.max(1, Math.round(stage.w * dpr)), ph = Math.max(1, Math.round(stage.h * dpr))
    if (viewport.width !== pw) viewport.width = pw
    if (viewport.height !== ph) viewport.height = ph
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, stage.w, stage.h)
    const z = view.zoom
    const toScreen = (b: Box): Box => ({ x: b.x * z + view.x, y: b.y * z + view.y, w: b.w * z, h: b.h * z })
    const r = toScreen(c)

    // While resizing the canvas, ghost the whole image so what a crop cuts
    // off stays visible.
    if (tool === 'canvas' && !doc.baseHidden) {
      ctx.save()
      ctx.globalAlpha = 0.2
      ctx.drawImage(base, view.x, view.y, W * z, H * z)
      ctx.restore()
    }
    ctx.save()
    ctx.shadowColor = 'rgba(0,0,0,0.55)'
    ctx.shadowBlur = 28
    ctx.shadowOffsetY = 8
    ctx.fillStyle = '#232326'
    ctx.fillRect(r.x, r.y, r.w, r.h)
    ctx.restore()
    if (!checkerRef.current) {
      const tile = document.createElement('canvas')
      tile.width = 16
      tile.height = 16
      const t = tile.getContext('2d')
      if (t) {
        t.fillStyle = '#2b2b2f'
        t.fillRect(0, 0, 16, 16)
        t.fillStyle = '#222225'
        t.fillRect(0, 0, 8, 8)
        t.fillRect(8, 8, 8, 8)
        checkerRef.current = ctx.createPattern(tile, 'repeat')
      }
    }
    if (checkerRef.current) {
      ctx.fillStyle = checkerRef.current
      ctx.fillRect(r.x, r.y, r.w, r.h)
    }
    ctx.imageSmoothingEnabled = z < 4
    ctx.imageSmoothingQuality = 'high'
    ctx.drawImage(docCanvas, r.x, r.y, r.w, r.h)

    const drawHandle = (p: Point, size: number, round = false) => {
      ctx.beginPath()
      if (round) ctx.arc(p.x, p.y, size / 2, 0, Math.PI * 2)
      else ctx.rect(Math.round(p.x - size / 2) + 0.5, Math.round(p.y - size / 2) + 0.5, size, size)
      ctx.fillStyle = '#ffffff'
      ctx.fill()
      ctx.lineWidth = 1.5
      ctx.strokeStyle = '#3b82f6'
      ctx.stroke()
    }

    if (cropDraft && cropDraft.w > 0 && cropDraft.h > 0) {
      const a = toScreen(cropDraft)
      ctx.save()
      ctx.fillStyle = 'rgba(0,0,0,0.6)'
      ctx.beginPath()
      ctx.rect(r.x, r.y, r.w, r.h)
      ctx.rect(a.x, a.y, a.w, a.h)
      ctx.fill('evenodd')
      ctx.strokeStyle = '#ffffff'
      ctx.lineWidth = 1
      ctx.setLineDash([6, 4])
      ctx.strokeRect(a.x + 0.5, a.y + 0.5, a.w, a.h)
      ctx.restore()
    }

    if (tool === 'canvas') {
      ctx.save()
      ctx.strokeStyle = '#3b82f6'
      ctx.lineWidth = 1.5
      ctx.strokeRect(r.x, r.y, r.w, r.h)
      BOX_HANDLES.forEach((handle) => drawHandle(handlePoint(r, handle), 10))
      const label = `${c.w} × ${c.h}`
      ctx.font = '600 11px ui-monospace, monospace'
      const lw = ctx.measureText(label).width + 12
      const lx = r.x + r.w / 2 - lw / 2, ly = r.y - 30
      ctx.fillStyle = '#2563eb'
      ctx.beginPath()
      ctx.roundRect?.(lx, ly, lw, 20, 5)
      ctx.fill()
      ctx.fillStyle = '#ffffff'
      ctx.textBaseline = 'middle'
      ctx.fillText(label, lx + 6, ly + 10.5)
      ctx.restore()
    } else {
      ctx.strokeStyle = 'rgba(255,255,255,0.14)'
      ctx.lineWidth = 1
      ctx.strokeRect(Math.round(r.x) - 0.5, Math.round(r.y) - 0.5, Math.round(r.w) + 1, Math.round(r.h) + 1)
    }

    const sel = shapes.find((layer) => layer.id === selectedId && !layer.hidden)
    if (sel && !draft && !editingId && tool !== 'canvas' && tool !== 'crop') {
      ctx.save()
      if (sel.type === 'arrow') {
        drawHandle({ x: sel.x1 * z + view.x, y: sel.y1 * z + view.y }, 10, true)
        drawHandle({ x: sel.x2 * z + view.x, y: sel.y2 * z + view.y }, 10, true)
      } else {
        const b = toScreen(shapeBox(dctx, sel))
        const pad = isBoxShape(sel) ? 0 : 4
        ctx.strokeStyle = '#60a5fa'
        ctx.lineWidth = 1
        if (!isBoxShape(sel)) ctx.setLineDash([5, 4])
        ctx.strokeRect(Math.round(b.x - pad) + 0.5, Math.round(b.y - pad) + 0.5, Math.round(b.w + pad * 2), Math.round(b.h + pad * 2))
        ctx.setLineDash([])
        if (isBoxShape(sel)) BOX_HANDLES.forEach((handle) => drawHandle(handlePoint(b, handle), 8))
      }
      ctx.restore()
    }
  }, [base, doc, shapes, draft, cropDraft, W, H, selectedId, textDraft, color, view, stage.w, stage.h, tool])

  const toWorld = (event: { clientX: number; clientY: number }) => {
    const rect = canvasRef.current!.getBoundingClientRect()
    const v = viewRef.current
    const sx = event.clientX - rect.left, sy = event.clientY - rect.top
    return { x: (sx - v.x) / v.zoom, y: (sy - v.y) / v.zoom, sx, sy }
  }

  // Handle of the canvas (canvas tool) or of the selected layer under a point.
  const handleAt = (p: { x: number; y: number; sx: number; sy: number }): { target: 'canvas' | 'layer'; handle: Handle } | null => {
    const v = viewRef.current
    const near = (wx: number, wy: number) => Math.hypot(wx * v.zoom + v.x - p.sx, wy * v.zoom + v.y - p.sy) <= 9
    if (tool === 'canvas') {
      const hit = BOX_HANDLES.find((handle) => { const hp = handlePoint(docRef.current.canvas, handle); return near(hp.x, hp.y) })
      return hit ? { target: 'canvas', handle: hit } : null
    }
    if (tool === 'crop') return null
    const layer = docRef.current.shapes.find((item) => item.id === selectedId && !item.hidden)
    if (!layer) return null
    if (layer.type === 'arrow') {
      if (near(layer.x2, layer.y2)) return { target: 'layer', handle: 'p2' }
      if (near(layer.x1, layer.y1)) return { target: 'layer', handle: 'p1' }
      return null
    }
    if (!isBoxShape(layer)) return null
    const b = normalizeBox(layer.x, layer.y, layer.x + layer.w, layer.y + layer.h)
    const hit = BOX_HANDLES.find((handle) => { const hp = handlePoint(b, handle); return near(hp.x, hp.y) })
    return hit ? { target: 'layer', handle: hit } : null
  }

  const addImageLayer = useCallback(async (blob: Blob) => {
    try {
      const { img, url } = await loadImage(blob)
      urlsRef.current.push(url)
      const c = docRef.current.canvas
      const scale = Math.min(1, (c.w * 0.6) / img.naturalWidth, (c.h * 0.6) / img.naturalHeight)
      const w = Math.max(1, Math.round(img.naturalWidth * scale))
      const h = Math.max(1, Math.round(img.naturalHeight * scale))
      addLayer({ type: 'image', img, x: Math.round(c.x + (c.w - w) / 2), y: Math.round(c.y + (c.h - h) / 2), w, h })
      setTool('move')
    } catch { /* unreadable image: ignore */ }
  }, [])

  const deleteLayer = (id: string) => {
    mutateShapes((list) => list.filter((layer) => layer.id !== id))
    if (selectedId === id) setSelectedId(null)
  }

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
    if (value) addLayer({ type: 'text', x: current.x, y: current.y, text: value, color, size: current.size })
  }

  // Topmost visible layer under a world-space point.
  const hitLayer = (p: Point, only?: Shape['type']): Layer | null => {
    const ctx = docCanvasRef.current?.getContext('2d') || null
    const slop = Math.max(4, strokeBase)
    for (let i = docRef.current.shapes.length - 1; i >= 0; i -= 1) {
      const layer = docRef.current.shapes[i]
      if (layer.hidden || (only && layer.type !== only)) continue
      const b = shapeBox(ctx, layer)
      if (p.x >= b.x - slop && p.x <= b.x + b.w + slop && p.y >= b.y - slop && p.y <= b.y + b.h + slop) return layer
    }
    return null
  }

  const capture = (event: React.PointerEvent<HTMLCanvasElement>) => event.currentTarget.setPointerCapture?.(event.pointerId)
  const startMove = (event: React.PointerEvent<HTMLCanvasElement>, layer: Layer, p: Point) => {
    setSelectedId(layer.id)
    dragRef.current = { kind: 'move', id: layer.id, x: p.x, y: p.y, orig: layer, snapshot: docRef.current, changed: false }
    capture(event)
  }
  const startPan = (event: React.PointerEvent<HTMLCanvasElement>) => {
    dragRef.current = { kind: 'pan', cx: event.clientX, cy: event.clientY, vx: viewRef.current.x, vy: viewRef.current.y }
    capture(event)
  }

  // Double-click a text layer to edit it in place.
  const onDoubleClick = (event: React.MouseEvent<HTMLCanvasElement>) => {
    const layer = hitLayer(toWorld(event), 'text')
    if (!layer || layer.type !== 'text') return
    event.preventDefault()
    dragRef.current = null
    textOpenedAtRef.current = Date.now()
    setSelectedId(layer.id)
    setTextDraft({ x: layer.x, y: layer.y, value: layer.text, editId: layer.id, color: layer.color, size: layer.size })
  }

  // Focus the box after it has mounted and the opening click is fully handled.
  const textDraftOpen = Boolean(textDraft)
  useEffect(() => {
    if (!textDraftOpen) return
    const frame = requestAnimationFrame(() => textInputRef.current?.focus())
    return () => cancelAnimationFrame(frame)
  }, [textDraftOpen])

  const onPointerDown = (event: React.PointerEvent<HTMLCanvasElement>) => {
    if (!base) return
    if (textDraftRef.current) { commitText(); return }
    if (event.button === 1 || (event.button === 0 && spaceRef.current)) { event.preventDefault(); startPan(event); return }
    if (event.button !== 0) return
    const p = toWorld(event)
    // Clicking the artwork takes the keyboard away from the note field so
    // Delete, arrows and tool keys act on the canvas.
    if (tool !== 'text') stageRef.current?.focus({ preventScroll: true })

    const grip = handleAt(p)
    if (grip?.target === 'canvas') {
      dragRef.current = { kind: 'canvas', handle: grip.handle, x: p.x, y: p.y, orig: docRef.current.canvas, snapshot: docRef.current, changed: false }
      capture(event)
      return
    }
    if (grip?.target === 'layer' && selected) {
      dragRef.current = { kind: 'resize', id: selected.id, handle: grip.handle, x: p.x, y: p.y, orig: selected, snapshot: docRef.current, changed: false }
      capture(event)
      return
    }
    if (tool === 'canvas') { startPan(event); return }
    if (tool === 'text') {
      // Clicking existing text selects (and drags) it instead of starting a
      // new one; double-click edits it.
      const existing = hitLayer(p, 'text')
      if (existing) { startMove(event, existing, p); return }
      // The click that opens the box would otherwise move focus to <body>
      // right after the textarea mounts, blur it, and close it empty.
      event.preventDefault()
      textOpenedAtRef.current = Date.now()
      setTextDraft({ x: p.x, y: p.y, value: '', size: textSize })
      return
    }
    if (tool === 'number') {
      const existing = hitLayer(p, 'number')
      if (existing) { startMove(event, existing, p); return }
      const n = docRef.current.shapes.reduce((max, layer) => (layer.type === 'number' ? Math.max(max, layer.n) : max), 0) + 1
      addLayer({ type: 'number', x: p.x, y: p.y, n, color, size: textSize })
      return
    }
    if (tool === 'image') { fileInputRef.current?.click(); return }
    if (tool === 'move') {
      const layer = hitLayer(p)
      if (layer) startMove(event, layer, p)
      else { setSelectedId(null); startPan(event) }
      return
    }
    dragRef.current = { kind: 'draw', x: p.x, y: p.y }
    if (tool === 'pen') setDraft({ type: 'pen', points: [p.x, p.y], color, width: strokeWidth })
    capture(event)
  }

  const onPointerMove = (event: React.PointerEvent<HTMLCanvasElement>) => {
    const drag = dragRef.current
    if (!drag) {
      if (!base) return
      const p = toWorld(event)
      const grip = handleAt(p)
      let next: string | null = null
      if (grip) next = HANDLE_CURSOR[grip.handle]
      else if (spaceRef.current || tool === 'canvas') next = 'grab'
      else if (tool === 'move') next = hitLayer(p) ? 'move' : 'grab'
      setHoverCursor((prev) => (prev === next ? prev : next))
      return
    }
    if (drag.kind === 'pan') {
      autoFitRef.current = false
      setView({ zoom: viewRef.current.zoom, x: drag.vx + event.clientX - drag.cx, y: drag.vy + event.clientY - drag.cy })
      return
    }
    const p = toWorld(event)
    const dx = p.x - drag.x, dy = p.y - drag.y
    if (drag.kind === 'draw') {
      if (tool === 'crop') setCropDraft(normalizeBox(drag.x, drag.y, p.x, p.y))
      else if (tool === 'arrow') setDraft({ type: 'arrow', x1: drag.x, y1: drag.y, x2: p.x, y2: p.y, color, width: strokeWidth })
      else if (tool === 'pen') setDraft((prev) => (prev?.type === 'pen' ? { ...prev, points: [...prev.points, p.x, p.y] } : prev))
      else if (tool === 'mosaic') setDraft({ type: 'mosaic', x: drag.x, y: drag.y, w: dx, h: dy, block: blockFor(sizeLevel) })
      else if (tool === 'rect' || tool === 'ellipse' || tool === 'overlay') setDraft({ type: tool, x: drag.x, y: drag.y, w: dx, h: dy, color, width: strokeWidth })
      return
    }
    if (!dx && !dy && !drag.changed) return
    if (!drag.changed) { drag.changed = true; pushUndo(drag.snapshot) }
    const current = docRef.current
    if (drag.kind === 'canvas') {
      setDocRaw({ ...current, canvas: clampCanvas(resizeBox(drag.orig, drag.handle, dx, dy, { keepRatio: event.shiftKey, min: CANVAS_MIN })) })
      return
    }
    let next: Layer
    if (drag.kind === 'move') next = moveShape(drag.orig, dx, dy)
    else if (drag.orig.type === 'arrow') next = drag.handle === 'p1' ? { ...drag.orig, x1: p.x, y1: p.y } : { ...drag.orig, x2: p.x, y2: p.y }
    else if (isBoxShape(drag.orig)) {
      const o = drag.orig
      const box = resizeBox(normalizeBox(o.x, o.y, o.x + o.w, o.y + o.h), drag.handle, dx, dy, { keepRatio: o.type === 'image' ? !event.shiftKey : event.shiftKey, min: 4 })
      next = { ...o, ...box }
    } else return
    setDocRaw({ ...current, shapes: current.shapes.map((layer) => (layer.id === drag.id ? next : layer)) })
  }

  const onPointerUp = () => {
    const drag = dragRef.current
    dragRef.current = null
    if (!drag) return
    if (drag.kind === 'canvas') {
      if (autoFitRef.current) setView(fitView(docRef.current.canvas, stage.w, stage.h))
      return
    }
    if (drag.kind !== 'draw') return
    if (tool === 'crop') {
      const c = docRef.current.canvas
      if (cropDraft) {
        // A crop can only keep what is on the canvas.
        const x1 = Math.max(c.x, Math.round(cropDraft.x)), y1 = Math.max(c.y, Math.round(cropDraft.y))
        const x2 = Math.min(c.x + c.w, Math.round(cropDraft.x + cropDraft.w)), y2 = Math.min(c.y + c.h, Math.round(cropDraft.y + cropDraft.h))
        if (x2 - x1 >= CANVAS_MIN && y2 - y1 >= CANVAS_MIN) {
          commit((current) => ({ ...current, canvas: { x: x1, y: y1, w: x2 - x1, h: y2 - y1 } }))
          autoFitRef.current = true
          // Hand over to the canvas tool so the edges can be fine-tuned.
          setTool('canvas')
        }
      }
      setCropDraft(null)
      return
    }
    if (draft) {
      let keep = true
      let shape: Shape = draft
      if (draft.type === 'arrow') keep = Math.hypot(draft.x2 - draft.x1, draft.y2 - draft.y1) >= 6
      else if (draft.type === 'pen') {
        const b = shapeBox(null, draft)
        keep = draft.points.length >= 4 && (b.w > draft.width + 3 || b.h > draft.width + 3)
      } else if (isBoxShape(draft)) {
        keep = Math.abs(draft.w) >= 4 || Math.abs(draft.h) >= 4
        shape = { ...draft, ...normalizeBox(draft.x, draft.y, draft.x + draft.w, draft.y + draft.h) }
      }
      if (keep) addLayer(shape)
    }
    setDraft(null)
  }

  const undo = () => {
    const previous = undoRef.current.pop()
    if (!previous) return
    redoRef.current.push(docRef.current)
    syncHistory()
    setDocRaw(previous)
    if (selectedId && !previous.shapes.some((layer) => layer.id === selectedId)) setSelectedId(null)
  }
  const redo = () => {
    const next = redoRef.current.pop()
    if (!next) return
    undoRef.current.push(docRef.current)
    syncHistory()
    setDocRaw(next)
    if (selectedId && !next.shapes.some((layer) => layer.id === selectedId)) setSelectedId(null)
  }
  const toggleLayerHidden = (id: string) => mutateShapes((list) => list.map((layer) => (layer.id === id ? { ...layer, hidden: !layer.hidden } : layer)))
  // delta +1 = one step closer to the top of the stack.
  const moveLayer = (id: string, delta: 1 | -1) => mutateShapes((list) => {
    const from = list.findIndex((layer) => layer.id === id)
    const to = from + delta
    if (from < 0 || to < 0 || to >= list.length) return list
    const next = [...list]
    ;[next[from], next[to]] = [next[to], next[from]]
    return next
  })
  const duplicateLayer = (id: string) => {
    const source = docRef.current.shapes.find((layer) => layer.id === id)
    if (!source) return
    const offset = strokeBase * 5
    const copy = { ...moveShape(source, offset, offset), id: newLayerId(), hidden: false } as Layer
    mutateShapes((list) => [...list, copy])
    setSelectedId(copy.id)
  }
  const nudge = (id: string, dx: number, dy: number) => mutateShapes((list) => list.map((layer) => (layer.id === id ? moveShape(layer, dx, dy) : layer)))

  // Colour and size apply to the selected layer as well as to what comes next.
  const applyColor = (next: string) => {
    setColor(next)
    if (!selected || !('color' in selected) || selected.color === next) return
    mutateShapes((list) => list.map((layer) => (layer.id === selected.id && 'color' in layer ? { ...layer, color: next } : layer)))
  }
  const applySize = (level: number) => {
    setSizeLevel(level)
    if (!selected) return
    mutateShapes((list) => list.map((layer) => {
      if (layer.id !== selected.id) return layer
      if (layer.type === 'text' || layer.type === 'number') return { ...layer, size: textFor(level) }
      if (layer.type === 'mosaic') return { ...layer, block: blockFor(level) }
      if (layer.type === 'image') return layer
      return { ...layer, width: strokeFor(level) }
    }))
  }

  const setCanvas = (next: Box) => {
    const canvas = clampCanvas(next)
    commit((current) => (sameBox(current.canvas, canvas) ? current : { ...current, canvas }))
  }
  const setBackground = (bg: string | null) => commit((current) => (current.bg === bg ? current : { ...current, bg }))
  const setBaseHidden = (hidden: boolean) => commit((current) => ({ ...current, baseHidden: hidden }))

  const pickTool = (id: Tool) => {
    commitText()
    if (id === 'image') { fileInputRef.current?.click(); return }
    // A drawing tool starts fresh, so colour and size describe the next shape
    // rather than whatever happened to be selected.
    if (id !== 'move' && id !== tool) setSelectedId(null)
    setTool(id)
  }

  const finish = async () => {
    if (!base || exporting) return
    if (!dirty) { onDone(file, promptValue()); return }
    setExporting(true)
    try {
      const out = document.createElement('canvas')
      out.width = doc.canvas.w
      out.height = doc.canvas.h
      const ctx = out.getContext('2d')
      if (!ctx) { onDone(file, promptValue()); return }
      renderDocument(ctx, doc, base, shapes)
      const blob = await new Promise<Blob | null>((resolve) => out.toBlob(resolve, 'image/png'))
      onDone(blob ? new File([blob], annotatedFileName(file.name), { type: 'image/png' }) : file, promptValue())
    } finally {
      setExporting(false)
    }
  }

  // Wheel zooms around the pointer. Native listener: React's is passive, so it
  // could not stop Ctrl+wheel from zooming the whole page.
  useEffect(() => {
    const node = canvasRef.current
    if (!node || !base) return
    const onWheel = (event: WheelEvent) => {
      event.preventDefault()
      const rect = node.getBoundingClientRect()
      const step = event.deltaMode === 1 ? event.deltaY * 16 : event.deltaY
      zoomAt(Math.exp(-step * 0.0018), event.clientX - rect.left, event.clientY - rect.top)
    }
    node.addEventListener('wheel', onWheel, { passive: false })
    return () => node.removeEventListener('wheel', onWheel)
  })

  // Shortcuts + pasting another image into the editor adds it as a layer.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (textDraftRef.current) return
      const target = event.target as HTMLElement | null
      const typing = !!target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
      const mod = event.ctrlKey || event.metaKey
      const key = event.key.toLowerCase()
      const stop = () => { event.preventDefault(); event.stopPropagation() }
      if (event.key === 'Escape') {
        event.preventDefault()
        // Esc backs out one step at a time: help → note field → selection →
        // editor. With unsent edits the editor asks for a second Esc.
        if (helpOpen) setHelpOpen(false)
        else if (typing) (target as HTMLElement).blur()
        else if (selectedId) setSelectedId(null)
        else if (dirty && !escArmed) setEscArmed(true)
        else onDone(null)
        return
      }
      if (mod && event.key === 'Enter') { event.preventDefault(); void finish(); return }
      if (mod && (key === 'z' || key === 'y')) {
        // Leave native undo to a note that has text in it.
        if (typing && (target as HTMLInputElement).value) return
        event.preventDefault()
        if (key === 'y' || event.shiftKey) redo()
        else undo()
        return
      }
      if (mod && key === '0') { stop(); fit(); return }
      if (mod && key === '1') { stop(); zoomTo(1); return }
      if (mod && (key === '=' || key === '+')) { stop(); zoomAt(1.25); return }
      if (mod && key === '-') { stop(); zoomAt(0.8); return }
      if (typing) return
      if (mod && key === 'd') { if (selectedId) { stop(); duplicateLayer(selectedId) } return }
      if (mod || event.altKey) return
      if (event.key === ' ') { stop(); spaceRef.current = true; return }
      if (event.key === 'Enter') { stop(); promptRef.current?.focus(); return }
      if (selectedId) {
        if (event.key === 'Delete' || event.key === 'Backspace') { stop(); deleteLayer(selectedId); return }
        const step = event.shiftKey ? 10 : 1
        const arrows: Record<string, [number, number]> = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }
        if (arrows[event.key]) { stop(); nudge(selectedId, arrows[event.key][0], arrows[event.key][1]); return }
        if (event.key === ']') { stop(); moveLayer(selectedId, 1); return }
        if (event.key === '[') { stop(); moveLayer(selectedId, -1); return }
      }
      if (key.length === 1 && TOOL_BY_KEY[key]) { stop(); pickTool(TOOL_BY_KEY[key]) }
    }
    const onKeyUp = (event: KeyboardEvent) => { if (event.key === ' ') spaceRef.current = false }
    const onPaste = (event: ClipboardEvent) => {
      const image = Array.from(event.clipboardData?.files || []).find((f) => f.type.startsWith('image/'))
      if (!image) return
      event.preventDefault()
      event.stopPropagation()
      void addImageLayer(image)
    }
    window.addEventListener('keydown', onKey, true)
    window.addEventListener('keyup', onKeyUp, true)
    window.addEventListener('paste', onPaste, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      window.removeEventListener('keyup', onKeyUp, true)
      window.removeEventListener('paste', onPaste, true)
    }
  })

  // The "press Esc again" prompt lapses on its own.
  useEffect(() => {
    if (!escArmed) return
    const timer = window.setTimeout(() => setEscArmed(false), 2500)
    return () => window.clearTimeout(timer)
  }, [escArmed])

  const toolCursor = tool === 'move' ? 'default' : tool === 'text' ? 'text' : tool === 'image' || tool === 'number' ? 'copy' : tool === 'canvas' ? 'grab' : 'crosshair'
  const names = layerNames(shapes.map((layer) => ({ id: layer.id, type: layer.type, text: layer.type === 'text' ? layer.text : undefined, n: layer.type === 'number' ? layer.n : undefined })))
  const canvasLabel = `${doc.canvas.w} × ${doc.canvas.h}`
  const bgLabel = !doc.bg ? '透明' : doc.bg === '#ffffff' ? '白色' : doc.bg === '#000000' ? '黑色' : doc.bg
  const colorless = selected ? !('color' in selected) : tool === 'mosaic' || tool === 'image'
  const sizeless = selected ? selected.type === 'image' || selected.type === 'overlay' : tool === 'overlay' || tool === 'image' || tool === 'crop' || tool === 'move'
  const fontPx = textDraft ? textDraft.size * view.zoom : 0

  return createPortal(
    <div
      ref={rootRef}
      data-id="image-annotator"
      className="fixed inset-0 z-[10050] flex items-center justify-center bg-black/70 p-4"
      onPointerDown={(event) => event.stopPropagation()}
      onMouseDown={(event) => event.stopPropagation()}
    >
      <div className="flex h-[min(92vh,940px)] w-[min(1320px,97vw)] flex-col overflow-hidden rounded-2xl border border-white/[0.12] bg-[#141416] shadow-2xl">
        <div data-id="image-annotator-header" className="flex shrink-0 items-center justify-between border-b border-white/[0.06] px-4 py-2">
          <div className="min-w-0 truncate text-sm font-semibold text-zinc-100">
            编辑图片
            {total > 1 ? <span className="ml-2 font-mono text-xs text-zinc-500">{index}/{total}</span> : null}
            <span className="ml-3 text-xs font-normal text-zinc-500">{file.name}{base ? ` · ${W}×${H}` : ''}</span>
          </div>
          <div className="relative flex shrink-0 items-center gap-1">
            <button type="button" data-id="image-annotator-help-toggle" aria-pressed={helpOpen} onClick={() => setHelpOpen((open) => !open)} className={`flex h-7 items-center gap-1.5 rounded-lg px-2 text-xs transition-colors ${helpOpen ? 'bg-white/[0.08] text-zinc-200' : 'text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200'}`}>
              <Keyboard className="h-3.5 w-3.5" />快捷键
            </button>
            <button type="button" data-id="image-annotator-close" onClick={() => onDone(null)} className="rounded-lg p-1.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200" title="取消（Esc）">
              <X className="h-4 w-4" />
            </button>
            {helpOpen ? (
              <div data-id="image-annotator-help" className="absolute right-0 top-9 z-20 w-[340px] rounded-xl border border-white/[0.1] bg-[#1b1c20] p-3 shadow-2xl">
                <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
                  {SHORTCUTS.map(([keys, what]) => (
                    <div key={keys} className="contents">
                      <span className="whitespace-nowrap font-mono text-[11px] text-zinc-300">{keys}</span>
                      <span className="text-zinc-500">{what}</span>
                    </div>
                  ))}
                </div>
                <div className="mt-2.5 border-t border-white/[0.06] pt-2 text-[11px] text-zinc-500">
                  工具：{TOOL_GROUPS.flat().map((t) => `${t.label} ${t.key}`).join(' · ')}
                </div>
              </div>
            ) : null}
          </div>
        </div>

        <div data-id="image-annotator-toolbar" className="flex shrink-0 flex-wrap items-center gap-1 border-b border-white/[0.06] px-3 py-1.5">
          {TOOL_GROUPS.map((group, groupIndex) => (
            <div key={group[0].id} className="flex items-center gap-0.5">
              {groupIndex > 0 ? <span className="mx-1.5 h-5 w-px bg-white/[0.08]" /> : null}
              {group.map(({ id, label, key, Icon }) => (
                <button
                  key={id}
                  type="button"
                  data-id={`image-annotator-tool-${id}`}
                  aria-pressed={tool === id}
                  title={`${label}（${key}）`}
                  onClick={() => pickTool(id)}
                  className={`flex h-8 items-center gap-1.5 rounded-lg px-2 text-xs transition-colors ${tool === id ? 'bg-blue-500/20 text-blue-200' : 'text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200'}`}
                >
                  <Icon className="h-4 w-4" />{label}
                </button>
              ))}
            </div>
          ))}
          <div className="ml-auto flex items-center gap-0.5">
            <button type="button" data-id="image-annotator-undo" onClick={undo} disabled={!history.undo} className={iconBtnClass} title="撤销（Ctrl+Z）"><Undo2 className="h-4 w-4" /></button>
            <button type="button" data-id="image-annotator-redo" onClick={redo} disabled={!history.redo} className={iconBtnClass} title="重做（Ctrl+Shift+Z）"><Redo2 className="h-4 w-4" /></button>
            <span className="mx-1.5 h-5 w-px bg-white/[0.08]" />
            <button
              type="button"
              data-id="image-annotator-layers-toggle"
              aria-pressed={layersOpen}
              onClick={() => setLayersOpen((open) => !open)}
              className={`flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-xs transition-colors ${layersOpen ? 'bg-white/[0.08] text-zinc-200' : 'text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200'}`}
              title="图层面板"
            >
              <PanelRight className="h-4 w-4" />图层
            </button>
          </div>
        </div>

        <div data-id="image-annotator-options" className="flex min-h-[42px] shrink-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-white/[0.06] bg-[#111113] px-3 py-1.5">
          {tool === 'canvas' ? (
            <>
              <NumField dataId="image-annotator-canvas-width" label="宽" value={doc.canvas.w} onCommit={(w) => setCanvas(resizeCanvasCentered(doc.canvas, w, doc.canvas.h))} />
              <span className="text-xs text-zinc-600">×</span>
              <NumField dataId="image-annotator-canvas-height" label="高" value={doc.canvas.h} onCommit={(h) => setCanvas(resizeCanvasCentered(doc.canvas, doc.canvas.w, h))} />
              <span className="mx-1 h-5 w-px bg-white/[0.08]" />
              <button type="button" data-id="image-annotator-canvas-preset-original" disabled={sameBox(doc.canvas, imageBox)} onClick={() => setCanvas(imageBox)} className={chipClass} title="画布恢复成原图大小">原图大小</button>
              <button type="button" data-id="image-annotator-canvas-preset-fit" onClick={() => setCanvas(contentBounds(docCanvasRef.current?.getContext('2d') || null, W, H, doc))} className={chipClass} title="刚好包住原图和所有图层">包住全部内容</button>
              <button type="button" data-id="image-annotator-canvas-preset-pad" onClick={() => setCanvas(padBox(doc.canvas, Math.max(24, Math.round(Math.max(W, H) * 0.12))))} className={chipClass} title="四周各加一圈空白，用来写说明">四周留白</button>
              <span className="mx-1 h-5 w-px bg-white/[0.08]" />
              <span className="text-[11px] text-zinc-500">比例</span>
              {[[1, 1], [4, 3], [16, 9]].map(([rw, rh]) => (
                <button key={`${rw}-${rh}`} type="button" data-id={`image-annotator-canvas-ratio-${rw}-${rh}`} onClick={() => setCanvas(expandToRatio(doc.canvas, rw, rh))} className={`${chipClass} font-mono`} title="向外扩展到这个比例，不裁掉内容">{rw}:{rh}</button>
              ))}
              <span className="mx-1 h-5 w-px bg-white/[0.08]" />
              <span className="text-[11px] text-zinc-500">背景</span>
              {([['transparent', null, '透明'], ['white', '#ffffff', '白'], ['black', '#000000', '黑']] as Array<[string, string | null, string]>).map(([id, value, label]) => (
                <button
                  key={id}
                  type="button"
                  data-id={`image-annotator-canvas-bg-${id}`}
                  aria-pressed={doc.bg === value}
                  onClick={() => setBackground(value)}
                  title={`${label}背景`}
                  className={`h-6 w-6 rounded-md border-2 ${doc.bg === value ? 'border-blue-400' : 'border-white/20'}`}
                  style={{ background: value || 'repeating-conic-gradient(#555 0% 25%, #2a2a2a 0% 50%) 50% / 8px 8px' }}
                />
              ))}
              <label className={`relative h-6 w-6 cursor-pointer overflow-hidden rounded-md border-2 ${doc.bg && doc.bg !== '#ffffff' && doc.bg !== '#000000' ? 'border-blue-400' : 'border-white/20'}`} title="自定义背景色" style={{ background: doc.bg && doc.bg !== '#ffffff' && doc.bg !== '#000000' ? doc.bg : 'conic-gradient(#ef4444, #f59e0b, #22c55e, #3b82f6, #a855f7, #ef4444)' }}>
                <input type="color" data-id="image-annotator-canvas-bg-custom" value={doc.bg || '#ffffff'} onChange={(event) => setBackground(event.target.value)} className="absolute inset-0 h-full w-full cursor-pointer opacity-0" />
              </label>
            </>
          ) : (
            <>
              <div className={`flex items-center gap-1 ${colorless ? 'pointer-events-none opacity-30' : ''}`}>
                {COLORS.map((c) => (
                  <button
                    key={c}
                    type="button"
                    data-id={`image-annotator-color-${c.slice(1)}`}
                    aria-label={c}
                    aria-pressed={color === c}
                    onClick={() => applyColor(c)}
                    className={`h-6 w-6 rounded-full border-2 transition-transform hover:scale-110 ${color === c ? 'border-white' : 'border-white/20'}`}
                    style={{ background: c }}
                  />
                ))}
              </div>
              <span className="mx-1 h-5 w-px bg-white/[0.08]" />
              <div className={`flex items-center rounded-md border border-white/[0.08] p-0.5 ${sizeless ? 'pointer-events-none opacity-30' : ''}`}>
                {SIZE_LABEL.map((label, level) => (
                  <button
                    key={label}
                    type="button"
                    data-id={`image-annotator-size-${level}`}
                    aria-pressed={sizeLevel === level}
                    onClick={() => applySize(level)}
                    title={`粗细 / 字号：${label}`}
                    className={`flex h-6 items-center gap-1.5 rounded px-2 text-xs transition-colors ${sizeLevel === level ? 'bg-white/[0.12] text-white' : 'text-zinc-400 hover:text-zinc-200'}`}
                  >
                    <span className="rounded-full bg-current" style={{ width: 4 + level * 3, height: 4 + level * 3 }} />{label}
                  </button>
                ))}
              </div>
              {selected ? (
                <>
                  <span className="mx-1 h-5 w-px bg-white/[0.08]" />
                  <span className="max-w-[140px] truncate text-xs text-zinc-300">{names[selected.id]}</span>
                  <button type="button" data-id="image-annotator-selection-duplicate" onClick={() => duplicateLayer(selected.id)} className={chipClass} title="复制一份（Ctrl+D）">复制</button>
                  <button type="button" data-id="image-annotator-selection-delete" onClick={() => deleteLayer(selected.id)} className={`${chipClass} hover:!text-red-300`} title="删除（Delete）">删除</button>
                </>
              ) : null}
            </>
          )}
          <span data-id="image-annotator-hint" className="ml-auto truncate pl-2 text-[11px] text-zinc-500">{TOOL_HINT[tool]}</span>
        </div>

        <div data-id="image-annotator-body" className="flex min-h-0 flex-1">
          <div
            ref={stageRef}
            tabIndex={-1}
            data-id="image-annotator-stage"
            className="relative min-h-0 min-w-0 flex-1 overflow-hidden bg-[#0b0b0d] outline-none"
            onDragOver={(event) => { if (Array.from(event.dataTransfer.types).includes('Files')) event.preventDefault() }}
            onDrop={(event) => {
              const dropped = Array.from(event.dataTransfer.files).find((f) => f.type.startsWith('image/'))
              if (!dropped) return
              event.preventDefault()
              void addImageLayer(dropped)
            }}
          >
            {loadError ? (
              <div className="absolute inset-0 grid place-items-center text-sm text-red-400">图片读取失败</div>
            ) : !base ? (
              <div className="absolute inset-0 grid place-items-center text-sm text-zinc-500">加载中…</div>
            ) : (
              <>
                <canvas
                  ref={canvasRef}
                  data-id="image-annotator-canvas"
                  onPointerDown={onPointerDown}
                  onMouseDown={(event) => event.preventDefault()}
                  onPointerMove={onPointerMove}
                  onPointerUp={onPointerUp}
                  onPointerCancel={onPointerUp}
                  onPointerLeave={() => setHoverCursor(null)}
                  onDoubleClick={onDoubleClick}
                  onContextMenu={(event) => event.preventDefault()}
                  className="absolute inset-0 block h-full w-full touch-none select-none"
                  style={{ cursor: hoverCursor || toolCursor }}
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
                      if (event.nativeEvent.isComposing) return
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
                    // Same metrics as drawShape('text') at the current zoom, so
                    // the text doesn't jump in size when it's committed.
                    className="absolute m-0 resize-none overflow-hidden whitespace-pre border-0 bg-transparent p-0 outline outline-1 outline-dashed outline-blue-400/70 placeholder:text-white/40"
                    style={{
                      left: textDraft.x * view.zoom + view.x,
                      // CSS centres the glyphs in a 1.25 line box (half-leading on
                      // top); canvas draws from the em top. Shift up so the caret
                      // lines up with the canvas-rendered text.
                      top: textDraft.y * view.zoom + view.y - fontPx * 0.125,
                      color: 'transparent',
                      caretColor: textDraft.color || color,
                      fontFamily: 'system-ui, sans-serif',
                      fontWeight: 600,
                      fontSize: `${fontPx}px`,
                      lineHeight: 1.25,
                      width: `calc(${Math.max(4, ...textDraft.value.split('\n').map((line) => [...line].length))}ch + ${fontPx}px)`,
                      minWidth: `${Math.max(120, fontPx * 4)}px`,
                    }}
                  />
                ) : null}
                <div data-id="image-annotator-viewbar" className="absolute bottom-3 left-3 flex items-center gap-0.5 rounded-lg border border-white/[0.08] bg-[#17181b]/95 p-0.5 text-xs text-zinc-300 shadow-lg backdrop-blur">
                  <button type="button" data-id="image-annotator-zoom-out" onClick={() => zoomAt(0.8)} className="grid h-7 w-7 place-items-center rounded-md hover:bg-white/[0.08]" title="缩小（Ctrl+-）"><Minus className="h-3.5 w-3.5" /></button>
                  <button type="button" data-id="image-annotator-zoom-reset" onClick={() => zoomTo(1)} className="h-7 min-w-[48px] rounded-md px-1 font-mono text-[11px] hover:bg-white/[0.08]" title="实际大小 100%（Ctrl+1）">{Math.round(view.zoom * 100)}%</button>
                  <button type="button" data-id="image-annotator-zoom-in" onClick={() => zoomAt(1.25)} className="grid h-7 w-7 place-items-center rounded-md hover:bg-white/[0.08]" title="放大（Ctrl+=）"><Plus className="h-3.5 w-3.5" /></button>
                  <span className="mx-0.5 h-4 w-px bg-white/[0.08]" />
                  <button type="button" data-id="image-annotator-zoom-fit" onClick={fit} className="flex h-7 items-center gap-1 rounded-md px-2 hover:bg-white/[0.08]" title="适应窗口（Ctrl+0）"><Maximize className="h-3.5 w-3.5" />适应</button>
                </div>
                <button
                  type="button"
                  data-id="image-annotator-canvas-size"
                  onClick={() => pickTool('canvas')}
                  className="absolute bottom-3 right-3 flex h-8 items-center gap-1.5 rounded-lg border border-white/[0.08] bg-[#17181b]/95 px-2.5 font-mono text-[11px] text-zinc-300 shadow-lg backdrop-blur hover:text-white"
                  title="调整画布大小（F）"
                >
                  <Frame className="h-3.5 w-3.5" />{canvasLabel}
                </button>
                {escArmed ? (
                  <div data-id="image-annotator-esc-confirm" className="absolute left-1/2 top-3 -translate-x-1/2 rounded-lg border border-amber-400/30 bg-[#2a2212]/95 px-3 py-1.5 text-xs text-amber-200 shadow-lg">
                    有未发送的编辑，再按一次 Esc 放弃
                  </div>
                ) : null}
              </>
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
                  const rowClass = (active: boolean) => `group flex w-full cursor-pointer items-center gap-2 px-2 py-1.5 text-left ${active ? 'bg-sky-600 text-white' : 'text-zinc-300 hover:bg-white/[0.05]'}`
                  const thumbStyle = { background: 'repeating-conic-gradient(#3a3a3a 0% 25%, #2a2a2a 0% 50%) 50% / 8px 8px' }
                  return (
                    <>
                      {[...shapes].reverse().map((layer, reversedIndex) => {
                        const stackIndex = shapes.length - 1 - reversedIndex
                        const active = layer.id === selectedId
                        const Icon = LAYER_ICON[layer.type]
                        const swatch = 'color' in layer ? layer.color : undefined
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
                              <button type="button" data-id={`image-annotator-layer-duplicate-${layer.id}`} onClick={(event) => { event.stopPropagation(); duplicateLayer(layer.id) }} className="rounded p-0.5 hover:bg-black/20" title="复制图层"><Copy className="h-3.5 w-3.5" /></button>
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
                        aria-selected={selectedId === null && tool !== 'canvas'}
                        data-id="image-annotator-layer-base"
                        onClick={() => setSelectedId(null)}
                        className={rowClass(selectedId === null && tool !== 'canvas')}
                      >
                        <span className="h-8 w-8 shrink-0 overflow-hidden rounded border border-white/10" style={thumbStyle}>
                          {base ? <img src={base.src} alt="" className="h-full w-full object-contain" /> : null}
                        </span>
                        <span className={`min-w-0 flex-1 ${doc.baseHidden ? 'opacity-40' : ''}`}>
                          <span className="block truncate text-xs font-semibold">原图</span>
                          <span className={`block text-[10px] italic ${selectedId === null && tool !== 'canvas' ? 'text-sky-100' : 'text-zinc-500'}`}>图片 · 锁定</span>
                        </span>
                        <button type="button" data-id="image-annotator-layer-visibility-base" aria-pressed={!doc.baseHidden} onClick={(event) => { event.stopPropagation(); setBaseHidden(!doc.baseHidden) }} className="shrink-0 rounded p-1 hover:bg-black/20" title={doc.baseHidden ? '显示' : '隐藏'}>
                          {doc.baseHidden ? <EyeOff className="h-4 w-4 opacity-60" /> : <Eye className="h-4 w-4" />}
                        </button>
                      </div>
                    </>
                  )
                })()}
              </div>
              <button
                type="button"
                data-id="image-annotator-layer-canvas"
                aria-pressed={tool === 'canvas'}
                onClick={() => { setSelectedId(null); pickTool('canvas') }}
                className={`flex shrink-0 items-center gap-2 border-t border-white/[0.06] px-2 py-2 text-left ${tool === 'canvas' ? 'bg-blue-500/15 text-blue-100' : 'text-zinc-300 hover:bg-white/[0.05]'}`}
                title="调整画布大小和背景"
              >
                <span className="grid h-8 w-8 shrink-0 place-items-center rounded border border-white/10" style={{ background: doc.bg || 'repeating-conic-gradient(#3a3a3a 0% 25%, #2a2a2a 0% 50%) 50% / 8px 8px' }}>
                  <Frame className="h-4 w-4 text-zinc-400 mix-blend-difference" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-xs font-semibold">画布</span>
                  <span className="block truncate font-mono text-[10px] text-zinc-500">{canvasLabel} · {bgLabel}</span>
                </span>
              </button>
            </aside>
          ) : null}
        </div>

        <div data-id="image-annotator-footer" className="flex shrink-0 items-end gap-2 border-t border-white/[0.06] px-4 py-2.5">
          <textarea
            ref={promptRef}
            data-id="image-annotator-prompt"
            rows={1}
            autoFocus
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
