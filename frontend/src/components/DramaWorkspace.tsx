import { useCallback, useEffect, useRef, useState } from 'react'
import { errorMessage, request } from '../api'
import { generationActive, generationLabels, projectDraftError, type DramaProject } from '../drama'
import { useDramaProject } from '../hooks/useDramaProject'
import { ConfirmDialog } from './ConfirmDialog'
import { DramaScriptEditor } from './DramaScriptEditor'
import { GenerationHistory } from './GenerationHistory'
import { Icon } from './Icon'

export function DramaWorkspace({space, onDirtyChange, requestLeave}: {
  space: string; onDirtyChange: (dirty: boolean) => void; requestLeave: (action: () => void) => void
}) {
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
      }).catch(failure => {if (!controller.signal.aborted) {setError(errorMessage(failure, '项目列表读取失败')); setLoading(false)}})
    return () => controller.abort()
  }, [space, revision])
  const updateProject = useCallback((project: DramaProject) => {
    mutation.current++
    setProjects(previous => [project, ...previous.filter(item => item.id !== project.id)].sort((a, b) => b.updated_at.localeCompare(a.updated_at) || a.id.localeCompare(b.id)))
    setSelection(previous => previous === 'new' ? project.id : previous)
  }, [])
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])
  return <div className="drama-workspace">
    <section className="intro"><div><div className="eyebrow">A STORY STARTS WITH ONE IDEA</div><h1>让故事，从这里开场<span>。</span></h1><p>记录一个想法，整理成三个场景，再把对白慢慢打磨。</p></div><div className="intro-count"><strong>{String(projects.length).padStart(2, '0')}</strong><span>个短剧项目</span></div></section>
    <div className="banner drama-banner"><span className="banner-icon"><Icon name="script" /></span><div><strong>模拟剧本工作台 · mock-script</strong><p>剧本由后端程序生成，非真实 AI 创作；不调用付费模型。项目、生成记录与已保存的剧本持久保存。</p><p>无生产认证，演示空间不构成权限隔离；请勿输入敏感信息。</p></div></div>
    <div className="drama-layout">
      <section className="panel drama-projects" aria-labelledby="projects-heading"><div className="section-heading"><div><span className="eyebrow">YOUR STORIES</span><h2 id="projects-heading">短剧项目</h2></div><span className="count">{projects.length}</span></div><button type="button" className="primary drama-new" disabled={selection === 'new'} onClick={() => requestLeave(() => setSelection('new'))}>新建短剧项目 <span aria-hidden="true">＋</span></button>
        {loading && <p role="status" className="drama-help">正在读取项目列表…</p>}
        {error && <div className="error" role="alert">{error}<button type="button" onClick={() => {setLoading(true); setRevision(value => value + 1)}}>重新加载项目列表</button></div>}
        {!loading && !error && projects.length === 0 && <p className="drama-help">还没有项目。给故事起个名字，保存第一份简报。</p>}
        <div className="drama-project-list">{projects.map(project => <button type="button" key={project.id} aria-current={selection === project.id ? 'page' : undefined} onClick={() => {if (selection !== project.id) requestLeave(() => setSelection(project.id))}}><span className="drama-project-title">{project.title}</span><span className="drama-project-meta">版本 {project.version} · {project.latest_generation ? generationLabels[project.latest_generation.status] : '等待第一份剧本'}</span><time>{new Date(project.updated_at).toLocaleDateString('zh-CN')}</time></button>)}</div>
      </section>
      {selection === 'loading' ? <div className="panel loading-block">选择项目后开始创作</div> : <ProjectEditor key={`${space}:${selection}`} space={space} id={selection === 'new' ? null : selection} onProject={updateProject} onDirtyChange={onDirtyChange} requestLeave={requestLeave} />}
    </div>
  </div>
}

