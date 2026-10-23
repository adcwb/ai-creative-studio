import { randomUUID } from 'node:crypto'
import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
const api = 'http://127.0.0.1:8081/api/v1'
const query = '?space_id=personal-demo'
async function project(request: APIRequestContext, name: string) {
  const response = await request.post(`${api}/projects${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {title: name, brief: '合成媒体验收，仅验证本地音视频和字幕，不代表真实模型生成。'}})
  expect(response.status()).toBe(201)
  return response.json() as Promise<{id: string; title: string}>
}
async function open(page: Page, title: string) {
  await page.goto('/')
  await page.getByRole('button', {name: '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: title}).click()
  await expect(page.getByRole('heading', {name: '素材与成片', exact: true})).toBeVisible()
}

test('synthetic local timeline saves, exports and plays actual MP4 on desktop and mobile', async ({page, request}, info) => {
  test.setTimeout(60000)
  const errors: string[] = []; page.on('pageerror', e => errors.push(e.message))
  const p = await project(request, `成片-${info.project.name}-${randomUUID()}`)
  let saved = await (await request.get(`${api}/projects/${p.id}${query}`)).json()
  for (const suffix of ['generations', 'storyboard-generations']) {
    const response = await request.post(`${api}/projects/${p.id}/${suffix}${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: saved.version}})
    expect(response.status()).toBe(202)
    await expect.poll(async () => {saved = await (await request.get(`${api}/projects/${p.id}${query}`)).json(); return suffix === 'generations' ? saved.latest_generation?.status : saved.latest_storyboard_generation?.status}).toBe('succeeded')
  }
  expect(saved.storyboard.shots).toHaveLength(3)
  await open(page, p.title)
  await page.getByRole('button', {name: '创建合成演示素材与时间轴', exact: true}).click()
  await expect(page.getByLabel('片段 1 字幕', {exact: true})).toBeVisible()
  const linked = await (await request.get(`${api}/projects/${p.id}/media${query}`)).json()
  expect(linked.assets.filter((asset: {kind: string}) => asset.kind === 'image').map((asset: {shot_id: string}) => asset.shot_id)).toEqual(saved.storyboard.shots.map((shot: {id: string}) => shot.id))
  const subtitle = `合成验收 ${info.project.name}`
  await page.getByLabel('片段 1 字幕', {exact: true}).fill(subtitle)
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await expect(page.getByRole('button', {name: '合成并导出 MP4', exact: true})).toBeEnabled()
  await page.getByRole('button', {name: '合成并导出 MP4', exact: true}).click()
  const video = page.getByLabel('成片预览', {exact: true}).first()
  await expect(video).toBeVisible({timeout: 40000})
  await expect.poll(() => video.evaluate((node: HTMLVideoElement) => node.readyState), {timeout: 10000}).toBeGreaterThanOrEqual(2)
  const played = await video.evaluate(async (node: HTMLVideoElement) => {
    node.muted = true; await node.play()
    await new Promise(resolve => setTimeout(resolve, 500)); node.pause()
    return {time: node.currentTime, duration: node.duration, width: node.videoWidth, error: node.error?.code}
  })
  expect(played.time).toBeGreaterThan(0)
  expect(played.duration).toBeGreaterThan(0)
  expect(played.width).toBeGreaterThan(0)
  expect(played.error).toBeUndefined()
  await expect.poll(() => video.evaluate((node: HTMLVideoElement) => node.textTracks[0]?.cues?.length ?? 0)).toBeGreaterThan(0)
  expect(await video.evaluate((node: HTMLVideoElement) => node.textTracks[0]?.mode)).toBe('showing')
  await expect(page.getByRole('link', {name: '下载 MP4 成片', exact: true}).first()).toHaveAttribute('href', /\/exports\/[^/]+\/file/)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: `/tmp/iteration6-media-${info.project.name}.png`, fullPage: true})
  await page.reload()
  await page.getByRole('button', {name: '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: p.title}).click()
  await expect(page.getByLabel('片段 1 字幕', {exact: true})).toHaveValue(subtitle)
  await expect(page.getByLabel('成片预览', {exact: true}).first()).toBeVisible()
  expect(errors).toEqual([])
})

test('media upload rejects forged image content and keeps the asset list empty', async ({page, request}, info) => {
  const p = await project(request, `上传安全-${info.project.name}-${randomUUID()}`)
  await open(page, p.title)
  await page.getByLabel('上传素材（PNG/JPEG/WAV/MP3/MP4）', {exact: true}).setInputFiles({name: 'forged.png', mimeType: 'image/png', buffer: Buffer.from('<svg onload=alert(1)>forged</svg>')})
  await expect(page.locator('.media-studio').getByRole('alert')).toBeVisible()
  const state = await (await request.get(`${api}/projects/${p.id}/media${query}`)).json()
  expect(state.assets).toHaveLength(0)
})

