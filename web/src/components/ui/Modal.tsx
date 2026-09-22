import { useEffect, useId, useRef } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

export interface ModalProps {
  title: ReactNode
  subtitle?: ReactNode
  onClose: () => void
  children: ReactNode
  /** Esc closes unless this is false (e.g. while a request is running). */
  closeOnEsc?: boolean
  width?: number
}

// Esc closes, Tab is trapped inside, focus returns to the trigger on close.
// Clicking the backdrop does nothing on purpose: it would throw away input.
export function Modal({ title, subtitle, onClose, children, closeOnEsc = true, width = 560 }: ModalProps) {
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const onCloseRef = useRef(onClose)
  useEffect(() => {
    onCloseRef.current = onClose
  }, [onClose])

  useEffect(() => {
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const root = ref.current
    // Focus in a frame the cleanup can cancel: StrictMode mounts effects twice,
    // and focusing then handing focus back to the trigger would blur the
    // first field and fire its on-blur validation before anyone typed.
    const frame = requestAnimationFrame(() => {
      const first = root?.querySelector<HTMLElement>('[data-autofocus]') ?? root?.querySelector<HTMLElement>(FOCUSABLE)
      ;(first ?? root)?.focus()
    })
    const prevOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      cancelAnimationFrame(frame)
      document.body.style.overflow = prevOverflow
      if (trigger && document.contains(trigger)) trigger.focus()
    }
  }, [])

  const closeOnEscRef = useRef(closeOnEsc)
  useEffect(() => {
    closeOnEscRef.current = closeOnEsc
  }, [closeOnEsc])

  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => onKeyDown(e, ref.current, closeOnEscRef.current, onCloseRef.current)
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  return createPortal(
    <div className="backdrop">
      <div ref={ref} className="sheet" role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1} style={{ width: `min(${width}px, 100%)` }}>
        <div className="sheet-h">
          <h2 id={titleId}>{title}</h2>
          {subtitle && <div className="muted" style={{ marginTop: 2 }}>{subtitle}</div>}
        </div>
        <div className="sheet-b">{children}</div>
      </div>
    </div>,
    document.body,
  )
}

// Only the top-most dialog reacts, so a modal opened from a modal behaves.
function onKeyDown(e: globalThis.KeyboardEvent, root: HTMLDivElement | null, closeOnEsc: boolean, onClose: () => void) {
  if (!root) return
  const dialogs = document.querySelectorAll('[role="dialog"][aria-modal="true"]')
  if (dialogs[dialogs.length - 1] !== root) return
  if (e.key === 'Escape') {
    e.preventDefault()
    if (closeOnEsc) onClose()
    return
  }
  if (e.key !== 'Tab') return
  const items = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => !el.hidden && el.getClientRects().length > 0)
  if (items.length === 0) {
    e.preventDefault()
    root.focus()
    return
  }
  const first = items[0]!
  const last = items[items.length - 1]!
  const active = document.activeElement
  if (!root.contains(active)) {
    e.preventDefault()
    first.focus()
  } else if (e.shiftKey && (active === first || active === root)) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && active === last) {
    e.preventDefault()
    first.focus()
  }
}
