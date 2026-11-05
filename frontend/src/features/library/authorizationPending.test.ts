import {beforeEach,expect,it} from 'vitest'
import {pendingKey,readAuthorizationPending} from './authorizationPending'
const values=new Map<string,string>()
beforeEach(()=>{values.clear();Object.defineProperty(globalThis,'sessionStorage',{configurable:true,value:{getItem:(key:string)=>values.get(key)??null,setItem:(key:string,v:string)=>values.set(key,v),removeItem:(key:string)=>values.delete(key)}})})
it('recovers only the exact asset and space original authorization body and key',()=>{
 const body=JSON.stringify({expected_revision:0,status:'granted',version_ids:['v1'],evidence:'Synthetic permission'})
 const pending={key:'original-key-001',body}
 sessionStorage.setItem(pendingKey('space','asset'),JSON.stringify(pending))
 expect(readAuthorizationPending('space','asset')).toEqual(pending)
 expect(readAuthorizationPending('other','asset')).toBeNull()
 expect(readAuthorizationPending('space','other')).toBeNull()
})
it('rejects oversized UTF8 requests, malformed keys and corrupt storage',()=>{
 for(const v of [{key:'short',body:'{}'},{key:'original-key-001',body:'字'.repeat(90000)},{key:'original-key-001',body:42}]){sessionStorage.setItem(pendingKey('space','asset'),JSON.stringify(v));expect(readAuthorizationPending('space','asset')).toBeNull()}
 sessionStorage.setItem(pendingKey('space','asset'),'invalid json');expect(readAuthorizationPending('space','asset')).toBeNull()
})
