import type { ApprovalPolicyInfo, ApprovalRule, ChangeAction, Item, ResourceChange } from './types'
import { i18n } from './i18n'
import { scopeMatches, subjectLabel } from './permissions'

/** The digest an item's confirmation echoes: the target for upgrades, the running one for restarts. */
export function itemDigest(it: Item): string {
  return it.kind === 'image' ? it.payload.to.digest : it.payload.current.digest
}

/** Human-readable thresholds that block releases in this environment, or [] when none are enforced. */
export function enforcedThresholds(app: { soakEnforced?: string[] | null; versionJumpEnforced?: string[] | null; minSoakMinutes?: number; multiVersionJump?: number } | undefined, env: string): string[] {
  const out: string[] = []
  if (app?.soakEnforced?.includes(env) && app.minSoakMinutes) out.push(i18n.t('release.soakEnforced', { minutes: app.minSoakMinutes }))
  if (app?.versionJumpEnforced?.includes(env) && app.multiVersionJump) out.push(i18n.t('release.jumpEnforced', { count: app.multiVersionJump }))
  return out
}

/** Who has to approve under a rule, as a sentence. */
export function approvalText(rule: ApprovalRule | undefined | null): string {
  if (!rule) return ''
  const who = (rule.approvers ?? []).map((a) => subjectLabel(a)).join(', ')
  const how =
    rule.mode === 'all' ? i18n.t('release.modeAll') : rule.mode === 'count' ? i18n.t('release.modeCount', { count: rule.minApprovals }) : i18n.t('release.modeAny')
  return `${who} ${how}`
}

export function changeActionText(a: ChangeAction): string {
  const name = { create: 'Create', update: 'Update', delete: 'Delete' }[a]
  return i18n.t(`release.change${name}`)
}

/** "changed 2 · deleted 1" */
export function changeSummary(changes: readonly ResourceChange[]): string {
  return (['create', 'update', 'delete'] as const)
    .map((a) => [a, changes.filter((c) => c.action === a).length] as const)
    .filter(([, n]) => n > 0)
    .map(([a, n]) => `${changeActionText(a)} ${n}`)
    .join(' · ')
}

/**
 * The approval rule a release of this service in this environment falls
 * under, mirroring the server: the most specific match wins (project beats
 * type beats neither), ties go to the first rule.
 */
export function approvalFor(
  app: { approvals?: ApprovalPolicyInfo[] | null } | undefined,
  env: string,
  project?: string,
  type?: string,
): ApprovalRule | undefined {
  let best: ApprovalPolicyInfo | undefined
  let score = -1
  for (const p of app?.approvals ?? []) {
    if (!p.envs.includes(env) || !scopeMatches(p.projects, project) || !scopeMatches(p.types, type)) continue
    const s = (scopeMatches(p.projects, undefined) ? 0 : 2) + (scopeMatches(p.types, undefined) ? 0 : 1)
    if (s > score) [best, score] = [p, s]
  }
  return best?.rule
}
