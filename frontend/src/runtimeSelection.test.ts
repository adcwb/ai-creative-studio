import { describe, expect, it } from 'vitest'
import { initialRuntimeChoice, runtimeFields, runtimeReady, runtimeFingerprint, type RuntimeModelOption } from './runtimeSelection'
const model: RuntimeModelOption = {id: 'synthetic-config', version: 4, provider: 'openai-text', model: 'synthetic', capability: 'text', is_default: true, available: true,
  quote: {currency: 'JPY', maximum_minor: 25, minor_units: 0, source: 'synthetic fixture', expires_at: '2999-01-01T00:00:00Z', contract: 'text-64k-6000-v1'}}
describe('explicit model cost consent', () => {
 it('does not convert a default into confirmed spending', () => {
  expect(runtimeFields(null)).toEqual({})
  const choice = initialRuntimeChoice(model)
  expect(runtimeReady(choice)).toBe(false)
  expect(runtimeReady({...choice, confirmed: true})).toBe(false)
  expect(runtimeFields(choice)).toEqual({model_config_id: model.id, model_config_version: 4})
 })
 it('rejects unknown, expired and disabled prices without currency conversion', () => {
  for (const next of [{...model, quote: undefined}, {...model, available: false}, {...model, quote: {...model.quote!, expires_at: '2000-01-01T00:00:00Z'}}]) {
   expect(runtimeReady({...initialRuntimeChoice(next), confirmed: true})).toBe(false)
  }
  const preview = {operation: 'image' as const, input: {prompt: 'synthetic'}}
  const quoted = {...initialRuntimeChoice(model), model: {...model, quote: {...model.quote!, currency: 'KWD', maximum_minor: 1250, minor_units: 3, request_sha256: 'a'.repeat(64)}}, quoteReady: true, confirmed: true, fingerprint: runtimeFingerprint(preview)}
  expect(runtimeFields(quoted)).toMatchObject({budget_currency: 'KWD', budget_maximum_minor: 1250, quote_request_sha256: 'a'.repeat(64)})
  expect(runtimeReady(quoted, preview)).toBe(true)
  expect(runtimeReady(quoted, {...preview, input: {prompt: 'changed'}})).toBe(false)
 })
})
