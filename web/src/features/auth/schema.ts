import { i18n } from '../../lib/i18n'
import { z } from 'zod'
import { addIssue, msg } from '../../lib/validation'

export const CAPTCHA_LENGTH = 5

/** captcha: whether the server currently requires a captcha for this sign-in. */
export function loginSchema(captcha: boolean) {
  return z.object({
    username: z.string().superRefine((v, ctx) => addIssue(ctx, [], v.trim() === '' ? msg.usernameRequired() : null)),
    password: z.string().superRefine((v, ctx) => addIssue(ctx, [], v === '' ? msg.passwordRequired() : null)),
    captchaCode: z.string().superRefine((v, ctx) => {
      if (!captcha) return
      const code = v.trim()
      addIssue(ctx, [], code === '' ? i18n.t('captcha.required') : code.length !== CAPTCHA_LENGTH ? i18n.t('captcha.length', { n: CAPTCHA_LENGTH }) : null)
    }),
  })
}

export type LoginValues = z.infer<ReturnType<typeof loginSchema>>
