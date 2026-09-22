import { useTranslation } from 'react-i18next'
import { useId, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Controller, useForm, type Control, type FieldErrors, type FieldValues, type Path } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMe } from '../../../app/session'
import { fmtTime } from '../../../lib/format'
import { applyServerError } from '../../../lib/forms'
import { canView, envScopeText, permissionLabel, subjectLabel } from '../../../lib/permissions'
import type { RoleBinding } from '../../../lib/types'
import {
  Button,
  ButtonRow,
  ConfirmModal,
  EmptyState,
  ErrorState,
  Form,
  FormErrorBanner,
  FormField,
  Group,
  GroupHeader,
  Input,
  Loading,
  Modal,
  Note,
  Page,
  Pill,
  Row,
  Segmented,
  useToast,
} from '../../../components/ui'
import { EnvSelector, ScopeSelector } from '../../../components/domain'
import { useServices } from '../../services/queries'
import { AdminToolbar } from '../AdminToolbar'
import { useCreateBinding, useDeleteBinding, useDeleteRole, useGroups, useRoleBindings, useRoles, useUpdateBinding, useUpdateRole } from '../queries'
import { bindingScopeSchema, bindingSchema, bindingSubject, mapListField, type BindingScopeValues, type BindingValues, type SubjectKind } from '../schemas'
import type { Role, UserRow } from '../types'
import { UserPicker } from '../users/UserPicker'
import { RoleForm } from './RoleForm'

export function RoleDetailPage() {
  const { t } = useTranslation()
  const { id = '' } = useParams()
  const roles = useRoles()
  const role = roles.data?.items?.find((r) => r.id === id)

  return (
    <>
      <AdminToolbar title={role?.name ?? id} sub={role ? role.id : undefined} back={{ to: '/admin/roles', label: t('roles.back') }}>
        {role?.builtin && <Pill tone="blue">{t('admin.builtin')}</Pill>}
      </AdminToolbar>
      <Page>
        {roles.isPending && <Loading />}
        {roles.error && <ErrorState error={roles.error} onRetry={() => void roles.refetch()} />}
        {roles.data && !role && <ErrorState error={t('roles.notFound')} />}
        {role && (
          <>
            {role.builtin ? <BuiltinRole role={role} /> : <CustomRole role={role} />}
            <Bindings role={role} />
          </>
        )}
      </Page>
    </>
  )
}

function BuiltinRole({ role }: { role: Role }) {
  const { t } = useTranslation()
  const all = role.permissions.includes('*')
  return (
    <>
      <GroupHeader>{t('roles.permissions')}</GroupHeader>
      <Group>
        {all ? (
          <Row>
            <div className="t">{t('roles.everything')}</div>
          </Row>
        ) : (
          role.permissions.map((p) => (
            <Row key={p}>
              <div className="grow t">{permissionLabel(p)}</div>
              <span className="mono faint tag">{p}</span>
            </Row>
          ))
        )}
      </Group>
      <Note>{t('roles.builtinNote', { desc: role.description ? `${role.description}. ` : '' })}</Note>
    </>
  )
}

function CustomRole({ role }: { role: Role }) {
  const { t } = useTranslation()
  const toast = useToast()
  const navigate = useNavigate()
  const update = useUpdateRole()
  const del = useDeleteRole()
  const [deleting, setDeleting] = useState(false)
  return (
    <>
      <RoleForm
        key={`${role.id}-${role.permissions.join(',')}-${role.name}`}
        creating={false}
        submitLabel={t('roles.saveRole')}
        initial={{ id: role.id, name: role.name, description: role.description, permissions: role.permissions.filter((p) => p !== '*') }}
        onSave={async (v) => {
          await update.mutateAsync({ id: role.id, name: v.name, description: v.description, permissions: v.permissions })
          toast.success(t('roles.roleSaved'))
        }}
      />
      <ButtonRow>
        <Button variant="danger" size="small" onClick={() => setDeleting(true)}>
          {t('roles.deleteRole')}
        </Button>
      </ButtonRow>
      {deleting && (
        <ConfirmModal
          title={t('roles.deleteTitle', { name: role.name })}
          confirmLabel={t('roles.delete')}
          danger
          pending={del.isPending}
          error={del.error}
          onClose={() => {
            setDeleting(false)
            del.reset()
          }}
          onConfirm={() =>
            del.mutate(role.id, {
              onSuccess: () => {
                toast.success(t('roles.roleDeleted', { name: role.name }))
                navigate('/admin/roles', { replace: true })
              },
            })
          }
        >
          {role.bindingCount > 0 ? t('roles.deleteWithBindings', { count: role.bindingCount }) : t('roles.deleteNoBindings')}
        </ConfirmModal>
      )}
    </>
  )
}

