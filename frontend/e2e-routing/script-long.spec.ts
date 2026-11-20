import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// The acceptance target is 100 scenes and 2000 dialogue lines. The real project
// response is patched rather than replaced, so every other field stays genuine.
async function loadLargeScript(page: import('@playwright/test').Page) {
  await page.route('**/api/v1/projects/story-1?*', async route => {
    const response = await route.fetch()
    const project = await response.json()
    const base = project.script?.scenes?.[0]
    if (!base) {await route.fulfill({response}); return}
    project.script.scenes = Array.from({length: 100}, (_, index) => ({
      ...base,
      id: `scene-${index}`,
      heading: `第 ${index + 1} 场 · 合成场次`,
      action: `合成动作描述 ${index}，用于长内容渲染与滚动测量。`,
      dialogue: Array.from({length: 20}, (_, line) => ({
        ...(base.dialogue?.[0] ?? {character: '角色'}),
        text: `第 ${index + 1} 场第 ${line + 1} 条合成对白。`,
      })),
    }))
    await route.fulfill({json: project})
  })
  const started = Date.now()
  await page.goto('/projects/story-1/script')
  await expect(page.locator('.reading-outline')).toBeVisible()
  return Date.now() - started
}

test('a hundred-scene script virtualises the outline and keeps navigation working', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await page.setViewportSize({width: 1440, height: 900})
  const elapsed = await loadLargeScript(page)
  const rows = await page.locator('.reading-outline button').count()
  expect(rows, 'outline must not mount every scene').toBeLessThan(100)
  expect(rows).toBeGreaterThan(0)
  await expect(page.locator('.scene-navigator')).toContainText('共 100')
  const nodes = await page.evaluate(() => document.querySelectorAll('*').length)
  console.log(`[long-script] outline rows=${rows} dom nodes=${nodes} interactive_ms=${elapsed}`)
  expect(elapsed).toBeLessThan(30000)

  await page.locator('.reading-outline button').nth(2).click()
  await expect(page).toHaveURL(/scene=3/)
  await expect(page.locator('.scene-navigator')).toContainText('第 3 场')

  await page.keyboard.press('Control+f')
  const find = page.getByRole('search', {name: '查找剧本正文'})
  await expect(find).toBeVisible()
  await find.getByLabel('查找剧本正文').fill('第 42 场第 1 条')
  await expect(find.locator('.find-in-page-count')).toHaveText('1/1')
  await find.getByLabel('查找剧本正文').press('Enter')
  await expect(page).toHaveURL(/scene=42/)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/script-long-${info.project.name}.png`})
})

test('a shared scene link restores the reading position', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await loadLargeScript(page)
  await page.goto('/projects/story-1/script?scene=17')
  await expect(page.locator('.scene-navigator')).toContainText('第 17 场')
  await expect(page.locator('.reading-outline button[aria-current]')).toContainText('17')
})