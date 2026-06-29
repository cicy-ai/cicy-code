import { useCallback, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { cn } from '../../lib/utils'

type TipBelowProps = {
  label: ReactNode
  className?: string
  children: ReactNode
}

// TipBelow wraps a trigger (typically an icon button) and shows a tooltip
// BELOW it on hover. The tooltip is rendered to document.body via a portal with
// position:fixed, so it ESCAPES any ancestor `overflow` clipping (e.g. the
// horizontally-scrollable tab row) — a pure CSS top-full tooltip would be cut
// off there. Replaces native `title` so we control direction + styling.
export default function TipBelow({ label, className, children }: TipBelowProps) {
  const ref = useRef<HTMLSpanElement>(null)
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null)

  const show = useCallback(() => {
    const el = ref.current
    if (!el) return
    const r = el.getBoundingClientRect()
    setPos({ x: r.left + r.width / 2, y: r.bottom })
  }, [])
  const hide = useCallback(() => setPos(null), [])

  return (
    <span
      ref={ref}
      data-id="tip-below"
      className={cn('relative inline-flex', className)}
      onMouseEnter={show}
      onMouseLeave={hide}
    >
      {children}
      {pos !== null && createPortal(
        <span
          data-id="tip-below-content"
          role="tooltip"
          style={{ position: 'fixed', left: pos.x, top: pos.y + 6, transform: 'translateX(-50%)', zIndex: 10000 }}
          className="pointer-events-none whitespace-nowrap rounded-md bg-zinc-900 px-2 py-1 text-[11px] font-medium leading-none text-zinc-100 shadow-lg ring-1 ring-white/10"
        >
          {label}
        </span>,
        document.body,
      )}
    </span>
  )
}
