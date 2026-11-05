export interface OutputProfile {duration_seconds: number; output_resolution: '1080p'|'1440p'|'1800p'|'2160p'; aspect_ratio: '16:9'|'9:16'|'1:1'}
export interface SeriesEntry {
 id: string; space_id: string; kind: 'series'|'season'|'episode'; parent_id?: string; series_id: string
 ordinal?: number; next_ordinal: number; row_version: number; title: string; summary: string; audience?: string; format?: string
 profile: OutputProfile; status: string; state: 'ready'; project_id?: string; project_version?: number
 created_by: string; created_at: string; updated_at: string
}
export interface EpisodeContext {series: SeriesEntry; season: SeriesEntry; episode: SeriesEntry}
export interface DirectoryPage {items: SeriesEntry[]; next_cursor: string}
export const defaultProfile: OutputProfile = {duration_seconds: 90, output_resolution:'1080p', aspect_ratio:'9:16'}
export const episodeStatus: Record<string,string> = {planned:'待创作',writing:'剧本创作中',storyboard:'分镜设计中',production:'镜头制作中',review:'审片中',released:'已交付'}
export function directoryURL(path: string, space: string, cursor='') {return `${path}${path.includes('?')?'&':'?'}${new URLSearchParams({space_id:space,...(cursor?{cursor}:{})})}`}
