export interface AIGateway {id: string; name: string; base_url: string; enabled: boolean; timeout_seconds: number; version: number; secret_status: string; updated_at?: string}
export interface GatewayModel {id: string; gateway_id: string; name: string; model: string; capabilities: string[]; enabled: boolean; version: number; max_output_tokens: number; json_output: boolean; video_seconds: number; video_size: string; currency: string; maximum_minor: number; price_source: string; updated_at?: string}
export interface FeatureDefinition {key: string; name: string; capability: string}
export interface FeatureBinding {feature_key: string; model_config_id: string; enabled: boolean; allow_override: boolean; voice?: string; version: number; updated_by?: string; updated_at?: string}
export interface ModelManagementReport {version: number; gateways: AIGateway[]; models: GatewayModel[]; features: FeatureDefinition[]; bindings: FeatureBinding[]; secret_storage_available: boolean; governance_ready: boolean; legacy_models: number}
export const capabilityNames: Record<string, string> = {text: '文本推理', vision: '视觉理解', image: '图像生成', image_edit: '图片编辑', video: '视频生成', speech: '语音合成', asr: '语音转写', asr_timestamps: '带时间戳转写'}
export const readinessNames: Record<string, string> = {gateway_disabled: '网关已停用', gateway_credential_missing: '未配置可用凭据', gateway_price_missing: '未配置单次费用上限', gateway_governance_missing: '费用与审核未就绪', role_denied: '没有执行权限'}
export function featureModelStatus(report: ModelManagementReport, modelID: string, capability: string) {
  const model = report.models.find(item => item.id === modelID)
  if (!model) return '请选择模型'
  const gateway = report.gateways.find(item => item.id === model.gateway_id)
  if (!gateway?.enabled) return '网关已停用'
  if (!model.enabled) return '模型已停用'
  if (!model.capabilities.includes(capability)) return '模型能力不匹配'
  if (gateway.secret_status !== 'encrypted' || !report.secret_storage_available) return '未配置可用凭据'
  if (!model.maximum_minor || !model.price_source.trim()) return '未配置单次费用上限'
  if (!report.governance_ready) return '费用与审核未就绪'
  return '可执行'
}
export function previewFeature(preview?: {operation: string; input: Record<string, unknown>}) {
  if (!preview) return ''
  const direct: Record<string, string> = {script: 'script_generation', storyboard: 'storyboard_planning', analysis: 'brief_analysis', image: 'image_generation'}
  if (direct[preview.operation]) return direct[preview.operation]
  if (preview.operation === 'alignment') return preview.input.mode === 'transcription' ? 'speech_transcription' : 'subtitle_alignment'
  const input = preview.input
  if (input.kind === 'image') return input.reference_asset_id || input.mask_asset_id || (Array.isArray(input.reference_asset_ids) && input.reference_asset_ids.length) ? 'image_edit' : input.character ? 'character_image' : input.shot_id ? 'shot_image' : 'image_generation'
  return ({analysis: 'brief_analysis', video: 'shot_video', speech: 'speech_synthesis', vision: 'vision_analysis', consistency: 'consistency_check'} as Record<string, string>)[String(input.kind)] ?? ''
}
