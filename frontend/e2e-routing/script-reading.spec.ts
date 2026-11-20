import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// S6 contract: reading typography, a speaker column, cast out of the prose, and
// text editors mounted for one scene at a time while structural controls stay
// available on every scene.
test('script reading uses the reading scale, a speaker column and a cast strip', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await page.goto('/projects/story-1/script')
  await expect(page.locator('.drama-cast-strip')).toBeVisible()
  const action = page.locator('.drama-action').first()
  await expect(action).toBeVisible()
  expect(await action.evaluate(node => getComputedStyle(node).fontSize)).toBe('16px')
  const line = page.locator('.drama-dialogue:not(.is-editing) > div').first()
  const tracks = await line.evaluate(node => getComputedStyle(node).gridTemplateColumns.split(' ').filter(Boolean).length)
  expect(tracks, 'dialogue needs a speaker column and a line column').toBe(2)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/script-reading-${info.project.name}.png`})
})

test('only the active scene mounts text editors while every scene keeps its structure controls', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  await page.goto('/projects/story-1/script')
  await page.getByRole('button', {name: '编辑剧本', exact: true}).click()
  await expect(page.getByLabel('第 1 场动作', {exact: true})).toBeVisible()
  const scenes = await page.locator('.drama-scene').count()
  expect(scenes).toBeGreaterThan(1)
  // One action editor, but a reorder control per scene.
  await expect(page.getByLabel(/场动作$/)).toHaveCount(1)
  await expect(page.getByRole('button', {name: '上移场次', exact: true})).toHaveCount(scenes)
  await page.getByRole('button', {name: '编辑本场', exact: true}).first().click()
  await expect(page.getByLabel('第 2 场动作', {exact: true})).toBeVisible()
  await expect(page.getByLabel('第 1 场动作', {exact: true})).toHaveCount(0)
  await expect(page.getByLabel(/场动作$/)).toHaveCount(1)
  await page.screenshot({path: `test-results/script-sharded-${info.project.name}.png`})
})