import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Controller, useFieldArray, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { errorMessage } from '../../../lib/api'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, Checkbox, CheckboxGroup, EmptyState, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, PasswordInput, Select, useToast } from '../../../components/ui'
import { EnvSelector } from '../../../components/domain'
import { useSaveSettings, useTestChannel } from '../queries'
import { channelSchema, mapListField, notifySchema, parseServices, NOTIFY_EVENTS, type NotifyValues } from '../schemas'
import { MASK, type Channel, type Notify, type NotifyEvent } from '../types'

const kinds = [
  ['lark', 'notifyForm.kindLark'],
  ['teams', 'Teams'],
  ['webhook', 'notifyForm.kindWebhook'],
] as const

const EVENT_LABELS: Record<NotifyEvent, string> = {
  'release.pending': 'notifyForm.evtPending',
  'release.approval_requested': 'notifyForm.evtApproval',
  'release.started': 'notifyForm.evtStarted',
  'release.succeeded': 'notifyForm.evtSucceeded',
  'release.failed': 'notifyForm.evtFailed',
  'release.rejected': 'notifyForm.evtRejected',
  'release.cancelled': 'notifyForm.evtCancelled',
  'build.failed': 'notifyForm.evtBuildFailed',
  'build.warning': 'notifyForm.evtBuildWarning',
}

type ChannelValues = NotifyValues['channels'][number]

function toChannel(c: ChannelValues): Channel {
  return {
    name: c.name.trim(),
    kind: c.kind,
    url: c.url === MASK ? MASK : c.url.trim(),
    secret: c.secret === MASK ? MASK : c.secret.trim(),
    enabled: c.enabled,
  }
}

function toValues(n: Notify | null | undefined): NotifyValues {
  return {
    channels: (n?.channels ?? []).map((c) => ({ name: c.name, kind: c.kind, url: c.url, secret: c.secret ?? '', enabled: c.enabled })),
    rules: (n?.rules ?? []).map((r) => ({
      name: r.name,
      enabled: r.enabled,
      envs: r.envs ?? [],
      events: r.events ?? [],
      services: (r.services ?? []).join(', '),
      channels: r.channels ?? [],
    })),
  }
}

