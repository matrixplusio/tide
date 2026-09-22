import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMe } from '../../app/session'
import { applyServerError } from '../../lib/forms'
import { changeSummary } from '../../lib/release'
import type { ConfigDiff, Deployment, Release } from '../../lib/types'
import { Banner, Button, ButtonRow, Checkbox, ErrorState, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Loading, Note, Textarea } from '../../components/ui'
import { ConfigChanges, ConfirmSheet, VersionLabel } from '../../components/domain'
import { useCreateRelease } from '../releases/queries'
import { useConfigDiff } from './queries'
import { mapSyncField, normalizeJira, requiredFields, syncSchema, type SyncValues } from './schema'

/**
 * Config sync: apply what was merged into the deploy repo besides the image
 * (env vars, resources, ConfigMaps, new or removed resources) by syncing the
 * Argo CD Application. The person reviews the exact diff; execution refuses
 * to run if it changed in the meantime.
 */
export function SyncForm({ d, canOperate, busy }: { d: Deployment; canOperate: boolean; busy: boolean }) {
  const { t } = useTranslation()
  const q = useConfigDiff(d.service, d.env, canOperate)
  if (!canOperate) return <Banner tone="warn">{t('forms.noSyncPermission', { env: d.env })}</Banner>
  return (
    <>
      <GroupHeader>
        {t('forms.pendingConfig', { app: d.app })}
        <span className="r">
          <Button variant="quiet" size="small" loading={q.isFetching} loadingText={t('forms.reading')} onClick={() => void q.refetch()}>
            {t('forms.reread')}
          </Button>
        </span>
      </GroupHeader>
      {q.isPending && <Loading />}
      {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
      {q.data && <SyncFormInner key={q.data.revision + changeSummary(q.data.changes)} d={d} diff={q.data} busy={busy} />}
    </>
  )
}

function SyncFormInner({ d, diff, busy }: { d: Deployment; diff: ConfigDiff; busy: boolean }) {
  const { t } = useTranslation()
  const nav = useNavigate()
  const req = requiredFields(useMe().app, d.env)
  const create = useCreateRelease()
  const [confirming, setConfirming] = useState<Release | null>(null)
  const [formError, setFormError] = useState<unknown>(null)
  const deletes = diff.changes.filter((c) => c.action === 'delete').length
  const form = useForm<SyncValues>({
    resolver: zodResolver(syncSchema(req, deletes)),
    mode: 'onTouched',
    defaultValues: { title: '', jiraTicket: '', reason: '', restart: diff.needsRestart, prune: false },
  })
  const { register, handleSubmit, formState } = form
  const empty = diff.changes.length === 0

  const onSubmit = handleSubmit(
    async (v) => {
      setFormError(null)
      try {
        const r = await create.mutateAsync({
          env: d.env,
          title: v.title.trim(),
          jiraTicket: normalizeJira(v.jiraTicket),
          reason: v.reason.trim(),
          items: [{ kind: 'sync', service: d.service, sequence: 1, prune: deletes > 0 && v.prune, restart: v.restart }],
        })
        setConfirming(r)
      } catch (e) {
        setFormError(applyServerError(form, e, { mapField: mapSyncField }))
      }
    },
    () => setFormError(null),
  )

  return (
    <>
      <ConfigChanges changes={diff.changes} />
      {!empty && (
        <Note>
          {t('forms.syncSummary', { summary: changeSummary(diff.changes), rev: diff.revision.slice(0, 8) })}
        </Note>
      )}
      {!empty && (
        <Form onSubmit={onSubmit} aria-label={t('forms.syncLabel')}>
          <GroupHeader>{t('forms.syncHeader', { env: d.env })}</GroupHeader>
          <Group form>
            <FormField label={t('forms.version')} plainLabel>
              {() => (
                <span className="t" style={{ paddingTop: 5 }}>
                  {t('forms.keep')} <VersionLabel a={d} full />
                </span>
              )}
            </FormField>
            <FormField label={t('forms.restartAfterSync')} plainLabel hint={diff.needsRestart ? t('forms.restartAfterSyncOn') : t('forms.restartAfterSyncOff')}>
              {(p) => <Checkbox id={p.id} {...register('restart')} label={t('forms.restartAfterSyncBox')} />}
            </FormField>
            {deletes > 0 && (
              <FormField label={t('forms.allowPrune')} plainLabel error={formState.errors.prune?.message}>
                {(p) => <Checkbox id={p.id} aria-invalid={!!formState.errors.prune} {...register('prune')} label={t('forms.allowPruneBox', { count: deletes })} />}
              </FormField>
            )}
            <FormField label={t('forms.title')} error={formState.errors.title?.message} hint={t('forms.titleHint')}>
              {(p) => <Input {...p} {...register('title')} placeholder={`${d.service} config sync @ ${d.env}`} autoComplete="off" />}
            </FormField>
            <FormField label={t('forms.jira')} error={formState.errors.jiraTicket?.message} required={req.jira} hint={req.jira ? undefined : t('forms.jiraOptional')}>
              {(p) => <Input {...p} {...register('jiraTicket')} mono className="uppercase" placeholder="OPS-1234" autoComplete="off" autoCapitalize="characters" spellCheck={false} />}
            </FormField>
            <FormField label={t('forms.reason')} error={formState.errors.reason?.message} required={req.reason} hint={t('forms.reasonHintSync', { extra: req.reason ? '' : t('forms.reasonOptionalSuffix') })}>
              {(p) => <Textarea {...p} {...register('reason')} placeholder={t('forms.syncPlaceholder', { env: d.env, service: d.service })} />}
            </FormField>
          </Group>
          <FormErrorBanner error={formError} />
          <Note>{t('forms.syncNote')}</Note>
          <ButtonRow>
            <Button type="submit" loading={formState.isSubmitting} loadingText={t('forms.submitting')} disabled={busy}>
              {t('forms.submit')}
            </Button>
            {busy && (
              <span className="note" style={{ padding: 0 }}>
                {t('forms.busyChange')}
              </span>
            )}
          </ButtonRow>
        </Form>
      )}
      {confirming && (
        <ConfirmSheet
          release={confirming}
          onClose={() => {
            setConfirming(null)
            nav(`/releases/${encodeURIComponent(confirming.id)}`)
          }}
          onDone={(r) => nav(`/releases/${encodeURIComponent(r.id)}`)}
        />
      )}
    </>
  )
}
