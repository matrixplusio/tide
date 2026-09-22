import { describe, expect, it } from 'vitest'
import { bindingViaLabel, can, canEnv, envScopeText, envSelectorLabel, jiraHref, subjectLabel } from './permissions'
import type { EnvironmentInfo, Permission } from './types'

const envs: EnvironmentInfo[] = [
  { name: 'qa', displayName: '测试', tier: 'testing', description: '' },
  { name: 'prod', displayName: 'prod', tier: 'production', description: '' },
]

describe('env scope rendering', () => {
  it.each([
    ['*', '所有环境'],
    ['tier:production', '生产类环境'],
    ['tier:development', '开发类环境'],
    ['tier:testing', '测试类环境'],
    ['tier:staging', '预发布类环境'],
    ['qa', '测试（qa）'],
    ['prod', 'prod'],
    ['gone', 'gone'],
  ])('%s → %s', (sel, text) => {
    expect(envSelectorLabel(sel, envs)).toBe(text)
  })

  it('joins several selectors', () => {
    expect(envScopeText(['tier:staging', 'qa'], envs)).toBe('预发布类环境、测试（qa）')
    expect(envScopeText([], envs)).toBe('—')
  })

  it('names subjects and how a binding applies', () => {
    expect(subjectLabel('*')).toBe('所有登录用户')
    expect(subjectLabel('group:ops')).toBe('组 ops')
    expect(subjectLabel('user:local:alice', 'Alice')).toBe('Alice')
    expect(bindingViaLabel({ via: 'group', subject: 'group:ops', subjectName: 'ops' })).toBe('通过组 ops')
    expect(bindingViaLabel({ via: 'user', subject: 'user:x', subjectName: 'x' })).toBe('直接授权')
  })
})

describe('permission checks', () => {
  const me = { permissions: ['services.view'] as Permission[], envPermissions: { qa: ['pods.view', 'releases.create'] as Permission[] } }
  it('global and env scoped', () => {
    expect(can(me, 'services.view')).toBe(true)
    expect(can(me, 'audit.view')).toBe(false)
    expect(canEnv(me, 'pods.view', 'qa')).toBe(true)
    expect(canEnv(me, 'pods.view', 'prod')).toBe(false)
  })
})

describe('jira links', () => {
  it('only builds http(s) links', () => {
    expect(jiraHref('https://jira.example.com/', 'OPS-1')).toBe('https://jira.example.com/browse/OPS-1')
    expect(jiraHref('javascript:alert(1)', 'OPS-1')).toBeUndefined()
    expect(jiraHref('', 'OPS-1')).toBeUndefined()
  })
})
