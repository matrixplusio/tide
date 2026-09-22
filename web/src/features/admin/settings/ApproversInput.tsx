import { useTranslation } from 'react-i18next'
import { useId, useState } from 'react'
import { Button, Pill, Segmented, Select } from '../../../components/ui'
import { subjectLabel } from '../../../lib/permissions'
import { useGroups, useUsers } from '../queries'

type Kind = 'user' | 'group'

/** Approvers as subject selectors: pick users from the account list, or name a group (local or SSO). */
export function ApproversInput({ id, value, onChange, ...rest }: { id?: string; value: string[]; onChange: (v: string[]) => void; 'aria-invalid'?: boolean; 'aria-describedby'?: string }) {
  const { t } = useTranslation()
  const [kind, setKind] = useState<Kind>('group')
  const [user, setUser] = useState('')
  const [group, setGroup] = useState('')
  const users = useUsers({ page: 1, pageSize: 100, status: 'active' })
  const groups = useGroups()
  const listId = useId()
  const names = new Map((users.data?.items ?? []).map((u) => [`user:${u.sub}`, u.name || u.username || u.sub]))
  const add = (s: string) => {
    if (s && !value.includes(s)) onChange([...value, s])
  }
  return (
    <div id={id} className="approvers" {...rest}>
      <div className="pills" style={{ marginBottom: value.length ? 8 : 0 }}>
        {value.map((s) => (
          <Pill key={s}>
            {subjectLabel(s, names.get(s))}
            <button type="button" className="pill-x" aria-label={t('admin.removeApprover', { who: subjectLabel(s, names.get(s)) })} onClick={() => onChange(value.filter((x) => x !== s))}>
              ×
            </button>
          </Pill>
        ))}
      </div>
      <span className="inline-control">
        <Segmented<Kind>
          label={t('admin.approverType')}
          value={kind}
          options={[
            ['group', t('admin.group')],
            ['user', t('admin.user')],
          ]}
          onChange={setKind}
        />
        {kind === 'user' ? (
          <Select
            appearance="filled"
            aria-label={t('admin.pickUser')}
            value={user}
            onChange={(e) => setUser(e.target.value)}
            options={[['', t('admin.pickUser')], ...(users.data?.items ?? []).map((u): [string, string] => [`user:${u.sub}`, `${u.name || u.username || u.sub}${u.username ? ` (${u.username})` : ''}`])]}
          />
        ) : (
          <>
            <input className="field" list={listId} aria-label={t('admin.groupName')} placeholder={t('admin.groupName')} value={group} onChange={(e) => setGroup(e.target.value)} />
            <datalist id={listId}>
              {(groups.data?.items ?? []).map((g) => (
                <option key={g.name} value={g.name}>
                  {g.description || g.name}
                </option>
              ))}
            </datalist>
          </>
        )}
        <Button
          size="small"
          variant="quiet"
          disabled={kind === 'user' ? !user : !group.trim()}
          onClick={() => {
            if (kind === 'user') {
              add(user)
              setUser('')
            } else {
              add(`group:${group.trim()}`)
              setGroup('')
            }
          }}
        >
          {t('admin.add')}
        </Button>
      </span>
    </div>
  )
}
