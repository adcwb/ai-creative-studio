import {request} from '../../api'
import {FileSHA256} from './sha256'
import {translate as t} from '../../i18n'
import type {UploadSession} from './ResumableMediaUpload'

// Browser Blobs may be disk backed. Read only bounded slices into JavaScript;
// retries reuse the accepted session/key rather than create another asset.
export async function uploadMediaBlob(space:string,project:string,blob:Blob,name:string,role:string,key:string,character='',shotID='') {
  const mime=blob.type==='audio/x-wav'?'audio/wav':blob.type
  if(!['image/png','image/jpeg','audio/wav','audio/mpeg','video/mp4'].includes(mime)||blob.size<1||!Number.isSafeInteger(blob.size))throw new Error(t('请选择有效的PNG、JPEG、MP4、WAV或MP3文件'))
  const hash=new FileSHA256()
  for(let offset=0;offset<blob.size;offset+=8*1024*1024){hash.update(new Uint8Array(await blob.slice(offset,offset+8*1024*1024).arrayBuffer()));await new Promise(resolve=>setTimeout(resolve,0))}
  const path=`/projects/${encodeURIComponent(project)}/media/upload-sessions`,query=`?${new URLSearchParams({space_id:space})}`
  let session=await request<UploadSession>(`${path}${query}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':key},body:JSON.stringify({name,mime_type:mime,size_bytes:blob.size,sha256:hash.hex(),role,character,shot_id:shotID})})
  if(session.state==='completed')return session
  if(session.state!=='receiving')throw new Error(t('上传会话已关闭，请重新选择文件开始'))
  while(session.offset<session.size_bytes){
    if(session.size_bytes!==blob.size||!Number.isSafeInteger(session.offset)||session.offset<0||!Number.isSafeInteger(session.chunk_size)||session.chunk_size<1||session.chunk_size>8*1024*1024)throw new Error(t('上传中断，请恢复原会话'))
    const offset=session.offset,part=blob.slice(offset,offset+session.chunk_size),digest=new FileSHA256()
    digest.update(new Uint8Array(await part.arrayBuffer()))
    session=await request<UploadSession>(`${path}/${session.id}/parts/${offset}${query}`,{method:'PUT',headers:{'Content-Type':'application/octet-stream','X-Chunk-SHA256':digest.hex(),'Idempotency-Key':`${session.id}-${offset}`},body:part})
    if(session.offset<=offset||session.offset>blob.size)throw new Error(t('上传中断，请恢复原会话'))
  }
  return request<UploadSession>(`${path}/${session.id}/complete${query}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':`complete-${session.id}`},body:'{}',timeoutMs:300000})
}
