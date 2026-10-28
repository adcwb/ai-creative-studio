// Synthetic HTTP fixtures for navigation tests. Shapes follow src/api.ts,
// src/drama.ts, the panel response interfaces, and api/openapi.json.
// These fixtures exercise the real browser HTTP client, not backend behavior.
import http from 'node:http'
import process from 'node:process'
import { URL } from 'node:url'

const clone = value => JSON.parse(JSON.stringify(value))

const now = '2026-10-28T10:00:00Z'
const project = {id: 'story-1', space_id: 'personal-demo', title: '钟楼来信', brief: '修钟师与快递员合作，把迟到的信送达。', version: 3, script_version: 1, storyboard_stale: false, created_at: now, updated_at: now,
  script_provider: 'mock-script', script_brief: '修钟师与快递员合作，把迟到的信送达。',
  script: {title: '钟楼来信', logline: '一封旧信让两个人相遇。', characters: [{name: '修钟师', description: '守护钟楼的人'}, {name: '快递员', description: '寻找收信人'}], scenes: [1, 2, 3].map(index => ({heading: `钟楼，第${index}场`, action: '两人一起寻找旧信的主人。', dialogue: [{character: '修钟师', text: '我们一起出发。'}]}))},
  storyboard: {provider: 'mock-storyboard', source_script_version: 1, shots: [{id: 'shot-1', scene_index: 1, description: '雨后的钟楼', shot_size: 'wide', camera_movement: 'static', duration_seconds: 3, dialogue: ''}]},
}
const baseTask = {id: 'image-1', space_id: 'personal-demo', prompt: '春日咖啡海报', kind: 'poster', ratio: '4:5', style: 'natural', provider: 'mock-svg', status: 'succeeded', progress: 100, result_url: '/fixture.svg', created_at: now}
let state
function reset(options = {}) {
  state = {mode: 'secure', role: 'owner', empty: false, projects: [clone(project)], tasks: [clone(baseTask)], assets: [{...baseTask, id: 'asset-1', task_id: baseTask.id}], receipts: new Map(), requests: [], ...options}
}
reset()
const server = http.createServer(async (req, res) => {
  res.setHeader('Access-Control-Allow-Origin', 'http://localhost:5178')
  res.setHeader('Access-Control-Allow-Credentials', 'true')
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type, X-CSRF-Token, Idempotency-Key')
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, PUT, OPTIONS')
  if (req.method === 'OPTIONS') {res.writeHead(204); res.end(); return}
  const url = new URL(req.url, 'http://127.0.0.1:5189')
  const path = url.pathname.replace(/^\/api\/v1/, '')
  function send(data, status = 200) {res.writeHead(status, {'Content-Type': 'application/json'}); res.end(JSON.stringify(data))}
  let body = ''
  for await (const chunk of req) body += chunk
  let input
  try {input = body ? JSON.parse(body) : {}} catch {send({error: {message: 'Invalid JSON'}}, 400); return}
  if (path === '/__test__/health') return send({ready: true})
  if (path === '/__test__/reset') {reset(input); return send({ready: true})}
  if (path === '/__test__/state') return send({requests: state.requests, projects: state.projects, tasks: state.tasks})
  if (path === '/fixture.svg') {res.writeHead(200, {'Content-Type': 'image/svg+xml'}); res.end('<svg xmlns="http://www.w3.org/2000/svg" width="600" height="750"><rect width="600" height="750" fill="#e8edda"/><circle cx="300" cy="300" r="150" fill="#52715b"/><text x="300" y="570" text-anchor="middle" font-size="36" fill="#193b2e">Coffee &amp; spring</text></svg>'); return}
  const spaceID = url.searchParams.get('space_id') ?? 'personal-demo'
  state.requests.push({method: req.method, path, space: spaceID, key: req.headers['idempotency-key']})
  if (path === '/auth/session') return send({mode: state.mode, authenticated: true, registration_enabled: false, sso_available: false, user: {id: 'user-1', email: 'studio@example.com'}, csrf_token: 'fixture-csrf'})
  if (path === '/spaces') return send(state.empty ? [] : [{id: 'personal-demo', name: '我的工作空间', type: 'personal', owner_id: 'user-1', role: state.role}, {id: 'team-demo', name: '品牌内容团队', type: 'team', owner_id: 'user-2', role: 'viewer'}])
  if (path === '/capabilities') return send({provider: 'mock-svg', mode: 'mock', ready: true, message: '模拟模式：程序绘制 SVG，不调用 AI 模型或产生模型费用。'})
  if (path === '/templates') return send({templates: [{id: 'poster', name: '春日咖啡', description: '为日常留一份清新', prompt: '春日咖啡海报', kind: 'poster', ratio: '4:5', style: 'natural'}, {id: 'portrait', name: '人物肖像', description: '记录人物的光影', prompt: '自然光肖像', kind: 'portrait', ratio: '4:5', style: 'studio'}, {id: 'general', name: '自由想象', description: '从一个画面开始', prompt: '雨后的森林', kind: 'general', ratio: '16:9', style: 'illustration'}], ratios: [{value: '1:1', label: '方形'}, {value: '4:5', label: '竖版'}, {value: '16:9', label: '横版'}], styles: [{value: 'natural', label: '自然'}, {value: 'studio', label: '棚拍'}, {value: 'illustration', label: '插画'}]})
  if (path === '/runtime-models') return send({governance_ready: false, models: []})
  if (path === '/tasks' && req.method === 'POST') {
    const key = req.headers['idempotency-key']
    if (state.receipts.has(key)) return send(state.receipts.get(key))
    const task = {...baseTask, ...input, id: 'image-' + (state.tasks.length + 1), space_id: spaceID}
    state.tasks.push(task); state.receipts.set(key, task); return send(task, 201)
  }
  if (path === '/tasks') return send(state.tasks.filter(task => task.space_id === spaceID))
  if (path === '/assets') return send(state.assets.filter(asset => asset.space_id === spaceID))
  if (/^\/organizations\/.+\/members$/.test(path)) return send([{id: 'user-1', email: 'studio@example.com', role: state.role}])
  if (path === '/model-config') return send({version: 1, entries: [], adapters: [{provider: 'openai-images', credential_refs: [], capabilities: ['image'], regions: ['global'], endpoint: 'https://api.openai.com/v1'}], secret_storage_available: false, live_enabled: false, runtime_applied: false})
  if (path === '/governance') return send({currency: url.searchParams.get('currency') ?? 'USD', minor_units: 2, limit_minor: 10000, exposure_minor: 1200, available_minor: 8800, entries: [], total_entries: 0, next_offset: null, budget_changes: [], live_execution_enabled: false, fx_available: false})
  if (path === '/creative-capabilities') return send({provider: 'mock-text', mode: 'mock', ready: true, message: '程序模拟创作'})
  if (path === '/creative-media-capabilities' || path === '/alignment-capabilities') return send({})
  if (path === '/projects' && req.method === 'POST') {
    const created = {...project, ...input, id: 'story-' + (state.projects.length + 1), space_id: spaceID, version: 1, script_version: 0, script: undefined, storyboard: undefined}
    state.projects.push(created); return send(created, 201)
  }
  if (path === '/projects') return send(state.projects.filter(item => item.space_id === spaceID))
  const match = path.match(/^\/projects\/([^/]+)(.*)$/)
  if (match) {
    const item = state.projects.find(item => item.id === match[1] && item.space_id === spaceID)
    if (!item) return send({error: {message: 'Project not found'}}, 404)
    if (!match[2] && req.method === 'PUT') {Object.assign(item, input, {version: item.version + 1}); return send(item)}
    if (!match[2]) return send(item)
    if (match[2] === '/script' && req.method === 'PUT') {item.script = input.script; item.version++; return send(item)}
    if (match[2] === '/media') return send({version: 1, assets: [], exports: [], timeline: {clips: [], music_asset_id: '', music_volume: 0.2}})
    if (['/generations', '/history', '/creative-jobs', '/media/alignments'].includes(match[2])) return send([])
    if (match[2] === '/social') return send({version: 1, platforms: [], connections: [], jobs: []})
    if (match[2] === '/social/douyin') return send({ready: false})
  }
  send({error: {message: 'Fixture does not model this endpoint: ' + path}}, 501)
})
server.listen(5189, '127.0.0.1')
process.on('SIGTERM', () => server.close())
