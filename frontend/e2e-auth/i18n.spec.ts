import { test, expect } from '@playwright/test'

test('browser locale fallback and language choice persist through reload', async ({ browser }) => {
  const context = await browser.newContext({locale: 'fr-FR'})
  const page = await context.newPage()
  await page.goto('http://localhost:5175')
  await expect(page.getByRole('combobox', {name: 'Interface language'})).toHaveValue('en')
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await page.getByRole('combobox', {name: 'Interface language'}).selectOption('zh-CN')
  await expect(page.getByRole('heading', {name: '登录映创'})).toBeVisible()
  await page.reload()
  await expect(page.getByRole('combobox', {name: '界面语言'})).toHaveValue('zh-CN')
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
  await context.close()
})
