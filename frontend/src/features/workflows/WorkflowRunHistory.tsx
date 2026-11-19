import { useCallback, useState } from 'react'
import { Link } from 'react-router-dom'
import { baseURL, errorMessage, request } from '../../api'
import { useAppWorkspace } from '../../app/workspace'
import { usePolling } from '../../hooks/usePolling'
import { useI18n } from '../../i18n'
import { hasPermission } from '../../permissions'
import { useDirectory } from '../series/useDirectory'
import { useSeriesWrite } from '../series/useSeriesWrite'
import { WorkflowRerun } from './WorkflowRerun'
import { isWorkflowVersionConflict, loadWorkflowAssets } from './requests'
import { labels, states } from './types'
import type { Asset, Run } from './types'

export function WorkflowRunHistory({
  project,
  space,
  workflow,
  selected,
  choose,
  observe,
}: {
  project: string
  space: string
  workflow: string
  selected: string
  choose: (id: string) => void
  observe: (value: Run) => void
}) {
  const { t } = useI18n()
  const [refresh, setRefresh] = useState(0)
  const list = useDirectory<{ id: string; workflow_id: string; status: string }>(
    `/projects/${project}/workflow-runs`,
    space,
    refresh,
  )
  return (
    <section className="panel">
      <h3>{t('流程运行记录')}</h3>
      <button onClick={() => setRefresh((v) => v + 1)}>{t('刷新运行记录')}</button>
      {list.error && <p role="alert">{list.error}</p>}
      <div className="media-actions">
        {list.items
          .filter((v) => v.workflow_id === workflow)
          .map((v, i) => (
            <button key={v.id} aria-pressed={selected === v.id} onClick={() => choose(v.id)}>
              {t('运行')} {i + 1} · {t(states[v.status] ?? v.status)}
            </button>
          ))}
      </div>
      {list.next_cursor && (
        <button onClick={() => void list.loadMore()}>{t('加载更多运行')}</button>
      )}
      {selected && (
        <RunView
          key={selected}
          project={project}
          space={space}
          id={selected}
          observe={observe}
          rerun={(r) => {
            choose(r.id)
            setRefresh((v) => v + 1)
          }}
        />
      )}
    </section>
  )
}
function RunView({
  project,
  space,
  id,
  observe,
  rerun,
}: {
  project: string
  space: string
  id: string
  observe: (value: Run) => void
  rerun: (value: Run) => void
}) {
  const { t } = useI18n()
  const [run, setRun] = useState<Run | null>(null)
  const [error, setError] = useState('')
  const [assets, setAssets] = useState<Asset[]>([])
  const load = useCallback(
    async (signal: AbortSignal) => {
      const value = await request<Run>(
        `/projects/${project}/workflow-runs/${id}?space_id=${space}`,
        { signal },
      )
      if (!signal.aborted) {
        setRun(value)
        observe(value)
        setError('')
      }
      const media = await loadWorkflowAssets(project, space, signal)
      if (!signal.aborted) setAssets(media)
    },
    [project, space, id, observe],
  )
  usePolling(
    async (signal) => {
      try {
        await load(signal)
      } catch (e) {
        if (!signal.aborted) setError(errorMessage(e, t('流程运行读取失败')))
        throw e
      }
    },
    2000,
    `workflow:${project}:${space}:${id}`,
  )
  return (
    <div className="workflow-run" aria-label={t('运行详情')}>
      {error && <p role="alert">{error}</p>}
      {run && (
        <>
          <p role="status">
            {t(states[run.status] ?? run.status)} · {t('运行版本')} {run.version}
          </p>
          {Object.values(run.nodes).some(
            (n) => n.status === 'failed' || n.status === 'unknown',
          ) && (
            <p className="editing-note">
              {t('有步骤未完成，后续生成已暂停；已开始的任务会继续核对结果。')}
            </p>
          )}
          {run.inputs && Object.keys(run.inputs).length > 0 && (
            <details>
              <summary>{t('本次固定输入')}</summary>
              <dl>
                {Object.entries(run.inputs).map(([name, value]) => (
                  <div key={name}>
                    <dt>{run.graph.variables?.find((v) => v.name === name)?.label || name}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
            </details>
          )}
          <p>
            {t(
              '运行固定保存时的流程版本；图中的每一步使用既有创作任务，结果未知时不会自动重新生成。',
            )}
          </p>
          {run.graph.nodes.map((step) => {
            const n = run.nodes[step.id]
            const asset = assets.find((a) => a.id === n.result.asset_id)
            return (
              <article className="editing-note" key={step.id}>
                <h4>
                  {step.label || t(labels[step.kind])} · {t(states[n.status] ?? n.status)}
                </h4>
                {n.error_code && (
                  <p className="error">
                    {t('步骤未完成（{code}），请核对原任务与当前权限。', { code: n.error_code })}
                  </p>
                )}
                {asset && (
                  <>
                    {asset.kind === 'image' ? (
                      <img
                        className="workflow-result"
                        alt={asset.name}
                        src={baseURL + asset.file_url}
                      />
                    ) : asset.kind === 'video' ? (
                      <video
                        className="workflow-result"
                        controls
                        preload="metadata"
                        src={baseURL + asset.file_url}
                      />
                    ) : (
                      <audio controls preload="metadata" src={baseURL + asset.file_url} />
                    )}
                    <p>{asset.name}</p>
                  </>
                )}
                {n.result.text && <p>{n.result.text}</p>}
                {n.reused_from_run_id && (
                  <p className="drama-help">{t('沿用已完成步骤，未再次调用模型')}</p>
                )}
                <WorkflowRerun project={project} run={run} node={step.id} saved={rerun} />
                {n.status === 'waiting_review' && (
                  <ReviewDecision
                    project={project}
                    space={space}
                    run={run}
                    node={step.id}
                    saved={setRun}
                  />
                )}
              </article>
            )
          })}
          <RunAction project={project} run={run} saved={setRun} />
          <Link className="button-link" to={`/projects/${project}/media`}>
            {t('在剪辑工作区使用素材')}
          </Link>
        </>
      )}
    </div>
  )
}
function ReviewDecision({
  project,
  space,
  run,
  node,
  saved,
}: {
  project: string
  space: string
  run: Run
  node: string
  saved: (v: Run) => void
}) {
  const { t } = useI18n()
  const workspace = useAppWorkspace()
  const [confirmed, setConfirmed] = useState(false)
  const write = useSeriesWrite<Run>(
    `workflow-review:${run.id}:${node}`,
    `/projects/${project}/workflow-runs/${run.id}/nodes/${node}/decision`,
    'POST',
    saved,
    isWorkflowVersionConflict,
  )
  return (
    <div data-space={space}>
      <label>
        <input
          type="checkbox"
          checked={confirmed}
          disabled={
            write.busy || write.uncertain || !hasPermission(workspace.space, 'workspace.write')
          }
          onChange={(e) => setConfirmed(e.target.checked)}
        />
        {t('我已检查此素材，继续后续步骤')}
      </label>
      <button
        disabled={
          write.busy ||
          !hasPermission(workspace.space, 'workspace.write') ||
          (!confirmed && !write.uncertain)
        }
        onClick={() => void write.save({ expected_version: run.version, approved: true })}
      >
        {t(write.uncertain ? '核对原审片决定' : '确认采用并继续')}
      </button>
      {write.error && <p role="alert">{write.error}</p>}
      {write.storageNotice && <p role="status">{write.storageNotice}</p>}
    </div>
  )
}
function RunAction({
  project,
  run,
  saved,
}: {
  project: string
  run: Run
  saved: (v: Run) => void
}) {
  const { t } = useI18n()
  const workspace = useAppWorkspace()
  const action = run.status === 'unknown' ? 'reconcile' : 'cancel'
  const write = useSeriesWrite<Run>(
    `workflow-action:${run.id}:${action}`,
    `/projects/${project}/workflow-runs/${run.id}/${action}`,
    'POST',
    saved,
    isWorkflowVersionConflict,
  )
  if (run.status === 'succeeded' || run.status === 'failed' || run.status === 'cancelled')
    return null
  return (
    <div>
      <p>
        {t(
          '停止只阻止后续派发，不能保证上游已开始的计算被撤回。核对沿用原任务，不创建新的模型请求。',
        )}
      </p>
      <button
        disabled={write.busy || !hasPermission(workspace.space, 'workspace.write')}
        onClick={() => void write.save({ expected_version: run.version })}
      >
        {t(
          write.uncertain
            ? '核对原运行操作'
            : action === 'reconcile'
              ? '核对原任务结果'
              : '停止后续步骤',
        )}
      </button>
      {write.error && <p role="alert">{write.error}</p>}
    </div>
  )
}
