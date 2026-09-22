import { i18n } from '../../lib/i18n'
import { useTranslation } from 'react-i18next'
import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { useFieldArray, useForm, type UseFormReturn } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMe } from '../../app/session'
import { apiFetch, isApiError } from '../../lib/api'
import { dimensionOptions } from '../../lib/catalog'
import { approvalFor, approvalText, enforcedThresholds } from '../../lib/release'
import { ErrCode } from '../../lib/errcode'
import { applyServerError } from '../../lib/forms'
import { canEnv, canService } from '../../lib/permissions'
import type { Candidates, Permission, Release } from '../../lib/types'
import {
  Banner,
  Button,
  ButtonRow,
  EmptyState,
  ErrorState,
  Form,
  FormErrorBanner,
  FormField,
  Group,
  GroupHeader,
  Input,
  Loading,
  Note,
  Page,
  Segmented,
  Checkbox,
  Select,
  Textarea,
  Toolbar,
} from '../../components/ui'
import { ConfirmSheet, VersionLabel } from '../../components/domain'
import { useCandidates, useServices } from '../services/queries'
import { normalizeJira } from '../services/schema'
import { useCreateRelease } from './queries'
import { batchCandidates, batchSchema, mapBatchField, matchPastedFreight, parsePaste, requiredFields, resolvePasteScope, type BatchKind, type BatchRow, type BatchValues, type PasteProblem } from './batch'

const KINDS = [
  ['image', 'releases.kindImage'],
  ['sync', 'releases.kindSync'],
  ['restart', 'releases.kindRestart'],
] as const
const KIND_PERMISSION: Record<BatchKind, Permission> = { image: 'releases.create', restart: 'releases.restart', sync: 'releases.sync' }
const kindVerb = (k: BatchKind): string => i18n.t(k === 'image' ? 'batch.verbImage' : k === 'restart' ? 'batch.verbRestart' : 'batch.verbSync')

/**
 * Batch release: several services of one project (and one type when the
 * catalog has a batch dimension) in one environment, one kind of change,
 * one confirmation. Items with the same sequence run in parallel; a higher
 * sequence waits for the lower ones and is skipped if any of them fails.
 */
