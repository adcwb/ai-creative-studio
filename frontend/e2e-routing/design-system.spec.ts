import {selectOption} from '../e2e/support/controls'
import {expect, test, type Locator} from '@playwright/test'
import {openNavigation, closeNavigation} from '../e2e/support/navigation'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

function contrast(a: string, b: string) {
  const luminance = (color: string) => {
    const channels = color.match(/[\d.]+/g)!.slice(0, 3).map(Number).map(value => {
      const n = value / 255
      return n <= .04045 ? n / 12.92 : ((n + .055) / 1.055) ** 2.4
    })
    return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722
  }
  const [low, high] = [luminance(a), luminance(b)].sort((x, y) => x - y)
  return (high + .05) / (low + .05)
}

async function colors(locator: Locator) {
  return locator.evaluate(element => {
    const style = getComputedStyle(element)
    let parent: Element | null = element
    let background = style.backgroundColor
    while (background === 'rgba(0, 0, 0, 0)' && parent?.parentElement) {
      parent = parent.parentElement
      background = getComputedStyle(parent).backgroundColor
    }
    return {text: style.color, background, border: style.borderTopColor, focus: style.outlineColor, outline: style.outlineStyle, font: style.fontSize}
  })
}

for (const theme of ['forest', 'paper', 'screening']) {
  test(`${theme} typography, contrast, keyboard focus and control boundaries`, async ({page}, info) => {
    await page.addInitScript(theme => localStorage.setItem('studio.preferences.v1:user-1', theme), theme)
    await page.goto('/settings/models')
    await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme', theme)
    expect(await page.locator('.app-shell').evaluate(el => getComputedStyle(el).fontSize)).toBe('14px')
    await page.getByRole('tab', {name: 'AI 网关', exact: true}).click()
    const tab = await colors(page.getByRole('tab', {name: 'AI 网关', exact: true}))
    expect(contrast(tab.text, tab.background)).toBeGreaterThanOrEqual(4.5)
    await page.getByRole('button', {name: '添加 AI 网关', exact: true}).click()
    const dialog = page.getByRole('dialog', {name: '添加 AI 网关'})
    const input = dialog.getByLabel('网关名称', {exact: true})
    const field = await colors(input)
    expect(contrast(field.border, field.background)).toBeGreaterThanOrEqual(3)
    expect(contrast(field.text, field.background)).toBeGreaterThanOrEqual(4.5)
    const help = await colors(dialog.locator('.drama-help'))
    expect(contrast(help.text, help.background)).toBeGreaterThanOrEqual(4.5)
    // Enable a real primary action, then check both default and hover contrast.
    await selectOption(dialog.getByLabel('凭据操作', {exact: true}), 'keep')
    const save = dialog.getByRole('button', {name: '保存网关'})
    for (const hover of [false, true]) {
      if (hover) await save.hover()
      await expect.poll(async () => {const c = await colors(save); return contrast(c.text, c.background)}).toBeGreaterThanOrEqual(4.5)
    }
    await page.screenshot({path: `test-results/design-${theme}-${info.project.name}.png`})
    await dialog.getByRole('button', {name: '取消', exact: true}).click()
    await page.getByRole('dialog', {name: '放弃未保存的修改？'}).getByRole('button', {name: '放弃修改'}).click()
    await openNavigation(page)
    const picker = page.locator('.sidebar .space-picker select')
    await page.keyboard.press('Tab')
    await picker.focus()
    const sidebar = await colors(picker)
    expect(sidebar.outline).toBe('solid')
    expect(contrast(sidebar.border, sidebar.background)).toBeGreaterThanOrEqual(3)
    const sidebarBackground = await page.locator('.sidebar').evaluate(el => getComputedStyle(el).backgroundColor)
    expect(contrast(sidebar.focus, sidebarBackground)).toBeGreaterThanOrEqual(3)
    const heights = await page.locator('.app-nav a, .app-nav .group-toggle').evaluateAll(items => items.filter(el => el.getClientRects().length).map(el => el.getBoundingClientRect().height))
    expect(Math.min(...heights)).toBeGreaterThanOrEqual(44)
    await closeNavigation(page)
    await page.goto('/settings/billing')
    const summary = page.locator('.legacy-model-config > summary')
    await page.keyboard.press('Tab')
    await summary.focus()
    const disclosure = await colors(summary)
    expect(disclosure.outline).toBe('solid')
    expect(contrast(disclosure.focus, disclosure.background)).toBeGreaterThanOrEqual(3)
    await page.keyboard.press('Enter')
    await expect(page.getByRole('combobox', {name: '账本币种'})).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

test('login uses the shared type scale and readable field boundaries', async ({page}, info) => {
  await page.route('**/auth/session', route => route.fulfill({json: {mode: 'secure', authenticated: false, registration_enabled: true, sso_available: false}}))
  await page.goto('/')
  await expect(page.locator('.auth-screen')).toBeVisible()
  expect(await page.locator('.auth-screen').evaluate(el => getComputedStyle(el).fontSize)).toBe('14px')
  const field = await colors(page.locator('#auth-email'))
  expect(contrast(field.border, field.background)).toBeGreaterThanOrEqual(3)
  expect(await page.locator('.auth-footnote').evaluate(el => parseFloat(getComputedStyle(el).fontSize))).toBeGreaterThanOrEqual(11)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/design-login-${info.project.name}.png`, fullPage: true})
})

test('navigation uses one mobile breakpoint and content stays within the viewport', async ({page}, info) => {
  test.skip(info.project.name !== 'desktop')
  for (const width of [640, 700, 760, 761, 900, 1200]) {
    await page.setViewportSize({width, height: 900})
    await page.goto('/projects/story-1/storyboard')
    await expect(page.locator('.app-shell')).toBeVisible()
    const toggle = page.locator('.menu-toggle')
    if (width <= 760) await expect(toggle).toBeVisible()
    else await expect(toggle).toBeHidden()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  }
})
