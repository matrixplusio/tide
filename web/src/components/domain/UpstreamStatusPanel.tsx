import { useTranslation } from 'react-i18next'
import { fmtTime } from '../../lib/format'
import { Group, GroupHeader, Note, StatusDot } from '../ui'
import type { UpstreamStatus } from '../../lib/types'

/**
 * What every configured upstream actually answers, right now.
 *
 * The form below it says what Tide was told to connect to; this says whether
 * that works. They are different questions, and the second one is the one
 * somebody has when they open this page after a page stopped loading.
 */
export function UpstreamStatusPanel({ upstreams, error }: { upstreams: UpstreamStatus[]; error?: string }) {
  const { t } = useTranslation()
  if (error) {
    return (
      <>
        <GroupHeader>{t('upstreamStatus.title')}</GroupHeader>
        <Group>
          <div className="urow bad">
            <StatusDot state="bad" />
            <span className="grow">{error}</span>
          </div>
        </Group>
      </>
    )
  }
  if (upstreams.length === 0) return null
  return (
    <>
      <GroupHeader>{t('upstreamStatus.title')}</GroupHeader>
      <Group>
        {upstreams.map((u) => (
          <div className="ucard" key={u.name}>
            <div className="uhead">
              <span className="t">{u.name}</span>
              {(u.envs?.length ?? 0) > 0 ? (
                <span className="d">{t('upstreamStatus.serves', { envs: u.envs?.join(' / ') })}</span>
              ) : (
                // Configured but wired to nothing: worth saying, because it
                // means this upstream is not serving any environment.
                <span className="d">{t('upstreamStatus.noEnvs')}</span>
              )}
              <span className="grow" />
              <span className="d nowrap">{t('upstreamStatus.checkedAt', { at: fmtTime(u.checkedAt, true) })}</span>
            </div>
            <System name="Kargo" ok={u.kargoOk} version={u.kargoVersion} error={u.kargoError} />
            <System name="Argo CD" ok={u.argocdOk} version={u.argocdVersion} error={u.argocdError} />
          </div>
        ))}
      </Group>
      <Note>{t('upstreamStatus.note')}</Note>
    </>
  )
}

/** One system of one upstream: reachable, which version, and if not, why. */
function System({ name, ok, version, error }: { name: string; ok: boolean; version?: string; error?: string }) {
  const { t } = useTranslation()
  return (
    <div className={`urow ${ok ? '' : 'bad'}`}>
      <StatusDot state={ok ? 'ok' : 'bad'} label={ok ? t('upstreamStatus.up') : t('upstreamStatus.down')} />
      <span className="usys">{name}</span>
      {ok ? (
        <span className="d mono">{version || t('upstreamStatus.noVersion')}</span>
      ) : (
        // The upstream's own words, not a summary of them: whoever reads this
        // is about to go and fix the thing that produced them.
        <span className="d prewrap grow">{error || t('upstreamStatus.down')}</span>
      )}
    </div>
  )
}
