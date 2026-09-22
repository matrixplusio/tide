import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Controller, useFieldArray, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { localInputToRfc3339, rfc3339ToLocalInput } from '../../../lib/format'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, EmptyState, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Note, Segmented, Textarea, useToast } from '../../../components/ui'
import { ApproversInput } from './ApproversInput'
import { EnvSelector, ScopeSelector } from '../../../components/domain'
import { useMe } from '../../../app/session'
import { canView } from '../../../lib/permissions'
import { useServices } from '../../services/queries'
import { useSaveSettings } from '../queries'
import { mapListField, releasePolicySchema, type ReleasePolicyValues } from '../schemas'
import type { ReleasePolicy } from '../types'

const numberInput = { type: 'text', inputMode: 'numeric', autoComplete: 'off', className: 'num' } as const

function toValues(p: ReleasePolicy | null | undefined): ReleasePolicyValues {
  return {
    confirmReadSeconds: String(p?.confirmReadSeconds ?? 10),
    confirmTtlMinutes: String(p?.confirmTtlMinutes ?? 10),
    executeTimeoutMinutes: String(p?.executeTimeoutMinutes ?? 15),
    minSoakMinutes: String(p?.minSoakMinutes ?? 30),
    multiVersionJump: String(p?.multiVersionJump ?? 3),
    jiraBaseUrl: p?.jiraBaseUrl ?? '',
    jiraRequired: p?.jiraRequired ?? ['tier:production'],
    reasonRequired: p?.reasonRequired ?? ['*'],
    approvals: (p?.approvals ?? []).map((a) => ({
      name: a.name,
      envs: a.envs ?? [],
      projects: a.projects ?? [],
      types: a.types ?? [],
      approvers: a.approvers ?? [],
      mode: a.mode,
      minApprovals: String(a.minApprovals || 2),
      timeoutMinutes: String(a.timeoutMinutes || 240),
    })),
    soakEnforced: p?.soakEnforced ?? [],
    versionJumpEnforced: p?.versionJumpEnforced ?? [],
    configDriftEnforced: p?.configDriftEnforced ?? [],
    jiraProjects: (p?.jiraProjects ?? []).map((value) => ({ value })),
    freezes: (p?.freezes ?? []).map((f) => ({
      name: f.name,
      envs: f.envs ?? [],
      startsAt: rfc3339ToLocalInput(f.startsAt),
      endsAt: rfc3339ToLocalInput(f.endsAt),
      reason: f.reason ?? '',
    })),
  }
}

