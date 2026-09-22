import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MeContext } from '../../app/session'
import { ToastProvider } from '../../components/ui'
import type { Me } from '../../lib/types'
import { ProfilePage } from './ProfilePage'

const now = new Date().toISOString()

function meOf(method: 'local' | 'oidc'): Me {
  return {
    user: { id: 7, sub: method === 'local' ? 'local:alice' : 'oidc:alice', username: method === 'local' ? 'alice' : undefined, name: 'Alice', groups: ['sre'], localGroups: [], method },
    scopedGrants: [],
  canView: { 'services.view': true },
  permissions: ['services.view'],
    envPermissions: { prod: ['releases.create'] },
    canOperate: { prod: true },
    envOrder: ['prod'],
    environments: [{ name: 'prod', displayName: '生产', tier: 'production', description: '' }],
    app: { siteName: 'Tide', confirmReadSeconds: 10, activeFreezes: [] },
  }
}

function server() {
  const calls: { url: string; method: string; body: unknown }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body ? JSON.parse(String(init.body)) : null })
      const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, msg: 'ok', data }), { status: 200 })
      if (url.startsWith('/api/v1/me/sessions')) return ok({ items: [{ id: 'a'.repeat(64), current: true, createdAt: now, expiresAt: now }] })
      if (url.startsWith('/api/v1/me/bindings'))
        return ok({ items: [{ id: 1, roleId: 'operator', roleName: '发布者', subject: 'group:sre', subjectName: 'sre', envs: ['tier:production'], createdAt: now, createdBy: 'x', via: 'group' }] })
      if (url.startsWith('/api/v1/me/activity')) return ok({ items: [], total: 0, page: 1, page_size: 20 })
      if (url.startsWith('/api/v1/me/password'))
        return new Response(JSON.stringify({ code: 1007, msg: '参数校验失败', data: { fields: [{ field: 'currentPassword', msg: '当前密码不正确' }] } }), { status: 400 })
      return new Response(JSON.stringify({ code: 1004, msg: 'nf', data: null }), { status: 404 })
    }),
  )
  return calls
}

afterEach(() => vi.unstubAllGlobals())

function renderProfile(method: 'local' | 'oidc') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <MemoryRouter initialEntries={['/profile']}>
          <MeContext.Provider value={meOf(method)}>
            <ProfilePage />
          </MeContext.Provider>
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('profile', () => {
  it('validates the password form on the client, then shows server field errors', async () => {
    const calls = server()
    const user = userEvent.setup()
    renderProfile('local')
    const submit = screen.getByRole('button', { name: '修改密码' })
    await user.click(submit)
    expect(await screen.findByText('请输入当前密码')).toBeTruthy()
    expect(screen.getByText('请输入密码')).toBeTruthy()
    expect(screen.getByText('请再次输入密码')).toBeTruthy()
    expect(calls.some((c) => c.url.startsWith('/api/v1/me/password'))).toBe(false)

    const current = screen.getByLabelText(/当前密码/, { selector: 'input' })
    const next = screen.getByLabelText(/^新密码/, { selector: 'input' })
    const confirm = screen.getByLabelText(/确认新密码/, { selector: 'input' })
    await user.type(current, 'old secret phrase')
    await user.type(next, 'alice-horse-battery')
    expect(await screen.findByText('密码不能包含用户名')).toBeTruthy()
    await user.clear(next)
    await user.type(next, 'correct horse battery')
    await user.type(confirm, 'correct horse batter')
    expect(await screen.findByText('两次输入的密码不一致')).toBeTruthy()
    await user.type(confirm, 'y')
    await waitFor(() => expect(screen.queryByText('两次输入的密码不一致')).toBeNull())

    await user.click(submit)
    expect(await screen.findByText('当前密码不正确')).toBeTruthy()
    expect(calls.find((c) => c.url.startsWith('/api/v1/me/password'))?.body).toEqual({ currentPassword: 'old secret phrase', newPassword: 'correct horse battery', confirmPassword: 'correct horse battery' })
    // Input is kept after a failed submit.
    expect((next as HTMLInputElement).value).toBe('correct horse battery')
  })

  it('SSO accounts cannot edit their name or password here', async () => {
    server()
    renderProfile('oidc')
    expect(screen.getByText(/由身份提供方管理/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: '修改密码' })).toBeNull()
    expect(screen.queryByRole('button', { name: '保存姓名' })).toBeNull()
    // Effective bindings render their scope in words.
    expect(await screen.findByText('通过组 sre · 生产类环境')).toBeTruthy()
  })
})
