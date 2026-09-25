import { beforeEach, describe, expect, it, vi } from 'vitest'
import { personIsHere, resetActivity, watchForActivity } from './activity'

describe('presence', () => {
  // main.tsx installs these once at start-up; the test has to do it too.
  watchForActivity()
  beforeEach(() => resetActivity())

  it('counts somebody as here just after they touched the page', () => {
    expect(personIsHere()).toBe(true)
  })

  // The point of the whole thing: a tab polling every three seconds with
  // nobody watching must stop counting as use, or the idle timeout never
  // fires.
  it('stops counting them after a while with no sign of them', () => {
    resetActivity(Date.now() - 6 * 60 * 1000)
    expect(personIsHere()).toBe(false)
  })

  it('takes reading as presence, not only clicking', () => {
    resetActivity(Date.now() - 6 * 60 * 1000)
    expect(personIsHere()).toBe(false)
    window.dispatchEvent(new Event('scroll'))
    expect(personIsHere()).toBe(true)
  })

  it('does not count a tab in the background', () => {
    const spy = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    expect(personIsHere()).toBe(false)
    spy.mockRestore()
  })

  it('listens for every sign of life it claims to', () => {
    const added: string[] = []
    watchForActivity({ addEventListener: ((e: string) => added.push(e)) as Window['addEventListener'] })
    for (const e of ['pointerdown', 'pointermove', 'keydown', 'scroll', 'wheel', 'touchstart']) {
      expect(added).toContain(e)
    }
  })
})
