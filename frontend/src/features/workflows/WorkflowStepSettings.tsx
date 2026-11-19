import { useState } from 'react'
import { useAppWorkspace } from '../../app/workspace'
import { RuntimeModelPicker } from '../../components/RuntimeModelPicker'
import { previewFeature } from '../../gatewayModels'
import { useRuntimeCatalog } from '../../hooks/useRuntimeCatalog'
import { useI18n } from '../../i18n'
import { initialRuntimeChoice, type RuntimePreview } from '../../runtimeSelection'
import { labels, portLabels, ports } from './types'
import type { Asset, Graph, Step, Wire } from './types'

function ModelSettings({
  step,
  graph,
  space,
  project,
  projectVersion,
  disabled,
  patch,
}: {
  step: Step
  graph: Graph
  space: string
  project: string
  projectVersion: number
  disabled: boolean
  patch: (v: Partial<Step>) => void
}) {
  const workspace = useAppWorkspace()
  const reference = graph.edges.some((e) => e.target === step.id && e.input === 'reference')
  const preview: RuntimePreview = {
    operation: 'creative',
    project_id: project,
    input: {
      kind: step.kind === 'llm' ? 'text' : step.kind,
      expected_version: projectVersion,
      ...(reference ? { reference_asset_id: 'workflow-reference' } : {}),
    },
  }
  const { catalog } = useRuntimeCatalog(
    workspace.session.user?.id ?? 'demo',
    space,
    previewFeature(preview),
    workspace.session.mode === 'secure',
  )
  const model = catalog?.models.find(
    (v) => v.id === step.model_config_id && v.version === step.model_config_version,
  )
  const choice = model ? { ...initialRuntimeChoice(model), voice: step.voice ?? '' } : null
  return (
    <RuntimeModelPicker
      space={space}
      capability={step.kind === 'llm' ? 'text' : step.kind}
      value={choice}
      onChange={(value) =>
        patch({
          model_config_id: value?.model.id,
          model_config_version: value?.model.version,
          voice: value?.voice || undefined,
        })
      }
      preview={preview}
      disabled={disabled}
      gatewayOnly
    />
  )
}
export function WorkflowStepSettings({
  step,
  graph,
  assets,
  space,
  project,
  projectVersion,
  disabled,
  patch,
  connect,
  remove,
}: {
  step: Step
  graph: Graph
  assets: Asset[]
  space: string
  project: string
  projectVersion: number
  disabled: boolean
  patch: (v: Partial<Step>) => void
  connect: (e: Wire) => void
  remove: () => void
}) {
  const { t } = useI18n()
  const [source, setSource] = useState('')
  const [port, setPort] = useState(ports(step.kind)[0] ?? '')
  return (
    <fieldset disabled={disabled}>
      <h3>{t(labels[step.kind])}</h3>
      <label>
        {t('步骤名称')}
        <input
          maxLength={80}
          value={step.label}
          onChange={(e) => patch({ label: e.target.value })}
        />
      </label>
      {(step.kind === 'text' || step.kind === 'template' || step.kind === 'llm') && (
        <label>
          {t(step.kind === 'llm' ? '文本处理要求' : '描述与文案')}
          <textarea
            maxLength={2000}
            value={step.text ?? ''}
            onChange={(e) => patch({ text: e.target.value })}
          />
        </label>
      )}
      {step.kind === 'template' && Boolean(graph.variables?.length) && (
        <p>
          {t('已声明变量')}：
          {graph.variables?.map((v) => (
            <code key={v.name}>{'{{' + v.name + '}} '}</code>
          ))}
        </p>
      )}
      {step.kind === 'template' && (
        <p className="drama-help">
          {t('使用 {{input}} 插入上一步文案，按原文替换，不执行表达式。')}
        </p>
      )}
      {step.kind === 'variable' && (
        <label>
          {t('读取运行变量')}
          <select
            value={step.variable_name ?? ''}
            onChange={(e) => patch({ variable_name: e.target.value })}
          >
            <option value="">{t('请选择变量')}</option>
            {graph.variables?.map((v) => (
              <option key={v.name} value={v.name}>
                {v.label || v.name}
              </option>
            ))}
          </select>
        </label>
      )}
      {step.kind === 'asset' && (
        <label>
          {t('项目素材')}
          <select
            value={step.asset_id ?? ''}
            onChange={(e) => {
              const a = assets.find((v) => v.id === e.target.value)
              patch({ asset_id: a?.id, asset_kind: a?.kind ?? 'image' })
            }}
          >
            <option value="">{t('请选择素材')}</option>
            {assets.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} · {a.kind}
              </option>
            ))}
          </select>
        </label>
      )}
      {['image', 'speech', 'video', 'llm'].includes(step.kind) && (
        <ModelSettings
          step={step}
          graph={graph}
          space={space}
          project={project}
          projectVersion={projectVersion}
          disabled={disabled}
          patch={patch}
        />
      )}
      {step.kind === 'image' && (
        <label>
          {t('画面比例')}
          <select value={step.ratio ?? '1:1'} onChange={(e) => patch({ ratio: e.target.value })}>
            {['9:16', '16:9', '1:1', '4:5'].map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        </label>
      )}
      {ports(step.kind).length > 0 && (
        <>
          <h4>{t('连接上一步')}</h4>
          <label>
            {t('来源步骤')}
            <select value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="">{t('选择步骤')}</option>
              {graph.nodes
                .filter((n) => n.id !== step.id && n.kind !== 'output')
                .map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.label || t(labels[n.kind])}
                  </option>
                ))}
            </select>
          </label>
          <label>
            {t('输入位置')}
            <select value={port} onChange={(e) => setPort(e.target.value)}>
              {ports(step.kind).map((v) => (
                <option key={v} value={v}>
                  {t(portLabels[v])}
                </option>
              ))}
            </select>
          </label>
          <button
            disabled={!source}
            onClick={() => connect({ source, target: step.id, input: port })}
          >
            {t('连接到此步骤')}
          </button>
        </>
      )}
      {step.kind === 'review' && (
        <p>{t('运行到这里会停下。创作者核对素材后，才继续后面的步骤。')}</p>
      )}
      {step.kind === 'output' && (
        <p>{t('输出保留在本项目素材中，不会自动覆盖剪辑或发布到外部平台。')}</p>
      )}
      <button onClick={remove}>{t('移除此步骤')}</button>
    </fieldset>
  )
}
