import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { Toolbar } from '../../components/ui'

/** Toolbar for an admin section: on narrow screens it leads back to the list. */
export function AdminToolbar({ title, sub, back, children }: { title: ReactNode; sub?: ReactNode; back?: { to: string; label: string }; children?: ReactNode }) {
  const { t } = useTranslation()
  return (
    <Toolbar title={title} sub={sub} back={back ?? { to: '/admin', label: t('nav.admin'), narrowOnly: true }}>
      {children}
    </Toolbar>
  )
}
