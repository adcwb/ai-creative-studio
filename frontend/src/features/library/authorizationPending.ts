export interface Pending {key:string;body:string}
export const pendingKey=(space:string,asset:string)=>`studio.library.authorization:${space}:${asset}`
export function readAuthorizationPending(space:string,asset:string):Pending|null {
 try {const v=JSON.parse(sessionStorage.getItem(pendingKey(space,asset))??'null') as Pending|null;return v&&typeof v.key==='string'&&/^[A-Za-z0-9_-]{8,128}$/.test(v.key)&&typeof v.body==='string'&&new TextEncoder().encode(v.body).length<=256<<10?v:null} catch{return null}
}
