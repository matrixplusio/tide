import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../../lib/api'
import { useToast } from '../ui'

/**
 * Asks the server to read the upstreams now, rather than within the service
 * catalog's cache lifetime.
 *
 * The pages poll, and anything done through Tide shows up at once — a release
 * invalidates the catalog as it commits. What has to wait is a change made
 * around Tide: somebody editing an Application, Argo CD syncing on its own,
 * a service being onboarded. Up to a minute of that is a reasonable trade for
 * not asking every upstream on every page load; standing in front of a screen
 * waiting out a minute with no way to hurry it is not, which is what this is.
 *
 * Deliberately a separate request rather than a flag on the polling one: a
 * poll that rebuilt the catalog would be every viewer's page hammering every
 * upstream, which is the cost the cache exists to avoid.
 */
export function RefreshNow({ query, path }: { query: string; path: string }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const toast = useToast()
  const [busy, setBusy] = useState(false)

  return (
    <button
      type="button"
      className="linklike refresh"
      disabled={busy}
      aria-busy={busy}
      title={t('common.refreshHint')}
      onClick={async () => {
        setBusy(true)
        try {
          await apiFetch(path, { query: { fresh: true } })
          await qc.invalidateQueries({ queryKey: [query] })
        } catch {
          // The page is still showing the last good answer, and the poll will
          // try again on its own: saying it did not work is the whole of the
          // handling, and leaving the old data up is better than blanking it.
          toast.error(t('common.refreshFailed'))
        } finally {
          setBusy(false)
        }
      }}
    >
      {busy ? t('common.refreshing') : t('common.refresh')}
    </button>
  )
}
