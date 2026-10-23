import { randomUUID } from 'node:crypto'
import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
const api = 'http://127.0.0.1:8081/api/v1'
const query = '?space_id=personal-demo'
async function post(request: APIRequestContext, path: string, data: unknown) {
  const response = await request.post(`${api}${path}${query}`, {headers: {'Idempotency-Key': randomUUID()}, data})
  expect(response.ok(), await response.text()).toBe(true)
  return response.json()
}
async function fixture(page: Page, request: APIRequestContext, name: string) {
  const project = await post(request, '/projects', {title: `${name}-${randomUUID()}`, brief: '合成字幕时间验收，不调用真实语音识别。'})
  const media = await post(request, `/projects/${project.id}/media/demo`, {expected_version: 1})
  await page.goto('/')
  await page.getByRole('button', {name: '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: project.title}).click()
  await expect(page.getByLabel('片段 1 字幕', {exact: true})).toBeVisible()
  return {project, media}
}

test('estimated dialogue timing is reviewed, edited, persisted and exported as exact subtitle cues', async ({page, request}, info) => {
  test.setTimeout(60000)
  const {project} = await fixture(page, request, `逐句字幕-${info.project.name}`)
  const region = page.getByRole('region', {name: '片段 1 逐句字幕', exact: true})
  await expect(region.getByRole('button', {name: '识别片段 1 配音并对齐', exact: true})).toBeDisabled()
  await expect(region).toContainText('不读取或识别音频')
  await page.getByLabel('片段 1 字幕', {exact: true}).fill('第一句对白。第二句对白。')
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await page.getByRole('button', {name: '估算片段 1 对白时间', exact: true}).click()
  await expect(region.getByRole('button', {name: '应用至片段 1 字幕草稿', exact: true})).toBeEnabled()
  await region.getByRole('button', {name: '应用至片段 1 字幕草稿', exact: true}).click()
  await expect(region).toContainText('字幕来源：按文本估算 · 未识别音频')
  await page.getByLabel('片段 1 第 1 句开始', {exact: true}).fill('0.25')
  await page.getByLabel('片段 1 第 1 句结束', {exact: true}).fill('0.75')
  await page.getByLabel('片段 1 第 1 句文本', {exact: true}).fill('人工校正对白')
  await expect(region).toContainText('字幕来源：人工编辑')
  const keys: string[] = [], bodies: string[] = []
  page.on('request', r => {if (r.method() === 'PUT' && r.url().includes(`/projects/${project.id}/media/timeline`)) {keys.push(r.headers()['idempotency-key']); bodies.push(r.postData() ?? '')}})
  await page.route(`**/api/v1/projects/${project.id}/media/timeline?*`, async route => {await route.fetch(); await route.abort('failed')}, {times: 1})
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await expect(page.locator('.media-studio').getByRole('alert')).toContainText('结果未确认')
  await page.getByRole('button', {name: '原请求重试', exact: true}).click()
  await expect(page.getByRole('button', {name: '保存时间轴', exact: true})).toBeDisabled()
  expect(keys).toHaveLength(2); expect(keys[0]).toBe(keys[1]); expect(bodies[0]).toBe(bodies[1])
  const saved = await (await request.get(`${api}/projects/${project.id}/media${query}`)).json()
  expect(saved.timeline.clips[0]).toMatchObject({subtitle_source: 'manual', subtitle_cues: [{start: 0.25, end: 0.75, text: '人工校正对白'}, {start: expect.any(Number), end: expect.any(Number), text: expect.any(String)}]})
  expect(saved.timeline.clips[0].subtitle_alignment_id).toBeTruthy()
  await page.getByRole('button', {name: '合成并导出 MP4', exact: true}).click()
  const video = page.getByLabel('成片预览', {exact: true}).first()
  await expect(video).toBeVisible({timeout: 40000})
  await expect.poll(() => video.evaluate((node: HTMLVideoElement) => node.textTracks[0]?.cues?.length ?? 0)).toBeGreaterThan(0)
  const cue = await video.evaluate((node: HTMLVideoElement) => {const cue = node.textTracks[0].cues![0] as VTTCue; return {start: cue.startTime, end: cue.endTime, text: cue.text}})
  expect(cue).toEqual({start: 0.25, end: 0.75, text: '人工校正对白'})
  expect(await video.evaluate((node: HTMLVideoElement) => node.textTracks[0].mode)).toBe('showing')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: `/tmp/iteration6-alignment-${info.project.name}.png`, fullPage: true})
  await region.screenshot({path: `/tmp/iteration6-alignment-${info.project.name}-cues.png`})
})

test('alignment results cannot overwrite a newer saved timeline', async ({page, request}, info) => {
  const {project, media} = await fixture(page, request, `字幕来源冲突-${info.project.name}`)
  await page.getByRole('button', {name: '估算片段 1 对白时间', exact: true}).click()
  const apply = page.getByRole('button', {name: '应用至片段 1 字幕草稿', exact: true})
  await expect(apply).toBeEnabled()
  const timeline = structuredClone(media.timeline)
  timeline.clips[0].subtitle = '另一客户端已保存的字幕'
  const changed = await request.put(`${api}/projects/${project.id}/media/timeline${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: media.version, timeline}})
  expect(changed.status()).toBe(200)
  await expect(page.getByLabel('片段 1 字幕', {exact: true})).toHaveValue(timeline.clips[0].subtitle)
  await expect(apply).toBeDisabled()
  await expect(page.getByRole('region', {name: '片段 1 逐句字幕', exact: true})).toContainText('来源版本已变化')
})

test('ASR UI sends saved audio without estimate text (intercepted request only, no recognition)', async ({page, request}, info) => {
  await page.route('**/api/v1/alignment-capabilities', async route => {
    await route.fulfill({json: {estimate: {provider: 'local-text-estimate', mode: 'mock', ready: true, message: '文本估算'}, asr: {provider: 'openai-asr', model: 'ui-fixture-model', mode: 'real', ready: true, message: '仅拦截请求体测试，不调用识别服务'}}})
  })
  const {project, media} = await fixture(page, request, `ASR请求体-${info.project.name}`)
  const audio = media.assets.find((asset: {kind: string}) => asset.kind === 'audio')
  await page.getByLabel('片段 1 音频', {exact: true}).selectOption(audio.id)
  await page.getByLabel('片段 1 字幕', {exact: true}).fill('这些已保存文字不得作为实际ASR的识别结果输入。')
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await expect(page.getByRole('button', {name: '保存时间轴', exact: true})).toBeDisabled()
  await page.route(`**/api/v1/projects/${project.id}/media/alignments?*`, async route => {
    if (route.request().method() !== 'POST') {await route.continue(); return}
    await route.fulfill({status: 202, json: {id: 'ui-intercept-only', status: 'queued', mode: 'asr', provider: 'openai-asr', model: 'ui-fixture-model', source_version: media.version + 1, clip_id: media.timeline.clips[0].id, audio_asset_id: audio.id, duration: 2, cues: [], warnings: []}})
  })
  const sent = page.waitForRequest(r => r.method() === 'POST' && r.url().includes(`/projects/${project.id}/media/alignments?`))
  await page.getByRole('button', {name: '识别片段 1 配音并对齐', exact: true}).click()
  const body = (await sent).postDataJSON()
  expect(body).toMatchObject({mode: 'asr', audio_asset_id: audio.id, clip_id: media.timeline.clips[0].id, expected_version: media.version + 1, expected_provider: 'openai-asr', expected_model: 'ui-fixture-model'})
  expect(body).not.toHaveProperty('text')
})
