import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, FormErrorBanner, FormField, Group, Input, Note, PasswordInput, useToast, Form } from '../../../components/ui'
import { ssoLoginHref } from '../../auth/queries'
import { useSaveSettings } from '../queries'
import { SSO_CALLBACK_PATH, ssoSchema, type SsoValues } from '../schemas'
import { MASK, type OIDC } from '../types'

export function SsoForm({ initial }: { initial: OIDC | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<OIDC>('oidc')
  const [formError, setFormError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  const form = useForm<SsoValues>({
    resolver: zodResolver(ssoSchema),
    mode: 'onTouched',
    defaultValues: initial
      ? { ...initial, groupsClaim: initial.groupsClaim ?? '' }
      : { issuer: '', clientId: '', clientSecret: '', groupsClaim: 'groups', redirectUrl: `${window.location.origin}${SSO_CALLBACK_PATH}` },
  })
  const { register, handleSubmit, formState } = form
  const errors = formState.errors

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    setSaved(false)
    try {
      const body: OIDC = {
        issuer: v.issuer.trim(),
        clientId: v.clientId.trim(),
        clientSecret: v.clientSecret === MASK ? MASK : v.clientSecret.trim(),
        groupsClaim: v.groupsClaim.trim(),
        redirectUrl: v.redirectUrl.trim(),
      }
      await save.mutateAsync(body)
      form.reset(v)
      setSaved(true)
      toast.success(t('settings.ssoSaved'))
    } catch (e) {
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit} aria-label={t('settings.ssoLabel')}>
      <Group form>
        <FormField label="Issuer" error={errors.issuer?.message} required>
          {(p) => <Input {...p} {...register('issuer')} mono type="url" inputMode="url" placeholder="https://sso.example.com" autoComplete="off" />}
        </FormField>
        <FormField label="Client ID" error={errors.clientId?.message} required>
          {(p) => <Input {...p} {...register('clientId')} mono autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label="Client secret" error={errors.clientSecret?.message} required hint={initial ? t('settings.secretKept', { mask: MASK }) : undefined}>
          {(p) => <PasswordInput {...p} {...register('clientSecret')} className="mono" autoComplete="off" />}
        </FormField>
        <FormField label="Groups claim" error={errors.groupsClaim?.message}>
          {(p) => <Input {...p} {...register('groupsClaim')} mono autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('settings.redirectUrl')} error={errors.redirectUrl?.message} required hint={t('settings.redirectHint', { path: SSO_CALLBACK_PATH })}>
          {(p) => <Input {...p} {...register('redirectUrl')} mono type="url" inputMode="url" autoComplete="off" placeholder="https://tide.example.com/api/v1/auth/sso/callback" />}
        </FormField>
      </Group>
      <Note>
        {t('settings.ssoNote1')}
        {t('settings.ssoNote2')}
      </Note>
      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('settings.saving')}>
          {t('settings.save')}
        </Button>
        {saved && !formState.isDirty && (
          <a className="btn quiet small" href={ssoLoginHref('/admin/sso')}>
            {t('settings.ssoVerify')}
          </a>
        )}
      </ButtonRow>
    </Form>
  )
}
