import {expectControlValue, selectOption} from './support/controls'
import { navigateTo, openProject } from './support/navigation'
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
  await navigateTo(page, '/projects')
  await openProject(page, title, 'media')
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
  await navigateTo(page, '/projects')
  await openProject(page, p.title, 'media')
  await expectControlValue(page.getByLabel('片段 1 字幕', {exact: true}), subtitle)
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
  await expectControlValue(page.getByLabel('片段 1 字幕', {exact: true}), localText)
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
  await selectOption(page.getByRole('combobox', {name: '输出清晰度', exact: true}), '720p')
  await selectOption(page.getByRole('combobox', {name: '成片画幅', exact: true}), '9:16')
  await selectOption(page.getByRole('combobox', {name: '字幕输出', exact: true}), 'burn')
  await selectOption(page.getByLabel('片段 1 转场', {exact: true}), 'fade')
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
  await navigateTo(page, '/projects')
  await openProject(page, p.title, 'media')
  await expectControlValue(page.getByRole('combobox', {name: '输出清晰度', exact: true}), '720p')
  await expectControlValue(page.getByLabel('音轨 1 volume', {exact: true}), '0.2')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('two-minute portrait 4K settings save without duration quotas and explain source quality', async ({page, request}, info) => {
  const p = await project(request, `高清剪辑-${info.project.name}-${randomUUID()}`)
  const path = `${api}/projects/${p.id}/media`
  expect((await request.post(`${path}/demo${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: 1}})).status()).toBe(200)
  await open(page, p.title)
  await selectOption(page.getByRole('combobox', {name: '输出清晰度', exact: true}), '2160p')
  await selectOption(page.getByRole('combobox', {name: '成片画幅', exact: true}), '9:16')
  for (const number of [1, 2, 3]) await page.getByLabel(`片段 ${number} 时长`, {exact: true}).fill('40')
  await expect(page.getByText('输出尺寸：2160 × 3840', {exact: true})).toBeVisible()
  await expect(page.getByText('清晰度用于剪辑合成，不代表模型原生画质。素材分辨率不足时会放大，细节以原始素材为准。', {exact: true})).toBeVisible()
  await expect(page.getByRole('button', {name: '保存时间轴', exact: true})).toBeEnabled()
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await expect(page.getByRole('button', {name: '合成并导出 MP4', exact: true})).toBeEnabled()
  const saved = await (await request.get(path + query)).json()
  expect(saved.timeline).toMatchObject({output_resolution: '2160p', aspect_ratio: '9:16'})
  expect(saved.timeline.clips.reduce((sum: number, clip: {duration: number}) => sum + clip.duration, 0)).toBe(120)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: `/tmp/studio-hd-output-${info.project.name}.png`, fullPage: true})
})

test('mask editor saves a transparent PNG as a new reference without model calls', async ({page, request}, info) => {
  const p = await project(request, `局部遮罩-${info.project.name}-${randomUUID()}`)
  const path = `${api}/projects/${p.id}/media`
  const seeded = await request.post(`${path}/demo${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: 1}})
  expect(seeded.status()).toBe(200)
  const before = await seeded.json()
  let generations = 0
  page.on('request', r => {if (r.method() === 'POST' && r.url().includes('/creative-jobs')) generations++})
  await open(page, p.title)
  await page.getByRole('button', {name: '绘制局部编辑遮罩', exact: true}).first().click()
  await page.getByRole('button', {name: '标记矩形修改区域', exact: true}).click()
  const canvas = page.getByLabel('局部编辑遮罩画布，可用下方矩形输入代替涂抹', {exact: true})
  expect(await canvas.evaluate((node: HTMLCanvasElement) => {
    const context = node.getContext('2d')!
    return [context.getImageData(0, 0, 1, 1).data[3], context.getImageData(node.width / 2, node.height / 2, 1, 1).data[3]]
  })).toEqual([255, 0])
  await page.getByRole('button', {name: '保存遮罩为新素材', exact: true}).click()
  await expect.poll(async () => (await (await request.get(path + query)).json()).assets.length).toBe(before.assets.length + 1)
  const after = await (await request.get(path + query)).json()
  const mask = after.assets.find((asset: {name: string}) => asset.name.startsWith('mask-'))
  expect(mask).toMatchObject({mime_type: 'image/png', role: 'reference', source: 'uploaded'})
  for (const asset of before.assets) expect(after.assets.some((item: {id: string}) => item.id === asset.id)).toBe(true)
  expect(generations).toBe(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('crossfade and visual overlay validate the shortened timeline, persist and render', async ({page, request}, info) => {
  test.setTimeout(60000)
  const p = await project(request, `交叉叠层-${info.project.name}-${randomUUID()}`)
  const path = `${api}/projects/${p.id}/media`
  const seeded = await request.post(`${path}/demo${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: 1}})
  expect(seeded.status()).toBe(200)
  const initial = await seeded.json()
  const expectedDuration = initial.timeline.clips.reduce((sum: number, clip: {duration: number}) => sum + clip.duration, 0) - 1
  await open(page, p.title)
  await selectOption(page.getByLabel('片段 1 转场', {exact: true}), 'crossfade')
  await page.getByLabel('片段 1 转场时长', {exact: true}).fill('1')
  await page.getByRole('button', {name: '添加画面叠层', exact: true}).click()
  await page.getByLabel('叠层 1 不透明度', {exact: true}).fill('0.5')
  await page.getByLabel('叠层 1 成片起点（秒）', {exact: true}).fill(String(expectedDuration - .1))
  await expect(page.getByRole('button', {name: '保存时间轴', exact: true})).toBeDisabled()
  await expect(page.locator('.media-studio').getByRole('alert')).toContainText('画面叠层无效')
  await page.getByLabel('叠层 1 成片起点（秒）', {exact: true}).fill('0')
  await page.getByRole('button', {name: '保存时间轴', exact: true}).click()
  await expect(page.getByRole('button', {name: '保存时间轴', exact: true})).toBeDisabled()
  const saved = await (await request.get(path + query)).json()
  expect(saved.timeline.clips[0]).toMatchObject({transition: 'crossfade', transition_duration: 1})
  expect(saved.timeline.video_overlays).toHaveLength(1)
  expect(saved.timeline.video_overlays[0]).toMatchObject({opacity: .5, preserve_source_audio: false, source_audio_volume: 0})
  await page.getByRole('button', {name: '合成并导出 MP4', exact: true}).click()
  const video = page.getByLabel('成片预览', {exact: true}).first()
  await expect(video).toBeVisible({timeout: 40000})
  await expect.poll(() => video.evaluate((node: HTMLVideoElement) => node.readyState)).toBeGreaterThanOrEqual(2)
  expect(await video.evaluate((node: HTMLVideoElement) => node.duration)).toBeCloseTo(expectedDuration, 1)
  await page.reload()
  await navigateTo(page, '/projects')
  await openProject(page, p.title, 'media')
  await expectControlValue(page.getByLabel('片段 1 转场', {exact: true}), 'crossfade')
  await expectControlValue(page.getByLabel('叠层 1 不透明度', {exact: true}), '0.5')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('durable rendering shows progress, cancellation and explicit resume without changing the consumer layout', async ({page,request},info)=>{
  const p=await project(request,`渲染恢复-${info.project.name}-${randomUUID()}`)
  const path=`${api}/projects/${p.id}/media`
  const demo=await request.post(path+'/demo'+query,{headers:{'Idempotency-Key':randomUUID()},data:{expected_version:1}})
  expect(demo.status()).toBe(200)
  const state=await demo.json()
  state.timeline.clips=state.timeline.clips.slice(0,2)
  for(const clip of state.timeline.clips)clip.duration=11
  const saved=await request.put(path+'/timeline'+query,{headers:{'Idempotency-Key':randomUUID()},data:{expected_version:state.version,timeline:state.timeline}})
  expect(saved.status()).toBe(200)
  await open(page,p.title)
  await page.getByRole('button',{name:'合成并导出 MP4',exact:true}).click()
  const exports=page.locator('.media-exports')
  await expect(exports.getByRole('status')).toContainText('已完成')
  await expect(exports.getByRole('status')).toContainText('合成会在后台继续')
  expect(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1)).toBe(false)
  await exports.screenshot({path:`/tmp/studio-render-progress-${info.project.name}.png`})
  await exports.getByRole('button',{name:'取消成片合成',exact:true}).click()
  await expect(exports.getByRole('heading')).toContainText('已取消')
  const final=await (await request.get(path+query)).json()
  expect(final.exports).toHaveLength(1)
  expect(final.exports[0]).toMatchObject({status:'cancelled',file_url:'',render_version:1,resume_available:true})
  await expect(exports.getByLabel('成片预览',{exact:true})).toHaveCount(0)
  await exports.getByRole('button',{name:'继续合成',exact:true}).click()
  await expect(exports.getByLabel('成片预览',{exact:true})).toBeVisible({timeout:20000})
  const resumed=await (await request.get(path+query)).json()
  expect(resumed.exports).toHaveLength(1)
  expect(resumed.exports[0]).toMatchObject({id:final.exports[0].id,status:'succeeded'})

})
