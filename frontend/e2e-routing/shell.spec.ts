import {test,expect} from '@playwright/test'
import {navigateTo,openNavigation,closeNavigation} from '../e2e/support/navigation'
const fixture='http://127.0.0.1:5189'
test.beforeEach(async({request})=>{await request.post(fixture+'/__test__/reset')})
test('navigation groups and compact sidebar remain reachable, keyboard operable and persistent',async({page},info)=>{
 await page.goto('/digital-humans/video')
 await openNavigation(page)
 await expect(page.locator('.sidebar .language-picker')).toHaveCount(0)
 const group=page.locator('[data-group=avatars]')
 const toggle=group.locator('.group-toggle')
 await expect(group).toHaveAttribute('data-active','true')
 await toggle.focus();await page.keyboard.press('Enter')
 await expect(toggle).toHaveAttribute('aria-expanded','false')
 await expect(group.locator('.nav-subgroup')).toBeHidden()
 await page.reload();await openNavigation(page)
 await expect(toggle).toHaveAttribute('aria-expanded','false')
 await toggle.click()
 await group.getByRole('link',{name:'交互型数字人',exact:true}).click()
 await expect(page).toHaveURL(/digital-humans\/interactive$/)
 await expect(page.locator('.workspace-page-context strong')).toHaveText('交互型数字人')
 if(info.project.name==='desktop'){
  await page.getByRole('button',{name:'收起侧栏',exact:true}).click()
  await expect(page.locator('.app-shell')).toHaveClass(/navigation-collapsed/)
  await page.reload()
  await expect(page.locator('.app-shell')).toHaveClass(/navigation-collapsed/)
  await page.getByRole('link',{name:'素材文件',exact:true}).click()
  await expect(page).toHaveURL(/\/assets$/)
  await expect(page.getByRole('link',{name:'素材文件',exact:true})).toHaveAttribute('aria-current','page')
  await page.getByRole('button',{name:'展开侧栏',exact:true}).click()
 }
 await navigateTo(page,'/settings/preferences')
 await expect(page.locator('.preferences-page .language-buttons')).toBeVisible()
 await closeNavigation(page)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
})
test('every workspace route has one shared context header across themes and languages',async({page},info)=>{
 await page.goto('/settings/preferences')
 for(const locale of ['zh-CN','en']){
  await page.locator('.preferences-page .language-buttons').getByRole('button',{name:locale==='en'?'English':'简体中文',exact:true}).click()
  for(const theme of ['forest','paper','screening']){
   await page.locator('.theme-option').filter({has:page.locator('.sample-'+theme)}).click()
   await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme',theme)
   await expect(page.locator('.workspace-page-header')).toHaveCount(1)
   await expect(page.locator('.workspace-page-context strong')).toHaveText(locale==='en'?'Personal settings':'系统设置')
   await page.screenshot({path:`test-results/shell-${info.project.name}-${locale}-${theme}.png`,fullPage:true})
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  }
 }
 for(const route of ['/studio','/studio/workspaces','/assets','/library','/publish','/projects','/projects/story-1/brief','/projects/story-1/script','/projects/story-1/storyboard','/projects/story-1/production','/projects/story-1/review','/digital-humans/video','/digital-humans/interactive','/digital-humans/3d','/settings/models','/settings/billing','/settings/organization']){
  await page.goto(route)
  await expect(page.locator('.workspace-page-header')).toHaveCount(1)
  await expect(page.locator('.workspace-page-context strong')).not.toHaveText('')
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
 }
})
