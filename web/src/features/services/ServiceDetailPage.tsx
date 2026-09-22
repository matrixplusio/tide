import { useTranslation } from 'react-i18next'
import { useParams } from 'react-router-dom'
import { useMe } from '../../app/session'
import { dimensionValueName } from '../../lib/catalog'
import { fmtTime, safeHttpUrl, shortDigest } from '../../lib/format'
import { Chev, EmptyState, ErrorState, Group, GroupHeader, Loading, Note, Page, Pill, Row, StatusDot, Toolbar } from '../../components/ui'
import { ConflictBanner, DeployDot, HealthPill, ReleaseRow, VersionLabel } from '../../components/domain'
import { useService } from './queries'

export function ServiceDetailPage() {
  const { t } = useTranslation()
  const { service = '' } = useParams()
  const me = useMe()
  const q = useService(service)
  const s = q.data?.service
  const releases = q.data?.releases ?? []
  const grafana = s && safeHttpUrl(Object.values(s.envs).find((d) => d?.grafana)?.grafana)

  return (
    <>
      <Toolbar title={service} sub={s && [s.domain, ...(me.app.dimensions ?? []).map((d) => dimensionValueName(d, s.dimensions?.[d.key]))].filter(Boolean).join(' · ')} back={{ to: '/services', label: t('services.title') }} />
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

            <GroupHeader>{t('services.history')}</GroupHeader>
            <Group>
              {releases.length === 0 && <EmptyState>{t('services.noReleases')}</EmptyState>}
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
