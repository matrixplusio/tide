import { useTranslation } from 'react-i18next'
import type { Release } from '../../lib/types'
import { fmtTime } from '../../lib/format'
import { Chev, Pill, Row } from '../ui'
import { AutoPill, ReleaseStatusPill } from './labels'

export function ReleaseRow({ r }: { r: Release }) {
  const { t } = useTranslation()
  return (
    <Row to={`/releases/${encodeURIComponent(r.id)}`}>
      <div className="grow">
        <div className="t ellipsis">{r.title || r.id}</div>
        <div className="d">
          <span className="mono">{r.id}</span> · {r.jiraTicket ? <span className="mono">{r.jiraTicket}</span> : <span className="faint">{t('domain.noJira')}</span>} · {r.createdByName} · {fmtTime(r.createdAt)}
        </div>
      </div>
      <span className="pills">
        {r.automatic && <AutoPill />}
        {/* Somebody has to act on it, so it is said in the list, not only on the release. */}
        {r.status === 'executing' && r.items?.some((it) => it.status === 'executing' && it.pausedSince) && <Pill tone="orange">{t('rollout.awaiting')}</Pill>}
        <ReleaseStatusPill status={r.status} />
      </span>
      <Chev />
    </Row>
  )
}
