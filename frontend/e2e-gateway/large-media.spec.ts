import {test,expect,type Page} from '@playwright/test'
import {execFileSync} from 'node:child_process'
import {createHash,randomUUID} from 'node:crypto'

let png:Buffer
test.beforeAll(()=>{
  png=execFileSync('ffmpeg',['-v','error','-f','lavfi','-i','color=c=0x306040:s=4096x3072','-frames:v','1','-threads','1','-pix_fmt','rgb24','-compression_level','0','-f','image2pipe','-vcodec','png','pipe:1'],{maxBuffer:64*1024*1024})
  expect(png.length).toBeGreaterThan(32*1024*1024)
})
const api='http://localhost:8084/api/v1'
async function login(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`large-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-large-file-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json();return {query:'?space_id='+session.spaces[0].id,space:session.spaces[0].id,headers:{Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token,'Idempotency-Key':randomUUID()}}
}
test('continue editing a large original recovers a lost completion without duplicating its project or asset',async({page,request},info)=>{
 test.setTimeout(90000)
 const {query,headers}=await login(page)
 const gateway=await page.request.post(api+'/ai-gateways'+query,{headers,data:{expected_version:0,gateway:{name:'Local image',base_url:'http://127.0.0.1:5193/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}}});expect(gateway.status()).toBe(200);const g=(await gateway.json()).gateways[0]
 const configured=await page.request.post(api+'/gateway-models'+query,{headers,data:{expected_version:0,model:{gateway_id:g.id,name:'Local image',model:'synthetic-image',capabilities:['image'],enabled:true,max_output_tokens:512}}});expect(configured.status()).toBe(200);const model=(await configured.json()).models[0]
 expect((await page.request.put(api+'/model-features/image_generation'+query,{headers,data:{expected_version:0,binding:{feature_key:'image_generation',model_config_id:model.id,enabled:true,allow_override:true}}})).status()).toBe(200)
 const created=await page.request.post(api+'/gateway-tasks'+query,{headers:{...headers,'Idempotency-Key':randomUUID()},data:{kind:'general',prompt:'Synthetic large original continuation',model_config_id:model.id,model_config_version:model.version,expected_provider:'gateway-images',expected_model:model.model}});expect(created.status()).toBe(202);const task=await created.json()
 await expect.poll(async()=>(await(await page.request.get(api+'/tasks/'+task.id+query)).json()).status).toBe('succeeded')
 const before=(await(await request.get('http://127.0.0.1:5193/__state')).json()).requests.length
 await page.route(`**/tasks/${task.id}/result*`,route=>route.fulfill({contentType:'image/png',body:png}))
 let lost=false;await page.route('**/media/upload-sessions/*/complete?*',async route=>{if(!lost){lost=true;await route.fetch();await route.abort('failed')}else await route.continue()})
 await page.reload();await page.locator('.result-preview').first().click();await page.getByRole('button',{name:'继续编辑',exact:true}).click();await expect(page.locator('.detail-dialog').getByRole('alert')).toBeVisible()
 await page.getByRole('button',{name:'继续编辑',exact:true}).click();await expect(page).toHaveURL(/\/studio\/workspaces\//)
 const project=page.url().split('/').pop()!
 const media=await(await page.request.get(api+'/projects/'+project+'/media'+query)).json();expect(media.assets).toHaveLength(1);expect(media.assets[0]).toMatchObject({content_sha256:createHash('sha256').update(png).digest('hex'),name:`source-${task.id}.png`})
 expect((await(await page.request.get(api+'/projects'+query)).json()).filter((p:{kind:string})=>p.kind==='image')).toHaveLength(1)
 expect((await(await request.get('http://127.0.0.1:5193/__state')).json()).requests.length).toBe(before)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.locator('.production-board').screenshot({path:`/tmp/large-image-edit-${info.project.name}.png`})
})
test('large photo rotation and a saved mask use actual chunked assets without a model call',async({page,request},info)=>{
 test.setTimeout(90000)
 const {query,headers}=await login(page)
 const created=await page.request.post(api+'/projects'+query,{headers,data:{title:'Synthetic large photo',brief:'Large-file photo editing fixture'}});expect(created.status()).toBe(201);const project=await created.json()
 const before=(await(await request.get('http://127.0.0.1:5193/__state')).json()).requests.length
 await page.goto(`/projects/${project.id}/media`);await page.getByLabel('素材用途',{exact:true}).selectOption('reference');await page.getByLabel('选择待编辑照片',{exact:true}).setInputFiles({name:'large-original.png',mimeType:'image/png',buffer:png});await page.getByLabel('照片旋转',{exact:true}).selectOption('90');await page.getByRole('button',{name:'保存编辑照片为新素材',exact:true}).click()
 const image=page.locator('.media-assets img').first();await expect(image).toBeVisible();await expect.poll(()=>image.evaluate((v:HTMLImageElement)=>[v.naturalWidth,v.naturalHeight])).toEqual([3072,4096])
 await page.getByRole('button',{name:'绘制局部编辑遮罩',exact:true}).first().click();await page.getByRole('button',{name:'标记矩形修改区域',exact:true}).click();await page.getByRole('button',{name:'保存遮罩为新素材',exact:true}).click()
 await expect.poll(async()=>(await(await page.request.get(api+'/projects/'+project.id+'/media'+query)).json()).assets.length).toBe(2)
 expect((await(await request.get('http://127.0.0.1:5193/__state')).json()).requests.length).toBe(before)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.locator('.media-studio').screenshot({path:`/tmp/large-photo-edit-${info.project.name}.png`})
})
