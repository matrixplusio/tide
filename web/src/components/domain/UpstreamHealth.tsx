import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { StatusDot } from '../ui'
import type { UpstreamStatus } from '../../lib/types'

/**
 * Upstream reachability, in as few lines as it can be said.
 *
 * It used to be one line per system per upstream — "Kargo · onprem up" six times
 * over for three upstreams — which is six lines to say nothing, and buries
 * the one line that matters on the day something breaks. Now it is a single
 * quiet line while everything answers, and lists only what does not.
 */
export function UpstreamHealth({ upstreams, detailPath }: { upstreams: UpstreamStatus[]; detailPath?: string }) {
  const { t } = useTranslation()
  if (upstreams.length === 0) return null
  const down = upstreams.flatMap((u) => [
    ...(u.kargoOk ? [] : [{ key: `${u.name}-kargo`, label: `Kargo · ${u.name}`, error: u.kargoError }]),
    ...(u.argocdOk ? [] : [{ key: `${u.name}-argocd`, label: `Argo CD · ${u.name}`, error: u.argocdError }]),
  ])
  const rest = upstreams.length * 2 - down.length
  const rows =
    down.length === 0 ? (
      <div className="hrow">
        <StatusDot state="ok" />
        {t('nav.upstreamsAllOk', { count: upstreams.length })}
      </div>
    ) : (
      <>
        {down.map((d) => (
          <div key={d.key} className="hrow bad" title={d.error}>
            <StatusDot state="bad" />
            <span className="ellipsis">{d.label}</span>
          </div>
        ))}
        {rest > 0 && <div className="hrow">{t('nav.upstreamsRestOk', { count: rest })}</div>}
      </>
    )
  // Summarising means the detail has to live somewhere reachable, and a
  // tooltip is not somewhere: this links to the page that shows all of it.
  if (!detailPath) return rows
  return (
    <Link to={detailPath} className="hlink" title={t('nav.upstreamsDetail')}>
      {rows}
    </Link>
  )
}
