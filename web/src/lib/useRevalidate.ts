import { useEffect, useRef } from 'react'
import type { FieldPath, FieldValues, UseFormReturn } from 'react-hook-form'

/**
 * Re-validate `target` whenever one of `sources` changes, but only once the
 * target has something to say: it was touched, edited, or a submit was
 * attempted. Used for confirm-password (compare live against the password)
 * and for rules that depend on another field (password must not contain the
 * username, new password must differ from the current one).
 */
export function useRevalidate<T extends FieldValues>(form: UseFormReturn<T>, sources: FieldPath<T>[], target: FieldPath<T>) {
  const { watch, trigger, getFieldState, formState } = form
  const sourcesRef = useRef(sources)
  const submittedRef = useRef(formState.isSubmitted)
  useEffect(() => {
    sourcesRef.current = sources
    submittedRef.current = formState.isSubmitted
  })
  useEffect(() => {
    const sub = watch((_values, { name }) => {
      if (!name || !sourcesRef.current.includes(name as FieldPath<T>)) return
      const st = getFieldState(target)
      if (st.isTouched || st.isDirty || submittedRef.current) void trigger(target)
    })
    return () => sub.unsubscribe()
  }, [watch, trigger, getFieldState, target])
}
