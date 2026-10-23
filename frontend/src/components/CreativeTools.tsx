import { useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request } from '../api'
import type { DramaProject, DramaScript, StoryboardPlan } from '../drama'
interface Analysis { summary: string; questions: string[]; suggestions: string[]; provider: string; source_version: number }
interface CreativeJob {id: string; kind: string; status: string; analysis?: Analysis; error?: string; provider?: string; upstream_id?: string}
interface Capability {provider: string; model?: string; mode: string; ready: boolean; message: string}
interface Revision { id: string; kind: 'script' | 'storyboard'; created_at: string; script_version: number; script?: DramaScript; storyboard?: StoryboardPlan }
export function CreativeTools({project, space, disabled, onRestore, onDirtyChange, expectedProvider, expectedModel}: {project: DramaProject; space: string; disabled: boolean; onRestore: () => void; onDirtyChange: (dirty: boolean) => void; expectedProvider?: string; expectedModel?: string}) {
  const [jobs, setJobs] = useState<CreativeJob[]>([])
  const [capabilities, setCapabilities] = useState<Record<string, Capability>>({})
  const [references, setReferences] = useState<{id: string; name: string; character: string}[]>([])
  const [referenceId, setReferenceId] = useState('')
  const [kind, setKind] = useState('image')
  const [prompt, setPrompt] = useState('')
  const [shotId, setShotId] = useState('')
  const [character, setCharacter] = useState('')
  const analysis = jobs.find(job => job.kind === 'analysis' && job.status === 'succeeded')?.analysis
  const [history, setHistory] = useState<Revision[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [confirm, setConfirm] = useState<Revision | null>(null)
  const pending = useRef<{path: string; body: string; key: string; restore: boolean} | null>(null)
  const live = useRef(true)
  const flight = useRef(false)
  const prefix = `/projects/${encodeURIComponent(project.id)}`
  const query = new URLSearchParams({space_id: space}).toString()
  useEffect(() => {live.current = true; return () => {live.current = false}}, [])
  useEffect(() => {onDirtyChange(busy || uncertain); return () => onDirtyChange(false)}, [busy, uncertain, onDirtyChange])
  useEffect(() => {const controller = new AbortController(); void request<Revision[]>(`${prefix}/history?${query}`, {signal: controller.signal}).then(setHistory).catch(failure => {if (!controller.signal.aborted) setError(errorMessage(failure, '历史读取失败'))}); return () => controller.abort()}, [prefix, query, project.version])
  useEffect(() => {
    const controller = new AbortController()
    void request<Record<string, Capability>>('/creative-media-capabilities', {signal: controller.signal}).then(setCapabilities).catch(() => {})
    async function poll() {try {const result = await request<CreativeJob[]>(`${prefix}/creative-jobs?${query}`, {signal: controller.signal}); if (!controller.signal.aborted) setJobs(result); const media = await request<{assets: {id: string; name: string; character: string; kind: string}[]}>(`${prefix}/media?${query}`, {signal: controller.signal}); if (!controller.signal.aborted) setReferences(media.assets.filter(asset => asset.kind === 'image'))} catch (failure) {if (!controller.signal.aborted) setError(errorMessage(failure, '创作任务读取失败'))}}
    void poll(); const timer = setInterval(() => {void poll()}, 2000)
    return () => {controller.abort(); clearInterval(timer)}
  }, [prefix, query])
  async function execute() {
    const attempt = pending.current
    if (!attempt || flight.current) return
    flight.current = true; setBusy(true); setError('')
    try {
      const result = await request<CreativeJob | DramaProject>(attempt.path, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.key}, body: attempt.body})
      if (!live.current) return
      setUncertain(false); pending.current = null
      if (attempt.restore) {setConfirm(null); onRestore()} else {const job = result as CreativeJob; setJobs(previous => [job, ...previous.filter(item => item.id !== job.id)])}
    } catch (failure) {if (live.current) {const unknown = !(failure instanceof ApiError) || failure.status >= 500; setUncertain(unknown); setError(unknown ? '结果尚未确认，请重试同一请求。' : errorMessage(failure, '操作失败'))}} finally {flight.current = false; if (live.current) setBusy(false)}
  }
  function submit(suffix: string, restore: boolean, data?: unknown) {if (disabled || busy || uncertain) return; pending.current = {path: `${prefix}${suffix}?${query}`, body: JSON.stringify(data ?? {expected_version: project.version, ...(!restore && expectedProvider ? {expected_provider: expectedProvider, expected_model: expectedModel ?? ''} : {})}), key: crypto.randomUUID(), restore}; void execute()}
  return <section className="panel creative-tools"><div className="section-heading"><div><span className="eyebrow">STORY DEVELOPMENT</span><h2>需求分析与版本历史</h2></div></div>
    <p className="drama-help">基于已保存简报识别待澄清信息。回答后补充到项目简报并保存；分析建议不会自动覆盖你的内容。</p><button disabled={disabled || busy || uncertain} onClick={() => submit('/analysis', false)}>{busy ? '处理中…' : '分析已保存需求'}</button>
    {error && <div role="alert" className="error">{error}{uncertain && <button disabled={busy} onClick={() => void execute()}>重试同一请求</button>}</div>}
    {analysis && <div className="creative-analysis"><p className="drama-source">来源：{analysis.provider} · 项目版本 {analysis.source_version}{analysis.source_version !== project.version ? ' · 当前项目已变化' : ''}</p><h3>{analysis.summary}</h3><h4>待澄清问题</h4><ul>{analysis.questions.map((question, i) => <li key={i}>{question}</li>)}</ul><h4>创作建议</h4><ul>{analysis.suggestions.map((suggestion, i) => <li key={i}>{suggestion}</li>)}</ul></div>}
    <h3>镜头图像、视频与配音</h3><p className="drama-help">素材生成成功后加入下方媒体库。仅已配置可用供应商可提交；真实请求可能计费，失败或取消不代表上游未收费，不自动重试。已有视频任务可继续查询原结果，不重新提交生成；另建请求可能再次计费。</p>
    <fieldset disabled={disabled || busy || uncertain}><div className="storyboard-fields"><label>创作类型<select value={kind} onChange={event => setKind(event.target.value)}><option value="image">分镜图片</option><option value="video">镜头视频</option><option value="speech">配音</option></select></label><label>关联镜头<select value={shotId} onChange={event => setShotId(event.target.value)}><option value="">不关联镜头</option>{project.storyboard?.shots.map((shot, i) => <option key={shot.id} value={shot.id}>第 {i + 1} 镜</option>)}</select></label><label>角色约束<select value={character} onChange={event => setCharacter(event.target.value)}><option value="">不关联角色</option>{project.script?.characters.map(item => <option key={item.name}>{item.name}</option>)}</select></label>{(kind === 'image' || kind === 'video') && <label>角色参考图片 / 视频首帧<select value={referenceId} onChange={event => setReferenceId(event.target.value)}><option value="">不使用参考图片</option>{references.map(asset => <option key={asset.id} value={asset.id}>{asset.character ? `${asset.character} · ` : ''}{asset.name}</option>)}</select></label>}</div><label>生成提示或配音文本<textarea maxLength={2000} value={prompt} onChange={event => setPrompt(event.target.value)} placeholder="描述镜头，或输入需要朗读的对白" /></label></fieldset><p className="drama-source">{capabilities[kind]?.provider ?? '读取供应商配置…'}{capabilities[kind]?.model ? ` / ${capabilities[kind].model}` : ''} · {capabilities[kind]?.message ?? '尚未确认可用能力'}。参考图片仅由支持此能力的适配器发送；不支持时请求会被拒绝。跨镜头人物一致性仍无法保证。</p><button disabled={disabled || busy || uncertain || !capabilities[kind]?.ready || prompt.trim().length < 3} onClick={() => submit('/creative-jobs', false, {kind, expected_version: project.version, expected_provider: capabilities[kind].provider, expected_model: capabilities[kind].model ?? '', prompt: prompt.trim(), shot_id: shotId, character, ...((kind === 'image' || kind === 'video') && referenceId ? {reference_asset_id: referenceId} : {})})}>{capabilities[kind]?.mode === 'real' ? '提交真实生成（可能计费）' : kind === 'image' ? '生成合成演示图片' : kind === 'speech' ? '生成配音（供应商不可用）' : '生成视频（供应商不可用）'}</button>
    <div className="drama-history"><h3>创作任务记录</h3><ol>{jobs.map(job => <li key={job.id}><div><span>{job.kind} · {job.provider} · {job.status}</span>{(job.status === 'queued' || job.status === 'running') && <button disabled={busy || uncertain} onClick={() => submit(`/creative-jobs/${job.id}/cancel`, false, {})}>取消创作任务</button>}{job.kind === 'video' && job.upstream_id && (job.status === 'failed' || job.status === 'cancelled') && <button disabled={busy || uncertain} onClick={() => submit(`/creative-jobs/${job.id}/resume`, false, {})}>继续查询已有视频</button>}</div>{job.error && <p className="error">{job.error}</p>}</li>)}</ol></div>
    <details className="drama-history"><summary>剧本与分镜版本历史（{history.length}）</summary><p className="drama-help">恢复会产生新的当前版本，原历史保留；恢复剧本后，旧分镜可能过期。</p><ol>{history.map(item => <li key={item.id}><div><span>{item.kind === 'script' ? '剧本' : '分镜'} · 来源剧本版本 {item.script_version}</span><time>{new Date(item.created_at).toLocaleString('zh-CN')}</time></div><details><summary>查看历史内容</summary><pre>{JSON.stringify(item.script ?? item.storyboard, null, 2)}</pre></details><button disabled={disabled || busy || uncertain} onClick={() => setConfirm(item)}>恢复此{item.kind === 'script' ? '剧本' : '分镜'}版本</button></li>)}</ol>{history.length === 0 && <p className="drama-help">保存或生成新内容后可在此查看历史。</p>}</details>
    {confirm && <div className="editing-note"><p>确认恢复此{confirm.kind === 'script' ? '剧本' : '分镜'}？当前内容将被替换，历史保留。</p><div className="media-actions"><button disabled={busy || uncertain} onClick={() => setConfirm(null)}>取消恢复</button><button disabled={disabled || busy || uncertain} onClick={() => submit(`/history/${encodeURIComponent(confirm.id)}/restore`, true)}>确认恢复历史版本</button></div></div>}
  </section>
}
