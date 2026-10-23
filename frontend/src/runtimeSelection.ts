export interface RuntimeModelOption {
  id: string; version: number; provider: string; model: string; capability: string; is_default: boolean; available: boolean
  quote?: {currency: string; maximum_minor: number; minor_units: number; source: string; expires_at: string; contract: string}
}
export interface RuntimeChoice { model: RuntimeModelOption; voice: string; confirmed: boolean; quoteReady: boolean }
export function runtimeFields(choice: RuntimeChoice | null) {
  return choice ? {model_config_id: choice.model.id, model_config_version: choice.model.version, ...(choice.voice ? {voice: choice.voice} : {}), ...(choice.model.quote ? {budget_currency: choice.model.quote.currency, budget_maximum_minor: choice.model.quote.maximum_minor} : {})} : {}
}
export function runtimeReady(choice: RuntimeChoice | null) {
  return !choice || (choice.model.available && choice.confirmed && choice.quoteReady && !!choice.model.quote && Date.parse(choice.model.quote.expires_at) > Date.now() && (choice.model.capability !== 'speech' || !!choice.voice.trim()))
}

export function initialRuntimeChoice(model: RuntimeModelOption): RuntimeChoice { return {model, voice: "", confirmed: false, quoteReady: !!model.quote && Date.parse(model.quote.expires_at) > Date.now()} }
