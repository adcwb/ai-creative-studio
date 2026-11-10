import {mkdirSync} from 'node:fs'
import {test, expect} from '@playwright/test'
import {navigateTo, openNavigation, closeNavigation, chooseLanguage} from '../e2e/support/navigation'

const api = 'http://localhost:8084/api/v1'
mkdirSync('/tmp/studio-consumer', {recursive: true})
test('workspace guidance, honest model state and persistent navigation preserve existing records', async ({page, request}, info) => {
  test.setTimeout(90000)
  await page.setViewportSize(info.project.name === 'desktop' ? {width:1920,height:1080} : {width:390,height:844})
  await page.goto('/')
  await page.getByRole('button', {name:'创建新账户',exact:true}).click()
  await page.locator('#auth-email').fill(`consumer-${info.project.name}-${crypto.randomUUID()}@example.invalid`)
  await page.locator('#auth-password').fill('Synthetic-consumer-workflow-password!')
  await page.getByRole('button', {name:'注册并登录',exact:true}).click()
  await expect(page.locator('.app-shell')).toBeVisible()
  await expect(page.getByLabel('描述你的画面',{exact:true})).toBeVisible()
  await expect(page.getByRole('region',{name:'工作空间提示'})).toHaveCount(0)
  const session = await (await page.request.get(api+'/auth/session')).json()
  const personal = session.spaces[0].id
  const headers = {Origin:'http://localhost:5179','X-CSRF-Token':session.csrf_token}
  const teamResponse = await page.request.post(api+'/organizations', {headers,data:{name:'林间创意团队'}})
  expect(teamResponse.status()).toBe(201)
  const team = await teamResponse.json(), query = '?space_id='+team.id
  // Seed historical synthetic work only in this local fixture. UI must keep its provenance.
  const historic = await page.request.post(api+'/tasks'+query, {headers:{...headers,'Idempotency-Key':crypto.randomUUID()},data:{prompt:'本地历史模拟记录，非AI生成',kind:'general',ratio:'1:1',style:'natural',expected_provider:'mock-svg',expected_model:''}})
  expect(historic.status(), await historic.text()).toBe(202)
  const task = await historic.json()
  await expect.poll(async()=> (await (await page.request.get(api+'/tasks/'+task.id+query)).json()).status).toBe('succeeded')
  const gatewayResponse = await page.request.post(api+'/ai-gateways'+query, {headers,data:{expected_version:0,gateway:{name:'本地协议网关',base_url:'http://127.0.0.1:5193/v1',enabled:true,timeout_seconds:30},credential:{action:'set',secret:'synthetic-key-A'}}})
  expect(gatewayResponse.status()).toBe(200)
  const gateway = (await gatewayResponse.json()).gateways[0]
  const capabilities = ['text','vision','image','image_edit','video','speech','asr','asr_timestamps']
  const configured = await page.request.post(api+'/gateway-models'+query, {headers,data:{expected_version:0,model:{gateway_id:gateway.id,name:'手工声明的测试模型',model:'synthetic-image',capabilities,enabled:true,max_output_tokens:512,json_output:true,max_reference_images:1,video_seconds:4,video_size:"1280x720"}}})
  expect(configured.status(), await configured.text()).toBe(200)
  const model = (await configured.json()).models[0]
  expect((await page.request.put(api+'/model-features/image_generation'+query, {headers,data:{expected_version:0,binding:{feature_key:'image_generation',model_config_id:model.id,enabled:true,allow_override:true}}})).status()).toBe(200)
  const before = (await (await request.get('http://127.0.0.1:5193/__state')).json()).calls
  await page.reload()
  const guide = page.getByRole('region', {name:'工作空间提示'})
  await expect(guide).toBeVisible()
  await expect(page.locator('.workspace-page-context')).toContainText('个人')
  await expect(page.getByRole('button',{name:'创建图像',exact:true})).toBeDisabled()
  expect(await page.locator('.prompt-tabs button').evaluateAll(nodes=>nodes.every(n=>n.getBoundingClientRect().height<80))).toBe(true)
  await page.screenshot({path:`/tmp/studio-consumer/personal-${info.project.name}.png`})
  await guide.getByRole('button',{name:'进入团队空间',exact:true}).click()
  await expect(page.locator('.workspace-page-context')).toContainText('林间创意团队')
  await expect(guide).toHaveCount(0)
  expect(await (await page.request.get(api+'/tasks?space_id='+personal)).json()).toEqual([])
  await expect(page.getByText('模板只填入提示词与参数，不代表已经生成的作品。',{exact:true})).toBeVisible()
  await expect(page.getByLabel('画面比例',{exact:true}).locator('option[value="9:16"]')).toHaveCount(1)
  await page.getByRole('button',{name:'保存当前描述',exact:true}).click()
  const editor=page.getByRole('dialog',{name:'保存自己的提示词',exact:true})
  await editor.getByLabel('提示词名称',{exact:true}).fill('竖屏构图草稿')
  await editor.getByLabel('提示词正文',{exact:true}).fill('固定主体，竖屏构图，用于本地参数保存测试')
  await editor.getByLabel('画面比例',{exact:true}).selectOption('9:16')
  const saved=page.waitForResponse(r=>r.url().includes('/prompt-templates?')&&r.request().method()==='POST')
  await editor.getByRole('button',{name:'保存提示词',exact:true}).click()
  const recipe=await saved
  expect(recipe.status()).toBe(201)
  expect((await recipe.json()).version.defaults.ratio).toBe('9:16')
  await expect(editor).toBeHidden()

  await page.reload()
  await expect(page.locator('.task[data-provider="mock-svg"]')).toContainText('模拟图像 · 非 AI 生成')
  await expect(page.locator('.provider-badge')).toHaveText('网关模型创作')
  for (const path of ['/assets','/settings/models','/settings/preferences']) {
    await navigateTo(page,path)
    await expect(page.locator('.provider-badge')).toHaveText('当前工作空间')
  }
  await navigateTo(page,'/settings/models')
  await expect(page.getByText('配置就绪',{exact:true})).toBeVisible()
  await expect(page.getByText('配置就绪仅表示本地配置条件满足，不代表上游能力、凭据或生成效果已经实测。',{exact:true})).toBeVisible()
  await page.screenshot({path:`/tmp/studio-consumer/models-${info.project.name}.png`})
  const after = await (await page.request.get(api+'/model-management'+query)).json()
  expect(after.models[0].capabilities).toEqual(model.capabilities)
  expect(after.models[0].capabilities).toHaveLength(8)
  expect((await (await request.get('http://127.0.0.1:5193/__state')).json()).calls).toBe(before)
  await chooseLanguage(page,'en')
  if (info.project.name === 'desktop') await page.setViewportSize({width:1366,height:768})
  await openNavigation(page)
  for (const group of await page.locator('.group-toggle').all()) if (await group.getAttribute('aria-expanded') === 'false') await group.click()
  const brand = await page.locator('.sidebar-brand').boundingBox()
  const picker = await page.locator('.space-picker').boundingBox()
  await page.locator('.sidebar-navigation').evaluate(node => {node.scrollTop=node.scrollHeight})
  expect(await page.locator('.sidebar-navigation').evaluate(node=>node.scrollTop)).toBeGreaterThan(0)
  await expect(page.locator('.sidebar-brand')).toBeInViewport()
  await expect(page.locator('.space-picker')).toBeInViewport()
  await expect(page.locator('.sidebar-settings-shortcut')).toBeInViewport()
  expect((await page.locator('.sidebar-brand').boundingBox())!.y).toBe(brand!.y)
  expect((await page.locator('.space-picker').boundingBox())!.y).toBe(picker!.y)
  expect(await page.locator('.app-nav a>span').evaluateAll(nodes=>nodes.filter(n=>n.getClientRects().length).every(n=>n.scrollWidth<=n.clientWidth+1))).toBe(true)
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await page.screenshot({path:`/tmp/studio-consumer/navigation-${info.project.name}.png`})
  if (info.project.name==='mobile') {await page.keyboard.press('Escape');await expect(page.locator('.menu-toggle')).toBeFocused()}
  else await closeNavigation(page)
})
