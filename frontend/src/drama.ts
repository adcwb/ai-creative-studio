import { translate } from './i18n'
export interface ScriptCharacter { name: string; description: string }
export interface ScriptDialogue { character: string; text: string }
export interface ScriptScene { heading: string; action: string; dialogue: ScriptDialogue[] }
export interface DramaScript { title: string; logline: string; characters: ScriptCharacter[]; scenes: ScriptScene[] }
export interface ScriptGeneration {
  id: string; space_id: string; project_id: string; source_title: string; source_brief: string; base_version: number
  provider: string; status: 'queued' | 'running' | 'succeeded' | 'failed'; progress: number
  created_at: string; updated_at: string; error?: string; error_code?: string
}
export interface DramaProject {
  id: string; space_id: string; title: string; brief: string; version: number; created_at: string; updated_at: string
  script_provider?: string; script?: DramaScript; script_brief?: string; latest_generation?: ScriptGeneration
  script_version: number; storyboard?: StoryboardPlan; storyboard_stale: boolean; latest_storyboard_generation?: StoryboardGeneration
}
export interface ProjectDraft { title: string; brief: string }
export const generationLabels: Record<ScriptGeneration['status'], string> = {
  queued: '等待生成', running: '正在生成剧本', succeeded: '剧本已生成', failed: '生成失败',
}
export function generationActive(project: DramaProject | null) {
  return [project?.latest_generation?.status, project?.latest_storyboard_generation?.status].some(status => status === 'queued' || status === 'running')
}
export function fieldLengthError(value: string, minimum: number, maximum: number, label: string) {
  const length = [...value.trim()].length
  return length < minimum || length > maximum ? translate("{p0}需要 {p1}–{p2} 个字符。", {p0: label, p1: minimum, p2: maximum}) : ''
}
export function projectDraftError(draft: ProjectDraft) {
  return fieldLengthError(draft.title, 1, 80, translate("项目名称")) || fieldLengthError(draft.brief, 3, 2000, translate("创作简报"))
}
export function scriptDraftError(script: DramaScript) {
  let error = fieldLengthError(script.title, 1, 80, translate("剧本标题")) || fieldLengthError(script.logline, 3, 300, translate("故事梗概"))
  for (const [index, scene] of script.scenes.entries()) {
    error ||= fieldLengthError(scene.heading, 1, 100, translate("第 {p0} 场标题", {p0: index + 1})) || fieldLengthError(scene.action, 1, 1000, translate("第 {p0} 场动作", {p0: index + 1}))
    for (const line of scene.dialogue) error ||= fieldLengthError(line.text, 1, 300, translate("第 {p0} 场对白", {p0: index + 1}))
  }
  return error
}

export const shotSizeLabels = {wide: '全景', medium: '中景', close_up: '特写'} as const
export const cameraMovementLabels = {static: '固定', pan: '摇镜', track: '跟拍'} as const
export interface StoryboardShot {
  id: string; scene_index: number; description: string; shot_size: keyof typeof shotSizeLabels
  camera_movement: keyof typeof cameraMovementLabels; duration_seconds: number; dialogue: string
}
export interface StoryboardPlan { provider?: string; source_script_version: number; shots: StoryboardShot[] }
export interface StoryboardGeneration {
  id: string; space_id: string; project_id: string; base_version: number; source_script_version: number
  provider: string; status: ScriptGeneration['status']; progress: number
  created_at: string; updated_at: string; error?: string; error_code?: string
}
export const storyboardGenerationLabels: Record<StoryboardGeneration['status'], string> = {
  queued: '等待生成分镜', running: '正在生成分镜', succeeded: '分镜已生成', failed: '分镜生成失败',
}
export function storyboardDraftError(shots: StoryboardShot[]) {
  if (shots.length < 1 || shots.length > 30) return translate("分镜计划需包含 1–30 个镜头。")
  const ids = new Set<string>()
  for (const [index, shot] of shots.entries()) {
    const label = translate("第 {p0} 镜", {p0: index + 1})
    if (!/^[A-Za-z0-9_-]{1,40}$/.test(shot.id) || ids.has(shot.id)) return translate("{p0}标识无效或重复。", {p0: label})
    ids.add(shot.id)
    if (!Number.isInteger(shot.scene_index) || shot.scene_index < 1 || shot.scene_index > 3) return translate("{p0}需引用第 1–3 场。", {p0: label})
    const textError = fieldLengthError(shot.description, 1, 1000, translate("{p0}画面描述", {p0: label})) || fieldLengthError(shot.dialogue, 0, 2000, translate("{p0}对白", {p0: label}))
    if (textError) return textError
    if (!Object.hasOwn(shotSizeLabels, shot.shot_size) || !Object.hasOwn(cameraMovementLabels, shot.camera_movement)) return translate("{p0}景别或运镜无效。", {p0: label})
    if (!Number.isInteger(shot.duration_seconds) || shot.duration_seconds < 1 || shot.duration_seconds > 60) return translate("{p0}时长需为 1–60 秒的整数。", {p0: label})
  }
  return ''
}
