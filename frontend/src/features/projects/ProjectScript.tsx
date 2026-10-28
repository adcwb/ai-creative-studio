import { translate } from '../../i18n'
import { localizedError } from '../../api'
import { generationLabels } from '../../drama'
import { DramaScriptEditor } from '../../components/DramaScriptEditor'
import { GenerationHistory } from '../../components/GenerationHistory'
import { CreativeTools } from '../../components/CreativeTools'
import { Icon } from '../../components/Icon'
import { RuntimeModelPicker } from '../../components/RuntimeModelPicker'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectScript({state}: {state: ProjectEditorState}) {
  const {space, id, editor, creative, effectiveCreative, scriptPreview, runtimeChoice, setRuntimeChoice, mediaDirty, setToolsDirty, active, generation, generate} = state
  if (!id || !editor.project) return null
  return <>
        <RuntimeModelPicker space={space} capability="text" preview={scriptPreview} value={runtimeChoice} onChange={setRuntimeChoice} disabled={editor.locked} />
        <section className="panel drama-generation" aria-labelledby="generation-heading"><div className="section-heading"><div><span className="eyebrow">02 / A FIRST DRAFT</span><h2 id="generation-heading">{effectiveCreative?.mode === 'real' ? translate("生成 AI 剧本") : translate("生成模拟剧本")}</h2></div><span>{translate("2 位角色 / 3 个场景")}</span></div>
          <p>{translate("根据已保存简报，由后端生成结构化剧本；成功后替换当前稿，失败会保留已有剧本。")}</p>
          {generation && <div className="drama-generation-state"><span className={`status ${generation.status}`}><span />{translate(generationLabels[generation.status])}</span>{active && <div className="progress-block"><progress aria-label={translate("剧本生成进度")} value={generation.progress} max="100" /><span>{generation.progress}{translate("% · 后端处理中，自动更新")}</span></div>}{generation.error && <div className="error" role="alert">{localizedError(generation.error, generation.error_code)}{generation.error_code && <code>（{generation.error_code}）</code>}<p>{translate("已有剧本不会被覆盖。调整并保存简报后，可以再次生成。")}</p></div>}</div>}
          {(editor.briefDirty || editor.editingScript || editor.editingStoryboard) && <p className="drama-help">{translate("请先保存或取消当前编辑，再生成剧本。")}</p>}
          <button type="button" className="primary" disabled={editor.locked || !effectiveCreative?.ready || editor.briefDirty || editor.editingScript || editor.editingStoryboard || Boolean(editor.loadError)} onClick={generate}><Icon name="spark" />{active ? translate("剧本生成中…") : editor.busy === 'generate' ? translate("正在提交…") : editor.project.script ? translate("重新生成剧本") : effectiveCreative?.mode === 'real' ? translate("生成 AI 剧本（可能计费）") : translate("生成模拟剧本")}</button>
          <GenerationHistory space={space} projectId={id} revision={editor.historyRevision} />
        </section>
        {editor.project.script_brief && editor.project.script_brief !== editor.project.brief && <div className="editing-note drama-source-note"><strong>{translate("当前剧本来自之前的简报")}</strong><p>{translate("项目需求已更新，现有剧本仍保留原来的内容。重新生成成功后才会替换。")}</p><details><summary>{translate("查看剧本来源简报")}</summary><p>{editor.project.script_brief}</p></details></div>}
        {editor.script ? <DramaScriptEditor provider={editor.project.script_provider} script={editor.script} editing={editor.editingScript} disabled={editor.locked || editor.briefDirty || editor.editingStoryboard || Boolean(editor.loadError)} dirty={editor.scriptDirty} onChange={editor.setScript} onEdit={() => editor.setEditingScript(true)} onCancel={editor.cancelScript} onSave={() => editor.submit('script')} /> : <section className="panel drama-no-script"><Icon name="script" /><h2>{translate("故事在等一个开场")}</h2><p>{translate("保存项目简报后，生成第一份模拟剧本。你可以继续编辑场景与对白。")}</p></section>}

<details className="panel project-tools"><summary>{translate("创作辅助工具与版本历史")}</summary>
        <CreativeTools expectedProvider={creative?.provider} expectedModel={creative?.model} project={editor.project} space={space} disabled={editor.locked || editor.dirty || mediaDirty} onRestore={() => editor.reload(true)} onDirtyChange={setToolsDirty} />
</details>
  </>
}
