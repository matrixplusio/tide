import { useTranslation } from 'react-i18next'
import { useMemo, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { useMe } from '../../app/session'
import { canService } from '../../lib/permissions'
import { fmtTime } from '../../lib/format'
import { Banner, Button, ButtonRow, Checkbox, EmptyState, ErrorState, Group, Input, Loading, Note, Page, Row, Segmented, StatusDot, Toolbar } from '../../components/ui'
import { LOG_TAIL, usePodEvents, usePodLogs, useServiceScope } from './queries'

type Tab = 'logs' | 'events'
type Level = 'all' | 'warn' | 'error'

export function PodDetailPage() {
  const { t } = useTranslation()
  const { service = '', env = '', pod = '' } = useParams()
  const [params] = useSearchParams()
  const uid = params.get('uid') ?? ''
  const base = `/services/${encodeURIComponent(service)}/envs/${encodeURIComponent(env)}`
  const [tab, setTab] = useState<Tab>('logs')
  const [level, setLevel] = useState<Level>('all')
  const [filter, setFilter] = useState('')
  const [follow, setFollow] = useState(true)

  const me = useMe()
  const scope = useServiceScope(service)
  const allowed = canService(me, 'pods.view', env, scope.project, scope.type)
  const logs = usePodLogs(service, env, pod, { enabled: allowed && tab === 'logs', follow })
  const events = usePodEvents(service, env, pod, uid, allowed && tab === 'events')

  const lines = useMemo(() => {
    const f = filter.toLowerCase()
    return (logs.data?.lines ?? []).filter((l) => {
      const c = l.content.toLowerCase()
      if (f && !c.includes(f)) return false
      if (level === 'error') return /error|fatal|panic|exception/.test(c)
      if (level === 'warn') return /warn|error|fatal|panic|exception/.test(c)
      return true
    })
  }, [logs.data, filter, level])

  const download = () => {
    const blob = new Blob([(logs.data?.lines ?? []).map((l) => `${l.timeStamp} ${l.content}`).join('\n')], { type: 'text/plain' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `${pod}.log`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  const evs = events.data?.events ?? []

  return (
    <>
      <Toolbar title={<span className="mono small-title">{pod}</span>} sub={`${service} · ${env}`} back={{ to: base, label: env }}>
        <Segmented label={t('pod.view')} value={tab} onChange={setTab} options={[['logs', t('pod.logs')], ['events', t('pod.events')]]} />
      </Toolbar>
      <Page wide>
        {!allowed && <Banner tone="warn">{t('pod.noPermission', { env })}</Banner>}
        {allowed && tab === 'logs' && (
          <>
            <ButtonRow style={{ marginTop: 0, marginBottom: 8 }}>
              <Input appearance="filled" type="search" aria-label={t('pod.filterLabel')} placeholder={t('pod.filter')} value={filter} onChange={(e) => setFilter(e.target.value)} />
              <Segmented label={t('pod.level')} value={level} onChange={setLevel} options={[['all', t('pod.all')], ['warn', 'WARN+'], ['error', 'ERROR']]} />
              <Checkbox label={t('pod.follow')} checked={follow} onChange={(e) => setFollow(e.target.checked)} className="nopad" />
              <Button size="small" variant="quiet" style={{ marginLeft: 'auto' }} onClick={download} disabled={!logs.data}>
                {t('pod.download')}
              </Button>
            </ButtonRow>
            {logs.isPending && <Loading />}
            {logs.error && <ErrorState error={logs.error} onRetry={() => void logs.refetch()} />}
            {logs.data && (
              <div className="log" role="log" aria-label={t('pod.logLabel')}>
                {lines.length === 0 && <span className="ts">{t('pod.noLogs')}</span>}
                {lines.map((l, i) => {
                  const c = l.content.toLowerCase()
                  const cls = /error|fatal|panic|exception/.test(c) ? 'e' : /warn/.test(c) ? 'w' : ''
                  return (
                    <div key={i}>
                      <span className="ts">{fmtTime(l.timeStamp, true)}</span> <span className={cls}>{l.content}</span>
                    </div>
                  )
                })}
              </div>
            )}
            <Note>{t('pod.tailNote', { n: LOG_TAIL })}</Note>
          </>
        )}
        {allowed && tab === 'events' && (
          <>
            {!uid && <Banner>{t('pod.noUid')}</Banner>}
            {uid && events.isPending && <Loading />}
            {events.error && <ErrorState error={events.error} onRetry={() => void events.refetch()} />}
            {events.data && (
              <Group>
                {evs.length === 0 && <EmptyState>{t('pod.noEvents')}</EmptyState>}
                {evs.map((e, i) => (
                  <Row key={i}>
                    <StatusDot state={e.type === 'Warning' ? 'warn' : 'ok'} label={e.type} />
                    <div className="grow">
                      <div className="t">
                        {e.reason}
                        {e.count > 1 ? ` ×${e.count}` : ''}
                      </div>
                      <div className="d mono break">{e.message}</div>
                    </div>
                    <span className="v nowrap">{fmtTime(e.lastTimestamp ?? e.eventTime ?? e.firstTimestamp, true)}</span>
                  </Row>
                ))}
              </Group>
            )}
          </>
        )}
      </Page>
    </>
  )
}
