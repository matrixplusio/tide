import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { InsightsPage } from './InsightsPage'
import type { Insights } from '../../lib/types'

// A period shaped like production: over a hundred services, a first-deploy
// anomaly code the page did not know, and a dozen services never released.
function report(): Insights {
  const services = Array.from({ length: 133 }, (_, i) => ({
    service: `svc-${String(i).padStart(3, '0')}`,
    project: 'acme',
    total: 133 - i,
    succeeded: 133 - i,
    failed: i % 40 === 0 ? 1 : 0,
  }))
  return {
    range: { from: '2026-06-30T00:00:00Z', to: '2026-09-28T00:00:00Z' },
    totals: { releases: 51, succeeded: 33, failed: 10, cancelled: 0, rejected: 0, inFlight: 8 },
    process: {
      confirmDwell: [
        { label: '<15s', upper: 15, count: 8 },
        { label: '15–30s', upper: 30, count: 9 },
      ],
      confirmSeconds: 10,
      anomalies: {
        withAnomaly: 45,
        wentAhead: 37,
        byCode: [
          { code: 'first_deploy_per_kargo', count: 19 },
          { code: 'first_deploy', count: 151 },
          { code: 'config_drift', count: 1 },
        ],
      },
      approvals: { requested: 0, approved: 0, rejected: 0, expired: 0, medianSeconds: 0, p90Seconds: 0, selfConfirmed: 43 },
      sources: [],
    },
    activity: {
      services,
      environments: [{ env: 'dev', total: 51, succeeded: 41, failed: 10 }],
      kinds: [{ kind: 'image', total: 51, succeeded: 41, failed: 10 }],
      weekly: [{ weekday: 4, hour: 13, count: 6 }],
    },
    risk: {
      coverage: { catalog: 147, released: 133, untouched: ['acme-frontend', 'order-api', 'portal-api'] },
      rollbacks: 0,
      repeats: [{ service: 'order-api', env: 'dev', day: '2026-09-24', count: 4 }],
      jiraReuse: [],
      freezeBlocked: 0,
      afterHours: 5,
    },
  }
}

function serve(data: Insights) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/v1/insights')) return new Response(JSON.stringify({ code: 0, msg: 'ok', data }), { status: 200 })
      return new Response(JSON.stringify({ code: 1004, msg: 'nf', data: null }), { status: 404 })
    }),
  )
}

afterEach(() => vi.unstubAllGlobals())

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/insights']}>
        <InsightsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('InsightsPage', () => {
  it('names every anomaly the server can send', async () => {
    // first_deploy_per_kargo was showing up as its raw code next to rows in
    // plain language; a code on a page is a bug, not a label.
    serve(report())
    renderPage()
    await screen.findByText('首次部署（Kargo 无记录）')
    expect(screen.queryByText('first_deploy_per_kargo')).toBeNull()
  })

  it('folds the service list to the busiest dozen', async () => {
    serve(report())
    renderPage()
    await screen.findByText('svc-000')
    // Busiest first, twelve of them, then one line saying how many more.
    expect(screen.getByText('svc-011')).toBeTruthy()
    expect(screen.queryByText('svc-012')).toBeNull()
    const more = screen.getByRole('button', { name: '还有 121 个' })
    await userEvent.click(more)
    expect(screen.getByText('svc-132')).toBeTruthy()
    expect(screen.getByRole('button', { name: '只看前 12 个' })).toBeTruthy()
  })

  it('lists the services never released as links, not a sentence', async () => {
    serve(report())
    renderPage()
    await screen.findByText('svc-000')
    expect(screen.getByRole('link', { name: 'order-api' }).getAttribute('href')).toBe('/services/order-api')
    expect(screen.getByText('这段时间没发过（3 个）')).toBeTruthy()
  })

  it('keeps the reasoning off the page and on the heading', async () => {
    serve(report())
    renderPage()
    await screen.findByText('svc-000')
    // The sentence about people waiting the countdown out is a tooltip on
    // the chart's heading; it is not a paragraph a reader has to scroll past.
    const hint = '倒计时结束前按钮是点不动的。如果绝大多数都落在刚过 10 秒的那一档，说明人是在等倒计时走完，不是在读清单。'
    expect(screen.queryByText(hint)).toBeNull()
    expect(screen.getByTitle(hint)).toBeTruthy()
    await waitFor(() => expect(screen.getByText('同一人建单并确认')).toBeTruthy())
  })
})
