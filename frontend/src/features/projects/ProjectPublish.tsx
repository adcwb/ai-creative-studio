import { SocialPublishCenter } from '../../components/SocialPublishCenter'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectPublish({state}: {state: ProjectEditorState}) {
  const {space, editor} = state
  if (!editor.project) return null
  return <>
        <SocialPublishCenter projectId={editor.project.id} space={space} />
  </>
}
