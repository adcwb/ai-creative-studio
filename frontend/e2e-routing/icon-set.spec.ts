import {expect, test} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
test.beforeEach(async ({request}) => {await request.post(`${fixture}/__test__/reset`)})

// The product ships exactly one icon set: the hand-drawn SVGs in components/Icon,
// all carrying the .icon class. Library components default to their own icons,
// so every one of them has to be handed ours explicitly. This gate catches a
// regression the moment a new antd control is added without doing that.
for (const route of ['/studio', '/settings/models', '/projects/story-1/script']) {
  test(`only the studio icon set renders on ${route}`, async ({page}) => {
    await page.goto(route)
    await expect(page.locator('.app-shell')).toBeVisible()
    expect(await page.locator('svg.icon').count()).toBeGreaterThan(0)
    const foreign = await page.locator('svg:not(.icon)').evaluateAll(nodes =>
      nodes.map(node => node.getAttribute('class') ?? node.parentElement?.className?.toString() ?? 'unclassed'))
    expect(foreign, `foreign icons on ${route}`).toEqual([])
  })
}

test('the sign-in screen uses the studio icon set and follows the active appearance', async ({page}, info) => {
  await page.route('**/auth/session', route => route.fulfill({json: {mode: 'secure', authenticated: false, registration_enabled: true, sso_available: false}}))
  await page.addInitScript(() => localStorage.setItem('studio.appearance', 'dark'))
  await page.goto('/')
  await expect(page.locator('.auth-screen')).toBeVisible()
  await expect(page.locator('#auth-email')).toBeVisible()
  const foreign = await page.locator('svg:not(.icon)').evaluateAll(nodes =>
    nodes.map(node => node.getAttribute('class') ?? node.parentElement?.className?.toString() ?? 'unclassed'))
  expect(foreign, 'foreign icons on sign-in').toEqual([])
  // The form column must paint on the dark canvas, not a hardcoded light one.
  const canvas = await page.locator('.auth-screen').evaluate(node => getComputedStyle(node).backgroundColor)
  expect(canvas).toBe('rgb(15, 21, 18)')
  await page.locator('.auth-password-toggle').click()
  await expect(page.locator('#auth-password')).toHaveAttribute('type', 'text')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({path: `test-results/signin-dark-${info.project.name}.png`, fullPage: true})
})