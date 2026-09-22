import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMe } from '../../../app/session'
import { fmtTime } from '../../../lib/format'
import { applyServerError } from '../../../lib/forms'
import { bindingViaLabel, can, envScopeText } from '../../../lib/permissions'
import { useRevalidate } from '../../../lib/useRevalidate'
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
  Modal,
  Note,
  Page,
  PasswordChecklist,
  PasswordInput,
  Pill,
  Row,
  StatusDot,
  useToast,
} from '../../../components/ui'
import { AdminToolbar } from '../AdminToolbar'
import { useRenameUser, useResetUserPassword, useRevokeUserSessions, useSetUserDisabled, useUser } from '../queries'
import { nameSchema, resetPasswordSchema, type NameValues, type ResetPasswordValues } from '../schemas'
import type { UserRow } from '../types'
import { methodLabel, userDisplay } from './format'

type Dialog = 'rename' | 'password' | 'disable' | 'signout' | null

export function UserDetailPage() {
  const { t } = useTranslation()
  const { id = '' } = useParams()
  const me = useMe()
  const toast = useToast()
  const q = useUser(id)
  const [dialog, setDialog] = useState<Dialog>(null)
  const setDisabled = useSetUserDisabled()
  const revoke = useRevokeUserSessions()
  const u = q.data?.user
  const self = !!u && u.id === me.user.id
  const local = u?.method === 'local'
  const bindings = q.data?.bindings ?? []
  const sessionCount = q.data?.sessionCount ?? 0
  const close = () => {
    setDialog(null)
    setDisabled.reset()
    revoke.reset()
  }

  return (
    <>
      <AdminToolbar title={u ? userDisplay(u) : t('users.title')} sub={u?.username ?? u?.sub} back={{ to: '/admin/users', label: t('users.title') }}>
        {u?.disabled && <Pill>{t('users.disabled')}</Pill>}
      </AdminToolbar>
      <Page>
        {q.isPending && <Loading />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {u && (
          <>
            <GroupHeader>{t('userDetail.basics')}</GroupHeader>
            <Group>
              <KV k={t('profile.name')}>
                {u.name || '—'}
                {self && <span className="muted">{t('userDetail.you')}</span>}
              </KV>
              <KV k={local ? t('profile.username') : 'sub'} mono>
                {u.username ?? u.sub}
              </KV>
              {local && u.username && (
                <KV k="sub" mono>
                  {u.sub}
                </KV>
              )}
              <KV k={t('userDetail.method')}>{methodLabel(u.method)}</KV>
              <KV k={t('userDetail.email')}>{u.email || '—'}</KV>
              <KV k={t('userDetail.status')}>
                <StatusDot state={u.disabled ? 'off' : 'ok'} /> {u.disabled ? t('users.disabled') : t('users.enabled')}
              </KV>
              <KV k={t('userDetail.lastLogin')}>{u.lastLoginAt ? fmtTime(u.lastLoginAt) : t('users.neverLoggedIn')}</KV>
              <KV k={t('userDetail.activeSessions')}>{sessionCount}</KV>
              <KV k={t('userDetail.createdAt')}>{fmtTime(u.createdAt)}</KV>
            </Group>
            <ButtonRow>
              {local && (
                <Button variant="quiet" size="small" onClick={() => setDialog('rename')}>
                  {t('userDetail.editName')}
                </Button>
              )}
              {local && !self && (
                <Button variant="quiet" size="small" onClick={() => setDialog('password')}>
                  {t('userDetail.resetPassword')}
                </Button>
              )}
              <Button variant="quiet" size="small" disabled={sessionCount === 0} onClick={() => setDialog('signout')}>
                {t('userDetail.signOutAll')}
              </Button>
              {!self && (
                <Button variant={u.disabled ? 'quiet' : 'danger'} size="small" onClick={() => setDialog('disable')}>
                  {u.disabled ? t('userDetail.enable') : t('userDetail.disable')}
                </Button>
              )}
            </ButtonRow>
            {!local && <Note>{t('userDetail.ssoNote')}</Note>}

            <GroupHeader>{t('userDetail.groups')}</GroupHeader>
            <Group>
              <KV k={t('profile.localGroups')}>
                {(u.localGroups ?? []).length === 0 ? (
                  <span className="faint">{t('profile.none')}</span>
                ) : (
                  <span className="pills">
                    {(u.localGroups ?? []).map((g) => (
                      <Link key={g} to={`/admin/groups/${encodeURIComponent(g)}`} className="pill">
                        {g}
                      </Link>
                    ))}
                  </span>
                )}
              </KV>
              <KV k={t('profile.idpGroups')}>
                {(u.groups ?? []).length === 0 ? (
                  <span className="faint">{t('profile.none')}</span>
                ) : (
                  <span className="pills">
                    {(u.groups ?? []).map((g) => (
                      <Pill key={g}>{g}</Pill>
                    ))}
                  </span>
                )}
              </KV>
            </Group>

            <GroupHeader right={can(me, 'roles.manage') ? <Link to="/admin/roles">{t('userDetail.manageBindings')}</Link> : undefined}>{t('userDetail.effectiveBindings')}</GroupHeader>
            <Group>
              {bindings.length === 0 && <EmptyState>{t('userDetail.noBindings')}</EmptyState>}
              {bindings.map((b) => (
                <Row key={`${b.id}-${b.via}`}>
                  <div className="grow">
                    <div className="t">
                      {can(me, 'roles.manage') ? <Link to={`/admin/roles/${encodeURIComponent(b.roleId)}`}>{b.roleName}</Link> : b.roleName}
                    </div>
                    <div className="d">
                      {bindingViaLabel(b)} · {envScopeText(b.envs, me.environments)}
                    </div>
                  </div>
                  {b.via === 'user' && <Pill tone="blue">{t('userDetail.direct')}</Pill>}
                </Row>
              ))}
            </Group>
          </>
        )}
      </Page>

      {u && dialog === 'rename' && <RenameModal user={u} onClose={close} />}
      {u && dialog === 'password' && <ResetPasswordModal user={u} onClose={close} />}
      {u && dialog === 'disable' && (
        <ConfirmModal
          title={u.disabled ? t('userDetail.enableTitle', { who: userDisplay(u) }) : t('userDetail.disableTitle', { who: userDisplay(u) })}
          confirmLabel={u.disabled ? t('userDetail.enable') : t('userDetail.disable')}
          danger={!u.disabled}
          pending={setDisabled.isPending}
          error={setDisabled.error}
          onClose={close}
          onConfirm={() =>
            setDisabled.mutate(
              { id: u.id, disabled: !u.disabled },
              {
                onSuccess: () => {
                  toast.success(u.disabled ? t('userDetail.enabled', { who: userDisplay(u) }) : t('userDetail.disabled', { who: userDisplay(u) }))
                  close()
                },
              },
            )
          }
        >
          {u.disabled ? t('userDetail.enableNote') : t('userDetail.disableNote', { how: u.method === 'oidc' ? t('userDetail.disableSso') : t('userDetail.disableLocal') })}
        </ConfirmModal>
      )}
      {u && dialog === 'signout' && (
        <ConfirmModal
          title={t('userDetail.signOutTitle', { who: userDisplay(u) })}
          confirmLabel={t('userDetail.signOutAll')}
          danger
          pending={revoke.isPending}
          error={revoke.error}
          onClose={close}
          onConfirm={() =>
            revoke.mutate(u.id, {
              onSuccess: () => {
                toast.success(t('userDetail.signedOut', { who: userDisplay(u) }))
                close()
              },
            })
          }
        >
          {self ? t('userDetail.signOutSelfNote') : t('userDetail.signOutOtherNote')}
        </ConfirmModal>
      )}
    </>
  )
}

function RenameModal({ user, onClose }: { user: UserRow; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const rename = useRenameUser()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<NameValues>({ resolver: zodResolver(nameSchema), mode: 'onTouched', defaultValues: { name: user.name } })
  const { register, handleSubmit, formState } = form

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await rename.mutateAsync({ id: user.id, name: v.name.trim() })
      toast.success(t('userDetail.nameChanged'))
      onClose()
    } catch (e) {
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Modal title={t('userDetail.editNameTitle', { who: user.username ?? user.sub })} onClose={onClose} closeOnEsc={!formState.isSubmitting} width={460}>
      <Form onSubmit={onSubmit}>
        <Group form>
          <FormField label={t('profile.name')} error={formState.errors.name?.message} required>
            {(p) => <Input {...p} {...register('name')} autoComplete="off" data-autofocus />}
          </FormField>
        </Group>
        <FormErrorBanner error={formError} />
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button variant="quiet" onClick={onClose} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
          <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
            {t('settings.save')}
          </Button>
        </ButtonRow>
      </Form>
    </Modal>
  )
}

function ResetPasswordModal({ user, onClose }: { user: UserRow; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const reset = useResetUserPassword()
  const username = user.username ?? ''
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<ResetPasswordValues>({ resolver: zodResolver(resetPasswordSchema(username)), mode: 'onTouched', defaultValues: { newPassword: '', confirmPassword: '' } })
  const { register, handleSubmit, formState, watch } = form
  useRevalidate(form, ['newPassword'], 'confirmPassword')
  const pw = watch('newPassword')

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await reset.mutateAsync({ id: user.id, newPassword: v.newPassword, confirmPassword: v.confirmPassword })
      toast.success(t('userDetail.resetDone', { who: username || userDisplay(user) }))
      onClose()
    } catch (e) {
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Modal title={t('userDetail.resetTitle', { who: username || userDisplay(user) })} subtitle={t('userDetail.resetSub')} onClose={onClose} closeOnEsc={!formState.isSubmitting}>
      <Form onSubmit={onSubmit}>
        {/* Hidden username helps password managers attach the new password to the right account. */}
        <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />
        <Group form>
          <FormField label={t('profile.newPassword')} error={formState.errors.newPassword?.message} required hint={<PasswordChecklist password={pw} username={username} />}>
            {(p) => <PasswordInput {...p} {...register('newPassword')} autoComplete="new-password" data-autofocus />}
          </FormField>
          <FormField label={t('profile.confirmNewPassword')} error={formState.errors.confirmPassword?.message} required>
            {(p) => <PasswordInput {...p} {...register('confirmPassword')} autoComplete="new-password" />}
          </FormField>
        </Group>
        <FormErrorBanner error={formError} />
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button variant="quiet" onClick={onClose} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
          <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
            {t('userDetail.resetPassword')}
          </Button>
        </ButtonRow>
      </Form>
    </Modal>
  )
}
