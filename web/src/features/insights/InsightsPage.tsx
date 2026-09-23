import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import { EmptyState, ErrorState, Group, GroupHeader, Loading, Note, Page, Pill, Segmented, Toolbar } from '../../components/ui'
import type { Insights } from '../../lib/types'
import { Bars, Heat, Split, Stat } from './charts'
import { RANGES, useInsights, type RangeDays } from './queries'

// The insights page answers two different kinds of question, and keeps them
// apart on purpose:
//
//   - how much was released, by whom, where — plain counting;
//   - whether the things Tide puts in the way changed any outcomes.
//
// The second one is the reason this page exists. A countdown nobody reads and
// an approval nobody ever refuses are process, not safety, and the only way to
// find that out is to measure them.

// Catalogue keys for the values the server sends back, spelled out rather
// than built from the value: keys.test.ts only sees literals.
const ANOMALY: Record<string, string> = {
  rollback: 'insights.anomalyRollback',
  first_deploy: 'insights.anomalyFirstDeploy',
  multi_version_jump: 'insights.anomalyJump',
  short_soak: 'insights.anomalySoak',
  config_drift: 'insights.anomalyDrift',
}

// Spelled out one by one: an array value would not be a string leaf, and
// keys.test.ts only sees string leaves.
const WEEKDAYS = [
  'insights.weekday0',
  'insights.weekday1',
  'insights.weekday2',
  'insights.weekday3',
  'insights.weekday4',
  'insights.weekday5',
  'insights.weekday6',
] as const

const KIND: Record<string, string> = {
  image: 'insights.kindImage',
  restart: 'insights.kindRestart',
  sync: 'insights.kindSync',
}

function pct(n: number, of: number): string {
  if (of === 0) return '—'
  return `${Math.round((n / of) * 1000) / 10}%`
}

type T = ReturnType<typeof useTranslation>['t']

function anomalyLabel(t: T, code: string): string {
  const key = ANOMALY[code]
  return key === undefined ? code : t(key)
}

function kindLabel(t: T, kind: string): string {
  const key = KIND[kind]
  return key === undefined ? kind : t(key)
}

function duration(seconds: number, t: T): string {
  if (seconds < 90) return t('insights.seconds', { n: Math.round(seconds) })
  if (seconds < 3600) return t('insights.minutes', { n: Math.round(seconds / 60) })
  return t('insights.hours', { n: Math.round(seconds / 360) / 10 })
}

export function InsightsPage() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const days = (RANGES.find((d) => String(d) === params.get('days')) ?? 90) as RangeDays
  const env = params.get('env') ?? undefined

  const q = useInsights({ days, env })

  const setDays = (d: string) => {
    const p = new URLSearchParams(params)
    p.set('days', d)
    setParams(p)
  }

  return (
    <Page>
      <Toolbar title={t('nav.insights')} sub={env}>
        <Segmented
          label={t('insights.range')}
          value={String(days)}
          onChange={setDays}
          options={RANGES.map((d) => [String(d), t('insights.lastDays', { n: d })] as const)}
        />
      </Toolbar>

      {q.isPending && <Loading />}
      {q.isError && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
      {q.data && <Report data={q.data} />}
    </Page>
  )
}

