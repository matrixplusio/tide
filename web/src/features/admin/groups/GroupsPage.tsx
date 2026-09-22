import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { isApiError } from '../../../lib/api'
import { ErrCode } from '../../../lib/errcode'
import { applyServerError } from '../../../lib/forms'
import { Button, ButtonRow, Chev, EmptyState, ErrorState, Form, FormErrorBanner, FormField, Group, Input, Loading, Modal, Note, Page, Row, Textarea, useToast } from '../../../components/ui'
import { AdminToolbar } from '../AdminToolbar'
import { useCreateGroup, useGroups } from '../queries'
import { createGroupSchema, type CreateGroupValues } from '../schemas'

export function GroupsPage() {
  const { t } = useTranslation()
  const groups = useGroups()
  const [creating, setCreating] = useState(false)
  const list = groups.data?.items ?? []

  return (
    <>
      <AdminToolbar title={t('admin.navGroups')}>
        <Button size="small" onClick={() => setCreating(true)}>
          {t('admin.newGroup')}
        </Button>
      </AdminToolbar>
      <Page>
        <Note style={{ paddingTop: 0, marginBottom: 8 }}>
          {t('admin.groupsNote1')} <span className="mono">group:&lt;name&gt;</span>{t('admin.groupsNote2')}
        </Note>
        {groups.isPending && <Loading />}
        {groups.error && <ErrorState error={groups.error} onRetry={() => void groups.refetch()} />}
        {groups.data && (
          <Group>
            {list.length === 0 && <EmptyState>{t('admin.noGroups')}</EmptyState>}
            {list.map((g) => (
              <Row key={g.name} to={`/admin/groups/${encodeURIComponent(g.name)}`}>
                <div className="grow">
                  <div className="t mono">{g.name}</div>
                  <div className="d ellipsis">{g.description || <span className="faint">{t('admin.noDescription')}</span>}</div>
                </div>
                <span className="v nowrap">{t('admin.memberCount', { count: g.memberCount })}</span>
                <Chev />
              </Row>
            ))}
          </Group>
        )}
      </Page>
      {creating && <CreateGroupModal onClose={() => setCreating(false)} />}
    </>
  )
}

function CreateGroupModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const navigate = useNavigate()
  const create = useCreateGroup()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<CreateGroupValues>({ resolver: zodResolver(createGroupSchema), mode: 'onTouched', defaultValues: { name: '', description: '' } })
  const { register, handleSubmit, formState } = form

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    const name = v.name.trim()
    try {
      await create.mutateAsync({ name, description: v.description.trim() })
      toast.success(t('admin.groupCreated', { name }))
      onClose()
      navigate(`/admin/groups/${encodeURIComponent(name)}`)
    } catch (e) {
      if (isApiError(e, ErrCode.GroupExists)) {
        form.setError('name', { type: 'server', message: e.msg }, { shouldFocus: true })
        return
      }
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Modal title={t('admin.newGroup')} onClose={onClose} closeOnEsc={!formState.isSubmitting} width={480}>
      <Form onSubmit={onSubmit}>
        <Group form>
          <FormField label={t('admin.groupName')} error={formState.errors.name?.message} required hint={t('admin.groupNameHint')}>
            {(p) => <Input {...p} {...register('name')} mono autoComplete="off" autoCapitalize="off" spellCheck={false} data-autofocus />}
          </FormField>
          <FormField label={t('admin.description')} error={formState.errors.description?.message}>
            {(p) => <Textarea {...p} {...register('description')} rows={2} placeholder={t('admin.optional')} />}
          </FormField>
        </Group>
        <FormErrorBanner error={formError} />
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button variant="quiet" onClick={onClose} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
          <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.creating')}>
            {t('admin.create')}
          </Button>
        </ButtonRow>
      </Form>
    </Modal>
  )
}
