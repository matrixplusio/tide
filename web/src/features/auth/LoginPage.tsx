import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { isApiError } from '../../lib/api'
import { ErrCode } from '../../lib/errcode'
import { applyServerError } from '../../lib/forms'
import { safeReturnPath } from '../../lib/format'
import { Banner, Button, ButtonRow, Form, FormErrorBanner, FormField, Group, Input, Note, PasswordInput } from '../../components/ui'
import { CAPTCHA_LENGTH, loginSchema, type LoginValues } from './schema'
import { fetchLoginChallenge, ssoLoginHref, useAuthMethods, useLogin } from './queries'
import './captcha.css'

interface Captcha {
  id: string
  image: string
}

export function LoginPage() {
  const { t } = useTranslation()
  const loc = useLocation()
  const navigate = useNavigate()
  const params = new URLSearchParams(loc.search)
  const onLoginPath = loc.pathname === '/login'
  const ret = safeReturnPath(onLoginPath ? params.get('return') : loc.pathname + loc.search)
  // SSO failures come back as /login?error=<msg>; shown as plain text only.
  const ssoError = params.get('error')
  const methods = useAuthMethods()
  const login = useLogin()
  const [formError, setFormError] = useState<unknown>(null)
  const [captcha, setCaptcha] = useState<Captcha | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const sso = methods.data?.sso === true

  const form = useForm<LoginValues>({
    // react-hook-form re-reads options every render, so the schema follows the captcha state.
    resolver: zodResolver(loginSchema(captcha !== null)),
    mode: 'onTouched',
    defaultValues: { username: '', password: '', captchaCode: '' },
  })
  const { register, handleSubmit, formState, setValue, setError, setFocus, getValues } = form

  /** Re-asks the server whether a captcha is needed; returns true when one is shown. */
  const refreshCaptcha = async (): Promise<boolean> => {
    setRefreshing(true)
    try {
      const ch = await fetchLoginChallenge(getValues('username').trim())
      setValue('captchaCode', '')
      if (ch.captchaRequired && ch.captchaId && ch.captchaImage) {
        setCaptcha({ id: ch.captchaId, image: ch.captchaImage })
        return true
      }
      setCaptcha(null)
      return false
    } catch (e) {
      setFormError(e)
      return captcha !== null
    } finally {
      setRefreshing(false)
    }
  }

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      await login.mutateAsync({
        username: v.username.trim(),
        password: v.password,
        ...(captcha ? { captchaId: captcha.id, captchaCode: v.captchaCode.trim() } : {}),
      })
      if (onLoginPath) navigate(ret, { replace: true })
    } catch (e) {
      if (!isApiError(e)) {
        setFormError(e)
        return
      }
      switch (e.code) {
        case ErrCode.InvalidCredentials: {
          // A captcha is single-use and the failure may have crossed the threshold.
          await refreshCaptcha()
          setFormError(e)
          setValue('password', '')
          setFocus('password')
          break
        }
        case ErrCode.CaptchaRequired:
        case ErrCode.CaptchaInvalid: {
          const shown = await refreshCaptcha()
          if (shown) {
            setError('captchaCode', { type: 'server', message: e.msg }, { shouldFocus: true })
          } else {
            setFormError(e)
          }
          break
        }
        default:
          setFormError(applyServerError(form, e))
      }
    }
  }, () => setFormError(null))

  return (
    <div className="login">
      <aside className="login-brand" aria-hidden="true">
        <div>
          <p className="login-mark">
            Tide<span className="accent-text">.</span>
          </p>
          <p className="login-tag">{t('login.tagline')}</p>
        </div>
        <Pipeline />
      </aside>

      <main className="login-form">
        <div>
          {/* Shown instead of the brand pane once the split collapses. */}
          <p className="login-mark login-head">
            Tide<span className="accent-text">.</span>
          </p>
          <p className="muted login-head">{t('login.tagline')}</p>
          <h1 className="login-h">{t('login.title')}</h1>

        {ssoError && <Banner tone="bad">{t('login.ssoFailed', { msg: ssoError })}</Banner>}

        {sso && (
          <>
            <a className="btn block" href={ssoLoginHref(ret)}>
              {t('login.sso')}
            </a>
            <Note style={{ textAlign: 'center', padding: '14px 0 8px' }}>{t('login.orLocal')}</Note>
          </>
        )}

        <Form onSubmit={onSubmit} aria-label={t('login.formLabel')}>
          <Group form>
            <FormField label={t('login.username')} error={formState.errors.username?.message} required>
              {(p) => (
                <Input {...p} {...register('username')} mono placeholder={t('login.usernameHint')} autoComplete="username" autoCapitalize="off" spellCheck={false} autoFocus={!sso} />
              )}
            </FormField>
            <FormField label={t('login.password')} error={formState.errors.password?.message} required>
              {(p) => <PasswordInput {...p} {...register('password')} placeholder={t('login.passwordHint')} autoComplete="current-password" />}
            </FormField>
            {captcha && (
              <FormField label={t('login.captcha')} error={formState.errors.captchaCode?.message} required hint={t('login.captchaHint')}>
                {(p) => (
                  <div className="captcha">
                    <Input
                      {...p}
                      {...register('captchaCode')}
                      mono
                      inputMode="text"
                      autoComplete="off"
                      autoCapitalize="off"
                      autoCorrect="off"
                      spellCheck={false}
                      maxLength={CAPTCHA_LENGTH + 2}
                    />
                    <button
                      type="button"
                      className="captcha-img"
                      onClick={() => void refreshCaptcha()}
                      aria-label={t('login.captchaRefresh')}
                      aria-busy={refreshing || undefined}
                      disabled={refreshing}
                    >
                      <img src={captcha.image} alt={t('login.captchaImage')} width={120} height={38} />
                    </button>
                  </div>
                )}
              </FormField>
            )}
          </Group>
          {captcha && !formError && <Note>{t('login.captchaNote')}</Note>}
          <FormErrorBanner error={formError} />
          <ButtonRow>
            <Button type="submit" variant={sso ? 'quiet' : 'primary'} className="block" loading={formState.isSubmitting} loadingText={t('login.submitting')}>
              {t('login.submit')}
            </Button>
          </ButtonRow>
        </Form>
        </div>
      </main>
    </div>
  )
}

/** The brand pane's one picture: the same artifact moving qa → uat → prod, drawn
 *  the way the service overview draws it. Illustrative, not live data — the
 *  sign-in page is unauthenticated and shows nothing real. */
function Pipeline() {
  const envs = [
    { env: 'qa', tag: '0918-46b7', dot: 'ok', same: false },
    { env: 'uat', tag: '0918-46b7', dot: 'ok', same: true },
    { env: 'prod', tag: '0917-a3f2', dot: 'run', same: false },
  ]
  return (
    <div className="pipe">
      {envs.map((e) => (
        <div key={e.env} className={e.same ? 'pipe-env same' : 'pipe-env'}>
          <div className="pipe-k">{e.env}</div>
          <div className="pipe-v">
            <span className={`dot ${e.dot}`} />
            <span className="mono ellipsis">{e.tag}</span>
          </div>
        </div>
      ))}
    </div>
  )
}
