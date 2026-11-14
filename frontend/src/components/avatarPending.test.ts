import {afterEach,expect,test,vi} from 'vitest'
import {readAvatarAttempt,saveAvatarAttempt,type AvatarAttempt} from './avatarPending'
afterEach(()=>vi.unstubAllGlobals())
test('avatar recovery pins its owner scope, fixed API path and exact request body',()=>{
 const values=new Map<string,string>();vi.stubGlobal('sessionStorage',{getItem:(k:string)=>values.get(k),setItem:(k:string,v:string)=>values.set(k,v)})
 const attempt:AvatarAttempt={path:'/projects/owned/creative-jobs?space_id=team',body:JSON.stringify({kind:'digital_human_authorized',voice_authorization_id:'grant'}),mime:'application/json',key:'original-key-1',action:'job'}
 expect(saveAvatarAttempt('owner:team:owned',attempt)).toBe(true)
 expect(readAvatarAttempt('owner:team:owned','/projects/owned','space_id=team')).toEqual(attempt)
 expect(readAvatarAttempt('owner:team:owned','/projects/other','space_id=team')).toBeNull()
 expect(readAvatarAttempt('owner:team:owned','/projects/owned','space_id=other')).toBeNull()
 for(const path of ['/projects/owned/../other/creative-jobs?space_id=team','https://evil.invalid/projects/owned/creative-jobs?space_id=team']){
  values.set('owner:team:owned',JSON.stringify({...attempt,path}));expect(readAvatarAttempt('owner:team:owned','/projects/owned','space_id=team')).toBeNull()
 }
})
