import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
const api='http://localhost:8084/api/v1',upstream='http://127.0.0.1:5193'
async function fixture(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`canvas-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-canvas-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),space=session.spaces[0].id,query='?space_id='+space,headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
 async function write(path:string,body:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data:body});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 const project=await write('/projects',{title:'创作流程验收',brief:'将真实素材与生成任务经过人工选择再交付。'})
 const image=Buffer.from(await page.evaluate(()=>{const c=document.createElement('canvas');c.width=256;c.height=256;const x=c.getContext('2d')!;x.fillStyle='#437761';x.fillRect(0,0,256,256);return c.toDataURL('image/png').split(',')[1]}),'base64')
 const upload=await page.request.post(api+`/projects/${project.id}/media/uploads`+query+'&filename=reference.png&role=reference',{headers:{...headers,'Content-Type':'image/png','Idempotency-Key':randomUUID()},data:image});expect(upload.status()).toBe(200);const media=await upload.json()
 return {project,media,space,query,headers,session,write}
}
test('canvas persists dragging and revisions, then pauses for a real human before owned media output',async({page},info)=>{
 const f=await fixture(page);if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 await page.goto(`/workflows/${f.project.id}`);await page.getByRole('button',{name:'已有素材审片模板',exact:true}).click();await page.locator('.workflow-step-list summary').click();await page.locator('.workflow-step-list').getByRole('button',{name:'项目素材',exact:true}).click();await page.getByRole('combobox',{name:'项目素材',exact:true}).selectOption(f.media.uploaded_asset_id)
 await page.getByRole('textbox',{name:'流程名称',exact:true}).fill('固定素材先审后用')
 const saving=page.waitForResponse(r=>r.url().includes('/workflows?')&&r.request().method()==='POST');await page.getByRole('button',{name:'保存流程修订',exact:true}).click();const saved=await(await saving).json();await expect(page.getByRole('button',{name:'运行已保存流程',exact:true})).toBeEnabled()
 if(info.project.name==='desktop'){
  const node=page.locator('.react-flow__node[data-id="source"]');const b=await node.boundingBox();expect(b).not.toBeNull();await page.mouse.move(b!.x+80,b!.y+30);await page.mouse.down();await page.mouse.move(b!.x+140,b!.y+70,{steps:8});await page.mouse.up();await expect(page.getByRole('button',{name:'运行已保存流程',exact:true})).toBeDisabled();const update=page.waitForResponse(r=>r.url().includes(`/workflows/${saved.id}?`)&&r.request().method()==='PUT');await page.getByRole('button',{name:'保存流程修订',exact:true}).click();await update
  const detail=await(await page.request.get(api+`/projects/${f.project.id}/workflows/${saved.id}`+f.query)).json();expect(detail.version).toBe(2);expect(detail.revision.graph.nodes.find((n:{id:string})=>n.id==='source').x).not.toBe(20)
 }
 const beforeZoom=await page.locator('.react-flow__viewport').evaluate(e=>new DOMMatrix(getComputedStyle(e).transform).a)
 await page.getByRole('button',{name:'放大画布',exact:true}).click()
 await expect.poll(()=>page.locator('.react-flow__viewport').evaluate(e=>new DOMMatrix(getComputedStyle(e).transform).a)).toBeGreaterThan(beforeZoom)
 const viewSave=page.waitForResponse(r=>r.url().includes(`/workflows/${saved.id}?`)&&r.request().method()==='PUT');await page.getByRole('button',{name:'保存流程修订',exact:true}).click();await viewSave
 const viewDetail=await(await page.request.get(api+`/projects/${f.project.id}/workflows/${saved.id}`+f.query)).json();expect(viewDetail.revision.graph.viewport.zoom).toBeGreaterThan(beforeZoom)
 await page.reload();await expect.poll(()=>page.locator('.react-flow__viewport').evaluate(e=>new DOMMatrix(getComputedStyle(e).transform).a)).toBeCloseTo(viewDetail.revision.graph.viewport.zoom,3)
 await expect(page.getByRole('textbox',{name:'流程名称',exact:true})).toHaveValue('固定素材先审后用');await page.screenshot({path:`/tmp/canvas-${info.project.name}.png`,fullPage:true});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);expect((await page.getByRole('textbox',{name:'流程名称',exact:true}).boundingBox())!.width).toBeGreaterThan(180)
 if(info.project.name==='desktop'){for(const theme of ['paper','screening','forest']){await page.evaluate(({user,theme})=>localStorage.setItem('studio.preferences.v1:'+encodeURIComponent(user),theme),{user:f.session.user.id,theme});await page.reload();await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme',theme);await expect(page.locator('.workflow-node').first()).toBeVisible();await page.screenshot({path:`/tmp/canvas-${theme}-desktop.png`,fullPage:true})}}
 const starting=page.waitForResponse(r=>r.url().includes(`/workflows/${saved.id}/runs?`)&&r.request().method()==='POST');await page.getByRole('button',{name:'运行已保存流程',exact:true}).click();const run=await(await starting).json()
 await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择');let state=await(await page.request.get(api+`/projects/${f.project.id}/workflow-runs/${run.id}`+f.query)).json();expect(state.nodes.output.status).toBe('queued');await page.reload();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择')
 await expect(page.getByRole('button',{name:'确认采用并继续',exact:true})).toBeDisabled();await page.getByRole('checkbox',{name:'我已检查此素材，继续后续步骤',exact:true}).check();await page.getByRole('button',{name:'确认采用并继续',exact:true}).click();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('已完成')
 state=await(await page.request.get(api+`/projects/${f.project.id}/workflow-runs/${run.id}`+f.query)).json();expect(state.nodes.output.result.asset_id).toBe(f.media.uploaded_asset_id);expect(state.nodes.review.reviewed_by).toBe(f.session.user.id)
 const media=await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json();expect(media.assets).toHaveLength(1);expect(media.timeline.clips).toHaveLength(0)
})
test('saved model graph creates one real gateway job and reconciles a lost run response without resubmission',async({page,request})=>{
 const f=await fixture(page)
 const g=await f.write('/ai-gateways',{expected_version:0,gateway:{name:'Canvas gateway',base_url:upstream+'/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}})
 const models=await f.write('/gateway-models',{expected_version:0,model:{gateway_id:g.gateways[0].id,name:'Canvas image',model:'synthetic-image',capabilities:['image'],enabled:true,max_output_tokens:512,json_output:true}}),model=models.models[0]
 await f.write('/model-features/image_generation',{expected_version:0,binding:{feature_key:'image_generation',model_config_id:model.id,enabled:true,allow_override:true}},'PUT')
 const graph={nodes:[{id:'text',kind:'text',label:'描述',x:0,y:0,text:'Synthetic canvas real gateway image {{not_expanded}}'},{id:'template',kind:'template',label:'文案模板',x:120,y:0,text:'Poster: {{input}}'},{id:'image',kind:'image',label:'真实图像',x:240,y:0,model_config_id:model.id,model_config_version:model.version,ratio:'9:16'},{id:'review',kind:'review',label:'选择素材',x:480,y:0},{id:'output',kind:'output',label:'输出',x:720,y:0}],edges:[{source:'text',target:'template',input:'prompt'},{source:'template',target:'image',input:'prompt'},{source:'image',target:'review',input:'media'},{source:'review',target:'output',input:'media'}]}
 const saved=await f.write(`/projects/${f.project.id}/workflows`,{name:'图像真实网关',expected_version:0,graph}),before=await(await request.get(upstream+'/__state')).json()
 await page.goto(`/workflows/${f.project.id}`);await page.getByRole('combobox',{name:'已保存流程',exact:true}).selectOption(saved.id);await expect(page.getByRole('button',{name:'运行已保存流程',exact:true})).toBeEnabled()
 let id='',key='',body:unknown;await page.route('**/workflows/*/runs?*',async route=>{key=route.request().headers()['idempotency-key'];body=route.request().postDataJSON();const r=await route.fetch();expect(r.status()).toBe(200);id=(await r.json()).id;await route.abort('failed')},{times:1})
 await page.getByRole('button',{name:'运行已保存流程',exact:true}).click();await expect(page.getByRole('button',{name:'核对原流程运行请求',exact:true})).toBeVisible();await page.reload();const recovery=page.waitForResponse(r=>r.url().includes(`/workflows/${saved.id}/runs?`)&&r.request().method()==='POST');await page.getByRole('button',{name:'核对原流程运行请求',exact:true}).click();const recovered=await recovery;expect(recovered.request().headers()['idempotency-key']).toBe(key);expect(recovered.request().postDataJSON()).toEqual(body);expect((await recovered.json()).id).toBe(id)
 await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择')
 const current=await(await page.request.get(api+`/projects/${f.project.id}/workflow-runs/${id}`+f.query)).json();expect(current.nodes.image.job_id).toBeTruthy();expect(current.nodes.image.result.asset_kind).toBe('image');expect(current.nodes.output.status).toBe('queued')
 const jobs=await(await page.request.get(api+`/projects/${f.project.id}/creative-jobs`+f.query)).json();expect(jobs).toHaveLength(1);expect(jobs[0].provider).toBe('gateway-images');expect(jobs[0].prompt).toBe('Poster: Synthetic canvas real gateway image {{not_expanded}}');expect((await(await request.get(upstream+'/__state')).json()).calls).toBe(before.calls+1)
 await page.getByRole('checkbox',{name:'我已检查此素材，继续后续步骤',exact:true}).check();await page.getByRole('button',{name:'确认采用并继续',exact:true}).click();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('已完成');expect((await(await request.get(upstream+'/__state')).json()).calls).toBe(before.calls+1)
})

test('speech steps route authorized copy and voice to the real speech protocol before human selection',async({page,request})=>{
 const f=await fixture(page)
 const gateways=await f.write('/ai-gateways',{expected_version:0,gateway:{name:'Canvas speech',base_url:upstream+'/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}})
 const models=await f.write('/gateway-models',{expected_version:0,model:{gateway_id:gateways.gateways[0].id,name:'Speech only',model:'synthetic-speech',capabilities:['speech'],enabled:true,max_output_tokens:512}}),model=models.models[0]
 await f.write('/model-features/speech_synthesis',{expected_version:0,binding:{feature_key:'speech_synthesis',model_config_id:model.id,enabled:true,allow_override:true}},'PUT')
 const graph={nodes:[{id:'text',kind:'text',label:'口播',x:0,y:0,text:'这是画布中的原始配音文案。'},{id:'speech',kind:'speech',label:'配音',x:240,y:0,model_config_id:model.id,model_config_version:model.version,voice:'alloy'},{id:'review',kind:'review',label:'试听',x:480,y:0},{id:'output',kind:'output',label:'输出',x:720,y:0}],edges:[{source:'text',target:'speech',input:'prompt'},{source:'speech',target:'review',input:'media'},{source:'review',target:'output',input:'media'}]}
 const saved=await f.write(`/projects/${f.project.id}/workflows`,{name:'文案到试听',expected_version:0,graph}),before=(await(await request.get(upstream+'/__state')).json()).requests.length
 await page.goto(`/workflows/${f.project.id}`);await page.getByRole('combobox',{name:'已保存流程',exact:true}).selectOption(saved.id)
 await page.getByRole('button',{name:'运行已保存流程',exact:true}).click();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择')
 const sent=(await(await request.get(upstream+'/__state')).json()).requests.slice(before);expect(sent).toHaveLength(1);expect(sent[0]).toMatchObject({path:'/v1/audio/speech',body:{model:'synthetic-speech',voice:'alloy',input:'这是画布中的原始配音文案。'}})
 const tasks=await(await page.request.get(api+`/projects/${f.project.id}/creative-jobs`+f.query)).json();expect(tasks).toHaveLength(1);expect(tasks[0].provider).toBe('gateway-speech');expect(tasks[0].status).toBe('succeeded')
 await expect(page.locator('.workflow-run audio')).toHaveCount(2)
 await page.getByRole('checkbox',{name:'我已检查此素材，继续后续步骤',exact:true}).check();await page.getByRole('button',{name:'确认采用并继续',exact:true}).click();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('已完成')
 expect((await(await request.get(upstream+'/__state')).json()).requests).toHaveLength(before+1)
})

test('video graph submits once, polls its durable upstream task and exposes decoded media for human review',async({page,request})=>{
 const f=await fixture(page)
 const gateways=await f.write('/ai-gateways',{expected_version:0,gateway:{name:'Canvas video',base_url:upstream+'/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}})
 const models=await f.write('/gateway-models',{expected_version:0,model:{gateway_id:gateways.gateways[0].id,name:'Video only',model:'synthetic-video',capabilities:['video'],enabled:true,max_output_tokens:512,video_seconds:4,video_size:'720x1280'}}),model=models.models[0]
 await f.write('/model-features/shot_video',{expected_version:0,binding:{feature_key:'shot_video',model_config_id:model.id,enabled:true,allow_override:true}},'PUT')
 const graph={nodes:[{id:'text',kind:'text',label:'镜头描述',x:0,y:0,text:'Synthetic video: hold on the closed door.'},{id:'video',kind:'video',label:'视频',x:240,y:0,model_config_id:model.id,model_config_version:model.version},{id:'review',kind:'review',label:'审片',x:480,y:0},{id:'output',kind:'output',label:'输出',x:720,y:0}],edges:[{source:'text',target:'video',input:'prompt'},{source:'video',target:'review',input:'media'},{source:'review',target:'output',input:'media'}]}
 const saved=await f.write(`/projects/${f.project.id}/workflows`,{name:'视频异步任务到审片',expected_version:0,graph}),before=(await(await request.get(upstream+'/__state')).json()).requests.length
 await page.goto(`/workflows/${f.project.id}`);await page.getByRole('combobox',{name:'已保存流程',exact:true}).selectOption(saved.id)
 await page.getByRole('button',{name:'运行已保存流程',exact:true}).click();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择')
 const sent=(await(await request.get(upstream+'/__state')).json()).requests.slice(before)
 expect(sent.filter((r:{path:string})=>r.path==='/v1/videos')).toHaveLength(1)
 expect(sent.find((r:{path:string})=>r.path==='/v1/videos')).toMatchObject({body:{model:'synthetic-video',prompt:'Synthetic video: hold on the closed door.',seconds:'4',size:'720x1280'}})
 expect(sent.filter((r:{path:string})=>r.path==='/v1/videos/canvas-video').length).toBeGreaterThan(0)
 expect(sent.some((r:{path:string})=>r.path==='/v1/videos/canvas-video/content')).toBe(true)
 const jobs=await(await page.request.get(api+`/projects/${f.project.id}/creative-jobs`+f.query)).json();expect(jobs).toHaveLength(1);expect(jobs[0]).toMatchObject({kind:'video',status:'succeeded',upstream_id:'canvas-video'})
 const player=page.locator('.workflow-run video').first();await expect(player).toBeVisible();await expect.poll(()=>player.evaluate((v:HTMLVideoElement)=>v.readyState)).toBeGreaterThanOrEqual(1);expect(await player.evaluate((v:HTMLVideoElement)=>v.duration)).toBeCloseTo(2,1)
 await page.reload();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择');await page.getByRole('checkbox',{name:'我已检查此素材，继续后续步骤',exact:true}).check();await page.getByRole('button',{name:'确认采用并继续',exact:true}).click();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('已完成')
 expect((await(await request.get(upstream+'/__state')).json()).requests.slice(before).filter((r:{path:string})=>r.path==='/v1/videos')).toHaveLength(1)
})

test('creators add and connect a literal copy template from the canvas controls',async({page},info)=>{
 const f=await fixture(page)
 if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 await page.goto(`/workflows/${f.project.id}`)
 await page.getByRole('combobox',{name:'步骤类型',exact:true}).selectOption('template')
 await page.getByRole('button',{name:'添加步骤',exact:true}).click()
 const panel=page.getByRole('complementary',{name:'步骤设置',exact:true})
 await panel.getByRole('textbox',{name:'步骤名称',exact:true}).fill('海报文案')
 await panel.getByRole('textbox',{name:'描述与文案',exact:true}).fill('海报主体：{{input}}。保留右上角标题空间。')
 await expect(panel.getByText('使用 {{input}} 插入上一步文案，按原文替换，不执行表达式。',{exact:true})).toBeVisible()
 await panel.getByRole('combobox',{name:'来源步骤',exact:true}).selectOption('description')
 await panel.getByRole('button',{name:'连接到此步骤',exact:true}).click()
 await page.getByRole('textbox',{name:'流程名称',exact:true}).fill('海报模板草稿')
 const saving=page.waitForResponse(r=>r.url().includes('/workflows?')&&r.request().method()==='POST')
 await page.getByRole('button',{name:'保存流程修订',exact:true}).click();const saved=await(await saving).json()
 const detail=await(await page.request.get(api+`/projects/${f.project.id}/workflows/${saved.id}`+f.query)).json(),step=detail.revision.graph.nodes.find((n:{kind:string})=>n.kind==='template')
 expect(step.text).toBe('海报主体：{{input}}。保留右上角标题空间。')
 expect(detail.revision.graph.edges).toContainEqual({source:'description',target:step.id,input:'prompt'})
 await page.reload();await page.locator('.workflow-step-list summary').click();await page.locator('.workflow-step-list').getByRole('button',{name:'海报文案',exact:true}).click()
 await expect(panel.getByRole('textbox',{name:'描述与文案',exact:true})).toHaveValue(step.text)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true)
 await page.screenshot({path:`/tmp/canvas-template-editor-${info.project.name}.png`,fullPage:true})
})
