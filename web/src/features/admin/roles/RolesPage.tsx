import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { permissionLabel } from '../../../lib/permissions'
import { Button, Chev, EmptyState, ErrorState, Group, Loading, Modal, Note, Page, Pill, Row, useToast } from '../../../components/ui'
import { AdminToolbar } from '../AdminToolbar'
import { useCreateRole, useRoles } from '../queries'
import { RoleForm } from './RoleForm'

export function RolesPage() {
  const { t } = useTranslation()
  const roles = useRoles()
  const [creating, setCreating] = useState(false)
  const list = roles.data?.items ?? []

  return (
    <>
      <AdminToolbar title={t('admin.navRoles')}>
        <Button size="small" onClick={() => setCreating(true)}>
          {t('admin.newRole')}
        </Button>
      </AdminToolbar>
      <Page>
        <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('admin.rolesNote')}</Note>
        {roles.isPending && <Loading />}
        {roles.error && <ErrorState error={roles.error} onRetry={() => void roles.refetch()} />}
        {roles.data && (
          <Group>
            {list.length === 0 && <EmptyState>{t('admin.noRoles')}</EmptyState>}
            {list.map((r) => (
              <Row key={r.id} to={`/admin/roles/${encodeURIComponent(r.id)}`}>
                <div className="grow">
                  <div className="t">
                    {r.name} <span className="mono muted tag">{r.id}</span>
                  </div>
                  <div className="d ellipsis">
                    {r.description ? `${r.description} · ` : ''}
                    {r.permissions.includes('*') ? t('admin.allPermissions') : r.permissions.map(permissionLabel).join(t('scope.listSeparator'))}
                  </div>
                </div>
                {r.builtin && <Pill tone="blue">{t('admin.builtin')}</Pill>}
                <span className="v nowrap">{t('admin.bindingCount', { count: r.bindingCount })}</span>
                <Chev />
              </Row>
            ))}
          </Group>
        )}
      </Page>
      {creating && <CreateRoleModal onClose={() => setCreating(false)} />}
    </>
  )
}

function CreateRoleModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const create = useCreateRole()
  const toast = useToast()
  const navigate = useNavigate()
  return (
    <Modal title={t('admin.newRole')} subtitle={t('admin.newRoleSub')} onClose={onClose} closeOnEsc={!create.isPending} width={680}>
      <RoleForm
        creating
        submitLabel={t('admin.create')}
        initial={{ id: '', name: '', description: '', permissions: ['services.view', 'releases.view'] }}
        onCancel={onClose}
        onSave={async (v) => {
          const r = await create.mutateAsync(v)
          toast.success(t('admin.roleCreated', { name: v.name }))
          onClose()
          navigate(`/admin/roles/${encodeURIComponent(r?.id ?? v.id)}`)
        }}
      />
    </Modal>
  )
}
