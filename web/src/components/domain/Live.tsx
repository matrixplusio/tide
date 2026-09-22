import { useTranslation } from 'react-i18next'
import { i18n } from '../../lib/i18n'
import { summarizeRollout } from '../../lib/rollout'
import type { Live, PromotionView } from '../../lib/types'
import { fmtDuration, fmtTime } from '../../lib/format'
import { EmptyState, ErrorState, Group, Pill, Row, StatusDot, type DotState } from '../ui'
import { phaseTone } from './tones'
import { HealthPill } from './labels'

/** `since`: restart start time; restarted pods run the same image, so age tells old from new. */
export function RolloutView({ live, since }: { live: Live; since?: string }) {
  const { t } = useTranslation()
  const pods = live.pods ?? []
  const rollouts = live.rollouts ?? []
  return (
    <Group>
      {rollouts.map((r) => {
        const s = summarizeRollout(r, live, since)
        const pct = s.desired ? Math.round((Math.min(s.targetReady, s.desired) / s.desired) * 100) : 0
        return (
          <Row key={r.name}>
            <div className="grow">
              <div className="t">
                {s.done ? t('live.ready') : s.unhealthy ? t('live.unhealthy') : t('live.rolling')} · <span className="mono">{r.name}</span>
              </div>
              <div className="d">
                {t('live.targetReady', { ready: s.targetReady, desired: s.desired })}
                {s.target > s.targetReady && t('live.notReady', { count: s.target - s.targetReady })}
                {s.other > 0 && t('live.oldRemain', { count: s.other })}
              </div>
              <div className={`prog ${s.unhealthy ? 'bad' : ''}`} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={t('live.progressLabel', { name: r.name })}>
                <i style={{ width: `${pct}%` }} />
              </div>
            </div>
          </Row>
        )
      })}
      {pods.length > 0 && (
        <Row>
          <div className="grow">
            <div className="d">{t('live.podLegend')}</div>
            <div className="pods">
              {pods.map((p) => (
                <span key={p.name} className={`podbar ${p.isTarget ? 'new' : 'old'}`} title={`${p.name} ${p.status || p.health}`} />
              ))}
            </div>
          </div>
        </Row>
      )}
      {rollouts.length === 0 && pods.length === 0 && <EmptyState>{t('live.noWorkloads')}</EmptyState>}
    </Group>
  )
}

function podState(health: string): DotState {
  return health === 'Healthy' ? 'ok' : health === 'Progressing' ? 'run' : health ? 'bad' : 'off'
}

/** Pods link to their logs and events only when `base` is given (the viewer has pods.view). */
export function PodsList({ live, base }: { live: Live; base?: string }) {
  const { t } = useTranslation()
  const pods = live.pods ?? []
  return (
    <Group>
      {pods.length === 0 && <EmptyState>{t('live.noPods')}</EmptyState>}
      {pods.map((p) => (
        <Row key={p.name} to={base ? `${base}/pods/${encodeURIComponent(p.name)}?uid=${encodeURIComponent(p.uid)}` : undefined}>
          <StatusDot state={podState(p.health)} label={p.health || t('live.unknown')} />
          <div className="grow">
            <div className="t mono ellipsis">{p.name}</div>
            <div className="d">
              {t('live.podLine', { version: p.isTarget ? t('live.targetVersion') : t('live.otherVersion'), status: p.status || p.health, ready: p.ready, restarts: p.restarts || 0 })}
              {p.message ? ` · ${p.message}` : ''}
            </div>
          </div>
          <span className="v">{p.createdAt ? fmtDuration(Date.now() - new Date(p.createdAt).getTime()) : ''}</span>
        </Row>
      ))}
    </Group>
  )
}

