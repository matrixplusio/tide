import { useTranslation } from 'react-i18next'
import { useMemo } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useMe } from '../../app/session'
import { can } from '../../lib/permissions'
import { isApiError } from '../../lib/api'
import { ErrCode } from '../../lib/errcode'
import { dimensionOptions, dimensionValueName, matchesSearch } from '../../lib/catalog'
import { fmtTime } from '../../lib/format'
import type { Service } from '../../lib/types'
import { Button, EmptyState, ErrorState, GroupHeader, Input, Loading, Note, Page, Pill, Segmented, Select, Toolbar } from '../../components/ui'
import { DeployDot, NoUpstreamsBanner, VersionLabel } from '../../components/domain'
import { useServices } from './queries'

// A dimension with at most this many values renders as a segmented control
// (first dimension only); anything else is a select.
const SEGMENTED_MAX = 6

// Read-only matrix. No actions here on purpose: to operate you must open a
// specific environment, so the target is always an explicit choice.
export function ServicesPage() {
  const { t } = useTranslation()
  const me = useMe()
  const nav = useNavigate()
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const dims = me.app.dimensions
  const domain = params.get('domain') ?? ''
  const project = params.get('project') ?? ''
  const set = (k: string, v: string) => {
    const p = new URLSearchParams(params)
    if (v) p.set(k, v)
    else p.delete(k)
    setParams(p, { replace: true })
  }

  const data = useServices()
  const services = data.data?.services ?? []
  // Applications the upstreams returned but could not classify. While this is
  // above zero and nothing came through, "no services yet" is the one thing
  // the page must not say: the upstream is full, the settings match nothing.
  // Optional chaining covers the seconds during a rollout when this tab is
  // newer than the backend answering it.
  const unclassified = (data.data?.upstreams ?? []).reduce((n, u) => ((u.envs?.length ?? 0) > 0 && u.catalog?.kept === 0 ? n + (u.catalog.applications ?? 0) : n), 0)
  const envs = data.data?.envOrder ?? me.envOrder

  // Scope (project, domain, dimensions) and the search are applied separately
  // so an empty result can say whether widening the scope would help.
  const { groups, outOfScope } = useMemo(() => {
    const picked = (dims ?? []).map((d) => [d.key, params.get(d.key) ?? ''] as const).filter(([, v]) => v !== '')
    const out = new Map<string, Service[]>()
    let outOfScope = 0
    for (const s of data.data?.services ?? []) {
      if (!matchesSearch(s.name, q)) continue
      if (picked.some(([k, v]) => s.dimensions?.[k] !== v) || (domain && s.domain !== domain) || (project && (s.project ?? '') !== project)) {
        outOfScope++
        continue
      }
      const key = `${s.project ?? ''}\u0000${s.domain}`
      out.set(key, [...(out.get(key) ?? []), s])
    }
    return { groups: [...out].sort(([a], [b]) => a.localeCompare(b)), outOfScope }
  }, [data.data, q, params, dims, domain, project])
  const total = data.data?.services.length ?? 0
  const projects = [...new Set(services.map((s) => s.project ?? '').filter(Boolean))].sort()
  const domains = [...new Set(services.filter((s) => !project || s.project === project).map((s) => s.domain))].sort()
  const scoped = !!(project || domain || (dims ?? []).some((d) => params.get(d.key)))
  const clearScope = () => {
    const next = new URLSearchParams()
    if (q) next.set('q', q)
    setParams(next, { replace: true })
  }

  return (
    <>
      <Toolbar title={t('services.title')} sub={data.data ? t('services.sub', { count: total, at: fmtTime(data.data.at, true) }) : undefined} />
      <Page wide>
        <div className="btnrow filters" role="search" aria-label={t('services.filterLabel')} style={{ marginTop: 0, marginBottom: 4 }}>
          {projects.length > 0 && (
            <Select
              appearance="filled"
              aria-label={t('services.project')}
              value={project}
              options={[['', t('services.allProjects')], ...projects.map((x): [string, string] => [x, x])]}
              onChange={(e) => {
                const next = new URLSearchParams(params)
                if (e.target.value) next.set('project', e.target.value)
                else next.delete('project')
                next.delete('domain') // domains belong to a project
                setParams(next, { replace: true })
              }}
            />
          )}
          <Select appearance="filled" aria-label={t('services.domain')} value={domain} options={[['', t('services.allDomains')], ...domains.map((x): [string, string] => [x, x])]} onChange={(e) => set('domain', e.target.value)} />
          {(dims ?? []).map((d, i) => {
            const value = params.get(d.key) ?? ''
            const options = dimensionOptions(d, services)
            if (i === 0 && options.length <= SEGMENTED_MAX)
              return <Segmented key={d.key} label={d.name} value={value} options={[['', t('services.all')], ...options]} onChange={(v) => set(d.key, v)} />
            return <Select key={d.key} appearance="filled" aria-label={d.name} value={value} options={[['', t('services.allOf', { what: d.name })], ...options]} onChange={(e) => set(d.key, e.target.value)} />
          })}
          <Input appearance="filled" type="search" className="filter-text" aria-label={t('services.searchLabel')} placeholder={t('services.searchPlaceholder')} value={q} autoFocus onChange={(e) => set('q', e.target.value)} />
          {scoped && (
            <Button size="small" variant="quiet" onClick={clearScope}>
              {t('services.clearFilters')}
            </Button>
          )}
        </div>
        {data.isPending && <Loading label={t('services.reading')} />}
        {isApiError(data.error, ErrCode.NoUpstreams) ? (
          <NoUpstreamsBanner canConfigure={can(me, 'environments.manage')}>{t('services.noUpstreams')}</NoUpstreamsBanner>
        ) : (
          data.error && <ErrorState error={data.error} onRetry={() => void data.refetch()} />
        )}
        {data.data && groups.length === 0 && (
          <EmptyState>
            {total === 0 ? (
              unclassified > 0 ? (
                <>
                  {t('services.noneClassified', { count: unclassified })}{' '}
                  {can(me, 'environments.manage') && (
                    <Button size="small" variant="quiet" onClick={() => nav('/admin/catalog')}>
                      {t('services.openCatalog')}
                    </Button>
                  )}
                </>
              ) : (
                t('services.none')
              )
            ) : outOfScope > 0 ? (
              <>
                {t('services.filteredOut', { count: outOfScope })}{' '}
                <Button size="small" variant="quiet" onClick={clearScope}>
                  {t('services.searchAll')}
                </Button>
              </>
            ) : (
              t('services.noMatch')
            )}
          </EmptyState>
        )}
        {groups.map(([key, list]) => {
          const [p = '', d = ''] = key.split('\u0000')
          return (
            <section key={key} aria-label={p ? `${p} / ${d}` : d}>
              <GroupHeader>{t('services.groupHeader', { group: `${p ? `${p} / ` : ''}${d}`, count: list.length })}</GroupHeader>
              <div className="group" style={{ overflowX: 'auto' }}>
                <table className="tbl matrix fixed">
                  <thead>
                    <tr>
                      <th scope="col" className="svccol">
                        {t('services.colService')}
                      </th>
                      {(dims ?? []).map((d) => (
                        <th scope="col" className="hide-sm dimcol" key={d.key}>
                          {d.name}
                        </th>
                      ))}
                      {envs.map((e) => (
                        <th scope="col" key={e} className="envcol">
                          {e}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {list.map((s) => (
                      <tr
                        key={s.name}
                        className="tap"
                        tabIndex={0}
                        onClick={() => nav(`/services/${encodeURIComponent(s.name)}`, { state: { from: `/services?${params}` } })}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') nav(`/services/${encodeURIComponent(s.name)}`, { state: { from: `/services?${params}` } })
                        }}
                      >
                        <td className="nowrap">
                          <b>{s.name}</b>
                          {!!s.conflicts?.length && (
                            <>
                              {' '}
                              <Pill tone="red" title={s.conflicts.join('\n')}>
                                {t('services.nameConflict')}
                              </Pill>
                            </>
                          )}
                        </td>
                        {(dims ?? []).map((d) => {
                          const v = s.dimensions?.[d.key]
                          return (
                            <td className="hide-sm" key={d.key}>
                              {v && <span className="pill">{dimensionValueName(d, v)}</span>}
                            </td>
                          )
                        })}
                        {envs.map((e, i) => {
                          const dep = s.envs[e]
                          const prevEnv = i > 0 ? envs[i - 1] : undefined
                          const prev = prevEnv ? s.envs[prevEnv] : undefined
                          const same = !!dep?.digest && dep.digest === prev?.digest
                          const nextEnv = envs[i + 1]
                          const sameNext = !!dep?.digest && !!nextEnv && dep.digest === s.envs[nextEnv]?.digest
                          return (
                            <td key={e} className={`envcol ${sameNext ? 'link-r' : ''}`} data-env={e} title={dep ? `${dep.tag ?? ''}\n${dep.digest ?? ''}` : ''}>
                              <span className={`envcell ${same ? 'same' : ''}`}>
                                <DeployDot d={dep} inFlight={!!data.data?.inFlight[`${s.name}/${e}`]} />
                                <VersionLabel a={dep} />
                              </span>
                            </td>
                          )
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )
        })}
        <Note>{t('services.legend')}</Note>
      </Page>
    </>
  )
}
