import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { useMe } from '../../app/session'
import { can } from '../../lib/permissions'
import { ErrCode } from '../../lib/errcode'
import { Banner, Chev, EmptyState, ErrorState, Group, GroupHeader, Loading, Note, Page, Pill, Row, StatusDot, Toolbar } from '../../components/ui'
import { DeployDot, NoUpstreamsBanner, ReleaseRow } from '../../components/domain'
import { useOverview } from './queries'

export function OverviewPage() {
  const { t } = useTranslation()
  const me = useMe()
  const q = useOverview()
  const d = q.data
  const upstreams = d?.upstreams ?? []
  const down = upstreams.filter((u) => !u.kargoOk || !u.argocdOk)
  const inFlight = d?.inFlight ?? []
  const recentFailed = d?.recentFailed ?? []
  const recent = d?.recent ?? []
  const unexpected = d?.unexpected ?? []
  const envOrder = d?.envOrder ?? []

  return (
    <>
      <Toolbar title={t('overview.title')} sub={t('overview.greeting', { name: me.user.name })} />
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

            <div className="cards" style={{ marginTop: down.length || d.upstreamError ? 14 : 0 }}>
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
                <div className="k">{t('overview.unexpected')}</div>
                <div className={`n ${unexpected.length ? 'orange' : ''}`}>{unexpected.length}</div>
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
                        {s?.unexpected ? <Pill tone="orange">{t('overview.envUnexpected', { count: s.unexpected })}</Pill> : <Pill>{t('overview.envOk')}</Pill>}
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

            {unexpected.length > 0 && (
              <>
                <GroupHeader>{t('overview.unexpected')}</GroupHeader>
                <Group>
                  {unexpected.slice(0, 20).map((x) => (
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
