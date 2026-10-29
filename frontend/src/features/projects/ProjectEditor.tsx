import { Link, NavLink } from 'react-router-dom'
import { useEffect, useRef } from 'react'
import { useI18n } from '../../i18n'
import { useProjectEditor } from './useProjectEditor'
import { ProjectBrief } from './ProjectBrief'
import { ProjectFeedback } from './ProjectFeedback'
import { ProjectScript } from './ProjectScript'
import { ProjectStoryboard } from './ProjectStoryboard'
import { ProjectMedia } from './ProjectMedia'
import { DigitalHumanStudio } from '../../components/DigitalHumanStudio'
import { ProjectPublish } from './ProjectPublish'
import { ProjectConfirmations } from './ProjectConfirmations'
import { ProjectReferences } from '../library/ProjectReferences'
import { ProductionBoard } from './ProductionBoard'

const steps = [['brief', '项目总览'], ['script', '故事与剧本'], ['references', '角色与美术'], ['storyboard', '分镜与预演'], ['production', '镜头制作'], ['media', '剪辑与声音'], ['review', '审片与交付']]

export function ProjectEditor(props: {
  space: string; id: string | null; section: string; onDirtyChange: (dirty: boolean) => void; requestLeave: (action: () => void) => void
}) {
  const {t} = useI18n()
  const {section, onDirtyChange} = props
  const state = useProjectEditor(props)
  const {editor} = state
  const previousSection = useRef(section)
  useEffect(() => {
    if (previousSection.current !== section) {
      if (!editor.uncertain && !editor.busy) {
        editor.cancelBrief(); editor.cancelScript(); editor.cancelStoryboard()
      } else onDirtyChange(true)
    }
    previousSection.current = section
  }, [section, onDirtyChange, editor])
  return <div className="project-page">
    <Link className="project-back" to="/projects">{t('返回项目列表')}</Link>
    <header className="page-heading"><div><h1>{props.id ? editor.project?.title ?? t('正在读取项目详情…') : t('新建短剧项目')}</h1><p>{t('简报、剧本、分镜、剪辑与发布，在同一个项目中衔接。')}</p></div>{editor.project && <span className="project-version">{t('版本')} {editor.project.version}</span>}</header>
    <nav className="project-steps" aria-label={t('项目工作区')}>{steps.map(([key, label]) => props.id ? <NavLink key={key} to={'/projects/' + encodeURIComponent(props.id!) + '/' + key}>{t(label)}</NavLink> : <span className={key === 'brief' ? 'current-step' : 'disabled-step'} key={key} aria-current={key === 'brief' ? 'step' : undefined}>{t(label)}</span>)}</nav>
    <div className="drama-editor"><ProjectFeedback state={state} />
      {(!props.id || editor.project) && <>
        {props.section === 'brief' && <ProjectBrief state={state} />}
        {props.id && editor.project && <>
          {props.section === 'script' && <ProjectScript state={state} />}
          {props.section === 'storyboard' && <ProjectStoryboard state={state} />}
          {props.section === 'references' && <ProjectReferences space={props.space} projectId={editor.project.id} shots={editor.project.storyboard?.shots ?? []} disabled={editor.locked || editor.dirty} />}
          {(props.section === 'production' || props.section === 'review') && <><ProductionBoard key={editor.project.id} project={editor.project} space={props.space} disabled={editor.locked || editor.dirty} onDirtyChange={state.setToolsDirty} />{props.section === 'review' && <Link className="button-link" to={`/publish/${editor.project.id}`}>{t('准备发布')}</Link>}</>}
          {props.section === 'media' && <ProjectMedia state={state} />}
          {props.section === 'avatar' && <DigitalHumanStudio key={editor.project.id} project={editor.project} space={props.space} disabled={editor.locked || editor.dirty || state.mediaDirty} onDirtyChange={state.setToolsDirty} />}
          {props.section === 'publish' && <ProjectPublish state={state} />}
        </>}
      </>}
    </div>
    <ProjectConfirmations state={state} />
  </div>
}
