import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { UpstreamHealth } from './UpstreamHealth'
import type { UpstreamStatus } from '../../lib/types'

const up = (name: string, kargoOk = true, argocdOk = true): UpstreamStatus => ({
  name,
  envs: [],
  kargoOk,
  argocdOk,
  kargoError: kargoOk ? '' : `dial ${name} kargo: connection refused`,
  argocdError: argocdOk ? '' : `dial ${name} argocd: 401`,
  catalog: { applications: 12, kept: 12, noEnv: 0, otherEnv: 0 },
  checkedAt: new Date().toISOString(),
})

describe('UpstreamHealth', () => {
  it('says nothing much while everything answers', () => {
    render(<UpstreamHealth upstreams={[up('onprem'), up('gcp'), up('local')]} />)
    // One line, not one per system per upstream.
    expect(screen.getByText('3 个上游正常')).toBeTruthy()
    expect(screen.queryByText(/Kargo/)).toBeNull()
    expect(document.querySelectorAll('.hrow')).toHaveLength(1)
  })

  it('names only what is broken, and keeps the reason', () => {
    render(<UpstreamHealth upstreams={[up('onprem'), up('gcp', true, false), up('local')]} />)
    const bad = screen.getByText('Argo CD · gcp')
    expect(bad).toBeTruthy()
    expect(bad.closest('.hrow')?.className).toContain('bad')
    // The error is where someone would look for it, not thrown away.
    expect(bad.closest('.hrow')?.getAttribute('title')).toContain('401')
    // And the rest are accounted for, so "one is down" does not read as
    // "only one was ever checked".
    expect(screen.getByText('其余 5 项正常')).toBeTruthy()
  })

  it('lists every failure when several are down', () => {
    render(<UpstreamHealth upstreams={[up('onprem', false, false), up('gcp', false, true)]} />)
    expect(screen.getByText('Kargo · onprem')).toBeTruthy()
    expect(screen.getByText('Argo CD · onprem')).toBeTruthy()
    expect(screen.getByText('Kargo · gcp')).toBeTruthy()
    expect(screen.getByText('其余 1 项正常')).toBeTruthy()
  })

  it('warns about a credential before it expires, while everything still answers', () => {
    const u = up('onprem')
    u.expiring = [{ upstream: 'onprem', kind: 'registry', expires: '2026-10-02', days: 9 }]
    render(<UpstreamHealth upstreams={[u]} />)
    // Still "all ok" — nothing is down yet. That is exactly why the warning
    // has to be said out loud rather than inferred from an outage.
    expect(screen.getByText('1 个上游正常')).toBeTruthy()
    const warn = screen.getByText('上游凭据 9 天后到期')
    expect(warn.closest('.hrow')?.className).toContain('warn')
    expect(warn.closest('.hrow')?.getAttribute('title')).toContain('registry')
  })

  it('switches to the past tense once the date has gone by', () => {
    const u = up('onprem')
    u.expiring = [
      { upstream: 'onprem', kind: 'registry', expires: '2026-09-20', days: -3 },
      { upstream: 'onprem', kind: 'kargo', expires: '2026-09-30', days: 7 },
    ]
    render(<UpstreamHealth upstreams={[u]} />)
    expect(screen.getByText('有 2 项上游凭据已过期')).toBeTruthy()
  })

  it('stays quiet when no expiry date was ever recorded', () => {
    render(<UpstreamHealth upstreams={[up('onprem')]} />)
    expect(document.querySelectorAll('.hrow.warn')).toHaveLength(0)
  })

  it('does not claim anything when nothing is configured', () => {
    const { container } = render(<UpstreamHealth upstreams={[]} />)
    expect(container.innerHTML).toBe('')
  })
})
