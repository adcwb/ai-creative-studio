import {test} from 'node:test'
import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {loadLayoutImages} from './support/layout.mjs'
const require = createRequire(new URL('../frontend/package.json', import.meta.url))
const {chromium} = require('@playwright/test')

test('layout image checks request offscreen lazy images and reject broken or stalled images', async () => {
  const browser = await chromium.launch({channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
  try {
    const page = await browser.newPage()
    await page.route('http://layout.invalid/good.svg', route => route.fulfill({contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>'}))
    await page.setContent('<div style="height:100000px"></div><img loading="lazy" src="http://layout.invalid/good.svg">')
    assert.equal(await page.locator('img').evaluate(image => image.complete), false)
    await loadLayoutImages(page)
    assert.equal(await page.locator('img').evaluate(image => image.naturalWidth), 10)
    await page.setContent('<img src="data:image/png,invalid">')
    await assert.rejects(loadLayoutImages(page, 500), /Layout images did not load\/decode/)
    await page.route('http://layout.invalid/stalled.png', () => {})
    await page.setContent('<img loading="lazy" style="margin-top:100000px" src="http://layout.invalid/stalled.png">')
    await assert.rejects(loadLayoutImages(page, 500), /Layout images did not load\/decode/)
  } finally {await browser.close()}
})
