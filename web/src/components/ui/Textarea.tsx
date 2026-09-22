import type { Ref, TextareaHTMLAttributes } from 'react'

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  mono?: boolean
  ref?: Ref<HTMLTextAreaElement>
}

export function Textarea({ mono, className, ...rest }: TextareaProps) {
  return <textarea {...rest} className={['input', mono && 'mono', className].filter(Boolean).join(' ')} />
}
