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
import { mapRestartField, normalizeJira, requiredFields, restartSchema, type RestartValues } from './schema'

/**
 * Restart without upgrading: for config changes (Nacos, ConfigMaps, secrets)
 * that the running version only picks up on start. Goes through the same
 * release flow as an upgrade: Jira, reason, ten-second confirmation, audit.
 */
export function RestartForm({ d, canOperate, busy }: { d: Deployment; canOperate: boolean; busy: boolean }) {
  const { t } = useTranslation()
  if (!canOperate) return <Banner tone="warn">{t('forms.noRestartPermission', { env: d.env })}</Banner>
  return <RestartFormInner d={d} busy={busy} />
}

function RestartFormInner({ d, busy }: { d: Deployment; busy: boolean }) {
  const { t } = useTranslation()
  const nav = useNavigate()
  const req = requiredFields(useMe().app, d.env)
  const create = useCreateRelease()
  const [confirming, setConfirming] = useState<Release | null>(null)
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<RestartValues>({ resolver: zodResolver(restartSchema(req)), mode: 'onTouched', defaultValues: { title: '', jiraTicket: '', reason: '' } })
  const { register, handleSubmit, formState } = form

  const onSubmit = handleSubmit(
    async (v) => {
      setFormError(null)
      try {
        const r = await create.mutateAsync({
          env: d.env,
          title: v.title.trim(),
          jiraTicket: normalizeJira(v.jiraTicket),
          reason: v.reason.trim(),
          items: [{ kind: 'restart', service: d.service, sequence: 1 }],
        })
        setConfirming(r)
      } catch (e) {
        setFormError(applyServerError(form, e, { mapField: mapRestartField }))
      }
    },
    () => setFormError(null),
  )

  return (
    <>
      <Form onSubmit={onSubmit} aria-label={t('forms.restartLabel')}>
        <GroupHeader>{t('forms.restartHeader', { env: d.env })}</GroupHeader>
        <Group form>
          <FormField label={t('forms.version')} plainLabel>
            {() => (
              <span className="t" style={{ paddingTop: 5 }}>
                {t('forms.keep')} <VersionLabel a={d} full />
              </span>
            )}
          </FormField>
          <FormField label={t('forms.title')} error={formState.errors.title?.message} hint={t('forms.titleHint')}>
            {(p) => <Input {...p} {...register('title')} placeholder={`${d.service} restart @ ${d.env}`} autoComplete="off" />}
          </FormField>
          <FormField label={t('forms.jira')} error={formState.errors.jiraTicket?.message} required={req.jira} hint={req.jira ? undefined : t('forms.jiraOptional')}>
            {(p) => (
              <Input {...p} {...register('jiraTicket')} mono className="uppercase" placeholder="OPS-1234" autoComplete="off" autoCapitalize="characters" spellCheck={false} />
            )}
          </FormField>
          <FormField label={t('forms.reason')} error={formState.errors.reason?.message} required={req.reason} hint={t('forms.reasonHintRestart', { extra: req.reason ? '' : t('forms.reasonOptionalSuffix') })}>
            {(p) => <Textarea {...p} {...register('reason')} placeholder={t('forms.restartPlaceholder', { env: d.env, service: d.service })} />}
          </FormField>
        </Group>
        <FormErrorBanner error={formError} />
        <Note>{t('forms.restartNote')}</Note>
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
