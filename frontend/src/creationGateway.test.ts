import {describe, it, expect} from 'vitest'
import {creationCapability, creationPath, creationReady, gatewayCreationRequired} from './creationGateway'
import {initialRuntimeChoice, runtimeFingerprint, type RuntimeModelOption} from './runtimeSelection'
const preview = {operation: 'image' as const, input: {prompt: 'Synthetic local protocol fixture'}}
const model: RuntimeModelOption = {id:'image-model',gateway_id:'gateway',provider:'gateway-images',model:'vendor/image',version:1,capability:'image',available:true,is_default:true}
describe('gateway-only product creation', () => {
  it('never enables the default mock or an unconfirmed model', () => {
    expect(gatewayCreationRequired).toBe(true)
    expect(creationReady(null, preview)).toBe(false)
    expect(creationCapability(null, preview, {provider:'mock-svg',mode:'mock',ready:true,message:''}).ready).toBe(false)
    expect(creationReady(initialRuntimeChoice(model), preview)).toBe(false)
  })
  it('requires a gateway, exact fresh quote and explicit consent', () => {
    const choice = {...initialRuntimeChoice(model),model:{...model,quote:{currency:'USD',maximum_minor:10,minor_units:2,source:'synthetic fixture',expires_at:'2999-01-01T00:00:00Z',contract:'studio-request-v1',request_sha256:'a'.repeat(64)}},quoteReady:true,confirmed:true,fingerprint:runtimeFingerprint(preview)}
    expect(creationReady(choice, preview)).toBe(true)
    expect(creationReady({...choice,model:{...choice.model,gateway_id:undefined}}, preview)).toBe(false)
    expect(creationReady({...choice,confirmed:false}, preview)).toBe(false)
    expect(creationReady(choice, {...preview,input:{prompt:'changed'}})).toBe(false)
  })
  it('uses strict creation and quote routes without changing reads or recovery', () => {
    for (const [path, expected] of [['/tasks?space_id=a','/gateway-tasks?space_id=a'],['/tasks/task/retry?space_id=a','/gateway-tasks/task/retry?space_id=a'],['/runtime-quote','/gateway-runtime-quote'],['/projects/p/generations','/projects/p/gateway-generations'],['/projects/p/storyboard-generations?space_id=a','/projects/p/gateway-storyboard-generations?space_id=a'],['/projects/p/creative-jobs','/projects/p/gateway-creative-jobs'],['/projects/p/analysis','/projects/p/gateway-analysis']]) expect(creationPath(path)).toBe(expected)
    expect(creationPath('/projects/p/creative-jobs/j/cancel')).toBe('/projects/p/creative-jobs/j/cancel')
    expect(creationPath('/projects/p/script')).toBe('/projects/p/script')
  })
})
