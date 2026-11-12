import {expect, test} from '@playwright/test'
import {navigateTo} from '../e2e/support/navigation'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

test('image reads stop outside image routes and while hidden, preserving the draft', async ({page}) => {
  let reads = 0
  page.on('request', request => {if (request.method() === 'GET' && /\/api\/v1\/(tasks|assets)\?/.test(request.url())) reads++})
  await page.goto('/settings/models')
  await expect(page.getByRole('heading', {name: '功能使用哪个模型', exact: true})).toBeVisible()
  await page.waitForTimeout(1800)
  expect(reads).toBe(0)
  await navigateTo(page, '/studio')
  await page.locator('#prompt').fill('保留跨页面图像草稿')
  await expect.poll(() => reads).toBeGreaterThanOrEqual(2)
  await page.evaluate(() => {Object.defineProperty(document, 'hidden', {configurable: true, value: true}); document.dispatchEvent(new Event('visibilitychange'))})
  const hiddenReads = reads
  await page.waitForTimeout(3200)
  expect(reads).toBe(hiddenReads)
  await page.evaluate(() => {Object.defineProperty(document, 'hidden', {configurable: true, value: false}); document.dispatchEvent(new Event('visibilitychange'))})
  await expect.poll(() => reads).toBeGreaterThan(hiddenReads)
  await navigateTo(page, '/projects')
  const awayReads = reads
  await page.waitForTimeout(3200)
  expect(reads).toBe(awayReads)
  await navigateTo(page, '/studio')
  await expect(page.locator('#prompt')).toHaveValue('保留跨页面图像草稿')
  await expect.poll(() => reads).toBeGreaterThan(awayReads)
})

test('slow image reads do not overlap and are cancelled on navigation', async ({page}) => {
  let reads = 0
  let release!: () => void
  const held = new Promise<void>(resolve => {release = resolve})
  await page.route('**/api/v1/tasks?*', async route => {
    reads++
    await held
    await route.fulfill({json: []}).catch(() => {})
  })
  await page.goto('/studio')
  await expect(page.locator('#prompt')).toBeVisible()
  // Strict Mode may cancel its first effect; measure after mounting settles.
  await page.waitForTimeout(200)
  const initialReads = reads
  await page.waitForTimeout(3300)
  expect(reads).toBe(initialReads)
  await navigateTo(page, '/projects')
  release()
  await expect(page).toHaveURL(/\/projects$/)
})

test('Chinese startup avoids English dictionaries and switching keeps the image draft', async ({page}) => {
  const dictionaries: string[] = []
  page.on('request', request => {if (/englishDictionary|\/locales\//.test(request.url())) dictionaries.push(request.url())})
  await page.goto('/studio')
  await page.locator('#prompt').fill('切换语言后保留的草稿')
  expect(dictionaries).toHaveLength(0)
  await navigateTo(page, '/settings/preferences')
  await page.getByRole('button', {name: 'English', exact: true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  expect(dictionaries.length).toBeGreaterThan(0)
  await navigateTo(page, '/studio')
  await expect(page.locator('#prompt')).toHaveValue('切换语言后保留的草稿')
})

test('a late English load cannot override the last language choice', async ({page}) => {
  let release!: () => void
  const held = new Promise<void>(resolve => {release = resolve})
  await page.route(/englishDictionary/, async route => {await held; await route.continue()})
  await page.goto('/settings/preferences')
  await page.getByRole('button', {name: 'English', exact: true}).click()
  await expect(page.getByText('Loading English…', {exact: true})).toBeVisible()
  await page.locator('.preferences-page').getByRole('button', {name: '简体中文', exact: true}).click()
  release()
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
  await page.waitForTimeout(300)
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
  await expect(page.getByText('Loading English…', {exact: true})).toHaveCount(0)
})

test('an initial English load failure offers recovery without mounting untranslated forms', async ({page}) => {
  await page.addInitScript(() => localStorage.setItem('studio.locale', 'en'))
  await page.route(/englishDictionary/, route => route.abort(), {times: 1})
  await page.goto('/studio')
  await expect(page.getByText('English could not be loaded. Please retry.')).toBeVisible()
  await expect(page.locator('#prompt')).toHaveCount(0)
  await page.getByRole('button', {name: 'Retry English', exact: true}).click()
  await expect(page.locator('#prompt')).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
})

test('multiple clip pickers share a catalog and one refresh updates all consumers', async ({page}) => {
  let catalogs = 0
  await page.route('**/api/v1/runtime-models?*', route => {catalogs++; return route.fulfill({json: {models: [], bindings: [], governance_ready: true}})})
  await page.route('**/api/v1/projects/story-1/media?*', route => route.fulfill({json: {
    version: 1, assets: [], exports: [], timeline: {music_asset_id: '', music_volume: .2,
      clips: Array.from({length: 3}, (_, index) => ({id: `clip-${index}`, asset_id: '', audio_asset_id: '', duration: 3, trim_start: 0, subtitle: ''}))},
  }}))
  await page.goto('/projects/story-1/media')
  await expect(page.locator('.media-timeline .runtime-model-picker')).toHaveCount(3)
  await expect(page.getByRole('button', {name: '刷新模型目录', exact: true})).toHaveCount(3)
  expect(catalogs).toBe(1)
  await page.getByRole('button', {name: '刷新模型目录', exact: true}).first().click()
  await expect.poll(() => catalogs).toBe(2)
  await expect(page.getByRole('button', {name: '刷新模型目录', exact: true})).toHaveCount(3)
})