function Bindings({ role }: { role: Role }) {
  const { t } = useTranslation()
  const me = useMe()
  const toast = useToast()
  const bindings = useRoleBindings({ role: role.id })
  const del = useDeleteBinding()
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<RoleBinding | null>(null)
  const [deleting, setDeleting] = useState<RoleBinding | null>(null)
  const list = bindings.data?.items ?? []

  return (
    <>
      <GroupHeader
        right={
          <Button size="small" variant="quiet" onClick={() => setAdding(true)}>
            {t('roles.addBinding')}
          </Button>
        }
      >
        {t('roles.bindings')}
      </GroupHeader>
      <Group>
        {bindings.isPending && <Loading />}
        {bindings.error && (
          <Row>
            <div className="grow">
              <ErrorState error={bindings.error} inline onRetry={() => void bindings.refetch()} />
            </div>
          </Row>
        )}
        {bindings.data && list.length === 0 && <EmptyState>{t('roles.noBindings')}</EmptyState>}
        {list.map((b) => (
          <Row key={b.id}>
            <div className="grow">
              <div className="t">
                {subjectLabel(b.subject, b.subjectName)}
                {b.subject.startsWith('user:') && <span className="mono muted tag"> {b.subject.slice('user:'.length)}</span>}
              </div>
              <div className="d">
                {t('roles.bindingMeta', { scope: envScopeText(b.envs, me.environments), who: b.createdBy === 'migration' || !b.createdBy ? t('roles.systemInit') : b.createdBy, at: fmtTime(b.createdAt) })}
              </div>
            </div>
            <Button size="small" variant="quiet" onClick={() => setEditing(b)} aria-label={t('roles.editScopeAria', { who: subjectLabel(b.subject, b.subjectName) })}>
              {t('roles.envScope')}
            </Button>
            <Button size="small" variant="danger" onClick={() => setDeleting(b)} aria-label={t('roles.deleteBindingAria', { who: subjectLabel(b.subject, b.subjectName) })}>
              {t('roles.delete')}
            </Button>
          </Row>
        ))}
      </Group>
      <Note>{t('roles.scopeNote')}</Note>

      {adding && <AddBindingModal role={role} onClose={() => setAdding(false)} />}
      {editing && <EditEnvsModal binding={editing} onClose={() => setEditing(null)} />}
      {deleting && (
        <ConfirmModal
          title={t('roles.deleteBindingTitle', { who: subjectLabel(deleting.subject, deleting.subjectName), role: role.name })}
          confirmLabel={t('roles.deleteBinding')}
          danger
          pending={del.isPending}
          error={del.error}
          onClose={() => {
            setDeleting(null)
            del.reset()
          }}
          onConfirm={() =>
            del.mutate(deleting.id, {
              onSuccess: () => {
                toast.success(t('roles.bindingDeleted'))
                setDeleting(null)
              },
            })
          }
        >
          {deleting.subject === '*' ? t('roles.deleteAllNote') : t('roles.deleteSoon')}
        </ConfirmModal>
      )}
    </>
  )
}

const subjectKinds = [
  ['user', 'roles.subjectUser'],
  ['group', 'roles.subjectGroup'],
  ['all', 'roles.subjectAll'],
] as const

function AddBindingModal({ role, onClose }: { role: Role; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const create = useCreateBinding()
  const groups = useGroups()
  const listId = useId()
  const [picked, setPicked] = useState<UserRow[]>([])
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<BindingValues>({ resolver: zodResolver(bindingSchema(role.id)), mode: 'onTouched', defaultValues: { kind: 'user', userSub: '', group: '', envs: ['*'], projects: [], types: [] } })
  const { register, handleSubmit, formState, control, watch, setValue } = form
  const kind = watch('kind')
  const errors = formState.errors

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await create.mutateAsync({ roleId: role.id, subject: bindingSubject(v), envs: v.envs, projects: v.projects, types: v.types })
      toast.success(t('roles.bindingAdded'))
      onClose()
    } catch (e) {
      setFormError(
        applyServerError(form, e, {
          mapField: (f) => (f === 'subject' ? (kind === 'group' ? 'group' : kind === 'user' ? 'userSub' : null) : mapListField(f, { controls: ['envs', 'projects', 'types'] })),
        }),
      )
    }
  }, () => setFormError(null))

  return (
    <Modal title={t('roles.grantTitle', { role: role.name })} onClose={onClose} closeOnEsc={!formState.isSubmitting} width={620}>
      <Form onSubmit={onSubmit}>
        <Group form>
          <FormField label={t('roles.grantTo')} plainLabel error={errors.kind?.message}>
            {() => (
              <span>
                <Segmented<SubjectKind> label={t('roles.subjectType')} value={kind} options={subjectKinds.map(([v, key]) => [v, t(key)] as const)} onChange={(k) => setValue('kind', k, { shouldValidate: formState.isSubmitted })} />
              </span>
            )}
          </FormField>
          {kind === 'user' && (
            <FormField label={t('roles.user')} error={errors.userSub?.message} required plainLabel>
              {(p) => (
                <UserPicker
                  selected={picked}
                  invalid={p['aria-invalid']}
                  describedBy={p['aria-describedby']}
                  onChange={(us) => {
                    setPicked(us)
                    setValue('userSub', us[0]?.sub ?? '', { shouldValidate: formState.isSubmitted })
                  }}
                />
              )}
            </FormField>
          )}
          {kind === 'group' && (
            <FormField label={t('roles.groupName')} error={errors.group?.message} required hint={t('roles.groupHint')}>
              {(p) => (
                <>
                  <Input {...p} {...register('group')} mono list={listId} autoComplete="off" autoCapitalize="off" spellCheck={false} data-autofocus />
                  <datalist id={listId}>
                    {(groups.data?.items ?? []).map((g) => (
                      <option key={g.name} value={g.name}>
                        {g.description}
                      </option>
                    ))}
                  </datalist>
                </>
              )}
            </FormField>
          )}
          {kind === 'all' && (
            <Row>
              <Note style={{ padding: 0 }}>{t('roles.allNote')}</Note>
            </Row>
          )}
          <FormField label={t('roles.envScope')} error={errors.envs?.message} required plainLabel>
            {(p) => <Controller control={control} name="envs" render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
          </FormField>
          <ScopeFields control={control} errors={errors} />
        </Group>
        <FormErrorBanner error={formError} />
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button variant="quiet" onClick={onClose} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
          <Button type="submit" loading={formState.isSubmitting} loadingText={t('roles.adding')}>
            {t('roles.addBinding')}
          </Button>
        </ButtonRow>
      </Form>
    </Modal>
  )
}

