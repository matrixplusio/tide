import { describe, expect, it } from 'vitest'
import { anomalyText } from './anomaly'

describe('anomalyText', () => {
  // The release records the sentence in the language of whoever confirmed it.
  // A reader working in another one used to get that sentence verbatim, which
  // is how an English release list ended up with Chinese lines in it.
  it('rebuilds the sentence in the reader language', () => {
    expect(anomalyText({ code: 'first_deploy', message: 'This environment has nothing deployed by Kargo yet; this is a first deployment' })).toBe(
      '该环境当前没有由 Kargo 部署的制品，这是首次部署',
    )
  })

  it('puts the stored facts back into it', () => {
    expect(anomalyText({ code: 'rollback', message: 'Rollback: the target v2 is older than the current v3', args: ['v2', 'v3'] })).toBe('版本回退：目标 v2 早于当前 v3')
  })

  // Releases confirmed before the facts were stored have only their own text,
  // and showing that is better than showing a sentence with holes in it.
  it('falls back to the stored text when the facts are missing', () => {
    const msg = 'Rollback: the target v2 is older than the current v3'
    expect(anomalyText({ code: 'rollback', message: msg })).toBe(msg)
  })

  it('falls back for a code it does not know', () => {
    expect(anomalyText({ code: 'something_new', message: 'whatever it said' })).toBe('whatever it said')
  })
})
