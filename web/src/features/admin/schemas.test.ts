import { describe, expect, it } from 'vitest'
import { bindingSchema, bindingSubject, environmentsSchema, envSelectorsIssue, mapListField, notifySchema, parseServices, releasePolicySchema, securitySchema, servicesIssue } from './schemas'

const security = {
  sessionTtlMinutes: '60',
  loginWindowMinutes: '15',
  captchaAfterUserFailures: '3',
  captchaAfterIpFailures: '5',
  lockAfterUserFailures: '10',
  lockAfterIpFailures: '30',
  localLoginAdminsOnly: false,
}

function issues(r: { success: boolean; error?: { issues: { path: PropertyKey[]; message: string }[] } }) {
  return Object.fromEntries((r.error?.issues ?? []).map((i) => [i.path.join('.'), i.message]))
}

describe('security form', () => {
  it('accepts the defaults', () => {
    expect(securitySchema.safeParse(security).success).toBe(true)
  })

  it('requires lock thresholds above the captcha thresholds', () => {
    const r = securitySchema.safeParse({ ...security, captchaAfterUserFailures: '10', lockAfterUserFailures: '10', captchaAfterIpFailures: '40', lockAfterIpFailures: '30' })
    expect(issues(r)).toEqual({ lockAfterUserFailures: '锁定阈值需大于验证码阈值（10）', lockAfterIpFailures: '锁定阈值需大于验证码阈值（40）' })
  })

  it('checks ranges, and still reports the cross-field rule next to another field error', () => {
    const r = securitySchema.safeParse({ ...security, sessionTtlMinutes: '4', lockAfterIpFailures: '1001', lockAfterUserFailures: '2' })
    expect(issues(r)).toEqual({ sessionTtlMinutes: '需在 5 到 1440 之间', lockAfterIpFailures: '需在 2 到 1000 之间', lockAfterUserFailures: '锁定阈值需大于验证码阈值（3）' })
  })
})

describe('release policy', () => {
  const base = { confirmReadSeconds: '10', confirmTtlMinutes: '10', executeTimeoutMinutes: '15', minSoakMinutes: '30', multiVersionJump: '3', jiraBaseUrl: '', jiraRequired: ['tier:production'], reasonRequired: ['*'], approvals: [], soakEnforced: [], versionJumpEnforced: [], configDriftEnforced: [], jiraProjects: [], freezes: [] }
  it('confirm reading time can only go up from 10 seconds', () => {
    expect(issues(releasePolicySchema.safeParse({ ...base, confirmReadSeconds: '9' }))).toEqual({ confirmReadSeconds: '需在 10 到 120 之间' })
  })
  it('freeze windows end after they start and name environments', () => {
    const r = releasePolicySchema.safeParse({ ...base, jiraProjects: [{ value: 'ops' }, { value: 'OPS' }], freezes: [{ name: 'x', envs: [], startsAt: '2026-10-01T00:00', endsAt: '2026-09-30T00:00', reason: '' }] })
    expect(issues(r)).toEqual({ 'jiraProjects.1.value': '项目 OPS 重复', 'freezes.0.envs': '请至少选择一个环境', 'freezes.0.endsAt': '结束时间需晚于开始时间' })
  })
})

describe('release policy cross-field', () => {
  it('confirm TTL must outlast the reading time', () => {
    const base = { confirmReadSeconds: '120', confirmTtlMinutes: '2', executeTimeoutMinutes: '15', minSoakMinutes: '30', multiVersionJump: '3', jiraBaseUrl: '', jiraRequired: ['tier:production'], reasonRequired: ['*'], approvals: [], soakEnforced: [], versionJumpEnforced: [], configDriftEnforced: [], jiraProjects: [{ value: 'A' }], freezes: [] }
    expect(issues(releasePolicySchema.safeParse(base))).toEqual({ confirmTtlMinutes: '确认有效期需长于阅读时间' })
  })
})

describe('notify', () => {
  it('rules must point at existing channels and pick events', () => {
    const r = notifySchema.safeParse({
      channels: [{ name: 'ops', kind: 'lark', url: 'https://example.com/hook', secret: '', enabled: true }],
      rules: [{ name: 'prod', enabled: true, envs: ['*'], events: [], services: '', channels: ['dev'] }],
    })
    expect(issues(r)).toEqual({ 'rules.0.events': '请至少选择一个事件', 'rules.0.channels': '渠道 dev 不存在' })
  })

  // The filter is typed as one line and sent as a list, so blank has to keep
  // meaning "every service" rather than "a service with no name".
  it('service patterns are optional, and checked when given', () => {
    expect(parseServices('')).toEqual([])
    expect(parseServices(' cart-*,  portal-api ')).toEqual(['cart-*', 'portal-api'])
    expect(servicesIssue('')).toBeNull()
    expect(servicesIssue('cart-*, portal-api')).toBeNull()
    expect(servicesIssue('Cart API')).not.toBeNull()
  })
})

describe('bindings', () => {
  it('builds subjects and validates env selectors', () => {
    expect(bindingSubject({ kind: 'group', group: ' ops ', userSub: '', envs: ['*'], projects: [], types: [] })).toBe('group:ops')
    expect(bindingSubject({ kind: 'all', group: '', userSub: '', envs: ['*'], projects: [], types: [] })).toBe('*')
    expect(envSelectorsIssue(['*', 'qa'])).toBe('选择「所有环境」时不能再选其他环境')
    expect(issues(bindingSchema('operator').safeParse({ kind: 'user', userSub: '', group: '', envs: [], projects: [], types: [] }))).toEqual({ userSub: '请选择用户', envs: '请至少选择一个环境' })
  })

  it('admin cannot go to everyone', () => {
    expect(issues(bindingSchema('admin').safeParse({ kind: 'all', userSub: '', group: '', envs: ['*'], projects: [], types: [] }))).toEqual({ kind: '不能把管理员授予所有登录用户' })
    expect(bindingSchema('viewer').safeParse({ kind: 'group', userSub: '', group: 'cn=ops,ou=groups', envs: ['*'], projects: [], types: [] }).success).toBe(true)
  })

  it('maps server field paths onto form controls', () => {
    expect(mapListField('rules.0.envs.1', { controls: ['envs'] })).toBe('rules.0.envs')
    expect(mapListField('envs.0', { controls: ['envs'] })).toBe('envs')
    expect(mapListField('jiraProjects.2', { scalarLists: ['jiraProjects'] })).toBe('jiraProjects.2.value')
    expect(mapListField('freezes.0.endsAt', { controls: ['envs'] })).toBe('freezes.0.endsAt')
  })
})

describe('environmentsSchema', () => {
  const env = (name: string, promotesFrom = '') => ({ name, displayName: '', tier: 'staging' as const, description: '', upstream: '', promotesFrom, ci: 'off' as const })
  it('allows a verification source only from an earlier environment', () => {
    expect(environmentsSchema.safeParse({ items: [env('qa'), env('uat', 'qa')] }).success).toBe(true)
    const later = environmentsSchema.safeParse({ items: [env('uat', 'qa'), env('qa')] })
    expect(later.error?.issues.map((i) => i.path.join('.'))).toEqual(['items.0.promotesFrom'])
    const self = environmentsSchema.safeParse({ items: [env('uat', 'uat')] })
    expect(self.success).toBe(false)
  })
})
