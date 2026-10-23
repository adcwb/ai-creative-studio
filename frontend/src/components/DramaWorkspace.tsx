import { translate, useI18n } from '../i18n'
import { useCallback, useEffect, useRef, useState } from 'react'
import { errorMessage, localizedError, request } from '../api'
import { generationLabels, storyboardGenerationLabels, projectDraftError, type DramaProject } from '../drama'
import { useDramaProject } from '../hooks/useDramaProject'
import { ConfirmDialog } from './ConfirmDialog'
import { DramaScriptEditor } from './DramaScriptEditor'
import { GenerationHistory } from './GenerationHistory'
import { StoryboardEditor } from './StoryboardEditor'
import { MediaStudio } from './MediaStudio'
import { CreativeTools } from './CreativeTools'
import { Icon } from './Icon'

export function DramaWorkspace({space, onDirtyChange, requestLeave}: {
  space: string; onDirtyChange: (dirty: boolean) => void; requestLeave: (action: () => void) => void
}) {
  const {formatDate} = useI18n()
  const [projects, setProjects] = useState<DramaProject[]>([])
  const [selection, setSelection] = useState('loading')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const mutation = useRef(0)
  useEffect(() => {
    const controller = new AbortController()
    const current = mutation.current
    void request<DramaProject[]>(`/projects?${new URLSearchParams({space_id: space})}`, {signal: controller.signal})
      .then(result => {
        if (controller.signal.aborted) return
        if (current === mutation.current) setProjects(result)
        setSelection(previous => previous === 'loading' ? result[0]?.id ?? 'new' : previous)
        setError(''); setLoading(false)
      }).catch(failure => {if (!controller.signal.aborted) {setError(errorMessage(failure, translate("项目列表读取失败"))); setLoading(false)}})
    return () => controller.abort()
  }, [space, revision])
  const updateProject = useCallback((project: DramaProject) => {
    mutation.current++
    setProjects(previous => [project, ...previous.filter(item => item.id !== project.id)].sort((a, b) => b.updated_at.localeCompare(a.updated_at) || a.id.localeCompare(b.id)))
    setSelection(previous => previous === 'new' ? project.id : previous)
  }, [])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])
  return <div className="drama-workspace">
    <section className="intro"><div><div className="eyebrow">A STORY STARTS WITH ONE IDEA</div><h1>{translate("让故事，从这里开场")}<span>。</span></h1><p>{translate("记录一个想法，整理成三个场景，再打磨对白与分镜计划。")}</p></div><div className="intro-count"><strong>{String(projects.length).padStart(2, '0')}</strong><span>{translate("个短剧项目")}</span></div></section>
    <div className="banner drama-banner"><span className="banner-icon"><Icon name="script" /></span><div><strong>{translate("短剧创作与本地成片")}</strong><p>{translate("默认使用程序模拟创作；实际来源见生成记录。本地素材、时间轴与成片导出可直接使用；合成演示不是模型效果验证。")}</p><p>{translate("演示模式不提供权限隔离；安全模式按组织授权。请勿在测试素材中输入敏感信息。")}</p></div></div>
    <div className="drama-layout">
      <section className="panel drama-projects" aria-labelledby="projects-heading"><div className="section-heading"><div><span className="eyebrow">YOUR STORIES</span><h2 id="projects-heading">{translate("短剧项目")}</h2></div><span className="count">{projects.length}</span></div><button type="button" className="primary drama-new" disabled={selection === 'new'} onClick={() => requestLeave(() => setSelection('new'))}>{translate("新建短剧项目")}<span aria-hidden="true">＋</span></button>
        {loading && <p role="status" className="drama-help">{translate("正在读取项目列表…")}</p>}
        {error && <div className="error" role="alert">{error}<button type="button" onClick={() => {setLoading(true); setRevision(value => value + 1)}}>{translate("重新加载项目列表")}</button></div>}
        {!loading && !error && projects.length === 0 && <p className="drama-help">{translate("还没有项目。给故事起个名字，保存第一份简报。")}</p>}
        <div className="drama-project-list">{projects.map(project => <button type="button" key={project.id} aria-current={selection === project.id ? 'page' : undefined} onClick={() => {if (selection !== project.id) requestLeave(() => setSelection(project.id))}}><span className="drama-project-title">{project.title}</span><span className="drama-project-meta">{translate("版本")}{project.version} · {project.latest_generation ? translate(generationLabels[project.latest_generation.status]) : translate("等待第一份剧本")}</span><time>{formatDate(project.updated_at, {dateStyle: 'medium'})}</time></button>)}</div>
      </section>
      {selection === 'loading' ? <div className="panel loading-block">{translate("选择项目后开始创作")}</div> : <ProjectEditor key={`${space}:${selection}`} space={space} id={selection === 'new' ? null : selection} onProject={updateProject} onDirtyChange={onDirtyChange} requestLeave={requestLeave} />}
    </div>
  </div>
}

