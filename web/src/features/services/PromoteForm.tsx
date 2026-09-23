import { useTranslation } from 'react-i18next'
import { useId, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Controller, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMe } from '../../app/session'
import { approvalFor, approvalText, changeSummary, enforcedThresholds } from '../../lib/release'
import { applyServerError } from '../../lib/forms'
import { fmtTime } from '../../lib/format'
import type { Candidate, Deployment, Release } from '../../lib/types'
import { Banner, Button, ButtonRow, Checkbox, EmptyState, ErrorState, FormErrorBanner, FormField, Group, GroupHeader, Input, Loading, Note, Pill, Segmented, Textarea, Form } from '../../components/ui'
import { ConfigChanges, ConfirmSheet } from '../../components/domain'
import { useCreateRelease } from '../releases/queries'
import { useCandidates, useConfigDiff, useServiceScope } from './queries'
import { mapReleaseField, normalizeJira, requiredFields, promoteSchema, type PromoteValues } from './schema'

type Scope = 'available' | 'all'

export function PromoteForm({ d, canOperate, busy }: { d: Deployment; canOperate: boolean; busy: boolean }) {
  const { t } = useTranslation()
  if (!d.kargoProject) return <Banner>{t('forms.notKargoManaged')}</Banner>
  if (!canOperate) return <Banner tone="warn">{t('forms.noPromotePermission', { env: d.env })}</Banner>
  return <PromoteFormInner d={d} busy={busy} />
}

