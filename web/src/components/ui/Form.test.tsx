import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Button } from './Button'

// Regression: pressing a submit button blurred the focused field, on-blur
// validation inserted error text, the button moved, and the click was lost.
describe('submit button', () => {
  it('does not steal focus on mouse down', () => {
    render(
      <form>
        <input aria-label="name" autoFocus />
        <Button type="submit">保存</Button>
      </form>,
    )
    const input = screen.getByLabelText('name')
    input.focus()
    const notPrevented = fireEvent.mouseDown(screen.getByRole('button', { name: '保存' }))
    expect(notPrevented).toBe(false)
  })

  it('keeps default behaviour for plain buttons', () => {
    render(<Button>取消</Button>)
    expect(fireEvent.mouseDown(screen.getByRole('button', { name: '取消' }))).toBe(true)
  })
})

// Regression: the delayed "focus first invalid" frame ran after the user had
// already clicked into another field and stole focus mid-typing.
describe('Form focus after submit', () => {
  it('does not steal focus the user moved elsewhere', async () => {
    const { Form } = await import('./Form')
    const frames: FrameRequestCallback[] = []
    const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
      frames.push(cb)
      return frames.length
    })
    render(
      <Form onSubmit={(e) => e.preventDefault()}>
        <input aria-label="first" aria-invalid="true" />
        <input aria-label="second" />
        <Button type="submit">保存</Button>
      </Form>,
    )
    const button = screen.getByRole('button', { name: '保存' })
    button.focus()
    fireEvent.submit(button.closest('form')!)
    await Promise.resolve()
    screen.getByLabelText('second').focus() // user moves on before the frames run
    while (frames.length) frames.shift()!(0)
    expect(document.activeElement).toBe(screen.getByLabelText('second'))
    raf.mockRestore()
  })

  it('focuses the first invalid control when focus stayed put', async () => {
    const { Form } = await import('./Form')
    const frames: FrameRequestCallback[] = []
    const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
      frames.push(cb)
      return frames.length
    })
    render(
      <Form onSubmit={(e) => e.preventDefault()}>
        <input aria-label="first" aria-invalid="true" />
        <Button type="submit">保存</Button>
      </Form>,
    )
    const button = screen.getByRole('button', { name: '保存' })
    button.focus()
    fireEvent.submit(button.closest('form')!)
    await Promise.resolve()
    await Promise.resolve()
    while (frames.length) frames.shift()!(0)
    expect(document.activeElement).toBe(screen.getByLabelText('first'))
    raf.mockRestore()
  })
})
