import type { Live, Pod, Rollout } from './types'

export interface RolloutSummary {
  name: string
  desired: number
  /** Pods of the target version that are ready. */
  targetReady: number
  /** Pods of the target version, ready or not. */
  target: number
  /** Pods of any other version still present. */
  other: number
  /** A target pod is crashing / failing. */
  unhealthy: boolean
  /** Every desired replica runs the target version, is ready, and no other version is left. */
  done: boolean
}

// "1/1" ready containers
function podReady(p: Pod): boolean {
  const [a, b] = (p.ready || '').split('/').map(Number)
  return !!b && a === b
}

/**
 * Summarises a workload's rollout from its pods rather than from the
 * Deployment counters: Kubernetes counts old pods in readyReplicas, so
 * "2/2 ready" can still mean one new pod is starting and one old pod serves.
 *
 * `since` is for restarts, where old and new pods run the same image: only
 * pods created at or after it count as the new generation.
 */
export function summarizeRollout(r: Rollout, live: Live, since?: string): RolloutSummary {
  const pods = (live.pods ?? []).filter((p) => p.name.startsWith(`${r.name}-`))
  const cutoff = since ? Date.parse(since) : NaN
  const isNew = (p: Pod) => (Number.isNaN(cutoff) ? p.isTarget : !!p.createdAt && Date.parse(p.createdAt) >= cutoff - 1000)
  const target = pods.filter(isNew)
  const targetReady = target.filter(podReady).length
  const other = pods.length - target.length
  return {
    name: r.name,
    desired: r.desired,
    targetReady,
    target: target.length,
    other,
    unhealthy: target.some((p) => p.health === 'Degraded' || /BackOff|Err/i.test(p.status)),
    // extra pods mean a rollout is still under way even when every pod looks like the target (restarts)
    done: r.desired > 0 && targetReady >= r.desired && other === 0 && pods.length <= r.desired,
  }
}
