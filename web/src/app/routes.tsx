import { lazy, Suspense } from 'react'
import type { ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { Loading } from '../components/ui'
import { safeReturnPath } from '../lib/format'
import { ADMIN_PERMISSIONS, can, canAny, canView, homePath } from '../lib/permissions'
import type { Permission } from '../lib/types'
import { OverviewPage } from '../features/overview/OverviewPage'
import { ServicesPage } from '../features/services/ServicesPage'
import { ServiceDetailPage } from '../features/services/ServiceDetailPage'
import { EnvDetailPage } from '../features/services/EnvDetailPage'
import { ReleasesPage } from '../features/releases/ReleasesPage'
import { ReleaseDetailPage } from '../features/releases/ReleaseDetailPage'
import { useMe } from './session'

// Less frequent pages load on demand.
const AdminRoutes = lazy(() => import('../features/admin/AdminRoutes'))
const ProfilePage = lazy(() => import('../features/profile/ProfilePage').then((m) => ({ default: m.ProfilePage })))
const BatchPage = lazy(() => import('../features/releases/BatchPage').then((m) => ({ default: m.BatchPage })))
const AuditPage = lazy(() => import('../features/audit/AuditPage').then((m) => ({ default: m.AuditPage })))
const InsightsPage = lazy(() => import('../features/insights/InsightsPage').then((m) => ({ default: m.InsightsPage })))
const PodDetailPage = lazy(() => import('../features/services/PodDetailPage').then((m) => ({ default: m.PodDetailPage })))

// View permissions are scoped: holding one anywhere opens the page, the
// server decides what is inside. Admin permissions stay global.
function Need({ perm, children }: { perm: Permission; children: ReactNode }) {
  const me = useMe()
  const allowed = VIEW_PERMISSIONS.includes(perm) ? canView(me, perm) : can(me, perm)
  return allowed ? children : <Navigate to={homePath(me)} replace />
}

const VIEW_PERMISSIONS: Permission[] = ['services.view', 'releases.view', 'audit.view']

export function AppRoutes() {
  const loc = useLocation()
  const me = useMe()
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        <Route path="/" element={<Need perm="services.view"><OverviewPage /></Need>} />
        <Route path="/services" element={<Need perm="services.view"><ServicesPage /></Need>} />
        <Route path="/services/:service" element={<Need perm="services.view"><ServiceDetailPage /></Need>} />
        <Route path="/services/:service/envs/:env" element={<Need perm="services.view"><EnvDetailPage /></Need>} />
        <Route path="/services/:service/envs/:env/pods/:pod" element={<Need perm="services.view"><PodDetailPage /></Need>} />
        <Route path="/releases" element={<Need perm="releases.view"><ReleasesPage /></Need>} />
        <Route path="/releases/batch" element={<Need perm="services.view"><BatchPage /></Need>} />
        <Route path="/releases/:id" element={<Need perm="releases.view"><ReleaseDetailPage /></Need>} />
        <Route path="/audit" element={<Need perm="audit.view"><AuditPage /></Need>} />
        <Route path="/insights" element={<Need perm="audit.view"><InsightsPage /></Need>} />
        <Route path="/profile" element={<ProfilePage />} />
        <Route path="/admin/*" element={canAny(me, ADMIN_PERMISSIONS) ? <AdminRoutes /> : <Navigate to={homePath(me)} replace />} />
        <Route path="/settings" element={<Navigate to="/admin" replace />} />
        <Route path="/login" element={<Navigate to={safeReturnPath(new URLSearchParams(loc.search).get('return'))} replace />} />
        <Route path="*" element={<Navigate to={homePath(me)} replace />} />
      </Routes>
    </Suspense>
  )
}
