import { useRef } from 'react'
import type { FormEvent, FormHTMLAttributes, ReactNode } from 'react'
import { focusFirstInvalid } from '../../lib/forms'

export interface FormProps extends Omit<FormHTMLAttributes<HTMLFormElement>, 'onSubmit'> {
  onSubmit: (e: FormEvent<HTMLFormElement>) => unknown
  children: ReactNode
}

/**
 * The only form element in the app. After every submit — rejected by client
 * validation or answered with server field errors — focus moves to the first
 * invalid control once React has rendered the errors. Browser validation is
 * off; validation belongs to the schema and the server.
 */
export function Form({ onSubmit, children, ...rest }: FormProps) {
  const ref = useRef<HTMLFormElement>(null)
  const handle = async (e: FormEvent<HTMLFormElement>) => {
    const focusedAtSubmit = document.activeElement
    try {
      await onSubmit(e)
    } finally {
      // Two frames: the first lets React commit error state, the second runs after layout.
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          // Only move focus if the person has not moved it themselves since
          // submitting — otherwise a late frame steals focus mid-typing and
          // keystrokes are lost.
          const now = document.activeElement
          if (now === focusedAtSubmit || now === document.body || now === null) focusFirstInvalid(ref.current)
        }),
      )
    }
  }
  return (
    <form ref={ref} noValidate {...rest} onSubmit={handle}>
      {children}
    </form>
  )
}
