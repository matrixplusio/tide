import { useTranslation } from 'react-i18next'
import { useLocation, useParams, useSearchParams } from 'react-router-dom'
import { useMe } from '../../app/session'
import { dimensionValueName } from '../../lib/catalog'
import { fmtTime, safeHttpUrl, shortDigest } from '../../lib/format'
import { Chev, EmptyState, ErrorState, Group, GroupHeader, Loading, Note, Page, Pill, Row, Segmented, StatusDot, Toolbar } from '../../components/ui'
import { ConflictBanner, DeployDot, HealthPill, ReleaseRow, VersionLabel } from '../../components/domain'
import { useService } from './queries'

export function ServiceDetailPage() {
  const { t } = useTranslation()
  const { service = '' } = useParams()
  // The list carries its filters in the URL, so going back to a bare
  // /services throws away the scope somebody just narrowed to. The page that
  // sent us here says where to return; a direct visit has no such state and
  // falls back to the unfiltered list.
  const from = (useLocation().state as { from?: string } | null)?.from
  const me = useMe()
  // In the URL so a filtered history can be linked to and survives going
  // back, the same as the scope on the services list.
  const [params, setParams] = useSearchParams()
  const env = params.get('env') ?? ''
  const q = useService(service, env)
  const s = q.data?.service
  const releases = q.data?.releases ?? []
  const grafana = s && safeHttpUrl(Object.values(s.envs).find((d) => d?.grafana)?.grafana)

  return (
    <>
      <Toolbar title={service} sub={s && [s.domain, ...(me.app.dimensions ?? []).map((d) => dimensionValueName(d, s.dimensions?.[d.key]))].filter(Boolean).join(' · ')} back={{ to: from ?? '/services', label: t('services.title') }} />
      <Page>
        {q.isPending && <Loading />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {s && (
          <>
            <ConflictBanner conflicts={s.conflicts} />
            <GroupHeader>{t('services.envs')}</GroupHeader>
            <Group>
              {me.envOrder.map((e) => {
                const d = s.envs[e]
                if (!d)
                  return (
                    <Row key={e}>
                      <StatusDot state="off" label={t('services.notOnboarded')} />
                      <div className="grow t">{e}</div>
                      <span className="faint">{t('services.notOnboarded')}</span>
                    </Row>
                  )
                return (
                  <Row key={e} to={`/services/${encodeURIComponent(service)}/envs/${encodeURIComponent(e)}`}>
                    <DeployDot d={d} />
                    <div className="grow">
                      <div className="t">
                        {e} · <VersionLabel a={d} full />
                      </div>
                      <div className="d mono">
                        {shortDigest(d.digest, 16) || '—'}
                        {d.since && <span className="sans">{t('services.since', { at: fmtTime(d.since) })}</span>}
                      </div>
                    </div>
                    {d.autoPromotion && <Pill tone="blue">{d.autoHeld ? t('services.autoHeld') : t('services.auto')}</Pill>}
                    {!me.canOperate[e] && <Pill>{t('services.readOnly')}</Pill>}
                    <HealthPill health={d.health} sync={d.sync} />
                    <Chev />
                  </Row>
                )
              })}
            </Group>
            <Note>{t('services.envHint')}</Note>

            <GroupHeader
              right={
                <Segmented
                  label={t('services.historyEnv')}
                  value={env}
                  options={[['', t('services.allEnvs')], ...me.envOrder.map((e): [string, string] => [e, e])]}
                  onChange={(v) => {
                    const next = new URLSearchParams(params)
                    if (v) next.set('env', v)
                    else next.delete('env')
                    setParams(next, { replace: true })
                  }}
                />
              }
            >
              {t('services.history')}
            </GroupHeader>
            <Group>
              {releases.length === 0 && <EmptyState>{env ? t('services.noReleasesIn', { env }) : t('services.noReleases')}</EmptyState>}
              {releases.map((r) => (
                <ReleaseRow key={r.id} r={r} />
              ))}
            </Group>

            {grafana && (
              <>
                <GroupHeader>{t('services.related')}</GroupHeader>
                <Group>
                  <Row href={grafana}>
                    <div className="grow">
                      <div className="t">{t('services.grafana')}</div>
                      <div className="d">{t('services.grafanaHint')}</div>
                    </div>
                    <Chev />
                  </Row>
                </Group>
              </>
            )}
          </>
        )}
      </Page>
    </>
  )
}
