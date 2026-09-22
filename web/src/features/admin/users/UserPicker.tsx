import { useTranslation } from 'react-i18next'
import { useDeferredValue, useId, useState } from 'react'
import { Checkbox, EmptyState, ErrorState, Group, Input, Loading, Row } from '../../../components/ui'
import { useUsers } from '../queries'
import type { UserRow } from '../types'
import { userDisplay } from './format'

/**
 * Search users (GET /users?q=) and pick one or several. Selection is kept by
 * the caller so it survives new searches.
 */
export function UserPicker({
  selected,
  onChange,
  multiple,
  exclude = [],
  invalid,
  describedBy,
}: {
  selected: UserRow[]
  onChange: (users: UserRow[]) => void
  multiple?: boolean
  /** Users that cannot be picked (e.g. already members). */
  exclude?: number[]
  invalid?: boolean
  describedBy?: string
}) {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const deferred = useDeferredValue(q.trim())
  const users = useUsers({ q: deferred || undefined, status: 'enabled', page: 1, pageSize: 20 })
  const id = useId()
  const list = users.data?.items ?? []
  const isOn = (u: UserRow) => selected.some((s) => s.id === u.id)

  const toggle = (u: UserRow, on: boolean) => {
    if (!multiple) return onChange(on ? [u] : [])
    onChange(on ? [...selected.filter((s) => s.id !== u.id), u] : selected.filter((s) => s.id !== u.id))
  }

  return (
    <div className="fcontrol" style={{ gap: 8 }}>
      <Input
        appearance="filled"
        type="search"
        placeholder={t('admin.searchUsers')}
        aria-label={t('admin.searchUsersLabel')}
        value={q}
        onChange={(e) => setQ(e.target.value)}
        maxLength={100}
        aria-invalid={invalid || undefined}
        aria-describedby={describedBy}
        data-autofocus
      />
      {selected.length > 0 && (
        <div className="chips" aria-label={t('admin.selected')}>
          {selected.map((u) => (
            <span className="chip" key={u.id}>
              {userDisplay(u)}
              <button type="button" aria-label={t('admin.deselect', { who: userDisplay(u) })} onClick={() => toggle(u, false)}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <Group className="picker-list">
        {users.isPending && <Loading />}
        {users.error && (
          <Row>
            <div className="grow">
              <ErrorState error={users.error} inline onRetry={() => void users.refetch()} />
            </div>
          </Row>
        )}
        {users.data && list.length === 0 && <EmptyState>{t('admin.noMatchingUsers')}</EmptyState>}
        {list.map((u) => {
          const excluded = exclude.includes(u.id)
          return (
            <Row key={u.id}>
              <Checkbox
                id={`${id}-${u.id}`}
                className="nopad grow"
                checked={excluded || isOn(u)}
                disabled={excluded}
                onChange={(e) => toggle(u, e.target.checked)}
                label={
                  <>
                    {userDisplay(u)} <span className="mono muted">{u.username ?? u.sub}</span>
                    {u.method === 'oidc' && <span className="muted"> · SSO</span>}
                    {excluded && <span className="muted">{t('admin.alreadyMember')}</span>}
                  </>
                }
              />
            </Row>
          )
        })}
      </Group>
    </div>
  )
}