export function NotifyForm({ initial }: { initial: Notify | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<Notify>('notify')
  const test = useTestChannel()
  const [testing, setTesting] = useState<number | null>(null)
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<NotifyValues>({ resolver: zodResolver(notifySchema), mode: 'onTouched', defaultValues: toValues(initial) })
  const { register, handleSubmit, formState, control, watch, getValues, setError, clearErrors } = form
  const channels = useFieldArray({ control, name: 'channels' })
  const rules = useFieldArray({ control, name: 'rules' })
  const errors = formState.errors
  const channelNames = watch('channels')
    .map((c) => c.name.trim())
    .filter(Boolean)

  const mapField = (f: string) => mapListField(f, { controls: ['envs', 'events', 'channels'] })

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({
        channels: v.channels.map(toChannel),
        rules: v.rules.map((r) => ({
          name: r.name.trim(),
          enabled: r.enabled,
          envs: r.envs,
          events: r.events as NotifyEvent[],
          services: parseServices(r.services),
          channels: r.channels,
        })),
      })
      form.reset(v)
      toast.success(t('notifyForm.saved'))
    } catch (e) {
      setFormError(applyServerError(form, e, { mapField }))
    }
  }, () => setFormError(null))

  // Test one channel as currently typed; masked values use the saved channel of the same name.
  const onTest = async (i: number) => {
    if (testing !== null) return
    const c = getValues(`channels.${i}`)
    const parsed = channelSchema.safeParse(c)
    if (!parsed.success || c.name.trim() === '') {
      if (c.name.trim() === '') setError(`channels.${i}.name`, { type: 'manual', message: t('notifyForm.nameRequired') }, { shouldFocus: true })
      for (const issue of parsed.success ? [] : parsed.error.issues) {
        const key = issue.path[0]
        if (key === 'url' || key === 'secret') setError(`channels.${i}.${key}`, { type: 'manual', message: issue.message }, { shouldFocus: true })
      }
      return
    }
    clearErrors([`channels.${i}.url`, `channels.${i}.secret`])
    setTesting(i)
    try {
      await test.mutateAsync(toChannel(c))
      toast.success(t('notifyForm.testSent', { name: c.name.trim() }))
    } catch (e) {
      const rest = applyServerError(form, e, { mapField: (f) => (f.startsWith('channel.') ? `channels.${i}.${f.slice('channel.'.length)}` : null) })
      if (rest) toast.error(errorMessage(rest))
    } finally {
      setTesting(null)
    }
  }

  return (
    <Form onSubmit={onSubmit} aria-label={t('notifyForm.label')}>
      <GroupHeader>{t('notifyForm.channels')}</GroupHeader>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('notifyForm.channelsNote')}</Note>
      {channels.fields.length === 0 && (
        <Group>
          <EmptyState>{t('notifyForm.noChannels')}</EmptyState>
        </Group>
      )}
      {channels.fields.map((f, i) => {
        const e = errors.channels?.[i]
        const label = watch(`channels.${i}.name`) || t('notifyForm.channelNth', { n: i + 1 })
        return (
          <div key={f.id}>
            <GroupHeader
              right={
                <span className="inline-control">
                  <Button size="small" variant="quiet" loading={testing === i} loadingText={t('notifyForm.sending')} disabled={testing !== null && testing !== i} onClick={() => void onTest(i)}>
                    {t('notifyForm.sendTest')}
                  </Button>
                  <Button size="small" variant="danger" onClick={() => channels.remove(i)} aria-label={t('notifyForm.removeAria', { label })}>
                    {t('notifyForm.remove')}
                  </Button>
                </span>
              }
            >
              {label}
            </GroupHeader>
            <Group form>
              <FormField label={t('notifyForm.name')} error={e?.name?.message} required hint={t('notifyForm.nameHint')}>
                {(p) => <Input {...p} {...register(`channels.${i}.name`)} placeholder="ops" autoComplete="off" />}
              </FormField>
              <FormField label={t('notifyForm.kind')} error={e?.kind?.message} required>
                {(p) => <Select {...p} {...register(`channels.${i}.kind`)} options={kinds.map(([v, k]) => [v, t(k)] as const)} />}
              </FormField>
              <FormField label={t('notifyForm.url')} error={e?.url?.message} required hint={f.url === MASK ? t('notifyForm.urlKept', { mask: MASK }) : undefined}>
                {(p) => <PasswordInput {...p} {...register(`channels.${i}.url`)} className="mono" autoComplete="off" placeholder="https://open.larksuite.com/open-apis/bot/v2/hook/…" />}
              </FormField>
              <FormField label={t('notifyForm.secret')} error={e?.secret?.message} hint={f.secret === MASK ? t('notifyForm.secretKept', { mask: MASK }) : t('notifyForm.optional')}>
                {(p) => <PasswordInput {...p} {...register(`channels.${i}.secret`)} className="mono" autoComplete="off" />}
              </FormField>
              <FormField label={t('notifyForm.status')} plainLabel>
                {(p) => <Checkbox id={p.id} {...register(`channels.${i}.enabled`)} label={t('notifyForm.enabled')} />}
              </FormField>
            </Group>
          </div>
        )
      })}
      <ButtonRow>
        <Button variant="quiet" onClick={() => channels.append({ name: '', kind: 'lark', url: '', secret: '', enabled: true })}>
          {t('notifyForm.addChannel')}
        </Button>
      </ButtonRow>

      <GroupHeader>{t('notifyForm.rules')}</GroupHeader>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('notifyForm.rulesNote')}</Note>
      {rules.fields.length === 0 && (
        <Group>
          <EmptyState>{t('notifyForm.noRules')}</EmptyState>
        </Group>
      )}
      {rules.fields.map((f, i) => {
        const e = errors.rules?.[i]
        const label = watch(`rules.${i}.name`) || t('notifyForm.ruleNth', { n: i + 1 })
        return (
          <div key={f.id}>
            <GroupHeader
              right={
                <Button size="small" variant="danger" onClick={() => rules.remove(i)} aria-label={t('notifyForm.removeAria', { label })}>
                  {t('notifyForm.remove')}
                </Button>
              }
            >
              {label}
            </GroupHeader>
            <Group form>
              <FormField label={t('notifyForm.name')} error={e?.name?.message} required>
                {(p) => <Input {...p} {...register(`rules.${i}.name`)} placeholder={t('notifyForm.rulePlaceholder')} autoComplete="off" />}
              </FormField>
              <FormField label={t('notifyForm.status')} plainLabel>
                {(p) => <Checkbox id={p.id} {...register(`rules.${i}.enabled`)} label={t('notifyForm.enabled')} />}
              </FormField>
              <FormField label={t('notifyForm.env')} error={e?.envs?.message} required plainLabel>
                {(p) => <Controller control={control} name={`rules.${i}.envs`} render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
              </FormField>
              <FormField label={t('notifyForm.events')} error={e?.events?.message} required plainLabel>
                {(p) => (
                  <Controller
                    control={control}
                    name={`rules.${i}.events`}
                    render={({ field }) => (
                      <CheckboxGroup
                        {...p}
                        ref={field.ref}
                        options={NOTIFY_EVENTS.map((ev) => ({ value: ev, label: t(EVENT_LABELS[ev]) }))}
                        value={field.value}
                        onChange={field.onChange}
                        onBlur={field.onBlur}
                      />
                    )}
                  />
                )}
              </FormField>
              <FormField label={t('notifyForm.services')} error={e?.services?.message} hint={t('notifyForm.servicesHint')}>
                {(p) => <Input {...p} {...register(`rules.${i}.services`)} placeholder={t('notifyForm.servicesPlaceholder')} autoComplete="off" />}
              </FormField>
              <FormField label={t('notifyForm.channelsField')} error={e?.channels?.message} required plainLabel hint={channelNames.length === 0 ? t('notifyForm.addChannelFirst') : undefined}>
                {(p) => (
                  <Controller
                    control={control}
                    name={`rules.${i}.channels`}
                    render={({ field }) => {
                      const missing = field.value.filter((c) => !channelNames.includes(c))
                      return (
                        <CheckboxGroup
                          {...p}
                          ref={field.ref}
                          options={[...new Set(channelNames)].map((c) => ({ value: c, label: c })).concat(missing.map((c) => ({ value: c, label: t('notifyForm.channelMissing', { name: c }) })))}
                          value={field.value}
                          onChange={field.onChange}
                          onBlur={field.onBlur}
                        />
                      )
                    }}
                  />
                )}
              </FormField>
            </Group>
          </div>
        )
      })}

      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button variant="quiet" onClick={() => rules.append({ name: '', enabled: true, envs: ['*'], events: ['release.started', 'release.succeeded', 'release.failed'], services: '', channels: [] })}>
          {t('notifyForm.addRule')}
        </Button>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
          {t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
