import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Banner } from '../ui'

export function NoUpstreamsBanner({ canConfigure, children }: { canConfigure: boolean; children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <Banner tone="warn" style={{ marginTop: 0 }} action={canConfigure ? <Link to="/admin/upstreams">{t('domain.configure')}</Link> : <span>{t('domain.askAdmin')}</span>}>
      {children}
    </Banner>
  )
}
