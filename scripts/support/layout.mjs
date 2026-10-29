// Layout contracts shared by the standalone real-browser CI checks.
export async function settleLayout(page) {
  await page.evaluate(async () => {
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
    await document.fonts.ready
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
    window.scrollTo(0, 0)
  })
}

export async function checkNavigationLayout(page, expect) {
  const toggle = page.locator('.menu-toggle')
  await expect(toggle).toBeAttached()
  const mobile = await toggle.isVisible()
  const sidebar = page.locator('#app-navigation')
  if (mobile) {
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(sidebar).toBeHidden()
    await toggle.click()
    await expect(sidebar).toBeVisible()
  }
  // The routed shell uses a viewport-height rail with its own scrolling area.
  // It no longer stretches to the bottom of a long document.
  const geometry = await sidebar.evaluate(node => {
    const box = node.getBoundingClientRect()
    return {top: box.top, bottom: box.bottom, height: box.height, viewportHeight: innerHeight}
  })
  expect(Math.abs(geometry.top)).toBeLessThanOrEqual(1)
  expect(Math.abs(geometry.height - geometry.viewportHeight)).toBeLessThanOrEqual(1)
  const language = sidebar.locator('.language-picker select')
  await language.scrollIntoViewIfNeeded()
  await expect(language).toBeInViewport()
  if (mobile) {
    await page.locator('.sidebar-close').click()
    await expect(sidebar).toBeHidden()
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  }
  return geometry
}

export async function checkProjectLayout(page, expect) {
  await settleLayout(page)
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(0)
  const geometry = await page.evaluate(() => {
    const rect = selector => {
      const box = document.querySelector(selector).getBoundingClientRect()
      return {left: box.left, right: box.right, top: box.top, bottom: box.bottom}
    }
    return {
      viewport: innerWidth, documentWidth: document.documentElement.scrollWidth,
      heading: rect('.project-page > .page-heading'), steps: rect('.project-steps'), editor: rect('.drama-editor'),
      clippedText: [...document.querySelectorAll('.drama-action,.drama-dialogue p,.drama-character-index + div p')]
        .filter(node => node.scrollHeight > node.clientHeight + 1 || node.scrollWidth > node.clientWidth + 1).length,
    }
  })
  expect(geometry.documentWidth, 'project horizontal overflow').toBeLessThanOrEqual(geometry.viewport)
  expect(geometry.clippedText, 'clipped reading text').toBe(0)
  expect(geometry.steps.top).toBeGreaterThanOrEqual(geometry.heading.bottom)
  expect(geometry.editor.top).toBeGreaterThanOrEqual(geometry.steps.bottom)
  expect(geometry.editor.left).toBeGreaterThanOrEqual(0)
  expect(geometry.editor.right).toBeLessThanOrEqual(geometry.viewport)
  // A scrollable step bar must keep the current route reachable on mobile.
  const current = page.locator('.project-steps > [aria-current]')
  await current.scrollIntoViewIfNeeded()
  await expect(current).toBeInViewport()
  geometry.sidebar = await checkNavigationLayout(page, expect)
  await settleLayout(page)
  return geometry
}
