import type { InputHTMLAttributes, ReactNode, Ref } from 'react'

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> {
  label: ReactNode
  ref?: Ref<HTMLInputElement>
}

export function Checkbox({ label, className, id, ...rest }: CheckboxProps) {
  return (
    <label className={['check', className].filter(Boolean).join(' ')} htmlFor={id}>
      <input {...rest} id={id} type="checkbox" />
      <span>{label}</span>
    </label>
  )
}