test('timeline polling cannot silently overwrite a concurrent save; lost responses replay one write', async ({page, request}, info) => {
  const p = await project(request, `时间轴保护-${info.project.name}-${randomUUID()}`)
  const path = `${api}/projects/${p.id}/media`
  const initial = await request.post(`${path}/demo${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: 1}})
  expect(initial.status()).toBe(200)
  const media = await initial.json()
  await open(page, p.title)
  const localText = '本地草稿不应覆盖另一个客户端'
  await page.getByLabel('片段 1 字幕', {exact: true}).fill(localText)
  const timeline = structuredClone(media.timeline)
  timeline.clips[0].subtitle = '另一个客户端的已保存字幕'
  const concurrent = await request.put(`${path}/timeline${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: media.version, timeline}})
  expect(concurrent.status()).toBe(200)
  // Force the routine refresh to observe the remote revision before saving.
  await expect.poll(async () => page.locator('.media-studio').textContent()).toContain(`媒体版本 ${media.version + 1}`)
  const conflict = page.waitForResponse(r => r.request().method() === 'PUT' && r.url().includes('/media/timeline'))
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  expect((await conflict).status()).toBe(409)
  await expect(page.getByLabel('片段 1 字幕', {exact: true})).toHaveValue(localText)
  expect((await (await request.get(path + query)).json()).timeline.clips[0].subtitle).toBe(timeline.clips[0].subtitle)
  await page.getByRole('button', {name: '取消时间轴编辑', exact: true}).click()
  await page.getByLabel('片段 1 字幕', {exact: true}).fill('响应丢失后原键恢复')
  const keys: string[] = [], bodies: string[] = []
  page.on('request', r => {if (r.method() === 'PUT' && r.url().includes('/media/timeline')) {keys.push(r.headers()['idempotency-key']); bodies.push(r.postData() ?? '')}})
  await page.route(`**/api/v1/projects/${p.id}/media/timeline?*`, async route => {await route.fetch(); await route.abort('failed')}, {times: 1})
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await expect(page.locator('.media-studio').getByRole('alert')).toContainText('结果未确认')
  const savedVersion = (await (await request.get(path + query)).json()).version
  await page.getByRole('button', {name: '原请求重试', exact: true}).click()
  await expect(page.getByRole('button', {name: '保存时间轴', exact: true})).toBeDisabled()
  expect(keys).toHaveLength(2); expect(keys[0]).toBe(keys[1]); expect(bodies[0]).toBe(bodies[1])
  expect((await (await request.get(path + query)).json()).version).toBe(savedVersion)
})

test('HD portrait output, burn subtitles, fade and extra audio survive timeline save', async ({page, request}, info) => {
  const p = await project(request, `高清音轨-${info.project.name}-${randomUUID()}`)
  const path = `${api}/projects/${p.id}/media`
  const seeded = await request.post(`${path}/demo${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: 1}})
  expect(seeded.status()).toBe(200)
  await open(page, p.title)
  await page.getByLabel('输出清晰度', {exact: true}).selectOption('720p')
  await page.getByLabel('成片画幅', {exact: true}).selectOption('9:16')
  await page.getByLabel('字幕输出', {exact: true}).selectOption('burn')
  await page.getByLabel('片段 1 转场', {exact: true}).selectOption('fade')
  await page.getByRole('button', {name: '添加音轨', exact: true}).click()
  await page.getByLabel('音轨 1 volume', {exact: true}).fill('0.2')
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await expect(page.getByRole('button', {name: '保存时间轴', exact: true})).toBeDisabled()
  const saved = await (await request.get(path + query)).json()
  expect(saved.timeline).toMatchObject({output_resolution: '720p', aspect_ratio: '9:16', subtitle_mode: 'burn'})
  expect(saved.timeline.clips[0].transition).toBe('fade')
  expect(saved.timeline.audio_tracks).toHaveLength(1)
  expect(saved.timeline.audio_tracks[0].volume).toBe(0.2)
  await page.reload()
  await page.getByRole('button', {name: '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: p.title}).click()
  await expect(page.getByLabel('输出清晰度', {exact: true})).toHaveValue('720p')
  await expect(page.getByLabel('音轨 1 volume', {exact: true})).toHaveValue('0.2')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})
