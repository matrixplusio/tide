import { i18n } from '../../lib/i18n'
import { z } from 'zod'
import { addIssue, codePointLength, msg, validatePassword } from '../../lib/validation'

export const profileSchema = z.object({
  name: z.string().superRefine((v, ctx) => {
    const s = v.trim()
    if (s === '') return addIssue(ctx, [], i18n.t('profile.nameRequired'))
    if (codePointLength(s) > 64) addIssue(ctx, [], msg.tooLong(64))
  }),
})
export type ProfileValues = z.infer<typeof profileSchema>

export const changePasswordSchema = (username: string) =>
  z.object({ currentPassword: z.string(), newPassword: z.string(), confirmPassword: z.string() }).superRefine((v, ctx) => {
    addIssue(ctx, ['currentPassword'], v.currentPassword === '' ? i18n.t('profile.currentPasswordRequired') : null)
    const policy = validatePassword(v.newPassword, username)
    addIssue(ctx, ['newPassword'], policy ?? (v.currentPassword !== '' && v.newPassword === v.currentPassword ? msg.passwordSameAsCurrent() : null))
    addIssue(ctx, ['confirmPassword'], v.confirmPassword === '' ? msg.confirmRequired() : v.confirmPassword !== v.newPassword ? msg.passwordMismatch() : null)
  })
export type ChangePasswordValues = { currentPassword: string; newPassword: string; confirmPassword: string }
