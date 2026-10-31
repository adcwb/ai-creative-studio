import { expect, type Page } from '@playwright/test'

export async function openNavigation(page: Page) {
  const toggle = page.locator('.menu-toggle')
  // Auth and workspace loading mount the shell asynchronously. isVisible()
  // alone returns false immediately when the toggle has not mounted yet.
  await expect(toggle).toBeAttached()
  if (await toggle.isVisible()) {
    if (await toggle.getAttribute('aria-expanded') !== 'true') await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(page.locator('#app-navigation')).toBeVisible()
  }
}
export async function closeNavigation(page: Page) {
  const close = page.locator('.sidebar-close')
  if (await page.locator('.menu-toggle').getAttribute('aria-expanded') === 'true') await close.click()
}
export async function navigateTo(page: Page, path: string) {
  const link = page.locator(`.app-nav a[href="${path}"]`)
  await openNavigation(page)
  await link.click()
}
export async function openProject(page: Page, title: string, section = 'brief') {
  if (new URL(page.url()).pathname !== '/projects') await navigateTo(page, '/projects')
  await page.locator('.project-card').filter({hasText: title}).click()
  await expect(page.locator('.project-page')).toBeVisible()
  if (section !== 'brief') await projectStep(page, section)
}
export async function projectStep(page: Page, section: string) {
  const current = new URL(page.url()).pathname
  if (current.startsWith('/publish/')) {
    if (section === 'publish') return
    const id = current.split('/')[2]
    await page.goto(`/projects/${id}/${section}`)
    return
  }
  if (section === 'publish') {
    await page.locator('.project-steps a[href$="/review"]').click()
    await page.locator('a[href^="/publish/"]').click()
    return
  }
  await page.locator(`.project-steps a[href$="/${section}"]`).click()
}
export async function chooseSpace(page: Page, space: string) {
  const picker = page.locator('#space')
  await openNavigation(page)
  await picker.selectOption(space)
}
export async function chooseLanguage(page: Page, locale: string) {
  const picker = page.locator('.language-picker select')
  const authScreen = await page.locator('.auth-screen').count()
  if (authScreen) await page.locator('.auth-toolbar').getByRole('button', {name: locale === 'en' ? 'English' : '简体中文', exact: true}).click()
  else {await openNavigation(page); await picker.selectOption(locale); await closeNavigation(page)}
}
export async function openCreativeTools(page: Page) {
  await projectStep(page, 'script')
  const tools = page.locator('.project-tools')
  if (await tools.getAttribute('open') === null) await tools.locator('summary').first().click()
}
