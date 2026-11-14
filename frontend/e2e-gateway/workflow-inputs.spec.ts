import {test,expect} from '@playwright/test'
import {randomUUID} from 'node:crypto'
const api='http://localhost:8084/api/v1',upstream='http://127.0.0.1:5193'
test('runtime variables and actual text model survive explicit local remake without repeating unaffected nodes',async({page,request},info)=>{
 if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`workflow-input-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-input-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),space=session.spaces[0].id,query='?space_id='+space,headers={Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
 async function write(path:string,body:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data:body});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 const project=await write('/projects',{title:'运行变量与文本改写',brief:'只使用本地协议，验证真实队列、输入和人工确认。'})
 const gateways=await write('/ai-gateways',{expected_version:0,gateway:{name:'Workflow text',base_url:upstream+'/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}})
 const models=await write('/gateway-models',{expected_version:0,model:{gateway_id:gateways.gateways[0].id,name:'Text transform',model:'synthetic-text',capabilities:['text'],enabled:true,max_output_tokens:512,json_output:true}}),model=models.models[0]
 await write('/model-features/workflow_text',{expected_version:0,binding:{feature_key:'workflow_text',model_config_id:model.id,enabled:true,allow_override:true}},'PUT')
 const graph={variables:[{name:'subject',label:'本次主体',required:true},{name:'location',label:'本次场景',required:false,default:'garden'}],nodes:[{id:'subject',kind:'variable',label:'主体输入',variable_name:'subject',x:0,y:0},{id:'template',kind:'template',label:'描述组合',text:'{{input}} in {{location}}',x:240,y:0},{id:'llm',kind:'llm',label:'文本改写',text:'保留主体与场景，改写成清晰镜头描述。',model_config_id:model.id,model_config_version:model.version,x:480,y:0},{id:'review',kind:'review',label:'确认文案',x:720,y:0},{id:'output',kind:'output',label:'文案输出',x:960,y:0}],edges:[{source:'subject',target:'template',input:'prompt'},{source:'template',target:'llm',input:'prompt'},{source:'llm',target:'review',input:'media'},{source:'review',target:'output',input:'media'}]}
 const saved=await write(`/projects/${project.id}/workflows`,{name:'每次输入，不改模板',expected_version:0,graph}),base=`/projects/${project.id}`,before=(await(await request.get(upstream+'/__state')).json()).requests.length
 await page.goto(`/workflows/${project.id}`);await page.getByRole('combobox',{name:'已保存流程',exact:true}).selectOption(saved.id)
 await expect(page.getByRole('button',{name:'运行已保存流程',exact:true})).toBeDisabled()
 await page.getByRole('region',{name:'本次运行输入',exact:true}).getByLabel('本次主体',{exact:true}).fill('{{literal}} hero')
 await page.getByRole('region',{name:'本次运行输入',exact:true}).getByLabel('本次场景',{exact:true}).fill('studio')
 await page.locator('.workflow-step-list summary').click();await page.locator('.workflow-step-list').getByRole('button',{name:'文本改写',exact:true}).click()
 await expect(page.getByRole('textbox',{name:'文本处理要求',exact:true})).toHaveValue('保留主体与场景，改写成清晰镜头描述。')
 await expect(page.getByRole('combobox',{name:'模型选择',exact:true})).toHaveValue(model.id)
 await expect(page.getByRole('button',{name:'运行已保存流程',exact:true})).toBeEnabled()
 const starting=page.waitForResponse(r=>r.url().includes(`/workflows/${saved.id}/runs?`)&&r.request().method()==='POST');await page.getByRole('button',{name:'运行已保存流程',exact:true}).click();const response=await starting,original=await response.json(),originalBody=response.request().postDataJSON(),originalKey=response.request().headers()['idempotency-key']
 await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择')
 const jobs=await(await page.request.get(api+base+'/creative-jobs'+query)).json();expect(jobs).toHaveLength(1);expect(jobs[0]).toMatchObject({kind:'text',provider:'gateway-text',status:'succeeded',prompt:'{{literal}} hero in studio',text_instruction:'保留主体与场景，改写成清晰镜头描述。',text_result:'{{literal}} hero in studio / transformed'})
 let state=await(await page.request.get(api+base+`/workflow-runs/${original.id}`+query)).json();expect(state.inputs).toEqual({subject:'{{literal}} hero',location:'studio'});expect(state.nodes.output.status).toBe('queued')
 const changed=await page.request.post(api+base+`/workflows/${saved.id}/runs`+query,{headers:{...headers,'Idempotency-Key':originalKey},data:{...originalBody,inputs:{subject:'changed'}}});expect(changed.status()).toBe(409)
 const foreign=await page.request.get(api+base+`/workflow-runs/${original.id}?space_id=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb`);expect(foreign.status()).toBe(403)
 await page.getByRole('checkbox',{name:'我已检查此素材，继续后续步骤',exact:true}).check();await page.getByRole('button',{name:'确认采用并继续',exact:true}).click();await expect(page.locator('.workflow-run>p[role=status]')).toContainText('已完成')
 const article=page.locator('.workflow-run article').filter({has:page.getByRole('heading',{name:/^文本改写/})})
 await article.getByRole('button',{name:'从此步骤重新制作',exact:true}).click();await expect(article.getByRole('button',{name:'开始局部重做',exact:true})).toBeDisabled();await article.getByRole('checkbox',{name:'确认重新执行此步骤及下游',exact:true}).check()
 let remake='',key='',body:unknown;await page.route('**/workflow-runs/*/reruns?*',async route=>{key=route.request().headers()['idempotency-key'];body=route.request().postDataJSON();const result=await route.fetch();expect(result.status()).toBe(200);remake=(await result.json()).id;await route.abort('failed')},{times:1})
 await article.getByRole('button',{name:'开始局部重做',exact:true}).click();await expect(article.getByRole('button',{name:'使用原请求核对结果',exact:true})).toBeVisible();await page.reload()
 const recovering=page.waitForResponse(r=>r.url().includes(`/workflow-runs/${original.id}/reruns?`)&&r.request().method()==='POST');await page.locator('.workflow-run article').filter({has:page.getByRole('heading',{name:/^文本改写/})}).getByRole('button',{name:'使用原请求核对结果',exact:true}).click();const recovered=await recovering;expect(recovered.request().headers()['idempotency-key']).toBe(key);expect(recovered.request().postDataJSON()).toEqual(body);expect((await recovered.json()).id).toBe(remake)
 await expect(page.locator('.workflow-run>p[role=status]')).toContainText('等待人工选择')
 state=await(await page.request.get(api+base+`/workflow-runs/${remake}`+query)).json();expect(state.from_run_id).toBe(original.id);expect(state.nodes.subject.reused_from_run_id).toBe(original.id);expect(state.nodes.template.reused_from_run_id).toBe(original.id);expect(state.nodes.llm.job_id).not.toBe(jobs[0].id);expect(state.nodes.output.status).toBe('queued')
 const sent=(await(await request.get(upstream+'/__state')).json()).requests.slice(before).filter((r:{path:string})=>r.path==='/v1/chat/completions');expect(sent).toHaveLength(2);expect(JSON.parse(sent[0].body.messages[1].content)).toEqual({instruction:'保留主体与场景，改写成清晰镜头描述。',input:'{{literal}} hero in studio'})
 expect((await(await page.request.get(api+base+`/workflow-runs/${original.id}`+query)).json()).status).toBe('succeeded')
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true)
 await page.evaluate(()=>scrollTo(0,0))
 await page.screenshot({path:`/tmp/workflow-runtime-viewport-${info.project.name}.png`})
 await page.screenshot({path:`/tmp/workflow-runtime-remake-${info.project.name}.png`,fullPage:true})
})
