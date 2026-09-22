import { useTranslation } from 'react-i18next'
import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { ReleaseStatus } from '../../lib/types'
import { Button, EmptyState, ErrorState, Group, Input, LinkButton, Loading, Note, Page, Pager, Segmented, Select, Toolbar } from '../../components/ui'
import { useMe } from '../../app/session'
import { canEnv, canView } from '../../lib/permissions'
import { ReleaseRow } from '../../components/domain'
import { dayBound } from '../../lib/format'
import { RELEASE_STATUSES, releaseStatusText } from '../../lib/status'
import { useServices } from '../services/queries'
import { PAGE_SIZE, useReleases, type ReleaseFilter } from './queries'

type View = 'all' | 'mine' | 'awaiting' | 'decided' | 'active'
const views = [
  ['all', 'releases.viewAll'],
  ['mine', 'releases.viewMine'],
  ['awaiting', 'releases.viewAwaiting'],
  ['decided', 'releases.viewDecided'],
  ['active', 'releases.viewActive'],
] as const
const ACTIVE: ReleaseStatus[] = ['confirming', 'approving', 'executing']
const TEXT_FILTERS = ['service', 'creator', 'jira'] as const
type TextFilter = (typeof TEXT_FILTERS)[number]
const SCOPE_KEYS = ['status', 'env', 'project', 'kind', 'since', 'until', ...TEXT_FILTERS]

function parsePage(v: string | null): number {
  const n = Number(v)
  return Number.isInteger(n) && n >= 1 ? n : 1
}


