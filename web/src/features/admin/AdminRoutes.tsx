import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { Navigate, NavLink, Route, Routes } from 'react-router-dom'
import { useMe } from '../../app/session'
import { can } from '../../lib/permissions'
import type { Permission } from '../../lib/types'
import { Chev, Group, GroupHeader, Page, Row, Toolbar } from '../../components/ui'
import { ADMIN_GROUPS, firstAdminPath } from './nav'
import { UsersPage } from './users/UsersPage'
import { UserDetailPage } from './users/UserDetailPage'
import { GroupsPage } from './groups/GroupsPage'
import { GroupDetailPage } from './groups/GroupDetailPage'
import { RolesPage } from './roles/RolesPage'
import { RoleDetailPage } from './roles/RoleDetailPage'
import { CIPage } from './ci/CIPage'
import { CatalogPage, EnvironmentsPage, GeneralPage, NotifyPage, ReleasePolicyPage, SecurityPage, SsoPage, UpstreamsPage } from './settings/SettingsPages'
import { KargoGenPage } from './settings/KargoGenPage'

function isNarrow(): boolean {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 760px)').matches
}

/** Admin console: a System Settings–like list on the left, the section on the right. */
export default function AdminRoutes() {
  const { t } = useTranslation()
  const me = useMe()
  const guard = (perm: Permission, el: ReactNode) => (can(me, perm) ? el : <Navigate to="/admin" replace />)
  const first = firstAdminPath(me)

  return (
    <div className="admin">
      <nav className="admin-nav" aria-label={t('nav.admin')}>
        <AdminNavItems />
      </nav>
      <div className="admin-main">
        <Routes>
          {/* Narrow screens: /admin is the list page; wide screens open the first section. */}
          <Route index element={isNarrow() || !first ? <AdminIndexPage /> : <Navigate to={first} replace />} />
          <Route path="users" element={guard('users.manage', <UsersPage />)} />
          <Route path="users/:id" element={guard('users.manage', <UserDetailPage />)} />
          <Route path="groups" element={guard('users.manage', <GroupsPage />)} />
          <Route path="groups/:name" element={guard('users.manage', <GroupDetailPage />)} />
          <Route path="roles" element={guard('roles.manage', <RolesPage />)} />
          <Route path="roles/:id" element={guard('roles.manage', <RoleDetailPage />)} />
          <Route path="environments" element={guard('environments.manage', <EnvironmentsPage />)} />
          <Route path="upstreams" element={guard('environments.manage', <UpstreamsPage />)} />
          <Route path="catalog" element={guard('environments.manage', <CatalogPage />)} />
          <Route path="kargo" element={guard('environments.manage', <KargoGenPage />)} />
          <Route path="release" element={guard('settings.manage', <ReleasePolicyPage />)} />
          <Route path="notify" element={guard('notifications.manage', <NotifyPage />)} />
          <Route path="ci" element={guard('settings.manage', <CIPage />)} />
          <Route path="security" element={guard('settings.manage', <SecurityPage />)} />
          <Route path="sso" element={guard('settings.manage', <SsoPage />)} />
          <Route path="general" element={guard('settings.manage', <GeneralPage />)} />
          <Route path="*" element={<Navigate to="/admin" replace />} />
        </Routes>
      </div>
    </div>
  )
}

function AdminNavItems() {
  const { t } = useTranslation()
  const me = useMe()
  return (
    <>
      {ADMIN_GROUPS.map((g) => {
        const items = g.items.filter((i) => can(me, i.perm))
        if (items.length === 0) return null
        return (
          <div key={g.label}>
            <div className="sgroup">{t(g.label)}</div>
            <div className="snav">
              {items.map((i) => (
                <NavLink key={i.path} to={`/admin/${i.path}`} className={({ isActive }) => `sitem ${isActive ? 'active' : ''}`}>
                  {t(i.label)}
                </NavLink>
              ))}
            </div>
          </div>
        )
      })}
    </>
  )
}

function AdminIndexPage() {
  const { t } = useTranslation()
  const me = useMe()
  return (
    <>
      <Toolbar title={t('nav.admin')} />
      <Page>
        {ADMIN_GROUPS.map((g) => {
          const items = g.items.filter((i) => can(me, i.perm))
          if (items.length === 0) return null
          return (
            <div key={g.label}>
              <GroupHeader>{t(g.label)}</GroupHeader>
              <Group className="admin-list">
                {items.map((i) => (
                  <Row key={i.path} to={`/admin/${i.path}`}>
                    <div className="grow">
                      <div className="t">{t(i.label)}</div>
                      <div className="d">{t(i.hint)}</div>
                    </div>
                    <Chev />
                  </Row>
                ))}
              </Group>
            </div>
          )
        })}
      </Page>
    </>
  )
}
