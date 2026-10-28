import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'

for (const locale of ['zh-CN', 'en']) test(`synthetic TikTok consent and unknown-result controls (${locale})`, async ({page, request}, info) => {
  const result = await request.post('http://127.0.0.1:8081/api/v1/projects?space_id=personal-demo', {headers: {'Idempotency-Key': randomUUID()}, data: {title: `Social contract ${info.project.name} ${randomUUID()}`, brief: 'Synthetic publishing UI contract, no supplier calls or actual external publication.'}})
  expect(result.status()).toBe(201)
  const project = await result.json() as {id: string; title: string}
  await page.addInitScript(value => localStorage.setItem('studio.locale', value), locale)
  const jobs: Record<string, unknown>[] = []
  const posts: Record<string, unknown>[] = []
  const view = () => ({version: 1 + jobs.length, platforms: [{id: 'tiktok', direct_status: 'available', handoff_status: 'available', connection_status: 'configured'}, {id: 'douyin', direct_status: 'approval_required', handoff_status: 'approval_required', connection_status: 'not_configured'}, {id: 'xiaohongshu', direct_status: 'unavailable', handoff_status: 'access_paused', connection_status: 'not_configured'}], connections: [{id: 'synthetic-account', platform: 'tiktok', status: 'connected', display_name: 'Synthetic creator'}], jobs})
  await page.route(`**/api/v1/projects/${project.id}/media?*`, async route => {
    const response = await route.fetch()
    const body = await response.json()
    body.assets = [{id: 'synthetic-video', kind: 'video', mime_type: 'video/mp4', name: 'Synthetic video metadata', duration: 1, source: 'fixture', file_url: `/api/v1/projects/${project.id}/media/assets/synthetic-video/file`}]
    await route.fulfill({response, json: body})
  })
  await page.route(`**/api/v1/projects/${project.id}/social**`, async route => {
    const url = new URL(route.request().url())
    if (url.pathname.endsWith('/creator')) return route.fulfill({json: {creator_username: 'synthetic', creator_nickname: 'Synthetic creator', privacy_level_options: ['SELF_ONLY', 'PUBLIC_TO_EVERYONE'], comment_disabled: true, duet_disabled: false, stitch_disabled: true, max_video_post_duration_sec: 60}})
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON() as Record<string, unknown>
      posts.push(body)
      if (url.pathname.endsWith('/jobs')) {
        jobs.push({id: 'synthetic-job', platform: 'tiktok', mode: body.mode, status: 'prepared', caption: body.caption, connection_id: 'synthetic-account', intent: body, created_at: '2026-10-28T00:00:00Z'})
        return route.fulfill({json: view()})
      }
      if (url.pathname.endsWith('/dispatch')) return route.abort('failed')
    }
    return route.fulfill({json: view()})
  })
  await page.goto('/')
  await page.getByRole('button', {name: locale === 'en' ? 'Story studio' : '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: project.title}).click()
  const panel = page.locator('.social-tiktok')
  await panel.locator('summary').click()
  await panel.getByRole('combobox', {name: locale === 'en' ? 'Target TikTok account' : '目标 TikTok 账号', exact: true}).selectOption('synthetic-account')
  await panel.getByRole('button', {name: locale === 'en' ? 'Load account publishing permissions' : '读取账号发布权限', exact: true}).click()
  const privacy = panel.getByRole('combobox', {name: locale === 'en' ? 'Who can watch' : '谁可以观看', exact: true})
  await expect(privacy).toHaveValue('')
  await expect(panel.getByLabel(locale === 'en' ? 'Allow comments' : '允许评论', {exact: true})).toBeDisabled()
  await expect(panel.getByLabel(locale === 'en' ? 'Allow Duet' : '允许合拍', {exact: true})).not.toBeChecked()
  expect(posts).toHaveLength(0)
  await panel.getByRole('combobox', {name: locale === 'en' ? 'MP4 to publish' : '待发布 MP4', exact: true}).selectOption('asset:synthetic-video')
  await panel.getByRole('combobox', {name: locale === 'en' ? 'Publishing mode' : '发布模式', exact: true}).selectOption('direct')
  await privacy.selectOption('SELF_ONLY')
  await panel.getByLabel(locale === 'en' ? 'Post caption' : '发布文案', {exact: true}).fill('Synthetic caption')
  const save = panel.getByRole('button', {name: locale === 'en' ? 'Save publishing record for review' : '保存待发送发布记录', exact: true})
  await expect(save).toBeDisabled()
  await panel.getByLabel(locale === 'en' ? 'I have the necessary rights to this video and audio and agree to TikTok’s Music Usage Confirmation.' : '我拥有此视频及音频的必要权利，并同意 TikTok 音乐使用确认。', {exact: true}).check()
  await panel.getByLabel(locale === 'en' ? 'I have previewed the video and confirmed the target account, caption, privacy, interactions and commercial disclosures.' : '我已预览视频并确认目标账号、文案、隐私、互动与商业披露设置。', {exact: true}).check()
  await page.locator('.social-center').screenshot({path: `/tmp/iteration10-social-consent-${locale}-${info.project.name}.png`})
  await save.click()
  await expect(page.locator('.social-job-actions')).toBeVisible()
  expect(posts[0]).toMatchObject({privacy_level: 'SELF_ONLY', allow_comment: false, allow_duet: false, allow_stitch: false, rights_confirmed: true, confirmed: true, caption: 'Synthetic caption'})
  expect(posts).toHaveLength(1)
  const job = page.locator('.social-job-actions')
  const send = job.getByRole('button', {name: locale === 'en' ? 'Send to TikTok' : '发送到 TikTok', exact: true})
  await expect(send).toBeDisabled()
  await job.getByLabel(locale === 'en' ? 'Confirm sending this record to the target account (uploads media)' : '确认发送此记录到目标账号（会上传素材）', {exact: true}).check()
  await send.click()
  await expect(send).toBeDisabled()
  await page.getByRole('button', {name: locale === 'en' ? 'Refresh media and publishing records' : '刷新素材与发布记录', exact: true}).click()
  await expect(send).toBeDisabled()
  expect(posts).toHaveLength(2)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

for (const locale of ['zh-CN', 'en']) test(`synthetic OAuth callback scrubs URL and exchanges only once (${locale})`, async ({page}) => {
  await page.addInitScript(value => {
    localStorage.setItem('studio.locale', value)
    sessionStorage.setItem('studio.social.oauth', JSON.stringify({projectId: 'synthetic-project', space: 'personal-demo'}))
  }, locale)
  let exchanges = 0
  await page.route('**/api/v1/projects/synthetic-project/social/connections/complete?*', async route => {
    exchanges++
    expect(route.request().postDataJSON()).toEqual({code: 'synthetic-code', state: 'synthetic-state'})
    await route.abort('failed')
  })
  await page.goto('/social-callback?code=synthetic-code&state=synthetic-state')
  await expect(page).toHaveURL(/\/social-callback$/)
  expect(exchanges).toBe(0)
  const confirm = page.getByRole('button', {name: locale === 'en' ? 'Confirm account connection' : '确认完成账号连接', exact: true})
  await confirm.click()
  await expect(confirm).toBeDisabled()
  await expect(page.getByRole('alert')).toBeVisible()
  expect(exchanges).toBe(1)
  expect(await page.evaluate(() => location.search)).toBe('')
})

for (const locale of ['zh-CN', 'en']) test(`Xiaohongshu manual copy is explicit and never enters export payload (${locale})`, async ({page, request}, info) => {
  const response = await request.post('http://127.0.0.1:8081/api/v1/projects?space_id=personal-demo', {headers: {'Idempotency-Key': randomUUID()}, data: {title: `Manual caption ${info.project.name} ${randomUUID()}`, brief: 'Synthetic manual clipboard acceptance, no social publication.'}})
  expect(response.status()).toBe(201)
  const project = await response.json() as {id: string; title: string}
  await page.addInitScript(value => {
    localStorage.setItem('studio.locale', value)
    const fixture = window as unknown as {clipboardWrites: {text: string; userGesture: boolean}[]; clipboardReject: boolean}
    fixture.clipboardWrites = []; fixture.clipboardReject = false
    Object.defineProperty(navigator, 'clipboard', {configurable: true, value: {writeText: async (text: string) => {
      if (fixture.clipboardReject) throw new DOMException('Synthetic permission denied', 'NotAllowedError')
      fixture.clipboardWrites.push({text, userGesture: navigator.userActivation.isActive})
    }}})
  }, locale)
  const posts: Record<string, unknown>[] = []
  const view = {version: 1, platforms: [{id: 'xiaohongshu', direct_status: 'unavailable', handoff_status: 'access_paused', connection_status: 'not_configured'}], connections: [], jobs: []}
  await page.route(`**/api/v1/projects/${project.id}/social**`, async route => {
    if (route.request().method() === 'POST') posts.push(route.request().postDataJSON())
    await route.fulfill({json: view})
  })
  await page.route(`**/api/v1/projects/${project.id}/media?*`, async route => {
    const response = await route.fetch(), body = await response.json()
    body.assets = [{id: 'synthetic-image', kind: 'image', mime_type: 'image/png', name: 'Synthetic image', duration: 0, source: 'fixture', file_url: `/api/v1/projects/${project.id}/media/assets/synthetic-image/file`}]
    await route.fulfill({response, json: body})
  })
  await page.goto('/')
  await page.getByRole('button', {name: locale === 'en' ? 'Story studio' : '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: project.title}).click()
  const center = page.locator('.social-center')
  await center.getByRole('combobox', {name: locale === 'en' ? 'Target platform' : '目标平台', exact: true}).selectOption('xiaohongshu')
  const caption = center.getByRole('textbox', {name: locale === 'en' ? 'Xiaohongshu manual caption (this page only)' : '小红书手动发布文案（仅本页）', exact: true})
  await expect(caption).toHaveAttribute('maxlength', '2200')
  await caption.fill('x'.repeat(2300))
  await expect(caption).toHaveValue('x'.repeat(2200))
  const text = 'Synthetic manual caption 手动文案'
  await caption.fill(text)
  expect(await page.evaluate(() => (window as unknown as {clipboardWrites: unknown[]}).clipboardWrites)).toEqual([])
  expect(posts).toHaveLength(0)
  await center.getByRole('button', {name: locale === 'en' ? 'Copy caption to clipboard' : '复制文案到剪贴板', exact: true}).click()
  await expect(center.locator('.social-manual-caption').getByRole('status')).toContainText(locale === 'en' ? 'Caption copied.' : '文案已复制')
  expect(await page.evaluate(() => (window as unknown as {clipboardWrites: unknown[]}).clipboardWrites)).toEqual([{text, userGesture: true}])
  expect(posts).toHaveLength(0)
  await center.screenshot({path: `/tmp/iteration10-xhs-copy-${locale}-${info.project.name}.png`})
  await page.evaluate(() => {(window as unknown as {clipboardReject: boolean}).clipboardReject = true})
  await center.getByRole('button', {name: locale === 'en' ? 'Copy caption to clipboard' : '复制文案到剪贴板', exact: true}).click()
  await expect(center.locator('.social-manual-caption').getByRole('alert')).toContainText(locale === 'en' ? 'Clipboard access failed.' : '无法访问剪贴板')
  await center.getByRole('combobox', {name: locale === 'en' ? 'Media to deliver' : '待交付素材', exact: true}).selectOption('asset:synthetic-image')
  await center.getByLabel(locale === 'en' ? 'I confirm the target platform and media, and understand this only prepares a download and does not publish automatically.' : '我确认目标平台与素材，并理解此操作只准备下载，不会自动发布。', {exact: true}).check()
  await center.getByRole('button', {name: locale === 'en' ? 'Prepare download for manual publishing' : '准备下载与手动发布', exact: true}).click()
  await expect.poll(() => posts.length).toBe(1)
  expect(posts[0]).toMatchObject({platform: 'xiaohongshu', mode: 'export', title: '', caption: '', confirmed: true})
  expect(JSON.stringify(posts)).not.toContain(text)
  expect(await page.evaluate(value => Object.values(localStorage).some(item => String(item).includes(value)), text)).toBe(false)
  await page.reload()
  await page.getByRole('button', {name: locale === 'en' ? 'Story studio' : '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: project.title}).click()
  await center.getByRole('combobox', {name: locale === 'en' ? 'Target platform' : '目标平台', exact: true}).selectOption('xiaohongshu')
  await expect(caption).toHaveValue('')
})
