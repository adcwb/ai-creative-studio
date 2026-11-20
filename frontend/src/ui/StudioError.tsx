import {Alert} from 'antd'
import type {ReactNode} from 'react'
import {ApiError, errorMessage} from '../api'
import {errorGuidance} from '../errorPresentation'
import {useI18n} from '../i18n'
import './atoms.css'

// One error surface. When it is handed the caught error it reads the backend's
// own category and suggestion instead of printing a raw code, and keeps both
// correlation ids available for support without shouting them at the reader.
export function StudioError({message, error, code, details, action, className}: {
  message?: string
  error?: unknown
  code?: string
  details?: ReactNode
  action?: ReactNode
  className?: string
}) {
  const {t} = useI18n()
  const api = error instanceof ApiError ? error : undefined
  const diagnostic = api?.diagnostic ?? {}
  const guidance = error !== undefined ? errorGuidance(diagnostic.category, api?.code ?? code) : undefined
  const text = message ?? (error !== undefined ? errorMessage(error, t('操作未能完成')) : '')
  const stableCode = code ?? api?.code
  const ids = [diagnostic.requestId, diagnostic.correlationId].filter(Boolean).join(' · ')
  const extra = guidance || stableCode || details || action || ids
  return <Alert
    className={`studio-error${className ? ` ${className}` : ''}`}
    type="error"
    role="alert"
    showIcon={false}
    title={guidance ? guidance.title : text}
    description={extra ? <div className="studio-error-detail">
      {guidance && <p className="studio-error-next">{guidance.next}</p>}
      {guidance && <p className="studio-error-message">{text}</p>}
      {diagnostic.suggestion && <p className="studio-error-next">{diagnostic.suggestion}</p>}
      {guidance?.mayBill && <p className="studio-error-billing">{t('上游可能已接收本次请求，重试前请先核对记录。')}</p>}
      {details}
      {stableCode && <code className="studio-error-code">{stableCode}</code>}
      {ids && <details className="studio-error-ids"><summary>{t('诊断编号')}</summary><code>{ids}</code></details>}
      {action && <div className="studio-error-action">{action}</div>}
    </div> : undefined}
  />
}