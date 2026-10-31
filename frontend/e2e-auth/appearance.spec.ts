import {test, expect} from '@playwright/test'
import {navigateTo, openNavigation} from '../e2e/support/navigation'
import {mkdirSync} from 'node:fs'
import {join} from 'node:path'
const evidence = process.env.STUDIO_DESIGN_EVIDENCE
async function screenshot(page: import('@playwright/test').Page, name: string) {
  if (evidence) {await page.evaluate(() => {window.scrollTo(0,0); const sidebar=document.querySelector('.sidebar-inner'); if(sidebar) sidebar.scrollTop=0}); mkdirSync(evidence, {recursive: true}); await page.screenshot({path: join(evidence, `${name}.png`), fullPage: true})}
}
for (const locale of ['zh-CN', 'en']) test(`login gallery loads native artwork, pauses, switches and respects motion (${locale})`, async ({page}, info) => {
  await page.clock.install()
  await page.goto('/')
  await page.locator('.auth-toolbar').getByRole('button', {name: locale === 'en' ? 'English' : '简体中文', exact: true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang', locale)
  await expect(page.locator('.auth-scene-select')).toHaveCount(0)
  await expect(page.locator('.auth-scene-kicker')).not.toContainText(/01|02|03/)
  await expect.poll(() => page.locator('.auth-scene-images img').evaluateAll(items => items.map(item => [(item as HTMLImageElement).naturalWidth,(item as HTMLImageElement).naturalHeight]))).toEqual([[1536,1024],[1536,1024],[1536,1024]])
  const gallery = page.locator('.auth-gallery')
  await expect(gallery).toHaveAttribute('data-scene','vision')
  await screenshot(page, `login-${info.project.name}-${locale}-vision`)
  await page.clock.fastForward(8100)
  await expect(gallery).toHaveAttribute('data-scene','sequence')
  await page.locator('.auth-playback').focus()
  await page.keyboard.press('Enter')
  await page.clock.fastForward(16000)
  await expect(gallery).toHaveAttribute('data-scene','sequence')
  await page.keyboard.press('Enter')
  await page.clock.fastForward(8100)
  await expect(gallery).toHaveAttribute('data-scene','presence')
  await expect(page.locator('.planned-badge')).toHaveText(locale === 'en' ? 'Planned' : '规划中')
  await screenshot(page, `login-${info.project.name}-${locale}-planned`)
  await page.locator('.auth-playback').click()
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('lang', locale)
  await expect.poll(() => page.locator('.auth-scene-images img').evaluateAll(items => items.every(item => (item as HTMLImageElement).complete))).toBe(true)
  await page.clock.fastForward(8100)
  await expect(gallery).toHaveAttribute('data-scene','sequence')
  await page.emulateMedia({reducedMotion: 'reduce'})
  await expect(page.locator('.auth-playback')).toBeDisabled()
  await page.clock.fastForward(16000)
  await expect(gallery).toHaveAttribute('data-scene','sequence')
  expect(await page.locator('.auth-scene-images img').first().evaluate(el => getComputedStyle(el).transitionDuration)).toBe('0s')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('personal themes and locale persist per account, reset on logout and never cross users', async ({page}, info) => {
  const emailA=`appearance-a-${info.project.name}-${Date.now()}@example.invalid`
  const emailB=`appearance-b-${info.project.name}-${Date.now()}@example.invalid`
  const password='Synthetic-appearance-passphrase!'
  async function register(email: string) {
    await page.getByRole('button',{name:'创建新账户',exact:true}).click()
    await page.locator('#auth-email').fill(email); await page.locator('#auth-password').fill(password)
    await page.getByRole('button',{name:'注册并登录',exact:true}).click()
    await expect(page.locator('.app-shell')).toBeVisible()
  }
  async function logout() {await openNavigation(page); await page.locator('.account-signout').click(); await expect(page.locator('.auth-screen')).toBeVisible()}
  await page.goto('/'); await register(emailA)
  await navigateTo(page,'/settings/preferences')
  const cards=page.locator('.theme-option')
  const themeNames=['forest','paper','screening']
  for (const locale of ['zh-CN','en']) {
    await page.locator('.preferences-page .language-buttons').getByRole('button',{name:locale==='en'?'English':'简体中文',exact:true}).click()
    for(let i=0;i<3;i++) {
      await cards.nth(i).click(); await cards.nth(i).click()
      await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme',themeNames[i])
      await expect(cards.nth(i)).toHaveAttribute('aria-pressed','true')
      expect(await page.evaluate(() => document.documentElement.scrollWidth<=innerWidth)).toBe(true)
      await screenshot(page,`settings-${info.project.name}-${locale}-${themeNames[i]}`)
    }
  }
  await page.reload()
  await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme','screening')
  await expect(page.locator('html')).toHaveAttribute('lang','en')
  await navigateTo(page,'/studio')
  await expect(page.locator('textarea').first()).toBeVisible()
  await screenshot(page,`studio-${info.project.name}-screening`)
  const contrast = await page.locator('textarea').first().evaluate(el => {
    const c=getComputedStyle(el)
    function luminance(rgb: string) { const a=(rgb.match(/[\d.]+/g) ?? []).slice(0,3).map(Number).map(v=> {v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4});return a[0]*.2126+a[1]*.7152+a[2]*.0722 }
    const a=luminance(c.color), b=luminance(c.backgroundColor);return (Math.max(a,b)+.05)/(Math.min(a,b)+.05)
  })
  expect(contrast).toBeGreaterThanOrEqual(4.5)
  await openNavigation(page)
  await page.locator('[data-group=resources] .group-toggle').click()
  await expect(page.locator('[data-group=resources] .group-toggle')).toHaveAttribute('aria-expanded','false')
  await logout()
  await expect(page.locator('html')).toHaveAttribute('lang','zh-CN')
  await expect(page.locator('.user-workspace')).toHaveCount(0)
  await register(emailB)
  await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme','forest')
  await expect(page.locator('html')).toHaveAttribute('lang','zh-CN')
  await openNavigation(page)
  await expect(page.locator('[data-group=resources] .group-toggle')).toHaveAttribute('aria-expanded','true')
  await navigateTo(page,'/settings/preferences'); await cards.nth(1).click()
  await logout()
  await page.locator('#auth-email').fill(emailA); await page.locator('#auth-password').fill(password)
  await page.getByRole('button',{name:'登录',exact:true}).click()
  await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme','screening')
  await expect(page.locator('html')).toHaveAttribute('lang','en')
  await openNavigation(page)
  await expect(page.locator('[data-group=resources] .group-toggle')).toHaveAttribute('aria-expanded','false')
  const icons=await page.locator('[data-group=avatars] .nav-subgroup').locator('svg path').evaluateAll(paths=>paths.map(path=>path.getAttribute('d')))
  expect(new Set(icons).size).toBe(3)
  await expect(page.locator('.group-toggle svg')).toHaveCount(4)
})

test('personal settings remain available without an organization and report blocked persistence', async ({page}, info) => {
  await page.goto('/')
  await page.getByRole('button',{name:'创建新账户',exact:true}).click()
  await page.locator('#auth-email').fill(`appearance-storage-${info.project.name}-${Date.now()}@example.invalid`)
  await page.locator('#auth-password').fill('Synthetic-appearance-passphrase!')
  await page.getByRole('button',{name:'注册并登录',exact:true}).click()
  await expect(page.locator('.app-shell')).toBeVisible()
  await page.route('**/api/v1/spaces', route => route.fulfill({json:[]}))
  await page.goto('/settings/preferences')
  await expect(page.locator('.preferences-page')).toBeVisible()
  await page.evaluate(() => { const original=Storage.prototype.setItem; Storage.prototype.setItem=function(key,value) {if(this===localStorage) throw new Error('synthetic blocked storage'); original.call(this,key,value)} })
  await page.locator('.theme-option').nth(2).click()
  await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme','screening')
  await expect(page.getByRole('status')).toContainText('浏览器无法保存偏好')
  await page.locator('.preferences-page').getByRole('button',{name:'English',exact:true}).click()
  await expect(page.getByRole('status')).toContainText('Your browser cannot save preferences')
  await page.reload()
  await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme','forest')
  await expect(page.locator('html')).toHaveAttribute('lang','zh-CN')
})

test('a first sign-in keeps the explicitly chosen login language', async ({page}, info) => {
  await page.goto('/')
  await page.getByRole('button',{name:'English',exact:true}).click()
  await page.getByRole('button',{name:'Create an account',exact:true}).click()
  await page.locator('#auth-email').fill(`appearance-language-${info.project.name}-${Date.now()}@example.invalid`)
  await page.locator('#auth-password').fill('Synthetic-appearance-passphrase!')
  await page.locator('button.auth-submit').click()
  await expect(page.locator('.app-shell')).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('lang','en')
  await navigateTo(page,'/settings/preferences')
  await expect(page.getByRole('heading',{name:'Personal settings',exact:true})).toBeVisible()
})

test('ordinary visible browser starts the gallery without clicking play',async({page})=>{
 await page.emulateMedia({reducedMotion:'no-preference'})
 await page.goto('/')
 await expect(page.locator('.auth-gallery')).toHaveAttribute('data-scene','vision')
 // The product interval is eight seconds; observe it in a real browser clock.
 await expect(page.locator('.auth-gallery')).toHaveAttribute('data-scene','sequence',{timeout:12000})
 await expect(page.locator('.auth-scene-select')).toHaveCount(0)
})
