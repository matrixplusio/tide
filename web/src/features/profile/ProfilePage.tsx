import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { ME_KEY, useMe } from '../../app/session'
import { describeUserAgent, fmtTime } from '../../lib/format'
import { applyServerError } from '../../lib/forms'
import { bindingViaLabel, envScopeText, envSelectorLabel, permissionLabel } from '../../lib/permissions'
import { useRevalidate } from '../../lib/useRevalidate'
import {
  Button,
  ButtonRow,
  ConfirmModal,
  EmptyState,
  ErrorState,
  Form,
  FormErrorBanner,
  FormField,
  Group,
  GroupHeader,
  Input,
  KV,
  Loading,
  Note,
  Page,
  Pager,
  PasswordChecklist,
  PasswordInput,
  Pill,
  Row,
  Toolbar,
  useToast,
} from '../../components/ui'
import { JiraLink } from '../../components/domain'
import { actionLabel } from '../audit/labels'
import type { Session } from '../../lib/types'
import { ACTIVITY_PAGE_SIZE, useChangeMyPassword, useMyActivity, useMyBindings, useMySessions, useRevokeMySession, useUpdateProfile } from './queries'
import { changePasswordSchema, profileSchema, type ChangePasswordValues, type ProfileValues } from './schema'

function usernameOf(user: { username?: string; sub: string }): string {
  if (user.username) return user.username
  return user.sub.startsWith('local:') ? user.sub.slice('local:'.length) : user.sub
}

export function ProfilePage() {
  const { t } = useTranslation()
  const me = useMe()
  const local = me.user.method === 'local'
  return (
    <>
      <Toolbar title={t('profile.title')} sub={me.user.name} />
      <Page>
        <BasicInfo />
        {local && <ChangePasswordForm />}
        <Sessions />
        <MyPermissions />
        <Activity />
      </Page>
    </>
  )
}

function BasicInfo() {
  const { t } = useTranslation()
  const me = useMe()
  const u = me.user
  const local = u.method === 'local'
  const toast = useToast()
  const update = useUpdateProfile()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<ProfileValues>({ resolver: zodResolver(profileSchema), mode: 'onTouched', defaultValues: { name: u.name } })
  const { register, handleSubmit, formState } = form

  const onSubmit = handleSubmit(
    async (v) => {
      setFormError(null)
      try {
        await update.mutateAsync({ name: v.name.trim() })
        form.reset({ name: v.name.trim() })
        toast.success(t('profile.nameSaved'))
      } catch (e) {
        setFormError(applyServerError(form, e))
      }
    },
    () => setFormError(null),
  )

  const groups = u.groups ?? []
  const localGroups = u.localGroups ?? []

  return (
    <section aria-labelledby="profile-basic-h">
      <GroupHeader id="profile-basic-h">{t('profile.basic')}</GroupHeader>
      <Form onSubmit={onSubmit} aria-label={t('profile.editName')}>
        <Group form>
          {local ? (
            <FormField label={t('profile.name')} error={formState.errors.name?.message} required>
              {(p) => (
                <span className="inline-control">
                  <Input {...p} {...register('name')} autoComplete="name" />
                  {formState.isDirty && (
                    <Button type="submit" size="small" loading={formState.isSubmitting} loadingText={t('profile.saving')}>
                      {t('profile.save')}
                    </Button>
                  )}
                </span>
              )}
            </FormField>
          ) : (
            <KV k={t('profile.name')}>
              {u.name || '—'} <span className="muted">{t('profile.managedByIdp')}</span>
            </KV>
          )}
          <KV k={local ? t('profile.username') : 'sub'} mono>
            {local ? usernameOf(u) : u.sub}
          </KV>
          <KV k={t('profile.method')}>{local ? t('profile.local') : 'SSO'}</KV>
          <KV k={t('profile.email')}>{u.email || '—'}</KV>
          {!local && (
            <KV k={t('profile.idpGroups')}>
              {groups.length === 0 ? (
                <span className="faint">{t('profile.none')}</span>
              ) : (
                <span className="pills">
                  {groups.map((g) => (
                    <Pill key={g}>{g}</Pill>
                  ))}
                </span>
              )}
            </KV>
          )}
          <KV k={t('profile.localGroups')}>
            {localGroups.length === 0 ? (
              <span className="faint">{t('profile.none')}</span>
            ) : (
              <span className="pills">
                {localGroups.map((g) => (
                  <Pill key={g}>{g}</Pill>
                ))}
              </span>
            )}
          </KV>
        </Group>
        <FormErrorBanner error={formError} />
      </Form>
      {!local && <Note>{t('profile.ssoNote')}</Note>}
    </section>
  )
}

