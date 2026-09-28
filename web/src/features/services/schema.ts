import { i18n } from '../../lib/i18n'
import { z } from 'zod'
import { addIssue, normalizeJira, zJira, zOptionalJira, zOptionalMinText, zOptionalText, zRequiredText } from '../../lib/validation'

export interface RequiredFields {
  jira: boolean
  reason: boolean
}

/** Which release fields the release policy makes mandatory in env. */
export function requiredFields(app: { jiraRequired?: string[] | null; reasonRequired?: string[] | null } | undefined, env: string): RequiredFields {
  return { jira: (app?.jiraRequired ?? []).includes(env), reason: (app?.reasonRequired ?? []).includes(env) }
}

/** Optional one-line summary shown in the release list and notifications. */
const zTitle = () => zOptionalText(200)

const zReason = (required: boolean) => (required ? zRequiredText(i18n.t('services.reason'), 2000, 4) : zOptionalMinText(i18n.t('services.reason'), 2000, 4))

export const promoteSchema = (req: RequiredFields) =>
  z.object({
    freight: z.string().superRefine((v, ctx) => addIssue(ctx, [], v === '' ? i18n.t('services.pickFreight') : null)),
    jiraTicket: req.jira ? zJira() : zOptionalJira(),
    title: zTitle(),
    reason: zReason(req.reason),
    // Send unsynced git config along with the image (it goes out anyway; this makes it deliberate).
    withConfig: z.boolean(),
  })
export type PromoteValues = z.infer<ReturnType<typeof promoteSchema>>

export { normalizeJira }

/** Server field paths for POST /releases → promote form fields. */
export function mapReleaseField(field: string): string | null {
  if (field === 'jiraTicket' || field === 'reason') return field
  if (/^items(\.\d+)?(\.freight)?$/.test(field)) return 'freight'
  return null
}

export const restartSchema = (req: RequiredFields) =>
  z.object({
    jiraTicket: req.jira ? zJira() : zOptionalJira(),
    title: zTitle(),
    reason: zReason(req.reason),
  })
export type RestartValues = z.infer<ReturnType<typeof restartSchema>>

export const syncSchema = (req: RequiredFields, deletes: number) =>
  z.object({
    jiraTicket: req.jira ? zJira() : zOptionalJira(),
    title: zTitle(),
    reason: zReason(req.reason),
    restart: z.boolean(),
    prune: z.boolean().refine((v) => deletes === 0 || v, { message: i18n.t('services.pruneConfirm', { count: deletes }) }),
  })
export type SyncValues = z.infer<ReturnType<typeof syncSchema>>

/** Server field paths for POST /releases → sync form fields. */
export function mapSyncField(field: string): string | null {
  if (field === 'jiraTicket' || field === 'reason') return field
  if (/^items\.\d+\.prune$/.test(field)) return 'prune'
  return null
}

export const MAX_REPLICAS = 50

export const scaleSchema = (req: RequiredFields) =>
  z.object({
    // A text field, not a number input: an empty number input reads as NaN and
    // a browser will happily let "1e3" through. Parsed here so the message says
    // what is wrong rather than the field silently holding nothing.
    replicas: z.string().superRefine((v, ctx) => {
      const s = v.trim()
      if (s === '') return addIssue(ctx, [], i18n.t('forms.replicasRequired'))
      if (!/^\d+$/.test(s)) return addIssue(ctx, [], i18n.t('forms.replicasWhole'))
      const n = Number(s)
      if (n > MAX_REPLICAS) return addIssue(ctx, [], i18n.t('forms.replicasMax', { max: MAX_REPLICAS }))
      return undefined
    }),
    jiraTicket: req.jira ? zJira() : zOptionalJira(),
    title: zTitle(),
    reason: zReason(req.reason),
  })
export type ScaleValues = z.infer<ReturnType<typeof scaleSchema>>

/** Server field paths for POST /releases → scale form fields. */
export function mapScaleField(field: string): string | null {
  if (field === 'jiraTicket' || field === 'reason') return field
  if (/^items\.\d+\.replicas$/.test(field)) return 'replicas'
  return null
}

/** Server field paths for POST /releases → restart form fields. */
export function mapRestartField(field: string): string | null {
  return field === 'jiraTicket' || field === 'reason' ? field : null
}
