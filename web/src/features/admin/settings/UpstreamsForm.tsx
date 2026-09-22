import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { isApiError } from '../../../lib/api'
import { ErrCode } from '../../../lib/errcode'
import { applyServerError } from '../../../lib/forms'
import type { CheckResult } from '../../../lib/types'
import { Banner, Button, ButtonRow, Checkbox, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, PasswordInput, Row, StatusDot, useToast, Form } from '../../../components/ui'
import { useSaveSettings, useTestUpstreams } from '../queries'
import { mapUpstreamsField, upstreamsSchema, type UpstreamItemValues, type UpstreamsValues } from '../schemas'
import { MASK, type Upstream } from '../types'

type Upstreams = { items: Upstream[] }

const emptyUpstream = (): UpstreamItemValues => ({
  name: '',
  kargoUrl: '',
  kargoToken: '',
  argocdUrl: '',
  argocdToken: '',
  registryUrl: '',
  registryUser: '',
  registryToken: '',
  insecureTls: false,
  grafanaUrl: '',
})

function toValues(u: { items: Upstream[] | null } | null | undefined): UpstreamsValues {
  if (!u) return { items: [emptyUpstream()] }
  return {
    items: (u.items ?? []).map((it) => ({
      name: it.name,
      kargoUrl: it.kargoUrl,
      kargoToken: it.kargoToken,
      argocdUrl: it.argocdUrl,
      argocdToken: it.argocdToken,
      registryUrl: it.registryUrl,
      registryUser: it.registryUser,
      registryToken: it.registryToken,
      insecureTls: it.insecureTls,
      grafanaUrl: it.grafanaUrl ?? '',
    })),
  }
}

function toBody(v: UpstreamsValues): Upstreams {
  return {
    items: v.items.map((it) => ({
      name: it.name.trim(),
      kargoUrl: it.kargoUrl.trim(),
      kargoToken: it.kargoToken === MASK ? it.kargoToken : it.kargoToken.trim(),
      argocdUrl: it.argocdUrl.trim(),
      argocdToken: it.argocdToken === MASK ? it.argocdToken : it.argocdToken.trim(),
      registryUrl: it.registryUrl.trim(),
      registryUser: it.registryUser.trim(),
      registryToken: it.registryToken,
      insecureTls: it.insecureTls,
      grafanaUrl: (it.grafanaUrl ?? '').trim(),
    })),
  }
}

function resultsOf(data: unknown): CheckResult[] | null {
  if (typeof data !== 'object' || data === null) return null
  const r = (data as { results?: unknown }).results
  return Array.isArray(r) ? (r as CheckResult[]) : null
}

