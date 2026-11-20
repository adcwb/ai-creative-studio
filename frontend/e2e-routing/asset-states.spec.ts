import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// S8 reuses the shared boundary states instead of each page spelling its own.
test('the asset library uses the shared empty and error surfaces', async ({page}) => {
  await page.route('**/api/v1/assets?**', route => route.fulfill({json: []}))
  await page.goto('/assets')
  const empty = page.locator('.studio-empty')
  await expect(empty).toBeVisible()
  await expect(empty).toHaveAttribute('data-kind', 'empty')
  await expect(page.locator('.empty-art')).toHaveCount(0)

  await page.unroute('**/api/v1/assets?**')
  await page.route('**/api/v1/assets?**', route => route.fulfill({status: 500, json: {error: {code: 'internal_error', message: '合成查询失败', category: 'storage'}}}))
  await page.reload()
  const alert = page.locator('.studio-error')
  await expect(alert).toBeVisible()
  await expect(alert.getByRole('button', {name: '重新查询', exact: true})).toBeVisible()
  await expect(page.locator('.loading-block')).toHaveCount(0)
})
