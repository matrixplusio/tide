import { useTranslation } from 'react-i18next'
import { Button } from './Button'

export function Pager({ page, pageSize, total, onChange }: { page: number; pageSize: number; total: number; onChange: (page: number) => void }) {
  const { t } = useTranslation()
  const pages = Math.max(1, Math.ceil(total / pageSize))
  if (total <= pageSize && page <= 1) return null
  return (
    <nav className="pager" aria-label={t('pager.label')}>
      <Button size="small" variant="quiet" disabled={page <= 1} onClick={() => onChange(page - 1)}>
        {t('pager.prev')}
      </Button>
      <span className="muted countdown">
        {t('pager.status', { page, pages, total })}
      </span>
      <Button size="small" variant="quiet" disabled={page >= pages} onClick={() => onChange(page + 1)}>
        {t('pager.next')}
      </Button>
    </nav>
  )
}
