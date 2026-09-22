import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { InputHTMLAttributes, Ref } from 'react'

export interface PasswordInputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> {
  autoComplete: 'new-password' | 'current-password' | 'off'
  ref?: Ref<HTMLInputElement>
}

export function PasswordInput({ className, autoComplete, ...rest }: PasswordInputProps) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(false)
  return (
    <span className="pw">
      <input
        {...rest}
        type={visible ? 'text' : 'password'}
        autoComplete={autoComplete}
        autoCapitalize="off"
        autoCorrect="off"
        spellCheck={false}
        className={['input', className].filter(Boolean).join(' ')}
      />
      <button type="button" className="pw-toggle" aria-label={visible ? t('common.hidePassword') : t('common.showPassword')} aria-pressed={visible} onClick={() => setVisible((v) => !v)}>
        {visible ? t('common.hide') : t('common.show')}
      </button>
    </span>
  )
}
