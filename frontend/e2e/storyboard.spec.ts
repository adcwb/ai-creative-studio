import { navigateTo, openProject, chooseSpace } from './support/navigation'
import { randomUUID } from 'node:crypto'
import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import type { DramaProject } from '../src/drama'
const api = 'http://127.0.0.1:8081/api/v1'
const query = '?space_id=personal-demo'
const errors = new WeakMap<Page, string[]>()
test.beforeEach(({ page }) => { const list: string[] = []; errors.set(page, list); page.on('pageerror', e => list.push(e.message)) })
test.afterEach(({ page }) => { expect(errors.get(page)).toEqual([]) })
async function read(request: APIRequestContext, id: string) {
  const response = await request.get(`${api}/projects/${id}${query}`)
  expect(response.status()).toBe(200)
  return response.json() as Promise<DramaProject>
}
async function write(request: APIRequestContext, path: string, body: unknown, method = 'POST') {
  const response = await request.fetch(`${api}${path}${query}`, { method, headers: {'Idempotency-Key': randomUUID()}, data: body })
  expect(response.ok(), await response.text()).toBe(true)
  return response.json()
}
async function fixture(request: APIRequestContext, title: string, board = true, script = true) {
  let project = await write(request, '/projects', {title, brief:'合成验收：修钟师与快递员合作，把迟到的信送往钟楼。'}) as DramaProject
  if (script) {
    await write(request, `/projects/${project.id}/generations`, {expected_version:project.version})
    await expect.poll(async () => {project = await read(request, project.id); return project.latest_generation?.status}).toBe('succeeded')
  }
  if (board) {
    await write(request, `/projects/${project.id}/storyboard-generations`, {expected_version:project.version})
    await expect.poll(async () => {project = await read(request, project.id); return project.latest_storyboard_generation?.status}).toBe('succeeded')
  }
  return project
}
async function open(page: Page, project: DramaProject) {
  await page.goto('/')
  await navigateTo(page, '/projects')
  await openProject(page, project.title, 'storyboard')
}
async function reload(page: Page, project: DramaProject) { await open(page, project) }
const description = (page: Page) => page.getByLabel('第 1 镜画面描述', {exact:true})
const edit = (page: Page) => page.getByRole('button',{name:'编辑分镜',exact:true})
const save = (page: Page) => page.getByRole('button',{name:'保存分镜',exact:true})

test('saved script generates a real persisted storyboard; lost save response replays the same request', async ({page,request}, info) => {
  const project = await fixture(request, `分镜闭环-${info.project.name}-${randomUUID()}`, false)
  await open(page,project)
  await expect(page.getByRole('button',{name:'生成模拟分镜',exact:true})).toBeEnabled()
  const accepted = page.waitForResponse(r => r.request().method()==='POST' && r.url().includes('/storyboard-generations'))
  await page.getByRole('button',{name:'生成模拟分镜',exact:true}).dblclick()
  expect((await accepted).status()).toBe(202)
  await expect(edit(page)).toBeVisible()
  const generated = await read(request,project.id)
  expect(generated.storyboard?.shots.length).toBeGreaterThan(0)
  expect(generated.storyboard?.source_script_version).toBe(generated.script_version)
  expect(await (await request.get(`${api}/projects/${project.id}/storyboard-generations${query}`)).json()).toHaveLength(1)
  await edit(page).click()
  await description(page).fill('取消的分镜草稿不应写入后端。')
  await page.getByRole('button',{name:'取消分镜编辑',exact:true}).click()
  expect((await read(request,project.id)).storyboard).toEqual(generated.storyboard)
  await edit(page).click()
  const text = '合成已保存画面：雨后钟楼，镜头缓缓靠近信封。'
  await description(page).fill(text)
  await page.getByLabel('第 1 镜场景引用', {exact:true}).selectOption('2')
  await page.getByLabel('第 1 镜景别', {exact:true}).selectOption('close_up')
  await page.getByLabel('第 1 镜运镜', {exact:true}).selectOption('track')
  await page.getByLabel('第 1 镜时长（秒）', {exact:true}).fill('12')
  await page.getByLabel('第 1 镜对白', {exact:true}).fill('合成对白：让我们一起打开这封信。')
  const keys: string[] = [], bodies: string[] = []
  page.on('request', r => {if(r.method()==='PUT' && r.url().includes('/storyboard?')){keys.push(r.headers()['idempotency-key']);bodies.push(r.postData() ?? '')}})
  await page.route(`**/api/v1/projects/${project.id}/storyboard?*`,async route=>{await route.fetch();await route.abort('failed')},{times:1})
  await save(page).click()
  await expect(page.getByRole('alert')).toContainText('提交结果尚未确认')
  await expect(description(page)).toHaveValue(text)
  const saved = await read(request,project.id)
  expect(saved.storyboard?.shots[0]).toMatchObject({description:text,scene_index:2,shot_size:'close_up',camera_movement:'track',duration_seconds:12,dialogue:'合成对白：让我们一起打开这封信。'})
  const response = page.waitForResponse(r=>r.request().method()==='PUT' && r.url().includes('/storyboard?'))
  await page.getByRole('button',{name:'重试上次请求',exact:true}).click()
  expect((await response).status()).toBe(200)
  expect(keys).toHaveLength(2);expect(keys[0]).toBe(keys[1]);expect(bodies[0]).toBe(bodies[1])
  await expect(edit(page)).toBeVisible()
  expect((await read(request,project.id)).version).toBe(saved.version)
  await reload(page,project)
  await expect(page.locator('.drama-storyboard')).toContainText(text)
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
})

