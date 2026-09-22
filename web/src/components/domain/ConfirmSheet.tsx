import { useTranslation } from 'react-i18next'
import { useEffect, useMemo, useState } from 'react'
import { useMe } from '../../app/session'
import type { Item, Release } from '../../lib/types'
import { changeSummary, itemDigest } from '../../lib/release'
import { ConfigChanges } from './ConfigChanges'
import { fmtTime, shortTag } from '../../lib/format'
import { useCancelRelease, useConfirmRelease } from '../../features/releases/queries'
import { useServiceScope } from '../../features/services/queries'
import { JiraLink } from './JiraLink'
import { Banner, Button, ButtonRow, FormErrorBanner, Group, GroupHeader, Modal, Note, Pill, Row } from '../ui'
import { approvalFor, approvalText } from '../../lib/release'

// Forced reading time, not forced waiting: the full checklist with anomalies
// is shown while the server-side window runs. When it ends the button becomes
// available but nothing happens until the person clicks it. The server
// enforces the window again; this countdown is only a mirror of it.
export function ConfirmSheet({ release, onClose, onDone }: { release: Release; onClose: () => void; onDone: (r: Release) => void }) {
  const { t } = useTranslation()
  // Offset between server and local clock, so the countdown follows server time.
  const skew = useMemo(() => new Date(release.now).getTime() - Date.now(), [release.now])
  const me = useMe()
  // The server's confirmableAt is authoritative; the configured reading time is only a fallback.
  const readFrom = release.submittedAt ?? release.createdAt
  const confirmableAt = release.confirmableAt
    ? new Date(release.confirmableAt).getTime()
    : readFrom
      ? new Date(readFrom).getTime() + (me.app?.confirmReadSeconds ?? 10) * 1000
      : 0
  const expiresAt = release.expiresAt ? new Date(release.expiresAt).getTime() : 0
  const [now, setNow] = useState(() => Date.now() + skew)
  const confirmM = useConfirmRelease()
  const cancelM = useCancelRelease()
  const busy = confirmM.isPending || cancelM.isPending
  const err = confirmM.error ?? cancelM.error

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now() + skew), 200)
    return () => clearInterval(t)
  }, [skew])

  const remaining = Math.max(0, Math.ceil((confirmableAt - now) / 1000))
  const expired = expiresAt > 0 && now > expiresAt
  const items = release.items ?? []
  const anomalies = items.flatMap((it) => (it.kind === 'image' ? (it.payload.anomalies ?? []) : []).map((a) => ({ ...a, service: it.payload.service })))
  const restartOnly = items.length > 0 && items.every((it) => it.kind === 'restart')
  const syncOnly = items.length > 0 && items.every((it) => it.kind === 'sync')
  const first = items[0]
  // Every item of a batch shares the project and type, so the first decides.
  const scope = useServiceScope(first?.payload.service)
  const rule = approvalFor(me.app, release.env, scope.project, scope.type)

  const confirm = () => {
    if (busy || remaining > 0 || expired) return
    cancelM.reset()
    // Echo exactly the digests shown above; the server rejects any mismatch.
    confirmM.mutate({ id: release.id, digests: items.map(itemDigest).filter(Boolean) }, { onSuccess: (r) => onDone(r) })
  }

  const cancel = () => {
    if (busy) return
    confirmM.reset()
    cancelM.mutate({ id: release.id, reason: 'cancelled at confirmation' }, { onSuccess: () => onClose() })
  }

  return (
    <Modal
      width={720}
      title={restartOnly ? t('confirm.titleRestart', { env: release.env }) : syncOnly ? t('confirm.titleSync', { env: release.env }) : t('confirm.titleRelease', { env: release.env })}
      subtitle={
        <>
          <span className="mono">{release.id}</span> · <JiraLink ticket={release.jiraTicket} /> · {release.createdByName} · {fmtTime(release.submittedAt, true)}
        </>
      }
      onClose={onClose}
      closeOnEsc={!busy}
    >
      {anomalies.length > 0 && (
        <>
          <GroupHeader>{t('confirm.anomalies', { count: anomalies.length })}</GroupHeader>
          <Group>
            {anomalies.map((a, i) => (
              <div key={i} className={`anomaly ${a.code}`}>
                <b aria-hidden="true">▲</b>
                <div className="t">
                  <b className="inherit">{a.service}</b> · {a.message}
                </div>
              </div>
            ))}
          </Group>
        </>
      )}

      <GroupHeader>{t('confirm.manifest', { count: items.length })}</GroupHeader>
      <Group>
        {items.map((it) => {
          if (it.kind === 'restart') return <RestartRow key={it.id} it={it} />
          if (it.kind === 'sync') return <SyncRow key={it.id} it={it} />
          const p = it.payload
          return (
            <Row key={it.id} className="top">
              <div className="grow">
                <div className="t">
                  <b>{p.service}</b> <span className="muted">seq {it.sequence}</span>
                </div>
                <div className="d">
                  {t('confirm.current')}{' '}
                  {p.from ? (
                    <>
                      {p.from.version && <b>{p.from.version} </b>}
                      <span className="mono">{p.from.tag}</span>
                    </>
                  ) : (
                    <b className="red">{t('confirm.notDeployed')}</b>
                  )}
                </div>
                <div className="d mono break">{p.from?.digest ?? ''}</div>
                <div className="d ink" style={{ marginTop: 4 }}>
                  {t('confirm.target')} {p.to.version && <b>{p.to.version} </b>}
                  <span className="mono">{p.to.tag}</span>
                </div>
                {/* The digest is what gets deployed and audited: always visible, never folded. */}
                <div className="d mono break ink">{p.to.digest}</div>
                {(p.configChanges?.length ?? 0) > 0 && (
                  <div style={{ marginTop: 8 }}>
                    <div className={`d ${p.withConfig ? 'ink' : 'orange'}`}>
                      {p.withConfig ? t('confirm.configWithSync') : t('confirm.configWillApply')} {t('confirm.configUnsynced', { summary: changeSummary(p.configChanges ?? []) })}
                    </div>
                    <ConfigChanges changes={p.configChanges ?? []} />
                  </div>
                )}
              </div>
              {(p.anomalies?.length ?? 0) > 0 && <Pill tone="orange">{t('confirm.anomalyCount', { count: p.anomalies?.length })}</Pill>}
            </Row>
          )
        })}
      </Group>

      <GroupHeader>{t('confirm.reason')}</GroupHeader>
      <Group>
        <Row>
          <div className="t prewrap">{release.reason}</div>
        </Row>
      </Group>

      {rule && (
        <Banner>
          {t('confirm.approvalNote', { rule: rule.name, who: approvalText(rule) })}
        </Banner>
      )}
      <FormErrorBanner error={err} />
      {expired && <Banner tone="bad">{t('confirm.expired')}</Banner>}

      <ButtonRow style={{ marginTop: 16 }}>
        <Button variant="quiet" disabled={busy} onClick={onClose}>
          {t('confirm.later')}
        </Button>
        <Button variant="danger" loading={cancelM.isPending} disabled={busy} onClick={cancel}>
          {t('confirm.cancelRelease')}
        </Button>
        <span className="grow" />
        {/* Deliberately not the heaviest button on the sheet. */}
        <Button
          variant="quiet"
          className={remaining || expired ? '' : 'accent'}
          loading={confirmM.isPending}
          disabled={busy || remaining > 0 || expired}
          onClick={confirm}
          aria-live="polite"
        >
          {remaining > 0 ? (
            <span className="countdown">{t('confirm.readFirst', { seconds: remaining })}</span>
          ) : restartOnly ? (
            t('confirm.confirmRestart')
          ) : syncOnly ? (
            t('confirm.confirmSync')
          ) : (
            t('confirm.confirmExecute', {
              what: items.length === 1 && first?.kind === 'image' ? shortTag(first.payload.to.tag) : t('confirm.nItems', { count: items.length }),
            })
          )}
        </Button>
      </ButtonRow>
      <Note>{t('confirm.noAutoRun')}</Note>
    </Modal>
  )
}

