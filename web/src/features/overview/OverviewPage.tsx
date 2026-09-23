import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { useMe } from '../../app/session'
import { can } from '../../lib/permissions'
import { ErrCode } from '../../lib/errcode'
import { Banner, Chev, EmptyState, ErrorState, Group, GroupHeader, Loading, Note, Page, Pill, Row, StatusDot, Toolbar } from '../../components/ui'
import { DeployDot, NoUpstreamsBanner, RefreshNow, ReleaseRow } from '../../components/domain'
import { useOverview } from './queries'

export function OverviewPage() {
  const { t } = useTranslation()
  const me = useMe()
  const q = useOverview()
  const d = q.data
  const upstreams = d?.upstreams ?? []
  const down = upstreams.filter((u) => !u.kargoOk || !u.argocdOk)
  // An upstream that answers but classifies nothing is not "down", so it
  // never reaches the banner above — and this page then reports zero services
  // with no reason given. It is the landing page: it is where most people
  // first see the number that is wrong.
  const unclassified = upstreams.filter((u) => (u.envs?.length ?? 0) > 0 && u.catalog?.kept === 0 && u.catalog.applications > 0)
  const expiring = upstreams.flatMap((u) => u.expiring ?? [])
  // Soonest first already, from the server; the first one decides the wording.
  const soonest = expiring.length > 0 ? expiring[0]!.days : null
  const inFlight = d?.inFlight ?? []
  const recentFailed = d?.recentFailed ?? []
  const recent = d?.recent ?? []
  const unhealthy = d?.unhealthy ?? []
  const envOrder = d?.envOrder ?? []

  return (
    <>
      <Toolbar
        title={t('overview.title')}
        sub={
          <>
            {t('overview.greeting', { name: me.user.name })} <RefreshNow query="overview" path="/api/v1/overview" />
          </>
        }
      />
      <Page>
        {q.isPending && <Loading />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {d && (
          <>
            {d.upstreamError?.code === ErrCode.NoUpstreams ? (
              <NoUpstreamsBanner canConfigure={can(me, 'environments.manage')}>{t('overview.noUpstreams')}</NoUpstreamsBanner>
            ) : (
              d.upstreamError && <Banner tone="bad">{t('overview.upstreamError', { msg: d.upstreamError.msg })}</Banner>
            )}
            {down.map((u) => (
              <Banner key={u.name} tone="bad">
                <StatusDot state="bad" /> {t('overview.upstreamDown', { name: u.name, envs: (u.envs ?? []).join(' / ') })}
                <span className="mono"> {u.kargoError ?? u.argocdError}</span>
              </Banner>
            ))}
            {unclassified.map((u) => (
              <Banner key={`${u.name}-catalog`} tone="warn">
                <StatusDot state="warn" />{' '}
                {t('overview.noneClassified', { name: u.name, count: u.catalog.applications })}
                {can(me, 'environments.manage') && (
                  <>
                    {' '}
                    <Link to="/admin/catalog">{t('overview.openCatalog')}</Link>
                  </>
                )}
              </Banner>
            ))}
            {soonest !== null && (
              <Banner tone="warn">
                <StatusDot state="warn" />{' '}
                {soonest < 0
                  ? t('overview.credentialExpired', { count: expiring.length })
                  : t('overview.credentialExpiring', { count: soonest })}
                {can(me, 'environments.manage') && (
                  <>
                    {' '}
                    <Link to="/admin/upstreams">{t('overview.openUpstreams')}</Link>
                  </>
                )}
              </Banner>
            )}

            <div className="cards" style={{ marginTop: down.length || unclassified.length || expiring.length || d.upstreamError ? 14 : 0 }}>
              <div className="card">
                <div className="k">{t('overview.inFlight')}</div>
                <div className={`n ${inFlight.length ? 'orange' : ''}`}>
                  {inFlight.length}
                  <small>{t('overview.mine', { count: d.myInFlight })}</small>
                </div>
              </div>
              <div className="card">
                <div className="k">{t('overview.today')}</div>
                <div className="n">
                  {d.today.total}
                  <small>{t('overview.succeeded', { count: d.today.succeeded })}</small>
                </div>
              </div>
              <div className="card">
                <div className="k">{t('overview.unhealthy')}</div>
                <div className={`n ${unhealthy.length ? 'orange' : ''}`}>
                  {unhealthy.length}
                  <small>{t('overview.drifted', { count: d.drifted })}</small>
                </div>
              </div>
              <div className="card">
                <div className="k">{t('overview.services')}</div>
                <div className="n">
                  {d.serviceCount}
                  <small>{t('overview.domains', { count: d.domainCount })}</small>
                </div>
              </div>
            </div>

            {envOrder.length > 0 && (
              <>
                <GroupHeader>{t('overview.environments')}</GroupHeader>
                <Group>
                  {envOrder.map((e) => {
                    const s = d.envStats?.[e]
                    return (
                      <Row key={e}>
                        <div className="grow t">{e}</div>
                        <span className="v">{t('overview.envServices', { count: s?.services ?? 0 })}</span>
                        {/* An environment with nothing in it is not healthy,
                            it is empty: a green "All good" over zero services
                            claims a clean bill of health nobody checked. */}
                        {!s?.services ? (
                          <Pill>{t('overview.envEmpty')}</Pill>
                        ) : s.unhealthy ? (
                          <Pill tone="orange">{t('overview.envUnhealthy', { count: s.unhealthy })}</Pill>
                        ) : s.drifted ? (
                          <Pill>{t('overview.envDrifted', { count: s.drifted })}</Pill>
                        ) : (
                          <Pill tone="green">{t('overview.envOk')}</Pill>
                        )}
                      </Row>
                    )
                  })}
                </Group>
              </>
            )}

            {(inFlight.length > 0 || recentFailed.length > 0) && (
              <>
                <GroupHeader>{t('overview.needsAttention')}</GroupHeader>
                <Group>
                  {[...inFlight, ...recentFailed].map((r) => (
                    <ReleaseRow key={r.id} r={r} />
                  ))}
                </Group>
              </>
            )}

            {unhealthy.length > 0 && (
              <>
                <GroupHeader>{t('overview.unhealthy')}</GroupHeader>
                <Group>
                  {unhealthy.slice(0, 20).map((x) => (
                    <Row key={x.app} to={`/services/${encodeURIComponent(x.service)}/envs/${encodeURIComponent(x.env)}`}>
                      <DeployDot d={x} />
                      <div className="grow">
                        <div className="t">
                          {x.service} · {x.env}
                        </div>
                        <div className="d">
                          {x.sync} · {x.health}
                          {x.healthMessage ? ` · ${x.healthMessage}` : ''}
                        </div>
                      </div>
                      <Chev />
                    </Row>
                  ))}
                </Group>
              </>
            )}

            <GroupHeader right={<Link to="/releases">{t('overview.all')}</Link>}>{t('overview.recent')}</GroupHeader>
            <Group>
              {recent.length === 0 && <EmptyState>{t('overview.noReleases')}</EmptyState>}
              {recent.map((r) => (
                <ReleaseRow key={r.id} r={r} />
              ))}
            </Group>
            <Note>{t('overviewNote.devAuto')}</Note>
          </>
        )}
      </Page>
    </>
  )
}
