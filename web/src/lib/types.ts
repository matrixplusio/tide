// Shapes from docs/api.md (camelCase, RFC3339 times).

export type Env = string

export interface Paged<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export type Permission =
  | 'services.view'
  | 'releases.view'
  | 'audit.view'
  | 'users.manage'
  | 'roles.manage'
  | 'environments.manage'
  | 'notifications.manage'
  | 'settings.manage'
  | 'pods.view'
  | 'releases.create'
  | 'releases.restart'
  | 'releases.sync'
  | 'releases.cancel_any'

export type Tier = 'development' | 'testing' | 'staging' | 'production'

export interface User {
  id: number
  sub: string
  name: string
  email?: string
  groups: string[]
  localGroups: string[]
  method: 'local' | 'oidc'
  username?: string
}

export interface EnvironmentInfo {
  name: string
  displayName: string
  tier: Tier
  description: string
}

export interface Freeze {
  name: string
  envs: string[]
  startsAt: string
  endsAt: string
  reason: string
}

export interface DimensionValue {
  value: string
  name: string
}

/** A service catalog filter read from an Argo CD Application label. */
export interface Dimension {
  key: string
  name: string
  label: string
  values: DimensionValue[] | null
}

export interface AppInfo {
  siteName: string
  announcement?: { level: 'info' | 'warning'; text: string } | null
  jiraBaseUrl?: string
  /** Environments where a Jira ticket is mandatory. */
  jiraRequired?: string[] | null
  /** Environments where a reason is mandatory. */
  reasonRequired?: string[] | null
  /** Environments where the minimum soak time / version jump threshold blocks a release. */
  soakEnforced?: string[] | null
  versionJumpEnforced?: string[] | null
  /** Environments where unsynced config blocks an upgrade unless sent along explicitly. */
  configDriftEnforced?: string[] | null
  minSoakMinutes?: number
  multiVersionJump?: number
  /** Environments whose releases need approval, with the rule. */
  /** Approval rules in policy order; pick one per service with approvalFor(). */
  approvals?: ApprovalPolicyInfo[] | null
  confirmReadSeconds: number
  activeFreezes: Freeze[] | null
  dimensions?: Dimension[] | null
  /** Dimension key batch releases may not mix and whose value order is enforced; "" = none. */
  batchDimension?: string
  /** What this server is, e.g. "v0.0.1-beta.1 (a1b2c3d)". */
  version?: string
}

/** One binding's environment permissions with the scope it applies to. */
export interface ScopedGrant {
  permissions: Permission[]
  envs: Env[]
  /** Projects it is limited to; empty or ["*"] = all. */
  projects: string[]
  /** Service types (the catalog's batch dimension) it is limited to. */
  types: string[]
}

export interface Me {
  user: User
  permissions: Permission[]
  envPermissions: Record<Env, Permission[] | undefined>
  scopedGrants: ScopedGrant[]
  /** Per view permission: held in at least one scope. */
  canView: Partial<Record<Permission, boolean>>
  canOperate: Record<Env, boolean>
  envOrder: Env[]
  environments: EnvironmentInfo[]
  app: AppInfo
}

export interface Session {
  id: string
  current: boolean
  createdAt: string
  expiresAt: string
  clientIp?: string
  userAgent?: string
}

export interface RoleBinding {
  id: number
  roleId: string
  roleName: string
  subject: string
  subjectName: string
  envs: string[]
  /** Projects / service types the grant is limited to; empty = all. */
  projects: string[]
  types: string[]
  createdAt: string
  createdBy: string
}

export interface EffectiveBinding extends RoleBinding {
  via: 'user' | 'group' | 'all'
}

export interface AuthMethods {
  initialized: boolean
  sso: boolean
}

export interface PasswordPolicy {
  minLength: number
  maxBytes: number
}

export interface SetupState {
  initialized: boolean
  tokenVerified: boolean
  passwordPolicy: PasswordPolicy
}

export interface Deployment {
  service: string
  env: Env
  domain: string
  upstream: string
  app: string
  namespace: string
  sync: string
  health: string
  healthMessage?: string
  operation?: string
  images: string[]
  image?: string
  tag?: string
  digest?: string
  version?: string
  builtAt?: string
  kargoProject?: string
  kargoStage?: string
  freight?: string
  since?: string
  promoting?: string
  autoPromotion: boolean
  autoHeld: boolean
  grafana?: string
}

export interface Service {
  name: string
  /** Groups domains; "" when unassigned. */
  project?: string
  domain: string
  /** Value of each catalog dimension, by dimension key. */
  dimensions?: Record<string, string | undefined> | null
  envs: Record<Env, Deployment | undefined>
  /** Why the name is ambiguous; changes are refused while present. */
  conflicts?: string[] | null
}

