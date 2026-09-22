import type { ReactNode } from 'react'

// Plain CSS charts. A charting library would be a large dependency for four
// shapes, and these carry their numbers as text anyway, which is what people
// read off them.

/** Stat is one headline number with the sentence that makes it mean
 *  something. The caption is not decoration: a percentage without what it is
 *  a percentage of invites the wrong conclusion.
 *
 *  It reuses the overview page's .card, so the two pages state a number the
 *  same way and the cards read as separate things rather than as one block
 *  divided by hairlines. */
export function Stat({
  label,
  value,
  caption,
  tone,
}: {
  label: string
  value: ReactNode
  caption?: ReactNode
  tone?: 'ok' | 'warn' | 'bad'
}) {
  return (
    <div className="card">
      <div className="k">{label}</div>
      <div className={['n', tone].filter(Boolean).join(' ')}>{value}</div>
      {caption !== undefined && <div className="cap">{caption}</div>}
    </div>
  )
}

export interface BarDatum {
  label: string
  value: number
  /** Shown to the right of the bar; defaults to the value. */
  text?: string
  tone?: 'ok' | 'warn' | 'bad' | 'accent'
  title?: string
}

/** Bars is a horizontal bar list: the shape most of these questions want,
 *  and it degrades to a readable list when every value is zero. */
export function Bars({ data, max }: { data: BarDatum[]; max?: number }) {
  const top = max ?? Math.max(1, ...data.map((d) => d.value))
  return (
    <div className="bars">
      {data.map((d) => (
        <div className="bar-row" key={d.label} title={d.title}>
          <span className="bar-label">{d.label}</span>
          <span className="bar-track">
            <span
              className={['bar-fill', d.tone].filter(Boolean).join(' ')}
              style={{ width: `${Math.round((d.value / top) * 100)}%` }}
            />
          </span>
          <span className="bar-value">{d.text ?? d.value}</span>
        </div>
      ))}
    </div>
  )
}

/** Split is one bar divided into parts — used where the parts sum to a whole
 *  (succeeded / failed / cancelled), which a set of separate bars would hide. */
export function Split({ parts }: { parts: { label: string; value: number; tone: string }[] }) {
  const total = parts.reduce((n, p) => n + p.value, 0)
  if (total === 0) return null
  return (
    <>
      <div className="split">
        {parts
          .filter((p) => p.value > 0)
          .map((p) => (
            <span
              key={p.label}
              className={`split-part ${p.tone}`}
              style={{ width: `${(p.value / total) * 100}%` }}
              title={`${p.label} ${p.value}`}
            />
          ))}
      </div>
      <div className="split-legend">
        {parts
          .filter((p) => p.value > 0)
          .map((p) => (
            <span key={p.label}>
              <i className={`dot ${p.tone}`} />
              {p.label} {p.value}
            </span>
          ))}
      </div>
    </>
  )
}

/** Heat is the weekday × hour grid. Colour carries the magnitude, but every
 *  cell also states its count in the tooltip — colour alone is not an
 *  accessible encoding. */
export function Heat({
  cells,
  dayNames,
  cellTitle,
}: {
  cells: Map<string, number>
  dayNames: string[]
  cellTitle: (day: string, hour: number, n: number) => string
}) {
  const max = Math.max(1, ...cells.values())
  return (
    <div className="heat">
      <div className="heat-hours" aria-hidden>
        <span />
        {Array.from({ length: 24 }, (_, h) => (
          <span key={h}>{h % 6 === 0 ? h : ''}</span>
        ))}
      </div>
      {dayNames.map((name, day) => (
        <div className="heat-row" key={name}>
          <span className="heat-day">{name}</span>
          {Array.from({ length: 24 }, (_, hour) => {
            const n = cells.get(`${day}-${hour}`) ?? 0
            return (
              <span
                key={hour}
                className="heat-cell"
                title={cellTitle(name, hour, n)}
                style={{ opacity: n === 0 ? undefined : 0.25 + 0.75 * (n / max) }}
                data-on={n > 0 ? '' : undefined}
              />
            )
          })}
        </div>
      ))}
    </div>
  )
}
