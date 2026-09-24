import { i18n } from '../../lib/i18n'
import { z } from 'zod'
import {
  addIssue,
  codePointLength,
  ENV_RE,
  GROUP_RE,
  integerInRange,
  JIRA_PROJECT_RE,
  msg,
  refineNewPassword,
  ROLE_ID_RE,
  validateHttpUrl,
  validateUsername,
  zOptionalText,
} from '../../lib/validation'
import { CI_MODES, MASK } from './types'

// Mirrors the server rules for the admin console (docs/api.md, docs/designs/admin-console.md §5–9).

const requiredText = (what: string, max: number) =>
  z.string().superRefine((v, ctx) => {
    if (v.trim() === '') return addIssue(ctx, [], msg.required(what))
    if (codePointLength(v.trim()) > max) addIssue(ctx, [], msg.tooLong(max))
  })

const secret = (what: string, requiredValue: boolean) =>
  z.string().superRefine((v, ctx) => {
    if (v === MASK) return
    if (requiredValue && v.trim() === '') return addIssue(ctx, [], msg.required(what))
    if (v.length > 4096) addIssue(ctx, [], msg.tooLong(4096))
  })

const baseUrl = () => z.string().superRefine((v, ctx) => addIssue(ctx, [], validateHttpUrl(v, { base: true })))
const optionalUrl = (opts: { base?: boolean } = {}) => z.string().superRefine((v, ctx) => addIssue(ctx, [], v.trim() === '' ? null : validateHttpUrl(v, opts)))
const intField = (min: number, max: number) => z.string().superRefine((v, ctx) => addIssue(ctx, [], integerInRange(v, min, max)))

export const envNameMsg = () => i18n.t('av.envName')

/** Environment selectors: `*`, `tier:<Tier>` or environment names; non-empty, `*` stands alone. */
export function envSelectorsIssue(envs: string[]): string | null {
  if (envs.length === 0) return i18n.t('av.pickEnv')
  if (envs.includes('*') && envs.length > 1) return i18n.t('av.allEnvsAlone')
  return null
}
export const zEnvSelectors = () => z.array(z.string()).superRefine((v, ctx) => addIssue(ctx, [], envSelectorsIssue(v)))

/**
 * Server field path → form field. Multi-value controls (env selectors, event
 * and channel checkboxes) are one control in the form, so "rules.0.envs.1"
 * lands on "rules.0.envs"; scalar string lists are {value} rows in the form.
 */
export function mapListField(field: string, opts: { controls?: string[]; scalarLists?: string[] } = {}): string {
  const m = field.match(/^(.*?)\.?([A-Za-z]+)\.(\d+)$/)
  if (m) {
    const [, prefix = '', name = '', index = ''] = m
    const head = prefix ? `${prefix}.${name}` : name
    if (opts.controls?.includes(name)) return head
    if (opts.scalarLists?.includes(name)) return `${head}.${index}.value`
  }
  return field
}

// ---- users, groups ------------------------------------------------------------

export const createUserSchema = z
  .object({ username: z.string(), name: zOptionalText(64), password: z.string(), confirmPassword: z.string() })
  .superRefine((v, ctx) => {
    addIssue(ctx, ['username'], validateUsername(v.username))
    refineNewPassword(ctx, { password: v.password, confirm: v.confirmPassword, username: v.username }, { password: 'password', confirm: 'confirmPassword' })
  })
export type CreateUserValues = z.infer<typeof createUserSchema>

export const resetPasswordSchema = (username: string) =>
  z.object({ newPassword: z.string(), confirmPassword: z.string() }).superRefine((v, ctx) => {
    refineNewPassword(ctx, { password: v.newPassword, confirm: v.confirmPassword, username }, { password: 'newPassword', confirm: 'confirmPassword' })
  })
export type ResetPasswordValues = { newPassword: string; confirmPassword: string }

export const nameSchema = z.object({ name: requiredText(i18n.t('av.name'), 64) })
export type NameValues = z.infer<typeof nameSchema>

export const groupNameMsg = () => i18n.t('av.groupName')
export const validateGroupName = (v: string): string | null => (v.trim() === '' ? i18n.t('av.groupRequired') : GROUP_RE.test(v.trim()) ? null : groupNameMsg())