function ProjectEditor({space, id, onProject, onDirtyChange, requestLeave}: {
  space: string; id: string | null; onProject: (project: DramaProject) => void
  onDirtyChange: (dirty: boolean) => void; requestLeave: (action: () => void) => void
}) {
  const editor = useDramaProject(space, id, onProject)
  const [confirmGenerate, setConfirmGenerate] = useState(false)
  const active = generationActive(editor.project)
  const generation = editor.project?.latest_generation
  const validation = projectDraftError(editor.draft)
  useEffect(() => {onDirtyChange(editor.dirty); return () => onDirtyChange(false)}, [editor.dirty, onDirtyChange])
  useEffect(() => {
    if (!editor.dirty) return
    const warn = (event: BeforeUnloadEvent) => {event.preventDefault(); event.returnValue = ''}
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [editor.dirty])
  function generate() {
    if (editor.project?.script) setConfirmGenerate(true)
    else editor.submit('generate')
  }
  return <div className="drama-editor">
    {editor.loadError && <div className="error" role="alert">无法同步项目：{editor.loadError}<button type="button" onClick={() => editor.reload()}>重试读取项目</button></div>}
    {editor.loading && !editor.project && <div className="panel loading-block" role="status">正在读取项目详情…</div>}
    {(!id || editor.project) && <>
      <section className="panel drama-brief" aria-labelledby="brief-heading"><div className="section-heading"><div><span className="eyebrow">01 / THE STARTING POINT</span><h2 id="brief-heading">{id ? '项目简报' : '新建短剧项目'}</h2></div><span>{id ? `版本 ${editor.baseline?.version ?? '—'}` : '先保存，再生成'}</span></div>
        <form onSubmit={event => {event.preventDefault(); editor.submit(id ? 'brief' : 'create')}}>
          <fieldset disabled={editor.locked || editor.editingScript}><label htmlFor="project-title">项目名称</label><input id="project-title" value={editor.draft.title} onChange={event => editor.setDraft({...editor.draft, title: event.target.value})} placeholder="给这个故事起个名字" />
            <div className="drama-field-title"><label htmlFor="project-brief">创作简报</label><span>3–2000 字</span></div><textarea id="project-brief" value={editor.draft.brief} onChange={event => editor.setDraft({...editor.draft, brief: event.target.value})} placeholder="写下人物关系、冲突和期待的结局。简报会成为本次生成的依据。" /><div className="field-footer"><span>请勿输入敏感信息</span><span>{[...editor.draft.brief.trim()].length} / 2000</span></div>
          </fieldset>
          {validation && editor.briefDirty && <p className="drama-validation" role="status">{validation}</p>}
          <div className="drama-brief-actions"><span>{editor.briefDirty ? '有未保存的简报修改' : id ? '简报已保存' : '建立你的第一个故事起点'}</span><div>{id && editor.briefDirty && <button type="button" disabled={editor.locked || editor.editingScript} onClick={editor.cancelBrief}>取消简报修改</button>}<button className="primary" type="submit" disabled={editor.locked || editor.editingScript || Boolean(validation) || (Boolean(id) && !editor.briefDirty)}>{editor.busy === 'create' || editor.busy === 'brief' ? '正在保存…' : id ? '保存项目简报' : '创建项目'}</button></div></div>
        </form>
      </section>
      {editor.actionError && <div className="error drama-action-error" role="alert">{editor.actionError}{editor.uncertain && <button type="button" disabled={Boolean(editor.busy)} onClick={editor.retry}>{editor.busy ? '正在重试…' : '重试上次请求'}</button>}{editor.conflict && <div><p>本地草稿已保留。重新读取会载入服务端最新内容并放弃本地修改。</p><button type="button" onClick={() => requestLeave(() => editor.reload(true))}>重新读取最新项目</button></div>}</div>}
      {editor.notice && <p className="notice" role="status"><Icon name="check" />{editor.notice}</p>}
      {editor.busy && <p className="notice" role="status">{editor.busy === 'generate' ? '正在提交生成请求…' : '正在提交保存请求…'}请等待服务端确认。</p>}
      {id && editor.project && <>
        <section className="panel drama-generation" aria-labelledby="generation-heading"><div className="section-heading"><div><span className="eyebrow">02 / A FIRST DRAFT</span><h2 id="generation-heading">生成模拟剧本</h2></div><span>2 位角色 / 3 个场景</span></div>
          <p>根据已保存简报，由后端生成结构化剧本；成功后替换当前稿，失败会保留已有剧本。</p>
          {generation && <div className="drama-generation-state"><span className={`status ${generation.status}`}><span />{generationLabels[generation.status]}</span>{active && <div className="progress-block"><progress aria-label="剧本生成进度" value={generation.progress} max="100" /><span>{generation.progress}% · 后端处理中，自动更新</span></div>}{generation.error && <div className="error" role="alert">{generation.error}{generation.error_code && <code>（{generation.error_code}）</code>}<p>已有剧本不会被覆盖。调整并保存简报后，可以再次生成。</p></div>}</div>}
          {(editor.briefDirty || editor.editingScript) && <p className="drama-help">请先保存或取消当前编辑，再生成剧本。</p>}
          <button type="button" className="primary" disabled={editor.locked || editor.briefDirty || editor.editingScript || Boolean(editor.loadError)} onClick={generate}><Icon name="spark" />{active ? '剧本生成中…' : editor.busy === 'generate' ? '正在提交…' : editor.project.script ? '重新生成剧本' : '生成模拟剧本'}</button>
          <GenerationHistory space={space} projectId={id} revision={editor.historyRevision} />
        </section>
        {editor.project.script_brief && editor.project.script_brief !== editor.project.brief && <div className="editing-note drama-source-note"><strong>当前剧本来自之前的简报</strong><p>项目需求已更新，现有剧本仍保留原来的内容。重新生成成功后才会替换。</p><details><summary>查看剧本来源简报</summary><p>{editor.project.script_brief}</p></details></div>}
        {editor.script ? <DramaScriptEditor script={editor.script} editing={editor.editingScript} disabled={editor.locked || editor.briefDirty || Boolean(editor.loadError)} dirty={editor.scriptDirty} onChange={editor.setScript} onEdit={() => editor.setEditingScript(true)} onCancel={editor.cancelScript} onSave={() => editor.submit('script')} /> : <section className="panel drama-no-script"><Icon name="script" /><h2>故事在等一个开场</h2><p>保存项目简报后，生成第一份模拟剧本。你可以继续编辑场景与对白。</p></section>}
      </>}
    </>}
    {confirmGenerate && <ConfirmDialog title="替换当前剧本？" message="新的模拟剧本生成成功后，会替换当前已保存的剧本；本轮不保留可回滚的旧稿。生成失败时，当前稿保持不变。" confirmLabel="确认重新生成" onCancel={() => setConfirmGenerate(false)} onConfirm={() => {setConfirmGenerate(false); editor.submit('generate')}} />}
  </div>
}
