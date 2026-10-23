import { randomUUID } from 'node:crypto'
import { test, expect, type APIRequestContext, type Page } from '@playwright/test'

const apiBase = 'http://127.0.0.1:8081/api/v1'
const personalQuery = '?space_id=personal-demo'
type Input = { prompt: string; kind: 'poster' | 'portrait' | 'general'; ratio: string; style: string }
type Task = Input & { id: string; status: string; retry_of?: string; result_url?: string }
const pageErrors = new WeakMap<Page, string[]>()

test.beforeEach(({ page }) => {
  const errors: string[] = []
  pageErrors.set(page, errors)
  page.on('pageerror', error => errors.push(error.message))
})

test.afterEach(({ page }) => {
  expect(pageErrors.get(page)).toEqual([])
})

async function createFixture(request: APIRequestContext, input: Input, save = false): Promise<Task> {
  const response = await request.post(`${apiBase}/tasks${personalQuery}`, {
    headers: { 'Idempotency-Key': randomUUID() }, data: input,
  })
  expect(response.status()).toBe(202)
  const task = await response.json() as Task
  await expect.poll(async () => {
    const detail = await request.get(`${apiBase}/tasks/${task.id}${personalQuery}`)
    return (await detail.json() as Task).status
  }).toBe(input.prompt.includes('[mock-fail]') ? 'failed' : 'succeeded')
  if (save) {
    const saved = await request.post(`${apiBase}/assets${personalQuery}`, { data: { task_id: task.id } })
    expect(saved.status()).toBe(200)
  }
  return task
}

async function expectNoOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
}

async function capture(page: Page, path: string, fullPage = true) {
  await page.evaluate(() => {
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
    window.scrollTo(0, 0)
  })
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
  await page.screenshot({ path, fullPage })
}

test('backend catalog drives parameters; duplicate submission preserves one real task', async ({ page, request }, testInfo) => {
  // StrictMode may cancel the first browser fetch after its headers arrive.
  // Read the expected catalog independently, then assert the UI applies it.
  const catalogResponse = await request.get(`${apiBase}/templates`)
  expect(catalogResponse.ok()).toBeTruthy()
  await page.goto('/')
  const catalog = await catalogResponse.json() as {
    templates: Array<Input & { id: string; name: string }>
  }
  expect(catalog.templates).toHaveLength(3)
  for (const template of catalog.templates) {
    await page.getByRole('button', { name: template.name, exact: false }).click()
    await expect(page.getByLabel('描述你的画面')).toHaveValue(template.prompt)
    await expect(page.getByLabel('画面比例')).toHaveValue(template.ratio)
    await expect(page.getByLabel('视觉风格')).toHaveValue(template.style)
  }
  const prompt = `Synthetic catalog ${testInfo.project.name} ${randomUUID()}`
  await page.getByLabel('描述你的画面').fill(prompt)
  await page.getByLabel('画面比例').selectOption('16:9')
  await page.getByLabel('视觉风格').selectOption('illustration')
  const createdResponse = page.waitForResponse(response => response.request().method() === 'POST' && response.url().includes('/api/v1/tasks?'))
  await page.getByRole('button', { name: '创建模拟图像', exact: false }).dblclick()
  const response = await createdResponse
  expect(response.status()).toBe(202)
  const created = await response.json() as Task
  expect(created.kind).toBe(catalog.templates.at(-1)?.kind)
  expect(created.ratio).toBe('16:9')
  expect(created.style).toBe('illustration')
  const card = page.locator('.task').filter({ hasText: prompt })
  await expect(card.getByText('模拟完成', { exact: true })).toBeVisible()
  const replayResponse = page.waitForResponse(response => response.request().method() === 'POST' && response.url().includes('/api/v1/tasks?'))
  await page.getByRole('button', { name: '创建模拟图像', exact: false }).click()
  const replay = await replayResponse
  expect(replay.status()).toBe(200)
  expect((await replay.json() as Task).id).toBe(created.id)
  await expect(card).toHaveCount(1)
  const list = await request.get(`${apiBase}/tasks${personalQuery}`)
  expect((await list.json() as Task[]).filter(task => task.prompt === prompt)).toHaveLength(1)
  const image = card.locator('img')
  await expect(image).toBeVisible()
  expect(await image.evaluate((node: HTMLImageElement) => node.naturalWidth / node.naturalHeight)).toBeCloseTo(16 / 9, 2)
  await expectNoOverflow(page)
  await capture(page, testInfo.outputPath('iteration-create.png'))
})

