import { i18n } from './i18n'
import { z } from 'zod'

// Shared rules. They mirror the server rules in internal/validate/validate.go
// (and the path-parameter rules in internal/server/api/v1/params.go); the
// server stays the only real defence.

export const PASSWORD_MIN_LENGTH = 12
export const PASSWORD_MAX_BYTES = 72

// Every message is a catalogue lookup, so a rule reads the same here as it
// does when the server rejects the same input in the same language.
export const msg = {
  required: (what: string) => i18n.t('validate.required', { what }),
  passwordRequired: () => i18n.t('validate.passwordRequired'),
  passwordTooShort: (n: number) => i18n.t('validate.passwordTooShort', { min: PASSWORD_MIN_LENGTH, n }),
  passwordTooLong: () => i18n.t('validate.passwordTooLong', { max: PASSWORD_MAX_BYTES }),
  passwordAllSpace: () => i18n.t('validate.passwordAllSpace'),
  passwordEdgeSpace: () => i18n.t('validate.passwordEdgeSpace'),
  passwordFewDistinct: () => i18n.t('validate.passwordFewDistinct'),
  passwordSequence: () => i18n.t('validate.passwordSequence'),
  passwordCommon: () => i18n.t('validate.passwordCommon'),
  passwordHasUsername: () => i18n.t('validate.passwordHasUsername'),
  passwordMismatch: () => i18n.t('validate.passwordMismatch'),
  confirmRequired: () => i18n.t('validate.confirmRequired'),
  passwordSameAsCurrent: () => i18n.t('validate.passwordSameAsCurrent'),
  username: () => i18n.t('validate.username'),
  usernameRequired: () => i18n.t('validate.usernameRequired'),
  jira: () => i18n.t('validate.jira'),
  jiraRequired: () => i18n.t('validate.jiraRequired'),
  urlRequired: () => i18n.t('validate.urlRequired'),
  urlInvalid: () => i18n.t('validate.urlInvalid'),
  urlCredentials: () => i18n.t('validate.urlCredentials'),
  urlQuery: () => i18n.t('validate.urlQuery'),
  tooLong: (max: number) => i18n.t('validate.tooLong', { max }),
  atLeast: (what: string, min: number) => i18n.t('validate.atLeast', { what, min }),
}

const COMMON_PASSWORDS = new Set([
  '123456789012',
  '1234567890123',
  '12345678901234',
  'qwertyuiopas',
  'qwertyuiop123',
  'password1234',
  'password12345',
  'passw0rd1234',
  'adminadmin123',
  'administrator',
  '1qaz2wsx3edc',
  '1q2w3e4r5t6y',
  'abcdefghijkl',
  'abc123456789',
  'iloveyou1234',
  'tide12345678',
  'changeme1234',
  'welcome12345',
  'letmein12345',
  'p@ssw0rd1234',
])

/** Length in Unicode code points, which is what users (and the server) count. */
export function codePointLength(s: string): number {
  return [...s].length
}

export function utf8ByteLength(s: string): number {
  return new TextEncoder().encode(s).length
}

const isDigit = (c: number) => c >= 48 && c <= 57

// Every step is +1 (or every step is -1). Digit runs may wrap 9→0 / 0→9.
export function isStraightSequence(s: string): boolean {
  const cps = [...s.toLowerCase()].map((c) => c.codePointAt(0) ?? 0)
  if (cps.length < 2) return false
  const step = (a: number, b: number): number => {
    if (isDigit(a) && isDigit(b)) {
      if (a === 57 && b === 48) return 1
      if (a === 48 && b === 57) return -1
    }
    return b - a
  }
  const first = step(cps[0]!, cps[1]!)
  if (first !== 1 && first !== -1) return false
  for (let i = 2; i < cps.length; i++) {
    if (step(cps[i - 1]!, cps[i]!) !== first) return false
  }
  return true
}

export interface PasswordRuleState {
  key: 'length' | 'bytes' | 'spaces' | 'simple' | 'username'
  label: string
  ok: boolean
}

/** Short live checklist shown under new-password fields. */
export function passwordChecklist(pw: string, username?: string): PasswordRuleState[] {
  const rules: PasswordRuleState[] = [
    { key: 'length', label: i18n.t('validate.ruleLength', { min: PASSWORD_MIN_LENGTH }), ok: codePointLength(pw) >= PASSWORD_MIN_LENGTH },
    { key: 'bytes', label: i18n.t('validate.ruleBytes', { max: PASSWORD_MAX_BYTES }), ok: pw !== '' && utf8ByteLength(pw) <= PASSWORD_MAX_BYTES },
    { key: 'spaces', label: i18n.t('validate.ruleSpaces'), ok: pw !== '' && pw.trim() !== '' && pw === pw.trim() },
    {
      key: 'simple',
      label: i18n.t('validate.ruleSimple'),
      ok: pw !== '' && new Set([...pw]).size >= 4 && !isStraightSequence(pw) && !COMMON_PASSWORDS.has(pw.toLowerCase()),
    },
  ]
  if (username && codePointLength(username) >= 3) {
    rules.push({ key: 'username', label: i18n.t('validate.ruleUsername'), ok: pw !== '' && !pw.toLowerCase().includes(username.toLowerCase()) })
  }
  return rules
}

