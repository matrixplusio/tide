// Mirror of the error code table in docs/api.md. The Go package
// internal/server/api/errcode is the single source; keep this in sync.
export const ErrCode = {
  OK: 0,

  // general
  BadRequest: 1001,
  Unauthenticated: 1002,
  Forbidden: 1003,
  NotFound: 1004,
  Conflict: 1005,
  TooManyRequests: 1006,
  Validation: 1007,
  UnsupportedMediaType: 1008,
  CrossSiteRejected: 1009,
  Internal: 1099,

  // auth / accounts / setup
  InvalidCredentials: 2001,
  LoginThrottled: 2002,
  SetupRequired: 2003,
  SetupTokenInvalid: 2004,
  AlreadyInitialized: 2005,
  SetupTokenRequired: 2006,
  UsernameTaken: 2010,
  UserNotFound: 2011,
  SelfAction: 2012,
  LastAdmin: 2013,
  SSOPasswordManagedByIdP: 2014,
  CaptchaRequired: 2015,
  CaptchaInvalid: 2016,
  SSOProfileManagedByIdP: 2017,
  LocalLoginAdminsOnly: 2018,
  RoleNotFound: 2020,
  BuiltinRole: 2021,
  RoleIdTaken: 2022,
  BindingExists: 2023,
  BindingNotFound: 2024,
  GroupNotFound: 2025,
  GroupExists: 2026,
  CITokenInvalid: 2030,

  // releases
  ReleaseNotFound: 3001,
  ReleaseInFlight: 3002,
  ReadChecklistFirst: 3003,
  ConfirmExpired: 3004,
  ReleaseStateInvalid: 3005,
  DigestMismatch: 3006,
  ArtifactNotAllowed: 3007,
  EnvFrozen: 3008,
  OrderBlocked: 3009,
  ThresholdBlocked: 3010,
  NotApprover: 3011,
  SelfApproval: 3012,
  ApprovalExpired: 3013,
  AlreadyDecided: 3014,
  CIDisabled: 3020,

  // upstreams & service catalog
  NoUpstreams: 4001,
  UpstreamError: 4002,
  UpstreamTimeout: 4003,
  UpstreamCheckFailed: 4004,
  ServiceNotFound: 4005,
  ServiceNotInEnv: 4006,
  EnvNotManagedByKargo: 4007,
  ServiceConflict: 4008,

  // settings
  UnknownSettingsSection: 5001,
  NotifyTestFailed: 5002,
} as const

export type ErrCodeValue = (typeof ErrCode)[keyof typeof ErrCode]
