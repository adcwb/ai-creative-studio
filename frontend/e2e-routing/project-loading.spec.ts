import { test, expect } from '@playwright/test'

test('cancellation from a loading render preserves the subsequently accepted project', async ({page, request}) => {
  await request.post('http://127.0.0.1:5189/__test__/reset', {data: {}})
  await page.goto('/e2e-routing/fixtures/project-cancel.html')
  const saved = JSON.stringify({loaded: true, title: '钟楼来信', script: '钟楼来信', shots: 1, dirty: false})
  await expect(page.locator('output')).toHaveText(saved)
  await page.getByRole('button', {name: 'Cancel from loading render'}).click()
  await expect(page.locator('output')).toHaveText(saved)
})
