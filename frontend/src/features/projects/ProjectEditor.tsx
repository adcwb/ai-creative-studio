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

const steps = [['brief', '项目简报'], ['script', '剧本创作'], ['storyboard', '分镜计划'], ['avatar', '数字人口播'], ['media', '素材与成片'], ['publish', '社交发布中心']]

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
    <nav className="project-steps" aria-label={t('项目工作步骤')}>{steps.map(([key, label], index) => props.id ? <NavLink key={key} to={'/projects/' + encodeURIComponent(props.id!) + '/' + key}><span>{index + 1}</span>{t(label)}</NavLink> : <span className={key === 'brief' ? 'current-step' : 'disabled-step'} key={key} aria-current={key === 'brief' ? 'step' : undefined}><span>{index + 1}</span>{t(label)}</span>)}</nav>
    <div className="drama-editor"><ProjectFeedback state={state} />
      {(!props.id || editor.project) && <>
        {props.section === 'brief' && <ProjectBrief state={state} />}
        {props.id && editor.project && <>
          {props.section === 'script' && <ProjectScript state={state} />}
          {props.section === 'storyboard' && <ProjectStoryboard state={state} />}
          {props.section === 'media' && <ProjectMedia state={state} />}
          {props.section === 'avatar' && <DigitalHumanStudio key={editor.project.id} project={editor.project} space={props.space} disabled={editor.locked || editor.dirty || state.mediaDirty} onDirtyChange={state.setToolsDirty} />}
          {props.section === 'publish' && <ProjectPublish state={state} />}
        </>}
      </>}
    </div>
    <ProjectConfirmations state={state} />
  </div>
}
