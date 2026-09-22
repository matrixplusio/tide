import { useTranslation } from 'react-i18next'
import { useMe } from '../../app/session'
import { jiraHref } from '../../lib/permissions'

/** A Jira ticket; a link when the release policy has a Jira base URL. Do not use inside another link. */
export function JiraLink({ ticket }: { ticket: string }) {
  const { t } = useTranslation()
  const me = useMe()
  if (!ticket) return <span className="faint">{t('domain.noJira')}</span>
  const href = jiraHref(me.app?.jiraBaseUrl, ticket)
  if (!href) return <span className="mono">{ticket}</span>
  return (
    <a className="mono" href={href} target="_blank" rel="noreferrer">
      {ticket}
    </a>
  )
}
