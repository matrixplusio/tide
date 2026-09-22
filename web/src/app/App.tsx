import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, Navigate, NavLink, useLocation } from 'react-router-dom'
import { isApiError } from '../lib/api'
import { ErrCode } from '../lib/errcode'
import { Banner, Button, ErrorState, Loading } from '../components/ui'
import { LanguageSwitch, UpstreamHealth } from '../components/domain'
import { ADMIN_PERMISSIONS, canAny, canView, envScopeText } from '../lib/permissions'
import { fmtTime } from '../lib/format'
import type { Me } from '../lib/types'
import { LoginPage } from '../features/auth/LoginPage'
import { useLogout } from '../features/auth/queries'
import { SetupPage } from '../features/setup/SetupPage'
import { useServices } from '../features/services/queries'
import { useActiveReleaseCount, useAwaitingApprovalCount } from '../features/releases/queries'
import { MeContext, useMe, useMeQuery } from './session'
import { AppRoutes } from './routes'

const icons: Record<string, ReactNode> = {
  home: <path d="M3 10l9-7 9 7v10a2 2 0 01-2 2H5a2 2 0 01-2-2z" />,
  svc: (
    <>
      <rect x="3" y="3" width="7" height="7" rx="1.6" />
      <rect x="14" y="3" width="7" height="7" rx="1.6" />
      <rect x="3" y="14" width="7" height="7" rx="1.6" />
      <rect x="14" y="14" width="7" height="7" rx="1.6" />
    </>
  ),
  rel: <path d="M5 4h14M5 9h14M5 14h9M5 19h6" />,
  audit: (
    <>
      <path d="M12 3l8 3v6c0 4.5-3.4 8.2-8 9-4.6-.8-8-4.5-8-9V6z" />
      <path d="M9 12l2 2 4-4" />
    </>
  ),
  insights: (
    <>
      <path d="M4 20V10M10 20V4M16 20v-7M22 20H2" />
    </>
  ),
  me: (
    <>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21c0-4.4 3.6-7 8-7s8 2.6 8 7" />
    </>
  ),
  set: (
    <>
      <circle cx="12" cy="12" r="3.2" />
      <path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M5.3 18.7l2.1-2.1M16.6 7.4l2.1-2.1" />
    </>
  ),
}

type NavItem = {
  to: string
  label: string
  icon: keyof typeof icons
  show: (me: Me) => boolean
  mobileOnly?: boolean
}

const nav: NavItem[] = [
  {
    to: '/',
    label: 'nav.overview',
    icon: 'home',
    show: (me) => canView(me, 'services.view'),
  },
  {
    to: '/services',
    label: 'nav.services',
    icon: 'svc',
    show: (me) => canView(me, 'services.view'),
  },
  {
    to: '/releases',
    label: 'nav.releases',
    icon: 'rel',
    show: (me) => canView(me, 'releases.view'),
  },
  {
    to: '/audit',
    label: 'nav.audit',
    icon: 'audit',
    show: (me) => canView(me, 'audit.view'),
  },
  {
    to: '/insights',
    label: 'nav.insights',
    icon: 'insights',
    show: (me) => canView(me, 'audit.view'),
  },
  {
    to: '/admin',
    label: 'nav.admin',
    icon: 'set',
    show: (me) => canAny(me, ADMIN_PERMISSIONS),
  },
  {
    to: '/profile',
    label: 'nav.me',
    icon: 'me',
    show: () => true,
    mobileOnly: true,
  },
]

// initial is the one character shown in place of an avatar. Latin names get
// an upper-case letter; anything else (a Chinese name, for instance) keeps
// its first character as written.
function initial(name: string): string {
  const c = [...name][0] ?? '?'
  return /[a-z]/i.test(c) ? c.toUpperCase() : c
}

function Icon({ name }: { name: keyof typeof icons }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {icons[name]}
    </svg>
  )
}

export default function App() {
  const loc = useLocation()
  const isSetup = loc.pathname.startsWith('/setup')
  const me = useMeQuery(!isSetup)

  if (isSetup) return <SetupPage />
  if (me.isPending)
    return (
      <div className="center">
        <Loading />
      </div>
    )
  if (me.error) {
    if (isApiError(me.error, ErrCode.SetupRequired)) return <Navigate to="/setup" replace />
    if (isApiError(me.error, ErrCode.Unauthenticated)) return <LoginPage />
    return (
      <div className="center">
        <div className="panel">
          <ErrorState error={me.error} onRetry={() => void me.refetch()} />
        </div>
      </div>
    )
  }

  return (
    <MeContext.Provider value={me.data}>
      <Shell />
    </MeContext.Provider>
  )
}

