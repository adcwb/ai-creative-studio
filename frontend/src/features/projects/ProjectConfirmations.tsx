import { translate } from '../../i18n'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectConfirmations({state}: {state: ProjectEditorState}) {
  const {editor, confirmGenerate, setConfirmGenerate, confirmStoryboard, setConfirmStoryboard} = state
  return <>
    {confirmStoryboard && <ConfirmDialog title={translate("替换当前分镜计划？")} message={translate("新的模拟分镜生成成功后，会替换当前已保存的分镜计划；旧计划可在版本历史中恢复。生成失败时，当前计划保持不变。")} confirmLabel={translate("确认重新生成分镜")} onCancel={() => setConfirmStoryboard(false)} onConfirm={() => {setConfirmStoryboard(false); editor.submit('generateStoryboard')}} />}
    {confirmGenerate && <ConfirmDialog title={translate("替换当前剧本？")} message={translate("新的模拟剧本生成成功后，会替换当前已保存的剧本；旧稿可在版本历史中恢复。生成失败时，当前稿保持不变；成功后已有分镜会标记为过期。")} confirmLabel={translate("确认重新生成")} onCancel={() => setConfirmGenerate(false)} onConfirm={() => {setConfirmGenerate(false); editor.submit('generate')}} />}
  </>
}
