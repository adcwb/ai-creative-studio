import {selectOption} from '../e2e/support/controls'
import {test,expect,type Page} from '@playwright/test'
import {randomUUID} from 'node:crypto'
const api='http://localhost:8084/api/v1',origin='http://localhost:5179'
async function account(page:Page){
 await page.goto('/');await page.getByRole('button',{name:'创建新账户',exact:true}).click();await page.locator('#auth-email').fill(`custom-role-${randomUUID()}@example.invalid`);await page.locator('#auth-password').fill('Synthetic-custom-role-password!');await page.getByRole('button',{name:'注册并登录',exact:true}).click();await expect(page.locator('.app-shell')).toBeVisible()
 const session=await(await page.request.get(api+'/auth/session')).json(),headers={Origin:origin,'X-CSRF-Token':session.csrf_token}
 async function write(path:string,data:unknown,method='POST'){const r=await page.request.fetch(api+path,{method,headers:{...headers,'Idempotency-Key':randomUUID()},data});expect(r.ok(),await r.text()).toBe(true);return r.json()}
 return {session,headers,write}
}
async function enter(page:Page,name:string){await page.goto('/settings/spaces');await page.locator('.space-card').filter({has:page.getByRole('heading',{name,exact:true})}).getByRole('button',{name:'进入空间',exact:true}).click();await page.goto('/settings/organization')}
test('custom grants affect real API and UI, recover original creation and transfer ownership explicitly',async({page,browser,request},info)=>{
 if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 const owner=await account(page),team=await owner.write('/organizations',{name:'按职责协作团队'}),memberPage=await browser.newPage({baseURL:origin,locale:'zh-CN',viewport:page.viewportSize()!})
 try{
  const member=await account(memberPage);await owner.write(`/organizations/${team.id}/members`,{email:member.session.user.email,user_id:member.session.user.id,role:'editor',expected_version:1},'PUT')
  await enter(page,'按职责协作团队');const roles=page.locator('.custom-role-panel');await roles.getByRole('button',{name:'添加权限角色',exact:true}).click();let editor=roles.locator('.custom-role-form');await editor.getByRole('textbox',{name:'角色名称',exact:true}).fill('审片协作者');await editor.getByRole('checkbox',{name:'我已核对权限变化及受影响成员',exact:true}).check()
  await page.route(`**/organizations/${team.id}/roles?*`,async route=>{if(route.request().method()!=='POST'){await route.continue();return}const r=await route.fetch();expect(r.status()).toBe(200);await route.abort('failed')},{times:1})
  await editor.getByRole('button',{name:'保存权限角色',exact:true}).click();await expect(editor.getByRole('button',{name:'使用原请求核对结果',exact:true})).toBeVisible()
  await page.reload();await roles.getByRole('button',{name:'添加权限角色',exact:true}).click();editor=roles.locator('.custom-role-form');await editor.getByRole('button',{name:'使用原请求核对结果',exact:true}).click();await expect(editor).toHaveCount(0)
  const saved=(await(await page.request.get(api+`/organizations/${team.id}/roles`)).json()).items;expect(saved).toHaveLength(1);const role=saved[0]
  await page.locator('.organization-panel>.member-list, .organization-panel>fieldset .member-list').filter({has:page.getByText(member.session.user.email,{exact:true})}).getByRole('button',{name:'调整权限',exact:true}).click()
  await selectOption(page.getByRole('combobox',{name:'成员权限',exact:true}), role.id);await page.getByRole('button',{name:'保存成员权限',exact:true}).click();await expect(page.getByRole('status').filter({hasText:'组织变更已保存。'})).toBeVisible()
  await roles.getByRole('button',{name:'编辑权限角色',exact:true}).click();editor=roles.locator('.custom-role-form');await editor.getByRole('checkbox',{name:'编辑作品',exact:true}).uncheck();await expect(editor.getByRole('checkbox',{name:'执行模型',exact:true})).not.toBeChecked();await editor.screenshot({path:`/tmp/custom-role-editor-${info.project.name}.png`});await editor.getByRole('checkbox',{name:'我已核对权限变化及受影响成员',exact:true}).check();await editor.getByRole('button',{name:'保存权限角色',exact:true}).click();await expect(editor).toHaveCount(0)
  await enter(memberPage,'按职责协作团队');await expect(memberPage.locator('.workspace-page-context')).toContainText('审片协作者');await memberPage.goto('/projects');await expect(memberPage.getByRole('link',{name:'新建短剧项目',exact:true})).toHaveCount(0)
  const rejected=await memberPage.request.post(api+`/projects?space_id=${team.id}`,{headers:{...member.headers,'Idempotency-Key':randomUUID()},data:{title:'不能创建',brief:'自定义只读权限必须作用于真实API。'}});expect(rejected.status()).toBe(403)
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true);await roles.screenshot({path:`/tmp/custom-roles-${info.project.name}.png`})
  await page.goto('/settings/spaces');const card=page.locator('.space-card').filter({has:page.getByRole('heading',{name:'按职责协作团队',exact:true})});await card.getByRole('button',{name:'移交团队所有权',exact:true}).click();await selectOption(card.getByRole('combobox',{name:'新的团队所有者',exact:true}), member.session.user.id);await expect(card.getByRole('button',{name:'确认移交所有权',exact:true})).toBeDisabled();await card.getByRole('checkbox').check();await card.screenshot({path:`/tmp/team-transfer-${info.project.name}.png`});await card.getByRole('button',{name:'确认移交所有权',exact:true}).click();await expect(card.getByText('团队所有权已交接，原所有者保留管理员权限',{exact:true})).toBeVisible()
  const current=await(await memberPage.request.get(api+`/organizations/${team.id}/access`)).json();expect(current.role).toBe('owner');expect(current.custom_role_id).toBeUndefined();expect(current.permissions).toContain('members.manage_admin')
  expect((await memberPage.request.get(api+`/projects?space_id=${owner.session.spaces[0].id}`)).status()).toBe(403)
  expect((await(await request.get('http://127.0.0.1:5193/__state')).json()).requests).toHaveLength(0)
 }finally{await memberPage.close()}
})