function ProjectEditor({space, id, onProject, onDirtyChange, requestLeave}: {
  space: string; id: string | null; onProject: (project: DramaProject) => void
  onDirtyChange: (dirty: boolean) => void; requestLeave: (action: () => void) => void
}) {
  useI18n()
  const [mediaDirty, setMediaDirty] = useState(false)
  const [toolsDirty, setToolsDirty] = useState(false)
  const [creative, setCreative] = useState<{provider: string; model?: string; mode: string; ready: boolean; message: string} | null>(null)
  useEffect(() => {const controller = new AbortController(); void request<{provider: string; model?: string; mode: string; ready: boolean; message: string}>('/creative-capabilities', {signal: controller.signal}).then(setCreative).catch(() => {if (!controller.signal.aborted) setCreative({provider: 'unavailable', mode: 'unavailable', ready: false, message: translate("无法读取创作配置，请刷新页面后重试。")})}); return () => controller.abort()}, [])
  const editor = useDramaProject(space, id, onProject, creative?.provider, creative?.model)
  const allDirty = editor.dirty || mediaDirty || toolsDirty
  const [confirmGenerate, setConfirmGenerate] = useState(false)
  const [confirmStoryboard, setConfirmStoryboard] = useState(false)
  const active = editor.project?.latest_generation?.status === 'queued' || editor.project?.latest_generation?.status === 'running'
  const storyboardGeneration = editor.project?.latest_storyboard_generation
  const storyboardActive = storyboardGeneration?.status === 'queued' || storyboardGeneration?.status === 'running'
  const generation = editor.project?.latest_generation
  const validation = projectDraftError(editor.draft)
  useEffect(() => {onDirtyChange(allDirty); return () => onDirtyChange(false)}, [allDirty, onDirtyChange])
  useEffect(() => {
    if (!allDirty) return
    const warn = (event: BeforeUnloadEvent) => {event.preventDefault(); event.returnValue = ''}
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [allDirty])
  function generate() {
    if (editor.project?.script) setConfirmGenerate(true)
    else editor.submit('generate')
  }
  return <div className="drama-editor">
    {editor.loadError && <div className="error" role="alert">{translate("无法同步项目：")}{editor.loadError}<button type="button" onClick={() => editor.reload()}>{translate("重试读取项目")}</button></div>}
    {editor.loading && !editor.project && <div className="panel loading-block" role="status">{translate("正在读取项目详情…")}</div>}
    {(!id || editor.project) && <>
      <section className="panel drama-brief" aria-labelledby="brief-heading"><div className="section-heading"><div><span className="eyebrow">01 / THE STARTING POINT</span><h2 id="brief-heading">{id ? translate("项目简报") : translate("新建短剧项目")}</h2></div><span>{id ? translate("版本 {p0}", {p0: editor.baseline?.version ?? '—'}) : translate("先保存，再生成")}</span></div>
        <form onSubmit={event => {event.preventDefault(); editor.submit(id ? 'brief' : 'create')}}>
          <fieldset disabled={editor.locked || editor.editingScript || editor.editingStoryboard}><label htmlFor="project-title">{translate("项目名称")}</label><input id="project-title" value={editor.draft.title} onChange={event => editor.setDraft({...editor.draft, title: event.target.value})} placeholder={translate("给这个故事起个名字")} />
            <div className="drama-field-title"><label htmlFor="project-brief">{translate("创作简报")}</label><span>{translate("3–2000 字")}</span></div><textarea id="project-brief" value={editor.draft.brief} onChange={event => editor.setDraft({...editor.draft, brief: event.target.value})} placeholder={translate("写下人物关系、冲突和期待的结局。简报会成为本次生成的依据。")} /><div className="field-footer"><span>{translate("请勿输入敏感信息")}</span><span>{[...editor.draft.brief.trim()].length} / 2000</span></div>
          </fieldset>
          {validation && editor.briefDirty && <p className="drama-validation" role="status">{validation}</p>}
          <div className="drama-brief-actions"><span>{editor.briefDirty ? translate("有未保存的简报修改") : id ? translate("简报已保存") : translate("建立你的第一个故事起点")}</span><div>{id && editor.briefDirty && <button type="button" disabled={editor.locked || editor.editingScript || editor.editingStoryboard} onClick={editor.cancelBrief}>{translate("取消简报修改")}</button>}<button className="primary" type="submit" disabled={editor.locked || editor.editingScript || editor.editingStoryboard || Boolean(validation) || (Boolean(id) && !editor.briefDirty)}>{editor.busy === 'create' || editor.busy === 'brief' ? translate("正在保存…") : id ? translate("保存项目简报") : translate("创建项目")}</button></div></div>
        </form>
      </section>
      {editor.actionError && <div className="error drama-action-error" role="alert">{editor.actionError}{editor.uncertain && <button type="button" disabled={Boolean(editor.busy)} onClick={editor.retry}>{editor.busy ? translate("正在重试…") : translate("重试上次请求")}</button>}{editor.conflict && <div><p>{translate("本地草稿已保留。重新读取会载入服务端最新内容并放弃本地修改。")}</p><button type="button" onClick={() => requestLeave(() => editor.reload(true))}>{translate("重新读取最新项目")}</button></div>}</div>}
      {editor.notice && <p className="notice" role="status"><Icon name="check" />{translate(editor.notice)}</p>}
      {editor.busy && <p className="notice" role="status">{editor.busy === 'generate' || editor.busy === 'generateStoryboard' ? translate("正在提交生成请求…") : translate("正在提交保存请求…")}{translate("请等待服务端确认。")}</p>}
      {id && editor.project && <>
        <CreativeTools expectedProvider={creative?.provider} expectedModel={creative?.model} project={editor.project} space={space} disabled={editor.locked || editor.dirty || mediaDirty} onRestore={() => editor.reload(true)} onDirtyChange={setToolsDirty} />
        {creative && <p className="drama-help">{translate("创作模式：")}{creative.provider}{creative.model ? ` / ${creative.model}` : ''} · {translate(creative.message)} {creative.mode === 'real' && translate("真实模式可能计费，模型效果与费用未验证。")}</p>}
        <section className="panel drama-generation" aria-labelledby="generation-heading"><div className="section-heading"><div><span className="eyebrow">02 / A FIRST DRAFT</span><h2 id="generation-heading">{creative?.mode === 'real' ? translate("生成 AI 剧本") : translate("生成模拟剧本")}</h2></div><span>{translate("2 位角色 / 3 个场景")}</span></div>
          <p>{translate("根据已保存简报，由后端生成结构化剧本；成功后替换当前稿，失败会保留已有剧本。")}</p>
          {generation && <div className="drama-generation-state"><span className={`status ${generation.status}`}><span />{translate(generationLabels[generation.status])}</span>{active && <div className="progress-block"><progress aria-label={translate("剧本生成进度")} value={generation.progress} max="100" /><span>{generation.progress}{translate("% · 后端处理中，自动更新")}</span></div>}{generation.error && <div className="error" role="alert">{localizedError(generation.error, generation.error_code)}{generation.error_code && <code>（{generation.error_code}）</code>}<p>{translate("已有剧本不会被覆盖。调整并保存简报后，可以再次生成。")}</p></div>}</div>}
          {(editor.briefDirty || editor.editingScript || editor.editingStoryboard) && <p className="drama-help">{translate("请先保存或取消当前编辑，再生成剧本。")}</p>}
          <button type="button" className="primary" disabled={editor.locked || !creative?.ready || editor.briefDirty || editor.editingScript || editor.editingStoryboard || Boolean(editor.loadError)} onClick={generate}><Icon name="spark" />{active ? translate("剧本生成中…") : editor.busy === 'generate' ? translate("正在提交…") : editor.project.script ? translate("重新生成剧本") : creative?.mode === 'real' ? translate("生成 AI 剧本（可能计费）") : translate("生成模拟剧本")}</button>
          <GenerationHistory space={space} projectId={id} revision={editor.historyRevision} />
        </section>
        {editor.project.script_brief && editor.project.script_brief !== editor.project.brief && <div className="editing-note drama-source-note"><strong>{translate("当前剧本来自之前的简报")}</strong><p>{translate("项目需求已更新，现有剧本仍保留原来的内容。重新生成成功后才会替换。")}</p><details><summary>{translate("查看剧本来源简报")}</summary><p>{editor.project.script_brief}</p></details></div>}
        {editor.script ? <DramaScriptEditor provider={editor.project.script_provider} script={editor.script} editing={editor.editingScript} disabled={editor.locked || editor.briefDirty || editor.editingStoryboard || Boolean(editor.loadError)} dirty={editor.scriptDirty} onChange={editor.setScript} onEdit={() => editor.setEditingScript(true)} onCancel={editor.cancelScript} onSave={() => editor.submit('script')} /> : <section className="panel drama-no-script"><Icon name="script" /><h2>{translate("故事在等一个开场")}</h2><p>{translate("保存项目简报后，生成第一份模拟剧本。你可以继续编辑场景与对白。")}</p></section>}
        <section className="panel drama-generation storyboard-generation" aria-labelledby="storyboard-generation-heading"><div className="section-heading"><div><span className="eyebrow">03 / FROM SCENES TO SHOTS</span><h2 id="storyboard-generation-heading">{creative?.mode === 'real' ? translate("生成 AI 分镜") : translate("生成模拟分镜")}</h2></div><span>{creative?.provider ?? translate("读取创作配置…")}</span></div>
          <p>{translate("从已保存的剧本生成镜头计划，再编辑画面、景别、运镜、时长和对白。生成成功替换当前计划，失败保留旧计划。")}</p>
          {!editor.project.script && <p className="drama-help">{translate("还没有已保存剧本。请先生成剧本，再创建分镜计划。")}</p>}
          {storyboardGeneration && <div className="drama-generation-state"><span className={`status ${storyboardGeneration.status}`}><span />{translate(storyboardGenerationLabels[storyboardGeneration.status])}</span>{storyboardActive && <div className="progress-block"><progress aria-label={translate("分镜生成进度")} value={storyboardGeneration.progress} max="100" /><span>{storyboardGeneration.progress}{translate("% · 后端处理中，自动更新")}</span></div>}{storyboardGeneration.error && <div className="error" role="alert">{localizedError(storyboardGeneration.error, storyboardGeneration.error_code)}{storyboardGeneration.error_code && <code>（{storyboardGeneration.error_code}）</code>}<p>{translate("旧分镜计划仍保留。检查并保存剧本后，可以再次生成。")}</p></div>}</div>}
          {(editor.briefDirty || editor.editingScript || editor.editingStoryboard) && <p className="drama-help">{translate("请先保存或取消当前编辑，再生成分镜。")}</p>}
          <button type="button" className="primary" disabled={editor.locked || !creative?.ready || !editor.project.script || editor.briefDirty || editor.editingScript || editor.editingStoryboard || Boolean(editor.loadError)} onClick={() => {if (editor.project?.storyboard) setConfirmStoryboard(true); else editor.submit('generateStoryboard')}}><Icon name="spark" />{storyboardActive ? translate("分镜生成中…") : editor.busy === 'generateStoryboard' ? translate("正在提交分镜…") : editor.project.storyboard ? translate("重新生成分镜") : creative?.mode === 'real' ? translate("生成 AI 分镜（可能计费）") : translate("生成模拟分镜")}</button>
        </section>
        {editor.project.storyboard ? <StoryboardEditor provider={editor.project.storyboard.provider} shots={editor.shots} sourceVersion={editor.baseline?.storyboard?.source_script_version ?? editor.project.storyboard.source_script_version} stale={(editor.baseline?.storyboard?.source_script_version ?? editor.project.storyboard.source_script_version) !== editor.project.script_version} editing={editor.editingStoryboard} disabled={editor.locked || editor.briefDirty || editor.editingScript || Boolean(editor.loadError)} dirty={editor.storyboardDirty} onChange={editor.setShots} onEdit={() => editor.setEditingStoryboard(true)} onCancel={editor.cancelStoryboard} onSave={() => editor.submit('storyboard')} /> : editor.project.script && <section className="panel drama-no-script storyboard-empty"><Icon name="script" /><h2>{translate("还没有分镜计划")}</h2><p>{translate("剧本已保存。生成模拟分镜后，可以逐镜编辑并保存。")}</p></section>}
        <MediaStudio project={editor.project} space={space} onDirtyChange={setMediaDirty} />
      </>}
    </>}
    {confirmStoryboard && <ConfirmDialog title={translate("替换当前分镜计划？")} message={translate("新的模拟分镜生成成功后，会替换当前已保存的分镜计划；旧计划可在版本历史中恢复。生成失败时，当前计划保持不变。")} confirmLabel={translate("确认重新生成分镜")} onCancel={() => setConfirmStoryboard(false)} onConfirm={() => {setConfirmStoryboard(false); editor.submit('generateStoryboard')}} />}
    {confirmGenerate && <ConfirmDialog title={translate("替换当前剧本？")} message={translate("新的模拟剧本生成成功后，会替换当前已保存的剧本；旧稿可在版本历史中恢复。生成失败时，当前稿保持不变；成功后已有分镜会标记为过期。")} confirmLabel={translate("确认重新生成")} onCancel={() => setConfirmGenerate(false)} onConfirm={() => {setConfirmGenerate(false); editor.submit('generate')}} />}
  </div>
}
