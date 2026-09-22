import { useTranslation } from 'react-i18next'
import type { ChangeAction, ResourceChange } from '../../lib/types'
import { changeActionText } from '../../lib/release'
import { EmptyState, Group, Pill } from '../ui'

const TONE: Record<ChangeAction, 'green' | 'blue' | 'red'> = { create: 'green', update: 'blue', delete: 'red' }


/**
 * The resources a config sync changes, each with its manifest diff folded
 * away. Deletions open by default: they are the ones to look at twice.
 */
export function ConfigChanges({ changes, open }: { changes: readonly ResourceChange[]; open?: boolean }) {
  const { t } = useTranslation()
  if (changes.length === 0) {
    return (
      <Group>
        <EmptyState>{t('domain.configInSync')}</EmptyState>
      </Group>
    )
  }
  return (
    <Group className="changes">
      {changes.map((c) => {
        const label = changeActionText(c.action)
        const tone = TONE[c.action]
        return (
          <details key={`${c.kind}/${c.namespace ?? ''}/${c.name}`} className="change" open={open || c.action === 'delete'}>
            <summary>
              <Pill tone={tone}>{label}</Pill>
              <span className="kind">{c.kind}</span>
              <span className="mono ellipsis">{c.name}</span>
              {c.namespace && <span className="muted ns">{c.namespace}</span>}
            </summary>
            <DiffView diff={c.diff ?? ''} truncated={c.truncated} />
          </details>
        )
      })}
    </Group>
  )
}

export function DiffView({ diff, truncated }: { diff: string; truncated?: boolean }) {
  const { t } = useTranslation()
  const lines = diff.replace(/\n$/, '').split('\n')
  return (
    <div className="diff" role="region" aria-label={t('domain.manifestDiff')}>
      <pre>
        {lines.map((l, i) => (
          <span key={i} className={l.startsWith('@@') ? 'hunk' : l.startsWith('+') ? 'add' : l.startsWith('-') ? 'del' : undefined}>
            {l}
            {'\n'}
          </span>
        ))}
      </pre>
      {truncated && <div className="muted diff-note">{t('domain.diffTruncated')}</div>}
    </div>
  )
}
