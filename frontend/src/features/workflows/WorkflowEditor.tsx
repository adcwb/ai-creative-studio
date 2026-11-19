import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { NodeChange } from '@xyflow/react'
import { errorMessage } from '../../api'
import { useNavigationTask } from '../../app/navigationGuard'
import { useAppWorkspace } from '../../app/workspace'
import { useI18n } from '../../i18n'
import { hasPermission } from '../../permissions'
import { StudioDialog } from '../../ui/StudioDialog'
import { useMobile } from '../../ui/useMobile'
import { readSeriesRecovery, useSeriesWrite } from '../series/useSeriesWrite'
import { WorkflowCanvas, type WorkflowFlowNode } from './WorkflowCanvas'
import { WorkflowStepSettings } from './WorkflowStepSettings'
import { WorkflowRunHistory } from './WorkflowRunHistory'
import { WorkflowRevisionHistory } from './WorkflowRevisionHistory'
import { RunInputs, VariableDefinitions } from './WorkflowInputs'
import { isWorkflowVersionConflict, loadWorkflow, loadWorkflowAssets } from './requests'
import { canConnect, labels, portLabels, template } from './types'
import type { Asset, Detail, Graph, Kind, Run, Step, Summary, Wire } from './types'

export function WorkflowEditor({
  project,
  space,
  detail,
  projectVersion,
  saved,
}: {
  project: string
  space: string
  detail: Detail | null
  projectVersion: number
  saved: (v: Summary) => void
}) {
  const mobile = useMobile()
  const [mobileView, setMobileView] = useState<'list' | 'canvas'>('list')
  const [canvasExpanded, setCanvasExpanded] = useState(false)
  const { t } = useI18n()
  const workspace = useAppWorkspace()
  const path = `/projects/${project}/workflows${detail ? '/' + detail.id : ''}`
  const write = useSeriesWrite<Summary>(
    `workflow-save:${project}:${detail?.id ?? 'new'}`,
    path,
    detail ? 'PUT' : 'POST',
    saved,
    isWorkflowVersionConflict,
  )
  const pending = write.recovery as { name?: string; graph?: Graph } | null
  const [name, setName] = useState(pending?.name ?? detail?.name ?? '')
  const [graph, setGraph] = useState<Graph>(
    () => pending?.graph ?? detail?.revision.graph ?? template(false, t),
  )
  const [basisVersion, setBasisVersion] = useState(detail?.version ?? 0)
  const [latest, setLatest] = useState<Detail | null>(null)
  const [replaceConfirmed, setReplaceConfirmed] = useState(false)
  const [selected, setSelected] = useState(graph.nodes[0]?.id ?? '')
  const [kind, setKind] = useState<Kind>('image')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [past, setPast] = useState<Graph[]>([])
  const [future, setFuture] = useState<Graph[]>([])
  const dragStart = useRef<Graph | null>(null)
  const [assets, setAssets] = useState<Asset[]>([])
  const [runState, setRunState] = useState<Run | null>(null)
  const runStorage = `studio.workflow.run:${workspace.session.user?.id ?? 'demo'}:${space}:${project}:${detail?.id ?? 'new'}`
  const [runId, setRunId] = useState(() => {
    try {
      return sessionStorage.getItem(runStorage) ?? ''
    } catch {
      return ''
    }
  })
  const runIdentity = `workflow-run:${project}:${detail?.id ?? 'new'}`
  const runPath = path + '/runs'
  const [values, setValues] = useState<Record<string, string>>(() => {
    const pendingRun = readSeriesRecovery(
      workspace.session.user?.id ?? 'demo-user',
      space,
      runIdentity,
      runPath,
      'POST',
    )
    return (pendingRun?.inputs as Record<string, string> | undefined) ?? {}
  })
  const [inputsBaseline, setInputsBaseline] = useState(() => JSON.stringify(values))
  const runningSaved = useCallback(
    (r: Run) => {
      setInputsBaseline(JSON.stringify(r.inputs ?? {}))
      setValues(r.inputs ?? {})
      try {
        sessionStorage.setItem(runStorage, r.id)
      } catch {
        /* The original request remains recoverable. */
      }
      setRunId(r.id)
      setNotice(t('运行已保存，离开页面后仍会继续。'))
    },
    [t, runStorage],
  )
  const run = useSeriesWrite<Run>(
    runIdentity,
    runPath,
    'POST',
    runningSaved,
    isWorkflowVersionConflict,
  )
  const missingInput = (graph.variables ?? []).some(
    (v) => v.required && !(values[v.name] ?? v.default ?? '').trim(),
  )
  const [initialGraph] = useState(() => JSON.stringify(graph))
  const [view, setView] = useState(graph.viewport)
  const dirty = JSON.stringify(graph) !== initialGraph || name !== (detail?.name ?? '')
  useNavigationTask(
    JSON.stringify(values) !== inputsBaseline ||
      dirty ||
      write.busy ||
      write.uncertain ||
      run.busy ||
      run.uncertain,
  )
  const readOnly = !hasPermission(workspace.space, 'workspace.write')
  const locked = readOnly || write.busy || write.uncertain || run.busy || run.uncertain
  useEffect(() => {
    const abort = new AbortController()
    void loadWorkflowAssets(project, space, abort.signal)
      .then((v) => {
        if (!abort.signal.aborted) setAssets(v)
      })
      .catch((e) => {
        if (!abort.signal.aborted) setError(errorMessage(e, t('项目素材读取失败')))
      })
    return () => abort.abort()
  }, [project, space, t])
  function commit(next: Graph) {
    setPast((v) => [...v, structuredClone(graph)])
    setFuture([])
    setGraph(next)
    setError('')
    setNotice('')
  }
  function patch(next: Partial<Step>) {
    const before = graph.nodes.find((n) => n.id === selected)
    if (!before || JSON.stringify({ ...before, ...next }) === JSON.stringify(before)) return
    commit({ ...graph, nodes: graph.nodes.map((n) => (n.id === selected ? { ...n, ...next } : n)) })
  }
  function connect(edge: Wire) {
    if (!canConnect(graph, edge)) {
      setError(t('无法连接：请检查输入类型、重复连线和循环。'))
      return
    }
    commit({ ...graph, edges: [...graph.edges, edge] })
  }
  const nodes = useMemo<WorkflowFlowNode[]>(
    () =>
      graph.nodes.map((n) => ({
        id: n.id,
        type: 'creative',
        position: { x: n.x, y: n.y },
        data: {
          step: n,
          status:
            !dirty && runState?.revision_id === detail?.head_revision_id
              ? runState?.nodes[n.id]?.status
              : undefined,
        },
        selected: n.id === selected,
      })),
    [graph.nodes, selected, dirty, runState, detail?.head_revision_id],
  )
  function removeNodes(ids: ReadonlySet<string>) {
    commit({
      ...graph,
      nodes: graph.nodes.filter((node) => !ids.has(node.id)),
      edges: graph.edges.filter((wire) => !ids.has(wire.source) && !ids.has(wire.target)),
    })
  }

  function changeCanvasNodes(changes: NodeChange[]) {
    if (locked) return
    const removed = new Set(
      changes.filter((change) => change.type === 'remove').map((change) => change.id),
    )
    if (removed.size > 0) {
      removeNodes(removed)
      return
    }
    const positions = new Map<string, { x: number; y: number }>()
    for (const change of changes) {
      if (change.type === 'position' && change.position) positions.set(change.id, change.position)
    }
    if (positions.size > 0) {
      setGraph((current) => ({
        ...current,
        nodes: current.nodes.map((node) => {
          const position = positions.get(node.id)
          return position ? { ...node, ...position } : node
        }),
      }))
    }
  }

  const incomplete = graph.nodes.filter(needsSettings).length
  const step = graph.nodes.find((n) => n.id === selected)

  const canvas = (
    <WorkflowCanvas
      nodes={nodes}
      wires={graph.edges}
      locked={locked}
      viewport={view ?? graph.viewport ?? { x: 20, y: 20, zoom: 1 }}
      fitView={!view && !graph.viewport && !mobile}
      onNodesChange={changeCanvasNodes}
      onSelect={setSelected}
      onDragStart={() => {
        dragStart.current = structuredClone(graph)
      }}
      onDragEnd={() => {
        const before = dragStart.current
        if (before) {
          setPast((history) => [...history, before])
          setFuture([])
          dragStart.current = null
        }
      }}
      onConnect={connect}
      onViewportChange={setView}
    />
  )

  return (
    <>
      <section className="panel workflow-editor">
        <div className="workflow-toolbar workflow-save-toolbar">
          <label>
            {t('流程名称')}
            <input
              maxLength={80}
              value={name}
              disabled={locked}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <button
            className="primary"
            disabled={locked || !name.trim() || !graph.nodes.length}
            onClick={() =>
              void write.save({
                name,
                expected_version: basisVersion,
                graph: { ...graph, ...(view ? { viewport: view } : {}) },
              })
            }
          >
            {t('保存流程修订')}
          </button>
          {detail && (
            <button
              disabled={locked || dirty || incomplete > 0 || missingInput}
              onClick={() =>
                void run.save({
                  revision_id: detail.head_revision_id,
                  expected_project_version: projectVersion,
                  inputs: values,
                })
              }
            >
              {t('运行已保存流程')}
            </button>
          )}
        </div>
        {detail && (
          <p>
            {t('当前编辑起点：保存版本 {version}', { version: basisVersion })}{' '}
            <button
              disabled={locked}
              onClick={() => {
                void loadWorkflow(project, space, detail.id)
                  .then((v) => {
                    setLatest(v)
                    setReplaceConfirmed(false)
                  })
                  .catch((e) => setError(errorMessage(e, t('流程读取失败'))))
              }}
            >
              {t('核对最新保存版本')}
            </button>
          </p>
        )}
        {latest && (
          <div className="editing-note">
            <p>
              {t('最新保存版本 {version}，当前草稿仍保留。请比较后明确选择是否基于它继续保存。', {
                version: latest.version,
              })}
            </p>
            <label>
              <input
                type="checkbox"
                checked={replaceConfirmed}
                disabled={locked}
                onChange={(e) => setReplaceConfirmed(e.target.checked)}
              />
              {t('已比较，保留当前草稿作为新修订')}
            </label>
            <button
              disabled={locked || !replaceConfirmed}
              onClick={() => {
                setBasisVersion(latest.version)
                setLatest(null)
                setNotice(t('保存起点已更新，当前草稿未替换。'))
              }}
            >
              {t('使用核对后的保存起点')}
            </button>
          </div>
        )}
        {incomplete > 0 && (
          <p className="drama-help">
            {t('还有 {count} 个步骤需要补充描述、素材或模型，保存后再运行。', {
              count: incomplete,
            })}
          </p>
        )}
        {dirty && (
          <p className="editing-note">
            {t('当前有未保存草稿。请先保存，再运行；旧运行不会随草稿变化。')}
          </p>
        )}
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        {(write.error || run.error) && (
          <p role="alert" className="error">
            {write.error || run.error}
          </p>
        )}
        {(write.storageNotice || run.storageNotice) && (
          <p role="status">{write.storageNotice || run.storageNotice}</p>
        )}
        {notice && <p role="status">{notice}</p>}
        {write.uncertain && (
          <button disabled={write.busy || readOnly} onClick={() => void write.save({})}>
            {t('核对原流程保存请求')}
          </button>
        )}
        {run.uncertain && (
          <button disabled={run.busy || readOnly} onClick={() => void run.save({})}>
            {t('核对原流程运行请求')}
          </button>
        )}
        <RunInputs
          items={graph.variables ?? []}
          values={values}
          change={setValues}
          disabled={locked}
        />
        <div className="workflow-toolbar">
          <label>
            {t('步骤类型')}
            <select
              disabled={locked}
              value={kind}
              onChange={(e) => setKind(e.target.value as Kind)}
            >
              {Object.entries(labels).map(([k, v]) => (
                <option key={k} value={k}>
                  {t(v)}
                </option>
              ))}
            </select>
          </label>
          <button
            disabled={locked}
            onClick={() => {
              const n: Step = {
                id: crypto.randomUUID(),
                kind,
                label: t(labels[kind]),
                x: 40 + graph.nodes.length * 180,
                y: 220,
                asset_kind: kind === 'asset' ? 'image' : undefined,
              }
              commit({ ...graph, nodes: [...graph.nodes, n] })
              setSelected(n.id)
            }}
          >
            {t('添加步骤')}
          </button>
          <button
            disabled={locked || !past.length}
            onClick={() => {
              setFuture((v) => [structuredClone(graph), ...v])
              setGraph(past[past.length - 1])
              setPast((v) => v.slice(0, -1))
            }}
          >
            {t('撤销编辑')}
          </button>
          <button
            disabled={locked || !future.length}
            onClick={() => {
              setPast((v) => [...v, structuredClone(graph)])
              setGraph(future[0])
              setFuture((v) => v.slice(1))
            }}
          >
            {t('重做编辑')}
          </button>
        </div>
        {!detail && (
          <div className="media-actions">
            <button disabled={locked} onClick={() => commit(template(false, t))}>
              {t('图像出稿模板')}
            </button>
            <button disabled={locked} onClick={() => commit(template(true, t))}>
              {t('已有素材审片模板')}
            </button>
          </div>
        )}
        {mobile && (
          <div className="workflow-view-switch" role="group" aria-label={t('工作流显示方式')}>
            <button aria-pressed={mobileView === 'list'} onClick={() => setMobileView('list')}>
              {t('步骤列表')}
            </button>
            <button aria-pressed={mobileView === 'canvas'} onClick={() => setMobileView('canvas')}>
              {t('画布')}
            </button>
          </div>
        )}
        <div className="workflow-layout">
          <div>
            {(!mobile || mobileView === 'canvas') && !canvasExpanded && canvas}
            <button
              className="workflow-expand"
              type="button"
              onClick={() => setCanvasExpanded(true)}
            >
              {t('展开画布')}
            </button>
            <details className="workflow-step-list" open={mobile && mobileView === 'list'}>
              <summary>{t('按列表选择步骤，支持键盘与手机操作')}</summary>
              {graph.nodes.map((n) => (
                <button
                  key={n.id}
                  aria-pressed={selected === n.id}
                  onClick={() => setSelected(n.id)}
                >
                  {n.label || t(labels[n.kind])}
                </button>
              ))}
            </details>
            <details>
              <summary>{t('检查与移除连线')}</summary>
              {graph.edges.map((e) => (
                <p key={`${e.source}:${e.target}:${e.input}`}>
                  {graph.nodes.find((n) => n.id === e.source)?.label} →{' '}
                  {graph.nodes.find((n) => n.id === e.target)?.label} · {t(portLabels[e.input])}
                  <button
                    disabled={locked}
                    onClick={() => commit({ ...graph, edges: graph.edges.filter((v) => v !== e) })}
                  >
                    {t('移除连线')}
                  </button>
                </p>
              ))}
            </details>
          </div>
          <aside className="workflow-properties" aria-label={t('步骤设置')}>
            {step ? (
              <WorkflowStepSettings
                key={step.id}
                step={step}
                graph={graph}
                assets={assets}
                space={space}
                project={project}
                projectVersion={projectVersion}
                disabled={locked}
                patch={patch}
                connect={connect}
                remove={() => {
                  removeNodes(new Set([step.id]))
                  setSelected('')
                }}
              />
            ) : (
              <p>{t('选择一个步骤，查看输入和设置。')}</p>
            )}
          </aside>
        </div>
        <VariableDefinitions
          items={graph.variables ?? []}
          disabled={locked}
          change={(variables) => commit({ ...graph, variables })}
        />{' '}
        {canvasExpanded && (
          <StudioDialog
            title={t('创作流程画布')}
            onClose={() => setCanvasExpanded(false)}
            width={1600}
            className="workflow-expanded-dialog"
          >
            <div className="studio-dialog-body">{canvas}</div>
          </StudioDialog>
        )}
        {detail && (
          <WorkflowRevisionHistory
            project={project}
            space={space}
            detail={detail}
            disabled={locked}
            load={(v) => {
              workspace.requestLeave(() => {
                commit(v.graph)
                setNotice(t('历史版本已载入草稿，保存后形成新修订，原记录保留。'))
              })
            }}
          />
        )}
      </section>
      {detail && (
        <WorkflowRunHistory
          key={runId}
          project={project}
          space={space}
          workflow={detail.id}
          selected={runId}
          choose={(id) => {
            try {
              sessionStorage.setItem(runStorage, id)
            } catch {
              /* In-page run remains visible. */
            }
            setRunId(id)
          }}
          observe={setRunState}
        />
      )}
    </>
  )
}

function needsSettings(node: Step): boolean {
  switch (node.kind) {
    case 'text':
    case 'template':
      return !node.text?.trim()
    case 'llm':
      return !node.text?.trim() || !node.model_config_id
    case 'variable':
      return !node.variable_name
    case 'asset':
      return !node.asset_id
    case 'image':
    case 'video':
    case 'speech':
      return !node.model_config_id || !node.model_config_version
    default:
      return false
  }
}
