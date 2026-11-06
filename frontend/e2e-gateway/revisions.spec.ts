import {test,expect,type Page} from '@playwright/test'
import {randomUUID,createHash} from 'node:crypto'
const api='http://localhost:8084/api/v1',upstream='http://127.0.0.1:5193'
async function fixture(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`revision-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-revision-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),query='?space_id='+session.spaces[0].id,headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
 async function write(path:string,data:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 let project=await write('/projects',{title:'雨夜 · 制作修订',brief:'保留门前的迟疑，用版本比较不同方向。'})
 const shots=[{id:'revision-shot',scene_index:0,description:'门前的迟疑',shot_size:'medium',camera_movement:'static',duration_seconds:2,dialogue:'谁在门外？',sound:'雨声暂缓',composition:'给门框留出空间'}]
 project=await write(`/projects/${project.id}/storyboard`,{expected_version:project.version,shots},'PUT')
 const png=Buffer.from(await page.evaluate(()=>{const c=document.createElement('canvas');c.width=160;c.height=90;const x=c.getContext('2d')!;x.fillStyle='#557766';x.fillRect(0,0,160,90);return c.toDataURL('image/png').split(',')[1]}),'base64')
 const r=await page.request.post(api+`/projects/${project.id}/media/uploads`+query+'&filename=雨夜.png&role=shot&shot_id=revision-shot',{headers:{...headers,'Content-Type':'image/png','Idempotency-Key':randomUUID()},data:png});expect(r.status()).toBe(200);const uploaded=await r.json()
 const timeline={aspect_ratio:'16:9',output_resolution:'1080p',clips:[{id:'first-cut',shot_id:'revision-shot',asset_id:uploaded.uploaded_asset_id,duration:2,trim_start:0,subtitle:'谁在门外？',audio_asset_id:'',audio_volume:1}],music_asset_id:'',music_volume:.2}
 const media=await write(`/projects/${project.id}/media/timeline`,{expected_version:uploaded.version,timeline},'PUT')
 return{project,shots,media,query,headers,write,pngSHA:createHash('sha256').update(png).digest('hex')}
}
async function open(page:Page,id:string){await page.goto(`/projects/${id}/media`);await page.getByText('制作修订 · 留住每个方向',{exact:true}).click();await expect(page.getByRole('textbox',{name:'修订说明',exact:true})).toBeEnabled()}
test('production snapshots pin real media and source content, fork explicitly and preserve the live edit without model calls',async({page,request},info)=>{
 const f=await fixture(page),before=await(await request.get(upstream+'/__state')).json();await open(page,f.project.id)
 await page.getByRole('textbox',{name:'修订说明',exact:true}).fill('先保留门前的停顿')
 // Collapsing this tool must not discard a creator's unsaved note.
 await page.getByText('制作修订 · 留住每个方向',{exact:true}).click();await page.getByText('制作修订 · 留住每个方向',{exact:true}).click();await expect(page.getByRole('textbox',{name:'修订说明',exact:true})).toHaveValue('先保留门前的停顿')
 const accepted=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/revision-branches/main/revisions?'));await page.getByRole('button',{name:'保存当前制作修订',exact:true}).click();const response=await accepted;expect(response.status()).toBe(201);const original=await response.json();expect(original.revision.manifest.sources.assets[0].sha256).toBe(f.pngSHA);expect(original.revision.manifest.timeline).toEqual(f.media.timeline);expect(original).not.toHaveProperty('key_hash');expect(JSON.stringify(original)).not.toContain('runtime_selection')
 await page.getByRole('button',{name:'查看此修订来源',exact:true}).click();await page.getByText('查看固定创作内容',{exact:true}).click();await expect(page.getByRole('region',{name:'固定修订来源',exact:true})).toContainText('门前的迟疑');await page.getByRole('textbox',{name:'新分支名称',exact:true}).fill('更克制的方向')
 await page.locator('details.project-tools').filter({hasText:'制作修订 · 留住每个方向'}).screenshot({path:`/tmp/revisions-${info.project.name}.png`})
 const branchResponse=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/revision-branches?'));await page.getByRole('button',{name:'从此修订建立分支',exact:true}).click();expect((await branchResponse).status()).toBe(201);const fork=await(await branchResponse).json();await expect(page.getByRole('combobox',{name:'制作分支',exact:true})).toHaveValue(fork.branch.id)
 expect(fork.branch.head_revision_id).toBe(original.id);await page.getByRole('textbox',{name:'修订说明',exact:true}).fill('这个分支的第一稿')
 const saved=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes(`/revision-branches/${fork.branch.id}/revisions?`));await page.getByRole('button',{name:'保存当前制作修订',exact:true}).click();expect((await saved).status()).toBe(201);const advanced=await(await saved).json();expect(advanced.revision.parent_id).toBe(original.id);expect(advanced.revision.manifest.parent_sha256).toBe(original.revision.content_sha256)
 const media=await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json();expect(media).toEqual(f.media);const current=await(await page.request.get(api+`/projects/${f.project.id}`+f.query)).json();expect(current.version).toBe(f.project.version)
 const main=await(await page.request.get(api+`/projects/${f.project.id}/revision-branches/main/revisions`+f.query)).json();expect(main.items).toHaveLength(1);expect(main.items[0]).not.toHaveProperty('manifest');expect((await(await request.get(upstream+'/__state')).json()).calls).toBe(before.calls)
})
test('lost snapshot and fork responses recover original keys and sources after current project changes',async({page})=>{
 const f=await fixture(page);await open(page,f.project.id);await page.getByRole('textbox',{name:'修订说明',exact:true}).fill('保留旧方向');let id='',sent:unknown,key=''
 await page.route('**/revision-branches/main/revisions?*',async route=>{if(route.request().method()!=='POST')return route.continue();sent=route.request().postDataJSON();key=route.request().headers()['idempotency-key'];const r=await route.fetch();expect(r.status()).toBe(201);id=(await r.json()).id;await route.abort('failed')},{times:1})
 await page.getByRole('button',{name:'保存当前制作修订',exact:true}).click();await expect(page.getByRole('button',{name:'核对原修订请求',exact:true})).toBeVisible()
 const changed=await f.write(`/projects/${f.project.id}`,{expected_version:f.project.version,title:'后来的方向',brief:f.project.brief},'PUT');await page.reload();await expect(page.getByRole('button',{name:'核对原修订请求',exact:true})).toBeVisible()
 const recovered=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/revision-branches/main/revisions?'));await page.getByRole('button',{name:'核对原修订请求',exact:true}).click();const r=await recovered;expect(r.status()).toBe(201);expect(r.request().headers()['idempotency-key']).toBe(key);expect(r.request().postDataJSON()).toEqual(sent);expect((await r.json()).id).toBe(id)
 await page.getByRole('button',{name:'查看此修订来源',exact:true}).click();await expect(page.getByRole('region',{name:'固定修订来源',exact:true})).toContainText(f.project.title);await page.getByRole('textbox',{name:'新分支名称',exact:true}).fill('旧方向备选')
 let branch='';await page.route('**/revision-branches?*',async route=>{if(route.request().method()!=='POST')return route.continue();const r=await route.fetch();expect(r.status()).toBe(201);branch=(await r.json()).branch.id;await route.abort('failed')},{times:1})
 await page.getByRole('button',{name:'从此修订建立分支',exact:true}).click();await expect(page.getByRole('button',{name:'核对原分支请求',exact:true})).toBeVisible();await page.reload();await page.getByRole('button',{name:'核对原分支请求',exact:true}).click();await expect(page.getByRole('combobox',{name:'制作分支',exact:true})).toHaveValue(branch)
 const current=await(await page.request.get(api+`/projects/${f.project.id}`+f.query)).json();expect(current.version).toBe(changed.version);expect(current.title).toBe(changed.title)
})
test('a concurrent branch advance preserves the revision note and requires explicit version refresh',async({page})=>{
 const f=await fixture(page);await open(page,f.project.id);await page.getByRole('textbox',{name:'修订说明',exact:true}).fill('尚未提交的导演说明')
 const concurrent=await f.write(`/projects/${f.project.id}/revision-branches/main/revisions`,{expected_project_version:f.project.version,expected_media_version:f.media.version,expected_branch_version:0,expected_head_revision_id:'',label:'另一位创作者的方向'})
 await page.getByRole('button',{name:'保存当前制作修订',exact:true}).click();await expect(page.getByText('分支已有新修订，请保留说明并重新核对',{exact:true})).toBeVisible();await expect(page.getByRole('textbox',{name:'修订说明',exact:true})).toHaveValue('尚未提交的导演说明');await expect(page.getByRole('button',{name:'核对原修订请求',exact:true})).toHaveCount(0)
 await page.getByRole('button',{name:'重新核对制作版本',exact:true}).click();await expect(page.getByText('已核对最新版本，保留了你的修订说明。保存前请确认当前作品。',{exact:true})).toBeVisible();const accepted=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().includes('/revision-branches/main/revisions?'));await page.getByRole('button',{name:'保存当前制作修订',exact:true}).click();const r=await accepted;expect(r.status()).toBe(201);expect((await r.json()).revision.parent_id).toBe(concurrent.id)
})
