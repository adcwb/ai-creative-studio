import { useState } from 'react'
import { errorMessage, request } from '../../api'
import { useI18n } from '../../i18n'
import { useDirectory } from '../series/useDirectory'
import type { Detail, Revision } from './types'

export function WorkflowRevisionHistory({
  project,
  space,
  detail,
  disabled,
  load,
}: {
  project: string
  space: string
  detail: Detail
  disabled: boolean
  load: (v: Revision) => void
}) {
  const { t } = useI18n()
  const list = useDirectory<{ id: string; number: number }>(
    `/projects/${project}/workflows/${detail.id}/revisions`,
    space,
    detail.version,
  )
  const [error, setError] = useState('')
  return (
    <details>
      <summary>{t('流程修订历史')}</summary>
      {list.items.map((v) => (
        <p key={v.id}>
          {t('版本')} {v.number}{' '}
          <button
            disabled={disabled}
            onClick={() => {
              void request<Revision>(
                `/projects/${project}/workflows/${detail.id}/revisions/${v.id}?space_id=${space}`,
              )
                .then(load)
                .catch((e) => setError(errorMessage(e, t('历史版本读取失败'))))
            }}
          >
            {t('作为草稿打开')}
          </button>
        </p>
      ))}
      {list.next_cursor && (
        <button onClick={() => void list.loadMore()}>{t('加载更早修订')}</button>
      )}
      {(list.error || error) && <p role="alert">{list.error || error}</p>}
    </details>
  )
}
