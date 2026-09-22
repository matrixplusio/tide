import { i18n } from '../../../lib/i18n'
import type { ReactNode } from 'react'
import { ErrorState, Loading, Page } from '../../../components/ui'
import { isApiError } from '../../../lib/api'
import { UpstreamStatusPanel } from '../../../components/domain'
import { useServices } from '../../services/queries'
import { AdminToolbar } from '../AdminToolbar'
import { useSettings } from '../queries'
import type { SettingsData } from '../types'
import { EnvironmentsForm } from './EnvironmentsForm'
import { UpstreamsForm } from './UpstreamsForm'
import { CatalogForm } from './CatalogForm'
import { ReleasePolicyForm } from './ReleasePolicyForm'
import { NotifyForm } from './NotifyForm'
import { SecurityForm } from './SecurityForm'
import { SsoForm } from './SsoForm'
import { SystemForm } from './SystemForm'

function SettingsPage({ title, sub, children }: { title: string; sub?: string; children: (d: SettingsData) => ReactNode }) {
  const q = useSettings()
  return (
    <>
      <AdminToolbar title={title} sub={sub} />
      <Page>
        {q.isPending && <Loading />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {q.data && children(q.data)}
      </Page>
    </>
  )
}

export const EnvironmentsPage = () => (
  <SettingsPage title={i18n.t('admin.navEnvironments')} sub={i18n.t('admin.envsSub')}>
    {(d) => <EnvironmentsForm initial={d.environments} upstreams={(d.upstreams?.items ?? []).map((u) => u.name)} />}
  </SettingsPage>
)

// The status panel comes first: somebody opening this page after a page
// stopped loading wants to know what is answering, not what was configured.
export const UpstreamsPage = () => {
  const services = useServices()
  return (
    <SettingsPage title={i18n.t('admin.navUpstreams')} sub={i18n.t('admin.upstreamsSub')}>
      {(d) => (
        <>
          <UpstreamStatusPanel upstreams={services.data?.upstreams ?? []} error={upstreamErrorOf(services.error)} />
          <UpstreamsForm initial={d.upstreams} />
        </>
      )}
    </SettingsPage>
  )
}

// A failure to reach any upstream at all shows as the panel's own message;
// anything else is the page's problem, not the upstreams'.
function upstreamErrorOf(err: unknown): string | undefined {
  return isApiError(err) ? err.msg : undefined
}

export const CatalogPage = () => (
  <SettingsPage title={i18n.t('admin.navCatalog')} sub={i18n.t('admin.catalogSub')}>
    {(d) => <CatalogForm initial={d.catalog} />}
  </SettingsPage>
)

export const ReleasePolicyPage = () => <SettingsPage title={i18n.t('admin.navPolicy')}>{(d) => <ReleasePolicyForm initial={d.release} />}</SettingsPage>

export const NotifyPage = () => <SettingsPage title={i18n.t('admin.navNotify')}>{(d) => <NotifyForm initial={d.notify} />}</SettingsPage>

export const SecurityPage = () => <SettingsPage title={i18n.t('admin.navSecurity')}>{(d) => <SecurityForm initial={d.security} />}</SettingsPage>

export const SsoPage = () => <SettingsPage title="SSO">{(d) => <SsoForm initial={d.oidc} />}</SettingsPage>

export const GeneralPage = () => <SettingsPage title={i18n.t('admin.navGeneral')}>{(d) => <SystemForm initial={d.system} />}</SettingsPage>