function RestartRow({ it }: { it: Extract<Item, { kind: 'restart' }> }) {
  const { t } = useTranslation()
  const p = it.payload
  return (
    <Row className="top">
      <div className="grow">
        <div className="t">
          <b>{p.service}</b> <span className="muted">seq {it.sequence}</span> <Pill tone="blue">{t('confirm.restart')}</Pill>
        </div>
        <div className="d ink">
          {t('confirm.keep')} {p.current.version && <b>{p.current.version} </b>}
          <span className="mono">{p.current.tag || t('confirm.currentVersion')}</span>
        </div>
        {p.current.digest && <div className="d mono break ink">{p.current.digest}</div>}
        <div className="d" style={{ marginTop: 4 }}>
          {t('confirm.rollingRestart', { workloads: (p.workloads ?? []).map((w) => `${w.kind}/${w.name}`).join(t('scope.listSeparator')), app: p.app })}
        </div>
      </div>
    </Row>
  )
}

function SyncRow({ it }: { it: Extract<Item, { kind: 'sync' }> }) {
  const { t } = useTranslation()
  const p = it.payload
  const changes = p.changes ?? []
  return (
    <Row className="top">
      <div className="grow">
        <div className="t">
          <b>{p.service}</b> <span className="muted">seq {it.sequence}</span> <Pill tone="blue">{t('confirm.configSync')}</Pill>
          {p.prune && <Pill tone="red">{t('confirm.allowPrune')}</Pill>} {p.restart && <Pill>{t('confirm.restartAfterSync')}</Pill>}
        </div>
        <div className="d ink">
          {t('confirm.keep')} {p.current.version && <b>{p.current.version} </b>}
          <span className="mono">{p.current.tag || t('confirm.currentVersion')}</span>
        </div>
        {p.current.digest && <div className="d mono break ink">{p.current.digest}</div>}
        <div className="d" style={{ margin: '4px 0 8px' }}>
          {changeSummary(changes)} · Argo CD {p.app} · git <span className="mono">{p.revision.slice(0, 8)}</span>
        </div>
        <ConfigChanges changes={changes} />
      </div>
    </Row>
  )
}
