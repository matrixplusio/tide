import { useTranslation } from 'react-i18next'
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { isApiError } from '../../lib/api'
import { ErrCode } from '../../lib/errcode'
import { applyServerError } from '../../lib/forms'
import { useRevalidate } from '../../lib/useRevalidate'
import { Button, ButtonRow, ErrorState, FormErrorBanner, FormField, Group, Input, Loading, Note, PasswordChecklist, PasswordInput, Form } from '../../components/ui'
import { adminSchema, tokenSchema, type AdminValues, type TokenValues } from './schema'
import { useCreateSetupAdmin, useSetupState, useVerifySetupToken } from './queries'

// First deploy: prove cluster access with the setup token from the pod log,
// then create the administrator. SSO and upstreams are configured afterwards
// in settings, signed in as that administrator.
export function SetupPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const st = useSetupState()
  const done = st.data?.initialized === true || isApiError(st.error, ErrCode.NotFound) || isApiError(st.error, ErrCode.AlreadyInitialized)

  useEffect(() => {
    if (done) navigate('/', { replace: true })
  }, [done, navigate])

  if (st.isPending || done) return <div className="center"><Loading /></div>
  if (st.error) return <div className="center"><div className="panel"><ErrorState error={st.error} onRetry={() => void st.refetch()} /></div></div>
  const step = st.data.tokenVerified ? 2 : 1

  return (
    <div className="center top">
      <main className="panel" style={{ paddingTop: 48 }}>
        <h1 className="hero">
          {t('setup.title')}<span className="accent-text">.</span>
        </h1>
        <p className="muted" style={{ marginTop: 0 }}>
          {t('setup.step', { step, what: step === 1 ? t('setup.stepToken') : t('setup.stepAdmin') })}
        </p>
        {step === 1 ? <TokenStep /> : <AdminStep onBackToToken={() => void st.refetch()} />}
      </main>
    </div>
  )
}

function TokenStep() {
  const { t } = useTranslation()
  const verify = useVerifySetupToken()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<TokenValues>({ resolver: zodResolver(tokenSchema), mode: 'onTouched', defaultValues: { token: '' } })
  const { register, handleSubmit, formState } = form

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await verify.mutateAsync({ token: v.token.trim() })
    } catch (e) {
      if (isApiError(e, ErrCode.SetupTokenInvalid)) {
        form.setError('token', { type: 'server', message: e.msg }, { shouldFocus: true })
        return
      }
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit}>
      <Group form>
        <FormField label="Setup token" error={formState.errors.token?.message} required>
          {(p) => <Input {...p} {...register('token')} mono autoFocus autoComplete="off" spellCheck={false} />}
        </FormField>
      </Group>
      <Note>
        {t('setup.tokenWhere')}
        <div className="mono" style={{ marginTop: 4 }}>
          kubectl -n &lt;namespace&gt; logs deploy/tide | grep setup_token
        </div>
      </Note>
      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('setup.verifying')}>
          {t('setup.continue')}
        </Button>
      </ButtonRow>
    </Form>
  )
}

export function AdminStep({ onBackToToken }: { onBackToToken: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const create = useCreateSetupAdmin()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<AdminValues>({
    resolver: zodResolver(adminSchema),
    mode: 'onTouched',
    defaultValues: { username: 'admin', name: '', password: '', confirmPassword: '' },
  })
  const { register, handleSubmit, formState, watch } = form
  useRevalidate(form, ['password', 'username'], 'confirmPassword')
  useRevalidate(form, ['username'], 'password')
  const password = watch('password')
  const username = watch('username')

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await create.mutateAsync({ username: v.username.trim(), name: v.name.trim(), password: v.password, confirmPassword: v.confirmPassword })
      navigate('/admin/upstreams', { replace: true })
    } catch (e) {
      // The setup cookie expired: go back to the token step.
      if (isApiError(e, ErrCode.SetupTokenRequired)) return onBackToToken()
      if (isApiError(e, ErrCode.AlreadyInitialized)) return navigate('/', { replace: true })
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit}>
      <Group form>
        <FormField label={t('setup.username')} error={formState.errors.username?.message} required>
          {(p) => <Input {...p} {...register('username')} mono autoComplete="username" autoCapitalize="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('setup.displayName')} error={formState.errors.name?.message}>
          {(p) => <Input {...p} {...register('name')} placeholder={t('setup.displayNameHint')} autoComplete="name" />}
        </FormField>
        <FormField label={t('setup.password')} error={formState.errors.password?.message} required hint={<PasswordChecklist password={password} username={username} />}>
          {(p) => <PasswordInput {...p} {...register('password')} autoComplete="new-password" autoFocus />}
        </FormField>
        <FormField label={t('setup.confirmPassword')} error={formState.errors.confirmPassword?.message} required>
          {(p) => <PasswordInput {...p} {...register('confirmPassword')} autoComplete="new-password" />}
        </FormField>
      </Group>
      <Note>{t('setup.note')}</Note>
      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('profile.creating')}>
          {t('profile.createAdmin')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
