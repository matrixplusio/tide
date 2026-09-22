import type { CSSProperties } from 'react'

// Shape + color: status must not rely on color alone.
export type DotState = 'ok' | 'run' | 'bad' | 'warn' | 'off'

export function StatusDot({ state, label, style, className }: { state: DotState; label?: string; style?: CSSProperties; className?: string }) {
  return (
    <i
      className={['dot', state, className].filter(Boolean).join(' ')}
      title={label}
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      style={style}
    />
  )
}
