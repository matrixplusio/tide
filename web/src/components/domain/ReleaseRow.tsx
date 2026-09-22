import { useTranslation } from 'react-i18next'
import type { Release } from '../../lib/types'
import { fmtTime } from '../../lib/format'
import { Chev, Row } from '../ui'
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
        <ReleaseStatusPill status={r.status} />
      </span>
      <Chev />
    </Row>
  )
}
