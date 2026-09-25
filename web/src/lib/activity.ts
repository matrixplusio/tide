/**
 * Whether a person is actually here.
 *
 * Several pages poll on a timer — the release detail every three seconds
 * while something is running — so "the browser made a request" says nothing
 * about whether anyone is watching. A session renewed on requests alone
 * would never end while a tab was open, which is not an idle timeout at all.
 *
 * So requests carry a header, and only the ones sent while somebody has
 * recently touched the page count as use. Reading counts: scrolling and
 * moving the pointer are as much presence as clicking.
 */
const EVENTS = ['pointerdown', 'pointermove', 'keydown', 'scroll', 'wheel', 'touchstart'] as const

/** How long after a touch of the page it still counts as somebody being here. */
const PRESENT_FOR = 5 * 60 * 1000

let lastSeen = Date.now()

function seen() {
  lastSeen = Date.now()
}

export function watchForActivity(target: Pick<Window, 'addEventListener'> = window) {
  for (const e of EVENTS) target.addEventListener(e, seen, { passive: true, capture: true })
}

/** True while somebody has been here recently and the tab is not hidden. */
export function personIsHere(now = Date.now()): boolean {
  if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return false
  return now - lastSeen < PRESENT_FOR
}

/** Test seam. */
export function resetActivity(at = Date.now()) {
  lastSeen = at
}
