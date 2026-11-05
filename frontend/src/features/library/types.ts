export type LibraryKind = 'character' | 'product' | 'scene' | 'style' | 'voice'
export interface LibraryCard {
  name: string; description: string; prohibited_changes: string[]
  character?: {appearance: string; hair: string; costume: string; expression: string; voice_notes: string}
  product?: {model: string; dimensions: string; materials: string; packaging: string; exact_text: string}
  scene?: {layout: string; objects: string; lighting: string; viewpoints: string}
  style?: {palette:string;linework:string;composition:string;lighting:string;texture:string}
  voice?: {timbre:string;performance:string;pace:string;pronunciation:string}
}
export interface LibraryReference {id: string; role: string; view_angle: string; mime_type: string; width: number; height: number; size_bytes: number; content_sha256: string; source_project_id: string; source_asset_id: string}
export interface LibraryAsset {id: string; space_id: string; kind: LibraryKind; name: string; status: 'active' | 'archived'; head_version_id: string; created_at: string; updated_at: string}
export interface LibraryVersion {id: string; asset_id: string; space_id: string; number: number; card: LibraryCard; references: LibraryReference[]; content_sha256: string; created_at: string; created_by: string}
export interface LibraryResult {asset: LibraryAsset; version: LibraryVersion}
export type ReferenceInput = {role: string; view_angle: string} & ({project_id: string; asset_id: string; library_version_id?: never; library_reference_id?: never} | {project_id?: never; asset_id?: never; library_version_id: string; library_reference_id: string})
export interface LibraryWrite {kind: LibraryKind; expected_asset_version_id?: string; card: LibraryCard; references: ReferenceInput[]}
export interface Page<T> {items: T[]; next_cursor: string}
export const kindLabels: Record<LibraryKind, string> = {character: '角色卡', product: '商品卡', scene: '场景卡', style:'风格卡', voice:'声音卡'}
export const cardFields: Record<LibraryKind, readonly [string, string][]> = {
  character: [['appearance', '外观特征'], ['hair', '发型'], ['costume', '服装'], ['expression', '表情参考'], ['voice_notes', '声音描述']],
  product: [['model', '商品型号'], ['dimensions', '尺寸比例'], ['materials', '材质'], ['packaging', '包装'], ['exact_text', '准确文字']],
  scene: [['layout', '空间布局'], ['objects', '关键物件'], ['lighting', '光照'], ['viewpoints', '机位参考']],
  style:[['palette','色彩与色板'],['linework','线条与造型'],['composition','构图与画面节奏'],['lighting','风格光照'],['texture','材质与画面质感']],
  voice:[['timbre','音色意图'],['performance','表演与情绪'],['pace','语速与停顿'],['pronunciation','发音与专名']],
}
export function emptyCard(kind: LibraryKind): LibraryCard {
  const common = {name: '', description: '', prohibited_changes: [] as string[]}
  if (kind === 'character') return {...common, character: {appearance: '', hair: '', costume: '', expression: '', voice_notes: ''}}
  if (kind === 'product') return {...common, product: {model: '', dimensions: '', materials: '', packaging: '', exact_text: ''}}
  if(kind==='style')return {...common,style:{palette:'',linework:'',composition:'',lighting:'',texture:''}}
  if(kind==='voice')return {...common,voice:{timbre:'',performance:'',pace:'',pronunciation:''}}
  return {...common, scene: {layout: '', objects: '', lighting: '', viewpoints: ''}}
}
