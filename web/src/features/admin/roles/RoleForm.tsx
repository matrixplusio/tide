import { useTranslation } from 'react-i18next'
import { useId, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { isApiError } from '../../../lib/api'
import { ErrCode } from '../../../lib/errcode'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, Checkbox, ErrorState, Form, FormErrorBanner, FormField, Group, GroupHeader, Input, Loading, Row, Textarea } from '../../../components/ui'
import { mapListField, roleSchema, type RoleValues } from '../schemas'
import { useRbacCatalog } from '../queries'
import type { PermissionInfo } from '../types'

/** Create or edit a custom role. `onSave` throws API errors back so they land on the fields. */
export function RoleForm({
  initial,
  creating,
  onSave,
  onCancel,
  submitLabel,
}: {
  initial: RoleValues
  creating: boolean
  onSave: (v: RoleValues) => Promise<void>
  onCancel?: () => void
  submitLabel: string
}) {
  const { t } = useTranslation()
  const catalog = useRbacCatalog()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<RoleValues>({ resolver: zodResolver(roleSchema(creating)), mode: 'onTouched', defaultValues: initial })
  const { register, handleSubmit, formState, control } = form
  const errors = formState.errors

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await onSave({ id: v.id.trim(), name: v.name.trim(), description: v.description.trim(), permissions: v.permissions })
      form.reset(v)
    } catch (e) {
      if (isApiError(e, ErrCode.RoleIdTaken)) {
        form.setError('id', { type: 'server', message: e.msg }, { shouldFocus: true })
        return
      }
      setFormError(applyServerError(form, e, { mapField: (f) => mapListField(f, { controls: ['permissions'] }) }))
    }
  }, () => setFormError(null))

  const items = catalog.data?.items ?? []

  return (
    <Form onSubmit={onSubmit} aria-label={creating ? t('admin.newRole') : t('admin.editRole')}>
      <Group form>
        {creating && (
          <FormField label={t('admin.roleId')} error={errors.id?.message} required hint={t('admin.roleIdHint')}>
            {(p) => <Input {...p} {...register('id')} mono placeholder="release-manager" autoComplete="off" autoCapitalize="off" spellCheck={false} data-autofocus />}
          </FormField>
        )}
        <FormField label={t('admin.name')} error={errors.name?.message} required>
          {(p) => <Input {...p} {...register('name')} placeholder={t('admin.rolePlaceholder')} autoComplete="off" />}
        </FormField>
        <FormField label={t('admin.description')} error={errors.description?.message}>
          {(p) => <Textarea {...p} {...register('description')} rows={2} placeholder={t('admin.optional')} />}
        </FormField>
      </Group>

      {catalog.isPending && <Loading />}
      {catalog.error && <ErrorState error={catalog.error} onRetry={() => void catalog.refetch()} />}
      {catalog.data && (
        <Controller
          control={control}
          name="permissions"
          render={({ field, fieldState }) => (
            <fieldset ref={field.ref} className="choice-group" tabIndex={-1} aria-invalid={!!fieldState.error} aria-describedby={fieldState.error ? 'role-perms-err' : undefined}>
              <legend className="sr-only">{t('admin.permissions')}</legend>
              {(['global', 'env'] as const).map((scope) => (
                <div key={scope}>
                  <GroupHeader>{scope === 'global' ? t('admin.globalPermissions') : t('admin.envPermissions')}</GroupHeader>
                  <Group className={fieldState.error ? 'invalid-group' : undefined}>
                    {items
                      .filter((p) => p.scope === scope)
                      .map((p) => (
                        <PermissionRow
                          key={p.key}
                          p={p}
                          checked={field.value.includes(p.key)}
                          onChange={(on) => field.onChange(on ? [...field.value.filter((x) => x !== p.key), p.key] : field.value.filter((x) => x !== p.key))}
                        />
                      ))}
                  </Group>
                </div>
              ))}
              {fieldState.error && (
                <div className="ferr standalone" id="role-perms-err" role="alert">
                  {fieldState.error.message}
                </div>
              )}
            </fieldset>
          )}
        />
      )}

      <FormErrorBanner error={formError} />
      <ButtonRow>
        {onCancel && (
          <Button variant="quiet" onClick={onCancel} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
        )}
        <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.saving')}>
          {submitLabel}
        </Button>
      </ButtonRow>
    </Form>
  )
}

function PermissionRow({ p, checked, onChange }: { p: PermissionInfo; checked: boolean; onChange: (on: boolean) => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const id = useId()
  const routes = p.routes ?? []
  return (
    <Row className="top">
      <div className="grow">
        <Checkbox
          id={`${id}-cb`}
          className="nopad"
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          label={
            <>
              {p.name} <span className="mono faint tag">{p.key}</span>
            </>
          }
        />
        {p.description && <div className="d">{p.description}</div>}
        {open && (
          <ul className="perm-routes mono" id={`${id}-routes`}>
            {routes.length === 0 && <li>{t('admin.noRoutes')}</li>}
            {routes.map((r) => (
              <li key={`${r.method} ${r.path}`}>
                {r.method} {r.path}
              </li>
            ))}
          </ul>
        )}
      </div>
      <Button size="small" variant="quiet" aria-expanded={open} aria-controls={`${id}-routes`} onClick={() => setOpen((o) => !o)}>
        {open ? t('admin.hideRoutes') : t('admin.showRoutes', { count: routes.length })}
      </Button>
    </Row>
  )
}
