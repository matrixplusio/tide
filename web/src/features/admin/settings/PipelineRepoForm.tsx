import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, PasswordInput, Select, useToast } from '../../../components/ui'
import { useSaveSettings } from '../queries'
import { pipelineRepoSchema, type PipelineRepoValues } from '../schemas'
import type { PipelineRepo } from '../types'

function toValues(r: PipelineRepo | null | undefined): PipelineRepoValues {
  return {
    provider: r?.provider ?? 'gitlab',
    baseUrl: r?.baseUrl ?? '',
    project: r?.project ?? '',
    branch: r?.branch ?? '',
    pathPrefix: r?.pathPrefix ?? '',
    token: r?.token ?? '',
  }
}

/** Where generated pipelines are committed. Separate from the upstreams form
 *  because it is a write target, not something Tide reads from. */
export function PipelineRepoForm({ initial }: { initial: PipelineRepo | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<PipelineRepo>('pipeline')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<PipelineRepoValues>({ resolver: zodResolver(pipelineRepoSchema), values: toValues(initial) })
  const e = form.formState.errors

  const submit = form.handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({
        provider: v.provider,
        baseUrl: v.baseUrl.trim(),
        project: v.project.trim(),
        branch: (v.branch ?? '').trim(),
        pathPrefix: (v.pathPrefix ?? '').trim(),
        token: v.token,
      })
      toast.success(t('kargogen.repoSaved'))
    } catch (err) {
      if (!applyServerError(form, err)) setFormError(err)
    }
  })

  return (
    <Form onSubmit={(ev) => void submit(ev)}>
      <GroupHeader>{t('kargogen.repo')}</GroupHeader>
      <FormErrorBanner error={formError} />
      <Group form>
        <FormField label={t('kargogen.provider')} error={e.provider?.message} required hint={t('kargogen.providerHint')}>
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
          {(p) => <Input {...p} {...form.register('project')} mono placeholder="devops/k8s-pipelines" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('kargogen.branch')} error={e.branch?.message} hint={t('kargogen.branchHint')}>
          {(p) => <Input {...p} {...form.register('branch')} mono placeholder="kargo-pipelines" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('kargogen.pathPrefix')} error={e.pathPrefix?.message} hint={t('kargogen.pathPrefixHint')}>
          {(p) => <Input {...p} {...form.register('pathPrefix')} mono placeholder="kargo" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('kargogen.token')} error={e.token?.message} required hint={t('kargogen.tokenHint')}>
          {(p) => <PasswordInput {...p} {...form.register('token')} className="mono" autoComplete="off" />}
        </FormField>
      </Group>
      <Note>{t('kargogen.repoHint')}</Note>
      <ButtonRow>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? t('settings.saving') : t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
