import {creationCapability} from '../../creationGateway'
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useOutletContext } from 'react-router-dom'
import type { Task, TaskInput, Template } from '../../api'
import { useAppWorkspace } from '../../app/workspace'
import { useWorkspace } from '../../hooks/useWorkspace'
import { runtimeFields, type RuntimeChoice } from '../../runtimeSelection'

const blankDraft: TaskInput = {prompt: '', kind: 'poster', ratio: '4:5', style: 'natural'}

export function useImageController() {
  const {space: selectedSpace, capability, capabilityError, refreshCapability, setImageDirty, session} = useAppWorkspace()
  const space = selectedSpace?.id ?? ''
  const navigate = useNavigate()
  const studio = useWorkspace(space)
  const [runtimeChoice, setRuntimeChoice] = useState<RuntimeChoice | null>(null)
  const [draft, setDraft] = useState<TaskInput>(blankDraft)
  const [editing, setEditing] = useState<{task: Task; previous: TaskInput} | null>(null)
  const [detailId, setDetailId] = useState<string | null>(null)
  const promptRef = useRef<HTMLTextAreaElement>(null)
  const preview = {operation: 'image' as const, input: {...draft, prompt: draft.prompt.trim()}}
  const effectiveCapability = creationCapability(runtimeChoice, preview, capability)
  const savedIds = new Set(studio.assets.map(asset => asset.task_id))
  const running = studio.tasks.filter(task => task.status === 'queued' || task.status === 'running').length
  const busy = studio.busy || Boolean(studio.saving)
  useEffect(() => {setImageDirty(busy); return () => setImageDirty(false)}, [busy, setImageDirty])

  function focusPrompt() {requestAnimationFrame(() => promptRef.current?.focus())}
  function chooseSavedTemplate(value: TaskInput) {studio.newAttempt(); setEditing(null); setDraft(value); focusPrompt()}
  function chooseTemplate(template: Template) {
    studio.newAttempt(); setEditing(null)
    setDraft({prompt: template.prompt, kind: template.kind, ratio: template.ratio, style: template.style})
    focusPrompt()
  }
  function editTask(task: Task) {
    studio.newAttempt(); setDetailId(null)
    setEditing({task, previous: editing?.previous ?? draft})
    setDraft({prompt: task.prompt, kind: task.kind, ratio: task.ratio, style: task.style, ...(task.prompt_source ? {prompt_source: {template_id: task.prompt_source.template_id, version_id: task.prompt_source.version_id, edited: true}} : {})})
    void navigate('/studio'); focusPrompt()
  }
  function cancelEdit() {
    if (editing) setDraft(editing.previous)
    studio.newAttempt(); setEditing(null); focusPrompt()
  }
  function startNew() {studio.newAttempt(); setEditing(null); setDraft(blankDraft); void navigate('/studio'); focusPrompt()}
  function openDetail(id: string) {studio.clearFeedback(); setDetailId(id)}
  async function submitDraft() {
    if (!effectiveCapability?.ready || effectiveCapability.mode === 'unavailable') return
    const outcome = await studio.create({...draft, ...runtimeFields(runtimeChoice)}, effectiveCapability, editing?.task.id)
    if (outcome === 'provider_changed') refreshCapability()
    if (outcome === 'created' && editing) {setEditing(null); setDraft(blankDraft)}
  }
  return {space, actor: session.user?.id ?? "demo-user", readOnly: selectedSpace?.role === "viewer", chooseSavedTemplate, capability, capabilityError, refreshCapability, studio, runtimeChoice, setRuntimeChoice, draft, setDraft, editing, detailId, setDetailId, promptRef, preview, effectiveCapability, savedIds, running, chooseTemplate, editTask, cancelEdit, startNew, openDetail, submitDraft}
}

export function useImageWorkspace() {return useOutletContext<{image: ReturnType<typeof useImageController>}>().image}
