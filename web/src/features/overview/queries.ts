import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { Deployment, Release, UpstreamStatus } from '../../lib/types'

export interface OverviewData {
  upstreams: UpstreamStatus[] | null
  upstreamError?: { code: number; msg: string } | null
  envOrder: string[] | null
  envStats: Record<string, { services: number; unhealthy: number; drifted: number } | undefined> | null
  /** Not serving. Drift is counted separately: it is the resting state of
   *  most Applications where nothing prunes, so mixing the two buries it. */
  unhealthy: Deployment[] | null
  drifted: number
  serviceCount: number
  domainCount: number
  inFlight: Release[] | null
  myInFlight: number
  recentFailed: Release[] | null
  recent: Release[] | null
  today: { total: number; succeeded: number }
}

export function useOverview() {
  return useQuery({ queryKey: ['overview'], queryFn: ({ signal }) => apiFetch<OverviewData>('/api/v1/overview', { signal }), refetchInterval: 10_000 })
}