function Report({ data }: { data: Insights }) {
  const { t } = useTranslation()
  const { totals, process: pr, activity, risk } = data

  if (totals.releases === 0) {
    return <EmptyState>{t('insights.empty')}</EmptyState>
  }

  const finished = totals.succeeded + totals.failed
  // Anomalies only mean something once some were raised; the same for
  // approvals and for the countdown.
  const dwellTotal = pr.confirmDwell.reduce((n, b) => n + b.count, 0)
  const ci = pr.sources.find((s) => s.source === 'ci')
  const ui = pr.sources.find((s) => s.source === 'ui')

  return (
    <>
      <GroupHeader>{t('insights.headline')}</GroupHeader>
      <div className="cards">
        <Stat label={t('insights.releases')} value={totals.releases} caption={t('insights.finishedOf', { n: finished })} />
        <Stat
          label={t('insights.successRate')}
          value={pct(totals.succeeded, finished)}
          caption={t('insights.ofFinished', { ok: totals.succeeded, n: finished })}
          tone={finished === 0 ? undefined : totals.failed / finished > 0.1 ? 'bad' : 'ok'}
        />
        {/* Every card on this row carries a line under its number; without
            one this card's bottom edge is empty and the row reads as though
            something failed to load. */}
        <Stat label={t('insights.failed')} value={totals.failed} caption={t('insights.failedHint')} tone={totals.failed > 0 ? 'bad' : undefined} />
        <Stat
          label={t('insights.abandoned')}
          value={totals.cancelled + totals.rejected}
          caption={t('insights.abandonedHint', { c: totals.cancelled, r: totals.rejected })}
        />
      </div>
      <Group>
        <Split
          parts={[
            { label: t('status.release.succeeded'), value: totals.succeeded, tone: 'g' },
            { label: t('status.release.failed'), value: totals.failed, tone: 'r' },
            { label: t('status.release.cancelled'), value: totals.cancelled, tone: 'n' },
            { label: t('status.release.rejected'), value: totals.rejected, tone: 'o' },
          ]}
        />
      </Group>

      {/* The part worth having. */}
      <GroupHeader>{t('insights.safeguards')}</GroupHeader>
      <Note style={{ padding: '0 4px 12px' }}>{t('insights.safeguardsHint')}</Note>

      <div className="sub">{t('insights.dwell', { n: pr.confirmSeconds })}</div>
      <Group>
        {dwellTotal === 0 ? (
          <Note>{t('insights.dwellNone')}</Note>
        ) : (
          <>
            <Bars
              data={pr.confirmDwell.map((b) => ({
                label: b.label,
                value: b.count,
                text: `${b.count} · ${pct(b.count, dwellTotal)}`,
                tone: b.upper === pr.confirmSeconds + 5 ? 'warn' : 'accent',
              }))}
            />
            <Note>{t('insights.dwellHint', { n: pr.confirmSeconds })}</Note>
          </>
        )}
      </Group>

      <div className="sub">{t('insights.anomalies')}</div>
      {pr.anomalies.withAnomaly === 0 ? (
        <Group>
          <Note>{t('insights.anomaliesNone')}</Note>
        </Group>
      ) : (
        <Group>
          {/* One number and the breakdown behind it are one thought, so they
              share a card: the number alone would leave half a row empty. */}
          <div className="figure">
            <div className="figure-stat">
              <div className="k">{t('insights.ignoredRate')}</div>
              <div className={`n ${pr.anomalies.wentAhead / pr.anomalies.withAnomaly > 0.8 ? 'warn' : ''}`}>
                {pct(pr.anomalies.wentAhead, pr.anomalies.withAnomaly)}
              </div>
              <div className="cap">
                {t('insights.ignoredOf', { ahead: pr.anomalies.wentAhead, n: pr.anomalies.withAnomaly })}
              </div>
            </div>
            <Bars data={pr.anomalies.byCode.map((c) => ({ label: anomalyLabel(t, c.code), value: c.count, tone: 'warn' }))} />
          </div>
          <Note>{t('insights.anomaliesHint')}</Note>
        </Group>
      )}

      <div className="sub">{t('insights.approvals')}</div>
      {pr.approvals.requested === 0 ? (
        <Group>
          <Note>{t('insights.approvalsNone')}</Note>
        </Group>
      ) : (
        <div className="cards">
          <Stat label={t('insights.approvalsRequested')} value={pr.approvals.requested} caption={t('insights.approvalsRequestedHint')} />
          <Stat
            label={t('insights.approvalRejectRate')}
            value={pct(pr.approvals.rejected, pr.approvals.approved + pr.approvals.rejected)}
            caption={t('insights.approvalRejectHint', { n: pr.approvals.rejected })}
          />
          <Stat
            label={t('insights.approvalWait')}
            value={duration(pr.approvals.medianSeconds, t)}
            caption={t('insights.approvalP90', { v: duration(pr.approvals.p90Seconds, t) })}
          />
        </div>
      )}
      <Note style={{ padding: '10px 4px 0' }}>
        {t('insights.selfConfirmed', {
          n: pr.approvals.selfConfirmed,
          pct: pct(pr.approvals.selfConfirmed, totals.releases),
        })}
      </Note>

      <div className="sub">{t('insights.sources')}</div>
      {ci === undefined ? (
        <Group>
          <Note>{t('insights.sourcesNone')}</Note>
        </Group>
      ) : (
        <div className="cards">
          <Stat
            label={t('insights.sourceUi')}
            value={pct(ui?.failed ?? 0, (ui?.succeeded ?? 0) + (ui?.failed ?? 0))}
            caption={t('insights.failureOf', { n: ui?.total ?? 0 })}
          />
          <Stat
            label={t('insights.sourceCi')}
            value={pct(ci.failed, ci.succeeded + ci.failed)}
            caption={t('insights.failureOf', { n: ci.total })}
          />
        </div>
      )}

      <GroupHeader>{t('insights.activity')}</GroupHeader>
      <Group>
        <div className="sub in-group">{t('insights.byService')}</div>
        <Bars
          data={activity.services.map((s) => ({
            label: s.service,
            value: s.total,
            text: s.failed > 0 ? `${s.total} · ${t('insights.nFailed', { n: s.failed })}` : String(s.total),
            tone: s.failed > 0 ? 'warn' : 'accent',
            title: s.project,
          }))}
        />

        <div className="sub in-group">{t('insights.byEnv')}</div>
        <Bars
          data={activity.environments.map((e) => ({
            label: e.env,
            value: e.total,
            text: e.failed > 0 ? `${e.total} · ${t('insights.nFailed', { n: e.failed })}` : String(e.total),
            tone: e.failed > 0 ? 'warn' : 'accent',
          }))}
        />

        <div className="sub in-group">{t('insights.byKind')}</div>
        <Bars data={activity.kinds.map((k) => ({ label: kindLabel(t, k.kind), value: k.total, tone: 'accent' }))} />

        <div className="sub in-group">{t('insights.whenTitle')}</div>
        <Heat
          cells={new Map(activity.weekly.map((s) => [`${s.weekday}-${s.hour}`, s.count]))}
          dayNames={WEEKDAYS.map((k) => t(k))}
          cellTitle={(day, hour, n) => t('insights.heatCell', { day, hour, n })}
        />
        <Note>{t('insights.afterHours', { n: risk.afterHours, pct: pct(risk.afterHours, totals.releases) })}</Note>
      </Group>

      {activity.people !== undefined && <People people={activity.people} />}

      <GroupHeader>{t('insights.risk')}</GroupHeader>
      <div className="cards">
        <Stat
          label={t('insights.coverage')}
          value={pct(risk.coverage.released, risk.coverage.catalog)}
          caption={t('insights.coverageHint', { n: risk.coverage.released, of: risk.coverage.catalog })}
          tone={risk.coverage.catalog > 0 && risk.coverage.released < risk.coverage.catalog ? 'warn' : 'ok'}
        />
        <Stat label={t('insights.rollbacks')} value={risk.rollbacks} caption={t('insights.rollbacksHint')} />
      </div>
      {risk.coverage.untouched.length > 0 && (
        <Note style={{ padding: '10px 4px 0' }}>
          {t('insights.untouched')}{' '}
          {risk.coverage.untouched.map((s, i) => (
            <span key={s}>
              {i > 0 && '、'}
              <Link to={`/services/${encodeURIComponent(s)}`}>{s}</Link>
            </span>
          ))}
        </Note>
      )}

      {risk.repeats.length > 0 && (
        <>
          <div className="sub">{t('insights.repeats')}</div>
          <Group>
            <Bars data={risk.repeats.map((r) => ({ label: `${r.service} · ${r.env} · ${r.day}`, value: r.count, tone: 'warn' }))} />
            <Note>{t('insights.repeatsHint')}</Note>
          </Group>
        </>
      )}

      {risk.jiraReuse.length > 0 && (
        <>
          <div className="sub">{t('insights.jiraReuse')}</div>
          <Group>
            <Bars data={risk.jiraReuse.map((j) => ({ label: j.jira, value: j.count, tone: 'warn' }))} />
            <Note>{t('insights.jiraReuseHint')}</Note>
          </Group>
        </>
      )}
    </>
  )
}

