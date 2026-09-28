import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, PasswordInput, Select, useToast } from '../../../components/ui'
import { useSaveSettings } from '../queries'
import { appsRepoSchema, type AppsRepoValues } from '../schemas'
import type { AppsRepo } from '../types'

function toValues(r: AppsRepo | null | undefined): AppsRepoValues {
  return {
    provider: r?.provider ?? 'gitlab',
    baseUrl: r?.baseUrl ?? '',
    project: r?.project ?? '',
    branch: r?.branch ?? '',
    registry: r?.registry ?? '',
    lineDimension: r?.lineDimension ?? '',
    token: r?.token ?? '',
  }
}

/** The repository holding the service registry. Its own form, and its own
 *  token, even when both match the pipeline repository: what a credential can
 *  reach is the first question asked after an incident, and an inherited one
 *  cannot be answered from the settings page. */
export function AppsRepoForm({ initial }: { initial: AppsRepo | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<AppsRepo>('apps')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<AppsRepoValues>({ resolver: zodResolver(appsRepoSchema), values: toValues(initial) })
  const e = form.formState.errors

  const submit = form.handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({
        provider: v.provider,
        baseUrl: v.baseUrl.trim(),
        project: v.project.trim(),
        branch: v.branch.trim(),
        registry: (v.registry ?? '').trim(),
        lineDimension: (v.lineDimension ?? '').trim(),
        token: v.token,
      })
      toast.success(t('appsrepo.saved'))
    } catch (err) {
      if (!applyServerError(form, err)) setFormError(err)
    }
  })

  return (
    <Form onSubmit={(ev) => void submit(ev)}>
      <GroupHeader>{t('appsrepo.title')}</GroupHeader>
      <FormErrorBanner error={formError} />
      <Note>{t('appsrepo.intro')}</Note>
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
        <FormField label={t('kargogen.baseUrl')} error={e.baseUrl?.message} required>
          {(p) => <Input {...p} {...form.register('baseUrl')} mono type="url" inputMode="url" placeholder="https://git.example.com" autoComplete="off" />}
        </FormField>
        <FormField label={t('kargogen.project')} error={e.project?.message} required>
          {(p) => <Input {...p} {...form.register('project')} mono placeholder="acme/k8s-apps" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('appsrepo.branch')} error={e.branch?.message} required hint={t('appsrepo.branchHint')}>
          {(p) => <Input {...p} {...form.register('branch')} mono placeholder="main" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('appsrepo.registry')} error={e.registry?.message} hint={t('appsrepo.registryHint')}>
          {(p) => <Input {...p} {...form.register('registry')} mono placeholder="{line}/services.yaml" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('appsrepo.lineDimension')} error={e.lineDimension?.message} hint={t('appsrepo.lineDimensionHint')}>
          {(p) => <Input {...p} {...form.register('lineDimension')} mono autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('appsrepo.token')} error={e.token?.message} required hint={t('appsrepo.tokenHint')}>
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