export interface UpstreamStatus {
  name: string
  /** Environments served by this upstream; null when none references it. */
  envs: Env[] | null
  kargoOk: boolean
  kargoError?: string
  kargoVersion?: string
  argocdOk: boolean
  argocdError?: string
  argocdVersion?: string
  catalog: CatalogStats
  /** Credentials near or past their recorded expiry, soonest first. */
  expiring?: CredentialExpiry[]
  /** Image metadata lookups that failed; versions and build times go blank. */
  registryFailed?: number
  registryError?: string
  /** The failures were refusals: a new credential fixes them, the network will not. */
  registryAuth?: boolean
  checkedAt: string
}

/** One credential whose recorded expiry is near or past. Absent entirely when
 *  nobody wrote a date down — Tide never reads expiry out of a token. */
export interface CredentialExpiry {
  upstream: string
  kind: 'kargo' | 'argocd' | 'registry'
  expires: string
  /** Days remaining; negative once the date has gone by. */
  days: number
}

/** What became of the Applications this upstream returned. `kept === 0` with
 *  `applications > 0` means the catalog settings match nothing — which reads
 *  as an empty upstream unless the page says otherwise. */
export interface CatalogStats {
  applications: number
  kept: number
  /** The environment dimension resolved to nothing: the usual misconfiguration. */
  noEnv: number
  /** Resolved to an environment this upstream does not serve. Ordinary. */
  otherEnv: number
}

export interface Artifact {
  digest: string
  tag: string
  version?: string
  builtAt?: string
}

export interface Anomaly {
  code: string
  message: string
}

export interface ImagePayload {
  upstream: string
  project: string
  stage: string
  service: string
  env: Env
  freight: string
  image: string
  from: Artifact | null
  to: Artifact
  anomalies?: Anomaly[] | null
  /** Git changes Argo CD had not applied when the release was built; they go out with the image. */
  configChanges?: ResourceChange[] | null
  /** The creator chose to send configChanges along explicitly. */
  withConfig?: boolean
}

export type ChangeAction = 'create' | 'update' | 'delete'

/** One resource a sync creates, updates or deletes, with a unified diff of its manifest. */
export interface ResourceChange {
  group?: string
  kind: string
  namespace?: string
  name: string
  action: ChangeAction
  diff?: string
  truncated?: boolean
}

/** What syncing an Application would change right now. */
export interface ConfigDiff {
  app: string
  revision: string
  sync: string
  changes: ResourceChange[]
  /** Only ConfigMaps / Secrets change: pods keep old values until restarted. */
  needsRestart: boolean
}

/** kind=sync: apply reviewed git changes (other than the image) through Argo CD. */
export interface SyncPayload {
  upstream: string
  service: string
  env: Env
  app: string
  revision: string
  current: Artifact
  changes: ResourceChange[] | null
  prune?: boolean
  restart?: boolean
  workloads?: Workload[] | null
}

export interface Workload {
  group: string
  version: string
  kind: string
  namespace: string
  name: string
}

/** kind=restart: roll the Application's workloads, keeping the running version. */
export interface RestartPayload {
  upstream: string
  service: string
  env: Env
  app: string
  current: Artifact
  workloads: Workload[] | null
}

export type ReleaseStatus = 'draft' | 'confirming' | 'approving' | 'executing' | 'succeeded' | 'failed' | 'cancelled' | 'rejected'

/** Who must approve and how many: approvers are `user:<sub>` / `group:<name>`. */
/** An approval rule with the scope it applies to, as /me returns it. */
export interface ApprovalPolicyInfo {
  envs: Env[]
  projects: string[]
  types: string[]
  rule: ApprovalRule
}

export interface ApprovalRule {
  name: string
  approvers: string[] | null
  mode: 'any' | 'all' | 'count'
  minApprovals: number
  timeoutMinutes: number
}

export interface Approval {
  sub: string
  name: string
  decision: 'approve' | 'reject'
  note?: string
  decidedAt: string
}
export type ItemStatus = 'planned' | 'pending' | 'executing' | 'succeeded' | 'failed' | 'skipped' | 'cancelled'

interface ItemBase {
  id: number
  releaseId: string
  sequence: number
  status: ItemStatus
  externalRef?: string
  error?: string
  startedAt?: string
  finishedAt?: string
}

export type Item = ItemBase & ({ kind: 'image'; payload: ImagePayload } | { kind: 'restart'; payload: RestartPayload } | { kind: 'sync'; payload: SyncPayload })
export type ItemKind = Item['kind']

