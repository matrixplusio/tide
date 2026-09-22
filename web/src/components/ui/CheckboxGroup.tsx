import type { ReactNode, Ref } from 'react'
import { Checkbox } from './Checkbox'

export interface CheckboxOption {
  value: string
  label: ReactNode
  disabled?: boolean
}

export interface CheckboxGroupProps {
  /** Visible group caption; omit when the surrounding FormField already labels it. */
  legend?: ReactNode
  options: readonly CheckboxOption[]
  value: readonly string[]
  onChange: (next: string[]) => void
  onBlur?: () => void
  id?: string
  name?: string
  'aria-invalid'?: boolean
  'aria-describedby'?: string
  'aria-label'?: string
  ref?: Ref<HTMLFieldSetElement>
}

/**
 * Multi-select as a row of checkboxes. The fieldset is focusable (tabIndex -1)
 * so "focus the first invalid control" can land on it.
 */
export function CheckboxGroup({ legend, options, value, onChange, onBlur, id, name, ref, ...aria }: CheckboxGroupProps) {
  const toggle = (v: string, on: boolean) => {
    const next = on ? [...value.filter((x) => x !== v), v] : value.filter((x) => x !== v)
    // Keep the options' order so the value is stable.
    const order = options.map((o) => o.value)
    next.sort((a, b) => {
      const ia = order.indexOf(a)
      const ib = order.indexOf(b)
      return (ia < 0 ? order.length : ia) - (ib < 0 ? order.length : ib)
    })
    onChange(next)
  }
  return (
    <fieldset ref={ref} id={id} className="choice-group" tabIndex={-1} {...aria}>
      {legend && <legend>{legend}</legend>}
      <div className="choices">
        {options.map((o) => (
          <Checkbox
            key={o.value}
            id={id ? `${id}-${o.value}` : undefined}
            name={name}
            label={o.label}
            checked={value.includes(o.value)}
            disabled={o.disabled}
            onBlur={onBlur}
            onChange={(e) => toggle(o.value, e.target.checked)}
          />
        ))}
      </div>
    </fieldset>
  )
}
