import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { applyServerError } from '../../../lib/forms'
import { TIERS, tierLabel } from '../../../lib/permissions'
import { Button, ButtonRow, EmptyState, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, Select, Textarea, useToast } from '../../../components/ui'
import { useSaveSettings } from '../queries'
import { environmentsSchema, type EnvironmentsValues } from '../schemas'
import type { CIMode, Environment } from '../types'
import { i18n } from '../../../lib/i18n'

// Written out rather than built from the mode list so every catalogue key is
// a literal that locales/keys.test.ts can check.
const ciOptions = (): [string, string][] => [
  ['off', i18n.t('envForm.ciOff')],
  ['approve', i18n.t('envForm.ciApprove')],
  ['auto', i18n.t('envForm.ciAuto')],
]

const ciHint = (mode: CIMode): string =>
  mode === 'auto' ? i18n.t('envForm.ciAutoHint') : mode === 'approve' ? i18n.t('envForm.ciApproveHint') : i18n.t('envForm.ciOffHint')

const emptyEnv = (): EnvironmentsValues['items'][number] => ({ name: '', displayName: '', tier: 'development', description: '', upstream: '', promotesFrom: '', ci: 'off' })

export function EnvironmentsForm({ initial, upstreams }: { initial: { items: Environment[] | null } | null | undefined; upstreams: string[] }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<{ items: Environment[] }>('environments')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<EnvironmentsValues>({
    resolver: zodResolver(environmentsSchema),
    mode: 'onTouched',
    defaultValues: { items: (initial?.items ?? []).map((e) => ({ ...e, displayName: e.displayName ?? '', description: e.description ?? '', upstream: e.upstream ?? '', promotesFrom: e.promotesFrom ?? '', ci: e.ci ?? 'off' })) },
  })
  const { register, handleSubmit, formState, control, watch } = form
  const { fields, append, remove, move } = useFieldArray({ control, name: 'items' })
  const items = watch('items')
  const errors = formState.errors.items

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({
        items: v.items.map((e) => ({ name: e.name.trim(), displayName: e.displayName.trim(), tier: e.tier, description: e.description.trim(), upstream: e.upstream, promotesFrom: e.promotesFrom, ci: e.ci })),
      })
      form.reset(v)
      toast.success(t('envForm.saved'))
    } catch (e) {
      // List-level errors ("items") have no control of their own: show them in the banner.
      setFormError(applyServerError(form, e, { mapField: (f) => (f === 'items' ? null : f) }))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit} aria-label={t('envForm.label')}>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>
        {t('envForm.note')}
      </Note>
      {fields.length === 0 && (
        <Group>
          <EmptyState>{t('envForm.none')}</EmptyState>
        </Group>
      )}
      {fields.map((f, i) => {
        const e = errors?.[i]
        const cur = items[i]
        const label = cur?.name || t('envForm.nth', { n: i + 1 })
        const upstreamOptions: [string, string][] = [['', t('envForm.notOnboarded')], ...upstreams.map((u): [string, string] => [u, u])]
        if (cur?.upstream && !upstreams.includes(cur.upstream)) upstreamOptions.push([cur.upstream, t('envForm.missing', { name: cur.upstream })])
        const sourceOptions: [string, string][] = [['', t('envForm.notNeeded')], ...items.slice(0, i).filter((x) => x.name.trim()).map((x): [string, string] => [x.name.trim(), x.displayName ? t('scope.envNamed', { display: x.displayName, name: x.name }) : x.name])]
        if (cur?.promotesFrom && !sourceOptions.some(([v]) => v === cur.promotesFrom)) sourceOptions.push([cur.promotesFrom, t('envForm.mustPrecede', { name: cur.promotesFrom })])
        return (
          <div key={f.id}>
            <GroupHeader
              right={
                <span className="inline-control">
                  <Button size="small" variant="quiet" disabled={i === 0} onClick={() => move(i, i - 1)} aria-label={t('envForm.moveUp', { label })}>
                    {t('envForm.up')}
                  </Button>
                  <Button size="small" variant="quiet" disabled={i === fields.length - 1} onClick={() => move(i, i + 1)} aria-label={t('envForm.moveDown', { label })}>
                    {t('envForm.down')}
                  </Button>
                  <Button size="small" variant="danger" disabled={fields.length === 1} onClick={() => remove(i)} aria-label={t('envForm.removeAria', { label })}>
                    {t('envForm.remove')}
                  </Button>
                </span>
              }
            >
              {i + 1}. {label}
              {cur?.tier && <span className="muted"> · {tierLabel(cur.tier)}</span>}
            </GroupHeader>
            <Group form>
              <FormField label={t('envForm.name')} error={e?.name?.message} required hint={t('envForm.nameHint')}>
                {(p) => <Input {...p} {...register(`items.${i}.name`)} mono placeholder="uat" autoComplete="off" spellCheck={false} />}
              </FormField>
              <FormField label={t('envForm.displayName')} error={e?.displayName?.message}>
                {(p) => <Input {...p} {...register(`items.${i}.displayName`)} placeholder={t('envForm.displayPlaceholder')} autoComplete="off" />}
              </FormField>
              <FormField label={t('envForm.tier')} error={e?.tier?.message} required>
                {(p) => <Select {...p} {...register(`items.${i}.tier`)} options={TIERS.map(([t]) => [t, tierLabel(t)] as const)} />}
              </FormField>
              <FormField label={t('envForm.upstream')} error={e?.upstream?.message}>
                {(p) => <Select {...p} {...register(`items.${i}.upstream`)} options={upstreamOptions} />}
              </FormField>
              <FormField
                label={t('envForm.promotesFrom')}
                error={e?.promotesFrom?.message}
                hint={t('envForm.promotesFromHint')}
              >
                {(p) => <Select {...p} {...register(`items.${i}.promotesFrom`)} options={sourceOptions} />}
              </FormField>
              <FormField label={t('envForm.ci')} error={e?.ci?.message} hint={ciHint(cur?.ci ?? 'off')}>
                {(p) => <Select {...p} {...register(`items.${i}.ci`)} options={ciOptions()} />}
              </FormField>
              <FormField label={t('envForm.description')} error={e?.description?.message}>
                {(p) => <Textarea {...p} {...register(`items.${i}.description`)} rows={2} placeholder={t('admin.optional')} />}
              </FormField>
            </Group>
          </div>
        )
      })}
      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button variant="quiet" disabled={fields.length >= 20} onClick={() => {
            setFormError(null)
            append(emptyEnv())
          }}>
          {t('envForm.addEnv')}
        </Button>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
          {t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
