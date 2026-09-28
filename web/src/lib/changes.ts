/** A commit that says nothing on its own: git's merge message, or the
 *  "build:<service>" commit the pipeline makes to name a service. Sampled
 *  across five active repositories these were nearly all of the noise in a
 *  history; the titles underneath were worth reading. Kept in the history
 *  (the running commit may be one of them) and hidden from the list. */
export function isNoiseCommit(title: string): boolean {
  return /^Merge (branch|remote-tracking branch|pull request|tag)\b/i.test(title) || /^build[:\s]/i.test(title)
}
