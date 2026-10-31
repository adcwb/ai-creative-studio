import { chooseLanguage } from '../e2e/support/navigation'
import { test, expect } from '@playwright/test'

test('browser locale fallback and language choice persist through reload', async ({ browser }) => {
  const context = await browser.newContext({locale: 'fr-FR'})
  const page = await context.newPage()
  await page.goto('http://localhost:5175')
  await expect(page.getByRole('button', {name: 'English', exact: true})).toHaveAttribute('aria-pressed', 'true')
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await chooseLanguage(page, 'zh-CN')
  await expect(page.getByRole('heading', {name: '登录映创'})).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', {name: '简体中文', exact: true})).toHaveAttribute('aria-pressed', 'true')
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN')
  await context.close()
})
