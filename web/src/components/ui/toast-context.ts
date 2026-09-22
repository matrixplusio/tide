import { createContext, useContext } from 'react'

export type ToastTone = 'success' | 'error'

export interface ToastApi {
  success: (message: string) => void
  error: (message: string) => void
}

export const ToastContext = createContext<ToastApi | null>(null)

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used inside <ToastProvider>')
  return ctx
}
