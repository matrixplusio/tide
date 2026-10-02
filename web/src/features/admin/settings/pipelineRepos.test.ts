import { describe, expect, it } from 'vitest'
import { pipelineEntryFor, withPipelineEntry } from './pipelineRepos'
import type { PipelineRepo } from '../types'

const repo = (prefix: string, extra: Partial<PipelineRepo> = {}): PipelineRepo => ({ baseUrl: 'https://git.example.com', project: 'ops/pipelines', branch: 'main', token: '••••••', pathPrefix: prefix, ...extra })

describe('pipeline repository per upstream', () => {
  const legacy = repo('idc')

  it('reads a configuration written before upstreams were told apart as the first one', () => {
    expect(pipelineEntryFor(legacy, 'idc', 'idc')?.pathPrefix).toBe('idc')
    expect(pipelineEntryFor(legacy, 'gcp', 'idc')).toBeUndefined()
  })

  it('adds a second upstream without touching the first', () => {
    const next = withPipelineEntry(legacy, 'gcp', 'idc', repo('gcp'))
    expect(next.pathPrefix).toBe('idc')
    expect(next.name ?? '').toBe('')
    expect(next.others).toHaveLength(1)
    expect(next.others?.[0]).toMatchObject({ name: 'gcp', pathPrefix: 'gcp' })
    expect(pipelineEntryFor(next, 'gcp', 'idc')?.pathPrefix).toBe('gcp')
  })

  it('edits the first without dropping the others, and an other in place', () => {
    const both = withPipelineEntry(legacy, 'gcp', 'idc', repo('gcp'))
    const first = withPipelineEntry(both, 'idc', 'idc', repo('idc', { branch: 'review' }))
    expect(first.branch).toBe('review')
    expect(first.others).toHaveLength(1)
    const second = withPipelineEntry(first, 'gcp', 'idc', repo('gcp', { branch: 'gcp-review' }))
    expect(second.others).toHaveLength(1)
    expect(second.others?.[0]?.branch).toBe('gcp-review')
    expect(second.branch).toBe('review')
  })

  it('puts the first save anywhere at the top level', () => {
    const saved = withPipelineEntry(null, 'gcp', 'idc', repo('gcp'))
    expect(saved).toMatchObject({ name: 'gcp', pathPrefix: 'gcp' })
    expect(pipelineEntryFor(saved, 'gcp', 'idc')?.pathPrefix).toBe('gcp')
    expect(pipelineEntryFor(saved, 'idc', 'idc')).toBeUndefined()
  })
})
