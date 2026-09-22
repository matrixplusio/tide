import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import type { CheckResult, Paged, RoleBinding } from '../../lib/types'
import type { Channel, CIIntake, CISnippet, CIToken, DiscoveredLabel, GroupRow, KargoPlan, KargoPushed, RbacCatalog, Role, SettingsData, SettingsSection, Upstream, UserDetail, UserRow } from './types'

const enc = encodeURIComponent

// ---- settings ---------------------------------------------------------------

export const SETTINGS_KEY = ['settings'] as const

export function useSettings() {
  return useQuery({
    queryKey: SETTINGS_KEY,
    queryFn: ({ signal }) => apiFetch<SettingsData>('/api/v1/settings', { signal }),
    // Forms are seeded from this; a background refetch must not clobber input.
    refetchOnWindowFocus: false,
    staleTime: Infinity,
  })
}

export function useSaveSettings<T>(section: SettingsSection) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: T) => apiFetch<{ results?: CheckResult[] | null } | null>(`/api/v1/settings/${section}`, { method: 'PUT', body }),
    // Environments, upstreams, the release policy and the site name feed almost every page (and /me).
    onSuccess: () => void qc.invalidateQueries(),
  })
}

export function useCatalogLabels() {
  return useQuery({
    queryKey: ['catalog-labels'],
    queryFn: ({ signal }) => apiFetch<{ items: DiscoveredLabel[] | null }>('/api/v1/settings/catalog/labels', { signal }),
    retry: false,
  })
}

export function useTestUpstreams() {
  return useMutation({
    mutationFn: (body: { items: Upstream[] }) => apiFetch<{ results: CheckResult[] | null }>('/api/v1/settings/upstreams/test', { method: 'POST', body }),
  })
}

export function useTestChannel() {
  return useMutation({
    mutationFn: (channel: Channel) => apiFetch<null>('/api/v1/settings/notify/test', { method: 'POST', body: { channel } }),
  })
}

// ---- CI-triggered releases --------------------------------------------------

const CI_TOKENS_KEY = ['ci-tokens'] as const

export function useCITokens() {
  return useQuery({
    queryKey: CI_TOKENS_KEY,
    queryFn: ({ signal }) => apiFetch<{ items: CIToken[] | null }>('/api/v1/ci/tokens', { signal }),
  })
}

export function useCreateCIToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string }) => apiFetch<{ token: CIToken; secret: string }>('/api/v1/ci/tokens', { method: 'POST', body }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: CI_TOKENS_KEY }),
  })
}

