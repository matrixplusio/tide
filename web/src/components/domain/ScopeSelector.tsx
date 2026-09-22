import { CheckboxGroup, type CheckboxOption } from '../ui'

/**
 * Picks the projects or service types a grant or rule is limited to. Nothing
 * selected means "all", which is also what the server stores as an empty list.
 */
export function ScopeSelector({
  options,
  value,
  onChange,
  onBlur,
  allLabel,
  ...aria
}: {
  options: readonly CheckboxOption[]
  value: readonly string[]
  onChange: (next: string[]) => void
  onBlur?: () => void
  allLabel: string
  id?: string
  'aria-invalid'?: boolean
  'aria-describedby'?: string
  'aria-label'?: string
}) {
  const all = value.length === 0 || value.includes('*')
  return (
    <CheckboxGroup
      {...aria}
      options={[{ value: '*', label: allLabel }, ...options]}
      value={all ? ['*'] : value}
      onChange={(next) => {
        // "All" and specific values are mutually exclusive.
        if (next.includes('*') && !all) onChange([])
        else onChange(next.filter((v) => v !== '*'))
      }}
      onBlur={onBlur}
    />
  )
}
