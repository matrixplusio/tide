import { useTranslation } from 'react-i18next'
import { Banner } from '../ui'

/** Explains why a service name is ambiguous; changes to it are refused until fixed. */
export function ConflictBanner({ conflicts }: { conflicts?: string[] | null }) {
  const { t } = useTranslation()
  if (!conflicts?.length) return null
  return (
    <Banner tone="bad">
      <div>
        <b>{t('domain.conflictTitle')}</b>
        {conflicts.map((c) => (
          <div key={c} className="d">
            {c}
          </div>
        ))}
        <div className="d">{t('domain.conflictHelp')}</div>
      </div>
    </Banner>
  )
}