test('task details use the backend; keyboard close and cancelled editing create nothing', async ({ page, request }, testInfo) => {
  const prompt = `[mock-fail] Synthetic dialog ${testInfo.project.name} ${randomUUID()}`
  const task = await createFixture(request, { prompt, kind: 'portrait', ratio: '4:5', style: 'studio' })
  let posts = 0
  page.on('request', req => { if (req.method() === 'POST') posts++ })
  await page.goto('/')
  const draft = 'Synthetic untouched draft before editing'
  await page.getByLabel('描述你的画面').fill(draft)
  await page.getByLabel('创作类型').selectOption('general')
  await page.getByLabel('画面比例').selectOption('1:1')
  await page.getByLabel('视觉风格').selectOption('illustration')
  const card = page.locator('.task').filter({ hasText: prompt })
  const trigger = card.getByRole('button', { name: '查看详情' })
  const detailResponse = page.waitForResponse(response => response.url().includes(`/tasks/${task.id}?`) && response.request().method() === 'GET')
  await trigger.click()
  expect((await detailResponse).status()).toBe(200)
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText(prompt)
  await expect(dialog).toContainText(task.id)
  const controls = dialog.locator('button, [href], input, select, textarea, [tabindex="0"]')
  await controls.last().focus()
  await page.keyboard.press('Tab')
  expect(await dialog.evaluate(node => node.contains(document.activeElement))).toBe(true)
  await controls.first().focus()
  await page.keyboard.press('Shift+Tab')
  expect(await dialog.evaluate(node => node.contains(document.activeElement))).toBe(true)
  await expectNoOverflow(page)
  await capture(page, testInfo.outputPath('iteration-detail.png'), false)
  await page.keyboard.press('Escape')
  await expect(dialog).not.toBeVisible()
  await expect(trigger).toBeFocused()
  await trigger.click()
  await dialog.getByRole('button', { name: '关闭详情' }).click()
  await expect(dialog).not.toBeVisible()
  await card.getByRole('button', { name: '编辑后重试' }).click()
  await expect(page.getByLabel('描述你的画面')).toHaveValue(prompt)
  await expect(page.getByLabel('创作类型')).toHaveValue('portrait')
  await expect(page.getByLabel('画面比例')).toHaveValue('4:5')
  await expect(page.getByLabel('视觉风格')).toHaveValue('studio')
  await expect(page.getByLabel('描述你的画面')).toBeFocused()
  await page.getByLabel('描述你的画面').fill('Synthetic discarded retry draft')
  await page.getByRole('button', { name: '取消编辑' }).click()
  await expect(page.getByLabel('描述你的画面')).toHaveValue(draft)
  await expect(page.getByLabel('创作类型')).toHaveValue('general')
  await expect(page.getByLabel('画面比例')).toHaveValue('1:1')
  await expect(page.getByLabel('视觉风格')).toHaveValue('illustration')
  expect(posts).toBe(0)
  await card.getByRole('button', { name: '编辑后重试' }).click()
  await page.reload()
  await expect(page.getByRole('button', { name: '取消编辑' })).toHaveCount(0)
  await expect(page.getByLabel('描述你的画面')).toHaveValue('')
  await expect(card.getByText('执行失败', { exact: true })).toBeVisible()
})

test('retry survives a lost response, preserves its failed parent and executes once', async ({ page, request }, testInfo) => {
  const originalPrompt = `[mock-fail] Synthetic retry ${testInfo.project.name} ${randomUUID()}`
  const parent = await createFixture(request, { prompt: originalPrompt, kind: 'general', ratio: '1:1', style: 'natural' })
  const retryPrompt = originalPrompt.replace('[mock-fail] ', '')
  let loseFirstResponse = true
  const keys: string[] = []
  await page.route(`**/api/v1/tasks/${parent.id}/retry?*`, async route => {
    keys.push(route.request().headers()['idempotency-key'])
    const response = await route.fetch()
    if (loseFirstResponse) {
      loseFirstResponse = false
      await route.abort('failed')
    } else {
      await route.fulfill({ response })
    }
  })
  await page.goto('/')
  const original = page.locator('.task').filter({ hasText: originalPrompt })
  await original.getByRole('button', { name: '编辑后重试' }).click()
  await page.getByLabel('描述你的画面').fill(retryPrompt)
  await page.getByLabel('画面比例').selectOption('16:9')
  await page.getByRole('button', { name: '创建重试任务', exact: false }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.getByRole('button', { name: '取消编辑' })).toBeVisible()
  await page.getByRole('button', { name: '创建重试任务', exact: false }).click()
  const retry = page.locator('.task').filter({ has: page.locator('.task-prompt', { hasText: retryPrompt }) }).filter({ hasNotText: '[mock-fail]' })
  await expect(retry.getByText('模拟完成', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '取消编辑' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '创建模拟图像', exact: false })).toBeVisible()
  expect(keys).toHaveLength(2)
  expect(keys[0]).toBeTruthy()
  expect(keys[1]).toBe(keys[0])
  const list = await request.get(`${apiBase}/tasks${personalQuery}`)
  const tasks = await list.json() as Task[]
  const children = tasks.filter(task => task.retry_of === parent.id)
  expect(children).toHaveLength(1)
  expect(children[0].prompt).toBe(retryPrompt)
  expect(children[0].ratio).toBe('16:9')
  expect(tasks.find(task => task.id === parent.id)?.status).toBe('failed')
  await retry.getByRole('button', { name: '保存到素材库' }).click()
  await expect(retry.getByRole('button', { name: '已保存' })).toBeDisabled()
  await page.reload()
  await expect(original.getByText('执行失败', { exact: true })).toBeVisible()
  await expect(retry.getByText('模拟完成', { exact: true })).toBeVisible()
  await retry.getByRole('button', { name: '查看详情' }).click()
  await expect(page.getByRole('dialog')).toContainText(parent.id)
  await page.keyboard.press('Escape')
  await expectNoOverflow(page)
})

