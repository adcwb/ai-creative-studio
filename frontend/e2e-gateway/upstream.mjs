import {execFileSync} from 'node:child_process'
import {mkdtempSync,readFileSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import http from 'node:http'
import {Buffer} from 'node:buffer'
import {createHash} from 'node:crypto'
import {deflateSync} from 'node:zlib'
let calls = 0
const requests = []
let video
function syntheticVideo(){if(!video){const dir=mkdtempSync(join(tmpdir(),'canvas-video-'));try{const file=join(dir,'synthetic.mp4');execFileSync('ffmpeg',['-hide_banner','-loglevel','error','-f','lavfi','-i','color=c=teal:s=256x256:r=12:d=2','-c:v','libx264','-pix_fmt','yuv420p','-movflags','+faststart',file]);video=readFileSync(file)}finally{rmSync(dir,{recursive:true,force:true})}}return video}
function wav() {
 const raw=Buffer.alloc(44+16000*2);raw.write('RIFF');raw.writeUInt32LE(raw.length-8,4);raw.write('WAVEfmt ',8);raw.writeUInt32LE(16,16);raw.writeUInt16LE(1,20);raw.writeUInt16LE(1,22);raw.writeUInt32LE(8000,24);raw.writeUInt32LE(16000,28);raw.writeUInt16LE(2,32);raw.writeUInt16LE(16,34);raw.write('data',36);raw.writeUInt32LE(raw.length-44,40);return raw
}
function png(width=64,height=64) {
 const chunk=(name,data)=>{const tag=Buffer.from(name), payload=Buffer.concat([tag,data]);let crc=0xffffffff;for(const b of payload){crc^=b;for(let i=0;i<8;i++)crc=(crc>>>1)^((crc&1)?0xedb88320:0)}const result=Buffer.alloc(data.length+12);result.writeUInt32BE(data.length);payload.copy(result,4);result.writeUInt32BE((crc^0xffffffff)>>>0,result.length-4);return result}
 const header=Buffer.alloc(13);header.writeUInt32BE(width);header.writeUInt32BE(height,4);header[8]=8;header[9]=2
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',header),chunk('IDAT',deflateSync(Buffer.alloc(height*(width*3+1),0))),chunk('IEND',Buffer.alloc(0))]).toString('base64')
}
http.createServer(async (req,res)=>{
 res.setHeader('Content-Type','application/json')
 if(req.url==='/__health'){res.end('{}');return}
 if(req.url==='/__state'){res.end(JSON.stringify({calls,requests}));return}
 calls++
 if(!['Bearer synthetic-key-A','Bearer synthetic-key-B'].includes(req.headers.authorization)){res.writeHead(401);res.end(JSON.stringify({error:'Synthetic authentication rejected'}));return}
 if(req.url==='/v1/videos/canvas-video/content'){requests.push({path:req.url,method:req.method});res.setHeader('Content-Type','video/mp4');res.end(syntheticVideo());return}
 if(req.url==='/v1/videos/canvas-video'){requests.push({path:req.url,method:req.method});res.end(JSON.stringify({id:'canvas-video',status:'completed'}));return}
 if(req.url==='/v1/models'){res.end(JSON.stringify({data:[{id:'synthetic-model'}]}));return}
 const chunks=[];for await(const part of req)chunks.push(part)
 const raw=Buffer.concat(chunks)
 let body
 if(req.headers['content-type']?.startsWith('multipart/form-data')){
  const data=await new globalThis.Request('http://fixture.invalid',{method:'POST',headers:{'Content-Type':req.headers['content-type']},body:raw}).formData()
  const images=data.getAll('image[]')
  body={model:data.get('model'),prompt:data.get('prompt'),size:data.get('size'),...(data.has('seconds')?{seconds:data.get('seconds')}:{}),image_hashes:await Promise.all(images.map(async image=>createHash('sha256').update(Buffer.from(await image.arrayBuffer())).digest('hex')))}
 } else body=JSON.parse(raw.toString()||'{}')
 requests.push({path:req.url,body})
 if(req.url==='/v1/videos'&&body.model==='synthetic-video'){res.end(JSON.stringify({id:'canvas-video',status:'queued'}));return}
 if(body.model==='synthetic-reject'){res.writeHead(429);res.end(JSON.stringify({error:'Synthetic upstream refusal'}));return}
 if(req.url==='/v1/audio/speech'&&body.model==='synthetic-speech'){res.setHeader('Content-Type','audio/wav');res.end(wav());return}
 if(req.url==='/v1/images/edits' && body.model==='synthetic-image' && body.image_hashes?.length){res.end(JSON.stringify({data:[{b64_json:png()}]}));return}
 if(req.url==='/v1/images/generations' && body.model==='synthetic-vertical-image'){if(body.size!=='864x1536'){res.writeHead(400);res.end(JSON.stringify({error:'Synthetic vertical size contract rejected'}));return}res.end(JSON.stringify({data:[{b64_json:png(864,1536)}]}));return}
 if(req.url==='/v1/images/generations' && body.model==='synthetic-image') {res.end(JSON.stringify({data:[{b64_json:png()}]}));return}
 if(req.url==='/v1/responses/input_tokens'&&body.model?.startsWith('synthetic-text')){
  res.end(JSON.stringify({object:'response.input_tokens',input_tokens:body.model.endsWith('-overflow')?4096:700}));return
 }
 if(req.url==='/v1/responses'&&body.model==='synthetic-text'){
  const content=body.instructions?.includes('"findings"')?{summary:'原生计数后的本地合成审阅。',findings:[]}:{summary:'原生计数后的本地合成分析。',questions:[],suggestions:[]}
  res.end(JSON.stringify({object:'response',status:'completed',error:null,output:[{type:'message',role:'assistant',status:'completed',content:[{type:'output_text',text:JSON.stringify(content)}]}],usage:{input_tokens:700,output_tokens:30}}));return
 }
 if(req.url==='/v1/chat/completions' && body.model==='synthetic-text') {
  const instruction=body.messages?.[0]?.content??''
  if(instruction.includes('Propose ONE editable season arc')){
   const input=JSON.parse(body.messages[1].content),scope=input.source
   const result={title:'门外的选择 · 本地协议候选',outline:{season_id:scope.season_id,...(scope.episode_id?{episode_id:scope.episode_id}:{}),goal:'角色决定是否打开门',opening:'门外传来熟悉的敲击',conflict:'角色想相信来者，却发现钥匙缺齿',turn:'角色选择先询问只有同伴知道的细节',hook:'对方给出了本应无人知道的答案'},rationale:'本地合成策划，只验证固定来源和人工采纳，不代表真实模型质量。'}
   res.end(JSON.stringify({choices:[{finish_reason:'stop',message:{content:JSON.stringify(result)}}],usage:{prompt_tokens:80,completion_tokens:80}}));return
  }
  if(instruction.includes('Optimize the supplied image-generation prompt')) {
   const input=JSON.parse(body.messages[1].content)
   res.end(JSON.stringify({choices:[{finish_reason:'stop',message:{content:JSON.stringify({prompt:input.prompt+'，柔和侧光，主体清晰，背景简洁，保留标题空间。'})}}],usage:{prompt_tokens:20,completion_tokens:30}}));return
  }
  if(instruction.includes('"findings"')){
   const input=JSON.parse(body.messages[1].content),record=input.material.find(r=>r.kind==='bible')
   const evidence=[{record_id:record.id,version_id:record.version_id,quote:record.body.bible.theme||record.title}]
   const result={summary:'本地合成连续性审阅，供测试显式判断。',findings:[
    {id:'',title:'核对本集人物意图',kind:'ambiguity',severity:'note',confidence:'low',explanation:'可能是作者有意设计的悬念，请核对原文。',evidence,proposal:{expected_version:0,title:'对来客身份的认知',kind:'fact',status:'active',required:false,body:{evidence:[],fact:{subject:'小满',predicate:'尚不确定',value:'来客身份',perspective:'unconfirmed',story_time:'',known_by:[]}}}},
    {id:'',title:'可能有意保留的悬念',kind:'knowledge',severity:'note',confidence:'low',explanation:'观众未知信息不一定是错误。',evidence},
    {id:'',title:'可忽略的创作疑问',kind:'character_arc',severity:'note',confidence:'low',explanation:'请以作者的人物弧线设计为准。',evidence}
   ]}

   res.end(JSON.stringify({choices:[{finish_reason:'stop',message:{content:JSON.stringify(result)}}],usage:{prompt_tokens:20,completion_tokens:30}}));return
  }
  const content=instruction.includes('"shots"') ? {shots:[{id:'synthetic-shot',scene_index:1,description:'本地协议验收画面，非真实模型生成。',shot_size:'medium',camera_movement:'static',duration_seconds:4,dialogue:''}]} : instruction.includes('"scenes"') ? {title:'本地协议验收剧本',logline:'这是一份验证真实网关链路的本地合成测试结果。',characters:[{name:'甲',description:'本地合成角色甲。'},{name:'乙',description:'本地合成角色乙。'}],scenes:[1,2,3].map(i=>({heading:`本地测试场次${i}`,action:'本地合成场景动作，仅作协议验收。',dialogue:[{character:'甲',text:'这是本地测试对白。'},{character:'乙',text:'不调用真实供应商。'}]}))} : {summary:'本地协议验收分析。',questions:[],suggestions:[]}
  res.end(JSON.stringify({choices:[{finish_reason:'stop',message:{content:JSON.stringify(content)}}],usage:{prompt_tokens:20,completion_tokens:30}}));return
 }
 res.writeHead(400);res.end(JSON.stringify({error:'Unsupported synthetic protocol request'}))
}).listen(5193,'127.0.0.1')
