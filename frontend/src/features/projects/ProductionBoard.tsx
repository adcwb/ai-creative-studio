import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError, baseURL, errorMessage, request } from '../../api'
import type { DramaProject } from '../../drama'
import { useI18n } from '../../i18n'
import { MaskEditor } from '../../components/MaskEditor'
import { RuntimeModelPicker } from '../../components/RuntimeModelPicker'
import { runtimeFields, runtimeReady, type RuntimeChoice } from '../../runtimeSelection'

interface Asset {id: string; kind: 'image' | 'video' | 'audio'; name: string; role: string; shot_id: string; file_url: string; duration: number}
interface Selection {id: string; shot_id: string; asset_id: string; note: string; created_at: string}
interface Review {assignee_id?: string; priority?: string; created_by?: string; updated_by?: string;id: string; asset_id: string; shot_id: string; time: number; text: string; resolved: boolean}
interface Media {uploaded_asset_id?: string; version: number; assets: Asset[]; selections?: Selection[]; reviews?: Review[]}
interface Job {reference_asset_ids?: string[]; first_frame_asset_id?: string; last_frame_asset_id?: string; motion_reference_asset_id?: string;id: string; kind: string; status: string; asset_id?: string; reference_asset_id?: string; shot_id?: string; prompt: string; error?: string; analysis?: {summary: string; questions: string[]; suggestions: string[]; provider: string; source_version: number}}
interface Capability {provider: string; model?: string; mode: string; ready: boolean; message?: string}
interface Attempt {path: string; body: BodyInit; mime: string; key: string; purpose: string}