test('stale source remains stale after plan edits and failed regeneration keeps the previous plan',async({page,request},info)=>{
  let project = await fixture(request,`分镜过期-${info.project.name}-${randomUUID()}`)
  const original = structuredClone(project.storyboard)
  const script = structuredClone(project.script!)
  script.scenes[0].action = '[mock-fail] 合成失败场景，已有分镜必须保留。'
  project = await write(request,`/projects/${project.id}/script`,{script,expected_version:project.version},'PUT') as DramaProject
  expect(project.storyboard_stale).toBe(true)
  await open(page,project)
  await expect(page.locator('.drama-storyboard')).toContainText('已过期')
  await edit(page).click();await description(page).fill('即使编辑旧分镜，也不能把来源伪装成最新剧本。');await save(page).click()
  await expect(edit(page)).toBeVisible()
  project = await read(request,project.id)
  expect(project.storyboard_stale).toBe(true)
  expect(project.storyboard?.source_script_version).toBe(original?.source_script_version)
  await page.getByRole('button',{name:'重新生成分镜',exact:true}).click()
  await page.keyboard.press('Escape')
  expect(await (await request.get(`${api}/projects/${project.id}/storyboard-generations${query}`)).json()).toHaveLength(1)
  await page.getByRole('button',{name:'重新生成分镜',exact:true}).click()
  await page.getByRole('dialog').getByRole('button',{name:'确认重新生成分镜',exact:true}).click()
  await expect.poll(async()=> (await read(request,project.id)).latest_storyboard_generation?.status).toBe('failed')
  await expect(page.locator('.storyboard-generation')).toContainText('mock_storyboard_failed')
  const failed = await read(request,project.id)
  expect(failed.storyboard).toEqual(project.storyboard);expect(failed.version).toBe(project.version)
})

test('conflicting storyboard saves and navigation preserve local drafts until explicit discard',async({page,request},info)=>{
  const project = await fixture(request,`分镜冲突-${info.project.name}-${randomUUID()}`)
  await open(page,project);await edit(page).click()
  const local = '未保存分镜草稿，切换和冲突均不能静默丢弃。'
  await description(page).fill(local)
  for(const action of [()=>navigateTo(page, '/projects'),()=>chooseSpace(page, 'team-demo'),()=>navigateTo(page, '/studio')]){
    await action();await expect(page.getByRole('dialog')).toBeVisible()
    await page.getByRole('dialog').getByRole('button',{name:'继续编辑',exact:true}).click()
    await expect(description(page)).toHaveValue(local)
  }
  const shots = structuredClone(project.storyboard!.shots);shots[0].description='另一客户端保存的分镜画面。'
  await write(request,`/projects/${project.id}/storyboard`,{shots,expected_version:project.version},'PUT')
  const conflict=page.waitForResponse(r=>r.request().method()==='PUT'&&r.url().includes('/storyboard?'))
  await save(page).click();expect((await conflict).status()).toBe(409)
  await expect(description(page)).toHaveValue(local)
  await expect(page.getByRole('alert')).toContainText('本地草稿已保留')
  await page.getByRole('button',{name:'重新读取最新项目',exact:true}).click();await page.keyboard.press('Escape')
  await expect(description(page)).toHaveValue(local)
  await page.getByRole('button',{name:'重新读取最新项目',exact:true}).click()
  await page.getByRole('dialog').getByRole('button',{name:/放弃/}).click()
  await expect(page.locator('.drama-storyboard')).toContainText(shots[0].description)
  await expect(page.locator('.drama-storyboard')).not.toContainText(local)
})

test('storyboard prerequisites and bounded editor validation do not create invalid writes',async({page,request},info)=>{
  const empty = await fixture(request,`分镜空态-${info.project.name}-${randomUUID()}`,false,false)
  await open(page,empty)
  await expect(page.getByRole('button',{name:'生成模拟分镜',exact:true})).toBeDisabled()
  const project = await fixture(request,`分镜校验-${info.project.name}-${randomUUID()}`)
  await open(page,project);await edit(page).click();await description(page).fill('')
  await expect(save(page)).toBeDisabled()
  expect((await read(request,project.id)).version).toBe(project.version)
})