function Shell() {
  const { t } = useTranslation()
  const me = useMe()
  const loc = useLocation()
  const canServices = canView(me, 'services.view')
  const services = useServices(canServices)
  const active = useActiveReleaseCount(canView(me, 'releases.view'))
  const logout = useLogout()
  // project → service count. Domains are a filter on the services page, not
  // sidebar entries: a project can have many of them.
  const projects = new Map<string, number>()
  for (const s of services.data?.services ?? []) {
    if (s.project) projects.set(s.project, (projects.get(s.project) ?? 0) + 1)
  }
  const items = nav.filter((n) => n.show(me))
  const currentProject = loc.pathname === '/services' ? new URLSearchParams(loc.search).get('project') : null
  const activeCount = active.data ?? 0
  const awaiting = useAwaitingApprovalCount(canView(me, 'releases.view'))
  const awaitingCount = awaiting.data ?? 0
  const siteName = me.app?.siteName || 'Tide'

  return (
    <div className="app">
      <a className="skip" href="#main">
        {t('nav.skipToContent')}
      </a>
      <aside className="side" aria-label={t('nav.sidebar')}>
        <div className="brand">
          {siteName === 'Tide' ? (
            <>
              Tide<i>.</i>
            </>
          ) : (
            <span className="ellipsis block">{siteName}</span>
          )}
        </div>
        <nav className="snav" aria-label={t('nav.main')}>
          {items
            .filter((n) => !n.mobileOnly)
            .map((n) => (
              <NavLink key={n.to} to={n.to} end={n.to === '/'} className={({ isActive }) => `sitem ${isActive && !(n.to === '/services' && currentProject) ? 'active' : ''}`}>
                <Icon name={n.icon} />
                {t(n.label)}
                {n.to === '/releases' && awaitingCount > 0 && (
                  <span className="badge approve" aria-label={t('nav.awaitingCount', { count: awaitingCount })} title={t('nav.awaiting')}>
                    {t('nav.awaitingShort', { count: awaitingCount })}
                  </span>
                )}
                {n.to === '/releases' && activeCount > 0 && (
                  <span className={`badge ${awaitingCount > 0 ? 'next' : ''}`} aria-label={t('nav.activeCount', { count: activeCount })}>
                    {activeCount}
                  </span>
                )}
              </NavLink>
            ))}
        </nav>
        {projects.size > 0 && (
          <>
            <div className="sgroup" id="projects-h">
              {t('nav.projects')}
            </div>
            <nav className="snav side-projects" aria-labelledby="projects-h">
              {[...projects]
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([p, n]) => {
                  const active = currentProject === p
                  return (
                    <Link key={p} to={`/services?${new URLSearchParams({ project: p }).toString()}`} className={`sitem ${active ? 'active' : ''}`} aria-current={active ? 'page' : undefined}>
                      <span className="ellipsis">{p}</span>
                      <span className="count">{n}</span>
                    </Link>
                  )
                })}
            </nav>
          </>
        )}
        <div className="health">
          <UpstreamHealth upstreams={services.data?.upstreams ?? []} detailPath={canAny(me, ['environments.manage']) ? '/admin/upstreams' : undefined} />
          <div className="me-side">
            <NavLink to="/profile" className={({ isActive }) => `me-link ${isActive ? 'active' : ''}`} title={t('nav.profile')}>
              <span className="avatar" aria-hidden="true">
                {initial(me.user.name || me.user.username || me.user.sub)}
              </span>
              <span className="ellipsis">{me.user.name || me.user.username || me.user.sub}</span>
            </NavLink>
            <LanguageSwitch />
            <Button
              size="small"
              variant="quiet"
              className="icon-only"
              loading={logout.isPending}
              onClick={() => logout.mutate()}
              aria-label={t('nav.signOut')}
              title={t('nav.signOut')}
            >
              <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M15 17l5-5-5-5M20 12H9M11 4H6a2 2 0 00-2 2v12a2 2 0 002 2h5" />
              </svg>
            </Button>
          </div>
          {/* Which build is this? Answerable without shell access to the pod. */}
          {me.app.version !== undefined && me.app.version !== '' && (
            <div className="build" title={t('nav.build')}>
              {me.app.version}
            </div>
          )}
        </div>
      </aside>

      <main className="main" id="main">
        <AppBanners />
        <AppRoutes />
      </main>

      <nav className="tabbar" aria-label={t('nav.main')}>
        {items.map((n) => (
          <NavLink key={n.to} to={n.to} end={n.to === '/'} className={({ isActive }) => `tab ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} />
            {t(n.label)}
          </NavLink>
        ))}
      </nav>
    </div>
  )
}

/** Site-wide announcement and active release freezes, above every page. */
function AppBanners() {
  const { t } = useTranslation()
  const me = useMe()
  const ann = me.app?.announcement
  const freezes = me.app?.activeFreezes ?? []
  if (!ann?.text && freezes.length === 0) return null
  return (
    <div className="app-banners">
      {ann?.text && (
        <Banner tone={ann.level === 'warning' ? 'warn' : 'info'} style={{ marginTop: 0 }}>
          <span className="prewrap">{ann.text}</span>
        </Banner>
      )}
      {freezes.length > 0 && (
        <Banner tone="warn" style={{ marginTop: ann?.text ? 6 : 0 }}>
          <b>{t('freeze.banner')}</b>
          {freezes.map((f, i) => (
            <span key={`${f.name}-${i}`} className="block">
              {t('freeze.window', { name: f.name, envs: envScopeText(f.envs, me.environments), until: fmtTime(f.endsAt) })}
              {f.reason ? ` · ${f.reason}` : ''}
            </span>
          ))}
        </Banner>
      )}
    </div>
  )
}
