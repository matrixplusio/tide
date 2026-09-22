import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { useRevalidate } from '../../../lib/useRevalidate'
import { Button, ButtonRow, Checkbox, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, useToast } from '../../../components/ui'
import { useSaveSettings } from '../queries'
import { securitySchema, type SecurityValues } from '../schemas'
import type { Security } from '../types'

const numberInput = { type: 'text', inputMode: 'numeric', autoComplete: 'off', className: 'num' } as const

const defaults: Security = {
  sessionTtlMinutes: 60,
  loginWindowMinutes: 15,
  captchaAfterUserFailures: 3,
  captchaAfterIpFailures: 5,
  lockAfterUserFailures: 10,
  lockAfterIpFailures: 30,
  localLoginAdminsOnly: false,
}

function toValues(s: Security): SecurityValues {
  return {
    sessionTtlMinutes: String(s.sessionTtlMinutes),
    loginWindowMinutes: String(s.loginWindowMinutes),
    captchaAfterUserFailures: String(s.captchaAfterUserFailures),
    captchaAfterIpFailures: String(s.captchaAfterIpFailures),
    lockAfterUserFailures: String(s.lockAfterUserFailures),
    lockAfterIpFailures: String(s.lockAfterIpFailures),
    localLoginAdminsOnly: s.localLoginAdminsOnly,
  }
}

export function SecurityForm({ initial }: { initial: Security | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<Security>('security')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<SecurityValues>({ resolver: zodResolver(securitySchema), mode: 'onTouched', defaultValues: toValues(initial ?? defaults) })
  const { register, handleSubmit, formState } = form
  const errors = formState.errors
  // Lock thresholds are compared against the captcha thresholds.
  useRevalidate(form, ['captchaAfterUserFailures'], 'lockAfterUserFailures')
  useRevalidate(form, ['captchaAfterIpFailures'], 'lockAfterIpFailures')

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({
        sessionTtlMinutes: Number(v.sessionTtlMinutes.trim()),
        loginWindowMinutes: Number(v.loginWindowMinutes.trim()),
        captchaAfterUserFailures: Number(v.captchaAfterUserFailures.trim()),
        captchaAfterIpFailures: Number(v.captchaAfterIpFailures.trim()),
        lockAfterUserFailures: Number(v.lockAfterUserFailures.trim()),
        lockAfterIpFailures: Number(v.lockAfterIpFailures.trim()),
        localLoginAdminsOnly: v.localLoginAdminsOnly,
      })
      form.reset(v)
      toast.success(t('settings.securitySaved'))
    } catch (e) {
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit} aria-label={t('settings.securityLabel')}>
      <GroupHeader>{t('settings.session')}</GroupHeader>
      <Group form>
        <FormField label={t('settings.sessionTtl')} error={errors.sessionTtlMinutes?.message} required hint={t('settings.sessionTtlHint')}>
          {(p) => <Input {...p} {...register('sessionTtlMinutes')} {...numberInput} />}
        </FormField>
      </Group>

      <GroupHeader>{t('settings.bruteForce')}</GroupHeader>
      <Group form>
        <FormField label={t('settings.window')} error={errors.loginWindowMinutes?.message} required hint={t('settings.windowHint')}>
          {(p) => <Input {...p} {...register('loginWindowMinutes')} {...numberInput} />}
        </FormField>
        <FormField label={t('settings.captchaUser')} error={errors.captchaAfterUserFailures?.message} required hint={t('settings.captchaUserHint')}>
          {(p) => <Input {...p} {...register('captchaAfterUserFailures')} {...numberInput} />}
        </FormField>
        <FormField label={t('settings.captchaIp')} error={errors.captchaAfterIpFailures?.message} required hint={t('settings.captchaIpHint')}>
          {(p) => <Input {...p} {...register('captchaAfterIpFailures')} {...numberInput} />}
        </FormField>
        <FormField label={t('settings.lockUser')} error={errors.lockAfterUserFailures?.message} required hint={t('settings.lockUserHint')}>
          {(p) => <Input {...p} {...register('lockAfterUserFailures')} {...numberInput} />}
        </FormField>
        <FormField label={t('settings.lockIp')} error={errors.lockAfterIpFailures?.message} required hint={t('settings.lockIpHint')}>
          {(p) => <Input {...p} {...register('lockAfterIpFailures')} {...numberInput} />}
        </FormField>
      </Group>

      <GroupHeader>{t('settings.localAccounts')}</GroupHeader>
      <Group form>
        <FormField label={t('settings.localLogin')} plainLabel>
          {(p) => <Checkbox id={p.id} {...register('localLoginAdminsOnly')} label={t('settings.localAdminsOnly')} />}
        </FormField>
      </Group>
      <Note>{t('settings.localNote')}</Note>

      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('settings.saving')}>
          {t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
