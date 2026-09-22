import { describe, expect, it } from 'vitest'
import { dimensionOptions, dimensionValueName, matchesSearch } from './catalog'
import type { Dimension, Service } from './types'

const dim: Dimension = { key: 'role', name: '类型', label: 'example.com/role', values: [{ value: 'frontend', name: '前端' }, { value: 'backend', name: '' }] }
const svc = (role?: string): Service => ({ name: 'x', domain: 'd', dimensions: role ? { role } : null, envs: {} })

describe('dimensionOptions', () => {
  it('keeps configured order and names, then appends discovered values', () => {
    expect(dimensionOptions(dim, [svc('worker'), svc('backend'), svc(), svc('api'), svc('worker')])).toEqual([
      ['frontend', '前端'],
      ['backend', 'backend'],
      ['api', 'api'],
      ['worker', 'worker'],
    ])
  })
  it('works without configured values', () => {
    expect(dimensionOptions({ ...dim, values: null }, [svc('b'), svc('a')])).toEqual([
      ['a', 'a'],
      ['b', 'b'],
    ])
  })
  it('names values', () => {
    expect(dimensionValueName(dim, 'frontend')).toBe('前端')
    expect(dimensionValueName(dim, 'backend')).toBe('backend')
    expect(dimensionValueName(dim, undefined)).toBe('')
  })
})

describe('matchesSearch', () => {
  it.each([
    ['api-server', '', true],
    ['api-server', 'server', true],
    ['api-server', 'API', true],
    ['api-server', 'api server', true],
    ['api-server', '  server  ', true],
    ['api-server', 'sr', false],
    ['api-gateway', 'aey', false],
    ['pc-frontend', 'api', false],
  ])('%s ~ %j → %s', (name, q, want) => {
    expect(matchesSearch(name, q)).toBe(want)
  })
})
