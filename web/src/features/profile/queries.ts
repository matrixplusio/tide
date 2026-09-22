import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { AuditEntry, EffectiveBinding, Paged, Session, User } from '../../lib/types'
import { ME_KEY } from '../../app/session'

export const ACTIVITY_PAGE_SIZE = 20

export function useUpdateProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string }) => apiFetch<User>('/api/v1/me/profile', { method: 'PUT', body }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ME_KEY }),
  })
}

export function useChangeMyPassword() {
  return useMutation({
    mutationFn: (body: { currentPassword: string; newPassword: string; confirmPassword: string }) => apiFetch<null>('/api/v1/me/password', { method: 'PUT', body }),
  })
}

export function useMySessions() {
  return useQuery({ queryKey: ['my-sessions'], queryFn: ({ signal }) => apiFetch<{ items: Session[] | null }>('/api/v1/me/sessions', { signal }) })
}

export function useRevokeMySession() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch<null>(`/api/v1/me/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['my-sessions'] }),
  })
}

export function useMyBindings() {
  return useQuery({ queryKey: ['my-bindings'], queryFn: ({ signal }) => apiFetch<{ items: EffectiveBinding[] | null }>('/api/v1/me/bindings', { signal }) })
}

export function useMyActivity(page: number) {
  return useQuery({
    queryKey: ['my-activity', page],
    queryFn: ({ signal }) => apiFetch<Paged<AuditEntry>>('/api/v1/me/activity', { query: { page, page_size: ACTIVITY_PAGE_SIZE }, signal }),
    placeholderData: keepPreviousData,
  })
}
