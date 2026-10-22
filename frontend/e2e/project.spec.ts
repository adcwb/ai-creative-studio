import { randomUUID } from 'node:crypto'
import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import type { DramaProject, ScriptGeneration } from '../src/drama'
const api = 'http://127.0.0.1:8081/api/v1'
const query = '?space_id=personal-demo'
const errors = new WeakMap<Page, string[]>()
test.beforeEach(({ page }) => { const list: string[] = []; errors.set(page, list); page.on('pageerror', e => list.push(e.message)) })
test.afterEach(({ page }) => { expect(errors.get(page)).toEqual([]) })
async function fixture(request: APIRequestContext, title: string, generate = true) {
  const response = await request.post(`${api}/projects${query}`, { headers: { 'Idempotency-Key': randomUUID() }, data: { title, brief: '合成验收简报：修钟师与快递员合作，把迟到的信送达。' } })
  expect(response.status()).toBe(201)
  let project = await response.json() as DramaProject
  if (generate) {
    const result = await request.post(`${api}/projects/${project.id}/generations${query}`, { headers: { 'Idempotency-Key': randomUUID() }, data: { expected_version: 1 } })
    expect(result.status()).toBe(202)
    await expect.poll(async () => { project = await readProject(request, project.id); return project.latest_generation?.status }).toBe('succeeded')
  }
  return project
}
async function readProject(request: APIRequestContext, id: string) { const response = await request.get(`${api}/projects/${id}${query}`); expect(response.ok()).toBe(true); return response.json() as Promise<DramaProject> }
async function openDrama(page: Page, title?: string) {
  await page.goto('/')
  await page.getByRole('button', { name: '短剧工作台', exact: true }).click()
  if (title) await page.locator('.drama-project-list button').filter({ hasText: title }).click()
  await expect(page.getByLabel('项目名称')).toBeVisible()
}
async function noOverflow(page: Page) { expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true) }
async function capture(page: Page, path: string) {
  await page.evaluate(async () => { await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); if (document.activeElement instanceof HTMLElement) document.activeElement.blur(); window.scrollTo(0, 0) })
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(0)
  await page.screenshot({ path, fullPage: true })
}