export function useRevokeCIToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch<null>(`/api/v1/ci/tokens/${enc(id)}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: CI_TOKENS_KEY }),
  })
}

export const INTAKES_PAGE_SIZE = 20

export function useCIIntakes(f: { status?: string; page: number }) {
  const q = new URLSearchParams({ page: String(f.page), page_size: String(INTAKES_PAGE_SIZE) })
  if (f.status) q.set('status', f.status)
  return useQuery({
    queryKey: ['ci-intakes', f],
    queryFn: ({ signal }) => apiFetch<Paged<CIIntake>>(`/api/v1/ci/intakes?${q}`, { signal }),
    placeholderData: keepPreviousData,
    // A waiting intake resolves on Tide's own clock, so the page follows it.
    refetchInterval: 15000,
  })
}

export function useCISnippet(env: string) {
  return useQuery({
    queryKey: ['ci-snippet', env],
    queryFn: ({ signal }) => apiFetch<CISnippet>(`/api/v1/ci/snippet?env=${enc(env)}`, { signal }),
    enabled: env !== '',
  })
}

// ---- users ------------------------------------------------------------------

export interface UserFilter {
  q?: string
  method?: string
  status?: string
  page: number
  pageSize?: number
}

export const USERS_PAGE_SIZE = 20

export function useUsers(f: UserFilter, enabled = true) {
  return useQuery({
    queryKey: ['users', f],
    queryFn: ({ signal }) =>
      apiFetch<Paged<UserRow>>('/api/v1/users', { query: { q: f.q, method: f.method, status: f.status, page: f.page, page_size: f.pageSize ?? USERS_PAGE_SIZE }, signal }),
    placeholderData: keepPreviousData,
    enabled,
  })
}

export function useUser(id: string) {
  return useQuery({ queryKey: ['user', id], queryFn: ({ signal }) => apiFetch<UserDetail>(`/api/v1/users/${enc(id)}`, { signal }) })
}

function useInvalidating<V, R>(fn: (v: V) => Promise<R>, keys: string[][]) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      for (const k of keys) void qc.invalidateQueries({ queryKey: k })
    },
  })
}

const USER_KEYS = [['users'], ['user'], ['groups'], ['group-members'], ['role-bindings'], ['roles']]

export const useCreateUser = () =>
  useInvalidating((body: { username: string; name: string; password: string; confirmPassword: string }) => apiFetch<UserRow>('/api/v1/users', { method: 'POST', body }), USER_KEYS)

export const useRenameUser = () =>
  useInvalidating(({ id, name }: { id: number; name: string }) => apiFetch<UserRow>(`/api/v1/users/${id}`, { method: 'PUT', body: { name } }), USER_KEYS)

export const useResetUserPassword = () =>
  useInvalidating(
    ({ id, ...body }: { id: number; newPassword: string; confirmPassword: string }) => apiFetch<null>(`/api/v1/users/${id}/password`, { method: 'PUT', body }),
    USER_KEYS,
  )

export const useSetUserDisabled = () =>
  useInvalidating(({ id, disabled }: { id: number; disabled: boolean }) => apiFetch<null>(`/api/v1/users/${id}/disabled`, { method: 'PUT', body: { disabled } }), USER_KEYS)

export const useRevokeUserSessions = () => useInvalidating((id: number) => apiFetch<null>(`/api/v1/users/${id}/sessions`, { method: 'DELETE' }), USER_KEYS)

// ---- groups -----------------------------------------------------------------

export function useGroups(enabled = true) {
  return useQuery({ queryKey: ['groups'], queryFn: ({ signal }) => apiFetch<{ items: GroupRow[] | null }>('/api/v1/groups', { signal }), enabled })
}

export function useGroupMembers(name: string) {
  return useQuery({ queryKey: ['group-members', name], queryFn: ({ signal }) => apiFetch<{ items: UserRow[] | null }>(`/api/v1/groups/${enc(name)}/members`, { signal }) })
}

export const useCreateGroup = () => useInvalidating((body: { name: string; description: string }) => apiFetch<null>('/api/v1/groups', { method: 'POST', body }), USER_KEYS)

export const useUpdateGroup = () =>
  useInvalidating(({ name, description }: { name: string; description: string }) => apiFetch<null>(`/api/v1/groups/${enc(name)}`, { method: 'PUT', body: { description } }), USER_KEYS)

export const useDeleteGroup = () => useInvalidating((name: string) => apiFetch<null>(`/api/v1/groups/${enc(name)}`, { method: 'DELETE' }), USER_KEYS)

export const useAddGroupMembers = () =>
  useInvalidating(({ name, userIds }: { name: string; userIds: number[] }) => apiFetch<null>(`/api/v1/groups/${enc(name)}/members`, { method: 'POST', body: { userIds } }), USER_KEYS)

export const useRemoveGroupMember = () =>
  useInvalidating(({ name, id }: { name: string; id: number }) => apiFetch<null>(`/api/v1/groups/${enc(name)}/members/${id}`, { method: 'DELETE' }), USER_KEYS)

// ---- roles & bindings -------------------------------------------------------

export function useRbacCatalog() {
  return useQuery({ queryKey: ['rbac-permissions'], queryFn: ({ signal }) => apiFetch<RbacCatalog>('/api/v1/rbac/permissions', { signal }), staleTime: Infinity })
}

export function useRoles() {
  return useQuery({ queryKey: ['roles'], queryFn: ({ signal }) => apiFetch<{ items: Role[] | null }>('/api/v1/roles', { signal }) })
}

export function useRoleBindings(f: { role?: string; subject?: string }) {
  return useQuery({ queryKey: ['role-bindings', f], queryFn: ({ signal }) => apiFetch<{ items: RoleBinding[] | null }>('/api/v1/role-bindings', { query: f, signal }) })
}

// Bindings change what /me reports for the current user too.
const RBAC_KEYS = [['roles'], ['role-bindings'], ['user'], ['users'], ['me'], ['my-bindings']]

export const useCreateRole = () =>
  useInvalidating((body: { id: string; name: string; description: string; permissions: string[] }) => apiFetch<Role>('/api/v1/roles', { method: 'POST', body }), RBAC_KEYS)

export const useUpdateRole = () =>
  useInvalidating(({ id, ...body }: { id: string; name: string; description: string; permissions: string[] }) => apiFetch<Role>(`/api/v1/roles/${enc(id)}`, { method: 'PUT', body }), RBAC_KEYS)

export const useDeleteRole = () => useInvalidating((id: string) => apiFetch<null>(`/api/v1/roles/${enc(id)}`, { method: 'DELETE' }), RBAC_KEYS)

export const useCreateBinding = () =>
  useInvalidating((body: { roleId: string; subject: string; envs: string[]; projects: string[]; types: string[] }) => apiFetch<RoleBinding>('/api/v1/role-bindings', { method: 'POST', body }), RBAC_KEYS)

export const useUpdateBinding = () =>
  useInvalidating(({ id, ...scope }: { id: number; envs: string[]; projects: string[]; types: string[] }) => apiFetch<RoleBinding>(`/api/v1/role-bindings/${id}`, { method: 'PUT', body: scope }), RBAC_KEYS)

export const useDeleteBinding = () => useInvalidating((id: number) => apiFetch<null>(`/api/v1/role-bindings/${id}`, { method: 'DELETE' }), RBAC_KEYS)

// ---- kargo pipeline generation ----------------------------------------------

/** Generating reads every upstream fresh, so it is asked for explicitly
 *  rather than on every render. */
export function useKargoPlan(domain: string, enabled: boolean) {
  return useQuery({
    queryKey: ['kargo-generate', domain],
    queryFn: ({ signal }) => apiFetch<KargoPlan>(`/api/v1/kargo/generate?domain=${enc(domain)}`, { signal }),
    enabled,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
  })
}

/** Commits the generated pipeline. One commit, so the repository is never
 *  left describing a pipeline that half exists. */
export function usePushKargo() {
  return useMutation({
    mutationFn: (body: { domain: string; message?: string }) =>
      apiFetch<KargoPushed>('/api/v1/kargo/push', { method: 'POST', body }),
  })
}
