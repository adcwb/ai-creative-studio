#!/usr/bin/env node
// Exercise the official runner's preinstalled Chrome through the locked Playwright.
import { createRequire } from 'node:module'
import { createServer } from 'node:http'
import { once } from 'node:events'
import { fileURLToPath } from 'node:url'
const require = createRequire(new URL('../frontend/package.json', import.meta.url))
const { chromium, expect } = require('@playwright/test')
const server = createServer((_request, response) => {
  response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' })
  response.end('<h1>CI browser ready</h1>')
})
let browser
try {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  browser = await chromium.launch({
    channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL || 'chrome',
    executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,
  })
  const page = await browser.newPage()
  await page.goto(`http://127.0.0.1:${server.address().port}`)
  await expect(page.getByRole('heading')).toHaveText('CI browser ready')
  console.log(`${fileURLToPath(import.meta.url)}: Chrome ${browser.version()} launched successfully`)
} finally {
  await browser?.close()
  if (server.listening) await new Promise(resolve => server.close(resolve))
}