test('project creation, asynchronous script generation and lost-save response use durable backend receipts', async ({ page, request }, info) => {
  await openDrama(page)
  if (await page.getByRole('button', { name: '新建短剧项目', exact: false }).isEnabled()) await page.getByRole('button', { name: '新建短剧项目', exact: false }).click()
  const title = `合成短剧-${info.project.name}-${randomUUID()}`
  await page.getByLabel('项目名称').fill(title)
  await page.getByLabel('创作简报').fill('合成验收：修钟师与快递员合作送达一封旧信。')
  const createdResponse = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/projects')
  await page.getByRole('button', { name: '创建项目', exact: true }).dblclick()
  const created = await createdResponse; expect(created.status()).toBe(201)
  const project = await created.json() as DramaProject
  await expect(page.getByLabel('项目名称')).toHaveValue(title)
  const accepted = page.waitForResponse(r => r.request().method() === 'POST' && r.url().includes(`/projects/${project.id}/generations`))
  await page.getByRole('button', { name: '生成模拟剧本', exact: true }).dblclick()
  expect((await accepted).status()).toBe(202)
  await expect(page.getByLabel('项目名称')).toBeDisabled()
  await expect(page.locator('.drama-script')).toBeVisible()
  const generated = await readProject(request, project.id)
  expect(generated.version).toBe(2); expect(generated.script?.characters).toHaveLength(2); expect(generated.script?.scenes).toHaveLength(3)
  const history = await request.get(`${api}/projects/${project.id}/generations${query}`); expect(await history.json()).toHaveLength(1)
  await page.getByRole('button', { name: '编辑剧本', exact: true }).click()
  const originalAction = await page.getByLabel('第 1 场动作', { exact: true }).inputValue()
  await page.getByLabel('第 1 场动作', { exact: true }).fill('取消的编辑不得进入后端。')
  await page.getByRole('button', { name: '取消剧本编辑', exact: true }).click()
  expect((await readProject(request, project.id)).version).toBe(2)
  await page.getByRole('button', { name: '编辑剧本', exact: true }).click()
  await expect(page.getByLabel('第 1 场动作', { exact: true })).toHaveValue(originalAction)
  const edited = '合成的已保存动作：两人打开钟楼，找到了信件。'
  await page.getByLabel('第 1 场动作', { exact: true }).fill(edited)
  const keys: string[] = []
  page.on('request', r => { if (r.method() === 'PUT' && r.url().includes(`/projects/${project.id}/script`)) keys.push(r.headers()['idempotency-key']) })
  await page.route(`**/api/v1/projects/${project.id}/script?*`, async route => { await route.fetch(); await route.abort('failed') }, { times: 1 })
  await page.getByRole('button', { name: '保存剧本', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('提交结果尚未确认')
  expect((await readProject(request, project.id)).version).toBe(3)
  await expect(page.getByLabel('第 1 场动作', { exact: true })).toHaveValue(edited)
  const replay = page.waitForResponse(r => r.request().method() === 'PUT' && r.url().includes(`/projects/${project.id}/script`))
  await page.getByRole('button', { name: '重试上次请求', exact: true }).click()
  expect((await replay).status()).toBe(200); expect(keys).toHaveLength(2); expect(keys[0]).toBe(keys[1])
  await expect(page.getByText('剧本修改已保存。', { exact: true })).toBeVisible()
  expect((await readProject(request, project.id)).version).toBe(3)
  await page.reload(); await page.getByRole('button', { name: '短剧工作台', exact: true }).click()
  await page.locator('.drama-project-list button').filter({ hasText: title }).click()
  await expect(page.locator('.drama-script')).toContainText(edited)
  await noOverflow(page); await capture(page, info.outputPath('project-script.png'))
})

test('version conflicts preserve the local script until an explicit discard and reload', async ({ page, request }, info) => {
  const project = await fixture(request, `冲突验收-${info.project.name}-${randomUUID()}`)
  await openDrama(page, project.title)
  await page.getByRole('button', { name: '编辑剧本', exact: true }).click()
  const local = '本地未保存的动作应在版本冲突后保留。'
  await page.getByLabel('第 1 场动作', { exact: true }).fill(local)
  const concurrent = await request.put(`${api}/projects/${project.id}${query}`, { headers: { 'Idempotency-Key': randomUUID() }, data: { title: project.title, brief: '另一客户端已保存的新简报，不能被旧版本覆盖。', expected_version: 2 } })
  expect(concurrent.status()).toBe(200)
  const conflict = page.waitForResponse(r => r.request().method() === 'PUT' && r.url().includes(`/projects/${project.id}/script`))
  await page.getByRole('button', { name: '保存剧本', exact: true }).click()
  const failed = await conflict; expect(failed.status()).toBe(409); expect((await failed.json()).error.code).toBe('version_conflict')
  await expect(page.getByLabel('第 1 场动作', { exact: true })).toHaveValue(local)
  await expect(page.getByRole('alert')).toContainText('本地草稿已保留')
  await page.getByRole('button', { name: '重新读取最新项目', exact: true }).click()
  const dialog = page.getByRole('dialog'); await expect(dialog).toBeVisible()
  await dialog.getByRole('button').last().focus(); await page.keyboard.press('Tab'); expect(await dialog.evaluate(n => n.contains(document.activeElement))).toBe(true)
  await page.keyboard.press('Escape'); await expect(dialog).toHaveCount(0)
  await expect(page.getByLabel('第 1 场动作', { exact: true })).toHaveValue(local)
  await page.getByRole('button', { name: '重新读取最新项目', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: /放弃/ }).click()
  await expect(page.getByLabel('创作简报')).toHaveValue('另一客户端已保存的新简报，不能被旧版本覆盖。')
  await expect(page.locator('.drama-script')).not.toContainText(local)
  await expect(page.getByText('当前剧本来自之前的简报', { exact: true })).toBeVisible()
  expect((await readProject(request, project.id)).version).toBe(3)
  await noOverflow(page)
})

test('dirty drafts require confirmation across projects, spaces and workspaces; refresh reads saved content', async ({ page, request }, info) => {
  const first = await fixture(request, `草稿一-${info.project.name}-${randomUUID()}`, false)
  const second = await fixture(request, `草稿二-${info.project.name}-${randomUUID()}`, false)
  await openDrama(page, first.title)
  await page.getByLabel('创作简报').fill('未保存的合成草稿。')
  await page.locator('.drama-project-list button').filter({ hasText: second.title }).click()
  await expect(page.getByRole('dialog')).toBeVisible(); await page.getByRole('dialog').getByRole('button', { name: '继续编辑' }).click()
  await expect(page.getByLabel('项目名称')).toHaveValue(first.title); await expect(page.getByLabel('创作简报')).toHaveValue('未保存的合成草稿。')
  await page.locator('.drama-project-list button').filter({ hasText: second.title }).click(); await page.getByRole('dialog').getByRole('button', { name: /放弃/ }).click()
  await expect(page.getByLabel('项目名称')).toHaveValue(second.title)
  await page.getByLabel('创作简报').fill('切换空间之前的未保存草稿。')
  await page.getByLabel('选择演示空间').selectOption('team-demo'); await page.getByRole('dialog').getByRole('button', { name: '继续编辑' }).click()
  await expect(page.getByLabel('选择演示空间')).toHaveValue('personal-demo')
  await page.getByLabel('选择演示空间').selectOption('team-demo'); await page.getByRole('dialog').getByRole('button', { name: /放弃/ }).click()
  await expect(page.getByLabel('选择演示空间')).toHaveValue('team-demo'); await expect(page.locator('.drama-project-list')).not.toContainText(second.title)
  await page.getByLabel('选择演示空间').selectOption('personal-demo'); await page.locator('.drama-project-list button').filter({ hasText: first.title }).click()
  await page.getByLabel('创作简报').fill('切换图像工作台前的未保存草稿。')
  await page.getByRole('button', { name: '图像工作室', exact: true }).click(); await page.getByRole('dialog').getByRole('button', { name: /放弃/ }).click()
  await expect(page.getByLabel('描述你的画面')).toBeVisible(); await page.getByRole('button', { name: '短剧工作台', exact: true }).click(); await page.locator('.drama-project-list button').filter({ hasText: first.title }).click()
  await expect(page.getByLabel('创作简报')).toHaveValue(first.brief)
  await page.getByLabel('创作简报').fill('刷新前未保存草稿。'); page.once('dialog', dialog => dialog.accept()); await page.reload()
  await page.getByRole('button', { name: '短剧工作台', exact: true }).click(); await page.locator('.drama-project-list button').filter({ hasText: first.title }).click(); await expect(page.getByLabel('创作简报')).toHaveValue(first.brief)
  expect((await readProject(request, first.id)).version).toBe(1); expect((await readProject(request, second.id)).version).toBe(1)
  await noOverflow(page)
})

test('regeneration confirmation and failure preserve the saved script and source brief', async ({ page, request }, info) => {
  const project = await fixture(request, `失败保稿-${info.project.name}-${randomUUID()}`)
  await openDrama(page, project.title)
  await page.getByLabel('创作简报').fill('[mock-fail] 合成失败必须保留当前稿。')
  await page.getByRole('button', { name: '保存项目简报', exact: true }).click(); await expect(page.getByText('项目简报已保存。', { exact: true })).toBeVisible()
  await expect(page.getByText('当前剧本来自之前的简报', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '重新生成剧本', exact: true }).click(); await page.keyboard.press('Escape')
  expect((await (await request.get(`${api}/projects/${project.id}/generations${query}`)).json() as ScriptGeneration[])).toHaveLength(1)
  await page.getByRole('button', { name: '重新生成剧本', exact: true }).click(); await page.getByRole('dialog').getByRole('button', { name: '确认重新生成', exact: true }).click()
  await expect(page.locator('.drama-generation')).toContainText('mock_script_failed')
  const failed = await readProject(request, project.id); expect(failed.version).toBe(3); expect(failed.script).toEqual(project.script); expect(failed.script_brief).toBe(project.brief)
  await page.route(`**/api/v1/projects/${project.id}/generations?*`, route => route.abort('failed'))
  await page.getByRole('button', { name: '查看生成记录', exact: false }).click(); await expect(page.locator('.drama-history').getByRole('alert')).toBeVisible()
  await page.unroute(`**/api/v1/projects/${project.id}/generations?*`); await page.getByRole('button', { name: '重试读取生成记录', exact: true }).click(); await expect(page.locator('.drama-history li')).toHaveCount(2)
  await noOverflow(page)
})

test('project list errors recover and a late create response cannot leak across spaces', async ({ page, request }, info) => {
  await page.route('**/api/v1/projects?*', route => route.abort('failed'))
  await page.goto('/'); await page.getByRole('button', { name: '短剧工作台', exact: true }).click()
  await expect(page.locator('.drama-projects').getByRole('alert')).toBeVisible()
  await page.unroute('**/api/v1/projects?*'); await page.getByRole('button', { name: '重新加载项目列表', exact: true }).click()
  await expect(page.getByLabel('项目名称')).toBeVisible()
  if (await page.getByRole('button', { name: '新建短剧项目', exact: false }).isEnabled()) await page.getByRole('button', { name: '新建短剧项目', exact: false }).click()
  const title = `迟到响应-${info.project.name}-${randomUUID()}`
  await page.getByLabel('项目名称').fill(title); await page.getByLabel('创作简报').fill('迟到响应只能属于创建时的个人演示空间。')
  let release: () => void = () => undefined
  const wait = new Promise<void>(resolve => { release = resolve })
  let accepted: (project: DramaProject) => void = () => undefined
  const created = new Promise<DramaProject>(resolve => { accepted = resolve })
  await page.route('**/api/v1/projects?space_id=personal-demo', async route => {
    if (route.request().method() !== 'POST') return route.continue()
    const response = await route.fetch(); accepted(await response.json() as DramaProject); await wait
    await route.fulfill({ response }).catch(() => undefined)
  }, { times: 1 })
  await page.getByRole('button', { name: '创建项目', exact: true }).click()
  const project = await created
  await page.getByLabel('选择演示空间').selectOption('team-demo'); await page.getByRole('dialog').getByRole('button', { name: /放弃/ }).click(); release()
  await expect(page.locator('.drama-project-list')).not.toContainText(title)
  await expect(page.getByLabel('项目名称')).not.toHaveValue(title)
  await page.getByLabel('选择演示空间').selectOption('personal-demo')
  await page.route(`**/api/v1/projects/${project.id}?*`, route => route.abort('failed'))
  await page.locator('.drama-project-list button').filter({ hasText: title }).click()
  await expect(page.getByRole('button', { name: '重试读取项目', exact: true })).toBeVisible()
  await page.unroute(`**/api/v1/projects/${project.id}?*`); await page.getByRole('button', { name: '重试读取项目', exact: true }).click()
  await expect(page.getByLabel('项目名称')).toHaveValue(title)
  expect((await readProject(request, project.id)).space_id).toBe('personal-demo')
  await noOverflow(page)
})

test('image drafts and an uncertain image idempotency key survive a round trip through drama', async ({ page, request }, info) => {
  await page.goto('/')
  const prompt = `跨工作台图像草稿-${info.project.name}-${randomUUID()}`
  await page.getByLabel('描述你的画面').fill(prompt)
  await page.getByLabel('画面比例').selectOption('16:9'); await page.getByLabel('视觉风格').selectOption('illustration')
  await page.getByRole('button', { name: '短剧工作台', exact: true }).click()
  await page.getByRole('button', { name: '图像工作室', exact: true }).click()
  await expect(page.getByLabel('描述你的画面')).toHaveValue(prompt)
  await expect(page.getByLabel('画面比例')).toHaveValue('16:9'); await expect(page.getByLabel('视觉风格')).toHaveValue('illustration')
  const keys: string[] = []
  page.on('request', r => { if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/tasks') keys.push(r.headers()['idempotency-key']) })
  await page.route('**/api/v1/tasks?space_id=personal-demo', async route => {
    if (route.request().method() !== 'POST') return route.continue()
    await route.fetch(); await route.abort('failed')
  }, { times: 1 })
  await page.getByRole('button', { name: '创建模拟图像', exact: true }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await page.getByRole('button', { name: '短剧工作台', exact: true }).click()
  await page.getByRole('button', { name: '图像工作室', exact: true }).click()
  await expect(page.getByLabel('描述你的画面')).toHaveValue(prompt)
  const replay = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/tasks')
  await page.getByRole('button', { name: '创建模拟图像', exact: true }).click()
  expect((await replay).status()).toBe(200); expect(keys).toHaveLength(2); expect(keys[0]).toBe(keys[1])
  const tasks = await (await request.get(`${api}/tasks${query}`)).json() as {prompt: string}[]
  expect(tasks.filter(t => t.prompt === prompt)).toHaveLength(1)
})