export const createGroupSchema = z.object({
  name: z.string().superRefine((v, ctx) => addIssue(ctx, [], validateGroupName(v))),
  description: zOptionalText(200),
})
export type CreateGroupValues = z.infer<typeof createGroupSchema>

export const groupDescriptionSchema = z.object({ description: zOptionalText(200) })
export type GroupDescriptionValues = z.infer<typeof groupDescriptionSchema>

// ---- roles & bindings ---------------------------------------------------------

export const roleIdMsg = () => i18n.t('av.roleId')

export const roleSchema = (creating: boolean) =>
  z
    .object({ id: z.string(), name: requiredText(i18n.t('av.nameField'), 32), description: zOptionalText(200), permissions: z.array(z.string()) })
    .superRefine((v, ctx) => {
      if (creating) addIssue(ctx, ['id'], v.id.trim() === '' ? i18n.t('av.roleIdRequired') : ROLE_ID_RE.test(v.id.trim()) ? null : roleIdMsg())
      addIssue(ctx, ['permissions'], v.permissions.length === 0 ? i18n.t('av.pickPermission') : null)
    })
export type RoleValues = { id: string; name: string; description: string; permissions: string[] }

export type SubjectKind = 'user' | 'group' | 'all'

// A binding may name an IdP group, which need not follow the local group rule.
export const validateGroupSubject = (v: string): string | null => {
  const s = v.trim()
  if (s === '') return i18n.t('av.groupRequired')
  if (/\s/.test(s) || [...s].some((ch) => (ch.codePointAt(0) ?? 0) < 32 || ch.codePointAt(0) === 127)) return i18n.t('av.groupNoSpace')
  return codePointLength(s) > 255 ? msg.tooLong(255) : null
}

export const bindingSchema = (roleId: string) =>
  z
    .object({ kind: z.enum(['user', 'group', 'all']), userSub: z.string(), group: z.string(), envs: zEnvSelectors(), projects: z.array(z.string()), types: z.array(z.string()) })
    .superRefine((v, ctx) => {
      if (v.kind === 'user') addIssue(ctx, ['userSub'], v.userSub === '' ? i18n.t('av.pickUser') : null)
      if (v.kind === 'group') addIssue(ctx, ['group'], validateGroupSubject(v.group))
      if (v.kind === 'all' && roleId === 'admin') addIssue(ctx, ['kind'], i18n.t('av.adminNotEveryone'))
    })
export type BindingValues = { kind: SubjectKind; userSub: string; group: string; envs: string[]; projects: string[]; types: string[] }

export function bindingSubject(v: BindingValues): string {
  if (v.kind === 'all') return '*'
  if (v.kind === 'group') return `group:${v.group.trim()}`
  return `user:${v.userSub}`
}

export const bindingScopeSchema = z.object({ envs: zEnvSelectors(), projects: z.array(z.string()), types: z.array(z.string()) })
export type BindingScopeValues = z.infer<typeof bindingScopeSchema>

// ---- environments -------------------------------------------------------------

export const TIER_VALUES = ['development', 'testing', 'staging', 'production'] as const

export const environmentsSchema = z
  .object({
    items: z.array(
      z.object({
        name: z.string(),
        displayName: zOptionalText(32),
        tier: z.enum(TIER_VALUES),
        description: zOptionalText(200),
        upstream: z.string(),
        promotesFrom: z.string(),
        ci: z.enum(CI_MODES),
      }),
    ),
  })
  .superRefine((v, ctx) => {
    const seen = new Set<string>()
    v.items.forEach((it, i) => {
      const n = it.name.trim()
      if (n === '') addIssue(ctx, ['items', i, 'name'], i18n.t('av.nameRequired'))
      else if (!ENV_RE.test(n)) addIssue(ctx, ['items', i, 'name'], envNameMsg())
      else if (seen.has(n)) addIssue(ctx, ['items', i, 'name'], i18n.t('av.envDup', { name: n }))
      if (it.promotesFrom && !v.items.slice(0, i).some((x) => x.name.trim() === it.promotesFrom)) addIssue(ctx, ['items', i, 'promotesFrom'], i18n.t('av.promotesFromOrder'))
      seen.add(n)
    })
  })
export type EnvironmentsValues = z.infer<typeof environmentsSchema>

// ---- catalog -------------------------------------------------------------------

