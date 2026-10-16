import { i18n } from '../../lib/i18n'
import { z } from 'zod'
import { addIssue } from '../../lib/validation'
import { requiredFields, type RequiredFields } from '../services/schema'
import { zJira, zOptionalJira, zOptionalMinText, zOptionalText, zRequiredText } from '../../lib/validation'
import type { Dimension, Service } from '../../lib/types'

export type BatchKind = 'image' | 'restart' | 'sync'

export interface BatchRow {
  service: string
  selected: boolean
  freight: string
  sequence: string
}

export const batchSchema = (req: RequiredFields, kind: BatchKind) =>
  z
    .object({
      rows: z.array(z.object({ service: z.string(), selected: z.boolean(), freight: z.string(), sequence: z.string() })),
      title: zOptionalText(200),
      jiraTicket: req.jira ? zJira() : zOptionalJira(),
      reason: req.reason ? zRequiredText(i18n.t('releases.reason'), 2000, 4) : zOptionalMinText(i18n.t('releases.reason'), 2000, 4),
      // Config sync only: allow deletions / restart after the sync, for every selected service.
      prune: z.boolean(),
      restart: z.boolean(),
      // Upgrade only: send unsynced git config along with the images.
      withConfig: z.boolean(),
    })
    .superRefine((v, ctx) => {
      const picked = v.rows.filter((r) => r.selected)
      if (picked.length === 0) addIssue(ctx, ['rows'], i18n.t('releases.pickOne'))
      if (picked.length > 50) addIssue(ctx, ['rows'], i18n.t('releases.max50'))
      v.rows.forEach((r, i) => {
        if (!r.selected) return
        if (kind === 'image' && r.freight === '') addIssue(ctx, ['rows', i, 'freight'], i18n.t('releases.pickFreightFor', { service: r.service }))
        const n = Number(r.sequence.trim())
        if (!/^\d+$/.test(r.sequence.trim()) || n < 1 || n > 100) addIssue(ctx, ['rows', i, 'sequence'], i18n.t('releases.sequenceRange'))
      })
    })
export type BatchValues = z.infer<ReturnType<typeof batchSchema>>

export { requiredFields }

/** Services a batch may target: in the project, of the type (when a batch dimension applies), deployed in env. */
export function batchCandidates(services: readonly Service[], env: string, project: string, dim: Dimension | undefined, type: string): Service[] {
  return services
    .filter((s) => (s.project ?? '') === project && project !== '' && !!s.envs[env] && (!dim || s.dimensions?.[dim.key] === type))
    .sort((a, b) => a.domain.localeCompare(b.domain) || a.name.localeCompare(b.name))
}

/**
 * Server field paths name items by their position among selected rows
 * ("items.1.freight"); map them back to the row they came from.
 */
export function mapBatchField(rows: readonly BatchRow[], field: string): string | null {
  if (field === 'jiraTicket' || field === 'reason') return field
  if (/^items\.\d+\.prune$/.test(field)) return 'prune'
  const m = field.match(/^items\.(\d+)\.(freight|service|kind|sequence)$/)
  if (!m) return null
  const selected = rows.map((r, i) => (r.selected ? i : -1)).filter((i) => i >= 0)
  const row = selected[Number(m[1])]
  if (row === undefined) return null
  return m[2] === 'freight' || m[2] === 'sequence' ? `rows.${row}.${m[2]}` : null
}

export interface PasteEntry {
  line: number
  service: string
  /** Image tag, version label, or freight name prefix; empty when only the service was given. */
  ref: string
  sequence?: string
}

export interface PasteProblem {
  line: number
  text: string
  msg: string
}

/**
 * Parses pasted lines into service + tag (+ optional sequence). Accepted per line:
 *   `svc tag`, `svc tag 2`, `svc,tag`, `svc:tag`, `svc=tag`, or an image reference
 *   `registry/path/svc:tag`. Blank lines and `#` comments are ignored.
 * A line may also be just the service, or an image without a tag: that picks
 * the service and leaves its version to the person — only a tag that was
 * pasted ever fills one in.
 */
