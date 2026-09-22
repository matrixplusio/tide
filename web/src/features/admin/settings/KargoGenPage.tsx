import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, EmptyState, ErrorState, Group, GroupHeader, Loading, Note, Page, Select, Toolbar, useToast } from '../../../components/ui'
import { useServices } from '../../services/queries'
import { useKargoPlan, usePushKargo, useSettings } from '../queries'
import type { KargoFile } from '../types'
import { PipelineRepoForm } from './PipelineRepoForm'

/**
 * Generate the Kargo pipeline for a business domain.
 *
 * Grouped by domain, not a flat file list. The domain is a Kargo project and
 * the unit somebody reviews; ninety files sorted by name are not reviewable,
 * and a header per domain does the separating that a border would otherwise
 * be asked to do.
 *
 * The YAML is shown in full rather than summarised. This is about to be
 * committed to a repository: a count of files says nothing about whether it
 * is right, so the reading has to be possible here.
 */
export function KargoGenPage() {
  const { t } = useTranslation()
  const toast = useToast()
  const [domain, setDomain] = useState('')
  const [asked, setAsked] = useState(false)
  const [open, setOpen] = useState<string | null>(null)
  const [shut, setShut] = useState<Set<string>>(new Set())
  const q = useKargoPlan(domain, asked)
  const result = q.data?.result

  const settings = useSettings()
  const repo = settings.data?.pipeline
  const canPush = !!(repo?.baseUrl && repo.project && repo.token)
  const push = usePushKargo()

  // The picker has to be usable before anything has been generated, so the
  // domains come from the catalog the rest of the app already holds rather
  // than from a run of the generator.
  const services = useServices()
  const domains = useMemo(() => {
    const count = new Map<string, number>()
    for (const s of services.data?.services ?? []) {
      if (s.domain) count.set(s.domain, (count.get(s.domain) ?? 0) + 1)
    }
    return [...count].sort(([a], [b]) => a.localeCompare(b))
  }, [services.data])

  const download = () => {
    // A file download, not a fetch: the endpoint answers with a zip and the
    // browser knows what to do with it.
    window.location.href = `/api/v1/kargo/generate.zip?domain=${encodeURIComponent(domain)}`
  }

  const generated = (result?.domains ?? []).length > 0
  // Collapsed by default once there is more than one, because thirteen
  // domains of four files each is fifty-two rows before anything has been
  // asked for. Generating a single domain is already a request to see it.
  const isOpen = (name: string) => (result?.domains.length ?? 0) === 1 || !shut.has(name)
  const toggleDomain = (name: string) =>
    setShut((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  const allShut = (result?.domains ?? []).every((d) => shut.has(d.name))

  return (
    <>
      <Toolbar title={t('kargogen.title')} sub={t('kargogen.sub')} />
      <Page>
        <Note>{t('kargogen.note')}</Note>

        <div className="btnrow" style={{ marginBottom: 4 }}>
          <Select
            appearance="filled"
            aria-label={t('kargogen.domain')}
            value={domain}
            options={[['', t('kargogen.allDomains')], ...domains.map(([name, n]): [string, string] => [name, `${name} · ${t('kargogen.serviceCount', { count: n })}`])]}
            onChange={(e) => setDomain(e.target.value)}
          />
          <Button onClick={() => (asked ? void q.refetch() : setAsked(true))} disabled={q.isFetching}>
            {q.isFetching ? t('kargogen.generating') : t('kargogen.generate')}
          </Button>
          {generated && (
            <>
              <Button variant="quiet" onClick={download}>
                {t('kargogen.download')}
              </Button>
              {/* Quiet on purpose: this one writes to a repository, and the
                  visual weight of a control really does move people's hands. */}
              <Button
                variant="quiet"
                disabled={!canPush || push.isPending}
                title={canPush ? undefined : t('kargogen.saveRepoFirst')}
                onClick={() => push.mutate({ domain }, { onSuccess: (r) => toast.success(t('kargogen.pushed', { files: r.files, branch: r.branch })) })}
              >
                {push.isPending ? t('kargogen.pushing') : t('kargogen.push')}
              </Button>
            </>
          )}
        </div>

        {push.error && <ErrorState error={push.error} />}
        {push.data?.commit.web_url && (
          <Note>
            <a href={push.data.commit.web_url} target="_blank" rel="noreferrer">
              {t('kargogen.viewCommit')}
            </a>{' '}
            · <span className="mono">{push.data.commit.short_id ?? push.data.commit.id.slice(0, 8)}</span> · {push.data.branch}
          </Note>
        )}

        {q.isFetching && <Loading label={t('kargogen.generating')} />}
        {q.error && <ErrorState error={q.error} onRetry={() => void q.refetch()} />}

        {result && !q.isFetching && (
          <>
            {/* One quiet line, not four cards. These are the totals for
                something whose substance is the YAML below; giving them a
                row of panels each makes the summary shout over the thing it
                summarises. */}
            {generated && (
              <p className="tally">
                <Tally n={result.domains.length} of={t('kargogen.countDomains')} />
                <Tally n={result.services} of={t('kargogen.countServices')} />
                <Tally n={result.warehouses} of={t('kargogen.countWarehouses')} />
                <Tally n={result.stages} of={t('kargogen.countStages')} />
                <Tally n={result.fileCount} of={t('kargogen.countFiles')} />
              </p>
            )}

            {/* Skipped before the YAML, on purpose: it decides whether the
                generated set is complete, and under a hundred lines of YAML
                it stops being read. */}
            {(result.skipped ?? []).length > 0 && (
              <>
                <GroupHeader right={String((result.skipped ?? []).length)}>{t('kargogen.skipped')}</GroupHeader>
                <Group>
                  {(result.skipped ?? []).map((s, i) => (
                    <div className="row warn" key={`${s.service}-${s.env ?? ''}-${i}`}>
                      <span className="fname mono">
                        {s.service}
                        {s.env ? ` · ${s.env}` : ''}
                      </span>
                      <span className="d">{s.reason}</span>
                    </div>
                  ))}
                </Group>
              </>
            )}

            {!generated ? (
              <EmptyState>{t('kargogen.nothing')}</EmptyState>
            ) : (
              <>
                {result.domains.length > 1 && (
                  <div className="btnrow" style={{ justifyContent: 'flex-end', marginBottom: 0 }}>
                    <Button
                      size="small"
                      variant="quiet"
                      onClick={() => setShut(allShut ? new Set() : new Set(result.domains.map((d) => d.name)))}
                    >
                      {allShut ? t('kargogen.expandAll') : t('kargogen.collapseAll')}
                    </Button>
                  </div>
                )}
                {result.domains.map((d) => {
                  const shown = isOpen(d.name)
                  return (
                    <section key={d.name} aria-label={d.name}>
                      <button type="button" className="ghead" aria-expanded={shown} onClick={() => toggleDomain(d.name)}>
                        <span className="fcar" aria-hidden="true">
                          {shown ? '▾' : '▸'}
                        </span>
                        {d.name}
                        <span className="r">{t('kargogen.domainCounts', { warehouses: d.warehouses, stages: d.stages })}</span>
                      </button>
                      {shown && (
                        <Group>
                          {d.files.map((f) => (
                            <FileRow key={f.path} file={f} open={open === f.path} onToggle={() => setOpen(open === f.path ? null : f.path)} />
                          ))}
                        </Group>
                      )}
                    </section>
                  )
                })}
              </>
            )}
          </>
        )}

        <PipelineRepoForm initial={repo} />
      </Page>
    </>
  )
}

function Tally({ n, of }: { n: number; of: string }) {
  return (
    <span className="tally-item">
      <b>{n}</b> {of}
    </span>
  )
}

/** One generated file, expandable. The path is shown without its domain
 *  prefix: the group header already said which domain this is, and repeating
 *  it in every row is noise that pushes the filename off the edge. */
function FileRow({ file, open, onToggle }: { file: KargoFile; open: boolean; onToggle: () => void }) {
  const { t } = useTranslation()
  const name = file.path.slice(file.path.indexOf('/') + 1)
  const lines = file.yaml.split('\n').length
  return (
    <div>
      <button type="button" className="row rowbtn" aria-expanded={open} onClick={onToggle}>
        <span className="fcar" aria-hidden="true">
          {open ? '▾' : '▸'}
        </span>
        <span className="fname mono">{name}</span>
        <span className="fmeta">{t('kargogen.lines', { count: lines })}</span>
      </button>
      {open && <pre className="yaml">{file.yaml}</pre>}
    </div>
  )
}
