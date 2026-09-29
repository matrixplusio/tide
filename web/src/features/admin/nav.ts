import { canAny } from '../../lib/permissions'
import type { Me, Permission } from '../../lib/types'

// Secondary navigation of the admin console. Kept out of the lazy chunk: the
// shell needs it to decide whether Admin shows up and where /admin leads.
// label and hint are catalogue keys, rendered where they are shown.

export interface AdminItem {
  path: string
  label: string
  // Any one of these opens it. A page split off a broader permission keeps
  // answering to that one too, so nobody loses a page by the split.
  perms: readonly Permission[]
  hint: string
}

export const ADMIN_GROUPS: readonly { label: string; items: readonly AdminItem[] }[] = [
  {
    label: 'admin.navAccess',
    items: [
      { path: 'users', label: 'admin.navUsers', perms: ['users.manage'], hint: 'admin.navUsersHint' },
      { path: 'groups', label: 'admin.navGroups', perms: ['users.manage'], hint: 'admin.navGroupsHint' },
      { path: 'roles', label: 'admin.navRoles', perms: ['roles.manage'], hint: 'admin.navRolesHint' },
    ],
  },
  {
    label: 'admin.navRelease',
    items: [
      { path: 'environments', label: 'admin.navEnvironments', perms: ['environments.manage'], hint: 'admin.navEnvironmentsHint' },
      { path: 'upstreams', label: 'admin.navUpstreams', perms: ['environments.manage'], hint: 'admin.navUpstreamsHint' },
      { path: 'catalog', label: 'admin.navCatalog', perms: ['environments.manage'], hint: 'admin.navCatalogHint' },
      { path: 'kargo', label: 'admin.navKargo', perms: ['pipelines.generate', 'environments.manage'], hint: 'admin.navKargoHint' },
      { path: 'release', label: 'admin.navPolicy', perms: ['settings.manage'], hint: 'admin.navPolicyHint' },
      { path: 'notify', label: 'admin.navNotify', perms: ['notifications.manage'], hint: 'admin.navNotifyHint' },
      { path: 'ci', label: 'admin.navCI', perms: ['ci.manage', 'settings.manage'], hint: 'admin.navCIHint' },
    ],
  },
  {
    label: 'admin.navSystem',
    items: [
      { path: 'security', label: 'admin.navSecurity', perms: ['settings.manage'], hint: 'admin.navSecurityHint' },
      { path: 'sso', label: 'admin.navSso', perms: ['settings.manage'], hint: 'admin.navSsoHint' },
      { path: 'general', label: 'admin.navGeneral', perms: ['settings.manage'], hint: 'admin.navGeneralHint' },
    ],
  },
]

export function adminItemsFor(me: Pick<Me, 'permissions'>): AdminItem[] {
  return ADMIN_GROUPS.flatMap((g) => g.items.filter((i) => canAny(me, i.perms)))
}

export function firstAdminPath(me: Pick<Me, 'permissions'>): string | null {
  const first = adminItemsFor(me)[0]
  return first ? `/admin/${first.path}` : null
}
