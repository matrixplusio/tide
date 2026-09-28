import { useTranslation } from 'react-i18next'
import { EmptyState, ErrorState, Loading, Modal } from '../../components/ui'
import type { ResourcePick } from '../../components/domain'
import { useResourceManifest } from './queries'

// One resource's manifest, read-only. Opened from a row in the resource list,
// one request per open — there is no batch fetch, because a person reads one
// manifest at a time and a whole tree would be most of a page nobody looks at.
//
// A Secret's values arrive already replaced by the server; the keys are still
// there, which is what "is the password even set?" needs.
export function ResourceModal({ service, env, pick, onClose }: { service: string; env: string; pick: ResourcePick; onClose: () => void }) {
  const { t } = useTranslation()
  const q = useResourceManifest(service, env, pick)
  return (
    <Modal title={`${pick.kind} / ${pick.name}`} subtitle={pick.namespace} onClose={onClose} width={860}>
      {q.isPending && <Loading />}
      {q.error && <ErrorState error={q.error} />}
      {/* The manifest scrolls inside its own box, not the sheet: the close button is the only way out of this sheet, and it must not scroll away. */}
      {q.data && q.data.yaml !== '' && <pre className="yaml" style={{ maxHeight: '60vh', overflow: 'auto' }}>{q.data.yaml}</pre>}
      {q.data && q.data.yaml === '' && <EmptyState>{t('live.manifestEmpty')}</EmptyState>}
    </Modal>
  )
}
