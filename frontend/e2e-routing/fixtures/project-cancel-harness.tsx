// Browser-only regression harness: retain cancellation callbacks from the
// loading render, then invoke them after the real hook accepts its HTTP load.
import { useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { useDramaProject } from '../../src/hooks/useDramaProject'

function onProject() {}
export function Harness() {
  const editor = useDramaProject('personal-demo', 'story-1', onProject)
  const initial = useRef({brief: editor.cancelBrief, script: editor.cancelScript, storyboard: editor.cancelStoryboard})
  return <>
    <output>{JSON.stringify({loaded: !editor.loading, title: editor.draft.title, script: editor.script?.title, shots: editor.shots.length, dirty: editor.dirty})}</output>
    <button disabled={editor.loading} onClick={() => {initial.current.brief(); initial.current.script(); initial.current.storyboard()}}>Cancel from loading render</button>
  </>
}
createRoot(document.getElementById('root')!).render(<Harness />)
