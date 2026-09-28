import type { Release } from './types'

/** What a change form is pre-filled with when a person re-creates a release
 *  from one that failed. Everything here is a suggestion the form shows and
 *  the person can change; nothing is submitted on their behalf. */
export interface Preset {
  /** The release this came from, so the form can say so. */
  from?: string
  freight?: string
  withConfig?: boolean
  replicas?: string
  prune?: boolean
  restart?: boolean
  jiraTicket?: string
  reason?: string
}

const CHANGE: Record<string, string> = { image: 'upgrade', restart: 'restart', sync: 'sync', scale: 'scale' }

/** redoLink is where "re-create this release" goes: the service's environment
 *  page with the same change type and the same inputs. A failed release is
 *  final on purpose — the cluster has changed under it — so the new one is
 *  built from fresh state; what carries over is what the person typed.
 *  Only for a release of one item: a batch is re-created from the batch page. */
export function redoLink(r: Release): string | null {
  const it = r.items?.[0]
  if (!it || (r.items?.length ?? 0) !== 1) return null
  const p = new URLSearchParams()
  const change = CHANGE[it.kind]
  if (change && change !== 'upgrade') p.set('change', change)
  p.set('from', r.id)
  if (r.jiraTicket) p.set('jira', r.jiraTicket)
  if (r.reason) p.set('reason', r.reason)
  switch (it.kind) {
    case 'image':
      if (it.payload.freight) p.set('freight', it.payload.freight)
      if (it.payload.withConfig) p.set('withConfig', '1')
      break
    case 'scale':
      p.set('replicas', String(it.payload.to))
      break
    case 'sync':
      if (it.payload.prune) p.set('prune', '1')
      if (it.payload.restart) p.set('restart', '1')
      break
    default:
      break
  }
  return `/services/${encodeURIComponent(it.payload.service)}/envs/${encodeURIComponent(r.env)}?${p.toString()}`
}

/** readPreset is the other end of redoLink. Anything not in the URL is left
 *  undefined so a form's own default applies. */
export function readPreset(params: URLSearchParams): Preset {
  const get = (k: string) => params.get(k) ?? undefined
  const flag = (k: string) => (params.get(k) === '1' ? true : undefined)
  return {
    from: get('from'),
    freight: get('freight'),
    withConfig: flag('withConfig'),
    replicas: get('replicas'),
    prune: flag('prune'),
    restart: flag('restart'),
    jiraTicket: get('jira'),
    reason: get('reason'),
  }
}
