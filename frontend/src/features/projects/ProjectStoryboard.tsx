import { translate } from '../../i18n'
import { localizedError } from '../../api'
import { storyboardGenerationLabels } from '../../drama'
import { StoryboardEditor } from '../../components/StoryboardEditor'
import { Icon } from '../../components/Icon'
import { RuntimeModelPicker } from '../../components/RuntimeModelPicker'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectStoryboard({state}: {state: ProjectEditorState}) {
  const {space, editor, creative, effectiveStoryboard, storyboardPreview, storyboardChoice, setStoryboardChoice, storyboardGeneration, storyboardActive, setConfirmStoryboard} = state
  if (!editor.project) return null
  return <>
        <RuntimeModelPicker space={space} capability="text" preview={storyboardPreview} value={storyboardChoice} onChange={setStoryboardChoice} disabled={editor.locked} />
        <section className="panel drama-generation storyboard-generation" aria-labelledby="storyboard-generation-heading"><div className="section-heading"><div><span className="eyebrow">03 / FROM SCENES TO SHOTS</span><h2 id="storyboard-generation-heading">{effectiveStoryboard?.mode === 'real' ? translate("生成 AI 分镜") : translate("生成模拟分镜")}</h2></div><span>{creative?.provider ?? translate("读取创作配置…")}</span></div>
          <p>{translate("从已保存的剧本生成镜头计划，再编辑画面、景别、运镜、时长和对白。生成成功替换当前计划，失败保留旧计划。")}</p>
          {!editor.project.script && <p className="drama-help">{translate("还没有已保存剧本。请先生成剧本，再创建分镜计划。")}</p>}
          {storyboardGeneration && <div className="drama-generation-state"><span className={`status ${storyboardGeneration.status}`}><span />{translate(storyboardGenerationLabels[storyboardGeneration.status])}</span>{storyboardActive && <div className="progress-block"><progress aria-label={translate("分镜生成进度")} value={storyboardGeneration.progress} max="100" /><span>{storyboardGeneration.progress}{translate("% · 后端处理中，自动更新")}</span></div>}{storyboardGeneration.error && <div className="error" role="alert">{localizedError(storyboardGeneration.error, storyboardGeneration.error_code)}{storyboardGeneration.error_code && <code>（{storyboardGeneration.error_code}）</code>}<p>{translate("旧分镜计划仍保留。检查并保存剧本后，可以再次生成。")}</p></div>}</div>}
          {(editor.briefDirty || editor.editingScript || editor.editingStoryboard) && <p className="drama-help">{translate("请先保存或取消当前编辑，再生成分镜。")}</p>}
          <button type="button" className="primary" disabled={editor.locked || !effectiveStoryboard?.ready || !editor.project.script || editor.briefDirty || editor.editingScript || editor.editingStoryboard || Boolean(editor.loadError)} onClick={() => {if (editor.project?.storyboard) setConfirmStoryboard(true); else editor.submit('generateStoryboard')}}><Icon name="spark" />{storyboardActive ? translate("分镜生成中…") : editor.busy === 'generateStoryboard' ? translate("正在提交分镜…") : editor.project.storyboard ? translate("重新生成分镜") : effectiveStoryboard?.mode === 'real' ? translate("生成 AI 分镜（可能计费）") : translate("生成模拟分镜")}</button>
        </section>
        {editor.project.storyboard ? <StoryboardEditor provider={editor.project.storyboard.provider} shots={editor.shots} sourceVersion={editor.baseline?.storyboard?.source_script_version ?? editor.project.storyboard.source_script_version} stale={(editor.baseline?.storyboard?.source_script_version ?? editor.project.storyboard.source_script_version) !== editor.project.script_version} editing={editor.editingStoryboard} disabled={editor.locked || editor.briefDirty || editor.editingScript || Boolean(editor.loadError)} dirty={editor.storyboardDirty} onChange={editor.setShots} onEdit={() => editor.setEditingStoryboard(true)} onCancel={editor.cancelStoryboard} onSave={() => editor.submit('storyboard')} /> : editor.project.script && <section className="panel drama-no-script storyboard-empty"><Icon name="script" /><h2>{translate("还没有分镜计划")}</h2><p>{translate("剧本已保存。生成模拟分镜后，可以逐镜编辑并保存。")}</p></section>}

  </>
}
