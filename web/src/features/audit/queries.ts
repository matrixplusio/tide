import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { AuditEntry, Paged } from '../../lib/types'

export const AUDIT_PAGE_SIZE = 50

export interface AuditFilter {
  jira?: string
  service?: string
  env?: string
  actor?: string
  action?: string
  since?: string
  until?: string
  page: number
}

export function useAudit(f: AuditFilter) {
  return useQuery({
    queryKey: ['audit', f],
    queryFn: ({ signal }) => apiFetch<Paged<AuditEntry>>('/api/v1/audit', { query: { ...f, page_size: AUDIT_PAGE_SIZE }, signal }),
    placeholderData: keepPreviousData,
  })
}
