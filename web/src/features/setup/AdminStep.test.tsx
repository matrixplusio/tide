import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AdminStep } from './SetupPage'

function renderStep() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <AdminStep onBackToToken={() => {}} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('setup admin form', () => {
  it('shows mismatch and length errors together after submit, and focuses the first invalid field', async () => {
    const user = userEvent.setup()
    renderStep()
    const password = screen.getByLabelText(/^密码/, { selector: 'input' })
    const confirm = screen.getByLabelText(/^确认密码/, { selector: 'input' })

    await user.type(password, 'short')
    await user.type(confirm, 'different')
    await user.click(screen.getByRole('button', { name: '创建并进入 Tide' }))

    expect(await screen.findByText('两次输入的密码不一致')).toBeTruthy()
    expect(screen.getByText('密码至少 12 位（当前 5 位）')).toBeTruthy()
    expect(password.getAttribute('aria-invalid')).toBe('true')
    expect(confirm.getAttribute('aria-invalid')).toBe('true')
    expect(document.activeElement).toBe(password)
  })

  it('re-validates confirm live when the password changes', async () => {
    const user = userEvent.setup()
    renderStep()
    const password = screen.getByLabelText(/^密码/, { selector: 'input' })
    const confirm = screen.getByLabelText(/^确认密码/, { selector: 'input' })

    await user.type(password, 'correct horse battery')
    await user.type(confirm, 'correct horse battery')
    await user.tab()
    expect(screen.queryByText('两次输入的密码不一致')).toBeNull()

    await user.type(password, '!')
    expect(await screen.findByText('两次输入的密码不一致')).toBeTruthy()
  })

  it('does not show errors before a field is touched', () => {
    renderStep()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
