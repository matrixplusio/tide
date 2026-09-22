import { useTranslation } from 'react-i18next'
import { useId, useState } from 'react'
import { useFieldArray, useForm, type UseFormReturn } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { isApiError } from '../../../lib/api'
import { ErrCode } from '../../../lib/errcode'
import { applyServerError } from '../../../lib/forms'
import type { Dimension } from '../../../lib/types'
import { Button, ButtonRow, EmptyState, ErrorState, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Loading, Note, Select, useToast } from '../../../components/ui'
import { useServices } from '../../services/queries'
import { useCatalogLabels, useSaveSettings } from '../queries'
import { catalogSchema, type CatalogValues } from '../schemas'
import type { CatalogSettings, DiscoveredLabel } from '../types'

const MAX_DIMENSIONS = 5

const emptyDimension = (): CatalogValues['dimensions'][number] => ({ key: '', name: '', label: '', values: [] })

function toValues(c: CatalogSettings | null | undefined): CatalogValues {
  return {
    serviceLabel: c?.serviceLabel ?? '',
    envLabel: c?.envLabel ?? '',
    domainLabel: c?.domainLabel ?? '',
    projectLabel: c?.projectLabel ?? '',
    batchDimension: c?.batchDimension ?? '',
    dimensions: (c?.dimensions ?? []).map((d) => ({ key: d.key, name: d.name, label: d.label, values: (d.values ?? []).map((v) => ({ value: v.value, name: v.name ?? '' })) })),
  }
}

/** Mirrors catalog.FromArgoProject: a dimension source, not a label name. */
const FROM_ARGO_PROJECT = 'argocd:project'

