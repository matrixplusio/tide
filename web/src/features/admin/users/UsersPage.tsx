import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { isApiError } from '../../../lib/api'
import { ErrCode } from '../../../lib/errcode'
import { fmtTime } from '../../../lib/format'
import { applyServerError } from '../../../lib/forms'
import { useRevalidate } from '../../../lib/useRevalidate'
import {
  Button,
  ButtonRow,
  Chev,
  EmptyState,
  ErrorState,
  Form,
  FormErrorBanner,
  FormField,
  Group,
  Input,
  Loading,
  Modal,
  Page,
  Pager,
  PasswordChecklist,
  PasswordInput,
  Pill,
  Row,
  Select,
  StatusDot,
  useToast,
} from '../../../components/ui'
import { AdminToolbar } from '../AdminToolbar'
import { USERS_PAGE_SIZE, useCreateUser, useUsers } from '../queries'
import { createUserSchema, type CreateUserValues } from '../schemas'
import { methodLabel, userDisplay } from './format'

const methods = [
  ['', 'users.allMethods'],
  ['local', 'profile.local'],
  ['oidc', 'SSO'],
] as const

const statuses = [
  ['', 'users.allStatuses'],
  ['enabled', 'users.enabled'],
  ['disabled', 'users.disabled'],
] as const

export function UsersPage() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const method = params.get('method') ?? ''
  const status = params.get('status') ?? ''
  const page = Math.max(1, Number(params.get('page')) || 1)
  const [creating, setCreating] = useState(false)

  const set = (patch: Record<string, string>) => {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    if (!('page' in patch)) p.delete('page')
    setParams(p, { replace: true })
  }

  const users = useUsers({ q: q || undefined, method: method || undefined, status: status || undefined, page })
  const list = users.data?.items ?? []

  return (
    <>
      <AdminToolbar title={t('users.title')} sub={users.data ? t('users.countSub', { count: users.data.total }) : undefined}>
        <Button size="small" onClick={() => setCreating(true)}>
          {t('users.newLocal')}
        </Button>
      </AdminToolbar>
      <Page>
        {/* Keyed by q: back/forward changes the URL and the search box follows. */}
        <UserFilters key={q} q={q} method={method} status={status} onChange={set} />

        {users.isPending && <Loading />}
        {users.error && <ErrorState error={users.error} onRetry={() => void users.refetch()} />}
        {users.data && (
          <Group>
            {list.length === 0 && <EmptyState>{q || method || status ? t('users.noMatch') : t('users.none')}</EmptyState>}
            {list.map((u) => (
              <Row key={u.id} to={`/admin/users/${u.id}`}>
                <StatusDot state={u.disabled ? 'off' : 'ok'} label={u.disabled ? t('users.disabled') : t('users.enabled')} />
                <div className="grow">
                  <div className="t ellipsis">
                    {userDisplay(u)} <span className="mono muted">{u.username ?? u.sub}</span>
                  </div>
                  <div className="d ellipsis">
                    {methodLabel(u.method)}
                    {u.email ? ` · ${u.email}` : ''}
                    {' · '}
                    {u.lastLoginAt ? t('users.lastLogin', { at: fmtTime(u.lastLoginAt) }) : t('users.neverLoggedIn')}
                  </div>
                </div>
                <span className="pills">
                  {(u.roles ?? []).map((r) => (
                    <Pill key={r.id} tone={r.id === 'admin' ? 'blue' : 'neutral'}>
                      {r.name}
                    </Pill>
                  ))}
                  {u.disabled && <Pill>{t('users.disabled')}</Pill>}
                </span>
                <Chev />
              </Row>
            ))}
          </Group>
        )}
        {users.data && <Pager page={page} pageSize={USERS_PAGE_SIZE} total={users.data.total} onChange={(p) => set({ page: String(p) })} />}
      </Page>
      {creating && <CreateUserModal onClose={() => setCreating(false)} />}
    </>
  )
}

function UserFilters({ q, method, status, onChange }: { q: string; method: string; status: string; onChange: (patch: Record<string, string>) => void }) {
  const { t } = useTranslation()
  const [search, setSearch] = useState(q)
  return (
    <Form
      className="btnrow filters"
      style={{ marginTop: 0, marginBottom: 12 }}
      aria-label={t('users.filterLabel')}
      onSubmit={(e) => {
        e.preventDefault()
        onChange({ q: search.trim().slice(0, 100) })
      }}
    >
      <Input appearance="filled" type="search" className="filter-text" aria-label={t('users.search')} placeholder={t('users.searchPlaceholder')} value={search} maxLength={100} onChange={(e) => setSearch(e.target.value)} />
      <Select appearance="filled" aria-label={t('users.method')} options={methods.map(([v, key]) => [v, t(key)] as const)} value={method} onChange={(e) => onChange({ method: e.target.value })} />
      <Select appearance="filled" aria-label={t('users.status')} options={statuses.map(([v, key]) => [v, t(key)] as const)} value={status} onChange={(e) => onChange({ status: e.target.value })} />
      <Button type="submit" variant="quiet">
        {t('users.doSearch')}
      </Button>
    </Form>
  )
}

const emptyUser: CreateUserValues = { username: '', name: '', password: '', confirmPassword: '' }

function CreateUserModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const navigate = useNavigate()
  const create = useCreateUser()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<CreateUserValues>({ resolver: zodResolver(createUserSchema), mode: 'onTouched', defaultValues: emptyUser })
  const { register, handleSubmit, formState, watch } = form
  useRevalidate(form, ['password'], 'confirmPassword')
  useRevalidate(form, ['username'], 'password')
  const pw = watch('password')
  const username = watch('username')

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      const u = await create.mutateAsync({ username: v.username.trim(), name: v.name.trim(), password: v.password, confirmPassword: v.confirmPassword })
      toast.success(t('users.accountCreated', { name: u?.username ?? v.username }))
      onClose()
      if (u?.id) navigate(`/admin/users/${u.id}`)
    } catch (e) {
      if (isApiError(e, ErrCode.UsernameTaken)) {
        form.setError('username', { type: 'server', message: e.msg }, { shouldFocus: true })
        return
      }
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Modal title={t('users.newLocal')} subtitle={t('users.newAccountSub')} onClose={onClose} closeOnEsc={!formState.isSubmitting}>
      <Form onSubmit={onSubmit}>
        <Group form>
          <FormField label={t('profile.username')} error={formState.errors.username?.message} required>
            {(p) => <Input {...p} {...register('username')} mono autoComplete="off" autoCapitalize="off" spellCheck={false} data-autofocus />}
          </FormField>
          <FormField label={t('profile.name')} error={formState.errors.name?.message}>
            {(p) => <Input {...p} {...register('name')} autoComplete="off" placeholder={t('admin.optional')} />}
          </FormField>
          <FormField label={t('users.initialPassword')} error={formState.errors.password?.message} required hint={<PasswordChecklist password={pw} username={username} />}>
            {(p) => <PasswordInput {...p} {...register('password')} autoComplete="new-password" />}
          </FormField>
          <FormField label={t('setup.confirmPassword')} error={formState.errors.confirmPassword?.message} required>
            {(p) => <PasswordInput {...p} {...register('confirmPassword')} autoComplete="new-password" />}
          </FormField>
        </Group>
        <FormErrorBanner error={formError} />
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button variant="quiet" onClick={onClose} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
          <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.creating')}>
            {t('admin.create')}
          </Button>
        </ButtonRow>
      </Form>
    </Modal>
  )
}