/** First violated password rule, in the server's order, or null. */
export function validatePassword(pw: string, username?: string): string | null {
  if (pw === '') return msg.passwordRequired()
  const n = codePointLength(pw)
  if (n < PASSWORD_MIN_LENGTH) return msg.passwordTooShort(n)
  if (utf8ByteLength(pw) > PASSWORD_MAX_BYTES) return msg.passwordTooLong()
  if (pw.trim() === '') return msg.passwordAllSpace()
  if (pw !== pw.trim()) return msg.passwordEdgeSpace()
  if (new Set([...pw]).size < 4) return msg.passwordFewDistinct()
  if (isStraightSequence(pw)) return msg.passwordSequence()
  if (COMMON_PASSWORDS.has(pw.toLowerCase())) return msg.passwordCommon()
  if (username && codePointLength(username) >= 3 && pw.toLowerCase().includes(username.toLowerCase())) return msg.passwordHasUsername()
  return null
}

export function validateConfirm(pw: string, confirm: string): string | null {
  if (confirm === '') return msg.confirmRequired()
  return pw === confirm ? null : msg.passwordMismatch()
}

const USERNAME_RE = /^[a-z][a-z0-9._-]{1,31}$/

export function validateUsername(u: string): string | null {
  if (u.trim() === '') return msg.usernameRequired()
  return USERNAME_RE.test(u) ? null : msg.username()
}

// Same as the server: role ids, group names, environment names.
export const ROLE_ID_RE = /^[a-z][a-z0-9-]{1,31}$/
export const GROUP_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/
export const ENV_RE = /^[a-z][a-z0-9-]{0,31}$/
export const JIRA_PROJECT_RE = /^[A-Z][A-Z0-9_]{0,31}$/

export const JIRA_RE = /^[A-Z][A-Z0-9_]+-[0-9]+$/

export function normalizeJira(v: string): string {
  return v.trim().toUpperCase()
}

export function validateJira(v: string): string | null {
  const j = normalizeJira(v)
  if (j === '') return msg.jiraRequired()
  return JIRA_RE.test(j) ? null : msg.jira()
}

export interface HttpUrlOptions {
  /** Base URLs (API endpoints, issuers) may not carry a query or fragment. */
  base?: boolean
  /** Path the URL must end with, e.g. the SSO callback. */
  suffix?: string
}

export function validateHttpUrl(v: string, opts: HttpUrlOptions = {}): string | null {
  const s = v.trim()
  if (s === '') return msg.urlRequired()
  let u: URL
  try {
    u = new URL(s)
  } catch {
    return msg.urlInvalid()
  }
  if ((u.protocol !== 'http:' && u.protocol !== 'https:') || !u.hostname || !/^https?:\/\//i.test(s)) return msg.urlInvalid()
  if (u.username || u.password) return msg.urlCredentials()
  if (opts.base && (u.search || u.hash || s.includes('?') || s.includes('#'))) return msg.urlQuery()
  if (opts.suffix && !u.pathname.endsWith(opts.suffix)) return i18n.t('validate.urlSuffix', { suffix: opts.suffix })
  return null
}

// ---- zod building blocks --------------------------------------------------
// Field-level rules are attached with superRefine so every field reports its
// own error independently; cross-field rules live on the object schema.

type Ctx = z.RefinementCtx

export function addIssue(ctx: Ctx, path: (string | number)[], message: string | null) {
  if (message) ctx.addIssue({ code: 'custom', path, message })
}

export const zUsername = () => z.string().superRefine((v, ctx) => addIssue(ctx, [], validateUsername(v)))

export const zJira = () => z.string().superRefine((v, ctx) => addIssue(ctx, [], validateJira(v)))

/** Like zRequiredText, but empty is allowed. */
export const zOptionalMinText = (what: string, max: number, min = 1) =>
  z.string().superRefine((v, ctx) => {
    const n = codePointLength(v.trim())
    if (n === 0) return
    if (n < min) return addIssue(ctx, [], msg.atLeast(what, min))
    if (n > max) return addIssue(ctx, [], msg.tooLong(max))
  })

/** Jira ticket that may be left empty (environments the release policy does not require it for). */
export const zOptionalJira = () => z.string().superRefine((v, ctx) => addIssue(ctx, [], normalizeJira(v) === '' ? null : validateJira(v)))

export const zRequiredText = (what: string, max: number, min = 1) =>
  z.string().superRefine((v, ctx) => {
    const s = v.trim()
    if (s === '') return addIssue(ctx, [], msg.required(what))
    const n = codePointLength(s)
    if (n < min) return addIssue(ctx, [], msg.atLeast(what, min))
    if (n > max) return addIssue(ctx, [], msg.tooLong(max))
  })

export const zOptionalText = (max: number) =>
  z.string().superRefine((v, ctx) => addIssue(ctx, [], codePointLength(v.trim()) > max ? msg.tooLong(max) : null))

/**
 * password + confirmPassword (+ username for the "contains username" rule).
 * Used as an object-level refinement so both fields are always checked.
 */
export function refineNewPassword(
  ctx: Ctx,
  v: { password: string; confirm: string; username?: string },
  paths: { password: string; confirm: string },
) {
  addIssue(ctx, [paths.password], validatePassword(v.password, v.username))
  addIssue(ctx, [paths.confirm], validateConfirm(v.password, v.confirm))
}

export function integerInRange(v: string, min: number, max: number): string | null {
  const s = v.trim()
  if (s === '') return i18n.t('validate.numberRequired')
  if (!/^-?\d+$/.test(s)) return i18n.t('validate.integerRequired')
  const n = Number(s)
  if (n < min || n > max) return i18n.t('validate.intRange', { min, max })
  return null
}
