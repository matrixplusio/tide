import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ApiError, apiFetch } from '../../lib/api'
import { ErrCode } from '../../lib/errcode'
import { useMe } from '../../app/session'
import { can } from '../../lib/permissions'
import type { Candidates, ConfigDiff, Deployment, Live, LogLine, PodEvent, PromotionView, Release, Service, UpstreamStatus } from '../../lib/types'

export interface ServicesData {
  services: Service[]
  envOrder: string[]
  upstreams: UpstreamStatus[]
  at: string
  inFlight: Record<string, string>
}

const svc = (s: string) => encodeURIComponent(s)

export function useServices(enabled = true) {
  return useQuery({
    queryKey: ['services'],
    enabled,
    queryFn: ({ signal }) => apiFetch<ServicesData>('/api/v1/services', { signal }),
    // "No upstream configured yet" is a state an administrator has to leave,
    // not a failure that passes on its own. Polling it every 30 seconds fills
    // the server log with 503s and never gets anywhere; stop until something
    // invalidates this query, which configuring an upstream does.
    refetchInterval: (q) => (isNoUpstreams(q.state.error) ? false : 30_000),
    retry: (n, err) => !isNoUpstreams(err) && n < 2,
  })
}

/** NoUpstreams answers with 503, so the default "do not retry 4xx" rule does
 *  not catch it. */
function isNoUpstreams(err: unknown): boolean {
  return err instanceof ApiError && err.code === ErrCode.NoUpstreams
}

/** env narrows the release history. Narrowed on the server, because the
 *  history is the most recent twenty across every environment: filtering
 *  those in the browser would report "no prod releases" the moment dev had
 *  twenty of its own. */
export function useService(service: string, env = '') {
  return useQuery({
    queryKey: ['service', service, env],
    queryFn: ({ signal }) =>
      apiFetch<{ service: Service; releases: Release[] | null }>(`/api/v1/services/${svc(service)}${env ? `?env=${encodeURIComponent(env)}` : ''}`, { signal }),
    refetchInterval: 15_000,
    // The environment tabs should not blank the list while the next answer
    // is on its way; the old rows stay until they are replaced.
    placeholderData: keepPreviousData,
  })
}

export interface EnvData {
  deployment: Deployment
  live: Live
  canOperate: boolean
  /** What the viewer may do on this service here (project / type scopes applied). */
  can: { create: boolean; sync: boolean; restart: boolean; pods: boolean }
  releases: Release[] | null
  promotions: PromotionView[] | null
  promotionsError?: string
  conflicts?: string[] | null
}

export function useEnvDetail(service: string, env: string) {
  return useQuery({
    queryKey: ['env', service, env],
    queryFn: ({ signal }) => apiFetch<EnvData>(`/api/v1/services/${svc(service)}/envs/${encodeURIComponent(env)}`, { signal }),
    refetchInterval: 10_000,
  })
}

/**
 * A service's project and type (the catalog's batch dimension), the scope
 * permissions and approval rules are keyed on. Empty while the catalog loads.
 */
export function useServiceScope(name: string | undefined): { project?: string; type?: string } {
  const me = useMe()
  const services = useServices(can(me, 'services.view'))
  const svc = (services.data?.services ?? []).find((s) => s.name === name)
  const dim = me.app.batchDimension
  return { project: svc?.project || undefined, type: dim ? svc?.dimensions?.[dim] : undefined }
}

export function useConfigDiff(service: string, env: string, enabled: boolean) {
  return useQuery({
    queryKey: ['config-diff', service, env],
    enabled,
    // Ask Argo CD to re-read git: a commit pushed seconds ago should show up.
    queryFn: ({ signal }) => apiFetch<ConfigDiff>(`/api/v1/services/${svc(service)}/envs/${encodeURIComponent(env)}/config-diff`, { query: { refresh: true }, signal }),
    staleTime: 15_000,
  })
}

export function useCandidates(service: string, env: string, all: boolean, enabled: boolean) {
  return useQuery({
    queryKey: ['candidates', service, env, all],
    queryFn: ({ signal }) =>
      apiFetch<Candidates>(`/api/v1/services/${svc(service)}/envs/${encodeURIComponent(env)}/candidates`, { query: { all: all || undefined }, signal }),
    enabled,
    placeholderData: keepPreviousData,
  })
}

export const LOG_TAIL = 1000

export function usePodLogs(service: string, env: string, pod: string, opts: { enabled: boolean; follow: boolean }) {
  return useQuery({
    queryKey: ['logs', service, env, pod],
    queryFn: ({ signal }) =>
      apiFetch<{ lines: LogLine[] | null }>(`/api/v1/services/${svc(service)}/envs/${encodeURIComponent(env)}/pods/${encodeURIComponent(pod)}/logs`, {
        query: { tail: LOG_TAIL },
        signal,
      }),
    enabled: opts.enabled,
    refetchInterval: opts.follow ? 3000 : false,
  })
}

export function usePodEvents(service: string, env: string, pod: string, uid: string, enabled: boolean) {
  return useQuery({
    queryKey: ['events', service, env, pod, uid],
    queryFn: ({ signal }) =>
      apiFetch<{ events: PodEvent[] | null }>(`/api/v1/services/${svc(service)}/envs/${encodeURIComponent(env)}/pods/${encodeURIComponent(pod)}/events`, {
        query: { uid },
        signal,
      }),
    enabled: enabled && !!uid,
    refetchInterval: 10_000,
  })
}
