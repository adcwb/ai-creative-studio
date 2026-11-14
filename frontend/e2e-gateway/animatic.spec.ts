import {selectOption, expectControlValue} from '../e2e/support/controls'
import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
const api='http://localhost:8084/api/v1',upstream='http://127.0.0.1:5193'
function voice(){const n=8000,b=Buffer.alloc(44+n*2);b.write('RIFF');b.writeUInt32LE(b.length-8,4);b.write('WAVEfmt ',8);b.writeUInt32LE(16,16);b.writeUInt16LE(1,20);b.writeUInt16LE(1,22);b.writeUInt32LE(8000,24);b.writeUInt32LE(16000,28);b.writeUInt16LE(2,32);b.writeUInt16LE(16,34);b.write('data',36);b.writeUInt32LE(n*2,40);for(let i=0;i<n;i++)b.writeInt16LE(Math.round(10000*Math.sin(2*Math.PI*440*i/8000)),44+i*2);return b}
async function fixture(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`animatic-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-animatic-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),query='?space_id='+session.spaces[0].id,headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
 async function write(path:string,data:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 const initial=await write('/projects',{title:'雨夜节奏 · 静帧预演',brief:'只使用自有合成图片和声轨，不调用模型。'})
 const shots=['preview-a','preview-b'].map((id,i)=>({id,scene_index:0,description:i?'门后的脚步':'推门前的迟疑',shot_size:'medium',camera_movement:'static',duration_seconds:2,dialogue:'Saved dialogue, not a subtitle',sound:'Saved sound intent, not generated',composition:'人物旁保留门框空白'}))
 const project=await write(`/projects/${initial.id}/storyboard`,{expected_version:initial.version,shots},'PUT')
 const frames=await page.evaluate(()=>['#557766','#775544'].map(colour=>{const c=document.createElement('canvas');c.width=160;c.height=90;const x=c.getContext('2d')!;x.fillStyle=colour;x.fillRect(0,0,160,90);return c.toDataURL('image/png').split(',')[1]}))
 let media;const images:string[]=[]
 for(const [i,shot] of shots.entries()){const r=await page.request.post(api+`/projects/${project.id}/media/uploads`+query+`&filename=frame-${i}.png&role=shot&shot_id=${shot.id}`,{headers:{...headers,'Content-Type':'image/png','Idempotency-Key':randomUUID()},data:Buffer.from(frames[i],'base64')});expect(r.status()).toBe(200);media=await r.json();images.push(media.uploaded_asset_id)}
 const r=await page.request.post(api+`/projects/${project.id}/media/uploads`+query+'&filename=temporary.wav&role=voice',{headers:{...headers,'Content-Type':'audio/wav','Idempotency-Key':randomUUID()},data:voice()});expect(r.status()).toBe(200);media=await r.json();const audio=media.uploaded_asset_id
 for(const [i,shot] of shots.entries())media=await write(`/projects/${project.id}/media/decisions`,{expected_version:media.version,action:'select',asset_id:images[i],shot_id:shot.id,text:'导演选片',time:0,review_id:'',resolved:false})
 return{project,query,headers,shots,images,audio,media,write}
}
async function plan(page:Page,id:string){
 await page.goto(`/projects/${id}/storyboard`)
 await expect(page.getByText('生成模拟分镜',{exact:true})).toHaveCount(0)
 await page.getByText('静帧预演 · 先看节奏',{exact:true}).click()
 await expect(page.getByRole('button',{name:'用已采用图片规划本集',exact:true})).toBeEnabled()
 await page.getByRole('button',{name:'用已采用图片规划本集',exact:true}).click()
 for(const i of [1,2])await page.getByRole('spinbutton',{name:`预演时长 ${i}（秒）`,exact:true}).fill('0.5')
}
test('independent animatic renders selected stills and temporary audio without changing adopted frames, formal edits or model usage',async({page,request},info)=>{
 const f=await fixture(page),before=await(await request.get(upstream+'/__state')).json()
 await plan(page,f.project.id)
 await page.getByRole('spinbutton',{name:'预演时长 1（秒）',exact:true}).fill('0.6');await page.getByRole('spinbutton',{name:'预演时长 2（秒）',exact:true}).fill('0.7')
 await selectOption(page.getByRole('combobox',{name:'预演声音',exact:true}), 'temporary');await selectOption(page.getByRole('combobox',{name:'预演临时声轨 1',exact:true}), f.audio)
 // A cutback can repeat a saved shot and image without creating a fake image or
 // imposing a per-project shot/candidate quota.
 await selectOption(page.getByRole('combobox',{name:'添加预演镜头',exact:true}), 'preview-a');await page.getByRole('button',{name:'加入预演',exact:true}).click();await selectOption(page.getByRole('combobox',{name:'预演图片 3',exact:true}), f.images[0]);await page.getByRole('spinbutton',{name:'预演时长 3（秒）',exact:true}).fill('0.5')
 const accepted=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/media/previews?'))
 await page.getByRole('button',{name:'创建独立静帧预演',exact:true}).click();const response=await accepted;expect(response.status()).toBe(202);const body=response.request().postDataJSON();expect(body).not.toHaveProperty('model_config_id');expect(body).not.toHaveProperty('budget_currency');const original=await response.json()
 expect(original.purpose).toBe('animatic');expect(original.preview_source.shots).toHaveLength(3);expect(original.preview_source.shots[0].shot.id).toBe(original.preview_source.shots[2].shot.id);expect(original.timeline.clips[0].id).not.toBe(original.timeline.clips[2].id)
 const path=`/projects/${f.project.id}/media/previews/${original.id}`
 // The three-clip render measured ~4.9s before the 2.5s UI refresh. Wait for
 // the completed player with the existing local-render budget, not a provider retry.
 const player=page.getByLabel('独立静帧预演播放器',{exact:true});await expect(player).toBeVisible({timeout:10000});await expect.poll(()=>player.evaluate(v=>(v as HTMLVideoElement).videoWidth)).toBe(640)
 const downloaded=page.waitForEvent('download');await page.getByRole('link',{name:'下载带标识的预演',exact:true}).click();expect((await downloaded).suggestedFilename()).toBe(`animatic-${original.id}.mp4`)
 const after=await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json();expect(after.timeline).toEqual(f.media.timeline);expect(after.selections).toEqual(f.media.selections);expect(after.version).toBe(f.media.version);expect(after.assets).toEqual(f.media.assets)
 const fixed=await(await page.request.get(api+path+f.query)).json();expect(fixed.status).toBe('succeeded');expect(fixed.preview_source).toEqual(original.preview_source)
 await page.getByText('查看固定镜头与声音来源',{exact:true}).click();await page.getByRole('button',{name:'读取预演来源',exact:true}).click();await expect(page.getByText('frame-0.png · temporary.wav',{exact:true})).toBeVisible()
 expect((await(await request.get(upstream+'/__state')).json()).calls).toBe(before.calls)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.getByText('静帧预演 · 先看节奏',{exact:true}).locator('..').screenshot({path:`/tmp/animatic-${info.project.name}.png`})
 await page.getByRole('navigation',{name:'项目工作区',exact:true}).getByRole('link',{name:'剪辑与声音',exact:true}).click();await expect(page.locator('.media-studio')).toBeVisible();await expect(page.locator('.media-exports > article')).toHaveCount(0)
 const delivery=await page.request.post(api+`/projects/${f.project.id}/social/jobs`+f.query,{headers:{...f.headers,'Idempotency-Key':randomUUID()},data:{expected_version:after.version,platform:'tiktok',mode:'export',export_id:original.id,confirmed:true}});expect(delivery.status()).toBe(400);expect((await delivery.json()).error.message).toContain('预演')
})
test('lost animatic acceptance recovers the exact original request after saved plan and formal cut change',async({page,request})=>{
 const f=await fixture(page),before=await(await request.get(upstream+'/__state')).json();await plan(page,f.project.id)
 let id='',key='',sent:unknown
 await page.route('**/media/previews?*',async route=>{if(route.request().method()!=='POST')return route.continue();sent=route.request().postDataJSON();key=route.request().headers()['idempotency-key'];const r=await route.fetch();expect(r.status()).toBe(202);id=(await r.json()).id;await route.abort('failed')},{times:1})
 await page.getByRole('button',{name:'创建独立静帧预演',exact:true}).click();await expect(page.getByRole('button',{name:'核对原预演请求',exact:true})).toBeVisible()
 const changed=await f.write(`/projects/${f.project.id}/storyboard`,{expected_version:f.project.version,shots:f.shots.map(s=>({...s,description:'后来改动的镜头计划'}))},'PUT')
 const cut={clips:[{id:'formal-cut',asset_id:f.images[1],duration:1,trim_start:0,subtitle:'later formal cut',audio_asset_id:'',audio_volume:1}],music_asset_id:'',music_volume:.2,aspect_ratio:'16:9'}
 const formal=await f.write(`/projects/${f.project.id}/media/timeline`,{expected_version:f.media.version,timeline:cut},'PUT')
 await page.reload();await expect(page.getByRole('button',{name:'核对原预演请求',exact:true})).toBeVisible()
 const accepted=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/media/previews?'));await page.getByRole('button',{name:'核对原预演请求',exact:true}).click();const response=await accepted;expect(response.status()).toBe(202);expect(response.request().postDataJSON()).toEqual(sent);expect(response.request().headers()['idempotency-key']).toBe(key);expect((await response.json()).id).toBe(id)
 const history=await(await page.request.get(api+`/projects/${f.project.id}/media/previews`+f.query)).json();expect(history.items).toHaveLength(1)
 const current=await(await page.request.get(api+`/projects/${f.project.id}`+f.query)).json();expect(current.version).toBe(changed.version)
 const media=await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json();expect(media.timeline).toEqual(formal.timeline)
 const source=await(await page.request.get(api+`/projects/${f.project.id}/media/previews/${id}`+f.query)).json();expect(source.preview_source.shots[0].shot.description).toBe(f.shots[0].description)
 expect((await(await request.get(upstream+'/__state')).json()).calls).toBe(before.calls)
})
test('a replay-first CAS rejection confirms an unaccepted request while preserving the creator selections for review',async({page})=>{
 const f=await fixture(page);await plan(page,f.project.id)
 await page.route('**/media/previews?*',async route=>{if(route.request().method()==='POST')return route.abort('failed');return route.continue()},{times:1})
 await page.getByRole('button',{name:'创建独立静帧预演',exact:true}).click();await expect(page.getByRole('button',{name:'核对原预演请求',exact:true})).toBeVisible()
 await f.write(`/projects/${f.project.id}/storyboard`,{expected_version:f.project.version,shots:f.shots.map(s=>({...s,description:'已核对的新计划'}))},'PUT')
 await page.getByRole('button',{name:'核对原预演请求',exact:true}).click();await expect(page.getByRole('button',{name:'核对原预演请求',exact:true})).toHaveCount(0)
 await expectControlValue(page.getByRole('spinbutton',{name:'预演时长 1（秒）',exact:true}), '0.5')
 await page.getByRole('button',{name:'重新核对分镜与素材',exact:true}).click();await expect(page.getByText('已读取最新分镜和素材，保留了你的选片、顺序和时长。请核对变更后再创建预演。',{exact:true})).toBeVisible()
 await expectControlValue(page.getByRole('combobox',{name:'预演图片 1',exact:true}), f.images[0])
 const accepted=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/media/previews?'));await page.getByRole('button',{name:'创建独立静帧预演',exact:true}).click();expect((await accepted).status()).toBe(202)
 const history=await(await page.request.get(api+`/projects/${f.project.id}/media/previews`+f.query)).json();expect(history.items).toHaveLength(1)
})
