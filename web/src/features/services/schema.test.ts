import { describe, expect, it } from 'vitest'
import { mapSyncField, promoteSchema, requiredFields, restartSchema, syncSchema } from './schema'

const app = { jiraRequired: ['prod'], reasonRequired: ['qa', 'prod'] }
const freight = 'f'.repeat(40)

describe('required release fields follow the release policy', () => {
  it('resolves per environment', () => {
    expect(requiredFields(app, 'prod')).toEqual({ jira: true, reason: true })
    expect(requiredFields(app, 'qa')).toEqual({ jira: false, reason: true })
    expect(requiredFields(app, 'dev')).toEqual({ jira: false, reason: false })
    expect(requiredFields(undefined, 'prod')).toEqual({ jira: false, reason: false })
  })

  it('lets optional fields be empty but still checks what is typed', () => {
    const dev = promoteSchema(requiredFields(app, 'dev'))
    expect(dev.safeParse({ freight, title: '', jiraTicket: '', reason: '', withConfig: false }).success).toBe(true)
    expect(dev.safeParse({ freight, jiraTicket: 'not a ticket', reason: '', withConfig: false }).success).toBe(false)
    expect(dev.safeParse({ freight, title: '', jiraTicket: '', reason: 'abc', withConfig: false }).success).toBe(false)
  })

  it('requires them where configured', () => {
    const prod = restartSchema(requiredFields(app, 'prod'))
    const r = prod.safeParse({ title: '', jiraTicket: '', reason: '' })
    expect(r.success).toBe(false)
    expect(r.error?.issues.map((i) => i.path[0]).sort()).toEqual(['jiraTicket', 'reason'])
    expect(prod.safeParse({ title: '', jiraTicket: 'OPS-1', reason: '重载配置项' }).success).toBe(true)
  })

  it('requires consent before a sync deletes resources', () => {
    const req = requiredFields(app, 'dev')
    const base = { title: '', jiraTicket: '', reason: '', restart: false }
    expect(syncSchema(req, 0).safeParse({ ...base, prune: false }).success).toBe(true)
    const r = syncSchema(req, 2).safeParse({ ...base, prune: false })
    expect(r.error?.issues.map((i) => i.path[0])).toEqual(['prune'])
    expect(syncSchema(req, 2).safeParse({ ...base, prune: true }).success).toBe(true)
    expect(mapSyncField('items.0.prune')).toBe('prune')
    expect(mapSyncField('items.0.service')).toBeNull()
  })
})
