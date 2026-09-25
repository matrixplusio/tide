import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ToastProvider } from '../components/ui'
import App from './App'

const now = new Date().toISOString()
const dep = { service: 'web-portal', env: 'uat', domain: 'acme-user', upstream: 'onprem', app: 'web-portal-uat', namespace: 'acme-user', sync: 'Synced', health: 'Healthy', images: [], tag: '20260916151427-46b7619f-0022', digest: 'sha256:' + 'a'.repeat(64), freight: 'f'.repeat(40), kargoProject: 'acme', kargoStage: 'uat', autoPromotion: false, autoHeld: false, since: now }
const release = { id: 'REL-20260917-001', title: 'web-portal → uat', env: 'uat', jiraTicket: 'OPS-1', reason: 'fix', createdBy: 'local:admin', createdByName: 'admin', status: 'confirming', createdAt: now, now, confirmableAt: now, expiresAt: now, items: [{ id: 1, releaseId: 'REL-20260917-001', kind: 'image', sequence: 1, status: 'planned', payload: { upstream: 'onprem', project: 'acme', stage: 'uat', service: 'web-portal', env: 'uat', freight: 'f'.repeat(40), image: 'x', from: null, to: { digest: 'sha256:' + 'b'.repeat(64), tag: 't' } } }] }
const live = { app: 'a', sync: 'Synced', health: 'Healthy', resources: [], pods: [], rollouts: [] }

const ALL = ['services.view', 'releases.view', 'audit.view', 'users.manage', 'roles.manage', 'environments.manage', 'notifications.manage', 'settings.manage', 'pods.view', 'releases.create', 'releases.restart', 'releases.cancel_any']
const environments = [
  { name: 'qa', displayName: '测试', tier: 'testing', description: '' },
  { name: 'uat', displayName: '预发布', tier: 'staging', description: '' },
]
const app = { siteName: 'Tide', confirmReadSeconds: 10, activeFreezes: [], jiraRequired: ['uat'], reasonRequired: ['qa', 'uat'] }
const adminMe = {
  user: { id: 1, sub: 'local:admin', username: 'admin', name: 'Admin', groups: [], localGroups: [], method: 'local' },
  permissions: ALL,
  canView: { 'services.view': true, 'releases.view': true, 'audit.view': true },
  scopedGrants: [{ permissions: ALL, envs: ['qa', 'uat'], projects: [], types: [] }],
  envPermissions: { qa: ['pods.view', 'releases.create', 'releases.cancel_any'], uat: ['pods.view', 'releases.create', 'releases.cancel_any'] },
  canOperate: { qa: true, uat: true },
  envOrder: ['qa', 'uat'],
  environments,
  app,
}
let me: unknown = adminMe
const userRow = { id: 1, sub: 'local:admin', method: 'local', username: 'admin', name: 'Admin', groups: [], localGroups: ['ops'], roles: [{ id: 'admin', name: '管理员' }], disabled: false, createdAt: now }
const binding = { id: 1, roleId: 'admin', roleName: '管理员', subject: 'user:local:admin', subjectName: 'Admin', envs: ['*'], createdAt: now, createdBy: 'setup' }

