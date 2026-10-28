import { test, expect } from '@playwright/test'

test('avatar consent, unknown-result replay and license gate', async ({page, request}) => {
  await request.post('http://127.0.0.1:5189/__test__/reset', {data: {}})
  const people: Record<string, unknown>[] = []
  const jobs: Record<string, unknown>[] = []
  const submissions: {key: string | undefined; body: string | null}[] = []
  let ready = false
  let failOnce = true
  await page.route('**/api/v1/creative-media-capabilities', route => route.fulfill({json: {digital_human_photo: {provider: 'synthetic-avatar', model: 'test-v1', ready, message: ready ? 'Synthetic configured engine' : '部署方尚未确认数字人引擎使用许可'}}}))
  await page.route('**/api/v1/projects/story-1/media?*', route => route.fulfill({json: {assets: [{id: 'photo', kind: 'image', name: 'Synthetic photo', duration: 0}, {id: 'audio', kind: 'audio', name: 'Synthetic voice', duration: 3, file_url: '/synthetic.wav'}]}}))
  await page.route('**/api/v1/projects/story-1/digital-humans?*', async route => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      expect(body.likeness_confirmed).toBe(true)
      expect(body.single_person_confirmed).toBe(true)
      const person = {...body, id: 'person', route: 'photo'}
      people.push(person)
      await route.fulfill({status: 201, json: person})
    } else await route.fulfill({json: people})
  })
  await page.route('**/api/v1/projects/story-1/creative-jobs?*', async route => {
    if (route.request().method() !== 'POST') {await route.fulfill({json: jobs}); return}
    submissions.push({key: route.request().headers()['idempotency-key'], body: route.request().postData()})
    if (failOnce) {failOnce = false; await route.fulfill({status: 503, json: {error: {message: 'Synthetic unknown result'}}}); return}
    const body = route.request().postDataJSON()
    expect(body.kind).toBe('digital_human')
    expect(body.voice_confirmed).toBe(true)
    expect(body.voice_consent_reference).toBe('voice-consent-001')
    const job = {...body, id: 'job-avatar', status: 'queued'}
    jobs.push(job); await route.fulfill({status: 202, json: job})
  })
  await page.goto('/projects/story-1/avatar')
  await expect(page.getByRole('heading', {name: '数字人口播', exact: true})).toBeVisible()
  await page.getByLabel('形象名称', {exact: true}).fill('测试形象')
  await page.getByRole('combobox', {name: '形象照片或视频', exact: true}).selectOption('photo')
  await page.getByLabel('形象授权依据', {exact: true}).fill('likeness-consent-001')
  await expect(page.getByRole('button', {name: '保存授权形象'})).toBeDisabled()
  await page.getByLabel('我确认有权使用此人物形象生成口播视频').check()
  await page.getByLabel('我已检查素材为单人、面部清晰且无遮挡').check()
  await page.getByRole('button', {name: '保存授权形象'}).click()
  await expect(page.getByRole('combobox', {name: '已授权形象', exact: true})).toHaveValue('person')
  await expect(page.getByText('部署方尚未确认数字人引擎使用许可')).toBeVisible()
  await page.getByRole('combobox', {name: '选择口播音频', exact: true}).selectOption('audio')
  await page.getByLabel('声音授权依据', {exact: true}).fill('voice-consent-001')
  await page.getByLabel('我确认有权使用所选配音制作此口播').check()
  await expect(page.getByRole('button', {name: '生成数字人口播', exact: true})).toBeDisabled()
  ready = true
  await expect(page.getByRole('button', {name: '生成数字人口播', exact: true})).toBeEnabled({timeout: 8000})
  await page.getByRole('button', {name: '生成数字人口播', exact: true}).click()
  await expect(page.getByRole('button', {name: '重试同一请求'})).toBeVisible()
  await expect(page.getByLabel('声音授权依据', {exact: true})).toBeDisabled()
  await page.getByRole('button', {name: '重试同一请求'}).click()
  await expect(page.getByRole('button', {name: '项目任务处理中…'})).toBeDisabled()
  expect(submissions).toHaveLength(2)
  expect(submissions[1]).toEqual(submissions[0])
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/digital-human-${test.info().project.name}.png`, fullPage: true})
})
