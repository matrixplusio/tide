import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'
import { DEFAULT_LOCALE, initI18n } from '../lib/i18n'

// Components read translations as they render, so the catalogues are loaded
// before the first test mounts anything. The locale is pinned so assertions
// do not depend on the machine's browser language (jsdom reports en-US).
initI18n(DEFAULT_LOCALE)

afterEach(() => cleanup())
