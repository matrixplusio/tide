import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'
import en from '../locales/en'
import zhCN from '../locales/zh-CN'

// The languages Tide serves. The server has the same list in internal/i18n;
// adding one means adding it in both places.
export const LOCALES = ['zh-CN', 'en'] as const
export type Locale = (typeof LOCALES)[number]
export const DEFAULT_LOCALE: Locale = 'zh-CN'

const STORAGE_KEY = 'tide.locale'

function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (LOCALES as readonly string[]).includes(v)
}

/**
 * The language to use: an explicit choice if one was made, otherwise the first
 * of the browser's languages we serve, otherwise the default. A stored choice
 * is a convenience for this browser only; nothing here reaches the server
 * except through the Accept-Language header apiFetch sends.
 */
export function resolveLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (isLocale(stored)) return stored
  } catch {
    // Private windows and blocked site data throw; the browser's own
    // preference is a fine answer when they do.
  }
  for (const tag of navigator.languages ?? [navigator.language]) {
    const lower = tag.toLowerCase()
    if (lower.startsWith('zh')) return 'zh-CN'
    if (lower.startsWith('en')) return 'en'
  }
  return DEFAULT_LOCALE
}

/** The language the UI is in right now; apiFetch asks the server for the same one. */
export function currentLocale(): Locale {
  return isLocale(i18next.language) ? i18next.language : DEFAULT_LOCALE
}

export async function setLocale(l: Locale): Promise<void> {
  if (l === currentLocale()) return
  try {
    localStorage.setItem(STORAGE_KEY, l)
  } catch {
    // A remembered choice is a nicety, not a requirement.
  }
  await i18next.changeLanguage(l)
  document.documentElement.lang = l
  // Reload rather than re-render: plain helpers (status names, thresholds)
  // read the catalogue when they are called, and only the components that
  // subscribe to the hook would re-run. A reload leaves nothing half
  // translated, and switching language is a deliberate, rare act.
  window.location.reload()
}

/**
 * Sets the UI language up before the first render. `force` pins it, which is
 * what tests want: assertions are written in one language and must not depend
 * on the machine's browser settings.
 */
export function initI18n(force?: Locale): typeof i18next {
  const lng = force ?? resolveLocale()
  void i18next.use(initReactI18next).init({
    lng,
    fallbackLng: DEFAULT_LOCALE,
    resources: { 'zh-CN': { translation: zhCN }, en: { translation: en } },
    interpolation: { escapeValue: false }, // React escapes for us
    returnNull: false,
  })
  document.documentElement.lang = lng
  return i18next
}

export { default as i18n } from 'i18next'
