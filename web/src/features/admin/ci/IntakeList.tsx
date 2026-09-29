import { useTranslation } from 'react-i18next'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { fmtTime } from '../../../lib/format'
import { useMe } from '../../../app/session'
import { EmptyState, ErrorState, Group, Input, Loading, Pager, Row, Segmented, Select, StatusDot } from '../../../components/ui'
import { INTAKES_PAGE_SIZE, useCIIntakes } from '../queries'
import type { IntakeStatus } from '../types'

const FILTERS = [
  ['', 'ci.filterAll'],
  ['waiting', 'ci.filterWaiting'],
  ['released', 'ci.filterReleased'],
  ['failed', 'ci.filterFailed'],
  ['expired', 'ci.filterExpired'],
  ['build_failed', 'ci.filterBuildFailed'],
  ['rejected', 'ci.filterRejected'],
] as const

// A waiting intake is not a problem: a warehouse discovers an image on its own
// schedule. Only failed and expired are states somebody has to look at.
const DOT: Record<IntakeStatus, 'ok' | 'off' | 'warn' | 'bad'> = {
  waiting: 'warn',
  released: 'ok',
  failed: 'bad',
  expired: 'bad',
  build_failed: 'bad',
  // Refused before Tide took it in: the build is green and went nowhere.
  rejected: 'bad',
}

const STATUS_LABELS: Record<IntakeStatus, string> = {
  waiting: 'ci.stWaiting',
  released: 'ci.stReleased',
  failed: 'ci.stFailed',
  expired: 'ci.stExpired',
  build_failed: 'ci.stBuildFailed',
  rejected: 'ci.stRejected',
}

export function IntakeList() {
  const { t } = useTranslation()
  const envs = useMe().environments
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const [env, setEnv] = useState('')
  // What is typed, and what is asked for: a query per keystroke would page
  // through results nobody reads while the name is half written.
  const [typed, setTyped] = useState('')
  const [service, setService] = useState('')
  useEffect(() => {
    const id = setTimeout(() => {
      setService(typed.trim())
      setPage(1)
    }, 300)
    return () => clearTimeout(id)
  }, [typed])
  const intakes = useCIIntakes({ status: status || undefined, service: service || undefined, env: env || undefined, page })
  const list = intakes.data?.items ?? []

  const statusLabel = (s: IntakeStatus) => t(STATUS_LABELS[s])

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
        <Input appearance="filled" type="search" aria-label={t('ci.byService')} placeholder={t('ci.servicePlaceholder')} value={typed} onChange={(e) => setTyped(e.target.value)} />
        <Select
          appearance="filled"
          aria-label={t('ci.byEnv')}
          value={env}
          options={[['', t('ci.allEnvs')], ...envs.map((e): [string, string] => [e.name, e.displayName ? `${e.displayName} ${e.name}` : e.name])]}
          onChange={(e) => {
            setEnv(e.target.value)
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
                  {x.stage && <span className="muted">{' · '}{x.stage}</span>}
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
                  {x.warning ? ` · ${x.warning}` : ''}
                </div>
              </div>
              <span className="v nowrap mono muted">{x.digest ? `${x.digest.slice(0, 19)}…` : '—'}</span>
            </Row>
          ))}
        </Group>
      )}
      {intakes.data && <Pager page={page} pageSize={INTAKES_PAGE_SIZE} total={intakes.data.total} onChange={setPage} />}
    </>
  )
}
