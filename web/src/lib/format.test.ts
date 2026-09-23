import { describe, expect, it } from 'vitest'
import { dayBound, describeUserAgent, shortTag } from './format'

describe('describeUserAgent', () => {
  it('names common browsers and systems', () => {
    expect(describeUserAgent('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36')).toBe('Chrome 152 · macOS')
    expect(describeUserAgent('Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36 Edg/140.0')).toBe('Edge 140 · Windows')
    expect(describeUserAgent('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1')).toBe('Safari 18 · iOS')
  })
  it('falls back to tool names and placeholders', () => {
    expect(describeUserAgent('curl/8.7.1')).toBe('curl 8.7.1')
    expect(describeUserAgent('Python-urllib/3.14')).toBe('Python-urllib 3.14')
    expect(describeUserAgent('')).toBe('未知客户端')
  })
})

describe('dayBound', () => {
  it('maps a local day to its first and last instant', () => {
    expect(new Date(dayBound('2026-09-18', false)!).getTime()).toBe(new Date(2026, 8, 18).getTime())
    expect(new Date(dayBound('2026-09-18', true)!).getTime()).toBe(new Date(2026, 8, 18, 23, 59, 59, 999).getTime())
    expect(dayBound('', false)).toBeUndefined()
    expect(dayBound('18/09/2026', true)).toBeUndefined()
  })
})

describe('shortTag', () => {
  // The commit and the build number are read off a pipeline and matched
  // character by character; shortening them is how two builds start looking
  // like the same one. Only the year and the time of day are dropped.
  it('keeps the commit and the build number whole', () => {
    expect(shortTag('20260920070636-4d7fdb08-0310')).toBe('0920-4d7fdb08-0310')
  })

  it('keeps a leading zero in the build number', () => {
    // 0310 and 310 are different builds; trimming the zero to save a column
    // makes them read as one.
    expect(shortTag('20260101000000-abcdef12-0001')).toBe('0101-abcdef12-0001')
  })

  it('leaves a tag it does not recognise alone', () => {
    expect(shortTag('v1.2.3')).toBe('v1.2.3')
    expect(shortTag('latest')).toBe('latest')
  })

  it('truncates an unrecognised tag only when it would break the column', () => {
    const long = 'release-candidate-for-the-autumn-launch'
    expect(shortTag(long)).toBe('release-candidate-fo…')
    expect(shortTag('')).toBe('')
    expect(shortTag(undefined)).toBe('')
  })
})
