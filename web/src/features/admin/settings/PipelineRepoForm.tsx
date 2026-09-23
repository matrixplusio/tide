import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, Checkbox, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, PasswordInput, Select, useToast } from '../../../components/ui'
import { useRepoIdentity, useSaveSettings } from '../queries'
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
    bareDomain: r?.bareDomain ?? false,
    imageStrategy: (r?.imageStrategy as PipelineRepoValues['imageStrategy']) || 'Lexical',
    tagPattern: r?.tagPattern ?? '',
  }
}

/** Where generated pipelines are committed. Separate from the upstreams form
 *  because it is a write target, not something Tide reads from. */
export function PipelineRepoForm({ initial }: { initial: PipelineRepo | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<PipelineRepo>('pipeline')
  const [formError, setFormError] = useState<unknown>(null)
  const qc = useQueryClient()
  const configured = !!(initial?.baseUrl && initial.project)
  const who = useRepoIdentity(configured)
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
        bareDomain: v.bareDomain,
        imageStrategy: v.imageStrategy,
        tagPattern: (v.tagPattern ?? '').trim(),
      })
      // The server only stores settings it could authenticate, so a
      // successful save already means the token works. Refetching turns that
      // into something the page says out loud.
      await qc.invalidateQueries({ queryKey: ['kargo-identity'] })
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
        <FormField label={t('kargogen.imageStrategy')} error={e.imageStrategy?.message} required hint={t('kargogen.imageStrategyHint')}>
          {(p) => (
            <Select
              {...p}
              {...form.register('imageStrategy')}
              options={[
                ['Lexical', t('kargogen.stratLexical')],
                ['SemVer', t('kargogen.stratSemVer')],
                ['NewestBuild', t('kargogen.stratNewestBuild')],
                ['Digest', t('kargogen.stratDigest')],
              ]}
            />
          )}
        </FormField>
        <FormField label={t('kargogen.tagPattern')} error={e.tagPattern?.message} hint={t('kargogen.tagPatternHint')}>
          {(p) => <Input {...p} {...form.register('tagPattern')} mono placeholder="^[0-9]" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('kargogen.projectNaming')} plainLabel hint={t('kargogen.bareDomainHint')}>
          {(p) => <Checkbox id={p.id} {...form.register('bareDomain')} label={t('kargogen.bareDomain')} />}
        </FormField>
        <FormField label={t('kargogen.token')} error={e.token?.message} required hint={t('kargogen.tokenHint')}>
          {(p) => <PasswordInput {...p} {...form.register('token')} className="mono" autoComplete="off" />}
        </FormField>
      </Group>
      {who.data?.username && (
        <Note>
          {t('kargogen.identity', { user: who.data.name ? `${who.data.name} (${who.data.username})` : who.data.username, project: who.data.project ?? '' })}
        </Note>
      )}
      {who.data?.error && <Note>{t('kargogen.identityUnknown', { msg: who.data.error })}</Note>}
      <Note>{t('kargogen.repoHint')}</Note>
      <ButtonRow>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? t('settings.saving') : t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
