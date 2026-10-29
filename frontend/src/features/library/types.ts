export type LibraryKind = 'character' | 'product' | 'scene'
export interface LibraryCard {
  name: string; description: string; prohibited_changes: string[]
  character?: {appearance: string; hair: string; costume: string; expression: string; voice_notes: string}
  product?: {model: string; dimensions: string; materials: string; packaging: string; exact_text: string}
  scene?: {layout: string; objects: string; lighting: string; viewpoints: string}
}
export interface LibraryReference {id: string; role: string; view_angle: string; mime_type: string; width: number; height: number; size_bytes: number; content_sha256: string; source_project_id: string; source_asset_id: string}
export interface LibraryAsset {id: string; space_id: string; kind: LibraryKind; name: string; status: 'active' | 'archived'; head_version_id: string; created_at: string; updated_at: string}
export interface LibraryVersion {id: string; asset_id: string; space_id: string; number: number; card: LibraryCard; references: LibraryReference[]; content_sha256: string; created_at: string; created_by: string}
export interface LibraryResult {asset: LibraryAsset; version: LibraryVersion}
export type ReferenceInput = {role: string; view_angle: string} & ({project_id: string; asset_id: string; library_version_id?: never; library_reference_id?: never} | {project_id?: never; asset_id?: never; library_version_id: string; library_reference_id: string})
export interface LibraryWrite {kind: LibraryKind; expected_asset_version_id?: string; card: LibraryCard; references: ReferenceInput[]}
export interface Page<T> {items: T[]; next_cursor: string}
export const kindLabels: Record<LibraryKind, string> = {character: '角色卡', product: '商品卡', scene: '场景卡'}
export const cardFields: Record<LibraryKind, readonly [string, string][]> = {
  character: [['appearance', '外观特征'], ['hair', '发型'], ['costume', '服装'], ['expression', '表情参考'], ['voice_notes', '声音描述']],
  product: [['model', '商品型号'], ['dimensions', '尺寸比例'], ['materials', '材质'], ['packaging', '包装'], ['exact_text', '准确文字']],
  scene: [['layout', '空间布局'], ['objects', '关键物件'], ['lighting', '光照'], ['viewpoints', '机位参考']],
}
export function emptyCard(kind: LibraryKind): LibraryCard {
  const common = {name: '', description: '', prohibited_changes: [] as string[]}
  if (kind === 'character') return {...common, character: {appearance: '', hair: '', costume: '', expression: '', voice_notes: ''}}
  if (kind === 'product') return {...common, product: {model: '', dimensions: '', materials: '', packaging: '', exact_text: ''}}
  return {...common, scene: {layout: '', objects: '', lighting: '', viewpoints: ''}}
}
