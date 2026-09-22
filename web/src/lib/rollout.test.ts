import { describe, expect, it } from 'vitest'
import { summarizeRollout } from './rollout'
import type { Live, Pod } from './types'

const pod = (name: string, isTarget: boolean, ready: string, extra: Partial<Pod> = {}): Pod => ({
  name, namespace: 'ns', uid: name, health: 'Healthy', status: 'Running', ready, restarts: '0', images: [], isTarget, ...extra,
})
const live = (pods: Pod[]): Live => ({ app: 'a', sync: 'Synced', health: 'Healthy', resources: [], rollouts: [], pods })
const r = { name: 'order-api', desired: 2, updated: 2, ready: 2, available: 2 }

describe('summarizeRollout', () => {
  it('is not done while a new pod starts and an old pod still serves, even if the counters say 2/2 ready', () => {
    const s = summarizeRollout(r, live([pod('order-api-new-a', true, '1/1'), pod('order-api-new-b', true, '0/1'), pod('order-api-old-c', false, '1/1')]))
    expect(s).toMatchObject({ targetReady: 1, target: 2, other: 1, done: false, unhealthy: false })
  })
  it('is done when every desired replica runs the target and is ready, and no old pod is left', () => {
    const s = summarizeRollout(r, live([pod('order-api-new-a', true, '1/1'), pod('order-api-new-b', true, '1/1')]))
    expect(s).toMatchObject({ targetReady: 2, other: 0, done: true })
  })
  it('flags crashing target pods', () => {
    const s = summarizeRollout({ ...r, desired: 1 }, live([pod('order-api-new-a', true, '0/1', { status: 'CrashLoopBackOff', health: 'Degraded' }), pod('order-api-old-b', false, '1/1')]))
    expect(s).toMatchObject({ targetReady: 0, other: 1, unhealthy: true, done: false })
  })
  it('only counts pods of this workload', () => {
    const s = summarizeRollout({ ...r, desired: 1 }, live([pod('order-api-x', true, '1/1'), pod('other-backend-y', false, '1/1')]))
    expect(s).toMatchObject({ other: 0, done: true })
  })
  it('for restarts, only pods created after the restart count as new', () => {
    const since = '2026-09-17T14:40:00Z'
    const s = summarizeRollout({ ...r, desired: 1 }, live([
      pod('order-api-a', true, '1/1', { createdAt: '2026-09-17T13:00:00Z' }),
      pod('order-api-b', true, '0/1', { createdAt: '2026-09-17T14:40:05Z' }),
    ]), since)
    expect(s).toMatchObject({ target: 1, targetReady: 0, other: 1, done: false })
  })
  it('without a restart time, extra pods of the same image still mean the rollout is not done', () => {
    const s = summarizeRollout({ ...r, desired: 1 }, live([pod('order-api-a', true, '1/1'), pod('order-api-b', true, '0/1')]))
    expect(s.done).toBe(false)
  })
})
