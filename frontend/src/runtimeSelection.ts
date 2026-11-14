export interface RuntimePreview {operation: 'prompt_optimization' | 'story_continuity' | 'image' | 'script' | 'storyboard' | 'creative' | 'analysis' | 'alignment'; project_id?: string; input: Record<string, unknown>}
export interface RuntimeModelOption {
  gateway_id?: string; gateway_name?: string; name?: string; capabilities?: string[]; reason_code?: string
  id: string; version: number; provider: string; model: string; capability: string; is_default: boolean; available: boolean
  quote?: {currency: string; maximum_minor: number; minor_units: number; source: string; expires_at: string; contract: string; request_sha256?: string}
}
export interface RuntimeChoice {model: RuntimeModelOption; voice: string}
export function runtimeFields(choice: RuntimeChoice | null) {
  return choice ? {model_config_id: choice.model.id, model_config_version: choice.model.version, ...(choice.voice ? {voice: choice.voice} : {})} : {}
}
export function runtimeReady(choice: RuntimeChoice | null, preview?: RuntimePreview) {
  if (!choice) return true
  const operation = preview?.operation === 'creative' ? String(preview.input.kind) : preview?.operation
  const required = ({story_continuity:'text',image:'image',script:'text',storyboard:'text',analysis:'text',text:'text',vision:'vision',consistency:'vision',video:'video',speech:'speech',alignment:'asr'} as Record<string,string>)[operation ?? '']
  return choice.model.available && (!required || choice.model.capability === required) && (choice.model.capability !== 'speech' || !!choice.voice.trim())
}
export function initialRuntimeChoice(model: RuntimeModelOption): RuntimeChoice {return {model, voice: ''}}
