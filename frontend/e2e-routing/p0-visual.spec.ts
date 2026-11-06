import {test,expect} from '@playwright/test'
import {openNavigation,closeNavigation} from '../e2e/support/navigation'
const fixture='http://127.0.0.1:5189'
test('P0 administration and image creation visual review across screen sizes',async({page,request},info)=>{
 await request.post(fixture+'/__test__/reset',{data:{deploymentAdmin:true}})
 const sizes=info.project.name==='desktop'?[{width:1920,height:1080},{width:1366,height:768}]:[{width:390,height:844}]
 for(const size of sizes){
  await page.setViewportSize(size)
  for(const [route,name]of [['/studio','image'],['/settings/backend','backend'],['/settings/organization','members'],['/settings/spaces','spaces']]){
   await page.goto(route)
   await expect(page.locator('.page-heading')).toBeVisible()
   await closeNavigation(page)
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
   await page.screenshot({path:`test-results/p0-${name}-${size.width}.png`,fullPage:true})
  }
  await page.goto('/settings/preferences')
  await page.getByRole('button',{name:'English',exact:true}).click()
  await openNavigation(page)
  expect(await page.locator('.sidebar-inner').evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
  await page.screenshot({path:`test-results/p0-navigation-en-${size.width}.png`,fullPage:true})
  await closeNavigation(page)
  await page.getByRole('button',{name:'简体中文',exact:true}).click()
  await openNavigation(page)
  const color=await page.getByRole('link',{name:'图像创作',exact:true}).evaluate(el=>getComputedStyle(el).color)
  expect(color).not.toBe('rgb(99, 116, 103)')
  await page.screenshot({path:`test-results/p0-navigation-${size.width}.png`,fullPage:true})
 }
})
