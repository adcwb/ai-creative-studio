import { translate, useI18n } from '../i18n'
import { type DramaScript, scriptDraftError } from '../drama'

export function DramaScriptEditor({script, provider, editing, disabled, dirty, onChange, onEdit, onCancel, onSave}: {
  script: DramaScript; provider?: string; editing: boolean; disabled: boolean; dirty: boolean
  onChange: (script: DramaScript) => void; onEdit: () => void; onCancel: () => void; onSave: () => void
}) {
  useI18n()
  const validation = editing ? scriptDraftError(script) : ''
  function sceneChange(index: number, field: 'heading' | 'action', value: string) {
    onChange({...script, scenes: script.scenes.map((scene, item) => item === index ? {...scene, [field]: value} : scene)})
  }
  function dialogueChange(sceneIndex: number, lineIndex: number, value: string) {
    onChange({...script, scenes: script.scenes.map((scene, index) => index === sceneIndex ? {...scene, dialogue: scene.dialogue.map((line, item) => item === lineIndex ? {...line, text: value} : line)} : scene)})
  }
  return <section className="panel drama-script" aria-labelledby="script-heading">
    <div className="section-heading"><div><span className="eyebrow">THE CURRENT DRAFT</span><h2 id="script-heading">{translate("当前剧本")}</h2></div><span>{editing ? translate("编辑中 · 尚未保存") : translate("已持久保存")}</span></div>
    <p className="drama-source">{!provider || provider.startsWith('mock') ? translate("mock-script · 程序生成的模拟剧本，非真实 AI 创作。") : translate("{p0} · 模型生成来源，可能包含人工编辑；真实效果与费用未验证。", {p0: provider})}</p>
    {!editing && <button type="button" className="drama-edit-button" disabled={disabled} onClick={onEdit}>{translate("编辑剧本")}</button>}
    <fieldset disabled={disabled}>
      {editing ? <div className="drama-script-intro"><label htmlFor="script-title">{translate("剧本标题")}</label><input id="script-title" value={script.title} onChange={event => onChange({...script, title: event.target.value})} /><label htmlFor="script-logline">{translate("故事梗概")}</label><textarea id="script-logline" value={script.logline} onChange={event => onChange({...script, logline: event.target.value})} /></div> : <div className="drama-script-intro"><h3>{script.title}</h3><p>{script.logline}</p></div>}
      <div className="drama-characters"><h3>{translate("登场人物")}<small>{translate("本轮编辑器暂不修改角色与对白归属")}</small></h3><div>{script.characters.map((character, index) => <article key={index}><span className="drama-character-index">{String(index + 1).padStart(2, '0')}</span><div><h4>{character.name}</h4><p>{character.description}</p></div></article>)}</div></div>
      <div className="drama-scenes">{script.scenes.map((scene, index) => <article className="drama-scene" key={index}>
        <div className="drama-scene-label">SCENE {String(index + 1).padStart(2, '0')} <span>{translate("第 {p0} 场", {p0: index + 1})}</span></div>
        {editing ? <><label htmlFor={`scene-heading-${index}`}>{translate("第 {p0} 场标题", {p0: index + 1})}</label><input id={`scene-heading-${index}`} value={scene.heading} onChange={event => sceneChange(index, 'heading', event.target.value)} /><label htmlFor={`scene-action-${index}`}>{translate("第 {p0} 场动作", {p0: index + 1})}</label><textarea id={`scene-action-${index}`} value={scene.action} onChange={event => sceneChange(index, 'action', event.target.value)} /></> : <><h3>{scene.heading}</h3><p className="drama-action">{scene.action}</p></>}
        <div className="drama-dialogue">{scene.dialogue.map((line, lineIndex) => <div key={lineIndex}>{editing ? <><label htmlFor={`line-${index}-${lineIndex}`}>{translate("第 {p0} 场对白", {p0: index + 1})}{lineIndex + 1} · {line.character}</label><textarea id={`line-${index}-${lineIndex}`} value={line.text} onChange={event => dialogueChange(index, lineIndex, event.target.value)} /></> : <><strong>{line.character}</strong><p>{line.text}</p></>}</div>)}</div>
      </article>)}</div>
    </fieldset>
    {editing && <div className="drama-script-actions">{validation && <p className="drama-validation" role="status">{validation}</p>}<p>{translate("保存会更新当前剧本，旧稿可在版本历史中恢复。")}</p><div><button type="button" disabled={disabled} onClick={onCancel}>{translate("取消剧本编辑")}</button><button type="button" className="primary" disabled={disabled || !dirty || Boolean(validation)} onClick={onSave}>{translate("保存剧本")}</button></div></div>}
  </section>
}
