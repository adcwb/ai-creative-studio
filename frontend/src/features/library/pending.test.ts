import {afterEach,expect,it,vi} from 'vitest'
import {readPendingLibrary,savePendingLibrary,readPendingBinding,savePendingBinding} from './pending'
function storage(){const values=new Map<string,string>();vi.stubGlobal('sessionStorage',{getItem:(k:string)=>values.get(k)??null,setItem:(k:string,v:string)=>values.set(k,v),removeItem:(k:string)=>values.delete(k)})}
afterEach(()=>vi.unstubAllGlobals())
it('keeps the exact original large library request and scopes recovery to its space',()=>{
 storage()
 const body=JSON.stringify({kind:'style',card:{name:'雨夜风格',description:'',prohibited_changes:[],style:{palette:'青灰',linework:'',composition:'',lighting:'',texture:''}},references:Array.from({length:800},(_,i)=>({project_id:'source-project',asset_id:`image-${i}`,role:'reference',view_angle:`exploration-${i}`}))})
 expect(new TextEncoder().encode(body).length).toBeGreaterThan(65536)
 const intent={path:'/library/assets',key:'library-original-intent',body}
 savePendingLibrary('space-a',intent)
 expect(readPendingLibrary('space-a')).toEqual(intent)
 expect(readPendingLibrary('space-b')).toBeNull()
 savePendingBinding('space-a','project-a',{key:'binding-original-intent',body})
 expect(readPendingBinding('space-a','project-a')?.body).toBe(body)
 expect(readPendingBinding('space-a','project-b')).toBeNull()
})
it('enforces the UTF-8 request working set and the known write paths when restoring',()=>{
 storage()
 const tooLarge='界'.repeat(400000)
 expect(tooLarge.length).toBeLessThan(1<<20)
 savePendingLibrary('space-a',{path:'/library/assets',key:'original-large-intent',body:tooLarge})
 expect(readPendingLibrary('space-a')).toBeNull()
 savePendingLibrary('space-a',{path:'/library/assets/id/unknown-operation',key:'original-known-intent',body:'{}'})
 expect(readPendingLibrary('space-a')).toBeNull()
})
