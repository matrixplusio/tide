import { useTranslation } from 'react-i18next'
import { LOCALES, currentLocale, setLocale, type Locale } from '../../lib/i18n'

/**
 * Switches the UI language. The choice is remembered in this browser and is
 * also what apiFetch asks the server for, so a page and the messages on it
 * never disagree.
 *
 * It is styled as sidebar chrome rather than a form field: a boxed select
 * next to a name and a sign-out button reads as something to fill in, and
 * this is a preference you set once.
 */
export function LanguageSwitch() {
  const { t } = useTranslation()
  return (
    <label className="lang-switch">
      <span className="sr-only">{t('language.label')}</span>
      <select
        className="lang-select"
        value={currentLocale()}
        onChange={(e) => void setLocale(e.target.value as Locale)}
        aria-label={t('language.label')}
      >
        {LOCALES.map((l) => (
          <option key={l} value={l}>
            {t(`language.${l}`)}
          </option>
        ))}
      </select>
    </label>
  )
}
