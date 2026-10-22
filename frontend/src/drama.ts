export interface ScriptCharacter { name: string; description: string }
export interface ScriptDialogue { character: string; text: string }
export interface ScriptScene { heading: string; action: string; dialogue: ScriptDialogue[] }
export interface DramaScript { title: string; logline: string; characters: ScriptCharacter[]; scenes: ScriptScene[] }
export interface ScriptGeneration {
  id: string; space_id: string; project_id: string; source_title: string; source_brief: string; base_version: number
  provider: 'mock-script'; status: 'queued' | 'running' | 'succeeded' | 'failed'; progress: number
  created_at: string; updated_at: string; error?: string; error_code?: string
}
export interface DramaProject {
  id: string; space_id: string; title: string; brief: string; version: number; created_at: string; updated_at: string
  script?: DramaScript; script_brief?: string; latest_generation?: ScriptGeneration
}
export interface ProjectDraft { title: string; brief: string }
export const generationLabels: Record<ScriptGeneration['status'], string> = {
  queued: '等待生成', running: '正在生成模拟剧本', succeeded: '模拟剧本已生成', failed: '生成失败',
}
export function generationActive(project: DramaProject | null) {
  return project?.latest_generation?.status === 'queued' || project?.latest_generation?.status === 'running'
}
export function fieldLengthError(value: string, minimum: number, maximum: number, label: string) {
  const length = [...value.trim()].length
  return length < minimum || length > maximum ? `${label}需要 ${minimum}–${maximum} 个字符。` : ''
}
export function projectDraftError(draft: ProjectDraft) {
  return fieldLengthError(draft.title, 1, 80, '项目名称') || fieldLengthError(draft.brief, 3, 2000, '创作简报')
}
export function scriptDraftError(script: DramaScript) {
  let error = fieldLengthError(script.title, 1, 80, '剧本标题') || fieldLengthError(script.logline, 3, 300, '故事梗概')
  for (const [index, scene] of script.scenes.entries()) {
    error ||= fieldLengthError(scene.heading, 1, 100, `第 ${index + 1} 场标题`) || fieldLengthError(scene.action, 1, 1000, `第 ${index + 1} 场动作`)
    for (const line of scene.dialogue) error ||= fieldLengthError(line.text, 1, 300, `第 ${index + 1} 场对白`)
  }
  return error
}
