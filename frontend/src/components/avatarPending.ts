export interface AvatarAttempt {path:string;body:BodyInit;mime:string;key:string;action:'profile'|'job'|'upload'|'revoke'|'cancel'|'resume'}
export function readAvatarAttempt(storage:string,prefix:string,query:string):AvatarAttempt|null{
 try{
  const raw=sessionStorage.getItem(storage);if(!raw||raw.length>65536)return null
  const p=JSON.parse(raw) as AvatarAttempt
  if(p.mime!=='application/json'||typeof p.body!=='string'||!/^[A-Za-z0-9_-]{8,128}$/.test(p.key))return null
  const suffix=({profile:'/digital-humans',job:'/creative-jobs'} as Record<string,string>)[p.action]
  const endpoint=p.path.split('?')[0],tail=endpoint.slice(prefix.length)
  if(!endpoint.startsWith(prefix)||p.path!==endpoint+'?'+query)return null
  if(suffix?tail!==suffix:!((p.action==='revoke'&&/^\/digital-humans\/[A-Za-z0-9_-]+\/revoke$/.test(tail))||((p.action==='cancel'||p.action==='resume')&&new RegExp('^/creative-jobs/[A-Za-z0-9_-]+/'+p.action+'$').test(tail))))return null
  const body:unknown=JSON.parse(p.body);if(!body||typeof body!=='object'||Array.isArray(body))return null
  return p
 }catch{return null}
}
export function saveAvatarAttempt(storage:string,attempt:AvatarAttempt){
 if(typeof attempt.body!=='string'||attempt.mime!=='application/json')return false
 try{sessionStorage.setItem(storage,JSON.stringify(attempt));return true}catch{return false}
}
export function clearAvatarAttempt(storage:string){try{sessionStorage.removeItem(storage)}catch{/* Same mounted request still clears in memory. */}}