export interface Release {
  id: string
  title: string
  env: Env
  jiraTicket: string
  reason: string
  createdBy: string
  createdByName: string
  /** Where it came from: "ui" (a person) or "ci" (a build pipeline). */
  source?: 'ui' | 'ci'
  /** Released without a person confirming it. */
  automatic?: boolean
  status: ReleaseStatus
  submittedAt?: string
  confirmedAt?: string
  confirmedBy?: string
  createdAt: string
  finishedAt?: string
  items?: Item[] | null
  now: string
  confirmableAt?: string
  expiresAt?: string
  approvalRule?: ApprovalRule | null
  approvalExpiresAt?: string
  approvals?: Approval[] | null
  /** The viewer is an approver who has not decided yet. */
  canApprove?: boolean
}

export interface Candidate {
  freight: string
  alias?: string
  image: string
  tag: string
  digest: string
  version?: string
  builtAt?: string
  createdAt?: string
  available: boolean
  current: boolean
  verifiedIn: { stage: string; since?: string }[] | null
  currentIn: { stage: string; since?: string }[] | null
}

export interface Candidates {
  deployment: Deployment
  upstreamStages: string[] | null
  /** Kargo warehouses the stage takes freight from. */
  warehouses?: string[] | null
  /** True when freight comes straight from CI (a warehouse), not through another stage. */
  direct?: boolean
  items: Candidate[] | null
  availableCount: number
  totalCount: number
  /** Cross-site verification: only digests verified in this source pass. */
  gate?: VerificationGate | null
}

export interface VerificationGate {
  env: string
  upstream?: string
  project?: string
  stage?: string
  /** e.g. "qa（onprem）"; also listed in upstreamStages and candidates' verifiedIn. */
  label: string
  /** Why the source could not be checked; nothing passes then. */
  problem?: string
}

export interface Pod {
  name: string
  namespace: string
  uid: string
  health: string
  message?: string
  status: string
  ready: string
  restarts: string
  images: string[]
  createdAt?: string
  isTarget: boolean
}

/** Deployment / StatefulSet counters as reported by Kubernetes (readyReplicas includes old pods). */
export interface Rollout {
  name: string
  desired: number
  updated: number
  ready: number
  available: number
}

export interface Live {
  app: string
  sync: string
  health: string
  operation?: string
  resources: { kind: string; name: string; namespace: string; sync: string; health?: string; message?: string }[] | null
  pods: Pod[] | null
  rollouts: Rollout[] | null
  error?: string
}

export interface PromotionStep {
  name: string
  uses: string
  status: string
  message?: string
  startedAt?: string
  finishedAt?: string
}

export interface PromotionView {
  name: string
  phase: string
  message?: string
  freight: string
  tag?: string
  digest?: string
  createdAt?: string
  finishedAt?: string
  actor?: string
  steps: PromotionStep[] | null
  releaseId?: string
}

export interface ItemLive {
  itemId: number
  promotion?: PromotionView
  live?: Live
  error?: string
}

export interface AuditEntry {
  id: number
  at: string
  actor: string
  actorName: string
  action: string
  target?: string
  jiraTicket?: string
  detail: unknown
  /** Where the request came from, and the id that ties it to the server logs. */
  clientIp?: string
  requestId?: string
}

export interface LogLine {
  content: string
  timeStamp: string
}

export interface PodEvent {
  type: string
  reason: string
  message: string
  count: number
  lastTimestamp?: string
  firstTimestamp?: string
  eventTime?: string
}

export interface CheckResult {
  upstream: string
  checks: { name: string; ok: boolean; detail: string }[]
}

/** The insights report: what the release process did, and whether its
 *  safeguards changed any outcomes. Mirrors internal/insights. */
export interface Insights {
  range: { from: string; to: string }
  totals: {
    releases: number
    succeeded: number
    failed: number
    cancelled: number
    rejected: number
    inFlight: number
  }
  process: {
    confirmDwell: { label: string; upper: number; count: number }[]
    confirmSeconds: number
    anomalies: {
      withAnomaly: number
      wentAhead: number
      byCode: { code: string; count: number }[]
    }
    approvals: {
      requested: number
      approved: number
      rejected: number
      expired: number
      medianSeconds: number
      p90Seconds: number
      selfConfirmed: number
    }
    sources: { source: string; total: number; succeeded: number; failed: number }[]
  }
  activity: {
    services: { service: string; project?: string; total: number; succeeded: number; failed: number }[]
    environments: { env: string; total: number; succeeded: number; failed: number }[]
    kinds: { kind: string; total: number; succeeded: number; failed: number }[]
    weekly: { weekday: number; hour: number; count: number }[]
    /** Absent for a viewer who cannot see every environment. */
    people?: {
      sub: string
      name: string
      token: boolean
      created: number
      confirmed: number
      approved: number
      rejected: number
      failed: number
    }[]
  }
  risk: {
    coverage: { catalog: number; released: number; untouched: string[] }
    rollbacks: number
    repeats: { service: string; env: string; day: string; count: number }[]
    jiraReuse: { jira: string; count: number }[]
    freezeBlocked: number
    afterHours: number
  }
}
