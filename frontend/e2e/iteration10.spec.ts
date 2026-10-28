import {randomUUID} from 'node:crypto'
import {mkdirSync} from 'node:fs'
import {test, expect} from '@playwright/test'

const api = 'http://127.0.0.1:8081/api/v1'
const query = '?space_id=personal-demo'
for (const language of ['zh-CN', 'en'] as const) {
  test(`creative editor, poster and social export persist honestly (${language})`, async ({page, request}, info) => {
    test.setTimeout(90000)
    page.setDefaultTimeout(12000)
    const english = language === 'en'
    const label = (zh: string, en: string) => english ? en : zh
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.addInitScript(value => localStorage.setItem('studio.locale', value), language)
    const title = `Synthetic iteration10 ${language} ${info.project.name} ${randomUUID()}`
    const created = await request.post(`${api}/projects${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {title, brief: 'Synthetic layered poster and timeline. No real accounts, logos or external publication.'}})
    expect(created.status()).toBe(201)
    const project = await created.json()
    const mediaPath = `${api}/projects/${project.id}/media`
    const demo = await request.post(`${mediaPath}/demo${query}`, {headers: {'Idempotency-Key': randomUUID()}, data: {expected_version: 1}})
    expect(demo.status()).toBe(200)
    const media = await demo.json()
    const overlay = media.assets.filter((asset: {kind: string}) => asset.kind === 'image')[1]
    media.timeline.video_overlays = [{id:'synthetic-layer',asset_id:overlay.id,start:0,trim_start:0,duration:2,x:0,y:0,width:.25,height:.25,opacity:1,preserve_source_audio:false,source_audio_volume:0,keyframes:[{time:0,x:0,y:0,opacity:1},{time:2,x:.7,y:.7,opacity:.5}]}]
    const saved = await request.put(`${mediaPath}/timeline${query}`, {headers:{'Idempotency-Key':randomUUID()},data:{expected_version:media.version,timeline:media.timeline}})
    expect(saved.status()).toBe(200)
    async function openProject() {
      await page.getByRole('button',{name:label('短剧工作台','Story studio'),exact:true}).click()
      await page.locator('.drama-project-list button').filter({hasText:title}).click()
    }
    await page.goto('/')
    await openProject()
    const preview = page.locator('.timeline-preview')
    await expect(preview).toBeVisible()
    const canvas = preview.locator('canvas')
    await expect.poll(() => canvas.evaluate((node:HTMLCanvasElement) => {
      const bytes=node.getContext('2d')!.getImageData(0,0,node.width,node.height).data
      return Array.from(bytes).some((value,index)=>index%4!==3&&value>20)
    })).toBe(true)
    const sample = () => canvas.evaluate((node:HTMLCanvasElement) => Array.from(node.getContext('2d')!.getImageData(40,20,1,1).data).slice(0,3))
    await expect.poll(sample).toEqual([190,145,90])
    const before = await sample()
    await page.getByLabel(label('叠层 1 关键帧 2 横向位置（0–1）','Overlay 1 keyframe 2 Horizontal position (0–1)'),{exact:true}).fill('0.6')
    await page.getByLabel(label('精确播放头（秒）','Exact playhead (seconds)'),{exact:true}).fill('1')
    await expect.poll(sample).not.toEqual(before)
    await page.getByRole('button',{name:label('在播放头拆分片段 1','Split clip 1 at playhead'),exact:true}).click()
    await page.getByRole('button',{name:label('复制片段 1','Duplicate clip 1'),exact:true}).click()
    await page.getByRole('combobox',{name:label('片段 1 转场','Clip 1 transition'),exact:true}).selectOption('wipeleft')
    await page.getByRole('combobox',{name:label('输出清晰度','Output resolution'),exact:true}).selectOption('2160p')
    await page.getByRole('button',{name:label('保存时间轴','Save timeline'),exact:true}).click()
    await expect(page.getByRole('button',{name:label('保存时间轴','Save timeline'),exact:true})).toBeDisabled()
    const timeline = (await (await request.get(mediaPath+query)).json()).timeline
    expect(timeline.clips).toHaveLength(media.timeline.clips.length+2)
    expect(timeline.clips[0].transition).toBe('wipeleft')
    expect(timeline.output_resolution).toBe('2160p')
    expect(timeline.video_overlays[0].keyframes).toHaveLength(2)

    const poster=page.locator('.poster-editor')
    await poster.getByRole('button',{name:label('重载海报与素材','Reload poster and assets'),exact:true}).click()
    await poster.getByRole('button',{name:label('添加文字图层','Add text layer'),exact:true}).click()
    await poster.getByRole('textbox',{name:label('图层文字','Layer text'),exact:true}).fill('Synthetic poster — 合成海报')
    await poster.getByRole('button',{name:label('添加 Logo 图层','Add logo layer'),exact:true}).click()
    await poster.getByLabel(label('图层纵坐标','Layer Y'),{exact:true}).last().fill('250')
    await poster.getByRole('button',{name:label('保存海报','Save poster'),exact:true}).click()
    await expect(poster.getByRole('button',{name:label('导出海报 PNG','Export poster PNG'),exact:true})).toBeEnabled()
    await poster.getByRole('button',{name:label('导出海报 PNG','Export poster PNG'),exact:true}).click()
    const exported=poster.getByRole('link',{name:label('查看已导出海报','View exported poster'),exact:false}).first()
    await expect(exported).toBeVisible()
    const href=await exported.getAttribute('href')
    const png=await request.get(href!)
    expect(png.status()).toBe(200)
    const content=await png.body()
    expect(content.subarray(0,8).toString('hex')).toBe('89504e470d0a1a0a')
    expect(content.readUInt32BE(16)).toBe(1080)
    expect(content.readUInt32BE(20)).toBe(1080)

    const social=page.locator('.social-center')
    await social.getByRole('button',{name:label('刷新素材与发布记录','Refresh media and publishing records'),exact:true}).click()
    await social.getByLabel(label('目标平台','Target platform'),{exact:true}).selectOption('xiaohongshu')
    await social.getByLabel(label('待交付素材','Media to deliver'),{exact:true}).selectOption(`asset:${overlay.id}`)
    const prepare=social.getByRole('button',{name:label('准备下载与手动发布','Prepare download for manual publishing'),exact:true})
    await expect(prepare).toBeDisabled()
    await social.getByRole('checkbox',{name:label('我确认目标平台与素材，并理解此操作只准备下载，不会自动发布。','I confirm the target platform and media, and understand this only prepares a download and does not publish automatically.'),exact:true}).check()
    await prepare.click()
    await expect(social.getByText(label('素材已备好，尚未发布','Media prepared, not published'),{exact:false})).toBeVisible()
    await expect(social.getByRole('link',{name:label('下载交付素材','Download prepared media'),exact:true})).toBeVisible()
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
    mkdirSync('/tmp/studio-iteration10-screenshots',{recursive:true})
    for (const [name,locator] of [['timeline',preview],['poster',poster],['social',social]] as const) {
      await locator.screenshot({path:`/tmp/studio-iteration10-screenshots/${name}-${language}-${info.project.name}.png`})
    }
    await page.reload()
    await openProject()
    await expect(page.locator('.poster-editor').getByRole('textbox',{name:label('图层文字','Layer text'),exact:true})).toHaveValue('Synthetic poster — 合成海报')
    await expect(page.getByRole('combobox',{name:label('输出清晰度','Output resolution'),exact:true})).toHaveValue('2160p')
    await expect(page.locator('.social-center').getByRole('link',{name:label('下载交付素材','Download prepared media'),exact:true})).toBeVisible()
    expect(errors).toEqual([])
  })
}
