import { describe, expect, it } from 'vitest'
import { batchCandidates, batchSchema, loadBatchScope, loadLastBatch, mapBatchField, reuseLastBatch, saveBatchScope, saveLastBatch, matchPastedFreight, parsePaste, resolvePasteScope, shownAfterPaste, tidyPaste } from './batch'
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
    const { entries, problems } = parsePaste('b v1 x\nc v1\nc v2\nd v1 2 extra')
    expect(entries.map((e) => e.service)).toEqual(['c'])
    expect(problems.map((p) => [p.line, p.msg])).toEqual([
      [1, '格式应为「服务」「服务 tag [顺序]」或「镜像地址[:tag]」'],
      [3, 'c 重复'],
      [4, '格式应为「服务」「服务 tag [顺序]」或「镜像地址[:tag]」'],
    ])
  })
  // Columns copied from a spreadsheet are tab separated, and a tab is eight
  // columns wide in the box; what lands there is one space per gap.
  it('tidies pasted blanks into single spaces', () => {
    expect(tidyPaste('order-api\t\t20260917150452-938efb5c-0006  \r\nmedia-api\u3000\u00a0v1.4.0\t2')).toBe('order-api 20260917150452-938efb5c-0006\nmedia-api v1.4.0 2')
  })
  // Tide shows a version as "v1.4.0 <tag>"; copied from there, the tag wins.
  it('reads a version copied from Tide as its tag', () => {
    const { entries, problems } = parsePaste('order-api v1.4.0 20260917150452-938efb5c-0006\ntask-worker 1.2.0 20260917150455-6458e234-0007 2\nmedia-api v1.4.0 2')
    expect(problems).toEqual([])
    expect(entries.map((e) => [e.service, e.ref, e.sequence])).toEqual([
      ['order-api', '20260917150452-938efb5c-0006', undefined],
      ['task-worker', '20260917150455-6458e234-0007', '2'],
      ['media-api', 'v1.4.0', '2'],
    ])
  })
  // A name alone picks the service and leaves its version empty; so does an
  // image without a tag, including one behind a registry port.
  it('takes a bare service or an untagged image as the service alone', () => {
    const { entries, problems } = parsePaste('order-api\nregistry.example.com/shop-qa/media-api\nregistry.example.com:5000/shop/task-worker\nregistry.example.com:5000/shop/admin-web:v1.4.0')
    expect(problems).toEqual([])
    expect(entries.map((e) => [e.service, e.ref])).toEqual([
      ['order-api', ''],
      ['media-api', ''],
      ['task-worker', ''],
      ['admin-web', 'v1.4.0'],
    ])
  })
  it('requires one project and one type, deployed in the env', () => {
    const all = [svc('a', 'acme', 'backend'), svc('b', 'acme', 'backend'), svc('web', 'acme', 'frontend'), svc('gateway', 'globex', 'backend'), svc('c', 'acme', 'backend', ['prod'])]
    const e = (s: string, line: number) => ({ line, service: s, ref: 't' })
    expect(resolvePasteScope([e('a', 1), e('b', 2)], all, 'qa', role)).toMatchObject({ project: 'acme', type: 'backend', problems: [], renamed: [] })
    const mixed = resolvePasteScope([e('a', 1), e('web', 2), e('gateway', 3), e('c', 4), e('zzz', 5)], all, 'qa', role)
    expect(mixed.problems.map((p) => p.msg)).toEqual([
      'c 没有部署到 qa',
      '没有服务 zzz',
      '粘贴的服务分属多个项目（acme、globex），一次只能发布一个项目',
      '粘贴的服务包含多种类型（后端、前端），一次只能发布一种',
    ])
  })
  // globex's services are named globex-x after their Argo CD Application, while
  // people and image paths say x. The short name is matched within the
  // environment; one that several projects share needs the project picked.
  it('reads a short name as the service it is short for', () => {
    const named = (name: string, project: string, short: string, envs = ['qa']) => ({ ...svc(name, project, 'backend', envs), short })
    const all = [
      named('globex-billing', 'globex', 'billing'),
      named('globex-ledger', 'globex', 'ledger'),
      named('ledger', 'acme', 'ledger'),
      named('initech-dice', 'initech', 'dice'),
      named('umbrella-dice', 'umbrella', 'dice'),
      named('umbrella-only-prod', 'umbrella', 'only-prod', ['prod']),
    ]
    const e = (s: string, line: number) => ({ line, service: s, ref: 't' })
    const one = resolvePasteScope([e('billing', 1), e('globex-ledger', 2)], all, 'qa', role)
    expect(one.problems).toEqual([])
    expect(one.entries.map((x) => x.service)).toEqual(['globex-billing', 'globex-ledger'])
    expect(one.renamed).toEqual([{ from: 'billing', to: 'globex-billing' }])
    // The full name of another service wins over a short name.
    expect(resolvePasteScope([e('ledger', 1)], all, 'qa', role, 'globex').entries.map((x) => x.service)).toEqual(['ledger'])
    // Shared by two projects: the page's project settles it, else it is refused.
    expect(resolvePasteScope([e('dice', 1)], all, 'qa', role, 'umbrella').entries.map((x) => x.service)).toEqual(['umbrella-dice'])
    expect(resolvePasteScope([e('dice', 1)], all, 'qa', role, 'acme').problems.map((p) => p.msg)).toEqual(['dice 在多个项目里都有（initech-dice、umbrella-dice），请先选项目或写全名'])
    // The rest of the paste names the project, whatever the page has picked.
    expect(resolvePasteScope([e('dice', 1), e('umbrella-only-prod', 2)], all, 'prod', role, 'acme').problems.map((p) => p.msg)).toEqual(['没有服务 dice'])
    const withOther = [...all, named('umbrella-api', 'umbrella', 'api')]
    expect(resolvePasteScope([e('dice', 1), e('api', 2)], withOther, 'qa', role, 'acme').entries.map((x) => x.service)).toEqual(['umbrella-dice', 'umbrella-api'])
    // Not deployed to the environment: not a match at all.
    expect(resolvePasteScope([e('only-prod', 1)], all, 'qa', role).problems.map((p) => p.msg)).toEqual(['没有服务 only-prod'])
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

describe('after a paste', () => {
  const imported = { 'order-api': { freight: 'f1' }, 'cart-web': { freight: 'f2' } }
  it('shows only the pasted services until asked for all', () => {
    expect(shownAfterPaste(imported, true, 'order-api')).toBe(true)
    expect(shownAfterPaste(imported, true, 'stock-api')).toBe(false)
    expect(shownAfterPaste(imported, false, 'stock-api')).toBe(true)
  })
  it('hides nothing when nothing was pasted', () => {
    expect(shownAfterPaste(null, true, 'stock-api')).toBe(true)
  })
})

describe('batch memory', () => {
  // Only the environment and kind come back by themselves; anything that is
  // not a known value is dropped rather than trusted.
  it('keeps the environment and kind and drops what it does not recognise', () => {
    localStorage.clear()
    expect(loadBatchScope()).toEqual({})
    saveBatchScope({ env: 'qa', kind: 'sync' })
    expect(loadBatchScope()).toEqual({ env: 'qa', kind: 'sync' })
    localStorage.setItem('tide.batch.scope', JSON.stringify({ env: 3, kind: 'delete-everything' }))
    expect(loadBatchScope()).toEqual({ env: undefined, kind: undefined })
    localStorage.setItem('tide.batch.scope', 'not json')
    expect(loadBatchScope()).toEqual({})
  })

  // The last batch is kept per environment and brings back its services and
  // order, never its versions; services gone from the environment, moved to
  // another project or out of reach are named instead of silently dropped.
  it('reuses the last batch of the environment without its versions', () => {
    localStorage.clear()
    const last = { at: '2026-10-15T11:00:00Z', kind: 'image' as const, project: 'acme', type: 'backend', services: [{ service: 'a', sequence: '1' }, { service: 'b', sequence: '2' }, { service: 'gone', sequence: '1' }, { service: 'gateway', sequence: '1' }, { service: 'c', sequence: '1' }] }
    saveLastBatch('qa', last)
    saveLastBatch('prod', { ...last, project: 'globex' })
    expect(loadLastBatch('qa')).toEqual(last)
    expect(loadLastBatch('prod')?.project).toEqual('globex')
    expect(loadLastBatch('dev')).toBeUndefined()
    const all = [svc('a', 'acme', 'backend'), svc('b', 'acme', 'backend'), svc('gateway', 'globex', 'backend'), svc('c', 'acme', 'backend')]
    const { picked, missing } = reuseLastBatch(last, all, 'qa', (s) => s.name !== 'c')
    expect(picked).toEqual({ a: { freight: '', sequence: '1' }, b: { freight: '', sequence: '2' } })
    expect(missing).toEqual(['gone', 'gateway', 'c'])
    localStorage.setItem('tide.batch.last', JSON.stringify({ qa: { ...last, services: [{ service: 'a', sequence: 'x' }] } }))
    expect(loadLastBatch('qa')).toBeUndefined()
  })
})
