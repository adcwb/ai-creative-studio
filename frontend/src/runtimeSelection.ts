export interface RuntimePreview {operation: 'image' | 'script' | 'storyboard' | 'creative' | 'analysis' | 'alignment'; project_id?: string; input: Record<string, unknown>}
export interface RuntimeModelOption {
  gateway_id?: string; gateway_name?: string; name?: string; capabilities?: string[]; reason_code?: string
  id: string; version: number; provider: string; model: string; capability: string; is_default: boolean; available: boolean
  quote?: {currency: string; maximum_minor: number; minor_units: number; source: string; expires_at: string; contract: string; request_sha256?: string}
}
export interface RuntimeChoice { model: RuntimeModelOption; voice: string; confirmed: boolean; quoteReady: boolean; fingerprint?: string }
export function runtimeFingerprint(preview: RuntimePreview, voice = '') {return JSON.stringify({preview, voice})}
export function runtimeFields(choice: RuntimeChoice | null) {
  return choice ? {model_config_id: choice.model.id, model_config_version: choice.model.version, ...(choice.voice ? {voice: choice.voice} : {}), ...(choice.model.quote ? {budget_currency: choice.model.quote.currency, budget_maximum_minor: choice.model.quote.maximum_minor, quote_request_sha256: choice.model.quote.request_sha256} : {})} : {}
}
export function runtimeReady(choice: RuntimeChoice | null, preview?: RuntimePreview) {
  return !choice || (choice.model.available && choice.confirmed && choice.quoteReady && !!choice.model.quote?.request_sha256 && (!preview || choice.fingerprint === runtimeFingerprint(preview, choice.voice)) && Date.parse(choice.model.quote.expires_at) > Date.now() && (choice.model.capability !== 'speech' || !!choice.voice.trim()))
}
export function initialRuntimeChoice(model: RuntimeModelOption): RuntimeChoice { return {model: {...model, quote: undefined}, voice: '', confirmed: false, quoteReady: false} }
