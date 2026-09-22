import { i18n } from '../../lib/i18n'
import type { Deployment, ItemStatus, ReleaseStatus } from '../../lib/types'
import { shortTag } from '../../lib/format'
import { Pill, StatusDot, type Tone } from '../ui'

export function VersionLabel({ a, full }: { a?: { version?: string; tag?: string } | null; full?: boolean }) {
  if (!a || (!a.tag && !a.version)) return <span className="faint">{i18n.t('domain.notDeployed')}</span>
  return (
    <span>
      {a.version && <b>{a.version} </b>}
      {/* two layers: bold semver first, build tag secondary; without a semver the tag is the identifier */}
      <span className={a.version ? 'mono muted tag' : 'mono'}>{full ? a.tag : shortTag(a.tag)}</span>
    </span>
  )
}

export function DeployDot({ d, inFlight }: { d?: Deployment; inFlight?: boolean }) {
  if (!d || (!d.tag && !d.freight)) return <StatusDot state="off" label={i18n.t('domain.notDeployed')} />
  if (inFlight || d.promoting || d.health === 'Progressing' || d.operation === 'Running') return <StatusDot state="run" label={i18n.t('domain.running')} />
  if (d.health === 'Degraded' || d.health === 'Missing' || d.operation === 'Failed' || d.operation === 'Error') return <StatusDot state="bad" label={d.health || i18n.t('domain.failed')} />
  if (d.sync === 'OutOfSync' || d.health !== 'Healthy') return <StatusDot state="warn" label={`${d.sync} / ${d.health}`} />
  return <StatusDot state="ok" label="Healthy" />
}

// The pill holds a catalogue key and a tone; the key is rendered when shown.
const releaseLabel: Record<ReleaseStatus, [string, Tone]> = {
  draft: ['status.release.draft', 'neutral'],
  confirming: ['status.release.confirming', 'orange'],
  approving: ['status.release.approving', 'blue'],
  executing: ['status.release.executing', 'orange'],
  succeeded: ['status.release.succeeded', 'green'],
  failed: ['status.release.failed', 'red'],
  cancelled: ['status.release.cancelled', 'neutral'],
  rejected: ['status.release.rejected', 'red'],
}

export function ReleaseStatusPill({ status }: { status: ReleaseStatus }) {
  const [key, tone] = releaseLabel[status] ?? [status, 'neutral']
  return <Pill tone={tone}>{i18n.t(key, { defaultValue: status })}</Pill>
}

// A release nobody let through is marked wherever a release is listed: the
// difference between "a machine released this" and "somebody decided to" is
// the first thing you want to see, not something to infer from the actor.
export function AutoPill({ title }: { title?: string }) {
  return (
    <Pill tone="blue" title={title ?? i18n.t('domain.autoHint')}>
      <span className="pill-ico">
        <svg width="8" height="11" viewBox="0 0 8 11" fill="currentColor" aria-hidden="true">
          <path d="M5 0 0 6.2h2.6L3 11 8 4.8H5.4z" />
        </svg>
        {i18n.t('domain.auto')}
      </span>
    </Pill>
  )
}

const itemLabel: Record<ItemStatus, [string, Tone]> = {
  planned: ['status.item.planned', 'neutral'],
  pending: ['status.item.pending', 'neutral'],
  executing: ['status.item.executing', 'orange'],
  succeeded: ['status.item.succeeded', 'green'],
  failed: ['status.item.failed', 'red'],
  skipped: ['status.item.skipped', 'neutral'],
  cancelled: ['status.item.cancelled', 'neutral'],
}

export function ItemStatusPill({ status }: { status: ItemStatus }) {
  const [key, tone] = itemLabel[status] ?? [status, 'neutral']
  return <Pill tone={tone}>{i18n.t(key, { defaultValue: status })}</Pill>
}

export function HealthPill({ health, sync }: { health?: string; sync?: string }) {
  // Healthy is the normal case and a status dot already says so: keep it quiet, color only what needs attention.
  const tone: Tone = health === 'Healthy' ? 'neutral' : health === 'Progressing' ? 'orange' : health ? 'red' : 'neutral'
  return (
    <span className="pills">
      {health && <Pill tone={tone}>{health}</Pill>}
      {sync && sync !== 'Synced' && <Pill tone="orange">{sync}</Pill>}
    </span>
  )
}
