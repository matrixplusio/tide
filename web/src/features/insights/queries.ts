import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { Insights } from '../../lib/types'

/** Periods the page offers. The server caps anything wider than a year. */
export const RANGES = [7, 30, 90, 365] as const
export type RangeDays = (typeof RANGES)[number]

export interface InsightsFilter {
  days: RangeDays
  env?: string
}

/** The browser's own zone, so "a release at 2am on Saturday" reads the way
 *  the person reading it experienced it. */
function timeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function useInsights(f: InsightsFilter) {
  const from = new Date(Date.now() - f.days * 24 * 3600 * 1000).toISOString()
  return useQuery({
    queryKey: ['insights', f],
    queryFn: ({ signal }) =>
      apiFetch<Insights>('/api/v1/insights', {
        query: { from, env: f.env, tz: timeZone() },
        signal,
      }),
    placeholderData: keepPreviousData,
  })
}
