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
  /** Image tag, version label, or freight name prefix. */
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
    if (parts[0]?.includes(':') && !/^https?:/.test(parts[0])) {
      const first = parts[0]
      const at = first.lastIndexOf(':')
      const repo = first.slice(0, at)
      parts = [repo.slice(repo.lastIndexOf('/') + 1), first.slice(at + 1), ...parts.slice(1)]
    }
    const [service = '', ref = '', sequence, ...rest] = parts
    if (!service || !ref || rest.length > 0 || (sequence !== undefined && !/^\d+$/.test(sequence))) {
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
 * Checks pasted services against the catalog for one environment: each must
 * exist and be deployed there, and together they must be one project and one
 * type (when a batch dimension applies) — the same rules as picking by hand.
 */
export function resolvePasteScope(
  entries: readonly PasteEntry[],
  services: readonly Service[],
  env: string,
  dim: Dimension | undefined,
): { project: string; type: string; problems: PasteProblem[] } {
  const problems: PasteProblem[] = []
  const found: Service[] = []
  for (const e of entries) {
    const s = services.find((x) => x.name === e.service)
    if (!s) problems.push({ line: e.line, text: e.service, msg: i18n.t('releases.noSuchService', { service: e.service }) })
    else if (!s.envs[env]) problems.push({ line: e.line, text: e.service, msg: i18n.t('releases.notInEnv', { service: e.service, env }) })
    else if (!s.project) problems.push({ line: e.line, text: e.service, msg: i18n.t('releases.noProject', { service: e.service }) })
    else found.push(s)
  }
  const projects = [...new Set(found.map((s) => s.project ?? ''))]
  const types = dim ? [...new Set(found.map((s) => s.dimensions?.[dim.key] ?? ''))] : ['']
  if (projects.length > 1) problems.push({ line: 0, text: '', msg: i18n.t('releases.manyProjects', { list: projects.join(i18n.t('scope.listSeparator')) }) })
  if (types.length > 1) problems.push({ line: 0, text: '', msg: i18n.t('releases.manyTypes', { what: dim?.name ?? i18n.t('releases.kindWord'), list: types.map((t) => dim?.values?.find((v) => v.value === t)?.name || t || i18n.t('releases.unset')).join(i18n.t('scope.listSeparator')) }) })
  return { project: projects[0] ?? '', type: types[0] ?? '', problems }
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
