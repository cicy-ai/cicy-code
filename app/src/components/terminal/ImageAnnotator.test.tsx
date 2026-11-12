// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import ImageAnnotator, { annotatedFileName, normalizeBox } from './ImageAnnotator'
import { contentBounds, expandToRatio, fitView, layerNames, padBox, resizeBox } from './imageAnnotatorModel'

class FakeImage {
  naturalWidth = 640
  naturalHeight = 480
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  set src(_value: string) { queueMicrotask(() => this.onload?.()) }
}

describe('ImageAnnotator', () => {
  const OriginalImage = globalThis.Image
  beforeEach(() => {
    ;(globalThis as any).Image = FakeImage
    URL.createObjectURL = vi.fn(() => 'blob:fake')
    URL.revokeObjectURL = vi.fn()
    // jsdom has no canvas backend; the editor must tolerate a null context.
    HTMLCanvasElement.prototype.getContext = vi.fn(() => null) as any
  })
  afterEach(() => { (globalThis as any).Image = OriginalImage })

  const file = new File([new Uint8Array([1, 2, 3])], 'shot.jpg', { type: 'image/jpeg' })

  it('loads the pasted image and offers all editing tools', async () => {
    render(<ImageAnnotator file={file} onDone={vi.fn()} />)
    await waitFor(() => expect(document.querySelector('[data-id="image-annotator-canvas"]')).toBeInTheDocument())
    for (const tool of ['move', 'canvas', 'crop', 'rect', 'ellipse', 'arrow', 'pen', 'text', 'number', 'overlay', 'mosaic', 'image']) {
      expect(document.querySelector(`[data-id="image-annotator-tool-${tool}"]`)).toBeInTheDocument()
    }
    expect(screen.getByText(/640×480/)).toBeInTheDocument()
  })

  it('sends the original, cancels, and passes an unedited image through unchanged', async () => {
    const onDone = vi.fn()
    render(<ImageAnnotator file={file} onDone={onDone} />)
    await waitFor(() => expect(document.querySelector('[data-id="image-annotator-canvas"]')).toBeInTheDocument())

    fireEvent.click(document.querySelector('[data-id="image-annotator-original"]')!)
    expect(onDone).toHaveBeenLastCalledWith(file, undefined)
    fireEvent.click(document.querySelector('[data-id="image-annotator-done"]')!)
    expect(onDone).toHaveBeenLastCalledWith(file, undefined)
    fireEvent.click(document.querySelector('[data-id="image-annotator-cancel"]')!)
    expect(onDone).toHaveBeenLastCalledWith(null)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onDone).toHaveBeenLastCalledWith(null)
  })

  it('adds text: the box survives the opening click and commits on Enter', async () => {
    render(<ImageAnnotator file={file} onDone={vi.fn()} />)
    const canvas = await waitFor(() => {
      const node = document.querySelector('[data-id="image-annotator-canvas"]')
      if (!node) throw new Error('canvas missing')
      return node as HTMLCanvasElement
    })
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 640, height: 480, right: 640, bottom: 480, x: 0, y: 0, toJSON: () => ({}) })
    fireEvent.click(document.querySelector('[data-id="image-annotator-tool-text"]')!)
    fireEvent.pointerDown(canvas, { button: 0, clientX: 100, clientY: 80 })

    const input = await waitFor(() => {
      const node = document.querySelector('[data-id="image-annotator-text-input"]')
      if (!node) throw new Error('text box missing')
      return node as HTMLTextAreaElement
    })
    // Edit box uses the same font size as the committed text (640px image shown at 1:1 → 18px).
    expect(input.style.fontSize).toBe('18px')
    expect(input.style.fontWeight).toBe('600')
    // The opening click's focus bounce must not close the empty box.
    fireEvent.blur(input)
    expect(document.querySelector('[data-id="image-annotator-text-input"]')).toBeInTheDocument()

    const undo = document.querySelector('[data-id="image-annotator-undo"]') as HTMLButtonElement
    expect(undo).toBeDisabled()
    fireEvent.change(input, { target: { value: '这里有问题' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    await waitFor(() => expect(document.querySelector('[data-id="image-annotator-text-input"]')).not.toBeInTheDocument())
    expect(undo).not.toBeDisabled()
  })

  it('lists layers in the side panel and supports hide, reorder, delete and undo', async () => {
    render(<ImageAnnotator file={file} onDone={vi.fn()} />)
    const canvas = await waitFor(() => {
      const node = document.querySelector('[data-id="image-annotator-canvas"]')
      if (!node) throw new Error('canvas missing')
      return node as HTMLCanvasElement
    })
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 640, height: 480, right: 640, bottom: 480, x: 0, y: 0, toJSON: () => ({}) })
    expect(document.querySelector('[data-id="image-annotator-layer-base"]')).toBeInTheDocument()

    const addText = async (value: string, x: number) => {
      fireEvent.click(document.querySelector('[data-id="image-annotator-tool-text"]')!)
      fireEvent.pointerDown(canvas, { button: 0, clientX: x, clientY: 40 })
      const input = await waitFor(() => {
        const node = document.querySelector('[data-id="image-annotator-text-input"]')
        if (!node) throw new Error('text box missing')
        return node as HTMLTextAreaElement
      })
      fireEvent.change(input, { target: { value } })
      fireEvent.keyDown(input, { key: 'Enter' })
    }
    await addText('第一', 50)
    await addText('第二', 200)

    const rows = () => [...document.querySelectorAll('[data-id^="image-annotator-layer-layer-"]')]
    await waitFor(() => expect(rows().map((row) => row.textContent)).toEqual([
      expect.stringContaining('第二'),
      expect.stringContaining('第一'),
    ]))
    // newest layer is selected
    expect(rows()[0]).toHaveAttribute('aria-selected', 'true')

    const topId = rows()[0].getAttribute('data-id')!.replace('image-annotator-layer-', '')
    fireEvent.click(document.querySelector(`[data-id="image-annotator-layer-visibility-${topId}"]`)!)
    expect(document.querySelector(`[data-id="image-annotator-layer-visibility-${topId}"]`)).toHaveAttribute('aria-pressed', 'false')

    fireEvent.click(document.querySelector(`[data-id="image-annotator-layer-down-${topId}"]`)!)
    expect(rows()[1].getAttribute('data-id')).toBe(`image-annotator-layer-${topId}`)

    fireEvent.click(document.querySelector(`[data-id="image-annotator-layer-delete-${topId}"]`)!)
    expect(rows()).toHaveLength(1)

    fireEvent.click(document.querySelector('[data-id="image-annotator-undo"]')!)
    expect(rows()).toHaveLength(2)
  })

  it('re-selects existing text on the canvas, edits it on double-click, and deletes it with Delete', async () => {
    render(<ImageAnnotator file={file} onDone={vi.fn()} />)
    const canvas = await waitFor(() => {
      const node = document.querySelector('[data-id="image-annotator-canvas"]')
      if (!node) throw new Error('canvas missing')
      return node as HTMLCanvasElement
    })
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 640, height: 480, right: 640, bottom: 480, x: 0, y: 0, toJSON: () => ({}) })
    const rows = () => [...document.querySelectorAll('[data-id^="image-annotator-layer-layer-"]')]
    const textInput = () => document.querySelector('[data-id="image-annotator-text-input"]') as HTMLTextAreaElement | null

    fireEvent.click(document.querySelector('[data-id="image-annotator-tool-text"]')!)
    fireEvent.pointerDown(canvas, { button: 0, clientX: 50, clientY: 40 })
    await waitFor(() => expect(textInput()).toBeInTheDocument())
    fireEvent.change(textInput()!, { target: { value: '标注' } })
    fireEvent.keyDown(textInput()!, { key: 'Enter' })
    await waitFor(() => expect(rows()).toHaveLength(1))

    // Deselect by clicking empty space with the move tool.
    fireEvent.click(document.querySelector('[data-id="image-annotator-tool-move"]')!)
    fireEvent.pointerDown(canvas, { button: 0, clientX: 500, clientY: 400 })
    fireEvent.pointerUp(canvas)
    expect(rows()[0]).toHaveAttribute('aria-selected', 'false')

    // With the text tool, clicking the existing text selects it — no new box.
    fireEvent.click(document.querySelector('[data-id="image-annotator-tool-text"]')!)
    fireEvent.pointerDown(canvas, { button: 0, clientX: 55, clientY: 45 })
    fireEvent.pointerUp(canvas)
    expect(textInput()).not.toBeInTheDocument()
    expect(rows()[0]).toHaveAttribute('aria-selected', 'true')

    // Double-click edits in place.
    fireEvent.doubleClick(canvas, { clientX: 55, clientY: 45 })
    await waitFor(() => expect(textInput()).toHaveValue('标注'))
    expect(textInput()!.style.fontSize).toBe('18px')
    fireEvent.change(textInput()!, { target: { value: '改过' } })
    fireEvent.keyDown(textInput()!, { key: 'Enter' })
    await waitFor(() => expect(textInput()).not.toBeInTheDocument())
    expect(rows()).toHaveLength(1)
    expect(rows()[0].textContent).toContain('改过')

    // Delete removes the selected text; undo brings it back.
    fireEvent.keyDown(window, { key: 'Delete' })
    expect(rows()).toHaveLength(0)
    fireEvent.click(document.querySelector('[data-id="image-annotator-undo"]')!)
    expect(rows()).toHaveLength(1)
  })

  it('passes the footer prompt along with the image, and Enter in the prompt sends', async () => {
    const onDone = vi.fn()
    render(<ImageAnnotator file={file} onDone={onDone} />)
    await waitFor(() => expect(document.querySelector('[data-id="image-annotator-canvas"]')).toBeInTheDocument())
    const prompt = document.querySelector('[data-id="image-annotator-prompt"]') as HTMLTextAreaElement
    expect(document.querySelector('[data-id="image-annotator-done"]')?.textContent).toBe('完成并发送')

    fireEvent.change(prompt, { target: { value: '  帮我看下  ' } })
    expect(document.querySelector('[data-id="image-annotator-done"]')?.textContent).toBe('发送')
    fireEvent.click(document.querySelector('[data-id="image-annotator-original"]')!)
    expect(onDone).toHaveBeenLastCalledWith(file, '帮我看下')

    fireEvent.keyDown(prompt, { key: 'Enter' })
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(2))
    expect(onDone).toHaveBeenLastCalledWith(file, '帮我看下')

    // Esc while typing only leaves the field instead of cancelling the editor.
    fireEvent.keyDown(prompt, { key: 'Escape' })
    expect(onDone).toHaveBeenCalledTimes(2)
  })

  it('focuses the note field as soon as the editor opens', async () => {
    render(<ImageAnnotator file={file} onDone={vi.fn()} />)
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector('[data-id="image-annotator-prompt"]')))
  })

  it('resizes the canvas from the canvas tool, with undo and redo', async () => {
    render(<ImageAnnotator file={file} onDone={vi.fn()} />)
    await waitFor(() => expect(document.querySelector('[data-id="image-annotator-canvas"]')).toBeInTheDocument())
    const size = () => document.querySelector('[data-id="image-annotator-canvas-size"]')!.textContent
    const q = (id: string) => document.querySelector(`[data-id="image-annotator-${id}"]`) as HTMLElement
    expect(size()).toBe('640 × 480')

    // The size chip is a shortcut to the canvas tool.
    fireEvent.click(q('canvas-size'))
    expect(q('tool-canvas')).toHaveAttribute('aria-pressed', 'true')
    expect(q('canvas-preset-original')).toBeDisabled()

    const width = q('canvas-width') as HTMLInputElement
    fireEvent.change(width, { target: { value: '800' } })
    fireEvent.keyDown(width, { key: 'Enter' })
    expect(size()).toBe('800 × 480')
    fireEvent.click(q('canvas-ratio-1-1'))
    expect(size()).toBe('800 × 800')
    fireEvent.click(q('canvas-bg-white'))
    expect(q('layer-canvas').textContent).toContain('白色')

    fireEvent.click(q('undo'))
    fireEvent.click(q('undo'))
    expect(size()).toBe('800 × 480')
    fireEvent.click(q('redo'))
    expect(size()).toBe('800 × 800')
    fireEvent.click(q('canvas-preset-original'))
    expect(size()).toBe('640 × 480')
  })

  it('drags a canvas handle to make room, and places numbered markers', async () => {
    render(<ImageAnnotator file={file} onDone={vi.fn()} />)
    const canvas = await waitFor(() => {
      const node = document.querySelector('[data-id="image-annotator-canvas"]')
      if (!node) throw new Error('canvas missing')
      return node as HTMLCanvasElement
    })
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 640, height: 480, right: 640, bottom: 480, x: 0, y: 0, toJSON: () => ({}) })
    const size = () => document.querySelector('[data-id="image-annotator-canvas-size"]')!.textContent

    fireEvent.click(document.querySelector('[data-id="image-annotator-tool-canvas"]')!)
    // East handle sits at the middle of the right edge (640, 240).
    fireEvent.pointerDown(canvas, { button: 0, clientX: 640, clientY: 240 })
    fireEvent.pointerMove(canvas, { clientX: 840, clientY: 260 })
    fireEvent.pointerUp(canvas)
    expect(size()).toBe('840 × 480')

    fireEvent.click(document.querySelector('[data-id="image-annotator-tool-number"]')!)
    fireEvent.pointerDown(canvas, { button: 0, clientX: 700, clientY: 100 })
    fireEvent.pointerUp(canvas)
    fireEvent.pointerDown(canvas, { button: 0, clientX: 700, clientY: 300 })
    fireEvent.pointerUp(canvas)
    const rows = [...document.querySelectorAll('[data-id^="image-annotator-layer-layer-"]')].map((row) => row.textContent)
    expect(rows).toEqual([expect.stringContaining('序号 2'), expect.stringContaining('序号 1')])
  })

  it('asks for a second Esc before dropping unsent edits', async () => {
    const onDone = vi.fn()
    render(<ImageAnnotator file={file} onDone={onDone} />)
    await waitFor(() => expect(document.querySelector('[data-id="image-annotator-canvas"]')).toBeInTheDocument())
    fireEvent.click(document.querySelector('[data-id="image-annotator-tool-canvas"]')!)
    fireEvent.click(document.querySelector('[data-id="image-annotator-canvas-preset-pad"]')!)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onDone).not.toHaveBeenCalled()
    expect(document.querySelector('[data-id="image-annotator-esc-confirm"]')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onDone).toHaveBeenLastCalledWith(null)
  })

  it('keeps canvas geometry sane', () => {
    // Dragging a handle moves only its side, never flips, and can hold the ratio.
    expect(resizeBox({ x: 0, y: 0, w: 100, h: 50 }, 'e', 40, 99)).toEqual({ x: 0, y: 0, w: 140, h: 50 })
    expect(resizeBox({ x: 0, y: 0, w: 100, h: 50 }, 'nw', -20, -10)).toEqual({ x: -20, y: -10, w: 120, h: 60 })
    expect(resizeBox({ x: 0, y: 0, w: 100, h: 50 }, 'w', 500, 0, { min: 8 })).toEqual({ x: 92, y: 0, w: 8, h: 50 })
    expect(resizeBox({ x: 0, y: 0, w: 100, h: 50 }, 'se', 100, 0, { keepRatio: true })).toEqual({ x: 0, y: 0, w: 200, h: 100 })
    // Ratio presets only ever add room.
    expect(expandToRatio({ x: 0, y: 0, w: 100, h: 50 }, 1, 1)).toEqual({ x: 0, y: -25, w: 100, h: 100 })
    expect(expandToRatio({ x: 0, y: 0, w: 90, h: 160 }, 16, 9).h).toBe(160)
    expect(padBox({ x: 0, y: 0, w: 100, h: 50 }, 10)).toEqual({ x: -10, y: -10, w: 120, h: 70 })
    // "Fit all" covers layers that hang outside the image.
    expect(contentBounds(null, 100, 50, { baseHidden: false, shapes: [{ id: 'a', type: 'rect', x: 90, y: -20, w: 40, h: 30, color: '#fff', width: 2 }] }))
      .toEqual({ x: 0, y: -20, w: 130, h: 70 })
    // A small image is enlarged to fill the stage, a big one shrunk; both centred.
    expect(fitView({ x: 0, y: 0, w: 100, h: 100 }, 1000, 600).zoom).toBe(3)
    const big = fitView({ x: -50, y: 0, w: 2000, h: 1000 }, 1112, 612)
    expect(big.zoom).toBe(0.5)
    expect(big.x).toBe(81)
    expect(layerNames([{ id: 'a', type: 'number', n: 3 }, { id: 'b', type: 'mosaic' }])).toEqual({ a: '序号 3', b: '马赛克 1' })
  })

  it('normalizes drag boxes and names the edited output as png', () => {
    expect(normalizeBox(10, 20, 4, 5)).toEqual({ x: 4, y: 5, w: 6, h: 15 })
    expect(annotatedFileName('shot.jpg')).toBe('shot.png')
    expect(annotatedFileName('')).toMatch(/^pasted-\d+\.png$/)
  })
})