// Saving tests every upstream against its real API first; nothing is stored
// unless all checks pass (4004 carries the check results).
export function UpstreamsForm({ initial }: { initial: { items: Upstream[] | null } | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<Upstreams>('upstreams')
  const test = useTestUpstreams()
  const [results, setResults] = useState<{ list: CheckResult[]; saved: boolean } | null>(null)
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<UpstreamsValues>({ resolver: zodResolver(upstreamsSchema), mode: 'onTouched', defaultValues: toValues(initial) })
  const { register, handleSubmit, formState, control, trigger, getValues, watch } = form
  const watchedItems = watch('items')
  const { fields, append, remove } = useFieldArray({ control, name: 'items' })
  const errors = formState.errors

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    setResults(null)
    try {
      const r = await save.mutateAsync(toBody(v))
      const list = resultsOf(r)
      if (list) setResults({ list, saved: true })
      form.reset(v)
      toast.success(t('upstreamForm.saved'))
    } catch (e) {
      if (isApiError(e, ErrCode.UpstreamCheckFailed)) {
        const list = resultsOf(e.data)
        if (list) setResults({ list, saved: false })
      }
      setFormError(applyServerError(form, e, { mapField: mapUpstreamsField }))
    }
  }, () => setFormError(null))

  const onTest = async () => {
    if (test.isPending) return
    setFormError(null)
    setResults(null)
    const ok = await trigger(undefined, { shouldFocus: true })
    if (!ok) return
    try {
      const r = await test.mutateAsync(toBody(getValues()))
      setResults({ list: r.results ?? [], saved: false })
    } catch (e) {
      setFormError(applyServerError(form, e, { mapField: mapUpstreamsField }))
    }
  }

  return (
    <Form onSubmit={onSubmit} aria-label={t('upstreamForm.label')}>
      {!initial && (
        <Banner tone="warn" style={{ marginTop: 0, marginBottom: 12 }}>
          {t('upstreamForm.none')}
        </Banner>
      )}
      {fields.map((f, i) => {
        const e = errors.items?.[i]
        const it = watchedItems[i]
        return (
          <div key={f.id}>
            <GroupHeader
              right={
                fields.length > 1 ? (
                  <Button size="small" variant="danger" onClick={() => remove(i)} aria-label={t('upstreamForm.removeAria', { name: it?.name || i + 1 })}>
                    {t('upstreamForm.remove')}
                  </Button>
                ) : undefined
              }
            >
              {t('upstreamForm.nth', { name: it?.name || i + 1 })}
            </GroupHeader>
            <Group form>
              <FormField label={t('upstreamForm.name')} error={e?.name?.message} required>
                {(p) => <Input {...p} {...register(`items.${i}.name`)} mono placeholder="onprem" autoComplete="off" spellCheck={false} />}
              </FormField>
              <FormField label={t('upstreamForm.kargoUrl')} error={e?.kargoUrl?.message} required>
                {(p) => <Input {...p} {...register(`items.${i}.kargoUrl`)} mono type="url" inputMode="url" placeholder="https://kargo.example.com" autoComplete="off" />}
              </FormField>
              <FormField label={t('upstreamForm.kargoToken')} error={e?.kargoToken?.message} required hint={t('upstreamForm.kargoTokenHint')}>
                {(p) => <PasswordInput {...p} {...register(`items.${i}.kargoToken`)} className="mono" autoComplete="off" />}
              </FormField>
              <FormField label={t('upstreamForm.argocdUrl')} error={e?.argocdUrl?.message} required>
                {(p) => <Input {...p} {...register(`items.${i}.argocdUrl`)} mono type="url" inputMode="url" placeholder="https://argocd.example.com" autoComplete="off" />}
              </FormField>
              <FormField label={t('upstreamForm.argocdToken')} error={e?.argocdToken?.message} required hint={t('upstreamForm.argocdTokenHint')}>
                {(p) => <PasswordInput {...p} {...register(`items.${i}.argocdToken`)} className="mono" autoComplete="off" />}
              </FormField>
              <FormField label={t('upstreamForm.registryUrl')} error={e?.registryUrl?.message}>
                {(p) => <Input {...p} {...register(`items.${i}.registryUrl`)} mono type="url" inputMode="url" placeholder="https://registry.example.com" autoComplete="off" />}
              </FormField>
              <FormField label={t('upstreamForm.registryUser')} error={e?.registryUser?.message}>
                {(p) => <Input {...p} {...register(`items.${i}.registryUser`)} mono autoComplete="off" />}
              </FormField>
              <FormField label={t('upstreamForm.registryToken')} error={e?.registryToken?.message}>
                {(p) => <PasswordInput {...p} {...register(`items.${i}.registryToken`)} className="mono" autoComplete="off" />}
              </FormField>
              <FormField label={t('upstreamForm.grafana')} error={e?.grafanaUrl?.message} hint={t('upstreamForm.grafanaHint')}>
                {(p) => <Input {...p} {...register(`items.${i}.grafanaUrl`)} mono type="url" inputMode="url" placeholder="https://grafana/d/x?var-service={service}&var-env={env}" autoComplete="off" />}
              </FormField>
              <FormField label="TLS" plainLabel>
                {(p) => <Checkbox id={p.id} {...register(`items.${i}.insecureTls`)} label={t('upstreamForm.insecureTls')} />}
              </FormField>
            </Group>
          </div>
        )
      })}
      {initial && <Note>{t('upstreamForm.note', { mask: MASK })}</Note>}

      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button variant="quiet" onClick={() => append(emptyUpstream())}>
          {t('upstreamForm.addUpstream')}
        </Button>
        <Button variant="quiet" loading={test.isPending} loadingText={t('upstreamForm.testing')} disabled={formState.isSubmitting} onClick={() => void onTest()}>
          {t('upstreamForm.testOnly')}
        </Button>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('upstreamForm.verifying')} disabled={test.isPending}>
          {t('upstreamForm.verifyAndSave')}
        </Button>
      </ButtonRow>

      {results && (
        <>
          <GroupHeader right={results.saved ? <span className="pill g">{t('upstreamForm.savedPill')}</span> : undefined}>{t('upstreamForm.checks')}</GroupHeader>
          <Group>
            {results.list.length === 0 && <div className="empty">{t('upstreamForm.noChecks')}</div>}
            {results.list.flatMap((r) =>
              (r.checks ?? []).map((c) => (
                <Row key={r.upstream + c.name}>
                  <StatusDot state={c.ok ? 'ok' : 'bad'} label={c.ok ? t('upstreamForm.pass') : t('upstreamForm.fail')} />
                  <div className="grow">
                    <div className="t">
                      {r.upstream} · {c.name}
                    </div>
                    <div className={`d ${c.ok ? '' : 'mono red'}`}>{c.detail}</div>
                  </div>
                </Row>
              )),
            )}
          </Group>
        </>
      )}
    </Form>
  )
}
