import { useTranslation } from 'react-i18next'
import { useEffect, useId, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { fmtTime, localInputToRfc3339, rfc3339ToLocalInput } from '../../lib/format'
import type { AuditEntry } from '../../lib/types'
import { Button, EmptyState, ErrorState, Group, Input, Loading, Page, Pager, StatusDot, Toolbar, Form } from '../../components/ui'
import { JiraLink } from '../../components/domain'
import { actionLabel } from './labels'
import { AUDIT_PAGE_SIZE, useAudit } from './queries'
import { auditFilterSchema, auditTextFields, type AuditFilterValues } from './schema'


function valuesFromParams(params: URLSearchParams): AuditFilterValues {
  return {
    jira: params.get('jira') ?? '',
    service: params.get('service') ?? '',
    env: params.get('env') ?? '',
    actor: params.get('actor') ?? '',
    action: params.get('action') ?? '',
    since: rfc3339ToLocalInput(params.get('since')),
    until: rfc3339ToLocalInput(params.get('until')),
  }
}

// Append-only log. Searchable by the questions people actually ask:
// "what did OPS-1234 deploy", "who touched prod last night".
export function AuditPage() {
  const { t } = useTranslation()
  const [params, setParams] = useSearchParams()
  const [open, setOpen] = useState<number | null>(null)
  const page = Math.max(1, Number(params.get('page')) || 1)
  const errId = useId()

  const form = useForm<AuditFilterValues>({ resolver: zodResolver(auditFilterSchema), mode: 'onTouched', defaultValues: valuesFromParams(params) })
  const { register, handleSubmit, formState, trigger, getFieldState, reset } = form
  const paramsKey = params.toString()

  // Keep the since ≤ until error live when either end changes.
  const revalidateUntil = () => {
    if (getFieldState('until').isTouched || form.formState.isSubmitted) void trigger('until')
  }

  // Back/forward navigation changes the URL: mirror it into the form.
  useEffect(() => {
    reset(valuesFromParams(new URLSearchParams(paramsKey)))
  }, [paramsKey, reset])

  const onSubmit = handleSubmit((v) => {
    const p = new URLSearchParams()
    for (const [k] of auditTextFields) if (v[k].trim()) p.set(k, v[k].trim())
    if (v.since) p.set('since', localInputToRfc3339(v.since))
    if (v.until) p.set('until', localInputToRfc3339(v.until))
    setParams(p)
  })

  const q = useAudit({
    jira: params.get('jira') ?? undefined,
    service: params.get('service') ?? undefined,
    env: params.get('env') ?? undefined,
    actor: params.get('actor') ?? undefined,
    action: params.get('action') ?? undefined,
    since: params.get('since') ?? undefined,
    until: params.get('until') ?? undefined,
    page,
  })
  const list = q.data?.items ?? []
  const firstError = auditTextFields.map(([k]) => formState.errors[k]?.message).find(Boolean) ?? formState.errors.since?.message ?? formState.errors.until?.message

  return (
    <>
      <Toolbar title={t('audit.title')} sub={t('audit.sub')} />
      <Page wide>
        <Form onSubmit={onSubmit} className="btnrow filters" style={{ marginTop: 0 }} aria-label={t('audit.searchLabel')}>
          {auditTextFields.map(([k, label]) => (
            <Input
              key={k}
              appearance="filled"
              className="filter-text"
              aria-label={t(label)}
              placeholder={t(label)}
              aria-invalid={!!formState.errors[k]}
              aria-describedby={formState.errors[k] ? errId : undefined}
              {...register(k)}
            />
          ))}
          <span className="filter-range">
            <label className="filter-dt" htmlFor={`${errId}-since`}>
              <span className="muted">{t('audit.from')}</span>
            </label>
            <Input id={`${errId}-since`} appearance="filled" type="datetime-local" aria-invalid={!!formState.errors.since} aria-describedby={formState.errors.since ? errId : undefined} {...register('since', { onChange: revalidateUntil })} />
          </span>
          <span className="filter-range">
            <label className="filter-dt" htmlFor={`${errId}-until`}>
              <span className="muted">{t('audit.to')}</span>
            </label>
            <Input id={`${errId}-until`} appearance="filled" type="datetime-local" aria-invalid={!!formState.errors.until} aria-describedby={formState.errors.until ? errId : undefined} {...register('until')} />
          </span>
          <Button type="submit">{t('audit.search')}</Button>
        </Form>
        {firstError && (
          <div className="ferr standalone" id={errId} role="alert">
            {firstError}
          </div>
        )}
        <div style={{ height: 12 }} />
        {q.isPending && <Loading />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}
        {q.data && (
          <>
            <Group>
              {list.length === 0 && <EmptyState>{t('audit.none')}</EmptyState>}
              {list.map((e) => (
                <AuditRow key={e.id} e={e} open={open === e.id} onToggle={() => setOpen(open === e.id ? null : e.id)} />
              ))}
            </Group>
            <Pager
              page={q.data.page || page}
              pageSize={q.data.page_size || AUDIT_PAGE_SIZE}
              total={q.data.total}
              onChange={(n) => {
                const p = new URLSearchParams(params)
                if (n > 1) p.set('page', String(n))
                else p.delete('page')
                setParams(p)
              }}
            />
          </>
        )}
      </Page>
    </>
  )
}

function AuditRow({ e, open, onToggle }: { e: AuditEntry; open: boolean; onToggle: () => void }) {
  const { t } = useTranslation()
  const detailId = useId()
  const s = summary(e)
  return (
    <div className="audit-item">
      <div className="row">
        <StatusDot state={/denied|failed/.test(e.action) ? 'bad' : /confirm$|succeeded|execute/.test(e.action) ? 'ok' : 'off'} />
        <div className="grow">
          <div className="t">
            <button type="button" className="linklike" aria-expanded={open} aria-controls={detailId} onClick={onToggle}>
              {actionLabel[e.action] ?? e.action}
            </button>{' '}
            <span className="mono faint tag">{e.action}</span>
          </div>
          <div className="d">
            {e.actorName} · {fmtTime(e.at, true)}
            {e.target && (
              <>
                {' · '}
                {e.target.startsWith('REL-') ? (
                  <Link to={`/releases/${encodeURIComponent(e.target)}`} className="mono">
                    {e.target}
                  </Link>
                ) : (
                  <span className="mono">{e.target}</span>
                )}
              </>
            )}
            {e.jiraTicket && (
              <>
                {' '}
                · <JiraLink ticket={e.jiraTicket} />
              </>
            )}
            {s && (
              <>
                {' '}
                · <span className="mono">{s}</span>
              </>
            )}
          </div>
        </div>
      </div>
      {open && (
        <div className="inset" id={detailId}>
          <pre className="log" style={{ margin: 0, maxHeight: 360 }}>
            {JSON.stringify(e.detail, null, 2)}
          </pre>
          {/* Who, from where, and the id that finds this request in the logs. */}
          <div className="note mono audit-meta">
            <span>actor sub: {e.actor}</span>
            {e.clientIp && <span>{t('audit.clientIp', { ip: e.clientIp })}</span>}
            {e.requestId && <span>request id: {e.requestId}</span>}
          </div>
        </div>
      )}
    </div>
  )
}

type Obj = Record<string, unknown>
const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)

function summary(e: AuditEntry): string {
  const d = isObj(e.detail) ? e.detail : {}
  const to = isObj(d.to) ? d.to : undefined
  const from = isObj(d.from) ? d.from : undefined
  const service = str(d.service)
  const toDigest = str(to?.digest)
  if (service && toDigest) return `${service}@${str(d.env) ?? ''} ${str(from?.digest)?.slice(0, 19) ?? '∅'} → ${toDigest.slice(0, 19)}`
  if (Array.isArray(d.items)) {
    const xs = d.items.filter(isObj).filter((i) => str(i.service))
    if (xs.length) return xs.map((i) => `${str(i.service)}@${str(i.env) ?? ''}`).join(', ')
  }
  if (d.error !== undefined && d.error !== null) return String(d.error).slice(0, 80)
  if (d.need !== undefined && d.need !== null) return `need ${String(d.need)}`
  return ''
}