export function ProductionBoard({project, space, onDirtyChange, disabled = false}: {project: DramaProject; space: string; onDirtyChange: (dirty: boolean) => void; disabled?: boolean}) {
  const {t, formatDate} = useI18n()
  const imageOnly = project.kind === 'image'
  const [media, setMedia] = useState<Media | null>(null)
  const [jobs, setJobs] = useState<Job[]>([])
  const [caps, setCaps] = useState<Record<string, Capability>>({})
  const [shot, setShot] = useState('')
  const [kind, setKind] = useState('image')
  const [prompt, setPrompt] = useState('')
  const [reference, setReference] = useState('')
  const [mask, setMask] = useState('')
  const [comparison, setComparison] = useState('')
  const [audio, setAudio] = useState('')
  const [character, setCharacter] = useState('')
  const [references, setReferences] = useState<string[]>([])
  const [firstFrame, setFirstFrame] = useState('')
  const [lastFrame, setLastFrame] = useState('')
  const [motion, setMotion] = useState('')
  const [controls, setControls] = useState<{max_references: number; frames: boolean; motion_reference: boolean; key: string} | null>(null)
  const [members, setMembers] = useState<{id: string; name: string}[]>([])
  const [assignee, setAssignee] = useState('')
  const [priority, setPriority] = useState('normal')
  const [reviewFilter, setReviewFilter] = useState('all')
  const [maskOpen, setMaskOpen] = useState(false)
  const [focus, setFocus] = useState('')
  const [compare, setCompare] = useState('')
  const [note, setNote] = useState('')
  const [reviewTime, setReviewTime] = useState(0)
  const [choice, setChoice] = useState<RuntimeChoice | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const pending = useRef<Attempt | null>(null)
  const flight = useRef(false)
  const alive = useRef(true)
  const path = `/projects/${encodeURIComponent(project.id)}`
  const query = new URLSearchParams({space_id: space}).toString()
  const storageKey = `production.pending:${space}:${project.id}`
  const locked = disabled || busy || uncertain
  const assets = media?.assets ?? []
  const selectedShot = project.storyboard?.shots.find(s => s.id === shot)
  const focused = assets.find(a => a.id === focus)
  const compared = assets.find(a => a.id === compare)
  const referenceAsset = assets.find(a => a.id === reference)
  const selections = (media?.selections ?? []).filter(s => s.shot_id === shot)
  const adopted = selections.at(-1)?.asset_id
  const advanced = references.length>0 || !!firstFrame || !!lastFrame || !!motion
  const creativeInput = {kind, expected_version: project.version, prompt: prompt.trim(), shot_id: shot, character, ...(references.length ? {reference_asset_ids: references} : {}), ...(firstFrame ? {first_frame_asset_id: firstFrame} : {}), ...(lastFrame ? {last_frame_asset_id: lastFrame} : {}), ...(motion ? {motion_reference_asset_id: motion} : {}), ...(kind === 'consistency' ? {comparison_asset_id: comparison} : {}), ...(kind === 'lip_sync' ? {audio_asset_id: audio} : {}), ...(reference ? {reference_asset_id: reference} : {}), ...(kind === 'image' && mask ? {mask_asset_id: mask} : {})}
  const preview = {operation: 'creative' as const, project_id: project.id, input: creativeInput}
  const cap: Capability | undefined = choice ? {provider: choice.model.provider, model: choice.model.model, mode: 'real', ready: runtimeReady(choice, preview)} : caps[kind]
  const provider = cap?.provider ?? '', model = cap?.model ?? ''
  const controlsCurrent = controls?.key === `${provider}:${model}:${kind}` ? controls : null
  const controlsValid = !advanced || (!!controlsCurrent && references.length<=controlsCurrent.max_references && (!firstFrame || controlsCurrent.frames) && (!lastFrame || (!!firstFrame && controlsCurrent.frames)) && (!motion || controlsCurrent.motion_reference) && !reference && !mask)
  const canGenerate = controlsValid && !!cap?.ready && runtimeReady(choice, preview) && prompt.trim().length >= 3 && (!mask || !!reference) && (!['vision', 'consistency', 'lip_sync'].includes(kind) || !!reference) && (kind !== 'consistency' || (!!comparison && comparison !== reference)) && (kind !== 'lip_sync' || !!audio)
  useEffect(() => {
    const controller = new AbortController()
    const params = new URLSearchParams({space_id: space, provider, model, kind})
    void request<{max_references: number; frames: boolean; motion_reference: boolean}>(`${path}/creative-controls?${params}`, {signal: controller.signal}).then(value => {if (!controller.signal.aborted) setControls({...value, key: `${provider}:${model}:${kind}`})}).catch(() => {if (!controller.signal.aborted) setControls(null)})
    return () => controller.abort()
  }, [path, space, provider, model, kind])
  useEffect(() => {const controller=new AbortController(); void request<{id:string;name:string}[]>(`${path}/media/review-members?${query}`, {signal:controller.signal}).then(setMembers).catch(() => {}); return () => controller.abort()}, [path,query])
  const load = useCallback(async () => {
    const [next, nextJobs] = await Promise.all([request<Media>(`${path}/media?${query}`), request<Job[]>(`${path}/creative-jobs?${query}`)])
    if (alive.current) {setMedia(next); setJobs(nextJobs)}
  }, [path, query])
  useEffect(() => {
    alive.current = true
    const controller = new AbortController()
    void request<Record<string, Capability>>('/creative-media-capabilities', {signal: controller.signal}).then(setCaps).catch(() => {})
    void load().catch(e => {if (alive.current) setError(errorMessage(e, t('素材读取失败')))})
    try {const saved = sessionStorage.getItem(storageKey); if (saved) {pending.current = JSON.parse(saved) as Attempt; setUncertain(true)}} catch { /* Recovery remains available during this mount. */ }
    const timer = setInterval(() => {if (!flight.current) void load().catch(() => {})}, 2500)
    return () => {alive.current = false; controller.abort(); clearInterval(timer)}
  }, [load, storageKey, t])
  useEffect(() => {onDirtyChange(busy || uncertain || !!prompt.trim() || !!note.trim() || maskOpen || advanced); return () => onDirtyChange(false)}, [busy, uncertain, prompt, note, maskOpen, advanced, onDirtyChange])
  async function execute(attempt: Attempt) {
    if (flight.current) return
    pending.current = attempt
    if (typeof attempt.body === 'string') try {sessionStorage.setItem(storageKey, JSON.stringify(attempt))} catch { /* In-memory idempotency still applies. */ }
    flight.current = true; setBusy(true); setError(''); setNotice('')
    try {
      const result = await request<Media | Job>(attempt.path, {method: 'POST', headers: {'Content-Type': attempt.mime, 'Idempotency-Key': attempt.key}, body: attempt.body})
      if (!alive.current) return
      pending.current = null; setUncertain(false)
      try {sessionStorage.removeItem(storageKey)} catch { /* No stored recovery. */ }
      if ('assets' in result) {
        setMedia(result)
        if (attempt.purpose === 'mask') setMask(result.uploaded_asset_id ?? '')
        if (attempt.purpose === 'upload') {const asset = result.assets.find(a => a.id === result.uploaded_asset_id); if (asset) {setFocus(asset.id); if (asset.kind === 'image') setReference(asset.id)}}
      }
      if (attempt.purpose === 'generate') setPrompt('')
      if (attempt.purpose === 'review' || attempt.purpose === 'select') setNote('')
      setNotice(t(attempt.purpose === 'select' ? '已采用此版本，剪辑保持不变。' : '操作已保存。'))
      await load()
    } catch (e) {
      if (!alive.current) return
      const unknown = pending.current !== null && (!(e instanceof ApiError) || e.status >= 500)
      setUncertain(unknown); setError(errorMessage(e, t('操作失败')))
      if (!unknown) {pending.current = null; try {sessionStorage.removeItem(storageKey)} catch { /* Optional storage. */ }}
    } finally {flight.current = false; if (alive.current) setBusy(false)}
  }
  function mutate(suffix: string, data: unknown, purpose: string) {
    if (locked) return
    void execute({path: `${path}${suffix}?${query}`, body: JSON.stringify(data), mime: 'application/json', key: crypto.randomUUID(), purpose})
  }
  function upload(file: Blob, name: string, purpose = 'upload') {
    if (locked) return
    const mime = file.type === 'audio/x-wav' ? 'audio/wav' : file.type
    if (!['image/png', 'image/jpeg', 'video/mp4', 'audio/wav', 'audio/mpeg'].includes(mime) || file.size > 32 * 1024 * 1024) {setError(t('请选择32MiB以内的PNG、JPEG、MP4、WAV或MP3')); return}
    const params = new URLSearchParams({space_id: space, filename: name, role: purpose === 'mask' ? 'mask' : 'shot', shot_id: shot})
    void execute({path: `${path}/media/uploads?${params}`, body: file, mime, key: crypto.randomUUID(), purpose})
  }
  function decision(action: string, extra: Record<string, unknown> = {}) {
    mutate('/media/decisions', {expected_version: media?.version, action, asset_id: focus, shot_id: shot, text: note.trim(), time: focused?.kind === 'image' ? 0 : reviewTime, review_id: '', resolved: false, ...(action === 'review' ? {assignee_id: assignee, priority} : {}), ...extra}, action)
  }
  function renderAsset(asset: Asset) {
    const url = `${baseURL}${asset.file_url}`
    return asset.kind === 'image' ? <img src={url} alt={asset.name} /> : asset.kind === 'video' ? <video controls preload="metadata" src={url} /> : <audio controls src={url} />
  }
  const visible = assets.filter(a => a.role !== 'mask' && (!shot || a.shot_id === shot))
  return <section className="production-board">
    <div className="production-toolbar"><div><h2>{t(imageOnly ? '图像编辑工作区' : '镜头制作')}</h2><p>{t('选择画面，继续修改、比较或采用。原始素材始终保留。')}</p></div>{!imageOnly && <Link className="button-link" to={`/projects/${project.id}/media`}>{t('进入剪辑与声音')}</Link>}</div>
    {error && <div role="alert" className="error">{error}<button disabled={busy} onClick={() => void (uncertain && pending.current ? execute(pending.current) : load().catch(e => setError(errorMessage(e, t('素材读取失败')))))}>{t(uncertain ? '重试同一请求' : '刷新素材')}</button></div>}
    {uncertain && !error && <div role="alert" className="editing-note">{t('上次请求结果尚未确认。')}<button disabled={busy} onClick={() => pending.current && void execute(pending.current)}>{t('重试同一请求')}</button></div>}
    {notice && <p className="notice" role="status">{notice}</p>}
    <div className="production-layout"><aside className="production-controls"><fieldset disabled={locked}>
      {!imageOnly && <label>{t('当前镜头')}<select aria-label={t('当前镜头')} value={shot} onChange={e => {setShot(e.target.value); setFocus(''); setCompare(''); setReference(''); setMask(''); setReferences([]); setFirstFrame(''); setLastFrame(''); setMotion('')}}><option value="">{t('全部素材 / 自由创作')}</option>{project.storyboard?.shots.map((s, i) => <option key={s.id} value={s.id}>{i + 1}. {s.description.slice(0, 35)}</option>)}</select></label>}
      {selectedShot && <div className="shot-context"><p>{selectedShot.description}</p>{selectedShot.purpose && <p>{t('叙事目的')}：{selectedShot.purpose}</p>}{selectedShot.performance && <p>{t('表演要求')}：{selectedShot.performance}</p>}<button type="button" onClick={() => setPrompt([selectedShot.description, selectedShot.purpose, selectedShot.performance, selectedShot.continuity].filter(Boolean).join('\n'))}>{t('使用镜头说明')}</button></div>}
      <label>{t('上传素材')}<input type="file" accept={imageOnly ? 'image/png,image/jpeg' : 'image/png,image/jpeg,video/mp4,audio/wav,audio/mpeg'} onChange={e => {const file = e.target.files?.[0]; if (file) upload(file, file.name); e.target.value = ''}} /></label>
      {!imageOnly && <label>{t('制作内容')}<select aria-label={t('制作内容')} value={kind} onChange={e => {setKind(e.target.value); setReferences([]); setFirstFrame(''); setLastFrame(''); setMotion(''); setChoice(null); setMask(''); setReference(''); setComparison(''); setAudio('')}}><option value="image">{t('分镜图片')}</option><option value="video">{t('镜头视频')}</option><option value="speech">{t('配音')}</option><option value="music">{t('器乐音乐')}</option><option value="sound_effect">{t('生成音效')}</option><option value="lip_sync">{t('口型同步')}</option><option value="vision">{t('图片创作分析')}</option><option value="consistency">{t('跨镜头外观检查')}</option></select></label>}
      {(['image', 'video', 'vision', 'consistency', 'lip_sync'].includes(kind)) && <label>{t('参考画面')}<select disabled={advanced} aria-label={t('参考画面')} value={reference} onChange={e => {setReference(e.target.value); setMask('')}}><option value="">{t('不使用参考图片')}</option>{assets.filter(a => a.role !== 'mask' && (kind === 'lip_sync' ? a.kind === 'video' : a.kind === 'image' || (kind === 'video' && cap?.provider === 'gemini-video' && a.kind === 'video'))).map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select></label>}
      {!imageOnly && !!project.script?.characters.length && <label>{t('生成角色关联')}<select aria-label={t('生成角色关联')} value={character} onChange={e => setCharacter(e.target.value)}><option value="">{t('不关联角色')}</option>{project.script.characters.map(c => <option key={c.name}>{c.name}</option>)}</select></label>}
      {kind === 'consistency' && <label>{t('待比较镜头图片')}<select aria-label={t('待比较镜头图片')} value={comparison} onChange={e => setComparison(e.target.value)}><option value="">{t('选择另一张图片')}</option>{assets.filter(a => a.kind === 'image' && a.role !== 'mask' && a.id !== reference).map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select></label>}
      {kind === 'lip_sync' && <label>{t('同步音频')}<select aria-label={t('同步音频')} value={audio} onChange={e => setAudio(e.target.value)}><option value="">{t('选择音频')}</option>{assets.filter(a => a.kind === 'audio').map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select></label>}
      {(kind === 'image' || kind === 'video') && <details className="reference-controls"><summary>{t('多参考与帧控制')}</summary><p>{t('参考图片按所选顺序传入；画面一致性仍需逐镜审核。')}</p>
        {!controlsCurrent?.max_references && <p>{t('当前模型尚未提供这些控制。')}</p>}
        {!!controlsCurrent?.max_references && <fieldset disabled={!!reference || !!mask}><legend>{t('多张参考图片')}</legend>{assets.filter(a => a.kind === 'image' && a.role !== 'mask').map(a => <label className="reference-choice" key={a.id}><input type="checkbox" checked={references.includes(a.id)} disabled={!references.includes(a.id) && references.length >= controlsCurrent.max_references} onChange={e => setReferences(previous => e.target.checked ? [...previous, a.id] : previous.filter(id => id !== a.id))} />{a.name}{references.includes(a.id) && <small>{references.indexOf(a.id)+1}</small>}</label>)}</fieldset>}
        {controlsCurrent?.frames && <><label>{t('首帧图片')}<select aria-label={t('首帧图片')} disabled={!!reference || !!mask} value={firstFrame} onChange={e => setFirstFrame(e.target.value)}><option value="">{t('不使用')}</option>{assets.filter(a=>a.kind==='image' && a.role!=='mask').map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label><label>{t('尾帧图片')}<select aria-label={t('尾帧图片')} disabled={!!reference || !!mask} value={lastFrame} onChange={e=>setLastFrame(e.target.value)}><option value="">{t('不使用')}</option>{assets.filter(a=>a.kind==='image' && a.role!=='mask').map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label></>}
        {controlsCurrent?.motion_reference && <label>{t('动作与外观参考视频')}<select aria-label={t('动作与外观参考视频')} disabled={!!reference || !!mask} value={motion} onChange={e=>setMotion(e.target.value)}><option value="">{t('不使用')}</option>{assets.filter(a=>a.kind==='video').map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label>}
        {advanced && <button onClick={()=>{setReferences([]);setFirstFrame('');setLastFrame('');setMotion('')}}>{t('清除高级参考')}</button>}
        {!controlsValid && <p role="status">{t('参考组合不可用：请检查当前模型、首尾帧与单参考设置。')}</p>}
      </details>}
      {referenceAsset && <div className="reference-thumbnail">{renderAsset(referenceAsset)}{kind === 'image' && <button type="button" onClick={() => setMaskOpen(true)}>{t('圈选修改区域')}</button>}{mask && <button type="button" onClick={() => setMask('')}>{t('清除修改区域')}</button>}</div>}
      <label>{t('创作或修改要求')}<textarea value={prompt} maxLength={2000} onChange={e => setPrompt(e.target.value)} placeholder={t('描述需要保留的内容，以及本次要改变的部分。')} /></label>
    </fieldset><details className="production-model"><summary>{t('生成模型与费用')}</summary><RuntimeModelPicker space={space} capability={kind === 'consistency' ? 'vision' : kind} preview={preview} value={choice} onChange={setChoice} disabled={locked} /></details>
    <button className="primary" disabled={locked || !canGenerate} onClick={() => mutate('/creative-jobs', {...creativeInput, ...runtimeFields(choice), expected_provider: cap?.provider, expected_model: cap?.model ?? ''}, 'generate')}>{t(kind === 'vision' ? '图片创作分析' : kind === 'consistency' ? '跨镜头外观检查' : kind === 'lip_sync' ? '口型同步' : reference ? '生成修改版本' : '生成候选')}</button>
    {cap?.ready && <p className="drama-help">{t(cap.mode === 'mock' ? '当前为本地合成演示，不是AI生成。' : '已接入生成服务，提交可能产生模型费用。')}</p>}
    {!cap?.ready && <p className="drama-help">{t('生成能力尚未就绪，可以先上传、比较和整理素材。')}</p>}
    <p className="drama-help">{t('每次生成一个候选。参考与局部编辑由所选模型能力决定，不支持的输入会被拒绝。')}</p>
    </aside><div className="production-stage">
      <div className={`candidate-view ${compared && compared.id !== focused?.id ? 'is-comparing' : ''}`}>{focused ? <figure>{renderAsset(focused)}<figcaption>{focused.name}{adopted === focused.id && <strong>{t('已采用')}</strong>}</figcaption></figure> : <div className="production-empty"><h3>{t('选择一个画面开始工作')}</h3><p>{t('上传素材或生成候选，然后在这里比较和修改。')}</p></div>}{compared && compared.id !== focused?.id && <figure>{renderAsset(compared)}<figcaption>{compared.name}<button onClick={() => setCompare('')}>{t('结束比较')}</button></figcaption></figure>}</div>
      {focused && <div className="production-actions"><button disabled={locked || focused.kind !== 'image'} onClick={() => {setReferences([]);setFirstFrame('');setLastFrame('');setMotion('');setReference(focused.id); setMask(''); setKind('image'); setChoice(null)}}>{t('继续编辑此图')}</button><button disabled={locked || focused.kind === 'audio' || adopted === focused.id} onClick={() => decision('select')}>{t('采用此版本')}</button><button disabled={locked} onClick={() => setCompare(focused.id)}>{t('设为比较基准')}</button><a className="button-link" href={`${baseURL}${focused.file_url}`} download>{t('下载素材')}</a></div>}
      {maskOpen && referenceAsset && <MaskEditor key={referenceAsset.id} url={`${baseURL}${referenceAsset.file_url}`} name={referenceAsset.name} disabled={locked} onSave={(blob, name) => upload(blob, name, 'mask')} onClose={() => setMaskOpen(false)} />}
      <div className="candidate-strip" aria-label={t('候选素材')}>{visible.map(a => <button type="button" className={focus === a.id ? 'is-selected' : ''} key={a.id} onClick={() => {setFocus(a.id); setReviewTime(0)}}>{a.kind === 'image' ? <img src={`${baseURL}${a.file_url}`} alt="" /> : <span>{t(a.kind === 'video' ? '视频' : '音频')}</span>}<span>{a.name}</span>{adopted === a.id && <strong>{t('已采用')}</strong>}</button>)}</div>
      {focused && <section className="production-review"><h3>{t('审片与返修')}</h3><fieldset disabled={locked}><label>{t('负责人')}<select aria-label={t('负责人')} value={assignee} onChange={e=>setAssignee(e.target.value)}><option value="">{t('未分配')}</option>{members.map(m=><option key={m.id} value={m.id}>{m.name}</option>)}</select></label><label>{t('优先级')}<select aria-label={t('优先级')} value={priority} onChange={e=>setPriority(e.target.value)}><option value="normal">{t('普通')}</option><option value="high">{t('高')}</option><option value="low">{t('低')}</option></select></label><label>{t('批注或采用理由')}<textarea value={note} maxLength={2000} onChange={e => setNote(e.target.value)} /></label>{focused.kind !== 'image' && <label>{t('素材时间点（秒）')}<input type="number" min="0" max={focused.duration} step="0.1" value={reviewTime} onChange={e => setReviewTime(Number(e.target.value))} /></label>}<button disabled={!note.trim() || !Number.isFinite(reviewTime)} onClick={() => decision('review')}>{t('添加返修批注')}</button></fieldset><label>{t('批注筛选')}<select aria-label={t('批注筛选')} value={reviewFilter} onChange={e=>setReviewFilter(e.target.value)}><option value="all">{t('全部')}</option><option value="open">{t('待返修')}</option><option value="resolved">{t('已处理')}</option><option value="unassigned">{t('未分配')}</option>{members.map(m=><option key={m.id} value={m.id}>{m.name}</option>)}</select></label><ul>{media?.reviews?.filter(r => r.asset_id === focused.id && (reviewFilter==='all' || (reviewFilter==='open' && !r.resolved) || (reviewFilter==='resolved' && r.resolved) || (reviewFilter==='unassigned' && !r.assignee_id) || r.assignee_id===reviewFilter)).map(r => <li key={r.id}><span>{r.time}s · {r.text}<small>{t('记录人')}：{members.find(m=>m.id===r.created_by)?.name ?? r.created_by ?? t('旧记录')} · {t(r.priority==='high'?'高':r.priority==='low'?'低':'普通')}</small></span><select aria-label={t('批注负责人')} disabled={locked} value={r.assignee_id ?? ''} onChange={e=>decision('assign',{review_id:r.id,assignee_id:e.target.value,priority:r.priority ?? 'normal'})}><option value="">{t('未分配')}</option>{r.assignee_id && !members.some(m=>m.id===r.assignee_id) && <option value={r.assignee_id}>{r.assignee_id}</option>}{members.map(m=><option key={m.id} value={m.id}>{m.name}</option>)}</select><select aria-label={t('批注优先级')} disabled={locked} value={r.priority ?? 'normal'} onChange={e=>decision('assign',{review_id:r.id,assignee_id:r.assignee_id ?? '',priority:e.target.value})}><option value="normal">{t('普通')}</option><option value="high">{t('高')}</option><option value="low">{t('低')}</option></select><button disabled={locked} onClick={() => decision('resolve', {review_id: r.id, resolved: !r.resolved})}>{t(r.resolved ? '重新打开批注' : '标记已处理')}</button><strong>{t(r.resolved ? '已处理' : '待返修')}</strong></li>)}</ul></section>}
      <details><summary>{t('采用与生成记录')}</summary><ul>{[...selections].reverse().map(s => <li key={s.id}>{assets.find(a => a.id === s.asset_id)?.name} · {s.note} · {formatDate(s.created_at)}</li>)}</ul><ul>{jobs.filter(j => !shot || j.shot_id === shot).map(j => <li key={j.id}>{t(j.status)} · {j.prompt}{j.reference_asset_id && <span> · {t('源素材')}：{assets.find(a => a.id === j.reference_asset_id)?.name}</span>}{(j.reference_asset_ids?.length || j.first_frame_asset_id || j.motion_reference_asset_id) ? <button disabled={locked} onClick={()=>{setKind(j.kind);setChoice(null);setReference('');setMask('');setReferences(j.reference_asset_ids ?? []);setFirstFrame(j.first_frame_asset_id ?? '');setLastFrame(j.last_frame_asset_id ?? '');setMotion(j.motion_reference_asset_id ?? '');setPrompt(j.prompt)}}>{t('复用参考与提示')}</button> : null}{j.analysis && <details><summary>{j.analysis.summary}</summary><p>{j.analysis.provider}</p><ul>{j.analysis.questions.map((q, i) => <li key={i}>{q}</li>)}{j.analysis.suggestions.map((q, i) => <li key={i}>{q}</li>)}</ul></details>}{j.error && <p className="error">{j.error}</p>}</li>)}</ul></details>
    </div></div>
  </section>
}
