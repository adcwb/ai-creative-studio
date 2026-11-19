import { useCallback, useEffect, useState } from 'react'
import { errorMessage } from '../../api'
import { useAppWorkspace } from '../../app/workspace'
import { translate, useI18n } from '../../i18n'
import { useDirectory } from '../series/useDirectory'
import { WorkflowEditor } from './WorkflowEditor'
import { loadWorkflow } from './requests'
import type { Detail, Summary } from './types'
import './workflow.css'

export default function WorkflowStudio({
  project,
  space,
  projectVersion,
}: {
  project: string
  space: string
  projectVersion: number
}) {
  const { t } = useI18n()
  const workspace = useAppWorkspace()
  const storage = `studio.workflow.selected:${workspace.session.user?.id ?? 'demo'}:${space}:${project}`
  const [selected, setSelected] = useState(() => {
    try {
      return sessionStorage.getItem(storage) ?? ''
    } catch {
      return ''
    }
  })
  const [revision, setRevision] = useState(0)
  const list = useDirectory<Summary>(`/projects/${project}/workflows`, space, revision)
  function choose(id: string) {
    workspace.requestLeave(() => {
      try {
        sessionStorage.setItem(storage, id)
      } catch {
        /* Pending writes show their own storage warning. */
      }
      setSelected(id)
    })
  }
  const saved = useCallback(
    (value: Summary) => {
      try {
        sessionStorage.setItem(storage, value.id)
      } catch {
        /* In-page recovery remains available. */
      }
      setSelected(value.id)
      setRevision((v) => v + 1)
    },
    [storage],
  )
  return (
    <section className="workflow-studio">
      <header className="series-heading">
        <div>
          <h2>{t('创作流程')}</h2>
          <p>{t('把描述、模型生成和人工选择连起来。保存流程不调用模型，运行时使用已保存版本。')}</p>
        </div>
        <button onClick={() => choose('')}>{t('新建流程')}</button>
      </header>
      <div className="workflow-directory">
        <label>
          {t('已保存流程')}
          <select value={selected} onChange={(e) => choose(e.target.value)}>
            <option value="">{t('新流程草稿')}</option>
            {list.items.map((v) => (
              <option key={v.id} value={v.id}>
                {v.name} · {t('版本')} {v.version}
              </option>
            ))}
            {selected && !list.items.some((v) => v.id === selected) && (
              <option value={selected}>{t('正在读取所选流程…')}</option>
            )}
          </select>
        </label>
        {list.next_cursor && (
          <button disabled={list.more} onClick={() => void list.loadMore()}>
            {t('加载更多流程')}
          </button>
        )}
        {list.error && <p role="alert">{list.error}</p>}
      </div>
      <EditorLoader
        key={`${space}:${project}:${selected}:${revision}`}
        project={project}
        space={space}
        id={selected}
        projectVersion={projectVersion}
        saved={saved}
      />
    </section>
  )
}
function EditorLoader({
  project,
  space,
  id,
  projectVersion,
  saved,
}: {
  project: string
  space: string
  id: string
  projectVersion: number
  saved: (v: Summary) => void
}) {
  const { t } = useI18n()
  const [detail, setDetail] = useState<Detail | null>(null)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  useEffect(() => {
    if (!id) return
    const abort = new AbortController()
    void loadWorkflow(project, space, id, abort.signal)
      .then((v) => {
        if (!abort.signal.aborted) setDetail(v)
      })
      .catch((e) => {
        if (!abort.signal.aborted) setError(errorMessage(e, translate('流程读取失败')))
      })
    return () => abort.abort()
  }, [project, space, id, refresh])
  if (id && !detail)
    return (
      <div className="panel">
        {error ? (
          <p role="alert">
            {error}
            <button onClick={() => setRefresh((v) => v + 1)}>{t('重新读取')}</button>
          </p>
        ) : (
          <p role="status">{t('正在读取流程…')}</p>
        )}
      </div>
    )
  return (
    <WorkflowEditor
      project={project}
      space={space}
      detail={detail}
      projectVersion={projectVersion}
      saved={saved}
    />
  )
}
