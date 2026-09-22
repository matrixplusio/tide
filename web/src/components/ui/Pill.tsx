import type { ReactNode } from 'react'

export type Tone = 'neutral' | 'green' | 'orange' | 'red' | 'blue'

const toneClass: Record<Tone, string> = { neutral: '', green: 'g', orange: 'o', red: 'r', blue: 'b' }

export function Pill({ tone = 'neutral', children, title }: { tone?: Tone; children: ReactNode; title?: string }) {
  return (
    <span className={`pill ${toneClass[tone]}`} title={title}>
      {children}
    </span>
  )
}
