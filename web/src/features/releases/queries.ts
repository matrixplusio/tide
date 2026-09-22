import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { ItemKind, ItemLive, Paged, Release, ReleaseStatus } from '../../lib/types'

export const PAGE_SIZE = 20

export interface ReleaseFilter {
  status?: ReleaseStatus[]
  env?: string
  service?: string
  jira?: string
  project?: string
  kind?: ItemKind
  creator?: string
  since?: string
  until?: string
  /** Only releases the viewer created. */
  mine?: boolean
  /** Only releases the viewer approved or rejected. */
  decided?: boolean
  /** Only approving releases the viewer may still approve. */
  awaiting?: boolean
  page: number
  pageSize?: number
}

export function releasesQueryKey(f: ReleaseFilter) {
  return ['releases', f] as const
}

export function useReleases(f: ReleaseFilter, opts: { refetchInterval?: number } = {}) {
  return useQuery({
    queryKey: releasesQueryKey(f),
    queryFn: ({ signal }) =>
      apiFetch<Paged<Release>>('/api/v1/releases', {
        query: {
          status: f.status?.join(','),
          env: f.env,
          service: f.service,
          jira: f.jira,
          project: f.project,
          kind: f.kind,
          creator: f.creator,
          since: f.since,
          until: f.until,
          mine: f.mine || undefined,
          decided: f.decided || undefined,
          awaiting: f.awaiting || undefined,
          page: f.page,
          page_size: f.pageSize ?? PAGE_SIZE,
        },
        signal,
      }),
    placeholderData: keepPreviousData,
    refetchInterval: opts.refetchInterval,
  })
}

export function useActiveReleaseCount(enabled = true) {
  return useQuery({
    enabled,
    queryKey: releasesQueryKey({ status: ['confirming', 'approving', 'executing'], page: 1, pageSize: 1 }),
    queryFn: ({ signal }) => apiFetch<Paged<Release>>('/api/v1/releases', { query: { status: 'confirming,approving,executing', page: 1, page_size: 1 }, signal }),
    select: (d) => d.total,
    refetchInterval: 10_000,
  })
}

export interface ReleaseDetail {
  release: Release
  /** What the viewer may do, with project / type scopes applied. */
  can: { confirm: boolean; cancel: boolean; pods: boolean }
  live?: ItemLive[] | null
}

export function useRelease(id: string) {
  return useQuery({
    queryKey: ['release', id],
    queryFn: ({ signal }) => apiFetch<ReleaseDetail>(`/api/v1/releases/${encodeURIComponent(id)}`, { query: { live: true }, signal }),
    refetchInterval: (query) => {
      const s = query.state.data?.release.status
      return s === 'executing' || s === 'confirming' || s === 'approving' ? 3000 : 30_000
    },
  })
}

export interface CreateReleaseInput {
  env: string
  /** One-line summary; the server generates one when empty. */
  title?: string
  jiraTicket: string
  reason: string
  items: (
    | { kind?: 'image'; service: string; freight: string; sequence: number; withConfig?: boolean }
    | { kind: 'restart'; service: string; sequence: number }
    | { kind: 'sync'; service: string; sequence: number; prune?: boolean; restart?: boolean }
  )[]
}

export function useCreateRelease() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateReleaseInput) => apiFetch<Release>('/api/v1/releases', { method: 'POST', body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['env'] })
      void qc.invalidateQueries({ queryKey: ['releases'] })
    },
  })
}

export function useConfirmRelease() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, digests }: { id: string; digests: string[] }) =>
      apiFetch<Release>(`/api/v1/releases/${encodeURIComponent(id)}/confirm`, { method: 'POST', body: { digests } }),
    onSuccess: () => void qc.invalidateQueries(),
  })
}

export function useCancelRelease() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, reason }: { id: string; reason?: string }) =>
      apiFetch<Release>(`/api/v1/releases/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: reason ? { reason } : {} }),
    onSuccess: () => void qc.invalidateQueries(),
  })
}

/** Approving releases the viewer may still decide on. */
export function useAwaitingApprovalCount(enabled = true) {
  return useQuery({
    enabled,
    queryKey: releasesQueryKey({ awaiting: true, page: 1, pageSize: 1 }),
    queryFn: ({ signal }) => apiFetch<Paged<Release>>('/api/v1/releases', { query: { awaiting: true, page: 1, page_size: 1 }, signal }),
    select: (d) => d.total,
    refetchInterval: 10_000,
  })
}

export function useDecideRelease() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, approve, note }: { id: string; approve: boolean; note: string }) =>
      apiFetch<Release>(`/api/v1/releases/${encodeURIComponent(id)}/${approve ? 'approve' : 'reject'}`, { method: 'POST', body: { note } }),
    onSuccess: () => void qc.invalidateQueries(),
  })
}
