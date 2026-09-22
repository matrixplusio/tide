import { i18n } from '../../lib/i18n'
import { z } from 'zod'
import { addIssue, refineNewPassword, validateUsername, zOptionalText } from '../../lib/validation'

export const tokenSchema = z.object({
  token: z.string().superRefine((v, ctx) => addIssue(ctx, [], v.trim() === '' ? i18n.t('setup.tokenRequired') : null)),
})
export type TokenValues = z.infer<typeof tokenSchema>

export const adminSchema = z
  .object({
    username: z.string(),
    name: zOptionalText(64),
    password: z.string(),
    confirmPassword: z.string(),
  })
  .superRefine((v, ctx) => {
    addIssue(ctx, ['username'], validateUsername(v.username))
    refineNewPassword(ctx, { password: v.password, confirm: v.confirmPassword, username: v.username }, { password: 'password', confirm: 'confirmPassword' })
  })
export type AdminValues = z.infer<typeof adminSchema>
