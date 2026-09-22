import type { InputHTMLAttributes, Ref } from 'react'

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** "row" sits borderless inside a form row; "filled" is a standalone toolbar field. */
  appearance?: 'row' | 'filled'
  mono?: boolean
  ref?: Ref<HTMLInputElement>
}

export function Input({ appearance = 'row', mono, className, ...rest }: InputProps) {
  const c = [appearance === 'row' ? 'input' : 'field', mono && 'mono', className].filter(Boolean).join(' ')
  return <input {...rest} className={c} />
}
