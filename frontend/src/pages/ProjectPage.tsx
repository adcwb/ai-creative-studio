import { useParams } from 'react-router-dom'
import { useAppWorkspace } from '../app/workspace'
import { ProjectEditor } from '../features/projects/ProjectEditor'
import { NotFoundPage } from './NotFoundPage'

export function ProjectPage() {
  const {projectId, section} = useParams()
  const {space, setDirty, requestLeave} = useAppWorkspace()
  if (!projectId || !section || !['brief', 'script', 'storyboard', 'avatar', 'media', 'publish'].includes(section) || (projectId === 'new' && section !== 'brief')) return <NotFoundPage />
  return <ProjectEditor key={`${space!.id}:${projectId}`} space={space!.id} id={projectId === 'new' ? null : projectId} section={section} onDirtyChange={setDirty} requestLeave={requestLeave} />
}
