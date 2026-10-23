import { test, expect } from '@playwright/test'

test('register, session reload, organization and logout on real secure API', async ({ page }, testInfo) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '登录映创' })).toBeVisible()
  await page.getByRole('button', { name: '创建新账户' }).click()
  const email = `synthetic-${testInfo.project.name}-${Date.now()}@example.invalid`
  await page.locator('#auth-email').fill(email)
  await page.locator('#auth-password').fill('Synthetic-passphrase-2026!')
  await page.getByRole('button', { name: '注册并登录' }).click()
  await expect(page.getByRole('button', { name: '退出登录' }).filter({visible: true})).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: '退出登录' }).filter({visible: true})).toBeVisible()
  await page.locator('summary').filter({hasText: '组织与成员'}).click()
  await page.getByLabel('新组织名称').fill('合成验收组织')
  await page.getByRole('button', { name: '创建组织', exact: true }).click()
  await expect(page.getByRole('option', { name: '合成验收组织', exact: true })).toHaveCount(1)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  await page.getByRole('button', { name: '退出登录' }).filter({visible: true}).click()
  await expect(page.getByRole('heading', { name: '登录映创' })).toBeVisible()
})

test('authenticated browser loads private synthetic media and rejects other tenant access', async ({page, browser}, info) => {
  const origin = 'http://localhost:5175'
  const api = 'http://localhost:8082/api/v1'
  await page.goto('/')
  await page.getByRole('button', {name: '创建新账户'}).click()
  await page.locator('#auth-email').fill(`private-${info.project.name}-${Date.now()}@example.invalid`)
  await page.locator('#auth-password').fill('Synthetic-media-passphrase!')
  await page.getByRole('button', {name: '注册并登录'}).click()
  await expect(page.getByRole('button', {name: '退出登录'}).filter({visible: true})).toBeVisible()
  const session = await (await page.request.get(api + '/auth/session')).json()
  const query = '?space_id=' + session.spaces[0].id
  const headers = {'Origin': origin, 'X-CSRF-Token': session.csrf_token, 'Idempotency-Key': `private-project-${Date.now()}`}
  const created = await page.request.post(api + '/projects' + query, {headers, data: {title: '私有合成媒体验收', brief: '仅为合成媒体测试，无真实人物和外部调用。'}})
  expect(created.status()).toBe(201)
  const project = await created.json()
  const seeded = await page.request.post(api + '/projects/' + project.id + '/media/demo' + query, {headers: {...headers, 'Idempotency-Key': `private-media-${Date.now()}`}, data: {expected_version: 1}})
  expect(seeded.status()).toBe(200)
  const media = await seeded.json()
  await page.getByRole('button', {name: '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: project.title}).click()
  const image = page.locator('.media-assets img').first()
  await image.scrollIntoViewIfNeeded()
  await expect.poll(() => image.evaluate((node: HTMLImageElement) => node.naturalWidth)).toBeGreaterThan(0)
  const outsider = await browser.newContext()
  const denied = await outsider.request.get('http://localhost:8082' + media.assets[0].file_url)
  expect(denied.status()).toBe(401)
  await outsider.close()
})