export function parsePaste(text: string): { entries: PasteEntry[]; problems: PasteProblem[] } {
  const entries: PasteEntry[] = []
  const problems: PasteProblem[] = []
  text.split(/\r?\n/).forEach((raw, i) => {
    const line = i + 1
    const t = raw.replace(/#.*$/, '').trim()
    if (!t) return
    let parts = t.split(/[\s,;=]+/).filter(Boolean)
    // image reference or svc:tag in the first field
    const first = parts[0] ?? ''
    const at = first.lastIndexOf(':')
    // A colon followed by a slash is a registry port, not a tag.
    if (at >= 0 && !/^https?:/.test(first) && !first.slice(at + 1).includes('/')) {
      const repo = first.slice(0, at)
      parts = [repo.slice(repo.lastIndexOf('/') + 1), first.slice(at + 1), ...parts.slice(1)]
    } else if (parts.length === 1 && first.includes('/')) {
      parts = [first.slice(first.lastIndexOf('/') + 1)]
    }
    const [service = '', ref = '', sequence, ...rest] = parts
    if (!service || (parts.length > 1 && !ref) || rest.length > 0 || (sequence !== undefined && !/^\d+$/.test(sequence))) {
      problems.push({ line, text: raw.trim(), msg: i18n.t('releases.pasteFormat') })
      return
    }
    if (entries.some((e) => e.service === service)) {
      problems.push({ line, text: raw.trim(), msg: i18n.t('releases.duplicate', { service }) })
      return
    }
    entries.push({ line, service, ref, sequence })
  })
  return { entries, problems }
}

/**
 * Finds the service a pasted name means. The full name wins. Otherwise the
 * name is taken as a short name — the service name without its project
 * prefix, which is how people and image paths name globex-x — among the
 * services deployed to env. A short name several projects share is settled
 * by the project picked on the page, and never guessed.
 */
export function findPastedService(
  name: string,
  services: readonly Service[],
  env: string,
  project: string,
): { service?: Service; ambiguous?: string[] } {
  const exact = services.find((x) => x.name === name)
  if (exact) return { service: exact }
  const byShort = services.filter((x) => x.short && x.short !== x.name && x.short === name && x.envs[env])
  if (byShort.length === 1) return { service: byShort[0] }
  const inProject = byShort.filter((x) => x.project === project)
  if (inProject.length === 1) return { service: inProject[0] }
  if (byShort.length > 1) return { ambiguous: byShort.map((x) => x.name) }
  return {}
}

/**
 * Checks pasted services against the catalog for one environment: each must
 * exist and be deployed there, and together they must be one project and one
 * type (when a batch dimension applies) — the same rules as picking by hand.
 * Entries come back under the full service name; renamed lists the short
 * names that were read as one, so the person sees what was matched.
 */
export function resolvePasteScope(
  entries: readonly PasteEntry[],
  services: readonly Service[],
  env: string,
  dim: Dimension | undefined,
  project = '',
): { project: string; type: string; problems: PasteProblem[]; entries: PasteEntry[]; renamed: { from: string; to: string }[] } {
  const problems: PasteProblem[] = []
  const found: Service[] = []
  const resolved: PasteEntry[] = []
  const renamed: { from: string; to: string }[] = []
  // A short name several projects share is settled by the rest of the paste
  // first: a batch is one project, so if every name that is not in doubt
  // points at one project, that is the one meant. The project on the page
  // only counts after that — it may be just the default nobody chose.
  const sure = new Set(
    entries.map((e) => findPastedService(e.service, services, env, '').service?.project).filter((p): p is string => !!p),
  )
  const prefer = sure.size === 1 ? ([...sure][0] ?? project) : project
  for (const e of entries) {
    const { service: s, ambiguous } = findPastedService(e.service, services, env, prefer)
    if (ambiguous) {
      problems.push({ line: e.line, text: e.service, msg: i18n.t('releases.ambiguousService', { service: e.service, list: ambiguous.join(i18n.t('scope.listSeparator')) }) })
      continue
    }
    if (!s) problems.push({ line: e.line, text: e.service, msg: i18n.t('releases.noSuchService', { service: e.service }) })
    else if (!s.envs[env]) problems.push({ line: e.line, text: e.service, msg: i18n.t('releases.notInEnv', { service: e.service, env }) })
    else if (!s.project) problems.push({ line: e.line, text: e.service, msg: i18n.t('releases.noProject', { service: e.service }) })
    else {
      found.push(s)
      resolved.push({ ...e, service: s.name })
      if (s.name !== e.service) renamed.push({ from: e.service, to: s.name })
    }
  }
  const projects = [...new Set(found.map((s) => s.project ?? ''))]
  const types = dim ? [...new Set(found.map((s) => s.dimensions?.[dim.key] ?? ''))] : ['']
  if (projects.length > 1) problems.push({ line: 0, text: '', msg: i18n.t('releases.manyProjects', { list: projects.join(i18n.t('scope.listSeparator')) }) })
  if (types.length > 1) problems.push({ line: 0, text: '', msg: i18n.t('releases.manyTypes', { what: dim?.name ?? i18n.t('releases.kindWord'), list: types.map((t) => dim?.values?.find((v) => v.value === t)?.name || t || i18n.t('releases.unset')).join(i18n.t('scope.listSeparator')) }) })
  return { project: projects[0] ?? '', type: types[0] ?? '', problems, entries: resolved, renamed }
}

/** Finds the freight a pasted ref means for one service: by tag, version label, or freight name prefix. */
export function matchPastedFreight(
  items: readonly { freight: string; tag: string; version?: string; available: boolean; current: boolean }[],
  service: string,
  ref: string,
  env: string,
): { freight?: string; msg?: string } {
  const c = items.find((x) => x.tag === ref || (!!x.version && x.version === ref) || (ref.length >= 7 && x.freight.startsWith(ref)))
  if (!c) return { msg: i18n.t('releases.noSuchFreight', { service, ref }) }
  if (c.current) return { msg: i18n.t('releases.alreadyRunning', { service, ref, env }) }
  if (!c.available) return { msg: i18n.t('releases.notPromotable', { service, ref, env }) }
  return { freight: c.freight }
}

/**
 * Whether a row stays on screen after a paste. The list is the whole project,
 * often a hundred services, and checking the four that were pasted meant
 * scrolling past the rest; so once a paste has been imported only its
 * services are shown, until the person asks for all of them again.
 * Selection is untouched either way: this only hides rows.
 */
export function shownAfterPaste(imported: Record<string, unknown> | null, onlyPasted: boolean, service: string): boolean {
  if (!imported || !onlyPasted) return true
  return Object.prototype.hasOwnProperty.call(imported, service)
}

/** The environment and kind the batch page was last set to, kept per browser. */
export interface BatchScope {
  env?: string
  kind?: BatchKind
}

const SCOPE_KEY = 'tide.batch.scope'
const LAST_KEY = 'tide.batch.last'

const isKind = (v: unknown): v is BatchKind => v === 'image' || v === 'restart' || v === 'sync'

function readJSON(key: string): Record<string, unknown> {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(key) ?? '{}')
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
  } catch {
    // Private windows and blocked site data throw; nothing remembered then.
    return {}
  }
}

