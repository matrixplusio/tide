import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { codePointLength } from '../../../lib/validation'
import { Button, ButtonRow, Checkbox, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Select, Textarea, useToast } from '../../../components/ui'
import { useSaveSettings } from '../queries'
import { systemSchema, type SystemValues } from '../schemas'
import type { SystemSettings } from '../types'

const levels = [
  ['info', 'settings.levelInfo'],
  ['warning', 'settings.levelWarning'],
] as const

export function SystemForm({ initial }: { initial: SystemSettings | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<SystemSettings>('system')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<SystemValues>({
    resolver: zodResolver(systemSchema),
    mode: 'onTouched',
    defaultValues: {
      siteName: initial?.siteName ?? 'Tide',
      baseUrl: initial?.baseUrl ?? '',
      announcement: {
        enabled: initial?.announcement?.enabled ?? false,
        level: initial?.announcement?.level === 'warning' ? 'warning' : 'info',
        text: initial?.announcement?.text ?? '',
      },
    },
  })
  const { register, handleSubmit, formState, watch, trigger } = form
  const errors = formState.errors
  const text = watch('announcement.text')

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({
        siteName: v.siteName.trim(),
        baseUrl: v.baseUrl.trim(),
        announcement: { enabled: v.announcement.enabled, level: v.announcement.level, text: v.announcement.text.trim() },
      })
      form.reset(v)
      toast.success(t('settings.systemSaved'))
    } catch (e) {
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit} aria-label={t('settings.systemLabel')}>
      <Group form>
        <FormField label={t('settings.siteName')} error={errors.siteName?.message} required hint={t('settings.siteNameHint')}>
          {(p) => <Input {...p} {...register('siteName')} autoComplete="off" />}
        </FormField>
        <FormField label={t('settings.baseUrl')} error={errors.baseUrl?.message} hint={t('settings.baseUrlHint')}>
          {(p) => <Input {...p} {...register('baseUrl')} mono type="url" inputMode="url" autoComplete="off" placeholder="https://tide.example.com" />}
        </FormField>
      </Group>

      <GroupHeader>{t('settings.announcement')}</GroupHeader>
      <Group form>
        <FormField label={t('settings.announcementField')} plainLabel>
          {(p) => (
            <Checkbox
              id={p.id}
              {...register('announcement.enabled', { onChange: () => formState.isSubmitted && void trigger('announcement.text') })}
              label={t('settings.announcementOn')}
            />
          )}
        </FormField>
        <FormField label={t('settings.level')} error={errors.announcement?.level?.message}>
          {(p) => <Select {...p} {...register('announcement.level')} options={levels.map(([v, key]) => [v, t(key)] as const)} />}
        </FormField>
        <FormField label={t('settings.text')} error={errors.announcement?.text?.message} hint={`${codePointLength(text.trim())} / 500`}>
          {(p) => <Textarea {...p} {...register('announcement.text')} rows={3} placeholder={t('settings.announcementPlaceholder')} />}
        </FormField>
      </Group>

      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('settings.saving')}>
          {t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
