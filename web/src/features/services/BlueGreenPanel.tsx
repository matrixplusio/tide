import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Button, ButtonRow, ConfirmModal, ErrorState, Group, GroupHeader, KV, Note, Pill, Row, useToast } from '../../components/ui'
import { VersionLabel } from '../../components/domain'
import { fmtTime, imageVersion } from '../../lib/format'
import type { BlueGreenState } from '../../lib/types'
import { useEndObservation, useRollback } from './queries'

/** A blue-green service after its switch: the previous version kept for the
 *  window, ending that early, and switching back to it. Both actions change
 *  what runs, so each asks once more before it is sent. */
export function BlueGreenPanel({ service, env, bg, error, canAct }: { service: string; env: string; bg?: BlueGreenState; error?: string; canAct: boolean }) {
  const { t } = useTranslation()
  const toast = useToast()
  const nav = useNavigate()
  const end = useEndObservation(service, env)
  const rollback = useRollback(service, env)
  const [asking, setAsking] = useState<'end' | 'rollback' | null>(null)
  if (!bg && !error) return null
  const minutes = bg ? Math.round(bg.windowSeconds / 60) : 0
  const live = bg?.rollouts.find((r) => r.scaleDownDelaySeconds !== undefined)
  const kept = bg?.retained ?? []
  const offer = bg?.rollback

  return (
    <>
      <GroupHeader>{t('bluegreen.title')}</GroupHeader>
      <Group>
        {error && (
          <Row>
            <div className="grow">
              <ErrorState error={error} inline />
            </div>
          </Row>
        )}
        {bg && (
          <>
            <KV k={t('bluegreen.window')}>
              {t('bluegreen.minutes', { count: minutes })}
              {bg.drift && live && (
                <>
                  {' '}
                  <Pill tone="orange">
                    {live.autoPromotion === false ? t('bluegreen.driftManual') : t('bluegreen.driftDelay', { count: Math.round((live.scaleDownDelaySeconds ?? 0) / 60) })}
                  </Pill>
                </>
              )}
            </KV>
            {kept.length === 0 && <KV k={t('bluegreen.previous')}>{t('bluegreen.noneKept')}</KV>}
            {kept.map((r) => (
              <KV key={r.replicaSet} k={t('bluegreen.previous')}>
                <span className="mono">{r.images.map(imageVersion).join(', ')}</span> · {t('bluegreen.keptUntil', { at: fmtTime(r.until) })} ·{' '}
                {t('bluegreen.ready', { ready: r.ready, desired: r.replicas })}
              </KV>
            ))}
          </>
        )}
      </Group>
      {bg?.drift && <Note>{t('bluegreen.driftHint')}</Note>}
      {bg?.rollbackError && kept.length > 0 && <Note>{bg.rollbackError}</Note>}
      {canAct && kept.length > 0 && (
        <ButtonRow style={{ marginTop: 8 }}>
          {offer && (
            <Button variant="danger" onClick={() => setAsking('rollback')}>
              {t('bluegreen.rollback')}
            </Button>
          )}
          <span className="grow" />
          <Button onClick={() => setAsking('end')}>{t('bluegreen.end')}</Button>
        </ButtonRow>
      )}
      {asking === 'end' && (
        <ConfirmModal
          title={t('bluegreen.endTitle', { service })}
          confirmLabel={t('bluegreen.end')}
          pending={end.isPending}
          error={end.error}
          onClose={() => {
            end.reset()
            setAsking(null)
          }}
          onConfirm={() =>
            end.mutate(undefined, {
              onSuccess: () => {
                toast.success(t('bluegreen.ended'))
                setAsking(null)
              },
            })
          }
        >
          {t('bluegreen.endHint')}
        </ConfirmModal>
      )}
      {asking === 'rollback' && offer && (
        <ConfirmModal
          title={t('bluegreen.rollbackTitle', { service })}
          confirmLabel={t('bluegreen.rollback')}
          danger
          pending={rollback.isPending}
          error={rollback.error}
          onClose={() => {
            rollback.reset()
            setAsking(null)
          }}
          onConfirm={() =>
            rollback.mutate(offer.to.digest, {
              onSuccess: (rel) => {
                toast.success(t('bluegreen.rolledBack'))
                setAsking(null)
                nav(`/releases/${encodeURIComponent(rel.id)}`)
              },
            })
          }
        >
          <div>
            {t('bluegreen.rollbackTo')} <VersionLabel a={offer.to} full />
          </div>
          <div className="mono d">{offer.to.digest}</div>
          <div style={{ marginTop: 8 }}>{t('bluegreen.rollbackHint', { at: fmtTime(offer.until), id: offer.releaseId })}</div>
        </ConfirmModal>
      )}
    </>
  )
}
