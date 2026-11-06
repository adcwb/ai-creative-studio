import {afterEach,expect,it,vi} from 'vitest'
import {createHash} from 'node:crypto'
import {request} from '../../api'
import {uploadMediaBlob} from './uploadBlob'
vi.mock('../../api',()=>({request:vi.fn()}))
const send=vi.mocked(request)
afterEach(()=>{vi.restoreAllMocks();send.mockReset()})

it('uploads a Blob above 32MiB without reading it whole and preserves exact SHA and receipt keys',async()=>{
  const bytes=new Uint8Array(33*1024*1024+1),blob=new Blob([bytes],{type:'image/png'}),sha=createHash('sha256').update(bytes).digest('hex')
  const read=Blob.prototype.arrayBuffer
  vi.spyOn(Blob.prototype,'arrayBuffer').mockImplementation(function(this:Blob){expect(this.size).toBeLessThanOrEqual(8*1024*1024);return read.call(this)})
  const session={id:'u',state:'receiving',size_bytes:blob.size,offset:0,chunk_size:8*1024*1024,uploaded_asset_id:'asset'}
  const offsets:number[]=[]
  send.mockImplementation(async(path,options)=>{
    if(path.includes('/parts/')){const offset=Number(path.split('/parts/')[1].split('?')[0]);expect(offset).toBe(session.offset);offsets.push(offset);session.offset+=(options?.body as Blob).size;return {...session} as never}
    if(path.includes('/complete')){expect(session.offset).toBe(blob.size);expect((options?.headers as Record<string,string>)['Idempotency-Key']).toBe('complete-u');return {...session,state:'completed'} as never}
    expect(JSON.parse(String(options?.body))).toMatchObject({sha256:sha,size_bytes:blob.size,role:'mask'});expect((options?.headers as Record<string,string>)['Idempotency-Key']).toBe('original');return {...session} as never
  })
  expect(await uploadMediaBlob('space','project',blob,'mask.png','mask','original')).toMatchObject({state:'completed',uploaded_asset_id:'asset'})
  expect(offsets).toEqual([0,8388608,16777216,25165824,33554432])
},15000)

it('recovers an accepted chunk with a lost response under the original creation key',async()=>{
  const blob=new Blob([new Uint8Array(32)],{type:'image/png'}),creates:string[]=[],parts:number[]=[]
  const session={id:'u',state:'receiving',size_bytes:blob.size,offset:0,chunk_size:16,uploaded_asset_id:'a'}
  let lost=true
  send.mockImplementation(async(path,options)=>{
    if(path.includes('/parts/')){parts.push(session.offset);session.offset+=16;if(lost){lost=false;throw new TypeError('Lost response')}return {...session} as never}
    if(path.includes('/complete'))return {...session,state:'completed'} as never
    creates.push(JSON.stringify(options));return {...session} as never
  })
  await expect(uploadMediaBlob('s','p',blob,'source.png','reference','original')).rejects.toThrow('Lost response')
  expect(await uploadMediaBlob('s','p',blob,'source.png','reference','original')).toMatchObject({uploaded_asset_id:'a'})
  expect(creates[0]).toBe(creates[1]);expect(parts).toEqual([0,16])
})
