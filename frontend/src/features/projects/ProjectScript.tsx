import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { translate } from '../../i18n'
import { localizedError } from '../../api'
import { DramaScriptEditor } from '../../components/DramaScriptEditor'
import { ProjectHistory } from '../../components/ProjectHistory'
import { Icon } from '../../components/Icon'
import { RuntimeModelPicker } from '../../components/RuntimeModelPicker'
import { NextStepDock } from '../../ui/NextStepDock'
import { StudioWorkbench } from '../../ui/StudioWorkbench'
import { StickyActionBar } from '../../ui/StickyActionBar'
import { StudioProgress } from '../../ui/StudioProgress'
import { ReadingOutline, type OutlineEntry } from '../../reading/ReadingOutline'
import { SceneNavigator } from '../../reading/SceneNavigator'
import { FindInPage } from '../../reading/FindInPage'
import { useReadingPosition } from '../../reading/useReadingPosition'
import type { ReadingCanvasHandle } from '../../reading/ReadingCanvas'
import { WritingContextPicker, type WritingReview } from './WritingContext'
import type { ProjectEditorState } from './useProjectEditor'

export function ProjectScript({state}: {state: ProjectEditorState}) {
  const {space, id, editor, effectiveCreative, scriptPreview, runtimeChoice, setRuntimeChoice, active, generation, toolsDirty, setToolsDirty} = state
  const [confirmation, setConfirmation] = useState<number | null>(null)
  const [context, setContext] = useState<WritingReview>({id: '', ready: true, changed: false})
  const scenes = useMemo(() => editor.script?.scenes ?? [], [editor.script])
  const {index: sceneIndex, publish} = useReadingPosition(scenes.length)
  const outline = useRef<ReadingCanvasHandle | null>(null)
  const [highlight, setHighlight] = useState(-1)
  const publishRef = useRef(publish)
  useEffect(() => {publishRef.current = publish}, [publish])

  const jump = useCallback((next: number) => {
    setHighlight(next)
    publish(next)
    outline.current?.scrollToIndex(next)
    document.querySelectorAll<HTMLElement>('.drama-scene')[next]?.scrollIntoView({behavior: 'smooth', block: 'start'})
  }, [publish])

  // Scrolling the prose drives the outline and the URL, so the reader always
  // knows which scene they are in without touching the outline.
  useEffect(() => {
    const nodes = [...document.querySelectorAll<HTMLElement>('.drama-scene')]
    if (!nodes.length) return
    const seen = new Map<number, number>()
    const observer = new IntersectionObserver(entries => {
      for (const entry of entries) seen.set(nodes.indexOf(entry.target as HTMLElement), entry.intersectionRatio)
      let best = -1, ratio = 0
      for (const [index, value] of seen) if (value > ratio) {ratio = value; best = index}
      if (best >= 0) publishRef.current(best)
    }, {threshold: [0, 0.25, 0.5, 0.75, 1]})
    for (const node of nodes) observer.observe(node)
    return () => observer.disconnect()
  }, [scenes.length, editor.editingScript])

  // A shared ?scene= link must land in the prose too, not only in the outline.
  useEffect(() => {
    if (sceneIndex <= 0) return
    const node = document.querySelectorAll<HTMLElement>('.drama-scene')[sceneIndex]
    node?.scrollIntoView({block: 'start'})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor.script?.scenes.length])

  const entries = useMemo<OutlineEntry[]>(() => scenes.map((scene, index) => ({
    key: scene.id ?? String(index),
    title: scene.heading,
    summary: scene.action.slice(0, 60),
    status: index === sceneIndex ? 'active' : 'idle',
  })), [scenes, sceneIndex])
  const texts = useMemo(() => scenes.map(scene => `${scene.heading} ${scene.action} ${scene.dialogue.map(line => `${line.character} ${line.text}`).join(' ')}`), [scenes])

  if (!id || !editor.project) return null
  const project = editor.project
  const blocked = editor.locked || Boolean(editor.loadError) || editor.dirty || editor.editingScript || editor.editingStoryboard || toolsDirty || !context.ready || !effectiveCreative?.ready
  const preview = {...scriptPreview, input: {...scriptPreview.input, ...(context.id ? {context_snapshot_id: context.id} : {})}}
  function generate() {
    if (blocked) return
    setConfirmation(null)
    editor.submit('generate', context.id)
  }
  const current = scenes[sceneIndex]
  const body = <>
    {project.structure_version !== 2 && <details className="panel editing-note">
      <summary>{translate('保留角色与场次关联')}</summary>
      <p>{translate('为旧项目启用稳定关联后，角色改名和场次重排会保留对应关系。原稿与旧成片仍保留；本操作不会生成内容或拆分剧集。')}</p>
      <button type="button" disabled={editor.locked || editor.dirty || Boolean(editor.loadError)} onClick={() => editor.submit('structure')}>{translate('启用稳定关联')}</button>
    </details>}
    <section className="panel drama-generation" aria-labelledby="generation-heading">
      <div className="section-heading"><h2 id="generation-heading">{translate(effectiveCreative?.mode === 'mock' ? '模拟剧本' : 'AI 剧本')}</h2><Link to={`/projects/${id}/brief`}>{translate('修改简报')}</Link></div>
      <details><summary>{translate('生成依据：已保存简报')}</summary><p>{project.brief}</p></details>
      <details open={!project.script}><summary>{translate('生成设置')}</summary><RuntimeModelPicker gatewayOnly space={space} capability="text" preview={preview} value={runtimeChoice} onChange={setRuntimeChoice} disabled={editor.locked} /></details>
      {project.series_episode_id && <WritingContextPicker episode={project.id} space={space} projectVersion={project.version} disabled={editor.locked || editor.dirty} onChange={setContext} />}
      <p className="drama-help">{translate('生成后直接保存为当前剧本，可继续编辑；旧稿可从版本历史恢复。')}</p>
      <button type="button" className={project.script ? '' : 'primary'} disabled={blocked} onClick={() => project.script ? setConfirmation(project.version) : generate()}>{active ? translate('正在生成剧本…') : project.script ? translate('重新生成') : translate(effectiveCreative?.mode === 'mock' ? '生成模拟剧本' : 'AI 生成剧本')}</button>
      {confirmation === project.version && <div className="editing-note" role="group" aria-label={translate('确认重新生成剧本')}>
        <p>{translate('将根据当前简报重新生成剧本，成功后替换当前内容，旧稿可从版本历史恢复。')}</p>
        {project.storyboard && <p>{translate('已有分镜会保留，生成新剧本后需要复核。')}</p>}
        <button type="button" onClick={() => setConfirmation(null)}>{translate('取消')}</button>
        <button type="button" className="primary" disabled={blocked} onClick={generate}>{translate('确认重新生成')}</button>
      </div>}
      {generation && <div className="drama-generation-state">
        {active && <StudioProgress progress={generation.progress} status={generation.status === 'failed' ? 'failed' : generation.status === 'succeeded' ? 'succeeded' : generation.status === 'running' ? 'running' : 'queued'} label={translate('剧本生成进度')} />}
        {generation.status === 'failed' && <div className="error" role="alert">
          <p>{translate('本次未能生成完整剧本，当前内容已保留。')}</p>
          <details><summary>{translate('查看详情')}</summary><p>{localizedError(generation.error, generation.error_code)}</p>{generation.error_code && <code>{generation.error_code}</code>}</details>
        </div>}
      </div>}
      {(editor.briefDirty || editor.editingScript || editor.editingStoryboard) && <p className="drama-help">{translate('请先保存或取消当前编辑，再生成剧本。')}</p>}
    </section>
    {project.script_brief && project.script_brief !== project.brief && <p className="editing-note">{translate('简报已更新，当前剧本仍保留原内容；需要按新简报创作时，可重新生成。')}</p>}
    {editor.script ? <DramaScriptEditor provider={project.script_provider} script={editor.script} editing={editor.editingScript} disabled={editor.locked || toolsDirty || editor.briefDirty || editor.editingStoryboard || Boolean(editor.loadError)} dirty={editor.scriptDirty} onChange={editor.setScript} onEdit={() => editor.setEditingScript(true)} onCancel={editor.cancelScript} onSave={() => editor.submit('script')} /> : <section className="panel drama-no-script"><Icon name="script" /><h2>{translate('故事在等一个开场')}</h2><button disabled={editor.locked || toolsDirty || Boolean(editor.loadError)} onClick={() => {editor.setScript({...(project.structure_version === 2 ? {structure_version: 2 as const} : {}), title: project.title, logline: project.brief.slice(0, 300), characters: [], scenes: [{...(project.structure_version === 2 ? {id: crypto.randomUUID()} : {}), heading: translate('新场次'), action: translate('场景动作'), dialogue: [], episode_number: 1}]}); editor.setEditingScript(true)}}>{translate('开始手写剧本')}</button></section>}
    <ProjectHistory project={project} space={space} disabled={editor.locked || editor.dirty || editor.editingScript} onRestore={() => editor.reload(true)} onDirtyChange={setToolsDirty} />
    {project.script && <NextStepDock><Link className="button-link primary" to={`/projects/${id}/storyboard`}>{translate('下一步：生成分镜')}</Link></NextStepDock>}
  </>
  if (!scenes.length) return body
  return <>
    <FindInPage texts={texts} label={translate('查找剧本正文')} onJump={jump} />
    <StudioWorkbench
      outlineTitle={translate('场次大纲')}
      inspectorTitle={translate('本场信息')}
      outline={<ReadingOutline entries={entries} activeIndex={sceneIndex} onSelect={jump} label={translate('场次大纲')} controlRef={outline} initialIndex={sceneIndex} />}
      inspector={<div className="scene-inspector">
        <h3>{translate('登场人物')}</h3>
        <ul className="scene-inspector-cast">{(editor.script?.characters ?? []).map(character => <li key={character.id ?? character.name}><span className="drama-cast-avatar" aria-hidden="true">{character.name.slice(0, 1)}</span><div><strong>{character.name}</strong><small>{character.description}</small></div></li>)}</ul>
        {current && <><h3>{translate('场景属性')}</h3><dl>
          <div><dt>{translate('所属集数')}</dt><dd>{current.episode_number ?? 1}</dd></div>
          <div><dt>{translate('对白条数')}</dt><dd>{current.dialogue.length}</dd></div>
          <div><dt>{translate('动作字数')}</dt><dd>{current.action.length}</dd></div>
        </dl></>}
      </div>}
      toolbar={<StickyActionBar label={translate('剧本导航')} status={<span>{highlight >= 0 ? translate('已定位到匹配场次') : translate('滚动或点击大纲定位场次')}</span>}>
        <SceneNavigator index={sceneIndex} total={scenes.length} onSelect={jump} unit={translate('场')} />
      </StickyActionBar>}
      measure
    >{body}</StudioWorkbench>
  </>
}