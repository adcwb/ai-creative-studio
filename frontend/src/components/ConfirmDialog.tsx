import { useI18n } from '../i18n'
import {Alert, Button} from 'antd'
import {StudioDialog} from '../ui/StudioDialog'

export function ConfirmDialog({title, message, confirmLabel, onConfirm, onCancel, cancelLabel, busy = false, error}: {
  title: string; message: string; confirmLabel: string; onConfirm: () => void; onCancel: () => void; cancelLabel?: string; busy?: boolean; error?: string
}) {
  const {t} = useI18n()
  return <StudioDialog title={title} onClose={onCancel} busy={busy} width={480} className="studio-confirm" initialFocus="cancel">
    <div className="studio-dialog-body"><p>{message}</p>{error && <Alert type="error" showIcon title={error} role="alert" />}</div>
    <div className="studio-dialog-actions"><Button data-dialog-cancel disabled={busy} onClick={onCancel}>{cancelLabel ?? t('继续编辑')}</Button><Button type="primary" loading={busy} disabled={busy} onClick={onConfirm}>{confirmLabel}</Button></div>
  </StudioDialog>
}
