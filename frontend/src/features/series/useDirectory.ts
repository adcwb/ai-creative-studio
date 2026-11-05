import {useEffect,useRef,useState} from 'react'
import {ApiError,errorMessage,request} from '../../api'
import {translate} from '../../i18n'
import {directoryURL,type SeriesEntry} from './types'

// Mount with a path/space key. Revision refresh preserves visible rows and drafts.
export function useDirectory<T extends {id:string}=SeriesEntry>(path:string,space:string,revision=0){
 type Page={items:T[];next_cursor:string}
 const [data,setData]=useState<Page>({items:[],next_cursor:''})
 const [finished,setFinished]=useState(-1)
 const [error,setError]=useState('')
 const [more,setMore]=useState(false)
 const controllers=useRef(new Set<AbortController>())
 const inFlight=useRef(false)
 useEffect(()=>{
  const controller=new AbortController();controllers.current.add(controller)
  void request<Page>(directoryURL(path,space),{signal:controller.signal}).then(result=>{if(!controller.signal.aborted){setData(result);setError('');setFinished(revision)}}).catch(reason=>{if(!controller.signal.aborted){setError(errorMessage(reason,translate('目录读取失败')));setFinished(revision)}})
  const active=controllers.current
  return ()=>{for(const c of active)c.abort();active.clear();inFlight.current=false}
 },[path,space,revision])
 async function loadMore(){
  if(inFlight.current||!data.next_cursor||finished!==revision)return
  inFlight.current=true;setMore(true)
  const controller=new AbortController();controllers.current.add(controller)
  try{const page=await request<Page>(directoryURL(path,space,data.next_cursor),{signal:controller.signal});if(!controller.signal.aborted){setData(old=>({items:[...old.items,...page.items.filter(n=>!old.items.some(v=>v.id===n.id))],next_cursor:page.next_cursor}));setError('')}}
  catch(reason){if(!controller.signal.aborted){setError(errorMessage(reason,translate('目录读取失败')));if(reason instanceof ApiError&&reason.status===409)setData(old=>({...old,next_cursor:''}))}}
  finally{controllers.current.delete(controller);if(!controller.signal.aborted){inFlight.current=false;setMore(false)}}
 }
 return {...data,error,loading:finished!==revision,more,loadMore}
}