function EditEnvsModal({ binding, onClose }: { binding: RoleBinding; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const update = useUpdateBinding()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<BindingScopeValues>({ resolver: zodResolver(bindingScopeSchema), mode: 'onTouched', defaultValues: { envs: binding.envs ?? [], projects: binding.projects ?? [], types: binding.types ?? [] } })
  const { handleSubmit, formState, control } = form

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await update.mutateAsync({ id: binding.id, envs: v.envs, projects: v.projects, types: v.types })
      toast.success(t('roles.scopeSaved'))
      onClose()
    } catch (e) {
      setFormError(applyServerError(form, e, { mapField: (f) => mapListField(f, { controls: ['envs', 'projects', 'types'] }) }))
    }
  }, () => setFormError(null))

  return (
    <Modal title={`${subjectLabel(binding.subject, binding.subjectName)} · ${binding.roleName}`} subtitle={t('roles.editScopeSub')} onClose={onClose} closeOnEsc={!formState.isSubmitting} width={560}>
      <Form onSubmit={onSubmit}>
        <Group form>
          <FormField label={t('roles.envScope')} error={formState.errors.envs?.message} required plainLabel>
            {(p) => <Controller control={control} name="envs" render={({ field }) => <EnvSelector {...p} ref={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} />} />}
          </FormField>
          <ScopeFields control={control} errors={formState.errors} />
        </Group>
        <FormErrorBanner error={formError} />
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button variant="quiet" onClick={onClose} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
          <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
            {t('settings.save')}
          </Button>
        </ButtonRow>
      </Form>
    </Modal>
  )
}

/**
 * Project and service-type limits shared by the add and edit forms. Nothing
 * selected means the grant is not limited on that axis.
 */
function ScopeFields<T extends FieldValues & { projects: string[]; types: string[] }>({ control, errors }: { control: Control<T>; errors: FieldErrors<T> }) {
  const { t } = useTranslation()
  const projectsName = 'projects' as Path<T>
  const typesName = 'types' as Path<T>
  const me = useMe()
  const services = useServices(canView(me, 'services.view'))
  const projects = [...new Set((services.data?.services ?? []).map((s) => s.project ?? '').filter(Boolean))].sort()
  const dim = (me.app.dimensions ?? []).find((d) => d.key === me.app.batchDimension)
  const types = dimensionOptions(dim)
  return (
    <>
      <FormField label={t('roles.projectScope')} error={errors.projects?.message as string | undefined} plainLabel hint={t('roles.projectScopeHint')}>
        {(p) => (
          <Controller
            control={control}
            name={projectsName}
            render={({ field }) => (
              <ScopeSelector {...p} value={field.value} onChange={field.onChange} onBlur={field.onBlur} allLabel={t('roles.allProjects')} options={projects.map((x) => ({ value: x, label: x }))} />
            )}
          />
        )}
      </FormField>
      {dim && (
        <FormField label={t('roles.dimScope', { what: dim.name })} error={errors.types?.message as string | undefined} plainLabel hint={t('roles.dimScopeHint', { what: dim.name })}>
          {(p) => (
            <Controller
              control={control}
              name={typesName}
              render={({ field }) => <ScopeSelector {...p} value={field.value} onChange={field.onChange} onBlur={field.onBlur} allLabel={t('roles.allOf', { what: dim.name })} options={types} />}
            />
          )}
        </FormField>
      )}
    </>
  )
}

function dimensionOptions(dim: { values?: { value: string; name: string }[] | null } | undefined) {
  return (dim?.values ?? []).map((v) => ({ value: v.value, label: v.name || v.value }))
}