export function ResourcesList({ live }: { live: Live }) {
  const { t } = useTranslation()
  const resources = live.resources ?? []
  return (
    <Group>
      {live.error && (
        <Row>
          <div className="grow">
            <ErrorState error={live.error} inline />
          </div>
        </Row>
      )}
      {resources.map((r) => {
        const state: DotState = r.sync === 'Synced' && (!r.health || r.health === 'Healthy') ? 'ok' : r.health === 'Progressing' ? 'run' : r.health && r.health !== 'Healthy' ? 'bad' : 'warn'
        return (
          <Row key={r.kind + r.namespace + r.name}>
            <StatusDot state={state} label={`${r.sync}${r.health ? ` / ${r.health}` : ''}`} />
            <div className="grow">
              <div className="t">{r.kind}</div>
              <div className="d mono ellipsis">
                {r.name}
                {r.message ? ` — ${r.message}` : ''}
              </div>
            </div>
            <HealthPill health={r.health} sync={r.sync} />
          </Row>
        )
      })}
      {!live.error && resources.length === 0 && <EmptyState>{t('live.noResources')}</EmptyState>}
    </Group>
  )
}

function StepIcon({ status }: { status: string }) {
  switch (status) {
    case 'Succeeded':
      return <span className="stepi ok" role="img" aria-label={i18n.t('live.stepSucceeded')}>✓</span>
    case 'Running':
      return <span className="stepi run" role="img" aria-label={i18n.t('live.stepRunning')} />
    case 'Failed':
    case 'Errored':
    case 'Aborted':
      return <span className="stepi bad" role="img" aria-label={i18n.t('live.stepFailed')}>✕</span>
    case 'Skipped':
      return <span className="stepi skip" role="img" aria-label={i18n.t('live.stepSkipped')} />
    default:
      return <span className="stepi todo" role="img" aria-label={i18n.t('live.stepPending')} />
  }
}

const stepNames: Record<string, string> = {
  'git-clone': 'live.step.gitClone',
  'kustomize-set-image': 'live.step.setImage',
  'git-commit': 'live.step.gitCommit',
  'git-push': 'live.step.gitPush',
  'argocd-update': 'live.step.argocdUpdate',
  'git-open-pr': 'live.step.openPr',
  'git-wait-for-pr': 'live.step.waitPr',
  'yaml-update': 'live.step.yamlUpdate',
  'helm-update-image': 'live.step.helmUpdateImage',
}

function stepLabel(uses: string, name: string) {
  const direct = stepNames[uses]
  if (direct) return direct
  for (const [k, v] of Object.entries(stepNames)) if (name.includes(k)) return v
  if (/update-image/.test(name)) return i18n.t('live.step.setImage')
  if (/commit/.test(name)) return i18n.t('live.step.gitCommit')
  return uses || name
}

export function PromotionSteps({ p }: { p: PromotionView }) {
  return (
    <Group>
      <Row>
        <div className="grow">
          <div className="t mono ellipsis">{p.name}</div>
          <div className="d">
            {p.phase || 'Pending'} · {fmtTime(p.createdAt, true)}
            {p.finishedAt && ` → ${fmtTime(p.finishedAt, true)}`}
          </div>
        </div>
        <Pill tone={phaseTone(p.phase)}>{p.phase || 'Pending'}</Pill>
      </Row>
      {(p.steps ?? []).map((s, i) => (
        <div key={i}>
          <Row>
            <StepIcon status={s.status} />
            <div className="grow">
              <div className="t">{stepLabel(s.uses, s.name)}</div>
              <div className="d mono">{s.name}</div>
            </div>
            <span className="v">
              {s.startedAt && s.finishedAt
                ? fmtDuration(new Date(s.finishedAt).getTime() - new Date(s.startedAt).getTime())
                : s.status === 'Running' && s.startedAt
                  ? i18n.t('live.waited', { duration: fmtDuration(Date.now() - new Date(s.startedAt).getTime()) })
                  : ''}
            </span>
          </Row>
          {s.message && s.status !== 'Succeeded' && (
            <div className="inset">
              <div className="err">{s.message}</div>
            </div>
          )}
        </div>
      ))}
      {p.message && ['Failed', 'Errored', 'Aborted'].includes(p.phase) && (
        <div className="inset">
          <div className="err">{p.message}</div>
        </div>
      )}
    </Group>
  )
}
