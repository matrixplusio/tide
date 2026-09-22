import { createContext, useContext } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Me } from '../lib/types'

export const ME_KEY = ['me'] as const

export function useMeQuery(enabled: boolean) {
  return useQuery({ queryKey: ME_KEY, queryFn: ({ signal }) => apiFetch<Me>('/api/v1/me', { signal }), retry: false, enabled })
}

export const MeContext = createContext<Me | null>(null)

export function useMe(): Me {
  const me = useContext(MeContext)
  if (!me) throw new Error('useMe used outside the signed-in shell')
  return me
}
