import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMe } from '../../app/session'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../lib/forms'
import type { Deployment, Release } from '../../lib/types'
import { Banner, Button, ButtonRow, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, Textarea } from '../../components/ui'
import { ConfirmSheet, VersionLabel } from '../../components/domain'
import { useCreateRelease } from '../releases/queries'
import { mapScaleField, normalizeJira, requiredFields, scaleSchema, type ScaleValues } from './schema'

/**
 * Change how many replicas a service runs in this environment.
 *
 * The number is written into git, not applied to the cluster: a scale applied
 * to the cluster is undone by the next sync, and until then the Application is
 * OutOfSync, which stalls the sync wave it belongs to.
 *
 * Zero is allowed and is the reason the form says what it says: it takes the
 * service out of service. The workload, its Service and its routes all stay —
 * a request gets a 503 rather than failing to resolve — but nothing answers.
 */
export function ScaleForm({ d, canOperate, busy }: { d: Deployment; canOperate: boolean; busy: boolean }) {
  const { t } = useTranslation()
  if (!canOperate) return <Banner tone="warn">{t('forms.noScalePermission', { env: d.env })}</Banner>
  return <ScaleFormInner d={d} busy={busy} />
}

function ScaleFormInner({ d, busy }: { d: Deployment; busy: boolean }) {
  const { t } = useTranslation()
  const nav = useNavigate()
  const req = requiredFields(useMe().app, d.env)
  const create = useCreateRelease()
  const [confirming, setConfirming] = useState<Release | null>(null)
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<ScaleValues>({
    resolver: zodResolver(scaleSchema(req)),
    mode: 'onTouched',
    defaultValues: { replicas: '', title: '', jiraTicket: '', reason: '' },
  })
  const { register, handleSubmit, formState, watch } = form
  const toZero = watch('replicas').trim() === '0'

  const onSubmit = handleSubmit(
    async (v) => {
      setFormError(null)
      try {
        const r = await create.mutateAsync({
          env: d.env,
          title: v.title.trim(),
          jiraTicket: normalizeJira(v.jiraTicket),
          reason: v.reason.trim(),
          items: [{ kind: 'scale', service: d.service, sequence: 1, replicas: Number(v.replicas) }],
        })
        setConfirming(r)
      } catch (e) {
        setFormError(applyServerError(form, e, { mapField: mapScaleField }))
      }
    },
    () => setFormError(null),
  )

  return (
    <>
      <Form onSubmit={onSubmit} aria-label={t('forms.scaleLabel')}>
        <GroupHeader>{t('forms.scaleHeader', { env: d.env })}</GroupHeader>
        <Group form>
          <FormField label={t('forms.version')} plainLabel>
            {() => (
              <span className="t" style={{ paddingTop: 5 }}>
                {t('forms.keep')} <VersionLabel a={d} full />
              </span>
            )}
          </FormField>
          <FormField label={t('forms.replicas')} error={formState.errors.replicas?.message} required hint={t('forms.replicasHint')}>
            {(p) => <Input {...p} {...register('replicas')} mono inputMode="numeric" placeholder="2" autoComplete="off" />}
          </FormField>
          <FormField label={t('forms.title')} error={formState.errors.title?.message} hint={t('forms.titleHint')}>
            {(p) => <Input {...p} {...register('title')} placeholder={`${d.service} replicas @ ${d.env}`} autoComplete="off" />}
          </FormField>
          <FormField label={t('forms.jira')} error={formState.errors.jiraTicket?.message} required={req.jira} hint={req.jira ? undefined : t('forms.jiraOptional')}>
            {(p) => (
              <Input {...p} {...register('jiraTicket')} mono className="uppercase" placeholder="OPS-1234" autoComplete="off" autoCapitalize="characters" spellCheck={false} />
            )}
          </FormField>
          <FormField label={t('forms.reason')} error={formState.errors.reason?.message} required={req.reason} hint={t('forms.reasonHintScale')}>
            {(p) => <Textarea {...p} {...register('reason')} placeholder={t('forms.scalePlaceholder', { env: d.env, service: d.service })} />}
          </FormField>
        </Group>
        <FormErrorBanner error={formError} />
        {/* Zero reads like any other number in a box, and it is not: nothing
            answers afterwards. Saying so next to the field, not after the
            fact. */}
        {toZero ? <Banner tone="warn">{t('forms.scaleZeroWarning')}</Banner> : <Note>{t('forms.scaleNote')}</Note>}
        <ButtonRow>
          <Button type="submit" variant={toZero ? 'danger' : undefined} loading={formState.isSubmitting} loadingText={t('forms.submitting')} disabled={busy}>
            {t('forms.submit')}
          </Button>
          {busy && (
            <span className="note" style={{ padding: 0 }}>
              {t('forms.busyChange')}
            </span>
          )}
        </ButtonRow>
      </Form>
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
