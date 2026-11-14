import { useState } from 'react'
import { Link } from 'react-router-dom'
import { translate } from '../../i18n'
import { localizedError } from '../../api'
import { generationLabels } from '../../drama'
import { DramaScriptEditor } from '../../components/DramaScriptEditor'
import { ProjectHistory } from '../../components/ProjectHistory'
import { Icon } from '../../components/Icon'
import { RuntimeModelPicker } from '../../components/RuntimeModelPicker'
import { WritingContextPicker, type WritingReview } from './WritingContext'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectScript({state}: {state: ProjectEditorState}) {
  const {space, id, editor, effectiveCreative, scriptPreview, runtimeChoice, setRuntimeChoice, active, generation, toolsDirty, setToolsDirty} = state
  const [confirmation, setConfirmation] = useState<number | null>(null)
  const [context, setContext] = useState<WritingReview>({id: '', ready: true, changed: false})
  if (!id || !editor.project) return null
  const project = editor.project
  const blocked = editor.locked || Boolean(editor.loadError) || editor.dirty || editor.editingScript || editor.editingStoryboard || toolsDirty || !context.ready || !effectiveCreative?.ready
  const preview = {...scriptPreview, input: {...scriptPreview.input, ...(context.id ? {context_snapshot_id: context.id} : {})}}
  function generate() {
    if (blocked) return
    setConfirmation(null)
    editor.submit('generate', context.id)
  }
  return <>
    {project.structure_version !== 2 && <details className="panel editing-note">
      <summary>{translate('保留角色与场次关联')}</summary>
      <p>{translate('为旧项目启用稳定关联后，角色改名和场次重排会保留对应关系。原稿与旧成片仍保留；本操作不会生成内容或拆分剧集。')}</p>
      <button type="button" disabled={editor.locked || editor.dirty || Boolean(editor.loadError)} onClick={() => editor.submit('structure')}>{translate('启用稳定关联')}</button>
    </details>}
    <section className="panel drama-generation" aria-labelledby="generation-heading">
      <div className="section-heading"><h2 id="generation-heading">{translate(effectiveCreative?.mode === 'mock' ? '模拟剧本' : 'AI 剧本')}</h2><Link to={`/projects/${id}/brief`}>{translate('修改简报')}</Link></div>
      <details><summary>{translate('生成依据：已保存简报')}</summary><p>{project.brief}</p></details>
      <details open={!project.script}><summary>{translate('生成设置')}</summary><RuntimeModelPicker gatewayOnly space={space} capability="text" preview={preview} value={runtimeChoice} onChange={setRuntimeChoice} disabled={editor.locked} /></details>
      {project.series_episode_id && <WritingContextPicker episode={project.id} space={space} projectVersion={project.version} disabled={editor.locked || editor.dirty} onChange={setContext} />}
      <p className="drama-help">{translate('生成后直接保存为当前剧本，可继续编辑；旧稿可从版本历史恢复。')}</p>
      <button type="button" className={project.script ? '' : 'primary'} disabled={blocked} onClick={() => project.script ? setConfirmation(project.version) : generate()}>{active ? translate('正在生成剧本…') : project.script ? translate('重新生成') : translate(effectiveCreative?.mode === 'mock' ? '生成模拟剧本' : 'AI 生成剧本')}</button>
      {confirmation === project.version && <div className="editing-note" role="group" aria-label={translate('确认重新生成剧本')}>
        <p>{translate('将根据当前简报重新生成剧本，成功后替换当前内容，旧稿可从版本历史恢复。')}</p>
        {project.storyboard && <p>{translate('已有分镜会保留，生成新剧本后需要复核。')}</p>}
        <button type="button" onClick={() => setConfirmation(null)}>{translate('取消')}</button>
        <button type="button" className="primary" disabled={blocked} onClick={generate}>{translate('确认重新生成')}</button>
      </div>}
      {generation && <div className="drama-generation-state">
        {active && <div className="progress-block"><span>{translate(generationLabels[generation.status])}</span><progress aria-label={translate('剧本生成进度')} value={generation.progress} max="100" /></div>}
        {generation.status === 'failed' && <div className="error" role="alert">
          <p>{translate('本次未能生成完整剧本，当前内容已保留。')}</p>
          <details><summary>{translate('查看详情')}</summary><p>{localizedError(generation.error, generation.error_code)}</p>{generation.error_code && <code>{generation.error_code}</code>}</details>
        </div>}
      </div>}
      {(editor.briefDirty || editor.editingScript || editor.editingStoryboard) && <p className="drama-help">{translate('请先保存或取消当前编辑，再生成剧本。')}</p>}
    </section>
    {project.script_brief && project.script_brief !== project.brief && <p className="editing-note">{translate('简报已更新，当前剧本仍保留原内容；需要按新简报创作时，可重新生成。')}</p>}
    {editor.script ? <DramaScriptEditor provider={project.script_provider} script={editor.script} editing={editor.editingScript} disabled={editor.locked || toolsDirty || editor.briefDirty || editor.editingStoryboard || Boolean(editor.loadError)} dirty={editor.scriptDirty} onChange={editor.setScript} onEdit={() => editor.setEditingScript(true)} onCancel={editor.cancelScript} onSave={() => editor.submit('script')} /> : <section className="panel drama-no-script"><Icon name="script" /><h2>{translate('故事在等一个开场')}</h2><button disabled={editor.locked || toolsDirty || Boolean(editor.loadError)} onClick={() => {editor.setScript({...(project.structure_version === 2 ? {structure_version: 2 as const} : {}), title: project.title, logline: project.brief.slice(0, 300), characters: [], scenes: [{...(project.structure_version === 2 ? {id: crypto.randomUUID()} : {}), heading: translate('新场次'), action: translate('场景动作'), dialogue: [], episode_number: 1}]}); editor.setEditingScript(true)}}>{translate('开始手写剧本')}</button></section>}
    {project.script && <Link className="button-link" to={`/projects/${id}/storyboard`}>{translate('下一步：生成分镜')}</Link>}
    <ProjectHistory project={project} space={space} disabled={editor.locked || editor.dirty || editor.editingScript} onRestore={() => editor.reload(true)} onDirtyChange={setToolsDirty} />
  </>
}
