import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { AuthMethods, User } from '../../lib/types'
import { ME_KEY } from '../../app/session'

export function useAuthMethods() {
  return useQuery({ queryKey: ['auth-methods'], queryFn: ({ signal }) => apiFetch<AuthMethods>('/api/v1/auth/methods', { signal }), retry: 1 })
}

export interface LoginBody {
  username: string
  password: string
  captchaId?: string
  captchaCode?: string
}

export interface LoginChallenge {
  captchaRequired: boolean
  captchaId?: string
  captchaImage?: string
}

/** Asks whether a captcha is needed for this username from this client; issues a fresh one when it is. */
export function fetchLoginChallenge(username: string) {
  return apiFetch<LoginChallenge>('/api/v1/auth/challenge', { query: { username } })
}

export function useLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: LoginBody) => apiFetch<{ user: User }>('/api/v1/auth/login', { method: 'POST', body }),
    onSuccess: () => qc.resetQueries({ queryKey: ME_KEY }),
  })
}

export function useLogout() {
  return useMutation({
    mutationFn: () => apiFetch<null>('/api/v1/auth/logout', { method: 'POST' }),
    // Full reload drops every cached query of the previous session.
    onSettled: () => window.location.assign('/'),
  })
}

export function ssoLoginHref(returnPath: string): string {
  return `/api/v1/auth/sso/login?return=${encodeURIComponent(returnPath)}`
}
