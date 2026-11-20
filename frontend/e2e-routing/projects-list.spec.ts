import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// GET /projects has no cursor or limit, so search and sort are deliberately
// client-side. These cases pin that behaviour and the three boundary states.
test('project list filters, sorts and offers a way out of an empty result', async ({page}, info) => {
  await page.goto('/projects')
  const cards = page.locator('.project-card')
  await expect(cards.first()).toBeVisible()
  const total = await cards.count()
  const title = (await cards.first().locator('h2').textContent()) ?? ''
  expect(title.length).toBeGreaterThan(1)

  const search = page.getByLabel('搜索项目', {exact: true})
  await search.fill(title.slice(0, 2))
  await expect(cards.first()).toBeVisible()
  expect(await cards.count()).toBeLessThanOrEqual(total)

  await search.fill('zzz-合成不存在的关键词')
  await expect(cards).toHaveCount(0)
  const empty = page.locator('.studio-empty[data-kind="no-results"]')
  await expect(empty).toBeVisible()
  await empty.getByRole('button', {name: '清除筛选', exact: true}).click()
  await expect(cards).toHaveCount(total)

  const sort = page.getByLabel('排序方式', {exact: true})
  await sort.click()
  await page.locator('.ant-select-dropdown:visible').getByRole('option', {name: '按标题排序', exact: true}).click()
  await expect(cards.first()).toBeVisible()
  const titles = await cards.locator('h2').allTextContents()
  expect([...titles].sort((a, b) => a.localeCompare(b, 'zh-Hans-CN'))).toEqual(titles)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/projects-list-${info.project.name}.png`})
})

test('project list reports a read failure and recovers on retry', async ({page}) => {
  await page.route('**/api/v1/projects?**', route => route.fulfill({status: 500, json: {error: {code: 'internal_error', message: '合成读取失败'}}}))
  await page.goto('/projects')
  const alert = page.locator('.projects-page').getByRole('alert')
  await expect(alert).toBeVisible()
  await expect(page.locator('.project-card')).toHaveCount(0)
  await page.unroute('**/api/v1/projects?**')
  await alert.getByRole('button', {name: '重新加载项目列表', exact: true}).click()
  await expect(page.locator('.project-card').first()).toBeVisible()
  await expect(page.locator('.projects-page').getByRole('alert')).toHaveCount(0)
})

test('the project filter bar stays reachable while scrolling', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await page.setViewportSize({width: 1280, height: 620})
  await page.goto('/projects')
  await expect(page.locator('.project-card').first()).toBeVisible()
  const bar = page.locator('.projects-page .studio-sticky-bar')
  await expect(bar).toBeInViewport()
  await page.evaluate(() => window.scrollTo({top: 400, behavior: 'instant'}))
  await expect(bar).toBeInViewport()
  await expect(page.getByLabel('搜索项目', {exact: true})).toBeInViewport()
})