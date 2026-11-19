import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// S2 contract: the page context and the forward action stay reachable at any
// scroll position, on both sides of the shared 760px shell breakpoint.
test('script page keeps the page context and the next step reachable while scrolling', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await page.goto('/projects/story-1/script')
  const header = page.locator('.workspace-page-header')
  const dock = page.locator('.studio-next-step')
  await expect(dock).toBeVisible()
  await expect(dock.getByRole('link', {name: '下一步：生成分镜'})).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollHeight > window.innerHeight + 200)).toBe(true)
  for (const offset of [300, 900]) {
    await page.evaluate(top => window.scrollTo({top, behavior: 'instant'}), offset)
    await expect(header, `header at ${offset}`).toBeInViewport()
    await expect(dock, `dock at ${offset}`).toBeInViewport()
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/workbench-sticky-${info.project.name}.png`})
})

test('narrow viewport keeps the next step docked without horizontal overflow', async ({page}, info) => {
  test.skip(info.project.name !== 'mobile')
  await page.goto('/projects/story-1/script')
  const dock = page.locator('.studio-next-step')
  await expect(dock).toBeVisible()
  await page.evaluate(() => window.scrollTo({top: 600, behavior: 'instant'}))
  await expect(dock).toBeInViewport()
  await expect(page.locator('.workspace-page-header')).toBeInViewport()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/workbench-sticky-${info.project.name}.png`})
})