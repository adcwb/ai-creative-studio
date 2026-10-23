#!/usr/bin/env node
// Run real-browser short-drama layout checks against isolated local services.
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
const temporary = mkdtempSync(join(tmpdir(), 'studio-storyboard-service-'))
const output = process.env.STUDIO_STORYBOARD_OUTPUT || mkdtempSync(join(tmpdir(), 'studio-storyboard-evidence-'))
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
  const apiPort = await unusedPort(), webPort = await unusedPort()
  const api = `http://127.0.0.1:${apiPort}`, origin = `http://127.0.0.1:${webPort}`
  const binary = join(temporary, 'server')
  execFileSync('go', ['build', '-o', binary, './cmd/server'], { cwd: join(repo, 'backend'), stdio: 'pipe' })
  // Image configuration intentionally unavailable: local mock-script is independent.
  const backend = start(binary, [], repo, { PATH: process.env.PATH, HTTP_ADDR: `127.0.0.1:${apiPort}`, FRONTEND_ORIGIN: origin, AUTH_MODE: 'demo', TEXT_PROVIDER: 'mock-text', SPEECH_PROVIDER: '', VIDEO_PROVIDER: '', ASR_PROVIDER: '', DATA_DIR: join(temporary, 'data'), IMAGE_PROVIDER: 'openai-images', OPENAI_API_KEY: '' })
  await ready(`${api}/healthz`, backend)
  const frontend = start('npm', ['run', 'dev', '--', '--port', String(webPort), '--strictPort'], join(repo, 'frontend'), { PATH: process.env.PATH, VITE_API_BASE_URL: api })
  await ready(origin, frontend)
  async function call(path, body, key, method) {
    const response = await fetch(`${api}/api/v1${path}`, body ? {method:method || 'POST', headers:{'Content-Type':'application/json','Idempotency-Key':key},body:JSON.stringify(body)} : {})
    const text = await response.text(); expect(response.ok, `${response.status} ${text}`).toBe(true); return JSON.parse(text)
  }
  expect((await call('/capabilities')).ready).toBe(false)
  expect(await call('/projects?space_id=personal-demo')).toEqual([])
  async function seed(title, key, board = true) {
    let project = await call('/projects?space_id=personal-demo', {title,brief:'合成演示：修钟师林钟与快递员阿雨穿过雨后的旧城，把迟到的信送达钟楼。'}, `${key}-create`)
    await call(`/projects/${project.id}/generations?space_id=personal-demo`, {expected_version:project.version}, `${key}-script`)
    await expect.poll(async () => {project=await call(`/projects/${project.id}?space_id=personal-demo`);return project.latest_generation?.status}, {timeout:10000}).toBe('succeeded')
    if(board) {
      await call(`/projects/${project.id}/storyboard-generations?space_id=personal-demo`, {expected_version:project.version}, `${key}-board`)
      await expect.poll(async () => {project=await call(`/projects/${project.id}?space_id=personal-demo`);return project.latest_storyboard_generation?.status}, {timeout:10000}).toBe('succeeded')
    }
    return project
  }
  browser = await chromium.launch({channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH })
  const views = [
    {name:'desktop-1280',width:1280,height:900}, {name:'desktop-1440',width:1440,height:1100},
    {name:'desktop-1920',width:1920,height:1080}, {name:'desktop-short',width:1280,height:640},
    {name:'stacked-1040',width:1040,height:900}, {name:'mobile-390',width:390,height:844,mobile:true},
  ]
  const pages = []
  for (const view of views) {
    const context = await browser.newContext(view.mobile ? {...devices['iPhone 13'],locale:'zh-CN',viewport:{width:view.width,height:view.height}} : {locale:'zh-CN',viewport:{width:view.width,height:view.height}})
    const page = await context.newPage(), errors = []; page.on('pageerror', e => errors.push(e.message)); pages.push({page,view,errors})
  }
  const measurements = []
  async function measure(state, project) {
    for (const {page,view,errors} of pages) {
      await page.goto(origin); await page.getByRole('button',{name:'短剧工作台',exact:true}).click()
      if (project) await page.locator('.drama-project-list button').filter({hasText:project.title}).click()
      await expect(page.getByLabel('项目名称')).toBeVisible()
      if (state === 'empty') await expect(page.locator('.storyboard-empty')).toBeVisible()
      else await expect(page.locator('.storyboard-shot')).toHaveCount(project.storyboard.shots.length)
      if(state === 'failed') await expect(page.locator('.storyboard-generation')).toContainText('mock_storyboard_failed')
      if(state === 'stale') await expect(page.locator('.storyboard-stale')).toContainText('已过期')
      if (state === 'editing') await page.getByRole('button',{name:'编辑分镜',exact:true}).click()
      await page.evaluate(async () => { await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); if (document.activeElement instanceof HTMLElement) document.activeElement.blur(); window.scrollTo(0,0) })
      await expect.poll(() => page.evaluate(() => scrollY)).toBe(0)
      const geometry = await page.evaluate(() => {
        const rect = selector => {const r = document.querySelector(selector).getBoundingClientRect();return {left:r.left,right:r.right,top:r.top+scrollY,bottom:r.bottom+scrollY}}
        return {viewport:innerWidth,documentWidth:document.documentElement.scrollWidth,sidebar:rect('.sidebar'),shell:rect('.shell'),projects:rect('.drama-projects'),editor:rect('.drama-editor'),
          clippedText:[...document.querySelectorAll('.drama-action,.drama-dialogue p,.drama-project-title,.drama-character-index + div p')].filter(n=>n.scrollHeight>n.clientHeight+1||n.scrollWidth>n.clientWidth+1).length}
      })
      expect(geometry.documentWidth,`${state}/${view.name} overflow`).toBeLessThanOrEqual(geometry.viewport)
      expect(geometry.clippedText,`${state}/${view.name} clipped reading text`).toBe(0)
      if (view.width>1100) {
        expect(Math.abs(geometry.projects.top-geometry.editor.top)).toBeLessThanOrEqual(1)
        expect(geometry.projects.right).toBeLessThanOrEqual(geometry.editor.left)
      } else expect(geometry.editor.top).toBeGreaterThanOrEqual(geometry.projects.bottom)
      if (view.width>700) expect(Math.abs(geometry.sidebar.bottom-geometry.shell.bottom)).toBeLessThanOrEqual(1)
      expect(errors).toEqual([]); measurements.push({state,name:view.name,...geometry})
      if (state==='generated' && ['desktop-1440','mobile-390'].includes(view.name)) await page.screenshot({path:join(output,`storyboard-${view.name}.png`),fullPage:true})
      const lastControl = state==='editing' ? page.getByRole('button',{name:'保存分镜',exact:true}) : state==='empty' ? page.getByRole('button',{name:'生成模拟分镜',exact:true}) : page.locator('.storyboard-shot').last()
      await lastControl.scrollIntoViewIfNeeded(); await expect(lastControl).toBeInViewport()
      if (state==='long') {const last=page.locator('.storyboard-shot').last().locator('.drama-dialogue p');await last.scrollIntoViewIfNeeded();await expect(last).toBeInViewport()}
      if (view.name==='desktop-short') {await page.locator('.demo-id').scrollIntoViewIfNeeded();await expect(page.locator('.demo-id')).toBeInViewport()}
    }
  }
  const empty = await seed('合成分镜空态','storyboard-empty',false)
  await measure('empty',empty)
  const normal = await seed('钟楼来信 · 分镜计划','storyboard-normal')
  await measure('generated',normal)
  let long = await seed('合成长分镜验收','storyboard-long')
  const shots = structuredClone(long.storyboard.shots)
  shots.forEach(shot=>{shot.description='合成长画面说明。'.repeat(200).slice(0,1000);shot.dialogue='合成长对白文字。'.repeat(300).slice(0,2000)})
  long=await call(`/projects/${long.id}/storyboard?space_id=personal-demo`,{shots,expected_version:long.version},'storyboard-long-save','PUT')
  await measure('long',long);await measure('editing',long)
  const script=structuredClone(normal.script);script.scenes[0].action='[mock-fail] 合成新剧本，旧分镜保留并标记过期。'
  const stale=await call(`/projects/${normal.id}/script?space_id=personal-demo`,{script,expected_version:normal.version},'storyboard-source-save','PUT')
  await measure('stale',stale)
  await call(`/projects/${normal.id}/storyboard-generations?space_id=personal-demo`,{expected_version:stale.version},'storyboard-fail')
  let failed
  await expect.poll(async()=>{failed=await call(`/projects/${normal.id}?space_id=personal-demo`);return failed.latest_storyboard_generation?.status},{timeout:10000}).toBe('failed')
  expect(failed.storyboard).toEqual(stale.storyboard)
  await measure('failed',failed)
  writeFileSync(join(output,'geometry.json'),JSON.stringify({image_provider_ready:false,storyboard_provider:'mock-storyboard',measurements},null,2))
  console.log(`PASS: ${measurements.length} storyboard browser states with image provider unavailable; real persisted storyboard plans, full-length text, editing controls, geometry and screenshots. Evidence: ${output}`)
} finally {
  await browser?.close()
  for (const process of processes.reverse()) await stop(process)
  rmSync(temporary,{recursive:true,force:true})
}
