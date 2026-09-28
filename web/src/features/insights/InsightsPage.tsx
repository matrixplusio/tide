import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import type { ReactNode } from 'react'
import { EmptyState, ErrorState, Group, Loading, Note, Page, Pill, Segmented, Toolbar } from '../../components/ui'
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
//
// Every section states its point in one line under its title. The reasoning
// behind a number lives in a tooltip on the heading, not in a paragraph on
// the page: a dashboard that lectures is one people stop reading.

// Catalogue keys for the values the server sends back, spelled out rather
// than built from the value: keys.test.ts only sees literals.
const ANOMALY: Record<string, string> = {
  rollback: 'insights.anomalyRollback',
  first_deploy: 'insights.anomalyFirstDeploy',
  first_deploy_per_kargo: 'insights.anomalyFirstDeployPerKargo',
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

// How many services the activity list shows before folding. A hundred rows
// of bars is a wall; the busiest dozen is what the question was.
const TOP_SERVICES = 12

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

/** Section is a titled block of the page: one heading, one line saying
 *  what the block answers, then its charts. */
function Section({ title, lead, children }: { title: string; lead?: string; children: ReactNode }) {
  return (
    <section className="ins">
      <h2 className="ins-h">{title}</h2>
      {lead !== undefined && <p className="ins-lead">{lead}</p>}
      {children}
    </section>
  )
}

/** SubHead names one chart inside a section. `hint` is the sentence that
 *  says how to read it — on hover, so it is there when wanted and not in
 *  the way when not. */
function SubHead({ title, hint, inGroup }: { title: string; hint?: string; inGroup?: boolean }) {
  const { t } = useTranslation()
  return (
    <div className={['ins-sub', inGroup && 'in-group'].filter(Boolean).join(' ')}>
      {title}
      {hint !== undefined && (
        <button type="button" className="tip" title={hint} aria-label={`${t('insights.howToRead')}: ${hint}`}>
          i
        </button>
      )}
    </div>
  )
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
  const decided = pr.approvals.approved + pr.approvals.rejected

  return (
    <>
      <Section title={t('insights.headline')}>
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
      </Section>

      {/* The part worth having. */}
      <Section title={t('insights.safeguards')} lead={t('insights.safeguardsHint')}>
        <SubHead title={t('insights.dwell', { n: pr.confirmSeconds })} hint={t('insights.dwellHint', { n: pr.confirmSeconds })} />
        <Group>
          {dwellTotal === 0 ? (
            <Note>{t('insights.dwellNone')}</Note>
          ) : (
            <Bars
              data={pr.confirmDwell.map((b) => ({
                label: b.label,
                value: b.count,
                text: `${b.count} · ${pct(b.count, dwellTotal)}`,
                // The bucket just past the countdown is the one the question
                // is about, so it is the one bar in another colour.
                tone: b.upper === pr.confirmSeconds + 5 ? 'warn' : 'accent',
              }))}
            />
          )}
        </Group>

        <SubHead title={t('insights.anomalies')} hint={t('insights.anomaliesHint')} />
        <Group>
          {pr.anomalies.withAnomaly === 0 ? (
            <Note>{t('insights.anomaliesNone')}</Note>
          ) : (
            /* One number and the breakdown behind it are one thought, so they
               share a card: the number alone would leave half a row empty. */
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
              <Bars
                data={[...pr.anomalies.byCode]
                  .sort((a, b) => b.count - a.count)
                  .map((c) => ({ label: anomalyLabel(t, c.code), value: c.count }))}
              />
            </div>
          )}
        </Group>

        <SubHead title={t('insights.approvals')} />
        {pr.approvals.requested === 0 ? (
          <div className="cards">
            <Stat label={t('insights.approvalsRequested')} value={0} caption={t('insights.approvalsNone')} />
            <Stat
              label={t('insights.selfConfirmed')}
              value={pr.approvals.selfConfirmed}
              caption={t('insights.selfConfirmedHint', { pct: pct(pr.approvals.selfConfirmed, totals.releases) })}
            />
          </div>
        ) : (
          <div className="cards">
            <Stat label={t('insights.approvalsRequested')} value={pr.approvals.requested} caption={t('insights.approvalsRequestedHint')} />
            <Stat
              label={t('insights.approvalRejectRate')}
              value={pct(pr.approvals.rejected, decided)}
              caption={t('insights.approvalRejectHint', { n: pr.approvals.rejected })}
            />
            <Stat
              label={t('insights.approvalWait')}
              value={duration(pr.approvals.medianSeconds, t)}
              caption={t('insights.approvalP90', { v: duration(pr.approvals.p90Seconds, t) })}
            />
            <Stat
              label={t('insights.selfConfirmed')}
              value={pr.approvals.selfConfirmed}
              caption={t('insights.selfConfirmedHint', { pct: pct(pr.approvals.selfConfirmed, totals.releases) })}
            />
          </div>
        )}

        <SubHead title={t('insights.sources')} />
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
      </Section>

      <Section title={t('insights.activity')}>
        <Group>
          <SubHead title={t('insights.byService')} inGroup />
          <ServiceBars services={activity.services} />

          <SubHead title={t('insights.byEnv')} inGroup />
          <Bars
            data={activity.environments.map((e) => ({
              label: e.env,
              value: e.total,
              failed: e.failed,
              tone: 'accent',
            }))}
          />

          <SubHead title={t('insights.byKind')} inGroup />
          <Bars data={activity.kinds.map((k) => ({ label: kindLabel(t, k.kind), value: k.total, tone: 'accent' }))} />

          <SubHead title={t('insights.whenTitle')} inGroup />
          <Heat
            cells={new Map(activity.weekly.map((s) => [`${s.weekday}-${s.hour}`, s.count]))}
            dayNames={WEEKDAYS.map((k) => t(k))}
            cellTitle={(day, hour, n) => t('insights.heatCell', { day, hour, n })}
          />
          <Note>{t('insights.afterHours', { n: risk.afterHours, pct: pct(risk.afterHours, totals.releases) })}</Note>
        </Group>

        {activity.people !== undefined && <People people={activity.people} />}
      </Section>

      <Section title={t('insights.risk')}>
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
          <>
            <SubHead title={t('insights.untouched', { n: risk.coverage.untouched.length })} />
            <div className="chips">
              {risk.coverage.untouched.map((s) => (
                <Link key={s} className="chip" to={`/services/${encodeURIComponent(s)}`}>
                  {s}
                </Link>
              ))}
            </div>
          </>
        )}

        {risk.repeats.length > 0 && (
          <>
            <SubHead title={t('insights.repeats')} hint={t('insights.repeatsHint')} />
            <Group>
              {/* The day goes with the number, not the name: three names
                  and a date in one label was more than the column could
                  hold, and the name is the part that must survive. */}
              <Bars wide data={risk.repeats.map((r) => ({ label: `${r.service} · ${r.env}`, value: r.count, text: `${r.count} · ${r.day}` }))} />
            </Group>
          </>
        )}

        {risk.jiraReuse.length > 0 && (
          <>
            <SubHead title={t('insights.jiraReuse')} hint={t('insights.jiraReuseHint')} />
            <Group>
              <Bars data={risk.jiraReuse.map((j) => ({ label: j.jira, value: j.count }))} />
            </Group>
          </>
        )}
      </Section>
    </>
  )
}

/** The busiest services first, the rest behind one click. The list keeps
 *  every service reachable — folding is about the page's height, not about
 *  hiding anything. */
function ServiceBars({ services }: { services: Insights['activity']['services'] }) {
  const { t } = useTranslation()
  const [all, setAll] = useState(false)
  const sorted = [...services].sort((a, b) => b.total - a.total)
  const shown = all ? sorted : sorted.slice(0, TOP_SERVICES)
  const rest = sorted.length - shown.length
  return (
    <>
      <Bars
        max={sorted[0]?.total}
        data={shown.map((s) => ({ label: s.service, value: s.total, failed: s.failed, tone: 'accent', title: s.project }))}
      />
      {(rest > 0 || all) && (
        <button type="button" className="ins-more" onClick={() => setAll((v) => !v)}>
          {all ? t('insights.fewer', { n: TOP_SERVICES }) : t('insights.more', { n: rest })}
        </button>
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
      <SubHead title={t('insights.people')} hint={t('insights.peopleHint')} />
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
                <td>{p.approved || p.rejected ? t('insights.decidedValue', { a: p.approved, r: p.rejected }) : '—'}</td>
                <td>{p.created > 0 ? `${p.failed} / ${p.created}` : '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Group>
    </>
  )
}
