import { translate } from '../../i18n'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectBrief({state}: {state: ProjectEditorState}) {
  const {id, editor, validation} = state
  return <>
      <section className="panel drama-brief" aria-labelledby="brief-heading"><div className="section-heading"><div><span className="eyebrow">01 / THE STARTING POINT</span><h2 id="brief-heading">{id ? translate("项目简报") : translate("新建短剧项目")}</h2></div><span>{id ? translate("版本 {p0}", {p0: editor.baseline?.version ?? '—'}) : translate("先保存，再生成")}</span></div>
        <form onSubmit={event => {event.preventDefault(); editor.submit(id ? 'brief' : 'create')}}>
          <fieldset disabled={editor.locked || editor.editingScript || editor.editingStoryboard}><label htmlFor="project-title">{translate("项目名称")}</label><input id="project-title" value={editor.draft.title} onChange={event => editor.setDraft({...editor.draft, title: event.target.value})} placeholder={translate("给这个故事起个名字")} />
            <div className="drama-field-title"><label htmlFor="project-brief">{translate("创作简报")}</label><span>{translate("3–2000 字")}</span></div><textarea id="project-brief" value={editor.draft.brief} onChange={event => editor.setDraft({...editor.draft, brief: event.target.value})} placeholder={translate("写下人物关系、冲突和期待的结局。简报会成为本次生成的依据。")} /><div className="field-footer"><span>{translate("请勿输入敏感信息")}</span><span>{[...editor.draft.brief.trim()].length} / 2000</span></div>
          </fieldset>
          {validation && editor.briefDirty && <p className="drama-validation" role="status">{validation}</p>}
          <div className="drama-brief-actions"><span>{editor.briefDirty ? translate("有未保存的简报修改") : id ? translate("简报已保存") : translate("建立你的第一个故事起点")}</span><div>{id && editor.briefDirty && <button type="button" disabled={editor.locked || editor.editingScript || editor.editingStoryboard} onClick={editor.cancelBrief}>{translate("取消简报修改")}</button>}<button className="primary" type="submit" disabled={editor.locked || editor.editingScript || editor.editingStoryboard || Boolean(validation) || (Boolean(id) && !editor.briefDirty)}>{editor.busy === 'create' || editor.busy === 'brief' ? translate("正在保存…") : id ? translate("保存项目简报") : translate("创建项目")}</button></div></div>
        </form>
      </section>

  </>
}