const routes: [RegExp, unknown][] = [
  [/\/api\/v1\/me$/, () => me],
  [/\/api\/v1\/me\/sessions$/, { items: [{ id: 'a'.repeat(64), current: true, createdAt: now, expiresAt: now, clientIp: '10.0.0.1', userAgent: 'Safari' }] }],
  [/\/api\/v1\/me\/bindings$/, { items: [{ ...binding, via: 'user' }] }],
  [/\/api\/v1\/me\/activity$/, { items: [], total: 0, page: 1, page_size: 20 }],
  [/\/api\/v1\/overview/, { upstreams: [], upstreamError: { code: 4001, msg: '还没有配置上游' }, envOrder: ['qa', 'uat'], envStats: {}, unhealthy: [], drifted: [], driftedCount: 0, serviceCount: 0, domainCount: 0, inFlight: [], myInFlight: 0, recentFailed: [], recent: [release], today: { total: 0, succeeded: 0 } }],
  [/\/api\/v1\/services\/[^/]+\/envs\/[^/]+\/candidates/, { deployment: dep, upstreamStages: ['qa'], items: [], availableCount: 0, totalCount: 0 }],
  [/\/api\/v1\/services\/[^/]+\/envs\/[^/]+$/, { deployment: dep, live, canOperate: true, releases: [], promotions: [] }],
  [/\/api\/v1\/services\/[^/?]+$/, { service: { name: 'web-portal', domain: 'acme-user', envs: { uat: dep } }, releases: [] }],
  [/\/api\/v1\/services/, { services: [{ name: 'web-portal', domain: 'acme-user', envs: { uat: dep } }], envOrder: ['qa', 'uat'], upstreams: [], at: now, inFlight: {} }],
  [/\/api\/v1\/releases\/REL/, { release, live: [] }],
  [/\/api\/v1\/releases/, { items: [release], total: 1, page: 1, page_size: 20 }],
  [/\/api\/v1\/audit/, { items: [{ id: 1, at: now, actor: 'local:admin', actorName: 'admin', action: 'auth.login', detail: {} }], total: 1, page: 1, page_size: 50 }],
  [
    /\/api\/v1\/settings$/,
    {
      upstreams: null,
      environments: { items: [{ name: 'qa', displayName: '测试', tier: 'testing', description: '', upstream: '' }] },
      notify: { channels: [], rules: [] },
      oidc: null,
      security: { sessionTtlMinutes: 60, sessionMaxHours: 12, loginWindowMinutes: 15, captchaAfterUserFailures: 3, captchaAfterIpFailures: 5, lockAfterUserFailures: 10, lockAfterIpFailures: 30, localLoginAdminsOnly: false },
      release: { confirmReadSeconds: 10, confirmTtlMinutes: 10, executeTimeoutMinutes: 15, minSoakMinutes: 30, multiVersionJump: 3, jiraBaseUrl: '', jiraProjects: [], freezes: [] },
      system: { siteName: 'Tide', baseUrl: '', announcement: { enabled: false, level: 'info', text: '' } },
    },
  ],
  [/\/api\/v1\/users\/\d+$/, { user: userRow, bindings: [{ ...binding, via: 'user' }], sessionCount: 1 }],
  [/\/api\/v1\/users$/, { items: [userRow], total: 1, page: 1, page_size: 20 }],
  [/\/api\/v1\/groups\/[^/]+\/members$/, { items: [userRow] }],
  [/\/api\/v1\/groups$/, { items: [{ name: 'ops', description: '运维', memberCount: 1, createdAt: now }] }],
  [/\/api\/v1\/roles$/, { items: [{ id: 'admin', name: '管理员', description: '', builtin: true, permissions: ['*'], bindingCount: 1 }] }],
  [/\/api\/v1\/role-bindings$/, { items: [binding] }],
  [/\/api\/v1\/rbac\/permissions$/, { items: [], tiers: [] }],
]

function mockApi() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      const path = url.split('?')[0] ?? url
      const hit = routes.find(([re]) => re.test(path))
      const data = typeof hit?.[1] === 'function' ? (hit[1] as () => unknown)() : hit?.[1]
      return new Response(JSON.stringify(hit ? { code: 0, msg: 'ok', data } : { code: 1004, msg: '资源不存在', data: null }), { status: hit ? 200 : 404 })
    }),
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  me = adminMe
})

function renderAt(path: string) {
  mockApi()
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, refetchInterval: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <MemoryRouter initialEntries={[path]}>
          <App />
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  )
}

describe('app smoke', () => {
  it.each([
    ['/', '最近发布'],
    ['/services', 'web-portal'],
    ['/services/web-portal', '各环境'],
    ['/services/web-portal/envs/uat', '发布信息'],
    ['/releases', 'REL-20260917-001'],
    ['/releases/REL-20260917-001', '进度'],
    ['/audit', 'Signed in'],
    ['/settings', '用户'],
    ['/admin/users', '新建本地账号'],
    ['/admin/users/1', '生效的授权'],
    ['/admin/groups', '新建组'],
    ['/admin/groups/ops', '添加成员'],
    ['/admin/roles', '新建角色'],
    ['/admin/roles/admin', '全部权限，包括以后新增的'],
    ['/admin/environments', '添加环境'],
    ['/admin/upstreams', '仅测试连通性'],
    ['/admin/release', '阅读清单时间'],
    ['/admin/notify', '还没有通知渠道'],
    ['/admin/security', '账号锁定阈值'],
    ['/admin/sso', '回调地址'],
    ['/admin/general', '全站公告'],
    ['/profile', '登录会话'],
  ])('%s renders', async (path, text) => {
    renderAt(path)
    expect((await screen.findAllByText(text, {}, { timeout: 3000 })).length).toBeGreaterThan(0)
  })
})

