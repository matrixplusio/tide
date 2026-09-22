import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMe } from '../../../app/session'
import { applyServerError } from '../../../lib/forms'
import { can } from '../../../lib/permissions'
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
  Loading,
  Modal,
  Page,
  Pill,
  Row,
  Textarea,
  useToast,
} from '../../../components/ui'
import { AdminToolbar } from '../AdminToolbar'
import { useAddGroupMembers, useDeleteGroup, useGroupMembers, useGroups, useRemoveGroupMember, useUpdateGroup } from '../queries'
import { groupDescriptionSchema, type GroupDescriptionValues } from '../schemas'
import type { GroupRow, UserRow } from '../types'
import { UserPicker } from '../users/UserPicker'
import { methodLabel, userDisplay } from '../users/format'

export function GroupDetailPage() {
  const { t } = useTranslation()
  const { name = '' } = useParams()
  const me = useMe()
  const toast = useToast()
  const navigate = useNavigate()
  const groups = useGroups()
  const members = useGroupMembers(name)
  const del = useDeleteGroup()
  const removeMember = useRemoveGroupMember()
  const [adding, setAdding] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [removing, setRemoving] = useState<UserRow | null>(null)
  const group = groups.data?.items?.find((g) => g.name === name)
  const list = members.data?.items ?? []

  return (
    <>
      <AdminToolbar title={<span className="mono">{name}</span>} sub={group ? t('admin.memberCount', { count: group.memberCount }) : undefined} back={{ to: '/admin/groups', label: t('groups.back') }}>
        {group && (
          <Button size="small" variant="danger" onClick={() => setDeleting(true)}>
            {t('groups.deleteGroup')}
          </Button>
        )}
      </AdminToolbar>
      <Page>
        {groups.isPending && <Loading />}
        {groups.error && <ErrorState error={groups.error} onRetry={() => void groups.refetch()} />}
        {groups.data && !group && <ErrorState error={t('groups.notFound')} />}
        {group && (
          <>
            <DescriptionForm key={group.description} group={group} />

            <GroupHeader
              right={
                <Button size="small" variant="quiet" onClick={() => setAdding(true)}>
                  {t('groups.addMembers')}
                </Button>
              }
            >
              {t('groups.members')}
            </GroupHeader>
            <Group>
              {members.isPending && <Loading />}
              {members.error && (
                <Row>
                  <div className="grow">
                    <ErrorState error={members.error} inline onRetry={() => void members.refetch()} />
                  </div>
                </Row>
              )}
              {members.data && list.length === 0 && <EmptyState>{t('groups.noMembers')}</EmptyState>}
              {list.map((u) => (
                <Row key={u.id}>
                  <div className="grow">
                    <div className="t">
                      {can(me, 'users.manage') ? <Link to={`/admin/users/${u.id}`}>{userDisplay(u)}</Link> : userDisplay(u)}{' '}
                      <span className="mono muted">{u.username ?? u.sub}</span>
                    </div>
                    <div className="d">{methodLabel(u.method)}</div>
                  </div>
                  {u.disabled && <Pill>{t('users.disabled')}</Pill>}
                  <Button size="small" variant="quiet" onClick={() => setRemoving(u)} aria-label={t('groups.removeAria', { who: userDisplay(u) })}>
                    {t('groups.remove')}
                  </Button>
                </Row>
              ))}
            </Group>
          </>
        )}
      </Page>

      {adding && <AddMembersModal name={name} existing={list.map((u) => u.id)} onClose={() => setAdding(false)} />}
      {removing && (
        <ConfirmModal
          title={t('groups.removeTitle', { who: userDisplay(removing), group: name })}
          confirmLabel={t('groups.remove')}
          danger
          pending={removeMember.isPending}
          error={removeMember.error}
          onClose={() => {
            setRemoving(null)
            removeMember.reset()
          }}
          onConfirm={() =>
            removeMember.mutate(
              { name, id: removing.id },
              {
                onSuccess: () => {
                  toast.success(t('groups.removed', { who: userDisplay(removing), group: name }))
                  setRemoving(null)
                },
              },
            )
          }
        >
          {t('groups.removeNote')}
        </ConfirmModal>
      )}
      {deleting && (
        <ConfirmModal
          title={t('groups.deleteTitle', { group: name })}
          confirmLabel={t('groups.delete')}
          danger
          pending={del.isPending}
          error={del.error}
          onClose={() => {
            setDeleting(false)
            del.reset()
          }}
          onConfirm={() =>
            del.mutate(name, {
              onSuccess: () => {
                toast.success(t('groups.deleted', { group: name }))
                navigate('/admin/groups', { replace: true })
              },
            })
          }
        >
          {t('groups.deleteNote1')}{name}{t('groups.deleteNote2')}
        </ConfirmModal>
      )}
    </>
  )
}

function DescriptionForm({ group }: { group: GroupRow }) {
  const { t } = useTranslation()
  const toast = useToast()
  const update = useUpdateGroup()
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<GroupDescriptionValues>({ resolver: zodResolver(groupDescriptionSchema), mode: 'onTouched', defaultValues: { description: group.description } })
  const { register, handleSubmit, formState } = form

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await update.mutateAsync({ name: group.name, description: v.description.trim() })
      form.reset(v)
      toast.success(t('groups.descriptionSaved'))
    } catch (e) {
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  return (
    <Form onSubmit={onSubmit} aria-label={t('groups.descriptionLabel')}>
      <Group form>
        <FormField label={t('admin.description')} error={formState.errors.description?.message}>
          {(p) => <Textarea {...p} {...register('description')} rows={2} placeholder={t('admin.optional')} />}
        </FormField>
      </Group>
      <FormErrorBanner error={formError} />
      <ButtonRow>
        <Button type="submit" size="small" variant="quiet" loading={formState.isSubmitting} loadingText={t('admin.saving')} disabled={!formState.isDirty}>
          {t('groups.saveDescription')}
        </Button>
      </ButtonRow>
    </Form>
  )
}

function AddMembersModal({ name, existing, onClose }: { name: string; existing: number[]; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const add = useAddGroupMembers()
  const [selected, setSelected] = useState<UserRow[]>([])
  const [tried, setTried] = useState(false)
  const invalid = tried && selected.length === 0

  const submit = () => {
    setTried(true)
    if (selected.length === 0 || add.isPending) return
    add.mutate(
      { name, userIds: selected.map((u) => u.id).slice(0, 100) },
      {
        onSuccess: () => {
          toast.success(t('groups.added', { count: selected.length, group: name }))
          onClose()
        },
      },
    )
  }

  return (
    <Modal title={t('groups.addTitle', { group: name })} subtitle={t('groups.addSub')} onClose={onClose} closeOnEsc={!add.isPending}>
      <UserPicker multiple selected={selected} onChange={setSelected} exclude={existing} invalid={invalid} describedBy={invalid ? 'add-members-err' : undefined} />
      {invalid && (
        <div className="ferr standalone" id="add-members-err" role="alert">
          {t('groups.pickAtLeastOne')}
        </div>
      )}
      <FormErrorBanner error={add.error} />
      <ButtonRow style={{ marginTop: 16 }}>
        <span className="grow" />
        <Button variant="quiet" onClick={onClose} disabled={add.isPending}>
          {t('admin.cancel')}
        </Button>
        <Button loading={add.isPending} loadingText={t('groups.adding')} onClick={submit}>
          {t('groups.addN', { suffix: selected.length > 0 ? t('groups.addNSuffix', { count: selected.length }) : '' })}
        </Button>
      </ButtonRow>
    </Modal>
  )
}
