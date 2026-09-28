import type { Service } from './types'

// A release id typed in full goes straight to the release: the id is what a
// person has in front of them (a channel message, a colleague's screen), and
// making them find the list page first is a detour.
const RELEASE_ID = /^REL-\d{8}-\d+$/i

export type SearchHit = { kind: 'service'; name: string; project?: string; domain: string; to: string } | { kind: 'release'; id: string; to: string }

/** What the palette shows for a query: at most a dozen services, the
 *  release id if the query is one. Exported so the ranking is testable
 *  without a DOM. */
export function searchHits(q: string, services: Service[]): SearchHit[] {
  const s = q.trim().toLowerCase()
  if (!s) return []
  if (RELEASE_ID.test(s)) {
    const id = s.toUpperCase()
    return [{ kind: 'release', id, to: `/releases/${encodeURIComponent(id)}` }]
  }
  // Name prefix first, then name contains, then project or domain: a person
  // typing "cart" wants cart-api above some service in the "cart" domain
  // whose name says nothing of the sort.
  const rank = (x: Service) => {
    const n = x.name.toLowerCase()
    if (n.startsWith(s)) return 0
    if (n.includes(s)) return 1
    if ((x.project ?? '').toLowerCase().includes(s) || x.domain.toLowerCase().includes(s)) return 2
    return -1
  }
  return services
    .map((x) => ({ x, r: rank(x) }))
    .filter((e) => e.r >= 0)
    .sort((a, b) => a.r - b.r || a.x.name.localeCompare(b.x.name))
    .slice(0, 12)
    .map(({ x }) => ({ kind: 'service' as const, name: x.name, project: x.project, domain: x.domain, to: `/services/${encodeURIComponent(x.name)}` }))
}
