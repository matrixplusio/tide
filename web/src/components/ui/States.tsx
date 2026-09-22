import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, errorMessage } from '../../lib/api'
import { ErrCode } from '../../lib/errcode'
import { Button } from './Button'

export function Loading({ label }: { label?: string }) {
  const { t } = useTranslation()
  return (
    <div className="empty" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" /> {label ?? t('common.loading')}
    </div>
  )
}

export function EmptyState({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      {children}
      {action && <div style={{ marginTop: 8 }}>{action}</div>}
    </div>
  )
}

/** Request id is shown for internal errors so people can quote it. */
export function ErrorText({ error }: { error: unknown }) {
  const rid = error instanceof ApiError && (error.code === ErrCode.Internal || error.httpStatus >= 500) ? error.requestId : undefined
  return (
    <>
      {errorMessage(error)}
      {rid && <span className="rid"> · request id <span className="mono">{rid}</span></span>}
    </>
  )
}

export function ErrorState({ error, onRetry, inline }: { error: unknown; onRetry?: () => void; inline?: boolean }) {
  const { t } = useTranslation()
  if (!error) return null
  return (
    <div className={`err ${inline ? '' : 'err-block'}`} role="alert">
      <ErrorText error={error} />
      {onRetry && (
        <div style={{ marginTop: 6 }}>
          <Button size="small" variant="quiet" onClick={onRetry}>
            {t('common.retry')}
          </Button>
        </div>
      )}
    </div>
  )
}
