import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { UpstreamStatusPanel } from './UpstreamStatusPanel'
import type { UpstreamStatus } from '../../lib/types'

const at = '2026-09-21T16:44:19Z'
const up = (over: Partial<UpstreamStatus> = {}): UpstreamStatus => ({
  name: 'onprem',
  envs: ['dev', 'qa'],
  kargoOk: true,
  kargoVersion: 'v1.11.4',
  argocdOk: true,
  argocdVersion: 'v3.5.3',
  checkedAt: at,
  ...over,
})

describe('UpstreamStatusPanel', () => {
  it('shows each system, its version, and what it serves', () => {
    render(<UpstreamStatusPanel upstreams={[up()]} />)
    expect(screen.getByText('onprem')).toBeTruthy()
    expect(screen.getByText('服务于 dev / qa')).toBeTruthy()
    expect(screen.getByText('v1.11.4')).toBeTruthy()
    expect(screen.getByText('v3.5.3')).toBeTruthy()
  })

  // The reason a page stopped loading is the upstream's own words; a summary
  // of them is no use to whoever has to go and fix it.
  it('shows the upstream error verbatim', () => {
    const err = 'Get "https://argocd.example.test:9/api/v1/applications": dial tcp 192.168.254.200:9: connect: connection refused'
    render(<UpstreamStatusPanel upstreams={[up({ argocdOk: false, argocdVersion: undefined, argocdError: err })]} />)
    expect(screen.getByText(err)).toBeTruthy()
    expect(screen.getByText('Argo CD').closest('.urow')?.className).toContain('bad')
    // The one that still works is not dragged down with it.
    expect(screen.getByText('Kargo').closest('.urow')?.className).not.toContain('bad')
  })

  // envs is null, not [], when no environment references the upstream; the
  // type used to say otherwise and the page crashed on it.
  it('survives an upstream no environment uses', () => {
    render(<UpstreamStatusPanel upstreams={[up({ envs: null })]} />)
    expect(screen.getByText('没有环境使用它')).toBeTruthy()
  })

  it('says so when a version is missing', () => {
    render(<UpstreamStatusPanel upstreams={[up({ kargoVersion: undefined })]} />)
    expect(screen.getByText('版本未知')).toBeTruthy()
  })

  it('reports a failure to reach anything at all', () => {
    render(<UpstreamStatusPanel upstreams={[]} error="还没有配置上游" />)
    expect(screen.getByText('还没有配置上游')).toBeTruthy()
  })

  it('says nothing when there is nothing to say', () => {
    const { container } = render(<UpstreamStatusPanel upstreams={[]} />)
    expect(container.innerHTML).toBe('')
  })
})
