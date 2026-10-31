import { describe, expect, it } from 'vitest'
import { featureModelStatus, previewFeature, type ModelManagementReport } from './gatewayModels'
const report: ModelManagementReport = {version: 1, gateways: [{id: 'a', name: 'First', base_url: 'https://a.example/v1', enabled: true, secret_status: 'encrypted', timeout_seconds: 120, version: 1}], models: [{id: 'model-a', gateway_id: 'a', name: 'Shared name', model: 'vendor/model', capabilities: ['text'], enabled: true, version: 1, max_output_tokens: 3000, json_output: true, video_seconds: 4, video_size: '1280x720', currency: 'USD', maximum_minor: 10, price_source: 'Synthetic test bound'}], bindings: [], features: [], secret_storage_available: true, governance_ready: true, legacy_models: 0}
describe('gateway model routing', () => {
  it('derives features from actual creative input', () => {
    expect(previewFeature({operation: 'storyboard', input: {}})).toBe('storyboard_planning')
    expect(previewFeature({operation: 'creative', input: {kind: 'image', character: 'A'}})).toBe('character_image')
    expect(previewFeature({operation: 'creative', input: {kind: 'image', shot_id: 'shot-1'}})).toBe('shot_image')
    expect(previewFeature({operation: 'creative', input: {kind: 'image', shot_id: 'shot-1', reference_asset_id: 'ref'}})).toBe('image_edit')
    expect(previewFeature({operation: 'alignment', input: {mode: 'transcription'}})).toBe('speech_transcription')
    expect(previewFeature({operation: 'alignment', input: {mode: 'asr'}})).toBe('subtitle_alignment')
  })
  it('reports concrete execution blockers instead of assuming configuration is live', () => {
    expect(featureModelStatus(report, 'model-a', 'text')).toBe('可执行')
    expect(featureModelStatus(report, 'model-a', 'image')).toBe('模型能力不匹配')
    expect(featureModelStatus({...report, governance_ready: false}, 'model-a', 'text')).toBe('费用与审核未就绪')
    expect(featureModelStatus({...report, gateways: report.gateways.map(item => ({...item, enabled: false}))}, 'model-a', 'text')).toBe('网关已停用')
    expect(featureModelStatus({...report, secret_storage_available: false}, 'model-a', 'text')).toBe('未配置可用凭据')
  })
})