export function BatchPage() {
  const { t } = useTranslation()
  const me = useMe()
  const nav = useNavigate()
  const services = useServices()
  const create = useCreateRelease()

  const envs = (me.environments ?? []).filter((e) => KINDS.some(([k]) => canEnv(me, KIND_PERMISSION[k], e.name)))
  const qc = useQueryClient()
  // Pasted service → freight / sequence, applied when the rows for the matching scope are built.
  const [imported, setImported] = useState<Record<string, { freight: string; sequence?: string }> | null>(null)
  const [pasteOpen, setPasteOpen] = useState(false)
  const [pasteText, setPasteText] = useState('')
  const [pasteBusy, setPasteBusy] = useState(false)
  const [pasteResult, setPasteResult] = useState<{ applied: number; problems: PasteProblem[] } | null>(null)
  const [env, setEnvState] = useState(envs[0]?.name ?? '')
  const kinds = KINDS.filter(([k]) => canEnv(me, KIND_PERMISSION[k], env))
  const [kind, setKindState] = useState<BatchKind>('image')
  const effectiveKind: BatchKind = kinds.some(([k]) => k === kind) ? kind : (kinds[0]?.[0] ?? 'image')

  const all = useMemo(() => services.data?.services ?? [], [services.data])
  const projects = [...new Set(all.filter((s) => s.project && s.envs[env]).map((s) => s.project ?? ''))].sort()
  const [project, setProjectState] = useState('')
  const effectiveProject = projects.includes(project) ? project : (projects[0] ?? '')

  const dim = (me.app.dimensions ?? []).find((d) => d.key === me.app.batchDimension)
  const typeOptions = dim ? dimensionOptions(dim, all.filter((s) => s.project === effectiveProject && s.envs[env])) : []
  const [type, setTypeState] = useState('')
  // Changing the scope by hand drops a previous paste import.
  const manual =
    <T,>(set: (v: T) => void) =>
    (v: T) => {
      setImported(null)
      set(v)
    }
  const setEnv = manual(setEnvState)
  const setKind = manual(setKindState)
  const setProject = manual(setProjectState)
  const setType = manual(setTypeState)
  const effectiveType = typeOptions.some(([v]) => v === type) ? type : (typeOptions[0]?.[0] ?? '')

  const list = useMemo(() => batchCandidates(all, env, effectiveProject, dim, effectiveType), [all, env, effectiveProject, dim, effectiveType])
  const req = requiredFields(me.app, env)
  const [formError, setFormError] = useState<unknown>(null)
  const [confirming, setConfirming] = useState<Release | null>(null)
  const form = useForm<BatchValues>({
    resolver: zodResolver(batchSchema(req, effectiveKind)),
    mode: 'onTouched',
    defaultValues: { rows: [], title: '', jiraTicket: '', reason: '', prune: false, restart: false, withConfig: false },
  })
  const { register, handleSubmit, formState, control, setValue, getValues } = form
  const rows = useFieldArray({ control, name: 'rows' })

  // A different scope is a different list: start the selection over.
  const scopeKey = `${env}|${effectiveKind}|${effectiveProject}|${effectiveType}|${list.map((s) => s.name).join(',')}`
  useEffect(() => {
    // A config sync starts with every service that has unsynced git changes picked.
    const pick = (s: (typeof list)[number]) => !!imported?.[s.name] || (effectiveKind === 'sync' && s.envs[env]?.sync === 'OutOfSync' && !s.conflicts?.length)
    setValue('rows', list.map((s): BatchRow => ({ service: s.name, selected: pick(s), freight: imported?.[s.name]?.freight ?? '', sequence: imported?.[s.name]?.sequence ?? '1' })))
    setFormError(null)
    // list is derived from scopeKey's parts
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey, imported])

  const onPaste = async () => {
    setPasteBusy(true)
    setPasteResult(null)
    try {
      const { entries, problems } = parsePaste(pasteText)
      if (entries.length === 0) {
        setPasteResult({ applied: 0, problems: problems.length ? problems : [{ line: 0, text: '', msg: t('batch.noServicesRecognised') }] })
        return
      }
      const scope = resolvePasteScope(entries, all, env, dim)
      problems.push(...scope.problems)
      if (scope.problems.some((x) => x.line === 0)) {
        setPasteResult({ applied: 0, problems })
        return
      }
      const bad = new Set(scope.problems.map((x) => x.text))
      const next: Record<string, { freight: string; sequence?: string }> = {}
      for (const e of entries.filter((x) => !bad.has(x.service))) {
        const cands = await qc.fetchQuery({
          queryKey: ['candidates', e.service, env, true],
          queryFn: ({ signal }) => apiFetch<Candidates>(`/api/v1/services/${encodeURIComponent(e.service)}/envs/${encodeURIComponent(env)}/candidates`, { query: { all: true }, signal }),
        })
        const m = matchPastedFreight(cands.items ?? [], e.service, e.ref, env)
        if (m.freight) next[e.service] = { freight: m.freight, sequence: e.sequence }
        else problems.push({ line: e.line, text: `${e.service} ${e.ref}`, msg: m.msg ?? t('batch.unrecognised') })
      }
      setKindState('image')
      setProjectState(scope.project)
      setTypeState(scope.type)
      setImported(next)
      setPasteResult({ applied: Object.keys(next).length, problems: problems.sort((a, b) => a.line - b.line) })
    } catch (e) {
      setPasteResult({ applied: 0, problems: [{ line: 0, text: '', msg: isApiError(e) ? e.msg : t('batch.readFreightFailed') }] })
    } finally {
      setPasteBusy(false)
    }
  }

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    const picked = v.rows.filter((r) => r.selected)
    try {
      const r = await create.mutateAsync({
        env,
        title: v.title.trim(),
        jiraTicket: normalizeJira(v.jiraTicket),
        reason: v.reason.trim(),
        items: picked.map((p) =>
          effectiveKind === 'restart'
            ? { kind: 'restart' as const, service: p.service, sequence: Number(p.sequence) }
            : effectiveKind === 'sync'
              ? { kind: 'sync' as const, service: p.service, sequence: Number(p.sequence), prune: v.prune, restart: v.restart }
              : { kind: 'image' as const, service: p.service, freight: p.freight, sequence: Number(p.sequence), withConfig: v.withConfig },
        ),
      })
      setConfirming(r)
    } catch (e) {
      setFormError(applyServerError(form, e, { mapField: (f) => mapBatchField(getValues('rows'), f) }))
    }
  }, () => setFormError(null))

  const approvalRule = approvalFor(me.app, env, effectiveProject, effectiveType || undefined)
  const inFlight = services.data?.inFlight ?? {}
  const selectedCount = form.watch('rows').filter((r) => r.selected).length

  return (
    <>
      <Toolbar title={t('batch.title')} back={{ to: '/releases', label: t('detail.back') }} />
      <Page>
        {envs.length === 0 && <Banner tone="warn">{t('batch.noPermission')}</Banner>}
        {services.isPending && <Loading />}
        {services.error && <ErrorState error={services.error} onRetry={() => void services.refetch()} />}
        {services.data && envs.length > 0 && (
          <Form onSubmit={onSubmit} aria-label={t('batch.formLabel')}>
            <GroupHeader right={<Button size="small" variant="quiet" onClick={() => setPasteOpen((v) => !v)}>{pasteOpen ? t('batch.collapsePaste') : t('batch.openPaste')}</Button>}>{t('batch.scope')}</GroupHeader>
            {pasteOpen && (
              <Group form>
                <FormField label={t('batch.pasteLabel')} hint={t('batch.pasteHint', { env })}>
                  {(p) => (
                    <Textarea
                      {...p}
                      value={pasteText}
                      onChange={(e) => setPasteText(e.target.value)}
                      mono
                      rows={5}
                      spellCheck={false}
                      placeholder={'order-api 20260917150452-938efb5c-0006\ntask-worker v1.4.0 2\nharbor.example.com/acme-qa/order-backend:20260917150455-6458e234-0007'}
                    />
                  )}
                </FormField>
                <div className="row">
                  <span className="flabel" />
                  <div className="fcontrol">
                    <span>
                      <Button size="small" loading={pasteBusy} loadingText={t('batch.recognising')} disabled={!pasteText.trim()} onClick={() => void onPaste()}>
                        {t('batch.recognise')}
                      </Button>
                    </span>
                    {pasteResult && (
                      <div className="fhint" role="status">
                        {pasteResult.applied > 0 && <div className="green">{t('batch.recognised', { count: pasteResult.applied })}</div>}
                        {pasteResult.problems.map((x, i) => (
                          <div key={i} className="red">
                            {x.line > 0 ? t('batch.lineNo', { line: x.line }) : ''}
                            {x.msg}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              </Group>
            )}
            <Group form>
              <FormField label={t('batch.env')} required>
                {(p) => <Select {...p} value={env} onChange={(e) => setEnv(e.target.value)} options={envs.map((e) => [e.name, e.displayName && e.displayName !== e.name ? `${e.displayName}（${e.name}）` : e.name])} />}
              </FormField>
              <FormField label={t('batch.action')} required plainLabel>
                {() => (kinds.length > 0 ? <Segmented label={t('batch.action')} value={effectiveKind} options={kinds.map(([k, key]) => [k, t(key)] as const)} onChange={setKind} /> : <span className="muted">{t('batch.noActionPermission')}</span>)}
              </FormField>
              <FormField label={t('batch.project')} required hint={t('batch.projectHint')}>
                {(p) =>
                  projects.length > 0 ? (
                    <Select {...p} value={effectiveProject} onChange={(e) => setProject(e.target.value)} options={projects.map((x) => [x, x])} />
                  ) : (
                    <span className="muted">{t('batch.noProjectServices')}</span>
                  )
                }
              </FormField>
              {dim && (
                <FormField label={dim.name} required plainLabel hint={t('batch.dimHint', { what: dim.name, order: (dim.values ?? []).map((v) => v.name || v.value).join(' → ') })}>
                  {() => (typeOptions.length > 0 ? <Segmented label={dim.name} value={effectiveType} options={typeOptions} onChange={setType} /> : <span className="muted">{t('batch.none')}</span>)}
                </FormField>
              )}
            </Group>

            <GroupHeader right={selectedCount > 0 ? <span className="muted">{effectiveKind === 'image' ? t('batch.targetAndOrder') : t('batch.orderOnly')}</span> : undefined}>{t('batch.servicesSelected', { count: selectedCount })}</GroupHeader>
            {list.length === 0 ? (
              <Group>
                <EmptyState>{t('batch.noMatchingServices')}</EmptyState>
              </Group>
            ) : (
              <Group>
                {rows.fields.map((f, i) => {
                  const svc = list.find((s) => s.name === f.service)
                  const dep = svc?.envs[env]
                  const busy = !!inFlight[`${f.service}/${env}`] || !!dep?.promoting
                  const conflict = !!svc?.conflicts?.length
                  const allowed = canService(me, KIND_PERMISSION[effectiveKind], env, svc?.project || undefined, dim ? svc?.dimensions?.[dim.key] : undefined)
                  return <BatchRowView key={f.id} form={form} index={i} env={env} kind={effectiveKind} current={dep} busy={busy} conflict={conflict} allowed={allowed} />
                })}
              </Group>
            )}
            {formState.errors.rows?.root?.message || formState.errors.rows?.message ? (
              <div className="ferr standalone" role="alert">
                {formState.errors.rows?.root?.message ?? formState.errors.rows?.message}
              </div>
            ) : null}
            <Note>{t('batch.orderNote')}</Note>
      {approvalRule && <Note>{t('batch.approvalNote', { env, rule: approvalRule.name, who: approvalText(approvalRule) })}</Note>}
            {effectiveKind === 'image' && enforcedThresholds(me.app, env).length > 0 && <Note>{t('batch.enforcedNote', { env, list: enforcedThresholds(me.app, env).join('; ') })}</Note>}

            {effectiveKind === 'image' && (
              <>
                <GroupHeader>{t('batch.driftHeader')}</GroupHeader>
                <Group form>
                  <FormField label={t('batch.config')} plainLabel hint={me.app.configDriftEnforced?.includes(env) ? t('batch.driftEnforcedHint', { env }) : t('batch.driftHint')}>
                    {(p) => <Checkbox id={p.id} {...register('withConfig')} label={t('batch.withConfigBox')} />}
                  </FormField>
                </Group>
              </>
            )}
            {effectiveKind === 'sync' && (
              <>
                <GroupHeader>{t('batch.syncOptions')}</GroupHeader>
                <Group form>
                  <FormField label={t('batch.restartAfterSync')} plainLabel hint={t('batch.restartAfterSyncHint')}>
                    {(p) => <Checkbox id={p.id} {...register('restart')} label={t('batch.restartAfterSyncBox')} />}
                  </FormField>
                  <FormField label={t('batch.allowPrune')} plainLabel error={formState.errors.prune?.message} hint={t('batch.allowPruneHint')}>
                    {(p) => <Checkbox id={p.id} {...register('prune')} label={t('batch.allowPruneBox')} />}
                  </FormField>
                </Group>
                <Note>{t('batch.syncNote')}</Note>
              </>
            )}

            <GroupHeader>{t('batch.releaseInfo')}</GroupHeader>
            <Group form>
              <FormField label={t('forms.title')} error={formState.errors.title?.message} hint={t('forms.titleHint')}>
                {(p) => <Input {...p} {...register('title')} placeholder={`${selectedCount > 0 ? t('batch.andMore', { service: form.watch('rows').find((r) => r.selected)?.service, count: selectedCount }) : t('batch.manyServices')} → ${env}`} autoComplete="off" />}
              </FormField>
              <FormField label={t('forms.jira')} error={formState.errors.jiraTicket?.message} required={req.jira} hint={req.jira ? undefined : t('forms.jiraOptional')}>
                {(p) => <Input {...p} {...register('jiraTicket')} mono className="uppercase" placeholder="OPS-1234" autoComplete="off" autoCapitalize="characters" spellCheck={false} />}
              </FormField>
              <FormField label={t('forms.reason')} error={formState.errors.reason?.message} required={req.reason} hint={req.reason ? t('forms.reasonHint') : t('forms.reasonHintOptional')}>
                {(p) => <Textarea {...p} {...register('reason')} placeholder={t('batch.placeholderReason', { env, verb: kindVerb(effectiveKind) })} />}
              </FormField>
            </Group>
            <FormErrorBanner error={formError} />
            {isApiError(formError, ErrCode.OrderBlocked) && <Note>{t('batch.orderBlocked')}</Note>}
            <ButtonRow>
              <Button type="submit" loading={formState.isSubmitting} loadingText={t('forms.submitting')} disabled={kinds.length === 0}>
                {t('forms.submit')}
              </Button>
            </ButtonRow>
          </Form>
        )}
      </Page>
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

function BatchRowView({
  form,
  index: i,
  env,
  kind,
  current,
  busy,
  conflict,
  allowed,
}: {
  form: UseFormReturn<BatchValues>
  index: number
  env: string
  kind: BatchKind
  allowed: boolean
  current: { tag?: string; version?: string; kargoProject?: string; sync?: string } | undefined
  busy: boolean
  conflict: boolean
}) {
  const { t } = useTranslation()
  const { register, watch, formState } = form
  const row = watch(`rows.${i}`)
  const e = formState.errors.rows?.[i]
  const needCandidates = kind === 'image' && !!row?.selected && !!current?.kargoProject
  const cands = useCandidates(row?.service ?? '', env, false, needCandidates)
  const options: [string, string][] = [
    ['', t('batch.pickFreight')],
    ...(cands.data?.items ?? []).filter((c) => c.available && !c.current).map((c): [string, string] => [c.freight, `${c.version ? c.version + ' ' : ''}${c.tag}`]),
  ]
  if (!row) return null
  return (
    <div className="row top batch-row">
      <label className="check grow" style={{ alignItems: 'flex-start' }}>
        <input type="checkbox" {...register(`rows.${i}.selected`)} disabled={busy || conflict || !allowed} aria-label={t('batch.selectService', { service: row.service })} />
        <span>
          <b>{row.service}</b>
          <span className="d block">
            {t('batch.current')} <VersionLabel a={current} full />
            {busy && <span className="orange">{t('batch.busy')}</span>}
            {conflict && <span className="red">{t('batch.conflict')}</span>}
            {!allowed && <span className="muted">{t('batch.notAllowed')}</span>}
            {kind === 'image' && !current?.kargoProject && <span className="muted">{t('batch.noKargo')}</span>}
            {kind === 'sync' && (current?.sync === 'OutOfSync' ? <span className="orange">{t('batch.outOfSync')}</span> : <span className="muted">{t('batch.inSync')}</span>)}
          </span>
        </span>
      </label>
      {row.selected && (
        <span className={`batch-ctl ${kind}`}>
          {kind === 'image' && (
            <span className="fcontrol">
              {cands.isPending && needCandidates ? (
                <span className="d">{t('batch.readingFreight')}</span>
              ) : cands.data && options.length === 1 ? (
                <span className="d" style={{ paddingTop: 5 }}>{t('batch.noPromotable')}</span>
              ) : (
                 <select className="field" {...register(`rows.${i}.freight`)} aria-label={t('batch.targetFor', { service: row.service })} aria-invalid={!!e?.freight}>
                  {options.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
              )}
              {e?.freight && <span className="ferr">{e.freight.message}</span>}
            </span>
          )}
          <span className="fcontrol">
            <input className="field mono seq" inputMode="numeric" placeholder={t('batch.order')} title={t('batch.orderTitle')} {...register(`rows.${i}.sequence`)} aria-label={t('batch.orderFor', { service: row.service })} aria-invalid={!!e?.sequence} />
            {e?.sequence && <span className="ferr">{e.sequence.message}</span>}
          </span>
        </span>
      )}
    </div>
  )
}
