import { useTranslation } from 'react-i18next'
import { useParams, useSearchParams } from 'react-router-dom'
import { fmtTime, safeHttpUrl } from '../../lib/format'
import { Banner, Chev, EmptyState, ErrorState, Group, GroupHeader, KV, Loading, Note, Page, Pill, Row, Segmented, StatusDot, Toolbar } from '../../components/ui'
import { ConflictBanner, HealthPill, PodsList, ResourcesList, RolloutView, VersionLabel, phaseDot, phaseTone } from '../../components/domain'
import { useEnvDetail } from './queries'
import { PromoteForm } from './PromoteForm'
import { RestartForm } from './RestartForm'
import { SyncForm } from './SyncForm'

const CHANGES = [
  ['upgrade', 'services.changeUpgrade'],
  ['sync', 'services.changeSync'],
  ['restart', 'services.changeRestart'],
] as const
type Change = (typeof CHANGES)[number][0]

export function EnvDetailPage() {
  const { t } = useTranslation()
  const { service = '', env = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const rawChange = params.get('change')
  const change: Change = rawChange === 'restart' || rawChange === 'sync' ? rawChange : 'upgrade'
  const setChange = (v: Change) => {
    const next = new URLSearchParams(params)
    if (v !== 'upgrade') next.set('change', v)
    else next.delete('change')
    setParams(next, { replace: true })
  }
  const base = `/services/${encodeURIComponent(service)}/envs/${encodeURIComponent(env)}`
  const q = useEnvDetail(service, env)
  const d = q.data?.deployment
  const inFlight = (q.data?.releases ?? []).find((r) => r.status === 'confirming' || r.status === 'approving' || r.status === 'executing')
  const promotions = q.data?.promotions ?? []
  const grafana = safeHttpUrl(d?.grafana)
  const can = q.data?.can

  return (
    <>
      <Toolbar title={`${service} · ${env}`} sub={d?.namespace} back={{ to: `/services/${encodeURIComponent(service)}`, label: service }}>
        {d && <HealthPill health={d.health} sync={d.sync} />}
      </Toolbar>
      <Page wide>
        {q.isPending && <Loading />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {q.data && d && (
          <div className="cols2">
            <div>
              <GroupHeader>{t('services.currentVersion')}</GroupHeader>
              <Group>
                <KV k={t('services.version')}>
                  <VersionLabel a={d} full />
                </KV>
                <KV k={t('services.digest')} mono>
                  {d.digest || '—'}
                </KV>
                <KV k="Freight" mono>
                  {d.freight || '—'}
                </KV>
                <KV k={t('services.deployedAt')}>{fmtTime(d.since)}</KV>
                <KV k="Kargo">{d.kargoProject ? `${d.kargoProject} / ${d.kargoStage ?? ''}` : t('services.notOnboarded')}</KV>
                {d.autoPromotion && (
                  <KV k={t('services.autoDeploy')}>
                    <Pill tone={d.autoHeld ? 'orange' : 'blue'}>{d.autoHeld ? t('services.autoPaused') : t('services.autoOn')}</Pill>
                  </KV>
                )}
              </Group>

              {inFlight && (
                <Banner tone="warn" to={`/releases/${encodeURIComponent(inFlight.id)}`}>
                  <StatusDot state="run" /> {t('services.inFlight')} <span className="mono">{inFlight.id}</span> ({inFlight.status === 'confirming' ? t('services.statusConfirming') : inFlight.status === 'approving' ? t('services.statusApproving') : t('services.statusExecuting')}) <Chev />
                </Banner>
              )}

              {q.data.conflicts?.length ? (
                <ConflictBanner conflicts={q.data.conflicts} />
              ) : (
                <>
                  <div className="change-switch">
                    <Segmented label={t('services.changeType')} value={change} options={CHANGES.map(([k, key]) => [k, t(key)] as const)} onChange={setChange} />
                  </div>
                  {change === 'upgrade' && <PromoteForm key={`${service}/${env}`} d={d} canOperate={q.data.canOperate} busy={!!inFlight || !!d.promoting} />}
                  {change === 'sync' && <SyncForm key={`${service}/${env}`} d={d} canOperate={!!can?.sync} busy={!!inFlight || !!d.promoting} />}
                  {change === 'restart' && <RestartForm key={`${service}/${env}`} d={d} canOperate={!!can?.restart} busy={!!inFlight || !!d.promoting} />}
                </>
              )}

              <GroupHeader>{t('services.kargoHistory')}</GroupHeader>
              <Group>
                {q.data.promotionsError && (
                  <Row>
                    <div className="grow">
                      <ErrorState error={q.data.promotionsError} inline />
                    </div>
                  </Row>
                )}
                {promotions.length === 0 && !q.data.promotionsError && <EmptyState>{t('services.noRecords')}</EmptyState>}
                {promotions.map((p) => {
                  const body = (
                    <>
                      <StatusDot state={phaseDot(p.phase)} label={p.phase || 'Pending'} />
                      <div className="grow">
                        <div className="t mono ellipsis">{p.tag || p.freight.slice(0, 12)}</div>
                        <div className="d">
                          {fmtTime(p.createdAt)} · {p.releaseId ? p.releaseId : env === 'dev' || p.actor?.includes('controller') ? t('services.autoDeployed') : t('services.external', { actor: p.actor || 'unknown' })}
                        </div>
                      </div>
                      <Pill tone={p.phase === 'Succeeded' ? 'neutral' : phaseTone(p.phase)}>{p.phase || 'Pending'}</Pill>
                    </>
                  )
                  return p.releaseId ? (
                    <Row key={p.name} to={`/releases/${encodeURIComponent(p.releaseId)}`}>
                      {body}
                      <Chev />
                    </Row>
                  ) : (
                    <Row key={p.name} title={p.message}>
                      {body}
                    </Row>
                  )
                })}
              </Group>
            </div>

            <div>
              <GroupHeader>{t('services.deployState')}</GroupHeader>
              <RolloutView live={q.data.live} />
              <GroupHeader>Pod</GroupHeader>
              <PodsList live={q.data.live} base={can?.pods ? base : undefined} />
              {!can?.pods && <Note>{t('services.noPodPermission')}</Note>}
              <GroupHeader>{t('services.resources')}</GroupHeader>
              <ResourcesList live={q.data.live} />
              {grafana && (
                <Note>
                  {t('services.metricsIn')}{' '}
                  <a href={grafana} target="_blank" rel="noreferrer">
                    Grafana
                  </a>
                </Note>
              )}
            </div>
          </div>
        )}
      </Page>
    </>
  )
}