export function ReleasesPage() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const get = (k: string) => params.get(k) ?? ''
  const rawView = get('view') || (get('tab') === 'failed' ? '' : get('tab'))
  const view: View = views.some(([v]) => v === rawView) ? (rawView as View) : 'all'
  const status = (get('status') || (get('tab') === 'failed' ? 'failed' : '')) as ReleaseStatus | ''
  const page = parsePage(params.get('page'))
  const [drafts, setDrafts] = useState<Record<TextFilter, string>>(() => ({ service: get('service'), creator: get('creator'), jira: get('jira') }))

  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])

  // Every filter change goes back to page 1.
  const update = (patch: Record<string, string>) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev)
        p.delete('tab')
        p.delete('page')
        for (const [k, v] of Object.entries(patch)) {
          if (v) p.set(k, v)
          else p.delete(k)
        }
        return p
      },
      { replace: true },
    )

  // Debounce free-text filters.
  const onText = (k: TextFilter, v: string) => {
    setDrafts((d) => ({ ...d, [k]: v }))
    clearTimeout(timer.current)
    timer.current = setTimeout(() => update({ [k]: k === 'jira' ? v.trim().toUpperCase() : v.trim() }), 300)
  }
  const clearScope = () => {
    clearTimeout(timer.current)
    setDrafts({ service: '', creator: '', jira: '' })
    update(Object.fromEntries(SCOPE_KEYS.map((k) => [k, ''])))
  }

  const me = useMe()
  const canBatch = canView(me, 'services.view') && (me.environments ?? []).some((e) => canEnv(me, 'releases.create', e.name) || canEnv(me, 'releases.restart', e.name))
  const services = useServices(canView(me, 'services.view'))
  const projects = [...new Set((services.data?.services ?? []).map((s) => s.project ?? '').filter(Boolean))].sort()
  const envs = me.environments ?? []

  const kind = get('kind')
  const since = get('since')
  const until = get('until')
  const filter: ReleaseFilter = {
    status: status ? [status] : view === 'active' ? ACTIVE : undefined,
    env: get('env') || undefined,
    project: get('project') || undefined,
    kind: kind === 'image' || kind === 'restart' || kind === 'sync' ? kind : undefined,
    service: get('service') || undefined,
    creator: get('creator') || undefined,
    jira: get('jira') || undefined,
    since: dayBound(since, false),
    until: dayBound(until, true),
    mine: view === 'mine' || undefined,
    decided: view === 'decided' || undefined,
    awaiting: view === 'awaiting' || undefined,
    page,
  }
  const badRange = !!since && !!until && since > until
  const q = useReleases(filter, { refetchInterval: 10_000 })
  const list = q.data?.items ?? []
  const scoped = SCOPE_KEYS.some((k) => get(k))
  const statusOptions = (view === 'active' ? ACTIVE : view === 'awaiting' ? [] : RELEASE_STATUSES).map((s): [string, string] => [s, releaseStatusText(s)])

  return (
    <>
      <Toolbar title={t('releases.title')}>
        <Segmented label={t('releases.scope')} value={view} options={views.map(([k, key]) => [k, t(key)] as const)} onChange={(v) => update({ view: v === 'all' ? '' : v, status: '' })} />
        {canBatch && (
          <LinkButton size="small" to="/releases/batch">
            {t('releases.batch')}
          </LinkButton>
        )}
      </Toolbar>
      <Page>
        <div className="release-filters" role="search" aria-label={t('releases.filterLabel')}>
          {view !== 'awaiting' && <Select appearance="filled" aria-label={t('releases.status')} value={status} options={[['', t('releases.allStatuses')], ...statusOptions]} onChange={(e) => update({ status: e.target.value })} />}
          <Select appearance="filled" aria-label={t('releases.env')} value={get('env')} options={[['', t('releases.allEnvs')], ...envs.map((e): [string, string] => [e.name, e.displayName ? `${e.displayName} ${e.name}` : e.name])]} onChange={(e) => update({ env: e.target.value })} />
          {projects.length > 0 && <Select appearance="filled" aria-label={t('releases.project')} value={get('project')} options={[['', t('releases.allProjects')], ...projects.map((p): [string, string] => [p, p])]} onChange={(e) => update({ project: e.target.value })} />}
          <Select
            appearance="filled"
            aria-label={t('releases.changeType')}
            value={kind}
            options={[
              ['', t('releases.allKinds')],
              ['image', t('releases.kindImage')],
              ['sync', t('releases.kindSync')],
              ['restart', t('releases.kindRestart')],
            ]}
            onChange={(e) => update({ kind: e.target.value })}
          />
          <Input appearance="filled" type="search" aria-label={t('releases.byService')} placeholder={t('releases.servicePlaceholder')} value={drafts.service} onChange={(e) => onText('service', e.target.value)} />
          <Input appearance="filled" type="search" aria-label={t('releases.byCreator')} placeholder={view === 'mine' ? t('releases.creatorMe') : t('releases.creator')} disabled={view === 'mine'} value={view === 'mine' ? '' : drafts.creator} onChange={(e) => onText('creator', e.target.value)} />
          <Input appearance="filled" type="search" aria-label={t('releases.byJira')} placeholder={t('releases.jira')} value={drafts.jira} onChange={(e) => onText('jira', e.target.value)} />
          <div className="range">
            <Input appearance="filled" type="date" aria-label={t('releases.dateFrom')} aria-invalid={badRange} value={since} max={until || undefined} onChange={(e) => update({ since: e.target.value })} />
            <span className="muted">{t('releases.to')}</span>
            <Input appearance="filled" type="date" aria-label={t('releases.dateTo')} aria-invalid={badRange} value={until} min={since || undefined} onChange={(e) => update({ until: e.target.value })} />
          </div>
        </div>
        <div className="filter-summary">
          <span className="muted">{q.data ? t('releases.total', { count: q.data.total }) : ''}</span>
          {scoped && (
            <Button size="small" variant="quiet" className="disclosure-plain" onClick={clearScope}>
              {t('releases.clearFilters')}
            </Button>
          )}
        </div>
        {badRange && <p className="field-error red">{t('releases.badRange')}</p>}
        {!badRange && q.isPending && <Loading />}
        {!badRange && q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {!badRange && q.data && (
          <>
            <Group>
              {list.length === 0 && <EmptyState>{scoped ? t('releases.noneFiltered') : view === 'awaiting' ? t('releases.noneAwaiting') : view === 'mine' ? t('releases.noneMine') : view === 'decided' ? t('releases.noneDecided') : t('releases.none')}</EmptyState>}
              {list.map((r) => (
                <ReleaseRow key={r.id} r={r} />
              ))}
            </Group>
            <Pager page={q.data.page || page} pageSize={q.data.page_size || PAGE_SIZE} total={q.data.total} onChange={(n) => update({ page: n > 1 ? String(n) : '' })} />
          </>
        )}
        <Note>{t('releases.hint')}</Note>
      </Page>
    </>
  )
}
