import { ErrCode } from './errcode'
import { currentLocale, i18n } from './i18n'
import { logger } from './logger'

// Thin client for /api/v1. Session is an HttpOnly cookie; nothing sensitive
// is stored in JS. Every response is an envelope { code, msg, data }.

export interface FieldError {
  field: string
  msg: string
}

export class ApiError extends Error {
  readonly code: number
  readonly msg: string
  readonly fields?: FieldError[]
  readonly data: unknown
  readonly httpStatus: number
  readonly requestId?: string

  constructor(init: { code: number; msg: string; fields?: FieldError[]; data?: unknown; httpStatus: number; requestId?: string }) {
    super(init.msg)
    this.name = 'ApiError'
    this.code = init.code
    this.msg = init.msg
    this.fields = init.fields
    this.data = init.data ?? null
    this.httpStatus = init.httpStatus
    this.requestId = init.requestId
  }
}

export function isApiError(e: unknown, code?: number): e is ApiError {
  return e instanceof ApiError && (code === undefined || e.code === code)
}

export type QueryValue = string | number | boolean | null | undefined

export interface ApiOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  body?: unknown
  query?: Record<string, QueryValue>
  signal?: AbortSignal
}

export function buildUrl(path: string, query?: Record<string, QueryValue>): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === '') continue
    params.set(k, String(v))
  }
  const qs = params.toString()
  return qs ? `${path}${path.includes('?') ? '&' : '?'}${qs}` : path
}

interface Envelope {
  code: number
  msg: string
  data: unknown
}

// The server sends a message with every envelope; these cover the cases where
// it cannot (a proxy's own error page, a response that is not an envelope).
function fallbackText(status: number): string {
  const key = fallbackMsg[status]
  return key ? i18n.t(key) : i18n.t('http.failed')
}

function isEnvelope(v: unknown): v is Envelope {
  return typeof v === 'object' && v !== null && typeof (v as { code?: unknown }).code === 'number' && 'data' in v
}

function parseFields(data: unknown): FieldError[] | undefined {
  if (typeof data !== 'object' || data === null) return undefined
  const raw = (data as { fields?: unknown }).fields
  if (!Array.isArray(raw)) return undefined
  return raw
    .filter((f): f is FieldError => typeof f === 'object' && f !== null && typeof f.field === 'string' && typeof f.msg === 'string')
    .map((f) => ({ field: f.field, msg: f.msg }))
}

const fallbackMsg: Record<number, string> = {
  400: 'http.400',
  401: 'http.401',
  403: 'http.403',
  404: 'http.404',
  413: 'http.413',
  429: 'http.429',
  502: 'http.502',
  503: 'http.503',
  504: 'http.504',
}

export async function apiFetch<T>(path: string, opts: ApiOptions = {}): Promise<T> {
  const method = opts.method ?? 'GET'
  const url = buildUrl(path, opts.query)
  const hasBody = opts.body !== undefined
  let res: Response
  try {
    res = await fetch(url, {
      method,
      credentials: 'same-origin',
      // CSRF: every non-GET request is JSON (server rejects anything else).
      // Ask the server for the language the UI is in, rather than letting the
      // browser's own preference answer: a message next to a Chinese label
      // should not arrive in English because Chrome is set to en-US.
      headers: {
        Accept: 'application/json',
        'Accept-Language': currentLocale(),
        ...(method !== 'GET' ? { 'Content-Type': 'application/json' } : {}),
      },
      body: hasBody ? JSON.stringify(opts.body) : method !== 'GET' ? '{}' : undefined,
      signal: opts.signal,
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    logger.warn('network error', { path, method })
    throw new ApiError({ code: ErrCode.Internal, msg: i18n.t('common.networkError'), httpStatus: 0 })
  }

  const requestId = res.headers.get('X-Request-ID') ?? undefined
  const text = await res.text()
  let parsed: unknown = undefined
  try {
    parsed = text ? JSON.parse(text) : undefined
  } catch {
    parsed = undefined
  }

  if (!isEnvelope(parsed)) {
    logger.warn('non-envelope response', { path, method, status: res.status, request_id: requestId })
    const msg = fallbackMsg[res.status] ? fallbackText(res.status) : res.ok ? i18n.t('http.badShape') : i18n.t('http.failedWithStatus', { status: res.status })
    throw new ApiError({ code: res.status === 401 ? ErrCode.Unauthenticated : ErrCode.Internal, msg, httpStatus: res.status, requestId })
  }

  if (parsed.code !== ErrCode.OK) {
    if (parsed.code === ErrCode.Internal) logger.error('api internal error', { path, method, request_id: requestId })
    throw new ApiError({
      code: parsed.code,
      msg: parsed.msg || fallbackText(res.status),
      fields: parsed.code === ErrCode.Validation ? parseFields(parsed.data) : undefined,
      data: parsed.data,
      httpStatus: res.status,
      requestId,
    })
  }
  return parsed.data as T
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.msg
  if (e instanceof Error) return e.message
  return String(e)
}
