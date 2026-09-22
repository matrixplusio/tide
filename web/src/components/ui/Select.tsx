import type { Ref, SelectHTMLAttributes } from 'react'

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  appearance?: 'row' | 'filled'
  options: readonly (readonly [value: string, label: string])[]
  ref?: Ref<HTMLSelectElement>
}

export function Select({ appearance = 'row', options, className, ...rest }: SelectProps) {
  return (
    <select {...rest} className={[appearance === 'row' ? 'input select' : 'field', className].filter(Boolean).join(' ')}>
      {options.map(([v, label]) => (
        <option key={v} value={v}>
          {label}
        </option>
      ))}
    </select>
  )
}