// Kubernetes label key and value rules, as on the server.
export const LABEL_KEY_RE = /^([a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?\/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$/
export const LABEL_VALUE_RE = /^[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$/
const DIMENSION_KEY_RE = /^[a-z][a-z0-9-]{0,31}$/
const labelKeyMsg = () => i18n.t('av.labelKey')

const optionalLabelKey = () => z.string().superRefine((v, ctx) => addIssue(ctx, [], v.trim() !== '' && !LABEL_KEY_RE.test(v.trim()) ? labelKeyMsg() : null))

export const catalogSchema = z
  .object({
    serviceLabel: optionalLabelKey(),
    envLabel: optionalLabelKey(),
    domainLabel: optionalLabelKey(),
    projectLabel: optionalLabelKey(),
    batchDimension: z.string(),
    dimensions: z.array(
      z.object({
        key: z.string(),
        name: requiredText(i18n.t('av.nameField'), 16),
        label: z.string(),
        values: z.array(z.object({ value: z.string(), name: zOptionalText(16) })),
      }),
    ),
  })
  .superRefine((v, ctx) => {
    if (v.dimensions.length > 5) addIssue(ctx, ['dimensions'], i18n.t('av.maxDimensions'))
    if (v.batchDimension !== '') {
      const d = v.dimensions.find((x) => x.key.trim() === v.batchDimension)
      if (!d) addIssue(ctx, ['batchDimension'], i18n.t('av.dimMissing', { key: v.batchDimension }))
      else if (d.values.length < 2) addIssue(ctx, ['batchDimension'], i18n.t('av.batchDimNeedsTwo'))
    }
    const keys = new Set<string>()
    const labels = new Set<string>()
    v.dimensions.forEach((d, i) => {
      const k = d.key.trim()
      if (k === '') addIssue(ctx, ['dimensions', i, 'key'], i18n.t('av.keyRequired'))
      else if (!DIMENSION_KEY_RE.test(k) || k === 'q' || k === 'domain')
        addIssue(ctx, ['dimensions', i, 'key'], i18n.t('av.keyFormat'))
      else if (keys.has(k)) addIssue(ctx, ['dimensions', i, 'key'], i18n.t('av.keyDup', { key: k }))
      keys.add(k)
      const l = d.label.trim()
      if (l === '') addIssue(ctx, ['dimensions', i, 'label'], i18n.t('av.labelRequired'))
      else if (!LABEL_KEY_RE.test(l)) addIssue(ctx, ['dimensions', i, 'label'], labelKeyMsg())
      else if (labels.has(l)) addIssue(ctx, ['dimensions', i, 'label'], i18n.t('av.labelTaken', { label: l }))
      labels.add(l)
      if (d.values.length > 50) addIssue(ctx, ['dimensions', i, 'values'], i18n.t('av.maxValues'))
      const seen = new Set<string>()
      d.values.forEach((val, j) => {
        const x = val.value.trim()
        if (!LABEL_VALUE_RE.test(x)) addIssue(ctx, ['dimensions', i, 'values', j, 'value'], i18n.t('av.labelValue'))
        else if (seen.has(x)) addIssue(ctx, ['dimensions', i, 'values', j, 'value'], i18n.t('av.valueDup', { value: x }))
        seen.add(x)
      })
    })
  })
export type CatalogValues = z.infer<typeof catalogSchema>

// ---- pipeline repository ------------------------------------------------------

export const pipelineRepoSchema = z.object({
  provider: z.enum(['gitlab', 'gitea']),
  baseUrl: baseUrl(),
  project: z.string().trim().min(1, i18n.t('kargogen.projectRequired')).max(256),
  branch: zOptionalText(256),
  pathPrefix: zOptionalText(256),
  token: secret(i18n.t('kargogen.token'), true),
  bareDomain: z.boolean(),
  projectNamePrefix: z.string(),
  imageStrategy: z.enum(['Lexical', 'SemVer', 'NewestBuild', 'Digest']),
  tagPattern: zOptionalText(256),
})
export type PipelineRepoValues = z.infer<typeof pipelineRepoSchema>

// ---- upstreams ----------------------------------------------------------------

export const upstreamItemSchema = z.object({
  name: z.string(),
  kargoUrl: baseUrl(),
  kargoToken: secret('Kargo token', true),
  argocdUrl: baseUrl(),
  argocdToken: secret('Argo CD token', true),
  registryUrl: optionalUrl(),
  registryUser: zOptionalText(256),
  registryToken: secret(i18n.t('av.registryPassword'), false),
  // The server is the authority on what a usable date is; the browser's date
  // input already refuses the shapes people get wrong by hand.
  kargoExpires: zOptionalText(10),
  argocdExpires: zOptionalText(10),
  registryExpires: zOptionalText(10),
  insecureTls: z.boolean(),
  grafanaUrl: optionalUrl(),
})

export const upstreamsSchema = z.object({ items: z.array(upstreamItemSchema) }).superRefine((v, ctx) => {
  const names = new Map<string, number>()
  v.items.forEach((it, i) => {
    const name = it.name.trim()
    if (name === '') addIssue(ctx, ['items', i, 'name'], i18n.t('av.nameRequired'))
    else if (!ENV_RE.test(name)) addIssue(ctx, ['items', i, 'name'], i18n.t('av.upstreamNameFormat'))
    else if (names.has(name)) addIssue(ctx, ['items', i, 'name'], i18n.t('av.upstreamNameTaken', { name, index: (names.get(name) ?? 0) + 1 }))
    else names.set(name, i)
    if (it.registryToken !== '' && it.registryToken !== MASK && it.registryUser.trim() === '') addIssue(ctx, ['items', i, 'registryUser'], i18n.t('av.registryUserNeeded'))
  })
})
export type UpstreamsValues = z.infer<typeof upstreamsSchema>
export type UpstreamItemValues = z.infer<typeof upstreamItemSchema>

export function mapUpstreamsField(field: string): string | null {
  const m = field.match(/^items\.(\d+)\.([A-Za-z]+)(\.\d+)?$/)
  return m ? `items.${m[1]}.${m[2]}` : null
}

// ---- SSO ----------------------------------------------------------------------

export const SSO_CALLBACK_PATH = '/api/v1/auth/sso/callback'

export const ssoSchema = z.object({
  issuer: baseUrl(),
  clientId: requiredText('Client ID', 256),
  clientSecret: secret('Client secret', true),
  groupsClaim: zOptionalText(64),
  redirectUrl: z.string().superRefine((v, ctx) => addIssue(ctx, [], validateHttpUrl(v, { base: true, suffix: SSO_CALLBACK_PATH }))),
})
export type SsoValues = z.infer<typeof ssoSchema>

// ---- security (§7) ------------------------------------------------------------

export const securitySchema = z
  .object({
    sessionTtlMinutes: intField(5, 1440),
    loginWindowMinutes: intField(5, 1440),
    captchaAfterUserFailures: intField(1, 20),
    captchaAfterIpFailures: intField(1, 100),
    lockAfterUserFailures: intField(2, 100),
    lockAfterIpFailures: intField(2, 1000),
    localLoginAdminsOnly: z.boolean(),
  })
  .superRefine((v, ctx) => {
    // Locking must kick in after the captcha does, otherwise the captcha step never happens.
    const pairs = [
      ['captchaAfterUserFailures', 'lockAfterUserFailures'],
      ['captchaAfterIpFailures', 'lockAfterIpFailures'],
    ] as const
    for (const [captcha, lock] of pairs) {
      if (integerInRange(v[captcha], 1, 1000) !== null || integerInRange(v[lock], 1, 1000) !== null) continue
      const c = Number(v[captcha].trim())
      if (Number(v[lock].trim()) <= c) addIssue(ctx, [lock], i18n.t('av.lockAboveCaptcha', { count: c }))
    }
  })
export type SecurityValues = z.infer<typeof securitySchema>

// ---- release policy (§8) ------------------------------------------------------

const localDateTime = (what: string) =>
  z.string().superRefine((v, ctx) => addIssue(ctx, [], v === '' ? i18n.t('av.pickTime', { what }) : isNaN(new Date(v).getTime()) ? i18n.t('av.badTime') : null))

/** Env selectors that may be empty (meaning: nowhere). */
const optionalEnvSelectors = () =>
  z.array(z.string()).superRefine((v, ctx) => addIssue(ctx, [], v.includes('*') && v.length > 1 ? i18n.t('av.allEnvsAlone') : null))

export const releasePolicySchema = z
  .object({
    confirmReadSeconds: intField(10, 120),
    confirmTtlMinutes: intField(2, 60),
    executeTimeoutMinutes: intField(5, 240),
    minSoakMinutes: intField(0, 10080),
    multiVersionJump: intField(0, 100),
    jiraBaseUrl: optionalUrl({ base: true }),
    jiraRequired: optionalEnvSelectors(),
    reasonRequired: optionalEnvSelectors(),
    approvals: z.array(
      z.object({
        name: requiredText(i18n.t('av.nameField'), 64),
        envs: zEnvSelectors(),
        projects: z.array(z.string()),
        types: z.array(z.string()),
        approvers: z.array(z.string()).min(1, i18n.t('av.pickApprover')),
        mode: z.enum(['any', 'all', 'count']),
        minApprovals: z.string(),
        timeoutMinutes: intField(10, 1440),
      }),
    ),
    soakEnforced: optionalEnvSelectors(),
    versionJumpEnforced: optionalEnvSelectors(),
    configDriftEnforced: optionalEnvSelectors(),
    jiraProjects: z.array(z.object({ value: z.string() })),
    freezes: z.array(
      z.object({
        name: requiredText(i18n.t('av.nameField'), 64),
        envs: zEnvSelectors(),
        startsAt: localDateTime(i18n.t('av.startTime')),
        endsAt: localDateTime(i18n.t('av.endTime')),
        reason: zOptionalText(500),
      }),
    ),
  })
  .superRefine((v, ctx) => {
    if (integerInRange(v.confirmReadSeconds, 10, 120) === null && integerInRange(v.confirmTtlMinutes, 2, 60) === null) {
      if (Number(v.confirmTtlMinutes.trim()) * 60 <= Number(v.confirmReadSeconds.trim())) addIssue(ctx, ['confirmTtlMinutes'], i18n.t('av.confirmTtlOrder'))
    }
    const seen = new Set<string>()
    v.jiraProjects.forEach((p, i) => {
      const s = p.value.trim().toUpperCase()
      if (s === '') addIssue(ctx, ['jiraProjects', i, 'value'], i18n.t('av.jiraKeyRequired'))
      else if (!JIRA_PROJECT_RE.test(s)) addIssue(ctx, ['jiraProjects', i, 'value'], i18n.t('av.jiraKeyFormat'))
      else if (seen.has(s)) addIssue(ctx, ['jiraProjects', i, 'value'], i18n.t('av.jiraDup', { key: s }))
      seen.add(s)
    })
    const ruleNames = new Set<string>()
    v.approvals.forEach((a, i) => {
      if (ruleNames.has(a.name.trim())) addIssue(ctx, ['approvals', i, 'name'], i18n.t('av.ruleNameDup'))
      ruleNames.add(a.name.trim())
      const users = a.approvers.filter((x) => x.startsWith('user:')).length
      if (a.mode === 'all' && users !== a.approvers.length) addIssue(ctx, ['approvals', i, 'mode'], i18n.t('av.allModeUsersOnly'))
      if (a.mode === 'count') {
        const n = integerInRange(a.minApprovals, 1, 10)
        if (n !== null) addIssue(ctx, ['approvals', i, 'minApprovals'], n)
        else if (users === a.approvers.length && Number(a.minApprovals) > users) addIssue(ctx, ['approvals', i, 'minApprovals'], i18n.t('av.minApprovalsTooMany'))
      }
    })
    v.freezes.forEach((f, i) => {
      const a = new Date(f.startsAt).getTime()
      const b = new Date(f.endsAt).getTime()
      if (f.startsAt && f.endsAt && !isNaN(a) && !isNaN(b) && b <= a) addIssue(ctx, ['freezes', i, 'endsAt'], i18n.t('av.endAfterStart'))
    })
  })
export type ReleasePolicyValues = z.infer<typeof releasePolicySchema>

// ---- notify (§6) --------------------------------------------------------------

export const CHANNEL_KINDS = ['lark', 'teams', 'webhook'] as const
export const NOTIFY_EVENTS = ['release.approval_requested', 'release.started', 'release.succeeded', 'release.failed', 'release.rejected', 'release.cancelled', 'build.failed', 'build.warning'] as const

/**
 * A rule's service filter is typed as one line, so the form holds a string
 * and the wire holds a list. Blank means every service.
 */
export function parseServices(s: string): string[] {
  return s.split(/[,\s]+/).map((x) => x.trim()).filter(Boolean)
}

const SERVICE_PATTERN = /^[a-z0-9*?-][a-z0-9*?.-]*$/

export function servicesIssue(s: string): string | null {
  const bad = parseServices(s).filter((p) => !SERVICE_PATTERN.test(p))
  return bad.length === 0 ? null : i18n.t('av.badServicePattern', { patterns: bad.join(', ') })
}

export const channelSchema = z.object({
  name: z.string(),
  kind: z.enum(CHANNEL_KINDS),
  url: z.string().superRefine((v, ctx) => addIssue(ctx, [], v === MASK ? null : validateHttpUrl(v))),
  secret: z.string(),
  enabled: z.boolean(),
}).superRefine((c, ctx) => {
  if (c.secret === MASK || c.secret.trim() === '') return
  if (c.kind !== 'lark') return addIssue(ctx, ['secret'], i18n.t('av.secretLarkOnly'))
  if (codePointLength(c.secret.trim()) > 256) addIssue(ctx, ['secret'], msg.tooLong(256))
})

export const notifySchema = z
  .object({
    channels: z.array(channelSchema),
    rules: z.array(
      z.object({
        name: z.string(),
        enabled: z.boolean(),
        envs: zEnvSelectors(),
        events: z.array(z.string()).superRefine((v, ctx) => addIssue(ctx, [], v.length === 0 ? i18n.t('av.pickEvent') : null)),
        services: z.string().superRefine((v, ctx) => addIssue(ctx, [], servicesIssue(v))),
        channels: z.array(z.string()),
      }),
    ),
  })
  .superRefine((v, ctx) => {
    const names = new Set<string>()
    v.channels.forEach((c, i) => {
      const n = c.name.trim()
      if (n === '') addIssue(ctx, ['channels', i, 'name'], i18n.t('av.nameRequired'))
      else if (codePointLength(n) > 32) addIssue(ctx, ['channels', i, 'name'], msg.tooLong(32))
      else if (names.has(n)) addIssue(ctx, ['channels', i, 'name'], i18n.t('av.channelDup', { name: n }))
      names.add(n)
    })
    const ruleNames = new Set<string>()
    v.rules.forEach((r, i) => {
      const n = r.name.trim()
      if (n === '') addIssue(ctx, ['rules', i, 'name'], i18n.t('av.nameRequired'))
      else if (codePointLength(n) > 32) addIssue(ctx, ['rules', i, 'name'], msg.tooLong(32))
      else if (ruleNames.has(n)) addIssue(ctx, ['rules', i, 'name'], i18n.t('av.ruleDup', { name: n }))
      ruleNames.add(n)
      if (r.channels.length === 0) addIssue(ctx, ['rules', i, 'channels'], i18n.t('av.pickChannel'))
      else {
        const missing = r.channels.find((c) => !names.has(c))
        if (missing) addIssue(ctx, ['rules', i, 'channels'], i18n.t('av.channelMissing', { name: missing }))
      }
    })
  })
export type NotifyValues = z.infer<typeof notifySchema>

// ---- general (§9) -------------------------------------------------------------

export const systemSchema = z
  .object({
    siteName: requiredText(i18n.t('av.siteName'), 32),
    baseUrl: optionalUrl({ base: true }),
    announcement: z.object({ enabled: z.boolean(), level: z.enum(['info', 'warning']), text: zOptionalText(500) }),
  })
  .superRefine((v, ctx) => {
    if (v.announcement.enabled && v.announcement.text.trim() === '') addIssue(ctx, ['announcement', 'text'], i18n.t('av.announcementTextRequired'))
  })
export type SystemValues = z.infer<typeof systemSchema>

// ---- CI tokens ---------------------------------------------------------------

export const ciTokenSchema = z.object({
  name: z.string().superRefine((v, ctx) => addIssue(ctx, [], v.trim() === '' ? i18n.t('av.nameRequired') : v.trim().length > 64 ? i18n.t('av.nameTooLong') : null)),
})

export type CITokenValues = z.infer<typeof ciTokenSchema>
