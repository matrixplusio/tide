import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Modal } from '../ui'
import type { Service } from '../../lib/types'
import { searchHits, type SearchHit } from '../../lib/search'

/** SearchPalette is the one box that reaches any service or release from
 *  anywhere. It searches what the app already holds — the services list is
 *  loaded for the sidebar — so there is no request and no server change. */
export function SearchPalette({ services, onClose }: { services: Service[]; onClose: () => void }) {
  const { t } = useTranslation()
  const nav = useNavigate()
  const [q, setQ] = useState('')
  const [cursor, setCursor] = useState(0)
  const hits = useMemo(() => searchHits(q, services), [q, services])

  // Clamped at render rather than reset in an effect: a shorter list after a
  // keystroke must not leave the cursor past its end.
  const sel = Math.min(cursor, Math.max(hits.length - 1, 0))

  const go = (h: SearchHit) => {
    onClose()
    nav(h.to)
  }

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setCursor((c) => Math.min(c + 1, Math.max(hits.length - 1, 0)))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setCursor((c) => Math.max(c - 1, 0))
    } else if (e.key === 'Enter' && hits[sel]) {
      e.preventDefault()
      go(hits[sel])
    }
  }

  return (
    <Modal title={t('search.title')} onClose={onClose} width={560}>
      <input
        className="input search-in"
        data-autofocus
        value={q}
        onChange={(e) => {
          setQ(e.target.value)
          setCursor(0)
        }}
        onKeyDown={onKey}
        placeholder={t('search.placeholder')}
        aria-label={t('search.title')}
        autoComplete="off"
        spellCheck={false}
      />
      <ul className="search-hits" aria-label={t('search.results')}>
        {hits.map((h, i) => (
          <li key={h.kind === 'release' ? h.id : h.name}>
            <button type="button" className={`search-hit ${i === sel ? 'on' : ''}`} aria-current={i === sel ? 'true' : undefined} onMouseEnter={() => setCursor(i)} onClick={() => go(h)}>
              {h.kind === 'release' ? (
                <>
                  <span className="mono">{h.id}</span>
                  <span className="muted">{t('search.openRelease')}</span>
                </>
              ) : (
                <>
                  <span className="mono">{h.name}</span>
                  <span className="muted">{[h.project, h.domain].filter(Boolean).join(' · ')}</span>
                </>
              )}
            </button>
          </li>
        ))}
        {q.trim() !== '' && hits.length === 0 && <li className="search-none">{t('search.none')}</li>}
      </ul>
      <div className="search-hint">{t('search.hint')}</div>
    </Modal>
  )
}
