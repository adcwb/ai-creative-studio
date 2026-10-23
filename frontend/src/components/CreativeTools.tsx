import { translate as t, useI18n } from "../i18n"
import { useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request } from '../api'
import type { DramaProject, DramaScript, StoryboardPlan } from '../drama'
interface Analysis { summary: string; questions: string[]; suggestions: string[]; provider: string; source_version: number }
interface CreativeJob {id: string; kind: string; status: string; analysis?: Analysis; error?: string; provider?: string; upstream_id?: string}
interface Capability {provider: string; model?: string; mode: string; ready: boolean; message: string}
interface Revision { id: string; kind: 'script' | 'storyboard'; created_at: string; script_version: number; script?: DramaScript; storyboard?: StoryboardPlan }
export function CreativeTools({project, space, disabled, onRestore, onDirtyChange, expectedProvider, expectedModel}: {project: DramaProject; space: string; disabled: boolean; onRestore: () => void; onDirtyChange: (dirty: boolean) => void; expectedProvider?: string; expectedModel?: string}) {
  const {formatDate} = useI18n()

  const [jobs, setJobs] = useState<CreativeJob[]>([])
  const [capabilities, setCapabilities] = useState<Record<string, Capability>>({})
  const [references, setReferences] = useState<{id: string; name: string; character: string; kind: string}[]>([])
  const [maskId, setMaskId] = useState('')
  const [audioId, setAudioId] = useState('')
  const [comparisonId, setComparisonId] = useState('')
  const [referenceId, setReferenceId] = useState('')
  const [kind, setKind] = useState('image')
  const [prompt, setPrompt] = useState('')
  const [shotId, setShotId] = useState('')
  const [character, setCharacter] = useState('')
  const analysisJob = jobs.find(job => (job.kind === 'analysis' || job.kind === 'vision' || job.kind === 'consistency') && job.status === 'succeeded')
  const analysis = analysisJob?.analysis
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
  useEffect(() => {const controller = new AbortController(); void request<Revision[]>(`${prefix}/history?${query}`, {signal: controller.signal}).then(setHistory).catch(failure => {if (!controller.signal.aborted) setError(errorMessage(failure, t("历史读取失败")))}); return () => controller.abort()}, [prefix, query, project.version])
  useEffect(() => {
    const controller = new AbortController()
    void request<Record<string, Capability>>('/creative-media-capabilities', {signal: controller.signal}).then(setCapabilities).catch(() => {})
    async function poll() {try {const result = await request<CreativeJob[]>(`${prefix}/creative-jobs?${query}`, {signal: controller.signal}); if (!controller.signal.aborted) setJobs(result); const media = await request<{assets: {id: string; name: string; character: string; kind: string}[]}>(`${prefix}/media?${query}`, {signal: controller.signal}); if (!controller.signal.aborted) setReferences(media.assets)} catch (failure) {if (!controller.signal.aborted) setError(errorMessage(failure, t("创作任务读取失败")))}}
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
    } catch (failure) {if (live.current) {const unknown = !(failure instanceof ApiError) || failure.status >= 500; setUncertain(unknown); setError(unknown ? t("结果尚未确认，请重试同一请求。") : errorMessage(failure, t("操作失败")))}} finally {flight.current = false; if (live.current) setBusy(false)}
  }
  function submit(suffix: string, restore: boolean, data?: unknown) {if (disabled || busy || uncertain) return; pending.current = {path: `${prefix}${suffix}?${query}`, body: JSON.stringify(data ?? {expected_version: project.version, ...(!restore && expectedProvider ? {expected_provider: expectedProvider, expected_model: expectedModel ?? ''} : {})}), key: crypto.randomUUID(), restore}; void execute()}
  return <section className="panel creative-tools"><div className="section-heading"><div><span className="eyebrow">STORY DEVELOPMENT</span><h2>{t("需求分析与版本历史")}</h2></div></div>
    <p className="drama-help">{t("基于已保存简报识别待澄清信息。回答后补充到项目简报并保存；分析建议不会自动覆盖你的内容。")}</p><button disabled={disabled || busy || uncertain} onClick={() => submit('/analysis', false)}>{busy ? t("处理中…") : t("分析已保存需求")}</button>
    {error && <div role="alert" className="error">{error}{uncertain && <button disabled={busy} onClick={() => void execute()}>{t("重试同一请求")}</button>}</div>}
    {analysis && <div className="creative-analysis"><p className="drama-source">{t("来源：")}{analysis.provider} {t("· 项目版本")}{analysis.source_version}{analysis.source_version !== project.version ? t(" · 当前项目已变化") : ''}</p><h3>{analysisJob?.kind === 'consistency' ? t("外观比较建议：") : analysisJob?.kind === 'vision' ? t("图片创作分析：") : ''}{analysis.summary}</h3><h4>{t("待澄清问题")}</h4><ul>{analysis.questions.map((question, i) => <li key={i}>{question}</li>)}</ul><h4>{t("创作建议")}</h4><ul>{analysis.suggestions.map((suggestion, i) => <li key={i}>{suggestion}</li>)}</ul></div>}
    <h3>{t("图像理解、镜头与配音")}</h3><p className="drama-help">{t("素材生成成功后加入下方媒体库。仅已配置可用供应商可提交；真实请求可能计费，失败或取消不代表上游未收费，不自动重试。已有视频任务可继续查询原结果，不重新提交生成；另建请求可能再次计费。")}</p>
    <fieldset disabled={disabled || busy || uncertain}><div className="storyboard-fields"><label>{t("创作类型")}<select value={kind} onChange={event => setKind(event.target.value)}><option value="image">{t("分镜图片")}</option><option value="video">{t("镜头视频")}</option><option value="speech">{t("配音")}</option><option value="music">{t("器乐音乐")}</option><option value="sound_effect">{t("生成音效")}</option><option value="lip_sync">{t("口型同步")}</option><option value="vision">{t("图片创作分析")}</option><option value="consistency">{t("跨镜头外观检查")}</option></select></label><label>{t("关联镜头")}<select value={shotId} onChange={event => setShotId(event.target.value)}><option value="">{t("不关联镜头")}</option>{project.storyboard?.shots.map((shot, i) => <option key={shot.id} value={shot.id}>{t("第")}{i + 1} {t("镜")}</option>)}</select></label><label>{t("生成角色关联")}<select value={character} onChange={event => setCharacter(event.target.value)}><option value="">{t("不关联角色")}</option>{project.script?.characters.map(item => <option key={item.name}>{item.name}</option>)}</select></label>{(kind === 'image' || kind === 'video' || kind === 'vision' || kind === 'consistency' || kind === 'lip_sync') && <label>{kind === 'vision' ? t("待分析图片（必选）") : t("角色参考图片 / 视频首帧")}<select value={referenceId} onChange={event => setReferenceId(event.target.value)}><option value="">{t("不使用参考图片")}</option>{references.filter(asset => kind === 'lip_sync' ? asset.kind === 'video' : asset.kind === 'image' || (kind === 'video' && capabilities.video?.provider === 'gemini-video')).map(asset => <option key={asset.id} value={asset.id}>{asset.character ? `${asset.character} · ` : ''}{asset.name}</option>)}</select></label>}{kind === 'image' && capabilities.image?.provider === 'openai-images' && <label>{t("局部编辑遮罩（可选）")}<select value={maskId} onChange={event => setMaskId(event.target.value)}><option value="">{t("不使用遮罩")}</option>{references.filter(asset => asset.kind === 'image' && asset.id !== referenceId).map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>}{kind === 'lip_sync' && <label>{t('同步音频')}<select value={audioId} onChange={event => setAudioId(event.target.value)}><option value="">{t('选择音频')}</option>{references.filter(asset => asset.kind === 'audio').map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>}{kind === 'consistency' && <label>{t("待比较镜头图片")}<select value={comparisonId} onChange={event => setComparisonId(event.target.value)}><option value="">{t("选择另一张图片")}</option>{references.filter(asset => asset.kind === 'image' && asset.id !== referenceId).map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>}</div><label>{t("生成提示或配音文本")}<textarea maxLength={2000} value={prompt} onChange={event => setPrompt(event.target.value)} placeholder={t("描述镜头，或输入需要朗读的对白")} /></label></fieldset><p className="drama-source">{capabilities[kind]?.provider ?? t("读取供应商配置…")}{capabilities[kind]?.model ? ` / ${capabilities[kind].model}` : ''} · {t(capabilities[kind]?.message ?? '尚未确认可用能力')}{t("。参考图片仅由支持此能力的适配器发送；不支持时请求会被拒绝。跨镜头检查仅提供服装、发型、配色和道具的建议，不判定人物身份或保证锁脸。遮罩须为与原图同尺寸PNG，透明区域表示修改；请在素材区绘制。Google视频可选择图片首帧或视频编辑，其他供应商仅支持已实现的图片参考。")}</p><button disabled={disabled || busy || uncertain || !capabilities[kind]?.ready || prompt.trim().length < 3 || ((kind === 'vision' || kind === 'consistency' || kind === 'lip_sync' || (kind === 'image' && !!maskId)) && !referenceId) || (kind === 'consistency' && (!comparisonId || comparisonId === referenceId)) || (kind === 'lip_sync' && !audioId)} onClick={() => submit('/creative-jobs', false, {kind, expected_version: project.version, expected_provider: capabilities[kind].provider, expected_model: capabilities[kind].model ?? '', prompt: prompt.trim(), shot_id: shotId, character, ...((kind === 'image' || kind === 'video' || kind === 'vision' || kind === 'consistency' || kind === 'lip_sync') && referenceId ? {reference_asset_id: referenceId} : {}), ...(kind === 'image' && maskId && capabilities.image?.provider === 'openai-images' ? {mask_asset_id: maskId} : {}), ...(kind === 'consistency' ? {comparison_asset_id: comparisonId} : {}), ...(kind === 'lip_sync' ? {audio_asset_id: audioId} : {})})}>{capabilities[kind]?.mode === 'real' ? t("提交真实生成（可能计费）") : kind === 'image' ? t("生成合成演示图片") : kind === 'sound_effect' ? t('生成音效（适配器不可用）') : kind === 'music' ? t('生成音乐（供应商不可用）') : kind === 'lip_sync' ? t('口型同步（适配器不可用）') : kind === 'speech' ? t("生成配音（供应商不可用）") : kind === 'consistency' ? t("比较外观（供应商不可用）") : kind === 'vision' ? t("分析图片（供应商不可用）") : t("生成视频（供应商不可用）")}</button>
    <div className="drama-history"><h3>{t("创作任务记录")}</h3><ol>{jobs.map(job => <li key={job.id}><div><span>{job.kind} · {job.provider} · {job.status}</span>{(job.status === 'queued' || job.status === 'running') && <button disabled={busy || uncertain} onClick={() => submit(`/creative-jobs/${job.id}/cancel`, false, {})}>{t("取消创作任务")}</button>}{(job.kind === 'video' || (job.kind === 'image' && job.provider === 'jimeng-images')) && job.upstream_id && (job.status === 'failed' || job.status === 'cancelled') && <button disabled={busy || uncertain} onClick={() => submit(`/creative-jobs/${job.id}/resume`, false, {})}>{job.kind === 'image' ? t("继续查询已有图像") : t("继续查询已有视频")}</button>}</div>{job.error && <p className="error">{job.error}</p>}</li>)}</ol></div>
    <details className="drama-history"><summary>{t('剧本与分镜版本历史（{count}）', {count: history.length})}</summary><p className="drama-help">{t("恢复会产生新的当前版本，原历史保留；恢复剧本后，旧分镜可能过期。")}</p><ol>{history.map(item => <li key={item.id}><div><span>{item.kind === 'script' ? t("剧本") : t("分镜")} {t("· 来源剧本版本")}{item.script_version}</span><time>{formatDate(item.created_at)}</time></div><details><summary>{t("查看历史内容")}</summary><pre>{JSON.stringify(item.script ?? item.storyboard, null, 2)}</pre></details><button disabled={disabled || busy || uncertain} onClick={() => setConfirm(item)}>{t('恢复此{kind}版本', {kind: item.kind === 'script' ? t('剧本') : t('分镜')})}</button></li>)}</ol>{history.length === 0 && <p className="drama-help">{t("保存或生成新内容后可在此查看历史。")}</p>}</details>
    {confirm && <div className="editing-note"><p>{t('确认恢复此{kind}？当前内容将被替换，历史保留。', {kind: confirm.kind === 'script' ? t('剧本') : t('分镜')})}</p><div className="media-actions"><button disabled={busy || uncertain} onClick={() => setConfirm(null)}>{t("取消恢复")}</button><button disabled={disabled || busy || uncertain} onClick={() => submit(`/history/${encodeURIComponent(confirm.id)}/restore`, true)}>{t("确认恢复历史版本")}</button></div></div>}
  </section>
}
