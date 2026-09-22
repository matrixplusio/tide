import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { fmtTime } from '../../../lib/format'
import { EmptyState, ErrorState, Group, Loading, Pager, Row, Segmented, StatusDot } from '../../../components/ui'
import { INTAKES_PAGE_SIZE, useCIIntakes } from '../queries'
import type { IntakeStatus } from '../types'

const FILTERS = [
  ['', 'ci.filterAll'],
  ['waiting', 'ci.filterWaiting'],
  ['released', 'ci.filterReleased'],
  ['failed', 'ci.filterFailed'],
  ['expired', 'ci.filterExpired'],
] as const

// A waiting intake is not a problem: a warehouse discovers an image on its own
// schedule. Only failed and expired are states somebody has to look at.
const DOT: Record<IntakeStatus, 'ok' | 'off' | 'warn' | 'bad'> = {
  waiting: 'warn',
  released: 'ok',
  failed: 'bad',
  expired: 'bad',
}

export function IntakeList() {
  const { t } = useTranslation()
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const intakes = useCIIntakes({ status: status || undefined, page })
  const list = intakes.data?.items ?? []

  const statusLabel = (s: IntakeStatus) =>
    s === 'waiting' ? t('ci.stWaiting') : s === 'released' ? t('ci.stReleased') : s === 'failed' ? t('ci.stFailed') : t('ci.stExpired')

  return (
    <>
      <div className="btnrow filters" style={{ marginTop: 0, marginBottom: 12 }}>
        <Segmented
          label={t('ci.filterLabel')}
          value={status}
          options={FILTERS.map(([v, k]) => [v, t(k)] as const)}
          onChange={(v) => {
            setStatus(v)
            setPage(1)
          }}
        />
      </div>
      {intakes.isPending && <Loading />}
      {intakes.error && <ErrorState error={intakes.error} onRetry={() => void intakes.refetch()} />}
      {intakes.data && (
        <Group>
          {list.length === 0 && <EmptyState>{t('ci.noIntakes')}</EmptyState>}
          {list.map((x) => (
            <Row key={x.id}>
              <StatusDot state={DOT[x.status]} label={statusLabel(x.status)} />
              <div className="grow">
                <div className="t ellipsis">
                  {x.service} <span className="muted">→ {x.env}</span>
                  {x.releaseId && (
                    <>
                      {' · '}
                      <Link to={`/releases/${encodeURIComponent(x.releaseId)}`} className="mono">
                        {x.releaseId}
                      </Link>
                    </>
                  )}
                </div>
                <div className="d ellipsis">
                  {x.actor ? t('ci.byActor', { actor: x.actor }) + ' · ' : ''}
                  {fmtTime(x.createdAt)}
                  {x.error ? ` · ${x.error}` : ''}
                </div>
              </div>
              <span className="v nowrap mono muted">{x.digest.slice(0, 19)}…</span>
            </Row>
          ))}
        </Group>
      )}
      {intakes.data && <Pager page={page} pageSize={INTAKES_PAGE_SIZE} total={intakes.data.total} onChange={setPage} />}
    </>
  )
}
