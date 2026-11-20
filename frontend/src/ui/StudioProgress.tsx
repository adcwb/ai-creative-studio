import {useI18n} from '../i18n'
import './atoms.css'

// The backend writes progress as 0, 35 or 100: they are stage markers, not a
// percentage of model work. Rendering "35%" claimed a precision the API does not
// have. This shows the three real stages on the brand's rail instead.
const stages = ['已接单', '生成中', '已完成'] as const

export function StudioProgress({progress, status, label}: {progress: number; status: 'queued' | 'running' | 'succeeded' | 'failed'; label: string}) {
  const {t} = useI18n()
  const reached = progress >= 100 ? 2 : progress >= 35 ? 1 : 0
  const failed = status === 'failed'
  return <div className="studio-progress" role="status" aria-label={label} data-failed={failed || undefined}>
    <ol>
      {stages.map((stage, index) => <li key={stage} data-state={failed && index === reached ? 'failed' : index < reached ? 'done' : index === reached ? 'active' : 'todo'}>
        <span className="studio-progress-rail" aria-hidden="true" />
        <span>{t(stage)}</span>
      </li>)}
    </ol>
    <p>{failed ? t('本次未返回结果，内容已保留。') : reached === 2 ? t('已完成。') : t('阶段进度，非模型生成百分比。')}</p>
  </div>
}