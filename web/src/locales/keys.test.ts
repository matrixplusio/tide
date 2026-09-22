import { describe, expect, it } from 'vitest'
import zhCN from './zh-CN'

// A key that does not exist is not a type error: i18next renders the key
// itself, so a typo ships "admin.navUesrs" to whoever is reading the page.
// TypeScript cannot see inside the string, so this does.
//
// Sources are read through Vite rather than node:fs, which keeps the app's
// tsconfig free of node types.
const sources = import.meta.glob('../**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

function flatten(obj: object, prefix = ''): Set<string> {
  const out = new Set<string>()
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') out.add(path)
    else if (v && typeof v === 'object') for (const p of flatten(v, path)) out.add(p)
  }
  return out
}

describe('catalogue keys', () => {
  const defined = flatten(zhCN)

  it('every t() literal exists', () => {
    const missing: string[] = []
    for (const [file, text] of Object.entries(sources)) {
      if (file.includes('/locales/') || /\.test\.tsx?$/.test(file)) continue
      for (const m of text.matchAll(/\bt\(\s*'([^']+)'/g)) {
        const key = m[1] ?? ''
        // A catalogue key always has a namespace; anything else is another t().
        if (key.includes('.') && !defined.has(key)) missing.push(`${key} (${file})`)
      }
    }
    expect(missing).toEqual([])
  })

  it('the numeric HTTP fallbacks are reachable', () => {
    for (const code of [400, 401, 403, 404, 413, 429, 502, 503, 504]) {
      expect(defined.has(`http.${code}`), `http.${code}`).toBe(true)
    }
  })
})
