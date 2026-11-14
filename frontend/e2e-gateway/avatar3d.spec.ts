import {test,expect} from '@playwright/test'
import {randomUUID} from 'node:crypto'
test.use({launchOptions:{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,args:['--use-angle=swiftshader','--enable-unsafe-swiftshader']}})
test('3D creator drafts and voices through the configured gateway before authorized browser playback',async({page,request},info)=>{
 test.setTimeout(90000)
 const api='http://localhost:8084/api/v1',upstream='http://127.0.0.1:5193'
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`three-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-three-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),space=session.spaces[0].id,query='?space_id='+space
 async function write(path:string,body:unknown,method='POST'){const r=await page.request.fetch(api+path+query,{method,headers:{Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token,'Idempotency-Key':randomUUID()},data:body});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 const project=await write('/projects',{title:'网页三维口播验收',brief:'验证文案、语音协议和授权后的真实WebGL播放，不是真实供应商作品。',kind:'digital-human'})
 const gateway=await write('/ai-gateways',{expected_version:0,gateway:{name:'3D synthetic gateway',base_url:upstream+'/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}})
 await write('/gateway-models',{expected_version:0,model:{gateway_id:gateway.gateways[0].id,name:'3D copy',model:'synthetic-text',capabilities:['text'],enabled:true,max_output_tokens:1024,json_output:true}})
 const speechState=await write('/gateway-models',{expected_version:0,model:{gateway_id:gateway.gateways[0].id,name:'3D speech',model:'synthetic-speech',capabilities:['speech'],enabled:true,max_output_tokens:512}})
 const textModel=speechState.models.find((v:{model:string})=>v.model==='synthetic-text'),speechModel=speechState.models.find((v:{model:string})=>v.model==='synthetic-speech')
 await write('/model-features/workflow_text',{expected_version:0,binding:{feature_key:'workflow_text',model_config_id:textModel.id,enabled:true,allow_override:true}},'PUT')
 await write('/model-features/speech_synthesis',{expected_version:0,binding:{feature_key:'speech_synthesis',model_config_id:speechModel.id,enabled:true,allow_override:true,voice:'alloy'}},'PUT')
 const before=(await(await request.get(upstream+'/__state')).json()).requests.length
 await page.goto(`/digital-humans/3d/${project.id}`);if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 await page.getByText('添加三维角色',{exact:true}).click();await page.getByRole('button',{name:'保存并使用CC0角色',exact:true}).click()
 const stage=page.getByRole('region',{name:'网页三维角色预览',exact:true});await expect(stage.locator('[data-webgl-ready]')).toHaveAttribute('data-webgl-ready','true',{timeout:30000})
 await page.getByText('用文本模型起草口播',{exact:true}).click();const copy='请用中文介绍这个虚构角色。';await page.getByRole('textbox',{name:'口播创作要求',exact:true}).fill(copy)
 await expect(page.getByRole('button',{name:'生成口播文案',exact:true})).toBeEnabled();await page.getByRole('button',{name:'生成口播文案',exact:true}).click()
 await expect(page.getByRole('button',{name:'采用此口播文案',exact:true})).toBeVisible();await page.getByRole('button',{name:'采用此口播文案',exact:true}).click();await expect(page.getByRole('textbox',{name:'口播文案',exact:true})).toHaveValue(copy+' / transformed')
 await expect(page.getByRole('button',{name:'生成配音',exact:true})).toBeEnabled();await page.getByRole('button',{name:'生成配音',exact:true}).click();await expect(page.getByRole('button',{name:'使用此配音',exact:true})).toBeVisible();await page.getByRole('button',{name:'使用此配音',exact:true}).click()
 await page.getByRole('textbox',{name:'声音授权依据',exact:true}).fill('Synthetic protocol fixture voice permission');await page.getByRole('checkbox',{name:'我确认有权使用所选配音制作此口播',exact:true}).check();await page.getByRole('button',{name:'保存可复用声音授权',exact:true}).click()
 await expect(stage.getByRole('button',{name:'播放三维口播',exact:true})).toBeEnabled();await stage.getByRole('button',{name:'播放三维口播',exact:true}).click();await expect(stage.getByRole('status')).toHaveText('正在播放')
 const sent=(await(await request.get(upstream+'/__state')).json()).requests.slice(before)
 expect(sent.filter((r:{path:string})=>r.path==='/v1/chat/completions')).toHaveLength(1)
 expect(sent.filter((r:{path:string})=>r.path==='/v1/audio/speech')).toEqual([expect.objectContaining({body:expect.objectContaining({model:'synthetic-speech',voice:'alloy',input:copy+' / transformed'})})])
 expect(JSON.stringify(sent)).not.toContain('mpfb');expect(JSON.stringify(sent)).not.toContain('model_sha256')
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true)
 await stage.locator('canvas').screenshot({path:`/tmp/avatar3d-gateway-${info.project.name}.png`})
})
