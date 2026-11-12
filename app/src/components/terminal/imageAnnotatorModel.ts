// SPDX-License-Identifier: Apache-2.0
//
// Pure model for the image editor: shape types, geometry and rendering. All
// coordinates are "world" units = pixels of the pasted image, whose top-left
// corner is (0, 0). The canvas (the exported area) is a box in that space and
// may be smaller than the image (a crop) or larger (extra room for notes).

export type Box = { x: number; y: number; w: number; h: number }
export type Point = { x: number; y: number }

export type Shape =
  | ({ type: 'rect' | 'ellipse' | 'overlay'; color: string; width: number } & Box)
  | ({ type: 'mosaic'; block: number } & Box)
  | { type: 'arrow'; x1: number; y1: number; x2: number; y2: number; color: string; width: number }
  | { type: 'pen'; points: number[]; color: string; width: number }
  | { type: 'text'; x: number; y: number; text: string; color: string; size: number }
  | { type: 'number'; x: number; y: number; n: number; color: string; size: number }
  | ({ type: 'image'; img: HTMLImageElement } & Box)

/** A shape on the layer stack. Index 0 sits right above the pasted image. */
export type Layer = Shape & { id: string; hidden?: boolean }

export type Doc = {
  shapes: Layer[]
  canvas: Box
  /** Canvas background colour, or null for transparent. */
  bg: string | null
  baseHidden: boolean
}

export type Handle = 'nw' | 'n' | 'ne' | 'e' | 'se' | 's' | 'sw' | 'w' | 'p1' | 'p2'
export const BOX_HANDLES: Handle[] = ['nw', 'n', 'ne', 'e', 'se', 's', 'sw', 'w']

export const CANVAS_MIN = 8
export const CANVAS_MAX = 8192

export const LAYER_LABEL: Record<Shape['type'], string> = {
  rect: '方框', ellipse: '椭圆', overlay: '高亮', mosaic: '马赛克', arrow: '箭头', pen: '画笔', text: '文字', number: '序号', image: '图片',
}
export const LAYER_KIND: Record<Shape['type'], string> = {
  rect: '形状', ellipse: '形状', overlay: '形状', mosaic: '遮挡', arrow: '形状', pen: '手绘', text: '文字', number: '标记', image: '图片',
}

/** Display names per layer: text shows its content, others "方框 2". */
export function layerNames(layers: Array<{ id: string; type: Shape['type']; text?: string; n?: number }>): Record<string, string> {
  const counts: Record<string, number> = {}
  const names: Record<string, string> = {}
  for (const layer of layers) {
    counts[layer.type] = (counts[layer.type] || 0) + 1
    if (layer.type === 'text' && layer.text) names[layer.id] = layer.text.split('\n')[0]
    else if (layer.type === 'number' && layer.n) names[layer.id] = `${LAYER_LABEL.number} ${layer.n}`
    else names[layer.id] = `${LAYER_LABEL[layer.type]} ${counts[layer.type]}`
  }
  return names
}

export function normalizeBox(x1: number, y1: number, x2: number, y2: number): Box {
  return { x: Math.min(x1, x2), y: Math.min(y1, y2), w: Math.abs(x2 - x1), h: Math.abs(y2 - y1) }
}

export function annotatedFileName(name: string): string {
  const base = (name || '').replace(/\.[^./\\]+$/, '') || `pasted-${Date.now()}`
  return `${base}.png`
}

export function isBoxShape(s: Shape): s is Extract<Shape, Box> {
  return s.type === 'rect' || s.type === 'ellipse' || s.type === 'overlay' || s.type === 'mosaic' || s.type === 'image'
}

