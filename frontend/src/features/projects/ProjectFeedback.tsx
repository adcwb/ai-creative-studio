import { translate } from '../../i18n'
import { Icon } from '../../components/Icon'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectFeedback({state}: {state: ProjectEditorState}) {
  const {editor, requestLeave} = state
  return <>
    {editor.loadError && <div className="error" role="alert">{translate("无法同步项目：")}{editor.loadError}<button type="button" onClick={() => editor.reload()}>{translate("重试读取项目")}</button></div>}
    {editor.loading && !editor.project && <div className="panel loading-block" role="status">{translate("正在读取项目详情…")}</div>}
      {editor.actionError && <div className="error drama-action-error" role="alert">{editor.actionError}{editor.uncertain && <button type="button" disabled={Boolean(editor.busy)} onClick={editor.retry}>{editor.busy ? translate("正在重试…") : translate("重试上次请求")}</button>}{editor.conflict && <div><p>{translate("本地草稿已保留。重新读取会载入服务端最新内容并放弃本地修改。")}</p><button type="button" onClick={() => requestLeave(() => editor.reload(true))}>{translate("重新读取最新项目")}</button></div>}</div>}
      {editor.notice && <p className="notice" role="status"><Icon name="check" />{translate(editor.notice)}</p>}
      {editor.busy && <p className="notice" role="status">{editor.busy === 'generate' || editor.busy === 'generateStoryboard' ? translate("正在提交生成请求…") : translate("正在提交保存请求…")}{translate("请等待服务端确认。")}</p>}

  </>
}
