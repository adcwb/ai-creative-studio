#!/usr/bin/env node
// Real app/worker/Eino/file integration. The upstream is loopback HTTP only.
import { spawn, execFileSync } from 'node:child_process'
import { once } from 'node:events'
import { mkdtempSync, rmSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { deflateSync } from 'node:zlib'
const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const require = createRequire(join(repo, 'frontend/package.json'))
const { chromium, expect } = require('@playwright/test')
const temp = mkdtempSync(join(tmpdir(), 'studio-provider-check-'))
const output = mkdtempSync(join(tmpdir(), 'studio-provider-evidence-'))
const children = []
const key = 'synthetic-test-key-not-a-real-credential'
const model = 'gpt-image-2-2026-04-21'
const fixturePrompt = '本地协议测试样本，非模型生成。合成 PNG 持久化验收。'
let browser, service, upstream
let calls = 0
const requests = []
const violations = []
const held = new Set()
function png(width, height) {
  function chunk(type, data) {
    const tag = Buffer.from(type), payload = Buffer.concat([tag, data]); let crc = 0xffffffff
    for (const byte of payload) { crc ^= byte; for (let i = 0; i < 8; i++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0) }
    const result = Buffer.alloc(data.length + 12); result.writeUInt32BE(data.length); payload.copy(result, 4); result.writeUInt32BE((crc ^ 0xffffffff) >>> 0, result.length - 4); return result
  }
  const header = Buffer.alloc(13); header.writeUInt32BE(width); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 2
  const pixels = Buffer.alloc((width * 3 + 1) * height)
  for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) { const i = y * (width * 3 + 1) + 1 + x * 3; pixels[i] = 55 + Math.floor(x * 130 / width); pixels[i + 1] = 95 + Math.floor(y * 110 / height); pixels[i + 2] = 78 }
  return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]), chunk('IHDR', header), chunk('IDAT', deflateSync(pixels)), chunk('IEND', Buffer.alloc(0))])
}
async function port() { const s = createServer(); s.listen(0, '127.0.0.1'); await once(s, 'listening'); const n = s.address().port; await new Promise(r => s.close(r)); return n }
function start(command, args, cwd, env) { const child = spawn(command, args, { cwd, env, detached: true, stdio: ['ignore','pipe','pipe'] }); let log = ''; child.stdout.on('data', b => { log += b }); child.stderr.on('data', b => { log += b }); const item = { child, log: () => log }; children.push(item); return item }
async function stop(item, signal = 'SIGTERM') { if (!item || item.child.exitCode !== null || item.child.signalCode !== null) return; const end = once(item.child, 'exit'); item.child.kill(signal); const timer = setTimeout(() => { try { process.kill(-item.child.pid, 'SIGKILL') } catch {} }, 3000); await end; clearTimeout(timer); try { process.kill(-item.child.pid, 'SIGTERM') } catch {} }
async function ready(url, item) { await expect.poll(async () => { if (item.child.exitCode !== null) throw Error(item.log()); try { return (await fetch(url, { signal: AbortSignal.timeout(500) })).status } catch { return 0 } }, { timeout: 15000 }).toBe(200) }
try {
  const apiPort = await port(), webPort = await port()
  const api = `http://127.0.0.1:${apiPort}`, origin = `http://127.0.0.1:${webPort}`
  upstream = createServer(async (req, res) => {
    let body = ''; for await (const part of req) body += part
    calls++; const input = JSON.parse(body); requests.push(input)
    if (req.method !== 'POST' || req.url !== '/v1/images/generations' || req.headers.authorization !== `Bearer ${key}` || input.model !== model || input.n !== 1 || input.quality !== 'low' || input.output_format !== 'png' || input.background !== 'opaque' || Object.keys(input).length !== 7) violations.push('official request shape mismatch')
    if (input.prompt.includes('[hold]')) { held.add(res); res.on('close', () => held.delete(res)); return }
    if (input.prompt.includes('[upstream-error]')) { res.writeHead(429, {'Content-Type':'application/json'}); res.end(JSON.stringify({ error: { message: `${key} upstream-private-body` } })); return }
    const [w,h] = input.size.split('x').map(Number); res.writeHead(200, {'Content-Type':'application/json'}); res.end(JSON.stringify({data:[{b64_json:png(w,h).toString('base64')}]}))
  })
  upstream.listen(0, '127.0.0.1'); await once(upstream, 'listening')
  const endpoint = `http://127.0.0.1:${upstream.address().port}/v1/images/generations`
  const binary = join(temp, 'harness')
  execFileSync('go', ['test', '-c', '-o', binary, './internal/studio'], { cwd: join(repo,'backend'), stdio:'pipe' })
  async function boot(mode) { await stop(service); service = start(binary, ['-test.run=^TestProviderHarness$', '-test.timeout=5m'], repo, { PATH: process.env.PATH, IMAGE_PROVIDER:'mock-svg', OPENAI_API_KEY:'', STUDIO_TEST_HARNESS:'1', STUDIO_TEST_MODE:mode, STUDIO_TEST_UPSTREAM:endpoint, AUTH_MODE: 'demo', TEXT_PROVIDER: 'mock-text', SPEECH_PROVIDER: '', VIDEO_PROVIDER: '', ASR_PROVIDER: '', DATA_DIR:join(temp,'data'), HTTP_ADDR:`127.0.0.1:${apiPort}`, FRONTEND_ORIGIN:origin }); await ready(`${api}/healthz`, service) }
  async function call(path, body, idempotencyKey, status = 200) { const response = await fetch(`${api}/api/v1${path}`, body ? { method:'POST', headers:{'Content-Type':'application/json', ...(idempotencyKey ? {'Idempotency-Key':idempotencyKey} : {})}, body:JSON.stringify(body) } : {}); const text = await response.text(); expect(text).not.toContain(key); expect(text).not.toContain('upstream-private-body'); expect(response.status, text).toBe(status); return JSON.parse(text) }
  const input = { prompt:fixturePrompt, kind:'general', ratio:'1:1', style:'natural' }
  async function terminal(id, status) { let task; await expect.poll(async () => { task = await call(`/tasks/${id}?space_id=personal-demo`); return task.status }, {timeout:10000}).toBe(status); return task }
  await boot('mock')
  const old = await call('/tasks?space_id=personal-demo', {...input,prompt:'历史模拟 SVG，非 AI 生成'}, 'old-mock',202); await terminal(old.id,'succeeded'); await call('/assets?space_id=personal-demo',{task_id:old.id},null,200)
  const frontend = start('npm',['run','dev','--','--port',String(webPort),'--strictPort'],join(repo,'frontend'),{PATH: process.env.PATH,VITE_API_BASE_URL:api}); await ready(origin,frontend)
  browser = await chromium.launch({channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL,executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH})
  const page = await browser.newPage({locale:'zh-CN',viewport:{width:1440,height:1100}}); const errors = []; page.on('pageerror', e => errors.push(e.message))
  await page.goto(origin); await expect(page.locator('.template-card')).toHaveCount(3); await expect(page.locator('.provider-mock')).toContainText('模拟')
  await page.getByLabel('描述你的画面').fill(fixturePrompt); await page.getByLabel('画面比例').selectOption('1:1')
  // Leave cached mock capabilities in this browser, then change the process.
  await boot('real'); let posts = 0; const seenKeys = []
  page.on('request', req => { if (req.method()==='POST' && req.url().includes('/tasks')) { posts++; seenKeys.push(req.headers()['idempotency-key']) } })
  const guardResponse = page.waitForResponse(r => r.request().method()==='POST' && r.url().includes('/tasks'))
  await page.getByRole('button',{name:'创建模拟图像'}).click(); expect((await guardResponse).status()).toBe(409)
  await expect(page.getByText(/本次未创建任务/)).toBeVisible(); await expect(page.locator('.provider-real')).toBeVisible(); expect(posts).toBe(1); expect(calls).toBe(0)
  await page.getByRole('button',{name:'创建图像',exact:true}).click()
  await expect(page.locator('.task[data-provider="openai-images"]')).toHaveCount(1)
  await expect(page.locator('.task[data-provider="openai-images"]')).toContainText('生成完成',{timeout:10000})
  expect(seenKeys[0]).toBe(seenKeys[1]); expect(calls).toBe(1)
  const real = (await call('/tasks?space_id=personal-demo')).find(t => t.provider==='openai-images')
  expect(real.result_mime).toBe('image/png'); expect(real.result_width).toBe(1024)
  const image = page.locator('.task[data-provider="openai-images"] img'); await expect.poll(() => image.evaluate(n => n.naturalWidth)).toBe(1024)
  await expect(page.locator('.task[data-provider="mock-svg"]')).toContainText('模拟完成')
  await page.locator('.task[data-provider="openai-images"]').getByRole('button',{name:'保存到素材库'}).click()
  await expect(page.locator('.task[data-provider="openai-images"]')).toContainText('已保存')
  await page.locator('.task[data-provider="openai-images"]').getByRole('button',{name:'查看详情'}).click(); await expect(page.getByRole('dialog')).toContainText(model); await expect(page.getByRole('dialog')).toContainText('1024'); await page.keyboard.press('Escape')
  await page.evaluate(() => { document.activeElement?.blur(); window.scrollTo(0,0) }); await page.screenshot({path:join(output,'local-protocol-fixture-desktop.png'),fullPage:true})
  const bytes = Buffer.from(await (await fetch(`${api}${real.result_url}`)).arrayBuffer()); expect(bytes).toEqual(png(1024,1024))
  // Repeated original key remains the original execution, including after restart.
  await boot('real'); await page.reload(); await expect(page.locator('.task')).toHaveCount(2)
  const stored = await call(`/tasks/${real.id}?space_id=personal-demo`); expect(stored.result_mime).toBe('image/png'); expect(await call('/assets?space_id=personal-demo')).toHaveLength(2)
  expect(Buffer.from(await (await fetch(`${api}${real.result_url}`)).arrayBuffer())).toEqual(bytes); expect(calls).toBe(1)
  // Lost response leaves the original draft/key. Unknown capability blocks POST.
  await page.getByLabel('描述你的画面').fill('本地协议测试响应丢失样本，非模型生成')
  let lostBody, lostKey; const match = '**/api/v1/tasks?space_id=personal-demo'
  await page.route(match,async route => { if(route.request().method()!=='POST') return route.continue(); lostBody=route.request().postDataJSON(); lostKey=route.request().headers()['idempotency-key']; await route.fetch(); await route.abort('failed') },{times:1})
  await page.getByRole('button',{name:'创建图像',exact:true}).click(); await expect(page.getByRole('alert')).toBeVisible()
  await page.route('**/api/v1/capabilities', route => route.abort('failed'))
  await page.getByRole('button',{name:'重新检查图像服务'}).click(); await expect(page.getByRole('button',{name:'创建图像',exact:true})).toBeDisabled(); const blockedPosts=posts
  await page.getByRole('button',{name:'创建图像',exact:true}).evaluate(button => button.click()); expect(posts).toBe(blockedPosts)
  await page.unroute('**/api/v1/capabilities'); await page.getByRole('button',{name:'重新检查图像服务'}).click(); await expect(page.getByRole('button',{name:'创建图像',exact:true})).toBeEnabled()
  await page.getByRole('button',{name:'创建图像',exact:true}).click(); await expect(page.getByText('任务已提交。相同设定重复点击会返回同一任务；可点击“开始新创作”另建任务。')).toBeVisible(); expect(seenKeys.at(-1)).toBe(lostKey)
  const recovered = await call('/tasks?space_id=personal-demo',lostBody,lostKey,200); await terminal(recovered.id,'succeeded'); expect(calls).toBe(2)
  const failed = await call('/tasks?space_id=personal-demo',{...input,prompt:'[upstream-error] 合成错误路径'},'safe-error',202); const failedTask=await terminal(failed.id,'failed'); expect(failedTask.error_code).toBe('provider_rate_limited'); expect(calls).toBe(3)
  const interrupted = await call('/tasks?space_id=personal-demo',{...input,prompt:'[hold] 合成进程中断验收'},'interrupted',202); await expect.poll(()=>calls).toBe(4)
  await stop(service,'SIGKILL'); await boot('real'); const interruptedTask = await terminal(interrupted.id,'failed'); expect(interruptedTask.error_code).toBe('execution_interrupted')
  await new Promise(r=>setTimeout(r,600)); expect(calls).toBe(4)
  await call('/tasks?space_id=personal-demo',{...input,prompt:'[hold] 合成进程中断验收'},'interrupted',200); expect(calls).toBe(4)
  const retry = await call(`/tasks/${interrupted.id}/retry?space_id=personal-demo`,input,'explicit-retry',202); await terminal(retry.id,'succeeded'); expect(retry.retry_of).toBe(interrupted.id); expect(calls).toBe(5)
  await boot('unavailable'); const caps=await call('/capabilities'); expect(caps.ready).toBe(false); await call('/tasks?space_id=personal-demo',{...input,prompt:'[hold] 合成进程中断验收'},'interrupted',200); await call('/tasks?space_id=personal-demo',input,'unready-new',503)
  await page.reload(); await expect(page.locator('.banner.provider-unavailable')).toBeVisible(); await page.getByLabel('描述你的画面').fill('不可用状态保留草稿'); await expect(page.getByRole('button',{name:'创建图像',exact:true})).toBeDisabled(); expect(calls).toBe(5)
  const state=readFileSync(join(temp,'data/state.json'),'utf8'); const logs=children.map(c=>c.log()).join('\n'); for (const text of [state,logs]) { expect(text).not.toContain(key); expect(text).not.toContain('upstream-private-body') }
  expect(violations).toEqual([]); expect(errors).toEqual([])
  writeFileSync(join(output,'evidence.json'),JSON.stringify({upstream:'loopback fake HTTP; synthetic PNG; no official API invocation',calls,requests:requests.map(({model,size})=>({model,size})),checks:['cached mock to real guard','browser Eino PNG save and restart','lost-response same-key replay','unknown capability blocks POST','sanitized upstream error','SIGKILL interruption no replay','explicit retry association','unavailable accepted-key replay','secret exclusion']},null,2))
  console.log(`PASS: local provider protocol + browser + restart/interruption; ${calls} explicit upstream calls, zero automatic replay. Evidence: ${output}`)
} finally { await browser?.close(); for(const item of children.reverse()) await stop(item); for(const res of held) res.destroy(); upstream?.closeAllConnections(); await new Promise(r=>upstream ? upstream.close(r) : r()); rmSync(temp,{recursive:true,force:true}) }
