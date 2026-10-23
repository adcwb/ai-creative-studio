import { test, expect } from '@playwright/test'

test.use({locale: 'en-US'})
test('English image workflow preserves provenance warnings and language switching', async ({page}, info) => {
  await page.goto('/')
  await expect(page.getByText('Simulation demo mode', {exact: true})).toBeVisible()
  await expect(page.getByText('Simulation mode: program-drawn SVGs with no AI calls or model charges.', {exact: true})).toBeVisible()
  await expect(page.getByRole('combobox', {name: 'Interface language'})).toHaveValue('en')
  const prompt = `Synthetic English fixture ${info.project.name} ${Date.now()}`
  await page.getByLabel('Describe your image', {exact: true}).fill(prompt)
  await page.getByRole('button', {name: 'Create simulated image', exact: true}).click()
  const card = page.locator('.task').filter({hasText: prompt})
  await expect(card.getByText('Simulation complete', {exact: true})).toBeVisible()
  await expect(card.locator('img')).toBeVisible()
  await card.getByRole('button', {name: 'Save to library', exact: true}).click()
  await expect(card.getByRole('button', {name: 'Saved', exact: true})).toBeDisabled()
  await page.getByRole('combobox', {name: 'Interface language'}).selectOption('zh-CN')
  await expect(card.getByText('模拟完成', {exact: true})).toBeVisible()
  await expect(card.getByRole('button', {name: '已保存', exact: true})).toBeDisabled()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.reload()
  await expect(page.getByRole('combobox', {name: '界面语言'})).toHaveValue('zh-CN')
})