function PromoteFormInner({ d, busy }: { d: Deployment; busy: boolean }) {
  const { t } = useTranslation()
  const nav = useNavigate()
  const me = useMe()
  const req = requiredFields(me.app, d.env)
  const enforced = enforcedThresholds(me.app, d.env)
  const [scope, setScope] = useState<Scope>('available')
  const [confirming, setConfirming] = useState<Release | null>(null)
  const [formError, setFormError] = useState<unknown>(null)
  const create = useCreateRelease()
  const cands = useCandidates(d.service, d.env, scope === 'all', true)
  // Kargo syncs the whole Application: unsynced git changes would go out with the image.
  const drift = useConfigDiff(d.service, d.env, true)
  const driftChanges = drift.data?.changes ?? []
  const driftEnforced = !!me.app.configDriftEnforced?.includes(d.env)
  const svcScope = useServiceScope(d.service)
  const rule = approvalFor(me.app, d.env, svcScope.project, svcScope.type)
  const listErrId = useId()

  const form = useForm<PromoteValues>({
    resolver: zodResolver(promoteSchema(req)),
    mode: 'onTouched',
    defaultValues: { freight: '', title: '', jiraTicket: '', reason: '', withConfig: false },
  })
  const { register, handleSubmit, formState, control, setValue } = form

  const items = cands.data?.items ?? []
  const upstreamNames = cands.data?.upstreamStages ?? []
  const warehouses = (cands.data?.warehouses ?? []).join(' / ')
  // Freight straight from CI: nothing to verify upstream first.
  const fromCI = !!cands.data && upstreamNames.length === 0

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      const r = await create.mutateAsync({
        env: d.env,
        title: v.title.trim(),
        jiraTicket: normalizeJira(v.jiraTicket),
        reason: v.reason.trim(),
        items: [{ service: d.service, freight: v.freight, sequence: 1, withConfig: driftChanges.length > 0 && v.withConfig }],
      })
      setConfirming(r)
    } catch (e) {
      setFormError(applyServerError(form, e, { mapField: mapReleaseField }))
    }
  }, () => setFormError(null))

  return (
    <>
    <Form onSubmit={onSubmit} aria-label={t('forms.promoteLabel')}>
      <GroupHeader
        id={`${listErrId}-h`}
        right={
          <Segmented
            label={t('forms.freightScope')}
            value={scope}
            onChange={(v) => {
              setScope(v)
              setValue('freight', '', { shouldValidate: formState.isSubmitted })
            }}
            options={[
              ['available', t('forms.scopeAvailable', { count: cands.data?.availableCount ?? '' })],
              ['all', t('forms.scopeAll', { count: cands.data?.totalCount ?? '' })],
            ]}
          />
        }
      >
        {fromCI ? t('forms.pickCi', { env: d.env }) : t('forms.pickFreight', { env: d.env })}
      </GroupHeader>

      <Controller
        control={control}
        name="freight"
        render={({ field, fieldState }) => (
          <>
            <div
              ref={field.ref}
              tabIndex={-1}
              role="radiogroup"
              aria-labelledby={`${listErrId}-h`}
              aria-invalid={!!fieldState.error}
              aria-describedby={fieldState.error ? listErrId : undefined}
              className={`group ${fieldState.error ? 'invalid-group' : ''}`}
            >
              {cands.isPending && <Loading />}
              {cands.error && (
                <div className="row">
                  <div className="grow">
                    <ErrorState error={cands.error} inline onRetry={() => void cands.refetch()} />
                  </div>
                </div>
              )}
              {cands.data && items.length === 0 && (
                <EmptyState>{scope === 'all' ? t('forms.noFreight') : fromCI ? t('forms.noCiFreight', { warehouses: warehouses ? t('forms.warehousesShort', { list: warehouses }) : '' }) : t('forms.noVerifiedFreight', { stages: upstreamNames.join(' / ') })}</EmptyState>
              )}
              {items.map((c) => (
                <ArtifactOption
                  key={c.freight}
                  c={c}
                  env={d.env}
                  upstreamNames={upstreamNames}
                  name={field.name}
                  checked={field.value === c.freight}
                  onSelect={() => {
                    field.onChange(c.freight)
                    field.onBlur()
                  }}
                />
              ))}
            </div>
            {fieldState.error && (
              <div className="ferr standalone" id={listErrId} role="alert">
                {fieldState.error.message}
              </div>
            )}
          </>
        )}
      />
      {cands.data?.gate?.problem && <Banner tone="warn">{t('forms.gateProblem', { label: cands.data.gate.label, problem: cands.data.gate.problem })}</Banner>}
      {cands.data?.gate && !cands.data.gate.problem && (
        <Note>
          {t('forms.crossKargo', { env: d.env, label: cands.data.gate.label })}
        </Note>
      )}
      {scope === 'available' && upstreamNames.length > 0 && (
        <Note>
          {t('forms.onlyVerified', { stages: upstreamNames.join(' / '), env: d.env })}
        </Note>
      )}
      {rule && <Note>{t('forms.approvalNote', { env: d.env, rule: rule.name, who: approvalText(rule) })}</Note>}
      {enforced.length > 0 && <Note>{t('forms.enforcedNote', { env: d.env, list: enforced.join('; ') })}</Note>}
      {fromCI && (
        <Note>
          {t('forms.ciDirect', { env: d.env, warehouses: warehouses ? t('forms.warehouses', { list: warehouses }) : '' })}
        </Note>
      )}

      {driftChanges.length > 0 && (
        <>
          <GroupHeader>{t('forms.driftHeader', { summary: changeSummary(driftChanges) })}</GroupHeader>
          <Banner tone="warn">
            {/* Two sentences from the catalogue, so the space between them has
                to be here: neither string can end with one without it being
                trimmed or looking wrong on its own. */}
            {t('forms.driftBody')}{' '}
            {driftEnforced ? t('forms.driftEnforced', { env: d.env }) : t('forms.driftOptional')}{' '}
            <Link to={{ search: '?change=sync' }}>{t('forms.goSync')}</Link>
          </Banner>
          <ConfigChanges changes={driftChanges} />
          <Group form style={{ marginTop: 10 }}>
            <FormField label={t('forms.config')} plainLabel>
              {(p) => <Checkbox id={p.id} {...register('withConfig')} label={t('forms.withConfigBox')} />}
            </FormField>
          </Group>
        </>
      )}

      <GroupHeader>{t('forms.releaseInfo')}</GroupHeader>
      <Group form>
        <FormField label={t('forms.title')} error={formState.errors.title?.message} hint={t('forms.titleHint')}>
          {(p) => <Input {...p} {...register('title')} placeholder={`${d.service} → ${d.env}`} autoComplete="off" />}
        </FormField>
        <FormField label={t('forms.jira')} error={formState.errors.jiraTicket?.message} required={req.jira} hint={req.jira ? undefined : t('forms.jiraOptional')}>
          {(p) => (
            // Shown upper-case via CSS and normalised on validate/submit. The
            // value itself is never rewritten while typing: mutating an
            // uncontrolled input drops keystrokes when typing fast.
            <Input {...p} {...register('jiraTicket')} mono className="uppercase" placeholder="OPS-1234" autoComplete="off" autoCapitalize="characters" spellCheck={false} />
          )}
        </FormField>
        <FormField label={t('forms.reason')} error={formState.errors.reason?.message} required={req.reason} hint={req.reason ? t('forms.reasonHint') : t('forms.reasonHintOptional')}>
          {(p) => <Textarea {...p} {...register('reason')} placeholder={t('forms.promotePlaceholder', { env: d.env })} />}
        </FormField>
      </Group>
      <FormErrorBanner error={formError} />
      {d.env === 'dev' && d.autoPromotion && <Note>{t('forms.autoPauseNote')}</Note>}
      <ButtonRow>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('forms.submitting')} disabled={busy}>
          {t('forms.submit')}
        </Button>
        {busy && (
          <span className="note" style={{ padding: 0 }}>
            {t('forms.busyRelease')}
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

function ArtifactOption({ c, env, upstreamNames, name, checked, onSelect }: { c: Candidate; env: string; upstreamNames: string[]; name: string; checked: boolean; onSelect: () => void }) {
  const { t } = useTranslation()
  const id = useId()
  const disabled = !c.available || c.current
  const inUp = (c.currentIn ?? []).concat(c.verifiedIn ?? []).find((x) => upstreamNames.includes(x.stage))
  return (
    <label
      htmlFor={id}
      className={`art ${checked ? 'on' : ''} ${disabled ? 'disabled' : ''}`}
      title={!c.available ? (upstreamNames.length ? t('forms.notVerifiedTitle', { stages: upstreamNames.join(' / '), env }) : t('forms.notPromotable')) : ''}
    >
      <input id={id} type="radio" className="sr-only" name={name} value={c.freight} checked={checked} disabled={disabled} onChange={onSelect} />
      <span className="radio" aria-hidden="true" />
      <span className="grow">
        <span className="t block">
          {c.version && <b>{c.version} </b>}
          <span className={c.version ? 'mono muted tag' : 'mono'}>{c.tag}</span>
        </span>
        {/* digest always visible, never folded */}
        <span className="d mono break block">{c.digest}</span>
        <span className="pills" style={{ marginTop: 4 }}>
          {c.current && <Pill>{t('forms.currentIn', { env })}</Pill>}
          {inUp && (
            <Pill tone="green">
              {t('forms.verifiedIn', { stage: inUp.stage, at: fmtTime(inUp.since) })}
            </Pill>
          )}
          {(c.currentIn ?? [])
            .filter((x) => x.stage !== env)
            .map((x) => (
              <Pill key={x.stage}>{t('forms.runningIn', { stage: x.stage })}</Pill>
            ))}
          {!c.available && <Pill>{upstreamNames.length ? t('forms.unverifiedPill') : t('forms.unavailablePill')}</Pill>}
        </span>
      </span>
      <span className="v nowrap">{fmtTime(c.createdAt)}</span>
    </label>
  )
}
