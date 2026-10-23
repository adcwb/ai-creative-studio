import { test, expect } from '@playwright/test'

test('register, session reload, organization and logout on real secure API', async ({ page }, testInfo) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '登录映创' })).toBeVisible()
  await page.getByRole('button', { name: '创建新账户' }).click()
  const email = `synthetic-${testInfo.project.name}-${Date.now()}@example.invalid`
  await page.locator('#auth-email').fill(email)
  await page.locator('#auth-password').fill('Synthetic-passphrase-2026!')
  await page.getByRole('button', { name: '注册并登录' }).click()
  await expect(page.getByRole('button', { name: '退出登录' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: '退出登录' })).toBeVisible()
  await page.locator('summary').filter({hasText: '组织与成员'}).click()
  await page.getByLabel('新组织名称').fill('合成验收组织')
  await page.getByRole('button', { name: '创建组织', exact: true }).click()
  await expect(page.getByText('合成验收组织', { exact: true }).first()).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  await page.getByRole('button', { name: '退出登录' }).click()
  await expect(page.getByRole('heading', { name: '登录映创' })).toBeVisible()
})