function writeJSON(key: string, v: unknown): void {
  try {
    localStorage.setItem(key, JSON.stringify(v))
  } catch {
    // Remembering is a convenience, never a requirement.
  }
}

/**
 * The environment comes first and is what the next batch most often shares
 * with the last, so it is restored by itself; project, type and services
 * are only brought back when someone asks to reuse the last batch.
 */
export function loadBatchScope(): BatchScope {
  const o = readJSON(SCOPE_KEY)
  return { env: typeof o.env === 'string' ? o.env : undefined, kind: isKind(o.kind) ? o.kind : undefined }
}

export function saveBatchScope(scope: BatchScope): void {
  writeJSON(SCOPE_KEY, scope)
}

/** The last batch sent to one environment, as far as reusing it needs. */
export interface LastBatch {
  at: string
  kind: BatchKind
  project: string
  type: string
  services: { service: string; sequence: string }[]
}

/** The last batch sent to env from this browser, or undefined. */
export function loadLastBatch(env: string): LastBatch | undefined {
  const v = readJSON(LAST_KEY)[env]
  if (!v || typeof v !== 'object') return undefined
  const o = v as Record<string, unknown>
  if (typeof o.at !== 'string' || !isKind(o.kind) || typeof o.project !== 'string' || typeof o.type !== 'string' || !Array.isArray(o.services)) return undefined
  const services = o.services
    .filter((x): x is { service: string; sequence: string } => !!x && typeof x.service === 'string' && typeof x.sequence === 'string' && /^\d+$/.test(x.sequence))
    .slice(0, 200)
  return services.length ? { at: o.at, kind: o.kind, project: o.project, type: o.type, services } : undefined
}

export function saveLastBatch(env: string, batch: LastBatch): void {
  writeJSON(LAST_KEY, { ...readJSON(LAST_KEY), [env]: batch })
}

/**
 * What reusing the last batch selects now: its services that are still
 * deployed to env in the same project, with their order but no version —
 * the version sent last time is most likely the one running now. The rest
 * are named, so nobody wonders where a service went.
 */
export function reuseLastBatch(
  last: LastBatch,
  services: readonly Service[],
  env: string,
  allowed: (s: Service) => boolean,
): { picked: Record<string, { freight: string; sequence: string }>; missing: string[] } {
  const picked: Record<string, { freight: string; sequence: string }> = {}
  const missing: string[] = []
  for (const x of last.services) {
    const s = services.find((y) => y.name === x.service)
    if (s && s.envs[env] && (s.project ?? '') === last.project && allowed(s)) picked[s.name] = { freight: '', sequence: x.sequence }
    else missing.push(x.service)
  }
  return { picked, missing }
}
