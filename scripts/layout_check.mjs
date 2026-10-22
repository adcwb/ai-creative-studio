#!/usr/bin/env node
// Run real-browser geometry checks against isolated local mock-mode services.
import { spawn, execFileSync } from 'node:child_process'
import { once } from 'node:events'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const require = createRequire(join(repo, 'frontend/package.json'))
const { chromium, devices, expect } = require('@playwright/test')
const temporary = mkdtempSync(join(tmpdir(), 'studio-layout-service-'))
const output = process.env.STUDIO_LAYOUT_OUTPUT || mkdtempSync(join(tmpdir(), 'studio-layout-evidence-'))
mkdirSync(output, { recursive: true })
const processes = []
let browser

async function unusedPort() {
  const server = createServer()
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  const port = server.address().port
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
  return port
}

function start(command, args, cwd, env) {
  const process = spawn(command, args, { cwd, env, detached: true, stdio: ['ignore', 'pipe', 'pipe'] })
  let log = ''
  process.stdout.on('data', chunk => { log += chunk.toString() })
  process.stderr.on('data', chunk => { log += chunk.toString() })
  processes.push(process)
  return { process, log: () => log }
}

async function ready(url, service) {
  await expect.poll(async () => {
    if (service.process.exitCode !== null) throw new Error(`Local service exited: ${service.log()}`)
    try { return (await fetch(url, { signal: AbortSignal.timeout(500) })).status } catch { return 0 }
  }, { timeout: 15000 }).toBe(200)
}

async function stop(process) {
  if (process.exitCode !== null || process.signalCode !== null) return
  const done = once(process, 'exit')
  process.kill('SIGTERM')
  const timer = setTimeout(() => {
    try { globalThis.process.kill(-process.pid, 'SIGKILL') } catch { /* Already exited. */ }
  }, 5000)
  await done
  clearTimeout(timer)
  // npm can finish before its child Vite process; stop the dedicated group too.
  try { globalThis.process.kill(-process.pid, 'SIGTERM') } catch { /* Group exited. */ }
}