test('library combines server search and type filtering and recovers from query failure', async ({ page, request }, testInfo) => {
  const marker = `SyntheticSearch-${testInfo.project.name}-${randomUUID()}`
  const poster = await createFixture(request, { prompt: `${marker} Coffee 海报`, kind: 'poster', ratio: '4:5', style: 'illustration' }, true)
  const portrait = await createFixture(request, { prompt: `${marker} coffee 肖像`, kind: 'portrait', ratio: '1:1', style: 'studio' }, true)
  await page.goto('/')
  await page.getByRole('button', { name: /素材库/ }).first().click()
  const search = page.getByLabel('搜索素材')
  const filter = page.getByLabel('素材类型')
  const queryResponse = page.waitForResponse(response => {
    const url = new URL(response.url())
    return url.pathname === '/api/v1/assets' && url.searchParams.get('q') === marker && response.ok()
  })
  await search.fill(marker)
  const response = await queryResponse
  expect((await response.json() as Array<{ id: string }>).map(asset => asset.id).sort()).toEqual([poster.id, portrait.id].sort())
  await expect(page.locator('.asset-grid article')).toHaveCount(2)
  const filteredResponse = page.waitForResponse(response => {
    const url = new URL(response.url())
    return url.pathname === '/api/v1/assets' && url.searchParams.get('q') === marker && url.searchParams.get('kind') === 'portrait' && response.ok()
  })
  await filter.selectOption('portrait')
  expect((await (await filteredResponse).json() as Array<{ id: string }>).map(asset => asset.id)).toEqual([portrait.id])
  await expect(page.locator('.asset-grid article')).toHaveCount(1)
  await expect(page.locator('.asset-grid')).toContainText(portrait.prompt)
  await expectNoOverflow(page)
  await capture(page, testInfo.outputPath('iteration-library.png'))
  let reportHeld: (() => void) | undefined
  let releaseOldResponse: (() => void) | undefined
  let reportDelivered: (() => void) | undefined
  const held = new Promise<void>(resolve => { reportHeld = resolve })
  const released = new Promise<void>(resolve => { releaseOldResponse = resolve })
  const delivered = new Promise<void>(resolve => { reportDelivered = resolve })
  await page.route('**/api/v1/assets?*', async route => {
    const url = new URL(route.request().url())
    if (url.searchParams.get('q') === marker && url.searchParams.get('kind') === 'poster') {
      const response = await route.fetch()
      reportHeld?.()
      await released
      await route.fulfill({ response }).catch(() => undefined)
      reportDelivered?.()
    } else await route.continue()
  })
  await filter.selectOption('poster')
  await held
  const latestResponse = page.waitForResponse(response => {
    const url = new URL(response.url())
    return url.pathname === '/api/v1/assets' && url.searchParams.get('q') === marker && url.searchParams.get('kind') === 'general' && response.ok()
  })
  await filter.selectOption('general')
  expect(await (await latestResponse).json()).toEqual([])
  releaseOldResponse?.()
  await delivered
  await expect(page.locator('.asset-grid article')).toHaveCount(0)
  await expect(page.getByText('没有找到匹配素材')).toBeVisible()
  await page.unroute('**/api/v1/assets?*')
  await search.fill(`${marker}-no-match`)
  await expect(page.getByText(/没有.*匹配|未找到.*素材|没有找到/)).toBeVisible()
  await expect(page.locator('.asset-grid article')).toHaveCount(0)
  await page.getByRole('button', { name: /清除.*筛选|清空.*筛选|重置.*筛选/ }).click()
  await expect(search).toHaveValue('')
  await expect(filter).toHaveValue('')
  await page.route('**/api/v1/assets?*', async route => {
    if (new URL(route.request().url()).searchParams.get('q') === marker) await route.abort('failed')
    else await route.continue()
  })
  await search.fill(marker)
  await expect(page.getByRole('alert')).toBeVisible()
  await page.unroute('**/api/v1/assets?*')
  await page.getByRole('button', { name: '重新查询', exact: true }).click()
  await expect(page.locator('.asset-grid article')).toHaveCount(2)
  await page.getByLabel('选择演示空间').selectOption('team-demo')
  await page.getByRole('button', { name: /素材库/ }).first().click()
  await expect(page.getByText(marker, { exact: false })).toHaveCount(0)
})

