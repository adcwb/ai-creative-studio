import {test, expect, type Page, type APIRequestContext} from '@playwright/test'
import type {DramaProject, DramaScript} from '../src/drama'

const fixture = 'http://127.0.0.1:5189'
const modelConfig = {
  version: 1, gateways: [{id: 'gateway-1', name: '测试网关'}],
  models: [{id: 'model-1', gateway_id: 'gateway-1', name: '测试文本模型', model: 'test-text', version: 1, capabilities: ['text'], enabled: true}],
  bindings: [{feature_key: 'script_generation', model_config_id: 'model-1', enabled: true, allow_override: false}],
  features: [], secret_storage_available: true, governance_ready: true,
}

async function setup(page: Page, request: APIRequestContext, empty = false) {
  await request.post(`${fixture}/__test__/reset`, {data: {management: modelConfig}})
  const state = await (await request.get(`${fixture}/__test__/state`)).json()
  const current = structuredClone(state.projects[0]) as DramaProject
  if (empty) {delete current.script; delete current.storyboard; current.script_version = 0}
  const original = structuredClone(current.script)
  const posts: {key: string; body: Record<string, unknown>}[] = []
  const requests: string[] = []
  const history: {id: string; kind: string; created_at: string; script_version: number; script: DramaScript}[] = []
  let failed = false, dropReply = false, activeReads = 0
  function capture() {
    if (current.script) history.unshift({id: `revision-${history.length + 1}`, kind: 'script', created_at: current.updated_at, script_version: current.script_version, script: structuredClone(current.script)})
  }
  page.on('request', req => {if (req.url().includes('/api/v1/projects/story-1')) requests.push(new URL(req.url()).pathname)})
  await page.route('**/api/v1/projects/story-1**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname.replace('/api/v1/projects/story-1', '')
    if (!path) {
      if (current.latest_generation?.status === 'running' && ++activeReads >= 2) {
        current.latest_generation = {...current.latest_generation, status: failed ? 'failed' : 'succeeded', progress: 100, ...(failed ? {error: '模型输出达到长度上限，剧本未完整生成。', error_code: 'provider_output_truncated'} : {})}
        if (!failed) {
          capture()
          current.script = {title: '新生成的剧本', logline: '修钟师终于找到了旧信的主人。', characters: [{name: '修钟师', description: '守护钟楼的人'}], scenes: [{heading: '钟楼门前', action: '修钟师推开大门。', episode_number: 1, dialogue: [{character: '修钟师', text: '信终于送到了。'}]}]}
          current.script_provider = 'gateway-text'; current.script_brief = current.brief
          current.version++; current.script_version++; current.storyboard_stale = !!current.storyboard
        }
      }
      return route.fulfill({json: current})
    }
    if (path === '/generations' && req.method() === 'POST') {
      const body = req.postDataJSON(), key = req.headers()['idempotency-key']
      posts.push({body, key})
      if (posts.length === 1 || key !== posts[posts.length - 2].key) {
        activeReads = 0
        current.latest_generation = {id: `generation-${posts.length}`, project_id: current.id, space_id: current.space_id, source_title: current.title, source_brief: current.brief, base_version: current.version, provider: 'gateway-text', status: 'running', progress: 20, created_at: current.updated_at, updated_at: current.updated_at}
      }
      if (dropReply) {dropReply = false; return route.fulfill({status: 503, json: {error: {code: 'storage_error', message: '提交结果尚未确认'}}})}
      return route.fulfill({status: 202, json: current.latest_generation})
    }
    if (path === '/script' && req.method() === 'PUT') {
      capture(); current.script = req.postDataJSON().script; current.version++; current.script_version++
      return route.fulfill({json: current})
    }
    if (path === '/history') return route.fulfill({json: history})
    if (/^\/history\/.+\/restore$/.test(path)) {
      const selected = history.find(item => path.includes(item.id))!
      const script = structuredClone(selected.script)
      capture(); current.script = script; current.version++; current.script_version++
      return route.fulfill({json: current})
    }
    return route.continue()
  })
  await page.goto('/projects/story-1/script')
  await expect(page.getByRole('heading', {name: 'AI 剧本', exact: true})).toBeVisible()
  return {posts, requests, original, fail: () => {failed = true}, drop: () => {dropReply = true}}
}

