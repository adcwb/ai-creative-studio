import { useEffect, useRef, useState } from 'react'
import { baseURL, promptError, request, type Asset, type Kind, type Task } from './api'

const statusLabels = { queued: '等待执行', running: '模拟制作中', succeeded: '模拟完成', failed: '执行失败' }
const samples = ['春日咖啡品牌海报，奶油色背景，柔和光影，留出标题空间', '个人职业肖像，简洁背景，自然光，沉稳而亲切的氛围']
export default function App() {
  const [space, setSpace] = useState('personal-demo')
  // Remount on space change prevents stale responses from crossing the active UI space.
  return <div className="shell">
    <aside><a className="brand" href="/">映<span>创</span><small>CREATIVE STUDIO</small></a><div className="workspace-label">工作空间</div><label className="sr-only" htmlFor="space">选择演示空间</label><select id="space" value={space} onChange={e => setSpace(e.target.value)}><option value="personal-demo">◈ 个人演示空间</option><option value="team-demo">◈ 团队演示空间</option></select><div className="sidebar-note">让创意，从一张图开始。</div><div className="roadmap"><span>下一阶段</span><strong>短剧 / 漫剧工作台</strong><p>规划中 · 暂未提供生成能力</p></div><div className="demo-id">演示身份 · demo-user<br/>空间切换不代表生产权限隔离</div></aside>
    <main><header><span>工作室 / 视觉创作</span><span className="pill">MOCK PROVIDER · 无需凭据</span></header><Workspace key={space} space={space}/></main>
  </div>
}
function Workspace({space}: {space: string}) {
  const [tab, setTab] = useState<'create' | 'assets'>('create')
  const [prompt, setPrompt] = useState('')
  const [kind, setKind] = useState<Kind>('poster')
  const [tasks, setTasks] = useState<Task[]>([])
  const [assets, setAssets] = useState<Asset[]>([])
  const [busy, setBusy] = useState(false)
  const [saving, setSaving] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [connectionError, setConnectionError] = useState('')
  const [actionError, setActionError] = useState('')
  const [notice, setNotice] = useState('')
  const [reload, setReload] = useState(0)
  const inFlight = useRef(false)
  const saveInFlight = useRef(false)
  const attempt = useRef<{fingerprint: string; key: string} | null>(null)
  const query = `?space_id=${space}`
  useEffect(() => {
    let active = true
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const [newTasks, newAssets] = await Promise.all([request<Task[]>(`/tasks${query}`, {signal: controller.signal}), request<Asset[]>(`/assets${query}`, {signal: controller.signal})])
        if (active) {setTasks(newTasks); setAssets(newAssets); setConnectionError('')}
      } catch (error) {if (active) setConnectionError(error instanceof Error ? error.message : '服务连接失败')}
      finally {if (active) {setLoading(false); timer = setTimeout(() => {void load()}, 1500)}}
    }
    void load()
    return () => {active = false; controller.abort(); clearTimeout(timer)}
  }, [query, reload])
  async function create(event: React.FormEvent) {
    event.preventDefault()
    if (inFlight.current) return
    const invalid = promptError(prompt)
    if (invalid) {setActionError(invalid); return}
    inFlight.current = true; setBusy(true); setActionError(''); setNotice('')
    const body = JSON.stringify({prompt: prompt.trim(), kind})
    if (attempt.current?.fingerprint !== body) attempt.current = {fingerprint: body, key: crypto.randomUUID()}
    try {
      const task = await request<Task>(`/tasks${query}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.current.key}, body})
      setTasks(previous => [task, ...previous.filter(t => t.id !== task.id)])
      setNotice('任务已提交；相同描述重复点击会返回同一任务。修改描述可创建新任务。')
    } catch (error) {setActionError(error instanceof Error ? error.message : '提交失败，请重试')}
    finally {inFlight.current = false; setBusy(false)}
  }
  async function save(task: Task) {
    if (saveInFlight.current) return
    saveInFlight.current = true; setSaving(task.id); setActionError('')
    try {
      const asset = await request<Asset>(`/assets${query}`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({task_id: task.id})})
      setAssets(previous => [asset, ...previous.filter(a => a.id !== asset.id)]); setNotice('已保存到当前空间的素材库。')
    } catch (error) {setActionError(error instanceof Error ? error.message : '保存失败，请重试')}
    finally {saveInFlight.current = false; setSaving(null)}
  }
  return <>
    <section className="intro"><div className="eyebrow">YOUR NEXT IDEA STARTS HERE</div><h1>把灵感，变成画面<span>。</span></h1><p>从海报到肖像，在一个工作室里探索你的视觉创意。</p></section>
    <div className="banner"><strong>模拟演示模式</strong><span>本阶段只生成带水印的程序绘制 SVG，用于验证任务流程。非真实 AI 图像，不支持肖像编辑。请勿输入敏感信息。</span></div>
    <nav className="tabs" aria-label="工作室功能"><button aria-current={tab==='create' ? 'page' : undefined} onClick={() => setTab('create')}>图像创作</button><button aria-current={tab==='assets' ? 'page' : undefined} onClick={() => setTab('assets')}>素材库 <span>{assets.length}</span></button></nav>
    {connectionError && <div role="alert" className="error">无法同步服务：{connectionError} <button onClick={() => setReload(n => n+1)}>重新连接</button></div>}
    {actionError && <div role="alert" className="error">{actionError}</div>}
    {notice && <p role="status" className="notice">{notice}</p>}
    {tab === 'create' ? <div className="creation-grid"><section className="panel composer"><div className="section-heading"><h2>创作设定</h2><span>01 / CREATE</span></div><form onSubmit={event => {void create(event)}}><fieldset disabled={busy}><legend>你想创作什么？</legend><div className="kind-options"><button type="button" aria-pressed={kind==='poster'} onClick={() => setKind('poster')}><b>▧</b>创意海报<small>品牌 · 活动 · 概念</small></button><button type="button" aria-pressed={kind==='portrait'} onClick={() => setKind('portrait')}><b>◉</b>肖像概念<small>仅模拟 · 不上传照片</small></button></div><label htmlFor="prompt">描述你的画面</label><textarea id="prompt" value={prompt} onChange={e => setPrompt(e.target.value)} placeholder="试着描述主题、风格、色彩与构图……" rows={7} maxLength={2000}/><div className="field-footer"><span>清晰的描述，是好创意的起点</span><span>{[...prompt.trim()].length} / 1000</span></div><div className="sample-label">没有灵感？从这里开始</div><div className="samples">{samples.map((text,index) => <button key={text} type="button" onClick={() => {setPrompt(text); setKind(index===0?'poster':'portrait')}}>{index===0?'↗ 春日品牌海报':'↗ 自然光肖像'}</button>)}</div><button className="primary" type="submit" disabled={busy || !!promptError(prompt)}>{busy ? '正在提交…' : '✧ 创建模拟图像'}</button><p className="footnote">不消耗额度 · Eino 流程编排 · 结果保存在本地服务</p></fieldset></form></section><section className="panel results"><div className="section-heading"><h2>创作记录</h2><span>{tasks.length} 个任务</span></div>{loading && <p role="status">正在加载任务…</p>}{!loading && !tasks.length && <div className="empty"><div className="empty-art">✧</div><h3>第一张画面，从这里诞生</h3><p>填写左侧描述，创建你的第一个模拟任务。<br/>进度与结果会出现在这里。</p></div>}<div className="task-list">{tasks.map(task => <article className="task" key={task.id}><div className="task-top"><span className={`status ${task.status}`}>{statusLabels[task.status]}</span><small>{task.kind==='poster'?'海报':'肖像概念'}</small></div><p className="task-prompt">{task.prompt}</p>{task.result_url && <img src={`${baseURL}${task.result_url}`} alt="程序绘制的模拟图像，非真实 AI 生成"/>}{(task.status==='queued'||task.status==='running') && <><progress max="100" value={task.progress}/><p className="footnote">任务进度 {task.progress}% · 正在自动更新</p></>}{task.error && <p className="error">{task.error}</p>}<div className="task-bottom"><small>MOCK SVG · {new Date(task.created_at).toLocaleTimeString('zh-CN')}</small>{task.status==='succeeded' && <button disabled={saving!==null||assets.some(a => a.task_id===task.id)} onClick={() => {void save(task)}}>{assets.some(a => a.task_id===task.id)?'已保存':saving===task.id?'保存中…':'保存到素材库'}</button>}{task.status==='failed' && <button onClick={() => {setPrompt(task.prompt.replaceAll('[mock-fail]','')); setKind(task.kind)}}>修改描述</button>}</div></article>)}</div></section></div> : <section className="panel library"><div className="section-heading"><h2>我的素材</h2><span>仅显示当前演示空间</span></div>{loading ? <p role="status">正在加载素材…</p> : !assets.length ? <div className="empty"><h3>为灵感留个位置</h3><p>完成任务后点击“保存到素材库”，即可在这里浏览。</p></div> : <div className="asset-grid">{assets.map(asset => <article key={asset.id}><img src={`${baseURL}${asset.result_url}`} alt="已保存的模拟 SVG，非真实 AI 图像"/><span className="pill">模拟素材 · MOCK SVG</span><p>{asset.prompt}</p></article>)}</div>}</section>}
    <footer>映创工作室 · Phase 01 <span>独立图像创作 / 可验证的第一步</span></footer>
  </>
}
