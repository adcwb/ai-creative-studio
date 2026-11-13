import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
const api='http://localhost:8084/api/v1',upstream='http://127.0.0.1:5193'
async function fixture(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`outline-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-outline-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),query='?space_id='+session.spaces[0].id,headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
 async function write(path:string,data:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 const gateway=await write('/ai-gateways',{expected_version:0,gateway:{name:'Local outline gateway',base_url:upstream+'/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}})
 const models=await write('/gateway-models',{expected_version:0,model:{gateway_id:gateway.gateways[0].id,name:'Local text',model:'synthetic-text',capabilities:['text'],enabled:true,max_output_tokens:512}})
 await write('/model-features/script_generation',{expected_version:0,binding:{feature_key:'script_generation',model_config_id:models.models[0].id,enabled:true,allow_override:true}},'PUT')
 const root=await write('/series',{title:'门外的选择',summary:'同一个人物在两次敲门之间决定是否信任同伴。'}),season=await write(`/series/${root.id}/seasons`,{title:'雨夜'}),ep=await write(`/seasons/${season.id}/episodes`,{title:'第一次敲门'})
 return {query,headers,write,root,season,ep}
}
async function open(page:Page,root:string,episode:string){
 await page.goto(`/series/${root}`);await page.getByText('编剧工作台 · 季与集的节拍',{exact:true}).click();await page.getByLabel('规划范围',{exact:true}).selectOption(episode);await page.getByText('AI 季集纲候选 · 先比较再采纳',{exact:true}).click();return page.getByRole('region',{name:'集纲候选工作区',exact:true})
}
test('a real text request stays a candidate until explicit edited adoption, with original-request recovery',async({page,request},info)=>{
 if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 const f=await fixture(page),before=(await(await request.get(upstream+'/__state')).json()).requests.length
 let panel=await open(page,f.root.id,f.ep.id)
 await panel.getByRole('button',{name:'核对本次策划来源',exact:true}).click();await expect(panel.getByText('本次固定来源 · 0 份正式计划',{exact:true})).toBeVisible();await panel.getByLabel('这次希望怎样推进故事',{exact:true}).fill('角色先试探再作出选择')
 await expect(panel.getByRole('button',{name:'生成集纲候选',exact:true})).toBeEnabled()
 let sent:unknown,key=''
 await page.route(`**/seasons/${f.season.id}/outline-candidates?*`,async route=>{if(route.request().method()!=='POST'){await route.continue();return}sent=route.request().postDataJSON();key=route.request().headers()['idempotency-key'];const r=await route.fetch();expect(r.status()).toBe(202);await route.abort('failed')},{times:1})
 await panel.getByRole('button',{name:'生成集纲候选',exact:true}).click();await expect(panel.getByRole('button',{name:'使用原请求核对结果',exact:true})).toBeVisible()
 panel=await open(page,f.root.id,f.ep.id)
 const recovered=page.waitForResponse(r=>r.url().includes(`/seasons/${f.season.id}/outline-candidates?`)&&r.request().method()==='POST');await panel.getByRole('button',{name:'使用原请求核对结果',exact:true}).click();const result=await recovered;expect(result.status()).toBe(202);expect(result.request().postDataJSON()).toEqual(sent);expect(result.request().headers()['idempotency-key']).toBe(key)
 const review=panel.getByRole('region',{name:'比较与采纳集纲候选',exact:true});await expect(review).toBeVisible();expect((await(await page.request.get(api+`/seasons/${f.season.id}/writing-beats`+f.query+'&episode_id='+f.ep.id)).json()).current).toBeUndefined()
 await review.getByLabel('结尾与下一段钩子',{exact:true}).fill('作者确认：门外传来第二种声音');await expect(review.getByRole('button',{name:'确认采纳集纲',exact:true})).toBeDisabled();await review.getByRole('checkbox').check()
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);await page.screenshot({path:`/tmp/outline-candidates-${info.project.name}.png`,fullPage:true});await review.screenshot({path:`/tmp/outline-candidates-review-${info.project.name}.png`})
 await page.route('**/outline-candidates/*/adopt?*',async route=>{const r=await route.fetch();expect(r.status()).toBe(200);await route.abort('failed')},{times:1})
 await review.getByRole('button',{name:'确认采纳集纲',exact:true}).click();await expect(review.getByRole('button',{name:'使用原请求核对结果',exact:true})).toBeVisible()
 const formal=(await(await page.request.get(api+`/seasons/${f.season.id}/writing-beats`+f.query+'&episode_id='+f.ep.id)).json()).current;expect(formal.number).toBe(1);expect(formal.body.outline.hook).toBe('作者确认：门外传来第二种声音')
 const later={expected_version:1,title:formal.title,kind:'outline',status:'active',required:true,body:{...formal.body,outline:{...formal.body.outline,hook:'采纳后继续修改，不应被重放覆盖'}}}
 const changed=await page.request.put(api+`/seasons/${f.season.id}/writing-beats`+f.query+'&episode_id='+f.ep.id,{headers:{...f.headers,'Idempotency-Key':randomUUID()},data:later});expect(changed.status()).toBe(200)
 panel=await open(page,f.root.id,f.ep.id);await panel.getByRole('button',{name:'查看与比较集纲候选',exact:true}).click();const retry=panel.getByRole('region',{name:'比较与采纳集纲候选',exact:true});await retry.getByRole('button',{name:'使用原请求核对结果',exact:true}).click();await expect(retry.getByText('集纲已保存为正式节拍，原候选和历史版本保留。',{exact:true})).toBeVisible()
 const current=(await(await page.request.get(api+`/seasons/${f.season.id}/writing-beats`+f.query+'&episode_id='+f.ep.id)).json()).current;expect(current.number).toBe(2);expect(current.body.outline.hook).toBe(later.body.outline.hook)
 const requests=(await(await request.get(upstream+'/__state')).json()).requests.slice(before);expect(requests).toHaveLength(1);expect(requests[0].path).toBe('/v1/chat/completions');expect(requests[0].body.messages[1].content).toContain(f.ep.id)
})