export function ReleasePolicyForm({ initial }: { initial: ReleasePolicy | null | undefined }) {
  const { t } = useTranslation()
  const toast = useToast()
  const save = useSaveSettings<ReleasePolicy>('release')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<ReleasePolicyValues>({ resolver: zodResolver(releasePolicySchema), mode: 'onTouched', defaultValues: toValues(initial) })
  const { register, handleSubmit, formState, control, trigger } = form
  const errors = formState.errors
  const projects = useFieldArray({ control, name: 'jiraProjects' })
  const freezes = useFieldArray({ control, name: 'freezes' })
  const approvals = useFieldArray({ control, name: 'approvals' })
  const me = useMe()
  const services = useServices(canView(me, 'services.view'))
  const projectOptions = [...new Set((services.data?.services ?? []).map((x) => x.project ?? '').filter(Boolean))].sort().map((x) => ({ value: x, label: x }))
  const dim = (me.app.dimensions ?? []).find((d) => d.key === me.app.batchDimension)
  const typeOptions = (dim?.values ?? []).map((v) => ({ value: v.value, label: v.name || v.value }))

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await save.mutateAsync({
        confirmReadSeconds: Number(v.confirmReadSeconds.trim()),
        confirmTtlMinutes: Number(v.confirmTtlMinutes.trim()),
        executeTimeoutMinutes: Number(v.executeTimeoutMinutes.trim()),
        minSoakMinutes: Number(v.minSoakMinutes.trim()),
        multiVersionJump: Number(v.multiVersionJump.trim()),
        jiraBaseUrl: v.jiraBaseUrl.trim(),
        jiraRequired: v.jiraRequired,
        reasonRequired: v.reasonRequired,
        approvals: v.approvals.map((a) => ({
          name: a.name.trim(),
          envs: a.envs,
          projects: a.projects,
          types: a.types,
          approvers: a.approvers,
          mode: a.mode,
          minApprovals: a.mode === 'count' ? Number(a.minApprovals.trim()) : 0,
          timeoutMinutes: Number(a.timeoutMinutes.trim()),
        })),
        soakEnforced: v.soakEnforced,
        versionJumpEnforced: v.versionJumpEnforced,
        configDriftEnforced: v.configDriftEnforced,
        // Shown upper-case by CSS while typing; normalised here, as on the server.
        jiraProjects: v.jiraProjects.map((p) => p.value.trim().toUpperCase()),
        freezes: v.freezes.map((f) => ({
          name: f.name.trim(),
          envs: f.envs,
          startsAt: localInputToRfc3339(f.startsAt),
          endsAt: localInputToRfc3339(f.endsAt),
          reason: f.reason.trim(),
        })),
      })
      form.reset(v)
      toast.success(t('policyForm.saved'))
    } catch (e) {
      setFormError(applyServerError(form, e, { mapField: (f) => (f === 'jiraProjects' || f === 'freezes' ? null : mapListField(f, { controls: ['envs', 'approvers', 'jiraRequired', 'reasonRequired', 'soakEnforced', 'versionJumpEnforced', 'configDriftEnforced'], scalarLists: ['jiraProjects'] })) }))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit} aria-label={t('policyForm.label')}>
      <GroupHeader>{t('policyForm.confirmExec')}</GroupHeader>
      <Group form>
        <FormField label={t('policyForm.readSeconds')} error={errors.confirmReadSeconds?.message} required hint={t('policyForm.readSecondsHint')}>
          {(p) => <Input {...p} {...register('confirmReadSeconds')} {...numberInput} />}
        </FormField>
        <FormField label={t('policyForm.confirmTtl')} error={errors.confirmTtlMinutes?.message} required hint={t('policyForm.confirmTtlHint')}>
          {(p) => <Input {...p} {...register('confirmTtlMinutes')} {...numberInput} />}
        </FormField>
        <FormField label={t('policyForm.execTimeout')} error={errors.executeTimeoutMinutes?.message} required hint={t('policyForm.execTimeoutHint')}>
          {(p) => <Input {...p} {...register('executeTimeoutMinutes')} {...numberInput} />}
        </FormField>
      </Group>

      <GroupHeader>{t('policyForm.thresholds')}</GroupHeader>
      <Group form>
        <FormField label={t('policyForm.minSoak')} error={errors.minSoakMinutes?.message} required hint={t('policyForm.minSoakHint')}>
          {(p) => <Input {...p} {...register('minSoakMinutes')} {...numberInput} />}
        </FormField>
        <FormField label={t('policyForm.soakEnforced')} error={errors.soakEnforced?.message} hint={t('policyForm.soakEnforcedHint')}>
          {(p) => <Controller control={control} name="soakEnforced" render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
        </FormField>
        <FormField label={t('policyForm.versionJump')} error={errors.multiVersionJump?.message} required hint={t('policyForm.versionJumpHint')}>
          {(p) => <Input {...p} {...register('multiVersionJump')} {...numberInput} />}
        </FormField>
        <FormField label={t('policyForm.versionJumpEnforced')} error={errors.versionJumpEnforced?.message} hint={t('policyForm.versionJumpEnforcedHint')}>
          {(p) => <Controller control={control} name="versionJumpEnforced" render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
        </FormField>
        <FormField label={t('policyForm.driftEnforced')} error={errors.configDriftEnforced?.message} hint={t('policyForm.driftEnforcedHint')}>
          {(p) => <Controller control={control} name="configDriftEnforced" render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
        </FormField>
      </Group>

      <GroupHeader
        right={
          <Button size="small" variant="quiet" disabled={approvals.fields.length >= 20} onClick={() => approvals.append({ name: '', envs: ['tier:production'], projects: [], types: [], approvers: [], mode: 'any', minApprovals: '2', timeoutMinutes: '240' })}>
            {t('policyForm.addApproval')}
          </Button>
        }
      >
        {t('policyForm.approval')}
      </GroupHeader>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('policyForm.approvalNote')}</Note>
      {approvals.fields.length === 0 && (
        <Group>
          <EmptyState>{t('policyForm.noApprovals')}</EmptyState>
        </Group>
      )}
      {approvals.fields.map((f, i) => {
        const e = errors.approvals?.[i]
        const mode = form.watch(`approvals.${i}.mode`)
        return (
          <div key={f.id}>
            <GroupHeader
              right={
                <Button size="small" variant="danger" onClick={() => approvals.remove(i)} aria-label={t('policyForm.removeApproval', { n: i + 1 })}>
                  {t('policyForm.remove')}
                </Button>
              }
            >
              {t('policyForm.approvalNth', { n: i + 1 })}
            </GroupHeader>
            <Group form>
              <FormField label={t('policyForm.name')} error={e?.name?.message} required>
                {(p) => <Input {...p} {...register(`approvals.${i}.name`)} placeholder={t('policyForm.approvalPlaceholder')} autoComplete="off" />}
              </FormField>
              <FormField label={t('policyForm.envs')} error={e?.envs?.message} required plainLabel>
                {(p) => <Controller control={control} name={`approvals.${i}.envs`} render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
              </FormField>
              <FormField label={t('policyForm.projectScope')} error={e?.projects?.message} plainLabel hint={t('policyForm.projectScopeHint')}>
                {(p) => (
                  <Controller
                    control={control}
                    name={`approvals.${i}.projects`}
                    render={({ field }) => <ScopeSelector {...p} value={field.value} onChange={field.onChange} onBlur={field.onBlur} allLabel={t('policyForm.allProjects')} options={projectOptions} />}
                  />
                )}
              </FormField>
              {dim && (
                <FormField label={t('policyForm.dimScope', { what: dim.name })} error={e?.types?.message} plainLabel hint={t('policyForm.dimScopeHint', { what: dim.name })}>
                  {(p) => (
                    <Controller
                      control={control}
                      name={`approvals.${i}.types`}
                      render={({ field }) => <ScopeSelector {...p} value={field.value} onChange={field.onChange} onBlur={field.onBlur} allLabel={t('policyForm.allOf', { what: dim.name })} options={typeOptions} />}
                    />
                  )}
                </FormField>
              )}
              <FormField label={t('policyForm.approvers')} error={e?.approvers?.message} required plainLabel hint={t('policyForm.approversHint')}>
                {(p) => <Controller control={control} name={`approvals.${i}.approvers`} render={({ field }) => <ApproversInput {...p} value={field.value} onChange={field.onChange} />} />}
              </FormField>
              <FormField label={t('policyForm.mode')} error={e?.mode?.message} required plainLabel>
                {() => (
                  <Controller
                    control={control}
                    name={`approvals.${i}.mode`}
                    render={({ field }) => (
                      <Segmented
                        label={t('policyForm.mode')}
                        value={field.value}
                        options={[
                          ['any', t('policyForm.modeAny')],
                          ['count', t('policyForm.modeCount')],
                          ['all', t('policyForm.modeAll')],
                        ]}
                        onChange={field.onChange}
                      />
                    )}
                  />
                )}
              </FormField>
              {mode === 'count' && (
                <FormField label={t('policyForm.minApprovals')} error={e?.minApprovals?.message} required hint={t('policyForm.minApprovalsHint')}>
                  {(p) => <Input {...p} {...register(`approvals.${i}.minApprovals`)} {...numberInput} />}
                </FormField>
              )}
              <FormField label={t('policyForm.timeout')} error={e?.timeoutMinutes?.message} required hint={t('policyForm.timeoutHint')}>
                {(p) => <Input {...p} {...register(`approvals.${i}.timeoutMinutes`)} {...numberInput} />}
              </FormField>
            </Group>
          </div>
        )
      })}

      <GroupHeader>{t('policyForm.required')}</GroupHeader>
      <Group form>
        <FormField label={t('policyForm.jiraRequired')} error={errors.jiraRequired?.message} hint={t('policyForm.jiraRequiredHint')}>
          {(p) => <Controller control={control} name="jiraRequired" render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
        </FormField>
        <FormField label={t('policyForm.reasonRequired')} error={errors.reasonRequired?.message} hint={t('policyForm.reasonRequiredHint')}>
          {(p) => <Controller control={control} name="reasonRequired" render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
        </FormField>
      </Group>

      <GroupHeader>Jira</GroupHeader>
      <Group form>
        <FormField label={t('policyForm.jiraBaseUrl')} error={errors.jiraBaseUrl?.message} hint={t('policyForm.jiraBaseUrlHint')}>
          {(p) => <Input {...p} {...register('jiraBaseUrl')} mono type="url" inputMode="url" autoComplete="off" placeholder="https://jira.example.com" />}
        </FormField>

        {projects.fields.map((f, i) => (
          <FormField key={f.id} label={i === 0 ? t('policyForm.jiraProjects') : ''} error={errors.jiraProjects?.[i]?.value?.message}>
            {(p) => (
              <span className="inline-control">
                <Input {...p} {...register(`jiraProjects.${i}.value`)} mono className="uppercase" placeholder="OPS" autoComplete="off" spellCheck={false} aria-label={t('policyForm.jiraKeyNth', { n: i + 1 })} />
                <Button size="small" variant="quiet" onClick={() => projects.remove(i)} aria-label={t('policyForm.deleteProject', { n: i + 1 })}>
                  {t('policyForm.delete')}
                </Button>
              </span>
            )}
          </FormField>
        ))}
        <div className="row">
          <span className="flabel">{projects.fields.length === 0 ? t('policyForm.jiraProjects') : ''}</span>
          <div className="fcontrol">
            <span>
              <Button size="small" variant="quiet" disabled={projects.fields.length >= 50} onClick={() => projects.append({ value: '' })}>
                {t('policyForm.addProject')}
              </Button>
            </span>
            {projects.fields.length === 0 && <div className="fhint">{t('policyForm.noProjectLimit')}</div>}
          </div>
        </div>
      </Group>

      <GroupHeader>{t('policyForm.freezes')}</GroupHeader>
      <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('policyForm.freezesNote')}</Note>
      {freezes.fields.length === 0 && (
        <Group>
          <EmptyState>{t('policyForm.noFreezes')}</EmptyState>
        </Group>
      )}
      {freezes.fields.map((f, i) => {
        const e = errors.freezes?.[i]
        return (
          <div key={f.id}>
            <GroupHeader
              right={
                <Button size="small" variant="danger" onClick={() => freezes.remove(i)} aria-label={t('policyForm.removeFreeze', { n: i + 1 })}>
                  {t('policyForm.remove')}
                </Button>
              }
            >
              {t('policyForm.freezeNth', { n: i + 1 })}
            </GroupHeader>
            <Group form>
              <FormField label={t('policyForm.name')} error={e?.name?.message} required>
                {(p) => <Input {...p} {...register(`freezes.${i}.name`)} placeholder={t('policyForm.freezePlaceholder')} autoComplete="off" />}
              </FormField>
              <FormField label={t('policyForm.freezeEnvs')} error={e?.envs?.message} required plainLabel>
                {(p) => <Controller control={control} name={`freezes.${i}.envs`} render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
              </FormField>
              <FormField label={t('policyForm.startsAt')} error={e?.startsAt?.message} required>
                {(p) => (
                  <Input
                    {...p}
                    type="datetime-local"
                    {...register(`freezes.${i}.startsAt`, { onChange: () => (form.getFieldState(`freezes.${i}.endsAt`).isTouched || formState.isSubmitted) && void trigger(`freezes.${i}.endsAt`) })}
                  />
                )}
              </FormField>
              <FormField label={t('policyForm.endsAt')} error={e?.endsAt?.message} required>
                {(p) => <Input {...p} type="datetime-local" {...register(`freezes.${i}.endsAt`)} />}
              </FormField>
              <FormField label={t('policyForm.reason')} error={e?.reason?.message}>
                {(p) => <Textarea {...p} {...register(`freezes.${i}.reason`)} rows={2} placeholder={t('admin.optional')} />}
              </FormField>
            </Group>
          </div>
        )
      })}

      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button variant="quiet" disabled={freezes.fields.length >= 50} onClick={() => freezes.append({ name: '', envs: ['tier:production'], startsAt: '', endsAt: '', reason: '' })}>
          {t('policyForm.addFreeze')}
        </Button>
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
          {t('settings.save')}
        </Button>
      </ButtonRow>
    </Form>
  )
}
