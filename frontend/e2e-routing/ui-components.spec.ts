import {expect,test} from '@playwright/test'
import {selectOption} from '../e2e/support/controls'

const fixture='http://127.0.0.1:5189'
test.beforeEach(async({request})=>{await request.post(`${fixture}/__test__/reset`)})

test('component editor preserves draft across resize and keeps busy saves open',async({page},info)=>{
  await page.goto('/settings/models')
  await page.getByRole('tab',{name:'AI 网关',exact:true}).click()
  await page.getByRole('button',{name:'添加 AI 网关',exact:true}).click()
  const dialog=page.getByRole('dialog',{name:'添加 AI 网关',exact:true})
  const name=dialog.getByLabel('网关名称',{exact:true})
  await name.fill('跨断点保留的草稿')
  await page.setViewportSize({width:info.project.name==='mobile'?1200:390,height:900})
  await expect(name).toHaveValue('跨断点保留的草稿')
  await expect(page.getByRole('dialog')).toHaveCount(1)
  await dialog.getByLabel('服务地址',{exact:true}).fill('https://synthetic.example/v1')
  await selectOption(dialog.getByLabel('凭据操作',{exact:true}),'keep')
  let release!:()=>void
  const pending=new Promise<void>(resolve=>{release=resolve})
  await page.route('**/ai-gateways?*',async route=>{await pending;await route.continue()})
  try {
    await dialog.getByRole('button',{name:'保存网关',exact:true}).click()
    await expect(dialog.getByRole('button',{name:'取消',exact:true})).toBeDisabled()
    await page.keyboard.press('Escape')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByRole('combobox',{name:'厂商',exact:true})).toBeDisabled()
    release()
    await expect(dialog).toHaveCount(0)
    await expect(page.locator('.gateway-table')).toContainText('跨断点保留的草稿')
  } finally {release()}
})