export function CatalogForm({ initial }: { initial: CatalogSettings | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<CatalogSettings>('catalog')
  const labels = useCatalogLabels()
  const discovered = labels.data?.items ?? []
  const listId = useId()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<CatalogValues>({ resolver: zodResolver(catalogSchema), mode: 'onTouched', defaultValues: toValues(initial) })
  const { register, handleSubmit, formState, control, watch } = form
  const { fields, append, remove, move } = useFieldArray({ control, name: 'dimensions' })
  const errors = formState.errors

  const onSubmit = handleSubmit(
    async (v) => {
      setFormError(null)
      const body: CatalogSettings = {
        serviceLabel: v.serviceLabel.trim(),
        envLabel: v.envLabel.trim(),
        domainLabel: v.domainLabel.trim(),
        projectLabel: v.projectLabel.trim(),
        batchDimension: v.batchDimension,
        dimensions: v.dimensions.map((d): Dimension => ({
          key: d.key.trim(),
          name: d.name.trim(),
          label: d.label.trim(),
          values: d.values.map((x) => ({ value: x.value.trim(), name: x.name.trim() })),
        })),
      }
      try {
        await save.mutateAsync(body)
        form.reset(v)
        toast.success(t('catalogForm.saved'))
      } catch (e) {
        setFormError(applyServerError(form, e, { mapField: (f) => (f === 'dimensions' ? null : f) }))
      }
    },
    () => setFormError(null),
  )

  return (
    <Form onSubmit={onSubmit} aria-label={t('catalogForm.label')}>
      <GroupHeader>{t('catalogForm.rules')}</GroupHeader>
      <Group form>
        <FormField label={t('catalogForm.serviceLabel')} error={errors.serviceLabel?.message} hint={t('catalogForm.serviceLabelHint')}>
          {(p) => <Input {...p} {...register('serviceLabel')} list={listId} mono placeholder="tide.io/service" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('catalogForm.envLabel')} error={errors.envLabel?.message} hint={t('catalogForm.envLabelHint')}>
          {(p) => <Input {...p} {...register('envLabel')} list={listId} mono placeholder="tide.io/env" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField
          label={t('catalogForm.projectLabel')}
          error={errors.projectLabel?.message}
          hint={`${t('catalogForm.projectLabelHint')} ${t('catalogForm.argoProjectHint')}`}
        >
          {(p) => <Input {...p} {...register('projectLabel')} list={listId} mono placeholder="tide.io/project" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField
          label={t('catalogForm.domainLabel')}
          error={errors.domainLabel?.message}
          hint={t('catalogForm.domainLabelHint')}
        >
          {(p) => <Input {...p} {...register('domainLabel')} list={listId} mono placeholder="tide.io/domain" autoComplete="off" spellCheck={false} />}
        </FormField>
      </Group>

      <RecognizedPreview />

      <GroupHeader>{t('catalogForm.dimensions')}</GroupHeader>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>
        {t('catalogForm.dimensionsNote')}
      </Note>
      {fields.length === 0 && (
        <Group>
          <EmptyState>{t('catalogForm.noDimensions')}</EmptyState>
        </Group>
      )}
      {fields.map((f, i) => (
        <DimensionFields key={f.id} form={form} index={i} total={fields.length} listId={listId} discovered={discovered} onMove={(to) => move(i, to)} onRemove={() => remove(i)} />
      ))}
      <ButtonRow>
        <Button
          variant="quiet"
          disabled={fields.length >= MAX_DIMENSIONS}
          onClick={() => {
            setFormError(null)
            append(emptyDimension())
          }}
        >
          {t('catalogForm.addDimension')}
        </Button>
      </ButtonRow>

      <GroupHeader>{t('catalogForm.batchRules')}</GroupHeader>
      <Group form>
        <FormField
          label={t('catalogForm.batchDimension')}
          error={errors.batchDimension?.message}
          hint={t('catalogForm.batchDimensionHint')}
        >
          {(p) => (
            <Select
              {...p}
              {...register('batchDimension')}
              options={[
                ['', t('catalogForm.unrestricted')],
                ...watch('dimensions')
                  .filter((d) => d.key.trim() !== '')
                  .map((d): [string, string] => [d.key.trim(), d.name || d.key]),
              ]}
            />
          )}
        </FormField>
      </Group>

      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
          {t('settings.save')}
        </Button>
      </ButtonRow>

      <datalist id={listId}>
        {/* Not a label: it reads the Argo CD project instead. Offered first
            because setups whose AppProject already names the thing Tide wants
            need no labelling at all. */}
        <option value={FROM_ARGO_PROJECT}>{t('catalogForm.fromArgoProject')}</option>
        {discovered.map((l) => (
          <option key={l.key} value={l.key}>
            {t('catalogForm.appCount', { count: l.count })}
          </option>
        ))}
      </datalist>
      <DiscoveredLabels query={labels} />
    </Form>
  )
}

function DimensionFields({
  form,
  index: i,
  total,
  listId,
  discovered,
  onMove,
  onRemove,
}: {
  form: UseFormReturn<CatalogValues>
  index: number
  total: number
  listId: string
  discovered: DiscoveredLabel[]
  onMove: (to: number) => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const { register, control, watch, formState } = form
  const values = useFieldArray({ control, name: `dimensions.${i}.values` })
  const e = formState.errors.dimensions?.[i]
  const cur = watch(`dimensions.${i}`)
  const title = cur.name || t('catalogForm.dimensionNth', { n: i + 1 })
  const found = discovered.find((l) => l.key === cur.label.trim())
  const missing = (found?.values ?? []).filter((v) => !cur.values.some((x) => x.value.trim() === v.value))

  return (
    <div>
      <GroupHeader
        right={
          <span className="inline-control">
            <Button size="small" variant="quiet" disabled={i === 0} onClick={() => onMove(i - 1)} aria-label={t('catalogForm.moveUp', { title })}>
              {t('catalogForm.up')}
            </Button>
            <Button size="small" variant="quiet" disabled={i === total - 1} onClick={() => onMove(i + 1)} aria-label={t('catalogForm.moveDown', { title })}>
              {t('catalogForm.down')}
            </Button>
            <Button size="small" variant="danger" onClick={onRemove} aria-label={t('catalogForm.removeAria', { title })}>
              {t('catalogForm.remove')}
            </Button>
          </span>
        }
      >
        {i + 1}. {title}
      </GroupHeader>
      <Group form>
        <FormField label={t('catalogForm.name')} error={e?.name?.message} required hint={t('catalogForm.nameHint')}>
          {(p) => <Input {...p} {...register(`dimensions.${i}.name`)} placeholder={t('catalogForm.namePlaceholder')} autoComplete="off" />}
        </FormField>
        <FormField label={t('catalogForm.key')} error={e?.key?.message} required hint={t('catalogForm.keyHint')}>
          {(p) => <Input {...p} {...register(`dimensions.${i}.key`)} mono placeholder="role" autoComplete="off" spellCheck={false} />}
        </FormField>
        <FormField label={t('catalogForm.labelKey')} error={e?.label?.message} required>
          {(p) => <Input {...p} {...register(`dimensions.${i}.label`)} list={listId} mono placeholder="example.com/role" autoComplete="off" spellCheck={false} />}
        </FormField>
        {values.fields.map((vf, j) => {
          const ve = e?.values?.[j]
          return (
            <FormField key={vf.id} label={j === 0 ? t('catalogForm.valuesAndNames') : ''} error={ve?.value?.message ?? ve?.name?.message}>
              {(p) => (
                <span className="inline-control">
                  <Input {...p} {...register(`dimensions.${i}.values.${j}.value`)} mono placeholder="backend" autoComplete="off" spellCheck={false} aria-label={t('catalogForm.valueNth', { n: j + 1 })} />
                  <Input {...register(`dimensions.${i}.values.${j}.name`)} placeholder={t('catalogForm.valuePlaceholder')} autoComplete="off" aria-label={t('catalogForm.valueNthName', { n: j + 1 })} />
                  <Button size="small" variant="quiet" onClick={() => values.remove(j)} aria-label={t('catalogForm.deleteValue', { n: j + 1 })}>
                    {t('catalogForm.delete')}
                  </Button>
                </span>
              )}
            </FormField>
          )
        })}
        <div className="row">
          <span className="flabel">{values.fields.length === 0 ? t('catalogForm.valuesAndNames') : ''}</span>
          <span className="inline-control">
            <Button size="small" variant="quiet" disabled={values.fields.length >= 50} onClick={() => values.append({ value: '', name: '' })}>
              {t('catalogForm.addValue')}
            </Button>
            {missing.length > 0 && (
              <Button
                size="small"
                variant="quiet"
                disabled={values.fields.length + missing.length > 50}
                onClick={() => values.append(missing.map((v) => ({ value: v.value, name: '' })))}
              >
                {t('catalogForm.fillDiscovered', { count: missing.length })}
              </Button>
            )}
          </span>
        </div>
      </Group>
    </div>
  )
}

/** What the saved rules currently produce: domains and the services in them. */
function RecognizedPreview() {
  const { t } = useTranslation()
  const q = useServices()
  const byDomain = new Map<string, string[]>()
  for (const s of q.data?.services ?? []) {
    const key = `${s.project || t('catalogForm.noProject')} / ${s.domain}`
    byDomain.set(key, [...(byDomain.get(key) ?? []), s.name])
  }
  const domains = [...byDomain].sort(([a], [b]) => a.localeCompare(b))
  return (
    <section aria-label={t('catalogForm.resultLabel')}>
      <GroupHeader>{t('catalogForm.resultLabel')}</GroupHeader>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('catalogForm.resultNote')}</Note>
      <Group>
        {q.isPending && <Loading />}
        {isApiError(q.error, ErrCode.NoUpstreams) ? <EmptyState>{t('catalogForm.noUpstreams')}</EmptyState> : q.error && <ErrorState error={q.error} inline onRetry={() => void q.refetch()} />}
        {q.data && domains.length === 0 && <EmptyState>{t('catalogForm.nothingRecognised')}</EmptyState>}
        {domains.map(([domain, services]) => (
          <div className="row" key={domain}>
            <div className="grow">
              <div className="t">
                <b>{domain}</b>
              </div>
              <div className="d break">{t('catalogForm.servicesLine', { list: services.sort().join(t('scope.listSeparator')) })}</div>
            </div>
            <span className="muted nowrap">{t('catalogForm.serviceCount', { count: services.length })}</span>
          </div>
        ))}
      </Group>
    </section>
  )
}

function DiscoveredLabels({ query }: { query: ReturnType<typeof useCatalogLabels> }) {
  const { t } = useTranslation()
  const items = query.data?.items ?? []
  return (
    <section aria-label={t('catalogForm.discoveredLabel')}>
      <GroupHeader>{t('catalogForm.appLabels')}</GroupHeader>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('catalogForm.appLabelsNote')}</Note>
      <Group>
        {query.isPending && <Loading />}
        {isApiError(query.error, ErrCode.NoUpstreams) ? (
          <EmptyState>{t('catalogForm.noUpstreamsYet')}</EmptyState>
        ) : (
          query.error && <ErrorState error={query.error} inline onRetry={() => void query.refetch()} />
        )}
        {query.data && items.length === 0 && <EmptyState>{t('catalogForm.noLabels')}</EmptyState>}
        {items.map((l) => (
          <div className="row" key={l.key}>
            <div className="grow">
              <div className="mono">{l.key}</div>
              <div className="muted break">{(l.values ?? []).map((v) => `${v.value}（${v.count}）`).join('、')}</div>
            </div>
            <span className="muted nowrap">{t('catalogForm.appCount', { count: l.count })}</span>
          </div>
        ))}
      </Group>
    </section>
  )
}
