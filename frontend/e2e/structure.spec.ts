import {test, expect, type APIRequestContext} from '@playwright/test'
import {randomUUID} from 'node:crypto'
import type {DramaProject} from '../src/drama'

const api = 'http://127.0.0.1:8081/api/v1', query = '?space_id=personal-demo'
async function write(request: APIRequestContext, path: string, data: unknown, method = 'POST') {
  const response = await request.fetch(api + path + query, {method, headers: {'Idempotency-Key': randomUUID()}, data})
  expect(response.ok(), await response.text()).toBe(true)
  return await response.json() as DramaProject
}
async function read(request: APIRequestContext, id: string) {
  const response = await request.get(`${api}/projects/${id}${query}`)
  expect(response.ok()).toBe(true)
  return await response.json() as DramaProject
}
async function fixture(request: APIRequestContext) {
  let p = await write(request, '/projects', {title: `旧稿稳定关联-${randomUUID()}`, brief: '保持旧稿与场次原始引用的合成验收。'})
  p = await write(request, `/projects/${p.id}/script`, {expected_version: p.version, script: {
    title: '河畔的约定', logline: '两个伙伴在河畔相遇，在街道告别。', characters: [{name: '小满', description: '想离开小镇的画家'}],
    scenes: [{heading: '河边相遇', action: '小满走向河边。', dialogue: [{character: '小满', text: '你好，明天见。'}], episode_number: 1}, {heading: '街道告别', action: '小满在街口挥手。', dialogue: [], episode_number: 7}],
  }}, 'PUT')
  return await write(request, `/projects/${p.id}/storyboard`, {expected_version: p.version, shots: [{id: 'old-shot', scene_index: 1, description: '人物在河边挥手。', shot_size: 'medium', camera_movement: 'static', duration_seconds: 3, dialogue: ''}]}, 'PUT')
}

test('explicit stable associations retain original scene references through rename, reorder and stale-board editing', async ({page, request}, info) => {
  const errors: string[] = []; page.on('pageerror', error => errors.push(error.message))
  let modelCalls = 0
  page.on('request', r => {if (r.method() === 'POST' && /\/(gateway-generations|generations|creative-jobs)\?/.test(r.url())) modelCalls++})
  const original = await fixture(request)
  await page.goto(`/projects/${original.id}/script`)
  await page.getByText('保留角色与场次关联',{exact:true}).click()
  const upgrade = page.getByRole('button', {name: '启用稳定关联', exact: true})
  await expect(upgrade).toBeEnabled()
  await page.screenshot({path: `/tmp/structure-upgrade-${info.project.name}.png`, fullPage: true})
  const keys: string[] = [], bodies: string[] = []
  page.on('request', r => {if (r.method() === 'POST' && r.url().includes('/structure-upgrades?')) {keys.push(r.headers()['idempotency-key']); bodies.push(r.postData() ?? '')}})
  await page.route(`**/api/v1/projects/${original.id}/structure-upgrades?*`, async route => {await route.fetch(); await route.abort('failed')}, {times: 1})
  await upgrade.click()
  await expect(page.getByRole('alert')).toContainText('提交结果尚未确认')
  await expect(upgrade).toBeDisabled()
  const accepted = await read(request, original.id)
  expect(accepted.structure_version).toBe(2)
  await page.getByRole('button', {name: '重试上次请求', exact: true}).click()
  await expect(upgrade).toHaveCount(0)
  expect(keys).toHaveLength(2); expect(keys[0]).toBe(keys[1]); expect(bodies[0]).toBe(bodies[1])
  await page.getByRole('button', {name: '编辑剧本', exact: true}).click()
  await page.getByRole('textbox', {name: '角色名', exact: true}).fill('秋生')
  await page.getByRole('button', {name: '上移场次', exact: true}).nth(1).click()
  await expect(page.getByRole('combobox', {name: '对白角色', exact: true})).toHaveValue(accepted.script!.characters[0].id!)
  await page.getByRole('button', {name: '保存剧本', exact: true}).click()
  await expect(page.getByRole('button', {name: '编辑剧本', exact: true})).toBeEnabled()
  const changed = await read(request, original.id)
  expect(changed.script!.characters[0].id).toBe(accepted.script!.characters[0].id)
  expect(changed.script!.scenes[1].id).toBe(accepted.script!.scenes[0].id)
  expect(changed.script!.scenes[1].dialogue[0]).toMatchObject({character: '秋生', character_id: accepted.script!.characters[0].id})
  expect(changed.storyboard_stale).toBe(true)
  await page.getByRole('link', {name: '分镜与预演', exact: true}).click()
  await expect(page.locator('.storyboard-stale')).toBeVisible()
  await page.getByRole('button', {name: '编辑分镜', exact: true}).click()
  await page.getByRole('textbox', {name: '第 1 镜画面描述', exact: true}).fill('保留河边场次的原始关联。')
  await page.getByRole('button', {name: '保存分镜', exact: true}).click()
  await expect(page.getByRole('button', {name: '编辑分镜', exact: true})).toBeEnabled()
  const board = await read(request, original.id)
  expect(board.storyboard!.shots[0].scene_id).toBe(accepted.script!.scenes[0].id)
  expect(board.storyboard!.shots[0].scene_id).not.toBe(changed.script!.scenes[0].id)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: `/tmp/structure-board-${info.project.name}.png`, fullPage: true})
  expect(modelCalls).toBe(0); expect(errors).toEqual([])
})

test('a missing historical source rejects upgrade while keeping the existing draft visible', async ({page, request}) => {
  const p = await fixture(request)
  await page.goto(`/projects/${p.id}/script`)
  await page.route(`**/api/v1/projects/${p.id}/structure-upgrades?*`, route => route.fulfill({status: 400, contentType: 'application/json', body: JSON.stringify({error: {code: 'invalid_input', message: '找不到分镜的来源剧本，未更改原项目，请先核对历史'}})}))
  await page.getByText('保留角色与场次关联',{exact:true}).click()
  await page.getByRole('button', {name: '启用稳定关联', exact: true}).click()
  await expect(page.getByRole('alert')).toContainText('未更改原项目')
  await expect(page.getByRole('heading', {name: '河畔的约定', exact: true})).toBeVisible()
  await expect(page.getByRole('button', {name: '编辑剧本', exact: true})).toBeEnabled()
  expect((await read(request, p.id)).structure_version).toBeUndefined()
})
