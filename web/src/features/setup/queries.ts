import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { SetupState, User } from '../../lib/types'
import { ME_KEY } from '../../app/session'

export const SETUP_KEY = ['setup'] as const

export function useSetupState() {
  return useQuery({ queryKey: SETUP_KEY, queryFn: ({ signal }) => apiFetch<SetupState>('/api/v1/setup/state', { signal }), retry: false })
}

export function useVerifySetupToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { token: string }) => apiFetch<null>('/api/v1/setup/token', { method: 'POST', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: SETUP_KEY }),
  })
}

export interface SetupAdminInput {
  username: string
  name: string
  password: string
  confirmPassword: string
}

export function useCreateSetupAdmin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: SetupAdminInput) => apiFetch<{ user: User }>('/api/v1/setup/admin', { method: 'POST', body }),
    onSuccess: () => qc.resetQueries({ queryKey: ME_KEY }),
  })
}
