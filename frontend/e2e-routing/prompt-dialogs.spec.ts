import {expect, test, type Page} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

const version = {
  id: 'ver-1', template_id: 'tpl-1', space_id: 'space-1', number: 1, sha256: 'a'.repeat(64),
  created_by: 'user-1', created_at: '2026-11-01T00:00:00Z', notes: 'synthetic note',
  syntax: 'variables_v1' as const, body: '森林中的{{subject}}，晨光柔和。',
  defaults: {kind: 'poster', ratio: '1:1', style: 'general'},
  variables: [{name: 'subject', label: '主体', type: 'text' as const, required: true, max_length: 40}],
}
const item = {
  template: {
    id: 'tpl-1', space_id: 'space-1', owner_id: 'user-1', status: 'active', head_version_id: 'ver-1',
    row_version: 1, created_at: '2026-11-01T00:00:00Z', updated_at: '2026-11-01T00:00:00Z',
    name: '晨光森林', description: '合成配方', category: '场景', tags: ['森林'], visibility: 'private',
  },
  version, favorite: false,
}

// The shared fake upstream does not serve prompt endpoints, so these cases stub
// just those reads. Everything else -- routing, session, shell -- stays real.
async function stubPrompts(page: Page) {
  await page.route('**/api/v1/prompt-templates?**', route => route.fulfill({json: {items: [item], next_cursor: ''}}))
  await page.route('**/api/v1/prompt-templates/*/versions?**', route => route.fulfill({json: {items: [version], next_cursor: ''}}))
  await page.route('**/api/v1/prompt-templates/*/preview?**', route => route.fulfill({json: {prompt: '森林中的旅人，晨光柔和。', defaults: version.defaults}}))
}

async function openLibrary(page: Page) {
  await stubPrompts(page)
  page.on('dialog', dialog => {void dialog.accept()})
  await page.goto('/studio')
  await page.getByRole('button', {name: '我的提示词', exact: true}).click()
  await expect(page.locator('.prompt-library-card').first()).toBeVisible()
}

async function assertShell(page: Page, name: string | RegExp, closeLabel: string, trigger: ReturnType<Page['getByRole']>) {
  const dialog = page.getByRole('dialog', {name})
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('button', {name: closeLabel, exact: true})).toBeVisible()
  expect(await dialog.evaluate(node => node.contains(document.activeElement))).toBe(true)
  for (const key of ['Tab', 'Shift+Tab']) {
    await page.keyboard.press(key)
    expect(await dialog.evaluate(node => node.contains(document.activeElement)), key).toBe(true)
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(trigger).toBeFocused()
}

test('prompt version history uses the shared dialog shell', async ({page}, info) => {
  await openLibrary(page)
  const trigger = page.locator('.prompt-library-card').first().getByRole('button', {name: '版本记录', exact: true})
  await trigger.click()
  await assertShell(page, /版本记录/, '关闭版本记录', trigger)
  await page.screenshot({path: `test-results/dialog-prompt-history-${info.project.name}.png`})
})

test('prompt variable preview uses the shared dialog shell', async ({page}, info) => {
  await openLibrary(page)
  const trigger = page.locator('.prompt-library-card').first().getByRole('button', {name: '选用提示词', exact: true})
  await trigger.click()
  await assertShell(page, '填写提示词变量', '关闭变量预览', trigger)
  await page.screenshot({path: `test-results/dialog-prompt-use-${info.project.name}.png`})
})