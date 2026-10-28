import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'
// Synthetic 64x64 FFmpeg orange JPEG; no private image or external model output.
const image = Buffer.from('/9j/4AAQSkZJRgABAgAAAQABAAD//gAQTGF2YzYxLjE5LjEwMQD/2wBDAAgEBAQEBAUFBQUFBQYGBgYGBgYGBgYGBgYHBwcICAgHBwcGBgcHCAgICAkJCQgICAgJCQoKCgwMCwsODg4RERT/xABNAAEBAAAAAAAAAAAAAAAAAAAABgEBAQEAAAAAAAAAAAAAAAAAAAYHEAEAAAAAAAAAAAAAAAAAAAAAEQEAAAAAAAAAAAAAAAAAAAAA/8AAEQgAQABAAwEiAAIRAAMRAP/aAAwDAQACEQMRAD8ArQGOroAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB/9k=', 'base64')
for (const locale of ['zh-CN', 'en']) test(`TikTok photo intent, normalization receipt and refresh version (${locale})`, async ({page, request}, info) => {
  const created = await request.post('http://127.0.0.1:8081/api/v1/projects?space_id=personal-demo', {headers: {'Idempotency-Key': randomUUID()}, data: {title: `Photo consent ${info.project.name} ${randomUUID()}`, brief: 'Synthetic photo publishing contract. No actual OAuth or platform posting.'}})
  expect(created.status()).toBe(201)
  const project = await created.json() as {id: string; title: string}
  await page.addInitScript(value => localStorage.setItem('studio.locale', value), locale)
  const prefix = `/api/v1/projects/${project.id}`
  const asset = (id: string, name: string, mime_type = 'image/jpeg') => ({id, name, mime_type, kind: 'image', duration: 0, source: 'synthetic', file_url: `${prefix}/media/assets/${id}/file`})
  const assets = [asset('poster-png', 'Synthetic poster PNG', 'image/png'), asset('photo-second', 'Synthetic second JPEG')]
  let version = 1
  const jobs: Record<string, unknown>[] = []
  const posts: {path: string; body: Record<string, unknown>}[] = []
  const view = () => ({version, platforms: [{id: 'tiktok', direct_status: 'available', handoff_status: 'available', photo_status: 'available', connection_status: 'configured'}], connections: [{id: 'synthetic-account', platform: 'tiktok', status: 'connected', display_name: 'Synthetic photo creator', expires_at: '2026-10-28T00:00:00Z'}], jobs})
  await page.route(`**${prefix}/media/assets/*/file*`, route => route.fulfill({contentType: 'image/jpeg', body: image}))
  await page.route(`**${prefix}/media?*`, async route => {
    const response = await route.fetch(), body = await response.json()
    await route.fulfill({response, json: {...body, version, assets}})
  })
  await page.route(`**${prefix}/social**`, async route => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON() as Record<string, unknown>
      posts.push({path, body})
      if (path.endsWith('/photos/normalize')) {
        expect(body).toMatchObject({asset_id: 'poster-png', confirmed: true})
        assets.push(asset('unrelated-jpeg', 'Unrelated concurrent JPEG'), asset('normalized-poster', 'Normalized poster JPEG'))
        version++
        return route.fulfill({json: {version, assets, exports: [], normalized_asset_id: 'normalized-poster'}})
      }
      if (path.endsWith('/creator')) {
        version++
        return route.fulfill({json: {version, creator_username: 'synthetic', creator_nickname: 'Synthetic photo creator', privacy_level_options: ['SELF_ONLY', 'PUBLIC_TO_EVERYONE'], comment_disabled: false, duet_disabled: false, stitch_disabled: false, max_video_post_duration_sec: 60}})
      }
      if (path.endsWith('/jobs')) {
        expect(body.expected_version).toBe(version)
        jobs.push({id: `photo-job-${jobs.length}`, platform: 'tiktok', mode: body.mode, status: 'prepared', title: body.title, caption: body.caption, connection_id: body.connection_id, intent: body, created_at: '2026-10-28T00:00:00Z'})
        version++
      } else if (path.endsWith('/dispatch')) {
        Object.assign(jobs[0], {status: 'processing', photo_expires_at: '2099-01-01T00:00:00Z', publish_id: 'synthetic-publish-id'})
        version++
      } else if (path.endsWith('/revoke-photo-access')) {
        expect(body.confirmed).toBe(true)
        Object.assign(jobs[0], {photo_access_revoked: true})
        version++
      }
    }
    return route.fulfill({json: view()})
  })
  await page.goto('/')
  await page.getByRole('button', {name: locale === 'en' ? 'Story studio' : '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: project.title}).click()
  const panel = page.locator('.social-tiktok')
  await panel.locator(':scope > summary').click()
  await panel.locator('.social-local-photos > summary').click()
  const editor = panel.locator('.tiktok-photos')
  await editor.getByRole('combobox', {name: locale === 'en' ? 'Convert an image or poster to publishing JPEG' : '转换图片或海报为发布用 JPEG', exact: true}).selectOption('poster-png')
  const normalize = editor.getByRole('button', {name: locale === 'en' ? 'Convert and preview new JPEG' : '转换并预览新 JPEG', exact: true})
  await expect(normalize).toBeDisabled()
  await editor.getByRole('checkbox').check()
  await normalize.click()
  await expect(editor.locator('.tiktok-photo-list')).toContainText('Normalized poster JPEG')
  await expect(editor.locator('.tiktok-photo-list')).not.toContainText('Unrelated concurrent JPEG')
  expect(posts).toHaveLength(1)
  await editor.getByRole('combobox', {name: locale === 'en' ? 'Add a photo to publish' : '添加待发布图片', exact: true}).selectOption('photo-second')
  await editor.getByRole('radio', {name: locale === 'en' ? 'Use photo 2 as cover' : '设为图片 2 封面', exact: true}).check()
  await editor.getByRole('button', {name: locale === 'en' ? 'Move photo 2 earlier' : '图片 2 前移', exact: true}).click()
  await expect(editor.getByRole('radio', {name: locale === 'en' ? 'Use photo 1 as cover' : '设为图片 1 封面', exact: true})).toBeChecked()
  await expect(editor.locator('.tiktok-photo-list li').first()).toContainText('Synthetic second JPEG')
  await panel.getByRole('combobox', {name: locale === 'en' ? 'Target TikTok account' : '目标 TikTok 账号', exact: true}).selectOption('synthetic-account')
  await panel.getByRole('button', {name: locale === 'en' ? 'Load account publishing permissions' : '读取账号发布权限', exact: true}).click()
  await expect(panel.getByRole('combobox', {name: locale === 'en' ? 'TikTok content type' : 'TikTok 内容类型', exact: true})).toHaveValue('PHOTO')
  await panel.getByRole('combobox', {name: locale === 'en' ? 'Publishing mode' : '发布模式', exact: true}).selectOption('direct')
  const title = panel.getByRole('textbox', {name: locale === 'en' ? 'Photo title (up to 90 characters)' : '图片标题（最多 90 字符）', exact: true})
  const description = panel.getByRole('textbox', {name: locale === 'en' ? 'Photo description (up to 4000 characters)' : '图片描述（最多 4000 字符）', exact: true})
  await title.fill('x'.repeat(100)); await expect(title).toHaveValue('x'.repeat(90)); await title.fill('Synthetic photo title')
  await description.fill('x'.repeat(4100)); await expect(description).toHaveValue('x'.repeat(4000)); await description.fill('Synthetic photo description')
  const privacy = panel.getByRole('combobox', {name: locale === 'en' ? 'Who can watch' : '谁可以观看', exact: true})
  await expect(privacy).toHaveValue(''); await privacy.selectOption('SELF_ONLY')
  await expect(panel.getByRole('checkbox', {name: locale === 'en' ? 'Allow Duet' : '允许合拍', exact: true})).toBeDisabled()
  await expect(panel.getByRole('checkbox', {name: locale === 'en' ? 'Allow Stitch' : '允许拼接', exact: true})).toBeDisabled()
  await panel.getByRole('checkbox', {name: locale === 'en' ? 'This content was generated or modified by AI' : '此内容由 AI 生成或修改', exact: true}).check()
  await panel.getByRole('checkbox', {name: locale === 'en' ? 'I have the necessary rights to the selected photos and agree to TikTok’s Music Usage Confirmation.' : '我拥有所选图片的必要权利，并同意 TikTok 音乐使用确认。', exact: true}).check()
  const consent = panel.getByRole('checkbox', {name: locale === 'en' ? 'I have previewed the photo order and cover and confirmed the account, title, description, privacy, interactions and disclosures.' : '我已预览图片顺序与封面，并确认目标账号、标题、描述、隐私、互动与披露设置。', exact: true})
  const save = panel.getByRole('button', {name: locale === 'en' ? 'Save publishing record for review' : '保存待发送发布记录', exact: true})
  await consent.check(); await expect(save).toBeDisabled()
  await panel.getByRole('checkbox', {name: locale === 'en' ? 'I consent to temporary public links for TikTok to download the selected photos.' : '我同意为所选图片创建短期公开链接供 TikTok 下载。', exact: true}).check()
  await consent.check()
  await panel.screenshot({path: `/tmp/iteration10-followup-photo-${locale}-${info.project.name}.png`})
  await save.click()
  await expect(page.locator('.social-job-actions')).toBeVisible()
  expect(posts.find(item => item.path.endsWith('/jobs'))?.body).toMatchObject({media_type: 'PHOTO', photo_asset_ids: ['photo-second', 'normalized-poster'], photo_cover_index: 0, auto_add_music: false, public_media_confirmed: true, title: 'Synthetic photo title', caption: 'Synthetic photo description', is_aigc: true, allow_duet: false, allow_stitch: false, privacy_level: 'SELF_ONLY'})
  const job = page.locator('.social-job-actions').first()
  await expect(job.locator('.tiktok-photo-list li').first()).toContainText('Synthetic second JPEG')
  await job.getByRole('checkbox', {name: locale === 'en' ? 'Confirm sending this record to the target account (uploads media)' : '确认发送此记录到目标账号（会上传素材）', exact: true}).check()
  await job.getByRole('button', {name: locale === 'en' ? 'Send to TikTok' : '发送到 TikTok', exact: true}).click()
  const revoke = job.getByRole('button', {name: locale === 'en' ? 'Revoke public photo links for this record' : '撤销此记录的公开图片链接', exact: true})
  await expect(revoke).toBeDisabled()
  await job.getByRole('checkbox', {name: locale === 'en' ? 'I understand revocation only blocks future photo downloads; it cannot recall existing copies or published content.' : '我理解撤销仅阻止后续图片下载，不能撤回已有副本或已发布内容。', exact: true}).check()
  await revoke.click()
  await expect(job).toContainText(locale === 'en' ? 'Public photo links are revoked.' : '公开图片链接已撤销')
  await panel.getByRole('combobox', {name: locale === 'en' ? 'Publishing mode' : '发布模式', exact: true}).selectOption('draft')
  await expect(title).toBeEnabled(); await expect(description).toBeEnabled(); await expect(privacy).toBeDisabled()
  await consent.check(); await save.click()
  await expect.poll(() => jobs.length).toBe(2)
  expect(posts.filter(item => item.path.endsWith('/jobs'))[1].body).toMatchObject({media_type: 'PHOTO', mode: 'draft', is_aigc: true, title: 'Synthetic photo title', caption: 'Synthetic photo description', privacy_level: '', auto_add_music: false, brand_content: false, brand_organic: false})
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

for (const locale of ['zh-CN', 'en']) test(`TikTok unknown token refresh permits explicit reconnect or local revoke only (${locale})`, async ({page, request}, info) => {
  const result = await request.post('http://127.0.0.1:8081/api/v1/projects?space_id=personal-demo', {headers: {'Idempotency-Key': randomUUID()}, data: {title: `Refresh controls ${info.project.name} ${randomUUID()}`, brief: 'Synthetic uncertain refresh contract, no real account or platform call.'}})
  expect(result.status()).toBe(201)
  const project = await result.json() as {id: string; title: string}
  await page.addInitScript(value => localStorage.setItem('studio.locale', value), locale)
  let status = 'connected', version = 1
  const mutations: string[] = []
  await page.route(`**/api/v1/projects/${project.id}/social**`, async route => {
    const path = new URL(route.request().url()).pathname
    if (route.request().method() === 'POST') {
      mutations.push(path)
      if (path.endsWith('/creator')) {status = 'refresh_unknown'; version++; return route.abort('failed')}
      if (path.endsWith('/revoke')) {status = 'revoked_remote_unconfirmed'; version++}
    }
    return route.fulfill({json: {version, platforms: [{id: 'tiktok', direct_status: 'available', handoff_status: 'available', photo_status: 'available', connection_status: 'configured'}], connections: [{id: 'account', platform: 'tiktok', status, display_name: 'Synthetic expiring account'}], jobs: []}})
  })
  await page.goto('/')
  await page.getByRole('button', {name: locale === 'en' ? 'Story studio' : '短剧工作台', exact: true}).click()
  await page.locator('.drama-project-list button').filter({hasText: project.title}).click()
  const panel = page.locator('.social-tiktok')
  await panel.locator(':scope > summary').click()
  await panel.getByRole('combobox', {name: locale === 'en' ? 'Target TikTok account' : '目标 TikTok 账号', exact: true}).selectOption('account')
  const permissions = panel.getByRole('button', {name: locale === 'en' ? 'Load account publishing permissions' : '读取账号发布权限', exact: true})
  await permissions.click()
  await expect(panel.locator('.social-account-states')).toContainText(locale === 'en' ? 'Refresh result unknown; reconnect explicitly' : '刷新结果未知，请主动重新授权')
  await expect(permissions).toBeDisabled()
  await expect(panel.getByRole('button', {name: locale === 'en' ? 'Connect an account through TikTok authorization' : '前往 TikTok 授权连接账号', exact: true})).toBeEnabled()
  expect(mutations).toHaveLength(1)
  await page.getByRole('button', {name: locale === 'en' ? 'Refresh media and publishing records' : '刷新素材与发布记录', exact: true}).click()
  await expect(permissions).toBeDisabled()
  expect(mutations).toHaveLength(1)
  await panel.getByRole('button', {name: locale === 'en' ? 'Revoke this account connection' : '撤销此账号连接', exact: true}).click()
  await expect(panel.locator('.social-account-states')).toContainText(locale === 'en' ? 'Local connection revoked; platform revocation unconfirmed' : '本地连接已撤销，平台撤销结果未确认')
  expect(mutations).toHaveLength(2)
  expect(mutations.every(path => path.endsWith('/creator') || path.endsWith('/revoke'))).toBe(true)
})