// Workload, not a ranking. The rows keep the server's order (by name), and a
// person's failures are shown next to how many releases they came from —
// a count on its own would be read as a score.
function People({ people }: { people: NonNullable<Insights['activity']['people']> }) {
  const { t } = useTranslation()
  if (people.length === 0) return null
  return (
    <>
      <div className="sub">{t('insights.people')}</div>
      <Note style={{ padding: '0 4px 10px' }}>{t('insights.peopleHint')}</Note>
      <Group>
        <table className="tbl">
        <thead>
          <tr>
            <th>{t('insights.who')}</th>
            <th>{t('insights.created')}</th>
            <th>{t('insights.confirmed')}</th>
            <th>{t('insights.decided')}</th>
            <th>{t('insights.theirFailed')}</th>
          </tr>
        </thead>
        <tbody>
          {people.map((p) => (
            <tr key={p.sub}>
              <td>
                {p.name}
                {p.token && (
                  <>
                    {' '}
                    <Pill tone="neutral">{t('insights.token')}</Pill>
                  </>
                )}
              </td>
              <td>{p.created || '—'}</td>
              <td>{p.confirmed || '—'}</td>
              <td>
                {p.approved || p.rejected
                  ? t('insights.decidedValue', { a: p.approved, r: p.rejected })
                  : '—'}
              </td>
              <td>{p.created > 0 ? `${p.failed} / ${p.created}` : '—'}</td>
            </tr>
          ))}
          </tbody>
        </table>
      </Group>
    </>
  )
}
