import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { VersionLabel } from './labels'

describe('VersionLabel', () => {
  it('shows the tag and nothing else for an ordinary deployment', () => {
    render(<VersionLabel a={{ tag: '20260923062916-17bb04a6-0006' }} />)
    expect(screen.getByText('0923-17bb04a6-0006')).toBeTruthy()
    expect(screen.queryByText('镜像不存在')).toBeNull()
  })

  // A tag nobody ever pushed still deploys: the pod sits pulling something
  // that does not exist, and the row otherwise looks like a service whose
  // build simply carried no labels.
  it('says so when the registry has never heard of the tag', () => {
    render(<VersionLabel a={{ tag: 'PLACEHOLDER', imageUnknown: true }} />)
    expect(screen.getByText('PLACEHOLDER')).toBeTruthy()
    expect(screen.getByText('镜像不存在')).toBeTruthy()
  })

  it('leaves a version that was read alone', () => {
    render(<VersionLabel a={{ version: 'v1.4.0', tag: '20260921000000-b5dab161-0008' }} />)
    expect(screen.getByText('v1.4.0')).toBeTruthy()
    expect(screen.queryByText('镜像不存在')).toBeNull()
  })

  // After an aborted blue-green the pods run the old version while Kargo
  // still records the new one. Showing the record as "current" is what made
  // an abort look like it had gone through.
  it('shows what runs, and the Kargo record beside it, when they differ', () => {
    render(<VersionLabel a={{ version: 'v2.0.0', tag: '20261015093244-0e5615c8-1928', running: '20260529094630-38' }} full />)
    expect(screen.getByText('20260529094630-38')).toBeTruthy()
    expect(screen.getByText('Kargo 记录为 20261015093244-0e5615c8-1928')).toBeTruthy()
    expect(screen.queryByText('v2.0.0')).toBeNull()
  })
})
