import { i18n } from './i18n'
// Absolute time: releases are lined up against other events after the fact,
// "2 hours ago" means nothing then.
export function fmtTime(s?: string | null, withSeconds = false): string {
  if (!s) return '—'
  const d = new Date(s)
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  const now = new Date()
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}${withSeconds ? ':' + p(d.getSeconds()) : ''}`
  if (d.toDateString() === now.toDateString()) return hm
  const md = `${p(d.getMonth() + 1)}-${p(d.getDate())}`
  return d.getFullYear() === now.getFullYear() ? `${md} ${hm}` : `${d.getFullYear()}-${md} ${hm}`
}

export function fmtDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m${s % 60 ? ` ${s % 60}s` : ''}`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h${m % 60 ? ` ${m % 60}m` : ''}`
  return `${Math.floor(h / 24)}d`
}

export function sinceMs(s?: string | null): number | null {
  if (!s) return null
  const t = new Date(s).getTime()
  return isNaN(t) ? null : t
}

// 20260916151427-46b7619f-0022 → 0916-46b7
export function shortTag(tag?: string): string {
  if (!tag) return ''
  const m = tag.match(/^\d{4}(\d{4})\d{6}-([0-9a-f]{4})/)
  return m ? `${m[1]}-${m[2]}` : tag.length > 14 ? tag.slice(0, 14) + '…' : tag
}

export function shortDigest(d?: string, n = 12): string {
  if (!d) return ''
  return d.startsWith('sha256:') ? 'sha256:' + d.slice(7, 7 + n) : d.slice(0, n)
}

// datetime-local value ("2026-09-17T10:30") ⇄ RFC3339
export function localInputToRfc3339(v: string): string {
  if (!v) return ''
  const d = new Date(v)
  return isNaN(d.getTime()) ? '' : d.toISOString()
}

export function rfc3339ToLocalInput(v: string | null | undefined): string {
  if (!v) return ''
  const d = new Date(v)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

// Only http(s) URLs may become links.
export function safeHttpUrl(u?: string | null): string | undefined {
  if (!u) return undefined
  try {
    const x = new URL(u)
    return x.protocol === 'http:' || x.protocol === 'https:' ? x.href : undefined
  } catch {
    return undefined
  }
}

// Login "return" only accepts in-site relative paths.
export function safeReturnPath(p?: string | null): string {
  if (!p || !p.startsWith('/') || p.startsWith('//') || p.startsWith('/\\')) return '/'
  return p
}

/** A short human label for a User-Agent, e.g. "Chrome 152 · macOS"; the raw string otherwise. */
export function describeUserAgent(ua?: string): string {
  if (!ua) return i18n.t('ua.unknown')
  const browser =
    ua.match(/Edg\/(\d+)/)?.[1] !== undefined
      ? `Edge ${ua.match(/Edg\/(\d+)/)?.[1]}`
      : ua.match(/Firefox\/(\d+)/)
        ? `Firefox ${ua.match(/Firefox\/(\d+)/)?.[1]}`
        : ua.match(/Chrome\/(\d+)/)
          ? `Chrome ${ua.match(/Chrome\/(\d+)/)?.[1]}`
          : ua.match(/Version\/(\d+).*Safari\//)
            ? `Safari ${ua.match(/Version\/(\d+)/)?.[1]}`
            : null
  const os = /iPhone|iPad/.test(ua)
    ? 'iOS'
    : /Android/.test(ua)
      ? 'Android'
      : /Mac OS X|Macintosh/.test(ua)
        ? 'macOS'
        : /Windows/.test(ua)
          ? 'Windows'
          : /Linux/.test(ua)
            ? 'Linux'
            : null
  if (browser) return os ? `${browser} · ${os}` : browser
  const tool = ua.match(/^([A-Za-z][\w.-]*)\/([\w.]+)/)
  return tool ? `${tool[1]} ${tool[2]}` : ua
}

/** Local calendar day ("2026-09-18") → RFC3339 at its start or end. */
export function dayBound(day: string, end: boolean): string | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day)
  if (!m) return undefined
  const d = end ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), 23, 59, 59, 999) : new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return isNaN(d.getTime()) ? undefined : d.toISOString()
}