export function ChangePasswordForm() {
  const { t } = useTranslation()
  const me = useMe()
  const toast = useToast()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const change = useChangeMyPassword()
  const username = usernameOf(me.user)
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<ChangePasswordValues>({
    resolver: zodResolver(changePasswordSchema(username)),
    mode: 'onTouched',
    defaultValues: { currentPassword: '', newPassword: '', confirmPassword: '' },
  })
  const { register, handleSubmit, formState, watch } = form
  useRevalidate(form, ['newPassword'], 'confirmPassword')
  useRevalidate(form, ['currentPassword'], 'newPassword')
  const pw = watch('newPassword')

  const onSubmit = handleSubmit(
    async (v) => {
      setFormError(null)
      try {
        await change.mutateAsync(v)
        // Every session of this account is revoked server side.
        toast.success(t('profile.passwordChanged'))
        // Drop the session state first so the shell does not bounce /login back to /.
        void qc.resetQueries({ queryKey: ME_KEY })
        navigate('/login', { replace: true })
      } catch (e) {
        setFormError(applyServerError(form, e))
      }
    },
    () => setFormError(null),
  )

  return (
    <Form onSubmit={onSubmit} aria-labelledby="change-pw-h">
      <GroupHeader id="change-pw-h">{t('profile.changePassword')}</GroupHeader>
      <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
      <Group form>
        <FormField label={t('profile.currentPassword')} error={formState.errors.currentPassword?.message} required>
          {(p) => <PasswordInput {...p} {...register('currentPassword')} autoComplete="current-password" />}
        </FormField>
        <FormField label={t('profile.newPassword')} error={formState.errors.newPassword?.message} required hint={<PasswordChecklist password={pw} username={username} />}>
          {(p) => <PasswordInput {...p} {...register('newPassword')} autoComplete="new-password" />}
        </FormField>
        <FormField label={t('profile.confirmNewPassword')} error={formState.errors.confirmPassword?.message} required>
          {(p) => <PasswordInput {...p} {...register('confirmPassword')} autoComplete="new-password" />}
        </FormField>
      </Group>
      <Note>{t('profile.passwordNote')}</Note>
      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('profile.changing')}>
          {t('profile.changePassword')}
        </Button>
      </ButtonRow>
    </Form>
  )
}

function Sessions() {
  const { t } = useTranslation()
  const toast = useToast()
  const sessions = useMySessions()
  const revoke = useRevokeMySession()
  const [target, setTarget] = useState<Session | null>(null)
  const list = [...(sessions.data?.items ?? [])].sort((a, b) => Number(b.current) - Number(a.current))

  return (
    <section aria-labelledby="profile-sessions-h">
      <GroupHeader id="profile-sessions-h">{t('profile.sessions')}</GroupHeader>
      <Group>
        {sessions.isPending && <Loading />}
        {sessions.error && (
          <Row>
            <div className="grow">
              <ErrorState error={sessions.error} inline onRetry={() => void sessions.refetch()} />
            </div>
          </Row>
        )}
        {sessions.data && list.length === 0 && <EmptyState>{t('profile.noSessions')}</EmptyState>}
        {list.map((s) => (
          <Row key={s.id}>
            <div className="grow">
              <div className="t ellipsis" title={s.userAgent}>
                {describeUserAgent(s.userAgent)}
              </div>
              <div className="d">
                {t('profile.sessionLine', { ip: s.clientIp || t('profile.unknownIp'), at: fmtTime(s.createdAt), until: fmtTime(s.expiresAt) })}
              </div>
            </div>
            {s.current ? (
              <Pill tone="blue">{t('profile.currentSession')}</Pill>
            ) : (
              <Button size="small" variant="quiet" onClick={() => setTarget(s)}>
                {t('profile.signOutSession')}
              </Button>
            )}
          </Row>
        ))}
      </Group>
      <Note>{t('profile.signOutNote')}</Note>
      {target && (
        <ConfirmModal
          title={t('profile.signOutTitle')}
          confirmLabel={t('profile.signOutSession')}
          danger
          pending={revoke.isPending}
          error={revoke.error}
          onClose={() => {
            setTarget(null)
            revoke.reset()
          }}
          onConfirm={() =>
            revoke.mutate(target.id, {
              onSuccess: () => {
                toast.success(t('profile.signOutDone'))
                setTarget(null)
              },
            })
          }
        >
          {describeUserAgent(target.userAgent)}
          {t('profile.signOutBody', { ip: target.clientIp ?? '' })}
        </ConfirmModal>
      )}
    </section>
  )
}

