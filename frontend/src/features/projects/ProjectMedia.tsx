import {gatewayCreationRequired} from '../../creationGateway'
import { MediaStudio } from '../../components/MediaStudio'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectMedia({state}: {state: ProjectEditorState}) {
  const {space, editor, setMediaDirty} = state
  if (!editor.project) return null
  return <>
        <MediaStudio allowSyntheticDemo={!gatewayCreationRequired} project={editor.project} space={space} onDirtyChange={setMediaDirty} />

  </>
}
