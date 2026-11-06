import type {DramaProject,StoryboardShot} from './drama'
export interface PreviewChoice {shot_id:string;asset_id:string;audio_asset_id?:string;duration:number}
export interface PreviewInput {expected_project_version:number;expected_media_version:number;episode_number:number;aspect_ratio:string;sound_mode:'silent'|'temporary';shots:PreviewChoice[]}
export interface PreviewAsset {id:string;kind:string;name:string;file_url:string;duration:number;role:string}
export interface PreviewMedia {version:number;assets:PreviewAsset[];selections?:{shot_id:string;asset_id:string}[];aspect_ratio?:string}
export interface PreviewSource {created_at:string;sha256:string;project_version:number;script_version:number;storyboard_script_version:number;storyboard_sha256:string;media_version:number;episode_number:number;sound_mode:string;shots:{shot:StoryboardShot;original_index:number;duration:number;asset_id:string;audio_asset_id?:string}[];assets:{id:string;name:string;kind:string;sha256:string;size:number}[]}
export interface PreviewSummary {id:string;purpose:'animatic';status:string;file_url:string;duration:number;error:string;resume_available:boolean;created_at:string;project_version:number;media_version:number;episode_number:number;sound_mode:string;source_sha256:string;render_progress?:{completed:number;total:number;phase:string}}
export interface PreviewDetail {id:string;purpose:'animatic';status:string;file_url:string;duration:number;preview_source:PreviewSource;timeline:unknown}
export type PreviewPlan=Pick<DramaProject,'id'|'version'|'script_version'|'series_episode_id'|'storyboard'>
export function eligiblePreviewShots(project:PreviewPlan,episode:number,shotEpisodes:Record<string,number>){return project.storyboard?.shots.filter(shot=>{const n=shotEpisodes[shot.id]??-1;return n===0||n===episode})??[]}
export function adoptedPreviewChoices(project:PreviewPlan,media:PreviewMedia,episode:number,shotEpisodes:Record<string,number>):PreviewChoice[]{
 return eligiblePreviewShots(project,episode,shotEpisodes).flatMap(shot=>{
  // Independent legacy shots require explicit addition to this episode.
  if(shotEpisodes[shot.id]===0&&project.script_version>0)return []
  const chosen=media.selections?.filter(v=>v.shot_id===shot.id).at(-1),asset=media.assets.find(v=>v.id===chosen?.asset_id&&v.kind==='image'&&v.role!=='mask')
  return asset?[{shot_id:shot.id,asset_id:asset.id,duration:shot.duration_seconds}]:[]
 })
}
