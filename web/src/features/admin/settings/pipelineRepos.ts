import type { PipelineRepo } from '../types'

/** Which upstream an entry serves: its name, or the first upstream when it has
 *  none — which is what a configuration written before there was a second
 *  upstream keeps meaning. Mirrors settings.PipelineRepo.For on the server. */
function ownerOf(entry: PipelineRepo, first: string): string {
  return entry.name || first
}

/** The entry serving one upstream, or undefined when none does yet. */
export function pipelineEntryFor(section: PipelineRepo | null | undefined, upstream: string, first: string): PipelineRepo | undefined {
  if (!section) return undefined
  if (ownerOf(section, first) === upstream) {
    const { others: _others, ...entry } = section
    return entry
  }
  return section.others?.find((o) => o.name === upstream)
}

/** The whole section with one upstream's entry replaced. The first upstream's
 *  entry is the top level (where a single-site configuration has always
 *  lived); every other upstream is an item of `others`, matched by name. The
 *  first save of a site with nothing stored yet goes to the top level. */
export function withPipelineEntry(section: PipelineRepo | null | undefined, upstream: string, first: string, entry: PipelineRepo): PipelineRepo {
  const others = section?.others ?? []
  if (!section || !section.baseUrl || ownerOf(section, first) === upstream) {
    return { ...entry, name: upstream === first ? '' : upstream, others }
  }
  const i = others.findIndex((o) => o.name === upstream)
  const next = { ...entry, name: upstream, others: undefined }
  return { ...section, others: i < 0 ? [...others, next] : others.map((o, j) => (j === i ? next : o)) }
}
