import { navigateTo, openProject, projectStep, openCreativeTools } from './support/navigation'
import { randomUUID } from 'node:crypto'
import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import type { DramaProject } from '../src/drama'
const api = 'http://127.0.0.1:8081/api/v1'
const query = '?space_id=personal-demo'
async function write(request: APIRequestContext, path: string, data: unknown) {
  const response = await request.post(`${api}${path}${query}`, {headers: {'Idempotency-Key': randomUUID()}, data})
  expect(response.ok(), await response.text()).toBe(true)
  return response.json()
}
async function create(request: APIRequestContext, title: string, storyboard = false) {
  let project = await write(request, '/projects', {title, brief: '合成创作验收：一位修钟师在雨夜寻找失散的朋友。'}) as DramaProject
  if (storyboard) {
    await write(request, `/projects/${project.id}/generations`, {expected_version: project.version})
    await expect.poll(async () => {project = await (await request.get(`${api}/projects/${project.id}${query}`)).json(); return project.latest_generation?.status}).toBe('succeeded')
    await write(request, `/projects/${project.id}/storyboard-generations`, {expected_version: project.version})
    await expect.poll(async () => {project = await (await request.get(`${api}/projects/${project.id}${query}`)).json(); return project.latest_storyboard_generation?.status}).toBe('succeeded')
  }
  return project
}
async function open(page: Page, project: DramaProject) {
  await page.goto('/')
  await navigateTo(page, '/projects')
  await openProject(page, project.title, 'brief')
  await expect(page.getByLabel('项目名称')).toHaveValue(project.title)
}

test('analysis and synthetic image generation persist through actual asynchronous APIs', async ({page, request}, info) => {
  const project = await create(request, `创作任务-${info.project.name}-${randomUUID()}`)
  await open(page, project)
  await openCreativeTools(page)
  await page.getByRole('button', {name: '分析已保存需求', exact: true}).click()
  await expect(page.locator('.creative-analysis')).toContainText('待澄清问题')
  await expect(page.locator('.creative-analysis')).toContainText('local-rules')
  await page.getByLabel('生成提示或配音文本', {exact: true}).fill('合成演示：雨夜钟楼色卡')
  await page.getByRole('button', {name: '生成合成演示图片', exact: true}).click()
  await expect.poll(async () => (await (await request.get(`${api}/projects/${project.id}/creative-jobs${query}`)).json() as {status: string}[]).some(job => job.status === 'succeeded')).toBe(true)
  await projectStep(page, 'media')
  await expect(page.locator('.media-assets img')).toHaveCount(1)
  const jobs = await (await request.get(`${api}/projects/${project.id}/creative-jobs${query}`)).json() as {kind: string; status: string; provider: string}[]
  expect(jobs.some(job => job.kind === 'analysis' && job.status === 'succeeded')).toBe(true)
  expect(jobs.some(job => job.kind === 'image' && job.status === 'succeeded' && job.provider === 'local-synthetic')).toBe(true)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('photo editing uploads new cropped and rotated pixels with reference metadata', async ({page, request}, info) => {
  const project = await create(request, `照片编辑-${info.project.name}-${randomUUID()}`, true)
  await open(page, project)
  await projectStep(page, 'media')
  const data = await page.evaluate(() => {const canvas = document.createElement('canvas'); canvas.width = 80; canvas.height = 40; const c = canvas.getContext('2d')!; c.fillStyle = '#678060'; c.fillRect(0, 0, 80, 40); return canvas.toDataURL('image/png').split(',')[1]})
  await page.getByLabel('素材用途', {exact: true}).selectOption('reference')
  await page.locator('.media-studio').getByLabel('关联角色', {exact: true}).selectOption(project.script!.characters[0].name)
  await page.getByLabel('选择待编辑照片', {exact: true}).setInputFiles({name: 'synthetic-photo.png', mimeType: 'image/png', buffer: Buffer.from(data, 'base64')})
  await page.getByLabel('照片旋转', {exact: true}).selectOption('90')
  await page.getByLabel('居中裁剪为正方形', {exact: true}).check()
  await page.getByRole('button', {name: '保存编辑照片为新素材', exact: true}).click()
  const image = page.locator('.media-assets img').first()
  await expect(image).toBeVisible()
  await expect.poll(() => image.evaluate((node: HTMLImageElement) => [node.naturalWidth, node.naturalHeight])).toEqual([40, 40])
  const media = await (await request.get(`${api}/projects/${project.id}/media${query}`)).json()
  expect(media.assets).toHaveLength(1)
  expect(media.assets[0]).toMatchObject({role: 'reference', character: project.script!.characters[0].name, name: 'edited-synthetic-photo.png'})
})

test('shots can be added reordered removed and restored from persisted history', async ({page, request}, info) => {
  const project = await create(request, `镜头结构-${info.project.name}-${randomUUID()}`, true)
  await open(page, project)
  await projectStep(page, 'storyboard')
  await page.getByRole('button', {name: '编辑分镜', exact: true}).click()
  await page.getByRole('button', {name: '添加镜头', exact: true}).click()
  await page.getByLabel('第 4 镜画面描述', {exact: true}).fill('新增镜头：合成灯光特写')
  await page.getByRole('button', {name: '上移第 4 镜', exact: true}).click()
  await expect(page.getByLabel('第 3 镜画面描述', {exact: true})).toHaveValue('新增镜头：合成灯光特写')
  await page.getByRole('button', {name: '删除第 4 镜', exact: true}).click()
  await page.getByRole('button', {name: '保存分镜', exact: true}).click()
  await expect(page.getByRole('button', {name: '编辑分镜', exact: true})).toBeVisible()
  const saved = await (await request.get(`${api}/projects/${project.id}${query}`)).json() as DramaProject
  expect(saved.storyboard!.shots).toHaveLength(3)
  expect(saved.storyboard!.shots[2].description).toBe('新增镜头：合成灯光特写')
  await openCreativeTools(page)
  await page.getByText(/剧本与分镜版本历史（/).click()
  await page.getByRole('button', {name: '恢复此分镜版本', exact: true}).first().click()
  await page.getByRole('button', {name: '确认恢复历史版本', exact: true}).click()
  await expect.poll(async () => (await (await request.get(`${api}/projects/${project.id}${query}`)).json()).version).toBeGreaterThan(saved.version)
  const restored = await (await request.get(`${api}/projects/${project.id}${query}`)).json() as DramaProject
  expect(restored.storyboard!.shots).toEqual(project.storyboard!.shots)
})
