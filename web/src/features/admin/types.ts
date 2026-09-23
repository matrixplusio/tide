import type { Dimension, EffectiveBinding, Freeze, Permission, Tier } from '../../lib/types'

export const MASK = '••••••'

export interface UserRow {
  id: number
  sub: string
  method: 'local' | 'oidc'
  username?: string
  name: string
  email?: string
  groups: string[] | null
  localGroups: string[] | null
  roles: { id: string; name: string }[] | null
  disabled: boolean
  lastLoginAt?: string
  createdAt: string
}

export interface UserDetail {
  user: UserRow
  bindings: EffectiveBinding[] | null
  sessionCount: number
}

export interface GroupRow {
  name: string
  description: string
  memberCount: number
  createdAt: string
}

export interface Role {
  id: string
  name: string
  description: string
  builtin: boolean
  permissions: (Permission | '*')[]
  bindingCount: number
}

export interface PermissionInfo {
  key: Permission
  name: string
  description: string
  scope: 'global' | 'env'
  routes: { method: string; path: string }[] | null
}

export interface RbacCatalog {
  items: PermissionInfo[]
  tiers: { key: Tier; name: string }[]
}

// ---- settings sections ------------------------------------------------------

export interface Upstream {
  name: string
  kargoUrl: string
  kargoToken: string
  argocdUrl: string
  argocdToken: string
  registryUrl: string
  registryUser: string
  registryToken: string
  /** Day each credential stops working, "2026-10-22"; empty when unrecorded. */
  kargoExpires?: string
  argocdExpires?: string
  registryExpires?: string
  insecureTls: boolean
  grafanaUrl?: string
}

export interface Environment {
  name: string
  displayName: string
  tier: Tier
  description: string
  upstream: string
  /** Environment (on another Kargo) whose verification this one requires; "" = none. */
  promotesFrom: string
  /** What a build pipeline's webhook may do here. */
  ci: CIMode
}

/** off: refuse CI releases. approve: create one and wait for a person.
 *  auto: start it (an approval rule still applies). */
export const CI_MODES = ['off', 'approve', 'auto'] as const
export type CIMode = (typeof CI_MODES)[number]

export type ChannelKind = 'lark' | 'teams' | 'webhook'
export type NotifyEvent = 'release.pending' | 'release.approval_requested' | 'release.started' | 'release.succeeded' | 'release.failed' | 'release.rejected' | 'release.cancelled'

export interface Channel {
  name: string
  kind: ChannelKind
  url: string
  secret: string
  enabled: boolean
}

export interface NotifyRule {
  name: string
  enabled: boolean
  envs: string[]
  events: NotifyEvent[]
  channels: string[]
}

export interface Notify {
  channels: Channel[] | null
  rules: NotifyRule[] | null
}

export interface OIDC {
  issuer: string
  clientId: string
  clientSecret: string
  groupsClaim: string
  redirectUrl: string
}

export interface Security {
  sessionTtlMinutes: number
  loginWindowMinutes: number
  captchaAfterUserFailures: number
  captchaAfterIpFailures: number
  lockAfterUserFailures: number
  lockAfterIpFailures: number
  localLoginAdminsOnly: boolean
}

export interface ReleasePolicy {
  confirmReadSeconds: number
  confirmTtlMinutes: number
  executeTimeoutMinutes: number
  minSoakMinutes: number
  multiVersionJump: number
  jiraBaseUrl: string
  jiraRequired: string[] | null
  reasonRequired: string[] | null
  approvals: ApprovalPolicy[] | null
  soakEnforced: string[] | null
  versionJumpEnforced: string[] | null
  configDriftEnforced: string[] | null
  jiraProjects: string[] | null
  freezes: Freeze[] | null
}

export interface SystemSettings {
  siteName: string
  baseUrl: string
  announcement: { enabled: boolean; level: 'info' | 'warning'; text: string }
}

export interface CatalogSettings {
  serviceLabel: string
  envLabel: string
  domainLabel: string
  projectLabel: string
  batchDimension: string
  dimensions: Dimension[] | null
}