try {
  const apiPort = await unusedPort()
  const webPort = await unusedPort()
  const api = `http://127.0.0.1:${apiPort}`
  const origin = `http://127.0.0.1:${webPort}`
  const binary = join(temporary, 'server')
  execFileSync('go', ['build', '-o', binary, './cmd/server'], { cwd: join(repo, 'backend'), stdio: 'pipe' })
  const backend = start(binary, [], repo, {
    ...process.env, HTTP_ADDR: `127.0.0.1:${apiPort}`, FRONTEND_ORIGIN: origin,
    DATA_DIR: join(temporary, 'data'), IMAGE_PROVIDER: 'mock-svg', OPENAI_API_KEY: '',
  })
  await ready(`${api}/healthz`, backend)
  const frontend = start('npm', ['run', 'dev', '--', '--port', String(webPort), '--strictPort'], join(repo, 'frontend'), {
    ...process.env, VITE_API_BASE_URL: api,
  })
  await ready(origin, frontend)
  async function call(path, init) {
    const response = await fetch(`${api}/api/v1${path}`, init)
    if (!response.ok) throw new Error(`Local API ${path}: ${response.status} ${await response.text()}`)
    return response.json()
  }
  expect(await call('/tasks?space_id=personal-demo')).toEqual([])
  const catalog = await call('/templates')
  async function seed(input, key) {
    const task = await call('/tasks?space_id=personal-demo', {
      method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key },
      body: JSON.stringify(input),
    })
    await expect.poll(async () => (await call(`/tasks/${task.id}?space_id=personal-demo`)).status, { timeout: 10000 })
      .toBe(input.prompt.includes('[mock-fail]') ? 'failed' : 'succeeded')
    return task
  }
  browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
  const viewports = [
    { name: 'desktop-1280', width: 1280, height: 900 },
    { name: 'desktop-1440', width: 1440, height: 1100 },
    { name: 'desktop-1920', width: 1920, height: 1080 },
    { name: 'desktop-short', width: 1280, height: 640 },
    { name: 'stacked-1040', width: 1040, height: 900 },
    { name: 'mobile-390', width: 390, height: 844, mobile: true },
  ]
  const pages = []
  for (const viewport of viewports) {
    const context = await browser.newContext(viewport.mobile ? { ...devices['iPhone 13'], viewport: { width: viewport.width, height: viewport.height } } : { viewport: { width: viewport.width, height: viewport.height } })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    pages.push({ page, viewport, errors })
  }
  const measurements = []
  async function measure(state, count) {
    for (const { page, viewport, errors } of pages) {
      await page.goto(origin)
      await expect(page.locator('.template-card')).toHaveCount(3)
      if (count === 0) await expect(page.getByText('给你的灵感，一张画布')).toBeVisible()
      else await expect(page.locator('.task')).toHaveCount(count)
      await page.getByRole('button', { name: catalog.templates[0].name, exact: false }).click()
      if (state === 'editing') {
        await page.locator('.task').filter({ hasText: '[mock-fail] 失败状态与按钮布局验收' }).getByRole('button', { name: '编辑后重试' }).click()
        await expect(page.getByRole('button', { name: '取消编辑' })).toBeVisible()
      }
      await page.evaluate(() => {
        if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
        window.scrollTo(0, 0)
      })
      await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
      const geometry = await page.evaluate(() => {
        const rect = selector => {
          const box = document.querySelector(selector).getBoundingClientRect()
          return { top: box.top + scrollY, bottom: box.bottom + scrollY, left: box.left, right: box.right, height: box.height }
        }
        return {
          composer: rect('.composer'), results: rect('.results'), sidebar: rect('.sidebar'), shell: rect('.shell'),
          viewport: innerWidth, documentWidth: document.documentElement.scrollWidth,
          clippedPrompts: [...document.querySelectorAll('.task-prompt')].filter(node => node.scrollHeight > node.clientHeight + 1 || node.scrollWidth > node.clientWidth + 1).length,
        }
      })
      expect(geometry.documentWidth, `${state}/${viewport.name} horizontal overflow`).toBeLessThanOrEqual(geometry.viewport)
      expect(geometry.clippedPrompts, `${state}/${viewport.name} clipped task description`).toBe(0)
      if (viewport.width > 1050) {
        expect(Math.abs(geometry.composer.top - geometry.results.top), `${state}/${viewport.name} panel tops`).toBeLessThanOrEqual(1)
        expect(Math.abs(geometry.composer.bottom - geometry.results.bottom), `${state}/${viewport.name} panel bottoms`).toBeLessThanOrEqual(1)
      } else {
        expect(geometry.results.top, `${state}/${viewport.name} stacked overlap`).toBeGreaterThanOrEqual(geometry.composer.bottom)
      }
      if (viewport.width > 700) {
        expect(Math.abs(geometry.sidebar.bottom - geometry.shell.bottom), `${state}/${viewport.name} sidebar page coverage`).toBeLessThanOrEqual(1)
      }
      expect(errors).toEqual([])
      measurements.push({ state, name: viewport.name, ...geometry })
      if (state === 'empty' || state === 'completed') await page.screenshot({ path: join(output, `${state}-${viewport.name}.png`), fullPage: true })
      if (state === 'editing') {
        await page.getByRole('button', { name: '创建重试任务' }).scrollIntoViewIfNeeded()
        await expect(page.getByRole('button', { name: '创建重试任务' })).toBeInViewport()
      }
      if (count) {
        await page.locator('.task-bottom').last().getByRole('button', { name: '查看详情' }).scrollIntoViewIfNeeded()
        await expect(page.locator('.task-bottom').last().getByRole('button', { name: '查看详情' })).toBeInViewport()
      }
      if (viewport.name === 'desktop-short') {
        await page.locator('.demo-id').scrollIntoViewIfNeeded()
        await expect(page.locator('.demo-id')).toBeInViewport()
      }
    }
  }
  await measure('empty', 0)
  const { prompt, kind, ratio, style } = catalog.templates[0]
  await seed({ prompt, kind, ratio, style }, 'layout-short-prompt')
  await measure('completed', 1)
  await seed({ prompt: '长文本布局验收。'.repeat(100), kind: 'general', ratio: '16:9', style: 'natural' }, 'layout-long-prompt')
  await seed({ prompt: '[mock-fail] 失败状态与按钮布局验收', kind: 'portrait', ratio: '4:5', style: 'studio' }, 'layout-failed-prompt')
  await measure('long', 3)
  await measure('editing', 3)
  writeFileSync(join(output, 'geometry.json'), JSON.stringify(measurements, null, 2))
  console.log(`PASS: ${measurements.length} real-browser layout states; panel/rail geometry, overflow, long-text and reachable controls. Evidence: ${output}`)
} finally {
  await browser?.close()
  for (const process of processes.reverse()) await stop(process)
  rmSync(temporary, { recursive: true, force: true })
}
