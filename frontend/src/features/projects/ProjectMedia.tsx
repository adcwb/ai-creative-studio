import {useCallback,useState} from 'react'
import {gatewayCreationRequired} from '../../creationGateway'
import { MediaStudio } from '../../components/MediaStudio'
import {RevisionWorkspace} from './RevisionWorkspace'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectMedia({state}: {state: ProjectEditorState}) {
  const {space, editor, setMediaDirty} = state
  const [restored,setRestored]=useState(0),[restoring,setRestoring]=useState(false)
  const onRestored=useCallback(()=>setRestored(v=>v+1),[])
  if (!editor.project) return null
  return <>
        <fieldset disabled={restoring}><MediaStudio key={restored} allowSyntheticDemo={!gatewayCreationRequired} project={editor.project} space={space} onDirtyChange={setMediaDirty} /></fieldset>
		<RevisionWorkspace key={`${space}:${editor.project.id}`} project={editor.project.id} space={space} disabled={Boolean(editor.busy)||editor.loading||Boolean(editor.loadError)||editor.editingStoryboard||editor.editingScript||editor.briefDirty||state.mediaDirty} onDirtyChange={state.setToolsDirty} onRestored={onRestored} onRestoreBusy={setRestoring}/>

  </>
}
