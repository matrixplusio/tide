import { describe, expect, it } from 'vitest'
import { changeSummary, itemDigest } from './release'
import type { Item } from './types'

describe('release helpers', () => {
  it('summarizes resource changes by action', () => {
    expect(changeSummary([])).toBe('')
    expect(
      changeSummary([
        { kind: 'ConfigMap', name: 'a', action: 'update' },
        { kind: 'ConfigMap', name: 'b', action: 'delete' },
        { kind: 'Deployment', name: 'c', action: 'update' },
      ]),
    ).toBe('修改 2 · 删除 1')
  })
  it('echoes the running digest for restarts and syncs, the target for upgrades', () => {
    const current = { digest: 'sha256:cur', tag: 't' }
    const base = { id: 1, releaseId: 'R', sequence: 1, status: 'planned' as const }
    expect(itemDigest({ ...base, kind: 'sync', payload: { upstream: 'u', service: 's', env: 'qa', app: 'a', revision: 'r', current, changes: [] } } as Item)).toBe('sha256:cur')
    expect(itemDigest({ ...base, kind: 'image', payload: { to: { digest: 'sha256:to', tag: 't' } } } as unknown as Item)).toBe('sha256:to')
  })
})
