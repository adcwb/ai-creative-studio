import { cameraMovementLabels, shotSizeLabels, storyboardDraftError, type StoryboardShot } from '../drama'

export function StoryboardEditor({shots, editing, disabled, dirty, sourceVersion, stale, onChange, onEdit, onCancel, onSave}: {
  shots: StoryboardShot[]; editing: boolean; disabled: boolean; dirty: boolean; sourceVersion: number; stale: boolean
  onChange: (shots: StoryboardShot[]) => void; onEdit: () => void; onCancel: () => void; onSave: () => void
}) {
  const validation = editing ? storyboardDraftError(shots) : ''
  function change(index: number, patch: Partial<StoryboardShot>) {
    onChange(shots.map((shot, item) => item === index ? {...shot, ...patch} : shot))
  }
  return <section className="panel drama-storyboard" aria-labelledby="storyboard-heading">
    <div className="section-heading"><div><span className="eyebrow">THE SHOT PLAN</span><h2 id="storyboard-heading">当前分镜计划</h2></div><span>{editing ? '编辑中 · 尚未保存' : '已持久保存'}</span></div>
    <p className="drama-source">mock-storyboard · 程序生成的模拟分镜，非真实 AI 创作。来源：剧本版本 {sourceVersion}。</p>
    {stale && <div className="editing-note storyboard-stale" role="status"><strong>分镜已过期</strong><p>已保存剧本发生变化。下方场景编号仍引用来源剧本，可能与当前内容不同；编辑保存不会消除过期状态。重新生成成功后才会更新来源。</p></div>}
    {!editing && <button type="button" className="drama-edit-button" disabled={disabled} onClick={onEdit}>编辑分镜</button>}
    <fieldset disabled={disabled}>
      <div className="storyboard-shots">{shots.map((shot, index) => <article className="storyboard-shot" key={shot.id}>
        <div className="drama-scene-label">SHOT {String(index + 1).padStart(2, '0')} <span>第 {index + 1} 镜</span></div>
        {editing ? <>
          <div className="storyboard-fields">
            <div><label htmlFor={`shot-scene-${index}`}>第 {index + 1} 镜场景引用</label><select id={`shot-scene-${index}`} value={shot.scene_index} onChange={event => change(index, {scene_index: Number(event.target.value)})}>{[1, 2, 3].map(scene => <option key={scene} value={scene}>来源剧本第 {scene} 场</option>)}</select></div>
            <div><label htmlFor={`shot-size-${index}`}>第 {index + 1} 镜景别</label><select id={`shot-size-${index}`} value={shot.shot_size} onChange={event => change(index, {shot_size: event.target.value as StoryboardShot['shot_size']})}>{Object.entries(shotSizeLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></div>
            <div><label htmlFor={`shot-camera-${index}`}>第 {index + 1} 镜运镜</label><select id={`shot-camera-${index}`} value={shot.camera_movement} onChange={event => change(index, {camera_movement: event.target.value as StoryboardShot['camera_movement']})}>{Object.entries(cameraMovementLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></div>
            <div><label htmlFor={`shot-duration-${index}`}>第 {index + 1} 镜时长（秒）</label><input id={`shot-duration-${index}`} type="number" min="1" max="60" step="1" value={shot.duration_seconds || ''} onChange={event => change(index, {duration_seconds: Number(event.target.value)})} /></div>
          </div>
          <label htmlFor={`shot-description-${index}`}>第 {index + 1} 镜画面描述</label><textarea id={`shot-description-${index}`} value={shot.description} onChange={event => change(index, {description: event.target.value})} /><div className="field-footer"><span>1–1000 字</span><span>{[...shot.description.trim()].length} / 1000</span></div>
          <label htmlFor={`shot-dialogue-${index}`}>第 {index + 1} 镜对白</label><textarea id={`shot-dialogue-${index}`} value={shot.dialogue} onChange={event => change(index, {dialogue: event.target.value})} /><div className="field-footer"><span>可留空，最多 2000 字</span><span>{[...shot.dialogue.trim()].length} / 2000</span></div>
        </> : <>
          <p className="storyboard-shot-meta">来源剧本第 {shot.scene_index} 场 · {shotSizeLabels[shot.shot_size]} · {cameraMovementLabels[shot.camera_movement]} · {shot.duration_seconds} 秒</p>
          <h3>画面描述</h3><p className="drama-action">{shot.description}</p>
          <div className="drama-dialogue"><strong>对白</strong><p>{shot.dialogue || '无对白'}</p></div>
        </>}
      </article>)}</div>
    </fieldset>
    {editing && <div className="drama-script-actions">{validation && <p className="drama-validation" role="status">{validation}</p>}<p>编辑已有镜头并保存当前计划；场景编号引用生成时的剧本，不生成图片或视频。</p><div><button type="button" disabled={disabled} onClick={onCancel}>取消分镜编辑</button><button type="button" className="primary" disabled={disabled || !dirty || Boolean(validation)} onClick={onSave}>保存分镜</button></div></div>}
  </section>
}
