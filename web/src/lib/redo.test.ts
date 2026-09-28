import { describe, expect, it } from 'vitest'
import { readPreset, redoLink } from './redo'
import type { Release } from './types'

const base = { id: 'REL-20260929-009', title: '', env: 'dev', source: 'ui', jiraTicket: 'OPS-1', reason: '修 bug', createdBy: 'u', createdByName: 'u', status: 'failed', createdAt: '', now: '' } as unknown as Release

describe('redoLink', () => {
  it('carries a scale release back to the form with its number', () => {
    const r = { ...base, items: [{ id: 1, kind: 'scale', payload: { service: 'order-api', env: 'dev', from: 1, to: 3 } }] } as unknown as Release
    const link = redoLink(r)!
    expect(link.startsWith('/services/order-api/envs/dev?')).toBe(true)
    const p = readPreset(new URLSearchParams(link.split('?')[1]))
    expect(p).toMatchObject({ from: 'REL-20260929-009', replicas: '3', jiraTicket: 'OPS-1', reason: '修 bug' })
    expect(new URLSearchParams(link.split('?')[1]).get('change')).toBe('scale')
  })

  it('carries an upgrade with its freight and the config choice', () => {
    const r = { ...base, items: [{ id: 1, kind: 'image', payload: { service: 'order-api', env: 'dev', freight: 'abc123', withConfig: true } }] } as unknown as Release
    const p = readPreset(new URLSearchParams(redoLink(r)!.split('?')[1]))
    expect(p).toMatchObject({ freight: 'abc123', withConfig: true })
    expect(p.replicas).toBeUndefined()
  })

  it('refuses a batch: that is re-created from the batch page', () => {
    const r = { ...base, items: [{ id: 1, kind: 'restart', payload: { service: 'a', env: 'dev' } }, { id: 2, kind: 'restart', payload: { service: 'b', env: 'dev' } }] } as unknown as Release
    expect(redoLink(r)).toBeNull()
  })
})