function hexAlpha(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

/** True for colours light enough that white ink on top would be unreadable. */
export function isLightColor(hex: string): boolean {
  const n = parseInt(hex.slice(1), 16)
  if (Number.isNaN(n)) return false
  return (((n >> 16) & 255) * 299 + ((n >> 8) & 255) * 587 + (n & 255) * 114) / 1000 > 160
}

const TEXT_FONT = (size: number, weight = 600) => `${weight} ${size}px system-ui, sans-serif`
export const numberRadius = (size: number) => size * 0.72

function textBox(ctx: CanvasRenderingContext2D | null, s: Extract<Shape, { type: 'text' }>): Box {
  if (ctx) ctx.font = TEXT_FONT(s.size)
  const lines = s.text.split('\n')
  const w = Math.max(...lines.map((line) => (ctx ? ctx.measureText(line).width : [...line].length * s.size * 0.6)))
  return { x: s.x, y: s.y, w, h: lines.length * s.size * 1.25 }
}

/** Bounding box of a shape, in world units. */
export function shapeBox(ctx: CanvasRenderingContext2D | null, s: Shape): Box {
  if (s.type === 'arrow') {
    const pad = s.width * 3
    const b = normalizeBox(s.x1, s.y1, s.x2, s.y2)
    return { x: b.x - pad, y: b.y - pad, w: b.w + pad * 2, h: b.h + pad * 2 }
  }
  if (s.type === 'pen') {
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
    for (let i = 0; i + 1 < s.points.length; i += 2) {
      minX = Math.min(minX, s.points[i]); maxX = Math.max(maxX, s.points[i])
      minY = Math.min(minY, s.points[i + 1]); maxY = Math.max(maxY, s.points[i + 1])
    }
    if (!Number.isFinite(minX)) return { x: 0, y: 0, w: 0, h: 0 }
    const pad = s.width / 2
    return { x: minX - pad, y: minY - pad, w: maxX - minX + pad * 2, h: maxY - minY + pad * 2 }
  }
  if (s.type === 'text') return textBox(ctx, s)
  if (s.type === 'number') {
    const r = numberRadius(s.size)
    return { x: s.x - r, y: s.y - r, w: r * 2, h: r * 2 }
  }
  return normalizeBox(s.x, s.y, s.x + s.w, s.y + s.h)
}

export function moveShape<T extends Shape>(s: T, dx: number, dy: number): T {
  if (s.type === 'arrow') return { ...s, x1: s.x1 + dx, y1: s.y1 + dy, x2: s.x2 + dx, y2: s.y2 + dy }
  if (s.type === 'pen') return { ...s, points: s.points.map((v, i) => v + (i % 2 === 0 ? dx : dy)) }
  return { ...s, x: (s as unknown as Point).x + dx, y: (s as unknown as Point).y + dy }
}

/**
 * Drag one handle of a box by (dx, dy). The opposite side stays put; the box
 * never gets smaller than `min` and never flips. `keepRatio` (corners only)
 * preserves the original aspect ratio.
 */
export function resizeBox(orig: Box, handle: Handle, dx: number, dy: number, opts: { keepRatio?: boolean; min?: number } = {}): Box {
  const min = opts.min ?? 1
  let x1 = orig.x, y1 = orig.y, x2 = orig.x + orig.w, y2 = orig.y + orig.h
  const west = handle.includes('w'), east = handle.includes('e'), north = handle.includes('n'), south = handle.includes('s')
  if (west) x1 += dx
  if (east) x2 += dx
  if (north) y1 += dy
  if (south) y2 += dy
  if (x2 - x1 < min) { if (west) x1 = x2 - min; else x2 = x1 + min }
  if (y2 - y1 < min) { if (north) y1 = y2 - min; else y2 = y1 + min }
  if (opts.keepRatio && (west || east) && (north || south) && orig.w > 0 && orig.h > 0) {
    const ratio = orig.w / orig.h
    let w = x2 - x1, h = y2 - y1
    if (w / orig.w > h / orig.h) h = w / ratio
    else w = h * ratio
    if (west) x1 = x2 - w; else x2 = x1 + w
    if (north) y1 = y2 - h; else y2 = y1 + h
  }
  return { x: x1, y: y1, w: x2 - x1, h: y2 - y1 }
}

/** Where a handle sits on a box. */
export function handlePoint(b: Box, handle: Handle): Point {
  const x = handle.includes('w') ? b.x : handle.includes('e') ? b.x + b.w : b.x + b.w / 2
  const y = handle.includes('n') ? b.y : handle.includes('s') ? b.y + b.h : b.y + b.h / 2
  return { x, y }
}

/** Integer canvas box within the allowed size range. */
export function clampCanvas(b: Box): Box {
  const w = Math.min(CANVAS_MAX, Math.max(CANVAS_MIN, Math.round(b.w)))
  const h = Math.min(CANVAS_MAX, Math.max(CANVAS_MIN, Math.round(b.h)))
  return { x: Math.round(b.x), y: Math.round(b.y), w, h }
}

/** Resize a canvas to w×h around its centre. */
export function resizeCanvasCentered(c: Box, w: number, h: number): Box {
  return clampCanvas({ x: c.x - (w - c.w) / 2, y: c.y - (h - c.h) / 2, w, h })
}

/** Grow (never shrink) a canvas around its centre until it has ratio rw:rh. */
export function expandToRatio(c: Box, rw: number, rh: number): Box {
  const target = rw / rh
  if (c.w / c.h < target) return resizeCanvasCentered(c, c.h * target, c.h)
  return resizeCanvasCentered(c, c.w, c.w / target)
}

export function padBox(c: Box, pad: number): Box {
  return clampCanvas({ x: c.x - pad, y: c.y - pad, w: c.w + pad * 2, h: c.h + pad * 2 })
}

function union(a: Box, b: Box): Box {
  const x = Math.min(a.x, b.x), y = Math.min(a.y, b.y)
  return { x, y, w: Math.max(a.x + a.w, b.x + b.w) - x, h: Math.max(a.y + a.h, b.y + b.h) - y }
}

/** Smallest canvas that holds the image and every visible layer. */
export function contentBounds(ctx: CanvasRenderingContext2D | null, imageW: number, imageH: number, doc: Pick<Doc, 'shapes' | 'baseHidden'>): Box {
  let out: Box | null = doc.baseHidden ? null : { x: 0, y: 0, w: imageW, h: imageH }
  for (const layer of doc.shapes) {
    if (layer.hidden) continue
    const b = shapeBox(ctx, layer)
    out = out ? union(out, b) : b
  }
  const b = out || { x: 0, y: 0, w: imageW, h: imageH }
  const x = Math.floor(b.x), y = Math.floor(b.y)
  return clampCanvas({ x, y, w: Math.ceil(b.x + b.w) - x, h: Math.ceil(b.y + b.h) - y })
}

export function sameBox(a: Box, b: Box): boolean {
  return a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h
}

/** The view that shows the whole canvas centred in a stage of the given size. */
export function fitView(canvas: Box, stageW: number, stageH: number, pad = 56, maxZoom = 3): { zoom: number; x: number; y: number } {
  if (!stageW || !stageH) return { zoom: 1, x: -canvas.x, y: -canvas.y }
  const zoom = Math.max(0.02, Math.min(maxZoom, (stageW - pad * 2) / canvas.w, (stageH - pad * 2) / canvas.h))
  return { zoom, x: (stageW - canvas.w * zoom) / 2 - canvas.x * zoom, y: (stageH - canvas.h * zoom) / 2 - canvas.y * zoom }
}

// Pixelate the part of the target canvas under the box. Works on what has
// been drawn so far, so it hides the image and every layer below it.
function drawMosaic(ctx: CanvasRenderingContext2D, s: Extract<Shape, { type: 'mosaic' }>, origin: Point) {
  const target = ctx.canvas
  const b = normalizeBox(s.x, s.y, s.x + s.w, s.y + s.h)
  const x1 = Math.max(0, Math.round(b.x - origin.x)), y1 = Math.max(0, Math.round(b.y - origin.y))
  const x2 = Math.min(target.width, Math.round(b.x + b.w - origin.x)), y2 = Math.min(target.height, Math.round(b.y + b.h - origin.y))
  const w = x2 - x1, h = y2 - y1
  if (w < 1 || h < 1 || typeof document === 'undefined') return
  const block = Math.max(2, s.block)
  const tw = Math.max(1, Math.round(w / block)), th = Math.max(1, Math.round(h / block))
  const tmp = document.createElement('canvas')
  tmp.width = tw
  tmp.height = th
  const t = tmp.getContext('2d')
  if (!t) return
  t.imageSmoothingEnabled = true
  t.drawImage(target, x1, y1, w, h, 0, 0, tw, th)
  ctx.save()
  ctx.setTransform(1, 0, 0, 1, 0, 0)
  ctx.imageSmoothingEnabled = false
  ctx.drawImage(tmp, 0, 0, tw, th, x1, y1, w, h)
  ctx.restore()
}

/** Draw one shape. `origin` is the world position of the target's (0, 0). */
export function drawShape(ctx: CanvasRenderingContext2D, s: Shape, origin: Point = { x: 0, y: 0 }) {
  if (s.type === 'mosaic') { drawMosaic(ctx, s, origin); return }
  ctx.save()
  if (s.type === 'rect') {
    ctx.strokeStyle = s.color
    ctx.lineWidth = s.width
    ctx.lineJoin = 'round'
    const b = normalizeBox(s.x, s.y, s.x + s.w, s.y + s.h)
    ctx.strokeRect(b.x, b.y, b.w, b.h)
  } else if (s.type === 'ellipse') {
    const b = normalizeBox(s.x, s.y, s.x + s.w, s.y + s.h)
    ctx.strokeStyle = s.color
    ctx.lineWidth = s.width
    ctx.beginPath()
    ctx.ellipse(b.x + b.w / 2, b.y + b.h / 2, b.w / 2, b.h / 2, 0, 0, Math.PI * 2)
    ctx.stroke()
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
  } else if (s.type === 'pen') {
    if (s.points.length >= 4) {
      ctx.strokeStyle = s.color
      ctx.lineWidth = s.width
      ctx.lineCap = 'round'
      ctx.lineJoin = 'round'
      ctx.beginPath()
      ctx.moveTo(s.points[0], s.points[1])
      for (let i = 2; i + 1 < s.points.length; i += 2) ctx.lineTo(s.points[i], s.points[i + 1])
      ctx.stroke()
    }
  } else if (s.type === 'text') {
    ctx.font = TEXT_FONT(s.size)
    ctx.textBaseline = 'top'
    ctx.lineJoin = 'round'
    ctx.lineWidth = Math.max(2, s.size / 7)
    // Dark/light outline keeps the label readable on any background.
    ctx.strokeStyle = isLightColor(s.color) ? 'rgba(0,0,0,0.7)' : 'rgba(255,255,255,0.85)'
    ctx.fillStyle = s.color
    s.text.split('\n').forEach((line, i) => {
      ctx.strokeText(line, s.x, s.y + i * s.size * 1.25)
      ctx.fillText(line, s.x, s.y + i * s.size * 1.25)
    })
  } else if (s.type === 'number') {
    const r = numberRadius(s.size)
    const light = isLightColor(s.color)
    ctx.beginPath()
    ctx.arc(s.x, s.y, r, 0, Math.PI * 2)
    ctx.fillStyle = s.color
    ctx.fill()
    ctx.lineWidth = Math.max(1.5, s.size / 12)
    ctx.strokeStyle = light ? 'rgba(0,0,0,0.55)' : 'rgba(255,255,255,0.9)'
    ctx.stroke()
    ctx.font = TEXT_FONT(s.n > 9 ? s.size * 0.8 : s.size, 700)
    ctx.textAlign = 'center'
    ctx.textBaseline = 'middle'
    ctx.fillStyle = light ? '#111111' : '#ffffff'
    ctx.fillText(String(s.n), s.x, s.y + s.size * 0.04)
  } else if (s.type === 'image') {
    ctx.drawImage(s.img, s.x, s.y, s.w, s.h)
  }
  ctx.restore()
}

/** Paint the whole document onto a context whose (0, 0) is the canvas corner. */
export function renderDocument(
  ctx: CanvasRenderingContext2D,
  doc: Pick<Doc, 'canvas' | 'bg' | 'baseHidden'>,
  base: CanvasImageSource,
  shapes: Array<Shape & { hidden?: boolean }>,
) {
  const origin = { x: doc.canvas.x, y: doc.canvas.y }
  ctx.save()
  ctx.setTransform(1, 0, 0, 1, 0, 0)
  ctx.clearRect(0, 0, doc.canvas.w, doc.canvas.h)
  if (doc.bg) {
    ctx.fillStyle = doc.bg
    ctx.fillRect(0, 0, doc.canvas.w, doc.canvas.h)
  }
  ctx.translate(-origin.x, -origin.y)
  if (!doc.baseHidden) ctx.drawImage(base, 0, 0)
  shapes.forEach((s) => { if (!s.hidden) drawShape(ctx, s, origin) })
  ctx.restore()
}