export interface DiscoveredLabel {
  key: string
  count: number
  values: { value: string; count: number }[] | null
}

export interface SettingsData {
  catalog?: CatalogSettings | null
  upstreams?: { items: Upstream[] | null } | null
  environments?: { items: Environment[] | null } | null
  notify?: Notify | null
  oidc?: OIDC | null
  security?: Security | null
  release?: ReleasePolicy | null
  system?: SystemSettings | null
  pipeline?: PipelineRepo | null
}

/** Where generated Kargo pipelines are committed. Tide writes through
 *  GitLab's API rather than driving git. */
export interface PipelineRepo {
  /** Which API to speak; empty means gitlab. */
  provider?: 'gitlab' | 'gitea'
  baseUrl: string
  /** Path with namespace, e.g. "devops/k8s-pipelines". */
  project: string
  /** Created from the default branch when it does not exist yet. */
  branch: string
  pathPrefix?: string
  token: string
  /** Name Kargo projects after the domain alone instead of line-and-domain. */
  bareDomain?: boolean
}

export type SettingsSection = 'catalog' | 'upstreams' | 'environments' | 'notify' | 'oidc' | 'security' | 'release' | 'system' | 'pipeline'

/** An approval rule and the environments it covers (first match wins). */
export interface ApprovalPolicy {
  /** Projects / service types the rule is limited to; empty = all. */
  projects: string[] | null
  types: string[] | null
  name: string
  envs: string[] | null
  approvers: string[] | null
  mode: 'any' | 'all' | 'count'
  minApprovals: number
  timeoutMinutes: number
}

// ---- CI-triggered releases --------------------------------------------------

/** A credential a build pipeline presents. The token itself is only ever in
 *  the response that created it; Tide stores a hash. */
export interface CIToken {
  id: string
  name: string
  prefix: string
  createdBy: string
  createdByName: string
  createdAt: string
  lastUsedAt?: string | null
  revokedAt?: string | null
}

export type IntakeStatus = 'waiting' | 'released' | 'failed' | 'expired'

/** One notification from a pipeline and what became of it. */
export interface CIIntake {
  id: number
  key: string
  service: string
  env: string
  image: string
  digest: string
  commit?: string
  pipeline?: string
  actor?: string
  jiraTicket?: string
  status: IntakeStatus
  releaseId?: string
  error?: string
  attempts: number
  createdAt: string
  updatedAt: string
}

export interface CISnippet {
  env: string
  mode: CIMode
  url: string
  variable: string
  snippet: string
}

// ---- kargo pipeline generation ----------------------------------------------

export interface KargoFile {
  /** Relative, "<domain>/<name>.yaml". */
  path: string
  yaml: string
}

/** A service that produced nothing, and why. Never silent: a generator that
 *  quietly drops one leaves a release process missing exactly one thing. */
export interface KargoSkipped {
  service: string
  env?: string
  reason: string
}

/** Everything generated for one business domain, which is also one Kargo
 *  project. Grouped rather than flat because the domain is what a person
 *  reviews. */
export interface KargoDomain {
  name: string
  services: number
  warehouses: number
  stages: number
  files: KargoFile[]
}

export interface KargoResult {
  domains: KargoDomain[]
  skipped: KargoSkipped[]
  services: number
  warehouses: number
  stages: number
  fileCount: number
}

export interface KargoPlan {
  result: KargoResult
  /** Every business domain in the catalog, for the picker. */
  domains: { name: string; services: number }[]
  at: string
}

export interface KargoPushed {
  commit: { id: string; short_id?: string; title?: string; web_url?: string }
  branch: string
  files: number
  result: KargoResult
}

/** Who the pipeline-repository token belongs to. Commits land under this
 *  name, which a token string does not reveal. */
export interface RepoIdentity {
  username?: string
  name?: string
  project?: string
  canWrite?: boolean
  /** The host did not answer; the settings themselves are fine. */
  error?: string
}
