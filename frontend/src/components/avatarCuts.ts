export interface AvatarSplitForm {mode:'pause'|'fixed'|'manual';manual:string;approved?:string}
export interface AvatarSegmentProgress {index:number;start_seconds:number;duration_seconds:number;status:string;cut_reason?:string;reused?:boolean}
export const defaultAvatarSplit:AvatarSplitForm={mode:'pause',manual:''}
export function avatarSplitInput(value:AvatarSplitForm){
 const tokens=value.manual.trim()?value.manual.trim().split(/[,，\s]+/):[]
 const cuts=tokens.map(Number)
 const valid=value.mode!=='manual'||cuts.every((v,i)=>Number.isFinite(v)&&v>0&&(i===0||v>cuts[i-1]))
 return {valid,input:{avatar_split_mode:value.mode,...(value.mode==='manual'?{avatar_cut_points:cuts}:{}),...(value.approved?{avatar_plan_sha256:value.approved}:{})}}
}
export function avatarCutLabel(reason?:string){return ({pause:'停顿候选',manual:'人工切点',window:'固定窗口',end:'音频结尾'} as Record<string,string>)[reason??'']??'固定窗口'}
