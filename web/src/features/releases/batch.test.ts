import { describe, expect, it } from 'vitest'
import { batchCandidates, batchSchema, mapBatchField, matchPastedFreight, parsePaste, resolvePasteScope } from './batch'
import type { Dimension, Service } from '../../lib/types'

const dep = { env: 'qa' } as never
const svc = (name: string, project: string, role: string, envs: string[] = ['qa']): Service => ({
  name,
  project,
  domain: 'd',
  dimensions: { role },
  envs: Object.fromEntries(envs.map((e) => [e, dep])),
})
const role: Dimension = { key: 'role', name: '类型', label: 'x/role', values: [{ value: 'backend', name: '后端' }, { value: 'frontend', name: '前端' }] }

describe('batch releases', () => {
  it('lists only one project, one type, deployed in the env', () => {
    const all = [svc('b', 'acme', 'backend'), svc('a', 'acme', 'backend'), svc('web', 'acme', 'frontend'), svc('gateway', 'globex', 'backend'), svc('c', 'acme', 'backend', ['prod']), svc('x', '', 'backend')]
    expect(batchCandidates(all, 'qa', 'acme', role, 'backend').map((s) => s.name)).toEqual(['a', 'b'])
    expect(batchCandidates(all, 'qa', 'acme', undefined, '').map((s) => s.name)).toEqual(['a', 'b', 'web'])
    expect(batchCandidates(all, 'qa', '', role, 'backend')).toEqual([])
  })

  it('validates the selection', () => {
    const rows = [
      { service: 'a', selected: true, freight: '', sequence: '1' },
      { service: 'b', selected: true, freight: 'f', sequence: '0' },
      { service: 'c', selected: false, freight: '', sequence: 'x' },
    ]
    const r = batchSchema({ jira: false, reason: false }, 'image').safeParse({ rows, title: '', jiraTicket: '', reason: '', prune: false, restart: false, withConfig: false })
    expect(r.success).toBe(false)
    expect(r.error?.issues.map((i) => i.path.join('.')).sort()).toEqual(['rows.0.freight', 'rows.1.sequence'])
    const none = batchSchema({ jira: false, reason: false }, 'restart').safeParse({ rows: rows.map((x) => ({ ...x, selected: false })), title: '', jiraTicket: '', reason: '', prune: false, restart: false, withConfig: false })
    expect(none.error?.issues[0]?.path.join('.')).toBe('rows')
    const restart = batchSchema({ jira: false, reason: false }, 'restart').safeParse({ rows: [{ ...rows[0]!, freight: '' }], title: '', jiraTicket: '', reason: '', prune: false, restart: false, withConfig: false })
    expect(restart.success).toBe(true)
    const sync = batchSchema({ jira: false, reason: false }, 'sync').safeParse({ rows: [{ ...rows[0]!, freight: '' }], title: '', jiraTicket: '', reason: '', prune: true, restart: false, withConfig: false })
    expect(sync.success).toBe(true)
  })

  it('maps server item paths to rows', () => {
    const rows = [
      { service: 'a', selected: false, freight: '', sequence: '1' },
      { service: 'b', selected: true, freight: '', sequence: '1' },
      { service: 'c', selected: true, freight: '', sequence: '1' },
    ]
    expect(mapBatchField(rows, 'items.1.freight')).toBe('rows.2.freight')
    expect(mapBatchField(rows, 'items.0.service')).toBeNull()
    expect(mapBatchField(rows, 'reason')).toBe('reason')
    expect(mapBatchField(rows, 'items.1.prune')).toBe('prune')
  })
})

describe('paste import', () => {
  it('parses service + tag lines in several shapes', () => {
    const { entries, problems } = parsePaste(`
# backends
order-api 20260917150452-938efb5c-0006
task-worker\tv1.4.0\t2
registry.example.com/acme-qa/media-api:20260917134251-fe5db5be-0002
admin-web:v1.4.0
gateway-backend,v2.0.0
`)
    expect(problems).toEqual([])
    expect(entries.map((e) => [e.service, e.ref, e.sequence])).toEqual([
      ['order-api', '20260917150452-938efb5c-0006', undefined],
      ['task-worker', 'v1.4.0', '2'],
      ['media-api', '20260917134251-fe5db5be-0002', undefined],
      ['admin-web', 'v1.4.0', undefined],
      ['gateway-backend', 'v2.0.0', undefined],
    ])
  })
  it('reports malformed and duplicate lines with their line numbers', () => {
    const { entries, problems } = parsePaste('a\nb v1 x\nc v1\nc v2')
    expect(entries.map((e) => e.service)).toEqual(['c'])
    expect(problems.map((p) => [p.line, p.msg])).toEqual([
      [1, '格式应为「服务 tag [顺序]」或「镜像地址:tag」'],
      [2, '格式应为「服务 tag [顺序]」或「镜像地址:tag」'],
      [4, 'c 重复'],
    ])
  })
  it('requires one project and one type, deployed in the env', () => {
    const all = [svc('a', 'acme', 'backend'), svc('b', 'acme', 'backend'), svc('web', 'acme', 'frontend'), svc('gateway', 'globex', 'backend'), svc('c', 'acme', 'backend', ['prod'])]
    const e = (s: string, line: number) => ({ line, service: s, ref: 't' })
    expect(resolvePasteScope([e('a', 1), e('b', 2)], all, 'qa', role)).toEqual({ project: 'acme', type: 'backend', problems: [] })
    const mixed = resolvePasteScope([e('a', 1), e('web', 2), e('gateway', 3), e('c', 4), e('zzz', 5)], all, 'qa', role)
    expect(mixed.problems.map((p) => p.msg)).toEqual([
      'c 没有部署到 qa',
      '没有服务 zzz',
      '粘贴的服务分属多个项目（acme、globex），一次只能发布一个项目',
      '粘贴的服务包含多种类型（后端、前端），一次只能发布一种',
    ])
  })
  it('matches a pasted ref to freight by tag, version or freight prefix', () => {
    const items = [
      { freight: 'aaaaaaa111', tag: '2026-0006', version: 'v1.4.0', available: true, current: false },
      { freight: 'bbbbbbb222', tag: '2026-0005', version: 'v1.3.0', available: true, current: true },
      { freight: 'ccccccc333', tag: '2026-0007', available: false, current: false },
    ]
    expect(matchPastedFreight(items, 'svc', '2026-0006', 'qa')).toEqual({ freight: 'aaaaaaa111' })
    expect(matchPastedFreight(items, 'svc', 'v1.4.0', 'qa')).toEqual({ freight: 'aaaaaaa111' })
    expect(matchPastedFreight(items, 'svc', 'aaaaaaa', 'qa')).toEqual({ freight: 'aaaaaaa111' })
    expect(matchPastedFreight(items, 'svc', 'v1.3.0', 'qa').msg).toBe('svc 的 v1.3.0 已经在 qa 运行')
    expect(matchPastedFreight(items, 'svc', '2026-0007', 'qa').msg).toBe('svc 的 2026-0007 还不能发布到 qa（未在上游验证）')
    expect(matchPastedFreight(items, 'svc', 'nope', 'qa').msg).toBe('svc 没有 tag 或版本为 nope 的制品')
  })
})
