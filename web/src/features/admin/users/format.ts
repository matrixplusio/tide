import { i18n } from '../../../lib/i18n'
import type { UserRow } from '../types'

export function userDisplay(u: Pick<UserRow, 'name' | 'username' | 'sub'>): string {
  return u.name || u.username || u.sub
}

export const methodLabel = (m: string) => (m === 'oidc' ? 'SSO' : i18n.t('profile.local'))
