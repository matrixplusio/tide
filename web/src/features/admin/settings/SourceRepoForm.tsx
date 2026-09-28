import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, PasswordInput, Select, useToast } from '../../../components/ui'
import { useSaveSettings } from '../queries'
import { sourceRepoSchema, type SourceRepoValues } from '../schemas'
import type { SourceRepo } from '../types'

function toValues(r: SourceRepo | null | undefined): SourceRepoValues {
  return { provider: r?.provider ?? 'gitlab', baseUrl: r?.baseUrl ?? '', token: r?.token ?? '' }
}

/** Read-only access to the developers' source repositories, so a release can
 *  list the commits between the image a service runs and the one it moves to.
 *  Its own credential, read-only by design: it is never handed anything
 *  that writes. */
export function SourceRepoForm({ initial }: { initial: SourceRepo | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<SourceRepo>('sources')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<SourceRepoValues>({ resolver: zodResolver(sourceRepoSchema), values: toValues(initial) })
  const e = form.formState.errors

  const submit = form.handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({ provider: v.provider, baseUrl: v.baseUrl.trim(), token: v.token })
      toast.success(t('sourcerepo.saved'))
    } catch (err) {
      if (!applyServerError(form, err)) setFormError(err)
    }
  })

  return (
    <Form onSubmit={(ev) => void submit(ev)}>
      <GroupHeader>{t('sourcerepo.title')}</GroupHeader>
      <FormErrorBanner error={formError} />
      <Note>{t('sourcerepo.intro')}</Note>
      <Group form>
        <FormField label={t('kargogen.provider')} error={e.provider?.message} required>
          {(p) => (
            <Select
              {...p}
              {...form.register('provider')}
              options={[
                ['gitlab', 'GitLab'],
                ['gitea', 'Gitea'],
              ]}
            />
          )}
        </FormField>
        <FormField label={t('kargogen.baseUrl')} error={e.baseUrl?.message} required hint={t('sourcerepo.baseUrlHint')}>
          {(p) => <Input {...p} {...form.register('baseUrl')} mono type="url" inputMode="url" placeholder="https://git.example.com" autoComplete="off" />}
        </FormField>
        <FormField label={t('sourcerepo.token')} error={e.token?.message} required hint={t('sourcerepo.tokenHint')}>
          {(p) => <PasswordInput {...p} {...form.register('token')} className="mono" autoComplete="off" />}
        </FormField>
      </Group>
      <ButtonRow>
        <Button type="submit" variant="primary" loading={form.formState.isSubmitting}>
          {t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
