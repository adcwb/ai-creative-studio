import { translate } from '../../i18n'
import { localizedError } from '../../api'
import { generationLabels } from '../../drama'
import { DramaScriptEditor } from '../../components/DramaScriptEditor'
import { CreativeTools } from '../../components/CreativeTools'
import { ScriptCandidates } from './ScriptCandidates'
import { Icon } from '../../components/Icon'
import { RuntimeModelPicker } from '../../components/RuntimeModelPicker'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectScript({state}: {state: ProjectEditorState}) {
  const {space, id, editor, effectiveCreative, scriptPreview, runtimeChoice, setRuntimeChoice, active, generation, toolsDirty, setToolsDirty} = state
  if (!id || !editor.project) return null
  return <>
        {editor.project.structure_version !== 2 && <section className="panel editing-note" aria-label={translate('稳定关联')}>
          <h2>{translate('保留角色与场次关联')}</h2>
          <p>{translate('为旧项目启用稳定关联后，角色改名和场次重排会保留对应关系。原稿与旧成片仍保留；本操作不会生成内容或拆分剧集。')}</p>
          <button type="button" disabled={editor.locked || editor.dirty || Boolean(editor.loadError)} onClick={() => editor.submit('structure')}>{translate('启用稳定关联')}</button>
        </section>}
        <RuntimeModelPicker gatewayOnly space={space} capability="text" preview={scriptPreview} value={runtimeChoice} onChange={setRuntimeChoice} disabled={editor.locked} />
        <section className="panel drama-generation" aria-labelledby="generation-heading"><div className="section-heading"><div><span className="eyebrow">02 / A FIRST DRAFT</span><h2 id="generation-heading">{effectiveCreative?.mode === 'real' ? translate("生成 AI 剧本") : translate("生成模拟剧本")}</h2></div><span>{translate("按故事组织角色、分集与场次")}</span></div>
          <p>{translate("根据已保存简报生成独立候选。正式剧本保持不变，比较和编辑后由你明确采纳。")}</p>
          {generation && <div className="drama-generation-state"><span className={`status ${generation.status}`}><span />{translate(generationLabels[generation.status])}</span>{active && <div className="progress-block"><progress aria-label={translate("剧本生成进度")} value={generation.progress} max="100" /><span>{generation.progress}{translate("% · 后端处理中，自动更新")}</span></div>}{generation.error && <div className="error" role="alert">{localizedError(generation.error, generation.error_code)}{generation.error_code && <code>（{generation.error_code}）</code>}<p>{translate("已有剧本不会被覆盖。调整并保存简报后，可以再次生成。")}</p></div>}</div>}
          {(editor.briefDirty || editor.editingScript || editor.editingStoryboard) && <p className="drama-help">{translate("请先保存或取消当前编辑，再生成剧本。")}</p>}

          <ScriptCandidates state={state} />
        </section>
        {editor.project.script_brief && editor.project.script_brief !== editor.project.brief && <div className="editing-note drama-source-note"><strong>{translate("当前剧本来自之前的简报")}</strong><p>{translate("项目需求已更新，现有剧本仍保留原来的内容。生成候选不会替换，明确采纳后才更新。")}</p><details><summary>{translate("查看剧本来源简报")}</summary><p>{editor.project.script_brief}</p></details></div>}
        {editor.script ? <DramaScriptEditor provider={editor.project.script_provider} script={editor.script} editing={editor.editingScript} disabled={editor.locked || toolsDirty || editor.briefDirty || editor.editingStoryboard || Boolean(editor.loadError)} dirty={editor.scriptDirty} onChange={editor.setScript} onEdit={() => editor.setEditingScript(true)} onCancel={editor.cancelScript} onSave={() => editor.submit('script')} /> : <section className="panel drama-no-script"><Icon name="script" /><h2>{translate("故事在等一个开场")}</h2><button onClick={() => {editor.setScript({...(editor.project!.structure_version === 2 ? {structure_version: 2 as const} : {}), title: editor.project!.title, logline: editor.project!.brief.slice(0, 300), characters: [], scenes: [{...(editor.project!.structure_version === 2 ? {id: crypto.randomUUID()} : {}), heading: translate("新场次"), action: translate("场景动作"), dialogue: [], episode_number: 1}]}); editor.setEditingScript(true)}}>{translate("开始手写剧本")}</button><p>{translate("保存项目简报后，选择网关模型生成剧本。你也可以手工编辑场景与对白。")}</p></section>}


    <details className="panel project-tools"><summary>{translate("创作辅助工具与版本历史")}</summary><CreativeTools historyOnly project={editor.project} space={space} disabled={editor.locked || editor.dirty} onRestore={() => editor.reload(true)} onDirtyChange={setToolsDirty} /></details>
  </>
}
