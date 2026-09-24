import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { ToastProvider } from '../../../components/ui'
import type { Notify } from '../types'
import { NotifyForm } from './NotifyForm'

function show(initial: Notify) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <ToastProvider>
          <NotifyForm initial={initial} />
        </ToastProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

const oneChannel: Notify = {
  channels: [{ name: 'ops', kind: 'lark', url: 'https://example.com/hook', secret: '', enabled: true }],
  rules: [],
}

describe('NotifyForm', () => {
  // The kind list holds catalogue keys, and a Select renders its options as
  // given: passing the list straight through put "notifyForm.kindLark" in
  // front of the person choosing a channel type.
  it('shows channel kinds as words, not as catalogue keys', () => {
    show(oneChannel)
    const select = screen.getByLabelText(/类型/) as HTMLSelectElement
    const labels = [...select.options].map((o) => o.text)
    expect(labels.some((l) => l.startsWith('notifyForm.'))).toBe(false)
    expect(labels).toContain('Teams')
  })
})
