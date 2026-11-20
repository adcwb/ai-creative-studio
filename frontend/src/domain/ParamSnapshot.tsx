import {useI18n} from '../i18n'
import './snapshot.css'

export interface SnapshotJob {
  id: string
  asset_id: string
  kind: string
  shot_id?: string
  prompt: string
  prompt_source?: string
  director_context?: string
  visual_context?: string
  reference_asset_ids?: string[]
  first_frame_asset_id?: string
  last_frame_asset_id?: string
  motion_reference_asset_id?: string
  library_versions?: {asset_id: string; version_id: string}[]
}

// Every generated shot already carries its full parameter snapshot in the
// revision manifest. It was typed unknown[] and never shown, so a reader could
// not tell what a version was actually made from. Collapsed by default: it is
// evidence to consult, not prose to read past.
export function ParamSnapshot({jobs}: {jobs: SnapshotJob[]}) {
  const {t} = useI18n()
  if (!jobs.length) return null
  return <details className="param-snapshot">
    <summary>{t('生成参数快照')} · {t('{n} 个镜头', {n: jobs.length})}</summary>
    <ul>
      {jobs.map(job => <li key={job.id}>
        <div className="param-snapshot-head">
          <strong>{job.shot_id ?? job.asset_id}</strong>
          <span>{job.kind}</span>
          {job.prompt_source && <span>{job.prompt_source}</span>}
        </div>
        <p className="param-snapshot-prompt">{job.prompt}</p>
        {(job.director_context || job.visual_context) && <p className="param-snapshot-context">{[job.director_context, job.visual_context].filter(Boolean).join(' · ')}</p>}
        <dl>
          {job.reference_asset_ids?.length ? <div><dt>{t('参考图')}</dt><dd>{job.reference_asset_ids.length}</dd></div> : null}
          {job.first_frame_asset_id && <div><dt>{t('首帧')}</dt><dd><code>{job.first_frame_asset_id}</code></dd></div>}
          {job.last_frame_asset_id && <div><dt>{t('尾帧')}</dt><dd><code>{job.last_frame_asset_id}</code></dd></div>}
          {job.motion_reference_asset_id && <div><dt>{t('运动参考')}</dt><dd><code>{job.motion_reference_asset_id}</code></dd></div>}
          {job.library_versions?.length ? <div><dt>{t('固定库版本')}</dt><dd>{job.library_versions.map(v => v.version_id).join(', ')}</dd></div> : null}
        </dl>
      </li>)}
    </ul>
  </details>
}