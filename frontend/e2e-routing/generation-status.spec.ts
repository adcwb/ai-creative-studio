import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

test('a classified failure explains itself in Chinese and keeps the diagnostic ids', async ({page}) => {
  await page.route('**/api/v1/projects?**', route => route.fulfill({status: 503, json: {error: {
    code: 'gateway_catalog_unavailable', message: 'gateway catalog unavailable', category: 'configuration',
    suggestion: '请在设置中绑定可用的网关模型。', request_id: 'req-synthetic-1', correlation_id: 'corr-synthetic-1',
  }}}))
  await page.goto('/projects')
  const alert = page.locator('.projects-page').getByRole('alert')
  await expect(alert).toBeVisible()
  await expect(alert).toContainText('配置未就绪')
  await expect(alert).toContainText('前往设置补齐网关与模型绑定。')
  await expect(alert).toContainText('请在设置中绑定可用的网关模型。')
  await alert.getByText('诊断编号').click()
  await expect(alert).toContainText('req-synthetic-1')
  await expect(alert).toContainText('corr-synthetic-1')
})