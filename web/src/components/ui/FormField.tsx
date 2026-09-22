import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'

export interface ControlProps {
  id: string
  'aria-invalid': boolean
  'aria-describedby'?: string
  'aria-required'?: boolean
}

export interface FormFieldProps {
  label: ReactNode
  /** Inline error; rendered under the control and linked via aria-describedby. */
  error?: string
  hint?: ReactNode
  required?: boolean
  children: (control: ControlProps) => ReactNode
  /** When the label wraps its own control (checkbox), render it as plain text. */
  plainLabel?: boolean
}

export function FormField({ label, error, hint, required, children, plainLabel }: FormFieldProps) {
  const { t } = useTranslation()
  const id = useId()
  const errId = `${id}-err`
  const hintId = `${id}-hint`
  const describedBy = [error && errId, hint && hintId].filter(Boolean).join(' ') || undefined
  return (
    <div className={`row ffield ${error ? 'invalid' : ''}`}>
      {plainLabel ? (
        <span className="flabel">{label}</span>
      ) : (
        <label className="flabel" htmlFor={id}>
          {label}
          {required && <span className="sr-only">{t('common.required')}</span>}
        </label>
      )}
      <div className="fcontrol">
        {children({ id, 'aria-invalid': !!error, 'aria-describedby': describedBy, 'aria-required': required || undefined })}
        {error && (
          <div className="ferr" id={errId} role="alert">
            {error}
          </div>
        )}
        {hint && (
          <div className="fhint" id={hintId}>
            {hint}
          </div>
        )}
      </div>
    </div>
  )
}
