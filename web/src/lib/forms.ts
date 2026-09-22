import type { FieldPath, FieldValues, UseFormReturn } from 'react-hook-form'
import { ApiError } from './api'
import { ErrCode } from './errcode'

export interface ServerErrorOptions {
  /**
   * Translate a server field path (JSON name, e.g. "items.0.kargoUrl") into a
   * form field name. Return null when the form has no such field.
   */
  mapField?: (serverField: string) => string | null
}

/**
 * Apply an error from a submit onto the form.
 *
 * 1007 field errors land on their fields (the first one is focused). Whatever
 * cannot be attached to a field is returned so the caller can show it in the
 * form-level banner; null means nothing is left to show there.
 * User input is never reset here.
 */
export function applyServerError<T extends FieldValues>(form: UseFormReturn<T>, err: unknown, opts: ServerErrorOptions = {}): unknown {
  if (!(err instanceof ApiError) || err.code !== ErrCode.Validation || !err.fields?.length) return err

  const values = form.getValues()
  const known = (name: string) => getPath(values, name) !== undefined
  const unmatched: string[] = []
  let focused = false
  for (const f of err.fields) {
    const name = opts.mapField ? opts.mapField(f.field) : f.field
    if (name && known(name)) {
      form.setError(name as FieldPath<T>, { type: 'server', message: f.msg }, { shouldFocus: !focused })
      focused = true
    } else {
      unmatched.push(f.msg)
    }
  }
  if (unmatched.length === 0) return null
  return new ApiError({
    code: err.code,
    msg: focused ? unmatched.join('；') : `${err.msg}：${unmatched.join('；')}`,
    httpStatus: err.httpStatus,
    requestId: err.requestId,
    data: err.data,
  })
}

function getPath(obj: unknown, path: string): unknown {
  let cur: unknown = obj
  for (const key of path.split('.')) {
    if (cur === null || typeof cur !== 'object') return undefined
    cur = (cur as Record<string, unknown>)[key]
  }
  return cur
}

/** Focus the first control marked invalid inside a container (fallback for custom controls). */
export function focusFirstInvalid(root: HTMLElement | null) {
  if (!root) return
  const el = root.querySelector<HTMLElement>('[aria-invalid="true"]')
  if (el && !el.contains(document.activeElement)) el.focus()
}
