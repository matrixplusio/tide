import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiFetch, buildUrl } from './api'
import { ErrCode } from './errcode'

function mockFetch(status: number, body: string, headers: Record<string, string> = {}) {
  const fn = vi.fn(async () => new Response(body, { status, headers }))
  vi.stubGlobal('fetch', fn)
  return fn
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('apiFetch', () => {
  it('unwraps data on code 0', async () => {
    const fn = mockFetch(200, JSON.stringify({ code: 0, msg: 'ok', data: { user: { sub: 'local:admin' } } }))
    await expect(apiFetch<{ user: { sub: string } }>('/api/v1/me')).resolves.toEqual({ user: { sub: 'local:admin' } })
    expect(fn).toHaveBeenCalledWith('/api/v1/me', expect.objectContaining({ method: 'GET', credentials: 'same-origin' }))
  })

  it('sends JSON bodies and query parameters', async () => {
    const fn = mockFetch(200, JSON.stringify({ code: 0, msg: 'ok', data: null }))
    await apiFetch('/api/v1/releases', { method: 'POST', body: { env: 'uat' }, query: { page: 2, env: '', status: undefined } })
    const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/v1/releases?page=2')
    expect(init.body).toBe('{"env":"uat"}')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
  })

  it('throws ApiError with fields on validation failure', async () => {
    mockFetch(
      400,
      JSON.stringify({ code: 1007, msg: '参数校验失败', data: { fields: [{ field: 'confirmPassword', msg: '两次输入的密码不一致' }] } }),
      { 'X-Request-ID': 'req-123' },
    )
    const err = await apiFetch('/api/v1/setup/admin', { method: 'POST', body: {} }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    const e = err as ApiError
    expect(e.code).toBe(ErrCode.Validation)
    expect(e.msg).toBe('参数校验失败')
    expect(e.httpStatus).toBe(400)
    expect(e.requestId).toBe('req-123')
    expect(e.fields).toEqual([{ field: 'confirmPassword', msg: '两次输入的密码不一致' }])
  })

  it('keeps data for 4004 check results', async () => {
    mockFetch(422, JSON.stringify({ code: 4004, msg: '上游连通性检查未通过，未保存', data: { results: [{ upstream: 'onprem', checks: [] }] } }))
    const e = (await apiFetch('/api/v1/settings/upstreams', { method: 'PUT', body: {} }).catch((x: unknown) => x)) as ApiError
    expect(e.code).toBe(ErrCode.UpstreamCheckFailed)
    expect(e.fields).toBeUndefined()
    expect(e.data).toEqual({ results: [{ upstream: 'onprem', checks: [] }] })
  })

  it('maps a non-JSON error body to an ApiError', async () => {
    mockFetch(502, '<html>Bad Gateway</html>', { 'X-Request-ID': 'req-9' })
    const e = (await apiFetch('/api/v1/overview').catch((x: unknown) => x)) as ApiError
    expect(e).toBeInstanceOf(ApiError)
    expect(e.code).toBe(ErrCode.Internal)
    expect(e.httpStatus).toBe(502)
    expect(e.msg).toBe('上游返回错误')
    expect(e.requestId).toBe('req-9')
  })

  it('treats a non-JSON 401 as unauthenticated', async () => {
    mockFetch(401, 'unauthorized')
    const e = (await apiFetch('/api/v1/me').catch((x: unknown) => x)) as ApiError
    expect(e.code).toBe(ErrCode.Unauthenticated)
  })

  it('maps network failures to an ApiError', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('Failed to fetch'))))
    const e = (await apiFetch('/api/v1/me').catch((x: unknown) => x)) as ApiError
    expect(e).toBeInstanceOf(ApiError)
    expect(e.httpStatus).toBe(0)
  })
})

describe('buildUrl', () => {
  it('skips empty values and appends to existing query', () => {
    expect(buildUrl('/a?x=1', { b: 'c d', e: null, f: false })).toBe('/a?x=1&b=c+d&f=false')
  })
})
