import type {NativeVoicePreset} from './types'
export interface LibraryInputSelection {binding_id:string;mode:string;media_asset_ids?:string[];nativePreset?:NativeVoicePreset}
// Native metadata helps the UI explain compatibility. The server independently
// resolves the pinned binding; no browser metadata becomes an authority pin.
export const libraryInputPayload=(values:LibraryInputSelection[])=>values.map(({binding_id,mode,media_asset_ids})=>({binding_id,mode,...(media_asset_ids?.length?{media_asset_ids:[...media_asset_ids]}:{})}))
