import {memo} from 'react'
import {Link} from 'react-router-dom'
import {useI18n} from '../i18n'
import {generationLabels, type DramaProject} from '../drama'
import {Icon} from '../components/Icon'

// Memoised so typing in the filter bar or a background refresh does not re-render
// every card. The i18n helpers are read inside the component rather than passed
// in, because the context returns fresh function identities on every render and
// would defeat the comparison.
export const ProjectCard = memo(function ProjectCard({project}: {project: DramaProject}) {
  const {t, formatDate} = useI18n()
  return <Link className="project-card" to={`/projects/${encodeURIComponent(project.id)}/brief`}>
    <div className="project-card-icon"><Icon name="script" /><span>{t('版本')}{project.version}</span></div>
    <h2>{project.title}</h2>
    <p>{project.brief}</p>
    <div className="project-card-footer">
      <span>{project.latest_generation ? t(generationLabels[project.latest_generation.status]) : t('等待第一份剧本')}</span>
      <time>{formatDate(project.updated_at, {dateStyle: 'medium'})}</time>
    </div>
  </Link>
})