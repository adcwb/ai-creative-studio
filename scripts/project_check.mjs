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
import { checkProjectLayout } from './support/layout.mjs'

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const require = createRequire(join(repo, 'frontend/package.json'))
const { chromium, devices, expect } = require('@playwright/test')
const temporary = mkdtempSync(join(tmpdir(), 'studio-project-service-'))
const output = process.env.STUDIO_PROJECT_OUTPUT || mkdtempSync(join(tmpdir(), 'studio-project-evidence-'))
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
  async function seed(title, brief, key) {
    const project = await call('/projects?space_id=personal-demo', {title,brief}, `${key}-create`)
    await call(`/projects/${project.id}/generations?space_id=personal-demo`, {expected_version:1}, `${key}-generate`)
    let generated
    await expect.poll(async () => { generated = await call(`/projects/${project.id}?space_id=personal-demo`); return generated.latest_generation?.status }, {timeout:10000}).toBe(brief.includes('[mock-fail]') ? 'failed' : 'succeeded')
    return generated
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
      console.log(`Checking script ${state}/${view.name}`)
      await page.goto(`${origin}/projects`)
      if (project) {
        await page.locator(`.project-card[href="/projects/${project.id}/brief"]`).click()
        await expect(page.getByLabel('项目名称')).toHaveValue(project.title)
        await page.locator(`.project-steps a[href="/projects/${project.id}/script"]`).click()
        await expect(page).toHaveURL(`${origin}/projects/${project.id}/script`)
      } else {
        await expect(page.locator('.page-empty')).toContainText('还没有短剧项目')
        await page.locator('.projects-page > .page-heading').getByRole('link', {name: '新建短剧项目', exact: true}).click()
        await expect(page.getByLabel('项目名称')).toBeVisible()
      }
      if (state === 'empty') await expect(page.locator('.drama-brief')).toBeVisible()
      else if (state === 'failed') await expect(page.getByRole('region', {name: '生成模拟剧本', exact: true})).toContainText('mock_script_failed')
      else await expect(page.locator('.drama-scene')).toHaveCount(3)
      if (state === 'editing') await page.getByRole('button',{name:'编辑剧本',exact:true}).click()
      const geometry = await checkProjectLayout(page, expect)
      expect(errors).toEqual([]); measurements.push({state,name:view.name,...geometry})
      if (state==='generated' && ['desktop-1440','mobile-390'].includes(view.name)) await page.screenshot({path:join(output,`script-${view.name}.png`),fullPage:true})
      const lastControl = state==='editing' ? page.getByRole('button',{name:'保存剧本',exact:true}) : state==='empty' ? page.getByRole('button',{name:'创建项目',exact:true}) : page.getByRole('button',{name:'查看生成记录',exact:false})
      await lastControl.scrollIntoViewIfNeeded(); await expect(lastControl).toBeInViewport()
      if (state==='long') {await page.locator('.drama-scene').last().locator('.drama-dialogue p').last().scrollIntoViewIfNeeded();await expect(page.locator('.drama-scene').last().locator('.drama-dialogue p').last()).toBeInViewport()}
    }
  }
  await measure('empty')
  const normal = await seed('钟楼来信', '修钟师林钟在旧车站发现一封迟到的信。快递员阿雨陪他寻找收件人，两人在落雨前把信送到钟楼，重启停摆多年的时钟。', 'project-normal')
  await measure('generated',normal)
  let long = await seed('合成超长项目名称'.repeat(10).slice(0,80),'合成长度边界验收。'.repeat(250).slice(0,2000),'project-long')
  const script = structuredClone(long.script); script.title='合成剧本标题'.repeat(20).slice(0,80); script.logline='合成故事梗概。'.repeat(50).slice(0,300)
  script.characters.forEach(c=>{c.description='合成人物说明。'.repeat(40).slice(0,200)})
  script.scenes.forEach((scene,index)=>{scene.heading=`第${index+1}场：`+'合成长场景标题'.repeat(20).slice(0,90);scene.action='合成动作描述。'.repeat(200).slice(0,1000);scene.dialogue=Array.from({length:6},(_,line)=>({character:script.characters[line%2].name,text:'合成对白文字。'.repeat(60).slice(0,300)}))})
  long = await call(`/projects/${long.id}/script?space_id=personal-demo`,{script,expected_version:2},'project-long-save','PUT')
  await measure('long',long); await measure('editing',long)
  const failed = await seed('合成失败状态验收','[mock-fail] 布局失败提示与按钮必须可见。','project-failure')
  await measure('failed',failed)
  writeFileSync(join(output,'geometry.json'),JSON.stringify({image_provider_ready:false,script_provider:'mock-script',measurements},null,2))
  console.log(`PASS: ${measurements.length} short-drama browser states with image provider unavailable; real persisted scripts, full-length text, editing controls, geometry and screenshots. Evidence: ${output}`)
} finally {
  await browser?.close()
  for (const process of processes.reverse()) await stop(process)
  rmSync(temporary,{recursive:true,force:true})
}