test('late submission response cannot leak across spaces; reload resumes durable task', async ({ page, request }, testInfo) => {
  let releaseResponse: (() => void) | undefined
  let reportCreated: ((task: Task) => void) | undefined
  const created = new Promise<Task>(resolve => { reportCreated = resolve })
  const released = new Promise<void>(resolve => { releaseResponse = resolve })
  await page.route('**/api/v1/tasks?*', async route => {
    if (route.request().method() !== 'POST') return route.continue()
    const response = await route.fetch()
    reportCreated?.(await response.json() as Task)
    await released
    await route.fulfill({ response }).catch(() => undefined)
  })
  await page.goto('/')
  const prompt = `Synthetic in-flight ${testInfo.project.name} ${randomUUID()}`
  await page.getByLabel('描述你的画面').fill(prompt)
  await page.getByRole('button', { name: '创建模拟图像', exact: false }).click()
  const task = await created
  await page.getByLabel('选择演示空间').selectOption('team-demo')
  releaseResponse?.()
  await expect(page.locator('.task').filter({ hasText: prompt })).toHaveCount(0)
  await expect(page.getByLabel('描述你的画面')).toHaveValue('')
  await page.getByLabel('选择演示空间').selectOption('personal-demo')
  await page.reload()
  const card = page.locator('.task').filter({ hasText: prompt })
  await expect(card.getByText('模拟完成', { exact: true })).toBeVisible()
  const result = await request.get(`${apiBase}/tasks/${task.id}${personalQuery}`)
  expect((await result.json() as Task).status).toBe('succeeded')
  await expectNoOverflow(page)
})

test('space, catalog, detail and save failures recover through real endpoints', async ({ page, request }, testInfo) => {
  await page.route('**/api/v1/spaces', route => route.abort('failed'))
  await page.route('**/api/v1/templates', route => route.abort('failed'))
  await page.goto('/')
  await expect(page.getByRole('alert')).toContainText('空间')
  await expect(page.getByLabel('选择演示空间')).toBeDisabled()
  await page.unroute('**/api/v1/spaces')
  await page.getByRole('button', { name: '重新加载空间' }).click()
  await expect(page.getByRole('alert')).toContainText('模板')
  await expect(page.getByRole('button', { name: '创建模拟图像', exact: false })).toBeDisabled()
  await page.unroute('**/api/v1/templates')
  await page.getByRole('button', { name: '重新加载模板' }).click()
  await expect(page.locator('.template-card')).toHaveCount(3)
  const task = await createFixture(request, {
    prompt: `Synthetic detail recovery ${testInfo.project.name} ${randomUUID()}`,
    kind: 'poster', ratio: '4:5', style: 'natural',
  })
  await page.route(`**/api/v1/tasks/${task.id}?*`, route => route.abort('failed'))
  const card = page.locator('.task').filter({ hasText: task.prompt })
  await card.getByRole('button', { name: '查看详情' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('alert')).toBeVisible()
  await page.unroute(`**/api/v1/tasks/${task.id}?*`)
  await dialog.getByRole('button', { name: /重新|重试/ }).click()
  await expect(dialog).toContainText(task.id)
  await expect(dialog.getByRole('alert')).toHaveCount(0)
  await page.route('**/api/v1/assets?*', async route => {
    if (route.request().method() === 'POST') await route.abort('failed')
    else await route.continue()
  })
  await dialog.getByRole('button', { name: '保存到素材库' }).click()
  await expect(dialog.getByRole('alert')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '保存到素材库' })).toBeEnabled()
  const unsaved = await request.get(`${apiBase}/assets${personalQuery}`)
  expect((await unsaved.json() as Array<{ task_id: string }>).some(asset => asset.task_id === task.id)).toBe(false)
  await page.unroute('**/api/v1/assets?*')
  const saveResponse = page.waitForResponse(response => response.url().includes('/api/v1/assets?') && response.request().method() === 'POST')
  await dialog.getByRole('button', { name: '保存到素材库' }).click()
  expect((await saveResponse).status()).toBe(200)
  await expect(dialog.getByRole('button', { name: '已保存' })).toBeDisabled()
  await page.keyboard.press('Escape')
  await page.reload()
  await page.getByRole('button', { name: /素材库/ }).first().click()
  await page.getByLabel('搜索素材').fill(task.prompt)
  await expect(page.locator('.asset-grid article')).toHaveCount(1)
  await expect(page.locator('.asset-grid')).toContainText(task.prompt)
})
