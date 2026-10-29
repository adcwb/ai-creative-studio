import { translate, useI18n } from '../i18n'
import { cameraMovementLabels, shotSizeLabels, storyboardDraftError, type StoryboardShot } from '../drama'

export function StoryboardEditor({shots, sceneCount = 0, provider, editing, disabled, dirty, sourceVersion, stale, onChange, onEdit, onCancel, onSave}: {
  shots: StoryboardShot[]; sceneCount?: number; provider?: string; editing: boolean; disabled: boolean; dirty: boolean; sourceVersion: number; stale: boolean
  onChange: (shots: StoryboardShot[]) => void; onEdit: () => void; onCancel: () => void; onSave: () => void
}) {
  useI18n()
  const validation = editing ? storyboardDraftError(shots) : ''
  function change(index: number, patch: Partial<StoryboardShot>) {
    onChange(shots.map((shot, item) => item === index ? {...shot, ...patch} : shot))
  }
  function move(index: number, direction: number) {
    const reordered = [...shots]; [reordered[index], reordered[index + direction]] = [reordered[index + direction], reordered[index]]; onChange(reordered)
  }
  return <section className="panel drama-storyboard" aria-labelledby="storyboard-heading">
    <div className="section-heading"><div><span className="eyebrow">THE SHOT PLAN</span><h2 id="storyboard-heading">{translate("当前分镜计划")}</h2></div><span>{editing ? translate("编辑中 · 尚未保存") : translate("已持久保存")}</span></div>
    <p className="drama-source">{provider === 'manual' ? translate('手工创作 · 已保存的当前版本。') : !provider || provider.startsWith('mock') ? translate("mock-storyboard · 程序生成的模拟分镜，非真实 AI 创作。") : translate("{p0} · 模型生成来源，可能包含人工编辑；真实效果与费用未验证。", {p0: provider})}{' '}{translate("来源：剧本版本 {p0}。", {p0: sourceVersion})}</p>
    {stale && <div className="editing-note storyboard-stale" role="status"><strong>{translate("分镜已过期")}</strong><p>{translate("已保存剧本发生变化。下方场景编号仍引用来源剧本，可能与当前内容不同；编辑保存不会消除过期状态。重新生成成功后才会更新来源。")}</p></div>}
    {!editing && <button type="button" className="drama-edit-button" disabled={disabled} onClick={onEdit}>{translate("编辑分镜")}</button>}
    <fieldset disabled={disabled}>
      <div className="storyboard-shots">{shots.map((shot, index) => <article className="storyboard-shot" key={shot.id}>
        <div className="drama-scene-label">SHOT {String(index + 1).padStart(2, '0')} <span>{translate("第 {p0} 镜", {p0: index + 1})}</span></div>
        {editing ? <>
          <div className="media-actions"><button type="button" disabled={disabled || index === 0} onClick={() => move(index, -1)}>{translate("上移第 {p0} 镜", {p0: index + 1})}</button><button type="button" disabled={disabled || index === shots.length - 1} onClick={() => move(index, 1)}>{translate("下移第 {p0} 镜", {p0: index + 1})}</button><button type="button" disabled={disabled || shots.length === 1} onClick={() => onChange(shots.filter((_, item) => item !== index))}>{translate("删除第 {p0} 镜", {p0: index + 1})}</button></div><div className="storyboard-fields">
            <div><label htmlFor={`shot-scene-${index}`}>{translate("第 {p0} 镜场景引用", {p0: index + 1})}</label><select id={`shot-scene-${index}`} value={shot.scene_index} onChange={event => change(index, {scene_index: Number(event.target.value)})}>{Array.from({length: sceneCount + 1}, (_, i) => i).map(scene => <option key={scene} value={scene}>{scene === 0 ? translate("独立镜头") : translate("来源剧本第 {p0} 场", {p0: scene})}</option>)}</select></div>
            <div><label htmlFor={`shot-size-${index}`}>{translate("第 {p0} 镜景别", {p0: index + 1})}</label><select id={`shot-size-${index}`} value={shot.shot_size} onChange={event => change(index, {shot_size: event.target.value as StoryboardShot['shot_size']})}>{Object.entries(shotSizeLabels).map(([value, label]) => <option key={value} value={value}>{translate(label)}</option>)}</select></div>
            <div><label htmlFor={`shot-camera-${index}`}>{translate("第 {p0} 镜运镜", {p0: index + 1})}</label><select id={`shot-camera-${index}`} value={shot.camera_movement} onChange={event => change(index, {camera_movement: event.target.value as StoryboardShot['camera_movement']})}>{Object.entries(cameraMovementLabels).map(([value, label]) => <option key={value} value={value}>{translate(label)}</option>)}</select></div>
            <div><label htmlFor={`shot-duration-${index}`}>{translate("第 {p0} 镜时长（秒）", {p0: index + 1})}</label><input id={`shot-duration-${index}`} type="number" min="1" max="60" step="1" value={shot.duration_seconds || ''} onChange={event => change(index, {duration_seconds: Number(event.target.value)})} /></div>
          </div>
          <details><summary>{translate("导演说明")}</summary>{(["purpose", "performance", "continuity"] as const).map(field => <label key={field}>{translate({purpose: "叙事目的", performance: "表演要求", continuity: "连续性要求"}[field])}<textarea maxLength={1000} value={shot[field] ?? ""} onChange={e => change(index, {[field]: e.target.value})} /></label>)}</details>
          <label htmlFor={`shot-description-${index}`}>{translate("第 {p0} 镜画面描述", {p0: index + 1})}</label><textarea id={`shot-description-${index}`} value={shot.description} onChange={event => change(index, {description: event.target.value})} /><div className="field-footer"><span>{translate("1–1000 字")}</span><span>{[...shot.description.trim()].length} / 1000</span></div>
          <label htmlFor={`shot-dialogue-${index}`}>{translate("第 {p0} 镜对白", {p0: index + 1})}</label><textarea id={`shot-dialogue-${index}`} value={shot.dialogue} onChange={event => change(index, {dialogue: event.target.value})} /><div className="field-footer"><span>{translate("可留空，最多 2000 字")}</span><span>{[...shot.dialogue.trim()].length} / 2000</span></div>
        </> : <>
          <p className="storyboard-shot-meta">{shot.scene_index === 0 ? translate("独立镜头") : translate("来源剧本第 {p0} 场", {p0: shot.scene_index})} · {translate(shotSizeLabels[shot.shot_size])} · {translate(cameraMovementLabels[shot.camera_movement])} · {shot.duration_seconds} {translate("秒")}</p>
          <h3>{translate("画面描述")}</h3><p className="drama-action">{shot.description}</p>
          <div className="drama-dialogue"><strong>{translate("对白")}</strong><p>{shot.dialogue || translate("无对白")}</p></div>
        </>}
      </article>)}</div>
    </fieldset>
    {editing && <div className="drama-script-actions">{validation && <p className="drama-validation" role="status">{validation}</p>}<p>{translate("支持1–120镜。独立镜头无需剧本；场景引用对应来源剧本。")}</p><div><button type="button" disabled={disabled || shots.length >= 120} onClick={() => onChange([...shots, {id: crypto.randomUUID(), scene_index: 0, description: translate("新镜头画面"), shot_size: 'medium', camera_movement: 'static', duration_seconds: 3, dialogue: ''}])}>{translate("添加镜头")}</button><button type="button" disabled={disabled} onClick={onCancel}>{translate("取消分镜编辑")}</button><button type="button" className="primary" disabled={disabled || !dirty || Boolean(validation)} onClick={onSave}>{translate("保存分镜")}</button></div></div>}
  </section>
}
