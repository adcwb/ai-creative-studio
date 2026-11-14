import {selectOption} from '../e2e/support/controls'
import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
test.beforeEach(async({page},info)=>{if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})})
const api='http://localhost:8084/api/v1',origin='http://localhost:5179'
async function account(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`creator-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-creator-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),space=session.spaces[0].id,query='?space_id='+space,headers={Origin:origin,'X-CSRF-Token':session.csrf_token}
 async function write(path:string,data:unknown,method='POST',scoped=true){const r=await page.request.fetch(api+path+(scoped?query:''),{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 return {session,space,query,headers,write}
}
async function fixture(page:Page){
 const f=await account(page),project=await f.write('/projects',{title:'门前的停顿',brief:'以同一镜头比较不同声音与对白，固定素材来源。'})
 const png=Buffer.from(await page.evaluate(()=>{const c=document.createElement('canvas');c.width=90;c.height=160;const x=c.getContext('2d')!;x.fillStyle='#698275';x.fillRect(0,0,90,160);return c.toDataURL('image/png').split(',')[1]}),'base64')
 const r=await page.request.post(api+`/projects/${project.id}/media/uploads`+f.query+'&filename=frame.png&role=shot',{headers:{...f.headers,'Content-Type':'image/png','Idempotency-Key':randomUUID()},data:png});expect(r.status()).toBe(200);const uploaded=await r.json()
 const timeline={aspect_ratio:'9:16',output_resolution:'360p',clips:[{id:'first-cut',asset_id:uploaded.uploaded_asset_id,duration:2,trim_start:0,subtitle:'谁在门外？',audio_asset_id:'',audio_volume:1}],music_asset_id:'',music_volume:.2}
 const media=await f.write(`/projects/${project.id}/media/timeline`,{expected_version:uploaded.version,timeline},'PUT')
 return {...f,project,media}
}
async function screenshot(page:Page,name:string,project:string){
 await page.screenshot({path:`/tmp/creator-${name}-${project}.png`,fullPage:true})
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true)
}
test('selective restore previews explicitly, reconciles a lost response after reload and preserves later edits',async({page},info)=>{
 const f=await fixture(page),snapshot=await f.write(`/projects/${f.project.id}/revision-branches/main/revisions`,{expected_project_version:f.project.version,expected_media_version:f.media.version,expected_branch_version:0,expected_head_revision_id:'',label:'保留原对白'})
 const changed=structuredClone(f.media.timeline);changed.clips[0].subtitle='新的对白';changed.clips[0].audio_volume=.3
 const current=await f.write(`/projects/${f.project.id}/media/timeline`,{expected_version:f.media.version,timeline:changed},'PUT')
 await page.goto(`/projects/${f.project.id}/media`);await page.getByText('制作修订 · 留住每个方向',{exact:true}).click();await page.getByRole('button',{name:'查看此修订来源',exact:true}).click()
 const restore=page.getByRole('region',{name:'从修订恢复剪辑',exact:true});await restore.getByRole('button',{name:'预览恢复结果',exact:true}).click();await expect(restore).toContainText('谁在门外？');await expect(restore.getByRole('button',{name:'确认恢复剪辑',exact:true})).toBeDisabled();await restore.getByRole('checkbox').check()
 await screenshot(page,'restore',info.project.name)
 let sent:unknown,key='';await page.route('**/restore-timeline?*',async route=>{sent=route.request().postDataJSON();key=route.request().headers()['idempotency-key'];const r=await route.fetch();expect(r.status()).toBe(200);await route.abort('failed')},{times:1})
 await restore.getByRole('button',{name:'确认恢复剪辑',exact:true}).click();await expect(restore.getByRole('button',{name:'使用原请求核对结果',exact:true})).toBeVisible()
 const restored=await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json();expect(restored.version).toBe(current.version+1);expect(restored.timeline.clips[0].subtitle).toBe('谁在门外？');expect(restored.timeline.clips[0].audio_volume).toBe(.3)
 const later=structuredClone(restored.timeline);later.clips[0].subtitle='恢复后继续编辑';const latest=await f.write(`/projects/${f.project.id}/media/timeline`,{expected_version:restored.version,timeline:later},'PUT')
 await page.reload();const retry=page.getByRole('region',{name:'从修订恢复剪辑',exact:true}).getByRole('button',{name:'使用原请求核对结果',exact:true});await expect(retry).toBeVisible()
 const recovered=page.waitForResponse(r=>r.url().includes('/restore-timeline?')&&r.request().method()==='POST');await retry.click();const response=await recovered;expect(response.status()).toBe(200);expect(response.request().postDataJSON()).toEqual(sent);expect(response.request().headers()['idempotency-key']).toBe(key)
 expect((await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json()).timeline).toEqual(latest.timeline)
 expect(snapshot.id).toBeTruthy()
})
test('reference checks show newer library versions without replacing fixed project bindings',async({page},info)=>{
 const f=await fixture(page),card={name:'同一角色',description:'固定服装和轮廓',prohibited_changes:[],character:{appearance:'短发',hair:'黑色',costume:'绿色外套',expression:'克制',voice_notes:'平稳'}}
 const created=await f.write('/library/assets',{kind:'character',card,references:[]})
 const bound=await f.write(`/projects/${f.project.id}/library-bindings`,{expected_media_version:f.media.version,library_asset_id:created.asset.id,asset_version_id:created.version.id,usage:'character',shot_id:''})
 await f.write(`/library/assets/${created.asset.id}/versions`,{kind:'character',expected_asset_version_id:created.version.id,card:{...card,name:'新版角色'},references:[]})
 await page.goto(`/projects/${f.project.id}/references`);await page.getByRole('button',{name:'核对参考版本',exact:true}).click();await expect(page.getByText('库中已有新版本，请人工复查',{exact:false})).toBeVisible();await screenshot(page,'references',info.project.name)
 expect((await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json()).library_bindings).toEqual(bound.library_bindings)
})
test('season overview separates observed production facts from manually selected release status',async({page},info)=>{
 const f=await account(page),root=await f.write('/series',{title:'雨夜故事',summary:'先完成一个季集，再逐集推进。'}),season=await f.write(`/series/${root.id}/seasons`,{title:'第一季'}),episode=await f.write(`/seasons/${season.id}/episodes`,{title:'初遇'})
 await f.write(`/episodes/${episode.id}`,{expected_version:episode.row_version,profile:episode.profile,status:'released'},'PATCH')
 await page.goto(`/series/${root.id}`);await expect(page.getByRole('heading',{name:'初遇',exact:true})).toBeVisible();await expect(page.getByText('等待剧本',{exact:false})).toBeVisible();await expect(page.getByText('尚无正式成片',{exact:true})).toBeVisible();await expect(page.getByRole('link',{name:'审片与成片',exact:true})).toHaveAttribute('href',`/projects/${episode.project_id}/media`);await screenshot(page,'season',info.project.name)
})
test('completed real local render exposes versioned release metadata and a delivery ZIP without publishing',async({page},info)=>{
 const f=await fixture(page),exported=await f.write(`/projects/${f.project.id}/media/exports`,{expected_version:f.media.version})
 await expect.poll(async()=>{const state=await(await page.request.get(api+`/projects/${f.project.id}/media`+f.query)).json();return state.exports.find((v:{id:string})=>v.id===exported.id)?.status},{timeout:30000,message:'The synthetic frame must finish an actual local FFmpeg render'}).toBe('succeeded')
 await page.goto(`/projects/${f.project.id}/media`);const link=page.getByRole('link',{name:'下载发布交付包',exact:true});await expect(link).toBeVisible();const r=await page.request.get((await link.getAttribute('href'))!);expect(r.status()).toBe(200);expect(r.headers()['content-type']).toBe('application/zip');expect((await r.body()).subarray(0,2).toString()).toBe('PK')
 await page.getByText('发布资料与版本',{exact:true}).click();const preparation=page.getByRole('region',{name:'成片发布资料',exact:true})
 await preparation.getByRole('textbox',{name:'发布标题',exact:true}).fill('门前的停顿 · 正式发布')
 await preparation.getByRole('textbox',{name:'发布简介',exact:true}).fill('本次固定成片与封面；不会自动外部发布。')
 await preparation.getByRole('textbox',{name:'标签 · 每行一个',exact:true}).fill('短剧\n雨夜')
 const cover=preparation.getByRole('combobox',{name:'交付封面',exact:true});await selectOption(cover, {index:1})
 await expect(preparation.getByRole('button',{name:'保存发布资料版本',exact:true})).toBeDisabled();await preparation.getByRole('checkbox').check()
 let sent:unknown,key='';await page.route('**/delivery-metadata?*',async route=>{if(route.request().method()!=='POST'){await route.continue();return}sent=route.request().postDataJSON();key=route.request().headers()['idempotency-key'];const response=await route.fetch();expect(response.status()).toBe(200);await route.abort('failed')},{times:1})
 await preparation.getByRole('button',{name:'保存发布资料版本',exact:true}).click();await expect(preparation.getByRole('button',{name:'使用原请求核对结果',exact:true})).toBeVisible();await page.reload();await page.getByText('发布资料与版本',{exact:true}).click()
 const retry=page.getByRole('region',{name:'成片发布资料',exact:true}).getByRole('button',{name:'使用原请求核对结果',exact:true});await expect(retry).toBeVisible();const response=page.waitForResponse(r=>r.url().includes('/delivery-metadata?')&&r.request().method()==='POST');await retry.click();const replay=await response;expect(replay.status()).toBe(200);expect(replay.request().postDataJSON()).toEqual(sent);expect(replay.request().headers()['idempotency-key']).toBe(key)
 await page.getByText('已保存的发布版本',{exact:true}).click();const fixed=page.getByRole('link',{name:'下载此发布版本交付包',exact:true});await expect(fixed).toHaveCount(1);await expect(fixed).toHaveAttribute('href',/metadata_version=1/);const packed=await page.request.get((await fixed.getAttribute('href'))!);expect(packed.status()).toBe(200);expect((await packed.body()).subarray(0,2).toString()).toBe('PK');await screenshot(page,'publishing-metadata',info.project.name);await preparation.screenshot({path:`/tmp/creator-publishing-panel-${info.project.name}.png`})

})
test('team rename requires confirmation and owners cannot abandon the team',async({page},info)=>{
 const f=await account(page),team=await f.write('/organizations',{name:'共同创作团队'},'POST',false)
 await page.goto('/settings/spaces');const card=page.locator('.space-card').filter({has:page.getByRole('heading',{name:'共同创作团队',exact:true})});await card.getByRole('button',{name:'修改团队名称',exact:true}).click();await card.getByRole('textbox',{name:'团队名称',exact:true}).fill('雨夜制作组');await expect(card.getByRole('button',{name:'保存团队名称',exact:true})).toBeDisabled();await card.getByRole('checkbox').check();await card.getByRole('button',{name:'保存团队名称',exact:true}).click();await expect(page.getByRole('heading',{name:'雨夜制作组',exact:true})).toBeVisible();await expect(page.locator('.space-card').filter({has:page.getByRole('heading',{name:'雨夜制作组',exact:true})}).getByRole('button',{name:'退出团队',exact:true})).toHaveCount(0);await screenshot(page,'spaces',info.project.name)
 const state=await(await page.request.get(api+`/organizations/${team.id}/access`)).json();expect(state.name).toBe('雨夜制作组');expect(state.type).toBe('team')
})

test('a team member explicitly leaves without deleting the team or retaining its access',async({page,browser})=>{
 const owner=await account(page),team=await owner.write('/organizations',{name:'可退出的协作团队'},'POST',false)
 const memberPage=await browser.newPage({baseURL:origin,locale:'zh-CN',viewport:page.viewportSize()!})
 try{
  const member=await account(memberPage)
  await owner.write(`/organizations/${team.id}/members`,{email:member.session.user.email,user_id:member.session.user.id,role:'editor'},'PUT',false)
  await memberPage.goto('/settings/spaces');const card=memberPage.locator('.space-card').filter({has:memberPage.getByRole('heading',{name:'可退出的协作团队',exact:true})})
  await card.getByRole('button',{name:'退出团队',exact:true}).click();await expect(card.getByRole('button',{name:'确认退出团队',exact:true})).toBeDisabled();await card.getByRole('checkbox').check();await card.getByRole('button',{name:'确认退出团队',exact:true}).click();await expect(card).toHaveCount(0)
  expect((await memberPage.request.get(api+`/organizations/${team.id}/access`)).status()).toBe(403)
  expect((await page.request.get(api+`/organizations/${team.id}/access`)).status()).toBe(200)
 }finally{await memberPage.close()}
})