function MyPermissions() {
  const { t } = useTranslation()
  const me = useMe()
  const bindings = useMyBindings()
  const list = bindings.data?.items ?? []
  const global = me.permissions ?? []
  const byEnv = (me.envOrder ?? []).map((env) => [env, me.envPermissions?.[env] ?? []] as const).filter(([, ps]) => ps.length > 0)

  return (
    <section aria-labelledby="profile-perms-h">
      <GroupHeader id="profile-perms-h">{t('profile.myPermissions')}</GroupHeader>
      <Group>
        {bindings.isPending && <Loading />}
        {bindings.error && (
          <Row>
            <div className="grow">
              <ErrorState error={bindings.error} inline onRetry={() => void bindings.refetch()} />
            </div>
          </Row>
        )}
        {bindings.data && list.length === 0 && <EmptyState>{t('profile.noGrants')}</EmptyState>}
        {list.map((b) => (
          <Row key={`${b.id}-${b.via}`}>
            <div className="grow">
              <div className="t">{b.roleName}</div>
              <div className="d">
                {bindingViaLabel(b)} · {envScopeText(b.envs, me.environments)}
              </div>
            </div>
          </Row>
        ))}
      </Group>

      <GroupHeader>{t('profile.effective')}</GroupHeader>
      <Group>
        <KV k={t('profile.globalScope')}>{global.length === 0 ? <span className="faint">{t('profile.none')}</span> : global.map(permissionLabel).join(t('scope.listSeparator'))}</KV>
        {byEnv.map(([env, ps]) => (
          <KV key={env} k={envSelectorLabel(env, me.environments)}>
            {ps.map(permissionLabel).join('、')}
          </KV>
        ))}
      </Group>
      <Note>{t('profile.permissionNote')}</Note>
    </section>
  )
}

function Activity() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const q = useMyActivity(page)
  const list = q.data?.items ?? []
  return (
    <section aria-labelledby="profile-activity-h">
      <GroupHeader id="profile-activity-h">{t('profile.recentActivity')}</GroupHeader>
      <Group>
        {q.isPending && <Loading />}
        {q.error && (
          <Row>
            <div className="grow">
              <ErrorState error={q.error} inline onRetry={() => void q.refetch()} />
            </div>
          </Row>
        )}
        {q.data && list.length === 0 && <EmptyState>{t('profile.noActivity')}</EmptyState>}
        {list.map((e) => (
          <Row key={e.id}>
            <div className="grow">
              <div className="t">
                {actionLabel[e.action] ?? e.action} <span className="mono faint tag">{e.action}</span>
              </div>
              <div className="d">
                {fmtTime(e.at, true)}
                {e.target && (
                  <>
                    {' · '}
                    {e.target.startsWith('REL-') ? (
                      <Link to={`/releases/${encodeURIComponent(e.target)}`} className="mono">
                        {e.target}
                      </Link>
                    ) : (
                      <span className="mono">{e.target}</span>
                    )}
                  </>
                )}
                {e.jiraTicket && (
                  <>
                    {' · '}
                    <JiraLink ticket={e.jiraTicket} />
                  </>
                )}
              </div>
            </div>
          </Row>
        ))}
      </Group>
      {q.data && <Pager page={page} pageSize={ACTIVITY_PAGE_SIZE} total={q.data.total} onChange={setPage} />}
    </section>
  )
}
