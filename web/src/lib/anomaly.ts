import { i18n } from './i18n'
import type { Anomaly } from './types'

/**
 * The sentence for each anomaly code, with the facts left as {{0}}, {{1}}…
 *
 * The release stores the sentence it put to whoever confirmed it, in the
 * language they were working in, and that stays: it is the record of what was
 * read before the decision. But a release list written half in one language
 * and half in another is what an installation whose people do not share one
 * actually gets, so where the facts were also stored apart from the wording,
 * the sentence is rebuilt here in the reader's language instead.
 */
const SENTENCE: Record<string, string> = {
  rollback: 'anomaly.rollback',
  multi_version_jump: 'anomaly.versionJump',
  short_soak: 'anomaly.shortSoak',
  config_drift: 'anomaly.configDrift',
  first_deploy: 'anomaly.firstDeploy',
  first_deploy_per_kargo: 'anomaly.firstDeployPerKargo',
}

/** How many facts each sentence needs. A release from before the facts were
 *  stored has none, and its own text is then the only text there is. */
const ARITY: Record<string, number> = {
  rollback: 2,
  multi_version_jump: 3,
  short_soak: 3,
  config_drift: 2,
  first_deploy: 0,
  first_deploy_per_kargo: 0,
}

export function anomalyText(a: Anomaly): string {
  const key = SENTENCE[a.code]
  const args = a.args ?? []
  if (!key || args.length < (ARITY[a.code] ?? 0)) return a.message
  return i18n.t(key, Object.fromEntries(args.map((v, i) => [String(i), v])))
}
