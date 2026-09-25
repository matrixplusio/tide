import { i18n } from '../../lib/i18n'
import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMe } from '../../app/session'
import { fmtDuration, fmtTime, sinceMs } from '../../lib/format'
import { itemStatusText } from '../../lib/status'
import type { Item, ItemKind, ItemLive, Live, Release } from '../../lib/types'
import { summarizeRollout } from '../../lib/rollout'
import { changeSummary } from '../../lib/release'
import { anomalyText, isNotice } from '../../lib/anomaly'
import { useCancelRelease, useDecideRelease, useRelease } from './queries'
import { subjectLabel } from '../../lib/permissions'
import {
  Banner,
  Textarea,
  Button,
  ButtonRow,
  FormErrorBanner,
  Modal,
  useToast,
  ErrorState,
  Group,
  GroupHeader,
  KV,
  LinkButton,
  Loading,
  Note,
  Page,
  Pill,
  Row,
  StatusDot,
  Toolbar,
} from '../../components/ui'
import { AutoPill, ConfigChanges, ConfirmSheet, JiraLink, ItemStatusPill, PodsList, PromotionSteps, ReleaseStatusPill, ResourcesList, RolloutView } from '../../components/domain'

export function ReleaseDetailPage() {
  const { t } = useTranslation()
  const { id = '' } = useParams()
  const me = useMe()
  const [sheet, setSheet] = useState(false)
  const q = useRelease(id)
  const r = q.data?.release
  const items = r?.items ?? []
  const mine = !!r && r.createdBy === me.user.sub
  // Confirming is for the creator only; cancelling someone else's release needs releases.cancel_any.
  // The server decides: permissions are scoped by project and service type.
  const can = q.data?.can
  const canConfirm = !!can?.confirm
  const canCancelOther = !mine && !!can?.cancel
  const canPods = !!can?.pods
  const [cancelOpen, setCancelOpen] = useState(false)
  const [deciding, setDeciding] = useState<'approve' | 'reject' | null>(null)
  // Fifty items is a page you scroll past, not one you read: the failures
  // are what somebody opened it for.
  const [itemFilter, setItemFilter] = useState('')
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(() => new Set())
  const toggleItem = (id: number) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  const cancelOwn = useCancelRelease()
  const toast = useToast()
  const firstService = items[0]?.payload.service

  return (
    <>
      <Toolbar title={<span className="mono">{id}</span>} sub={r?.title} back={{ to: '/releases', label: t('detail.back') }}>
        {r?.automatic && <AutoPill />}
        {r && <ReleaseStatusPill status={r.status} />}
        {r?.status === 'confirming' && canConfirm && (
          <Button variant="quiet" onClick={() => setSheet(true)}>
            {t('detail.confirm')}
          </Button>
        )}
        {r?.status === 'approving' && r.canApprove && (
          <>
            <Button onClick={() => setDeciding('approve')}>{t('detail.approve')}</Button>
            <Button variant="danger" onClick={() => setDeciding('reject')}>
              {t('detail.reject')}
            </Button>
          </>
        )}
        {r?.status === 'approving' && mine && (
          <Button
            variant="quiet"
            loading={cancelOwn.isPending}
            onClick={() => cancelOwn.mutate({ id: r.id, reason: 'withdrawn while awaiting approval' }, { onSuccess: () => toast.success(t('detail.withdrawn', { id: r.id })) })}
          >
            {t('detail.withdraw')}
          </Button>
        )}
        {(r?.status === 'confirming' || r?.status === 'approving') && canCancelOther && (
          <Button variant="danger" onClick={() => setCancelOpen(true)}>
            {t('detail.cancel')}
          </Button>
        )}
        {r?.status === 'failed' && firstService && <LinkButton to={`/services/${encodeURIComponent(firstService)}/envs/${encodeURIComponent(r.env)}`}>{t('detail.restart')}</LinkButton>}
      </Toolbar>
      <Page wide>
        {q.isPending && <Loading />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {r && (
          <>
            <div className="cols2">
              <div>
                <GroupHeader>{t('detail.release')}</GroupHeader>
                <Group>
                  <KV k="Jira">
                    <JiraLink ticket={r.jiraTicket} />
                  </KV>
                  <KV k={t('detail.env')}>{r.env}</KV>
                  <KV k={t('detail.creator')}>
                    {r.createdByName}
                    {r.source === 'ci' && <span className="muted"> · {t('detail.fromCI')}</span>}
                  </KV>
                  <KV k={t('detail.reason')}>
                    <span className="prewrap">{r.reason}</span>
                  </KV>
                </Group>
              </div>
              <div>
                <GroupHeader>{t('detail.progress')}</GroupHeader>
                <Group>
                  <Timeline r={r} />
                </Group>
                {items.length > 1 && (
                  <Note>{t('detail.batchNote')}</Note>
                )}
              </div>
            </div>

            {r.approvalRule && <ApprovalPanel r={r} />}

            {items.length > 1 && (
              <BatchOverview
                items={items}
                status={r.status}
                lives={q.data?.live}
                expanded={expanded}
                onToggle={toggleItem}
                onAll={(open) => setExpanded(open ? new Set(items.map((it) => it.id)) : new Set())}
                filter={itemFilter}
                onFilter={setItemFilter}
              />
            )}

            {items.map((it) =>
              (items.length > 1 && !expanded.has(it.id)) || (itemFilter && it.status !== itemFilter) ? null : (
                <ItemSection key={it.id} it={it} live={q.data?.live?.find((l) => l.itemId === it.id)} release={r} canPods={canPods} onCollapse={items.length > 1 ? () => toggleItem(it.id) : undefined} />
              ),
            )}
          </>
        )}
      </Page>
      {cancelOpen && r && <CancelReleaseModal release={r} onClose={() => setCancelOpen(false)} />}
      {deciding && r && <DecideModal release={r} approve={deciding === 'approve'} onClose={() => setDeciding(null)} />}
      {sheet && r && (
        <ConfirmSheet
          release={r}
          onClose={() => setSheet(false)}
          onDone={() => {
            setSheet(false)
            void q.refetch()
          }}
        />
      )}
    </>
  )
}

// Every code the server can attach needs an entry: one that is missing falls
// back to the bare word "anomaly", which tells the reader that something is
// notable without saying what — and first_deploy_per_kargo, the one that was
// missing, is the ordinary state of a pipeline that has just been rebuilt.
const ANOMALY_SHORT: Record<string, string> = {
  rollback: 'detail.anomalyRollback',
  first_deploy: 'detail.anomalyFirst',
  first_deploy_per_kargo: 'detail.anomalyFirstPerKargo',
  multi_version_jump: 'detail.anomalyJump',
  short_soak: 'detail.anomalySoak',
  config_drift: 'detail.anomalyDrift',
}
const kindJoin = (k: ItemKind): string => (k === 'image' ? '→' : k === 'restart' ? i18n.t('detail.kindRestart') : i18n.t('detail.kindSync'))

type Ev = { label: string; at?: string; state: 'done' | 'now' | 'todo'; who?: string }

function Timeline({ r }: { r: Release }) {
  const ev: Ev[] = [
    { label: i18n.t('detail.submitted'), at: r.submittedAt ?? r.createdAt, state: 'done', who: r.createdByName },
    {
      // "Confirmed" would claim somebody read it; on an automatic release
      // nobody did, and the step says so.
      label: r.status === 'cancelled' && !r.confirmedAt ? i18n.t('detail.cancelled') : r.automatic ? i18n.t('detail.autoReleased') : i18n.t('detail.confirmed'),
      at: r.confirmedAt ?? (r.status === 'cancelled' ? r.finishedAt : undefined),
      state: r.confirmedAt || r.status === 'cancelled' ? 'done' : r.status === 'confirming' ? 'now' : 'todo',
    },
  ]
  if (r.approvalRule && r.confirmedAt) {
    const decisions = r.approvals ?? []
    const last = decisions[decisions.length - 1]
    const approved = !['approving', 'rejected', 'cancelled'].includes(r.status)
    ev.push({
      label: r.status === 'rejected' ? i18n.t('detail.rejected') : r.status === 'approving' ? i18n.t('detail.approving') : approved ? i18n.t('detail.approved') : i18n.t('detail.approvalIncomplete'),
      at: last?.decidedAt,
      state: r.status === 'approving' ? 'now' : 'done',
      who: decisions.map((d) => d.name).join('、'),
    })
  }
  if (r.status === 'rejected') return <TimelineList ev={ev} />
  if (r.status !== 'cancelled' || (r.confirmedAt && !r.approvalRule)) {
    ev.push({ label: i18n.t('detail.execute'), at: r.approvalRule ? undefined : r.confirmedAt, state: r.status === 'executing' ? 'now' : r.finishedAt && r.status !== 'cancelled' ? 'done' : 'todo' })
    ev.push({ label: r.status === 'failed' ? i18n.t('detail.failed') : i18n.t('detail.done'), at: r.status === 'cancelled' ? undefined : r.finishedAt, state: r.finishedAt && r.status !== 'cancelled' ? 'done' : 'todo' })
  }
  return <TimelineList ev={ev} />
}

function TimelineList({ ev }: { ev: Ev[] }) {
  return (
    <ol className="timeline">
      {ev.map((e, i) => (
        <li key={i} className={e.state}>
          <div className="tl-rail">
            <StatusDot state={e.state === 'done' ? (e.label === i18n.t('detail.failed') || e.label === i18n.t('detail.rejected') ? 'bad' : 'ok') : e.state === 'now' ? 'run' : 'off'} className="tl-dot" />
            {i < ev.length - 1 && <span className="tl-line" />}
          </div>
          <div className="tl-body">
            <b>{e.label}</b>
            <span className="muted tl-meta">
              {e.at ? fmtTime(e.at, true) : ''} {e.who ?? ''}
            </span>
          </div>
        </li>
      ))}
    </ol>
  )
}

function CancelReleaseModal({ release, onClose }: { release: Release; onClose: () => void }) {
  const { t } = useTranslation()
  const cancel = useCancelRelease()
  const toast = useToast()
  return (
    <Modal title={t('detail.cancelTitle', { who: release.createdByName })} subtitle={<span className="mono">{release.id}</span>} onClose={onClose} closeOnEsc={!cancel.isPending}>
      <Note style={{ paddingTop: 0 }}>{t('detail.cancelNote')}</Note>
      <FormErrorBanner error={cancel.error} />
      <ButtonRow style={{ marginTop: 16 }}>
        <span className="grow" />
        <Button variant="quiet" onClick={onClose} disabled={cancel.isPending}>
          {t('detail.back2')}
        </Button>
        <Button
          variant="danger"
          loading={cancel.isPending}
          onClick={() =>
            cancel.mutate(
              { id: release.id, reason: 'cancelled by another user' },
              {
                onSuccess: () => {
                  toast.success(t('detail.cancelled2', { id: release.id }))
                  onClose()
                },
              },
            )
          }
        >
          {t('detail.cancel')}
        </Button>
      </ButtonRow>
    </Modal>
  )
}

function ItemSection({ it, live, release, canPods, onCollapse }: { it: Item; live?: ItemLive; release: Release; canPods: boolean; onCollapse?: () => void }) {
  const { t } = useTranslation()
  const p = it.payload
  const base = `/services/${encodeURIComponent(p.service)}/envs/${encodeURIComponent(p.env)}`
  const pushed = (live?.promotion?.steps ?? []).some((s) => /push/.test(s.uses + s.name) && s.status === 'Succeeded')
  return (
    <section id={`item-${it.id}`} aria-label={`${p.service} ${kindJoin(it.kind)} ${p.env}`}>
      <div className="ghead item-head">
        <Link to={base}>{p.service}</Link> {kindJoin(it.kind)} {p.env}
        <span className="r">
          <ItemStatusPill status={it.status} />
        </span>
        {onCollapse && (
          <Button variant="quiet" size="small" className="disclosure open" onClick={onCollapse} aria-expanded aria-label={t('detail.collapseDetail', { service: p.service })}>
            {t('detail.collapse')}
          </Button>
        )}
      </div>
      <div className="cols2">
        <div>
          <GroupHeader>{it.kind === 'restart' ? t('releases.kindRestart') : it.kind === 'sync' ? t('releases.kindSync') : t('detail.change')}</GroupHeader>
          <Group>
            {it.kind === 'sync' ? (
              <>
                <KV k={t('detail.action')}>
                  {t('detail.syncAction', { restart: it.payload.restart ? t('detail.syncRestartSuffix') : '', prune: it.payload.prune ? t('detail.syncPruneSuffix') : '' })}
                </KV>
                <KV k={t('detail.version')}>
                  {it.payload.current.version && <b>{it.payload.current.version} </b>}
                  <span className="mono">{it.payload.current.tag || t('detail.currentVersion')}</span>
                </KV>
                {it.payload.current.digest && (
                  <KV k="" mono>
                    {it.payload.current.digest}
                  </KV>
                )}
                <KV k="Argo CD" mono>
                  {it.payload.app}
                </KV>
                <KV k={t('detail.gitRevision')} mono>
                  {it.payload.revision}
                </KV>
                <KV k={t('detail.changes')}>{changeSummary(it.payload.changes ?? [])}</KV>
                {syncStartedAt(it) && <KV k={t('detail.startedAt')}>{fmtTime(syncStartedAt(it), true)}</KV>}
              </>
            ) : it.kind === 'restart' ? (
              <>
                <KV k={t('detail.action')}>{t('detail.restartAction')}</KV>
                <KV k={t('detail.version')}>
                  {it.payload.current.version && <b>{it.payload.current.version} </b>}
                  <span className="mono">{it.payload.current.tag || t('detail.currentVersion')}</span>
                </KV>
                {it.payload.current.digest && (
                  <KV k="" mono>
                    {it.payload.current.digest}
                  </KV>
                )}
                <KV k="Argo CD" mono>
                  {it.payload.app}
                </KV>
                {(it.payload.workloads ?? []).map((w) => (
                  <KV key={`${w.kind}/${w.namespace}/${w.name}`} k={w.kind} mono>
                    {w.namespace}/{w.name}
                  </KV>
                ))}
              </>
            ) : (
              <>
                <KV k={t('detail.from')}>
                  {it.payload.from ? (
                    <>
                      {it.payload.from.version && <b>{it.payload.from.version} </b>}
                      <span className="mono">{it.payload.from.tag}</span>
                    </>
                  ) : (
                    t('detail.firstDeploy')
                  )}
                </KV>
                {it.payload.from && (
                  <KV k="" mono>
                    {it.payload.from.digest}
                  </KV>
                )}
                <KV k={t('detail.to')}>
                  {it.payload.to.version && <b>{it.payload.to.version} </b>}
                  <span className="mono">{it.payload.to.tag}</span>
                </KV>
                <KV k="" mono>
                  {it.payload.to.digest}
                </KV>
                <KV k="Freight" mono>
                  {it.payload.freight}
                </KV>
                {it.externalRef && (
                  <KV k="Promotion" mono>
                    {it.externalRef}
                  </KV>
                )}
                {it.payload.withConfig && <KV k={t('detail.config')}>{t('detail.withConfig', { summary: changeSummary(it.payload.configChanges ?? []) })}</KV>}
                {(it.payload.anomalies ?? []).map((a, i) => (
                  <div key={i} className={`anomaly ${isNotice(a) ? 'notice' : ''} ${a.code}`}>
                    <b aria-hidden="true">{isNotice(a) ? 'ℹ' : '▲'}</b>
                    <span title={a.message}>{anomalyText(a)}</span>
                  </div>
                ))}
              </>
            )}
            {it.kind === 'restart' && it.externalRef && <KV k={t('detail.startedAt')}>{fmtTime(it.externalRef.replace(/^restart@/, ''), true)}</KV>}
          </Group>
          {it.kind === 'sync' && (
            <>
              <GroupHeader>{t('detail.configDiff', { count: (it.payload.changes ?? []).length })}</GroupHeader>
              <ConfigChanges changes={it.payload.changes ?? []} />
            </>
          )}
          {it.kind === 'image' && (it.payload.configChanges?.length ?? 0) > 0 && (
            <>
              <GroupHeader>{t('detail.configWithUpgrade', { count: it.payload.configChanges?.length })}</GroupHeader>
              <ConfigChanges changes={it.payload.configChanges ?? []} />
            </>
          )}
          {it.error && (
            <>
              <GroupHeader>{t('detail.errorText')}</GroupHeader>
              <div className="err">{it.error}</div>
            </>
          )}
          {it.status === 'executing' && live?.live && (it.kind !== 'image' || live.promotion?.phase === 'Succeeded') && <WaitingForPods live={live.live} kind={it.kind} since={restartSince(it)} />}
          {live?.promotion ? (
            <>
              <GroupHeader>{t('detail.promotionSteps')}</GroupHeader>
              <PromotionSteps p={live.promotion} />
            </>
          ) : (
            // An item is "executing" from the moment the release starts, but
            // the promotion only exists once the worker reaches this item —
            // in a batch, that can be a while. Saying so beats an empty space
            // that reads as a step list which is failing to load.
            it.kind === 'image' &&
            it.status === 'executing' && (
              <>
                <GroupHeader>{t('detail.promotionSteps')}</GroupHeader>
                <Group>
                  <Row>
                    <div className="grow d">{t('detail.promotionQueued')}</div>
                  </Row>
                </Group>
              </>
            )
          )}
          {live?.error && <ErrorState error={live.error} />}
          {it.status === 'failed' && live?.live && (
            <>
              <GroupHeader>{t('detail.impact')}</GroupHeader>
              <Group>
                <Row>
                  <div className="grow">
                    <div className="t">{live.live.health === 'Healthy' ? t('detail.stillHealthy') : t('detail.serviceHealth', { health: live.live.health })}</div>
                    <div className="d">{(live.live.pods ?? []).some((x) => x.isTarget) ? t('detail.hasTargetPods') : t('detail.noTargetPods')}</div>
                  </div>
                  <Pill tone={live.live.health === 'Healthy' ? 'green' : 'red'}>{live.live.health}</Pill>
                </Row>
                <Row>
                  <div className="grow">
                    <div className="t">{t('detail.gitChanged')}</div>
                    <div className="d">{pushed ? t('detail.gitPushed') : t('detail.gitNotPushed')}</div>
                  </div>
                </Row>
              </Group>
            </>
          )}
        </div>
        <div>
          {live?.live && release.status !== 'confirming' && (
            <>
              {it.startedAt && (
                <>
                  <GroupHeader>{t('detail.deployState')}</GroupHeader>
                  <RolloutView live={live.live} since={it.status === 'executing' ? restartSince(it) : undefined} />
                </>
              )}
              <GroupHeader>Pod</GroupHeader>
              <PodsList live={live.live} base={canPods ? base : undefined} />
              <GroupHeader>{t('detail.resources')}</GroupHeader>
              <ResourcesList live={live.live} />
            </>
          )}
        </div>
      </div>
    </section>
  )
}

/** Kargo is done but the release is not: say what it still waits for. */
function WaitingForPods({ live, kind, since }: { live: Live; kind: string; since?: string }) {
  const { t } = useTranslation()
  const parts = (live.rollouts ?? []).map((r) => summarizeRollout(r, live, since)).filter((s) => !s.done)
  return (
    <Banner tone={parts.some((s) => s.unhealthy) ? 'warn' : 'info'}>
      <div>
        <b>{kind === 'restart' ? t('detail.waitRestart') : kind === 'sync' ? t('detail.waitSync') : t('detail.waitUpgrade')}</b>
        {parts.map((s) => (
          <div key={s.name} className="d">
            {t('detail.rolloutLine', { name: s.name, ready: s.targetReady, desired: s.desired })}
            {s.other > 0 && t('detail.oldRemain', { count: s.other })}
            {s.unhealthy && t('detail.podsUnhealthy')}
          </div>
        ))}
        <div className="d">{t('detail.waitNote')}</div>
      </div>
    </Banner>
  )
}

/** When a restart or sync started (from its external reference): pods created after it are the new ones. */
function restartSince(it: Item): string | undefined {
  if (it.kind === 'restart' && it.externalRef) return it.externalRef.replace(/^restart@/, '')
  return syncStartedAt(it)
}

function syncStartedAt(it: Item): string | undefined {
  return it.kind === 'sync' && it.externalRef ? it.externalRef.split('@')[1] : undefined
}

/** Every service of a batch at a glance: order, change, status and how many new pods are ready. */
function BatchOverview({ items, status, lives, expanded, onToggle, onAll, filter, onFilter }: { items: Item[]; status: Release['status']; lives?: ItemLive[] | null; expanded: ReadonlySet<number>; onToggle: (id: number) => void; onAll: (open: boolean) => void; filter: string; onFilter: (s: string) => void }) {
  const { t } = useTranslation()
  const sorted = [...items].sort((a, b) => a.sequence - b.sequence || a.payload.service.localeCompare(b.payload.service)).filter((it) => !filter || it.status === filter)
  const rows = sorted.map((it) => {
    const live = lives?.find((l) => l.itemId === it.id)?.live
    const since = it.status === 'executing' ? restartSince(it) : undefined
    const rollouts = live ? (live.rollouts ?? []).map((r) => summarizeRollout(r, live, since)) : []
    return {
      it,
      ready: rollouts.reduce((n, s) => n + Math.min(s.targetReady, s.desired), 0),
      desired: rollouts.reduce((n, s) => n + s.desired, 0),
      old: rollouts.reduce((n, s) => n + s.other, 0),
      unhealthy: rollouts.some((s) => s.unhealthy),
      showPods: !!it.startedAt,
    }
  })
  const done = items.filter((it) => !['planned', 'pending', 'executing'].includes(it.status)).length
  const counts = (['succeeded', 'executing', 'pending', 'failed', 'skipped', 'cancelled'] as const)
    .map((st) => [st, items.filter((it) => it.status === st).length] as const)
    .filter(([, n]) => n > 0)
  const podReady = rows.reduce((n, r) => n + (r.showPods ? r.ready : 0), 0)
  const podWant = rows.reduce((n, r) => n + (r.showPods ? r.desired : 0), 0)
  const pct = Math.round((done / items.length) * 100)
  const allOpen = expanded.size === items.length
  const now = Date.now()
  return (
    <>
      <div className="ghead item-head">
        {t('detail.overview', { count: items.length })}
        <Button variant="quiet" size="small" className={`disclosure ${allOpen ? 'open' : ''}`} aria-expanded={allOpen} onClick={() => onAll(!allOpen)}>
          {allOpen ? t('detail.collapseAll') : t('detail.expandAll')}
        </Button>
      </div>
      <Group>
        <Row>
          <div className="grow">
            <div className="t">
              {t('detail.finished', { done, total: items.length })}
              {podWant > 0 && <span className="muted">{t('detail.podsReady', { ready: podReady, want: podWant })}</span>}
            </div>
            {/* The counts were already the summary; clicking one is the
                shortest way from "two failed" to the two. */}
            <div className="d btnrow" role="group" aria-label={t('detail.filterByStatus')}>
              {counts.map(([st, n]) => (
                <button key={st} type="button" className="statfilter" aria-pressed={filter === st} onClick={() => onFilter(filter === st ? '' : st)}>
                  {itemStatusText(st)} {n}
                </button>
              ))}
              {filter && (
                <button type="button" className="statfilter" onClick={() => onFilter('')}>
                  {t('detail.filterClear')}
                </button>
              )}
            </div>
            <div className={`prog ${items.some((it) => it.status === 'failed') ? 'bad' : ''}`} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={t('detail.batchProgress')}>
              <i style={{ width: `${pct}%` }} />
            </div>
          </div>
        </Row>
      </Group>
      <div className="group" style={{ overflowX: 'auto', marginTop: 10 }}>
        <table className="tbl batch-overview">
          <thead>
            <tr>
              <th scope="col">{t('detail.colOrder')}</th>
              <th scope="col">{t('detail.colService')}</th>
              <th scope="col">{t('detail.colChange')}</th>
              <th scope="col">{t('detail.colStatus')}</th>
              <th scope="col">{t('detail.colPods')}</th>
              <th scope="col">{t('detail.colDuration')}</th>
              <th scope="col">
                <span className="sr-only">{t('detail.colDetail')}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ it, ready, desired, old, unhealthy, showPods }) => {
              const pct = desired ? Math.round((ready / desired) * 100) : 0
              const start = sinceMs(it.startedAt)
              const end = sinceMs(it.finishedAt) ?? (it.status === 'executing' ? now : null)
              const open = expanded.has(it.id)
              return (
                <tr key={it.id} className={it.status === 'failed' ? 'bad' : undefined}>
                  <td className="mono">{it.sequence}</td>
                  <td>
                    <Link to={`/services/${encodeURIComponent(it.payload.service)}/envs/${encodeURIComponent(it.payload.env)}`} style={{ fontWeight: 600 }}>
                      {it.payload.service}
                    </Link>
                  </td>
                  <td>
                    {it.kind === 'sync' ? (
                      <span>{t('detail.syncCell', { summary: changeSummary(it.payload.changes ?? []) })}</span>
                    ) : it.kind === 'restart' ? (
                      <span>{t('detail.restartCell', { version: it.payload.current.version || it.payload.current.tag || t('detail.currentVersion') })}</span>
                    ) : (
                      <span title={`${it.payload.from?.tag ?? ''} → ${it.payload.to.tag}`}>
                        {it.payload.from?.version || it.payload.from?.tag || t('detail.nothing')} → <b>{it.payload.to.version || it.payload.to.tag}</b>
                        {(it.payload.anomalies?.length ?? 0) > 0 && (
                          <>
                            {' '}
                            <Pill tone={(it.payload.anomalies ?? []).every(isNotice) ? 'neutral' : 'orange'}>
                              {(it.payload.anomalies ?? []).map((a) => t(ANOMALY_SHORT[a.code] ?? 'detail.anomalyPill')).join(t('scope.listSeparator'))}
                            </Pill>
                          </>
                        )}
                        {it.payload.withConfig && (it.payload.configChanges?.length ?? 0) > 0 && (
                          <>
                            {' '}
                            <Pill tone="blue">{t('detail.withConfigPill', { summary: changeSummary(it.payload.configChanges ?? []) })}</Pill>
                          </>
                        )}
                      </span>
                    )}
                  </td>
                  <td>
                    <ItemStatusPill status={it.status} />
                    {it.error && (
                      <div className="d red clamp1" title={it.error}>
                        {it.error}
                      </div>
                    )}
                  </td>
                  <td style={{ minWidth: 180 }}>
                    {showPods && desired > 0 ? (
                      <>
                        <span className={unhealthy ? 'red' : undefined}>
                          {t('detail.readyCell', { ready, desired, old: old > 0 ? t('detail.oldCell', { count: old }) : '' })}
                          {unhealthy ? t('detail.unhealthyCell') : ''}
                        </span>
                        <div className={`prog ${unhealthy ? 'bad' : ''}`} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={t('detail.rolloutLabel', { service: it.payload.service })}>
                          <i style={{ width: `${pct}%` }} />
                        </div>
                      </>
                    ) : (
                      <span className="muted">{it.status !== 'pending' ? '—' : status === 'approving' ? t('detail.waitingApproval') : status === 'confirming' ? t('detail.waitingConfirm') : t('detail.waitingPrior')}</span>
                    )}
                  </td>
                  <td className="mono muted">{start && end ? fmtDuration(end - start) : '—'}</td>
                  <td>
                    <Button variant="quiet" size="small" className={`disclosure ${open ? 'open' : ''}`} aria-expanded={open} aria-label={t('detail.toggleDetail', { action: open ? t('detail.collapse') : t('detail.expand'), service: it.payload.service })} onClick={() => onToggle(it.id)}>
                      {open ? t('detail.collapse') : t('detail.expand')}
                    </Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </>
  )
}

const modeText = (rule: NonNullable<Release['approvalRule']>) =>
  rule.mode === 'all' ? i18n.t('detail.modeAll') : rule.mode === 'count' ? i18n.t('detail.modeCount', { count: rule.minApprovals }) : i18n.t('detail.modeAny')

/** Who has to approve, who did, and until when. */
function ApprovalPanel({ r }: { r: Release }) {
  const { t } = useTranslation()
  const rule = r.approvalRule
  if (!rule) return null
  const decisions = r.approvals ?? []
  const approvedCount = decisions.filter((d) => d.decision === 'approve').length
  return (
    <>
      <GroupHeader>{t('detail.approvalHeader', { rule: rule.name })}</GroupHeader>
      <Group>
        <KV k={t('detail.mode')}>
          {modeText(rule)}
          {r.status === 'approving' && t('detail.approvedCount', { count: approvedCount })}
        </KV>
        <KV k={t('detail.approvers')}>{(rule.approvers ?? []).map((a) => subjectLabel(a)).join(t('scope.listSeparator'))}</KV>
        {r.status === 'approving' && r.approvalExpiresAt && <KV k={t('detail.deadline')}>{t('detail.deadlineNote', { at: fmtTime(r.approvalExpiresAt, true) })}</KV>}
        {decisions.length === 0 && (
          <Row>
            <span className="muted">{t('detail.noDecisions')}</span>
          </Row>
        )}
        {decisions.map((d) => (
          <Row key={d.sub}>
            <StatusDot state={d.decision === 'approve' ? 'ok' : 'bad'} label={d.decision === 'approve' ? t('detail.approve') : t('detail.reject')} />
            <div className="grow">
              <div className="t">
                {d.name} <Pill tone={d.decision === 'approve' ? 'green' : 'red'}>{d.decision === 'approve' ? t('detail.approve') : t('detail.reject')}</Pill>
              </div>
              {d.note && <div className="d prewrap">{d.note}</div>}
            </div>
            <span className="muted nowrap">{fmtTime(d.decidedAt, true)}</span>
          </Row>
        ))}
      </Group>
      {r.status === 'approving' && !r.canApprove && <Note>{t('detail.waitingApprovers')}</Note>}
    </>
  )
}

function DecideModal({ release, approve, onClose }: { release: Release; approve: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const decide = useDecideRelease()
  const toast = useToast()
  const [note, setNote] = useState('')
  const [touched, setTouched] = useState(false)
  const noteError = !approve && note.trim().length < 2 ? t('detail.rejectReasonRequired') : note.length > 500 ? t('detail.tooLong500') : null
  const submit = () => {
    setTouched(true)
    if (noteError) return
    decide.mutate(
      { id: release.id, approve, note: note.trim() },
      {
        onSuccess: (r) => {
          toast.success(approve ? (r.status === 'executing' ? t('detail.approvedAndRunning', { id: release.id }) : t('detail.approvedToast', { id: release.id })) : t('detail.rejectedToast', { id: release.id }))
          onClose()
        },
      },
    )
  }
  return (
    <Modal title={t('detail.decideTitle', { action: approve ? t('detail.approve') : t('detail.reject'), who: release.createdByName })} subtitle={<span className="mono">{release.id}</span>} onClose={onClose} closeOnEsc={!decide.isPending}>
      <Note style={{ paddingTop: 0 }}>{approve ? t('detail.approveNote') : t('detail.rejectNote')}</Note>
      <label className="flabel" htmlFor="decide-note">
        {approve ? t('detail.approveNoteLabel') : t('detail.rejectNoteLabel')}
      </label>
      <Textarea
        id="decide-note"
        value={note}
        onChange={(e) => setNote(e.target.value)}
        onBlur={() => setTouched(true)}
        aria-invalid={touched && !!noteError}
        placeholder={approve ? t('detail.approvePlaceholder') : t('detail.rejectPlaceholder')}
      />
      {touched && noteError && (
        <div className="ferr standalone" role="alert">
          {noteError}
        </div>
      )}
      <FormErrorBanner error={decide.error} />
      <ButtonRow style={{ marginTop: 16 }}>
        <span className="grow" />
        <Button variant="quiet" onClick={onClose} disabled={decide.isPending}>
          {t('detail.back2')}
        </Button>
        <Button variant={approve ? 'primary' : 'danger'} loading={decide.isPending} onClick={submit}>
          {approve ? t('detail.approve') : t('detail.reject')}
        </Button>
      </ButtonRow>
    </Modal>
  )
}

