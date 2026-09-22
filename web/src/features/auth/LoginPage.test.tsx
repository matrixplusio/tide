import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { LoginPage } from './LoginPage'

type Call = { url: string; body: Record<string, string> | null }

/** A tiny fake server: login answers come from `logins`, challenges hand out numbered captchas. */
function fakeServer(logins: { code: number; msg: string }[]) {
  const calls: Call[] = []
  let issued = 0
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, string>) : null
      calls.push({ url, body })
      const env = (code: number, msg: string, data: unknown, status: number) => new Response(JSON.stringify({ code, msg, data }), { status })
      if (url.startsWith('/api/v1/auth/methods')) return env(0, 'ok', { initialized: true, sso: false }, 200)
      if (url.startsWith('/api/v1/auth/challenge')) {
        issued++
        return env(0, 'ok', { captchaRequired: true, captchaId: `00000000-0000-4000-8000-00000000000${issued}`, captchaImage: `data:image/png;base64,IMG${issued}` }, 200)
      }
      if (url.startsWith('/api/v1/auth/login')) {
        const next = logins.shift() ?? { code: 0, msg: 'ok' }
        return next.code === 0 ? env(0, 'ok', { user: { sub: 'local:ops' } }, 200) : env(next.code, next.msg, null, next.code === 2001 ? 401 : 400)
      }
      return env(1004, 'nf', null, 404)
    }),
  )
  return calls
}

afterEach(() => vi.unstubAllGlobals())

function renderLogin() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/login']}>
        <LoginPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('login captcha', () => {
  it('shows no captcha until the server asks for one, then requires and sends it', async () => {
    const calls = fakeServer([
      { code: 2001, msg: '用户名或密码错误' },
      { code: 2016, msg: '验证码错误或已过期，请重新输入' },
      { code: 0, msg: 'ok' },
    ])
    const user = userEvent.setup()
    renderLogin()
    expect(screen.queryByLabelText(/验证码/, { selector: 'input' })).toBeNull()

    await user.type(screen.getByLabelText(/用户名/, { selector: 'input' }), 'ops')
    await user.type(screen.getByLabelText(/密码/, { selector: 'input' }), 'wrong password!!')
    await user.click(screen.getByRole('button', { name: '登录' }))

    // Wrong password: message shown, password cleared and focused, captcha appears.
    expect(await screen.findByText('用户名或密码错误')).toBeTruthy()
    const captchaInput = await screen.findByLabelText(/验证码/, { selector: 'input' })
    expect((screen.getByLabelText(/密码/, { selector: 'input' }) as HTMLInputElement).value).toBe('')
    expect(screen.getByAltText('验证码图片').getAttribute('src')).toBe('data:image/png;base64,IMG1')

    // Captcha is now required client-side.
    await user.type(screen.getByLabelText(/密码/, { selector: 'input' }), 'correct horse battery')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByText('请输入验证码')).toBeTruthy()
    expect(calls.filter((c) => c.url.startsWith('/api/v1/auth/login'))).toHaveLength(1)
    // The previous attempt's banner must not linger next to the new field error.
    expect(screen.queryByText('用户名或密码错误')).toBeNull()

    // Wrong captcha: field error, a fresh image replaces the used one.
    await user.type(captchaInput, 'abcde')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByText('验证码错误或已过期，请重新输入')).toBeTruthy()
    await waitFor(() => expect(screen.getByAltText('验证码图片').getAttribute('src')).toBe('data:image/png;base64,IMG2'))
    const second = calls.filter((c) => c.url.startsWith('/api/v1/auth/login'))[1]
    expect(second?.body).toMatchObject({ captchaId: '00000000-0000-4000-8000-000000000001', captchaCode: 'abcde' })

    // Right captcha goes out with the new id.
    await user.type(screen.getByLabelText(/验证码/, { selector: 'input' }), 'k7m2x')
    await user.click(screen.getByRole('button', { name: '登录' }))
    await waitFor(() => expect(calls.filter((c) => c.url.startsWith('/api/v1/auth/login'))).toHaveLength(3))
    expect(calls.filter((c) => c.url.startsWith('/api/v1/auth/login'))[2]?.body).toMatchObject({ captchaId: '00000000-0000-4000-8000-000000000002', captchaCode: 'k7m2x' })
  })

  it('refreshes the image when clicked', async () => {
    fakeServer([{ code: 2015, msg: '请输入验证码' }])
    const user = userEvent.setup()
    renderLogin()
    await user.type(screen.getByLabelText(/用户名/, { selector: 'input' }), 'ops')
    await user.type(screen.getByLabelText(/密码/, { selector: 'input' }), 'correct horse battery')
    await user.click(screen.getByRole('button', { name: '登录' }))
    expect(await screen.findByText('请输入验证码')).toBeTruthy()
    await user.click(screen.getByRole('button', { name: '换一张验证码' }))
    await waitFor(() => expect(screen.getByAltText('验证码图片').getAttribute('src')).toBe('data:image/png;base64,IMG2'))
  })
})
