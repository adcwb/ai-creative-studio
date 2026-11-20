import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// The three-column creation workspace only engages at 1400px and up; below that
// the proven two-column flow is unchanged. The history grid must stay 2x2 with
// full-height thumbnails at every width -- that geometry is asserted by a
// backend-dependent suite in CI, so it is pinned here as well where it can run.
test('wide screens get three columns and keep the history grid intact', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await page.setViewportSize({width: 1440, height: 900})
  await page.goto('/studio')
  const templates = page.locator('.image-page .templates')
  const results = page.locator('.image-page .results')
  const params = page.locator('.image-page .creation-params')
  await expect(results).toBeVisible()
  const box = async (l: typeof results) => (await l.boundingBox())!
  const [t, r, p] = await Promise.all([box(templates), box(results), box(params)])
  expect(t.x).toBeLessThan(r.x)
  expect(r.x).toBeLessThan(p.x)
  expect(Math.abs(t.y - r.y)).toBeLessThan(4)
  expect(Math.abs(p.y - r.y)).toBeLessThan(4)

  const gallery = page.locator('.task-gallery')
  await expect(gallery).toBeVisible()
  const tracks = await gallery.evaluate(node => getComputedStyle(node).gridTemplateColumns.split(' ').filter(Boolean).length)
  expect(tracks, 'history grid must stay two columns').toBe(2)
  const thumb = page.locator('.task-gallery .result-preview img').first()
  if (await thumb.count()) expect((await box(thumb)).height).toBeGreaterThanOrEqual(190)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/creation-wide-${info.project.name}.png`})
})

test('mid widths keep the two-column flow and the same history geometry', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await page.setViewportSize({width: 1280, height: 900})
  await page.goto('/studio')
  const controls = page.locator('.image-page .creation-controls')
  const results = page.locator('.image-page .results')
  await expect(results).toBeVisible()
  const c = (await controls.boundingBox())!
  const r = (await results.boundingBox())!
  expect(c.x).toBeLessThan(r.x)
  expect(Math.abs(c.y - r.y)).toBeLessThan(4)
  // Templates stay inside the controls column rather than becoming a third one.
  const t = (await page.locator('.image-page .templates').boundingBox())!
  expect(t.x).toBeGreaterThanOrEqual(c.x - 1)
  expect(t.x + t.width).toBeLessThanOrEqual(c.x + c.width + 1)
  const tracks = await page.locator('.task-gallery').evaluate(node => getComputedStyle(node).gridTemplateColumns.split(' ').filter(Boolean).length)
  expect(tracks).toBe(2)
  await page.screenshot({path: `test-results/creation-mid-${info.project.name}.png`})
})

test('narrow screens stack the creation workspace without overflow', async ({page}, info) => {
  test.skip(info.project.name !== 'mobile')
  await page.goto('/studio')
  await expect(page.locator('.image-page .results')).toBeVisible()
  const c = (await page.locator('.image-page .creation-controls').boundingBox())!
  const r = (await page.locator('.image-page .results').boundingBox())!
  expect(r.y).toBeGreaterThan(c.y)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/creation-narrow-${info.project.name}.png`})
})