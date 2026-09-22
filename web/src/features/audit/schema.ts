import { i18n } from '../../lib/i18n'
import { z } from 'zod'
import { addIssue, msg } from '../../lib/validation'

const text = z.string().superRefine((v, ctx) => addIssue(ctx, [], [...v.trim()].length > 100 ? msg.tooLong(100) : null))
const datetime = z.string().superRefine((v, ctx) => addIssue(ctx, [], v !== '' && isNaN(new Date(v).getTime()) ? i18n.t('audit.badTime') : null))

export const auditFilterSchema = z
  .object({ jira: text, service: text, env: text, actor: text, action: text, since: datetime, until: datetime })
  .superRefine((v, ctx) => {
    if (!v.since || !v.until) return
    const a = new Date(v.since).getTime()
    const b = new Date(v.until).getTime()
    if (!isNaN(a) && !isNaN(b) && a > b) addIssue(ctx, ['until'], i18n.t('audit.endBeforeStart'))
  })

export type AuditFilterValues = z.infer<typeof auditFilterSchema>

export const auditTextFields = [
  ['jira', 'audit.fJira'],
  ['service', 'audit.fService'],
  ['env', 'audit.fEnv'],
  ['actor', 'audit.fActor'],
  ['action', 'audit.fAction'],
] as const
