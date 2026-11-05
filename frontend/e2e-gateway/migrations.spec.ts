import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
import {deflateSync} from 'node:zlib'

const api='http://localhost:8084/api/v1'
function png(){
 const chunk=(name:string,data:Buffer)=>{const tag=Buffer.from(name),payload=Buffer.concat([tag,data]);let crc=0xffffffff;for(const b of payload){crc^=b;for(let i=0;i<8;i++)crc=(crc>>>1)^((crc&1)?0xedb88320:0)}const out=Buffer.alloc(data.length+12);out.writeUInt32BE(data.length);payload.copy(out,4);out.writeUInt32BE((crc^0xffffffff)>>>0,out.length-4);return out}
 const h=Buffer.alloc(13);h.writeUInt32BE(64);h.writeUInt32BE(64,4);h[8]=8;h[9]=2
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',h),chunk('IDAT',deflateSync(Buffer.alloc(64*(64*3+1)))),chunk('IEND',Buffer.alloc(0))])
}
async function fixture(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click()
 await page.locator('#auth-email').fill(`migration-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-migration-password!')
 await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),query='?space_id='+session.spaces[0].id
 const headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
 async function write(path:string,data:unknown,method='POST'){
  const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()
 }
 let source=await write('/projects',{title:'三集旧项目-'+randomUUID(),brief:'完整保留旧剧本、素材和旧成片的迁移验收。'})
 source=await write(`/projects/${source.id}/script`,{expected_version:source.version,script:{title:'归途',logline:'旅人在三个不同的集数中依次出发、相遇和返程。',characters:[{name:'小满',description:'在小镇旅行的画家。'}],scenes:[1,7,120].map(ep=>({episode_number:ep,heading:`原第${ep}集`,action:'人物沿着河岸向前走。',dialogue:[{character:'小满',text:'我们继续向前。'}]}))}},'PUT')
 source=await write(`/projects/${source.id}/storyboard`,{expected_version:source.version,shots:[...['one','seven','last'].map((id,i)=>({id,scene_index:i+1,description:`原第${[1,7,120][i]}集的镜头`,shot_size:'medium',camera_movement:'static',duration_seconds:3,dialogue:''})),{id:'independent',scene_index:0,description:'保留在原项目的桥面空镜',shot_size:'wide',camera_movement:'static',duration_seconds:3,dialogue:''}]},'PUT')
 const upload=await page.request.post(api+`/projects/${source.id}/media/uploads`+query+'&filename=source.png&role=shot&shot_id=seven',{headers:{...headers,'Idempotency-Key':randomUUID(),'Content-Type':'image/png'},data:png()});expect(upload.ok(),await upload.text()).toBe(true);const media=await upload.json()
 await write(`/projects/${source.id}/media/timeline`,{expected_version:media.version,timeline:{episode_number:7,output_resolution:'720p',aspect_ratio:'16:9',clips:[{id:'original-clip',asset_id:media.uploaded_asset_id,shot_id:'seven',duration:.5,trim_start:0,subtitle:'旧字幕原样保留',audio_asset_id:'',audio_volume:0}],music_asset_id:'',music_volume:.2}},'PUT')
 const root=await write('/series',{title:'分集复制验收-'+randomUUID(),summary:'旧项目复制到独立的系列项目。',profile:{duration_seconds:120,output_resolution:'2160p',aspect_ratio:'9:16'}})
 const season=await write(`/series/${root.id}/seasons`,{})
 const before=await(await page.request.get(api+`/projects/${source.id}`+query)).json()
 const beforeMedia=await(await page.request.get(api+`/projects/${source.id}/media`+query)).json()
 return{query,source,root,season,before,beforeMedia}
}
async function preview(page:Page,f:Awaited<ReturnType<typeof fixture>>){
 await page.goto(`/projects/${f.source.id}/brief`)
 await page.getByText('将旧项目分集复制到系列',{exact:true}).click()
 await page.getByRole('combobox',{name:'原项目集数',exact:true}).selectOption('7')
 await page.getByRole('combobox',{name:'复制到系列',exact:true}).selectOption(f.root.id)
 await page.getByRole('combobox',{name:'目标季',exact:true}).selectOption(f.season.id)
 await page.getByRole('combobox',{name:'镜头归属 independent',exact:true}).selectOption('0')
 await page.getByRole('button',{name:'预览本集复制内容',exact:true}).click()
 await expect(page.getByRole('button',{name:'确认复制本集',exact:true})).toBeEnabled()
 await expect(page.getByLabel('迁移预览',{exact:true})).toContainText('1 个场次、1 个镜头、1 份素材')
}
test('secure migration previews explicit choices and copies actual owned assets without requiring a model',async({page,request},info)=>{
 const f=await fixture(page);const upstreamBefore=await(await request.get('http://127.0.0.1:5193/__state')).json()
 await preview(page,f)
 await page.screenshot({path:`/tmp/migration-preview-${info.project.name}.png`,fullPage:true})
 await page.getByRole('button',{name:'确认复制本集',exact:true}).click()
 await expect(page.getByRole('link',{name:'打开独立剧集',exact:true})).toBeVisible()
 const jobs=await(await page.request.get(api+`/projects/${f.source.id}/episode-migrations`+f.query)).json()
 expect(jobs.items).toHaveLength(1);expect(jobs.items[0]).not.toHaveProperty('plan');expect(jobs.items[0].state).toBe('completed')
 const target=jobs.items[0].target_project_id
 expect(await(await page.request.get(api+`/projects/${f.source.id}`+f.query)).json()).toEqual(f.before)
 expect(await(await page.request.get(api+`/projects/${f.source.id}/media`+f.query)).json()).toEqual(f.beforeMedia)
 const copied=await(await page.request.get(api+`/projects/${target}/media`+f.query)).json()
 expect(copied.timeline).toMatchObject({episode_number:1,output_resolution:'2160p',aspect_ratio:'9:16'})
 expect(copied.assets).toHaveLength(1);expect(copied.assets[0].id).not.toBe(f.beforeMedia.assets[0].id);expect(copied.assets[0].file_url).toContain('/projects/'+target+'/')
 const image=await page.request.get('http://localhost:8084'+copied.assets[0].file_url);expect(image.ok()).toBe(true);expect(await image.body()).toEqual(png())
 const upstreamAfter=await(await request.get('http://127.0.0.1:5193/__state')).json();expect(upstreamAfter.calls).toBe(upstreamBefore.calls)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 await page.getByRole('link',{name:'打开独立剧集',exact:true}).click()
 await expect(page.locator('.series-context')).toContainText('第 7 集')
 await expect(page.getByRole('link',{name:'查看原项目与旧成片',exact:true})).toHaveAttribute('href',`/projects/${f.source.id}/media`)
 await page.screenshot({path:`/tmp/migration-target-${info.project.name}.png`,fullPage:true})
})
test('an unconfirmed creation survives refresh and a denied retry without creating a second migration',async({page})=>{
 const f=await fixture(page);await preview(page,f)
 const keys:string[]=[],bodies:string[]=[]
 page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname===`/api/v1/projects/${f.source.id}/episode-migrations`){keys.push(r.headers()['idempotency-key']);bodies.push(r.postData()??'')}})
 const pattern=`**/api/v1/projects/${f.source.id}/episode-migrations?*`
 await page.route(pattern,async route=>{if(route.request().method()!=='POST')return route.continue();await route.fetch();await route.abort('failed')},{times:1})
 await page.getByRole('button',{name:'确认复制本集',exact:true}).click()
 await expect(page.getByText('创建结果尚未确认。请用原请求核对，避免重复创建。',{exact:true})).toBeVisible()
 page.on('dialog',d=>void d.accept());await page.reload();await page.getByText('将旧项目分集复制到系列',{exact:true}).click()
 await page.route(pattern,route=>route.request().method()==='POST'?route.fulfill({status:403,contentType:'application/json',body:JSON.stringify({error:{code:'role_denied',message:'当前权限不能迁移此项目，请重新核对空间与成员权限'}})}):route.continue())
 await page.getByRole('button',{name:'核对并重试保存',exact:true}).click()
 await expect(page.getByRole('combobox',{name:'原项目集数',exact:true})).toBeDisabled()
 await page.unroute(pattern)
 await page.getByRole('button',{name:'核对并重试保存',exact:true}).click()
 await expect(page.getByRole('link',{name:'打开独立剧集',exact:true})).toBeVisible()
 expect(keys).toHaveLength(3);expect(new Set(keys).size).toBe(1);expect(new Set(bodies).size).toBe(1)
 const jobs=await(await page.request.get(api+`/projects/${f.source.id}/episode-migrations`+f.query)).json();expect(jobs.items).toHaveLength(1)
})
test('a lost step response can be resumed after reload and unfinished targets can be stopped',async({page})=>{
 const f=await fixture(page);await preview(page,f)
 await page.route(`**/api/v1/projects/${f.source.id}/episode-migrations/*/continue?*`,async route=>{await route.fetch();await route.abort('failed')},{times:1})
 await page.getByRole('button',{name:'确认复制本集',exact:true}).click()
 await expect(page.getByRole('button',{name:'继续迁移',exact:true})).toBeEnabled()
 const records=await(await page.request.get(api+`/projects/${f.source.id}/episode-migrations`+f.query)).json()
 expect((await page.request.get(api+`/projects/${records.items[0].target_project_id}`+f.query)).status()).toBe(404)
 await page.reload();await page.getByText('将旧项目分集复制到系列',{exact:true}).click()
 await page.getByRole('button',{name:'停止并移除未完成目标',exact:true}).click()
 await expect(page.getByText('已停止，未完成目标已移除',{exact:true})).toBeVisible()
 expect((await page.request.get(api+`/projects/${records.items[0].target_project_id}`+f.query)).status()).toBe(404)
 expect(await(await page.request.get(api+`/projects/${f.source.id}`+f.query)).json()).toEqual(f.before)
})
