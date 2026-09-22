import type { ButtonHTMLAttributes, ReactNode, Ref } from 'react'
import { Link, type LinkProps } from 'react-router-dom'

export type ButtonVariant = 'primary' | 'quiet' | 'danger'
export type ButtonSize = 'md' | 'small'

function cls(variant: ButtonVariant, size: ButtonSize, extra?: string) {
  return ['btn', variant !== 'primary' && variant, size === 'small' && 'small', extra].filter(Boolean).join(' ')
}

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
  /** Shows a spinner, sets aria-busy and blocks further clicks. */
  loading?: boolean
  loadingText?: ReactNode
  ref?: Ref<HTMLButtonElement>
}

export function Button({ variant = 'primary', size = 'md', loading = false, loadingText, disabled, className, children, type = 'button', onMouseDown, ...rest }: ButtonProps) {
  return (
    <button
      {...rest}
      type={type}
      onMouseDown={(e) => {
        // A submit button must not take focus on press: blurring the field
        // runs on-blur validation, error text shifts the layout, and the
        // mouseup lands off the button so the click (and submit) is lost.
        if (type === 'submit') e.preventDefault()
        onMouseDown?.(e)
      }}
      className={cls(variant, size, [loading && 'loading', className].filter(Boolean).join(' '))}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
    >
      {loading && <span className="spinner" aria-hidden="true" />}
      {loading && loadingText ? loadingText : children}
    </button>
  )
}

export interface LinkButtonProps extends LinkProps {
  variant?: ButtonVariant
  size?: ButtonSize
}

export function LinkButton({ variant = 'quiet', size = 'md', className, ...rest }: LinkButtonProps) {
  return <Link {...rest} className={cls(variant, size, className)} />
}
