import { i18n } from './i18n'
import type { EffectiveBinding, EnvironmentInfo, Me, Permission, Tier } from './types'

// Permission checks mirror what /me reports. They only decide what the UI
// offers; every endpoint checks again on the server.

export function can(me: Pick<Me, 'permissions'>, perm: Permission): boolean {
  const ps = me.permissions ?? []
  return ps.includes(perm) || (ps as string[]).includes('*')
}

/**
 * Whether a view permission (services / releases / audit) is held anywhere.
 * The server filters each page's contents per service, so this only decides
 * whether the page and its navigation entry exist at all.
 */
export function canView(me: Pick<Me, 'permissions' | 'canView'>, perm: Permission): boolean {
  if ((me.permissions as string[] | undefined)?.includes('*')) return true
  return !!me.canView?.[perm]
}

export function canAny(me: Pick<Me, 'permissions'>, perms: readonly Permission[]): boolean {
  return perms.some((p) => can(me, p))
}

/** Environment-scoped permission on one environment. */
export function canEnv(me: Pick<Me, 'permissions' | 'envPermissions'>, perm: Permission, env: string): boolean {
  if ((me.permissions as string[] | undefined)?.includes('*')) return true
  const byEnv = me.envPermissions ?? {}
  return !!byEnv[env]?.includes(perm)
}

/**
 * Environment permission on one service: grants may be narrowed to projects
 * and service types, so the service's project and type decide too. Pass what
 * the catalog knows; an unknown project only matches unrestricted grants.
 */
export function canService(me: Pick<Me, 'permissions' | 'scopedGrants'>, perm: Permission, env: string, project?: string, type?: string): boolean {
  if ((me.permissions as string[] | undefined)?.includes('*')) return true
  return (me.scopedGrants ?? []).some((g) => g.permissions.includes(perm) && g.envs.includes(env) && scopeMatches(g.projects, project) && scopeMatches(g.types, type))
}

/** Empty or ["*"] matches everything; otherwise the value must be listed. */
export function scopeMatches(list: readonly string[] | null | undefined, value: string | undefined): boolean {
  if (!list || list.length === 0 || list.includes('*')) return true
  return !!value && list.includes(value)
}

/** Where "/" leads for someone who cannot see the overview. */
export function homePath(me: Pick<Me, 'permissions' | 'canView'>): string {
  if (canView(me, 'services.view')) return '/'
  if (canView(me, 'releases.view')) return '/releases'
  if (canView(me, 'audit.view')) return '/audit'
  return '/profile'
}

export const ADMIN_PERMISSIONS: readonly Permission[] = ['users.manage', 'roles.manage', 'environments.manage', 'notifications.manage', 'settings.manage']

// Holds catalogue keys, not words: read them through permissionLabel.
export const PERMISSION_LABELS: Record<Permission, string> = {
  'services.view': 'perm.servicesView',
  'releases.view': 'perm.releasesView',
  'audit.view': 'perm.auditView',
  'users.manage': 'perm.usersManage',
  'roles.manage': 'perm.rolesManage',
  'environments.manage': 'perm.environmentsManage',
  'notifications.manage': 'perm.notificationsManage',
  'settings.manage': 'perm.settingsManage',
  'pods.view': 'perm.podsView',
  'releases.create': 'perm.releasesCreate',
  'releases.restart': 'perm.releasesRestart',
  'releases.sync': 'perm.releasesSync',
  'releases.cancel_any': 'perm.releasesCancelAny',
}

export function permissionLabel(p: string): string {
  if (p === '*') return i18n.t('perm.all')
  const key = PERMISSION_LABELS[p as Permission]
  return key ? i18n.t(key) : p
}

// Holds catalogue keys, not words: read them through tierLabel.
export const TIERS: readonly (readonly [Tier, string])[] = [
  ['development', 'tier.development'],
  ['testing', 'tier.testing'],
  ['staging', 'tier.staging'],
  ['production', 'tier.production'],
]

export function tierLabel(t: string): string {
  const key = TIERS.find(([k]) => k === t)?.[1]
  return key ? i18n.t(key) : t
}

/** One env selector (`*`, `tier:<Tier>` or an environment name) in words. */
export function envSelectorLabel(sel: string, environments: readonly EnvironmentInfo[] = []): string {
  if (sel === '*') return i18n.t('scope.allEnvs')
  if (sel.startsWith('tier:')) return i18n.t('scope.tierEnvs', { tier: tierLabel(sel.slice('tier:'.length)) })
  const env = environments.find((e) => e.name === sel)
  return env?.displayName && env.displayName !== env.name ? i18n.t('scope.envNamed', { display: env.displayName, name: env.name }) : sel
}

export function envScopeText(envs: readonly string[] | null | undefined, environments: readonly EnvironmentInfo[] = []): string {
  if (!envs || envs.length === 0) return '—'
  // Chinese enumerates with 、 and English with a comma.
  return envs.map((e) => envSelectorLabel(e, environments)).join(i18n.t('scope.listSeparator'))
}

/** `*` | `user:<sub>` | `group:<name>` in words; `name` is the server's subjectName. */
export function subjectLabel(subject: string, name?: string): string {
  if (subject === '*') return i18n.t('scope.everyone')
  if (subject.startsWith('group:')) return i18n.t('scope.group', { name: subject.slice('group:'.length) })
  if (subject.startsWith('user:')) return name || subject.slice('user:'.length)
  return name || subject
}

export function bindingViaLabel(b: Pick<EffectiveBinding, 'via' | 'subject' | 'subjectName'>): string {
  if (b.via === 'all') return i18n.t('scope.everyone')
  if (b.via === 'group') return i18n.t('scope.viaGroup', { label: subjectLabel(b.subject, b.subjectName) })
  return i18n.t('scope.direct')
}

/** Jira link when a base URL is configured; only http(s) bases become links. */
export function jiraHref(base: string | undefined | null, ticket: string): string | undefined {
  if (!base || !ticket) return undefined
  try {
    const u = new URL(`${base.replace(/\/+$/, '')}/browse/${encodeURIComponent(ticket)}`)
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.href : undefined
  } catch {
    return undefined
  }
}
