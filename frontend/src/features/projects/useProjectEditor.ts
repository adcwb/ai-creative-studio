import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { translate, useI18n } from '../../i18n'
import { request } from '../../api'
import { projectDraftError } from '../../drama'
import { useDramaProject } from '../../hooks/useDramaProject'
import { runtimeReady, type RuntimeChoice } from '../../runtimeSelection'

function ignoreProjectUpdate() {}

export function useProjectEditor({space, id, onDirtyChange, requestLeave}: {
  space: string; id: string | null; onDirtyChange: (dirty: boolean) => void
  requestLeave: (action: () => void) => void
}) {
  useI18n()
  const navigate = useNavigate()
  const [mediaDirty, setMediaDirty] = useState(false)
  const [runtimeChoice, setRuntimeChoice] = useState<RuntimeChoice | null>(null)
  const [storyboardChoice, setStoryboardChoice] = useState<RuntimeChoice | null>(null)
  const [toolsDirty, setToolsDirty] = useState(false)
  const [creative, setCreative] = useState<{provider: string; model?: string; mode: string; ready: boolean; message: string} | null>(null)
  useEffect(() => {const controller = new AbortController(); void request<{provider: string; model?: string; mode: string; ready: boolean; message: string}>('/creative-capabilities', {signal: controller.signal}).then(setCreative).catch(() => {if (!controller.signal.aborted) setCreative({provider: 'unavailable', mode: 'unavailable', ready: false, message: translate("无法读取创作配置，请刷新页面后重试。")})}); return () => controller.abort()}, [])

  const editor = useDramaProject(space, id, ignoreProjectUpdate, creative?.provider, creative?.model, runtimeChoice, storyboardChoice)
  const scriptPreview = {operation: 'script' as const, project_id: id ?? '', input: {expected_version: editor.baseline?.version}}
  const storyboardPreview = {operation: 'storyboard' as const, project_id: id ?? '', input: {expected_version: editor.baseline?.version}}
  const effectiveCreative = runtimeChoice ? {provider: runtimeChoice.model.provider, model: runtimeChoice.model.model, mode: 'real', ready: runtimeReady(runtimeChoice, scriptPreview)} : creative
  const effectiveStoryboard = storyboardChoice ? {mode: 'real', ready: runtimeReady(storyboardChoice, storyboardPreview)} : creative
  const allDirty = editor.dirty || mediaDirty || toolsDirty
  const [confirmGenerate, setConfirmGenerate] = useState(false)
  const [confirmStoryboard, setConfirmStoryboard] = useState(false)
  const active = editor.project?.latest_generation?.status === 'queued' || editor.project?.latest_generation?.status === 'running'
  const storyboardGeneration = editor.project?.latest_storyboard_generation
  const storyboardActive = storyboardGeneration?.status === 'queued' || storyboardGeneration?.status === 'running'
  const generation = editor.project?.latest_generation
  const validation = projectDraftError(editor.draft)
  useEffect(() => {onDirtyChange(allDirty); return () => onDirtyChange(false)}, [allDirty, onDirtyChange])
  function generate() {
    if (editor.project?.script) setConfirmGenerate(true)
    else editor.submit('generate')
  }
  useEffect(() => {
    if (id || !editor.project || editor.dirty) return
    const projectId = editor.project.id
    const timer = setTimeout(() => {void navigate('/projects/' + encodeURIComponent(projectId) + '/brief', {replace: true})}, 0)
    return () => clearTimeout(timer)
  }, [id, editor.project, editor.dirty, navigate])
  return {space, id, editor, creative, effectiveCreative, effectiveStoryboard, scriptPreview, storyboardPreview, runtimeChoice, setRuntimeChoice, storyboardChoice, setStoryboardChoice, mediaDirty, setMediaDirty, setToolsDirty, active, storyboardGeneration, storyboardActive, generation, validation, confirmGenerate, setConfirmGenerate, confirmStoryboard, setConfirmStoryboard, generate, requestLeave}
}

export type ProjectEditorState = ReturnType<typeof useProjectEditor>
