import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
const api='http://localhost:8084/api/v1'
async function fixture(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`season-delivery-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-season-delivery-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),query='?space_id='+session.spaces[0].id,headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token,'Idempotency-Key':randomUUID()}
 async function write(path:string,data:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 return {query,headers,write}
}
test('season delivery shows missing episodes, requires review and downloads two actual local renders',async({page},info)=>{
 if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 const f=await fixture(page),root=await f.write('/series',{title:'门外的两次敲击',summary:'两集共用同一参考画面，检验整季交付。'}),season=await f.write(`/series/${root.id}/seasons`,{title:'雨夜'}),episodes=[]
 for(const title of ['第一次敲门','第二次敲门'])episodes.push(await f.write(`/seasons/${season.id}/episodes`,{title}))
 await page.goto(`/series/${root.id}`)
 const panel=page.locator('.season-delivery');await panel.locator('summary').click();await panel.getByRole('button',{name:'检查本季交付',exact:true}).click();await expect(panel.getByText('尚无正式成片，请进入审片与成片完成导出。',{exact:true})).toHaveCount(2);await expect(panel.getByRole('link',{name:'下载整季交付包',exact:true})).toHaveCount(0)
 const png=Buffer.from(await page.evaluate(()=>{const c=document.createElement('canvas');c.width=90;c.height=160;c.getContext('2d')!.fillRect(0,0,90,160);return c.toDataURL('image/png').split(',')[1]}),'base64')
 for(const ep of episodes){
  const r=await page.request.post(api+`/projects/${ep.project_id}/media/uploads`+f.query+'&filename=frame.png&role=shot',{headers:{...f.headers,'Content-Type':'image/png','Idempotency-Key':randomUUID()},data:png});expect(r.status()).toBe(200);const uploaded=await r.json()
  const media=await f.write(`/projects/${ep.project_id}/media/timeline`,{expected_version:uploaded.version,timeline:{aspect_ratio:'9:16',output_resolution:'360p',clips:[{id:'first-cut',asset_id:uploaded.uploaded_asset_id,duration:2,trim_start:0,subtitle:ep.title,audio_asset_id:'',audio_volume:1}],music_asset_id:'',music_volume:.2}},'PUT')
  const exp=await f.write(`/projects/${ep.project_id}/media/exports`,{expected_version:media.version})
  await expect.poll(async()=>{const state=await(await page.request.get(api+`/projects/${ep.project_id}/media`+f.query)).json();return state.exports.find((v:{id:string})=>v.id===exp.id)?.status},{timeout:30000}).toBe('succeeded')
 }
 await panel.getByRole('button',{name:'重新检查本季交付',exact:true}).click();await expect(panel.getByText('尚无正式成片，请进入审片与成片完成导出。',{exact:true})).toHaveCount(0);await expect(panel.getByRole('checkbox')).toBeVisible();await expect(panel.getByRole('link',{name:'下载整季交付包',exact:true})).toHaveCount(0)
 await panel.getByRole('checkbox').check();const link=panel.getByRole('link',{name:'下载整季交付包',exact:true});await expect(link).toBeVisible();const href=(await link.getAttribute('href'))!
 const response=await page.request.get(href);expect(response.status()).toBe(200);expect(response.headers()['content-type']).toBe('application/zip');expect((await response.body()).subarray(0,2).toString()).toBe('PK')
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);await page.screenshot({path:`/tmp/season-delivery-${info.project.name}.png`,fullPage:true})
 // A later directory edit invalidates the reviewed selection before streaming.
 await f.write(`/seasons/${season.id}/episodes`,{title:'尚未完成的第三集'})
 expect((await page.request.get(href)).status()).toBe(409)
})
