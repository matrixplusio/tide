import { useTranslation } from 'react-i18next'
import type { ReactNode } from 'react'
import { Button } from './Button'
import { ButtonRow } from './Group'
import { FormErrorBanner } from './Banner'
import { Modal } from './Modal'

export interface ConfirmModalProps {
  title: ReactNode
  children?: ReactNode
  confirmLabel: string
  /** Destructive actions use the danger look. */
  danger?: boolean
  pending?: boolean
  error?: unknown
  onConfirm: () => void
  onClose: () => void
}

/** "Are you sure" for destructive or disruptive actions. Esc and the cancel
 * button close it; focus returns to the trigger. */
export function ConfirmModal({ title, children, confirmLabel, danger, pending, error, onConfirm, onClose }: ConfirmModalProps) {
  const { t } = useTranslation()
  return (
    <Modal title={title} onClose={onClose} closeOnEsc={!pending} width={460}>
      {children && <div className="note" style={{ paddingTop: 0 }}>{children}</div>}
      <FormErrorBanner error={error} />
      <ButtonRow style={{ marginTop: 16 }}>
        <span className="grow" />
        <Button variant="quiet" onClick={onClose} disabled={pending} data-autofocus>
          {t('common.cancel')}
        </Button>
        <Button variant={danger ? 'danger' : 'primary'} loading={pending} onClick={() => !pending && onConfirm()}>
          {confirmLabel}
        </Button>
      </ButtonRow>
    </Modal>
  )
}
