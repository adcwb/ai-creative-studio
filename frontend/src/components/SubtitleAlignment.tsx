export interface SubtitleCue { start: number; end: number; text: string }
export interface AlignmentTask {
  id: string; status: string; mode: 'estimate' | 'asr'; provider: string; model?: string; source_version: number
  clip_id: string; audio_asset_id: string; duration: number; cues: SubtitleCue[]; warnings: string[]; error?: string
}
export interface AlignmentCapability { provider: string; model?: string; mode: string; ready: boolean; message: string }
export interface SubtitleFields { subtitle_cues?: SubtitleCue[]; subtitle_source?: 'manual' | 'estimated' | 'asr'; subtitle_alignment_id?: string }
export function SubtitleAlignment({index, clip, tasks, capabilities, locked, dirty, version, onChange, onCreate, onCancel, onApply}: {
  index: number; clip: SubtitleFields & {id: string; duration: number; subtitle: string; audio_asset_id: string}; tasks: AlignmentTask[]
  capabilities: Partial<Record<'estimate' | 'asr', AlignmentCapability>>; locked: boolean; dirty: boolean; version: number
  onChange: (patch: SubtitleFields) => void; onCreate: (mode: 'estimate' | 'asr') => void; onCancel: (task: AlignmentTask) => void; onApply: (task: AlignmentTask) => void
}) {
  const cues = clip.subtitle_cues ?? []
  function change(cues: SubtitleCue[]) {onChange({subtitle_cues: cues, subtitle_source: 'manual'})}
  function edit(index: number, patch: Partial<SubtitleCue>) {change(cues.map((cue, i) => i === index ? {...cue, ...patch} : cue))}
  const invalid = cues.reduce((total, cue) => total + [...cue.text.trim()].length, 0) > 2000 || cues.some((cue, i) => !Number.isFinite(cue.start) || !Number.isFinite(cue.end) || cue.start < 0 || cue.start >= cue.end || cue.end > clip.duration || !cue.text.trim() || (i > 0 && cue.start < cues[i - 1].end))
  const active = tasks.some(task => task.status === 'queued' || task.status === 'running')
  return <section className="subtitle-alignment" aria-label={`片段 ${index + 1} 逐句字幕`}>
    <h4>逐句字幕与对白对齐</h4>
    <p className="drama-help">时间相对本片段起点，以秒为单位；逐句字幕存在时优先于整镜字幕。修改配音或片段时长后，请重新检查时间。</p>
    <p className="drama-source">字幕来源：{clip.subtitle_source === 'asr' ? '语音识别结果（需人工校正）' : clip.subtitle_source === 'estimated' ? '按文本估算 · 未识别音频' : '人工编辑'}{clip.subtitle_alignment_id && ` · 来源任务 ${clip.subtitle_alignment_id}`}</p>
    <div className="subtitle-cues">{cues.map((cue, cueIndex) => <div className="subtitle-cue" key={cueIndex}>
      <div className="storyboard-fields"><label>第 {cueIndex + 1} 句开始（秒）<input aria-label={`片段 ${index + 1} 第 ${cueIndex + 1} 句开始`} type="number" min="0" max={clip.duration} step="0.01" value={cue.start} onChange={event => edit(cueIndex, {start: Number(event.target.value)})} /></label><label>结束（秒）<input aria-label={`片段 ${index + 1} 第 ${cueIndex + 1} 句结束`} type="number" min="0" max={clip.duration} step="0.01" value={cue.end} onChange={event => edit(cueIndex, {end: Number(event.target.value)})} /></label></div>
      <label>第 {cueIndex + 1} 句文本<textarea aria-label={`片段 ${index + 1} 第 ${cueIndex + 1} 句文本`} maxLength={300} value={cue.text} onChange={event => edit(cueIndex, {text: event.target.value})} /></label><button type="button" disabled={locked} onClick={() => change(cues.filter((_, i) => i !== cueIndex))}>删除片段 {index + 1} 第 {cueIndex + 1} 句</button>
    </div>)}</div>
    {invalid && <p role="status" className="drama-validation">每句需要非空文本，起止时间须在片段范围内，按时间顺序排列且不能重叠，每镜合计不超过2000字。保存前请修正。</p>}
    <div className="media-actions"><button type="button" disabled={locked || cues.length >= 60 || (cues.at(-1)?.end ?? 0) >= clip.duration} onClick={() => {const start = cues.at(-1)?.end ?? 0; change([...cues, {start, end: Math.min(clip.duration, start + 1), text: '新对白'}])}}>添加片段 {index + 1} 字幕句</button>{cues.length > 0 && <button type="button" disabled={locked} onClick={() => onChange({subtitle_cues: [], subtitle_source: 'manual', subtitle_alignment_id: ''})}>清空片段 {index + 1} 逐句字幕</button>}</div>
    <div className="subtitle-methods"><div><strong>按文本估算时间</strong><p className="drama-help">按整镜字幕的句子与长度分配时长，不读取或识别音频，不代表实际对白时间。</p><button type="button" disabled={locked || dirty || active || !capabilities.estimate?.ready || !clip.subtitle.trim()} onClick={() => onCreate('estimate')}>估算片段 {index + 1} 对白时间</button></div><div><strong>从实际配音识别</strong><p className="drama-help">{capabilities.asr?.message ?? '正在检查语音识别配置'}{capabilities.asr?.model && ` · ${capabilities.asr.model}`}。使用本片段已保存的配音素材，供应商可能收费；真实识别效果与费用未验证。</p><button type="button" disabled={locked || dirty || active || !capabilities.asr?.ready || !clip.audio_asset_id} onClick={() => onCreate('asr')}>识别片段 {index + 1} 配音并对齐</button></div></div>
    {dirty && <p className="drama-help">先保存时间轴，再创建新的对齐任务。</p>}
    {tasks.map(task => <div className="alignment-task" key={task.id}><p>{task.mode === 'estimate' ? '按文本估算 · 未识别音频' : '实际音频识别'} · {task.provider}{task.model ? ` / ${task.model}` : ''} · {task.status} · 来源媒体版本 {task.source_version}</p>{task.error && <p className="error">{task.error}</p>}{(task.warnings ?? []).map((warning, i) => <p className="drama-help" key={i}>{warning}</p>)}{task.source_version !== version && <p className="drama-validation">来源版本已变化，请以最新保存时间轴重新创建对齐任务。</p>}<div className="media-actions">{task.status === 'succeeded' && <button type="button" disabled={locked || task.source_version !== version} onClick={() => onApply(task)}>应用至片段 {index + 1} 字幕草稿</button>}{(task.status === 'queued' || task.status === 'running') && <button type="button" disabled={locked} onClick={() => onCancel(task)}>取消片段 {index + 1} 对齐任务</button>}</div></div>)}
  </section>
}
