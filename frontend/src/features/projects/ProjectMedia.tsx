import {gatewayCreationRequired} from '../../creationGateway'
import { MediaStudio } from '../../components/MediaStudio'
import {RevisionWorkspace} from './RevisionWorkspace'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectMedia({state}: {state: ProjectEditorState}) {
  const {space, editor, setMediaDirty} = state
  if (!editor.project) return null
  return <>
        <MediaStudio allowSyntheticDemo={!gatewayCreationRequired} project={editor.project} space={space} onDirtyChange={setMediaDirty} />
		<RevisionWorkspace key={`${space}:${editor.project.id}`} project={editor.project.id} space={space} disabled={Boolean(editor.busy)||editor.loading||Boolean(editor.loadError)||editor.editingStoryboard||editor.editingScript||editor.briefDirty} onDirtyChange={state.setToolsDirty}/>

  </>
}
