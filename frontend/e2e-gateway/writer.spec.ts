import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
const api='http://localhost:8084/api/v1',upstream='http://127.0.0.1:5193'
async function fixture(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`writer-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-writer-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),query='?space_id='+session.spaces[0].id,headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
 async function write(path:string,data:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 const gateway=(await write('/ai-gateways',{expected_version:0,gateway:{name:'Local writer test',base_url:upstream+'/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}})).gateways[0]
 const model=(await write('/gateway-models',{expected_version:0,model:{gateway_id:gateway.id,name:'Writer text',model:'synthetic-text',capabilities:['text'],enabled:true,max_output_tokens:2000}})).models[0]
 await write('/model-features/script_generation',{expected_version:0,binding:{feature_key:'script_generation',model_config_id:model.id,enabled:true,allow_override:true}},'PUT')
 const root=await write('/series',{title:'归途 · 编剧验收',summary:'保留作者决定与每集独立剧本。'}),season=await write(`/series/${root.id}/seasons`,{}),episode=await write(`/seasons/${season.id}/episodes`,{title:'门前的选择'})
 const original=await write(`/projects/${episode.id}/script`,{expected_version:1,script:{structure_version:2,title:'作者原稿',logline:'旅人在门前停下。',characters:[],scenes:[{id:randomUUID(),episode_number:1,heading:'门前 · 雨夜',action:'原稿保留迟疑，不自动透露来客身份。',dialogue:[]}]}},'PUT')
 return {query,headers,model,project:original,write}
}

test('writer candidates preserve official drafts, recover original requests, and require explicit versioned adoption',async({page,request},info)=>{
 const f=await fixture(page),before=(await(await request.get(upstream+'/__state')).json()).requests.length
 const genPath=`/api/v1/projects/${f.project.id}/gateway-generations`,keys:string[]=[],bodies:string[]=[]
 page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname===genPath){keys.push(r.headers()['idempotency-key']);bodies.push(r.postData()??'')}})
 await page.goto(`/projects/${f.project.id}/script`)
 await expect(page.getByRole('button',{name:'生成剧本候选',exact:true})).toBeEnabled()
 await page.route(`**${genPath}?*`,async route=>{if(route.request().method()!=='POST')return route.continue();const r=await route.fetch();expect(r.status()).toBe(202);await route.abort('failed')},{times:1})
 await page.getByRole('button',{name:'生成剧本候选',exact:true}).click();await expect(page.getByText('生成结果尚未确认。请核对原请求，系统不会另开一次模型调用。',{exact:true})).toBeVisible()
 await expect.poll(async()=>{const p=await(await page.request.get(api+`/projects/${f.project.id}`+f.query)).json();return p.latest_generation?.status}).toBe('succeeded')
 let current=await(await page.request.get(api+`/projects/${f.project.id}`+f.query)).json();expect(current.version).toBe(f.project.version);expect(current.script).toEqual(f.project.script)
 await f.write('/gateway-models/'+f.model.id,{expected_version:f.model.version,model:{id:f.model.id,gateway_id:f.model.gateway_id,name:f.model.name,model:f.model.model,capabilities:f.model.capabilities,enabled:false,max_output_tokens:2000}},'PATCH')
 page.on('dialog',d=>void d.accept());await page.reload();await page.getByRole('button',{name:'核对原生成请求',exact:true}).click();await expect(page.getByText('生成结果尚未确认。请核对原请求，系统不会另开一次模型调用。',{exact:true})).toHaveCount(0)
 expect(keys).toHaveLength(2);expect(new Set(keys).size).toBe(1);expect(new Set(bodies).size).toBe(1);expect(JSON.parse(bodies[0]).candidate_only).toBe(true)
 const sent=(await(await request.get(upstream+'/__state')).json()).requests.slice(before);expect(sent).toHaveLength(1);expect(sent[0].path).toBe('/v1/chat/completions')
 const id=current.latest_generation.id,full=await(await page.request.get(api+`/projects/${f.project.id}/generations/${id}`+f.query)).json();expect(full.candidate.structure_version).toBe(2);expect(full.source_script).toEqual(f.project.script);expect(full.runtime_selection).toBeUndefined()
 const modified=structuredClone(current.script);modified.scenes[0].action='作者后来保留了另一种动作。';current=await f.write(`/projects/${f.project.id}/script`,{expected_version:current.version,script:modified},'PUT');await page.reload()
 const card=page.locator('.script-candidate').filter({has:page.getByRole('button',{name:'比较这份候选',exact:true})}).first();await card.getByRole('button',{name:'比较这份候选',exact:true}).click()
 await card.getByText('查看生成时的原稿与简报',{exact:true}).click();await expect(card).toContainText('原稿保留迟疑，不自动透露来客身份。');await card.getByText('查看现在的正式剧本',{exact:true}).click();await expect(card).toContainText(modified.scenes[0].action)
 await card.getByRole('button',{name:'编辑剧本',exact:true}).click();await card.getByLabel('故事梗概',{exact:true}).fill('编剧调整停顿，仍然不揭示来客的身份。');await card.getByRole('button',{name:'完成候选编辑',exact:true}).click();await card.getByLabel('采纳理由（可选）',{exact:true}).fill('保留悬念，调整本集节奏。')
 const adopt=card.getByRole('button',{name:'明确采纳为正式剧本',exact:true});await expect(adopt).toBeDisabled();const check=card.getByRole('checkbox',{name:'我已比较当前版本，仍决定采纳这份候选。',exact:true});await check.check();await expect(adopt).toBeEnabled();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect((await check.boundingBox())?.width).toBeLessThanOrEqual(20);await card.screenshot({path:`/tmp/writer-candidate-${info.project.name}.png`})
 // Another editor wins after the creator's first comparison. Refresh only the
 // official source: local candidate edits and the reason must remain intact.
 const latestBrief='另一位编剧刚刚保存的本集新简报。'
 current=await f.write(`/projects/${f.project.id}`,{expected_version:current.version,title:current.title,brief:latestBrief},'PUT')
 const conflict=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname.endsWith(`/generations/${id}/adoptions`))
 await adopt.click();expect((await conflict).status()).toBe(409)
 await card.getByRole('button',{name:'核对最新正式稿',exact:true}).click();await expect(card).toContainText(latestBrief);await expect(check).not.toBeChecked();await expect(adopt).toBeDisabled();await expect(card).toContainText('编剧调整停顿，仍然不揭示来客的身份。');await expect(card.getByLabel('采纳理由（可选）',{exact:true})).toHaveValue('保留悬念，调整本集节奏。');await check.check();await expect(adopt).toBeEnabled()
 const adoptPath=`/api/v1/projects/${f.project.id}/generations/${id}/adoptions`,adoptKeys:string[]=[],adoptBodies:string[]=[];page.on('request',r=>{if(r.method()==='POST'&&new URL(r.url()).pathname===adoptPath){adoptKeys.push(r.headers()['idempotency-key']);adoptBodies.push(r.postData()??'')}})
 await page.route(`**${adoptPath}?*`,async route=>{if(route.request().method()!=='POST')return route.continue();const r=await route.fetch();expect(r.status()).toBe(200);await route.abort('failed')},{times:1});await adopt.click();await expect(card.getByText('采纳结果尚未确认。请用原请求核对，避免重复更新正式稿。',{exact:true})).toBeVisible()
 current=await(await page.request.get(api+`/projects/${f.project.id}`+f.query)).json();expect(current.script.logline).toBe('编剧调整停顿，仍然不揭示来客的身份。');const later=structuredClone(current.script);later.title='采纳后继续手写的正式稿';current=await f.write(`/projects/${f.project.id}/script`,{expected_version:current.version,script:later},'PUT')
 await page.reload();await page.getByRole('button',{name:'核对原采纳请求',exact:true}).click();await expect(page.getByText('采纳结果尚未确认。请用原请求核对，避免重复更新正式稿。',{exact:true})).toHaveCount(0)
 expect(adoptKeys).toHaveLength(2);expect(new Set(adoptKeys).size).toBe(1);expect(new Set(adoptBodies).size).toBe(1);const final=await(await page.request.get(api+`/projects/${f.project.id}`+f.query)).json();expect(final.version).toBe(current.version);expect(final.script).toEqual(later)
 const detail=await(await page.request.get(api+`/projects/${f.project.id}/generations/${id}`+f.query)).json();expect(detail.candidate).toEqual(full.candidate);expect(detail.source_script).toEqual(f.project.script);expect(detail.adoption.edited).toBe(true);expect(detail.adoption.reason).toBe('保留悬念，调整本集节奏。');expect((await(await request.get(upstream+'/__state')).json()).requests.length).toBe(before+1)
})