test('first generation becomes the current editable script without candidate adoption', async ({page, request}) => {
  const state = await setup(page, request, true)
  await expect(page.getByRole('button', {name: 'AI 生成剧本', exact: true})).toBeEnabled()
  await page.getByRole('button', {name: 'AI 生成剧本', exact: true}).click()
  await expect(page.getByRole('button', {name: '正在生成剧本…'})).toBeDisabled()
  await expect(page.getByRole('button', {name: '开始手写剧本'})).toBeDisabled()
  await expect(page.locator('.drama-script')).toContainText('新生成的剧本')
  expect(state.posts).toHaveLength(1)
  expect(state.posts[0].body).not.toHaveProperty('candidate_only')
  expect(state.posts[0].body).toMatchObject({model_config_id: 'model-1', model_config_version: 1})
  await expect(page.getByText('剧本候选与采纳', {exact: true})).toHaveCount(0)
  await page.getByRole('button', {name: '编辑剧本', exact: true}).click()
  await page.getByLabel('剧本标题', {exact: true}).fill('作者修改后的剧本')
  await expect(page.getByRole('button', {name: '重新生成', exact: true})).toBeDisabled()
  await page.getByRole('button', {name: '保存剧本', exact: true}).click()
  await expect(page.locator('.drama-script h3').first()).toHaveText('作者修改后的剧本')
  expect(state.requests.some(path => /\/(media|creative-jobs|generations)$/.test(path) && !path.endsWith('/generations'))).toBe(false)
  expect(state.requests.filter(path => path.endsWith('/history'))).toHaveLength(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: test.info().outputPath('script-editor.png'), fullPage: true})
})

test('regeneration confirms before submitting and the old draft can be restored on demand', async ({page, request}) => {
  const state = await setup(page, request)
  await page.getByRole('button', {name: '重新生成', exact: true}).click()
  const confirmation = page.getByRole('group', {name: '确认重新生成剧本'})
  await expect(confirmation).toBeVisible()
  await confirmation.getByRole('button', {name: '取消', exact: true}).click()
  expect(state.posts).toHaveLength(0)
  await page.getByRole('button', {name: '重新生成', exact: true}).click()
  await confirmation.getByRole('button', {name: '确认重新生成', exact: true}).click()
  await expect(page.locator('.drama-script')).toContainText('新生成的剧本')
  expect(state.requests.filter(path => path.endsWith('/history'))).toHaveLength(0)
  await page.getByText('版本历史', {exact: true}).click()
  await page.getByRole('button', {name: '恢复此剧本版本', exact: true}).first().click()
  await page.getByRole('button', {name: '确认恢复历史版本', exact: true}).click()
  await expect(page.locator('.drama-script h3').first()).toHaveText(state.original!.title)
  expect(state.posts).toHaveLength(1)
})

test('failed generation preserves the draft and stops polling without reading media', async ({page, request}) => {
  const state = await setup(page, request)
  state.fail()
  await page.getByRole('button', {name: '重新生成', exact: true}).click()
  await page.getByRole('button', {name: '确认重新生成', exact: true}).click()
  await expect(page.getByRole('alert')).toHaveCount(1)
  await expect(page.getByRole('alert')).toContainText('本次未能生成完整剧本，当前内容已保留。')
  await expect(page.locator('.drama-script h3').first()).toHaveText(state.original!.title)
  await page.getByText('查看详情', {exact: true}).click()
  await expect(page.getByRole('alert')).toContainText('provider_output_truncated')
  const count = state.requests.length
  // Observe longer than the former 2-second background polling interval.
  await page.waitForTimeout(2600)
  expect(state.requests).toHaveLength(count)
  expect(state.requests.some(path => /\/(media|creative-jobs|history)$/.test(path))).toBe(false)
  expect(state.posts).toHaveLength(1)
  await expect(page.getByRole('button', {name: '重新生成', exact: true})).toBeEnabled()
})

test('an unconfirmed submission checks the same request without creating another generation', async ({page, request}) => {
  const state = await setup(page, request, true)
  state.drop()
  await page.getByRole('button', {name: 'AI 生成剧本', exact: true}).click()
  await expect(page.getByRole('button', {name: '核对原生成请求', exact: true})).toBeVisible()
  await expect(page.getByRole('button', {name: 'AI 生成剧本', exact: true})).toBeDisabled()
  await page.getByRole('button', {name: '核对原生成请求', exact: true}).click()
  await expect(page.locator('.drama-script')).toContainText('新生成的剧本')
  expect(state.posts).toHaveLength(2)
  expect(state.posts[0]).toEqual(state.posts[1])
})
