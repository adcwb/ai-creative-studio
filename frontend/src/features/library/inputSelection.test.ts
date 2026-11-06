import {expect,it} from 'vitest'
import {libraryInputPayload,type LibraryInputSelection} from './inputSelection'
it('sends explicit binding choices while leaving preset identity and authority resolution to the server',()=>{
 const inputs:LibraryInputSelection[]=[{binding_id:'voice-bound',mode:'native_preset',nativePreset:{model_config_id:'model',provider:'gateway-speech',model:'speech',voice:'voice'}},{binding_id:'style-bound',mode:'reference',media_asset_ids:['copied-image']}]
 const payload=libraryInputPayload(inputs)
 expect(payload).toEqual([{binding_id:'voice-bound',mode:'native_preset'},{binding_id:'style-bound',mode:'reference',media_asset_ids:['copied-image']}])
 payload[1].media_asset_ids!.push('other')
 expect(inputs[1].media_asset_ids).toEqual(['copied-image'])
})
