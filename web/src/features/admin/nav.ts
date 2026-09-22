import { can } from '../../lib/permissions'
import type { Me, Permission } from '../../lib/types'

// Secondary navigation of the admin console. Kept out of the lazy chunk: the
// shell needs it to decide whether Admin shows up and where /admin leads.
// label and hint are catalogue keys, rendered where they are shown.

export interface AdminItem {
  path: string
  label: string
  perm: Permission
  hint: string
}

export const ADMIN_GROUPS: readonly { label: string; items: readonly AdminItem[] }[] = [
  {
    label: 'admin.navAccess',
    items: [
      { path: 'users', label: 'admin.navUsers', perm: 'users.manage', hint: 'admin.navUsersHint' },
      { path: 'groups', label: 'admin.navGroups', perm: 'users.manage', hint: 'admin.navGroupsHint' },
      { path: 'roles', label: 'admin.navRoles', perm: 'roles.manage', hint: 'admin.navRolesHint' },
    ],
  },
  {
    label: 'admin.navRelease',
    items: [
      { path: 'environments', label: 'admin.navEnvironments', perm: 'environments.manage', hint: 'admin.navEnvironmentsHint' },
      { path: 'upstreams', label: 'admin.navUpstreams', perm: 'environments.manage', hint: 'admin.navUpstreamsHint' },
      { path: 'catalog', label: 'admin.navCatalog', perm: 'environments.manage', hint: 'admin.navCatalogHint' },
      { path: 'release', label: 'admin.navPolicy', perm: 'settings.manage', hint: 'admin.navPolicyHint' },
      { path: 'notify', label: 'admin.navNotify', perm: 'notifications.manage', hint: 'admin.navNotifyHint' },
      { path: 'ci', label: 'admin.navCI', perm: 'settings.manage', hint: 'admin.navCIHint' },
    ],
  },
  {
    label: 'admin.navSystem',
    items: [
      { path: 'security', label: 'admin.navSecurity', perm: 'settings.manage', hint: 'admin.navSecurityHint' },
      { path: 'sso', label: 'admin.navSso', perm: 'settings.manage', hint: 'admin.navSsoHint' },
      { path: 'general', label: 'admin.navGeneral', perm: 'settings.manage', hint: 'admin.navGeneralHint' },
    ],
  },
]

export function adminItemsFor(me: Pick<Me, 'permissions'>): AdminItem[] {
  return ADMIN_GROUPS.flatMap((g) => g.items.filter((i) => can(me, i.perm)))
}

export function firstAdminPath(me: Pick<Me, 'permissions'>): string | null {
  const first = adminItemsFor(me)[0]
  return first ? `/admin/${first.path}` : null
}
