import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { LinkButton } from './Button'

export function Toolbar({ title, sub, back, children }: { title: ReactNode; sub?: ReactNode; back?: { to: string; label: string; narrowOnly?: boolean }; children?: ReactNode }) {
  const { t } = useTranslation()
  return (
    <header className="toolbar">
      {back && (
        <LinkButton size="small" className={back.narrowOnly ? 'narrow-only' : undefined} to={back.to} aria-label={t('common.back', { what: back.label })}>
          ‹ {back.label}
        </LinkButton>
      )}
      <h1>{title}</h1>
      {sub && <span className="sub">{sub}</span>}
      {children && <div className="tbar-r">{children}</div>}
    </header>
  )
}

export function Page({ children, wide }: { children: ReactNode; wide?: boolean }) {
  return (
    <div className="scroll">
      <div className={`pad ${wide ? 'wide' : ''}`}>{children}</div>
    </div>
  )
}
