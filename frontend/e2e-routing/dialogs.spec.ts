import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// The task detail overlay used to hand-roll showModal, a focus trap and focus
// restoration. It now uses the shared dialog shell, so the shell contract is
// asserted here against the fake upstream on both sides of the 760px breakpoint.
test('task details use the shared dialog shell, trap focus and restore it on close', async ({page}, info) => {
  await page.goto('/studio')
  const trigger = page.getByRole('button', {name: '查看详情', exact: true}).first()
  await expect(trigger).toBeVisible()
  await trigger.click()
  const dialog = page.getByRole('dialog', {name: '任务详情', exact: true})
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('button', {name: '关闭详情', exact: true})).toBeVisible()
  expect(await dialog.evaluate(node => node.contains(document.activeElement))).toBe(true)
  for (const key of ['Tab', 'Tab', 'Shift+Tab']) {
    await page.keyboard.press(key)
    expect(await dialog.evaluate(node => node.contains(document.activeElement)), key).toBe(true)
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/dialog-detail-${info.project.name}.png`})
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(trigger).toBeFocused()
})