describe('promote form', () => {
  it('requires an artifact, a Jira ticket and a reason, focusing the artifact list first', async () => {
    const { default: userEvent } = await import('@testing-library/user-event')
    const user = userEvent.setup()
    renderAt('/services/web-portal/envs/uat')
    const submit = await screen.findByRole('button', { name: '提交，进入确认' }, { timeout: 3000 })
    await user.click(submit)
    expect(await screen.findByText('请选择要发布的制品')).toBeTruthy()
    expect(screen.getByText('请输入 Jira 单号')).toBeTruthy()
    expect(screen.getByText('请输入原因')).toBeTruthy()
    expect(document.activeElement?.getAttribute('role')).toBe('radiogroup')

    const jira = screen.getByLabelText(/Jira 单号/, { selector: 'input' })
    await user.type(jira, 'ops-12')
    // Typed text is kept exactly (no keystrokes dropped); it is shown and sent upper-case.
    expect((jira as HTMLInputElement).value).toBe('ops-12')
    expect(jira.classList.contains('uppercase')).toBe(true)
    expect(screen.queryByText('请输入 Jira 单号')).toBeNull()
  })
})

describe('permission gating', () => {
  const viewer = {
    ...adminMe,
    user: { ...adminMe.user, id: 2, sub: 'oidc:bob', username: undefined, name: 'Bob', method: 'oidc' },
    permissions: [],
    canView: { 'services.view': true, 'releases.view': true },
    scopedGrants: [{ permissions: ['services.view', 'releases.view'], envs: ['qa', 'uat'], projects: [], types: [] }],
    envPermissions: {},
    canOperate: {},
  }

  it('hides 审计 and 管理 without the permissions and bounces admin routes', async () => {
    me = viewer
    renderAt('/admin/users')
    expect((await screen.findAllByText('最近发布', {}, { timeout: 3000 })).length).toBeGreaterThan(0)
    const nav = screen.getAllByRole('navigation', { name: '主导航' })[0]!
    expect(nav.textContent).toContain('服务')
    expect(nav.textContent).not.toContain('审计')
    expect(nav.textContent).not.toContain('管理')
  })

  it('shows only the admin sections a user may manage', async () => {
    me = { ...viewer, permissions: ['settings.manage'] }
    renderAt('/admin')
    // First permitted section opens; sections for other permissions are not offered.
    expect((await screen.findAllByText('阅读清单时间', {}, { timeout: 3000 })).length).toBeGreaterThan(0)
    const adminNav = screen.getByRole('navigation', { name: '管理' })
    expect(adminNav.textContent).toContain('登录与安全')
    expect(adminNav.textContent).not.toContain('用户')
    expect(adminNav.textContent).not.toContain('通知')
  })

  it('redirects a section the user lacks to one they have', async () => {
    me = { ...viewer, permissions: ['notifications.manage'], canView: {} }
    renderAt('/admin/roles')
    expect((await screen.findAllByText('还没有通知渠道', {}, { timeout: 3000 })).length).toBeGreaterThan(0)
  })

  it('shows the announcement and active freezes', async () => {
    me = {
      ...adminMe,
      app: {
        ...app,
        announcement: { level: 'warning', text: '今晚维护' },
        activeFreezes: [{ name: '国庆封版', envs: ['tier:production'], startsAt: now, endsAt: now, reason: '' }],
      },
    }
    renderAt('/')
    expect(await screen.findByText('今晚维护', {}, { timeout: 3000 })).toBeTruthy()
    expect(screen.getByText(/国庆封版 · 生产类环境/)).toBeTruthy()
  })
})
