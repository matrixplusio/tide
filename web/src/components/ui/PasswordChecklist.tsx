import { useTranslation } from 'react-i18next'
import { passwordChecklist } from '../../lib/validation'

/** Live policy checklist under new-password fields. Purely informative; errors come from the schema. */
export function PasswordChecklist({ password, username, id }: { password: string; username?: string; id?: string }) {
  const { t } = useTranslation()
  const rules = passwordChecklist(password, username)
  return (
    <ul className="pwcheck" id={id} aria-label={t('common.passwordRules')}>
      {rules.map((r) => (
        <li key={r.key} className={r.ok ? 'ok' : ''}>
          <span aria-hidden="true">{r.ok ? '✓' : '·'}</span> {r.label}
          <span className="sr-only">{r.ok ? t('common.ruleMet') : t('common.ruleUnmet')}</span>
        </li>
      ))}
    </ul>
  )
}
