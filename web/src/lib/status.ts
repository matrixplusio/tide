import { i18n } from './i18n'
import type { ItemStatus, ReleaseStatus } from './types'

// Looked up per call rather than frozen into a map when the module loads: the
// catalogues are not ready that early, and the language can change afterwards.
export function itemStatusText(status: ItemStatus): string {
  return i18n.t(`status.item.${status}`, { defaultValue: status })
}

export const RELEASE_STATUSES: ReleaseStatus[] = ['draft', 'confirming', 'approving', 'executing', 'succeeded', 'failed', 'cancelled', 'rejected']

export function releaseStatusText(status: ReleaseStatus): string {
  return i18n.t(`status.release.${status}`, { defaultValue: status })
}
