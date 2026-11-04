import {describe, expect, it} from 'vitest'
import {initialRuntimeChoice, runtimeFields, runtimeReady, type RuntimeModelOption} from './runtimeSelection'
const model: RuntimeModelOption = {id:'synthetic-config',version:4,provider:'gateway-text',gateway_id:'gateway',model:'synthetic',capability:'text',is_default:true,available:true}
describe('model execution without cost management', () => {
  it('uses an available model without a quote or cost consent', () => {
    const choice = initialRuntimeChoice(model)
    expect(runtimeReady(choice)).toBe(true)
    expect(runtimeFields(choice)).toEqual({model_config_id:model.id,model_config_version:4})
    expect(runtimeFields(null)).toEqual({})
    expect(runtimeReady(initialRuntimeChoice({...model,quote:{currency:'USD',maximum_minor:10,minor_units:2,source:'Legacy',expires_at:'2000-01-01T00:00:00Z',contract:'legacy'}}))).toBe(true)
  })
  it('keeps availability, capability and speech voice checks', () => {
    expect(runtimeReady(initialRuntimeChoice({...model,available:false}))).toBe(false)
    expect(runtimeReady(initialRuntimeChoice(model),{operation:'image',input:{}})).toBe(false)
    const speech = initialRuntimeChoice({...model,capability:'speech'})
    expect(runtimeReady(speech)).toBe(false)
    expect(runtimeReady({...speech,voice:'synthetic-voice'})).toBe(true)
  })
})
