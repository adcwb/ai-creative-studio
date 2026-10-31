import { expect, test } from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`);})

test('configure two gateways, multiple models, and independent feature routes', async ({page, request}) => {
  await page.goto('/settings/models')
  await expect(page.getByRole('heading', {name: '模型管理', exact: true})).toBeVisible()
  await page.getByRole('tab', {name: 'AI 网关', exact: true}).click()
  for (const name of ['文本主网关', '分镜网关']) {
    await page.getByRole('button', {name: '添加 AI 网关', exact: true}).click()
    const form = page.locator('.model-editor-form')
    await form.getByLabel('网关名称', {exact: true}).fill(name)
    await form.getByLabel('服务地址', {exact: true}).fill(`https://${name === '文本主网关' ? 'first' : 'second'}.example.com/v1`)
    await form.getByRole('combobox', {name: '凭据操作', exact: true}).selectOption('set')
    await form.getByLabel('新的 API Key', {exact: true}).fill('synthetic-browser-test-key')
    await form.getByRole('button', {name: '保存网关', exact: true}).click()
    await expect(form).toHaveCount(0)
  }
  await page.getByRole('tab', {name: '网关模型', exact: true}).click()
  for (const [gateway, name, model] of [['gateway-1', '通用文本', 'vendor/shared'], ['gateway-1', '快速文本', 'vendor/fast'], ['gateway-2', '分镜推理', 'vendor/shared']]) {
    await page.getByRole('button', {name: '添加模型', exact: true}).click()
    const form = page.locator('.model-editor-form')
    await form.getByRole('combobox', {name: '所属网关', exact: true}).selectOption(gateway)
    await form.getByLabel('模型显示名称', {exact: true}).fill(name)
    await form.getByLabel('上游模型 ID', {exact: true}).fill(model)
    await form.getByLabel('单次费用上限', {exact: true}).fill('0.50')
    await form.getByLabel('费用上限依据', {exact: true}).fill('Synthetic test ceiling')
    await form.getByRole('button', {name: '保存模型', exact: true}).click()
    await expect(form).toHaveCount(0)
  }
  await page.getByRole('tab', {name: '功能绑定', exact: true}).click()
  const script = page.locator('.feature-binding-table tbody tr').filter({hasText: '剧本生成'})
  await script.getByRole('combobox', {name: '剧本生成 AI 网关', exact: true}).selectOption('gateway-1')
  await expect(script.getByRole('combobox', {name: '剧本生成 模型', exact: true}).locator('option')).toHaveCount(3)
  await script.getByRole('combobox', {name: '剧本生成 模型', exact: true}).selectOption('model-1')
  await script.getByRole('button', {name: '保存绑定'}).click()
  await expect(script.getByRole('button', {name: '保存绑定'})).toBeDisabled()
  const storyboard = page.locator('.feature-binding-table tbody tr').filter({hasText: '分镜规划'})
  await storyboard.getByRole('combobox', {name: '分镜规划 AI 网关', exact: true}).selectOption('gateway-1')
  await storyboard.getByRole('combobox', {name: '分镜规划 模型', exact: true}).selectOption('model-1')
  await storyboard.getByRole('combobox', {name: '分镜规划 AI 网关', exact: true}).selectOption('gateway-2')
  await expect(storyboard.getByRole('combobox', {name: '分镜规划 模型', exact: true})).toHaveValue('')
  await expect(storyboard.getByRole('combobox', {name: '分镜规划 模型', exact: true}).locator('option')).toHaveCount(2)
  await storyboard.getByRole('combobox', {name: '分镜规划 模型', exact: true}).selectOption('model-3')
  await storyboard.getByRole('button', {name: '保存绑定'}).click()
  await expect(storyboard.getByRole('button', {name: '保存绑定'})).toBeDisabled()
  await page.reload()
  await expect(page.getByRole('combobox', {name: '剧本生成 AI 网关', exact: true})).toHaveValue('gateway-1')
  await expect(page.getByRole('combobox', {name: '分镜规划 AI 网关', exact: true})).toHaveValue('gateway-2')
  const state = await (await request.get(`${fixture}/__test__/state`)).json()
  expect(state.management.models).toHaveLength(3)
  expect(state.management.bindings.map((item: {model_config_id: string}) => item.model_config_id)).toEqual(['model-1', 'model-3'])
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/model-routing-${test.info().project.name}.png`, fullPage: true})
  await page.goto('/projects/story-1/storyboard')
  await expect(page.locator('.runtime-model-picker select').nth(0)).toHaveValue('gateway-2')
  await expect(page.locator('.runtime-model-picker select').nth(1)).toHaveValue('model-3')
})

test('usage page shows empty guidance and the cost ledger remains available', async ({page}) => {
  await page.goto('/settings/billing')
  await expect(page.getByRole('heading', {name: '模型使用统计', exact: true})).toBeVisible()
  await expect(page.getByText('当前条件下尚无真实模型调用。')).toBeVisible()
  await page.getByText('调用限制与费用账本', {exact: true}).click()
  await expect(page.getByRole('combobox', {name: '账本币种', exact: true})).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('usage tables distinguish unknown token counts from zero and show original-currency ceilings', async ({page, request}) => {
  await request.post(`${fixture}/__test__/reset`, {data: {usage: {
    items: [{id: 'call-1', gateway_id: 'gateway-1', gateway_name: '合成测试网关', model_config_id: 'model-1', model_name: '测试模型', model: 'vendor/test', feature_key: 'storyboard_planning', capability: 'text', state: 'unknown', http_calls: 3, input_tokens: null, output_tokens: null, duration_ms: 1200, started_at: '2026-10-30T01:00:00Z', currency: 'CNY', maximum_minor: 50}],
    summary: [{gateway_id: 'gateway-1', gateway_name: '合成测试网关', model_config_id: 'model-1', model_name: '测试模型', model: 'vendor/test', capability: 'text', currency: 'CNY', calls: 1, succeeded: 0, failed: 0, unknown: 1, average_ms: 1200, p95_ms: 1200, input_tokens: 0, output_tokens: 0, metered_calls: 0, maximum_minor: 50}], total: 1, next_offset: null,
  }}})
  await page.goto('/settings/billing')
  await expect(page.getByRole('cell', {name: '未知', exact: true})).toBeVisible()
  await page.getByRole('button', {name: '调用明细', exact: true}).click()
  await expect(page.getByRole('cell', {name: '未知 / 未知', exact: true})).toBeVisible()
  await expect(page.getByRole('cell', {name: '分镜规划', exact: true})).toBeVisible()
  await expect(page.getByRole('cell', {name: '3', exact: true})).toBeVisible()
  await expect(page.getByText('调用上限用于预留，不能视为实际费用；缺失用量显示未知，各币种分别统计。')).toBeVisible()
})
