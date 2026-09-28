import { describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { Modal } from './Modal'

// Focus is handed out in a requestAnimationFrame, so every assertion about it
// has to let one frame pass first.
async function frame() {
  await act(async () => {
    await new Promise((r) => requestAnimationFrame(() => r(null)))
  })
}

describe('Modal', () => {
  it('always offers a way out', async () => {
    // A read-only sheet has no footer buttons and the backdrop is inert on
    // purpose, so without this button Esc was the only way to close it.
    const onClose = vi.fn()
    render(<Modal title="清单" onClose={onClose}><p>正文</p></Modal>)
    await frame()
    const x = screen.getByRole('button', { name: '关闭' })
    act(() => x.click())
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not open with the focus on dismiss', async () => {
    // The button is last in the DOM; picking "the first focusable element"
    // naively would land on it and greet the reader with a focus ring on the
    // one control that throws the sheet away.
    render(<Modal title="清单" onClose={vi.fn()}><p>正文</p></Modal>)
    await frame()
    expect(document.activeElement?.className).toContain('sheet')
    expect(document.activeElement?.className).not.toContain('sheet-x')
  })

  it('still puts the caret in the first field of a form', async () => {
    cleanup()
    render(
      <Modal title="新建组" onClose={vi.fn()}>
        <input aria-label="组名" />
      </Modal>,
    )
    await frame()
    expect(document.activeElement?.getAttribute('aria-label')).toBe('组名')
  })
})
