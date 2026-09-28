import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { SearchPalette } from './SearchPalette'
import { searchHits } from '../../lib/search'
import type { Service } from '../../lib/types'

const svc = (name: string, project: string, domain: string): Service => ({ name, project, domain, envs: {} })
const services = [svc('order-api', 'acme', 'shop'), svc('portal-api', 'acme', 'web'), svc('media-api', 'globex', 'shop')]

describe('searchHits', () => {
  it('ranks a name prefix above a name match above a project or domain match', () => {
    expect(searchHits('shop', services).map((h) => (h.kind === 'service' ? h.name : h.id))).toEqual(['media-api', 'order-api'])
    expect(searchHits('api', services).map((h) => (h.kind === 'service' ? h.name : h.id))).toEqual(['media-api', 'order-api', 'portal-api'])
    expect(searchHits('ord', services)[0]).toMatchObject({ kind: 'service', name: 'order-api' })
  })

  it('turns a release id into a jump, whatever the case', () => {
    expect(searchHits('rel-20260929-004', services)).toEqual([{ kind: 'release', id: 'REL-20260929-004', to: '/releases/REL-20260929-004' }])
  })

  it('shows nothing for nothing', () => {
    expect(searchHits('   ', services)).toEqual([])
  })
})

describe('SearchPalette', () => {
  it('opens the chosen service on Enter and closes', async () => {
    const onClose = vi.fn()
    render(
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<SearchPalette services={services} onClose={onClose} />} />
          <Route path="/services/:name" element={<h1>service page</h1>} />
        </Routes>
      </MemoryRouter>,
    )
    const box = screen.getByRole('textbox')
    await userEvent.type(box, 'portal')
    expect(screen.getByText('portal-api').closest('button')?.getAttribute('aria-current')).toBe('true')
    await userEvent.keyboard('{Enter}')
    expect(onClose).toHaveBeenCalled()
    expect(await screen.findByText('service page')).toBeTruthy()
  })
})
