import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { PromotionSteps } from './Live'
import type { PromotionView } from '../../lib/types'

function promo(statuses: string[]): PromotionView {
  return {
    name: 'svc-dev.01abc',
    phase: 'Succeeded',
    createdAt: '2026-09-25T08:53:18Z',
    finishedAt: '2026-09-25T08:53:21Z',
    steps: statuses.map((status, i) => ({ uses: ['git-clone', 'kustomize-set-image', 'git-commit', 'git-push'][i], name: `task-1::step-${i + 1}`, status })),
  } as PromotionView
}

describe('PromotionSteps', () => {
  // A promotion that changes nothing still runs: git-commit reports Skipped
  // because there was no diff. That is a normal outcome and has to look like
  // one — an icon with nothing in it reads as a step that never ran.
  it('gives a skipped step a mark of its own', () => {
    const { container } = render(<PromotionSteps p={promo(['Succeeded', 'Succeeded', 'Skipped', 'Succeeded'])} />)
    const icons = [...container.querySelectorAll('.stepi')]
    expect(icons).toHaveLength(4)
    const skipped = icons.find((e) => !e.classList.contains('ok'))!
    // Empty is what "pending" looks like, and a step that was deliberately
    // passed over is not the same thing as one that has not happened yet.
    expect(skipped.textContent).not.toBe('')
    expect(skipped.getAttribute('aria-label')).toBeTruthy()
  })

  // The icon used to be class "stepi skip", and ".skip" is the skip-to-content
  // link: position:absolute, z-index 100, its own padding. The icon left its
  // row, covered the tick above it, and the row it belonged to showed nothing.
  it('does not reuse the skip-to-content class name', () => {
    const { container } = render(<PromotionSteps p={promo(['Skipped'])} />)
    const icon = container.querySelector('.stepi')!
    expect(icon.classList.contains('skip')).toBe(false)
    // That the name is not a global utility is checked where the stylesheet
    // can actually be read — Vitest hands back an empty string for a CSS
    // import, so asserting against it here passes whatever the CSS says.
    // See TestNoModifierClassIsAlsoAUtility.
  })
})
