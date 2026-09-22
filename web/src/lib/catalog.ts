import type { Dimension, Service } from './types'

/**
 * Options for a dimension filter: configured values first, in their order
 * and with their display names, then values only found on services, raw.
 */
export function dimensionOptions(dim: Dimension, services: readonly Service[]): [string, string][] {
  const out: [string, string][] = (dim.values ?? []).map((v) => [v.value, v.name || v.value])
  const known = new Set(out.map(([v]) => v))
  const found = new Set<string>()
  for (const s of services) {
    const v = s.dimensions?.[dim.key]
    if (v && !known.has(v)) found.add(v)
  }
  return [...out, ...[...found].sort().map((v): [string, string] => [v, v])]
}

export function dimensionValueName(dim: Dimension, value: string | undefined): string {
  if (!value) return ''
  return dim.values?.find((v) => v.value === value)?.name || value
}

/**
 * Service name search: every whitespace-separated word must appear in the
 * name (case-insensitive). Plain substrings, so "api" never matches
 * "a-p-i" spread across a name.
 */
export function matchesSearch(name: string, query: string): boolean {
  const n = name.toLowerCase()
  return query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => n.includes(w))
}
