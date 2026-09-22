import type { CSSProperties, ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ErrorText } from './States'

export type BannerTone = 'info' | 'warn' | 'bad' | 'ok'

export function Banner({ tone = 'info', children, action, to, style }: { tone?: BannerTone; children: ReactNode; action?: ReactNode; to?: string; style?: CSSProperties }) {
  const c = `banner ${tone === 'info' ? '' : tone}`
  const role = tone === 'bad' ? 'alert' : undefined
  if (to)
    return (
      <Link className={c} to={to} style={style}>
        {children}
      </Link>
    )
  return (
    <div className={c} role={role} style={style}>
      <span className="grow">{children}</span>
      {action && <span className="banner-action">{action}</span>}
    </div>
  )
}

/** Form-level error: whatever could not be attached to a field. */
export function FormErrorBanner({ error }: { error: unknown }) {
  if (!error) return null
  return (
    <Banner tone="bad">
      <ErrorText error={error} />
    </Banner>
  )
}
