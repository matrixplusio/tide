import type { KeyboardEvent, ReactNode } from 'react'

export interface SegmentedProps<T extends string> {
  value: T
  options: readonly (readonly [T, ReactNode])[]
  onChange: (v: T) => void
  label: string
}

export function Segmented<T extends string>({ value, options, onChange, label }: SegmentedProps<T>) {
  const onKey = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
    const i = options.findIndex(([v]) => v === value)
    const next = options[(i + (e.key === 'ArrowRight' ? 1 : options.length - 1)) % options.length]
    if (!next) return
    e.preventDefault()
    onChange(next[0])
    const btn = e.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('button')[options.indexOf(next)]
    btn?.focus()
  }
  return (
    <div className="seg" role="tablist" aria-label={label}>
      {options.map(([v, text]) => (
        <button key={v} type="button" className={v === value ? 'on' : ''} role="tab" aria-selected={v === value} tabIndex={v === value ? 0 : -1} onClick={() => onChange(v)} onKeyDown={onKey}>
          {text}
        </button>
      ))}
    </div>
  )
}
